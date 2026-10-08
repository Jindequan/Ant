package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

const managedResourceSQL = `EXISTS (
 SELECT 1 FROM offer_resources ore
 JOIN resource_offers o ON o.offer_id=ore.offer_id
 JOIN pool_members pm ON pm.offer_id=o.offer_id
 JOIN resource_pools p ON p.pool_id=pm.pool_id
 WHERE ore.resource_id=r.resource_id AND p.namespace=?
)`

func managedResourceDefinition(ctx context.Context, q queryer, namespace, resourceID domain.ID) (domain.ResourceDefinition, bool, error) {
	var definition domain.ResourceDefinition
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT r.definition_json FROM resources r WHERE r.resource_id=? AND `+managedResourceSQL, string(resourceID), string(namespace)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return definition, false, nil
	}
	if err != nil {
		return definition, false, err
	}
	if err = json.Unmarshal(raw, &definition); err != nil || definition.ResourceID != resourceID {
		return definition, false, fmt.Errorf("admin: resource definition is corrupt")
	}
	return definition, true, nil
}

func managedResource(ctx context.Context, q queryer, namespace, resourceID, windowKey domain.ID) (store.ManagedResourceRecord, bool, error) {
	var result store.ManagedResourceRecord
	var raw []byte
	var ownerKind, ownerID, metricID, kind, semanticsRef, semanticsRevision string
	var unit string
	var quantum int
	err := q.QueryRowContext(ctx, `SELECT r.definition_json,r.owner_kind,r.owner_id,r.metric_id,r.unit,r.quantum_exp,r.kind,r.semantics_ref,r.semantics_revision,
s.capacity_units,s.committed_units,s.adjustment_units,s.reserved_units,s.active_units,s.uncertain_units,s.version,s.adjustment_version
FROM resources r JOIN resource_states s USING(resource_id)
WHERE r.resource_id=? AND s.window_key=? AND `+managedResourceSQL,
		string(resourceID), string(windowKey), string(namespace)).Scan(&raw, &ownerKind, &ownerID, &metricID, &unit, &quantum, &kind, &semanticsRef, &semanticsRevision,
		&result.Snapshot.Capacity, &result.Snapshot.Committed, &result.Snapshot.Adjustment, &result.Snapshot.Reserved, &result.Snapshot.Active, &result.Snapshot.Uncertain, &result.Snapshot.Version, &result.AdjustmentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if err = json.Unmarshal(raw, &result.Definition); err != nil || result.Definition.ResourceID != resourceID || result.Definition.OwnerKind != ownerKind || result.Definition.OwnerID != domain.ID(ownerID) || result.Definition.MetricID != domain.ID(metricID) || result.Definition.Unit != unit || result.Definition.QuantumExp != quantum || result.Definition.Kind != kind || result.Definition.SemanticsRef != domain.ID(semanticsRef) || result.Definition.SemanticsRevision != domain.ID(semanticsRevision) {
		return result, false, fmt.Errorf("admin: resource identity mismatch")
	}
	result.Snapshot.ResourceID, result.Snapshot.WindowKey = resourceID, windowKey
	result.Snapshot.Kind, result.Snapshot.MetricID, result.Snapshot.Unit = kind, domain.ID(metricID), unit
	return result, true, nil
}

func managedHold(ctx context.Context, q queryer, namespace, holdID domain.ID) (store.ManagedHoldRecord, bool, error) {
	var hold store.ManagedHoldRecord
	var scopeID string
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT h.hold_id,h.scope_kind,h.scope_id,h.reason,h.evidence_json FROM holds h
WHERE h.hold_id=? AND h.status='open' AND EXISTS (
 SELECT 1 FROM resources r
 JOIN offer_resources ore ON ore.resource_id=r.resource_id
 JOIN resource_offers o ON o.offer_id=ore.offer_id
 JOIN pool_members pm ON pm.offer_id=o.offer_id
 JOIN resource_pools p ON p.pool_id=pm.pool_id
 LEFT JOIN bindings b ON b.binding_id=o.binding_id
 WHERE p.namespace=? AND (
  (h.scope_kind='resource' AND h.scope_id=r.resource_id) OR
  (h.scope_kind='account' AND h.scope_id=CASE WHEN r.owner_kind='account' THEN r.owner_id ELSE b.account_id END) OR
  (h.scope_kind='binding' AND h.scope_id=o.binding_id) OR
  (h.scope_kind='credential' AND h.scope_id=b.credential_ref) OR
  (h.scope_kind='domain' AND h.scope_id=o.domain)
 )
)`, string(holdID), string(namespace)).Scan(&hold.HoldID, &hold.ScopeKind, &scopeID, &hold.Reason, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return hold, false, nil
	}
	if err != nil {
		return hold, false, err
	}
	hold.ScopeID = domain.ID(scopeID)
	if err = json.Unmarshal(raw, &hold.EvidenceRefs); err != nil {
		return hold, false, fmt.Errorf("admin: hold evidence is corrupt")
	}
	return hold, true, nil
}

