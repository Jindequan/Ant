package commands

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/store"
)

var (
	ErrSweepForbidden = errors.New("ledger sweep: forbidden")
	ErrSweepInvalid   = errors.New("ledger sweep: invalid input")
)

// maxSweepBatch caps one sweep transaction (03-EXECUTION §7: 每批最多100).
const maxSweepBatch = 100

// Sweep takes over running operations whose lease expired (03-EXECUTION §7).
// Per due operation, in one transaction:
//   - no active attempt, no permit ever granted → the unified resolver
//     completes it as ExecutionInterrupted/owner_lease_expired_before_dispatch;
//   - prepared attempt → closed through the same K3 matrix (release, not
//     executed) — with the sweep marker the resolver may complete it;
//   - executing attempt, permit still granted → CAS-proven unconsumed:
//     revoked and closed not_executed (zero send), generation bumped;
//   - executing attempt, permit consumed → lease expiry is NOT external end
//     evidence: conservative unknown (counter bound committed, occupancy
//     uncertain + encumbrance) and the resolver terminalizes unknown.
//
// The generation bump removes the old owner's write/send standing. A still
// unresolvable orphan stays running, parked at its deadline, until the
// resolver can complete it. Late reports of swept attempts go to
// reconciliation; nothing is re-dispatched here.
func (s *PreparationService) Sweep(ctx context.Context, auth domain.AuthContext, now domain.Millis, limit int) error {
	if s == nil || s.st == nil || s.clock == nil || s.runtimeID == "" {
		return ErrSweepInvalid
	}
	// Cross-namespace recovery is a trusted runtime action, not a user
	// payload: the manage permission marks the internal context (same rule
	// as claim's internal system_probe note).
	if auth.PrincipalID == "" || !hasPermission(auth.Permissions, domain.PermManage) {
		return ErrSweepForbidden
	}
	if now < 0 {
		return ErrSweepInvalid
	}
	if limit < 1 {
		return ErrSweepInvalid
	}
	if limit > maxSweepBatch {
		limit = maxSweepBatch
	}
	var terminalized []domain.ID
	err := s.st.SubmitWrite(ctx, store.WriteCommand{ID: "sweep/" + string(s.runtimeID), Control: true, Run: func(w store.WriterTx) error {
		rx, ok := w.(store.RecoveryWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		txNow := s.clock.Now()
		if txNow < 0 {
			return ErrSweepInvalid
		}
		due, err := rx.ListDueRecoveryOperations(txNow, limit)
		if err != nil {
			return err
		}
		for i := range due {
			op := due[i]
			attemptID, _, active, err := rx.GetActiveRecoveryAttempt(domain.OperationKey{Namespace: op.Namespace, OperationID: op.OperationID})
			if err != nil {
				return err
			}
			if active {
				if _, err := applyRecoveryAbort(rx, s.runtimeID, auth.PrincipalID, domain.OperationKey{Namespace: op.Namespace, OperationID: op.OperationID}, attemptID, op.Generation, ledger.ProofNone, ledger.DispatchPermit{}, true, txNow); err != nil {
					return err
				}
				terminalized = append(terminalized, attemptID)
			} else {
				attemptFacts, err := rx.ListRecoveryAttemptOutcomes(domain.OperationKey{Namespace: op.Namespace, OperationID: op.OperationID})
				if err != nil {
					return err
				}
				state, code, reasons := domain.ResolveResult(recoveryResultFacts(op, attemptFacts, true, txNow))
				key := domain.OperationKey{Namespace: op.Namespace, OperationID: op.OperationID}
				if state != domain.Running {
					result := &domain.OperationResult{OperationID: op.OperationID, State: state, Code: code, Reasons: reasons, RetrySafety: domain.Undetermined, Attempts: attemptIDs(attemptFacts), Resources: []domain.ResourceOutcome{}, DecisionRefs: []domain.ID{}, OutputAvailability: "none"}
					raw, err := json.Marshal(result)
					if err != nil {
						return err
					}
					applied, err := rx.CompleteRecoveryOperation(key, op.Version, result, true, op.LeaseUntil, txNow)
					if err != nil {
						return err
					}
					if !applied {
						return ledger.ErrAbortConflict
					}
					if err := rx.AppendRecoveryEvent(key, "operation_terminal", raw, txNow); err != nil {
						return err
					}
				} else {
					// Nothing terminal yet (e.g. attempts remain but the owner
					// vanished mid-continuation): take over the generation and
					// park the orphan at its deadline; the resolver completes
					// it there. Lease expiry alone never fabricates a result.
					if applied, err := rx.BumpRecoveryGeneration(key, op.Version, op.DeadlineAt, txNow); err != nil {
						return err
					} else if !applied {
						return ledger.ErrAbortConflict
					}
				}
			}
			if err := rx.InsertAudit(store.AuditRecord{AuditID: domain.ID("sweep/" + string(op.Namespace) + "/" + string(op.OperationID) + "/" + fmtInt64(op.Generation)), PrincipalID: auth.PrincipalID, Namespace: op.Namespace, Action: "sweep", TargetRef: op.OperationID, EvidenceRef: op.RuntimeID, CreatedAt: txNow}); err != nil {
				return err
			}
		}
		return nil
	}})
	if err != nil {
		return err
	}
	// Registry/handle lifecycle after commit: swept terminal attempts can
	// never issue or consume a permit again.
	s.releaseTerminalAttempt(terminalized...)
	return nil
}

// releaseTerminalAttempt drops cached dispatch handles and both in-process
// registry indexes for attempts that durably reached a terminal state in a
// committed transaction. The durable permit row remains authoritative.
func (s *PreparationService) releaseTerminalAttempt(ids ...domain.ID) {
	if len(ids) == 0 {
		return
	}
	s.mu.Lock()
	for _, id := range ids {
		delete(s.handles, id)
		s.registry.ReleaseTerminal(id)
	}
	s.mu.Unlock()
}

func fmtInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}
