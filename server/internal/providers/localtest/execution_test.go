package localtest

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
)

func TestLocalAdapterProjectsOnlySyntheticFixtureDimensions(t *testing.T) {
	catalog, err := New()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, ok := catalog.Descriptor("provider-alpha")
	if !ok {
		t.Fatal("localtest provider descriptor missing")
	}
	adapter, err := NewExecutionAdapter(descriptor, fixedTestClock(100))
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.ExecutionPlan{
		ProviderTypeID: "provider-alpha", AdapterRevision: descriptor.AdapterRevision,
		DomainRevision: localDomainRevision, ReportMappingRevision: localMappingRevision,
		TargetModelID: "fixture", ResponseModelIDs: []domain.ID{"fixture"}, Endpoint: "http://127.0.0.1:8080/v1/localtest/chat/completions",
		Requirements: []domain.ResourceRequirement{
			{MetricID: "metric.tokens.total", Unit: "token", UpperBound: 10},
			{MetricID: "metric.usd.cents", Unit: "usd_cents", UpperBound: 1},
			{MetricID: "metric.concurrency.request", Unit: "slot", UpperBound: 1},
		},
	}
	body := `{"id":"localtest-response-test","object":"chat.completion","created":1,"model":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"Local test reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
	report, err := adapter.Decode(t.Context(), adapters.RawResponse{Status: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, plan, "attempt-1", func([]byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != "known_success" || len(report.Metrics) != 3 || report.RetrySafety != domain.Unsafe {
		t.Fatalf("unexpected fixture report: %+v", report)
	}
	got := map[domain.ID]domain.Units{}
	for _, metric := range report.Metrics {
		if metric.Actual == nil {
			t.Fatalf("fixture metric has no exact actual: %+v", metric)
		}
		got[metric.MetricID] = *metric.Actual
	}
	if got["metric.tokens.total"] != 3 || got["metric.usd.cents"] != 1 || got["metric.concurrency.request"] != 0 {
		t.Fatalf("unexpected synthetic metrics: %+v", got)
	}
}

type fixedTestClock int64

func (c fixedTestClock) Now() domain.Millis { return domain.Millis(c) }
