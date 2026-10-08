package ledger

import (
	"errors"
	"testing"

	"ant/server/internal/domain"
)

// K3 protocol tests: the decision table in abort.go must cover every frozen
// matrix row and reject impossible combinations. Transactional execution and
// failure evidence belong to T2.2 — nothing here claims runtime verification.

func TestAbortPreparedWithoutPermit(t *testing.T) {
	plan, err := DecideAbort(AbortBeforeDispatchInput{AttemptPhase: "prepared", PermitStatus: PermitNone, Proof: ProofNone})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != AbortClosePrepared || !plan.ReplanAllowed || !plan.ReleaseCounter || !plan.ReleaseOccupancy {
		t.Fatalf("prepared close mismatch: %+v", plan)
	}
	if plan.TerminalOutcome != "not_executed" {
		t.Fatalf("prepared must close as not_executed, got %q", plan.TerminalOutcome)
	}
}

func TestAbortGrantedUnconsumed(t *testing.T) {
	plan, err := DecideAbort(AbortBeforeDispatchInput{AttemptPhase: "executing", PermitStatus: PermitGranted, Proof: ProofNone})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != AbortRevokeGranted || !plan.RevokePermit {
		t.Fatalf("granted abort mismatch: %+v", plan)
	}
	if !plan.KeepPermitBudget {
		t.Fatal("granted permit budget must be preserved (permitsIssued not refunded)")
	}
	if plan.TerminalOutcome != "not_executed" || !plan.ReleaseCounter || !plan.ReleaseOccupancy || !plan.ReplanAllowed {
		t.Fatalf("granted effects mismatch: %+v", plan)
	}
}

func TestAbortConsumedWithReliableUnsentProof(t *testing.T) {
	plan, err := DecideAbort(AbortBeforeDispatchInput{AttemptPhase: "executing", PermitStatus: PermitConsumed, Proof: ProofReliableUnsent})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != AbortSettleNotExecuted || !plan.ReleaseCounter || plan.ConserveCounter {
		t.Fatalf("consumed+proof mismatch: %+v", plan)
	}
}

func TestAbortConsumedWithoutProofConservesUnknown(t *testing.T) {
	plan, err := DecideAbort(AbortBeforeDispatchInput{AttemptPhase: "executing", PermitStatus: PermitConsumed, Proof: ProofNone})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != AbortConserveUnknown {
		t.Fatalf("want conserve_unknown, got %s", plan.Kind)
	}
	if plan.ReplanAllowed {
		t.Fatal("unknown outcome must not allow automatic replan/redistribution")
	}
	if !plan.ConserveCounter || !plan.ConserveOccupancy {
		t.Fatalf("unknown must conserve counter and occupancy: %+v", plan)
	}
}

func TestAbortInvalidCombinationsAreHardErrors(t *testing.T) {
	cases := []AbortBeforeDispatchInput{
		// prepared with a permit cannot exist
		{AttemptPhase: "prepared", PermitStatus: PermitGranted, Proof: ProofNone},
		{AttemptPhase: "prepared", PermitStatus: PermitConsumed, Proof: ProofNone},
		// evidence about a permit that was never issued
		{AttemptPhase: "prepared", PermitStatus: PermitNone, Proof: ProofReliableUnsent},
		// executing without a permit cannot exist
		{AttemptPhase: "executing", PermitStatus: PermitNone, Proof: ProofNone},
		// external_ended calls go through settle, not abort
		{AttemptPhase: "executing", PermitStatus: PermitConsumed, Proof: ProofExternalEnded},
		{AttemptPhase: "executing", PermitStatus: PermitGranted, Proof: ProofExternalEnded},
	}
	for i, in := range cases {
		if _, err := DecideAbort(in); !errors.Is(err, ErrAbortInvalidState) {
			t.Fatalf("case %d (%+v): want ErrAbortInvalidState, got %v", i, in, err)
		}
	}
}

func TestAbortAlreadyClosedIsIdempotent(t *testing.T) {
	plan, err := DecideAbort(AbortBeforeDispatchInput{AttemptPhase: "executing", PermitStatus: PermitRevoked, Proof: ProofNone})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != AbortAlreadyClosed {
		t.Fatalf("revoked permit should be already_closed, got %s", plan.Kind)
	}
	plan, err = DecideAbort(AbortBeforeDispatchInput{AttemptPhase: "terminal", PermitStatus: PermitNone, Proof: ProofNone})
	if err != nil || plan.Kind != AbortAlreadyClosed {
		t.Fatalf("terminal attempt should be already_closed, got %s err=%v", plan.Kind, err)
	}
}

func TestAbortReasonsAreDiagnostic(t *testing.T) {
	plan, err := DecideAbort(AbortBeforeDispatchInput{AttemptPhase: "executing", PermitStatus: PermitConsumed, Proof: ProofNone, CancelRequested: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.TerminalOutcome != "unknown" {
		t.Fatal("cancel request must not downgrade a consumed attempt without proof to not_executed")
	}
	_ = domain.Running // keep domain import if reasons evolve to use it
}
