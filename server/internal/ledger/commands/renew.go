package commands

import (
	"context"
	"errors"
	"fmt"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

var ErrRenewStale = errors.New("ledger renew: owner lost standing or operation no longer running")

// RenewLease extends the lease of every still-owned running operation in one
// transaction (03-EXECUTION §7). Each row is renewed by conditional UPDATE,
// competing with sweep's takeover on the same row: a renewal that commits
// first removes the operation from the due set, a sweep that commits first
// bumps the generation and this renewal reports ErrRenewStale. The new lease
// is capped at the operation deadline and never resurrects a dead operation.
// A stale error tells the host to stop dispatching on that token; it must not
// retry with the same token.
func (s *PreparationService) RenewLease(ctx context.Context, auth domain.AuthContext, owners []domain.OwnerToken, leaseMillis domain.Millis) (err error) {
	if s == nil || s.st == nil || s.clock == nil || s.runtimeID == "" {
		return ErrCancelInvalid
	}
	if leaseMillis <= 0 || len(owners) == 0 {
		return ErrCancelInvalid
	}
	for _, o := range owners {
		if o.Key.Namespace == "" || o.Key.OperationID == "" || o.OwnerID == "" || o.RuntimeID != s.runtimeID || o.Generation < 1 {
			return ErrCancelInvalid
		}
		if auth.PrincipalID == "" || auth.Namespace != o.Key.Namespace || !hasPermission(auth.Permissions, domain.PermExecute) {
			return ErrPreparationForbidden
		}
	}
	return s.st.SubmitWrite(ctx, store.WriteCommand{ID: "renew/" + string(owners[0].Key.Namespace) + "/" + fmt.Sprint(len(owners)), Control: true, Run: func(w store.WriterTx) error {
		rx, ok := w.(store.RecoveryWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		now := s.clock.Now()
		if now < 0 {
			return ErrCancelInvalid
		}
		for _, o := range owners {
			op, found, err := rx.GetClaimOperation(o.Key.Namespace, o.Key.OperationID)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("%w: %s", ErrRenewStale, string(o.Key.OperationID))
			}
			// Compare against the remaining horizon before adding. Directly
			// computing now+leaseMillis can overflow int64 and turn a very large
			// requested lease into an already-expired negative timestamp.
			leaseUntil := op.DeadlineAt
			if op.DeadlineAt > now {
				remaining := op.DeadlineAt - now // safe: both are non-negative and deadline is greater
				if leaseMillis < remaining {
					leaseUntil = now + leaseMillis // bounded by deadline, so cannot overflow
				}
			}
			applied, err := rx.RenewRecoveryLease(o.Key, o.RuntimeID, o.OwnerID, o.Generation, leaseUntil, now)
			if err != nil {
				return err
			}
			if !applied {
				return fmt.Errorf("%w: %s", ErrRenewStale, string(o.Key.OperationID))
			}
			if err := rx.ExtendRecoveryAttemptLease(o.Key, o.Generation, leaseUntil); err != nil {
				return err
			}
		}
		return nil
	}})
}
