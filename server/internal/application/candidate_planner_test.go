package application

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"ant/server/internal/config"
	"ant/server/internal/domain"
	"ant/server/internal/providers/localtest"
	"ant/server/internal/store"
)

type candidateStoreStub struct {
	store.Store
	reader store.ReaderTx
}

func (s candidateStoreStub) Read(ctx context.Context, fn func(store.ReaderTx) error) error {
	return fn(s.reader)
}

type candidateReaderStub struct {
	store.ReaderTx
	active   store.ActiveConfigRecord
	revision store.ConfigRevisionRecord
	pool     store.PoolRecord
	members  []store.PoolMemberRecord
	offers   map[domain.ID]store.OfferRecord
}

func (r candidateReaderStub) GetActiveConfig() (store.ActiveConfigRecord, bool, error) {
	return r.active, true, nil
}
func (r candidateReaderStub) GetConfigRevision(id domain.ID) (store.ConfigRevisionRecord, bool, error) {
	return r.revision, id == r.revision.Revision, nil
}
func (r candidateReaderStub) GetPool(id domain.ID) (store.PoolRecord, bool, error) {
	return r.pool, id == r.pool.PoolID, nil
}
func (r candidateReaderStub) ListPoolMembers(id domain.ID) ([]store.PoolMemberRecord, error) {
	if id != r.pool.PoolID {
		return nil, nil
	}
	return append([]store.PoolMemberRecord(nil), r.members...), nil
}
func (r candidateReaderStub) GetOffer(id domain.ID) (store.OfferRecord, bool, error) {
	value, ok := r.offers[id]
	return value, ok, nil
}

type candidatePolicyStub struct {
	calls                int
	wrongMappingRevision bool
}

type providerCandidatePolicyStub struct {
	*candidatePolicyStub
	provider domain.ID
}

func (p *providerCandidatePolicyStub) ProviderTypeID() domain.ID { return p.provider }

type candidateTestClock struct{ now domain.Millis }

func (c *candidateTestClock) Now() domain.Millis { return c.now }

func (p *candidatePolicyStub) Domain() string               { return "ai/text" }
func (p *candidatePolicyStub) CapabilityProfile() domain.ID { return "profile.chat.basic" }
func (p *candidatePolicyStub) Build(_ context.Context, _ domain.ExecuteInput, _ config.OfferDef, _ config.BindingDef, _ config.AccountDef, resources []config.CompiledResource, evidence []config.EvidenceDef) (PlanConstraints, []domain.Reason, error) {
	p.calls++
	plan := PlanConstraints{TargetModelID: "model-x", ResponseModelIDs: []domain.ID{"model-x"}, DomainRevision: "domain-r1", ReportMappingRevision: "report-r1"}
	for _, resource := range resources {
		var boundDef *config.EvidenceDef
		for i := range evidence {
			if slices.Contains(resource.EvidenceRefs, evidence[i].EvidenceID) && (evidence[i].Kind == "bound" || evidence[i].Kind == "hard_stop") {
				boundDef = &evidence[i]
				break
			}
		}
		if boundDef == nil {
			return PlanConstraints{}, nil, ErrCandidateIntegrity
		}
		upper, err := domain.ParseUnits(boundDef.Payload["bound"])
		if err != nil {
			return PlanConstraints{}, nil, err
		}
		plan.Requirements = append(plan.Requirements, domain.ResourceRequirement{
			MetricID: resource.MetricID, Unit: resource.Unit, UpperBound: upper,
			EvidenceID: boundDef.EvidenceID, Mode: "all_constraints", ResourceIDs: []domain.ID{resource.ResourceID},
		})
		if resource.CommercialRole == "entitlement" {
			plan.Requirements[len(plan.Requirements)-1].Mode = "provider_entitlement_waterfall"
		}
		mappingRevision := domain.ID("report-r1")
		if p.wrongMappingRevision {
			mappingRevision = "stale-mapping-r0"
		}
		plan.Bounds = append(plan.Bounds, domain.BoundEvidence{
			EvidenceID: boundDef.EvidenceID, MetricID: resource.MetricID, Unit: resource.Unit, UpperBound: upper,
			EnforcementSource: boundDef.Payload["enforcement"], MappingRevision: mappingRevision, EndpointRef: "fixture-endpoint",
			SourceRef: domain.ID(boundDef.SourceRef), VerifiedAt: boundDef.VerifiedAtMillis, ExpiresAt: boundDef.ExpiresAtMillis,
		})
	}
	return plan, nil, nil
}

