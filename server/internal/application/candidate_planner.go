package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"ant/server/internal/config"
	"ant/server/internal/domain"
	"ant/server/internal/domain/pools"
	"ant/server/internal/planning"
	"ant/server/internal/store"
)

var (
	ErrCandidatePlannerInvalid = errors.New("candidate planner: invalid input")
	ErrCandidatePlannerDenied  = errors.New("candidate planner: forbidden")
	ErrCandidatePoolMissing    = errors.New("candidate planner: pool unavailable")
	ErrCandidateNotReady       = errors.New("candidate planner: active config unavailable")
	ErrCandidatePolicyMissing  = errors.New("candidate planner: no registered request policy")
	ErrCandidateIntegrity      = errors.New("candidate planner: active config and live projection disagree")
	ErrCandidatePolicyFailure  = errors.New("candidate planner: request policy failed")
	ErrCandidateEvidenceStale  = errors.New("candidate planner: supporting evidence is expired")
)

// Aliases preserve existing integrations while new providers can implement
// the neutral planning port without depending on the application runtime.
type PlanConstraints = planning.Constraints

// CandidatePlanPolicy owns business-specific request bounds and report
// mappings. It receives only validated active config projections; it cannot
// access SQL, credentials, or the network.
type CandidatePlanPolicy = planning.Policy

// ProviderSpecificCandidatePlanPolicy lets several providers implement the
// same domain/profile while keeping each provider's model, usage and tariff
// rules isolated. Policies without this method remain profile-wide fallbacks.
type ProviderSpecificCandidatePlanPolicy = planning.ProviderPolicy

type CandidateRejection struct {
	OfferID domain.ID
	Reasons []domain.Reason
}

type CandidatePlan struct {
	ConfigRevision domain.ID
	PoolVersion    int64
	Candidates     []domain.Candidate
	Rejected       []CandidateRejection
}

type CandidatePlanner struct {
	store            store.Store
	catalog          config.ProviderCatalog
	policies         map[string]CandidatePlanPolicy
	providerPolicies map[string]ProviderSpecificCandidatePlanPolicy
	now              func() domain.Millis
}

func NewCandidatePlanner(st store.Store, catalog config.ProviderCatalog, registered ...CandidatePlanPolicy) (*CandidatePlanner, error) {
	return newCandidatePlanner(st, catalog, func() domain.Millis { return domain.Millis(time.Now().UnixMilli()) }, registered...)
}

// NewCandidatePlannerWithClock builds a planner whose evidence-age checks use
// the same injected clock as configuration and ledger commands. Production
// composition roots should use this constructor so admission decisions do
// not silently depend on a second wall-clock source.
func NewCandidatePlannerWithClock(st store.Store, catalog config.ProviderCatalog, clock Clock, registered ...CandidatePlanPolicy) (*CandidatePlanner, error) {
	if clock == nil {
		return nil, ErrCandidatePlannerInvalid
	}
	return newCandidatePlanner(st, catalog, clock.Now, registered...)
}

func newCandidatePlanner(st store.Store, catalog config.ProviderCatalog, now func() domain.Millis, registered ...CandidatePlanPolicy) (*CandidatePlanner, error) {
	if st == nil || catalog == nil || now == nil || len(registered) == 0 {
		return nil, ErrCandidatePlannerInvalid
	}
	policies := make(map[string]CandidatePlanPolicy, len(registered))
	providerPolicies := make(map[string]ProviderSpecificCandidatePlanPolicy, len(registered))
	for _, policy := range registered {
		if policy == nil || policy.Domain() == "" || policy.CapabilityProfile() == "" {
			return nil, ErrCandidatePlannerInvalid
		}
		key := candidatePolicyKey(policy.Domain(), policy.CapabilityProfile())
		if providerPolicy, ok := policy.(ProviderSpecificCandidatePlanPolicy); ok {
			if providerPolicy.ProviderTypeID() == "" {
				return nil, ErrCandidatePlannerInvalid
			}
			providerKey := providerCandidatePolicyKey(providerPolicy.ProviderTypeID(), policy.Domain(), policy.CapabilityProfile())
			if _, exists := providerPolicies[providerKey]; exists {
				return nil, ErrCandidatePlannerInvalid
			}
			providerPolicies[providerKey] = providerPolicy
			continue
		}
		if _, exists := policies[key]; exists {
			return nil, ErrCandidatePlannerInvalid
		}
		policies[key] = policy
	}
	return &CandidatePlanner{store: st, catalog: catalog, policies: policies, providerPolicies: providerPolicies, now: now}, nil
}

