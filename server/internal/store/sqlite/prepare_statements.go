package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

var ErrPreparationDecisionLimit = errors.New("sqlite: rejected preparation decision limit reached")

const (
	maxDecisionSnapshotBytes        = 16 * 1024
	maxDecisionTraceBytes           = 64 * 1024
	maxRejectedPreparationDecisions = 16
)

func preparationBundle(ctx context.Context, q queryer, plan domain.ExecutionPlan, now domain.Millis) (store.PreparationBundle, error) {
	var b store.PreparationBundle
	var poolID, ns, offerID, bindingID, accountID, providerTypeID, endpoint, credential, revision, adapter, active string
	var enabledPool, enabledMember, enabledOffer, enabledAccount, enabledBinding int
	var accountJSON, bindingJSON []byte
	err := q.QueryRowContext(ctx, `SELECT p.pool_id,p.namespace,p.name,p.enabled,p.version,
 m.priority,m.enabled,m.version,o.offer_id,o.binding_id,o.domain,o.capability_profile,o.enabled,o.version,
	 a.account_id,a.provider_owner_ref,a.enabled,bi.enabled,bi.endpoint,bi.credential_ref,bi.credential_revision,bi.adapter_ref,
 a.definition_json,bi.definition_json,ac.revision,rc.control_version,rc.restore_guard
 FROM resource_pools p JOIN pool_members m ON m.pool_id=p.pool_id JOIN resource_offers o ON o.offer_id=m.offer_id
 JOIN bindings bi ON bi.binding_id=o.binding_id JOIN accounts a ON a.account_id=bi.account_id
 CROSS JOIN active_config ac CROSS JOIN runtime_control rc WHERE p.pool_id=? AND o.offer_id=?`, string(plan.PoolID), string(plan.OfferID)).Scan(
		&poolID, &ns, &b.Pool.Name, &enabledPool, &b.Pool.Version, &b.Member.Priority, &enabledMember, &b.Member.Version,
		&offerID, &bindingID, &b.Offer.Domain, &b.Offer.CapabilityProfile, &enabledOffer, &b.Offer.Version,
		&accountID, &providerTypeID, &enabledAccount, &enabledBinding, &endpoint, &credential, &revision, &adapter, &accountJSON, &bindingJSON, &active, &b.ControlVersion, &b.RestoreGuard)
	if err != nil {
		return b, err
	}
	b.Pool.PoolID, b.Pool.Namespace, b.Pool.Enabled = domain.ID(poolID), domain.ID(ns), enabledPool == 1
	b.Member.PoolID, b.Member.OfferID, b.Member.Enabled = domain.ID(poolID), domain.ID(offerID), enabledMember == 1
	b.Offer.OfferID, b.Offer.BindingID, b.Offer.Enabled = domain.ID(offerID), domain.ID(bindingID), enabledOffer == 1
	b.AccountID, b.ProviderTypeID, b.Endpoint = domain.ID(accountID), domain.ID(providerTypeID), endpoint
	b.AccountEnabled, b.BindingEnabled = enabledAccount == 1, enabledBinding == 1
	b.CredentialRef, b.CredentialRevision, b.AdapterRef, b.ActiveRevision = domain.ID(credential), domain.ID(revision), domain.ID(adapter), domain.ID(active)
	var accountPolicy, bindingPolicy struct {
		AllowMetered *bool `json:"allowMetered"`
	}
	if err := json.Unmarshal(accountJSON, &accountPolicy); err != nil {
		return b, err
	}
	if err := json.Unmarshal(bindingJSON, &bindingPolicy); err != nil {
		return b, err
	}
	// Omitted policy imposes no additional permission; the first-claim policy
	// remains mandatory. Explicit false is a current spend tightening.
	b.AllowMetered = (accountPolicy.AllowMetered == nil || *accountPolicy.AllowMetered) && (bindingPolicy.AllowMetered == nil || *bindingPolicy.AllowMetered)
	var holds int
	err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM holds WHERE status='open' AND (until_at IS NULL OR until_at>?) AND
 ((scope_kind='account' AND scope_id=?) OR (scope_kind='binding' AND scope_id=?) OR (scope_kind='credential' AND scope_id=?) OR (scope_kind='domain' AND scope_id=?))`, int64(now), accountID, bindingID, credential, b.Offer.Domain).Scan(&holds)
	if err != nil {
		return b, err
	}
	b.Held = holds > 0
	rows, err := q.QueryContext(ctx, `SELECT hold_id FROM holds WHERE status='open' AND (until_at IS NULL OR until_at>?) AND
 ((scope_kind='account' AND scope_id=?) OR (scope_kind='binding' AND scope_id=?) OR (scope_kind='credential' AND scope_id=?) OR (scope_kind='domain' AND scope_id=?)) ORDER BY hold_id`, int64(now), accountID, bindingID, credential, b.Offer.Domain)
	if err != nil {
		return b, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return b, err
		}
		b.HoldIDs = append(b.HoldIDs, domain.ID(id))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return b, err
	}
	rows, err = q.QueryContext(ctx, `SELECT r.resource_id,r.owner_kind,r.owner_id,r.metric_id,r.unit,r.quantum_exp,r.kind,r.semantics_ref,r.semantics_revision,r.definition_json FROM resources r JOIN offer_resources o ON o.resource_id=r.resource_id WHERE o.offer_id=? ORDER BY r.resource_id`, offerID)
	if err != nil {
		return b, err
	}
	for rows.Next() {
		var r store.PreparationResource
		var id, ownerKind, ownerID, metric, unit, kind, sem, semRev string
		var quantum int
		var raw []byte
		if err = rows.Scan(&id, &ownerKind, &ownerID, &metric, &unit, &quantum, &kind, &sem, &semRev, &raw); err != nil {
			rows.Close()
			return b, err
		}
		if err = json.Unmarshal(raw, &r.Definition); err != nil {
			rows.Close()
			return b, err
		}
		if r.Definition.ResourceID != domain.ID(id) || r.Definition.OwnerKind != ownerKind || r.Definition.OwnerID != domain.ID(ownerID) || r.Definition.MetricID != domain.ID(metric) || r.Definition.Unit != unit || r.Definition.Kind != kind || r.Definition.QuantumExp != quantum || r.Definition.SemanticsRef != domain.ID(sem) || r.Definition.SemanticsRevision != domain.ID(semRev) {
			rows.Close()
			return b, fmt.Errorf("preparation: resource definition mismatch")
		}
		r.AdjustmentVersions = map[domain.ID]int64{}
		b.Resources = append(b.Resources, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return b, err
	}
	for i := range b.Resources {
		r := &b.Resources[i]
		id := string(r.Definition.ResourceID)
		rows, err = q.QueryContext(ctx, `SELECT window_key,capacity_units,committed_units,adjustment_units,reserved_units,active_units,uncertain_units,version,adjustment_version FROM resource_states WHERE resource_id=? ORDER BY window_key`, id)
		if err != nil {
			return b, err
		}
		for rows.Next() {
			snap := domain.ResourceSnapshot{ResourceID: r.Definition.ResourceID, Kind: r.Definition.Kind, MetricID: r.Definition.MetricID, Unit: r.Definition.Unit}
			var key string
			var av int64
			if err = rows.Scan(&key, &snap.Capacity, &snap.Committed, &snap.Adjustment, &snap.Reserved, &snap.Active, &snap.Uncertain, &snap.Version, &av); err != nil {
				rows.Close()
				return b, err
			}
			snap.WindowKey = domain.ID(key)
			r.States = append(r.States, snap)
			r.AdjustmentVersions[snap.WindowKey] = av
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return b, err
		}
		err = q.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM holds WHERE status='open' AND scope_kind='resource' AND scope_id=? AND (until_at IS NULL OR until_at>?))+(SELECT COUNT(*) FROM encumbrances WHERE resource_id=? AND status='open')`, id, int64(now), id).Scan(&holds)
		if err != nil {
			return b, err
		}
		r.Isolated = holds > 0
		rows, err = q.QueryContext(ctx, `SELECT hold_id FROM holds WHERE status='open' AND scope_kind='resource' AND scope_id=? AND (until_at IS NULL OR until_at>?) ORDER BY hold_id`, id, int64(now))
		if err != nil {
			return b, err
		}
		for rows.Next() {
			var holdID string
			if err = rows.Scan(&holdID); err != nil {
				rows.Close()
				return b, err
			}
			r.HoldIDs = append(r.HoldIDs, domain.ID(holdID))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return b, err
		}
		rows, err = q.QueryContext(ctx, `SELECT encumbrance_id FROM encumbrances WHERE resource_id=? AND status='open' ORDER BY encumbrance_id`, id)
		if err != nil {
			return b, err
		}
		for rows.Next() {
			var encumbranceID string
			if err = rows.Scan(&encumbranceID); err != nil {
				rows.Close()
				return b, err
			}
			r.EncumbranceIDs = append(r.EncumbranceIDs, domain.ID(encumbranceID))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return b, err
		}
		rows, err = q.QueryContext(ctx, `SELECT observation_id,window_key,remaining_units,committed_at_start,adjustment_version_at_start,observed_at,expires_at,source_ref FROM observations WHERE resource_id=? AND valid=1 ORDER BY observed_at DESC,observation_id`, id)
		if err != nil {
			return b, err
		}
		for rows.Next() {
			o := domain.ObservationEvidence{ResourceID: r.Definition.ResourceID}
			var oi, key, src string
			if err = rows.Scan(&oi, &key, &o.Remaining, &o.CommittedAtStart, &o.AdjustmentVersionAtStart, &o.ObservedAt, &o.ExpiresAt, &src); err != nil {
				rows.Close()
				return b, err
			}
			o.ID, o.WindowKey, o.SourceRef = domain.ID(oi), domain.ID(key), domain.ID(src)
			r.Observations = append(r.Observations, o)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return b, err
		}
		rows, err = q.QueryContext(ctx, `SELECT committed_delta+adjustment_delta,charged_at FROM resource_events WHERE resource_id=? AND event_kind IN('consume','adjust','reconcile') ORDER BY recorded_at,event_id`, id)
		if err != nil {
			return b, err
		}
		for rows.Next() {
			var c store.PreparationCharge
			var at sql.NullInt64
			if err = rows.Scan(&c.Units, &at); err != nil {
				rows.Close()
				return b, err
			}
			c.Attributed = at.Valid
			c.ChargedAt = domain.Millis(at.Int64)
			r.Charges = append(r.Charges, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return b, err
		}
	}
	rows, err = q.QueryContext(ctx, `SELECT evidence_id,scope,revision,allowed,observed_at,expires_at,source_ref FROM guard_states ORDER BY scope`)
	if err != nil {
		return b, err
	}
	for rows.Next() {
		var g domain.GuardEvidence
		var id, scope, src string
		var allowed int
		if err = rows.Scan(&id, &scope, &g.Revision, &allowed, &g.ObservedAt, &g.ExpiresAt, &src); err != nil {
			rows.Close()
			return b, err
		}
		g.ID, g.Scope, g.SourceRef, g.Allowed = domain.ID(id), domain.ID(scope), domain.ID(src), allowed == 1
		b.Guards = append(b.Guards, g)
	}
	err = rows.Err()
	rows.Close()
	return b, err
}

