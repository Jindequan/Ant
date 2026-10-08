// Package application contains use-case orchestration above the domain and
// store ports. It never issues SQL or provider requests.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"ant/server/internal/config"
	"ant/server/internal/domain"
	"ant/server/internal/domain/pools"
	"ant/server/internal/domain/resources"
	"ant/server/internal/store"
)

var (
	ErrConfigInvalid       = errors.New("config service: invalid input")
	ErrConfigForbidden     = errors.New("config service: forbidden")
	ErrConfigNotFound      = errors.New("config service: draft not found")
	ErrConfigConflict      = errors.New("config service: version conflict")
	ErrConfigNotExecutable = errors.New("config service: draft is not executable")
	ErrConfigStore         = errors.New("config service: store lacks draft transaction support")
)

// Clock is supplied by the composition root so evidence expiry is testable.
type Clock interface{ Now() domain.Millis }

// ConfigDraftInput uses caller-supplied stable IDs. ExpectedVersion is zero
// only for creation; updates are compare-and-swap writes.
type ConfigDraftInput struct {
	CommandID       domain.ID
	DraftID         domain.ID
	BaseRevision    domain.ID
	ExpectedVersion int64
	Definition      json.RawMessage
}

type ConfigService struct {
	store   store.Store
	catalog config.ProviderCatalog
	clock   Clock
}

// resultSlot synchronizes callback results with SubmitWrite's caller. The
// caller may return on context cancellation while the queued callback finishes.
type resultSlot[T any] struct {
	mu    sync.Mutex
	value T
}

func (s *resultSlot[T]) set(value T) {
	s.mu.Lock()
	s.value = value
	s.mu.Unlock()
}

func (s *resultSlot[T]) get() T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func NewConfigService(st store.Store, catalog config.ProviderCatalog, clock Clock) (*ConfigService, error) {
	if st == nil || catalog == nil || clock == nil {
		return nil, ErrConfigInvalid
	}
	return &ConfigService{store: st, catalog: catalog, clock: clock}, nil
}

func authorizeConfig(auth domain.AuthContext) error {
	if auth.PrincipalID == "" || auth.Namespace == "" || !slices.Contains(auth.Permissions, domain.PermConfigure) {
		return ErrConfigForbidden
	}
	return nil
}

func authorizeConfigRead(auth domain.AuthContext) error {
	if auth.PrincipalID == "" || auth.Namespace == "" ||
		(!slices.Contains(auth.Permissions, domain.PermRead) && !slices.Contains(auth.Permissions, domain.PermConfigure)) {
		return ErrConfigForbidden
	}
	return nil
}

