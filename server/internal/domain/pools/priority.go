// Package pools implements the pure user-pool scheduling rules of
// docs/design/07-POOLS-AND-CONFIG.md §3: hard eligibility filtering and the
// frozen K4 ordering. Pure functions only — no clock, network, SQL or global
// state; the caller (compiler/router) supplies verified config projections.
package pools

import (
	"errors"
	"fmt"
	"sort"

	"ant/server/internal/domain"
)

// MaxPriority is the inclusive upper bound of the member priority range
// frozen for manual reordering ([0, 2147483647]; the same range the UI's
// numeric editor validates). Priorities are never normalized: equal values
// are legal and tie-break by offerID at scheduling time.
const MaxPriority int64 = 2147483647

var (
	// ErrReorderCoverage: every current member must appear exactly once in
	// the submitted group — missing, duplicated or unknown offers all fail.
	ErrReorderCoverage = errors.New("pools: reorder entries must cover every current member exactly once")
	// ErrPriorityRange: priority outside [0, MaxPriority].
	ErrPriorityRange = errors.New("pools: priority out of range")
	// ErrEmptyEntries: submitting an empty group for a non-empty pool would
	// silently delete members; use the dedicated removal flow instead.
	ErrEmptyEntries = errors.New("pools: empty reorder entries for non-empty pool")
)

// ValidateReorderEntries validates one full-group reorder submission (K4
// authoritative write path, 07 §4) and returns the updated member slice:
//   - every current member appears exactly once in entries; unknown offers,
//     duplicates and missing members are errors;
//   - priority must lie in [0, MaxPriority];
//   - Enabled and Version keep their stored values; only Priority updates;
//   - the returned slice preserves the current member order (call SortMembers
//     to materialize the scheduling order);
//   - empty entries with a non-empty pool is an error; both empty returns an
//     empty slice.
//
// Priorities are stored exactly as submitted: two entries with priority 7
// stay 7/7 (no 10/20/30 renormalization — that is a UI presentation choice).
func ValidateReorderEntries(current []domain.PoolMember, entries []domain.ReorderPoolEntry) ([]domain.PoolMember, error) {
	if len(current) == 0 && len(entries) == 0 {
		return []domain.PoolMember{}, nil
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: %d members on pool", ErrEmptyEntries, len(current))
	}
	byID := make(map[domain.ID]domain.PoolMember, len(current))
	for _, m := range current {
		if _, dup := byID[m.OfferID]; dup {
			return nil, fmt.Errorf("%w: current pool has duplicate member %q", ErrReorderCoverage, m.OfferID)
		}
		byID[m.OfferID] = m
	}
	priority := make(map[domain.ID]int64, len(entries))
	seen := make(map[domain.ID]bool, len(entries))
	for _, e := range entries {
		if e.Priority < 0 || e.Priority > MaxPriority {
			return nil, fmt.Errorf("%w: %d for offer %q", ErrPriorityRange, e.Priority, e.OfferID)
		}
		if seen[e.OfferID] {
			return nil, fmt.Errorf("%w: duplicate entry for offer %q", ErrReorderCoverage, e.OfferID)
		}
		seen[e.OfferID] = true
		if _, ok := byID[e.OfferID]; !ok {
			return nil, fmt.Errorf("%w: unknown offer %q", ErrReorderCoverage, e.OfferID)
		}
		priority[e.OfferID] = e.Priority
	}
	out := make([]domain.PoolMember, 0, len(current))
	for _, m := range current {
		if !seen[m.OfferID] {
			return nil, fmt.Errorf("%w: missing entry for offer %q", ErrReorderCoverage, m.OfferID)
		}
		m.Priority = priority[m.OfferID]
		out = append(out, m)
	}
	return out, nil
}

// SortMembers orders members in place with the scheduling key frozen in K4:
// priority ascending, offerID ascending, stable for fully equal keys. It is
// the same comparison RankCandidates applies to compiled candidates.
func SortMembers(members []domain.PoolMember) {
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].Priority != members[j].Priority {
			return members[i].Priority < members[j].Priority
		}
		return members[i].OfferID < members[j].OfferID
	})
}