func (w *writerTx) GetPreparationBundle(p domain.ExecutionPlan, n domain.Millis) (store.PreparationBundle, error) {
	return preparationBundle(w.ctx, w.tx, p, n)
}
func (r *readerTx) GetPreparationBundle(p domain.ExecutionPlan, n domain.Millis) (store.PreparationBundle, error) {
	return preparationBundle(r.ctx, r.tx, p, n)
}

func (w *writerTx) HasActivePreparationAttempt(k domain.OperationKey) (bool, error) {
	var count int
	err := w.tx.QueryRowContext(w.ctx, `SELECT COUNT(*) FROM attempts WHERE namespace=? AND operation_id=? AND phase IN('prepared','executing')`, string(k.Namespace), string(k.OperationID)).Scan(&count)
	return count > 0, err
}
func (w *writerTx) GetPreparationAttempt(id domain.ID) (store.PreparationAttempt, bool, error) {
	var a store.PreparationAttempt
	var ns, op, phase string
	var raw, guards []byte
	err := w.tx.QueryRowContext(w.ctx, `SELECT a.namespace,a.operation_id,a.generation,a.phase,p.plan_json,a.request_digest,a.guard_refs_json,a.lease_until FROM attempts a JOIN execution_plans p ON p.plan_id=a.plan_id WHERE a.attempt_id=?`, string(id)).Scan(&ns, &op, &a.Generation, &phase, &raw, &a.RequestDigest, &guards, &a.LeaseUntil)
	if err == sql.ErrNoRows {
		return a, false, nil
	}
	if err != nil {
		return a, false, err
	}
	a.AttemptID, a.Key, a.Phase = id, domain.OperationKey{Namespace: domain.ID(ns), OperationID: domain.ID(op)}, phase
	if err = json.Unmarshal(raw, &a.Plan); err != nil {
		return a, false, err
	}
	if err = json.Unmarshal(guards, &a.Guards); err != nil {
		return a, false, err
	}
	rows, err := w.tx.QueryContext(w.ctx, `SELECT reservation_id,resource_id,window_key,kind,units,phase FROM reservations WHERE attempt_id=? ORDER BY resource_id,window_key`, string(id))
	if err != nil {
		return a, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var r domain.ReservationState
		var rid, res, key, kind, ph string
		if err = rows.Scan(&rid, &res, &key, &kind, &r.Units, &ph); err != nil {
			return a, false, err
		}
		r.ReservationID, r.AttemptID, r.ResourceID, r.WindowKey, r.Kind, r.Phase = domain.ID(rid), id, domain.ID(res), domain.ID(key), kind, ph
		a.Allocations = append(a.Allocations, r)
	}
	return a, true, rows.Err()
}
func (w *writerTx) InsertPreparationAttempt(a store.PreparationAttempt, hash string, now domain.Millis) error {
	raw, err := json.Marshal(a.Plan)
	if err != nil {
		return err
	}
	guards, err := json.Marshal(a.Guards)
	if err != nil {
		return err
	}
	if _, err = w.tx.ExecContext(w.ctx, `INSERT INTO execution_plans(plan_id,config_revision,plan_json,plan_hash) VALUES(?,?,CAST(? AS TEXT),?)`, string(a.Plan.PlanID), string(a.Plan.ConfigRevision), raw, hash); err != nil {
		return err
	}
	if _, err = w.tx.ExecContext(w.ctx, `INSERT INTO attempts(attempt_id,namespace,operation_id,binding_id,account_id,offer_id,plan_id,generation,request_digest,guard_refs_json,phase,lease_until,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,CAST(? AS TEXT),'prepared',?,?,?)`, string(a.AttemptID), string(a.Key.Namespace), string(a.Key.OperationID), string(a.Plan.BindingID), string(a.Plan.AccountID), string(a.Plan.OfferID), string(a.Plan.PlanID), a.Generation, a.RequestDigest, guards, int64(a.LeaseUntil), int64(now), int64(now)); err != nil {
		return err
	}
	for _, r := range a.Allocations {
		if _, err = w.tx.ExecContext(w.ctx, `INSERT INTO reservations(reservation_id,attempt_id,resource_id,window_key,kind,units,phase) VALUES(?,?,?,?,?,?,'reserved')`, string(r.ReservationID), string(a.AttemptID), string(r.ResourceID), string(r.WindowKey), r.Kind, int64(r.Units)); err != nil {
			return err
		}
	}
	return nil
}
func (w *writerTx) ApplyPreparationDelta(d domain.ResourceDelta, attempt, scope domain.ID, kind string, now domain.Millis) (bool, error) {
	res, err := w.tx.ExecContext(w.ctx, `UPDATE resource_states SET committed_units=committed_units+?,adjustment_units=adjustment_units+?,reserved_units=reserved_units+?,active_units=active_units+?,uncertain_units=uncertain_units+?,version=version+1 WHERE resource_id=? AND window_key=? AND version=?`, int64(d.Committed), int64(d.Adjustment), int64(d.Reserved), int64(d.Active), int64(d.Uncertain), string(d.ResourceID), string(d.WindowKey), d.ExpectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	_, err = w.tx.ExecContext(w.ctx, `INSERT INTO resource_events(event_id,command_scope,command_id,resource_id,window_key,attempt_id,event_kind,committed_delta,adjustment_delta,reserved_delta,active_delta,uncertain_delta,evidence_json,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?, '[]',?)`, string(scope)+"/"+string(attempt)+"/"+string(d.ResourceID)+"/"+string(d.WindowKey), string(scope), string(attempt), string(d.ResourceID), string(d.WindowKey), string(attempt), kind, int64(d.Committed), int64(d.Adjustment), int64(d.Reserved), int64(d.Active), int64(d.Uncertain), int64(now))
	return err == nil, err
}
func (w *writerTx) MovePreparationReservation(id, old, next domain.ID, phase string) (bool, error) {
	res, err := w.tx.ExecContext(w.ctx, `UPDATE reservations SET window_key=?,phase=? WHERE reservation_id=? AND window_key=? AND phase='reserved'`, string(next), phase, string(id), string(old))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
func (w *writerTx) AuthorizePreparationAttempt(id domain.ID, version int64, p store.PreparationPermit, now domain.Millis) (bool, error) {
	res, err := w.tx.ExecContext(w.ctx, `UPDATE attempts SET phase='executing',updated_at=?,version=version+1 WHERE attempt_id=? AND phase='prepared'`, int64(now), string(id))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	res, err = w.tx.ExecContext(w.ctx, `UPDATE operations SET permits_issued=permits_issued+1,updated_at=?,version=version+1 WHERE (namespace,operation_id)=(SELECT namespace,operation_id FROM attempts WHERE attempt_id=?) AND version=? AND state='running' AND permits_issued<max_attempts`, int64(now), string(id), version)
	if err != nil {
		return false, err
	}
	n, err = res.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	_, err = w.tx.ExecContext(w.ctx, `INSERT INTO dispatch_permits(permit_id,attempt_id,runtime_id,generation,control_version,credential_revision,expires_at,request_digest,status,granted_at) VALUES(?,?,?,?,?,?,?,?,'granted',?)`, string(p.PermitID), string(id), string(p.RuntimeID), p.Generation, p.ControlVersion, string(p.CredentialRevision), int64(p.ExpiresAt), p.RequestDigest, int64(now))
	return err == nil, err
}
func (w *writerTx) GetPreparationPermit(id domain.ID) (store.PreparationPermit, bool, error) {
	var p store.PreparationPermit
	var pid, aid, rt, cred string
	err := w.tx.QueryRowContext(w.ctx, `SELECT permit_id,attempt_id,runtime_id,generation,control_version,credential_revision,expires_at,request_digest,status,granted_at FROM dispatch_permits WHERE attempt_id=?`, string(id)).Scan(&pid, &aid, &rt, &p.Generation, &p.ControlVersion, &cred, &p.ExpiresAt, &p.RequestDigest, &p.Status, &p.GrantedAt)
	if err == sql.ErrNoRows {
		return p, false, nil
	}
	p.PermitID, p.AttemptID, p.RuntimeID, p.CredentialRevision = domain.ID(pid), domain.ID(aid), domain.ID(rt), domain.ID(cred)
	return p, err == nil, err
}
func (w *writerTx) ConsumePreparationPermit(id domain.ID, now domain.Millis) (bool, error) {
	res, err := w.tx.ExecContext(w.ctx, `UPDATE dispatch_permits SET status='consumed',consumed_at=? WHERE permit_id=? AND status='granted' AND expires_at>?`, int64(now), string(id), int64(now))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
func (w *writerTx) InsertPreparationDecision(k domain.OperationKey, attempt domain.ID, stage string, raw, trace []byte, now domain.Millis) error {
	if len(trace) == 0 {
		trace = []byte(`{}`)
	}
	if len(trace) > maxDecisionTraceBytes || !json.Valid(trace) {
		return errors.New("sqlite: invalid preparation decision trace")
	}
	truncated := 0
	if len(raw) > maxDecisionSnapshotBytes {
		digest := sha256.Sum256(raw)
		raw, _ = json.Marshal(struct {
			Truncated bool   `json:"truncated"`
			SHA256    string `json:"sha256"`
		}{true, hex.EncodeToString(digest[:])})
		truncated = 1
	}
	var aid any
	if attempt != "" && stage != "rejected" {
		aid = string(attempt)
	}
	res, err := w.tx.ExecContext(w.ctx, `INSERT INTO decision_records(decision_id,namespace,operation_id,attempt_id,stage,snapshot_json,trace_truncated,created_at,trace_json)
		SELECT ?,?,?,?,?,CAST(? AS TEXT),?,?,CAST(? AS TEXT) WHERE ?!='rejected' OR
		(SELECT COUNT(*) FROM decision_records WHERE namespace=? AND operation_id=? AND stage='rejected')<?`,
		stage+"/"+string(k.Namespace)+"/"+string(k.OperationID)+"/"+string(attempt), string(k.Namespace), string(k.OperationID), aid, stage, raw, truncated, int64(now), trace, stage,
		string(k.Namespace), string(k.OperationID), maxRejectedPreparationDecisions)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrPreparationDecisionLimit
	}
	return nil
}
