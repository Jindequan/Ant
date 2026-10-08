package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/ledger/commands"
	"ant/server/internal/runtime"
	"ant/server/internal/store/sqlite"
)

type operationQueriesStub struct{ missing bool }

func (s operationQueriesStub) Get(_ context.Context, _ domain.AuthContext, id domain.ID) (domain.OperationView, error) {
	if s.missing {
		return domain.OperationView{}, application.ErrOperationQueryNotFound
	}
	return domain.OperationView{OperationID: id, PoolID: "pool", State: domain.Running, Attempts: []domain.AttemptView{}, Resources: []domain.ResourceOutcome{}, Reconciliations: []domain.ReconciliationView{}}, nil
}

type operationCancelStub struct {
	gotAuth    domain.AuthContext
	gotKey     domain.OperationKey
	gotCommand domain.ID
	ack        domain.CancellationAck
	err        error
}

type operationReconcileStub struct {
	called bool
	auth   domain.AuthContext
	input  domain.ReconciliationInput
	view   domain.ReconciliationView
	err    error
}

func (s *operationReconcileStub) Reconcile(_ context.Context, auth domain.AuthContext, input domain.ReconciliationInput) (domain.ReconciliationView, error) {
	s.called, s.auth, s.input = true, auth, input
	return s.view, s.err
}

type operationSubmitStub struct {
	called bool
	auth   domain.AuthContext
	input  domain.ExecuteInput
	reply  domain.SubmitReply
	err    error
}

func (s *operationSubmitStub) Submit(_ context.Context, auth domain.AuthContext, input domain.ExecuteInput) (domain.SubmitReply, error) {
	s.called, s.auth, s.input = true, auth, input
	return s.reply, s.err
}

func (s *operationCancelStub) RequestCancel(_ context.Context, auth domain.AuthContext, key domain.OperationKey, command domain.ID) (domain.CancellationAck, error) {
	s.gotAuth, s.gotKey, s.gotCommand = auth, key, command
	return s.ack, s.err
}
func (operationQueriesStub) List(_ context.Context, _ domain.AuthContext, cursor string, limit int, state string) (application.OperationPage, error) {
	return application.OperationPage{Operations: []domain.OperationView{}, NextCursor: state + cursor}, nil
}
func (operationQueriesStub) Output(_ context.Context, _ domain.AuthContext, _ domain.ID, _ int64, _ int) (domain.OutputPage, error) {
	return domain.OutputPage{Chunks: []domain.OutputChunk{}, Complete: true, Availability: "none"}, nil
}
func (s operationQueriesStub) Decisions(_ context.Context, auth domain.AuthContext, id domain.ID) ([]domain.DecisionView, error) {
	if s.missing {
		return nil, application.ErrOperationQueryNotFound
	}
	return []domain.DecisionView{{DecisionID: "decision-1", AttemptID: "attempt-1", PoolID: "pool-1", PoolVersion: 3, OfferID: "offer-1", MemberPriority: 4, ConfigRevision: "config-1", Stage: "prepare", Candidates: []domain.CandidateDecision{{OfferID: "offer-1", Priority: 4, Reasons: []domain.Reason{}}}, HookRegistryRevision: "registry-rev", PlannerCandidateOrder: []domain.ID{"offer-1"}, HookCandidateOrder: []domain.ID{"offer-1"}}}, nil
}

