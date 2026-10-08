package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"ant/server/internal/domain"
	"ant/server/internal/security"
	"ant/server/internal/store"
)

var (
	ErrSecretInvalid   = errors.New("secret service: invalid input")
	ErrSecretForbidden = errors.New("secret service: forbidden")
	ErrSecretNotFound  = errors.New("secret service: not found")
	ErrSecretConflict  = errors.New("secret service: revision conflict")
	ErrSecretStore     = errors.New("secret service: store unavailable")
)

// SecretCommands is the application boundary for write-only credential
// management. Cleartext is accepted only on the write call and is never
// included in receipts, audit records, projections, or returned errors.
type SecretCommands struct {
	store  store.Store
	cipher *security.SecretService
	clock  Clock
	keyRef string
}

func NewSecretCommands(st store.Store, cipher *security.SecretService, clock Clock, keyRef string) (*SecretCommands, error) {
	if st == nil || cipher == nil || clock == nil || keyRef == "" {
		return nil, ErrSecretInvalid
	}
	return &SecretCommands{store: st, cipher: cipher, clock: clock, keyRef: keyRef}, nil
}

func authorizeSecrets(auth domain.AuthContext) error {
	if auth.PrincipalID == "" || auth.Namespace == "" || !slices.Contains(auth.Permissions, domain.PermConfigure) {
		return ErrSecretForbidden
	}
	return nil
}

func secretCommandID() (domain.ID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("secret id generation failed")
	}
	return domain.ID(hex.EncodeToString(raw[:])), nil
}

func secretInputHash(ref, expected domain.ID, value string) string {
	hash := sha256.New()
	for _, part := range []string{string(ref), string(expected), value} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *SecretCommands) Create(ctx context.Context, auth domain.AuthContext, commandID domain.ID, value string) (security.SecretView, error) {
	if s == nil || authorizeSecrets(auth) != nil {
		return security.SecretView{}, ErrSecretForbidden
	}
	if commandID == "" || len(commandID) > 128 || value == "" || len(value) > 1<<20 {
		return security.SecretView{}, ErrSecretInvalid
	}
	inputHash := secretInputHash("", "", value)
	scope := domain.ID("secret-create/" + string(auth.PrincipalID) + "/" + string(auth.Namespace))
	var result resultSlot[security.SecretView]
	err := s.store.SubmitWrite(ctx, store.WriteCommand{ID: "secret-create/" + string(commandID), Run: func(tx store.WriterTx) error {
		secrets, ok := tx.(store.SecretWriterTx)
		if !ok {
			return ErrSecretStore
		}
		receipt, found, err := tx.GetReceipt(scope, commandID)
		if err != nil {
			return err
		}
		if found {
			if receipt.Kind != "secret_create" || receipt.InputHash != inputHash {
				return ErrSecretConflict
			}
			var replay security.SecretView
			if err := json.Unmarshal(receipt.ResultJSON, &replay); err != nil {
				return err
			}
			result.set(replay)
			return nil
		}
		ref, err := secretCommandID()
		if err != nil {
			return err
		}
		revision, err := secretCommandID()
		if err != nil {
			return err
		}
		sealed, err := s.cipher.Seal(ref, revision, map[string]string{"value": value})
		if err != nil {
			return err
		}
		now := s.clock.Now()
		record := store.SecretRevisionRecord{Ref: ref, Revision: revision, Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext,
			MasterKeyRef: s.keyRef, Mask: sealed.Mask, CreatedAt: now}
		reference := store.SecretReferenceRecord{Ref: ref, PrincipalID: auth.PrincipalID, Namespace: auth.Namespace,
			CurrentRevision: revision, Mask: sealed.Mask, CreatedAt: now, UpdatedAt: now}
		if err := secrets.InsertSecret(reference, record); err != nil {
			return err
		}
		view := security.SecretView{Ref: ref, Revision: revision, Mask: sealed.Mask}
		if err := saveSecretReceipt(tx, scope, commandID, "secret_create", inputHash, view, now); err != nil {
			return err
		}
		if err := tx.InsertAudit(store.AuditRecord{AuditID: ref, PrincipalID: auth.PrincipalID, Namespace: auth.Namespace,
			Action: "secret.create", TargetRef: ref, CreatedAt: now}); err != nil {
			return err
		}
		result.set(view)
		return nil
	}})
	if err != nil {
		return security.SecretView{}, classifySecretStoreError(err)
	}
	return result.get(), nil
}