func (w *writerTx) GetManagedResourceDefinition(namespace, resourceID domain.ID) (domain.ResourceDefinition, bool, error) {
	return managedResourceDefinition(w.ctx, w.tx, namespace, resourceID)
}
func (r *readerTx) GetManagedResourceDefinition(namespace, resourceID domain.ID) (domain.ResourceDefinition, bool, error) {
	return managedResourceDefinition(r.ctx, r.tx, namespace, resourceID)
}
func (w *writerTx) GetManagedResource(namespace, resourceID, windowKey domain.ID) (store.ManagedResourceRecord, bool, error) {
	return managedResource(w.ctx, w.tx, namespace, resourceID, windowKey)
}
func (r *readerTx) GetManagedResource(namespace, resourceID, windowKey domain.ID) (store.ManagedResourceRecord, bool, error) {
	return managedResource(r.ctx, r.tx, namespace, resourceID, windowKey)
}
func (w *writerTx) GetManagedHold(namespace, holdID domain.ID) (store.ManagedHoldRecord, bool, error) {
	return managedHold(w.ctx, w.tx, namespace, holdID)
}
func (r *readerTx) GetManagedHold(namespace, holdID domain.ID) (store.ManagedHoldRecord, bool, error) {
	return managedHold(r.ctx, r.tx, namespace, holdID)
}

const holdEncumbranceCountSQL = `SELECT COUNT(*) FROM encumbrances e JOIN resources r ON r.resource_id=e.resource_id
JOIN offer_resources ore ON ore.resource_id=r.resource_id JOIN resource_offers o ON o.offer_id=ore.offer_id
JOIN pool_members pm ON pm.offer_id=o.offer_id JOIN resource_pools p ON p.pool_id=pm.pool_id
JOIN holds h ON h.hold_id=?
WHERE e.status='open' AND p.namespace=? AND h.status='open' AND (
 (h.scope_kind='resource' AND e.resource_id=h.scope_id) OR
 (h.scope_kind='account' AND (r.owner_kind='account' AND r.owner_id=h.scope_id OR r.owner_kind='binding' AND EXISTS(SELECT 1 FROM bindings b WHERE b.binding_id=r.owner_id AND b.account_id=h.scope_id))) OR
 (h.scope_kind='binding' AND o.binding_id=h.scope_id) OR
 (h.scope_kind='credential' AND EXISTS(SELECT 1 FROM bindings b WHERE b.binding_id=o.binding_id AND b.credential_ref=h.scope_id)) OR
 (h.scope_kind='domain' AND o.domain=h.scope_id)
)`

