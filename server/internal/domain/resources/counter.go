package resources

import (
	"fmt"

	"ant/server/internal/domain"
)

// Counter rules for fixed-window counters (04-RESOURCE-ALGORITHMS §2).

// CounterAvailable computes the admit basis of a counter snapshot:
//
//	netCommitted      = committed + adjustment
//	localAvailable    = capacity - netCommitted - reserved
//	providerAvailable = observedRemaining - max(0, committed - committedAtStart) - reserved
//	available         = max(0, min(localAvailable, providerAvailable))
//
// obs counts as valid only when it matches the snapshot's resource and
// window, has not expired at now, and was captured at the authoritative
// adjustment version (a new adjustment invalidates older observations). An
// invalid or nil observation degrades to the local baseline only — whether
// that is admissible is a policy decision above this pure function ("无可靠
// 初始状态的严格候选拒绝" is enforced by callers, not here).
//
// adjustmentVersion is the AUTHORITATIVE current version held by the caller
// (ResourceContext.AdjustmentVersion, R11). It is never taken from the
// observation itself: ObservationEvidence.AdjustmentVersionAtStart is the
// past, and comparing it against itself would turn staleness checking into a
// no-op. All arithmetic uses domain.AddUnits/SubUnits; overflow errors.
func CounterAvailable(snap domain.ResourceSnapshot, obs *domain.ObservationEvidence, now domain.Millis, adjustmentVersion int64) (domain.Units, error) {
	netCommitted, err := domain.AddUnits(snap.Committed, snap.Adjustment)
	if err != nil {
		return 0, fmt.Errorf("counter: committed+adjustment overflow on %s: %w", snap.ResourceID, err)
	}
	local, err := domain.SubUnits(snap.Capacity, netCommitted)
	if err != nil {
		return 0, fmt.Errorf("counter: capacity-netCommitted overflow on %s: %w", snap.ResourceID, err)
	}
	local, err = domain.SubUnits(local, snap.Reserved)
	if err != nil {
		return 0, fmt.Errorf("counter: local-reserved overflow on %s: %w", snap.ResourceID, err)
	}
	if !counterObservationValid(snap, obs, now, adjustmentVersion) {
		return clampNonNegative(local), nil
	}
	// Concurrent consumption since the observation started is subtracted
	// conservatively; a watermark that moved backwards cannot free provider
	// quota.
	consumedSinceStart, err := domain.SubUnits(snap.Committed, obs.CommittedAtStart)
	if err != nil {
		return 0, fmt.Errorf("counter: committed-committedAtStart overflow on %s: %w", snap.ResourceID, err)
	}
	if consumedSinceStart < 0 {
		consumedSinceStart = 0
	}
	provider, err := domain.SubUnits(obs.Remaining, consumedSinceStart)
	if err != nil {
		return 0, fmt.Errorf("counter: remaining-consumed overflow on %s: %w", snap.ResourceID, err)
	}
	provider, err = domain.SubUnits(provider, snap.Reserved)
	if err != nil {
		return 0, fmt.Errorf("counter: provider-reserved overflow on %s: %w", snap.ResourceID, err)
	}
	if provider < local {
		local = provider
	}
	return clampNonNegative(local), nil
}

func counterObservationValid(snap domain.ResourceSnapshot, obs *domain.ObservationEvidence, now domain.Millis, adjustmentVersion int64) bool {
	if obs == nil {
		return false
	}
	if obs.ResourceID != snap.ResourceID || obs.WindowKey != snap.WindowKey {
		return false
	}
	if now > obs.ExpiresAt {
		return false
	}
	// R11: staleness is judged against the authoritative version supplied by
	// the caller — never against the observation's own version.
	return obs.AdjustmentVersionAtStart == adjustmentVersion
}

func clampNonNegative(u domain.Units) domain.Units {
	if u < 0 {
		return 0
	}
	return u
}

