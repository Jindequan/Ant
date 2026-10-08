package sqlite

import (
	"database/sql"
	"errors"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

func (r *readerTx) GetRestoreGuard() (int64, bool, error) {
	return readRestoreGuard(r.ctx, r.tx)
}

func (r *readerTx) GetOperation(namespace, operationID domain.ID) (store.ClaimOperationRecord, bool, error) {
	return getClaimOperation(r.ctx, r.tx, namespace, operationID)
}

func (r *readerTx) ListOperations(namespace domain.ID, state string, beforeUpdatedAt domain.Millis, beforeOperationID domain.ID, limit int) ([]store.ClaimOperationRecord, error) {
	if namespace == "" || limit < 1 || limit > 201 {
		return nil, errors.New("sqlite: invalid operation query")
	}
	query := `SELECT namespace, operation_id, pool_id, fingerprint, state,
		runtime_id, owner_id, generation, lease_until, deadline_at, max_attempts,
		permits_issued, strategy_json, cancel_requested_at, visible_output_at,
		result_json, created_at, updated_at, version
		FROM operations WHERE namespace=?`
	args := []any{string(namespace)}
	if state != "" {
		query += ` AND state=?`
		args = append(args, state)
	}
	if beforeUpdatedAt > 0 || beforeOperationID != "" {
		if beforeUpdatedAt <= 0 || beforeOperationID == "" {
			return nil, errors.New("sqlite: incomplete operation cursor")
		}
		query += ` AND (updated_at < ? OR (updated_at = ? AND operation_id < ?))`
		args = append(args, int64(beforeUpdatedAt), int64(beforeUpdatedAt), string(beforeOperationID))
	}
	query += ` ORDER BY updated_at DESC, operation_id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.tx.QueryContext(r.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]store.ClaimOperationRecord, 0, limit)
	for rows.Next() {
		rec, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *readerTx) ListRuntimeControlSignals(namespace, runtimeID domain.ID, limit int) ([]store.ControlSignalRecord, error) {
	if namespace == "" || runtimeID == "" || limit < 1 || limit > 128 {
		return nil, errors.New("sqlite: invalid runtime control signal query")
	}
	rows, err := r.tx.QueryContext(r.ctx, `SELECT o.namespace,o.operation_id,rc.control_version
		FROM operations o CROSS JOIN runtime_control rc
		WHERE o.namespace=? AND o.runtime_id=? AND o.state='running' AND o.cancel_requested_at IS NOT NULL
		ORDER BY o.updated_at,o.operation_id LIMIT ?`, string(namespace), string(runtimeID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]store.ControlSignalRecord, 0)
	for rows.Next() {
		var rec store.ControlSignalRecord
		if err := rows.Scan(&rec.Namespace, &rec.OperationID, &rec.ControlVersion); err != nil {
			return nil, err
		}
		rec.CancelRequested = true
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type operationScanner interface{ Scan(...any) error }

func scanOperation(scanner operationScanner) (store.ClaimOperationRecord, error) {
	var rec store.ClaimOperationRecord
	var ns, opID, poolID, state, runtimeID, ownerID string
	var cancelAt, visibleAt sql.NullInt64
	if err := scanner.Scan(
		&ns, &opID, &poolID, &rec.Fingerprint, &state, &runtimeID, &ownerID,
		&rec.Generation, &rec.LeaseUntil, &rec.DeadlineAt, &rec.MaxAttempts, &rec.PermitsIssued,
		&rec.StrategyJSON, &cancelAt, &visibleAt, &rec.ResultJSON, &rec.CreatedAt, &rec.UpdatedAt, &rec.Version,
	); err != nil {
		return store.ClaimOperationRecord{}, err
	}
	rec.Namespace, rec.OperationID, rec.PoolID = domain.ID(ns), domain.ID(opID), domain.ID(poolID)
	rec.State, rec.RuntimeID, rec.OwnerID = domain.OperationState(state), domain.ID(runtimeID), domain.ID(ownerID)
	if cancelAt.Valid {
		v := domain.Millis(cancelAt.Int64)
		rec.CancelRequestedAt = &v
	}
	if visibleAt.Valid {
		v := domain.Millis(visibleAt.Int64)
		rec.VisibleOutputAt = &v
	}
	return rec, nil
}

func (r *readerTx) ListOperationAttempts(namespace, operationID domain.ID) ([]store.OperationAttemptRecord, error) {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT attempt_id,offer_id,binding_id,account_id,phase,external_outcome,provider_request_id
		FROM attempts WHERE namespace=? AND operation_id=? ORDER BY created_at,attempt_id`, string(namespace), string(operationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]store.OperationAttemptRecord, 0)
	for rows.Next() {
		var rec store.OperationAttemptRecord
		var attemptID, offerID, bindingID, accountID, outcome string
		var providerRequestID sql.NullString
		if err := rows.Scan(&attemptID, &offerID, &bindingID, &accountID, &rec.Phase, &outcome, &providerRequestID); err != nil {
			return nil, err
		}
		rec.AttemptID, rec.OfferID, rec.BindingID, rec.AccountID = domain.ID(attemptID), domain.ID(offerID), domain.ID(bindingID), domain.ID(accountID)
		rec.Outcome = outcome
		if providerRequestID.Valid {
			rec.ProviderRequestID = domain.ID(providerRequestID.String)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *readerTx) ListOperationReconciliations(namespace, operationID domain.ID) ([]store.OperationReconciliationRecord, error) {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT x.reconciliation_id,x.attempt_id,x.evidence_json,x.correction_json,x.created_at
		FROM reconciliations x JOIN attempts a ON a.attempt_id=x.attempt_id
		WHERE a.namespace=? AND a.operation_id=? ORDER BY x.created_at,x.reconciliation_id`, string(namespace), string(operationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]store.OperationReconciliationRecord, 0)
	for rows.Next() {
		var rec store.OperationReconciliationRecord
		if err := rows.Scan(&rec.ReconciliationID, &rec.AttemptID, &rec.EvidenceJSON, &rec.CorrectionJSON, &rec.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *readerTx) ListOperationEvents(namespace, operationID domain.ID, afterSequence int64, limit int) ([]store.OperationEventRecord, error) {
	if namespace == "" || operationID == "" || afterSequence < 0 || limit < 1 || limit > 100 {
		return nil, errors.New("sqlite: invalid operation event query")
	}
	rows, err := r.tx.QueryContext(r.ctx, `SELECT sequence,event_type,payload_json,created_at
		FROM operation_events WHERE namespace=? AND operation_id=? AND sequence>?
		ORDER BY sequence LIMIT ?`, string(namespace), string(operationID), afterSequence, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]store.OperationEventRecord, 0, limit)
	for rows.Next() {
		var rec store.OperationEventRecord
		if err := rows.Scan(&rec.Sequence, &rec.EventType, &rec.PayloadJSON, &rec.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *readerTx) GetOperationEventWatermark(namespace, operationID domain.ID) (int64, error) {
	if namespace == "" || operationID == "" {
		return 0, errors.New("sqlite: invalid operation event watermark query")
	}
	var sequence int64
	err := r.tx.QueryRowContext(r.ctx, `SELECT COALESCE(MAX(sequence),0) FROM operation_events WHERE namespace=? AND operation_id=?`, string(namespace), string(operationID)).Scan(&sequence)
	return sequence, err
}

func (r *readerTx) ListOperationDecisions(namespace, operationID domain.ID, limit int) ([]store.OperationDecisionRecord, error) {
	if namespace == "" || operationID == "" || limit < 1 || limit > 257 {
		return nil, errors.New("sqlite: invalid operation decision query")
	}
	rows, err := r.tx.QueryContext(r.ctx, `SELECT d.decision_id,COALESCE(d.attempt_id,''),d.stage,d.snapshot_json,
		CASE WHEN d.trace_json='{}' AND d.stage='dispatch' THEN COALESCE(p.trace_json,'{}') ELSE d.trace_json END,
		d.trace_truncated,d.created_at
		FROM decision_records d
		LEFT JOIN decision_records p ON p.namespace=d.namespace AND p.operation_id=d.operation_id
			AND p.attempt_id=d.attempt_id AND p.stage='prepare'
		WHERE d.namespace=? AND d.operation_id=?
		ORDER BY d.created_at,d.decision_id LIMIT ?`, string(namespace), string(operationID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]store.OperationDecisionRecord, 0, limit)
	for rows.Next() {
		var rec store.OperationDecisionRecord
		var decisionID, attemptID string
		var truncated int
		if err := rows.Scan(&decisionID, &attemptID, &rec.Stage, &rec.SnapshotJSON, &rec.TraceJSON, &truncated, &rec.CreatedAt); err != nil {
			return nil, err
		}
		rec.DecisionID, rec.AttemptID, rec.TraceTruncated = domain.ID(decisionID), domain.ID(attemptID), truncated != 0
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