// PlanPool reads the active validated document and live pool/member/offer
// versions in one read snapshot. It applies hard eligibility before stable
// priority ordering and asks the registered domain policy for each surviving
// offer's exact resource requirements.
func (p *CandidatePlanner) PlanPool(ctx context.Context, auth domain.AuthContext, input domain.ExecuteInput, attemptID domain.ID) (CandidatePlan, error) {
	if p == nil || p.store == nil || p.catalog == nil || p.now == nil || ctx == nil || input.PoolID == "" || input.OperationID == "" || attemptID == "" || input.Request.Domain == "" || input.Request.CapabilityProfile == "" {
		return CandidatePlan{}, ErrCandidatePlannerInvalid
	}
	if auth.PrincipalID == "" || auth.Namespace == "" || !slices.Contains(auth.Permissions, domain.PermExecute) {
		return CandidatePlan{}, ErrCandidatePlannerDenied
	}
	if !p.hasPolicyForRequest(input.Request.Domain, input.Request.CapabilityProfile) {
		return CandidatePlan{}, ErrCandidatePolicyMissing
	}
	var result CandidatePlan
	nowMillis := p.now()
	err := p.store.Read(ctx, func(rx store.ReaderTx) error {
		configReader, ok := rx.(store.OfferReaderTx)
		if !ok {
			return ErrOperationQueryStore
		}
		active, found, err := rx.GetActiveConfig()
		if err != nil {
			return err
		}
		if !found {
			return ErrCandidateNotReady
		}
		revision, found, err := rx.GetConfigRevision(active.Revision)
		if err != nil {
			return err
		}
		if !found || len(revision.DefinitionJSON) == 0 {
			return ErrCandidateIntegrity
		}
		validation := config.Compile(revision.DefinitionJSON, p.catalog)
		if len(validation.Issues) != 0 || validation.Doc == nil || validation.Compiled == nil {
			return ErrCandidateIntegrity
		}
		livePool, poolFound, err := rx.GetPool(input.PoolID)
		if err != nil {
			return err
		}
		if !poolFound || livePool.Namespace != auth.Namespace || !livePool.Enabled {
			return ErrCandidatePoolMissing
		}
		poolDef, poolFound := findPool(validation.Doc.Pools, input.PoolID)
		if !poolFound || poolDef.Namespace != auth.Namespace || !poolDef.Enabled {
			return ErrCandidateIntegrity
		}
		members, err := rx.ListPoolMembers(input.PoolID)
		if err != nil {
			return err
		}
		if !sameMemberIDs(poolDef.Members, members) {
			return ErrCandidateIntegrity
		}
		result = CandidatePlan{ConfigRevision: active.Revision, PoolVersion: livePool.Version, Candidates: []domain.Candidate{}, Rejected: []CandidateRejection{}}
		compiledOffers := indexCompiledOffers(validation.Compiled.Offers)
		compiledResources := indexCompiledResources(validation.Compiled.Resources)
		evidence := indexEvidence(validation.Doc.Evidence)
		accounts := indexAccounts(validation.Doc.Accounts)
		bindings := indexBindings(validation.Doc.Bindings)
		definitions := indexOffers(validation.Doc.Offers)
		accountByBinding := make(map[domain.ID]domain.ID, len(bindings))
		meteredOffers := make(map[domain.ID]bool)
		projectionRejections := make(map[domain.ID][]domain.Reason)
		candidates := make([]domain.Candidate, 0, len(members))
		for _, member := range members {
			memberDef, ok := findMember(poolDef.Members, member.OfferID)
			if !ok || memberDef.Enabled != member.Enabled || memberDef.Priority != member.Priority {
				return ErrCandidateIntegrity
			}
			offer, ok := definitions[member.OfferID]
			if !ok {
				return ErrCandidateIntegrity
			}
			offerRecord, found, err := configReader.GetOffer(member.OfferID)
			if err != nil {
				return err
			}
			if !found || offerRecord.BindingID != offer.BindingID || offerRecord.Domain != offer.Domain || offerRecord.CapabilityProfile != offer.CapabilityProfile || offerRecord.Enabled != offer.Enabled {
				return ErrCandidateIntegrity
			}
			binding, ok := bindings[offer.BindingID]
			if !ok || binding.Domain != offer.Domain {
				return ErrCandidateIntegrity
			}
			account, ok := accounts[binding.AccountID]
			if !ok {
				return ErrCandidateIntegrity
			}
			accountByBinding[binding.BindingID] = account.AccountID
			if !binding.Enabled {
				projectionRejections[offer.OfferID] = append(projectionRejections[offer.OfferID], domain.Reason{Group: "eligibility", Code: "binding_disabled", ScopeRef: offer.OfferID})
			}
			if !account.Enabled {
				projectionRejections[offer.OfferID] = append(projectionRejections[offer.OfferID], domain.Reason{Group: "eligibility", Code: "account_disabled", ScopeRef: offer.OfferID})
			}
			compiledOffer, ok := compiledOffers[offer.OfferID]
			if !ok || compiledOffer.Domain != offer.Domain || compiledOffer.CapabilityProfile != offer.CapabilityProfile {
				return ErrCandidateIntegrity
			}
			for _, resourceID := range offer.ResourceRefs {
				resource, exists := compiledResources[resourceID]
				if !exists {
					return ErrCandidateIntegrity
				}
				if resource.Metered {
					meteredOffers[offer.OfferID] = true
				}
			}
			candidates = append(candidates, domain.Candidate{
				Member: domain.PoolMember{OfferID: member.OfferID, Priority: member.Priority, Enabled: member.Enabled, Version: member.Version},
				Offer: domain.ResourceOffer{OfferID: offer.OfferID, BindingID: offer.BindingID, Domain: offer.Domain,
					CapabilityProfile: offer.CapabilityProfile, Enabled: offer.Enabled, ResourceRefs: slices.Clone(offer.ResourceRefs), Version: offerRecord.Version},
			})
		}
		eligible, rejected := pools.FilterEligible(candidates, pools.EligibilityInput{
			Domain: input.Request.Domain, CapabilityProfile: input.Request.CapabilityProfile,
			RequiredAffinity: input.Affinity, AllowMetered: input.AllowMetered,
			MeteredOfferIDs: meteredOffers, AccountByBinding: accountByBinding,
		})
		for _, rejection := range rejected {
			result.Rejected = append(result.Rejected, CandidateRejection{OfferID: rejection.OfferID, Reasons: rejection.Reasons})
		}
		eligibleWithEnabledProjection := eligible[:0]
		for _, candidate := range eligible {
			reasons := projectionRejections[candidate.Offer.OfferID]
			if len(reasons) > 0 {
				result.Rejected = append(result.Rejected, CandidateRejection{OfferID: candidate.Offer.OfferID, Reasons: slices.Clone(reasons)})
				continue
			}
			eligibleWithEnabledProjection = append(eligibleWithEnabledProjection, candidate)
		}
		eligible = eligibleWithEnabledProjection
		plannedCandidates := make([]domain.Candidate, 0, len(eligible))
		for i := range eligible {
			candidate := &eligible[i]
			offer := definitions[candidate.Offer.OfferID]
			binding := bindings[offer.BindingID]
			account := accounts[binding.AccountID]
			resources := make([]config.CompiledResource, 0, len(offer.ResourceRefs))
			windowRevision := domain.ID("")
			for _, resourceID := range offer.ResourceRefs {
				resource := compiledResources[resourceID]
				if windowRevision == "" {
					windowRevision = resource.SemanticsRevision
				} else if windowRevision != resource.SemanticsRevision {
					return ErrCandidateIntegrity
				}
				resources = append(resources, resource)
			}
			policy := p.policyFor(account.ProviderTypeID, input.Request.Domain, input.Request.CapabilityProfile)
			if policy == nil {
				result.Rejected = append(result.Rejected, CandidateRejection{OfferID: offer.OfferID, Reasons: []domain.Reason{{Group: "eligibility", Code: "provider_policy_unavailable", ScopeRef: offer.OfferID}}})
				continue
			}
			constraints, policyReasons, err := policy.Build(ctx, input, offer, binding, account, resources, validation.Doc.Evidence)
			if err != nil {
				return ErrCandidatePolicyFailure
			}
			if len(policyReasons) > 0 {
				result.Rejected = append(result.Rejected, CandidateRejection{OfferID: offer.OfferID, Reasons: slices.Clone(policyReasons)})
				continue
			}
			if err := validatePlanConstraints(offer, resources, evidence, constraints, nowMillis); err != nil {
				return err
			}
			descriptor, ok := p.catalog.Descriptor(account.ProviderTypeID)
			if !ok || descriptor.AdapterRevision == "" || binding.CredentialRevision == "" || windowRevision == "" {
				return ErrCandidateIntegrity
			}
			candidate.Plan = domain.ExecutionPlan{
				PlanID:         domain.ID("plan/" + string(input.OperationID) + "/" + string(attemptID) + "/" + string(offer.OfferID)),
				ConfigRevision: active.Revision, DomainRevision: constraints.DomainRevision,
				AdapterRevision: descriptor.AdapterRevision, ProviderTypeID: account.ProviderTypeID,
				TargetModelID: constraints.TargetModelID, ResponseModelIDs: slices.Clone(constraints.ResponseModelIDs),
				Endpoint: binding.Endpoint, CredentialRef: binding.CredentialRef,
				PoolID: input.PoolID, PoolVersion: livePool.Version,
				OfferID: offer.OfferID, MemberPriority: memberPriority(members, offer.OfferID),
				AccountID: account.AccountID, BindingID: binding.BindingID, CredentialRevision: binding.CredentialRevision,
				Requirements: cloneRequirements(constraints.Requirements), Bounds: slices.Clone(constraints.Bounds),
				WindowRuleRevision: windowRevision, ReportMappingRevision: constraints.ReportMappingRevision,
			}
			plannedCandidates = append(plannedCandidates, *candidate)
		}
		result.Candidates = domain.RankCandidates(plannedCandidates)
		return nil
	})
	if err != nil {
		return CandidatePlan{}, err
	}
	return result, nil
}

