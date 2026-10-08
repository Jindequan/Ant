package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

var (
	ErrRestoreGuardInvalid   = errors.New("restore guard: invalid input")
	ErrRestoreGuardForbidden = errors.New("restore guard: forbidden")
	ErrRestoreGuardConflict  = errors.New("restore guard: version or command conflict")
	ErrRestoreGuardStore     = errors.New("restore guard: store unavailable")
)

const restoreGuardReceiptScope domain.ID = "runtime/restore-guard"

type RestoreGuardState struct {
	Enabled        bool  `json:"enabled"`
	ControlVersion int64 `json:"controlVersion"`
}

type RestoreGuardInput struct {
	CommandID              domain.ID `json:"commandId"`
	ExpectedControlVersion int64     `json:"expectedControlVersion"`
	Enabled                bool      `json:"enabled"`
	EvidenceRef            domain.ID `json:"evidenceRef"`
}

type RestoreGuardService struct {
	store store.Store
	clock Clock
}

func NewRestoreGuardService(st store.Store, clock Clock) (*RestoreGuardService, error) {
	if st == nil || clock == nil {
		return nil, ErrRestoreGuardInvalid
	}
	return &RestoreGuardService{store: st, clock: clock}, nil
}

func (s *RestoreGuardService) Get(ctx context.Context, auth domain.AuthContext) (RestoreGuardState, error) {
	if s == nil || s.store == nil || ctx == nil {
		return RestoreGuardState{}, ErrRestoreGuardInvalid
	}
	if !canManageRestoreGuard(auth) {
		return RestoreGuardState{}, ErrRestoreGuardForbidden
	}
	var result RestoreGuardState
	err := s.store.Read(ctx, func(rx store.ReaderTx) error {
		reader, ok := rx.(store.RestoreGuardReaderTx)
		if !ok {
			return ErrRestoreGuardStore
		}
		version, enabled, err := reader.GetRestoreGuard()
		if err != nil {
			return err
		}
		result = RestoreGuardState{Enabled: enabled, ControlVersion: version}
		return nil
	})
	if err != nil {
		return RestoreGuardState{}, fmt.Errorf("%w: read runtime control", ErrRestoreGuardStore)
	}
	return result, nil
}

func (s *RestoreGuardService) Set(ctx context.Context, auth domain.AuthContext, input RestoreGuardInput) (RestoreGuardState, error) {
	if s == nil || s.store == nil || ctx == nil || input.ExpectedControlVersion < 1 || !validControlID(input.CommandID) || !validControlID(input.EvidenceRef) {
		return RestoreGuardState{}, ErrRestoreGuardInvalid
	}
	if !canManageRestoreGuard(auth) {
		return RestoreGuardState{}, ErrRestoreGuardForbidden
	}
	input.CommandID = domain.ID(strings.TrimSpace(string(input.CommandID)))
	input.EvidenceRef = domain.ID(strings.TrimSpace(string(input.EvidenceRef)))
	hash, err := restoreGuardHash(auth, input)
	if err != nil {
		return RestoreGuardState{}, ErrRestoreGuardInvalid
	}
	var result RestoreGuardState
	err = s.store.SubmitWrite(ctx, store.WriteCommand{
		ID: "restore-guard/" + string(input.CommandID), Control: true,
		Run: func(w store.WriterTx) error {
			tx, ok := w.(store.RestoreGuardWriterTx)
			if !ok {
				return ErrRestoreGuardStore
			}
			if rec, found, err := tx.GetReceipt(restoreGuardReceiptScope, input.CommandID); err != nil {
				return err
			} else if found {
				if rec.Kind != "restore_guard" || rec.InputHash != hash || json.Unmarshal(rec.ResultJSON, &result) != nil {
					return ErrRestoreGuardConflict
				}
				return nil
			}
			version, current, err := tx.GetControlVersion()
			if err != nil {
				return err
			}
			if version != input.ExpectedControlVersion || (current != 0 && current != 1) {
				return ErrRestoreGuardConflict
			}
			newVersion := version
			if (current == 1) != input.Enabled {
				if version == math.MaxInt64 {
					return ErrRestoreGuardConflict
				}
				applied, err := tx.SetRestoreGuard(version, input.Enabled)
				if err != nil {
					return err
				}
				if !applied {
					return ErrRestoreGuardConflict
				}
				newVersion++
			}
			now := s.clock.Now()
			if now < 0 {
				return ErrRestoreGuardInvalid
			}
			state := RestoreGuardState{Enabled: input.Enabled, ControlVersion: newVersion}
			resultJSON, err := json.Marshal(state)
			if err != nil {
				return err
			}
			if err := tx.InsertReceipt(store.ReceiptRecord{
				Scope: restoreGuardReceiptScope, CommandID: input.CommandID, Kind: "restore_guard",
				InputHash: hash, ResultJSON: resultJSON, CreatedAt: now,
			}); err != nil {
				return err
			}
			action := "runtime.restore_guard.close"
			if !input.Enabled {
				action = "runtime.restore_guard.open"
			}
			if err := tx.InsertAudit(store.AuditRecord{
				AuditID:     domain.ID("restore-guard/" + string(auth.PrincipalID) + "/" + string(input.CommandID)),
				PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Action: action,
				TargetRef: "runtime_control", EvidenceRef: input.EvidenceRef, CreatedAt: now,
			}); err != nil {
				return err
			}
			result = state
			return nil
		},
	})
	if err != nil {
		if errors.Is(err, ErrRestoreGuardConflict) || errors.Is(err, ErrRestoreGuardInvalid) {
			return RestoreGuardState{}, err
		}
		return RestoreGuardState{}, fmt.Errorf("%w: update runtime control", ErrRestoreGuardStore)
	}
	return result, nil
}

func canManageRestoreGuard(auth domain.AuthContext) bool {
	return auth.PrincipalID != "" && auth.Namespace != "" && slices.Contains(auth.Permissions, domain.PermManage)
}

func validControlID(value domain.ID) bool {
	text := strings.TrimSpace(string(value))
	if len(text) == 0 || len(text) > 64 || text != string(value) {
		return false
	}
	for _, r := range text {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:-", r)) {
			return false
		}
	}
	return true
}

func restoreGuardHash(auth domain.AuthContext, input RestoreGuardInput) (string, error) {
	value, err := json.Marshal(struct {
		PrincipalID            domain.ID `json:"principalId"`
		Namespace              domain.ID `json:"namespace"`
		CommandID              domain.ID `json:"commandId"`
		ExpectedControlVersion int64     `json:"expectedControlVersion"`
		Enabled                bool      `json:"enabled"`
		EvidenceRef            domain.ID `json:"evidenceRef"`
	}{auth.PrincipalID, auth.Namespace, input.CommandID, input.ExpectedControlVersion, input.Enabled, input.EvidenceRef})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:]), nil
}