func TestOperationDecisionsRouteUsesReadPermissionAndPersistedProjection(t *testing.T) {
	env := newEnv(t, domain.PermRead)
	recorder := env.do(t, AuthMiddleware(env.auth, OperationQueryHandler(operationQueriesStub{})), http.MethodGet, "/v1/operations/op-1/decisions", env.token, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("decision status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var got []domain.DecisionView
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].DecisionID != "decision-1" || got[0].HookRegistryRevision != "registry-rev" || got[0].HookCandidateOrder[0] != "offer-1" {
		t.Fatalf("decisions = %+v", got)
	}

	noRead := newEnv(t, domain.PermExecute)
	denied := noRead.do(t, AuthMiddleware(noRead.auth, OperationQueryHandler(operationQueriesStub{})), http.MethodGet, "/v1/operations/op-1/decisions", noRead.token, "")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("execute-only decision status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestOperationSubmitRouteBindsPoolAndRequiresExecute(t *testing.T) {
	env := newEnv(t, domain.PermExecute)
	commands := &operationSubmitStub{reply: domain.SubmitReply{
		Kind: "accepted", State: domain.Running, OperationID: "op-1",
		OperationURL: "/v1/operations/op-1", EventsURL: "/v1/operations/op-1/events",
	}}
	handler := AuthMiddleware(env.auth, OperationSubmitHandler(commands))
	body := `{"operationId":"op-1","poolId":"pool-1","request":{"domain":"ai/text","capabilityProfile":"chat","payload":{"prompt":"hello"}},"deadlineAt":9999999999999,"maxAttempts":2,"allowMetered":false}`
	response := env.do(t, handler, http.MethodPost, "/v1/pools/pool-1/operations", env.token, body)
	if response.Code != http.StatusAccepted || !commands.called {
		t.Fatalf("submit status=%d called=%v body=%s", response.Code, commands.called, response.Body.String())
	}
	if commands.auth.Namespace != nsA || commands.input.PoolID != "pool-1" || commands.input.OperationID != "op-1" {
		t.Fatalf("server-bound submit auth=%+v input=%+v", commands.auth, commands.input)
	}
	commands.called = false
	if mismatch := env.do(t, handler, http.MethodPost, "/v1/pools/other/operations", env.token, body); mismatch.Code != http.StatusBadRequest || commands.called {
		t.Fatalf("path/body pool mismatch status=%d called=%v", mismatch.Code, commands.called)
	}
	readOnly := newEnv(t, domain.PermRead)
	denied := readOnly.do(t, AuthMiddleware(readOnly.auth, OperationSubmitHandler(commands)), http.MethodPost, "/v1/pools/pool-1/operations", readOnly.token, body)
	if denied.Code != http.StatusForbidden || commands.called {
		t.Fatalf("read-only submit status=%d called=%v", denied.Code, commands.called)
	}
}

func TestOperationCancelRecordsOnlyAuthorizedIntent(t *testing.T) {
	env := newEnv(t, domain.PermCancel)
	control := &operationCancelStub{ack: domain.CancellationAck{Status: "recorded"}}
	h := AuthMiddleware(env.auth, OperationQueryHandler(operationQueriesStub{}, control))
	response := env.do(t, h, http.MethodPost, "/v1/operations/op-1/cancel", env.token, `{"commandId":"cancel-1"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", response.Code, response.Body.String())
	}
	if control.gotAuth.Namespace != nsA || control.gotKey != (domain.OperationKey{Namespace: nsA, OperationID: "op-1"}) || control.gotCommand != "cancel-1" {
		t.Fatalf("server-derived cancellation request = auth:%+v key:%+v command:%q", control.gotAuth, control.gotKey, control.gotCommand)
	}
	var ack domain.CancellationAck
	if err := json.Unmarshal(response.Body.Bytes(), &ack); err != nil || ack.Status != "recorded" {
		t.Fatalf("cancel ack = %+v err=%v", ack, err)
	}

	readOnly := newEnv(t, domain.PermRead)
	denied := readOnly.do(t, AuthMiddleware(readOnly.auth, OperationQueryHandler(operationQueriesStub{}, control)), http.MethodPost, "/v1/operations/op-1/cancel", readOnly.token, `{"commandId":"cancel-2"}`)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("read-only cancellation status=%d body=%s", denied.Code, denied.Body.String())
	}
	if invalid := env.do(t, h, http.MethodPost, "/v1/operations/op-1/cancel", env.token, `{"commandId":""}`); invalid.Code != http.StatusBadRequest {
		t.Fatalf("empty command id status=%d", invalid.Code)
	}
	control.err = application.ErrOperationCancelConflict
	conflict := env.do(t, h, http.MethodPost, "/v1/operations/op-1/cancel", env.token, `{"commandId":"cancel-1"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("command conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestOperationReconciliationRequiresManageAndBindsAttemptPath(t *testing.T) {
	env := newEnv(t, domain.PermManage)
	command := &operationReconcileStub{view: domain.ReconciliationView{ReconciliationID: "rec-1", ConfirmedOutcome: "known_success"}}
	handler := AuthMiddleware(env.auth, OperationReconciliationHandler(command))
	body := `{"commandId":"reconcile-1","confirmedOutcome":"known_success","retrySafety":"unsafe","evidenceRefs":["review-1"],"corrections":[{"resourceId":"counter","windowKey":"sr:lifetime","metricId":"tokens","unit":"token","consumed":"2","consumptionBasis":"adjustment","occupancy":"not_applicable","evidenceRefs":[]}]}`
	response := env.do(t, handler, http.MethodPost, "/v1/attempts/attempt-1/reconciliations", env.token, body)
	if response.Code != http.StatusCreated || !command.called || command.auth.Namespace != nsA || command.input.AttemptID != "attempt-1" || command.input.ConfirmedOutcome != "known_success" {
		t.Fatalf("status=%d called=%v auth=%+v input=%+v body=%s", response.Code, command.called, command.auth, command.input, response.Body.String())
	}
	command.called = false
	if got := env.do(t, handler, http.MethodPost, "/v1/attempts/other/reconciliations", env.token, body); got.Code != http.StatusCreated || !command.called || command.input.AttemptID != "other" {
		t.Fatalf("path-bound target status=%d called=%v attempt=%s body=%s", got.Code, command.called, command.input.AttemptID, got.Body.String())
	}
	command.called = false
	reader := newEnv(t, domain.PermRead)
	denied := env.do(t, AuthMiddleware(reader.auth, OperationReconciliationHandler(command)), http.MethodPost, "/v1/attempts/attempt-1/reconciliations", reader.token, body)
	if denied.Code != http.StatusForbidden || command.called {
		t.Fatalf("read-only reconciliation status=%d called=%v", denied.Code, command.called)
	}
	if invalid := env.do(t, handler, http.MethodPost, "/v1/attempts/attempt-1/reconciliations", env.token, `{"commandId":"","report":{},"evidenceRefs":[]}`); invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid reconciliation status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	command.err = application.ErrOperationReconcileInvalid
	if conflict := env.do(t, handler, http.MethodPost, "/v1/attempts/attempt-1/reconciliations", env.token, body); conflict.Code != http.StatusBadRequest {
		t.Fatalf("invalid command status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestOperationOutputRouteIsAuthenticatedAndValidatesPagination(t *testing.T) {
	env := newEnv(t, domain.PermRead)
	h := AuthMiddleware(env.auth, OperationQueryHandler(operationQueriesStub{}))
	got := env.do(t, h, http.MethodGet, "/v1/operations/op-1/output?afterSequence=0&limit=10", env.token, "")
	if got.Code != http.StatusOK {
		t.Fatalf("output status=%d body=%s", got.Code, got.Body.String())
	}
	for _, path := range []string{
		"/v1/operations/op-1/output?afterSequence=0&afterSequence=1",
		"/v1/operations/op-1/output?afterSequence=-1",
		"/v1/operations/op-1/output?limit=101",
	} {
		bad := env.do(t, h, http.MethodGet, path, env.token, "")
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("invalid output query %s status=%d body=%s", path, bad.Code, bad.Body.String())
		}
	}
	noPermission := newEnv(t, domain.PermExecute)
	denied := noPermission.do(t, AuthMiddleware(noPermission.auth, OperationQueryHandler(operationQueriesStub{})), http.MethodGet, "/v1/operations/op-1/output", noPermission.token, "")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("execute-only output status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestOperationCancelRouteUsesSQLiteReceiptIdempotency(t *testing.T) {
	ctx := context.Background()
	clock := runtime.NewClock()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "ant.db"), sqlite.Config{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatal(err)
	}
	prep := commands.NewPreparationService(st, "runtime-test", clock)
	controls, err := application.NewOperationControls(prep)
	if err != nil {
		t.Fatal(err)
	}
	env := newEnv(t, domain.PermCancel)
	h := AuthMiddleware(env.auth, OperationQueryHandler(operationQueriesStub{}, controls))
	request := func(operationID string) *httptest.ResponseRecorder {
		return env.do(t, h, http.MethodPost, "/v1/operations/"+operationID+"/cancel", env.token, `{"commandId":"cancel-once"}`)
	}
	first := request("missing-op")
	replay := request("missing-op")
	if first.Code != http.StatusOK || replay.Code != http.StatusOK || first.Body.String() != replay.Body.String() {
		t.Fatalf("first=%d %s replay=%d %s", first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}
	var ack domain.CancellationAck
	if err := json.Unmarshal(first.Body.Bytes(), &ack); err != nil || ack.Status != "notFound" {
		t.Fatalf("ack=%+v err=%v", ack, err)
	}
	conflict := request("another-op")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("reused command ID for another operation status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestOperationQueryRoutesRequireReadAndNeverMutate(t *testing.T) {
	env := newEnv(t, domain.PermRead)
	h := AuthMiddleware(env.auth, OperationQueryHandler(operationQueriesStub{}))
	unauthenticated := env.do(t, h, http.MethodGet, "/v1/operations", "", "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", unauthenticated.Code)
	}
	listed := env.do(t, h, http.MethodGet, "/v1/operations?limit=10&cursor=c", env.token, "")
	if listed.Code != http.StatusOK || listed.Body.String() == "" {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	filtered := env.do(t, h, http.MethodGet, "/v1/operations?state=unknown", env.token, "")
	if filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), `"nextCursor":"unknown"`) {
		t.Fatalf("filtered list status=%d body=%s", filtered.Code, filtered.Body.String())
	}
	for _, path := range []string{"/v1/operations?state=unknown&state=failed", "/v1/operations?state=bogus"} {
		if response := env.do(t, h, http.MethodGet, path, env.token, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid state query %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	got := env.do(t, h, http.MethodGet, "/v1/operations/op-1", env.token, "")
	if got.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", got.Code, got.Body.String())
	}
	if badLimit := env.do(t, h, http.MethodGet, "/v1/operations?limit=201", env.token, ""); badLimit.Code != http.StatusBadRequest {
		t.Fatalf("bad limit status=%d", badLimit.Code)
	}
	missing := env.do(t, AuthMiddleware(env.auth, OperationQueryHandler(operationQueriesStub{missing: true})), http.MethodGet, "/v1/operations/missing", env.token, "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", missing.Code, missing.Body.String())
	}

	other := newEnv(t, domain.PermExecute)
	for _, path := range []string{"/v1/operations", "/v1/operations/op-1"} {
		response := other.do(t, AuthMiddleware(other.auth, OperationQueryHandler(operationQueriesStub{})), http.MethodGet, path, other.token, "")
		if response.Code != http.StatusForbidden {
			t.Fatalf("execute-only %s status=%d", path, response.Code)
		}
	}
}
