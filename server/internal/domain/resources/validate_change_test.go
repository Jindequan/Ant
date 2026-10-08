package resources

import (
	"errors"
	"math"
	"testing"

	"ant/server/internal/domain"
)

func validateSnap() domain.ResourceSnapshot {
	return domain.ResourceSnapshot{
		ResourceID: "res-1", WindowKey: "w-1", Kind: "counter",
		Version: 7, Capacity: 100, Committed: 30, Adjustment: -10,
		Reserved: 10, Active: 2, Uncertain: 1,
	}
}

func TestValidateChangeAcceptsWellFormedDelta(t *testing.T) {
	delta := domain.ResourceDelta{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 7, Reserved: 5}
	if err := ValidateChange(validateSnap(), delta); err != nil {
		t.Fatalf("well-formed delta rejected: %v", err)
	}
	// A no-op delta is structurally fine.
	if err := ValidateChange(validateSnap(), domain.ResourceDelta{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 7}); err != nil {
		t.Fatalf("empty delta rejected: %v", err)
	}
}

func TestValidateChangeRejectsWrongIdentity(t *testing.T) {
	snap := validateSnap()
	wrongResource := domain.ResourceDelta{ResourceID: "res-2", WindowKey: "w-1", ExpectedVersion: 7}
	if err := ValidateChange(snap, wrongResource); !errors.Is(err, ErrResourceIdentity) {
		t.Fatalf("want ErrResourceIdentity, got %v", err)
	}
	wrongWindow := domain.ResourceDelta{ResourceID: "res-1", WindowKey: "w-2", ExpectedVersion: 7}
	if err := ValidateChange(snap, wrongWindow); !errors.Is(err, ErrResourceIdentity) {
		t.Fatalf("want ErrResourceIdentity for window, got %v", err)
	}
}

func TestValidateChangeRejectsStaleVersion(t *testing.T) {
	delta := domain.ResourceDelta{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 6, Reserved: 5}
	if err := ValidateChange(validateSnap(), delta); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("want ErrStaleVersion, got %v", err)
	}
}

func TestValidateChangeRejectsNegativeComponents(t *testing.T) {
	snap := validateSnap()
	cases := []struct {
		name  string
		delta domain.ResourceDelta
	}{
		{"negative reserved (capacity minting)", domain.ResourceDelta{ExpectedVersion: 7, Reserved: -11}},
		{"negative active", domain.ResourceDelta{ExpectedVersion: 7, Active: -3}},
		{"negative uncertain", domain.ResourceDelta{ExpectedVersion: 7, Uncertain: -2}},
		{"negative committed watermark", domain.ResourceDelta{ExpectedVersion: 7, Committed: -31}},
	}
	for _, tc := range cases {
		tc.delta.ResourceID = "res-1"
		tc.delta.WindowKey = "w-1"
		if err := ValidateChange(snap, tc.delta); !errors.Is(err, ErrNegativeComponent) {
			t.Fatalf("%s: want ErrNegativeComponent, got %v", tc.name, err)
		}
	}
}

func TestValidateChangeAdjustmentRules(t *testing.T) {
	snap := validateSnap() // committed 30, adjustment -10 → net 20
	// Deeper negative adjustment is fine while net stays >= 0.
	ok := domain.ResourceDelta{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 7, Adjustment: -15}
	if err := ValidateChange(snap, ok); err != nil {
		t.Fatalf("adjustment to net 5 must pass: %v", err)
	}
	// Net below zero would mint allowance out of nothing.
	bad := domain.ResourceDelta{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 7, Adjustment: -25}
	if err := ValidateChange(snap, bad); !errors.Is(err, ErrNetCommittedNegative) {
		t.Fatalf("want ErrNetCommittedNegative, got %v", err)
	}
	// Positive adjustment (correction upward) passes.
	up := domain.ResourceDelta{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 7, Adjustment: 5}
	if err := ValidateChange(snap, up); err != nil {
		t.Fatalf("positive adjustment must pass: %v", err)
	}
}

func TestValidateChangeRejectsOverflow(t *testing.T) {
	snap := validateSnap()
	snap.Committed = domain.Units(math.MaxInt64)
	delta := domain.ResourceDelta{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 7, Committed: 1}
	err := ValidateChange(snap, delta)
	if !errors.Is(err, domain.ErrQuantityRange) {
		t.Fatalf("overflow must surface as ErrQuantityRange, got %v", err)
	}
}

