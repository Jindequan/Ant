package config

// Compiled configuration chain (Q1): trusted facts constrain the document;
// zero issues yield a consumable CompiledConfig for the execution system.
// Identity comes from RESOLVING each resource to exactly one server-verified
// fact instance — classification strings, groups, providerOrder and resource
// ids never mint capacity.

import (
	"ant/server/internal/domain"
	"ant/server/internal/domain/resources"
)

// CompiledResource is one resolved, constraint-checked resource: the
// physical ledger it accounts against and its verified quantity space.
type CompiledResource struct {
	ResourceID         domain.ID
	PhysicalKey        domain.ID
	ScopeID            domain.ID
	OwnerKind          string
	OwnerID            domain.ID
	MetricID           domain.ID
	Unit               string
	QuantumExp         int
	Currency           string
	Metered            bool
	Kind               string // counter|occupancy
	CommercialRole     string // limit|entitlement
	ProviderOrder      *int64
	EntitlementGroupID domain.ID
	SemanticsRef       domain.ID
	SemanticsRevision  domain.ID
	Window             domain.WindowRule
	Capacity           domain.Units
	InitialRemaining   domain.Units
	EvidenceRefs       []domain.ID
}

type CompiledOffer struct {
	OfferID           domain.ID
	Domain            string
	CapabilityProfile domain.ID
	PhysicalKeys      []domain.ID
}

type CompiledConfig struct {
	Resources []CompiledResource
	Offers    []CompiledOffer
}

// windowRuleOf converts a config WindowDef to the domain rule shape.
func windowRuleOf(w *WindowDef) domain.WindowRule {
	return domain.WindowRule{Kind: w.Kind, Period: w.Period, TimeZone: w.TimeZone, AnchorISO: w.AnchorISO, DurationMillis: w.DurationMillis}
}

// windowsExecutableEqual compares normalized kind-specific rule identities.
// Invalid rules never match; finite WindowAt samples cannot prove equality.
func windowsExecutableEqual(a, b domain.WindowRule) bool {
	identityA, errA := resources.NormalizeWindowRuleIdentity(a)
	identityB, errB := resources.NormalizeWindowRuleIdentity(b)
	return errA == nil && errB == nil && identityA == identityB
}

// factWindowMatches compares a verified fact's window with a configured one
// by executable equality.
func factWindowMatches(f domain.VerifiedResourceFact, w *WindowDef) bool {
	return windowsExecutableEqual(f.WindowRule(), windowRuleOf(w))
}

// resolveResource finds the single verified fact instance for a resource.
// Explicit factId resolves by name; otherwise limits resolve dimensionally
// (metric+unit+executable-equal window) within the owner's billing scopes.
func resolveResource(doc *ConfigDocument, cat ProviderCatalog, r *ResourceDef, v *validator) (domain.VerifiedResourceFact, bool) {
	scopeID := r.OwnerID
	if r.OwnerKind == "binding" {
		scopeID = bindingAccountID(doc, r.OwnerID)
		if scopeID == "" {
			v.issue(resourcePath(doc, r.ResourceID)+"/ownerId", "unresolvableResourceOwner")
			return domain.VerifiedResourceFact{}, false
		}
	}
	providerType := providerTypeOfAccount(doc, scopeID)
	if providerType == "" {
		v.issue(resourcePath(doc, r.ResourceID)+"/ownerId", "unresolvableResourceOwner")
		return domain.VerifiedResourceFact{}, false
	}
	if r.ProviderFactID != "" {
		fact, ok := cat.ResolveScopeFact(scopeID, r.ProviderFactID)
		if !ok {
			v.issue(resourcePath(doc, r.ResourceID)+"/providerFactId", "unknownProviderFact")
			return domain.VerifiedResourceFact{}, false
		}
		return fact, true
	}
	fact, count, ok := cat.ResolveDimensionalFact(scopeID, providerType, r.MetricID, r.Unit, func(f domain.VerifiedResourceFact) bool {
		return factWindowMatches(f, &r.Window)
	})
	if !ok {
		if count > 1 {
			v.issue(resourcePath(doc, r.ResourceID)+"/providerFactId", "ambiguousFact")
		} else {
			v.issue(resourcePath(doc, r.ResourceID)+"/providerFactId", "factUnresolved")
		}
		return domain.VerifiedResourceFact{}, false
	}
	return fact, true
}

// verifyFactConstrainsResource: the verified instance's dimensions bound what
// the configuration may claim (C2). Any disagreement is not executable.
func verifyFactConstrainsResource(v *validator, p string, r *ResourceDef, fact domain.VerifiedResourceFact) {
	if fact.MetricID != r.MetricID {
		v.issue(p+"/metricId", "providerFactMetadataMismatch")
	}
	if fact.Unit != r.Unit {
		v.issue(p+"/unit", "providerFactMetadataMismatch")
	}
	if fact.QuantumExp != r.QuantumExp {
		v.issue(p+"/quantumExp", "providerFactMetadataMismatch")
	}
	if fact.Currency != r.Currency {
		v.issue(p+"/currency", "providerFactMetadataMismatch")
	}
	if fact.Metered != r.Metered {
		// Commercial nature comes from the verified instance, not a UI flag.
		v.issue(p+"/metered", "providerFactMetadataMismatch")
	}
	if fact.SemanticsRef != r.SemanticsRef || fact.SemanticsRevision != r.SemanticsRevision {
		v.issue(p+"/semanticsRef", "providerFactMetadataMismatch")
	}
	// The configured window must be the verified fact's window (executable
	// equality) — window changes cannot mint a second ledger.
	if !factWindowMatches(fact, &r.Window) {
		v.issue(p+"/window", "providerFactMetadataMismatch")
	}
}

// compileConstraintErrors collects the numeric constraints (C2) for one
// resource against its resolved fact: capacity cannot exceed the verified
// bound and evidence claims must agree with the verified material.
func compileConstraintErrors(v *validator, p string, r *ResourceDef, fact domain.VerifiedResourceFact, evidenceByID func(domain.ID) *EvidenceDef) {
	configured, err := domain.ParseUnits(r.ConfiguredCapacity)
	if err != nil || configured > fact.CapacityBound {
		v.issue(p+"/configuredCapacity", "capacityExceedsVerifiedBound")
	}
	if ev := evidenceByID(r.InitialStateEvidenceRef); ev != nil && ev.Kind == "initial_state" {
		checkEvidenceQuantity(v, p+"/initialStateEvidenceRef", ev, "remaining", fact.InitialRemaining)
	}
	if ev := evidenceByID(r.CapacityEvidenceRef); ev != nil && (ev.Kind == "bound" || ev.Kind == "hard_stop") {
		checkEvidenceQuantity(v, p+"/capacityEvidenceRef", ev, "bound", fact.CapacityBound)
	}
}

// Evidence quantities describe the fixed verified snapshot, independently of
// any conservative configured capacity. Missing, noncanonical or negative
// claims are invalid; valid quantities must equal the corresponding fact.
func checkEvidenceQuantity(v *validator, path string, ev *EvidenceDef, field string, verified domain.Units) {
	raw, exists := ev.Payload[field]
	quantity, err := domain.ParseUnits(raw)
	if !exists || err != nil || quantity < 0 {
		v.issue(path, "evidenceNotApplicable")
		return
	}
	if quantity != verified {
		v.issue(path, "evidenceConflictsVerifiedFact")
	}
}
