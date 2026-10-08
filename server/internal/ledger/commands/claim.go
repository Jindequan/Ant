package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/store"
)

var (
	ErrClaimInvalid         = ledger.ErrClaimInvalid
	ErrClaimForbidden       = ledger.ErrClaimForbidden
	ErrClaimNotReady        = ledger.ErrClaimNotReady
	ErrClaimStoreCapability = errors.New("ledger claim: store lacks claim transaction support")
	ErrClaimIntegrity       = errors.New("ledger claim: persisted identity is inconsistent")
)

// ClaimPolicy is supplied by the trusted runtime/configuration composition
// root, never by the ExecuteInput payload.
type ClaimPolicy = ledger.ClaimPolicy

// Claim atomically accepts a new operation or resolves a repeated operation
// ID. Its write callback is local database work only; no provider/network call
// may occur before this transaction commits.
func Claim(ctx context.Context, st store.Store, auth domain.AuthContext, input domain.ExecuteInput, fingerprint string, policy ClaimPolicy) (ledger.ClaimReply, error) {
	if st == nil || input.OperationID == "" || input.PoolID == "" || input.Request.Domain == "" ||
		input.Request.CapabilityProfile == "" || fingerprint == "" {
		return ledger.ClaimReply{}, ErrClaimInvalid
	}
	if auth.PrincipalID == "" || auth.Namespace == "" || !slices.Contains(auth.Permissions, domain.PermExecute) {
		return ledger.ClaimReply{}, ErrClaimForbidden
	}
	expected, err := domain.SemanticFingerprint(domain.FingerprintInput{
		Domain:            input.Request.Domain,
		PoolID:            input.PoolID,
		CapabilityProfile: input.Request.CapabilityProfile,
		RequiredAffinity:  input.Affinity,
		Payload:           input.Request.Payload,
	})
	if err != nil || expected != fingerprint {
		return ledger.ClaimReply{}, ErrClaimInvalid
	}
	key := domain.OperationKey{Namespace: auth.Namespace, OperationID: input.OperationID}
	scope := domain.ID("claim/" + string(auth.Namespace))
	var reply ledger.ClaimReply
	command := store.WriteCommand{
		ID: "claim/" + string(auth.Namespace) + "/" + string(input.OperationID),
		Run: func(tx store.WriterTx) error {
			claimTx, ok := tx.(store.ClaimWriterTx)
			if !ok {
				return ErrClaimStoreCapability
			}
			receipt, hasReceipt, err := tx.GetReceipt(scope, input.OperationID)
			if err != nil {
				return err
			}
			if hasReceipt && (receipt.Kind != "claim" || receipt.InputHash != fingerprint) {
				reply = ledger.ClaimReply{Kind: "conflict"}
				return nil
			}
			existing, found, err := claimTx.GetClaimOperation(auth.Namespace, input.OperationID)
			if err != nil {
				return err
			}
			if found {
				if existing.Fingerprint != fingerprint || existing.PoolID != input.PoolID {
					reply = ledger.ClaimReply{Kind: "conflict"}
					return nil
				}
				if !hasReceipt {
					return ErrClaimIntegrity
				}
				reply, err = replyForExisting(existing)
				return err
			}
			if hasReceipt {
				return ErrClaimIntegrity
			}
			// Deadline, attempt budget, and runtime lease are first-claim
			// strategy. Existing IDs must resolve before current policy is
			// validated, so expired requests can still return inProgress/replay.
			if policy.RuntimeID == "" || policy.Now < 0 || policy.LeaseMillis <= 0 ||
				policy.MaxRuntimeAttempts < 1 || input.MaxAttempts < 1 ||
				input.MaxAttempts > policy.MaxRuntimeAttempts || input.DeadlineAt <= policy.Now {
				return ErrClaimInvalid
			}
			leaseUntil := policy.Now + policy.LeaseMillis
			if leaseUntil < policy.Now { // overflow
				return ErrClaimInvalid
			}
			if leaseUntil > input.DeadlineAt {
				leaseUntil = input.DeadlineAt
			}
			ownerID, err := newOwnerID()
			if err != nil {
				return fmt.Errorf("ledger claim: owner id unavailable")
			}
			strategy, err := json.Marshal(input)
			if err != nil {
				return ErrClaimInvalid
			}
			pool, found, err := tx.GetPool(input.PoolID)
			if err != nil {
				return err
			}
			if !found || pool.Namespace != auth.Namespace || !pool.Enabled {
				return ErrClaimForbidden
			}
			active, activeFound, err := tx.GetActiveConfig()
			if err != nil {
				return err
			}
			if !activeFound {
				return ErrClaimNotReady
			}
			if _, revisionFound, err := tx.GetConfigRevision(active.Revision); err != nil {
				return err
			} else if !revisionFound {
				return ErrClaimIntegrity
			}
			if _, restoreGuard, err := tx.GetControlVersion(); err != nil {
				return err
			} else if restoreGuard != 0 {
				return ErrClaimNotReady
			}
			record := store.ClaimOperationRecord{
				Namespace:    auth.Namespace,
				OperationID:  input.OperationID,
				PoolID:       input.PoolID,
				Fingerprint:  fingerprint,
				State:        domain.Running,
				RuntimeID:    policy.RuntimeID,
				OwnerID:      ownerID,
				Generation:   1,
				LeaseUntil:   leaseUntil,
				DeadlineAt:   input.DeadlineAt,
				MaxAttempts:  input.MaxAttempts,
				StrategyJSON: strategy,
				CreatedAt:    policy.Now,
				UpdatedAt:    policy.Now,
				Version:      1,
			}
			if err := claimTx.InsertClaimOperation(record); err != nil {
				return err
			}
			// The receipt deliberately stores no owner token. A retry with the same
			// ID sees inProgress and cannot mint a second execution right.
			if err := tx.InsertReceipt(store.ReceiptRecord{
				Scope: scope, CommandID: input.OperationID, Kind: "claim", InputHash: fingerprint,
				ResultJSON: []byte(`{"accepted":true}`), CreatedAt: policy.Now,
			}); err != nil {
				return err
			}
			events, ok := tx.(store.OperationEventWriterTx)
			if !ok {
				return ErrClaimStoreCapability
			}
			acceptedPayload, err := json.Marshal(struct {
				State domain.OperationState `json:"state"`
			}{State: domain.Running})
			if err != nil {
				return err
			}
			if err := events.AppendOperationEvent(key, "accepted", acceptedPayload, policy.Now); err != nil {
				return err
			}
			view := operationView(record)
			reply = ledger.ClaimReply{
				Kind:      "new",
				Owner:     &domain.OwnerToken{Key: key, RuntimeID: policy.RuntimeID, OwnerID: ownerID, Generation: 1},
				Operation: &view,
			}
			return nil
		},
	}
	if err := st.SubmitWrite(ctx, command); err != nil {
		return ledger.ClaimReply{}, err
	}
	return reply, nil
}

