package domain

// Result resolver (DESIGN §7.1): the single source of truth for the six-way
// competition between replay, confirmed success, unknown outcome,
// cancellation, failure classification and continued execution. Every
// terminal path (settle, completeWithoutAttempt, sweep, cancel completion)
// must call ResolveResult instead of picking its own row of the state table
// (I08). The function is pure: it reads durable facts and returns the
// operation state to persist inside the result transaction.

// Result codes frozen by DESIGN §4 and §7.1. Reasons are grouped and
// extensible; the state and code vocabulary is not.
const (
	ResultCodeSuccess              = "Success"
	ResultCodeUnknownOutcome       = "UnknownOutcome"
	ResultCodeCancelled            = "Cancelled"
	ResultCodeAffinityUnavailable  = "AffinityUnavailable"
	ResultCodeDeadlineExceeded     = "DeadlineExceeded"
	ResultCodeAttemptsExhausted    = "AttemptsExhausted"
	ResultCodeExecutionInterrupted = "ExecutionInterrupted"
	ResultCodeAdmissionBlocked     = "AdmissionBlocked"
	ResultCodeDomainFailure        = "DomainFailure"

	// ReasonOwnerLeaseExpiredBeforeDispatch is the only permitted recovery
	// reason for ExecutionInterrupted: durable proof that the operation died
	// after claim with no attempt and with no dispatch permit ever granted.
	// It must never be attached to an attempt that held a send permit.
	ReasonOwnerLeaseExpiredBeforeDispatch = "owner_lease_expired_before_dispatch"
	// ReasonGroupExecutionRecovery groups runtime-recovery causes
	// (ExecutionInterrupted family), deliberately outside the
	// admission/capacity groups.
	ReasonGroupExecutionRecovery = "execution_recovery"
	ReasonGroupAccounting        = "accounting"
	ReasonActualOutOfRange       = "actual_out_of_range"
)

const (
	reasonGroupCancellation          = "cancellation"
	reasonCancelRequested            = "cancel_requested"
	reasonDispatchOutcomeUnconfirmed = "dispatch_outcome_unconfirmed"
)

// ResultFacts carries every durable fact the resolver may need. Flags are
// committed facts, not heuristics; callers must derive them from persisted
// attempts, permits, reports and cancel records.
type ResultFacts struct {
	// ExistingResult is an already-persisted result. Any non-running state is
	// terminal and is replayed verbatim; late material can only append
	// reconciliation evidence, never overwrite (§7.1.1).
	ExistingResult *OperationResult
	// ConfirmedSuccess: persisted facts confirm complete business success.
	ConfirmedSuccess bool
	// UnconfirmedDispatchPending: an attempt that consumed a send permit
	// cannot have its outcome confirmed and a terminal state is required.
	// Unknown wins over cancellation and over any admission refusal (§7.1.3).
	UnconfirmedDispatchPending bool
	// AccountingUnrepresentable: a durable provider fact cannot be committed
	// into the bounded int64 ledger and requires explicit reconciliation.
	AccountingUnrepresentable bool
	// CancelRequested: a cancellation intent is durably recorded.
	CancelRequested bool
	// AllActionsNotExecutedOrEnded: every authorized action is confirmed to
	// have never executed or to be externally ended. Only then may a cancel
	// intent become a cancelled result (§7.1.4).
	AllActionsNotExecutedOrEnded bool
	// AffinityUnavailable: a required binding/account constraint cannot be
	// satisfied; outranks the ordinary admission reasons (§7.1.5).
	AffinityUnavailable bool
	DeadlineExceeded    bool
	AttemptsExhausted   bool
	// ExecutionInterrupted: the operation died after claim with no attempt
	// and no dispatch permit ever granted. A caller that granted a send
	// permit and cannot confirm non-dispatch MUST NOT set this flag — that
	// situation is UnconfirmedDispatchPending and resolves to unknown, never
	// to the "no execution happened" recovery reason.
	ExecutionInterrupted    bool
	AdmissionBlockedReasons []Reason // capacity/budget/policy refusals with their evidence
	DomainFailureReasons    []Reason // domain-policy failure classification
	// CompetingReasons are non-causal race facts (capacity exhausted, budget
	// spent, deadline hit) observed while cancelling. They are preserved on a
	// cancelled result (§7.1.4) but never escalate it to failed.
	CompetingReasons []Reason
}

