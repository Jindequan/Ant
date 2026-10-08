// Package chatcompletions implements the portable, non-streaming subset of the
// OpenAI-compatible Chat Completions wire protocol. It performs no networking;
// all requests still pass through the controlled transport.
package chatcompletions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
)

const (
	maxResponseBytes = 4 << 20
	maxRequestTokens = 1_000_000
)

var (
	ErrInvalidAdapter    = errors.New("chat completions adapter: invalid configuration")
	ErrInvalidRequest    = errors.New("chat completions adapter: unsupported request")
	ErrInvalidResponse   = errors.New("chat completions adapter: invalid provider response")
	ErrUnsupportedMetric = errors.New("chat completions adapter: unsupported accounting dimension")
)

type Clock interface{ Now() domain.Millis }

// PlanValidator is supplied by the selected domain/provider policy. It must
// prove that this request's enforceable bounds and report mapping match plan.
// In particular, it owns the selected model's semantics for max_tokens versus
// max_completion_tokens and any provider-documented variance around that cap.
type PlanValidator func(domain.DomainRequest, domain.ExecutionPlan) error

// Usage contains the exact integer dimensions surfaced by a completed
// Chat Completions response. Optional breakdowns stay optional because
// providers/models do not return every detail.
type Usage struct {
	PromptTokens       int64
	CompletionTokens   int64
	TotalTokens        int64
	CachedPromptTokens *int64
	// PromptCacheHitTokens / PromptCacheMissTokens are exposed by providers
	// such as DeepSeek alongside the OpenAI prompt_tokens_details breakdown.
	// They remain pointers because many compatible APIs omit this detail.
	PromptCacheHitTokens  *int64
	PromptCacheMissTokens *int64
	ReasoningTokens       *int64
}

// UsageProjector is provider/domain-owned. It converts the response's usage
// facts into every planned metric, including any verified monetary rates.
type UsageProjector func(domain.ExecutionPlan, Usage, domain.Millis, domain.ID) ([]domain.MetricReport, error)

type ResultClassification struct {
	Outcome string
	Reasons []domain.Reason
}

// CompletionClassifier maps provider completion facts onto the selected
// domain's success contract. The adapter never equates HTTP 200 with business
// success.
type CompletionClassifier func(string, bool) (ResultClassification, error)

// Adapter handles a deliberately small shared subset: text messages, one
// completion and non-streaming responses. Provider/domain policy must validate
// pre-send bounds and project every usage dimension before an adapter can be
// constructed; there is intentionally no generic pricing assumption here.
type Adapter struct {
	descriptor domain.ProviderDescriptor
	clock      Clock
	validate   PlanValidator
	project    UsageProjector
	classify   CompletionClassifier
}

func New(descriptor domain.ProviderDescriptor, clock Clock, validate PlanValidator, project UsageProjector, classify CompletionClassifier) (*Adapter, error) {
	if descriptor.TypeID == "" || descriptor.AdapterRevision == "" || clock == nil || validate == nil || project == nil || classify == nil || !slices.Contains(descriptor.SupportedDomains, "ai/text") {
		return nil, ErrInvalidAdapter
	}
	return &Adapter{descriptor: cloneDescriptor(descriptor), clock: clock, validate: validate, project: project, classify: classify}, nil
}

func (a *Adapter) Descriptor() domain.ProviderDescriptor {
	if a == nil {
		return domain.ProviderDescriptor{}
	}
	return cloneDescriptor(a.descriptor)
}

// ValidateRequest exposes the exact input contract to the HTTP submission
// boundary so malformed or unsupported requests are rejected before claim.
func ValidateRequest(request domain.DomainRequest) error {
	_, err := parseRequest(request)
	return err
}

