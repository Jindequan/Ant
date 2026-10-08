package sqlite

import (
	"database/sql"
	"encoding/json"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

// LED-04 recovery statements. Every method runs inside the caller's
// SubmitWrite transaction; guarded UPDATEs report applied=false on CAS
// misses so the command rolls back without partial effects (K3).

func (w *writerTx) GetActiveRecoveryAttempt(k domain.OperationKey) (domain.ID, string, bool, error) {
	var id, phase string
	err := w.tx.QueryRowContext(w.ctx, `SELECT attempt_id,phase FROM attempts WHERE namespace=? AND operation_id=? AND phase IN('prepared','executing') ORDER BY attempt_id LIMIT 1`, string(k.Namespace), string(k.OperationID)).Scan(&id, &phase)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return domain.ID(id), phase, true, nil
}

func (w *writerTx) ListRecoveryAttemptOutcomes(k domain.OperationKey) ([]store.RecoveryAttemptOutcome, error) {
	rows, err := w.tx.QueryContext(w.ctx, `SELECT attempt_id,phase,external_outcome,report_json FROM attempts WHERE namespace=? AND operation_id=? ORDER BY created_at,attempt_id`, string(k.Namespace), string(k.OperationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.RecoveryAttemptOutcome
	for rows.Next() {
		var o store.RecoveryAttemptOutcome
		var phase string
		var outcome sql.NullString
		var raw []byte
		if err = rows.Scan(&o.AttemptID, &phase, &outcome, &raw); err != nil {
			return nil, err
		}
		o.Phase = phase
		if outcome.Valid {
			o.ExternalOutcome = outcome.String
		}
		if len(raw) > 0 {
			var report domain.ExecutionReport
			if json.Unmarshal(raw, &report) == nil {
				o.ReportEnded = report.ExternalEnded
			}
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (w *writerTx) MarkRecoveryCancelRequested(k domain.OperationKey, expectedVersion int64, at domain.Millis) (bool, error) {
	r, err := w.tx.ExecContext(w.ctx, `UPDATE operations SET cancel_requested_at=COALESCE(cancel_requested_at,?),updated_at=?,version=version+1 WHERE namespace=? AND operation_id=? AND state='running' AND version=?`, int64(at), int64(at), string(k.Namespace), string(k.OperationID), expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (w *writerTx) RenewRecoveryLease(k domain.OperationKey, runtimeID, ownerID domain.ID, generation int64, leaseUntil, now domain.Millis) (bool, error) {
	// deadline_at > now keeps the renewed lease strictly in the future; a
	// dead operation cannot be resurrected by renewal.
	r, err := w.tx.ExecContext(w.ctx, `UPDATE operations SET lease_until=?,updated_at=?,version=version+1 WHERE namespace=? AND operation_id=? AND state='running' AND runtime_id=? AND owner_id=? AND generation=? AND deadline_at>?`, int64(leaseUntil), int64(now), string(k.Namespace), string(k.OperationID), string(runtimeID), string(ownerID), generation, int64(now))
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (w *writerTx) ExtendRecoveryAttemptLease(k domain.OperationKey, generation int64, leaseUntil domain.Millis) error {
	_, err := w.tx.ExecContext(w.ctx, `UPDATE attempts SET lease_until=? WHERE namespace=? AND operation_id=? AND generation=? AND phase IN('prepared','executing')`, int64(leaseUntil), string(k.Namespace), string(k.OperationID), generation)
	return err
}

func (w *writerTx) ListDueRecoveryOperations(now domain.Millis, limit int) ([]store.ClaimOperationRecord, error) {
	rows, err := w.tx.QueryContext(w.ctx, `SELECT namespace,operation_id,pool_id,fingerprint,state,runtime_id,owner_id,generation,lease_until,deadline_at,max_attempts,permits_issued,strategy_json,cancel_requested_at,visible_output_at,result_json,created_at,updated_at,version FROM operations WHERE state='running' AND lease_until<=? ORDER BY lease_until,operation_id LIMIT ?`, int64(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.ClaimOperationRecord
	for rows.Next() {
		var rec store.ClaimOperationRecord
		var ns, opID, poolID, state, runtimeID, ownerID string
		var cancelAt, visibleAt sql.NullInt64
		if err = rows.Scan(&ns, &opID, &poolID, &rec.Fingerprint, &state, &runtimeID, &ownerID, &rec.Generation, &rec.LeaseUntil, &rec.DeadlineAt, &rec.MaxAttempts, &rec.PermitsIssued, &rec.StrategyJSON, &cancelAt, &visibleAt, &rec.ResultJSON, &rec.CreatedAt, &rec.UpdatedAt, &rec.Version); err != nil {
			return nil, err
		}
		rec.Namespace, rec.OperationID, rec.PoolID, rec.State = domain.ID(ns), domain.ID(opID), domain.ID(poolID), domain.OperationState(state)
		rec.RuntimeID, rec.OwnerID = domain.ID(runtimeID), domain.ID(ownerID)
		if cancelAt.Valid {
			v := domain.Millis(cancelAt.Int64)
			rec.CancelRequestedAt = &v
		}
		if visibleAt.Valid {
			v := domain.Millis(visibleAt.Int64)
			rec.VisibleOutputAt = &v
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (w *writerTx) FinishRecoveryAttempt(attemptID domain.ID, fromPhase, externalOutcome string, now domain.Millis) (bool, error) {
	// Same (phase, report absent) guard family the settle transaction uses;
	// generation is enforced by the caller against the read attempt.
	r, err := w.tx.ExecContext(w.ctx, `UPDATE attempts SET phase='terminal',external_outcome=?,updated_at=?,version=version+1 WHERE attempt_id=? AND phase=? AND report_hash IS NULL`, externalOutcome, int64(now), string(attemptID), fromPhase)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (w *writerTx) RevokeRecoveryPermit(permitID domain.ID, now domain.Millis) (bool, error) {
	r, err := w.tx.ExecContext(w.ctx, `UPDATE dispatch_permits SET status='revoked' WHERE permit_id=? AND status='granted'`, string(permitID))
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (w *writerTx) ApplyRecoveryDelta(d domain.ResourceDelta, attempt, scope domain.ID, eventKind string, refs []domain.ID, now domain.Millis) (bool, error) {
	r, err := w.tx.ExecContext(w.ctx, `UPDATE resource_states SET committed_units=committed_units+?,adjustment_units=adjustment_units+?,reserved_units=reserved_units+?,active_units=active_units+?,uncertain_units=uncertain_units+?,version=version+1 WHERE resource_id=? AND window_key=? AND version=?`, int64(d.Committed), int64(d.Adjustment), int64(d.Reserved), int64(d.Active), int64(d.Uncertain), string(d.ResourceID), string(d.WindowKey), d.ExpectedVersion)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	raw, err := json.Marshal(refs)
	if err != nil {
		return false, err
	}
	_, err = w.tx.ExecContext(w.ctx, `INSERT INTO resource_events(event_id,command_scope,command_id,resource_id,window_key,attempt_id,event_kind,committed_delta,adjustment_delta,reserved_delta,active_delta,uncertain_delta,charged_at,evidence_json,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,NULL,CAST(? AS TEXT),?)`, string(scope)+"/"+string(attempt)+"/"+string(d.ResourceID)+"/"+string(d.WindowKey), string(scope), string(attempt), string(d.ResourceID), string(d.WindowKey), string(attempt), eventKind, int64(d.Committed), int64(d.Adjustment), int64(d.Reserved), int64(d.Active), int64(d.Uncertain), raw, int64(now))
	return err == nil, err
}

func (w *writerTx) CompleteRecoveryOperation(k domain.OperationKey, expectedVersion int64, result *domain.OperationResult, bumpGeneration bool, leaseUntil, now domain.Millis) (bool, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return false, err
	}
	q := `UPDATE operations SET state=?,result_json=CAST(? AS TEXT),lease_until=?,updated_at=?,version=version+1`
	if bumpGeneration {
		q += `,generation=generation+1`
	}
	q += ` WHERE namespace=? AND operation_id=? AND state='running' AND version=?`
	r, err := w.tx.ExecContext(w.ctx, q, string(result.State), raw, int64(leaseUntil), int64(now), string(k.Namespace), string(k.OperationID), expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (w *writerTx) BumpRecoveryGeneration(k domain.OperationKey, expectedVersion int64, leaseUntil, now domain.Millis) (bool, error) {
	r, err := w.tx.ExecContext(w.ctx, `UPDATE operations SET generation=generation+1,lease_until=?,updated_at=?,version=version+1 WHERE namespace=? AND operation_id=? AND state='running' AND version=?`, int64(leaseUntil), int64(now), string(k.Namespace), string(k.OperationID), expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (w *writerTx) AppendRecoveryEvent(k domain.OperationKey, eventType string, payload []byte, now domain.Millis) error {
	return w.AppendOperationEvent(k, eventType, payload, now)
}

func (w *writerTx) AppendOperationEvent(k domain.OperationKey, eventType string, payload []byte, now domain.Millis) error {
	_, err := w.tx.ExecContext(w.ctx, `INSERT INTO operation_events(namespace,operation_id,sequence,event_type,payload_json,created_at) SELECT ?,?,COALESCE(MAX(sequence),0)+1,?,CAST(? AS TEXT),? FROM operation_events WHERE namespace=? AND operation_id=?`, string(k.Namespace), string(k.OperationID), eventType, payload, int64(now), string(k.Namespace), string(k.OperationID))
	return err
}
