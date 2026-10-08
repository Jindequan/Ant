package application

import (
	"context"
	"errors"
	"slices"
	"sort"

	"ant/server/internal/config"
	"ant/server/internal/domain"
	"ant/server/internal/domain/resources"
	"ant/server/internal/store"
)

var (
	ErrPoolQueryForbidden = errors.New("pool query: forbidden")
	ErrPoolQueryNotFound  = errors.New("pool query: not found")
	ErrPoolQueryStore     = errors.New("pool query: store lacks read capability")
	ErrPoolQueryIntegrity = errors.New("pool query: active config and projections disagree")
)

type PoolProjection struct {
	Pool   domain.ResourcePool
	Groups []PoolGroupProjection
}

type PoolGroupProjection struct {
	Domain            string
	CapabilityProfile domain.ID
	Members           []PoolMemberProjection
}

type PoolMemberProjection struct {
	Member    domain.PoolMember
	Offer     domain.ResourceOffer
	Status    string
	Reasons   []domain.Reason
	Resources []PoolResourceProjection
}

type PoolResourceProjection struct {
	ResourceID               domain.ID
	WindowKey                domain.ID
	MetricID                 domain.ID
	Unit                     string
	Committed                domain.Units
	AdjustmentVersion        int64
	Available                *domain.Units
	Reserved                 domain.Units
	Active                   domain.Units
	Uncertain                domain.Units
	EvidenceStatus           string
	HoldIDs                  []domain.ID
	EncumbranceIDs           []domain.ID
	ObservationRefreshNeeded bool
}

type PoolQueries struct {
	store   store.Store
	catalog config.ProviderCatalog
	now     func() domain.Millis
}

func NewPoolQueries(st store.Store, catalog config.ProviderCatalog, clock Clock) (*PoolQueries, error) {
	if st == nil || catalog == nil || clock == nil {
		return nil, ErrPoolQueryStore
	}
	return &PoolQueries{store: st, catalog: catalog, now: clock.Now}, nil
}

func authorizePoolRead(auth domain.AuthContext) error {
	if auth.PrincipalID == "" || auth.Namespace == "" ||
		(!slices.Contains(auth.Permissions, domain.PermRead) && !slices.Contains(auth.Permissions, domain.PermConfigure)) {
		return ErrPoolQueryForbidden
	}
	return nil
}

func (q *PoolQueries) Get(ctx context.Context, auth domain.AuthContext, poolID domain.ID) (PoolProjection, error) {
	if q == nil || authorizePoolRead(auth) != nil {
		return PoolProjection{}, ErrPoolQueryForbidden
	}
	if poolID == "" {
		return PoolProjection{}, ErrPoolQueryNotFound
	}
	var projection PoolProjection
	err := q.store.Read(ctx, func(rx store.ReaderTx) error {
		reader, doc, err := q.readableSnapshot(rx, auth.Namespace)
		if err != nil {
			return err
		}
		pool, found, err := rx.GetPool(poolID)
		if err != nil {
			return err
		}
		if !found || pool.Namespace != auth.Namespace {
			return ErrPoolQueryNotFound
		}
		if _, exists := findPool(doc.Pools, poolID); !exists {
			return ErrPoolQueryNotFound
		}
		projection, err = projectPool(reader, q.catalog, doc, pool, q.now())
		return err
	})
	return projection, err
}

func (q *PoolQueries) List(ctx context.Context, auth domain.AuthContext) ([]PoolProjection, error) {
	if q == nil || authorizePoolRead(auth) != nil {
		return nil, ErrPoolQueryForbidden
	}
	projections := make([]PoolProjection, 0)
	now := q.now()
	err := q.store.Read(ctx, func(rx store.ReaderTx) error {
		_, hasActive, err := rx.GetActiveConfig()
		if err != nil {
			return err
		}
		if !hasActive {
			projections = []PoolProjection{}
			return nil
		}
		reader, doc, err := q.readableSnapshot(rx, auth.Namespace)
		if err != nil {
			return err
		}
		activePoolIDs := make(map[domain.ID]bool, len(doc.Pools))
		for _, pool := range doc.Pools {
			if pool.Namespace == auth.Namespace {
				activePoolIDs[pool.PoolID] = true
			}
		}
		pools, err := rx.ListPoolsByNamespace(auth.Namespace)
		if err != nil {
			return err
		}
		projections = make([]PoolProjection, 0, len(pools))
		for _, pool := range pools {
			if !activePoolIDs[pool.PoolID] {
				continue
			}
			view, err := projectPool(reader, q.catalog, doc, pool, now)
			if err != nil {
				return err
			}
			projections = append(projections, view)
		}
		return nil
	})
	return projections, err
}

