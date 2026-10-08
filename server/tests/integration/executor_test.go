package integration

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ant/server/internal/api"
	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/hooks"
	"ant/server/internal/ledger"
	"ant/server/internal/ledger/commands"
	"ant/server/internal/providers/chatcompletions"
	"ant/server/internal/providers/localtest"
	"ant/server/internal/runtime"
	"ant/server/internal/security"
	"ant/server/internal/transport"
)

type sqliteExecutorPlanner struct{ plan domain.ExecutionPlan }

func sqliteChatPlanValidator(request domain.DomainRequest, plan domain.ExecutionPlan) error {
	if err := chatcompletions.ValidateRequest(request); err != nil {
		return err
	}
	var wire struct {
		MaxTokens int64 `json:"max_tokens"`
	}
	if err := json.Unmarshal(request.Payload, &wire); err != nil {
		return err
	}
	for _, requirement := range plan.Requirements {
		if requirement.MetricID == "tokens" && requirement.Unit == "token" {
			if domain.Units(wire.MaxTokens) > requirement.UpperBound {
				return chatcompletions.ErrInvalidRequest
			}
		} else if requirement.Unit != "slot" {
			return chatcompletions.ErrUnsupportedMetric
		}
	}
	return nil
}

func sqliteChatUsageProjector(plan domain.ExecutionPlan, usage chatcompletions.Usage, at domain.Millis, providerRequestID domain.ID) ([]domain.MetricReport, error) {
	total := domain.Units(usage.TotalTokens)
	evidence := domain.ID("fixture-usage/" + string(providerRequestID))
	metrics := make([]domain.MetricReport, 0, len(plan.Requirements))
	for _, requirement := range plan.Requirements {
		var actual *domain.Units
		if requirement.MetricID == "tokens" && requirement.Unit == "token" {
			actual = &total
		} else if requirement.Unit != "slot" {
			return nil, chatcompletions.ErrUnsupportedMetric
		}
		chargedAt := at
		metrics = append(metrics, domain.MetricReport{MetricID: requirement.MetricID, Unit: requirement.Unit, Actual: actual, ChargedAt: &chargedAt, EvidenceRefs: []domain.ID{evidence}})
	}
	return metrics, nil
}

func sqliteChatClassifier(finishReason string, refused bool) (chatcompletions.ResultClassification, error) {
	if finishReason == "stop" && !refused {
		return chatcompletions.ResultClassification{Outcome: "known_success"}, nil
	}
	return chatcompletions.ResultClassification{Outcome: "known_failure", Reasons: []domain.Reason{{Group: "domain", Code: "fixture_incomplete"}}}, nil
}

func (p sqliteExecutorPlanner) PlanPool(_ context.Context, _ domain.AuthContext, _ domain.ExecuteInput, attemptID domain.ID) (application.CandidatePlan, error) {
	plan := p.plan
	plan.PlanID = domain.ID("plan/" + string(attemptID))
	return application.CandidatePlan{ConfigRevision: plan.ConfigRevision, PoolVersion: plan.PoolVersion, Candidates: []domain.Candidate{{
		Member: domain.PoolMember{OfferID: plan.OfferID, Priority: plan.MemberPriority, Enabled: true, Version: 1},
		Offer:  domain.ResourceOffer{OfferID: plan.OfferID, BindingID: plan.BindingID, Domain: "ai/text", CapabilityProfile: "cap-profile", Enabled: true},
		Plan:   plan,
	}}}, nil
}

type sqliteExecutionLedger struct {
	fixture  prepareFixture
	output   *commands.OutputService
	settle   *commands.SettlementService
	finished chan error
}

func (l *sqliteExecutionLedger) Quote(ctx context.Context, auth domain.AuthContext, plan domain.ExecutionPlan) ([]domain.ResourceSnapshot, error) {
	return l.fixture.s.Quote(ctx, auth, plan, l.fixture.clock.Now())
}
func (l *sqliteExecutionLedger) TryPrepare(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, id domain.ID, plan domain.ExecutionPlan, guards []domain.GuardEvidence, digest string, decisionTrace []byte) (ledger.PrepareReply, error) {
	l.fixture.clock.SetNow(20)
	return l.fixture.s.TryPrepare(ctx, auth, owner, id, plan, guards, digest, decisionTrace, 20)
}
func (l *sqliteExecutionLedger) AuthorizeDispatch(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, id domain.ID) (ledger.DispatchPermit, error) {
	return l.fixture.AuthorizeDispatch(ctx, auth, owner, id, 30)
}
func (l *sqliteExecutionLedger) AbortBeforeDispatch(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, id domain.ID, proof ledger.UnsentProof, permit ledger.DispatchPermit) (ledger.AbortOutcome, error) {
	return l.fixture.s.AbortBeforeDispatch(ctx, auth, commands.AbortRequest{Owner: owner, Attempt: id, Proof: proof, Permit: permit})
}
func (l *sqliteExecutionLedger) AppendOutput(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, attemptID domain.ID, chunk domain.OutputChunk) error {
	return l.output.AppendOutput(ctx, auth, owner, attemptID, chunk)
}
func (l *sqliteExecutionLedger) Settle(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, report domain.ExecutionReport) (ledger.SettleReply, error) {
	reply, err := l.settle.Settle(ctx, auth, owner, report)
	select {
	case l.finished <- err:
	default:
	}
	return reply, err
}
func (l *sqliteExecutionLedger) CompleteWithoutAttempt(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, reasons []domain.Reason) (domain.OperationResult, error) {
	return l.fixture.s.CompleteWithoutAttempt(ctx, auth, owner, reasons)
}