// SaveDraft persists even structurally invalid configuration so the caller can
// repair it. Secret-like fields are rejected before persistence: a malformed
// draft must never become a side door for storing cleartext credentials.
func (s *ConfigService) SaveDraft(ctx context.Context, auth domain.AuthContext, in ConfigDraftInput) (domain.ConfigDraft, error) {
	if s == nil || authorizeConfig(auth) != nil {
		return domain.ConfigDraft{}, ErrConfigForbidden
	}
	if in.DraftID == "" || len(in.Definition) == 0 || in.ExpectedVersion < 0 || containsSecretMaterial(in.Definition) {
		return domain.ConfigDraft{}, ErrConfigInvalid
	}
	if in.CommandID != "" && len(in.CommandID) > 128 {
		return domain.ConfigDraft{}, ErrConfigInvalid
	}
	inputJSON, err := json.Marshal(struct {
		DraftID         domain.ID
		BaseRevision    domain.ID
		ExpectedVersion int64
		Definition      json.RawMessage
	}{in.DraftID, in.BaseRevision, in.ExpectedVersion, in.Definition})
	if err != nil {
		return domain.ConfigDraft{}, ErrConfigInvalid
	}
	inputDigest := sha256.Sum256(inputJSON)
	inputHash := hex.EncodeToString(inputDigest[:])
	validation := s.validate(in.Definition)
	issuesJSON, err := json.Marshal(validation.Issues)
	if err != nil {
		return domain.ConfigDraft{}, ErrConfigInvalid
	}
	var result resultSlot[domain.ConfigDraft]
	commandID := "config-save-draft/" + string(auth.PrincipalID) + "/" + string(in.DraftID)
	if in.CommandID != "" {
		commandID = "config-save-draft/" + string(in.CommandID)
	}
	err = s.store.SubmitWrite(ctx, store.WriteCommand{ID: commandID, Run: func(tx store.WriterTx) error {
		drafts, ok := tx.(store.ConfigDraftWriterTx)
		if !ok {
			return ErrConfigStore
		}
		scope := domain.ID("config-save-draft/" + string(auth.PrincipalID))
		if in.CommandID != "" {
			receipt, found, err := tx.GetReceipt(scope, in.CommandID)
			if err != nil {
				return err
			}
			if found {
				if receipt.Kind != "config_save_draft" || receipt.InputHash != inputHash {
					return ErrConfigConflict
				}
				var replay domain.ConfigDraft
				if err := json.Unmarshal(receipt.ResultJSON, &replay); err != nil {
					return err
				}
				result.set(replay)
				return nil
			}
		}
		persistResult := func(rec store.ConfigDraftRecord) error {
			value := toDomainDraft(rec)
			if in.CommandID != "" {
				resultJSON, err := json.Marshal(value)
				if err != nil {
					return err
				}
				if err := tx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: in.CommandID, Kind: "config_save_draft",
					InputHash: inputHash, ResultJSON: resultJSON, CreatedAt: s.clock.Now()}); err != nil {
					return err
				}
			}
			result.set(value)
			return nil
		}
		existing, found, err := drafts.GetConfigDraft(in.DraftID)
		if err != nil {
			return err
		}
		active, hasActive, err := tx.GetActiveConfig()
		if err != nil {
			return err
		}
		if hasActive {
			current, exists, err := tx.GetConfigRevision(active.Revision)
			if err != nil {
				return err
			}
			if !exists || !namespaceAllowed(current.DefinitionJSON, auth.Namespace) {
				return ErrConfigForbidden
			}
		}
		if in.ExpectedVersion == 0 {
			if found {
				return ErrConfigConflict
			}
			base := in.BaseRevision
			if base == "" {
				if hasActive {
					base = active.Revision
				}
			}
			if base != "" {
				if _, exists, err := tx.GetConfigRevision(base); err != nil {
					return err
				} else if !exists {
					return ErrConfigConflict
				}
			}
			now := s.clock.Now()
			rec := store.ConfigDraftRecord{DraftID: in.DraftID, PrincipalID: auth.PrincipalID, BaseRevision: base,
				Version: 1, Status: "editing", DefinitionJSON: slices.Clone(in.Definition), IssuesJSON: issuesJSON, CreatedAt: now, UpdatedAt: now}
			if err := drafts.InsertConfigDraft(rec); err != nil {
				return err
			}
			return persistResult(rec)
		}
		if !found || existing.PrincipalID != auth.PrincipalID || existing.Version != in.ExpectedVersion || existing.Status == "activated" || existing.Status == "archived" {
			return ErrConfigConflict
		}
		if namespaceMismatch(existing.DefinitionJSON, auth.Namespace) {
			return ErrConfigForbidden
		}
		base := in.BaseRevision
		if base == "" {
			base = existing.BaseRevision
		}
		rec := existing
		rec.BaseRevision, rec.Version, rec.Status = base, existing.Version+1, "editing"
		rec.DefinitionJSON, rec.IssuesJSON, rec.UpdatedAt = slices.Clone(in.Definition), issuesJSON, s.clock.Now()
		applied, err := drafts.UpdateConfigDraftCAS(auth.PrincipalID, existing.Version, rec)
		if err != nil {
			return err
		}
		if !applied {
			return ErrConfigConflict
		}
		return persistResult(rec)
	}})
	if err != nil {
		return domain.ConfigDraft{}, err
	}
	return result.get(), nil
}

