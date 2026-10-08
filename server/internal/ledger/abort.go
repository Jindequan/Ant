package ledger

import (
	"errors"

	"ant/server/internal/domain"
)

// K3 protocol (frozen in this batch; transactional execution in T2.2).
// DecideAbort is the pure decision table for AbortBeforeDispatch; the T2.2
// transaction must execute exactly this plan inside one BEGIN IMMEDIATE tx,
// competing with consume/sweep via the same (attempt.phase, permit.status,
// generation) CAS guards — a losing racer gets AbortConflict and must not
// apply partial effects. docs/design/03-EXECUTION.md §9 is the authority.

var (
	ErrAbortInvalidState = errors.New("abort: state/proof combination not abortible")
	ErrAbortConflict     = errors.New("abort: lost CAS race against consume/sweep/settle")
)

type UnsentProof string

const (
	ProofNone           UnsentProof = "none"            // no reliable unsent/end evidence
	ProofReliableUnsent UnsentProof = "reliable_unsent" // controlled Transport proves request never written
	ProofExternalEnded  UnsentProof = "external_ended"  // provider confirms the call ended
)

type PermitStatus string

const (
	PermitNone     PermitStatus = "none"     // no permit ever issued
	PermitGranted  PermitStatus = "granted"  // issued, CAS proves not consumed
	PermitConsumed PermitStatus = "consumed" // send authorization consumed
	PermitRevoked  PermitStatus = "revoked"  // already closed by an earlier abort/sweep
)

type AbortBeforeDispatchInput struct {
	AttemptPhase    string       // prepared | executing
	PermitStatus    PermitStatus // observed inside the same transaction
	Proof           UnsentProof  // evidence class presented by the caller
	CancelRequested bool
}

type AbortKind string

const (
	AbortClosePrepared     AbortKind = "close_prepared"      // prepared, never authorized
	AbortRevokeGranted     AbortKind = "revoke_granted"      // granted, CAS-proven unconsumed
	AbortSettleNotExecuted AbortKind = "settle_not_executed" // consumed + reliable unsent proof
	AbortConserveUnknown   AbortKind = "conserve_unknown"    // consumed, no reliable evidence
	AbortNotAbortible      AbortKind = "not_abortible"       // external_ended → normal settle path
	AbortAlreadyClosed     AbortKind = "already_closed"      // revoked earlier / terminal attempt
)

// AbortOutcome is the atomic effect set the T2.2 transaction applies.
type AbortOutcome struct {
	Kind              AbortKind
	ReplanAllowed     bool   // Router may plan a new attempt on the current pool
	TerminalOutcome   string // not_executed | unknown (attempt.external_outcome)
	ReleaseCounter    bool   // counter reservations released (reserved -= q)
	ReleaseOccupancy  bool   // occupancy reserved/active released
	ConserveCounter   bool   // counter: committed += upper bound (unknown charge)
	ConserveOccupancy bool   // occupancy: active -> uncertain + open encumbrance
	RevokePermit      bool
	KeepPermitBudget  bool // permitsIssued stays: granted budget is never refunded
	Reasons           []domain.Reason
}

// DecideAbort maps the frozen matrix. Any phase/permit combination that
// cannot occur (e.g. prepared with an issued permit) is a hard error, not a
// best-effort guess.
func DecideAbort(in AbortBeforeDispatchInput) (AbortOutcome, error) {
	switch in.AttemptPhase {
	case "prepared":
		if in.PermitStatus != PermitNone {
			return AbortOutcome{}, ErrAbortInvalidState // permits exist only from executing on
		}
		if in.Proof != ProofNone {
			return AbortOutcome{}, ErrAbortInvalidState
		}
		return AbortOutcome{
			Kind:             AbortClosePrepared,
			ReplanAllowed:    true,
			TerminalOutcome:  "not_executed",
			ReleaseCounter:   true,
			ReleaseOccupancy: true,
			KeepPermitBudget: true, // no permit was issued; budget unchanged
		}, nil

	case "executing":
		switch in.PermitStatus {
		case PermitGranted:
			if in.Proof != ProofNone {
				return AbortOutcome{}, ErrAbortInvalidState
			}
			// CAS proves the permit was never consumed ⇒ no request left this
			// process via Transport: counters release; occupancy releases only
			// because no external execution can have started (I06 evidence:
			// permit never consumed). Granted-attempt budget is preserved.
			return AbortOutcome{
				Kind:             AbortRevokeGranted,
				ReplanAllowed:    true,
				TerminalOutcome:  "not_executed",
				ReleaseCounter:   true,
				ReleaseOccupancy: true,
				RevokePermit:     true,
				KeepPermitBudget: true,
			}, nil
		case PermitConsumed:
			switch in.Proof {
			case ProofReliableUnsent:
				// Controlled Transport proof that the socket write never
				// started. The transaction must verify the proof against the
				// permit/attempt records — caller-supplied claims alone are
				// never sufficient (K3).
				return AbortOutcome{
					Kind:             AbortSettleNotExecuted,
					ReplanAllowed:    true,
					TerminalOutcome:  "not_executed",
					ReleaseCounter:   true,
					ReleaseOccupancy: true,
					KeepPermitBudget: true,
				}, nil
			case ProofExternalEnded:
				return AbortOutcome{}, ErrAbortInvalidState // ended calls go through settle
			case ProofNone:
				// Cannot prove non-execution: conserve. Counters take the
				// verified upper bound, occupancy becomes an uncertainty
				// encumbrance; automatic re-dispatch is forbidden.
				return AbortOutcome{
					Kind:              AbortConserveUnknown,
					ReplanAllowed:     false,
					TerminalOutcome:   "unknown",
					ConserveCounter:   true,
					ConserveOccupancy: true,
					KeepPermitBudget:  true,
				}, nil
			}
		case PermitRevoked:
			return AbortOutcome{Kind: AbortAlreadyClosed}, nil
		case PermitNone:
			return AbortOutcome{}, ErrAbortInvalidState // executing implies an issued permit
		}
	case "terminal":
		return AbortOutcome{Kind: AbortAlreadyClosed}, nil
	}
	return AbortOutcome{}, ErrAbortInvalidState
}
