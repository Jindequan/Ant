package domain

import (
	"reflect"
	"testing"
)

func reasonIn(rs []Reason, group, code string) bool {
	for _, r := range rs {
		if r.Group == group && r.Code == code {
			return true
		}
	}
	return false
}

func TestResolveResultReplayWins(t *testing.T) {
	existing := &OperationResult{
		State:   Failed,
		Code:    ResultCodeAttemptsExhausted,
		Reasons: []Reason{{Group: "budget", Code: "attempts_spent"}},
	}
	f := ResultFacts{
		ExistingResult:               existing,
		ConfirmedSuccess:             true, // even success may not overwrite a terminal
		UnconfirmedDispatchPending:   true,
		CancelRequested:              true,
		AllActionsNotExecutedOrEnded: true,
	}
	state, code, reasons := ResolveResult(f)
	if state != Failed || code != ResultCodeAttemptsExhausted {
		t.Fatalf("terminal replay must win, got %s/%s", state, code)
	}
	if !reasonIn(reasons, "budget", "attempts_spent") {
		t.Fatalf("original reasons must be replayed, got %v", reasons)
	}
}

func TestResolveResultUnrepresentableAccountingRequiresReview(t *testing.T) {
	state, code, reasons := ResolveResult(ResultFacts{
		ConfirmedSuccess:          true,
		AccountingUnrepresentable: true,
	})
	if state != Unknown || code != ResultCodeUnknownOutcome || !reasonIn(reasons, ReasonGroupAccounting, ReasonActualOutOfRange) {
		t.Fatalf("unrepresentable accounting must remain unknown for review, got %s/%s/%v", state, code, reasons)
	}
}

func TestResolveResultReplayUnknownStaysUnknown(t *testing.T) {
	existing := &OperationResult{State: Unknown, Code: ResultCodeUnknownOutcome}
	state, code, _ := ResolveResult(ResultFacts{
		ExistingResult:   existing,
		ConfirmedSuccess: true,
	})
	if state != Unknown || code != ResultCodeUnknownOutcome {
		t.Fatalf("unknown terminal must replay as unknown, got %s/%s", state, code)
	}
}

func TestResolveResultNonTerminalExistingIgnored(t *testing.T) {
	state, _, _ := ResolveResult(ResultFacts{ExistingResult: &OperationResult{State: Running}})
	if state != Running {
		t.Fatalf("a running stored projection is not terminal, got %s", state)
	}
}

func TestResolveResultSuccessBeatsCancel(t *testing.T) {
	state, code, reasons := ResolveResult(ResultFacts{
		ConfirmedSuccess: true,
		CancelRequested:  true,
		CompetingReasons: []Reason{{Group: "capacity", Code: "capacity_exhausted"}},
	})
	if state != Succeeded || code != ResultCodeSuccess {
		t.Fatalf("confirmed success must win over cancel intent, got %s/%s", state, code)
	}
	if !reasonIn(reasons, "cancellation", "cancel_requested") {
		t.Fatalf("cancel intent must survive as diagnostic, got %v", reasons)
	}
}

func TestResolveResultUnknownBeatsCancelAndRefusals(t *testing.T) {
	state, code, reasons := ResolveResult(ResultFacts{
		UnconfirmedDispatchPending: true,
		CancelRequested:            true,
		AdmissionBlockedReasons:    []Reason{{Group: "capacity", Code: "capacity_exhausted"}},
		DeadlineExceeded:           true,
	})
	if state != Unknown || code != ResultCodeUnknownOutcome {
		t.Fatalf("unknown must not be masked by cancel/refusal/deadline, got %s/%s", state, code)
	}
	if !reasonIn(reasons, "execution_recovery", "dispatch_outcome_unconfirmed") {
		t.Fatalf("unknown result should explain the unconfirmed dispatch, got %v", reasons)
	}
	if reasonIn(reasons, "capacity", "capacity_exhausted") {
		t.Fatal("admission refusals are not causes of unknown; must not leak into reasons")
	}
}