type poolSnapshotReader interface {
	store.OfferReaderTx
	store.PreparationReaderTx
	store.SecretReaderTx
}

func (q *PoolQueries) readableSnapshot(rx store.ReaderTx, namespace domain.ID) (poolSnapshotReader, *config.ConfigDocument, error) {
	reader, ok := rx.(poolSnapshotReader)
	if !ok {
		return nil, nil, ErrPoolQueryStore
	}
	active, found, err := rx.GetActiveConfig()
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, ErrPoolQueryNotFound
	}
	revision, found, err := rx.GetConfigRevision(active.Revision)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, ErrPoolQueryNotFound
	}
	if !namespaceAllowed(revision.DefinitionJSON, namespace) {
		return nil, nil, ErrPoolQueryNotFound
	}
	validated := config.Validate(revision.DefinitionJSON, q.catalog)
	if len(validated.Issues) != 0 || validated.Compiled == nil || validated.Doc == nil {
		return nil, nil, ErrPoolQueryIntegrity
	}
	return reader, validated.Doc, nil
}

func projectPool(reader poolSnapshotReader, catalog config.ProviderCatalog, doc *config.ConfigDocument, pool store.PoolRecord, now domain.Millis) (PoolProjection, error) {
	poolDef, found := findPool(doc.Pools, pool.PoolID)
	if !found || pool.Namespace != poolDef.Namespace || pool.Name != poolDef.Name || pool.Enabled != poolDef.Enabled {
		return PoolProjection{}, ErrPoolQueryIntegrity
	}
	live, err := reader.ListPoolMembers(pool.PoolID)
	if err != nil {
		return PoolProjection{}, err
	}
	if len(live) != len(poolDef.Members) {
		return PoolProjection{}, ErrPoolQueryIntegrity
	}
	defMembers := make(map[domain.ID]config.PoolMemberDef, len(poolDef.Members))
	for _, member := range poolDef.Members {
		if _, duplicate := defMembers[member.OfferID]; duplicate {
			return PoolProjection{}, ErrPoolQueryIntegrity
		}
		defMembers[member.OfferID] = member
	}
	view := PoolProjection{Pool: domain.ResourcePool{PoolID: pool.PoolID, Namespace: pool.Namespace, Name: pool.Name, Enabled: pool.Enabled, Version: pool.Version, Members: make([]domain.PoolMember, 0, len(live))}}
	groups := map[string]*PoolGroupProjection{}
	for _, liveMember := range live {
		defMember, ok := defMembers[liveMember.OfferID]
		if !ok || defMember.Priority != liveMember.Priority || defMember.Enabled != liveMember.Enabled {
			return PoolProjection{}, ErrPoolQueryIntegrity
		}
		member := domain.PoolMember{OfferID: liveMember.OfferID, Priority: liveMember.Priority, Enabled: liveMember.Enabled, Version: liveMember.Version}
		view.Pool.Members = append(view.Pool.Members, member)
		bundle, err := reader.GetPreparationBundle(domain.ExecutionPlan{PoolID: pool.PoolID, OfferID: liveMember.OfferID}, now)
		if err != nil {
			return PoolProjection{}, ErrPoolQueryIntegrity
		}
		offerDef, exists := findPoolOffer(doc.Offers, liveMember.OfferID)
		if !exists || bundle.Offer.BindingID != offerDef.BindingID || bundle.Offer.Domain != offerDef.Domain || bundle.Offer.CapabilityProfile != offerDef.CapabilityProfile || bundle.Offer.Enabled != offerDef.Enabled {
			return PoolProjection{}, ErrPoolQueryIntegrity
		}
		accountDef, foundAccount := findPoolAccount(doc.Accounts, bundle.AccountID)
		bindingDef, foundBinding := findPoolBinding(doc.Bindings, bundle.Offer.BindingID)
		if !foundAccount || !foundBinding || bindingDef.AccountID != accountDef.AccountID || accountDef.ProviderTypeID != bundle.ProviderTypeID ||
			bindingDef.Endpoint != bundle.Endpoint || bindingDef.CredentialRef != bundle.CredentialRef || bindingDef.CredentialRevision != bundle.CredentialRevision ||
			accountDef.Enabled != bundle.AccountEnabled || bindingDef.Enabled != bundle.BindingEnabled {
			return PoolProjection{}, ErrPoolQueryIntegrity
		}
		offer := domain.ResourceOffer{OfferID: bundle.Offer.OfferID, BindingID: bundle.Offer.BindingID, Domain: bundle.Offer.Domain, CapabilityProfile: bundle.Offer.CapabilityProfile, Enabled: bundle.Offer.Enabled, Version: bundle.Offer.Version, ResourceRefs: slices.Clone(offerDef.ResourceRefs)}
		memberView := PoolMemberProjection{Member: member, Offer: offer, Status: "available", Reasons: []domain.Reason{}, Resources: []PoolResourceProjection{}}
		if !pool.Enabled || !member.Enabled || !offer.Enabled || !bundle.AccountEnabled || !bundle.BindingEnabled {
			memberView.Status = "disabled"
			memberView.Reasons = append(memberView.Reasons, domain.Reason{Group: "eligibility", Code: "disabled"})
		} else {
			descriptor, supported := catalog.Descriptor(bundle.ProviderTypeID)
			if !supported || !catalog.SupportsDomain(bundle.ProviderTypeID, offer.Domain) || !hasProfile(descriptor, offer.CapabilityProfile, offer.Domain) {
				memberView.Status = "unsupported"
				memberView.Reasons = append(memberView.Reasons, domain.Reason{Group: "capability", Code: "unsupported"})
			} else {
				memberView.Resources, memberView.Reasons = projectResources(bundle, offerDef.ResourceRefs, doc.Evidence, now)
				for _, evidenceID := range offerDef.RequiredEvidenceRefs {
					if !isCurrentEvidence(doc.Evidence, evidenceID, now) {
						memberView.Reasons = append(memberView.Reasons, domain.Reason{Group: "evidence", Code: "evidence_expired", EvidenceRef: evidenceID})
					}
				}
				if _, reason := secretReadiness(reader, bundle); reason != nil {
					memberView.Reasons = append(memberView.Reasons, *reason)
				}
				if bundle.RestoreGuard > 0 {
					memberView.Reasons = append(memberView.Reasons, domain.Reason{Group: "recovery", Code: "restore_recovery_required"})
				}
				if bundle.Held {
					memberView.Reasons = append(memberView.Reasons, domain.Reason{Group: "admission", Code: "scope_held"})
				}
				for _, guard := range bundle.Guards {
					if guard.Scope != bundle.AccountID && guard.Scope != bundle.Offer.BindingID && guard.Scope != domain.ID(offer.Domain) {
						continue
					}
					if !guard.Allowed || guard.ExpiresAt <= now {
						memberView.Reasons = append(memberView.Reasons, domain.Reason{Group: "guard", Code: "guard_not_current", ScopeRef: guard.Scope, EvidenceRef: guard.ID})
					}
				}
				for _, resource := range bundle.Resources {
					if resource.Definition.Metered && !bundle.AllowMetered {
						memberView.Reasons = append(memberView.Reasons, domain.Reason{Group: "spend", Code: "metered_not_allowed", ScopeRef: resource.Definition.ResourceID})
					}
				}
				if len(memberView.Reasons) > 0 {
					memberView.Status = "blocked"
				}
			}
		}
		key := string(offer.Domain) + "\x00" + string(offer.CapabilityProfile)
		group := groups[key]
		if group == nil {
			group = &PoolGroupProjection{Domain: offer.Domain, CapabilityProfile: offer.CapabilityProfile, Members: []PoolMemberProjection{}}
			groups[key] = group
		}
		group.Members = append(group.Members, memberView)
	}
	sort.Slice(view.Pool.Members, func(i, j int) bool {
		if view.Pool.Members[i].Priority != view.Pool.Members[j].Priority {
			return view.Pool.Members[i].Priority < view.Pool.Members[j].Priority
		}
		return view.Pool.Members[i].OfferID < view.Pool.Members[j].OfferID
	})
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		sort.Slice(group.Members, func(i, j int) bool {
			if group.Members[i].Member.Priority != group.Members[j].Member.Priority {
				return group.Members[i].Member.Priority < group.Members[j].Member.Priority
			}
			return group.Members[i].Member.OfferID < group.Members[j].Member.OfferID
		})
		view.Groups = append(view.Groups, *group)
	}
	if view.Pool.Members == nil {
		view.Pool.Members = []domain.PoolMember{}
	}
	if view.Groups == nil {
		view.Groups = []PoolGroupProjection{}
	}
	return view, nil
}

