package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"

	"ant/server/internal/config"
	"ant/server/internal/domain"
	"ant/server/internal/providers/deepseek"
)

var ErrDeepSeekPolicy = errors.New("deepseek policy: unsupported executable plan")

const (
	deepSeekDomainRevision  domain.ID = deepseek.DomainRevision
	deepSeekMappingRevision domain.ID = deepseek.UsageMappingRevision
)

// DeepSeekTextPolicy reserves the worst-case request charge against a
// provider-verified USD balance and one concurrency slot. It deliberately
// does not turn a local tokenizer estimate into an account token limit.
type DeepSeekTextPolicy struct{}

func (DeepSeekTextPolicy) Domain() string               { return "ai/text" }
func (DeepSeekTextPolicy) CapabilityProfile() domain.ID { return "profile.chat.basic" }
func (DeepSeekTextPolicy) ProviderTypeID() domain.ID    { return deepseek.ProviderTypeID }

func (DeepSeekTextPolicy) Build(_ context.Context, input domain.ExecuteInput, offer config.OfferDef, binding config.BindingDef, account config.AccountDef, resources []config.CompiledResource, evidence []config.EvidenceDef) (PlanConstraints, []domain.Reason, error) {
	if account.ProviderTypeID != deepseek.ProviderTypeID || offer.Domain != "ai/text" || offer.CapabilityProfile != "profile.chat.basic" ||
		input.Request.Domain != "ai/text" || input.Request.CapabilityProfile != "profile.chat.basic" ||
		binding.Domain != "ai/text" || !validDeepSeekPolicyEndpoint(binding.Endpoint) || len(resources) == 0 || deepseek.ValidateRequest(input.Request) != nil {
		return PlanConstraints{}, nil, ErrDeepSeekPolicy
	}
	var capability struct {
		Model           string `json:"model"`
		MaxOutputTokens *int64 `json:"maxOutputTokens"`
	}
	if len(binding.ProviderExtensions) > 0 && json.Unmarshal(binding.ProviderExtensions, &capability) != nil {
		return PlanConstraints{}, nil, ErrDeepSeekPolicy
	}
	if capability.Model != "" && capability.Model != deepseek.SupportedModel {
		return PlanConstraints{}, nil, ErrDeepSeekPolicy
	}
	maxOutput, err := deepseek.RequestedMaxOutputTokens(input.Request)
	if err != nil || maxOutput > deepseek.MaxContextTokens || capability.MaxOutputTokens != nil &&
		(*capability.MaxOutputTokens < 1 || *capability.MaxOutputTokens > deepseek.MaxOutputTokens || maxOutput > *capability.MaxOutputTokens) {
		return PlanConstraints{}, nil, ErrDeepSeekPolicy
	}
	maxPrompt := deepseek.MaxContextTokens - maxOutput
	maxCharge, err := deepseek.PeakWorstCasePriceCents(maxPrompt, maxOutput)
	if err != nil {
		return PlanConstraints{}, nil, ErrDeepSeekPolicy
	}

	plan := PlanConstraints{
		TargetModelID: deepseek.SupportedModel, ResponseModelIDs: []domain.ID{deepseek.SupportedModel},
		DomainRevision: deepSeekDomainRevision, ReportMappingRevision: deepSeekMappingRevision,
		Requirements: []domain.ResourceRequirement{}, Bounds: []domain.BoundEvidence{},
	}
	for _, dimension := range []struct {
		metric string
		unit   string
		role   string
		upper  domain.Units
	}{
		{metric: "metric.usd.cents", unit: "usd_cents", role: "entitlement", upper: domain.Units(maxCharge)},
		{metric: "metric.concurrency.request", unit: "slot", role: "occupancy", upper: 1},
	} {
		var selected *config.CompiledResource
		for index := range resources {
			resource := &resources[index]
			if resource.MetricID == domain.ID(dimension.metric) && resource.Unit == dimension.unit && deepSeekResourceMatches(*resource, dimension.role) {
				if selected != nil {
					return PlanConstraints{}, nil, ErrDeepSeekPolicy
				}
				selected = resource
			}
		}
		if selected == nil || dimension.upper > selected.Capacity {
			return PlanConstraints{}, nil, ErrDeepSeekPolicy
		}
		mode := "all_constraints"
		if dimension.role == "entitlement" {
			if selected.EntitlementGroupID == "" || selected.ProviderOrder == nil {
				return PlanConstraints{}, nil, ErrDeepSeekPolicy
			}
			mode = "provider_entitlement_waterfall"
		}
		var bound *config.EvidenceDef
		for index := range evidence {
			item := &evidence[index]
			if slices.Contains(selected.EvidenceRefs, item.EvidenceID) && (item.Kind == "bound" || item.Kind == "hard_stop") {
				if bound != nil {
					return PlanConstraints{}, nil, ErrDeepSeekPolicy
				}
				bound = item
			}
		}
		if bound == nil || bound.Payload["metricId"] != dimension.metric {
			return PlanConstraints{}, nil, ErrDeepSeekPolicy
		}
		evidenceUpper, err := domain.ParseUnits(bound.Payload["bound"])
		if err != nil || evidenceUpper < dimension.upper || evidenceUpper > selected.Capacity {
			return PlanConstraints{}, nil, ErrDeepSeekPolicy
		}
		plan.Requirements = append(plan.Requirements, domain.ResourceRequirement{
			MetricID: selected.MetricID, Unit: selected.Unit, UpperBound: dimension.upper,
			EvidenceID: bound.EvidenceID, Mode: mode, ResourceIDs: []domain.ID{selected.ResourceID},
		})
		plan.Bounds = append(plan.Bounds, domain.BoundEvidence{
			EvidenceID: bound.EvidenceID, MetricID: selected.MetricID, Unit: selected.Unit,
			UpperBound: evidenceUpper, EnforcementSource: bound.Payload["enforcement"],
			MappingRevision: deepSeekMappingRevision, EndpointRef: binding.BindingID,
			SourceRef: domain.ID(bound.SourceRef), VerifiedAt: domain.Millis(bound.VerifiedAtMillis),
			ExpiresAt: domain.Millis(bound.ExpiresAtMillis),
		})
	}
	return plan, nil, nil
}

func validDeepSeekPolicyEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && raw == deepseek.APIEndpoint && u.Scheme == "https" && u.Host == "api.deepseek.com" && u.Path == "/chat/completions" &&
		u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func deepSeekResourceMatches(resource config.CompiledResource, role string) bool {
	switch role {
	case "entitlement":
		return resource.Kind == "counter" && resource.CommercialRole == "entitlement"
	case "occupancy":
		return resource.Kind == "occupancy"
	default:
		return false
	}
}

var _ CandidatePlanPolicy = DeepSeekTextPolicy{}
var _ ProviderSpecificCandidatePlanPolicy = DeepSeekTextPolicy{}
