package application

import (
	"context"
	"testing"

	"ant/server/internal/config"
	"ant/server/internal/domain"
)

func TestLocalTestTextPolicyBindsAllFixtureDimensionsAndCapsMaximumRequest(t *testing.T) {
	const now = 100
	order := int64(10)
	resources := []config.CompiledResource{
		{ResourceID: "tokens", MetricID: "metric.tokens.total", Unit: "token", Kind: "counter", CommercialRole: "limit", Capacity: 500_000, EvidenceRefs: []domain.ID{"e-tokens"}},
		{ResourceID: "cost", MetricID: "metric.usd.cents", Unit: "usd_cents", Kind: "counter", CommercialRole: "entitlement", Capacity: 500, ProviderOrder: &order, EntitlementGroupID: "cost-group", EvidenceRefs: []domain.ID{"e-cost"}},
		{ResourceID: "slots", MetricID: "metric.concurrency.request", Unit: "slot", Kind: "occupancy", CommercialRole: "limit", Capacity: 1, EvidenceRefs: []domain.ID{"e-slots"}},
	}
	evidence := []config.EvidenceDef{
		{EvidenceID: "e-tokens", Kind: "bound", SourceRef: "fixture:tokens", VerifiedAtMillis: now - 1, ExpiresAtMillis: now + 1_000, Payload: map[string]string{"bound": "500000"}},
		{EvidenceID: "e-cost", Kind: "bound", SourceRef: "fixture:cost", VerifiedAtMillis: now - 1, ExpiresAtMillis: now + 1_000, Payload: map[string]string{"bound": "500"}},
		{EvidenceID: "e-slots", Kind: "bound", SourceRef: "fixture:slots", VerifiedAtMillis: now - 1, ExpiresAtMillis: now + 1_000, Payload: map[string]string{"bound": "1"}},
	}
	input := domain.ExecuteInput{Request: domain.DomainRequest{Domain: "ai/text", CapabilityProfile: "profile.chat.basic", Payload: []byte(`{"model":"fixture-model","messages":[{"role":"user","content":"hi"}],"max_tokens":128}`)}}
	offer := config.OfferDef{Domain: "ai/text", CapabilityProfile: "profile.chat.basic"}
	binding := config.BindingDef{Domain: "ai/text", BindingID: "binding-1", Endpoint: "http://127.0.0.1:8080/v1/localtest/chat/completions"}
	account := config.AccountDef{ProviderTypeID: "provider-alpha"}
	plan, reasons, err := (LocalTestTextPolicy{}).Build(context.Background(), input, offer, binding, account, resources, evidence)
	if err != nil || len(reasons) != 0 {
		t.Fatalf("plan err=%v reasons=%v", err, reasons)
	}
	if len(plan.Requirements) != 3 || len(plan.Bounds) != 3 || plan.TargetModelID != "fixture-model" || plan.ReportMappingRevision != localTestMappingRevision {
		t.Fatalf("plan did not bind all dimensions and model: %+v", plan)
	}
	if plan.Requirements[0].Mode != "all_constraints" || plan.Requirements[1].Mode != "provider_entitlement_waterfall" {
		t.Fatalf("token limit and spend entitlement must use their ledger reservation modes: %+v", plan.Requirements)
	}

	short := append([]config.CompiledResource(nil), resources...)
	short[0].Capacity = 100
	shortEvidence := append([]config.EvidenceDef(nil), evidence...)
	shortEvidence[0].Payload = map[string]string{"bound": "100"}
	if _, _, err := (LocalTestTextPolicy{}).Build(context.Background(), input, offer, binding, account, short, shortEvidence); err == nil {
		t.Fatal("request whose maximum output exceeds token capacity was admitted")
	}

	duplicate := append(append([]config.CompiledResource(nil), resources...), resources[0])
	if _, _, err := (LocalTestTextPolicy{}).Build(context.Background(), input, offer, binding, account, duplicate, evidence); err == nil {
		t.Fatal("ambiguous token dimension was admitted")
	}
}