// ValidateDraft recompiles the persisted definition against the currently
// injected verified catalog and records issues using a draft-version CAS.
func (s *ConfigService) ValidateDraft(ctx context.Context, auth domain.AuthContext, draftID domain.ID, expectedVersion int64) (domain.ConfigDraft, error) {
	if s == nil || authorizeConfig(auth) != nil {
		return domain.ConfigDraft{}, ErrConfigForbidden
	}
	if draftID == "" || expectedVersion < 1 {
		return domain.ConfigDraft{}, ErrConfigInvalid
	}
	var result resultSlot[domain.ConfigDraft]
	err := s.store.SubmitWrite(ctx, store.WriteCommand{ID: "config-validate-draft/" + string(draftID), Run: func(tx store.WriterTx) error {
		drafts, ok := tx.(store.ConfigDraftWriterTx)
		if !ok {
			return ErrConfigStore
		}
		rec, found, err := drafts.GetConfigDraft(draftID)
		if err != nil {
			return err
		}
		if !found || rec.PrincipalID != auth.PrincipalID {
			return ErrConfigNotFound
		}
		if rec.Version != expectedVersion || rec.Status == "activated" || rec.Status == "archived" {
			return ErrConfigConflict
		}
		active, hasActive, err := tx.GetActiveConfig()
		if err != nil {
			return err
		}
		if hasActive {
			current, exists, err := tx.GetConfigRevision(active.Revision)
			if err != nil {
				return err
			}
			if !exists || !namespaceAllowed(current.DefinitionJSON, auth.Namespace) {
				return ErrConfigForbidden
			}
		}
		validation := s.validateForNamespace(rec.DefinitionJSON, auth.Namespace)
		issuesJSON, err := json.Marshal(validation.Issues)
		if err != nil {
			return ErrConfigInvalid
		}
		rec.Version++
		rec.Status = "editing"
		if len(validation.Issues) == 0 {
			rec.Status = "validated"
		}
		rec.IssuesJSON, rec.UpdatedAt = issuesJSON, s.clock.Now()
		applied, err := drafts.UpdateConfigDraftCAS(auth.PrincipalID, expectedVersion, rec)
		if err != nil {
			return err
		}
		if !applied {
			return ErrConfigConflict
		}
		result.set(toDomainDraft(rec))
		return nil
	}})
	if err != nil {
		return domain.ConfigDraft{}, err
	}
	return result.get(), nil
}

// ActivateDraft revalidates the exact persisted bytes inside the writer
// transaction, checks the active and draft revisions, and atomically installs
// an immutable config revision with its receipt and draft status transition.
func (s *ConfigService) ActivateDraft(ctx context.Context, auth domain.AuthContext, in domain.ActivationInput) (domain.ActivationResult, error) {
	if s == nil || authorizeConfig(auth) != nil {
		return domain.ActivationResult{}, ErrConfigForbidden
	}
	if in.CommandID == "" || in.DraftID == "" || in.ExpectedDraftVersion < 1 {
		return domain.ActivationResult{}, ErrConfigInvalid
	}
	inputJSON, _ := json.Marshal(in)
	inputHash := sha256.Sum256(inputJSON)
	var result resultSlot[domain.ActivationResult]
	scope := domain.ID("config-activate/" + string(auth.PrincipalID))
	err := s.store.SubmitWrite(ctx, store.WriteCommand{ID: "config-activate/" + string(in.CommandID), Run: func(tx store.WriterTx) error {
		drafts, ok := tx.(store.ConfigDraftWriterTx)
		if !ok {
			return ErrConfigStore
		}
		receipt, hasReceipt, err := tx.GetReceipt(scope, in.CommandID)
		if err != nil {
			return err
		}
		if hasReceipt {
			if receipt.Kind != "config_activate" || receipt.InputHash != hex.EncodeToString(inputHash[:]) {
				return ErrConfigConflict
			}
			var replay domain.ActivationResult
			if err := json.Unmarshal(receipt.ResultJSON, &replay); err != nil {
				return err
			}
			result.set(replay)
			return nil
		}
		draft, found, err := drafts.GetConfigDraft(in.DraftID)
		if err != nil {
			return err
		}
		if !found || draft.PrincipalID != auth.PrincipalID {
			return ErrConfigNotFound
		}
		if draft.Version != in.ExpectedDraftVersion || draft.Status != "validated" {
			return ErrConfigConflict
		}
		active, hasActive, err := tx.GetActiveConfig()
		if err != nil {
			return err
		}
		currentRevision := domain.ID("")
		configVersion := int64(1)
		if hasActive {
			currentRevision, configVersion = active.Revision, active.Version
			current, exists, err := tx.GetConfigRevision(active.Revision)
			if err != nil {
				return err
			}
			if !exists || !namespaceAllowed(current.DefinitionJSON, auth.Namespace) {
				return ErrConfigForbidden
			}
		}
		if currentRevision != in.ExpectedConfigRevision || draft.BaseRevision != currentRevision {
			return ErrConfigConflict
		}
		validation := s.validateForNamespace(draft.DefinitionJSON, auth.Namespace)
		if len(validation.Issues) != 0 || validation.Compiled == nil {
			return ErrConfigNotExecutable
		}
		for _, binding := range validation.Doc.Bindings {
			present, err := drafts.HasSecretRevision(auth.Namespace, binding.CredentialRef, binding.CredentialRevision)
			if err != nil {
				return err
			}
			if !present {
				return ErrConfigNotExecutable
			}
		}
		canonical, err := json.Marshal(validation.Doc)
		if err != nil {
			return ErrConfigNotExecutable
		}
		digest := sha256.Sum256(canonical)
		revision := domain.ID(hex.EncodeToString(digest[:]))
		now := s.clock.Now()
		projection, err := buildConfigProjection(validation.Doc, validation.Compiled, now, s.catalog)
		if err != nil {
			return ErrConfigNotExecutable
		}
		projector, ok := tx.(store.ConfigProjectionWriterTx)
		if !ok {
			return ErrConfigStore
		}
		if err := projector.ApplyConfigProjection(projection); err != nil {
			if errors.Is(err, store.ErrProjectionConflict) {
				return ErrConfigConflict
			}
			return err
		}
		if err := tx.InsertConfigRevision(store.ConfigRevisionRecord{Revision: revision, DefinitionJSON: canonical, CreatedAt: now}); err != nil {
			// An identical immutable revision may already exist. Verify the
			// stored bytes before reusing its identity.
			old, exists, readErr := tx.GetConfigRevision(revision)
			if readErr != nil || !exists || !slices.Equal(old.DefinitionJSON, canonical) {
				return err
			}
		}
		if configVersion == int64(^uint64(0)>>1) {
			return ErrConfigInvalid
		}
		configVersion++
		controlVersion, err := tx.BumpControlVersion()
		if err != nil {
			return err
		}
		if err := tx.SetActiveConfig(store.ActiveConfigRecord{Revision: revision, Version: configVersion}); err != nil {
			return err
		}
		draft.Status = "activated"
		draft.Version++
		draft.UpdatedAt = now
		applied, err := drafts.UpdateConfigDraftCAS(auth.PrincipalID, in.ExpectedDraftVersion, draft)
		if err != nil {
			return err
		}
		if !applied {
			return ErrConfigConflict
		}
		activated := domain.ActivationResult{ConfigRevision: revision, ControlVersion: controlVersion}
		resultJSON, err := json.Marshal(activated)
		if err != nil {
			return err
		}
		if err := tx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: in.CommandID, Kind: "config_activate",
			InputHash: hex.EncodeToString(inputHash[:]), ResultJSON: resultJSON, CreatedAt: now}); err != nil {
			return err
		}
		result.set(activated)
		return nil
	}})
	if err != nil {
		return domain.ActivationResult{}, err
	}
	return result.get(), nil
}