func (s *SecretCommands) Rotate(ctx context.Context, auth domain.AuthContext, ref, commandID, expectedRevision domain.ID, value string) (security.SecretView, error) {
	if s == nil || authorizeSecrets(auth) != nil {
		return security.SecretView{}, ErrSecretForbidden
	}
	if ref == "" || commandID == "" || expectedRevision == "" || len(commandID) > 128 || value == "" || len(value) > 1<<20 {
		return security.SecretView{}, ErrSecretInvalid
	}
	inputHash := secretInputHash(ref, expectedRevision, value)
	scope := domain.ID("secret-rotate/" + string(auth.PrincipalID) + "/" + string(auth.Namespace))
	var result resultSlot[security.SecretView]
	err := s.store.SubmitWrite(ctx, store.WriteCommand{ID: "secret-rotate/" + string(commandID), Run: func(tx store.WriterTx) error {
		secrets, ok := tx.(store.SecretWriterTx)
		if !ok {
			return ErrSecretStore
		}
		receipt, found, err := tx.GetReceipt(scope, commandID)
		if err != nil {
			return err
		}
		if found {
			if receipt.Kind != "secret_rotate" || receipt.InputHash != inputHash {
				return ErrSecretConflict
			}
			var replay security.SecretView
			if err := json.Unmarshal(receipt.ResultJSON, &replay); err != nil {
				return err
			}
			result.set(replay)
			return nil
		}
		reference, found, err := secrets.GetSecretReference(ref)
		if err != nil {
			return err
		}
		if !found || reference.PrincipalID != auth.PrincipalID || reference.Namespace != auth.Namespace {
			return ErrSecretNotFound
		}
		if reference.CurrentRevision != expectedRevision {
			return ErrSecretConflict
		}
		revision, err := secretCommandID()
		if err != nil {
			return err
		}
		sealed, err := s.cipher.Seal(ref, revision, map[string]string{"value": value})
		if err != nil {
			return err
		}
		now := s.clock.Now()
		record := store.SecretRevisionRecord{Ref: ref, Revision: revision, Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext,
			MasterKeyRef: s.keyRef, Mask: sealed.Mask, CreatedAt: now}
		applied, err := secrets.AdvanceSecretRevision(auth.PrincipalID, auth.Namespace, ref, expectedRevision, record, now)
		if err != nil {
			return err
		}
		if !applied {
			return ErrSecretConflict
		}
		view := security.SecretView{Ref: ref, Revision: revision, Mask: sealed.Mask}
		if err := saveSecretReceipt(tx, scope, commandID, "secret_rotate", inputHash, view, now); err != nil {
			return err
		}
		if err := tx.InsertAudit(store.AuditRecord{AuditID: revision, PrincipalID: auth.PrincipalID, Namespace: auth.Namespace,
			Action: "secret.rotate", TargetRef: ref, CreatedAt: now}); err != nil {
			return err
		}
		result.set(view)
		return nil
	}})
	if err != nil {
		return security.SecretView{}, classifySecretStoreError(err)
	}
	return result.get(), nil
}

func (s *SecretCommands) Get(ctx context.Context, auth domain.AuthContext, ref domain.ID) (security.SecretView, error) {
	if s == nil || authorizeSecrets(auth) != nil {
		return security.SecretView{}, ErrSecretForbidden
	}
	if ref == "" {
		return security.SecretView{}, ErrSecretInvalid
	}
	var view security.SecretView
	err := s.store.Read(ctx, func(rx store.ReaderTx) error {
		secrets, ok := rx.(store.SecretReaderTx)
		if !ok {
			return ErrSecretStore
		}
		reference, found, err := secrets.GetSecretReference(ref)
		if err != nil {
			return err
		}
		if !found || reference.PrincipalID != auth.PrincipalID || reference.Namespace != auth.Namespace {
			return ErrSecretNotFound
		}
		view = security.SecretView{Ref: reference.Ref, Revision: reference.CurrentRevision, Mask: reference.Mask}
		return nil
	})
	if err != nil {
		return security.SecretView{}, classifySecretStoreError(err)
	}
	return view, nil
}

func (s *SecretCommands) Resolve(ctx context.Context, ref, revision domain.ID) (map[string]string, error) {
	var record store.SecretRevisionRecord
	err := s.store.Read(ctx, func(rx store.ReaderTx) error {
		secrets, ok := rx.(store.SecretReaderTx)
		if !ok {
			return ErrSecretStore
		}
		value, found, err := secrets.GetSecretRevision(ref, revision)
		if err != nil {
			return err
		}
		if !found {
			return security.ErrSecretUnknown
		}
		record = value
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.cipher.Open(ref, revision, record.Nonce, record.Ciphertext)
}

// ResolveOwned returns the current secret value only when the configuring
// principal owns the reference and the requested revision is still current.
// Ownership and revision checks share the same read snapshot as ciphertext
// retrieval, so a rotated or unrelated secret cannot be verified by ID alone.
func (s *SecretCommands) ResolveOwned(ctx context.Context, auth domain.AuthContext, ref, revision domain.ID) (map[string]string, error) {
	if s == nil || authorizeSecrets(auth) != nil {
		return nil, ErrSecretForbidden
	}
	if ref == "" || revision == "" {
		return nil, ErrSecretInvalid
	}
	var record store.SecretRevisionRecord
	err := s.store.Read(ctx, func(rx store.ReaderTx) error {
		secrets, ok := rx.(store.SecretReaderTx)
		if !ok {
			return ErrSecretStore
		}
		reference, found, err := secrets.GetSecretReference(ref)
		if err != nil {
			return err
		}
		if !found || reference.PrincipalID != auth.PrincipalID || reference.Namespace != auth.Namespace {
			return ErrSecretNotFound
		}
		if reference.CurrentRevision != revision {
			return ErrSecretConflict
		}
		value, found, err := secrets.GetSecretRevision(ref, revision)
		if err != nil {
			return err
		}
		if !found {
			return ErrSecretNotFound
		}
		record = value
		return nil
	})
	if err != nil {
		return nil, classifySecretStoreError(err)
	}
	return s.cipher.Open(ref, revision, record.Nonce, record.Ciphertext)
}

func saveSecretReceipt(tx store.WriterTx, scope, commandID domain.ID, kind, inputHash string, view security.SecretView, now domain.Millis) error {
	body, err := json.Marshal(view)
	if err != nil {
		return err
	}
	return tx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: commandID, Kind: kind, InputHash: inputHash, ResultJSON: body, CreatedAt: now})
}

func classifySecretStoreError(err error) error {
	if errors.Is(err, ErrSecretInvalid) || errors.Is(err, ErrSecretForbidden) || errors.Is(err, ErrSecretNotFound) || errors.Is(err, ErrSecretConflict) {
		return err
	}
	if errors.Is(err, store.ErrUnavailable) || errors.Is(err, store.ErrQueueClosed) || errors.Is(err, store.ErrQueueFull) || errors.Is(err, store.ErrCommandBudget) {
		return fmt.Errorf("%w: %v", ErrSecretStore, err)
	}
	return err
}
