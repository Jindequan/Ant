// Package deepseek adds DeepSeek-specific admission and response guarantees
// around the shared, non-streaming Chat Completions codec. It does not read
// credentials or perform network I/O.
package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
	"ant/server/internal/providers/chatcompletions"
)

const (
	ProviderTypeID       domain.ID = "deepseek-standard"
	AdapterRevision      domain.ID = "deepseek-chat-v1"
	SupportedModel                 = "deepseek-flash"
	APIEndpoint                    = "https://api.deepseek.com/chat/completions"
	MaxOutputTokens      int64     = 393216
	MaxContextTokens     int64     = 1_000_000
	DomainRevision                 = "deepseek-ai-text-v1"
	UsageMappingRevision           = "deepseek-usage-peak-bound-v1"
)

var ErrUnsupported = errors.New("deepseek adapter: unsupported request or plan")

type PlanValidator func(domain.DomainRequest, domain.ExecutionPlan) error
type UsageProjector func(domain.ExecutionPlan, chatcompletions.Usage, domain.Millis, domain.ID) ([]domain.MetricReport, error)

type Adapter struct{ codec *chatcompletions.Adapter }

type requestBody struct {
	Model           string               `json:"model"`
	Messages        []chatRequestMessage `json:"messages"`
	MaxTokens       *int64               `json:"max_tokens"`
	ReasoningEffort string               `json:"reasoning_effort,omitempty"`
	Stream          *bool                `json:"stream,omitempty"`
	N               *int                 `json:"n,omitempty"`
	Temperature     *float64             `json:"temperature,omitempty"`
}

type chatRequestMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func New(descriptor domain.ProviderDescriptor, clock chatcompletions.Clock, validate PlanValidator, project UsageProjector) (*Adapter, error) {
	if descriptor.TypeID != ProviderTypeID || descriptor.AdapterRevision != AdapterRevision || validate == nil || project == nil {
		return nil, chatcompletions.ErrInvalidAdapter
	}
	codec, err := chatcompletions.New(descriptor, clock, func(request domain.DomainRequest, plan domain.ExecutionPlan) error {
		if err := ValidateExecutionPlan(request, plan); err != nil {
			return err
		}
		return validate(request, plan)
	}, func(plan domain.ExecutionPlan, usage chatcompletions.Usage, at domain.Millis, requestID domain.ID) ([]domain.MetricReport, error) {
		// DeepSeek documents both cache counters in its completed usage record.
		// Missing either one makes price attribution incomplete, so fail closed
		// before the settlement command can book a partial report.
		if usage.PromptCacheHitTokens == nil || usage.PromptCacheMissTokens == nil {
			return nil, chatcompletions.ErrInvalidResponse
		}
		return project(plan, usage, at, requestID)
	}, classify)
	if err != nil {
		return nil, err
	}
	return &Adapter{codec: codec}, nil
}

func ValidateRequest(request domain.DomainRequest) error {
	body, err := parseRequest(request)
	if err != nil || body.Model != SupportedModel || body.MaxTokens == nil || *body.MaxTokens > MaxOutputTokens {
		return ErrUnsupported
	}
	return nil
}

// RequestedMaxOutputTokens returns the validated output cap used by request
// planning. It never estimates prompt tokenization; the provider-enforced
// context length supplies the conservative prompt ceiling instead.
func RequestedMaxOutputTokens(request domain.DomainRequest) (int64, error) {
	body, err := parseRequest(request)
	if err != nil || body.MaxTokens == nil {
		return 0, ErrUnsupported
	}
	return *body.MaxTokens, nil
}

