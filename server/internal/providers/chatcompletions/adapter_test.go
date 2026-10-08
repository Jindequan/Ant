package chatcompletions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
	"ant/server/internal/domain/metering"
)

type fixedClock domain.Millis

func (c fixedClock) Now() domain.Millis { return domain.Millis(c) }

func fixturePlanValidator(request domain.DomainRequest, plan domain.ExecutionPlan) error {
	if err := ValidateRequest(request); err != nil {
		return err
	}
	var requested chatRequest
	if err := json.Unmarshal(request.Payload, &requested); err != nil {
		return ErrInvalidRequest
	}
	outputLimit := requested.MaxTokens
	if outputLimit == nil {
		outputLimit = requested.MaxCompletionTokens
	}
	for _, requirement := range plan.Requirements {
		switch {
		case isTotalMetric(requirement.MetricID, requirement.Unit), isPromptMetric(requirement.MetricID, requirement.Unit):
		case isCompletionMetric(requirement.MetricID, requirement.Unit):
			if domain.Units(*outputLimit) > requirement.UpperBound {
				return ErrInvalidRequest
			}
		case requirement.Unit == "slot":
		default:
			return ErrUnsupportedMetric
		}
	}
	return nil
}

func fixtureUsageProjector(plan domain.ExecutionPlan, usage Usage, at domain.Millis, requestID domain.ID) ([]domain.MetricReport, error) {
	prompt := domain.Units(usage.PromptTokens)
	completion := domain.Units(usage.CompletionTokens)
	total := domain.Units(usage.TotalTokens)
	evidence := domain.ID("fixture-usage/" + string(requestID))
	metrics := make([]domain.MetricReport, 0, len(plan.Requirements))
	for _, requirement := range plan.Requirements {
		var actual *domain.Units
		switch {
		case isTotalMetric(requirement.MetricID, requirement.Unit):
			actual = &total
		case isPromptMetric(requirement.MetricID, requirement.Unit):
			actual = &prompt
		case isCompletionMetric(requirement.MetricID, requirement.Unit):
			actual = &completion
		case requirement.Unit == "slot":
		default:
			return nil, ErrUnsupportedMetric
		}
		chargedAt := at
		metrics = append(metrics, domain.MetricReport{MetricID: requirement.MetricID, Unit: requirement.Unit, Actual: actual, ChargedAt: &chargedAt, EvidenceRefs: []domain.ID{evidence}})
	}
	return metrics, nil
}

func fixtureClassifier(finishReason string, refused bool) (ResultClassification, error) {
	switch {
	case refused:
		return ResultClassification{Outcome: "known_failure", Reasons: []domain.Reason{{Group: "domain", Code: "response_refused"}}}, nil
	case finishReason == "stop":
		return ResultClassification{Outcome: "known_success"}, nil
	case finishReason == "length":
		return ResultClassification{Outcome: "known_failure", Reasons: []domain.Reason{{Group: "domain", Code: "response_truncated"}}}, nil
	case finishReason == "content_filter":
		return ResultClassification{Outcome: "known_failure", Reasons: []domain.Reason{{Group: "domain", Code: "response_filtered"}}}, nil
	case finishReason == "insufficient_system_resource":
		return ResultClassification{Outcome: "known_failure", Reasons: []domain.Reason{{Group: "provider", Code: "insufficient_system_resource"}}}, nil
	case finishReason == "aborted":
		return ResultClassification{Outcome: "unknown", Reasons: []domain.Reason{{Group: "provider", Code: "completion_aborted"}}}, nil
	default:
		return ResultClassification{}, ErrInvalidResponse
	}
}

func isTotalMetric(metric domain.ID, unit string) bool {
	return unit == "token" && (metric == "tokens" || metric == "metric.tokens.total")
}
func isPromptMetric(metric domain.ID, unit string) bool {
	return unit == "token" && (metric == "metric.tokens.prompt" || metric == "input_tokens")
}
func isCompletionMetric(metric domain.ID, unit string) bool {
	return unit == "token" && (metric == "metric.tokens.completion" || metric == "output_tokens")
}

func fixtureAdapter(t *testing.T) (*Adapter, domain.ExecutionPlan) {
	t.Helper()
	descriptor := domain.ProviderDescriptor{TypeID: "provider-alpha", AdapterRevision: "chat-completions-v1", SupportedDomains: []string{"ai/text"}}
	adapter, err := New(descriptor, fixedClock(100), fixturePlanValidator, fixtureUsageProjector, fixtureClassifier)
	if err != nil {
		t.Fatal(err)
	}
	return adapter, domain.ExecutionPlan{
		ProviderTypeID: "provider-alpha", AdapterRevision: descriptor.AdapterRevision, TargetModelID: "model-x", ResponseModelIDs: []domain.ID{"model-x", "model-x-2026-01"},
		Endpoint: "https://fixture.invalid/v1/chat/completions", CredentialRef: "secret-ref", CredentialRevision: "secret-r1",
		Requirements: []domain.ResourceRequirement{
			{MetricID: "metric.tokens.total", Unit: "token", UpperBound: 1000},
			{MetricID: "metric.concurrency.request", Unit: "slot", UpperBound: 1},
		},
	}
}