func TestValidateChangePlanRules(t *testing.T) {
	snap := validateSnap()
	res2 := validateSnap()
	res2.ResourceID = "res-2"
	snapshots := map[domain.ID]domain.ResourceSnapshot{"res-1": snap, "res-2": res2}

	good := domain.ResourceChangePlan{Admitted: true, Deltas: []domain.ResourceDelta{
		{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 7, Reserved: 5},
		{ResourceID: "res-2", WindowKey: "w-1", ExpectedVersion: 7, Reserved: 1},
	}}
	if err := ValidateChangePlan(snapshots, good); err != nil {
		t.Fatalf("multi-resource plan rejected: %v", err)
	}

	// Admitted plan with no deltas is valid.
	if err := ValidateChangePlan(snapshots, domain.ResourceChangePlan{Admitted: true}); err != nil {
		t.Fatalf("empty admitted plan rejected: %v", err)
	}

	// Refused plan must not carry deltas.
	refused := domain.ResourceChangePlan{Admitted: false, Deltas: good.Deltas}
	if err := ValidateChangePlan(snapshots, refused); !errors.Is(err, ErrRejectedWithDeltas) {
		t.Fatalf("want ErrRejectedWithDeltas, got %v", err)
	}

	// Unknown resource row.
	unknown := domain.ResourceChangePlan{Admitted: true, Deltas: []domain.ResourceDelta{
		{ResourceID: "res-x", WindowKey: "w-1", ExpectedVersion: 7},
	}}
	if err := ValidateChangePlan(snapshots, unknown); !errors.Is(err, ErrUnknownResource) {
		t.Fatalf("want ErrUnknownResource, got %v", err)
	}

	// Duplicate resource rows.
	dup := domain.ResourceChangePlan{Admitted: true, Deltas: []domain.ResourceDelta{
		{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 7, Reserved: 1},
		{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 7, Reserved: 2},
	}}
	if err := ValidateChangePlan(snapshots, dup); !errors.Is(err, ErrDuplicateResource) {
		t.Fatalf("want ErrDuplicateResource, got %v", err)
	}
}

// R11/I06: attribution isolation requests must satisfy their shape contract;
// refused plans must not carry them at all.
func TestValidateChangePlanIsolationRequests(t *testing.T) {
	snapshots := map[domain.ID]domain.ResourceSnapshot{"res-1": validateSnap()}
	validReq := domain.AttributionIsolation{
		ResourceID: "res-1",
		WindowKey:  "", // empty: cross-window until ownership is proven
		Units:      30,
		Reason:     domain.Reason{Group: "attribution", Code: "window_attribution_unverified", ScopeRef: "res-1"},
	}

	// A well-formed request passes (with an unrelated admitted delta).
	good := domain.ResourceChangePlan{Admitted: true,
		Deltas:            []domain.ResourceDelta{{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 7, Reserved: -5, Committed: 30}},
		IsolationRequests: []domain.AttributionIsolation{validReq},
	}
	if err := ValidateChangePlan(snapshots, good); err != nil {
		t.Fatalf("well-formed isolation request rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*domain.AttributionIsolation)
	}{
		{"unknown resource", func(r *domain.AttributionIsolation) { r.ResourceID = "res-x" }},
		{"non-empty window key", func(r *domain.AttributionIsolation) { r.WindowKey = "w-1" }},
		{"zero units", func(r *domain.AttributionIsolation) { r.Units = 0 }},
		{"negative units", func(r *domain.AttributionIsolation) { r.Units = -5 }},
		{"missing reason", func(r *domain.AttributionIsolation) { r.Reason = domain.Reason{} }},
		{"empty resource id", func(r *domain.AttributionIsolation) { r.ResourceID = "" }},
	}
	for _, tc := range cases {
		req := validReq
		tc.mut(&req)
		plan := domain.ResourceChangePlan{Admitted: true, IsolationRequests: []domain.AttributionIsolation{req}}
		if err := ValidateChangePlan(snapshots, plan); !errors.Is(err, ErrBadIsolationRequest) {
			t.Fatalf("%s: want ErrBadIsolationRequest, got %v", tc.name, err)
		}
	}

	// A refused plan must not carry isolation requests either.
	refused := domain.ResourceChangePlan{Admitted: false, IsolationRequests: []domain.AttributionIsolation{validReq}}
	if err := ValidateChangePlan(snapshots, refused); !errors.Is(err, ErrRejectedWithDeltas) {
		t.Fatalf("refused plan with isolation requests: want ErrRejectedWithDeltas, got %v", err)
	}
}