// ValidateExecutionPlan checks the provider and accounting identity again at
// encode time, after candidate planning and before dispatch authorization.
func ValidateExecutionPlan(request domain.DomainRequest, plan domain.ExecutionPlan) error {
	maxOutput, err := RequestedMaxOutputTokens(request)
	if err != nil || plan.ProviderTypeID != ProviderTypeID || plan.TargetModelID != SupportedModel ||
		len(plan.ResponseModelIDs) != 1 || plan.ResponseModelIDs[0] != SupportedModel || !deepSeekEndpoint(plan.Endpoint) ||
		plan.AdapterRevision != AdapterRevision || plan.DomainRevision != DomainRevision ||
		plan.ReportMappingRevision != UsageMappingRevision || plan.CredentialRef == "" || plan.CredentialRevision == "" {
		return ErrUnsupported
	}
	maxPrompt := MaxContextTokens - maxOutput
	maxPrice, err := PeakWorstCasePriceCents(maxPrompt, maxOutput)
	if err != nil {
		return ErrUnsupported
	}
	requirementByMetric := make(map[domain.ID]domain.ResourceRequirement, len(plan.Requirements))
	for _, requirement := range plan.Requirements {
		if requirement.MetricID == "" || requirement.Unit == "" || requirement.UpperBound < 0 || requirement.EvidenceID == "" ||
			len(requirement.ResourceIDs) == 0 || requirementByMetric[requirement.MetricID].MetricID != "" {
			return ErrUnsupported
		}
		requirementByMetric[requirement.MetricID] = requirement
		switch requirement.MetricID {
		case "metric.usd.cents":
			if requirement.Unit != "usd_cents" || int64(requirement.UpperBound) < maxPrice {
				return ErrUnsupported
			}
		case "metric.concurrency.request":
			if requirement.Unit != "slot" || requirement.UpperBound < 1 {
				return ErrUnsupported
			}
		case "metric.tokens.total":
			if requirement.Unit != "token" || requirement.UpperBound < domain.Units(MaxContextTokens) {
				return ErrUnsupported
			}
		case "metric.tokens.prompt":
			if requirement.Unit != "token" || requirement.UpperBound < domain.Units(maxPrompt) {
				return ErrUnsupported
			}
		case "metric.tokens.completion", "output_tokens":
			if requirement.Unit != "token" || requirement.UpperBound < domain.Units(maxOutput) {
				return ErrUnsupported
			}
		default:
			return ErrUnsupported
		}
	}
	if requirementByMetric["metric.usd.cents"].MetricID == "" || requirementByMetric["metric.concurrency.request"].MetricID == "" {
		return ErrUnsupported
	}
	if len(plan.Bounds) != len(plan.Requirements) {
		return ErrUnsupported
	}
	seenBounds := make(map[domain.ID]bool, len(plan.Bounds))
	for _, bound := range plan.Bounds {
		requirement, ok := requirementByMetric[bound.MetricID]
		if !ok || seenBounds[bound.MetricID] || bound.Unit != requirement.Unit || bound.EvidenceID != requirement.EvidenceID ||
			bound.UpperBound < requirement.UpperBound || bound.MappingRevision != UsageMappingRevision || bound.ExpiresAt <= bound.VerifiedAt {
			return ErrUnsupported
		}
		seenBounds[bound.MetricID] = true
	}
	return nil
}

func parseRequest(request domain.DomainRequest) (requestBody, error) {
	if request.Domain != "ai/text" || len(request.Payload) == 0 {
		return requestBody{}, ErrUnsupported
	}
	if err := chatcompletions.ValidateRequest(request); err != nil {
		return requestBody{}, ErrUnsupported
	}
	var body requestBody
	decoder := json.NewDecoder(bytes.NewReader(request.Payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || ensureJSONEnd(decoder) != nil || body.Model != SupportedModel || len(body.Messages) == 0 ||
		body.MaxTokens == nil || *body.MaxTokens < 1 || *body.MaxTokens > MaxOutputTokens ||
		(body.Stream != nil && *body.Stream) || (body.N != nil && *body.N != 1) {
		return requestBody{}, ErrUnsupported
	}
	switch body.ReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
	default:
		return requestBody{}, ErrUnsupported
	}
	for _, message := range body.Messages {
		if message.Content == "" || (message.Role != "system" && message.Role != "user" && message.Role != "assistant") {
			return requestBody{}, ErrUnsupported
		}
	}
	return body, nil
}

func deepSeekEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && raw == APIEndpoint && u.Scheme == "https" && u.Host == "api.deepseek.com" && u.Path == "/chat/completions" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func classify(finishReason string, refused bool) (chatcompletions.ResultClassification, error) {
	switch {
	case refused:
		return chatcompletions.ResultClassification{Outcome: "known_failure", Reasons: []domain.Reason{{Group: "domain", Code: "response_refused"}}}, nil
	case finishReason == "stop":
		return chatcompletions.ResultClassification{Outcome: "known_success"}, nil
	case finishReason == "length":
		return chatcompletions.ResultClassification{Outcome: "known_failure", Reasons: []domain.Reason{{Group: "domain", Code: "response_truncated"}}}, nil
	case finishReason == "content_filter":
		return chatcompletions.ResultClassification{Outcome: "known_failure", Reasons: []domain.Reason{{Group: "domain", Code: "response_filtered"}}}, nil
	case finishReason == "insufficient_system_resource":
		return chatcompletions.ResultClassification{Outcome: "known_failure", Reasons: []domain.Reason{{Group: "provider", Code: "insufficient_system_resource"}}}, nil
	case finishReason == "aborted":
		return chatcompletions.ResultClassification{Outcome: "unknown", Reasons: []domain.Reason{{Group: "provider", Code: "completion_aborted"}}}, nil
	default:
		return chatcompletions.ResultClassification{}, chatcompletions.ErrInvalidResponse
	}
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	}
	return ErrUnsupported
}

func (a *Adapter) Descriptor() domain.ProviderDescriptor {
	if a == nil || a.codec == nil {
		return domain.ProviderDescriptor{}
	}
	return a.codec.Descriptor()
}

func (a *Adapter) Encode(request domain.DomainRequest, plan domain.ExecutionPlan) (adapters.EncodedRequest, error) {
	if a == nil || a.codec == nil {
		return adapters.EncodedRequest{}, ErrUnsupported
	}
	return a.codec.Encode(request, plan)
}

func (a *Adapter) Decode(ctx context.Context, response adapters.RawResponse, plan domain.ExecutionPlan, attemptID domain.ID, storeOutput func([]byte) error) (domain.ExecutionReport, error) {
	if a == nil || a.codec == nil {
		return domain.ExecutionReport{}, ErrUnsupported
	}
	return a.codec.Decode(ctx, response, plan, attemptID, storeOutput)
}