func hasProfile(descriptor domain.ProviderDescriptor, profileID domain.ID, domainName string) bool {
	for _, profile := range descriptor.Profiles {
		if profile.ProfileID == profileID && profile.Domain == domainName {
			return true
		}
	}
	return false
}

func findPoolOffer(offers []config.OfferDef, id domain.ID) (config.OfferDef, bool) {
	for _, offer := range offers {
		if offer.OfferID == id {
			return offer, true
		}
	}
	return config.OfferDef{}, false
}

func findPoolAccount(accounts []config.AccountDef, id domain.ID) (config.AccountDef, bool) {
	for _, account := range accounts {
		if account.AccountID == id {
			return account, true
		}
	}
	return config.AccountDef{}, false
}

func findPoolBinding(bindings []config.BindingDef, id domain.ID) (config.BindingDef, bool) {
	for _, binding := range bindings {
		if binding.BindingID == id {
			return binding, true
		}
	}
	return config.BindingDef{}, false
}

func secretReadiness(reader poolSnapshotReader, bundle store.PreparationBundle) (bool, *domain.Reason) {
	ref, found, err := reader.GetSecretReference(bundle.CredentialRef)
	if err != nil || !found || ref.Namespace != bundle.Pool.Namespace || ref.CurrentRevision != bundle.CredentialRevision {
		return false, &domain.Reason{Group: "credential", Code: "credential_unavailable"}
	}
	if _, found, err := reader.GetSecretRevision(bundle.CredentialRef, bundle.CredentialRevision); err != nil || !found {
		return false, &domain.Reason{Group: "credential", Code: "credential_unavailable"}
	}
	return true, nil
}

