package resources

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"ant/server/internal/domain"
)

func counterSnap() domain.ResourceSnapshot {
	return domain.ResourceSnapshot{
		ResourceID: "res-1", WindowKey: "w-1", Kind: "counter",
		Version: 7, Capacity: 100, Committed: 30, Adjustment: -10, Reserved: 10,
	}
}

func observation() *domain.ObservationEvidence {
	return &domain.ObservationEvidence{
		ID: "obs-1", ResourceID: "res-1", WindowKey: "w-1",
		Remaining:                50,
		CommittedAtStart:         25,
		AdjustmentVersionAtStart: 2,
		ObservedAt:               domain.Millis(1000),
		ExpiresAt:                domain.Millis(2000),
	}
}

func TestCounterAvailableLocalBaselineOnly(t *testing.T) {
	// local = capacity - (committed + adjustment) - reserved = 100 - 20 - 10
	got, err := CounterAvailable(counterSnap(), nil, domain.Millis(1500), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 70 {
		t.Fatalf("want 70, got %d", int64(got))
	}
}

func TestCounterAvailableWithValidObservation(t *testing.T) {
	// provider = remaining - max(0, committed - committedAtStart) - reserved
	//          = 50 - 5 - 10 = 35; local = 70; available = min = 35.
	got, err := CounterAvailable(counterSnap(), observation(), domain.Millis(1500), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 35 {
		t.Fatalf("want 35, got %d", int64(got))
	}
}

func TestCounterAvailableInvalidObservationsIgnored(t *testing.T) {
	now := domain.Millis(1500)
	cases := []struct {
		name string
		mut  func(*domain.ObservationEvidence)
	}{
		{"wrong resource", func(o *domain.ObservationEvidence) { o.ResourceID = "res-other" }},
		{"wrong window", func(o *domain.ObservationEvidence) { o.WindowKey = "w-old" }},
		{"expired", func(o *domain.ObservationEvidence) { o.ExpiresAt = domain.Millis(1499) }},
		{"stale adjustment version", func(o *domain.ObservationEvidence) { o.AdjustmentVersionAtStart = 1 }},
	}
	for _, tc := range cases {
		obs := observation()
		tc.mut(obs)
		got, err := CounterAvailable(counterSnap(), obs, now, 2)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if got != 70 {
			t.Fatalf("%s: invalid observation must be ignored (want 70), got %d", tc.name, int64(got))
		}
	}
	// Expiry boundary is inclusive: now == ExpiresAt is still valid.
	got, err := CounterAvailable(counterSnap(), observation(), domain.Millis(2000), 2)
	if err != nil || got != 35 {
		t.Fatalf("expiry boundary must stay valid, got %d %v", int64(got), err)
	}
}

func TestCounterReserveAdmitsAndProposesDelta(t *testing.T) {
	ctx := domain.ResourceContext{Now: domain.Millis(1500), Snapshot: counterSnap(), Observation: observation(), AdjustmentVersion: 2}
	plan, err := CounterReserve(ctx, 30) // available under the observation is 35
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !plan.Admitted || len(plan.Deltas) != 1 {
		t.Fatalf("want admitted plan with one delta, got %+v", plan)
	}
	want := domain.ResourceDelta{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 7, Reserved: 30}
	if !reflect.DeepEqual(plan.Deltas[0], want) {
		t.Fatalf("want delta %+v, got %+v", want, plan.Deltas[0])
	}
	if len(plan.Reasons) != 0 {
		t.Fatalf("admitted plan must not carry reasons, got %v", plan.Reasons)
	}
}

// TestCounterReserveStaleObservationIgnored is the StaleObservationInput
// audit probe scenario: authoritative version 2, observation captured at
// version 1. The observation must be IGNORED (local baseline 75 available),
// not self-compared into a wrongful refusal.
func TestCounterReserveStaleObservationIgnored(t *testing.T) {
	snap := domain.ResourceSnapshot{
		ResourceID: "res-1", WindowKey: "w-1", Kind: "counter",
		Version: 3, Capacity: 100, Committed: 30, Adjustment: -10, Reserved: 5,
	}
	stale := &domain.ObservationEvidence{
		ResourceID: "res-1", WindowKey: "w-1",
		Remaining:                5, // would drag available to 0 if (wrongly) trusted
		CommittedAtStart:         30,
		AdjustmentVersionAtStart: 1,
		ObservedAt:               domain.Millis(100),
		ExpiresAt:                domain.Millis(2000),
	}
	available, err := CounterAvailable(snap, stale, domain.Millis(1000), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if available != 75 {
		t.Fatalf("stale observation must be ignored: want available 75, got %d", int64(available))
	}
	ctx := domain.ResourceContext{Now: domain.Millis(1000), Snapshot: snap, Observation: stale, AdjustmentVersion: 2}
	plan, err := CounterReserve(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !plan.Admitted || len(plan.Deltas) != 1 || plan.Deltas[0].Reserved != 1 {
		t.Fatalf("reserve of 1 unit must be admitted on stale observation, got %+v", plan)
	}
}

func TestCounterReserveRefusals(t *testing.T) {
	base := domain.ResourceContext{Now: domain.Millis(1500), Snapshot: counterSnap(), Observation: observation(), AdjustmentVersion: 2}
	cases := []struct {
		name      string
		ctx       domain.ResourceContext
		q         domain.Units
		wantGroup string
		wantCode  string
	}{
		{"negative quantity", base, -1, "admission", "invalid_quantity"},
		{"insufficient capacity", base, 36, "capacity", "capacity_exhausted"},
		{"scoped hold", func() domain.ResourceContext {
			c := base
			c.HoldIDs = []domain.ID{"hold-1"}
			return c
		}(), 5, "admission", "scoped_hold"},
		{"attribution encumbrance", func() domain.ResourceContext {
			c := base
			c.Observation = nil
			c.AttributionEncumbranceIDs = []domain.ID{"enc-1"}
			return c
		}(), 71, "admission", "scoped_hold"},
	}
	for _, tc := range cases {
		plan, err := CounterReserve(tc.ctx, tc.q)
		if err != nil {
			t.Fatalf("%s: refusal must not be an error, got %v", tc.name, err)
		}
		if plan.Admitted || len(plan.Deltas) != 0 {
			t.Fatalf("%s: want refused plan without deltas, got %+v", tc.name, plan)
		}
		if len(plan.Reasons) != 1 || plan.Reasons[0].Group != tc.wantGroup || plan.Reasons[0].Code != tc.wantCode {
			t.Fatalf("%s: want %s/%s, got %v", tc.name, tc.wantGroup, tc.wantCode, plan.Reasons)
		}
		if plan.Reasons[0].ScopeRef != "res-1" {
			t.Fatalf("%s: reason must scope the resource, got %v", tc.name, plan.Reasons[0])
		}
	}
	// Boundary: q == available is admissible.
	plan, err := CounterReserve(base, 35)
	if err != nil || !plan.Admitted {
		t.Fatalf("q == available must be admissible, got %+v %v", plan, err)
	}
}

func settleCtx() (domain.ResourceContext, domain.ReservationState) {
	snap := counterSnap()
	snap.Reserved = 30
	ctx := domain.ResourceContext{Snapshot: snap}
	res := domain.ReservationState{
		ReservationAllocation: domain.ReservationAllocation{ResourceID: "res-1", WindowKey: "w-1", Units: 30, Kind: "counter"},
		ReservationID:         "r1",
	}
	return ctx, res
}

func TestCounterSettleKnownActual(t *testing.T) {
	ctx, res := settleCtx()
	actual := domain.Units(20)
	plan, err := CounterSettle(ctx, res, domain.SettlementProjection{End: domain.EndProofExternalEnded, Actual: &actual, Attribution: domain.AttributionKnown}, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := domain.ResourceDelta{ResourceID: "res-1", WindowKey: "w-1", ExpectedVersion: 7, Reserved: -30, Committed: 20}
	if len(plan.Deltas) != 1 || !reflect.DeepEqual(plan.Deltas[0], want) {
		t.Fatalf("want %+v, got %+v", want, plan.Deltas)
	}
	if len(plan.Reasons) != 0 {
		t.Fatalf("exact settle must not carry reasons, got %v", plan.Reasons)
	}
	if len(plan.IsolationRequests) != 0 {
		t.Fatalf("attributed settle must not request isolation, got %+v", plan.IsolationRequests)
	}
}

func TestCounterSettleOverrunRecordedNotTruncated(t *testing.T) {
	ctx, res := settleCtx()
	actual := domain.Units(35) // exceeds the 30 bound
	plan, err := CounterSettle(ctx, res, domain.SettlementProjection{End: domain.EndProofExternalEnded, Actual: &actual, Attribution: domain.AttributionKnown}, 30)
	if err != nil {
		t.Fatalf("overrun is a legal fact path: %v", err)
	}
	if plan.Deltas[0].Committed != 35 {
		t.Fatalf("overrun must be recorded in full, got %d", int64(plan.Deltas[0].Committed))
	}
	if len(plan.Reasons) != 1 || plan.Reasons[0].Group != "overrun" || plan.Reasons[0].Code != "actual_exceeded_reservation" {
		t.Fatalf("want overrun reason, got %v", plan.Reasons)
	}
}

func TestCounterSettleUnknownActualConservativeBound(t *testing.T) {
	ctx, res := settleCtx()
	// Verified external end with unknown actual: charge the bound (§5.1).
	proj := domain.SettlementProjection{End: domain.EndProofExternalEnded, Attribution: domain.AttributionKnown}
	plan, err := CounterSettle(ctx, res, proj, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Deltas[0].Committed != 30 || plan.Deltas[0].Reserved != -30 {
		t.Fatalf("conservative bound expected, got %+v", plan.Deltas[0])
	}
	if len(plan.IsolationRequests) != 0 {
		t.Fatalf("attributed settle must not request isolation, got %+v", plan.IsolationRequests)
	}
	// EndProofNone carries no end claim — conservation still applies and the
	// bound is accounted rather than silently reopening capacity.
	proj = domain.SettlementProjection{End: domain.EndProofNone, Attribution: domain.AttributionKnown}
	plan, err = CounterSettle(ctx, res, proj, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Deltas[0].Committed != 30 {
		t.Fatalf("EndProofNone settle must conserve the bound, got %+v", plan.Deltas[0])
	}
}

// R11: EvidenceRefs only document a proof — they never constitute the end
// fact or the window attribution.
func TestCounterSettleEvidenceRefsProveNothing(t *testing.T) {
	ctx, res := settleCtx()
	proj := domain.SettlementProjection{
		End:          domain.EndProofNone,
		Attribution:  domain.AttributionUnknown,
		EvidenceRefs: []domain.ID{"ev-1", "ev-2"}, // arbitrary evidence IDs
	}
	plan, err := CounterSettle(ctx, res, proj, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The charge is conservative AND the plan must demand attribution
	// isolation — evidence IDs changed nothing about either fact.
	if plan.Deltas[0].Committed != 30 || plan.Deltas[0].Reserved != -30 {
		t.Fatalf("conservative bound expected, got %+v", plan.Deltas[0])
	}
	if len(plan.IsolationRequests) != 1 {
		t.Fatalf("unattributed charge must carry exactly one isolation request, got %+v", plan.IsolationRequests)
	}
}

// R11: an unattributed charge settles conservatively but MUST carry an
// isolation request (resource + empty window key + charged quantity +
// reason); releasing the reservation while silently reopening capacity is
// forbidden. AttributionKnown never carries one.
func TestCounterSettleUnattributedRequiresIsolation(t *testing.T) {
	ctx, res := settleCtx()

	// Unattributed bound charge (end fact present).
	proj := domain.SettlementProjection{End: domain.EndProofExternalEnded, Attribution: domain.AttributionUnknown}
	plan, err := CounterSettle(ctx, res, proj, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Deltas[0].Committed != 30 {
		t.Fatalf("unattributed settle must still charge conservatively, got %+v", plan.Deltas[0])
	}
	if len(plan.IsolationRequests) != 1 {
		t.Fatalf("want exactly one isolation request, got %+v", plan.IsolationRequests)
	}
	req := plan.IsolationRequests[0]
	wantReq := domain.AttributionIsolation{
		ResourceID: "res-1",
		WindowKey:  "", // empty: cross-window until ownership is proven
		Units:      30,
		Reason:     domain.Reason{Group: "attribution", Code: "window_attribution_unverified", ScopeRef: "res-1"},
	}
	if !reflect.DeepEqual(req, wantReq) {
		t.Fatalf("want isolation request %+v, got %+v", wantReq, req)
	}
	if len(plan.Reasons) == 0 || plan.Reasons[len(plan.Reasons)-1].Code != "window_attribution_unverified" {
		t.Fatalf("unattributed settle must surface the attribution reason, got %v", plan.Reasons)
	}

	// Unattributed known actual: the actual quantity is what gets isolated.
	actual := domain.Units(20)
	plan, err = CounterSettle(ctx, res, domain.SettlementProjection{End: domain.EndProofExternalEnded, Actual: &actual, Attribution: domain.AttributionUnknown}, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.IsolationRequests) != 1 || plan.IsolationRequests[0].Units != 20 {
		t.Fatalf("isolation must cover the charged actual, got %+v", plan.IsolationRequests)
	}

	// Reliable unsent: provably zero usage — release without charge and
	// without isolation.
	proj = domain.SettlementProjection{End: domain.EndProofReliableUnsent, Attribution: domain.AttributionUnknown}
	plan, err = CounterSettle(ctx, res, proj, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Deltas[0].Committed != 0 || plan.Deltas[0].Reserved != -30 {
		t.Fatalf("reliable unsent must release without charging, got %+v", plan.Deltas[0])
	}
	if len(plan.IsolationRequests) != 0 {
		t.Fatalf("zero-usage release must not request isolation, got %+v", plan.IsolationRequests)
	}
}

func TestCounterSettleIllegalCalls(t *testing.T) {
	ctx, res := settleCtx()
	wrongRes := domain.ReservationState{ReservationAllocation: domain.ReservationAllocation{ResourceID: "res-x", WindowKey: "w-1", Kind: "counter"}, ReservationID: "r1"}
	if _, err := CounterSettle(ctx, wrongRes, domain.SettlementProjection{Attribution: domain.AttributionKnown}, 1); err == nil {
		t.Fatal("identity mismatch must error")
	}
	negative := domain.Units(-5)
	if _, err := CounterSettle(ctx, res, domain.SettlementProjection{Actual: &negative, Attribution: domain.AttributionKnown}, 1); err == nil {
		t.Fatal("negative actual must error (use adjustment)")
	}
	if _, err := CounterSettle(ctx, res, domain.SettlementProjection{Attribution: domain.AttributionKnown}, 31); err == nil {
		t.Fatal("bound exceeding reserved must error")
	}
	// Undefined fact values must error: zero values are not facts (R11).
	if _, err := CounterSettle(ctx, res, domain.SettlementProjection{End: "made-up", Attribution: domain.AttributionKnown}, 1); err == nil {
		t.Fatal("undefined end proof kind must error")
	}
	if _, err := CounterSettle(ctx, res, domain.SettlementProjection{End: domain.EndProofNone}, 1); err == nil {
		t.Fatal("unset attribution must error; callers must state facts explicitly")
	}
	// Contradictory fact: provably unsent request with non-zero usage.
	nonzero := domain.Units(5)
	if _, err := CounterSettle(ctx, res, domain.SettlementProjection{End: domain.EndProofReliableUnsent, Actual: &nonzero, Attribution: domain.AttributionKnown}, 1); err == nil {
		t.Fatal("reliable unsent with non-zero actual must error")
	}
	// Released reserved never goes negative via the delta.
	if _, err := CounterSettle(ctx, res, domain.SettlementProjection{End: domain.EndProofExternalEnded, Attribution: domain.AttributionKnown}, 30); err != nil {
		t.Fatalf("exact bound must settle: %v", err)
	}
}

func TestCounterAvailableOverflowRejected(t *testing.T) {
	snap := counterSnap()
	snap.Capacity = domain.Units(math.MaxInt64)
	snap.Committed = 1
	snap.Adjustment = domain.Units(math.MaxInt64) // 1 + MaxInt64 overflows
	if _, err := CounterAvailable(snap, nil, 0, 0); !errors.Is(err, domain.ErrQuantityRange) {
		t.Fatalf("overflow must surface as ErrQuantityRange, got %v", err)
	}
}