func buildConfigProjection(doc *config.ConfigDocument, compiled *config.CompiledConfig, now domain.Millis, catalog config.ProviderCatalog) (store.ConfigProjection, error) {
	if doc == nil || compiled == nil || catalog == nil {
		return store.ConfigProjection{}, ErrConfigNotExecutable
	}
	projection := store.ConfigProjection{
		Accounts:  make([]store.AccountProjectionRecord, 0, len(doc.Accounts)),
		Bindings:  make([]store.BindingProjectionRecord, 0, len(doc.Bindings)),
		Resources: make([]store.ResourceProjectionRecord, 0, len(doc.Resources)),
		Offers:    make([]store.OfferProjectionRecord, 0, len(doc.Offers)),
		Pools:     make([]store.PoolProjectionRecord, 0, len(doc.Pools)),
	}
	for _, account := range doc.Accounts {
		definition, err := json.Marshal(account)
		if err != nil {
			return store.ConfigProjection{}, err
		}
		projection.Accounts = append(projection.Accounts, store.AccountProjectionRecord{
			AccountID: account.AccountID, ProviderOwnerRef: account.ProviderTypeID, Enabled: account.Enabled, DefinitionJSON: definition,
		})
	}
	for _, binding := range doc.Bindings {
		account, ok := findConfigAccount(doc.Accounts, binding.AccountID)
		if !ok {
			return store.ConfigProjection{}, ErrConfigNotExecutable
		}
		descriptor, ok := catalog.Descriptor(account.ProviderTypeID)
		if !ok || descriptor.AdapterRevision == "" {
			return store.ConfigProjection{}, ErrConfigNotExecutable
		}
		definition, err := json.Marshal(binding)
		if err != nil {
			return store.ConfigProjection{}, err
		}
		projection.Bindings = append(projection.Bindings, store.BindingProjectionRecord{
			BindingID: binding.BindingID, AccountID: binding.AccountID, AdapterRef: descriptor.AdapterRevision,
			Domain: binding.Domain, Endpoint: binding.Endpoint, CredentialRef: binding.CredentialRef,
			CredentialRevision: binding.CredentialRevision, Enabled: binding.Enabled,
			DefinitionJSON: definition, ResourceRefs: slices.Clone(binding.ResourceRefs),
		})
	}
	compiledResources := make(map[domain.ID]config.CompiledResource, len(compiled.Resources))
	for _, resource := range compiled.Resources {
		compiledResources[resource.ResourceID] = resource
	}
	for _, resource := range doc.Resources {
		cr, ok := compiledResources[resource.ResourceID]
		if !ok {
			return store.ConfigProjection{}, ErrConfigNotExecutable
		}
		capacity, err := domain.ParseUnits(resource.ConfiguredCapacity)
		if err != nil || capacity < 0 {
			return store.ConfigProjection{}, ErrConfigNotExecutable
		}
		definition := domain.ResourceDefinition{
			ResourceID: resource.ResourceID, PhysicalKey: cr.PhysicalKey, OwnerKind: resource.OwnerKind, OwnerID: resource.OwnerID,
			MetricID: cr.MetricID, Unit: cr.Unit, QuantumExp: cr.QuantumExp, Kind: cr.Kind,
			CommercialRole: cr.CommercialRole, SemanticsRef: cr.SemanticsRef, SemanticsRevision: cr.SemanticsRevision,
			Window: cr.Window, Metered: cr.Metered, Currency: cr.Currency, ProviderOrder: cr.ProviderOrder,
			ProviderFactID: resource.ProviderFactID, CapacityEvidenceRef: resource.CapacityEvidenceRef,
			ConfiguredCapacity: capacity, InitialStateEvidenceRef: resource.InitialStateEvidenceRef,
			RefillRule: slices.Clone(resource.RefillRule),
		}
		if resource.ExpiresAtMillis != nil {
			expiry := domain.Millis(*resource.ExpiresAtMillis)
			definition.ExpiresAt = &expiry
		}
		if resource.EntitlementGroupID != "" {
			definition.ProviderEntitlementGroupID = resource.EntitlementGroupID
		}
		definitionJSON, err := json.Marshal(definition)
		if err != nil {
			return store.ConfigProjection{}, err
		}
		projected := store.ResourceProjectionRecord{Definition: definition, DefinitionJSON: definitionJSON}
		// Seed only a new resource's first accounting window. Later activations
		// preserve every ledger row; a missing later window remains unknown until
		// verified observation or reconciliation materializes it.
		if definition.Kind == "occupancy" || definition.InitialStateEvidenceRef != "" {
			windowKey, start, end, err := resources.WindowAt(now, definition.Window, definition.SemanticsRevision)
			if errors.Is(err, resources.ErrRollingNoBounds) {
				err = nil
			}
			if err != nil {
				return store.ConfigProjection{}, ErrConfigNotExecutable
			}
			state := &domain.ResourceSnapshot{ResourceID: definition.ResourceID, WindowKey: windowKey, Kind: definition.Kind, MetricID: definition.MetricID, Unit: definition.Unit, Version: 1, Capacity: capacity}
			if definition.Kind == "counter" {
				remaining := cr.InitialRemaining
				if remaining < 0 {
					return store.ConfigProjection{}, ErrConfigNotExecutable
				}
				if remaining > capacity {
					remaining = capacity
				}
				state.Committed, err = domain.SubUnits(capacity, remaining)
				if err != nil {
					return store.ConfigProjection{}, ErrConfigNotExecutable
				}
			}
			projected.InitialState = state
			if start != 0 || end != 0 {
				projected.StartMS, projected.EndMS = &start, &end
			}
		}
		projection.Resources = append(projection.Resources, projected)
	}
	for _, offer := range doc.Offers {
		definition, err := json.Marshal(offer)
		if err != nil {
			return store.ConfigProjection{}, err
		}
		projection.Offers = append(projection.Offers, store.OfferProjectionRecord{
			Offer:          store.OfferRecord{OfferID: offer.OfferID, BindingID: offer.BindingID, Domain: offer.Domain, CapabilityProfile: offer.CapabilityProfile, Enabled: offer.Enabled, Version: 1},
			DefinitionJSON: definition, ResourceRefs: slices.Clone(offer.ResourceRefs),
		})
	}
	for _, pool := range doc.Pools {
		definition, err := json.Marshal(pool)
		if err != nil {
			return store.ConfigProjection{}, err
		}
		members := make([]store.PoolMemberRecord, 0, len(pool.Members))
		for _, member := range pool.Members {
			members = append(members, store.PoolMemberRecord{PoolID: pool.PoolID, OfferID: member.OfferID, Priority: member.Priority, Enabled: member.Enabled, Version: 1})
		}
		projection.Pools = append(projection.Pools, store.PoolProjectionRecord{
			Pool:           store.PoolRecord{PoolID: pool.PoolID, Namespace: pool.Namespace, Name: pool.Name, Enabled: pool.Enabled, Version: 1},
			DefinitionJSON: definition, Members: members,
		})
	}
	return projection, nil
}

