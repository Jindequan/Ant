package deepseek

import (
	"errors"
	"math"

	"ant/server/internal/domain"
	"ant/server/internal/domain/metering"
	"ant/server/internal/providers/chatcompletions"
)

var ErrInvalidUsageForPricing = errors.New("deepseek pricing: invalid usage")

const (
	// Prices are represented as tenths of a USD cent per million tokens.
	// These are the integration's frozen peak-rate snapshot. Real account
	// admission must verify that this mapping still bounds the provider tariff.
	peakCacheHitDeciCentsPerMillion  int64 = 6
	peakCacheMissDeciCentsPerMillion int64 = 300
	peakOutputDeciCentsPerMillion    int64 = 1200
	deciCentsPerMillionTokens        int64 = 10_000_000
)

// PeakPriceUpperBoundCents returns a conservative USD-cent upper bound for
// one completed DeepSeek request. It prices every reported token at the
// documented peak rate, so it does not need a local Chinese holiday calendar
// or assume that the request ran off-peak. The result rounds up to a whole
// cent because the resource ledger uses integer USD cents.
func PeakPriceUpperBoundCents(usage chatcompletions.Usage) (int64, error) {
	if usage.PromptTokens < 0 || usage.CompletionTokens < 0 || usage.TotalTokens < 0 ||
		usage.PromptCacheHitTokens == nil || usage.PromptCacheMissTokens == nil ||
		*usage.PromptCacheHitTokens < 0 || *usage.PromptCacheMissTokens < 0 ||
		*usage.PromptCacheHitTokens > usage.PromptTokens || *usage.PromptCacheMissTokens > usage.PromptTokens {
		return 0, ErrInvalidUsageForPricing
	}
	if *usage.PromptCacheHitTokens > math.MaxInt64-*usage.PromptCacheMissTokens ||
		*usage.PromptCacheHitTokens+*usage.PromptCacheMissTokens != usage.PromptTokens ||
		usage.PromptTokens > math.MaxInt64-usage.CompletionTokens ||
		usage.PromptTokens+usage.CompletionTokens != usage.TotalTokens {
		return 0, ErrInvalidUsageForPricing
	}

	return priceCents(
		*usage.PromptCacheHitTokens,
		*usage.PromptCacheMissTokens,
		usage.CompletionTokens,
	)
}

// PeakWorstCasePriceCents bounds an individual request using an input token
// ceiling and requested output ceiling. It assumes every input token misses
// cache, then prices both dimensions at the documented peak rate.
func PeakWorstCasePriceCents(maxPromptTokens, maxOutputTokens int64) (int64, error) {
	if maxPromptTokens < 0 || maxOutputTokens < 0 {
		return 0, ErrInvalidUsageForPricing
	}
	return priceCents(0, maxPromptTokens, maxOutputTokens)
}

func priceCents(cacheHitTokens, cacheMissTokens, outputTokens int64) (int64, error) {
	if peakMeteringErr != nil {
		return 0, ErrInvalidUsageForPricing
	}
	values, err := peakMetering.Evaluate([]metering.Dimension{{MetricID: "metric.usd.cents", Unit: "usd_cents"}}, priceSamples(cacheHitTokens, cacheMissTokens, outputTokens))
	if err != nil {
		return 0, ErrInvalidUsageForPricing
	}
	cents, err := domain.ParseUnits(values[0].Decimal)
	if err != nil {
		return 0, ErrInvalidUsageForPricing
	}
	return int64(cents), nil
}

// One compiled mapping serves both request bounds and response accounting.
// Prices are the existing frozen integration rates, not live billing facts.
var peakMetering, peakMeteringErr = metering.New(metering.Definition{
	ID: "deepseek-peak", Revision: UsageMappingRevision,
	Formulas: []metering.Formula{
		{Output: metering.Dimension{MetricID: "metric.usd.cents", Unit: "usd_cents"}, Rounding: metering.Ceiling, Conservative: true, Terms: []metering.Term{
			{Input: metering.Dimension{MetricID: "cache.hit", Unit: "token"}, Numerator: domain.Units(peakCacheHitDeciCentsPerMillion), Denominator: domain.Units(deciCentsPerMillionTokens)},
			{Input: metering.Dimension{MetricID: "cache.miss", Unit: "token"}, Numerator: domain.Units(peakCacheMissDeciCentsPerMillion), Denominator: domain.Units(deciCentsPerMillionTokens)},
			{Input: metering.Dimension{MetricID: "completion", Unit: "token"}, Numerator: domain.Units(peakOutputDeciCentsPerMillion), Denominator: domain.Units(deciCentsPerMillionTokens)},
		}},
		{Output: metering.Dimension{MetricID: "metric.tokens.total", Unit: "token"}, Rounding: metering.Exact, Terms: []metering.Term{{Input: metering.Dimension{MetricID: "total", Unit: "token"}, Numerator: 1, Denominator: 1}}},
		{Output: metering.Dimension{MetricID: "metric.tokens.prompt", Unit: "token"}, Rounding: metering.Exact, Terms: []metering.Term{{Input: metering.Dimension{MetricID: "prompt", Unit: "token"}, Numerator: 1, Denominator: 1}}},
		{Output: metering.Dimension{MetricID: "metric.tokens.completion", Unit: "token"}, Rounding: metering.Exact, Terms: []metering.Term{{Input: metering.Dimension{MetricID: "completion", Unit: "token"}, Numerator: 1, Denominator: 1}}},
		{Output: metering.Dimension{MetricID: "output_tokens", Unit: "token"}, Rounding: metering.Exact, Terms: []metering.Term{{Input: metering.Dimension{MetricID: "completion", Unit: "token"}, Numerator: 1, Denominator: 1}}},
		{Output: metering.Dimension{MetricID: "metric.concurrency.request", Unit: "slot"}, Rounding: metering.Exact},
	},
})

func priceSamples(hit, miss, output int64) []metering.Sample {
	return []metering.Sample{
		{Dimension: metering.Dimension{MetricID: "cache.hit", Unit: "token"}, Amount: domain.Units(hit)},
		{Dimension: metering.Dimension{MetricID: "cache.miss", Unit: "token"}, Amount: domain.Units(miss)},
		{Dimension: metering.Dimension{MetricID: "completion", Unit: "token"}, Amount: domain.Units(output)},
	}
}

// PeakUsageProjector maps DeepSeek's exact token counters onto the supported
// ai/text accounting metrics. The monetary amount is computed at peak rates
// and explicitly marked conservative because the completion response does
// not include the currency amount actually billed by DeepSeek.
func PeakUsageProjector(plan domain.ExecutionPlan, usage chatcompletions.Usage, at domain.Millis, requestID domain.ID) ([]domain.MetricReport, error) {
	if requestID == "" || at < 0 {
		return nil, ErrInvalidUsageForPricing
	}
	_, err := PeakPriceUpperBoundCents(usage)
	if err != nil {
		return nil, err
	}
	evidence := domain.ID("deepseek-usage/" + string(requestID))
	samples := priceSamples(*usage.PromptCacheHitTokens, *usage.PromptCacheMissTokens, usage.CompletionTokens)
	samples = append(samples,
		metering.Sample{Dimension: metering.Dimension{MetricID: "total", Unit: "token"}, Amount: domain.Units(usage.TotalTokens)},
		metering.Sample{Dimension: metering.Dimension{MetricID: "prompt", Unit: "token"}, Amount: domain.Units(usage.PromptTokens)})
	reports, err := peakMetering.Project(plan, samples, at, []domain.ID{evidence})
	if err != nil {
		return nil, ErrInvalidUsageForPricing
	}
	return reports, nil
}