func (a *Adapter) Encode(request domain.DomainRequest, plan domain.ExecutionPlan) (adapters.EncodedRequest, error) {
	if a == nil || a.clock == nil || request.Domain != "ai/text" ||
		plan.ProviderTypeID != a.descriptor.TypeID || plan.AdapterRevision != a.descriptor.AdapterRevision ||
		plan.TargetModelID == "" || len(plan.ResponseModelIDs) == 0 ||
		plan.Endpoint == "" || plan.CredentialRef == "" || plan.CredentialRevision == "" ||
		!slices.Contains(a.descriptor.SupportedDomains, request.Domain) {
		return adapters.EncodedRequest{}, ErrInvalidRequest
	}
	body, err := parseRequest(request)
	if err != nil {
		return adapters.EncodedRequest{}, err
	}
	if domain.ID(body.Model) != plan.TargetModelID {
		return adapters.EncodedRequest{}, ErrInvalidRequest
	}
	if err := a.validate(request, plan); err != nil {
		return adapters.EncodedRequest{}, err
	}
	// Streaming is intentionally disabled in this adapter. A successful
	// response therefore includes a complete usage record before settlement.
	body.Stream = false
	encoded, err := json.Marshal(body)
	if err != nil {
		return adapters.EncodedRequest{}, ErrInvalidRequest
	}
	return adapters.EncodedRequest{
		Endpoint: plan.Endpoint, Method: "POST",
		Headers: map[string]string{"Content-Type": "application/json", "Accept": "application/json"},
		Body:    encoded, CredentialRef: plan.CredentialRef,
		CredentialHeader: "Authorization", CredentialField: "value", CredentialPrefix: "Bearer ",
		MaxResponseBytes: maxResponseBytes,
	}, nil
}

func parseRequest(request domain.DomainRequest) (chatRequest, error) {
	if request.Domain != "ai/text" || len(request.Payload) == 0 {
		return chatRequest{}, ErrInvalidRequest
	}
	var body chatRequest
	decoder := json.NewDecoder(bytes.NewReader(request.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return chatRequest{}, ErrInvalidRequest
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return chatRequest{}, ErrInvalidRequest
	}
	if body.Model == "" || len(body.Model) > 128 || len(body.Messages) == 0 || len(body.Messages) > 256 ||
		(body.MaxTokens == nil) == (body.MaxCompletionTokens == nil) || body.Stream || (body.N != nil && *body.N != 1) {
		return chatRequest{}, ErrInvalidRequest
	}
	outputLimit := body.MaxTokens
	if outputLimit == nil {
		outputLimit = body.MaxCompletionTokens
	}
	if *outputLimit < 1 || *outputLimit > maxRequestTokens {
		return chatRequest{}, ErrInvalidRequest
	}
	switch body.ReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
	default:
		return chatRequest{}, ErrInvalidRequest
	}
	if body.Temperature != nil && (*body.Temperature < 0 || *body.Temperature > 2) {
		return chatRequest{}, ErrInvalidRequest
	}
	for _, message := range body.Messages {
		if message.Content == "" || (message.Role != "system" && message.Role != "developer" && message.Role != "user" && message.Role != "assistant") {
			return chatRequest{}, ErrInvalidRequest
		}
	}
	return body, nil
}