// ResolveResult applies the §7.1 priority order exactly. Durable replay wins;
// unrepresentable accounting facts then force unknown/review before success,
// dispatch uncertainty, cancellation, the failure ladder, or running.
func ResolveResult(f ResultFacts) (OperationState, string, []Reason) {
	// (1) Existing terminal state: stable replay, late material cannot cover.
	if f.ExistingResult != nil && f.ExistingResult.State != Running {
		return f.ExistingResult.State, f.ExistingResult.Code, copyReasons(f.ExistingResult.Reasons)
	}
	if f.AccountingUnrepresentable {
		reasons := []Reason{{Group: ReasonGroupAccounting, Code: ReasonActualOutOfRange}}
		if f.UnconfirmedDispatchPending {
			reasons = append(reasons, Reason{Group: ReasonGroupExecutionRecovery, Code: reasonDispatchOutcomeUnconfirmed})
		}
		return Unknown, ResultCodeUnknownOutcome, append(reasons, cancelDiagnostics(f)...)
	}
	// (2) Confirmed success, even against a recorded cancel intent; the
	// intent survives only as a diagnostic reason.
	if f.ConfirmedSuccess {
		return Succeeded, ResultCodeSuccess, cancelDiagnostics(f)
	}
	// (3) Unconfirmed externally dispatched work: unknown must not be masked
	// by cancellation, capacity refusal or any failure classification.
	if f.UnconfirmedDispatchPending {
		reasons := append([]Reason{{
			Group: ReasonGroupExecutionRecovery,
			Code:  reasonDispatchOutcomeUnconfirmed,
		}}, cancelDiagnostics(f)...)
		return Unknown, ResultCodeUnknownOutcome, reasons
	}
	// (4) Effective cancellation: intent recorded AND every action confirmed
	// not-executed-or-ended AND no success. Competing reasons are preserved
	// as diagnostics; they do not change the outcome.
	if f.CancelRequested && f.AllActionsNotExecutedOrEnded {
		return Cancelled, ResultCodeCancelled, copyReasons(f.CompetingReasons)
	}
	// (5) No cancel intent and a mandatory failure: required affinity first,
	// then deadline, attempts, execution interruption, admission, domain.
	if !f.CancelRequested {
		diagnostics := concatReasons(f.AdmissionBlockedReasons, f.DomainFailureReasons)
		switch {
		case f.AffinityUnavailable:
			return Failed, ResultCodeAffinityUnavailable, diagnostics
		case f.DeadlineExceeded:
			return Failed, ResultCodeDeadlineExceeded, diagnostics
		case f.AttemptsExhausted:
			return Failed, ResultCodeAttemptsExhausted, diagnostics
		case f.ExecutionInterrupted:
			// Only reachable with no dispatch permit ever granted (see
			// ResultFacts.ExecutionInterrupted); permit holders resolve to
			// unknown at priority (3) instead.
			reasons := append([]Reason{{
				Group: ReasonGroupExecutionRecovery,
				Code:  ReasonOwnerLeaseExpiredBeforeDispatch,
			}}, diagnostics...)
			return Failed, ResultCodeExecutionInterrupted, reasons
		case len(f.AdmissionBlockedReasons) > 0:
			return Failed, ResultCodeAdmissionBlocked, diagnostics
		case len(f.DomainFailureReasons) > 0:
			return Failed, ResultCodeDomainFailure, copyReasons(f.DomainFailureReasons)
		}
	}
	// (6) Nothing terminal established and active execution still has a valid
	// owner: stay running (a cancel intent alone blocks new attempts but does
	// not fabricate a business result).
	return Running, "", nil
}

func cancelDiagnostics(f ResultFacts) []Reason {
	if !f.CancelRequested {
		return nil
	}
	return []Reason{{Group: reasonGroupCancellation, Code: reasonCancelRequested}}
}

func copyReasons(rs []Reason) []Reason {
	if rs == nil {
		return nil
	}
	out := make([]Reason, len(rs))
	copy(out, rs)
	return out
}

func concatReasons(lists ...[]Reason) []Reason {
	n := 0
	for _, l := range lists {
		n += len(l)
	}
	if n == 0 {
		return nil
	}
	out := make([]Reason, 0, n)
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}