func TestResolveResultCancelledKeepsCompetingReasons(t *testing.T) {
	competing := []Reason{
		{Group: "capacity", Code: "capacity_exhausted", ScopeRef: "res-1"},
		{Group: "budget", Code: "budget_exhausted"},
	}
	state, code, reasons := ResolveResult(ResultFacts{
		CancelRequested:              true,
		AllActionsNotExecutedOrEnded: true,
		CompetingReasons:             competing,
	})
	if state != Cancelled || code != ResultCodeCancelled {
		t.Fatalf("want cancelled/Cancelled, got %s/%s", state, code)
	}
	if !reflect.DeepEqual(reasons, competing) {
		t.Fatalf("competing reasons must be preserved on cancelled, got %v", reasons)
	}
}

func TestResolveResultFailureLadder(t *testing.T) {
	blocked := []Reason{{Group: "capacity", Code: "capacity_exhausted"}}
	domainFail := []Reason{{Group: "domain", Code: "recipient_suppressed"}}
	cases := []struct {
		name     string
		facts    ResultFacts
		wantCode string
		check    func(t *testing.T, reasons []Reason)
	}{
		{
			name: "affinity beats deadline and admission",
			facts: ResultFacts{
				AffinityUnavailable:     true,
				DeadlineExceeded:        true,
				AdmissionBlockedReasons: blocked,
				DomainFailureReasons:    domainFail,
				AttemptsExhausted:       true,
				ExecutionInterrupted:    true,
			},
			wantCode: ResultCodeAffinityUnavailable,
			check: func(t *testing.T, reasons []Reason) {
				if !reasonIn(reasons, "capacity", "capacity_exhausted") {
					t.Fatal("remaining diagnostics must be preserved")
				}
			},
		},
		{
			name: "deadline beats attempts",
			facts: ResultFacts{
				DeadlineExceeded:  true,
				AttemptsExhausted: true,
			},
			wantCode: ResultCodeDeadlineExceeded,
		},
		{
			name: "attempts beat interruption",
			facts: ResultFacts{
				AttemptsExhausted:    true,
				ExecutionInterrupted: true,
			},
			wantCode: ResultCodeAttemptsExhausted,
		},
		{
			name: "interruption carries recovery reason and diagnostics",
			facts: ResultFacts{
				ExecutionInterrupted:    true,
				AdmissionBlockedReasons: blocked,
			},
			wantCode: ResultCodeExecutionInterrupted,
			check: func(t *testing.T, reasons []Reason) {
				if !reasonIn(reasons, ReasonGroupExecutionRecovery, ReasonOwnerLeaseExpiredBeforeDispatch) {
					t.Fatalf("interruption must carry owner_lease_expired_before_dispatch, got %v", reasons)
				}
				if !reasonIn(reasons, "capacity", "capacity_exhausted") {
					t.Fatal("admission diagnostics preserved")
				}
			},
		},
		{
			name:     "admission blocked",
			facts:    ResultFacts{AdmissionBlockedReasons: blocked},
			wantCode: ResultCodeAdmissionBlocked,
			check: func(t *testing.T, reasons []Reason) {
				if !reflect.DeepEqual(reasons, blocked) {
					t.Fatalf("admission reasons must be the result reasons, got %v", reasons)
				}
			},
		},
		{
			name:     "domain failure last",
			facts:    ResultFacts{DomainFailureReasons: domainFail},
			wantCode: ResultCodeDomainFailure,
		},
	}
	for _, tc := range cases {
		state, code, reasons := ResolveResult(tc.facts)
		if state != Failed {
			t.Fatalf("%s: want failed, got %s", tc.name, state)
		}
		if code != tc.wantCode {
			t.Fatalf("%s: want code %s, got %s", tc.name, tc.wantCode, code)
		}
		if tc.check != nil {
			tc.check(t, reasons)
		}
	}
}

func TestResolveResultCancelWithActiveExecutionStaysRunning(t *testing.T) {
	// Cancel intent alone never fabricates a result, and a mandatory failure
	// requires "no cancel": with the intent recorded, the ladder must not run.
	state, _, _ := ResolveResult(ResultFacts{
		CancelRequested:   true,
		DeadlineExceeded:  true,
		AttemptsExhausted: true,
	})
	if state != Running {
		t.Fatalf("cancel intent without confirmed end must stay running, got %s", state)
	}
}

func TestResolveResultNothingSetStaysRunning(t *testing.T) {
	state, code, reasons := ResolveResult(ResultFacts{})
	if state != Running || code != "" || reasons != nil {
		t.Fatalf("empty facts must resolve to running, got %s/%s/%v", state, code, reasons)
	}
}
