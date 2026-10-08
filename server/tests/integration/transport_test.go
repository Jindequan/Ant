package integration

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/ledger/commands"
	"ant/server/internal/store"
	"ant/server/internal/transport"
)

type integrationCredentialResolver struct{ values map[string]string }

// replyLossStore commits the consume command and then drops its reply, modeling
// a connection failure after SQLite commit but before the caller receives ACK.
type replyLossStore struct {
	store.Store
	dropConsumeReply atomic.Bool
}

func (s *replyLossStore) SubmitWrite(ctx context.Context, command store.WriteCommand) error {
	err := s.Store.SubmitWrite(ctx, command)
	if err == nil && s.dropConsumeReply.Load() && strings.HasPrefix(command.ID, "consume/") {
		return errors.New("injected lost consume reply")
	}
	return err
}

func (r integrationCredentialResolver) Resolve(context.Context, domain.ID, domain.ID) (map[string]string, error) {
	copyValues := make(map[string]string, len(r.values))
	for key, value := range r.values {
		copyValues[key] = value
	}
	return copyValues, nil
}

func TestControlledTransportConsumesDurablePermitBeforeWireRequest(t *testing.T) {
	f := preparationFixture(t, 2)
	ctx := context.Background()
	db := openRawDB(t, f.path)
	defer db.Close()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer fixture-token" {
			t.Errorf("authorization = %q", got)
		}
		if body, err := io.ReadAll(r.Body); err != nil || string(body) != `{"prompt":"fixture"}` {
			t.Errorf("request body=%q err=%v", body, err)
		}
		var status string
		if err := db.QueryRow(`SELECT status FROM dispatch_permits WHERE attempt_id='attempt-wire'`).Scan(&status); err != nil {
			t.Errorf("read persisted permit at wire arrival: %v", err)
		} else if status != "consumed" {
			t.Errorf("wire arrived before durable permit consume: status=%q", status)
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	encoded := adapters.EncodedRequest{
		Endpoint: server.URL, Method: http.MethodPost,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"prompt":"fixture"}`), CredentialRef: "secret-ref",
		CredentialHeader: "Authorization", CredentialField: "value", CredentialPrefix: "Bearer ", MaxResponseBytes: 1024,
	}
	if _, err := db.ExecContext(ctx, `UPDATE bindings SET endpoint=? WHERE binding_id='bind-1'`, server.URL); err != nil {
		t.Fatal(err)
	}
	f.plan.Endpoint = server.URL
	digest := transport.DigestRequest(encoded)
	if _, err := f.TryPrepare(ctx, claimAuth(), f.owner, "attempt-wire", f.plan, nil, digest, 20); err != nil {
		t.Fatal(err)
	}
	permit, err := f.AuthorizeDispatch(ctx, claimAuth(), f.owner, "attempt-wire", 30)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client, err := transport.NewHTTPClientWithRootCAs(roots)
	if err != nil {
		t.Fatal(err)
	}
	consumer := func(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, permit ledger.DispatchPermit, digest string) error {
		f.clock.SetNow(40)
		return f.s.ConsumeDispatchPermit(ctx, auth, owner, permit, digest, 40)
	}
	controlled, err := transport.NewControlledHTTPTransportWithClient("rt1", integrationCredentialResolver{values: map[string]string{"value": "fixture-token"}}, consumer, client)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := controlled.Prepare(ctx, claimAuth(), f.owner, "attempt-wire", encoded, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	if handle.RequestDigest() != digest || strings.Contains(handle.RequestDigest(), "fixture-token") {
		t.Fatalf("request digest did not preserve the prepared identity: %q", handle.RequestDigest())
	}
	response, err := controlled.Dispatch(ctx, permit, handle)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil || response.Status != http.StatusOK || string(body) != `{"ok":true}` || requests.Load() != 1 {
		t.Fatalf("status=%d body=%s requests=%d readErr=%v closeErr=%v", response.Status, body, requests.Load(), err, closeErr)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM dispatch_permits WHERE attempt_id='attempt-wire'`).Scan(&status); err != nil || status != "consumed" {
		t.Fatalf("durable permit status=%q err=%v", status, err)
	}
}

