package commands

import (
	"ant/server/internal/domain"
	"ant/server/internal/domain/resources"
	"ant/server/internal/store"
	"context"
	"encoding/json"
	"errors"
	"slices"
)

var (
	ErrAdminInvalid   = errors.New("ledger admin: invalid input")
	ErrAdminForbidden = errors.New("ledger admin: forbidden")
	ErrAdminNotFound  = errors.New("ledger admin: not found")
	ErrAdminConflict  = errors.New("ledger admin: stale command or unsafe transition")
)

type AdminService struct {
	st    store.Store
	clock PreparationClock
}

func NewAdminService(st store.Store, clock PreparationClock) *AdminService {
	return &AdminService{st: st, clock: clock}
}

// RecordObservation accepts a manager-supplied observation that was collected
// outside the ledger transaction. It binds the sample to its resource window,
// committed watermark and adjustment version before making it admissible.
func (s *AdminService) RecordObservation(ctx context.Context, auth domain.AuthContext, commandID domain.ID, evidence domain.ObservationEvidence) (domain.RefreshResult, error) {
	if !s.authorized(auth) {
		return domain.RefreshResult{}, ErrAdminForbidden
	}
	if commandID == "" || evidence.ResourceID == "" || evidence.SourceRef == "" || evidence.ObservedAt < 0 || evidence.ExpiresAt <= evidence.ObservedAt || evidence.Remaining < 0 || evidence.CommittedAtStart < 0 || evidence.AdjustmentVersionAtStart < 0 {
		return domain.RefreshResult{}, ErrAdminInvalid
	}
	hash, _, err := commandHash(struct {
		ResourceID domain.ID
		Evidence   domain.ObservationEvidence
	}{evidence.ResourceID, evidence})
	if err != nil {
		return domain.RefreshResult{}, err
	}
	scope := domain.ID("observe/" + string(auth.Namespace) + "/" + string(evidence.ResourceID))
	var result domain.RefreshResult
	err = s.st.SubmitWrite(ctx, store.WriteCommand{ID: string(scope) + "/" + string(commandID), Control: true, Run: func(tx store.WriterTx) error {
		rx, ok := tx.(store.AdminWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		now := s.clock.Now()
		if now < 0 {
			return ErrAdminInvalid
		}
		receipt, exists, e := tx.GetReceipt(scope, commandID)
		if e != nil {
			return e
		}
		if exists {
			if receipt.Kind == "resource_observation" && receipt.InputHash == hash {
				return json.Unmarshal(receipt.ResultJSON, &result)
			}
			return ErrAdminConflict
		}
		definition, found, e := rx.GetManagedResourceDefinition(auth.Namespace, evidence.ResourceID)
		if e != nil {
			return e
		}
		if !found {
			return ErrAdminNotFound
		}
		windowKey, _, _, e := resources.WindowAt(evidence.ObservedAt, definition.Window, definition.SemanticsRevision)
		if errors.Is(e, resources.ErrRollingNoBounds) {
			e = nil // rolling has one stable key; attribution remains to that key.
		}
		if e != nil {
			return ErrAdminInvalid
		}
		current, currentFound, e := rx.GetManagedResource(auth.Namespace, evidence.ResourceID, windowKey)
		if e != nil {
			return e
		}
		result = domain.RefreshResult{Status: "stored", Reasons: []domain.Reason{}}
		stale := func(code string) {
			result.Status = "rejectedStale"
			result.Reasons = []domain.Reason{{Group: "observation", Code: code, ScopeRef: evidence.ResourceID, EvidenceRef: evidence.SourceRef}}
		}
		switch {
		case !currentFound:
			stale("window_changed")
		case evidence.ObservedAt > now || evidence.ExpiresAt <= now:
			stale("evidence_expired")
		case evidence.AdjustmentVersionAtStart != current.AdjustmentVersion:
			stale("adjustment_changed")
		case evidence.CommittedAtStart > current.Snapshot.Committed:
			stale("watermark_ahead")
		}
		if result.Status == "stored" {
			evidence.ID = domain.ID("observation/" + string(auth.Namespace) + "/" + string(evidence.ResourceID) + "/" + string(commandID))
			evidence.WindowKey = windowKey
			if e = rx.InsertResourceObservation(evidence, scope, commandID, []domain.ID{evidence.SourceRef}, now); e != nil {
				return e
			}
		}
		raw, e := json.Marshal(result)
		if e != nil {
			return e
		}
		if e = tx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: commandID, Kind: "resource_observation", InputHash: hash, ResultJSON: raw, CreatedAt: now}); e != nil {
			return e
		}
		return tx.InsertAudit(store.AuditRecord{AuditID: domain.ID(string(scope) + "/" + string(commandID)), PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Action: "resource.observation.refresh", TargetRef: evidence.ResourceID, EvidenceRef: evidence.SourceRef, CreatedAt: now})
	}})
	if err != nil {
		return domain.RefreshResult{}, err
	}
	return result, nil
}