func TestEncodePinsTargetCredentialAndDisablesStreaming(t *testing.T) {
	adapter, plan := fixtureAdapter(t)
	payload := json.RawMessage(`{"model":"model-x","messages":[{"role":"user","content":"hello"}],"max_tokens":64}`)
	encoded, err := adapter.Encode(domain.DomainRequest{Domain: "ai/text", CapabilityProfile: "profile.chat.basic", Payload: payload}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if encoded.Endpoint != plan.Endpoint || encoded.Method != "POST" || encoded.CredentialRef != plan.CredentialRef {
		t.Fatalf("encoded target or credential binding incorrect: %+v", encoded)
	}
	if encoded.CredentialHeader != "Authorization" || encoded.CredentialField != "value" || encoded.CredentialPrefix != "Bearer " {
		t.Fatalf("credential injection rule=%+v", encoded)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["stream"] != false || body["max_tokens"] != float64(64) {
		t.Fatalf("non-streaming request body=%s", encoded.Body)
	}
	if encoded.MaxResponseBytes != maxResponseBytes {
		t.Fatalf("max response bytes=%d", encoded.MaxResponseBytes)
	}
}

func TestDecodeOneUsageCounterForMultipleResourceWindows(t *testing.T) {
	base, plan := fixtureAdapter(t)
	plan.ReportMappingRevision = "window-mapping-v1"
	plan.Requirements = append(plan.Requirements, plan.Requirements[0])
	calculator, err := metering.New(metering.Definition{ID: "fixture-windows", Revision: plan.ReportMappingRevision, Formulas: []metering.Formula{
		{Output: metering.Dimension{MetricID: "metric.tokens.total", Unit: "token"}, Rounding: metering.Exact, Terms: []metering.Term{{Input: metering.Dimension{MetricID: "total", Unit: "token"}, Numerator: 1, Denominator: 1}}},
		{Output: metering.Dimension{MetricID: "metric.concurrency.request", Unit: "slot"}, Rounding: metering.Exact},
	}})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := New(base.Descriptor(), fixedClock(100), fixturePlanValidator, func(p domain.ExecutionPlan, usage Usage, at domain.Millis, requestID domain.ID) ([]domain.MetricReport, error) {
		return calculator.Project(p, []metering.Sample{{Dimension: metering.Dimension{MetricID: "total", Unit: "token"}, Amount: domain.Units(usage.TotalTokens)}}, at, []domain.ID{requestID})
	}, fixtureClassifier)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"id":"r","object":"chat.completion","model":"model-x","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
	writes := 0
	report, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(body))}, plan, "a1", func([]byte) error { writes++; return nil })
	if err != nil || len(report.Metrics) != 2 || *report.Metrics[0].Actual != 3 || writes != 1 {
		t.Fatalf("multi-window response rejected: %+v writes=%d err=%v", report, writes, err)
	}
	plan.Requirements[2].Unit = "second"
	writes = 0
	if _, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(body))}, plan, "a2", func([]byte) error { writes++; return nil }); err == nil || writes != 0 {
		t.Fatal("unit mismatch published output")
	}
}

func TestEncodeRejectsModelDifferentFromPlan(t *testing.T) {
	adapter, plan := fixtureAdapter(t)
	payload := json.RawMessage(`{"model":"model-y","messages":[{"role":"user","content":"hello"}],"max_tokens":64}`)
	if _, err := adapter.Encode(domain.DomainRequest{Domain: "ai/text", Payload: payload}, plan); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Encode error=%v, want model mismatch rejection", err)
	}
}