func findConfigAccount(accounts []config.AccountDef, id domain.ID) (config.AccountDef, bool) {
	for _, account := range accounts {
		if account.AccountID == id {
			return account, true
		}
	}
	return config.AccountDef{}, false
}

// ReorderPool replaces a pool's complete priority set as one transaction.
// The immutable active document is revised in the same commit as the live
// pool projection so planning never observes mismatched priorities.
func (s *ConfigService) ReorderPool(ctx context.Context, auth domain.AuthContext, poolID domain.ID, in domain.ReorderPoolInput) (domain.ResourcePool, error) {
	if s == nil || authorizeConfig(auth) != nil {
		return domain.ResourcePool{}, ErrConfigForbidden
	}
	if poolID == "" || in.CommandID == "" || len(in.CommandID) > 128 || in.ExpectedPoolVersion < 1 || in.ExpectedConfigRevision == "" || len(in.Entries) == 0 {
		return domain.ResourcePool{}, ErrConfigInvalid
	}
	payload, err := json.Marshal(struct {
		PoolID domain.ID
		Input  domain.ReorderPoolInput
	}{poolID, in})
	if err != nil {
		return domain.ResourcePool{}, ErrConfigInvalid
	}
	digest := sha256.Sum256(payload)
	inputHash := hex.EncodeToString(digest[:])
	scope := domain.ID("pool-reorder/" + string(auth.PrincipalID) + "/" + string(poolID))
	var result resultSlot[domain.ResourcePool]
	err = s.store.SubmitWrite(ctx, store.WriteCommand{ID: "pool-reorder/" + string(in.CommandID), Run: func(tx store.WriterTx) error {
		receipt, found, err := tx.GetReceipt(scope, in.CommandID)
		if err != nil {
			return err
		}
		if found {
			if receipt.Kind != "pool_reorder" || receipt.InputHash != inputHash {
				return ErrConfigConflict
			}
			var replay domain.ResourcePool
			if err := json.Unmarshal(receipt.ResultJSON, &replay); err != nil {
				return err
			}
			result.set(replay)
			return nil
		}

		pool, found, err := tx.GetPool(poolID)
		if err != nil {
			return err
		}
		if !found || pool.Namespace != auth.Namespace {
			return ErrConfigNotFound
		}
		if pool.Version != in.ExpectedPoolVersion {
			return ErrConfigConflict
		}
		active, found, err := tx.GetActiveConfig()
		if err != nil {
			return err
		}
		if !found || active.Revision != in.ExpectedConfigRevision {
			return ErrConfigConflict
		}
		revision, found, err := tx.GetConfigRevision(active.Revision)
		if err != nil {
			return err
		}
		if !found || !namespaceAllowed(revision.DefinitionJSON, auth.Namespace) {
			return ErrConfigForbidden
		}
		var doc config.ConfigDocument
		if err := json.Unmarshal(revision.DefinitionJSON, &doc); err != nil {
			return ErrConfigNotExecutable
		}
		poolIndex := -1
		for i := range doc.Pools {
			if doc.Pools[i].PoolID == poolID {
				poolIndex = i
				break
			}
		}
		if poolIndex < 0 || doc.Pools[poolIndex].Namespace != auth.Namespace {
			return ErrConfigNotFound
		}
		live, err := tx.ListPoolMembers(poolID)
		if err != nil {
			return err
		}
		current := make([]domain.PoolMember, 0, len(live))
		for _, member := range live {
			current = append(current, domain.PoolMember{OfferID: member.OfferID, Priority: member.Priority, Enabled: member.Enabled, Version: member.Version})
		}
		updated, err := pools.ValidateReorderEntries(current, in.Entries)
		if err != nil {
			return ErrConfigInvalid
		}
		if len(doc.Pools[poolIndex].Members) != len(current) {
			return ErrConfigNotExecutable
		}
		memberIndex := make(map[domain.ID]int, len(doc.Pools[poolIndex].Members))
		for i, member := range doc.Pools[poolIndex].Members {
			if _, duplicate := memberIndex[member.OfferID]; duplicate {
				return ErrConfigNotExecutable
			}
			memberIndex[member.OfferID] = i
		}
		for _, member := range current {
			i, ok := memberIndex[member.OfferID]
			if !ok || doc.Pools[poolIndex].Members[i].Priority != member.Priority || doc.Pools[poolIndex].Members[i].Enabled != member.Enabled {
				return ErrConfigNotExecutable
			}
		}
		for _, member := range updated {
			i := memberIndex[member.OfferID]
			doc.Pools[poolIndex].Members[i].Priority = member.Priority
		}
		canonical, err := json.Marshal(doc)
		if err != nil {
			return ErrConfigNotExecutable
		}
		validation := s.validateForNamespace(canonical, auth.Namespace)
		if len(validation.Issues) != 0 || validation.Compiled == nil {
			return ErrConfigNotExecutable
		}
		if active.Version == int64(^uint64(0)>>1) || pool.Version == int64(^uint64(0)>>1) {
			return ErrConfigInvalid
		}
		newVersion := pool.Version + 1
		members := make([]store.PoolMemberRecord, 0, len(updated))
		for _, member := range updated {
			members = append(members, store.PoolMemberRecord{PoolID: poolID, OfferID: member.OfferID, Priority: member.Priority, Enabled: member.Enabled, Version: member.Version})
		}
		applied, err := tx.ReplacePoolMembersCAS(poolID, pool.Version, newVersion, members)
		if err != nil {
			return err
		}
		if !applied {
			return ErrConfigConflict
		}
		newRevisionHash := sha256.Sum256(canonical)
		newRevision := domain.ID(hex.EncodeToString(newRevisionHash[:]))
		if err := tx.InsertConfigRevision(store.ConfigRevisionRecord{Revision: newRevision, DefinitionJSON: canonical, CreatedAt: s.clock.Now()}); err != nil {
			old, exists, readErr := tx.GetConfigRevision(newRevision)
			if readErr != nil || !exists || !slices.Equal(old.DefinitionJSON, canonical) {
				return err
			}
		}
		if _, err := tx.BumpControlVersion(); err != nil {
			return err
		}
		if err := tx.SetActiveConfig(store.ActiveConfigRecord{Revision: newRevision, Version: active.Version + 1}); err != nil {
			return err
		}
		view := domain.ResourcePool{PoolID: pool.PoolID, Namespace: pool.Namespace, Name: pool.Name, Enabled: pool.Enabled, Version: newVersion, Members: updated}
		resultJSON, err := json.Marshal(view)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if err := tx.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: in.CommandID, Kind: "pool_reorder", InputHash: inputHash, ResultJSON: resultJSON, CreatedAt: now}); err != nil {
			return err
		}
		if err := tx.InsertAudit(store.AuditRecord{AuditID: domain.ID("pool-reorder/" + string(auth.PrincipalID) + "/" + string(in.CommandID)), PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Action: "pool.reorder", TargetRef: poolID, CreatedAt: now}); err != nil {
			return err
		}
		result.set(view)
		return nil
	}})
	if err != nil {
		return domain.ResourcePool{}, err
	}
	return result.get(), nil
}

