package commands

import (
	"context"
	"encoding/json"
	"errors"

	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/store"
)

var ErrCompleteWithoutAttempt = errors.New("ledger completion: operation cannot be completed without an attempt")

// CompleteWithoutAttempt resolves and persists a terminal result when planning
// finishes without creating an attempt. It uses the same durable facts and
// result resolver as settlement and recovery; candidate diagnostics are inputs
// to classification, never proof that a dispatched action ended.
func (s *PreparationService) CompleteWithoutAttempt(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, reasons []domain.Reason) (domain.OperationResult, error) {
	if s == nil || s.st == nil || s.clock == nil || ctx == nil || owner.RuntimeID != s.runtimeID || owner.Key.Namespace == "" || owner.Key.OperationID == "" || owner.OwnerID == "" || owner.Generation < 1 || !authorized(auth, owner.Key.Namespace) {
		return domain.OperationResult{}, ErrPreparationForbidden
	}
	if len(reasons) > 128 {
		return domain.OperationResult{}, ErrCompleteWithoutAttempt
	}
	reasons = append([]domain.Reason(nil), reasons...)
	var result domain.OperationResult
	err := s.st.SubmitWrite(ctx, store.WriteCommand{
		ID:      "complete-without-attempt/" + string(owner.Key.Namespace) + "/" + string(owner.Key.OperationID),
		Control: true,
		Run: func(w store.WriterTx) error {
			rx, ok := w.(store.RecoveryWriterTx)
			if !ok {
				return ErrClaimStoreCapability
			}
			now := s.clock.Now()
			if now < 0 {
				return ErrCompleteWithoutAttempt
			}
			op, found, err := rx.GetClaimOperation(owner.Key.Namespace, owner.Key.OperationID)
			if err != nil {
				return err
			}
			if !found || op.RuntimeID != owner.RuntimeID || op.OwnerID != owner.OwnerID || op.Generation != owner.Generation {
				return ErrPreparationForbidden
			}
			if op.State != domain.Running {
				if len(op.ResultJSON) == 0 || json.Unmarshal(op.ResultJSON, &result) != nil || result.OperationID != op.OperationID || result.State != op.State {
					return ErrClaimIntegrity
				}
				return nil
			}
			if op.LeaseUntil <= now {
				return ErrPreparationStale
			}
			_, _, active, err := rx.GetActiveRecoveryAttempt(owner.Key)
			if err != nil {
				return err
			}
			if active {
				return ErrCompleteWithoutAttempt
			}
			attempts, err := rx.ListRecoveryAttemptOutcomes(owner.Key)
			if err != nil {
				return err
			}
			settled := store.SettlementFacts{Attempts: attemptIDs(attempts), Outcomes: []domain.ResourceOutcome{}}
			if len(attempts) > 0 {
				last, found, err := rx.GetPreparationAttempt(attempts[len(attempts)-1].AttemptID)
				if err != nil {
					return err
				}
				if !found || last.Phase != "terminal" {
					return ErrClaimIntegrity
				}
				settled, err = rx.GetSettlementFacts(last, now)
				if err != nil {
					return err
				}
			}
			facts := recoveryResultFacts(op, attempts, false, now)
			for _, reason := range reasons {
				if reason.Group == "affinity" {
					facts.AffinityUnavailable = true
				}
				if reason.Group == "domain" {
					facts.DomainFailureReasons = append(facts.DomainFailureReasons, reason)
				} else {
					facts.AdmissionBlockedReasons = append(facts.AdmissionBlockedReasons, reason)
				}
			}
			state, code, resolvedReasons := domain.ResolveResult(facts)
			if state == domain.Running {
				return ErrCompleteWithoutAttempt
			}
			result = domain.OperationResult{
				OperationID:        op.OperationID,
				State:              state,
				Code:               code,
				Reasons:            resolvedReasons,
				RetrySafety:        domain.Undetermined,
				Attempts:           settled.Attempts,
				Resources:          settled.Outcomes,
				DecisionRefs:       []domain.ID{},
				OutputAvailability: "none",
			}
			if settled.OutputStored {
				result.OutputAvailability = "available"
				result.OutputRef = domain.ID("operation/" + string(op.OperationID))
			}
			raw, err := json.Marshal(result)
			if err != nil {
				return err
			}
			applied, err := rx.CompleteRecoveryOperation(owner.Key, op.Version, &result, false, op.LeaseUntil, now)
			if err != nil {
				return err
			}
			if !applied {
				return ledger.ErrAbortConflict
			}
			if err := rx.AppendRecoveryEvent(owner.Key, "operation_terminal", raw, now); err != nil {
				return err
			}
			return w.InsertAudit(store.AuditRecord{
				AuditID:     domain.ID("complete-without-attempt/" + string(owner.Key.Namespace) + "/" + string(owner.Key.OperationID)),
				PrincipalID: auth.PrincipalID, Namespace: owner.Key.Namespace, Action: "complete_without_attempt",
				TargetRef: owner.Key.OperationID, CreatedAt: now,
			})
		},
	})
	if err != nil {
		return domain.OperationResult{}, err
	}
	return result, nil
}