func validatePlanConstraints(offer config.OfferDef, resources []config.CompiledResource, evidence map[domain.ID]config.EvidenceDef, plan PlanConstraints, now domain.Millis) error {
	if plan.TargetModelID == "" || len(plan.ResponseModelIDs) == 0 || plan.DomainRevision == "" || plan.ReportMappingRevision == "" || len(plan.Requirements) == 0 || len(plan.Bounds) == 0 {
		return ErrCandidatePolicyFailure
	}
	responseModels := make(map[domain.ID]bool, len(plan.ResponseModelIDs))
	for _, modelID := range plan.ResponseModelIDs {
		if modelID == "" || responseModels[modelID] {
			return ErrCandidatePolicyFailure
		}
		responseModels[modelID] = true
	}
	allowedResources := make(map[domain.ID]bool, len(offer.ResourceRefs))
	resourceByID := make(map[domain.ID]config.CompiledResource, len(resources))
	for _, id := range offer.ResourceRefs {
		if id == "" || allowedResources[id] {
			return ErrCandidateIntegrity
		}
		allowedResources[id] = true
	}
	for _, resource := range resources {
		if !allowedResources[resource.ResourceID] || resourceByID[resource.ResourceID].ResourceID != "" {
			return ErrCandidateIntegrity
		}
		resourceByID[resource.ResourceID] = resource
	}
	if len(resources) == 0 || len(resourceByID) != len(allowedResources) {
		return ErrCandidateIntegrity
	}
	boundByDimension := make(map[string]domain.BoundEvidence, len(plan.Bounds))
	for _, bound := range plan.Bounds {
		if bound.EvidenceID == "" || bound.MetricID == "" || bound.Unit == "" || bound.UpperBound < 0 || bound.MappingRevision == "" || bound.MappingRevision != plan.ReportMappingRevision || bound.ExpiresAt <= bound.VerifiedAt {
			return ErrCandidatePolicyFailure
		}
		evidenceDef, exists := evidence[bound.EvidenceID]
		if !exists || (evidenceDef.Kind != "bound" && evidenceDef.Kind != "hard_stop") || evidenceDef.SourceRef != string(bound.SourceRef) || evidenceDef.VerifiedAtMillis != bound.VerifiedAt || evidenceDef.ExpiresAtMillis != bound.ExpiresAt || evidenceDef.Payload["metricId"] != string(bound.MetricID) || evidenceDef.Payload["enforcement"] != bound.EnforcementSource {
			return ErrCandidateIntegrity
		}
		if bound.VerifiedAt > now || bound.ExpiresAt <= now {
			return ErrCandidateEvidenceStale
		}
		verifiedBound, err := domain.ParseUnits(evidenceDef.Payload["bound"])
		if err != nil || verifiedBound != bound.UpperBound {
			return ErrCandidateIntegrity
		}
		boundResourceFound := false
		for _, resource := range resources {
			if resource.MetricID == bound.MetricID && resource.Unit == bound.Unit && slices.Contains(resource.EvidenceRefs, bound.EvidenceID) {
				boundResourceFound = true
				break
			}
		}
		if !boundResourceFound {
			return ErrCandidateIntegrity
		}
		key := string(bound.EvidenceID) + "\x00" + string(bound.MetricID) + "\x00" + bound.Unit
		if _, exists := boundByDimension[key]; exists {
			return ErrCandidatePolicyFailure
		}
		boundByDimension[key] = bound
	}
	covered := make(map[domain.ID]bool, len(resources))
	metricUnits := make(map[domain.ID]string, len(plan.Requirements))
	usedBounds := make(map[string]bool, len(plan.Requirements))
	for _, requirement := range plan.Requirements {
		if requirement.MetricID == "" || requirement.Unit == "" || requirement.UpperBound < 0 || len(requirement.ResourceIDs) == 0 {
			return ErrCandidatePolicyFailure
		}
		if unit, exists := metricUnits[requirement.MetricID]; exists && unit != requirement.Unit {
			return ErrCandidatePolicyFailure
		}
		metricUnits[requirement.MetricID] = requirement.Unit
		group := domain.ID("")
		orders := make(map[int64]bool)
		for _, id := range requirement.ResourceIDs {
			resource, exists := resourceByID[id]
			if !allowedResources[id] || !exists || covered[id] || resource.MetricID != requirement.MetricID || resource.Unit != requirement.Unit {
				return ErrCandidateIntegrity
			}
			covered[id] = true
			switch requirement.Mode {
			case "all_constraints":
				if resource.CommercialRole != "limit" || (resource.Kind != "counter" && resource.Kind != "occupancy") {
					return ErrCandidateIntegrity
				}
			case "provider_entitlement_waterfall":
				if resource.CommercialRole != "entitlement" || resource.Kind != "counter" || resource.EntitlementGroupID == "" || resource.ProviderOrder == nil || orders[*resource.ProviderOrder] {
					return ErrCandidateIntegrity
				}
				if group != "" && group != resource.EntitlementGroupID {
					return ErrCandidateIntegrity
				}
				group = resource.EntitlementGroupID
				orders[*resource.ProviderOrder] = true
			default:
				return ErrCandidatePolicyFailure
			}
		}
		key := string(requirement.EvidenceID) + "\x00" + string(requirement.MetricID) + "\x00" + requirement.Unit
		bound, exists := boundByDimension[key]
		if !exists || requirement.UpperBound > bound.UpperBound || requirement.EvidenceID != bound.EvidenceID {
			return ErrCandidateIntegrity
		}
		usedBounds[key] = true
	}
	if len(covered) != len(resourceByID) || len(usedBounds) != len(boundByDimension) {
		return ErrCandidateIntegrity
	}
	return nil
}

