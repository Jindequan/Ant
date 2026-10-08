package resources

import (
	"errors"
	"fmt"

	"ant/server/internal/domain"
)

// Change-plan validation (04-RESOURCE-ALGORITHMS §7; structural part of the
// I01–I10 invariants that a pure rule can decide). The Ledger re-checks the
// full contract (evidence, event IDs, holds, transition legality) before
// commit; this file is the shared structural gate both sides agree on.

var (
	// ErrResourceIdentity: the delta targets a resource/window other than the
	// snapshot it is validated against (I01).
	ErrResourceIdentity = errors.New("validate: delta resource identity does not match snapshot")
	// ErrStaleVersion: ExpectedVersion does not match the snapshot version (I03).
	ErrStaleVersion = errors.New("validate: delta version does not match snapshot")
	// ErrNegativeComponent: a set value would go negative, e.g. a negative
	// reservation used to mint capacity (I02/I05).
	ErrNegativeComponent = errors.New("validate: component would become negative")
	// ErrNetCommittedNegative: committed+adjustment would go below zero, i.e.
	// a negative adjustment minting allowance out of nothing (I02).
	ErrNetCommittedNegative = errors.New("validate: committed+adjustment would become negative")
	// ErrDuplicateResource: the plan touches one resource in two rows.
	ErrDuplicateResource = errors.New("validate: plan contains duplicate resource rows")
	// ErrRejectedWithDeltas: a refused admission must not carry any delta (I04).
	ErrRejectedWithDeltas = errors.New("validate: rejected plan must not carry deltas")
	// ErrUnknownResource: no snapshot was supplied for a delta's resource.
	ErrUnknownResource = errors.New("validate: plan references unknown resource")
	// ErrBadIsolationRequest: an AttributionIsolation row violates its
	// contract (R11/I06): isolation targets must reference a known resource,
	// carry an EMPTY window key (cross-window until ownership is proven), a
	// positive quantity and a stated reason.
	ErrBadIsolationRequest = errors.New("validate: malformed attribution isolation request")
)

// ValidateChange re-derives one delta against its snapshot:
//   - resourceID/windowKey identity must match (I01);
//   - ExpectedVersion must equal snapshot.Version (I03);
//   - every component is summed with checked arithmetic; reserved/active/
//     uncertain and the committed watermark must stay non-negative (I02/I05);
//   - negative adjustment is allowed, but the net committed+adjustment must
//     stay >= 0: corrections reduce already-recorded consumption, they never
//     mint allowance (I02). Capacity is absent from ResourceDelta by design —
//     a change plan cannot create capacity; the non-negative set checks above
//     are the structural guard for that.
//
// Overflow anywhere is an error (wrapped domain.ErrQuantityRange), never a
// wrap-around.
func ValidateChange(snap domain.ResourceSnapshot, delta domain.ResourceDelta) error {
	if delta.ResourceID != snap.ResourceID || delta.WindowKey != snap.WindowKey {
		return fmt.Errorf("%w: delta %s/%s vs snapshot %s/%s",
			ErrResourceIdentity, delta.ResourceID, delta.WindowKey, snap.ResourceID, snap.WindowKey)
	}
	if delta.ExpectedVersion != snap.Version {
		return fmt.Errorf("%w: delta %d vs snapshot %d", ErrStaleVersion, delta.ExpectedVersion, snap.Version)
	}
	newCommitted, err := domain.AddUnits(snap.Committed, delta.Committed)
	if err != nil {
		return fmt.Errorf("validate: committed overflow on %s: %w", snap.ResourceID, err)
	}
	if newCommitted < 0 {
		return fmt.Errorf("%w: committed %d %+d on %s", ErrNegativeComponent, int64(snap.Committed), int64(delta.Committed), snap.ResourceID)
	}
	newAdjustment, err := domain.AddUnits(snap.Adjustment, delta.Adjustment)
	if err != nil {
		return fmt.Errorf("validate: adjustment overflow on %s: %w", snap.ResourceID, err)
	}
	// Net accounting level may not go negative; the (unenforced) tighter rule
	// "adjustment total >= -committed" is deliberately left to evidence.
	netCommitted, err := domain.AddUnits(newCommitted, newAdjustment)
	if err != nil {
		return fmt.Errorf("validate: committed+adjustment overflow on %s: %w", snap.ResourceID, err)
	}
	if netCommitted < 0 {
		return fmt.Errorf("%w: net %d on %s", ErrNetCommittedNegative, int64(netCommitted), snap.ResourceID)
	}
	for _, c := range []struct {
		name    string
		current domain.Units
		change  domain.Units
	}{
		{"reserved", snap.Reserved, delta.Reserved},
		{"active", snap.Active, delta.Active},
		{"uncertain", snap.Uncertain, delta.Uncertain},
	} {
		next, err := domain.AddUnits(c.current, c.change)
		if err != nil {
			return fmt.Errorf("validate: %s overflow on %s: %w", c.name, snap.ResourceID, err)
		}
		if next < 0 {
			return fmt.Errorf("%w: %s %d %+d on %s", ErrNegativeComponent, c.name, int64(c.current), int64(c.change), snap.ResourceID)
		}
	}
	return nil
}

// ValidateChangePlan validates a whole plan: a refused plan carries no
// deltas and no isolation requests, every delta has a supplied snapshot
// (keyed by resourceID), no resource appears twice, each delta passes
// ValidateChange, and every attribution isolation request satisfies its
// shape contract (known resource, empty window key, positive units, stated
// reason — R11/I06). The Ledger re-checks that an unattributed charge is
// actually covered by a request; this structural gate only rejects rows that
// could not be applied as written. An admitted plan with zero deltas is
// valid (e.g. a pure diagnostic admission).
func ValidateChangePlan(snapshots map[domain.ID]domain.ResourceSnapshot, plan domain.ResourceChangePlan) error {
	if !plan.Admitted && (len(plan.Deltas) > 0 || len(plan.IsolationRequests) > 0) {
		return fmt.Errorf("%w: %d deltas, %d isolation requests on refused plan",
			ErrRejectedWithDeltas, len(plan.Deltas), len(plan.IsolationRequests))
	}
	seen := make(map[domain.ID]bool, len(plan.Deltas))
	for _, d := range plan.Deltas {
		if seen[d.ResourceID] {
			return fmt.Errorf("%w: %s", ErrDuplicateResource, d.ResourceID)
		}
		seen[d.ResourceID] = true
		snap, ok := snapshots[d.ResourceID]
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownResource, d.ResourceID)
		}
		if err := ValidateChange(snap, d); err != nil {
			return err
		}
	}
	for _, req := range plan.IsolationRequests {
		switch {
		case req.ResourceID == "":
			return fmt.Errorf("%w: empty resource id", ErrBadIsolationRequest)
		case req.WindowKey != "":
			return fmt.Errorf("%w: window key %q must be empty (cross-window isolation)", ErrBadIsolationRequest, req.WindowKey)
		case req.Units <= 0:
			return fmt.Errorf("%w: non-positive units %d", ErrBadIsolationRequest, int64(req.Units))
		case req.Reason.Code == "":
			return fmt.Errorf("%w: missing reason code", ErrBadIsolationRequest)
		}
		if _, ok := snapshots[req.ResourceID]; !ok {
			return fmt.Errorf("%w: unknown resource %s", ErrBadIsolationRequest, req.ResourceID)
		}
	}
	return nil
}