func candidatePlannerFixture(t *testing.T) (*CandidatePlanner, *candidatePolicyStub) {
	planner, policy, _ := candidatePlannerFixtureWithClock(t)
	return planner, policy
}

func candidatePlannerFixtureWithClock(t *testing.T) (*CandidatePlanner, *candidatePolicyStub, *candidateTestClock) {
	t.Helper()
	fixture, err := os.ReadFile("../../../contracts/fixtures/config-document-valid.json")
	if err != nil {
		t.Fatal(err)
	}

	catalog, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	policy := &candidatePolicyStub{}
	members := []store.PoolMemberRecord{
		{PoolID: "pool-main", OfferID: "offer-alpha-chat-a", Priority: 7, Enabled: true, Version: 2},
		{PoolID: "pool-main", OfferID: "offer-alpha-chat-b", Priority: 7, Enabled: true, Version: 3},
	}
	reader := candidateReaderStub{
		active:   store.ActiveConfigRecord{Revision: "config-r1", Version: 1},
		revision: store.ConfigRevisionRecord{Revision: "config-r1", DefinitionJSON: fixture},
		pool:     store.PoolRecord{PoolID: "pool-main", Namespace: "ns-demo", Enabled: true, Version: 9},
		members:  members,
		offers: map[domain.ID]store.OfferRecord{
			"offer-alpha-chat-a": {OfferID: "offer-alpha-chat-a", BindingID: "bind-alpha-main", Domain: "ai/text", CapabilityProfile: "profile.chat.basic", Enabled: true, Version: 4},
			"offer-alpha-chat-b": {OfferID: "offer-alpha-chat-b", BindingID: "bind-alpha-main", Domain: "ai/text", CapabilityProfile: "profile.chat.basic", Enabled: true, Version: 5},
		},
	}
	clock := &candidateTestClock{now: 1760000000000}
	planner, err := NewCandidatePlannerWithClock(candidateStoreStub{reader: reader}, catalog, clock, policy)
	if err != nil {
		t.Fatal(err)
	}
	return planner, policy, clock
}