func (a *Adapter) Decode(ctx context.Context, response adapters.RawResponse, plan domain.ExecutionPlan, attemptID domain.ID, storeOutput func([]byte) error) (domain.ExecutionReport, error) {
	if a == nil || a.clock == nil || ctx == nil || response.Body == nil || attemptID == "" || storeOutput == nil ||
		plan.ProviderTypeID != a.descriptor.TypeID || plan.AdapterRevision != a.descriptor.AdapterRevision {
		return domain.ExecutionReport{}, ErrInvalidResponse
	}
	if response.Status < 200 || response.Status >= 300 {
		return domain.ExecutionReport{}, ErrInvalidResponse
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return domain.ExecutionReport{}, ErrInvalidResponse
	}
	var result chatResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&result); err != nil || ensureResponseJSONEnd(decoder) != nil ||
		result.Object != "chat.completion" || result.ID == "" || result.Model == "" || !slices.Contains(plan.ResponseModelIDs, domain.ID(result.Model)) || len(result.Choices) != 1 ||
		result.Choices[0].Index != 0 || result.Choices[0].Message.Role != "assistant" ||
		!supportedFinishReason(result.Choices[0].FinishReason) ||
		result.Usage.PromptTokens == nil || result.Usage.CompletionTokens == nil || result.Usage.TotalTokens == nil ||
		*result.Usage.PromptTokens < 0 || *result.Usage.CompletionTokens < 0 || *result.Usage.TotalTokens < 0 {
		return domain.ExecutionReport{}, ErrInvalidResponse
	}
	total, err := domain.AddUnits(domain.Units(*result.Usage.PromptTokens), domain.Units(*result.Usage.CompletionTokens))
	if err != nil || int64(total) != *result.Usage.TotalTokens {
		return domain.ExecutionReport{}, ErrInvalidResponse
	}
	content := result.Choices[0].Message.Content
	if content == "" {
		content = result.Choices[0].Message.Refusal
	}
	if err := ctx.Err(); err != nil {
		return domain.ExecutionReport{}, err
	}
	now := a.clock.Now()
	if now < 0 {
		return domain.ExecutionReport{}, ErrInvalidResponse
	}
	usage := Usage{
		PromptTokens: *result.Usage.PromptTokens, CompletionTokens: *result.Usage.CompletionTokens, TotalTokens: *result.Usage.TotalTokens,
		CachedPromptTokens:    result.Usage.PromptTokensDetails.CachedTokens,
		PromptCacheHitTokens:  result.Usage.PromptCacheHitTokens,
		PromptCacheMissTokens: result.Usage.PromptCacheMissTokens,
		ReasoningTokens:       result.Usage.CompletionTokensDetails.ReasoningTokens,
	}
	if !validUsageDetails(usage) {
		return domain.ExecutionReport{}, ErrInvalidResponse
	}
	classification, err := a.classify(result.Choices[0].FinishReason, result.Choices[0].Message.Refusal != "")
	if err != nil || (classification.Outcome != "known_success" && classification.Outcome != "known_failure" && classification.Outcome != "unknown") || !validReasons(classification.Reasons) {
		return domain.ExecutionReport{}, ErrInvalidResponse
	}
	if content == "" && classification.Outcome == "known_success" {
		return domain.ExecutionReport{}, ErrInvalidResponse
	}
	metrics, err := a.project(plan, usage, now, domain.ID(result.ID))
	if err != nil || !validProjectedMetrics(plan, metrics, now) {
		return domain.ExecutionReport{}, ErrInvalidResponse
	}
	if content != "" {
		if err := storeOutput([]byte(content)); err != nil {
			return domain.ExecutionReport{}, err
		}
	}
	evidence := domain.ID("provider-response/" + result.ID)
	return domain.ExecutionReport{
		ReportID: domain.ID("chat-completion/" + string(attemptID)), AttemptID: attemptID,
		Outcome: classification.Outcome, RetrySafety: domain.Unsafe, Reasons: slices.Clone(classification.Reasons), ProviderRequestID: domain.ID(result.ID),
		Metrics: metrics, ExternalEnded: true, EndEvidenceRefs: []domain.ID{evidence},
	}, nil
}

func supportedFinishReason(reason string) bool {
	switch reason {
	case "stop", "length", "content_filter", "insufficient_system_resource", "aborted":
		return true
	default:
		return false
	}
}

type chatRequest struct {
	Model               string        `json:"model"`
	Messages            []chatMessage `json:"messages"`
	MaxTokens           *int64        `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int64        `json:"max_completion_tokens,omitempty"`
	ReasoningEffort     string        `json:"reasoning_effort,omitempty"`
	Stream              bool          `json:"stream"`
	N                   *int          `json:"n,omitempty"`
	Temperature         *float64      `json:"temperature,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens          *int64 `json:"prompt_tokens"`
		CompletionTokens      *int64 `json:"completion_tokens"`
		TotalTokens           *int64 `json:"total_tokens"`
		PromptCacheHitTokens  *int64 `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens *int64 `json:"prompt_cache_miss_tokens"`
		PromptTokensDetails   struct {
			CachedTokens *int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails struct {
			ReasoningTokens *int64 `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrInvalidRequest
	}
	return nil
}

func ensureResponseJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrInvalidResponse
	}
	return nil
}

