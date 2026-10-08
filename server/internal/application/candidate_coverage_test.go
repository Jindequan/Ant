package application

import (
	"context"
	"errors"
	"testing"

	"ant/server/internal/config"
	"ant/server/internal/domain"
)

type alteredCandidatePolicy struct {
	base   CandidatePlanPolicy
	mutate func(*PlanConstraints)
}

func (p alteredCandidatePolicy) Domain() string               { return p.base.Domain() }
func (p alteredCandidatePolicy) CapabilityProfile() domain.ID { return p.base.CapabilityProfile() }
func (p alteredCandidatePolicy) Build(ctx context.Context, input domain.ExecuteInput, offer config.OfferDef, binding config.BindingDef, account config.AccountDef, resources []config.CompiledResource, evidence []config.EvidenceDef) (PlanConstraints, []domain.Reason, error) {
	plan, reasons, err := p.base.Build(ctx, input, offer, binding, account, resources, evidence)
	if err == nil {
		p.mutate(&plan)
	}
	return plan, reasons, err
}

func TestCandidatePolicyCannotOmitDuplicateOrReclassifyResources(t *testing.T) {
	for name, mutate := range map[string]func(*PlanConstraints){
		"omitted resource": func(p *PlanConstraints) {
			p.Requirements = p.Requirements[:len(p.Requirements)-1]
			p.Bounds = p.Bounds[:len(p.Bounds)-1]
		},
		"duplicate resource": func(p *PlanConstraints) {
			p.Requirements[0].ResourceIDs = append(p.Requirements[0].ResourceIDs, p.Requirements[0].ResourceIDs[0])
		},
		"duplicate requirement": func(p *PlanConstraints) { p.Requirements = append(p.Requirements, p.Requirements[0]) },
		"duplicate bound":       func(p *PlanConstraints) { p.Bounds = append(p.Bounds, p.Bounds[0]) },
		"unknown mode":          func(p *PlanConstraints) { p.Requirements[0].Mode = "extension-bypass" },
		"limit as entitlement":  func(p *PlanConstraints) { p.Requirements[0].Mode = "provider_entitlement_waterfall" },
		"entitlement as limit": func(p *PlanConstraints) {
			for i := range p.Requirements {
				if p.Requirements[i].Mode == "provider_entitlement_waterfall" {
					p.Requirements[i].Mode = "all_constraints"
					break
				}
			}
		},
		"demand above bound": func(p *PlanConstraints) { p.Requirements[0].UpperBound++ },
		"foreign resource":   func(p *PlanConstraints) { p.Requirements[0].ResourceIDs[0] = "invented" },
	} {
		t.Run(name, func(t *testing.T) {
			planner, policy := candidatePlannerFixture(t)
			planner.policies[candidatePolicyKey(policy.Domain(), policy.CapabilityProfile())] = alteredCandidatePolicy{policy, mutate}
			auth, input := candidateExecuteInput()
			result, err := planner.PlanPool(context.Background(), auth, input, "a1")
			if err == nil || len(result.Candidates) != 0 {
				t.Fatalf("unsafe candidate accepted: %+v %v", result, err)
			}
		})
	}
}

func TestCandidatePolicyMayReserveLessThanVerifiedCeiling(t *testing.T) {
	planner, policy := candidatePlannerFixture(t)
	planner.policies[candidatePolicyKey(policy.Domain(), policy.CapabilityProfile())] = alteredCandidatePolicy{policy, func(p *PlanConstraints) {
		for i := range p.Requirements {
			p.Requirements[i].UpperBound = 1
		}
	}}
	auth, input := candidateExecuteInput()
	result, err := planner.PlanPool(context.Background(), auth, input, "a1")
	if err != nil || len(result.Candidates) != 2 {
		t.Fatalf("bounded demand rejected: %+v %v", result, err)
	}
}

func TestCandidatePlannerRejectsNotYetVerifiedEvidence(t *testing.T) {
	planner, _, clock := candidatePlannerFixtureWithClock(t)
	clock.now = 1
	auth, input := candidateExecuteInput()
	_, err := planner.PlanPool(context.Background(), auth, input, "a1")
	if !errors.Is(err, ErrCandidateEvidenceStale) {
		t.Fatalf("future evidence accepted: %v", err)
	}
}