func TestCandidatePlannerSelectsProviderSpecificPolicyForSharedProfile(t *testing.T) {
	catalog, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	alpha := &providerCandidatePolicyStub{candidatePolicyStub: &candidatePolicyStub{}, provider: "provider-alpha"}
	deepseek := &providerCandidatePolicyStub{candidatePolicyStub: &candidatePolicyStub{}, provider: "deepseek-standard"}
	fallback := &candidatePolicyStub{}
	planner, err := NewCandidatePlannerWithClock(candidateStoreStub{}, catalog, &candidateTestClock{now: 1_800_000_000_000}, alpha, deepseek, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if planner.policyFor("provider-alpha", "ai/text", "profile.chat.basic") != alpha {
		t.Fatal("provider-alpha must use its provider-specific policy")
	}
	if planner.policyFor("deepseek-standard", "ai/text", "profile.chat.basic") != deepseek {
		t.Fatal("DeepSeek must use its provider-specific policy for the shared profile")
	}
	if planner.policyFor("unregistered-provider", "ai/text", "profile.chat.basic") != fallback {
		t.Fatal("providers without a specific policy should use the registered generic fallback")
	}
}

func candidateExecuteInput() (domain.AuthContext, domain.ExecuteInput) {
	return domain.AuthContext{PrincipalID: "p1", Namespace: "ns-demo", Permissions: []domain.Permission{domain.PermExecute}}, domain.ExecuteInput{
		OperationID: "op-1", PoolID: "pool-main", Request: domain.DomainRequest{Domain: "ai/text", CapabilityProfile: "profile.chat.basic", Payload: json.RawMessage(`{"prompt":"fixture"}`)}, AllowMetered: true,
	}
}

func TestCandidatePlannerCompilesValidatedActivePoolAndRanksAfterEligibility(t *testing.T) {
	planner, policy := candidatePlannerFixture(t)
	auth, input := candidateExecuteInput()
	result, err := planner.PlanPool(context.Background(), auth, input, "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfigRevision != "config-r1" || result.PoolVersion != 9 || len(result.Candidates) != 2 || len(result.Rejected) != 0 {
		t.Fatalf("candidate plan = %+v", result)
	}
	if result.Candidates[0].Offer.OfferID != "offer-alpha-chat-a" || result.Candidates[1].Offer.OfferID != "offer-alpha-chat-b" {
		t.Fatalf("candidate order = %q, %q; want stable offer-id tie order a,b", result.Candidates[0].Offer.OfferID, result.Candidates[1].Offer.OfferID)
	}
	for _, candidate := range result.Candidates {
		if candidate.Plan.ConfigRevision != "config-r1" || candidate.Plan.PoolVersion != 9 || candidate.Plan.MemberPriority != candidate.Member.Priority || candidate.Plan.DomainRevision != "domain-r1" || candidate.Plan.ReportMappingRevision != "report-r1" || candidate.Plan.ProviderTypeID != "provider-alpha" || candidate.Plan.Endpoint != "https://fixture.invalid/v1/messages" || candidate.Plan.CredentialRef != "secret-alpha-key" || len(candidate.Plan.Requirements) != 3 {
			t.Fatalf("candidate plan fields = %+v", candidate)
		}
	}
	if policy.calls != 2 {
		t.Fatalf("policy calls=%d, want one per eligible offer", policy.calls)
	}
}

func TestCandidatePlannerRejectsExpiredEvidence(t *testing.T) {
	planner, _, clock := candidatePlannerFixtureWithClock(t)
	clock.now = 1790899200001
	auth, input := candidateExecuteInput()
	_, err := planner.PlanPool(context.Background(), auth, input, "attempt-1")
	if err != ErrCandidateEvidenceStale {
		t.Fatalf("PlanPool() error = %v, want ErrCandidateEvidenceStale", err)
	}
}

func TestCandidatePlannerRejectsMismatchedReportMappingRevision(t *testing.T) {
	planner, policy := candidatePlannerFixture(t)
	policy.wrongMappingRevision = true
	auth, input := candidateExecuteInput()
	_, err := planner.PlanPool(context.Background(), auth, input, "attempt-1")
	if err != ErrCandidatePolicyFailure {
		t.Fatalf("PlanPool() error = %v, want ErrCandidatePolicyFailure", err)
	}
}

func TestCandidatePlannerRejectsDisabledAccountBeforePlanning(t *testing.T) {
	planner, policy := candidatePlannerFixture(t)
	currentStore := planner.store.(candidateStoreStub)
	reader := currentStore.reader.(candidateReaderStub)
	var document config.ConfigDocument
	if err := json.Unmarshal(reader.revision.DefinitionJSON, &document); err != nil {
		t.Fatal(err)
	}
	document.Accounts[0].Enabled = false
	updatedDefinition, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	reader.revision.DefinitionJSON = updatedDefinition
	planner.store = candidateStoreStub{reader: reader}

	auth, input := candidateExecuteInput()
	result, err := planner.PlanPool(context.Background(), auth, input, "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 0 || len(result.Rejected) != 2 || policy.calls != 0 {
		t.Fatalf("disabled account result = %+v, policy calls=%d", result, policy.calls)
	}
	for _, rejection := range result.Rejected {
		if len(rejection.Reasons) != 1 || rejection.Reasons[0].Code != "account_disabled" {
			t.Fatalf("disabled account reasons = %+v", rejection)
		}
	}
}

func TestCandidatePlannerRejectsMissingPolicyBeforeReadingConfig(t *testing.T) {
	catalog, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	reader := candidateReaderStub{}
	planner, err := NewCandidatePlanner(candidateStoreStub{reader: reader}, catalog, &candidatePolicyStub{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = planner.PlanPool(context.Background(), domain.AuthContext{PrincipalID: "p1", Namespace: "ns-demo", Permissions: []domain.Permission{domain.PermExecute}}, domain.ExecuteInput{
		OperationID: "op-1", PoolID: "pool-main", Request: domain.DomainRequest{Domain: "email/send", CapabilityProfile: "mail.basic", Payload: json.RawMessage(`{}`)},
	}, "attempt-1")
	if err != ErrCandidatePolicyMissing {
		t.Fatalf("PlanPool() error = %v, want ErrCandidatePolicyMissing", err)
	}
}

func TestCandidatePlannerWithClockRejectsMissingClock(t *testing.T) {
	catalog, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCandidatePlannerWithClock(candidateStoreStub{}, catalog, nil, &candidatePolicyStub{}); err != ErrCandidatePlannerInvalid {
		t.Fatalf("planner with nil clock error=%v, want ErrCandidatePlannerInvalid", err)
	}
}