func TestHTTPSubmitRunsSQLiteLedgerAndControlledTLSEndToEnd(t *testing.T) {
	f := preparationFixture(t, 2)
	ctx := context.Background()
	f.plan.TargetModelID = "fixture-model"
	f.plan.ResponseModelIDs = []domain.ID{"fixture-model"}
	f.plan.Requirements[0].UpperBound = 2
	f.plan.Bounds[0].UpperBound = 2
	db := openRawDB(t, f.path)
	var requestCount atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer fixture-token" {
			t.Errorf("authorization = %q", got)
		}
		requestBody, err := io.ReadAll(r.Body)
		if err != nil || string(requestBody) != `{"model":"fixture-model","messages":[{"role":"user","content":"integration"}],"max_tokens":1,"stream":false}` {
			t.Errorf("request body = %q, err=%v", requestBody, err)
		}
		var status string
		if err := db.QueryRow(`SELECT status FROM dispatch_permits WHERE attempt_id=(SELECT attempt_id FROM attempts WHERE operation_id='executor-http-op')`).Scan(&status); err != nil || status != "consumed" {
			t.Errorf("permit at network arrival = %q, err=%v", status, err)
		}
		_, _ = io.WriteString(w, `{"id":"fixture-request-1","object":"chat.completion","created":40,"model":"fixture-model","choices":[{"index":0,"message":{"role":"assistant","content":"fixture answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer server.Close()
	if _, err := db.ExecContext(ctx, `UPDATE bindings SET endpoint=? WHERE binding_id='bind-1'`, server.URL); err != nil {
		t.Fatal(err)
	}
	f.plan.Endpoint = server.URL
	clock := f.clock
	output := commands.NewOutputService(f.st, "rt1", clock)
	preparation := f.s
	settlement := commands.NewSettlementService(f.st, "rt1", clock, commands.SettlementPolicy{}, preparation)
	finished := make(chan error, 1)
	executionLedger := &sqliteExecutionLedger{fixture: f, output: output, settle: settlement, finished: finished}
	catalog, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, ok := catalog.Descriptor(f.plan.ProviderTypeID)
	if !ok {
		t.Fatal("localtest provider descriptor missing")
	}
	f.plan.AdapterRevision = descriptor.AdapterRevision
	adapter, err := chatcompletions.New(descriptor, clock, sqliteChatPlanValidator, sqliteChatUsageProjector, sqliteChatClassifier)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	httpClient, err := transport.NewHTTPClientWithRootCAs(roots)
	if err != nil {
		t.Fatal(err)
	}
	controlled, err := transport.NewControlledHTTPTransportWithClient("rt1", integrationCredentialResolver{values: map[string]string{"value": "fixture-token"}}, func(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, permit ledger.DispatchPermit, digest string) error {
		return f.ConsumeDispatchPermit(ctx, auth, owner, permit, digest, 40)
	}, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	hookRegistry := hooks.NewRegistry()
	if err := hookRegistry.Register(hooks.Registration{Name: "integration-orderer", Version: "1", Point: hooks.PointCandidatesReady, Orderer: func(ctx context.Context, event hooks.Event, ids []string) ([]string, error) {
		if event.OperationID != "executor-http-op" || len(ids) != 1 || ids[0] != string(f.plan.OfferID) {
			t.Errorf("candidate hook input event=%+v ids=%v", event, ids)
		}
		return ids, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := hookRegistry.Seal(); err != nil {
		t.Fatal(err)
	}
	executor, err := application.NewCandidateOperationExecutor(application.ExecutorConfig{
		Planner: sqliteExecutorPlanner{plan: f.plan}, Ledger: executionLedger,
		Adapters: application.ExecutionAdapterMap{f.plan.ProviderTypeID: adapter}, Transport: controlled,
		Hooks: hookRegistry, HookTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	host, err := runtime.NewExecutionHost(ctx, runtime.HostConfig{MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	claim := func(ctx context.Context, auth domain.AuthContext, input domain.ExecuteInput, fingerprint string, policy ledger.ClaimPolicy) (ledger.ClaimReply, error) {
		return commands.Claim(ctx, f.st, auth, input, fingerprint, policy)
	}
	router, err := application.NewRouter(f.st, claim, host, clock, executor, application.OperationRouterConfig{
		RuntimeID: "rt1", LeaseMillis: 4000, MaxRuntimeAttempts: 3,
		ValidateRequest: chatcompletions.ValidateRequest,
	})
	if err != nil {
		t.Fatal(err)
	}
	tokens := security.NewMemoryTokenStore()
	const token = "sqlite-executor-integration-token"
	auth := claimAuth()
	auth.Permissions = []domain.Permission{domain.PermExecute, domain.PermRead}
	if err := tokens.Put(security.HashToken(token), security.TokenRecord{PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Permissions: auth.Permissions, Kind: security.TokenKindAPIToken}); err != nil {
		t.Fatal(err)
	}
	handler := api.AuthMiddleware(security.NewAuthProvider(tokens), api.OperationSubmitHandler(router))
	input := claimInput(`{"model":"fixture-model","messages":[{"role":"user","content":"integration"}],"max_tokens":1}`)
	input.OperationID = "executor-http-op"
	input.DeadlineAt = domain.Millis(time.Now().Add(time.Minute).UnixMilli())
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/pools/pool-1/operations", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("submit status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("ledger settlement: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not settle")
	}
	if got := requestCount.Load(); got != 1 {
		t.Fatalf("provider request count=%d, want one", got)
	}
	var state, resultJSON string
	if err := db.QueryRow(`SELECT state,result_json FROM operations WHERE namespace='ns-1' AND operation_id='executor-http-op'`).Scan(&state, &resultJSON); err != nil {
		t.Fatal(err)
	}
	var result domain.OperationResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if state != string(domain.Succeeded) || result.Code != domain.ResultCodeSuccess || result.OutputAvailability != "available" || result.OutputRef != "operation/executor-http-op" || result.Reasons == nil {
		t.Fatalf("terminal operation state=%s result=%+v", state, result)
	}
	if queryInt(t, db, `SELECT COUNT(*) FROM output_chunks o JOIN attempts a USING(attempt_id) WHERE a.operation_id='executor-http-op'`) != 1 || queryInt(t, db, `SELECT COUNT(*) FROM operation_events WHERE operation_id='executor-http-op' AND event_type='operation_terminal'`) != 1 {
		t.Fatal("expected one durable output chunk and terminal event")
	}
	var traceRaw string
	if err := db.QueryRow(`SELECT trace_json FROM decision_records WHERE operation_id='executor-http-op' AND stage='prepare'`).Scan(&traceRaw); err != nil {
		t.Fatalf("read persisted candidate decision: %v", err)
	}
	var trace struct {
		HookRegistryRevision string                 `json:"hookRegistryRevision"`
		HookRegistryManifest hooks.RegistryManifest `json:"hookRegistryManifest"`
		PlannerOrder         []domain.ID            `json:"plannerCandidateOrder"`
		HookOrder            []domain.ID            `json:"hookCandidateOrder"`
		SelectedOfferID      domain.ID              `json:"selectedOfferId"`
	}
	if err := json.Unmarshal([]byte(traceRaw), &trace); err != nil {
		t.Fatalf("decode persisted candidate decision: %v", err)
	}
	revision, err := hookRegistry.Revision()
	if err != nil {
		t.Fatal(err)
	}
	if trace.HookRegistryRevision != revision || trace.HookRegistryManifest.APIVersion != hooks.RegistryAPIVersion || len(trace.PlannerOrder) != 1 || trace.PlannerOrder[0] != f.plan.OfferID || len(trace.HookOrder) != 1 || trace.HookOrder[0] != f.plan.OfferID || trace.SelectedOfferID != f.plan.OfferID {
		t.Fatalf("persisted candidate decision = %+v", trace)
	}
	var persistedOutput string
	if err := db.QueryRow(`SELECT CAST(o.data AS TEXT) FROM output_chunks o JOIN attempts a USING(attempt_id) WHERE a.operation_id='executor-http-op'`).Scan(&persistedOutput); err != nil || persistedOutput != "fixture answer" {
		t.Fatalf("persisted output=%q err=%v", persistedOutput, err)
	}
}
