package resources

import (
	"errors"
	"fmt"

	"ant/server/internal/domain"
)

// Concurrency occupancy rules (04-RESOURCE-ALGORITHMS §4). The sets
// reserved/active/uncertain are mutually exclusive per reservation;
// available = capacity - reserved - active - uncertain, so a slot held by
// uncertainty is NOT reusable (unknown consumers keep blocking admission).
//
// Transitions produce deltas only; admission refusals come back as
// Admitted=false plans, while a request that would drive one of the sets
// negative is an illegal state transition and returns ErrIllegalTransition —
// it is a caller bug, not an admission outcome (Ledger validates deltas
// against the same transition table, I05/I06).

// ErrIllegalTransition marks an occupancy transition that would make a state
// set negative (e.g. activating more than is reserved).
var ErrIllegalTransition = errors.New("occupancy: illegal state transition")

func occupancyUsed(snap domain.ResourceSnapshot) (domain.Units, error) {
	used, err := domain.AddUnits(snap.Reserved, snap.Active)
	if err != nil {
		return 0, fmt.Errorf("occupancy: reserved+active overflow on %s: %w", snap.ResourceID, err)
	}
	used, err = domain.AddUnits(used, snap.Uncertain)
	if err != nil {
		return 0, fmt.Errorf("occupancy: used+uncertain overflow on %s: %w", snap.ResourceID, err)
	}
	return used, nil
}

// OccupancyReserve prepares q slots (§4 prepare: reserved += q).
func OccupancyReserve(ctx domain.ResourceContext, q domain.Units) (domain.ResourceChangePlan, error) {
	snap := ctx.Snapshot
	reject := func(reason domain.Reason) (domain.ResourceChangePlan, error) {
		reason.ScopeRef = snap.ResourceID
		return domain.ResourceChangePlan{Admitted: false, Reasons: []domain.Reason{reason}}, nil
	}
	if q < 0 {
		return reject(domain.Reason{Group: "admission", Code: "invalid_quantity"})
	}
	used, err := occupancyUsed(snap)
	if err != nil {
		return domain.ResourceChangePlan{}, err
	}
	available, err := domain.SubUnits(snap.Capacity, used)
	if err != nil {
		return domain.ResourceChangePlan{}, fmt.Errorf("occupancy: capacity-used overflow on %s: %w", snap.ResourceID, err)
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

// OccupancyActivate authorizes dispatch of q reserved slots
// (§4 authorize: reserved -= q; active += q).
func OccupancyActivate(ctx domain.ResourceContext, q domain.Units) (domain.ResourceChangePlan, error) {
	snap := ctx.Snapshot
	if q < 0 {
		return domain.ResourceChangePlan{}, fmt.Errorf("%w: negative activate quantity", ErrIllegalTransition)
	}
	newReserved, err := domain.SubUnits(snap.Reserved, q)
	if err != nil || newReserved < 0 {
		return domain.ResourceChangePlan{}, fmt.Errorf(
			"%w: activating %d with %d reserved on %s", ErrIllegalTransition, int64(q), int64(snap.Reserved), snap.ResourceID)
	}
	if _, err := domain.AddUnits(snap.Active, q); err != nil {
		return domain.ResourceChangePlan{}, fmt.Errorf("occupancy: active+q overflow on %s: %w", snap.ResourceID, err)
	}
	delta := domain.ResourceDelta{
		ResourceID:      snap.ResourceID,
		WindowKey:       snap.WindowKey,
		ExpectedVersion: snap.Version,
		Reserved:        -q,
		Active:          q,
	}
	return domain.ResourceChangePlan{Admitted: true, Deltas: []domain.ResourceDelta{delta}}, nil
}

// OccupancyEndProved releases q active slots against proof of external end
// (§4 endProved: active -= q). Only provider end evidence justifies calling
// this; local abort/timeout/lease expiry must not (§4, I06).
func OccupancyEndProved(ctx domain.ResourceContext, q domain.Units) (domain.ResourceChangePlan, error) {
	snap := ctx.Snapshot
	if q < 0 {
		return domain.ResourceChangePlan{}, fmt.Errorf("%w: negative end quantity", ErrIllegalTransition)
	}
	newActive, err := domain.SubUnits(snap.Active, q)
	if err != nil || newActive < 0 {
		return domain.ResourceChangePlan{}, fmt.Errorf(
			"%w: ending %d with %d active on %s", ErrIllegalTransition, int64(q), int64(snap.Active), snap.ResourceID)
	}
	delta := domain.ResourceDelta{
		ResourceID:      snap.ResourceID,
		WindowKey:       snap.WindowKey,
		ExpectedVersion: snap.Version,
		Active:          -q,
	}
	return domain.ResourceChangePlan{Admitted: true, Deltas: []domain.ResourceDelta{delta}}, nil
}

// OccupancyToUncertain moves q active slots into the uncertainty set when the
// external outcome cannot be confirmed (§4 unknown: active -= q;
// uncertain += q). The slots stay consumed; an encumbrance record is the
// caller's separate step.
func OccupancyToUncertain(ctx domain.ResourceContext, q domain.Units) (domain.ResourceChangePlan, error) {
	snap := ctx.Snapshot
	if q < 0 {
		return domain.ResourceChangePlan{}, fmt.Errorf("%w: negative uncertain quantity", ErrIllegalTransition)
	}
	newActive, err := domain.SubUnits(snap.Active, q)
	if err != nil || newActive < 0 {
		return domain.ResourceChangePlan{}, fmt.Errorf(
			"%w: moving %d with %d active on %s", ErrIllegalTransition, int64(q), int64(snap.Active), snap.ResourceID)
	}
	if _, err := domain.AddUnits(snap.Uncertain, q); err != nil {
		return domain.ResourceChangePlan{}, fmt.Errorf("occupancy: uncertain+q overflow on %s: %w", snap.ResourceID, err)
	}
	delta := domain.ResourceDelta{
		ResourceID:      snap.ResourceID,
		WindowKey:       snap.WindowKey,
		ExpectedVersion: snap.Version,
		Active:          -q,
		Uncertain:       q,
	}
	return domain.ResourceChangePlan{Admitted: true, Deltas: []domain.ResourceDelta{delta}}, nil
}

// OccupancyReleaseUncertain clears q uncertain slots once later end proof
// arrives (§4 laterEndProved: uncertain -= q; exactly once per encumbrance).
func OccupancyReleaseUncertain(ctx domain.ResourceContext, q domain.Units) (domain.ResourceChangePlan, error) {
	snap := ctx.Snapshot
	if q < 0 {
		return domain.ResourceChangePlan{}, fmt.Errorf("%w: negative release quantity", ErrIllegalTransition)
	}
	newUncertain, err := domain.SubUnits(snap.Uncertain, q)
	if err != nil || newUncertain < 0 {
		return domain.ResourceChangePlan{}, fmt.Errorf(
			"%w: releasing %d with %d uncertain on %s", ErrIllegalTransition, int64(q), int64(snap.Uncertain), snap.ResourceID)
	}
	delta := domain.ResourceDelta{
		ResourceID:      snap.ResourceID,
		WindowKey:       snap.WindowKey,
		ExpectedVersion: snap.Version,
		Uncertain:       -q,
	}
	return domain.ResourceChangePlan{Admitted: true, Deltas: []domain.ResourceDelta{delta}}, nil
}