func projectResources(bundle store.PreparationBundle, refs []domain.ID, evidence []config.EvidenceDef, now domain.Millis) ([]PoolResourceProjection, []domain.Reason) {
	resourcesByID := make(map[domain.ID]store.PreparationResource, len(bundle.Resources))
	for _, resource := range bundle.Resources {
		if _, duplicate := resourcesByID[resource.Definition.ResourceID]; duplicate {
			return nil, []domain.Reason{{Group: "resource", Code: "resource_projection_invalid", ScopeRef: resource.Definition.ResourceID}}
		}
		resourcesByID[resource.Definition.ResourceID] = resource
	}
	views := make([]PoolResourceProjection, 0, len(refs))
	reasons := []domain.Reason{}
	for _, id := range refs {
		resource, found := resourcesByID[id]
		if !found {
			reasons = append(reasons, domain.Reason{Group: "resource", Code: "resource_projection_missing", ScopeRef: id})
			continue
		}
		def := resource.Definition
		key, _, _, windowErr := resources.WindowAt(now, def.Window, def.SemanticsRevision)
		view := PoolResourceProjection{ResourceID: id, WindowKey: key, MetricID: def.MetricID, Unit: def.Unit, Reserved: 0, Active: 0, Uncertain: 0, EvidenceStatus: "unknown", HoldIDs: slices.Clone(resource.HoldIDs), EncumbranceIDs: slices.Clone(resource.EncumbranceIDs)}
		view.AdjustmentVersion = resource.AdjustmentVersions[key]
		if view.HoldIDs == nil {
			view.HoldIDs = []domain.ID{}
		}
		if view.EncumbranceIDs == nil {
			view.EncumbranceIDs = []domain.ID{}
		}
		var snap domain.ResourceSnapshot
		foundState := false
		for _, state := range resource.States {
			if state.WindowKey == key {
				snap, foundState = state, true
				break
			}
		}
		capacityEvidenceOK := isCurrentEvidence(evidence, def.CapacityEvidenceRef, now)
		initialEvidenceOK := def.Kind == "occupancy" || isCurrentEvidence(evidence, def.InitialStateEvidenceRef, now)
		if foundState {
			view.Committed, view.Reserved, view.Active, view.Uncertain = snap.Committed, snap.Reserved, snap.Active, snap.Uncertain
			if capacityEvidenceOK && initialEvidenceOK {
				view.EvidenceStatus = "verified"
			} else if def.CapacityEvidenceRef != "" {
				view.EvidenceStatus = "expired"
			}
		}
		if def.Kind == "counter" {
			view.ObservationRefreshNeeded = true
			for i := range resource.Observations {
				o := &resource.Observations[i]
				if o.WindowKey == key && o.ExpiresAt > now && o.ObservedAt <= now && o.AdjustmentVersionAtStart == resource.AdjustmentVersions[key] {
					view.ObservationRefreshNeeded = false
					break
				}
			}
		}
		views = append(views, view)
		if errors.Is(windowErr, resources.ErrRollingNoBounds) {
			// Rolling resources share one fixed accounting identity; they do not
			// need a calendar boundary to present their current state.
			windowErr = nil
		}
		initialStateMissing := def.Kind == "counter" && def.InitialStateEvidenceRef == ""
		if !capacityEvidenceOK || !initialEvidenceOK {
			reasons = append(reasons, domain.Reason{Group: "evidence", Code: "evidence_expired", ScopeRef: id})
		}
		if windowErr != nil || !foundState || resource.Isolated || !capacityEvidenceOK || !initialEvidenceOK || initialStateMissing || (foundState && snap.Capacity > def.ConfiguredCapacity) {
			reasons = append(reasons, domain.Reason{Group: "resource", Code: "resource_state_unavailable", ScopeRef: id})
			continue
		}
		if def.ExpiresAt != nil && *def.ExpiresAt <= now {
			reasons = append(reasons, domain.Reason{Group: "resource", Code: "resource_expired", ScopeRef: id})
			continue
		}
		available, err := poolResourceAvailable(resource, snap, key, now)
		if err != nil || available < 0 {
			reasons = append(reasons, domain.Reason{Group: "resource", Code: "resource_state_unavailable", ScopeRef: id})
			continue
		}
		views[len(views)-1].Available = &available
		if available == 0 {
			reasons = append(reasons, domain.Reason{Group: "capacity", Code: "capacity_exhausted", ScopeRef: id})
		}
	}
	if len(resourcesByID) != len(refs) {
		reasons = append(reasons, domain.Reason{Group: "resource", Code: "resource_bundle_mismatch"})
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].ResourceID != views[j].ResourceID {
			return views[i].ResourceID < views[j].ResourceID
		}
		return views[i].WindowKey < views[j].WindowKey
	})
	return views, reasons
}