func TestEncodeSupportsTotalCompletionLimitAndReasoningEffort(t *testing.T) {
	adapter, plan := fixtureAdapter(t)
	payload := json.RawMessage(`{"model":"model-x","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":64,"reasoning_effort":"high"}`)
	encoded, err := adapter.Encode(domain.DomainRequest{Domain: "ai/text", Payload: payload}, plan)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["max_completion_tokens"] != float64(64) || body["max_tokens"] != nil || body["reasoning_effort"] != "high" {
		t.Fatalf("encoded total output bound/reasoning effort=%s", encoded.Body)
	}
	plan.Requirements = []domain.ResourceRequirement{{MetricID: "metric.tokens.completion", Unit: "token", UpperBound: 32}}
	if _, err := adapter.Encode(domain.DomainRequest{Domain: "ai/text", Payload: payload}, plan); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("request cap above the planned output bound error=%v, want invalid request", err)
	}

	for name, invalid := range map[string]string{
		"both output bounds":       `{"model":"model-x","messages":[{"role":"user","content":"hello"}],"max_tokens":64,"max_completion_tokens":64}`,
		"invalid reasoning effort": `{"model":"model-x","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":64,"reasoning_effort":"extreme"}`,
		"zero output bound":        `{"model":"model-x","messages":[{"role":"user","content":"hello"}],"max_completion_tokens":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateRequest(domain.DomainRequest{Domain: "ai/text", Payload: json.RawMessage(invalid)}); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("ValidateRequest() error=%v, want invalid request", err)
			}
		})
	}
}

func TestEncodeRejectsStreamingUnknownFieldsAndUnaccountedMetrics(t *testing.T) {
	adapter, plan := fixtureAdapter(t)
	base := `"model":"model-x","messages":[{"role":"user","content":"hello"}],"max_tokens":64`
	for name, payload := range map[string]string{
		"stream":        `{` + base + `,"stream":true}`,
		"tools":         `{` + base + `,"tools":[{}]}`,
		"multiple":      `{` + base + `,"n":2}`,
		"missing limit": `{"model":"model-x","messages":[{"role":"user","content":"hello"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := adapter.Encode(domain.DomainRequest{Domain: "ai/text", Payload: json.RawMessage(payload)}, plan)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Encode error=%v, want invalid request", err)
			}
		})
	}
	plan.Requirements = append(plan.Requirements, domain.ResourceRequirement{MetricID: "metric.usd.cents", Unit: "usd_cents", UpperBound: 50})
	_, err := adapter.Encode(domain.DomainRequest{Domain: "ai/text", Payload: json.RawMessage(`{` + base + `}`)}, plan)
	if !errors.Is(err, ErrUnsupportedMetric) {
		t.Fatalf("unmapped cost metric error=%v, want fail-closed rejection before dispatch", err)
	}
	plan.Requirements = []domain.ResourceRequirement{{MetricID: "metric.tokens.completion", Unit: "token", UpperBound: 10}}
	_, err = adapter.Encode(domain.DomainRequest{Domain: "ai/text", Payload: json.RawMessage(`{` + base + `}`)}, plan)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("completion cap beyond configured bound error=%v, want invalid request", err)
	}
}

func TestDecodeProjectsCompleteUsageAndPersistsOnlyAnswer(t *testing.T) {
	adapter, plan := fixtureAdapter(t)
	var saved string
	report, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(`{
		"id":"req-123","object":"chat.completion","created":100,"model":"model-x",
		"choices":[{"index":0,"message":{"role":"assistant","content":"hello back"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10,"prompt_tokens_details":{"cached_tokens":0}}
	}`))}, plan, "attempt-1", func(data []byte) error { saved = string(data); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if saved != "hello back" || report.AttemptID != "attempt-1" || report.ProviderRequestID != "req-123" || report.Outcome != "known_success" || !report.ExternalEnded {
		t.Fatalf("saved=%q report=%+v", saved, report)
	}
	if len(report.Metrics) != 2 || report.Metrics[0].Actual == nil || *report.Metrics[0].Actual != 10 || report.Metrics[0].ChargedAt == nil || *report.Metrics[0].ChargedAt != 100 || report.Metrics[1].Actual != nil {
		t.Fatalf("metric projection=%+v", report.Metrics)
	}
	if len(report.EndEvidenceRefs) != 1 || len(report.Metrics[0].EvidenceRefs) != 1 {
		t.Fatalf("missing response/usage evidence: %+v", report)
	}
}

func TestDecodeRejectsModelDifferentFromPlan(t *testing.T) {
	adapter, plan := fixtureAdapter(t)
	body := `{"id":"req-123","object":"chat.completion","model":"model-y","choices":[{"index":0,"message":{"role":"assistant","content":"hello back"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`
	if _, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(body))}, plan, "attempt-1", func([]byte) error { return nil }); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("Decode error=%v, want model mismatch rejection", err)
	}
}

func TestDecodeAcceptsPlanApprovedResolvedModelID(t *testing.T) {
	adapter, plan := fixtureAdapter(t)
	body := `{"id":"req-123","object":"chat.completion","model":"model-x-2026-01","choices":[{"index":0,"message":{"role":"assistant","content":"hello back"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`
	if _, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(body))}, plan, "attempt-1", func([]byte) error { return nil }); err != nil {
		t.Fatalf("Decode rejected approved resolved model ID: %v", err)
	}
}

