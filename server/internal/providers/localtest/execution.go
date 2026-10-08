package localtest

// This package provides an explicitly simulated Chat Completions endpoint for
// local development. Its synthetic usage and charge values must never be used
// to represent a real provider account or to admit production traffic.

import (
	"encoding/json"
	"net"
	"net/url"

	"ant/server/internal/domain"
	"ant/server/internal/providers/chatcompletions"
)

const (
	LocalTextDomain       = "ai/text"
	LocalTextProfile      = "profile.chat.basic"
	localDomainRevision   = "localtest-ai-text-v1"
	localMappingRevision  = "localtest-usage-v1"
	localOutputTokenLimit = 8192
)

type localRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	MaxTokens           *int64 `json:"max_tokens"`
	MaxCompletionTokens *int64 `json:"max_completion_tokens"`
}

// NewExecutionAdapter builds the local fixture adapter. Its unit charge is a
// stable synthetic 1 cent per 10,000 simulated tokens (rounded up), solely so
// local ledger flows exercise both token and entitlement dimensions.
func NewExecutionAdapter(descriptor domain.ProviderDescriptor, clock chatcompletions.Clock) (*chatcompletions.Adapter, error) {
	return chatcompletions.New(descriptor, clock, validateLocalPlan, projectLocalUsage, classifyLocalCompletion)
}

func ValidateExecutionRequest(request domain.DomainRequest) error {
	if err := chatcompletions.ValidateRequest(request); err != nil {
		return err
	}
	var body localRequest
	if err := json.Unmarshal(request.Payload, &body); err != nil {
		return chatcompletions.ErrInvalidRequest
	}
	limit := body.MaxTokens
	if limit == nil {
		limit = body.MaxCompletionTokens
	}
	if limit == nil || *limit > localOutputTokenLimit {
		return chatcompletions.ErrInvalidRequest
	}
	return nil
}

func validateLocalPlan(request domain.DomainRequest, plan domain.ExecutionPlan) error {
	if err := ValidateExecutionRequest(request); err != nil {
		return err
	}
	if plan.ProviderTypeID != "provider-alpha" || plan.DomainRevision != localDomainRevision || plan.ReportMappingRevision != localMappingRevision || len(plan.Requirements) != 3 || !validProviderEndpoint(plan.Endpoint) {
		return chatcompletions.ErrInvalidRequest
	}
	for _, requirement := range plan.Requirements {
		if requirement.MetricID == "metric.tokens.total" && requirement.Unit == "token" {
			var body localRequest
			if json.Unmarshal(request.Payload, &body) != nil {
				return chatcompletions.ErrInvalidRequest
			}
			limit := body.MaxTokens
			if limit == nil {
				limit = body.MaxCompletionTokens
			}
			if limit == nil || domain.Units(*limit) > requirement.UpperBound {
				return chatcompletions.ErrInvalidRequest
			}
		} else if !((requirement.MetricID == "metric.usd.cents" && requirement.Unit == "usd_cents") || (requirement.MetricID == "metric.concurrency.request" && requirement.Unit == "slot")) {
			return chatcompletions.ErrUnsupportedMetric
		}
	}
	return nil
}

func validProviderEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/v1/localtest/chat/completions" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return u.Port() != ""
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && u.Port() != ""
}

func projectLocalUsage(plan domain.ExecutionPlan, usage chatcompletions.Usage, at domain.Millis, requestID domain.ID) ([]domain.MetricReport, error) {
	if usage.TotalTokens < 0 || usage.TotalTokens > int64(^uint64(0)>>1)-9_999 {
		return nil, chatcompletions.ErrInvalidResponse
	}
	tokens := domain.Units(usage.TotalTokens)
	cents := domain.Units((usage.TotalTokens + 9_999) / 10_000)
	if cents == 0 && usage.TotalTokens > 0 {
		cents = 1
	}
	ended := domain.Units(0)
	evidence := domain.ID("localtest-usage/" + string(requestID))
	metrics := make([]domain.MetricReport, 0, len(plan.Requirements))
	for _, requirement := range plan.Requirements {
		var actual domain.Units
		switch {
		case requirement.MetricID == "metric.tokens.total" && requirement.Unit == "token":
			actual = tokens
		case requirement.MetricID == "metric.usd.cents" && requirement.Unit == "usd_cents":
			actual = cents
		case requirement.MetricID == "metric.concurrency.request" && requirement.Unit == "slot":
			actual = ended
		default:
			return nil, chatcompletions.ErrUnsupportedMetric
		}
		chargedAt := at
		metrics = append(metrics, domain.MetricReport{MetricID: requirement.MetricID, Unit: requirement.Unit, Actual: &actual, ChargedAt: &chargedAt, EvidenceRefs: []domain.ID{evidence}})
	}
	return metrics, nil
}

func classifyLocalCompletion(finishReason string, refused bool) (chatcompletions.ResultClassification, error) {
	if finishReason == "stop" && !refused {
		return chatcompletions.ResultClassification{Outcome: "known_success"}, nil
	}
	return chatcompletions.ResultClassification{Outcome: "known_failure", Reasons: []domain.Reason{{Group: "domain", Code: "localtest_incomplete"}}}, nil
}