// CounterReserve proposes reserving q units (§2 prepare: reserved += q).
// Admission is refused (Admitted=false with reasons, error nil) for negative
// quantities, scoped holds/encumbrances (§2: "no scoped hold/encumbrance") or
// insufficient availability; error is reserved for internal failures such as
// arithmetic overflow.
//
// R11: staleness of ctx.Observation is judged against ctx.AdjustmentVersion —
// the authoritative current adjustment version injected by the ledger beside
// the snapshot. The former convention (passing the observation's own
// AdjustmentVersionAtStart back as "current") made the check a self-
// comparison that wrongly blocked admission on stale evidence instead of
// simply ignoring it.
func CounterReserve(ctx domain.ResourceContext, q domain.Units) (domain.ResourceChangePlan, error) {
	snap := ctx.Snapshot
	reject := func(reason domain.Reason) (domain.ResourceChangePlan, error) {
		reason.ScopeRef = snap.ResourceID
		return domain.ResourceChangePlan{Admitted: false, Reasons: []domain.Reason{reason}}, nil
	}
	if q < 0 {
		return reject(domain.Reason{Group: "admission", Code: "invalid_quantity"})
	}
	if len(ctx.HoldIDs) > 0 || len(ctx.AttributionEncumbranceIDs) > 0 {
		return reject(domain.Reason{Group: "admission", Code: "scoped_hold"})
	}
	available, err := CounterAvailable(snap, ctx.Observation, ctx.Now, ctx.AdjustmentVersion)
	if err != nil {
		return domain.ResourceChangePlan{}, err
	}
	if q > available {
		return reject(domain.Reason{Group: "capacity", Code: "capacity_exhausted"})
	}
	delta := domain.ResourceDelta{
		ResourceID:      snap.ResourceID,
		WindowKey:       snap.WindowKey,
		ExpectedVersion: snap.Version,
		Reserved:        q,
	}
	return domain.ResourceChangePlan{Admitted: true, Deltas: []domain.ResourceDelta{delta}}, nil
}