// GetDraft returns only a caller-owned draft.
func (s *ConfigService) GetDraft(ctx context.Context, auth domain.AuthContext, draftID domain.ID) (domain.ConfigDraft, error) {
	if s == nil || authorizeConfigRead(auth) != nil {
		return domain.ConfigDraft{}, ErrConfigForbidden
	}
	var result domain.ConfigDraft
	err := s.store.Read(ctx, func(rx store.ReaderTx) error {
		drafts, ok := rx.(store.ConfigDraftReaderTx)
		if !ok {
			return ErrConfigStore
		}
		rec, found, err := drafts.GetConfigDraft(draftID)
		if err != nil {
			return err
		}
		if !found || rec.PrincipalID != auth.PrincipalID {
			return ErrConfigNotFound
		}
		if namespaceMismatch(rec.DefinitionJSON, auth.Namespace) {
			return ErrConfigNotFound
		}
		result = toDomainDraft(rec)
		return nil
	})
	return result, err
}

// Read returns the current immutable revision and never includes secret
// cleartext because the configuration schema accepts only credential refs.
func (s *ConfigService) Read(ctx context.Context, auth domain.AuthContext) (domain.ConfigDraft, error) {
	if s == nil || authorizeConfigRead(auth) != nil {
		return domain.ConfigDraft{}, ErrConfigForbidden
	}
	var result domain.ConfigDraft
	err := s.store.Read(ctx, func(rx store.ReaderTx) error {
		active, found, err := rx.GetActiveConfig()
		if err != nil {
			return err
		}
		if !found {
			return ErrConfigNotFound
		}
		revision, found, err := rx.GetConfigRevision(active.Revision)
		if err != nil {
			return err
		}
		if !found {
			return ErrConfigConflict
		}
		if !namespaceAllowed(revision.DefinitionJSON, auth.Namespace) {
			return ErrConfigForbidden
		}
		result = domain.ConfigDraft{BaseRevision: active.Revision, Version: active.Version, Status: "activated", Definition: slices.Clone(revision.DefinitionJSON)}
		return nil
	})
	return result, err
}