func TestControlledTransportDoesNotSendWhenDurablePermitBecomesStale(t *testing.T) {
	f := preparationFixture(t, 2)
	ctx := context.Background()
	db := openRawDB(t, f.path)
	defer db.Close()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if _, err := db.ExecContext(ctx, `UPDATE bindings SET endpoint=? WHERE binding_id='bind-1'`, server.URL); err != nil {
		t.Fatal(err)
	}
	encoded := adapters.EncodedRequest{
		Endpoint: server.URL, Method: http.MethodPost,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"prompt":"fixture"}`), CredentialRef: "secret-ref",
		CredentialHeader: "Authorization", CredentialField: "value", CredentialPrefix: "Bearer ", MaxResponseBytes: 1024,
	}
	f.plan.Endpoint = server.URL
	digest := transport.DigestRequest(encoded)
	if _, err := f.TryPrepare(ctx, claimAuth(), f.owner, "attempt-stale-wire", f.plan, nil, digest, 20); err != nil {
		t.Fatal(err)
	}
	permit, err := f.AuthorizeDispatch(ctx, claimAuth(), f.owner, "attempt-stale-wire", 30)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client, err := transport.NewHTTPClientWithRootCAs(roots)
	if err != nil {
		t.Fatal(err)
	}
	consumer := func(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, permit ledger.DispatchPermit, digest string) error {
		f.clock.SetNow(40)
		return f.s.ConsumeDispatchPermit(ctx, auth, owner, permit, digest, 40)
	}
	controlled, err := transport.NewControlledHTTPTransportWithClient("rt1", integrationCredentialResolver{values: map[string]string{"value": "fixture-token"}}, consumer, client)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := controlled.Prepare(ctx, claimAuth(), f.owner, "attempt-stale-wire", encoded, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	var reordered reorderResult
	if err := f.st.SubmitWrite(ctx, reorderCommand("pool-1", 1, "rev-2", []store.PoolMemberRecord{member("pool-1", "of-1", 11)}, &reordered)); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if _, err := controlled.Dispatch(ctx, permit, handle); err == nil {
		t.Fatal("Dispatch succeeded after the pool order changed")
	}
	if requests.Load() != 0 {
		t.Fatalf("stale permit sent %d wire requests", requests.Load())
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM dispatch_permits WHERE attempt_id='attempt-stale-wire'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "granted" {
		t.Fatalf("stale permit status = %q, want granted and unused", status)
	}
}

func TestControlledTransportDoesNotSendWhenConsumeCommitReplyIsLost(t *testing.T) {
	f := preparationFixture(t, 2)
	ctx := context.Background()
	db := openRawDB(t, f.path)
	defer db.Close()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if _, err := db.ExecContext(ctx, `UPDATE bindings SET endpoint=? WHERE binding_id='bind-1'`, server.URL); err != nil {
		t.Fatal(err)
	}
	f.plan.Endpoint = server.URL
	storeWithReplyLoss := &replyLossStore{Store: f.st}
	f.s = commands.NewPreparationService(storeWithReplyLoss, "rt1", f.clock)
	encoded := adapters.EncodedRequest{
		Endpoint: server.URL, Method: http.MethodPost,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"prompt":"fixture"}`), CredentialRef: "secret-ref",
		CredentialHeader: "Authorization", CredentialField: "value", CredentialPrefix: "Bearer ", MaxResponseBytes: 1024,
	}
	digest := transport.DigestRequest(encoded)
	if _, err := f.TryPrepare(ctx, claimAuth(), f.owner, "attempt-lost-consume-ack", f.plan, nil, digest, 20); err != nil {
		t.Fatal(err)
	}
	permit, err := f.AuthorizeDispatch(ctx, claimAuth(), f.owner, "attempt-lost-consume-ack", 30)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client, err := transport.NewHTTPClientWithRootCAs(roots)
	if err != nil {
		t.Fatal(err)
	}
	controlled, err := transport.NewControlledHTTPTransportWithClient("rt1", integrationCredentialResolver{values: map[string]string{"value": "fixture-token"}}, func(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, permit ledger.DispatchPermit, digest string) error {
		f.clock.SetNow(40)
		return f.s.ConsumeDispatchPermit(ctx, auth, owner, permit, digest, 40)
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := controlled.Prepare(ctx, claimAuth(), f.owner, "attempt-lost-consume-ack", encoded, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	storeWithReplyLoss.dropConsumeReply.Store(true)
	if _, err := controlled.Dispatch(ctx, permit, handle); err == nil {
		t.Fatal("Dispatch succeeded without receiving the durable consume acknowledgement")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("lost consume acknowledgement sent %d requests, want 0", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM dispatch_permits WHERE attempt_id='attempt-lost-consume-ack' AND status='consumed'`); got != 1 {
		t.Fatalf("committed consumed permit count=%d, want 1", got)
	}
	// Simulate a process restart: the new runtime has a fresh permit registry
	// and resolves the committed executing attempt through durable recovery.
	f.clock.SetNow(9011) // the original operation lease expires at 9010.
	restarted := commands.NewPreparationService(f.st, "rt2", f.clock)
	recoveryAuth := domain.AuthContext{PrincipalID: "runtime-rt2", Permissions: []domain.Permission{domain.PermManage}}
	if err := restarted.Sweep(ctx, recoveryAuth, f.clock.Now(), 10); err != nil {
		t.Fatalf("sweep after restart: %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("restart recovery sent %d requests, want 0", got)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM operations WHERE operation_id='op-1'`).Scan(&state); err != nil || state != string(domain.Unknown) {
		t.Fatalf("operation after consumed permit restart=%q err=%v, want unknown", state, err)
	}
	if got := queryInt(t, db, `SELECT uncertain_units FROM resource_states WHERE resource_id='r2'`); got != 1 {
		t.Fatalf("uncertain occupancy=%d, want 1 conservatively retained", got)
	}
	if got := queryInt(t, db, `SELECT committed_units FROM resource_states WHERE resource_id='r1'`); got != 1 {
		t.Fatalf("conservative counter charge=%d, want 1", got)
	}
}

func TestPreparedCancellationPreventsAuthorizationAndWireSend(t *testing.T) {
	f := preparationFixture(t, 2)
	ctx := context.Background()
	db := openRawDB(t, f.path)
	defer db.Close()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if _, err := db.ExecContext(ctx, `UPDATE bindings SET endpoint=? WHERE binding_id='bind-1'`, server.URL); err != nil {
		t.Fatal(err)
	}
	f.plan.Endpoint = server.URL
	encoded := adapters.EncodedRequest{
		Endpoint: server.URL, Method: http.MethodPost,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"prompt":"fixture"}`), CredentialRef: "secret-ref",
		CredentialHeader: "Authorization", CredentialField: "value", CredentialPrefix: "Bearer ", MaxResponseBytes: 1024,
	}
	digest := transport.DigestRequest(encoded)
	if _, err := f.TryPrepare(ctx, claimAuth(), f.owner, "attempt-cancel-prepared", f.plan, nil, digest, 20); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client, err := transport.NewHTTPClientWithRootCAs(roots)
	if err != nil {
		t.Fatal(err)
	}
	controlled, err := transport.NewControlledHTTPTransportWithClient("rt1", integrationCredentialResolver{values: map[string]string{"value": "fixture-token"}}, func(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, permit ledger.DispatchPermit, digest string) error {
		f.clock.SetNow(40)
		return f.s.ConsumeDispatchPermit(ctx, auth, owner, permit, digest, 40)
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := controlled.Prepare(ctx, claimAuth(), f.owner, "attempt-cancel-prepared", encoded, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := f.s.RequestCancel(ctx, domain.AuthContext{PrincipalID: "principal-1", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermCancel}}, f.owner.Key, "cancel-prepared")
	if err != nil || ack.Status != "recorded" {
		t.Fatalf("cancel prepared attempt: %+v err=%v", ack, err)
	}
	if _, err := f.AuthorizeDispatch(ctx, claimAuth(), f.owner, "attempt-cancel-prepared", 50); err == nil {
		t.Fatal("cancelled prepared attempt received a dispatch permit")
	}
	controlled.Discard(handle)
	if got := requests.Load(); got != 0 {
		t.Fatalf("cancelled prepared attempt sent %d requests, want 0", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM dispatch_permits WHERE attempt_id='attempt-cancel-prepared'`); got != 0 {
		t.Fatalf("cancelled prepared attempt permits=%d, want 0", got)
	}
	if got := queryInt(t, db, `SELECT reserved_units+active_units+uncertain_units FROM resource_states WHERE resource_id='r2'`); got != 0 {
		t.Fatalf("occupancy after prepared cancellation=%d, want 0", got)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM operations WHERE operation_id='op-1'`).Scan(&state); err != nil || state != string(domain.Cancelled) {
		t.Fatalf("operation state=%q err=%v, want cancelled", state, err)
	}
}
