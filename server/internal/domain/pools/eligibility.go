package pools

import (
	"ant/server/internal/domain"
)

// Hard eligibility filtering (07-POOLS-AND-CONFIG §3): domain/profile
// semantics, enabled flags, required affinity and the spend policy are gates
// applied BEFORE ranking. A required constraint is never relaxed by priority,
// cost or preferred affinity; ranking (domain.RankCandidates / SortMembers)
// only orders what survives this filter.

// EligibilityInput carries the request-side hard-gate material.
type EligibilityInput struct {
	Domain            string
	CapabilityProfile domain.ID
	// RequiredAffinity gates only when Mode == domain.AffinityModeRequired;
	// preferred and other modes are diagnostic and never filter here.
	RequiredAffinity *domain.Affinity
	// AllowMetered is the spend-policy hard gate (false refuses known
	// metered capacity).
	AllowMetered bool
	// MeteredOfferIDs enumerates offers whose verified plan includes a
	// metered (pay-per-use) resource. ExecutionPlan.Requirements carries no
	// metered flag, so the compiler supplies this registry — documented
	// decision; the compiler owns its completeness (unknown-cost-is-not-zero
	// is enforced where plans are compiled, not by guessing here).
	MeteredOfferIDs map[domain.ID]bool
	// AccountByBinding maps bindingID → owning accountID from verified
	// config. domain.Candidate carries only Offer.BindingID, so a required
	// account affinity is checked through this mapping — documented
	// decision. A required account that cannot be verified fails closed.
	AccountByBinding map[domain.ID]domain.ID
}

// EligibilityRejection records why one offer was filtered out.
type EligibilityRejection struct {
	OfferID domain.ID
	Reasons []domain.Reason
}

// FilterEligible partitions candidates into eligible and rejected, preserving
// input order in both. Each rejection accumulates every failed gate (not
// just the first) so the diagnostic stays complete.
func FilterEligible(cands []domain.Candidate, in EligibilityInput) (eligible []domain.Candidate, rejected []EligibilityRejection) {
	eligible = make([]domain.Candidate, 0, len(cands))
	for _, c := range cands {
		reasons := eligibilityRejections(c, in)
		if len(reasons) > 0 {
			rejected = append(rejected, EligibilityRejection{OfferID: c.Offer.OfferID, Reasons: reasons})
			continue
		}
		eligible = append(eligible, c)
	}
	return eligible, rejected
}

func eligibilityRejections(c domain.Candidate, in EligibilityInput) []domain.Reason {
	scope := c.Offer.OfferID
	var reasons []domain.Reason
	if !c.Offer.Enabled {
		reasons = append(reasons, domain.Reason{Group: "eligibility", Code: "offer_disabled", ScopeRef: scope})
	}
	if !c.Member.Enabled {
		reasons = append(reasons, domain.Reason{Group: "eligibility", Code: "member_disabled", ScopeRef: scope})
	}
	if c.Offer.Domain != in.Domain {
		reasons = append(reasons, domain.Reason{Group: "eligibility", Code: "domain_mismatch", ScopeRef: scope})
	}
	if c.Offer.CapabilityProfile != in.CapabilityProfile {
		reasons = append(reasons, domain.Reason{Group: "eligibility", Code: "capability_profile_mismatch", ScopeRef: scope})
	}
	reasons = append(reasons, affinityRejections(c, in)...)
	if !in.AllowMetered && in.MeteredOfferIDs[c.Offer.OfferID] {
		reasons = append(reasons, domain.Reason{Group: "spend_policy", Code: "allowMetered", ScopeRef: scope})
	}
	return reasons
}

func affinityRejections(c domain.Candidate, in EligibilityInput) []domain.Reason {
	aff := in.RequiredAffinity
	if aff == nil || aff.Mode != domain.AffinityModeRequired {
		return nil // preferred/none: diagnostic only, never a hard gate
	}
	scope := c.Offer.OfferID
	if aff.BindingID == "" && aff.AccountID == "" {
		// Fail closed: a required affinity naming no target cannot be honored.
		return []domain.Reason{{Group: "affinity", Code: "affinity_unspecified", ScopeRef: scope}}
	}
	var reasons []domain.Reason
	if aff.BindingID != "" && c.Offer.BindingID != aff.BindingID {
		reasons = append(reasons, domain.Reason{Group: "affinity", Code: "affinity_binding_mismatch", ScopeRef: scope})
	}
	if aff.AccountID != "" {
		account, ok := in.AccountByBinding[c.Offer.BindingID]
		if !ok {
			reasons = append(reasons, domain.Reason{Group: "affinity", Code: "affinity_account_unverifiable", ScopeRef: scope})
		} else if account != aff.AccountID {
			reasons = append(reasons, domain.Reason{Group: "affinity", Code: "affinity_account_mismatch", ScopeRef: scope})
		}
	}
	return reasons
}