func (s *ConfigService) validate(raw []byte) config.DocumentValidation {
	return s.addRuntimeIssues(config.Compile(raw, s.catalog), "")
}

func (s *ConfigService) validateForNamespace(raw []byte, namespace domain.ID) config.DocumentValidation {
	return s.addRuntimeIssues(config.Compile(raw, s.catalog), namespace)
}

func (s *ConfigService) addRuntimeIssues(result config.DocumentValidation, namespace domain.ID) config.DocumentValidation {
	if result.Doc == nil {
		return result
	}
	now := s.clock.Now()
	for i, evidence := range result.Doc.Evidence {
		if evidence.ExpiresAtMillis <= now {
			result.Issues = append(result.Issues, domain.FieldIssue{Path: fmt.Sprintf("/evidence/%d/expiresAtMillis", i), MessageKey: "ant.config.evidenceExpired"})
		}
	}
	if namespace != "" {
		for i, pool := range result.Doc.Pools {
			if pool.Namespace != namespace {
				result.Issues = append(result.Issues, domain.FieldIssue{Path: fmt.Sprintf("/pools/%d/namespace", i), MessageKey: "ant.config.namespaceForbidden"})
			}
		}
		for i, grant := range result.Doc.Grants {
			if grant.Namespace != namespace {
				result.Issues = append(result.Issues, domain.FieldIssue{Path: fmt.Sprintf("/grants/%d/namespace", i), MessageKey: "ant.config.namespaceForbidden"})
			}
		}
	}
	if len(result.Issues) != 0 {
		result.Compiled = nil
	}
	return result
}

