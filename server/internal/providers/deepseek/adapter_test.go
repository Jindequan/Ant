package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
	"ant/server/internal/providers/chatcompletions"
)

type testClock domain.Millis

func (c testClock) Now() domain.Millis { return domain.Millis(c) }

func testAdapter(t *testing.T) (*Adapter, domain.ExecutionPlan) {
	t.Helper()
	descriptor := domain.ProviderDescriptor{TypeID: ProviderTypeID, AdapterRevision: AdapterRevision, SupportedDomains: []string{"ai/text"}}
	project := func(plan domain.ExecutionPlan, usage chatcompletions.Usage, at domain.Millis, requestID domain.ID) ([]domain.MetricReport, error) {
		total := domain.Units(usage.TotalTokens)
		prompt := domain.Units(usage.PromptTokens)
		completion := domain.Units(usage.CompletionTokens)
		hit := domain.Units(*usage.PromptCacheHitTokens)
		miss := domain.Units(*usage.PromptCacheMissTokens)
		cost := domain.Units(1)
		slot := domain.Units(0)
		values := map[domain.ID]*domain.Units{
			"metric.tokens.total": &total, "metric.tokens.prompt": &prompt,
			"metric.tokens.completion": &completion, "metric.tokens.cache_hit": &hit,
			"metric.tokens.cache_miss": &miss, "metric.usd.cents": &cost,
			"metric.concurrency.request": &slot,
		}
		reports := make([]domain.MetricReport, 0, len(plan.Requirements))
		for _, requirement := range plan.Requirements {
			actual, ok := values[requirement.MetricID]
			if !ok {
				return nil, chatcompletions.ErrUnsupportedMetric
			}
			chargedAt := at
			reports = append(reports, domain.MetricReport{MetricID: requirement.MetricID, Unit: requirement.Unit, Actual: actual, ChargedAt: &chargedAt, EvidenceRefs: []domain.ID{"deepseek-usage/" + requestID}})
		}
		return reports, nil
	}
	adapter, err := New(descriptor, testClock(100), func(_ domain.DomainRequest, _ domain.ExecutionPlan) error { return nil }, project)
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.ExecutionPlan{
		ProviderTypeID: ProviderTypeID, AdapterRevision: AdapterRevision,
		TargetModelID: SupportedModel, ResponseModelIDs: []domain.ID{SupportedModel},
		DomainRevision: DomainRevision, ReportMappingRevision: UsageMappingRevision,
		Endpoint: "https://api.deepseek.com/chat/completions", CredentialRef: "secret-ref", CredentialRevision: "secret-r1",
		Requirements: []domain.ResourceRequirement{
			{MetricID: "metric.tokens.total", Unit: "token", UpperBound: domain.Units(MaxContextTokens), EvidenceID: "e-total", ResourceIDs: []domain.ID{"r-total"}},
			{MetricID: "metric.tokens.prompt", Unit: "token", UpperBound: domain.Units(MaxContextTokens - 64), EvidenceID: "e-prompt", ResourceIDs: []domain.ID{"r-prompt"}},
			{MetricID: "metric.tokens.completion", Unit: "token", UpperBound: 64, EvidenceID: "e-completion", ResourceIDs: []domain.ID{"r-completion"}},
			{MetricID: "metric.usd.cents", Unit: "usd_cents", UpperBound: 100, EvidenceID: "e-cost", ResourceIDs: []domain.ID{"r-cost"}},
			{MetricID: "metric.concurrency.request", Unit: "slot", UpperBound: 1, EvidenceID: "e-slot", ResourceIDs: []domain.ID{"r-slot"}},
		},
	}
	for _, requirement := range plan.Requirements {
		plan.Bounds = append(plan.Bounds, domain.BoundEvidence{
			EvidenceID: requirement.EvidenceID, MetricID: requirement.MetricID, Unit: requirement.Unit,
			UpperBound: requirement.UpperBound, MappingRevision: UsageMappingRevision,
			EndpointRef: "binding", SourceRef: "fixture", VerifiedAt: 1, ExpiresAt: 200,
		})
	}
	return adapter, plan
}