// Adjust appends an evidence-backed change to the adjustment watermark. A
// negative correction must name a matching prior charge and cannot reverse
// more than that charge's still-unreversed amount.
func (s *AdminService) Adjust(ctx context.Context, auth domain.AuthContext, input domain.AdjustmentInput) (domain.CommandAck, error) {
	if !s.authorized(auth) {
		return domain.CommandAck{}, ErrAdminForbidden
	}
	if input.CommandID == "" || input.ResourceID == "" || input.WindowKey == "" || input.Delta == 0 || len(input.EvidenceRefs) == 0 || len(uniqueIDs(input.EvidenceRefs)) != len(input.EvidenceRefs) {
		return domain.CommandAck{}, ErrAdminInvalid
	}
	for _, ref := range input.EvidenceRefs {
		if ref == "" {
			return domain.CommandAck{}, ErrAdminInvalid
		}
	}
	if input.Delta < 0 && input.OriginalEventID == "" {
		return domain.CommandAck{}, ErrAdminInvalid
	}
	hash, _, err := commandHash(input)
	if err != nil {
		return domain.CommandAck{}, err
	}
	scope := domain.ID("adjust/" + string(auth.Namespace) + "/" + string(input.ResourceID))
	ack := domain.CommandAck{CommandID: input.CommandID, Status: "applied"}
	err = s.st.SubmitWrite(ctx, store.WriteCommand{ID: string(scope) + "/" + string(input.CommandID), Control: true, Run: func(tx store.WriterTx) error {
		rx, ok := tx.(store.AdminWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		now := s.clock.Now()
		if now < 0 {
			return ErrAdminInvalid
		}
		receipt, exists, e := tx.GetReceipt(scope, input.CommandID)
		if e != nil {
			return e
		}
		if exists {
			if receipt.Kind == "resource_adjustment" && receipt.InputHash == hash {
				ack.Status = "replayed"
				return nil
			}
			return ErrAdminConflict
		}
		resource, found, e := rx.GetManagedResource(auth.Namespace, input.ResourceID, input.WindowKey)
		if e != nil {
			return e
		}
		if !found {
			return ErrAdminNotFound
		}
		if resource.Definition.Kind != "counter" {
			return ErrAdminInvalid
		}
		if input.Delta < 0 {
			charge, exists, e := rx.GetOriginalCharge(input.ResourceID, input.WindowKey, input.OriginalEventID)
			if e != nil {
				return e
			}
			reversal, rangeErr := domain.SubUnits(0, input.Delta)
			if rangeErr != nil || !exists || reversal > charge {
				return ErrAdminConflict
			}
		}
		delta := domain.ResourceDelta{ResourceID: input.ResourceID, WindowKey: input.WindowKey, ExpectedVersion: resource.Snapshot.Version, Adjustment: input.Delta}
		if e = resources.ValidateChange(resource.Snapshot, delta); e != nil {
			return ErrAdminConflict
		}
		raw, e := json.Marshal(ack)
		if e != nil {
			return e
		}
		if e = tx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: input.CommandID, Kind: "resource_adjustment", InputHash: hash, ResultJSON: raw, CreatedAt: now}); e != nil {
			return e
		}
		applied, e := rx.ApplyAdminAdjustment(delta, scope, input.CommandID, input.OriginalEventID, input.EvidenceRefs, now)
		if e != nil {
			return e
		}
		if !applied {
			return ErrAdminConflict
		}
		if input.Delta > 0 {
			if e = rx.InsertAdminOverrunHold(auth.Namespace, input.ResourceID, input.CommandID, input.EvidenceRefs, now); e != nil {
				return e
			}
		}
		return tx.InsertAudit(store.AuditRecord{AuditID: domain.ID(string(scope) + "/" + string(input.CommandID)), PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Action: "resource.adjust", TargetRef: input.ResourceID, EvidenceRef: input.EvidenceRefs[0], CreatedAt: now})
	}})
	if err != nil {
		return domain.CommandAck{}, err
	}
	return ack, nil
}

