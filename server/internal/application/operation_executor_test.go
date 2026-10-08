package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
	"ant/server/internal/hooks"
	"ant/server/internal/ledger"
	"ant/server/internal/transport"
)

type executorPlannerStub struct {
	candidate  domain.Candidate
	candidates []domain.Candidate
	err        error
	calls      int
}

func (p *executorPlannerStub) PlanPool(_ context.Context, _ domain.AuthContext, _ domain.ExecuteInput, attempt domain.ID) (CandidatePlan, error) {
	p.calls++
	if p.err != nil {
		return CandidatePlan{}, p.err
	}
	candidate := p.candidate
	candidate.Plan.PlanID = domain.ID("plan/" + string(attempt))
	if p.candidates != nil {
		candidates := make([]domain.Candidate, len(p.candidates))
		for i, item := range p.candidates {
			item.Plan.PlanID = domain.ID("plan/" + string(attempt) + "/" + string(item.Offer.OfferID))
			candidates[i] = item
		}
		return CandidatePlan{ConfigRevision: "cfg-1", PoolVersion: 1, Candidates: candidates}, nil
	}
	return CandidatePlan{ConfigRevision: "cfg-1", PoolVersion: 1, Candidates: []domain.Candidate{candidate}}, nil
}

type executorLedgerStub struct {
	prepareKind    string
	prepareErr     error
	settleErr      error
	abortCalls     int
	lastAbortProof ledger.UnsentProof
	settleCalls    int
	output         []domain.OutputChunk
	completed      [][]domain.Reason
	quotedOffers   []domain.ID
	decisionTraces [][]byte
}

func (l *executorLedgerStub) Quote(_ context.Context, _ domain.AuthContext, plan domain.ExecutionPlan) ([]domain.ResourceSnapshot, error) {
	l.quotedOffers = append(l.quotedOffers, plan.OfferID)
	return []domain.ResourceSnapshot{{ResourceID: "resource-1", Available: 10}}, nil
}

func (l *executorLedgerStub) TryPrepare(_ context.Context, _ domain.AuthContext, _ domain.OwnerToken, id domain.ID, plan domain.ExecutionPlan, _ []domain.GuardEvidence, digest string, decisionTrace []byte) (ledger.PrepareReply, error) {
	l.decisionTraces = append(l.decisionTraces, append([]byte(nil), decisionTrace...))
	if l.prepareErr != nil {
		return ledger.PrepareReply{}, l.prepareErr
	}
	if l.prepareKind == "blocked" {
		return ledger.PrepareReply{Kind: "blocked", Reasons: []domain.Reason{{Group: "admission", Code: "capacity"}}}, nil
	}
	return ledger.PrepareReply{Kind: "prepared", Attempt: &domain.PreparedAttempt{AttemptID: id, Plan: plan, RequestDigest: digest}}, nil
}
func (*executorLedgerStub) AuthorizeDispatch(context.Context, domain.AuthContext, domain.OwnerToken, domain.ID) (ledger.DispatchPermit, error) {
	return ledger.DispatchPermit{}, nil
}
func (l *executorLedgerStub) AbortBeforeDispatch(_ context.Context, _ domain.AuthContext, _ domain.OwnerToken, _ domain.ID, proof ledger.UnsentProof, _ ledger.DispatchPermit) (ledger.AbortOutcome, error) {
	l.abortCalls++
	l.lastAbortProof = proof
	return ledger.AbortOutcome{}, nil
}
func (l *executorLedgerStub) AppendOutput(_ context.Context, _ domain.AuthContext, _ domain.OwnerToken, _ domain.ID, chunk domain.OutputChunk) error {
	l.output = append(l.output, chunk)
	return nil
}
func (l *executorLedgerStub) Settle(context.Context, domain.AuthContext, domain.OwnerToken, domain.ExecutionReport) (ledger.SettleReply, error) {
	l.settleCalls++
	return ledger.SettleReply{}, l.settleErr
}
func (l *executorLedgerStub) CompleteWithoutAttempt(_ context.Context, _ domain.AuthContext, _ domain.OwnerToken, reasons []domain.Reason) (domain.OperationResult, error) {
	l.completed = append(l.completed, append([]domain.Reason(nil), reasons...))
	return domain.OperationResult{State: domain.Succeeded}, nil
}