func validUsageDetails(usage Usage) bool {
	if usage.PromptTokens < 0 || usage.CompletionTokens < 0 || usage.TotalTokens < 0 {
		return false
	}
	total, err := domain.AddUnits(domain.Units(usage.PromptTokens), domain.Units(usage.CompletionTokens))
	if err != nil || int64(total) != usage.TotalTokens {
		return false
	}
	if usage.CachedPromptTokens != nil && (*usage.CachedPromptTokens < 0 || *usage.CachedPromptTokens > usage.PromptTokens) {
		return false
	}
	if (usage.PromptCacheHitTokens == nil) != (usage.PromptCacheMissTokens == nil) {
		return false
	}
	if usage.PromptCacheHitTokens != nil {
		if *usage.PromptCacheHitTokens < 0 || *usage.PromptCacheMissTokens < 0 ||
			*usage.PromptCacheHitTokens > usage.PromptTokens || *usage.PromptCacheMissTokens > usage.PromptTokens {
			return false
		}
		promptParts, err := domain.AddUnits(domain.Units(*usage.PromptCacheHitTokens), domain.Units(*usage.PromptCacheMissTokens))
		if err != nil || int64(promptParts) != usage.PromptTokens {
			return false
		}
		if usage.CachedPromptTokens != nil && *usage.CachedPromptTokens != *usage.PromptCacheHitTokens {
			return false
		}
	}
	return usage.ReasoningTokens == nil || (*usage.ReasoningTokens >= 0 && *usage.ReasoningTokens <= usage.CompletionTokens)
}

func validProjectedMetrics(plan domain.ExecutionPlan, metrics []domain.MetricReport, now domain.Millis) bool {
	expected := make(map[domain.ID]string, len(plan.Requirements))
	for _, req := range plan.Requirements {
		if req.MetricID == "" || req.Unit == "" {
			return false
		}
		if unit, found := expected[req.MetricID]; found && unit != req.Unit {
			return false
		}
		expected[req.MetricID] = req.Unit
	}
	if len(expected) == 0 || len(metrics) != len(expected) {
		return false
	}
	seen := map[domain.ID]bool{}
	for _, metric := range metrics {
		if metric.MetricID == "" || metric.Unit == "" || seen[metric.MetricID] || len(metric.EvidenceRefs) == 0 ||
			(metric.Actual != nil && *metric.Actual < 0) || metric.ChargedAt == nil || *metric.ChargedAt < 0 || *metric.ChargedAt > now {
			return false
		}
		if metric.ActualOutOfRangeDecimal != "" && (metric.Actual != nil || domain.ValidateOutOfRangeUnitsDecimal(metric.ActualOutOfRangeDecimal) != nil) {
			return false
		}
		seen[metric.MetricID] = true
		matched := false
		for _, requirement := range plan.Requirements {
			if metric.MetricID == requirement.MetricID && metric.Unit == requirement.Unit {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func validReasons(reasons []domain.Reason) bool {
	if len(reasons) > 128 {
		return false
	}
	for _, reason := range reasons {
		if reason.Group == "" || reason.Code == "" || len(reason.Group) > 64 || len(reason.Code) > 128 || len(reason.ScopeRef) > 256 {
			return false
		}
	}
	return true
}

func cloneDescriptor(descriptor domain.ProviderDescriptor) domain.ProviderDescriptor {
	descriptor.SupportedDomains = slices.Clone(descriptor.SupportedDomains)
	descriptor.ConfigSchema = bytes.Clone(descriptor.ConfigSchema)
	descriptor.ResourceSchema = bytes.Clone(descriptor.ResourceSchema)
	descriptor.CapabilitySchema = bytes.Clone(descriptor.CapabilitySchema)
	descriptor.ExtensionSchema = bytes.Clone(descriptor.ExtensionSchema)
	descriptor.EntitlementFacts = slices.Clone(descriptor.EntitlementFacts)
	descriptor.Semantics = slices.Clone(descriptor.Semantics)
	for i := range descriptor.Semantics {
		descriptor.Semantics[i].AcceptedRevisions = slices.Clone(descriptor.Semantics[i].AcceptedRevisions)
	}
	descriptor.Profiles = slices.Clone(descriptor.Profiles)
	for i := range descriptor.Profiles {
		descriptor.Profiles[i].RequiredDimensions = slices.Clone(descriptor.Profiles[i].RequiredDimensions)
	}
	return descriptor
}

var _ adapters.ProviderAdapter = (*Adapter)(nil)