func toDomainDraft(rec store.ConfigDraftRecord) domain.ConfigDraft {
	var issues []domain.FieldIssue
	_ = json.Unmarshal(rec.IssuesJSON, &issues)
	return domain.ConfigDraft{DraftID: rec.DraftID, BaseRevision: rec.BaseRevision, Version: rec.Version,
		Status: rec.Status, Definition: slices.Clone(rec.DefinitionJSON), FieldIssues: issues}
}

func namespaceAllowed(raw []byte, namespace domain.ID) bool {
	return namespace != "" && !namespaceMismatch(raw, namespace)
}

func namespaceMismatch(raw []byte, namespace domain.ID) bool {
	var doc config.ConfigDocument
	if namespace == "" || json.Unmarshal(raw, &doc) != nil {
		return false
	}
	for _, pool := range doc.Pools {
		if pool.Namespace != namespace {
			return true
		}
	}
	for _, grant := range doc.Grants {
		if grant.Namespace != namespace {
			return true
		}
	}
	return false
}

func containsSecretMaterial(raw []byte) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		// Do not persist unparsable input that may contain credential material.
		lower := strings.ToLower(string(raw))
		for _, marker := range []string{"apikey", "api_key", "password", "secret", "bearer", "token", "privatekey", "authorization"} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
		return false
	}
	return containsSecretKey(value)
}

func containsSecretKey(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			k := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			if strings.Contains(k, "apikey") || strings.Contains(k, "password") || strings.Contains(k, "secret") ||
				strings.Contains(k, "bearer") || strings.Contains(k, "token") || k == "privatekey" ||
				(strings.Contains(k, "credential") && k != "credentialref" && k != "credentialrevision") || k == "authorization" {
				return true
			}
			if containsSecretKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsSecretKey(child) {
				return true
			}
		}
	}
	return false
}
