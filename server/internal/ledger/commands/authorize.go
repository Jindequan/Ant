package commands

import (
	"context"
	"encoding/json"

	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/store"
)

func recheckAttempt(tx store.PreparationWriterTx, auth domain.AuthContext, owner domain.OwnerToken, id domain.ID, now domain.Millis) (store.ClaimOperationRecord, store.PreparationAttempt, store.PreparationBundle, map[domain.ID]domain.ResourceSnapshot, error) {
	op, err := checkOwner(tx, auth, owner, now)
	if err != nil {
		return op, store.PreparationAttempt{}, store.PreparationBundle{}, nil, err
	}
	a, ok, err := tx.GetPreparationAttempt(id)
	if err != nil {
		return op, a, store.PreparationBundle{}, nil, err
	}
	if !ok || a.Key != owner.Key || a.Generation != owner.Generation {
		return op, a, store.PreparationBundle{}, nil, ErrPreparationForbidden
	}
	b, err := tx.GetPreparationBundle(a.Plan, now)
	if err != nil {
		return op, a, b, nil, err
	}
	if err = checkBundle(auth, a.Plan, b, a.Guards, now); err != nil {
		return op, a, b, nil, err
	}
	snapshots, err := resourceSnapshots(a.Plan, b, now)
	return op, a, b, snapshots, err
}

// verifyReservedPlan verifies the pinned allocation still satisfies the full
// plan and current policy. The attempt's own pending allocation is restored to
// availability only for this check, never added to persistent capacity.
func verifyReservedPlan(op store.ClaimOperationRecord, a store.PreparationAttempt, b store.PreparationBundle, snaps map[domain.ID]domain.ResourceSnapshot, now domain.Millis) error {
	var strategy domain.ExecuteInput
	if err := json.Unmarshal(op.StrategyJSON, &strategy); err != nil {
		return err
	}
	if strategy.Request.Domain != b.Offer.Domain || strategy.Request.CapabilityProfile != b.Offer.CapabilityProfile {
		return ErrPreparationStale
	}
	basis := map[domain.ID]domain.ResourceSnapshot{}
	for id, snap := range snaps {
		basis[id] = snap
	}
	for _, r := range a.Allocations {
		snap, ok := basis[r.ResourceID]
		if !ok || r.Kind != snap.Kind || r.Units < 0 {
			return ErrPreparationInvalid
		}
		var resource *store.PreparationResource
		for i := range b.Resources {
			if b.Resources[i].Definition.ResourceID == r.ResourceID {
				resource = &b.Resources[i]
				break
			}
		}
		if resource == nil {
			return ErrPreparationInvalid
		}
		definition := resource.Definition
		// Never restore the reservation onto an availability value that was
		// clamped to zero by expiry or by an authoritative provider observation.
		if definition.ExpiresAt != nil && *definition.ExpiresAt <= now {
			return ErrPreparationBlocked
		}
		if definition.Kind == "counter" && definition.Window.Kind != "rolling" && r.WindowKey == snap.WindowKey {
			for i := range resource.Observations {
				obs := &resource.Observations[i]
				if obs.WindowKey != snap.WindowKey || obs.ExpiresAt <= now || obs.ObservedAt > now || obs.AdjustmentVersionAtStart != resource.AdjustmentVersions[snap.WindowKey] {
					continue
				}
				consumed, err := domain.SubUnits(snap.Committed, obs.CommittedAtStart)
				if err != nil {
					return err
				}
				if consumed < 0 {
					consumed = 0
				}
				providerRemaining, err := domain.SubUnits(obs.Remaining, consumed)
				if err != nil {
					return err
				}
				// The fresh observation must still back all local reservations,
				// including this attempt. A clamped Available=0 cannot prove that.
				if providerRemaining < snap.Reserved {
					return ErrPreparationBlocked
				}
				break
			}
		}
		if r.WindowKey == snap.WindowKey {
			var err error
			snap.Available, err = domain.AddUnits(snap.Available, r.Units)
			if err != nil {
				return err
			}
			basis[r.ResourceID] = snap
		}
		if r.Phase != "reserved" && r.Phase != "executing" {
			return ErrPreparationStale
		}
	}
	expected, err := validateRequirements(a.Plan, b, basis, strategy.AllowMetered && b.AllowMetered, now)
	if err != nil {
		return err
	}
	if len(expected) != len(a.Allocations) {
		return ErrPreparationStale
	}
	for _, allocation := range expected {
		found := false
		for _, r := range a.Allocations {
			if r.ResourceID == allocation.ResourceID && r.Units == allocation.Units && r.Kind == allocation.Kind {
				found = true
			}
		}
		if !found {
			return ErrPreparationStale
		}
	}
	return nil
}
func findState(b store.PreparationBundle, id, key domain.ID) (domain.ResourceSnapshot, bool) {
	for _, r := range b.Resources {
		if r.Definition.ResourceID == id {
			for _, snap := range r.States {
				if snap.WindowKey == key {
					return snap, true
				}
			}
		}
	}
	return domain.ResourceSnapshot{}, false
}