func replyForExisting(record store.ClaimOperationRecord) (ledger.ClaimReply, error) {
	view := operationView(record)
	if record.State == domain.Running {
		return ledger.ClaimReply{Kind: "inProgress", Operation: &view}, nil
	}
	if record.State != domain.Succeeded && record.State != domain.Failed &&
		record.State != domain.Cancelled && record.State != domain.Unknown {
		return ledger.ClaimReply{}, ErrClaimIntegrity
	}
	if len(record.ResultJSON) == 0 {
		return ledger.ClaimReply{}, ErrClaimIntegrity
	}
	var result domain.OperationResult
	if err := json.Unmarshal(record.ResultJSON, &result); err != nil || result.OperationID != record.OperationID || result.State != record.State {
		return ledger.ClaimReply{}, ErrClaimIntegrity
	}
	view.Result = &result
	return ledger.ClaimReply{Kind: "replay", Operation: &view, Result: &result}, nil
}

func operationView(record store.ClaimOperationRecord) domain.OperationView {
	return domain.OperationView{
		OperationID:     record.OperationID,
		PoolID:          record.PoolID,
		State:           record.State,
		CancelRequested: record.CancelRequestedAt != nil,
		Attempts:        []domain.AttemptView{},
		Resources:       []domain.ResourceOutcome{},
		Reconciliations: []domain.ReconciliationView{},
		UpdatedAt:       record.UpdatedAt,
	}
}

func newOwnerID() (domain.ID, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	hexID := hex.EncodeToString(bytes[:])
	return domain.ID(hexID[:8] + "-" + hexID[8:12] + "-" + hexID[12:16] + "-" + hexID[16:20] + "-" + hexID[20:]), nil
}