func (s *AdminService) ClearHold(ctx context.Context, auth domain.AuthContext, input domain.ClearHoldInput) (domain.CommandAck, error) {
	if !s.authorized(auth) {
		return domain.CommandAck{}, ErrAdminForbidden
	}
	if input.CommandID == "" || input.HoldID == "" || len(input.EvidenceIDs) == 0 || len(uniqueIDs(input.EvidenceIDs)) != len(input.EvidenceIDs) {
		return domain.CommandAck{}, ErrAdminInvalid
	}
	for _, ref := range input.EvidenceIDs {
		if ref == "" {
			return domain.CommandAck{}, ErrAdminInvalid
		}
	}
	hash, _, err := commandHash(input)
	if err != nil {
		return domain.CommandAck{}, err
	}
	scope := domain.ID("clear-hold/" + string(auth.Namespace) + "/" + string(input.HoldID))
	ack := domain.CommandAck{CommandID: input.CommandID, Status: "applied"}
	err = s.st.SubmitWrite(ctx, store.WriteCommand{ID: string(scope) + "/" + string(input.CommandID), Control: true, Run: func(tx store.WriterTx) error {
		rx, ok := tx.(store.AdminWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		now := s.clock.Now()
		if now < 0 {
			return ErrAdminInvalid
		}
		receipt, exists, e := tx.GetReceipt(scope, input.CommandID)
		if e != nil {
			return e
		}
		if exists {
			if receipt.Kind == "hold_clear" && receipt.InputHash == hash {
				ack.Status = "replayed"
				return nil
			}
			return ErrAdminConflict
		}
		_, found, e := rx.GetManagedHold(auth.Namespace, input.HoldID)
		if e != nil {
			return e
		}
		if !found {
			return ErrAdminNotFound
		}
		raw, e := json.Marshal(ack)
		if e != nil {
			return e
		}
		if e = tx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: input.CommandID, Kind: "hold_clear", InputHash: hash, ResultJSON: raw, CreatedAt: now}); e != nil {
			return e
		}
		cleared, e := rx.ClearManagedHold(auth.Namespace, input.HoldID, input.EvidenceIDs, now)
		if e != nil {
			return e
		}
		if !cleared {
			return ErrAdminConflict
		}
		return tx.InsertAudit(store.AuditRecord{AuditID: domain.ID(string(scope) + "/" + string(input.CommandID)), PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Action: "hold.clear", TargetRef: input.HoldID, EvidenceRef: input.EvidenceIDs[0], CreatedAt: now})
	}})
	if err != nil {
		return domain.CommandAck{}, err
	}
	return ack, nil
}

func (s *AdminService) authorized(auth domain.AuthContext) bool {
	return s != nil && s.st != nil && s.clock != nil && auth.PrincipalID != "" && auth.Namespace != "" && slices.Contains(auth.Permissions, domain.PermManage)
}
