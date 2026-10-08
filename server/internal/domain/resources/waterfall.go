package resources

import "ant/server/internal/domain"

// Provider entitlement waterfall (04-RESOURCE-ALGORITHMS §3). The order of
// pools is the provider-verified deduction order supplied by the caller
// (providerOrder / provider expiry rules) — this function never re-sorts,
// never invents a "cheapest first" commercial order, and never lets a user
// priority influence it (07 §1: user scheduling order and provider
// entitlement order are separate).
//
// A metered pool standing in the verified path with available balance is a
// hard spend-policy gate: when allowMetered is false the whole allocation is
// refused — skipping the metered pool to reach a later free pool would forge
// a consumption path the provider did not verify. Expired pools are skipped
// without triggering the gate. Insufficient total capacity refuses the whole
// allocation (no partial prepares); since this function is pure, "rollback"
// means simply returning no allocations.

// EntitlementPool is one entitlement bucket in provider-verified order.
type EntitlementPool struct {
	PoolID    domain.ID
	Available domain.Units
	Metered   bool
	Expired   bool
}

// EntitlementAllocation is the reserved quantity on one pool.
type EntitlementAllocation struct {
	PoolID domain.ID
	Units  domain.Units
}

// AllocateWaterfall distributes need across pools in input order and returns
// (allocations, admitted, reasons). admitted=false always comes with an empty
// allocation slice and at least one reason; need == 0 succeeds without
// touching any pool (even metered ones behind a strict spend policy —
// reserving nothing cannot incur cost).
func AllocateWaterfall(pools []EntitlementPool, need domain.Units, allowMetered bool) ([]EntitlementAllocation, bool, []domain.Reason) {
	if need < 0 {
		return nil, false, []domain.Reason{{Group: "entitlement", Code: "invalid_quantity"}}
	}
	if need == 0 {
		return nil, true, nil
	}
	allocs := make([]EntitlementAllocation, 0, len(pools))
	remaining := need
	for _, p := range pools {
		if p.Expired {
			continue
		}
		if p.Available <= 0 {
			continue // exhausted (or defensive: negative balance never allocates)
		}
		if p.Metered && !allowMetered {
			return nil, false, []domain.Reason{{
				Group:    "spend_policy",
				Code:     "allowMetered",
				ScopeRef: p.PoolID,
			}}
		}
		q := min(remaining, p.Available)
		allocs = append(allocs, EntitlementAllocation{PoolID: p.PoolID, Units: q})
		remaining -= q // q <= remaining, no overflow possible
		if remaining == 0 {
			return allocs, true, nil
		}
	}
	return nil, false, []domain.Reason{{
		Group: "entitlement",
		Code:  "insufficient_entitlement",
	}}
}