func (r *readerTx) ListManagedHoldsForResource(namespace, resourceID domain.ID) ([]store.ManagedHoldRecord, error) {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT DISTINCT h.hold_id FROM holds h
JOIN resources r ON r.resource_id=?
JOIN offer_resources ore ON ore.resource_id=r.resource_id
JOIN resource_offers o ON o.offer_id=ore.offer_id
JOIN pool_members pm ON pm.offer_id=o.offer_id
JOIN resource_pools p ON p.pool_id=pm.pool_id
LEFT JOIN bindings b ON b.binding_id=o.binding_id
WHERE p.namespace=? AND h.status='open' AND (
 (h.scope_kind='resource' AND h.scope_id=r.resource_id) OR
 (h.scope_kind='account' AND h.scope_id=CASE WHEN r.owner_kind='account' THEN r.owner_id ELSE b.account_id END) OR
 (h.scope_kind='binding' AND h.scope_id=o.binding_id) OR
 (h.scope_kind='credential' AND h.scope_id=b.credential_ref) OR
 (h.scope_kind='domain' AND h.scope_id=o.domain)
) ORDER BY h.hold_id`, string(resourceID), string(namespace))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []store.ManagedHoldRecord
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		h, ok, err := managedHold(r.ctx, r.tx, namespace, domain.ID(id))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var count int
		if err := r.tx.QueryRowContext(r.ctx, holdEncumbranceCountSQL, id, string(namespace)).Scan(&count); err != nil {
			return nil, err
		}
		h.CanClear = count == 0
		result = append(result, h)
	}
	return result, rows.Err()
}

func (r *readerTx) ListAdjustableChargesForOperation(namespace, operationID domain.ID) ([]store.AdjustableChargeRecord, error) {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT e.event_id,e.resource_id,e.window_key,r.metric_id,r.unit,
(e.committed_delta+e.adjustment_delta)+COALESCE((SELECT SUM(a.adjustment_delta) FROM resource_events a
 WHERE a.resource_id=e.resource_id AND a.window_key=e.window_key AND a.event_kind='adjust'
 AND json_extract(a.evidence_json,'$.originalEventId')=e.event_id),0) AS remaining
FROM resource_events e JOIN attempts at ON at.attempt_id=e.attempt_id
JOIN resources r ON r.resource_id=e.resource_id
WHERE at.namespace=? AND at.operation_id=? AND e.event_kind IN('consume','reconcile') AND r.kind='counter'
AND (e.committed_delta+e.adjustment_delta)+COALESCE((SELECT SUM(a.adjustment_delta) FROM resource_events a
 WHERE a.resource_id=e.resource_id AND a.window_key=e.window_key AND a.event_kind='adjust'
 AND json_extract(a.evidence_json,'$.originalEventId')=e.event_id),0)>0
ORDER BY e.recorded_at,e.event_id`, string(namespace), string(operationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []store.AdjustableChargeRecord
	for rows.Next() {
		var item store.AdjustableChargeRecord
		var eventID, resourceID, windowKey, metricID string
		var remaining int64
		if err := rows.Scan(&eventID, &resourceID, &windowKey, &metricID, &item.Unit, &remaining); err != nil {
			return nil, err
		}
		item.EventID, item.ResourceID, item.WindowKey, item.MetricID = domain.ID(eventID), domain.ID(resourceID), domain.ID(windowKey), domain.ID(metricID)
		item.RemainingReversible = domain.Units(remaining)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (w *writerTx) InsertResourceObservation(o domain.ObservationEvidence, scope, command domain.ID, refs []domain.ID, now domain.Millis) error {
	raw, err := json.Marshal(struct {
		SourceRef    domain.ID   `json:"sourceRef"`
		EvidenceRefs []domain.ID `json:"evidenceRefs"`
	}{o.SourceRef, refs})
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, `INSERT INTO observations(observation_id,resource_id,window_key,remaining_units,committed_at_start,adjustment_version_at_start,observed_at,expires_at,valid,source_ref,evidence_json) VALUES(?,?,?,?,?,?,?,?,1,?,CAST(? AS TEXT))`, string(o.ID), string(o.ResourceID), string(o.WindowKey), int64(o.Remaining), int64(o.CommittedAtStart), o.AdjustmentVersionAtStart, int64(o.ObservedAt), int64(o.ExpiresAt), string(o.SourceRef), raw)
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, `UPDATE observations SET valid=0 WHERE resource_id=? AND window_key=? AND observation_id<>? AND valid=1`, string(o.ResourceID), string(o.WindowKey), string(o.ID))
	return err
}

func (w *writerTx) ApplyAdminAdjustment(d domain.ResourceDelta, scope, command, originalEvent domain.ID, refs []domain.ID, now domain.Millis) (bool, error) {
	res, err := w.tx.ExecContext(w.ctx, `UPDATE resource_states SET adjustment_units=adjustment_units+?,adjustment_version=adjustment_version+1,version=version+1 WHERE resource_id=? AND window_key=? AND version=?`, int64(d.Adjustment), string(d.ResourceID), string(d.WindowKey), d.ExpectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	raw, err := json.Marshal(struct {
		OriginalEventID domain.ID   `json:"originalEventId,omitempty"`
		EvidenceRefs    []domain.ID `json:"evidenceRefs"`
	}{originalEvent, refs})
	if err != nil {
		return false, err
	}
	_, err = w.tx.ExecContext(w.ctx, `INSERT INTO resource_events(event_id,command_scope,command_id,resource_id,window_key,event_kind,adjustment_delta,evidence_json,recorded_at) VALUES(?,?,?,?,?,'adjust',?,CAST(? AS TEXT),?)`, string(scope)+"/"+string(command)+"/"+string(d.ResourceID)+"/"+string(d.WindowKey), string(scope), string(command), string(d.ResourceID), string(d.WindowKey), int64(d.Adjustment), raw, int64(now))
	return err == nil, err
}

func (w *writerTx) GetOriginalCharge(resourceID, windowKey, eventID domain.ID) (domain.Units, bool, error) {
	var committed, adjustment int64
	err := w.tx.QueryRowContext(w.ctx, `SELECT committed_delta+adjustment_delta,
COALESCE((SELECT SUM(e.adjustment_delta) FROM resource_events e WHERE e.resource_id=resource_events.resource_id AND e.window_key=resource_events.window_key AND e.event_kind='adjust' AND json_extract(e.evidence_json,'$.originalEventId')=resource_events.event_id),0)
FROM resource_events WHERE event_id=? AND resource_id=? AND window_key=? AND event_kind IN('consume','reconcile')`, string(eventID), string(resourceID), string(windowKey)).Scan(&committed, &adjustment)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	amount, err := domain.AddUnits(domain.Units(committed), domain.Units(adjustment))
	if err != nil {
		return 0, false, err
	}
	return amount, amount > 0, nil
}

func (w *writerTx) InsertAdminOverrunHold(namespace, resourceID, commandID domain.ID, refs []domain.ID, now domain.Millis) error {
	raw, err := json.Marshal(refs)
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, `INSERT INTO holds(hold_id,scope_kind,scope_id,reason,status,evidence_json,created_at)
SELECT ?,'resource',?,'admin_adjustment_overrun','open',CAST(? AS TEXT),?
WHERE EXISTS(SELECT 1 FROM resource_states WHERE resource_id=? AND committed_units+adjustment_units>capacity_units)`,
		"admin-adjustment/"+string(namespace)+"/"+string(commandID)+"/"+string(resourceID), string(resourceID), raw, int64(now), string(resourceID))
	return err
}

func (w *writerTx) ClearManagedHold(namespace, holdID domain.ID, refs []domain.ID, now domain.Millis) (bool, error) {
	hold, found, err := managedHold(w.ctx, w.tx, namespace, holdID)
	if err != nil || !found {
		return false, err
	}
	var encumbrances int
	err = w.tx.QueryRowContext(w.ctx, holdEncumbranceCountSQL, string(holdID), string(namespace)).Scan(&encumbrances)
	if err != nil {
		return false, err
	}
	if encumbrances > 0 {
		return false, nil
	}
	for _, ref := range refs {
		if ref == "" {
			return false, fmt.Errorf("admin: empty hold evidence")
		}
		hold.EvidenceRefs = append(hold.EvidenceRefs, ref)
	}
	raw, err := json.Marshal(hold.EvidenceRefs)
	if err != nil {
		return false, err
	}
	res, err := w.tx.ExecContext(w.ctx, `UPDATE holds SET status='cleared',cleared_at=?,evidence_json=CAST(? AS TEXT) WHERE hold_id=? AND status='open'`, int64(now), raw, string(holdID))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
