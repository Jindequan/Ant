package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

var (
	ErrOutputInvalid   = errors.New("ledger output: invalid chunk")
	ErrOutputForbidden = errors.New("ledger output: operation ownership or permission rejected")
	ErrOutputConflict  = errors.New("ledger output: sequence conflict")
	ErrOutputLimit     = errors.New("ledger output: operation output limit reached")
)

const (
	MaxOutputChunkBytes = domain.MaxOutputChunkBytes
	MaxOutputBytes      = domain.MaxOperationOutputBytes
)

// OutputService persists a decoded output chunk before making its sequence
// visible. Retrying an identical sequence is safe; a different value at the
// same sequence is rejected. It performs no network or decoder work.
type OutputService struct {
	st        store.Store
	runtimeID domain.ID
	clock     PreparationClock
}

func NewOutputService(st store.Store, runtimeID domain.ID, clock PreparationClock) *OutputService {
	return &OutputService{st: st, runtimeID: runtimeID, clock: clock}
}

func (s *OutputService) AppendOutput(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, attemptID domain.ID, chunk domain.OutputChunk) error {
	if s == nil || s.st == nil || s.clock == nil || s.runtimeID == "" || owner.RuntimeID != s.runtimeID ||
		owner.Key.Namespace == "" || owner.Key.OperationID == "" || attemptID == "" ||
		chunk.Sequence < 1 || len(chunk.Data) == 0 || len(chunk.Data) > MaxOutputChunkBytes {
		return ErrOutputInvalid
	}
	if auth.PrincipalID == "" || auth.Namespace != owner.Key.Namespace || !slices.Contains(auth.Permissions, domain.PermExecute) {
		return ErrOutputForbidden
	}
	digest := sha256.Sum256(chunk.Data)
	hash := hex.EncodeToString(digest[:])
	return s.st.SubmitWrite(ctx, store.WriteCommand{
		ID: "output/" + string(attemptID) + "/" + strconv.FormatInt(chunk.Sequence, 10),
		Run: func(tx store.WriterTx) error {
			writer, ok := tx.(store.OutputWriterTx)
			if !ok {
				return ErrClaimStoreCapability
			}
			now := s.clock.Now()
			if now < 0 {
				return ErrOutputInvalid
			}
			attempt, found, err := writer.GetPreparationAttempt(attemptID)
			if err != nil {
				return err
			}
			if !found || attempt.Key != owner.Key {
				return ErrOutputForbidden
			}
			existing, found, err := writer.GetOutputChunk(attemptID, chunk.Sequence)
			if err != nil {
				return err
			}
			if found {
				if existing.SHA256 == hash && bytes.Equal(existing.Data, chunk.Data) {
					return nil
				}
				return ErrOutputConflict
			}
			op, found, err := writer.GetClaimOperation(owner.Key.Namespace, owner.Key.OperationID)
			if err != nil {
				return err
			}
			if !found || op.State != domain.Running || op.RuntimeID != owner.RuntimeID || op.OwnerID != owner.OwnerID ||
				op.Generation != owner.Generation || op.LeaseUntil <= now {
				return ErrOutputForbidden
			}
			if attempt.Generation != owner.Generation || attempt.Phase != "executing" {
				return ErrOutputForbidden
			}
			permit, found, err := writer.GetPreparationPermit(attemptID)
			if err != nil {
				return err
			}
			if !found || permit.AttemptID != attemptID || permit.Status != "consumed" || permit.RuntimeID != s.runtimeID ||
				permit.Generation != owner.Generation || permit.RequestDigest != attempt.RequestDigest || permit.CredentialRevision != attempt.Plan.CredentialRevision {
				return ErrOutputForbidden
			}
			count, size, err := writer.CountOperationOutput(owner.Key)
			if err != nil {
				return err
			}
			if chunk.Sequence != count+1 {
				return ErrOutputConflict
			}
			if size < 0 || int64(len(chunk.Data)) > MaxOutputBytes-size {
				return ErrOutputLimit
			}
			if err := writer.AppendOutputChunk(owner.Key, store.OutputChunkRecord{AttemptID: attemptID, Sequence: chunk.Sequence, Data: slices.Clone(chunk.Data), SHA256: hash, CreatedAt: now}); err != nil {
				return err
			}
			if err := writer.MarkOutputVisible(owner.Key, now); err != nil {
				return err
			}
			payload, err := json.Marshal(struct {
				OutputRef string `json:"outputRef"`
				First     int64  `json:"firstSequence"`
				Last      int64  `json:"lastSequence"`
			}{"operation/" + string(owner.Key.OperationID), chunk.Sequence, chunk.Sequence})
			if err != nil {
				return err
			}
			events, ok := tx.(store.OperationEventWriterTx)
			if !ok {
				return ErrClaimStoreCapability
			}
			return events.AppendOperationEvent(owner.Key, "output_stored", payload, now)
		},
	})
}
