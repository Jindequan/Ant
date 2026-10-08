package commands

import (
	"context"
	"encoding/json"
	"errors"

	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/store"
)

var (
	ErrCancelInvalid   = errors.New("ledger cancel: invalid input")
	ErrCancelForbidden = errors.New("ledger cancel: forbidden")
	ErrCancelConflict  = errors.New("ledger cancel: conflicting command id")
)

// RequestCancel records the cancellation intent with its instant and audit
// trail (03-EXECUTION §7). A prepared attempt is closed inside the same
// transaction through the K3 abort core — leaving a prepared attempt behind
// a terminal operation is a protocol violation (§9 supplement 1). An
// executing attempt only records the intent: the outcome converges through
// the report, unknown conservation or sweep; a confirmed success report
// outranks the recorded intent in the shared resolver.
func (s *PreparationService) RequestCancel(ctx context.Context, auth domain.AuthContext, key domain.OperationKey, commandID domain.ID) (domain.CancellationAck, error) {
	if s == nil || s.st == nil || s.clock == nil || s.runtimeID == "" {
		return domain.CancellationAck{}, ErrCancelInvalid
	}
	if key.Namespace == "" || key.OperationID == "" || commandID == "" {
		return domain.CancellationAck{}, ErrCancelInvalid
	}
	if auth.PrincipalID == "" || auth.Namespace != key.Namespace || !hasPermission(auth.Permissions, domain.PermCancel) {
		return domain.CancellationAck{}, ErrCancelForbidden
	}
	hash, _, err := commandHash(struct {
		Key       domain.OperationKey
		CommandID domain.ID
	}{key, commandID})
	if err != nil {
		return domain.CancellationAck{}, err
	}
	scope := domain.ID("cancel/" + string(key.Namespace))
	var ack domain.CancellationAck
	var closedAttempt domain.ID
	err = s.st.SubmitWrite(ctx, store.WriteCommand{ID: string(scope) + "/" + string(commandID), Control: true, Run: func(w store.WriterTx) error {
		closedAttempt = ""
		rx, ok := w.(store.RecoveryWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		now := s.clock.Now()
		if now < 0 {
			return ErrCancelInvalid
		}
		if receipt, found, err := rx.GetReceipt(scope, commandID); err != nil {
			return err
		} else if found {
			if receipt.Kind != "cancel" || receipt.InputHash != hash {
				return ErrCancelConflict
			}
			return json.Unmarshal(receipt.ResultJSON, &ack)
		}
		op, found, err := rx.GetClaimOperation(key.Namespace, key.OperationID)
		if err != nil {
			return err
		}
		if !found {
			ack = domain.CancellationAck{Status: "notFound"}
		} else if op.State != domain.Running {
			ack = domain.CancellationAck{Status: "alreadyTerminal"}
		} else {
			ack = domain.CancellationAck{Status: "recorded"}
			applied, err := rx.MarkRecoveryCancelRequested(key, op.Version, now)
			if err != nil {
				return err
			}
			if !applied {
				return ErrPreparationStale
			}
			cancelEvent, err := json.Marshal(struct {
				CommandID domain.ID `json:"commandId"`
			}{CommandID: commandID})
			if err != nil {
				return err
			}
			if err := rx.AppendRecoveryEvent(key, "cancel_requested", cancelEvent, now); err != nil {
				return err
			}
			// A prepared attempt closes now through the same frozen abort
			// matrix; an executing attempt keeps the intent-only contract —
			// its outcome converges via report, conservation or sweep.
			// Prepared attempts have no durable permit, but a failed authorize
			// transaction may have left an unreturned runtime-local handle. Defer
			// its cleanup until this cancellation transaction commits.
			if attemptID, phase, active, err := rx.GetActiveRecoveryAttempt(key); err != nil {
				return err
			} else if active && phase == "prepared" {
				if _, err := applyRecoveryAbort(rx, s.runtimeID, auth.PrincipalID, key, attemptID, op.Generation, ledger.ProofNone, ledger.DispatchPermit{}, false, now); err != nil {
					return err
				}
				closedAttempt = attemptID
			}
		}
		raw, err := json.Marshal(ack)
		if err != nil {
			return err
		}
		if err := rx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: commandID, Kind: "cancel", InputHash: hash, ResultJSON: raw, CreatedAt: now}); err != nil {
			return err
		}
		if ack.Status == "recorded" {
			return rx.InsertAudit(store.AuditRecord{AuditID: domain.ID("cancel/" + string(commandID)), PrincipalID: auth.PrincipalID, Namespace: key.Namespace, Action: "cancel", TargetRef: key.OperationID, EvidenceRef: commandID, CreatedAt: now})
		}
		return nil
	}})
	if err != nil {
		return domain.CancellationAck{}, err
	}
	if closedAttempt != "" {
		s.releaseTerminalAttempt(closedAttempt)
	}
	return ack, nil
}

func hasPermission(perms []domain.Permission, want domain.Permission) bool {
	for _, p := range perms {
		if p == want {
			return true
		}
	}
	return false
}
