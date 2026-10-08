package sqlite

import (
	"ant/server/internal/domain"
	"ant/server/internal/store"
	"encoding/json"
	"fmt"
)

func (w *writerTx) GetSettlementFacts(a store.PreparationAttempt, now domain.Millis) (store.SettlementFacts, error) {
	f := store.SettlementFacts{AllPriorEnded: true}
	var hash *string
	if err := w.tx.QueryRowContext(w.ctx, `SELECT report_hash,report_json FROM attempts WHERE attempt_id=?`, string(a.AttemptID)).Scan(&hash, &f.ReportJSON); err != nil {
		return f, err
	}
	if hash != nil {
		f.ReportHash = *hash
	}
	for _, r := range a.Allocations {
		var x store.SettlementResource
		var raw []byte
		var ownerKind, ownerID, metric, unit, kind, sem, semRev string
		var quantum int
		x.Snapshot.ResourceID, x.Snapshot.WindowKey = r.ResourceID, r.WindowKey
		err := w.tx.QueryRowContext(w.ctx, `SELECT r.owner_kind,r.owner_id,r.metric_id,r.unit,r.quantum_exp,r.kind,r.semantics_ref,r.semantics_revision,r.definition_json,s.capacity_units,s.committed_units,s.adjustment_units,s.reserved_units,s.active_units,s.uncertain_units,s.version FROM resources r JOIN resource_states s USING(resource_id) WHERE r.resource_id=? AND s.window_key=?`, string(r.ResourceID), string(r.WindowKey)).Scan(&ownerKind, &ownerID, &metric, &unit, &quantum, &kind, &sem, &semRev, &raw, &x.Snapshot.Capacity, &x.Snapshot.Committed, &x.Snapshot.Adjustment, &x.Snapshot.Reserved, &x.Snapshot.Active, &x.Snapshot.Uncertain, &x.Snapshot.Version)
		if err != nil {
			return f, err
		}
		if err = json.Unmarshal(raw, &x.Definition); err != nil {
			return f, err
		}
		// Persisted columns, the certified definition JSON and the pinned
		// reservation must agree before any settlement fact is accepted —
		// the same comparison preparation performs at prepare time. Drift is
		// integrity failure, never silently reinterpreted (LED-03 review item 3).
		if x.Definition.ResourceID != r.ResourceID || x.Definition.OwnerKind != ownerKind || x.Definition.OwnerID != domain.ID(ownerID) || x.Definition.MetricID != domain.ID(metric) || x.Definition.Unit != unit || x.Definition.QuantumExp != quantum || x.Definition.Kind != kind || x.Definition.Kind != r.Kind || x.Definition.SemanticsRef != domain.ID(sem) || x.Definition.SemanticsRevision != domain.ID(semRev) {
			return f, fmt.Errorf("settlement: resource identity mismatch")
		}
		if x.Definition.SemanticsRevision != a.Plan.WindowRuleRevision {
			return f, fmt.Errorf("settlement: resource semantics revision %q does not match plan window revision %q", string(x.Definition.SemanticsRevision), string(a.Plan.WindowRuleRevision))
		}
		if (x.Definition.OwnerKind == "account" && x.Definition.OwnerID != a.Plan.AccountID) || (x.Definition.OwnerKind == "binding" && x.Definition.OwnerID != a.Plan.BindingID) {
			return f, fmt.Errorf("settlement: resource owner %s/%s does not match plan ownership", x.Definition.OwnerKind, string(x.Definition.OwnerID))
		}
		x.Snapshot.Kind, x.Snapshot.MetricID, x.Snapshot.Unit = kind, domain.ID(metric), unit
		f.Resources = append(f.Resources, x)
	}
	rows, err := w.tx.QueryContext(w.ctx, `SELECT attempt_id,report_json FROM attempts WHERE namespace=? AND operation_id=? ORDER BY created_at,attempt_id`, string(a.Key.Namespace), string(a.Key.OperationID))
	if err != nil {
		return f, err
	}
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return f, err
		}
		f.Attempts = append(f.Attempts, domain.ID(id))
		if id != string(a.AttemptID) {
			var report domain.ExecutionReport
			if len(raw) == 0 || json.Unmarshal(raw, &report) != nil || !report.ExternalEnded {
				f.AllPriorEnded = false
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return f, err
	}
	rows, err = w.tx.QueryContext(w.ctx, `SELECT payload_json FROM operation_events WHERE namespace=? AND operation_id=? AND event_type='attempt_settled' ORDER BY sequence`, string(a.Key.Namespace), string(a.Key.OperationID))
	if err != nil {
		return f, err
	}
	for rows.Next() {
		var raw []byte
		var outcomes []domain.ResourceOutcome
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return f, err
		}
		if err = json.Unmarshal(raw, &outcomes); err != nil {
			rows.Close()
			return f, err
		}
		f.Outcomes = append(f.Outcomes, outcomes...)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return f, err
	}
	var domainName string
	if err = w.tx.QueryRowContext(w.ctx, `SELECT domain FROM resource_offers WHERE offer_id=?`, string(a.Plan.OfferID)).Scan(&domainName); err != nil {
		return f, err
	}
	// Credential holds gate continuation exactly like preparation admission
	// does: the binding's current credential_ref is the hold scope.
	var credentialRef string
	if err = w.tx.QueryRowContext(w.ctx, `SELECT credential_ref FROM bindings WHERE binding_id=?`, string(a.Plan.BindingID)).Scan(&credentialRef); err != nil {
		return f, err
	}
	var holds int
	err = w.tx.QueryRowContext(w.ctx, `SELECT
 (SELECT COUNT(*) FROM holds WHERE status='open' AND (until_at IS NULL OR until_at>?) AND
 ((scope_kind='account' AND scope_id=?) OR (scope_kind='binding' AND scope_id=?) OR
 (scope_kind='credential' AND scope_id=?) OR (scope_kind='domain' AND scope_id=?) OR (scope_kind='resource' AND scope_id IN (SELECT resource_id FROM reservations WHERE attempt_id=?))))+
 (SELECT COUNT(*) FROM encumbrances WHERE status='open' AND resource_id IN (SELECT resource_id FROM reservations WHERE attempt_id=?))`, int64(now), string(a.Plan.AccountID), string(a.Plan.BindingID), credentialRef, domainName, string(a.AttemptID), string(a.AttemptID)).Scan(&holds)
	if err != nil {
		return f, err
	}
	f.Blocked = holds > 0
	var n int
	err = w.tx.QueryRowContext(w.ctx, `SELECT COUNT(*) FROM output_chunks o JOIN attempts a USING(attempt_id) WHERE a.namespace=? AND a.operation_id=?`, string(a.Key.Namespace), string(a.Key.OperationID)).Scan(&n)
	f.OutputStored = n > 0
	return f, err
}
func (w *writerTx) ApplySettlementDelta(d domain.ResourceDelta, attempt, scope domain.ID, at *domain.Millis, refs []domain.ID, now domain.Millis) (bool, error) {
	res, err := w.tx.ExecContext(w.ctx, `UPDATE resource_states SET committed_units=committed_units+?,adjustment_units=adjustment_units+?,reserved_units=reserved_units+?,active_units=active_units+?,uncertain_units=uncertain_units+?,version=version+1 WHERE resource_id=? AND window_key=? AND version=?`, int64(d.Committed), int64(d.Adjustment), int64(d.Reserved), int64(d.Active), int64(d.Uncertain), string(d.ResourceID), string(d.WindowKey), d.ExpectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	raw, err := json.Marshal(refs)
	if err != nil {
		return false, err
	}
	var charged any
	if at != nil {
		charged = int64(*at)
	}
	kind := "consume"
	if d.Active != 0 || d.Uncertain != 0 {
		kind = "release"
		if d.Uncertain > 0 {
			kind = "encumber"
		}
	}
	_, err = w.tx.ExecContext(w.ctx, `INSERT INTO resource_events(event_id,command_scope,command_id,resource_id,window_key,attempt_id,event_kind,committed_delta,adjustment_delta,reserved_delta,active_delta,uncertain_delta,charged_at,evidence_json,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,CAST(? AS TEXT),?)`, string(scope)+"/"+string(attempt)+"/"+string(d.ResourceID)+"/"+string(d.WindowKey), string(scope), string(attempt), string(d.ResourceID), string(d.WindowKey), string(attempt), kind, int64(d.Committed), int64(d.Adjustment), int64(d.Reserved), int64(d.Active), int64(d.Uncertain), charged, raw, int64(now))
	return err == nil, err
}
func (w *writerTx) SettleReservation(id domain.ID, old, next string) (bool, error) {
	r, e := w.tx.ExecContext(w.ctx, `UPDATE reservations SET phase=? WHERE reservation_id=? AND phase=?`, next, string(id), old)
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n == 1, e
}
func (w *writerTx) InsertSettlementIsolation(attempt, resource, window domain.ID, kind string, units domain.Units, refs []domain.ID, now domain.Millis) error {
	raw, e := json.Marshal(refs)
	if e != nil {
		return e
	}
	_, e = w.tx.ExecContext(w.ctx, `INSERT INTO encumbrances(encumbrance_id,attempt_id,resource_id,window_key,kind,units,status,evidence_json,created_at) VALUES(?,?,?,?,?,?,'open',CAST(? AS TEXT),?)`, "settle/"+string(attempt)+"/"+string(resource)+"/"+kind, string(attempt), string(resource), string(window), kind, int64(units), raw, int64(now))
	return e
}
func (w *writerTx) InsertSettlementHold(attempt, resource domain.ID, reason string, refs []domain.ID, now domain.Millis) error {
	raw, e := json.Marshal(refs)
	if e != nil {
		return e
	}
	_, e = w.tx.ExecContext(w.ctx, `INSERT INTO holds(hold_id,scope_kind,scope_id,reason,status,evidence_json,created_at) VALUES(?,'resource',?,?,'open',CAST(? AS TEXT),?)`, "settle/"+string(attempt)+"/"+string(resource)+"/"+reason, string(resource), reason, raw, int64(now))
	return e
}
func (w *writerTx) settlementEvent(k domain.OperationKey, kind string, raw []byte, now domain.Millis) error {
	_, e := w.tx.ExecContext(w.ctx, `INSERT INTO operation_events(namespace,operation_id,sequence,event_type,payload_json,created_at) SELECT ?,?,COALESCE(MAX(sequence),0)+1,?,CAST(? AS TEXT),? FROM operation_events WHERE namespace=? AND operation_id=?`, string(k.Namespace), string(k.OperationID), kind, raw, int64(now), string(k.Namespace), string(k.OperationID))
	return e
}
func (w *writerTx) PersistSettlement(op store.ClaimOperationRecord, a store.PreparationAttempt, s store.SettlementWrite) (bool, error) {
	r, e := w.tx.ExecContext(w.ctx, `UPDATE attempts SET phase='terminal',external_outcome=?,report_json=CAST(? AS TEXT),report_hash=?,provider_request_id=?,updated_at=?,version=version+1 WHERE attempt_id=? AND phase='executing' AND report_hash IS NULL`, s.Report.Outcome, s.ReportJSON, s.ReportHash, string(s.Report.ProviderRequestID), int64(s.Now), string(a.AttemptID))
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	if e != nil || n != 1 {
		return false, e
	}
	raw, e := json.Marshal(s.Outcomes)
	if e != nil {
		return false, e
	}
	if e = w.settlementEvent(a.Key, "attempt_settled", raw, s.Now); e != nil {
		return false, e
	}
	if s.Result != nil {
		raw, e = json.Marshal(s.Result)
		if e != nil {
			return false, e
		}
		r, e = w.tx.ExecContext(w.ctx, `UPDATE operations SET state=?,result_json=CAST(? AS TEXT),updated_at=?,version=version+1 WHERE namespace=? AND operation_id=? AND state='running' AND version=?`, string(s.Result.State), raw, int64(s.Now), string(a.Key.Namespace), string(a.Key.OperationID), op.Version)
		if e != nil {
			return false, e
		}
		n, e = r.RowsAffected()
		if e != nil || n != 1 {
			return false, e
		}
		if e = w.settlementEvent(a.Key, "operation_terminal", raw, s.Now); e != nil {
			return false, e
		}
	}
	return true, nil
}
func (w *writerTx) AppendSettlementEvidence(k domain.OperationKey, r domain.ExecutionReport, hash string, principal domain.ID, now domain.Millis) error {
	raw, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = w.tx.ExecContext(w.ctx, `INSERT INTO reconciliations(reconciliation_id,attempt_id,principal_id,report_hash,evidence_json,correction_json,created_at) VALUES(?,?,?, ?,CAST(? AS TEXT),'[]',?) ON CONFLICT(reconciliation_id) DO NOTHING`, "late/"+string(r.AttemptID)+"/"+hash, string(r.AttemptID), string(principal), hash, raw, int64(now))
	return e
}