func (s *PreparationService) AuthorizeDispatch(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, id domain.ID, now domain.Millis) (ledger.DispatchPermit, error) {
	if s.st == nil || s.clock == nil || id == "" || owner.RuntimeID != s.runtimeID || now < 0 {
		return ledger.DispatchPermit{}, ErrPreparationInvalid
	}
	var result ledger.DispatchPermit
	scope := domain.ID("authorize/" + string(owner.Key.Namespace))
	err := s.st.SubmitWrite(ctx, store.WriteCommand{ID: string(scope) + "/" + string(id), Control: true, Run: func(w store.WriterTx) error {
		txNow := s.clock.Now()
		if txNow < 0 {
			return ErrPreparationInvalid
		}
		tx, ok := w.(store.PreparationWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		op, a, b, snapshots, err := recheckAttempt(tx, auth, owner, id, txNow)
		if err != nil {
			return err
		}
		if p, found, err := tx.GetPreparationPermit(id); err != nil {
			return err
		} else if found {
			s.mu.Lock()
			handle, local := s.handles[id]
			s.mu.Unlock()
			if !local || p.PermitID != handle.ID() || p.Status != "granted" || p.ExpiresAt <= txNow || a.Phase != "executing" {
				return ErrPreparationStale
			}
			if err = s.registry.Validate(handle, s.runtimeID, a.RequestDigest, a.Plan.CredentialRevision); err != nil {
				return err
			}
			result = handle
			return nil
		}
		if a.Phase != "prepared" || op.PermitsIssued >= op.MaxAttempts {
			return ErrPreparationBlocked
		}
		if err = verifyReservedPlan(op, a, b, snapshots, txNow); err != nil {
			return err
		}
		// Generate/cache only inside the validated command. Failed SQL leaves the
		// unreturned handle pending; retry reuses it rather than permanently locking
		// the attempt through registry.byAttempt. No handle is returned before commit.
		s.mu.Lock()
		handle, exists := s.handles[id]
		if !exists {
			handle, err = s.registry.Issue(id, s.runtimeID, a.RequestDigest, a.Plan.CredentialRevision)
			if err != nil {
				s.mu.Unlock()
				return err
			}
			s.handles[id] = handle
		} else if err = s.registry.Validate(handle, s.runtimeID, a.RequestDigest, a.Plan.CredentialRevision); err != nil {
			s.mu.Unlock()
			return err
		}
		s.mu.Unlock()
		for _, r := range a.Allocations {
			snap := snapshots[r.ResourceID]
			if r.WindowKey != snap.WindowKey {
				old, ok := findState(b, r.ResourceID, r.WindowKey)
				if !ok || r.Kind != "counter" {
					return ErrPreparationStale
				}
				release := domain.ResourceDelta{ResourceID: r.ResourceID, WindowKey: r.WindowKey, ExpectedVersion: old.Version, Reserved: -r.Units}
				if err = ledger.ValidatePreparationChange(old, release, r.Units, "window_release"); err != nil {
					return err
				}
				if applied, err := tx.ApplyPreparationDelta(release, id, scope, "window_move", txNow); err != nil {
					return err
				} else if !applied {
					return ErrPreparationStale
				}
				reserve := domain.ResourceDelta{ResourceID: r.ResourceID, WindowKey: snap.WindowKey, ExpectedVersion: snap.Version, Reserved: r.Units}
				if err = ledger.ValidatePreparationChange(snap, reserve, r.Units, "reserve"); err != nil {
					return err
				}
				if applied, err := tx.ApplyPreparationDelta(reserve, id, scope, "window_move", txNow); err != nil {
					return err
				} else if !applied {
					return ErrPreparationStale
				}
				if applied, err := tx.MovePreparationReservation(r.ReservationID, r.WindowKey, snap.WindowKey, "reserved"); err != nil {
					return err
				} else if !applied {
					return ErrPreparationStale
				}
			} else if r.Kind == "occupancy" {
				d := domain.ResourceDelta{ResourceID: r.ResourceID, WindowKey: r.WindowKey, ExpectedVersion: snap.Version, Reserved: -r.Units, Active: r.Units}
				if err = ledger.ValidatePreparationChange(snap, d, r.Units, "activate"); err != nil {
					return err
				}
				if applied, err := tx.ApplyPreparationDelta(d, id, scope, "activate", txNow); err != nil {
					return err
				} else if !applied {
					return ErrPreparationStale
				}
				if applied, err := tx.MovePreparationReservation(r.ReservationID, r.WindowKey, r.WindowKey, "executing"); err != nil {
					return err
				} else if !applied {
					return ErrPreparationStale
				}
			}
		}
		expires := min(op.LeaseUntil, op.DeadlineAt)
		for _, g := range a.Guards {
			expires = min(expires, g.ExpiresAt)
		}
		for _, bound := range a.Plan.Bounds {
			expires = min(expires, bound.ExpiresAt)
		}
		p := store.PreparationPermit{PermitID: handle.ID(), AttemptID: id, RuntimeID: s.runtimeID, Generation: owner.Generation, ControlVersion: b.ControlVersion, CredentialRevision: a.Plan.CredentialRevision, ExpiresAt: expires, RequestDigest: a.RequestDigest, Status: "granted", GrantedAt: txNow}
		if applied, err := tx.AuthorizePreparationAttempt(id, op.Version, p, txNow); err != nil {
			return err
		} else if !applied {
			return ErrPreparationStale
		}
		hash, raw, err := commandHash(struct {
			Owner     domain.OwnerToken
			AttemptID domain.ID
			Digest    string
		}{owner, id, a.RequestDigest})
		if err != nil {
			return err
		}
		if err = w.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: id, Kind: "authorize", InputHash: hash, ResultJSON: []byte(`{"granted":true}`), CreatedAt: txNow}); err != nil {
			return err
		}
		if err = tx.InsertPreparationDecision(owner.Key, id, "dispatch", raw, nil, txNow); err != nil {
			return err
		}
		events, ok := w.(store.OperationEventWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		eventPayload, err := json.Marshal(struct {
			AttemptID domain.ID `json:"attemptId"`
		}{AttemptID: id})
		if err != nil {
			return err
		}
		if err = events.AppendOperationEvent(owner.Key, "dispatch_authorized", eventPayload, txNow); err != nil {
			return err
		}
		result = handle
		return nil
	}})
	if err != nil {
		return ledger.DispatchPermit{}, err
	}
	return result, nil
}