func TestValidateRequestRequiresSupportedDeepSeekModelAndBoundedNonStreamingText(t *testing.T) {
	valid := domain.DomainRequest{Domain: "ai/text", Payload: json.RawMessage(`{"model":"deepseek-flash","messages":[{"role":"user","content":"hello"}],"max_tokens":64}`)}
	if err := ValidateRequest(valid); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	for name, raw := range map[string]string{
		"unknown model":      `{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hello"}],"max_tokens":64}`,
		"other output bound": `{"model":"deepseek-flash","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":64}`,
		"too large":          `{"model":"deepseek-flash","messages":[{"role":"user","content":"hello"}],"max_tokens":393217}`,
		"streaming":          `{"model":"deepseek-flash","messages":[{"role":"user","content":"hello"}],"max_tokens":64,"stream":true}`,
		"trailing data":      `{"model":"deepseek-flash","messages":[{"role":"user","content":"hello"}],"max_tokens":64} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateRequest(domain.DomainRequest{Domain: "ai/text", Payload: json.RawMessage(raw)}); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("ValidateRequest()=%v, want ErrUnsupported", err)
			}
		})
	}
}

func TestEncodePinsDeepSeekEndpointAndEnforcesOutputRequirement(t *testing.T) {
	adapter, plan := testAdapter(t)
	request := domain.DomainRequest{Domain: "ai/text", Payload: json.RawMessage(`{"model":"deepseek-flash","messages":[{"role":"user","content":"hello"}],"max_tokens":64}`)}
	encoded, err := adapter.Encode(request, plan)
	if err != nil {
		t.Fatal(err)
	}
	if encoded.Endpoint != plan.Endpoint || encoded.CredentialRef != plan.CredentialRef || encoded.CredentialPrefix != "Bearer " {
		t.Fatalf("encoded binding mismatch: %+v", encoded)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded.Body, &body); err != nil || body["stream"] != false {
		t.Fatalf("wire request=%s decodeErr=%v", encoded.Body, err)
	}

	plan.Endpoint = "https://attacker.example/chat/completions"
	if _, err := adapter.Encode(request, plan); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("untrusted endpoint error=%v", err)
	}
	plan.Endpoint = "https://api.deepseek.com/chat/completions"
	plan.ResponseModelIDs = append(plan.ResponseModelIDs, "unexpected-model")
	if _, err := adapter.Encode(request, plan); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unexpected response model error=%v", err)
	}
	plan.ResponseModelIDs = plan.ResponseModelIDs[:1]
	plan.Requirements = plan.Requirements[:2]
	if _, err := adapter.Encode(request, plan); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("missing output bound error=%v", err)
	}
}

func TestEncodeRejectsPreparedPlanFromDifferentUsageMappingRevision(t *testing.T) {
	adapter, plan := testAdapter(t)
	request := domain.DomainRequest{Domain: "ai/text", Payload: json.RawMessage(`{"model":"deepseek-flash","messages":[{"role":"user","content":"hello"}],"max_tokens":64}`)}
	plan.ReportMappingRevision = "usage-mapping-previous"
	if _, err := adapter.Encode(request, plan); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("old usage mapping plan error=%v, want fail closed before dispatch preparation", err)
	}
	plan.ReportMappingRevision = UsageMappingRevision
	plan.Bounds[0].MappingRevision = "usage-mapping-previous"
	if _, err := adapter.Encode(request, plan); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("old bound mapping revision error=%v, want fail closed before dispatch preparation", err)
	}
}

func TestDecodeRequiresExactCacheBreakdownAndPreservesUsage(t *testing.T) {
	adapter, plan := testAdapter(t)
	response := `{"id":"request-1","object":"chat.completion","created":100,"model":"deepseek-flash","choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning_content":"hidden reasoning"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":2},"prompt_cache_hit_tokens":2,"prompt_cache_miss_tokens":5,"completion_tokens_details":{"reasoning_tokens":1}}}`
	var saved string
	report, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(response))}, plan, "attempt-1", func(data []byte) error { saved = string(data); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if saved != "answer" || report.Outcome != "known_success" || report.ProviderRequestID != "request-1" || len(report.Metrics) != len(plan.Requirements) {
		t.Fatalf("saved=%q report=%+v", saved, report)
	}
	for index, metric := range report.Metrics {
		if metric.Actual == nil && metric.Unit != "slot" {
			t.Fatalf("metric %d lacks exact usage: %+v", index, metric)
		}
	}
	missingBreakdown := strings.ReplaceAll(response, `,"prompt_cache_miss_tokens":5`, "")
	if _, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(missingBreakdown))}, plan, "attempt-2", func([]byte) error { return nil }); !errors.Is(err, chatcompletions.ErrInvalidResponse) {
		t.Fatalf("missing documented price breakdown error=%v", err)
	}
	optionalCacheDetail := strings.ReplaceAll(response, `"cached_tokens":2,`, "")
	if _, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(optionalCacheDetail))}, plan, "attempt-3", func([]byte) error { return nil }); err != nil {
		t.Fatalf("optional cached token summary rejected despite exact hit/miss fields: %v", err)
	}
}

func TestDeepSeekFinishReasonsDoNotPromoteInterruptedCompletionsToSuccess(t *testing.T) {
	for _, tc := range []struct{ reason, outcome string }{
		{"stop", "known_success"}, {"length", "known_failure"},
		{"content_filter", "known_failure"}, {"insufficient_system_resource", "known_failure"}, {"aborted", "unknown"},
	} {
		classification, err := classify(tc.reason, false)
		if err != nil || classification.Outcome != tc.outcome {
			t.Errorf("classify(%q)=%+v,%v want %s", tc.reason, classification, err, tc.outcome)
		}
	}
}
