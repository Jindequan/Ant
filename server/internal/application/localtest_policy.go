package application

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"slices"

	"ant/server/internal/config"
	"ant/server/internal/domain"
)

var ErrLocalTestPolicy = errors.New("localtest policy: unsupported executable plan")

const (
	localTestDomainRevision  domain.ID = "localtest-ai-text-v1"
	localTestMappingRevision domain.ID = "localtest-usage-v1"
	localTestMaxOutputTokens           = 8192
)

type localTestTextRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	MaxTokens           *int64 `json:"max_tokens"`
	MaxCompletionTokens *int64 `json:"max_completion_tokens"`
}

// LocalTestTextPolicy builds plans only for the localtest catalogue's
// deterministic AI fixture. It is deliberately isolated from production
// provider policies and requires one bound resource for each fixture dimension.
type LocalTestTextPolicy struct{}

func (LocalTestTextPolicy) Domain() string               { return "ai/text" }
func (LocalTestTextPolicy) CapabilityProfile() domain.ID { return "profile.chat.basic" }
func (LocalTestTextPolicy) ProviderTypeID() domain.ID    { return "provider-alpha" }

func (LocalTestTextPolicy) Build(_ context.Context, input domain.ExecuteInput, offer config.OfferDef, binding config.BindingDef, account config.AccountDef, resources []config.CompiledResource, evidence []config.EvidenceDef) (PlanConstraints, []domain.Reason, error) {
	if account.ProviderTypeID != "provider-alpha" || offer.Domain != "ai/text" || offer.CapabilityProfile != "profile.chat.basic" || binding.Domain != "ai/text" || !validLocalTestEndpoint(binding.Endpoint) || len(resources) == 0 {
		return PlanConstraints{}, nil, ErrLocalTestPolicy
	}
	var request localTestTextRequest
	if json.Unmarshal(input.Request.Payload, &request) != nil || request.Model == "" || len(request.Model) > 128 || len(request.Messages) == 0 || len(request.Messages) > 256 || (request.MaxTokens == nil) == (request.MaxCompletionTokens == nil) {
		return PlanConstraints{}, nil, ErrLocalTestPolicy
	}
	maxOutput := request.MaxTokens
	if maxOutput == nil {
		maxOutput = request.MaxCompletionTokens
	}
	if *maxOutput < 1 || *maxOutput > localTestMaxOutputTokens {
		return PlanConstraints{}, nil, ErrLocalTestPolicy
	}

	plan := PlanConstraints{
		TargetModelID: domain.ID(request.Model), ResponseModelIDs: []domain.ID{domain.ID(request.Model)},
		DomainRevision: localTestDomainRevision, ReportMappingRevision: localTestMappingRevision,
		Requirements: []domain.ResourceRequirement{}, Bounds: []domain.BoundEvidence{},
	}
	var promptRunes int64
	for _, message := range request.Messages {
		promptRunes += int64(len([]rune(message.Content)))
	}
	maximumTokens := promptRunes + *maxOutput
	for _, dimension := range []domain.RequiredDimension{
		{MetricID: "metric.tokens.total", Role: "limit"},
		{MetricID: "metric.usd.cents", Role: "entitlement"},
		{MetricID: "metric.concurrency.request", Role: "occupancy"},
	} {
		var selected *config.CompiledResource
		for index := range resources {
			resource := &resources[index]
			if resource.MetricID == dimension.MetricID && localTestResourceMatches(*resource, dimension.Role) {
				if selected != nil {
					return PlanConstraints{}, nil, ErrLocalTestPolicy
				}
				selected = resource
			}
		}
		if selected == nil {
			return PlanConstraints{}, nil, ErrLocalTestPolicy
		}
		mode := "all_constraints"
		if dimension.Role == "entitlement" {
			if selected.EntitlementGroupID == "" || selected.ProviderOrder == nil {
				return PlanConstraints{}, nil, ErrLocalTestPolicy
			}
			mode = "provider_entitlement_waterfall"
		}
		var bound *config.EvidenceDef
		for index := range evidence {
			item := &evidence[index]
			if slices.Contains(selected.EvidenceRefs, item.EvidenceID) && (item.Kind == "bound" || item.Kind == "hard_stop") {
				if bound != nil {
					return PlanConstraints{}, nil, ErrLocalTestPolicy
				}
				bound = item
			}
		}
		if bound == nil {
			return PlanConstraints{}, nil, ErrLocalTestPolicy
		}
		upper, err := domain.ParseUnits(bound.Payload["bound"])
		if err != nil || upper < 0 || upper > selected.Capacity {
			return PlanConstraints{}, nil, ErrLocalTestPolicy
		}
		if dimension.MetricID == "metric.tokens.total" && maximumTokens > int64(upper) {
			return PlanConstraints{}, nil, ErrLocalTestPolicy
		}
		if dimension.MetricID == "metric.usd.cents" {
			maximumCharge := (maximumTokens + 9_999) / 10_000
			if maximumCharge > int64(upper) {
				return PlanConstraints{}, nil, ErrLocalTestPolicy
			}
		}
		plan.Requirements = append(plan.Requirements, domain.ResourceRequirement{
			MetricID: selected.MetricID, Unit: selected.Unit, UpperBound: upper,
			EvidenceID: bound.EvidenceID, Mode: mode, ResourceIDs: []domain.ID{selected.ResourceID},
		})
		plan.Bounds = append(plan.Bounds, domain.BoundEvidence{
			EvidenceID: bound.EvidenceID, MetricID: selected.MetricID, Unit: selected.Unit,
			UpperBound: upper, EnforcementSource: bound.Payload["enforcement"],
			MappingRevision: localTestMappingRevision, EndpointRef: binding.BindingID,
			SourceRef: domain.ID(bound.SourceRef), VerifiedAt: domain.Millis(bound.VerifiedAtMillis),
			ExpiresAt: domain.Millis(bound.ExpiresAtMillis),
		})
	}
	return plan, nil, nil
}

func validLocalTestEndpoint(raw string) bool {
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

func localTestResourceMatches(resource config.CompiledResource, role string) bool {
	switch role {
	case "entitlement":
		return resource.CommercialRole == "entitlement"
	case "occupancy":
		return resource.Kind == "occupancy"
	case "limit":
		return resource.Kind != "occupancy" && resource.CommercialRole == "limit"
	default:
		return false
	}
}

var _ CandidatePlanPolicy = LocalTestTextPolicy{}
