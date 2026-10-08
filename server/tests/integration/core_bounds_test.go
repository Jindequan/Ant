package integration

import (
	"context"
	"testing"

	"ant/server/internal/domain"
)

func TestPreparationAcceptsDemandBelowVerifiedCeiling(t *testing.T) {
	f := preparationFixture(t, 10)
	for i := range f.plan.Bounds {
		f.plan.Bounds[i].UpperBound = 10
	}
	reply, err := f.prepare(t, "below-ceiling")
	if err != nil || reply.Kind != "prepared" || reply.Attempt == nil || len(reply.Attempt.Allocations) != 2 {
		t.Fatalf("bounded request rejected: %+v %v", reply, err)
	}
	for _, allocation := range reply.Attempt.Allocations {
		if allocation.Units != 1 {
			t.Fatalf("must reserve request demand, not verified ceiling: %+v", allocation)
		}
	}
	permit, err := f.AuthorizeDispatch(context.Background(), claimAuth(), f.owner, "below-ceiling", 30)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ConsumeDispatchPermit(context.Background(), claimAuth(), f.owner, permit, "digest", 40); err != nil {
		t.Fatalf("safe smaller reservation cannot send: %v", err)
	}
}

func TestPreparationRejectsDemandAboveCeilingAndStaleMapping(t *testing.T) {
	for name, mutate := range map[string]func(*domain.ExecutionPlan){
		"above verified ceiling": func(p *domain.ExecutionPlan) { p.Requirements[0].UpperBound = 2 },
		"stale usage mapping":    func(p *domain.ExecutionPlan) { p.Bounds[0].MappingRevision = "stale-v0" },
	} {
		t.Run(name, func(t *testing.T) {
			f := preparationFixture(t, 10)
			mutate(&f.plan)
			reply, err := f.prepare(t, "unsafe")
			assertPreparationBlocked(t, reply, err)
			db := openRawDB(t, f.path)
			defer db.Close()
			var reservations, permits int
			if err := db.QueryRow(`SELECT COUNT(*) FROM reservations WHERE attempt_id='unsafe'`).Scan(&reservations); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM dispatch_permits WHERE attempt_id='unsafe'`).Scan(&permits); err != nil {
				t.Fatal(err)
			}
			if reservations != 0 || permits != 0 {
				t.Fatalf("unsafe demand acquired authority: reservations=%d permits=%d", reservations, permits)
			}
		})
	}
}