type executorAdapterStub struct{ descriptor domain.ProviderDescriptor }

func (a executorAdapterStub) Descriptor() domain.ProviderDescriptor { return a.descriptor }
func (a executorAdapterStub) Encode(_ domain.DomainRequest, plan domain.ExecutionPlan) (adapters.EncodedRequest, error) {
	return adapters.EncodedRequest{Endpoint: plan.Endpoint, Method: http.MethodPost, Body: []byte("request")}, nil
}
func (executorAdapterStub) Decode(_ context.Context, response adapters.RawResponse, _ domain.ExecutionPlan, attempt domain.ID, storeOutput func([]byte) error) (domain.ExecutionReport, error) {
	if _, err := io.ReadAll(response.Body); err != nil {
		return domain.ExecutionReport{}, err
	}
	if err := storeOutput([]byte("answer")); err != nil {
		return domain.ExecutionReport{}, err
	}
	return domain.ExecutionReport{ReportID: "report/1", AttemptID: attempt, Outcome: "success"}, nil
}

type executorTransportStub struct {
	handles     *transport.HandleTable
	prepareErr  error
	dispatchErr error
	sends       int
}

func (t *executorTransportStub) Prepare(_ context.Context, _ domain.AuthContext, _ domain.OwnerToken, _ domain.ID, encoded adapters.EncodedRequest, plan domain.ExecutionPlan) (transport.PreparedHTTPRequest, error) {
	if t.prepareErr != nil {
		return transport.PreparedHTTPRequest{}, t.prepareErr
	}
	return t.handles.Prepare(encoded, plan)
}
func (t *executorTransportStub) Discard(handle transport.PreparedHTTPRequest) {
	t.handles.Discard(handle)
}
func (t *executorTransportStub) Dispatch(_ context.Context, _ ledger.DispatchPermit, _ transport.PreparedHTTPRequest) (adapters.RawResponse, error) {
	t.sends++
	if t.dispatchErr != nil {
		return adapters.RawResponse{}, t.dispatchErr
	}
	return adapters.RawResponse{Status: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
}

func executorFixture(t *testing.T, transportErr error, prepareKind string) (*CandidateOperationExecutor, *executorLedgerStub, *executorTransportStub) {
	t.Helper()
	provider := domain.ID("provider-fixture")
	descriptor := domain.ProviderDescriptor{TypeID: provider, AdapterRevision: "adapter-1", SupportedDomains: []string{"ai/text"}}
	planner := &executorPlannerStub{candidate: domain.Candidate{Offer: domain.ResourceOffer{OfferID: "offer-1"}, Plan: domain.ExecutionPlan{
		ProviderTypeID: provider, OfferID: "offer-1", AdapterRevision: descriptor.AdapterRevision, Endpoint: "https://fixture.invalid", CredentialRevision: "credential-1",
		Requirements: []domain.ResourceRequirement{{Mode: "all_constraints", UpperBound: 1, ResourceIDs: []domain.ID{"resource-1"}}},
	}}}
	store := &executorLedgerStub{prepareKind: prepareKind}
	wire := &executorTransportStub{handles: transport.NewHandleTable(), dispatchErr: transportErr}
	planner.candidate.Plan.PoolID = "pool-1"
	planner.candidate.Plan.PoolVersion = 1
	planner.candidate.Plan.ConfigRevision = "cfg-1"
	executor, err := NewCandidateOperationExecutor(ExecutorConfig{
		Planner: planner, Ledger: store, Adapters: ExecutionAdapterMap{provider: executorAdapterStub{descriptor}}, Transport: wire,
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor, store, wire
}

func executorInput() (domain.AuthContext, domain.OwnerToken, domain.ExecuteInput) {
	auth := domain.AuthContext{PrincipalID: "principal-1", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermExecute}}
	owner := domain.OwnerToken{Key: domain.OperationKey{Namespace: "ns-1", OperationID: "op-1"}, RuntimeID: "runtime-1", OwnerID: "owner-1", Generation: 1}
	input := domain.ExecuteInput{OperationID: "op-1", PoolID: "pool-1", MaxAttempts: 2, Request: domain.DomainRequest{Domain: "ai/text", CapabilityProfile: "text-v1", Payload: []byte(`{"prompt":"hello"}`)}}
	return auth, owner, input
}

func TestCandidateOperationExecutorPersistsOutputBeforeSettlement(t *testing.T) {
	executor, store, wire := executorFixture(t, nil, "prepared")
	auth, owner, input := executorInput()
	executor.Execute(context.Background(), auth, owner, input)
	if wire.sends != 1 || store.settleCalls != 1 || store.abortCalls != 0 || len(store.completed) != 0 {
		t.Fatalf("sends=%d settles=%d aborts=%d completed=%v", wire.sends, store.settleCalls, store.abortCalls, store.completed)
	}
	if len(store.output) != 1 || store.output[0].Sequence != 1 || string(store.output[0].Data) != "answer" {
		t.Fatalf("persisted output = %+v", store.output)
	}
}

func TestCandidateOperationExecutorCompletesAfterAllCandidatesAreBlocked(t *testing.T) {
	executor, store, wire := executorFixture(t, nil, "blocked")
	auth, owner, input := executorInput()
	executor.Execute(context.Background(), auth, owner, input)
	if wire.sends != 0 || store.settleCalls != 0 || len(store.completed) != 1 {
		t.Fatalf("sends=%d settles=%d completed=%v", wire.sends, store.settleCalls, store.completed)
	}
	if len(store.completed[0]) != 1 || store.completed[0][0].Code != "capacity" {
		t.Fatalf("completion reasons = %+v", store.completed[0])
	}
}

func TestCandidateOperationExecutorDoesNotRetryAfterTransportFailure(t *testing.T) {
	wantErr := errors.New("transport failed after permit")
	executor, store, wire := executorFixture(t, wantErr, "prepared")
	auth, owner, input := executorInput()
	executor.Execute(context.Background(), auth, owner, input)
	if wire.sends != 1 || store.abortCalls != 1 || store.settleCalls != 0 || len(store.completed) != 0 {
		t.Fatalf("sends=%d aborts=%d settles=%d completed=%v", wire.sends, store.abortCalls, store.settleCalls, store.completed)
	}
}

func TestCandidateOperationExecutorUsesHookOrderWithoutExposingRequestOrSecrets(t *testing.T) {
	provider := domain.ID("provider-fixture")
	descriptor := domain.ProviderDescriptor{TypeID: provider, AdapterRevision: "adapter-1", SupportedDomains: []string{"ai/text"}}
	candidate := func(id domain.ID, endpoint, credential string) domain.Candidate {
		return domain.Candidate{Offer: domain.ResourceOffer{OfferID: id}, Plan: domain.ExecutionPlan{
			ProviderTypeID: provider, OfferID: id, PoolID: "pool-1", PoolVersion: 1, ConfigRevision: "cfg-1", MemberPriority: 10, AdapterRevision: descriptor.AdapterRevision,
			Endpoint: endpoint, CredentialRevision: domain.ID(credential),
			Requirements: []domain.ResourceRequirement{{Mode: "all_constraints", UpperBound: 1, ResourceIDs: []domain.ID{"resource-1"}}},
		}}
	}
	planner := &executorPlannerStub{candidates: []domain.Candidate{
		candidate("offer-first", "https://private.invalid/first", "secret-revision-1"),
		candidate("offer-second", "https://private.invalid/second", "secret-revision-2"),
	}}
	store := &executorLedgerStub{prepareKind: "prepared"}
	wire := &executorTransportStub{handles: transport.NewHandleTable()}
	registry := hooks.NewRegistry()
	if err := registry.Register(hooks.Registration{Name: "test-reverse-order", Version: "1", Point: hooks.PointCandidatesReady, Orderer: func(_ context.Context, event hooks.Event, ids []string) ([]string, error) {
		if event.OperationID != "op-1" || strings.Contains(string(event.Payload), "hello") || strings.Contains(string(event.Payload), "private.invalid") || strings.Contains(string(event.Payload), "secret-revision") {
			t.Errorf("hook event exposed unexpected data: %+v", event)
		}
		return []string{ids[1], ids[0]}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Seal(); err != nil {
		t.Fatal(err)
	}
	executor, err := NewCandidateOperationExecutor(ExecutorConfig{
		Planner: planner, Ledger: store, Adapters: ExecutionAdapterMap{provider: executorAdapterStub{descriptor}},
		Transport: wire, Hooks: registry, HookTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, owner, input := executorInput()
	executor.Execute(context.Background(), auth, owner, input)
	if len(store.quotedOffers) != 1 || store.quotedOffers[0] != "offer-second" || wire.sends != 1 || store.settleCalls != 1 || len(store.decisionTraces) != 1 {
		t.Fatalf("quoted offers=%v sends=%d settlements=%d", store.quotedOffers, wire.sends, store.settleCalls)
	}
	var trace executionDecisionTrace
	if err := json.Unmarshal(store.decisionTraces[0], &trace); err != nil {
		t.Fatal(err)
	}
	revision, err := registry.Revision()
	if err != nil {
		t.Fatal(err)
	}
	if trace.HookRegistryRevision != revision || len(trace.PlannerCandidateOrder) != 2 || trace.PlannerCandidateOrder[0] != "offer-first" || len(trace.HookCandidateOrder) != 2 || trace.HookCandidateOrder[0] != "offer-second" || trace.SelectedOfferID != "offer-second" {
		t.Fatalf("decision trace = %+v", trace)
	}
}

func TestCandidateOperationExecutorFailsClosedOnInvalidHookOrder(t *testing.T) {
	executor, store, wire := executorFixture(t, nil, "prepared")
	registry := hooks.NewRegistry()
	if err := registry.Register(hooks.Registration{Name: "test-invalid-order", Version: "1", Point: hooks.PointCandidatesReady, Orderer: func(context.Context, hooks.Event, []string) ([]string, error) {
		return []string{"invented-offer"}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Seal(); err != nil {
		t.Fatal(err)
	}
	executor.hooks = registry
	executor.hookTimeout = time.Second
	auth, owner, input := executorInput()
	executor.Execute(context.Background(), auth, owner, input)
	if wire.sends != 0 || store.settleCalls != 0 || len(store.completed) != 1 || len(store.completed[0]) != 1 || store.completed[0][0].Code != "candidate_order_failed" {
		t.Fatalf("sends=%d settlements=%d completed=%+v", wire.sends, store.settleCalls, store.completed)
	}
}

func TestCandidateOperationExecutorCandidateGuardDenialDoesNotPrepareOrSend(t *testing.T) {
	executor, store, wire := executorFixture(t, nil, "prepared")
	registry := hooks.NewRegistry()
	if err := registry.Register(hooks.Registration{Name: "deny-candidate", Version: "1", Point: hooks.PointCandidatePrepare, Guard: func(_ context.Context, event hooks.Event) hooks.GuardDecision {
		if event.OperationID != "op-1" || event.AttemptID == "" || strings.Contains(string(event.Payload), "fixture.invalid") || strings.Contains(string(event.Payload), "credential") {
			t.Errorf("candidate guard received unexpected event: %+v", event)
		}
		return hooks.GuardDecision{Allowed: false, Code: "candidate-policy"}
	}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Seal(); err != nil {
		t.Fatal(err)
	}
	executor.hooks = registry
	executor.hookTimeout = time.Second
	auth, owner, input := executorInput()
	executor.Execute(context.Background(), auth, owner, input)
	if len(store.quotedOffers) != 0 || len(store.decisionTraces) != 0 || wire.sends != 0 || store.settleCalls != 0 || len(store.completed) != 1 {
		t.Fatalf("quotes=%v traces=%d sends=%d settles=%d completed=%+v", store.quotedOffers, len(store.decisionTraces), wire.sends, store.settleCalls, store.completed)
	}
	if len(store.completed[0]) != 1 || store.completed[0][0].Group != "hook" || store.completed[0][0].Code != "candidate_rejected" {
		t.Fatalf("completion reasons = %+v", store.completed[0])
	}
}

func TestCandidateOperationExecutorGuardsFailClosedBeforeAuthorizeSendAndSettle(t *testing.T) {
	for _, point := range []hooks.Point{hooks.PointAttemptAuthorize, hooks.PointDispatchBeforeSend, hooks.PointReportBeforeSettle} {
		t.Run(string(point), func(t *testing.T) {
			executor, store, wire := executorFixture(t, nil, "prepared")
			registry := hooks.NewRegistry()
			if err := registry.Register(hooks.Registration{Name: "deny-stage", Version: "1", Point: point, Guard: func(_ context.Context, event hooks.Event) hooks.GuardDecision {
				if event.OperationID != "op-1" || event.AttemptID == "" || strings.Contains(string(event.Payload), "hello") || strings.Contains(string(event.Payload), "fixture.invalid") {
					t.Errorf("lifecycle guard received unexpected event: %+v", event)
				}
				return hooks.GuardDecision{Allowed: false, Code: "stage-policy"}
			}}); err != nil {
				t.Fatal(err)
			}
			if err := registry.Seal(); err != nil {
				t.Fatal(err)
			}
			executor.hooks = registry
			executor.hookTimeout = time.Second
			auth, owner, input := executorInput()
			executor.Execute(context.Background(), auth, owner, input)
			wantSends := 0
			if point == hooks.PointReportBeforeSettle {
				wantSends = 1
			}
			if wire.sends != wantSends || store.abortCalls != 1 || store.lastAbortProof != ledger.ProofNone || store.settleCalls != 0 || len(store.completed) != 0 {
				t.Fatalf("sends=%d aborts=%d proof=%s settles=%d completed=%+v", wire.sends, store.abortCalls, store.lastAbortProof, store.settleCalls, store.completed)
			}
		})
	}
}

func TestQuoteFitsChecksEveryConstraintAndAggregatesWaterfall(t *testing.T) {
	constraints := domain.ExecutionPlan{Requirements: []domain.ResourceRequirement{{
		Mode: "all_constraints", UpperBound: 4, ResourceIDs: []domain.ID{"r1", "r2"},
	}}}
	if _, fits := quoteFits(constraints, []domain.ResourceSnapshot{{ResourceID: "r1", Available: 4}, {ResourceID: "r2", Available: 3}}); fits {
		t.Fatal("all-constraints quote fit despite one insufficient resource")
	}
	waterfall := domain.ExecutionPlan{Requirements: []domain.ResourceRequirement{{
		Mode: "provider_entitlement_waterfall", UpperBound: 7, ResourceIDs: []domain.ID{"r1", "r2"},
	}}}
	if reason, fits := quoteFits(waterfall, []domain.ResourceSnapshot{{ResourceID: "r1", Available: 4}, {ResourceID: "r2", Available: 3}}); !fits {
		t.Fatalf("waterfall quote = %v, %v; want fit", reason, fits)
	}
	if _, fits := quoteFits(waterfall, []domain.ResourceSnapshot{{ResourceID: "r1", Available: 4}, {ResourceID: "r1", Available: 3}}); fits {
		t.Fatal("quote fit with duplicate resource identities")
	}
}