func isCurrentEvidence(evidence []config.EvidenceDef, id domain.ID, now domain.Millis) bool {
	if id == "" {
		return false
	}
	for _, item := range evidence {
		if item.EvidenceID == id {
			return item.VerifiedAtMillis <= now && item.ExpiresAtMillis > now
		}
	}
	return false
}

func poolResourceAvailable(resource store.PreparationResource, snap domain.ResourceSnapshot, key domain.ID, now domain.Millis) (domain.Units, error) {
	def := resource.Definition
	switch def.Kind {
	case "occupancy":
		used, err := domain.AddUnits(snap.Reserved, snap.Active)
		if err != nil {
			return 0, err
		}
		used, err = domain.AddUnits(used, snap.Uncertain)
		if err != nil {
			return 0, err
		}
		return domain.SubUnits(snap.Capacity, used)
	case "counter":
		if def.Window.Kind == "rolling" {
			cutoff := now - domain.Millis(def.Window.DurationMillis)
			var used domain.Units
			for _, charge := range resource.Charges {
				if !charge.Attributed || charge.ChargedAt > now {
					return 0, errors.New("rolling charge attribution is unavailable")
				}
				if charge.ChargedAt > cutoff && charge.ChargedAt <= now {
					var err error
					used, err = domain.AddUnits(used, charge.Units)
					if err != nil {
						return 0, err
					}
				}
			}
			used, err := domain.AddUnits(used, snap.Reserved)
			if err != nil {
				return 0, err
			}
			return domain.SubUnits(snap.Capacity, used)
		}
		var observation *domain.ObservationEvidence
		for i := range resource.Observations {
			o := &resource.Observations[i]
			if o.WindowKey == key && o.ExpiresAt > now && o.ObservedAt <= now && o.AdjustmentVersionAtStart == resource.AdjustmentVersions[key] {
				observation = o
				break
			}
		}
		return resources.CounterAvailable(snap, observation, now, resource.AdjustmentVersions[key])
	default:
		return 0, errors.New("unknown resource kind")
	}
}