func (s *PreparationService) ConsumeDispatchPermit(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, p ledger.DispatchPermit, digest string, now domain.Millis) error {
	if s.st == nil || s.clock == nil || owner.RuntimeID != s.runtimeID || p.RuntimeID() != s.runtimeID || now < 0 || !authorized(auth, owner.Key.Namespace) {
		return ErrPreparationInvalid
	}
	// The local gate precedes the durable CAS. Any ambiguous/failed result is
	// spent locally and must be recovered via LED-04; it cannot authorize a retry
	// or a supplier send. Registry validates the private nonce and pairing.
	if err := s.registry.MarkConsumed(p, s.runtimeID, digest); err != nil {
		return err
	}
	scope := domain.ID("consume/" + string(owner.Key.Namespace))
	return s.st.SubmitWrite(ctx, store.WriteCommand{ID: string(scope) + "/" + string(p.ID()), Control: true, Run: func(w store.WriterTx) error {
		txNow := s.clock.Now()
		if txNow < 0 {
			return ErrPreparationInvalid
		}
		tx, ok := w.(store.PreparationWriterTx)
		if !ok {
			return ErrClaimStoreCapability
		}
		op, a, b, snapshots, err := recheckAttempt(tx, auth, owner, p.AttemptID(), txNow)
		if err != nil {
			return err
		}
		permit, found, err := tx.GetPreparationPermit(p.AttemptID())
		if err != nil {
			return err
		}
		if !found || a.Phase != "executing" || permit.PermitID != p.ID() || permit.Status != "granted" || permit.RuntimeID != owner.RuntimeID || permit.Generation != owner.Generation || permit.ControlVersion != b.ControlVersion || permit.CredentialRevision != b.CredentialRevision || permit.ExpiresAt <= txNow || permit.RequestDigest != digest || a.RequestDigest != digest {
			return ErrPreparationStale
		}
		for _, r := range a.Allocations {
			snap, ok := snapshots[r.ResourceID]
			if !ok || snap.WindowKey != r.WindowKey {
				return ErrPreparationStale
			}
		}
		if err = verifyReservedPlan(op, a, b, snapshots, txNow); err != nil {
			return err
		}
		if applied, err := tx.ConsumePreparationPermit(p.ID(), txNow); err != nil {
			return err
		} else if !applied {
			return ErrPreparationStale
		}
		hash, _, err := commandHash(struct {
			Owner    domain.OwnerToken
			PermitID domain.ID
			Digest   string
		}{owner, p.ID(), digest})
		if err != nil {
			return err
		}
		return w.InsertReceipt(store.ReceiptRecord{Scope: scope, CommandID: p.ID(), Kind: "consume", InputHash: hash, ResultJSON: []byte(`{"consumed":true}`), CreatedAt: txNow})
	}})
}