func candidatePolicyKey(domainName string, profile domain.ID) string {
	return domainName + "\x00" + string(profile)
}

func providerCandidatePolicyKey(providerType domain.ID, domainName string, profile domain.ID) string {
	return candidatePolicyKey(domainName, profile) + "\x00" + string(providerType)
}

func (p *CandidatePlanner) hasPolicyForRequest(domainName string, profile domain.ID) bool {
	if _, ok := p.policies[candidatePolicyKey(domainName, profile)]; ok {
		return true
	}
	prefix := candidatePolicyKey(domainName, profile) + "\x00"
	for key := range p.providerPolicies {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func (p *CandidatePlanner) policyFor(providerType domain.ID, domainName string, profile domain.ID) CandidatePlanPolicy {
	if policy := p.providerPolicies[providerCandidatePolicyKey(providerType, domainName, profile)]; policy != nil {
		return policy
	}
	return p.policies[candidatePolicyKey(domainName, profile)]
}

func findPool(pools []config.PoolDef, id domain.ID) (config.PoolDef, bool) {
	for _, candidate := range pools {
		if candidate.PoolID == id {
			return candidate, true
		}
	}
	return config.PoolDef{}, false
}

func sameMemberIDs(defs []config.PoolMemberDef, live []store.PoolMemberRecord) bool {
	if len(defs) != len(live) {
		return false
	}
	seen := make(map[domain.ID]bool, len(defs))
	for _, def := range defs {
		if seen[def.OfferID] {
			return false
		}
		seen[def.OfferID] = true
	}
	for _, member := range live {
		if !seen[member.OfferID] {
			return false
		}
	}
	return true
}

func findMember(members []config.PoolMemberDef, id domain.ID) (config.PoolMemberDef, bool) {
	for _, member := range members {
		if member.OfferID == id {
			return member, true
		}
	}
	return config.PoolMemberDef{}, false
}

func indexAccounts(values []config.AccountDef) map[domain.ID]config.AccountDef {
	result := make(map[domain.ID]config.AccountDef, len(values))
	for _, value := range values {
		result[value.AccountID] = value
	}
	return result
}

func indexBindings(values []config.BindingDef) map[domain.ID]config.BindingDef {
	result := make(map[domain.ID]config.BindingDef, len(values))
	for _, value := range values {
		result[value.BindingID] = value
	}
	return result
}

func indexOffers(values []config.OfferDef) map[domain.ID]config.OfferDef {
	result := make(map[domain.ID]config.OfferDef, len(values))
	for _, value := range values {
		result[value.OfferID] = value
	}
	return result
}

func indexEvidence(values []config.EvidenceDef) map[domain.ID]config.EvidenceDef {
	result := make(map[domain.ID]config.EvidenceDef, len(values))
	for _, value := range values {
		result[value.EvidenceID] = value
	}
	return result
}

func indexCompiledOffers(values []config.CompiledOffer) map[domain.ID]config.CompiledOffer {
	result := make(map[domain.ID]config.CompiledOffer, len(values))
	for _, value := range values {
		result[value.OfferID] = value
	}
	return result
}

func indexCompiledResources(values []config.CompiledResource) map[domain.ID]config.CompiledResource {
	result := make(map[domain.ID]config.CompiledResource, len(values))
	for _, value := range values {
		result[value.ResourceID] = value
	}
	return result
}

func memberPriority(members []store.PoolMemberRecord, offerID domain.ID) int64 {
	for _, member := range members {
		if member.OfferID == offerID {
			return member.Priority
		}
	}
	return -1
}

func cloneRequirements(values []domain.ResourceRequirement) []domain.ResourceRequirement {
	result := slices.Clone(values)
	for i := range result {
		result[i].ResourceIDs = slices.Clone(result[i].ResourceIDs)
	}
	return result
}
