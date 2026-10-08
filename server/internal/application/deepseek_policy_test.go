package application

import (
	"context"
	"encoding/json"
	"testing"

	"ant/server/internal/config"
	"ant/server/internal/domain"
	"ant/server/internal/providers/deepseek"
)

func TestDeepSeekTextPolicyReservesWorstCasePriceAndOneSlot(t *testing.T) {
	const now = 1_800_000_000_000
	order := int64(10)
	resources := []config.CompiledResource{
		{ResourceID: "balance", MetricID: "metric.usd.cents", Unit: "usd_cents", Kind: "counter", CommercialRole: "entitlement", Capacity: 100, ProviderOrder: &order, EntitlementGroupID: "balance-group", EvidenceRefs: []domain.ID{"e-balance"}},
		{ResourceID: "slots", MetricID: "metric.concurrency.request", Unit: "slot", Kind: "occupancy", CommercialRole: "limit", Capacity: 1, EvidenceRefs: []domain.ID{"e-slots"}},
	}
	evidence := []config.EvidenceDef{
		{EvidenceID: "e-balance", Kind: "bound", SourceRef: "verified:balance", VerifiedAtMillis: now - 1, ExpiresAtMillis: now + 1_000, Payload: map[string]string{"bound": "100", "metricId": "metric.usd.cents", "enforcement": "provider balance"}},
		{EvidenceID: "e-slots", Kind: "hard_stop", SourceRef: "verified:concurrency", VerifiedAtMillis: now - 1, ExpiresAtMillis: now + 1_000, Payload: map[string]string{"bound": "1", "metricId": "metric.concurrency.request", "enforcement": "provider concurrency"}},
	}
	input := domain.ExecuteInput{Request: domain.DomainRequest{
		Domain: "ai/text", CapabilityProfile: "profile.chat.basic",
		Payload: []byte(`{"model":"deepseek-flash","messages":[{"role":"user","content":"hello"}],"max_tokens":64}`),
	}}
	offer := config.OfferDef{Domain: "ai/text", CapabilityProfile: "profile.chat.basic"}
	binding := config.BindingDef{BindingID: "binding-deepseek", Domain: "ai/text", Endpoint: "https://api.deepseek.com/chat/completions"}
	account := config.AccountDef{ProviderTypeID: deepseek.ProviderTypeID}
	plan, reasons, err := (DeepSeekTextPolicy{}).Build(context.Background(), input, offer, binding, account, resources, evidence)
	if err != nil || len(reasons) != 0 {
		t.Fatalf("plan err=%v reasons=%v", err, reasons)
	}
	if plan.TargetModelID != deepseek.SupportedModel || plan.DomainRevision != deepSeekDomainRevision || plan.ReportMappingRevision != deepSeekMappingRevision || len(plan.Requirements) != 2 || len(plan.Bounds) != 2 {
		t.Fatalf("DeepSeek plan did not bind its model, tariff mapping and resources: %+v", plan)
	}
	if plan.Requirements[0].MetricID != "metric.usd.cents" || plan.Requirements[0].UpperBound != 31 || plan.Requirements[0].Mode != "provider_entitlement_waterfall" {
		t.Fatalf("64 output tokens must reserve the worst-case 1M-context peak price: %+v", plan.Requirements[0])
	}
	if plan.Requirements[1].MetricID != "metric.concurrency.request" || plan.Requirements[1].UpperBound != 1 {
		t.Fatalf("request must reserve one concurrency slot: %+v", plan.Requirements[1])
	}
	executionPlan := domain.ExecutionPlan{
		ProviderTypeID: deepseek.ProviderTypeID, AdapterRevision: deepseek.AdapterRevision,
		DomainRevision: deepseek.DomainRevision, ReportMappingRevision: deepseek.UsageMappingRevision,
		TargetModelID: plan.TargetModelID, ResponseModelIDs: plan.ResponseModelIDs,
		Endpoint: binding.Endpoint, CredentialRef: "secret-ref", CredentialRevision: "secret-r1",
		Requirements: plan.Requirements, Bounds: plan.Bounds,
	}
	if err := deepseek.ValidateExecutionPlan(input.Request, executionPlan); err != nil {
		t.Fatalf("planner output rejected by adapter's pre-dispatch validator: %v", err)
	}
	binding.ProviderExtensions = json.RawMessage(`{"model":"deepseek-flash","maxOutputTokens":32}`)
	if _, _, err := (DeepSeekTextPolicy{}).Build(context.Background(), input, offer, binding, account, resources, evidence); err == nil {
		t.Fatal("request output cap above binding capability must be rejected")
	}
}

func TestDeepSeekTextPolicyRejectsInsufficientEvidenceAndUnsafeTargets(t *testing.T) {
	const now = 1_800_000_000_000
	resources := []config.CompiledResource{
		{ResourceID: "balance", MetricID: "metric.usd.cents", Unit: "usd_cents", Kind: "counter", CommercialRole: "entitlement", Capacity: 30, EvidenceRefs: []domain.ID{"e-balance"}},
		{ResourceID: "slots", MetricID: "metric.concurrency.request", Unit: "slot", Kind: "occupancy", CommercialRole: "limit", Capacity: 1, EvidenceRefs: []domain.ID{"e-slots"}},
	}
	evidence := []config.EvidenceDef{
		{EvidenceID: "e-balance", Kind: "bound", SourceRef: "verified:balance", VerifiedAtMillis: now - 1, ExpiresAtMillis: now + 1_000, Payload: map[string]string{"bound": "30", "metricId": "metric.usd.cents", "enforcement": "provider balance"}},
		{EvidenceID: "e-slots", Kind: "hard_stop", SourceRef: "verified:concurrency", VerifiedAtMillis: now - 1, ExpiresAtMillis: now + 1_000, Payload: map[string]string{"bound": "1", "metricId": "metric.concurrency.request", "enforcement": "provider concurrency"}},
	}
	input := domain.ExecuteInput{Request: domain.DomainRequest{
		Domain: "ai/text", CapabilityProfile: "profile.chat.basic",
		Payload: []byte(`{"model":"deepseek-flash","messages":[{"role":"user","content":"hello"}],"max_tokens":64}`),
	}}
	offer := config.OfferDef{Domain: "ai/text", CapabilityProfile: "profile.chat.basic"}
	binding := config.BindingDef{BindingID: "binding-deepseek", Domain: "ai/text", Endpoint: "https://api.deepseek.com/chat/completions"}
	account := config.AccountDef{ProviderTypeID: deepseek.ProviderTypeID}
	if _, _, err := (DeepSeekTextPolicy{}).Build(context.Background(), input, offer, binding, account, resources, evidence); err == nil {
		t.Fatal("30 cents of verified capacity cannot cover the 31-cent conservative request bound")
	}
	binding.Endpoint = "https://attacker.example/chat/completions"
	resources[0].Capacity = 100
	evidence[0].Payload["bound"] = "100"
	if _, _, err := (DeepSeekTextPolicy{}).Build(context.Background(), input, offer, binding, account, resources, evidence); err == nil {
		t.Fatal("unregistered endpoint must be rejected")
	}
}
