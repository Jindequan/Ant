package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"sync"

	"ant/server/internal/domain"
	"ant/server/internal/domain/resources"
	"ant/server/internal/ledger"
	"ant/server/internal/store"
)

var (
	ErrPreparationInvalid   = errors.New("ledger prepare: invalid plan")
	ErrPreparationForbidden = errors.New("ledger prepare: forbidden")
	ErrPreparationStale     = errors.New("ledger prepare: stale owner/version/control")
	ErrPreparationBlocked   = errors.New("ledger prepare: admission blocked")
	ErrPreparationConflict  = errors.New("ledger prepare: command conflict")
)

// PreparationService owns runtime-private permit handles, with pending handles
// retained internally after an uncommitted authorization failure. No failed
// transaction returns a handle. Terminal paths release both handle and
// registry records only after their durable transaction commits.
type PreparationService struct {
	st        store.Store
	runtimeID domain.ID
	clock     PreparationClock
	registry  *ledger.PermitRegistry
	mu        sync.Mutex
	handles   map[domain.ID]ledger.DispatchPermit
}

// PreparationClock is read from inside writer transactions so queued commands
// cannot use a stale caller timestamp to cross a lease, deadline, or evidence expiry.
type PreparationClock interface{ Now() domain.Millis }

func NewPreparationService(st store.Store, runtimeID domain.ID, clock PreparationClock) *PreparationService {
	return &PreparationService{st: st, runtimeID: runtimeID, clock: clock, registry: ledger.NewPermitRegistry(), handles: map[domain.ID]ledger.DispatchPermit{}}
}
func authorized(auth domain.AuthContext, namespace domain.ID) bool {
	return auth.PrincipalID != "" && auth.Namespace == namespace && slices.Contains(auth.Permissions, domain.PermExecute)
}
func commandHash(v any) (string, []byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", nil, err
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), raw, nil
}
func checkOwner(tx store.PreparationWriterTx, auth domain.AuthContext, o domain.OwnerToken, now domain.Millis) (store.ClaimOperationRecord, error) {
	if !authorized(auth, o.Key.Namespace) {
		return store.ClaimOperationRecord{}, ErrPreparationForbidden
	}
	op, ok, err := tx.GetClaimOperation(o.Key.Namespace, o.Key.OperationID)
	if err != nil {
		return op, err
	}
	if !ok || op.State != domain.Running || op.OwnerID != o.OwnerID || op.RuntimeID != o.RuntimeID || op.Generation != o.Generation || op.LeaseUntil <= now || op.DeadlineAt <= now || op.CancelRequestedAt != nil {
		return op, ErrPreparationStale
	}
	return op, nil
}
func guardScope(g domain.GuardEvidence, b store.PreparationBundle) bool {
	if g.Scope == b.AccountID || g.Scope == b.Offer.BindingID || g.Scope == domain.ID(b.Offer.Domain) {
		return true
	}
	for _, r := range b.Resources {
		if g.Scope == r.Definition.ResourceID {
			return true
		}
	}
	return false
}
func checkBundle(auth domain.AuthContext, plan domain.ExecutionPlan, b store.PreparationBundle, guards []domain.GuardEvidence, now domain.Millis) error {
	if !authorized(auth, b.Pool.Namespace) {
		return ErrPreparationForbidden
	}
	if plan.PlanID == "" || plan.PoolID != b.Pool.PoolID || plan.PoolVersion != b.Pool.Version || plan.OfferID != b.Offer.OfferID || plan.MemberPriority != b.Member.Priority || plan.AccountID != b.AccountID || plan.BindingID != b.Offer.BindingID || plan.ProviderTypeID != b.ProviderTypeID || plan.Endpoint == "" || plan.Endpoint != b.Endpoint || plan.CredentialRef != b.CredentialRef || plan.CredentialRevision == "" || plan.CredentialRevision != b.CredentialRevision || plan.AdapterRevision == "" || plan.DomainRevision == "" || plan.ReportMappingRevision == "" || plan.ConfigRevision != b.ActiveRevision {
		return ErrPreparationStale
	}
	if !b.Pool.Enabled || !b.Member.Enabled || !b.Offer.Enabled || !b.AccountEnabled || !b.BindingEnabled || b.RestoreGuard != 0 || b.Held {
		return ErrPreparationBlocked
	}
	for _, g := range guards {
		if !guardScope(g, b) || g.ID == "" || !g.Allowed || g.ExpiresAt <= now || g.ObservedAt > now {
			return ErrPreparationBlocked
		}
		matched := false
		for _, current := range b.Guards {
			if current.Scope == g.Scope && current.ID == g.ID && current.Revision == g.Revision && current.Allowed && current.ExpiresAt > now && current.ObservedAt <= now {
				matched = true
			}
		}
		if !matched {
			return ErrPreparationStale
		}
	}
	// Every applicable persisted guard is mandatory; callers cannot omit a
	// guard to bypass a denied, expired or revised current admission policy.
	for _, g := range b.Guards {
		if !guardScope(g, b) {
			continue
		}
		found := false
		for _, claimed := range guards {
			if claimed.ID == g.ID && claimed.Scope == g.Scope && claimed.Revision == g.Revision {
				found = true
			}
		}
		if !found || !g.Allowed || g.ExpiresAt <= now {
			return ErrPreparationBlocked
		}
	}
	if len(plan.Requirements) == 0 || len(plan.Bounds) == 0 || len(b.Resources) == 0 {
		return ErrPreparationInvalid
	}
	return nil
}
func resourceSnapshots(plan domain.ExecutionPlan, b store.PreparationBundle, now domain.Millis) (map[domain.ID]domain.ResourceSnapshot, error) {
	result := map[domain.ID]domain.ResourceSnapshot{}
	for _, r := range b.Resources {
		d := r.Definition
		if d.ResourceID == "" || d.SemanticsRevision != plan.WindowRuleRevision || (d.Kind == "counter" && d.InitialStateEvidenceRef == "") || d.CapacityEvidenceRef == "" || d.ConfiguredCapacity < 0 || r.Isolated {
			return nil, ErrPreparationBlocked
		}
		if (d.OwnerKind == "account" && d.OwnerID != plan.AccountID) || (d.OwnerKind == "binding" && d.OwnerID != plan.BindingID) || (d.OwnerKind != "account" && d.OwnerKind != "binding") {
			return nil, ErrPreparationInvalid
		}
		key, _, _, err := resources.WindowAt(now, d.Window, plan.WindowRuleRevision)
		if err != nil && !errors.Is(err, resources.ErrRollingNoBounds) {
			return nil, ErrPreparationInvalid
		}
		var snap domain.ResourceSnapshot
		found := false
		for _, s := range r.States {
			if s.WindowKey == key {
				snap = s
				found = true
			}
		}
		// Only activated/verified reset state can supply capacity. Missing current
		// state rejects; preparation never initializes arbitrary window balances.
		if !found || snap.Capacity > d.ConfiguredCapacity || snap.Capacity < 0 || snap.Committed < 0 || snap.Reserved < 0 || snap.Active < 0 || snap.Uncertain < 0 {
			return nil, ErrPreparationBlocked
		}
		switch d.Kind {
		case "occupancy":
			used, err := domain.AddUnits(snap.Reserved, snap.Active)
			if err != nil {
				return nil, err
			}
			used, err = domain.AddUnits(used, snap.Uncertain)
			if err != nil {
				return nil, err
			}
			snap.Available, err = domain.SubUnits(snap.Capacity, used)
			if err != nil {
				return nil, err
			}
		case "counter":
			if d.Window.Kind == "rolling" {
				var used domain.Units
				cutoff := now - domain.Millis(d.Window.DurationMillis)
				for _, c := range r.Charges {
					if !c.Attributed || c.ChargedAt > now {
						return nil, ErrPreparationBlocked
					}
					if c.ChargedAt > cutoff && c.ChargedAt <= now {
						used, err = domain.AddUnits(used, c.Units)
						if err != nil {
							return nil, err
						}
					}
				}
				if used < 0 {
					return nil, ErrPreparationInvalid
				}
				used, err = domain.AddUnits(used, snap.Reserved)
				if err != nil {
					return nil, err
				}
				snap.Available, err = domain.SubUnits(snap.Capacity, used)
				if err != nil {
					return nil, err
				}
			} else {
				net, sumErr := domain.AddUnits(snap.Committed, snap.Adjustment)
				if sumErr != nil || net < 0 {
					return nil, ErrPreparationInvalid
				}
				used, sumErr := domain.AddUnits(net, snap.Reserved)
				if sumErr != nil {
					return nil, sumErr
				}
				if used > snap.Capacity {
					return nil, ErrPreparationBlocked
				}
				var obs *domain.ObservationEvidence
				for i := range r.Observations {
					o := &r.Observations[i]
					if o.WindowKey == key && o.ExpiresAt > now && o.ObservedAt <= now && o.AdjustmentVersionAtStart == r.AdjustmentVersions[key] {
						obs = o
						break
					}
				}
				snap.Available, err = resources.CounterAvailable(snap, obs, now, r.AdjustmentVersions[key])
				if err != nil {
					return nil, err
				}
			}
		default:
			return nil, ErrPreparationInvalid
		}
		if d.ExpiresAt != nil && *d.ExpiresAt <= now {
			snap.Available = 0
		}
		result[d.ResourceID] = snap
	}
	return result, nil
}
func validateRequirements(plan domain.ExecutionPlan, b store.PreparationBundle, snapshots map[domain.ID]domain.ResourceSnapshot, allowMetered bool, now domain.Millis) ([]domain.ReservationAllocation, error) {
	defs := map[domain.ID]domain.ResourceDefinition{}
	for _, r := range b.Resources {
		defs[r.Definition.ResourceID] = r.Definition
	}
	covered := map[domain.ID]bool{}
	var allocations []domain.ReservationAllocation
	for _, req := range plan.Requirements {
		if req.UpperBound < 0 || len(req.ResourceIDs) == 0 {
			return nil, ErrPreparationInvalid
		}
		evidenceOK := false
		for _, bound := range plan.Bounds {
			if bound.EvidenceID == req.EvidenceID && bound.MetricID == req.MetricID && bound.Unit == req.Unit && req.UpperBound <= bound.UpperBound && bound.VerifiedAt <= now && bound.ExpiresAt > now && bound.EnforcementSource != "" && bound.MappingRevision == plan.ReportMappingRevision && bound.EndpointRef != "" && bound.SourceRef != "" {
				evidenceOK = true
			}
		}
		if !evidenceOK {
			return nil, ErrPreparationBlocked
		}
		ids := append([]domain.ID(nil), req.ResourceIDs...)
		if req.Mode == "provider_entitlement_waterfall" {
			orders := map[int64]bool{}
			for _, id := range ids {
				order := defs[id].ProviderOrder
				if order == nil || orders[*order] {
					return nil, ErrPreparationInvalid
				}
				orders[*order] = true
			}
			sort.Slice(ids, func(i, j int) bool {
				di, dj := defs[ids[i]], defs[ids[j]]
				if di.ProviderOrder == nil || dj.ProviderOrder == nil {
					return ids[i] < ids[j]
				}
				return *di.ProviderOrder < *dj.ProviderOrder
			})
		}
		var pools []resources.EntitlementPool
		for _, id := range ids {
			d, ok := defs[id]
			snap := snapshots[id]
			if !ok || covered[id] || d.MetricID != req.MetricID || d.Unit != req.Unit {
				return nil, ErrPreparationInvalid
			}
			covered[id] = true
			switch req.Mode {
			case "all_constraints":
				if d.CommercialRole != "limit" {
					return nil, ErrPreparationInvalid
				}
				if d.Metered && !allowMetered {
					return nil, ErrPreparationBlocked
				}
				if req.UpperBound > snap.Available {
					return nil, ErrPreparationBlocked
				}
				allocations = append(allocations, domain.ReservationAllocation{ResourceID: id, WindowKey: snap.WindowKey, Units: req.UpperBound, Kind: d.Kind})
			case "provider_entitlement_waterfall":
				if d.CommercialRole != "entitlement" || d.Kind != "counter" || d.ProviderOrder == nil {
					return nil, ErrPreparationInvalid
				}
				pools = append(pools, resources.EntitlementPool{PoolID: id, Available: snap.Available, Metered: d.Metered, Expired: d.ExpiresAt != nil && *d.ExpiresAt <= now})
			default:
				return nil, ErrPreparationInvalid
			}
		}
		if req.Mode == "provider_entitlement_waterfall" {
			a, admitted, _ := resources.AllocateWaterfall(pools, req.UpperBound, allowMetered)
			if !admitted {
				return nil, ErrPreparationBlocked
			}
			for _, q := range a {
				snap := snapshots[q.PoolID]
				allocations = append(allocations, domain.ReservationAllocation{ResourceID: q.PoolID, WindowKey: snap.WindowKey, Units: q.Units, Kind: snap.Kind})
			}
		}
	}
	if len(covered) != len(defs) {
		return nil, ErrPreparationInvalid
	}
	sort.Slice(allocations, func(i, j int) bool { return allocations[i].ResourceID < allocations[j].ResourceID })
	return allocations, nil
}
func (s *PreparationService) Quote(ctx context.Context, auth domain.AuthContext, plan domain.ExecutionPlan, now domain.Millis) ([]domain.ResourceSnapshot, error) {
	if s.st == nil || now < 0 || auth.Namespace == "" {
		return nil, ErrPreparationInvalid
	}
	var result []domain.ResourceSnapshot
	err := s.st.Read(ctx, func(rx store.ReaderTx) error {
		tx, ok := rx.(store.PreparationReaderTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		b, err := tx.GetPreparationBundle(plan, now)
		if err != nil {
			return err
		}
		if !authorized(auth, b.Pool.Namespace) {
			return ErrPreparationForbidden
		}
		snapshots, err := resourceSnapshots(plan, b, now)
		if err != nil {
			return err
		}
		for _, r := range b.Resources {
			result = append(result, snapshots[r.Definition.ResourceID])
		}
		return nil
	})
	return result, err
}

func preparationRejection(err error, scope domain.ID) (domain.Reason, bool) {
	switch {
	case errors.Is(err, ErrPreparationBlocked):
		return domain.Reason{Group: "admission", Code: "preparation_blocked", ScopeRef: scope}, true
	case errors.Is(err, ErrPreparationStale):
		return domain.Reason{Group: "concurrency", Code: "preparation_stale", ScopeRef: scope}, true
	case errors.Is(err, ErrPreparationInvalid):
		return domain.Reason{Group: "plan", Code: "preparation_invalid", ScopeRef: scope}, true
	default:
		return domain.Reason{}, false
	}
}

func (s *PreparationService) TryPrepare(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, attemptID domain.ID, plan domain.ExecutionPlan, guards []domain.GuardEvidence, digest string, decisionTrace []byte, now domain.Millis) (ledger.PrepareReply, error) {
	if s.st == nil || s.clock == nil || attemptID == "" || digest == "" || owner.RuntimeID != s.runtimeID || now < 0 || len(decisionTrace) > 64*1024 || (len(decisionTrace) > 0 && !json.Valid(decisionTrace)) {
		return ledger.PrepareReply{}, ErrPreparationInvalid
	}
	if len(decisionTrace) == 0 {
		decisionTrace = []byte(`{}`)
	}
	hash, raw, err := commandHash(struct {
		Owner         domain.OwnerToken
		Plan          domain.ExecutionPlan
		Guards        []domain.GuardEvidence
		Digest        string
		DecisionTrace json.RawMessage
	}{owner, plan, guards, digest, decisionTrace})
	if err != nil {
		return ledger.PrepareReply{}, err
	}
	scope := domain.ID("prepare/" + string(owner.Key.Namespace))
	var reply ledger.PrepareReply
	err = s.st.SubmitWrite(ctx, store.WriteCommand{ID: string(scope) + "/" + string(attemptID), Run: func(w store.WriterTx) error {
		txNow := s.clock.Now()
		if txNow < 0 {
			return ErrPreparationInvalid
		}
		tx, ok := w.(store.PreparationWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		if !authorized(auth, owner.Key.Namespace) {
			return ErrPreparationForbidden
		}
		if receipt, found, err := w.GetReceipt(scope, attemptID); err != nil {
			return err
		} else if found {
			if receipt.InputHash != hash {
				return ErrPreparationConflict
			}
			if receipt.Kind == "prepare_blocked" {
				return json.Unmarshal(receipt.ResultJSON, &reply)
			}
			if receipt.Kind != "prepare" {
				return ErrPreparationConflict
			}
			a, ok, err := tx.GetPreparationAttempt(attemptID)
			if err != nil {
				return err
			}
			if !ok || a.Key != owner.Key || (a.Phase != "prepared" && a.Phase != "executing") {
				return ErrPreparationStale
			}
			alloc := make([]domain.ReservationAllocation, len(a.Allocations))
			for i, r := range a.Allocations {
				alloc[i] = r.ReservationAllocation
			}
			reply = ledger.PrepareReply{Kind: "prepared", Attempt: &domain.PreparedAttempt{AttemptID: attemptID, Plan: a.Plan, RequestDigest: a.RequestDigest, Allocations: alloc}}
			return nil
		}
		op, err := checkOwner(tx, auth, owner, txNow)
		if err != nil {
			return err
		}
		reject := func(cause error) error {
			reason, ok := preparationRejection(cause, plan.PlanID)
			if !ok {
				return cause
			}
			reply = ledger.PrepareReply{Kind: "blocked", Reasons: []domain.Reason{reason}}
			resultJSON, marshalErr := json.Marshal(reply)
			if marshalErr != nil {
				return marshalErr
			}
			snapshot, marshalErr := json.Marshal(struct {
				AttemptID domain.ID       `json:"attemptId"`
				PlanID    domain.ID       `json:"planId"`
				PoolID    domain.ID       `json:"poolId"`
				Reasons   []domain.Reason `json:"reasons"`
				InputHash string          `json:"inputHash"`
			}{attemptID, plan.PlanID, plan.PoolID, reply.Reasons, hash})
			if marshalErr != nil {
				return marshalErr
			}
			if err := tx.InsertPreparationDecision(owner.Key, attemptID, "rejected", snapshot, decisionTrace, txNow); err != nil {
				return err
			}
			return w.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: attemptID, Kind: "prepare_blocked", InputHash: hash, ResultJSON: resultJSON, CreatedAt: txNow})
		}
		if op.PoolID != plan.PoolID || op.PermitsIssued >= op.MaxAttempts {
			cause := ErrPreparationBlocked
			if op.PoolID != plan.PoolID {
				cause = ErrPreparationInvalid
			}
			return reject(cause)
		}
		var strategy domain.ExecuteInput
		if err = json.Unmarshal(op.StrategyJSON, &strategy); err != nil {
			return err
		}
		b, err := tx.GetPreparationBundle(plan, txNow)
		if err != nil {
			return err
		}
		if strategy.Request.Domain != b.Offer.Domain || strategy.Request.CapabilityProfile != b.Offer.CapabilityProfile {
			return reject(ErrPreparationInvalid)
		}
		if strategy.Affinity != nil && strategy.Affinity.Mode == "required" && ((strategy.Affinity.AccountID != "" && strategy.Affinity.AccountID != plan.AccountID) || (strategy.Affinity.BindingID != "" && strategy.Affinity.BindingID != plan.BindingID)) {
			return ErrPreparationForbidden
		}
		if err = checkBundle(auth, plan, b, guards, txNow); err != nil {
			if errors.Is(err, ErrPreparationForbidden) {
				return err
			}
			return reject(err)
		}
		if active, err := tx.HasActivePreparationAttempt(owner.Key); err != nil {
			return err
		} else if active {
			return ErrPreparationConflict
		}
		snapshots, err := resourceSnapshots(plan, b, txNow)
		if err != nil {
			return reject(err)
		}
		allocations, err := validateRequirements(plan, b, snapshots, strategy.AllowMetered && b.AllowMetered, txNow)
		if err != nil {
			return reject(err)
		}
		a := store.PreparationAttempt{AttemptID: attemptID, Key: owner.Key, Generation: owner.Generation, Phase: "prepared", Plan: plan, Guards: guards, RequestDigest: digest, LeaseUntil: op.LeaseUntil}
		for _, allocation := range allocations {
			d := domain.ResourceDelta{ResourceID: allocation.ResourceID, WindowKey: allocation.WindowKey, ExpectedVersion: snapshots[allocation.ResourceID].Version, Reserved: allocation.Units}
			if err = ledger.ValidatePreparationChange(snapshots[allocation.ResourceID], d, allocation.Units, "reserve"); err != nil {
				return reject(ErrPreparationInvalid)
			}
			a.Allocations = append(a.Allocations, domain.ReservationState{ReservationAllocation: allocation, ReservationID: domain.ID(string(attemptID) + "/" + string(allocation.ResourceID)), AttemptID: attemptID, Phase: "reserved"})
		}
		if err = tx.InsertPreparationAttempt(a, hash, txNow); err != nil {
			return err
		}
		for _, allocation := range allocations {
			d := domain.ResourceDelta{ResourceID: allocation.ResourceID, WindowKey: allocation.WindowKey, ExpectedVersion: snapshots[allocation.ResourceID].Version, Reserved: allocation.Units}
			if applied, err := tx.ApplyPreparationDelta(d, attemptID, scope, "reserve", txNow); err != nil {
				return err
			} else if !applied {
				return ErrPreparationStale
			}
		}
		if err = w.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: attemptID, Kind: "prepare", InputHash: hash, ResultJSON: []byte(`{"prepared":true}`), CreatedAt: txNow}); err != nil {
			return err
		}
		if err = tx.InsertPreparationDecision(owner.Key, attemptID, "prepare", raw, decisionTrace, txNow); err != nil {
			return err
		}
		events, ok := w.(store.OperationEventWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		eventPayload, err := json.Marshal(struct {
			AttemptID domain.ID `json:"attemptId"`
			OfferID   domain.ID `json:"offerId"`
		}{AttemptID: attemptID, OfferID: plan.OfferID})
		if err != nil {
			return err
		}
		if err = events.AppendOperationEvent(owner.Key, "attempt_prepared", eventPayload, txNow); err != nil {
			return err
		}
		reply = ledger.PrepareReply{Kind: "prepared", Attempt: &domain.PreparedAttempt{AttemptID: attemptID, Plan: plan, RequestDigest: digest, Allocations: allocations}}
		return nil
	}})
	if err != nil {
		return ledger.PrepareReply{}, err
	}
	return reply, nil
}
