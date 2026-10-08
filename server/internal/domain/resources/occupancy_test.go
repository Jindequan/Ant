package resources

import (
	"errors"
	"testing"

	"ant/server/internal/domain"
)

func occupancyCtx() domain.ResourceContext {
	return domain.ResourceContext{Snapshot: domain.ResourceSnapshot{
		ResourceID: "slot-1", WindowKey: "w-1", Kind: "occupancy",
		Version: 3, Capacity: 3,
	}}
}

func oneDelta(t *testing.T, plan domain.ResourceChangePlan) domain.ResourceDelta {
	t.Helper()
	if len(plan.Deltas) != 1 {
		t.Fatalf("want exactly one delta, got %+v", plan)
	}
	return plan.Deltas[0]
}

func TestOccupancyNormalLifecycle(t *testing.T) {
	ctx := occupancyCtx()

	// prepare: reserved += 1
	plan, err := OccupancyReserve(ctx, 1)
	if err != nil || !plan.Admitted {
		t.Fatalf("reserve: %+v %v", plan, err)
	}
	if d := oneDelta(t, plan); d.Reserved != 1 || d.ExpectedVersion != 3 {
		t.Fatalf("reserve delta wrong: %+v", d)
	}

	// authorize: reserved -= 1; active += 1
	snap := ctx.Snapshot
	snap.Reserved = 1
	plan, err = OccupancyActivate(domain.ResourceContext{Snapshot: snap}, 1)
	if err != nil || !plan.Admitted {
		t.Fatalf("activate: %+v %v", plan, err)
	}
	if d := oneDelta(t, plan); d.Reserved != -1 || d.Active != 1 {
		t.Fatalf("activate delta wrong: %+v", d)
	}

	// proved external end: active -= 1
	snap = ctx.Snapshot
	snap.Active = 1
	plan, err = OccupancyEndProved(domain.ResourceContext{Snapshot: snap}, 1)
	if err != nil || !plan.Admitted {
		t.Fatalf("endProved: %+v %v", plan, err)
	}
	if d := oneDelta(t, plan); d.Active != -1 {
		t.Fatalf("endProved delta wrong: %+v", d)
	}
}

func TestOccupancyUnknownKeepsSlotUntilProof(t *testing.T) {
	ctx := occupancyCtx()
	snap := ctx.Snapshot
	snap.Active = 1

	// response lost: active -= 1; uncertain += 1
	plan, err := OccupancyToUncertain(domain.ResourceContext{Snapshot: snap}, 1)
	if err != nil || !plan.Admitted {
		t.Fatalf("toUncertain: %+v %v", plan, err)
	}
	if d := oneDelta(t, plan); d.Active != -1 || d.Uncertain != 1 {
		t.Fatalf("toUncertain delta wrong: %+v", d)
	}

	// uncertain slots are not reusable: 3 capacity - 1 uncertain = 2
	uncertainSnap := ctx.Snapshot
	uncertainSnap.Uncertain = 1
	plan, err = OccupancyReserve(domain.ResourceContext{Snapshot: uncertainSnap}, 2)
	if err != nil || !plan.Admitted {
		t.Fatalf("two remaining slots must be reservable: %+v %v", plan, err)
	}
	plan, err = OccupancyReserve(domain.ResourceContext{Snapshot: uncertainSnap}, 3)
	if err != nil {
		t.Fatalf("refusal must not error: %v", err)
	}
	if plan.Admitted || len(plan.Reasons) != 1 || plan.Reasons[0].Code != "capacity_exhausted" {
		t.Fatalf("third slot must stay blocked by uncertainty, got %+v", plan)
	}

	// later end proof releases exactly the uncertainty
	plan, err = OccupancyReleaseUncertain(domain.ResourceContext{Snapshot: uncertainSnap}, 1)
	if err != nil || !plan.Admitted {
		t.Fatalf("releaseUncertain: %+v %v", plan, err)
	}
	if d := oneDelta(t, plan); d.Uncertain != -1 {
		t.Fatalf("releaseUncertain delta wrong: %+v", d)
	}
}

func TestOccupancyReserveRefusals(t *testing.T) {
	ctx := occupancyCtx()
	plan, err := OccupancyReserve(ctx, 4)
	if err != nil {
		t.Fatalf("refusal must not error: %v", err)
	}
	if plan.Admitted || len(plan.Deltas) != 0 || plan.Reasons[0].Group != "capacity" || plan.Reasons[0].Code != "capacity_exhausted" {
		t.Fatalf("over-capacity must refuse with capacity_exhausted, got %+v", plan)
	}
	plan, err = OccupancyReserve(ctx, -1)
	if err != nil {
		t.Fatalf("refusal must not error: %v", err)
	}
	if plan.Admitted || plan.Reasons[0].Code != "invalid_quantity" {
		t.Fatalf("negative quantity must refuse, got %+v", plan)
	}
}

func TestOccupancyIllegalTransitionsError(t *testing.T) {
	ctx := occupancyCtx()

	// activating with nothing reserved is an illegal transition, not a refusal
	if _, err := OccupancyActivate(ctx, 1); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("want ErrIllegalTransition, got %v", err)
	}
	// activating more than reserved
	snap := ctx.Snapshot
	snap.Reserved = 1
	if _, err := OccupancyActivate(domain.ResourceContext{Snapshot: snap}, 2); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("want ErrIllegalTransition, got %v", err)
	}
	// ending more than active
	if _, err := OccupancyEndProved(ctx, 1); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("want ErrIllegalTransition, got %v", err)
	}
	// releasing more than uncertain
	if _, err := OccupancyReleaseUncertain(ctx, 1); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("want ErrIllegalTransition, got %v", err)
	}
	// moving more than active into uncertain
	if _, err := OccupancyToUncertain(ctx, 1); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("want ErrIllegalTransition, got %v", err)
	}
	// negative quantities on transitions are illegal calls, not refusals
	if _, err := OccupancyEndProved(ctx, -1); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("want ErrIllegalTransition, got %v", err)
	}
}

func TestOccupancyZeroQuantityNoOps(t *testing.T) {
	ctx := occupancyCtx()
	for name, fn := range map[string]func(domain.ResourceContext, domain.Units) (domain.ResourceChangePlan, error){
		"reserve":   OccupancyReserve,
		"activate":  OccupancyActivate,
		"end":       OccupancyEndProved,
		"uncertain": OccupancyToUncertain,
		"release":   OccupancyReleaseUncertain,
	} {
		plan, err := fn(ctx, 0)
		if err != nil {
			t.Fatalf("%s(0): unexpected error %v", name, err)
		}
		if !plan.Admitted || len(plan.Deltas) != 1 {
			t.Fatalf("%s(0): want admitted single (zero) delta, got %+v", name, plan)
		}
	}
}
