package application

import (
	"ant/server/internal/domain"
	"ant/server/internal/domain/supply"
	"ant/server/internal/store"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
)

type InventoryView struct {
	Version  int64           `json:"version"`
	Document supply.Document `json:"document"`
}
type InventoryInput struct {
	CommandID       domain.ID       `json:"commandId"`
	ExpectedVersion int64           `json:"expectedVersion"`
	Document        supply.Document `json:"document"`
}
type InventoryService struct {
	store store.Store
	clock Clock
}

func NewInventoryService(st store.Store, clock Clock) (*InventoryService, error) {
	if st == nil || clock == nil {
		return nil, ErrConfigInvalid
	}
	return &InventoryService{st, clock}, nil
}
func inventoryAuthorized(auth domain.AuthContext, write bool) bool {
	return auth.PrincipalID != "" && auth.Namespace != "" && (slices.Contains(auth.Permissions, domain.PermConfigure) || (!write && slices.Contains(auth.Permissions, domain.PermRead)))
}
func inventoryRecordView(r store.InventoryRecord) (InventoryView, error) {
	var d supply.Document
	if err := json.Unmarshal(r.DefinitionJSON, &d); err != nil {
		return InventoryView{}, ErrConfigStore
	}
	if err := d.Normalize(); err != nil {
		return InventoryView{}, ErrConfigStore
	}
	return InventoryView{r.Version, d}, nil
}
func (s *InventoryService) Read(ctx context.Context, auth domain.AuthContext) (InventoryView, error) {
	if !inventoryAuthorized(auth, false) {
		return InventoryView{}, ErrConfigForbidden
	}
	value := InventoryView{0, supply.Empty()}
	err := s.store.Read(ctx, func(tx store.ReaderTx) error {
		rx, ok := tx.(store.InventoryReaderTx)
		if !ok {
			return ErrConfigStore
		}
		rec, found, err := rx.GetInventory(auth.Namespace)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		value, err = inventoryRecordView(rec)
		return err
	})
	if err != nil {
		return InventoryView{}, errors.Join(ErrConfigStore, err)
	}
	return value, nil
}
func (s *InventoryService) Save(ctx context.Context, auth domain.AuthContext, in InventoryInput) (InventoryView, error) {
	if !inventoryAuthorized(auth, true) {
		return InventoryView{}, ErrConfigForbidden
	}
	if !supply.ValidID(string(in.CommandID)) || in.ExpectedVersion < 0 || in.ExpectedVersion >= 9_007_199_254_740_991 {
		return InventoryView{}, ErrConfigInvalid
	}
	original, err := json.Marshal(in)
	if err != nil {
		return InventoryView{}, ErrConfigInvalid
	}
	digest := sha256.Sum256(original)
	hash := hex.EncodeToString(digest[:])
	var detached InventoryInput
	if err := json.Unmarshal(original, &detached); err != nil {
		return InventoryView{}, ErrConfigInvalid
	}
	in = detached
	if err := in.Document.Normalize(); err != nil {
		return InventoryView{}, ErrConfigInvalid
	}
	definition, err := json.Marshal(in.Document)
	if err != nil || len(definition) > 1<<20 {
		return InventoryView{}, ErrConfigInvalid
	}
	scope := domain.ID("inventory/" + string(auth.PrincipalID) + "/" + string(auth.Namespace))
	var value InventoryView
	err = s.store.SubmitWrite(ctx, store.WriteCommand{ID: "inventory/" + string(in.CommandID), Run: func(tx store.WriterTx) error {
		wx, ok := tx.(store.InventoryWriterTx)
		if !ok {
			return ErrConfigStore
		}
		receipt, found, err := tx.GetReceipt(scope, in.CommandID)
		if err != nil {
			return err
		}
		if found {
			if receipt.Kind != "inventory_save" || receipt.InputHash != hash {
				return ErrConfigConflict
			}
			return json.Unmarshal(receipt.ResultJSON, &value)
		}
		current, found, err := wx.GetInventory(auth.Namespace)
		if err != nil {
			return err
		}
		if (!found && in.ExpectedVersion != 0) || (found && current.Version != in.ExpectedVersion) {
			return ErrConfigConflict
		}
		if found {
			old, err := inventoryRecordView(current)
			if err != nil {
				return err
			}
			if !supply.CompatibleUpdate(old.Document, in.Document) {
				return ErrConfigInvalid
			}
		}
		now := s.clock.Now()
		applied, err := wx.SaveInventoryCAS(in.ExpectedVersion, store.InventoryRecord{Namespace: auth.Namespace, Version: in.ExpectedVersion + 1, DefinitionJSON: definition, UpdatedAt: now})
		if err != nil {
			return err
		}
		if !applied {
			return ErrConfigConflict
		}
		value = InventoryView{in.ExpectedVersion + 1, in.Document}
		result, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err := tx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: in.CommandID, Kind: "inventory_save", InputHash: hash, ResultJSON: result, CreatedAt: now}); err != nil {
			return err
		}
		auditID, err := secretCommandID()
		if err != nil {
			return err
		}
		return tx.InsertAudit(store.AuditRecord{AuditID: auditID, PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Action: "inventory.save", TargetRef: auth.Namespace, CreatedAt: now})
	}})
	if err != nil {
		if errors.Is(err, ErrConfigInvalid) || errors.Is(err, ErrConfigConflict) || errors.Is(err, ErrConfigForbidden) {
			return InventoryView{}, err
		}
		return InventoryView{}, errors.Join(ErrConfigStore, err)
	}
	return value, nil
}
