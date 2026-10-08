package deepseek

import (
	"errors"
	"math"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/providers/chatcompletions"
)

func TestPeakPriceUpperBoundCents(t *testing.T) {
	tests := []struct {
		name  string
		usage chatcompletions.Usage
		want  int64
	}{
		{
			name:  "cache hit rounds up to ledger cent",
			usage: deepSeekUsage(1_000_000, 0, 1_000_000, 1_000_000, 0),
			want:  1,
		},
		{
			name:  "cache miss peak rate",
			usage: deepSeekUsage(1_000_000, 0, 1_000_000, 0, 1_000_000),
			want:  30,
		},
		{
			name:  "output peak rate",
			usage: deepSeekUsage(0, 1_000_000, 1_000_000, 0, 0),
			want:  120,
		},
		{
			name:  "mixed usage rounds the whole request up once",
			usage: deepSeekUsage(2_000_000, 384_000, 2_384_000, 1_000_000, 1_000_000),
			want:  77,
		},
		{
			name:  "empty usage costs zero",
			usage: deepSeekUsage(0, 0, 0, 0, 0),
			want:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PeakPriceUpperBoundCents(tt.usage)
			if err != nil || got != tt.want {
				t.Fatalf("PeakPriceUpperBoundCents() = %d, %v; want %d", got, err, tt.want)
			}
		})
	}
}

func TestPeakWorstCasePriceCoversMaximumContextAndOutput(t *testing.T) {
	maxPrompt := MaxContextTokens - MaxOutputTokens
	got, err := PeakWorstCasePriceCents(maxPrompt, MaxOutputTokens)
	if err != nil || got != 66 {
		t.Fatalf("maximum-context peak price = %d, %v; want 66 cents", got, err)
	}
}

func TestPeakPricingDoesNotRejectRepresentableFinalChargeAfterLargeProduct(t *testing.T) {
	// The intermediate token×rate product exceeds int64, but its final
	// cents quotient fits. Pricing must divide exactly before range checking.
	got, err := PeakWorstCasePriceCents(0, math.MaxInt64)
	if err != nil || got != 1106804644422574 {
		t.Fatalf("large product: cents=%d err=%v", got, err)
	}
}

func TestPeakPriceUpperBoundRejectsIncompleteOrInconsistentUsage(t *testing.T) {
	valid := deepSeekUsage(10, 5, 15, 7, 3)
	tests := map[string]chatcompletions.Usage{
		"missing cache hit":         {PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, PromptCacheMissTokens: int64Ptr(10)},
		"missing cache miss":        {PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, PromptCacheHitTokens: int64Ptr(10)},
		"cache counters do not sum": deepSeekUsage(10, 5, 3, 6, 5),
		"total does not sum":        deepSeekUsage(10, 5, 3, 7, 4),
		"negative counter":          deepSeekUsage(10, 5, 3, 12, -2),
		"negative prompt":           deepSeekUsage(-1, 5, 4, 1, 0),
	}
	for name, usage := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := PeakPriceUpperBoundCents(usage); !errors.Is(err, ErrInvalidUsageForPricing) {
				t.Fatalf("PeakPriceUpperBoundCents() error = %v", err)
			}
		})
	}
	if _, err := PeakPriceUpperBoundCents(valid); err != nil {
		t.Fatalf("valid usage rejected: %v", err)
	}
}

func TestPeakUsageProjectorMarksPriceConservative(t *testing.T) {
	usage := deepSeekUsage(10, 5, 15, 7, 3)
	plan := domain.ExecutionPlan{ReportMappingRevision: UsageMappingRevision, Requirements: []domain.ResourceRequirement{
		{MetricID: "metric.tokens.total", Unit: "token"},
		{MetricID: "metric.usd.cents", Unit: "usd_cents"},
		{MetricID: "metric.concurrency.request", Unit: "slot"},
	}}
	reports, err := PeakUsageProjector(plan, usage, 1234, "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 3 {
		t.Fatalf("got %d reports; want 3", len(reports))
	}
	if reports[0].Actual == nil || *reports[0].Actual != 15 || reports[0].Conservative {
		t.Fatalf("token report = %+v", reports[0])
	}
	if reports[1].Actual == nil || *reports[1].Actual != 1 || !reports[1].Conservative || reports[1].Unit != "usd_cents" {
		t.Fatalf("peak-price report must be a conservative USD-cent amount: %+v", reports[1])
	}
	if reports[2].Actual == nil || *reports[2].Actual != 0 || reports[2].Conservative {
		t.Fatalf("occupancy report = %+v", reports[2])
	}
	for _, report := range reports {
		if report.ChargedAt == nil || *report.ChargedAt != 1234 || len(report.EvidenceRefs) != 1 || report.EvidenceRefs[0] != "deepseek-usage/request-1" {
			t.Fatalf("report lost request attribution: %+v", report)
		}
	}
}

func deepSeekUsage(prompt, completion, total, hit, miss int64) chatcompletions.Usage {
	return chatcompletions.Usage{
		PromptTokens: prompt, CompletionTokens: completion, TotalTokens: total,
		PromptCacheHitTokens: int64Ptr(hit), PromptCacheMissTokens: int64Ptr(miss),
	}
}

func int64Ptr(value int64) *int64 { return &value }
