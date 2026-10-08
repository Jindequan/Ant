package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ant/server/internal/api"
	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/ledger/commands"
	"ant/server/internal/providers/chatcompletions"
	"ant/server/internal/runtime"
	"ant/server/internal/security"
)

type operationExecutorFunc func(context.Context, domain.AuthContext, domain.OwnerToken, domain.ExecuteInput)

func (f operationExecutorFunc) Execute(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, in domain.ExecuteInput) {
	f(ctx, auth, owner, in)
}

func TestRouterClaimsThenStartsHostAndResolvesDuplicatesBeforeCapacity(t *testing.T) {
	f := preparationFixture(t, 2)
	ctx := context.Background()
	started := make(chan struct{})
	finished := make(chan struct{})
	host, err := runtime.NewExecutionHost(context.Background(), runtime.HostConfig{MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	claim := func(ctx context.Context, auth domain.AuthContext, input domain.ExecuteInput, fingerprint string, policy ledger.ClaimPolicy) (ledger.ClaimReply, error) {
		return commands.Claim(ctx, f.st, auth, input, fingerprint, policy)
	}
	router, err := application.NewRouter(f.st, claim, host, f.clock, operationExecutorFunc(func(ctx context.Context, _ domain.AuthContext, _ domain.OwnerToken, _ domain.ExecuteInput) {
		close(started)
		<-ctx.Done()
		close(finished)
	}), application.OperationRouterConfig{RuntimeID: "rt1", LeaseMillis: 5000, MaxRuntimeAttempts: 3, ValidateRequest: func(domain.DomainRequest) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	input := claimInput(`{"prompt":"router host test"}`)
	input.OperationID = "router-op"
	input.DeadlineAt = domain.Millis(time.Now().Add(time.Minute).UnixMilli())
	reply, err := router.Submit(ctx, claimAuth(), input)
	if err != nil || reply.Kind != "accepted" || reply.State != domain.Running {
		t.Fatalf("Submit() = %+v, %v", reply, err)
	}
	<-started

	// The host is full, but this existing id still resolves without needing a
	// new capacity reservation or a second claim.
	again, err := router.Submit(ctx, claimAuth(), input)
	if err != nil || again.Kind != "inProgress" || again.Operation == nil {
		t.Fatalf("duplicate Submit() = %+v, %v", again, err)
	}

	conflict := input
	conflict.Request.Payload = []byte(`{"prompt":"different semantics"}`)
	conflicted, err := router.Submit(ctx, claimAuth(), conflict)
	if err != nil || conflicted.Kind != "rejected" || conflicted.Error == nil || conflicted.Error.Code != "OperationIdConflict" {
		t.Fatalf("conflicting Submit() = %+v, %v", conflicted, err)
	}

	host.Cancel(domain.OperationKey{Namespace: claimAuth().Namespace, OperationID: input.OperationID})
	shutdown, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := host.Close(shutdown); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("host task did not stop")
	}
}

func TestOperationSubmitHTTPToSQLiteAndHost(t *testing.T) {
	f := preparationFixture(t, 2)
	ctx := context.Background()
	started := make(chan struct{})
	finished := make(chan struct{})
	host, err := runtime.NewExecutionHost(context.Background(), runtime.HostConfig{MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	claim := func(ctx context.Context, auth domain.AuthContext, input domain.ExecuteInput, fingerprint string, policy ledger.ClaimPolicy) (ledger.ClaimReply, error) {
		return commands.Claim(ctx, f.st, auth, input, fingerprint, policy)
	}
	router, err := application.NewRouter(f.st, claim, host, f.clock, operationExecutorFunc(func(ctx context.Context, _ domain.AuthContext, _ domain.OwnerToken, _ domain.ExecuteInput) {
		close(started)
		<-ctx.Done()
		close(finished)
	}), application.OperationRouterConfig{RuntimeID: "rt1", LeaseMillis: 5000, MaxRuntimeAttempts: 3, ValidateRequest: func(domain.DomainRequest) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	tokens := security.NewMemoryTokenStore()
	token := "integration-operation-submit-token"
	if err := tokens.Put(security.HashToken(token), security.TokenRecord{
		PrincipalID: "principal-1", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermExecute}, Kind: security.TokenKindAPIToken,
	}); err != nil {
		t.Fatal(err)
	}
	handler := api.AuthMiddleware(security.NewAuthProvider(tokens), api.OperationSubmitHandler(router))
	input := claimInput(`{"prompt":"http integration"}`)
	input.OperationID = "http-router-op"
	input.DeadlineAt = domain.Millis(time.Now().Add(time.Minute).UnixMilli())
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/pools/pool-1/operations", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	first := request()
	if first.Code != http.StatusAccepted {
		t.Fatalf("submit status=%d body=%s", first.Code, first.Body.String())
	}
	<-started
	second := request()
	if second.Code != http.StatusOK {
		t.Fatalf("duplicate status=%d body=%s", second.Code, second.Body.String())
	}
	var duplicate domain.SubmitReply
	if err := json.Unmarshal(second.Body.Bytes(), &duplicate); err != nil || duplicate.Kind != "inProgress" {
		t.Fatalf("duplicate reply=%+v err=%v", duplicate, err)
	}
	if got := queryInt(t, openRawDB(t, f.path), `SELECT COUNT(*) FROM operations WHERE namespace='ns-1' AND operation_id='http-router-op'`); got != 1 {
		t.Fatalf("operation rows=%d, want exactly one", got)
	}
	if host.Active() != 1 {
		t.Fatalf("active host tasks=%d, want 1", host.Active())
	}
	host.Cancel(domain.OperationKey{Namespace: "ns-1", OperationID: input.OperationID})
	shutdown, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := host.Close(shutdown); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("HTTP submitted task did not stop")
	}
}

func TestRouterValidatesDomainRequestBeforeClaim(t *testing.T) {
	f := preparationFixture(t, 2)
	db := openRawDB(t, f.path)
	defer db.Close()
	host, err := runtime.NewExecutionHost(context.Background(), runtime.HostConfig{MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	var claims int
	claim := func(context.Context, domain.AuthContext, domain.ExecuteInput, string, ledger.ClaimPolicy) (ledger.ClaimReply, error) {
		claims++
		return ledger.ClaimReply{}, nil
	}
	router, err := application.NewRouter(f.st, claim, host, f.clock, operationExecutorFunc(func(context.Context, domain.AuthContext, domain.OwnerToken, domain.ExecuteInput) {}), application.OperationRouterConfig{
		RuntimeID: "rt1", LeaseMillis: 5000, MaxRuntimeAttempts: 3, ValidateRequest: func(request domain.DomainRequest) error {
			return chatcompletions.ValidateRequest(request)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := claimInput(`{"model":"fixture","messages":[{"role":"user","content":"hello"}],"max_tokens":1,"stream":true}`)
	input.OperationID = "invalid-before-claim"
	input.DeadlineAt = 10000
	if _, err := router.Submit(context.Background(), claimAuth(), input); !errors.Is(err, application.ErrSubmitInvalid) {
		t.Fatalf("invalid request error=%v, want ErrSubmitInvalid", err)
	}
	if claims != 0 || queryInt(t, db, `SELECT COUNT(*) FROM operations WHERE operation_id='invalid-before-claim'`) != 0 {
		t.Fatalf("invalid request reached claim: calls=%d", claims)
	}
}