// CounterSettle projects a finished reservation onto the counter (§2 settle):
// the reservation is always released (reserved -= bound), and the usage is
// charged to committed according to the structured proof facts of
// SettlementProjection (R11):
//
//	actual known          → committed += actual. A real overrun
//	                        (actual > bound) is recorded in full — the fact
//	                        is never truncated (I10) — and flagged with
//	                        group=overrun; scoping the hold is a separate,
//	                        explicit change.
//	actual unknown, End = reliable_unsent → charge nothing: the controlled
//	                        Transport proved the request never left the
//	                        process, so provable usage is zero. A non-zero
//	                        actual on such a projection is a contradictory
//	                        fact and errors.
//	actual unknown, otherwise → committed += bound. EndProofExternalEnded is
//	                        a verified end fact; EndProofNone is no claim at
//	                        all, and conservation (planning.go) still applies:
//	                        the reserved bound is accounted rather than
//	                        silently reopening capacity. EvidenceRefs never
//	                        stand in for either fact — they document only.
//
// Attribution is a separate proposition from the end fact (R11): when
// proj.Attribution is AttributionUnknown, the charge (actual or bound) may
// belong to another window, so the returned plan MUST carry an
// AttributionIsolation request (resource + EMPTY window key + charged
// quantity + reason) that the Ledger applies in the same transaction.
// Releasing the reservation while silently reopening capacity is forbidden
// (I06). AttributionKnown produces no isolation request.
//
// Settlement is a fact path and is never admission-rejected (I10: overrun
// still settles). Errors signal illegal calls: reservation/snapshot identity
// mismatch, negative actual (corrections go through adjustment), a bound
// that would drive reserved negative, or an undefined End/Attribution value
// (callers must state facts explicitly; zero values are not facts).
func CounterSettle(ctx domain.ResourceContext, reservation domain.ReservationState, proj domain.SettlementProjection, bound domain.Units) (domain.ResourceChangePlan, error) {
	snap := ctx.Snapshot
	if reservation.ResourceID != snap.ResourceID || reservation.WindowKey != snap.WindowKey {
		return domain.ResourceChangePlan{}, fmt.Errorf(
			"counter: reservation %s (%s/%s) does not match snapshot %s/%s",
			reservation.ReservationID, reservation.ResourceID, reservation.WindowKey, snap.ResourceID, snap.WindowKey)
	}
	switch proj.End {
	case domain.EndProofNone, domain.EndProofReliableUnsent, domain.EndProofExternalEnded:
	default:
		return domain.ResourceChangePlan{}, fmt.Errorf(
			"counter: undefined end proof kind %q on %s", string(proj.End), snap.ResourceID)
	}
	switch proj.Attribution {
	case domain.AttributionKnown, domain.AttributionUnknown:
	default:
		return domain.ResourceChangePlan{}, fmt.Errorf(
			"counter: attribution must be stated explicitly on %s, got %q", snap.ResourceID, string(proj.Attribution))
	}
	if bound < 0 {
		return domain.ResourceChangePlan{}, fmt.Errorf("counter: negative settle bound %d on %s", int64(bound), snap.ResourceID)
	}
	newReserved, err := domain.SubUnits(snap.Reserved, bound)
	if err != nil || newReserved < 0 {
		return domain.ResourceChangePlan{}, fmt.Errorf(
			"counter: settle bound %d exceeds reserved %d on %s", int64(bound), int64(snap.Reserved), snap.ResourceID)
	}
	if proj.End == domain.EndProofReliableUnsent && proj.Actual != nil && *proj.Actual != 0 {
		return domain.ResourceChangePlan{}, fmt.Errorf(
			"counter: reliable-unsent projection with non-zero actual %d on %s is contradictory", int64(*proj.Actual), snap.ResourceID)
	}

	delta := domain.ResourceDelta{
		ResourceID:      snap.ResourceID,
		WindowKey:       snap.WindowKey,
		ExpectedVersion: snap.Version,
		Reserved:        -bound,
	}
	var reasons []domain.Reason
	scope := snap.ResourceID
	var charged domain.Units
	if actual := proj.Actual; actual != nil {
		if *actual < 0 {
			return domain.ResourceChangePlan{}, fmt.Errorf(
				"counter: negative actual on %s is an accounting correction, use adjustment", scope)
		}
		if _, err := domain.AddUnits(snap.Committed, *actual); err != nil {
			return domain.ResourceChangePlan{}, fmt.Errorf("counter: committed+actual overflow on %s: %w", scope, err)
		}
		delta.Committed = *actual
		charged = *actual
		if *actual > bound {
			reasons = append(reasons, domain.Reason{Group: "overrun", Code: "actual_exceeded_reservation", ScopeRef: scope})
		}
	} else if proj.End == domain.EndProofReliableUnsent {
		// Provably never sent: zero usage, reservation released.
		reasons = append(reasons, domain.Reason{Group: "settlement", Code: "reliable_unsent_released", ScopeRef: scope})
	} else {
		// EndProofExternalEnded (known end, unknown usage) or EndProofNone
		// (no claim; conservation rules apply): charge the certified bound.
		if _, err := domain.AddUnits(snap.Committed, bound); err != nil {
			return domain.ResourceChangePlan{}, fmt.Errorf("counter: committed+bound overflow on %s: %w", scope, err)
		}
		delta.Committed = bound
		charged = bound
		reasons = append(reasons, domain.Reason{Group: "settlement", Code: "conservative_bound_applied", ScopeRef: scope})
	}

	plan := domain.ResourceChangePlan{Admitted: true, Deltas: []domain.ResourceDelta{delta}, Reasons: reasons}
	// R11/I06: an unattributed charge must not silently reopen capacity. The
	// isolation request is the only way such a settle may conclude; WindowKey
	// is empty on purpose — the charge is not provably inside any window.
	if proj.Attribution == domain.AttributionUnknown && charged > 0 {
		reason := domain.Reason{Group: "attribution", Code: "window_attribution_unverified", ScopeRef: scope}
		plan.Reasons = append(plan.Reasons, reason)
		plan.IsolationRequests = []domain.AttributionIsolation{{
			ResourceID: snap.ResourceID,
			WindowKey:  "",
			Units:      charged,
			Reason:     reason,
		}}
	}
	return plan, nil
}