func TestDecodePreservesAndCrossChecksProviderCacheBreakdown(t *testing.T) {
	descriptor := domain.ProviderDescriptor{TypeID: "provider-alpha", AdapterRevision: "chat-completions-v1", SupportedDomains: []string{"ai/text"}}
	var got Usage
	adapter, err := New(descriptor, fixedClock(100), fixturePlanValidator,
		func(plan domain.ExecutionPlan, usage Usage, at domain.Millis, requestID domain.ID) ([]domain.MetricReport, error) {
			got = usage
			return fixtureUsageProjector(plan, usage, at, requestID)
		}, fixtureClassifier)
	if err != nil {
		t.Fatal(err)
	}
	_, plan := fixtureAdapter(t)
	valid := `{"id":"r","object":"chat.completion","model":"model-x","choices":[{"index":0,"message":{"role":"assistant","content":"x"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10,"prompt_cache_hit_tokens":2,"prompt_cache_miss_tokens":5,"prompt_tokens_details":{"cached_tokens":2}}}`
	if _, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(valid))}, plan, "attempt-cache", func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got.PromptCacheHitTokens == nil || *got.PromptCacheHitTokens != 2 || got.PromptCacheMissTokens == nil || *got.PromptCacheMissTokens != 5 || got.CachedPromptTokens == nil || *got.CachedPromptTokens != 2 {
		t.Fatalf("cache usage detail lost: %+v", got)
	}

	for name, usage := range map[string]string{
		"only hit":              `"prompt_cache_hit_tokens":2`,
		"parts do not sum":      `"prompt_cache_hit_tokens":2,"prompt_cache_miss_tokens":4`,
		"breakdown contradicts": `"prompt_cache_hit_tokens":2,"prompt_cache_miss_tokens":5,"prompt_tokens_details":{"cached_tokens":1}`,
		"negative miss":         `"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":-1`,
	} {
		t.Run(name, func(t *testing.T) {
			body := `{"id":"r","object":"chat.completion","model":"model-x","choices":[{"index":0,"message":{"role":"assistant","content":"x"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10,` + usage + `}}`
			if _, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(body))}, plan, "attempt-cache", func([]byte) error { return nil }); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("Decode error=%v, want invalid response", err)
			}
		})
	}
}

func TestDecodeRejectsMissingOrInconsistentUsageAndUnsupportedOutcome(t *testing.T) {
	adapter, plan := fixtureAdapter(t)
	for name, body := range map[string]string{
		"missing total":      `{"id":"r","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"x"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
		"inconsistent total": `{"id":"r","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"x"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":3}}`,
		"tool outcome":       `{"id":"r","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(body))}, plan, "attempt-1", func([]byte) error { return nil })
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("Decode error=%v, want invalid response", err)
			}
		})
	}
}

func TestDecodeClassifiesLengthAsFailureAndNeverBusinessSuccess(t *testing.T) {
	adapter, plan := fixtureAdapter(t)
	body := `{"id":"r","object":"chat.completion","model":"model-x","choices":[{"index":0,"message":{"role":"assistant","content":"partial"},"finish_reason":"length"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
	report, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(body))}, plan, "attempt-1", func([]byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "known_failure" || report.ExternalEnded != true || len(report.Reasons) != 1 || report.Reasons[0].Code != "response_truncated" {
		t.Fatalf("truncated output classification=%+v", report)
	}
}

func TestDecodePreservesUsageForCompletedProviderInterruption(t *testing.T) {
	adapter, plan := fixtureAdapter(t)
	for _, tc := range []struct {
		finishReason string
		outcome      string
	}{
		{finishReason: "aborted", outcome: "unknown"},
		{finishReason: "insufficient_system_resource", outcome: "known_failure"},
	} {
		t.Run(tc.finishReason, func(t *testing.T) {
			requestID := "r-" + tc.finishReason
			body := `{"id":"` + requestID + `","object":"chat.completion","model":"model-x","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"` + tc.finishReason + `"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`
			outputWritten := false
			report, err := adapter.Decode(context.Background(), adapters.RawResponse{Status: 200, Body: io.NopCloser(strings.NewReader(body))}, plan, "attempt-aborted", func([]byte) error {
				outputWritten = true
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if outputWritten || report.Outcome != tc.outcome || !report.ExternalEnded || report.ProviderRequestID != domain.ID(requestID) || len(report.Metrics) != 2 || report.Metrics[0].Actual == nil || *report.Metrics[0].Actual != 7 {
				t.Fatalf("interrupted completion lost its terminal usage evidence: outputWritten=%v report=%+v", outputWritten, report)
			}
		})
	}
}
