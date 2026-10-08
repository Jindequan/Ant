package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

func getClaimOperation(ctx context.Context, q queryer, namespace, operationID domain.ID) (store.ClaimOperationRecord, bool, error) {
	var rec store.ClaimOperationRecord
	var ns, opID, poolID, state, runtimeID, ownerID string
	var cancelAt, visibleAt sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT namespace, operation_id, pool_id, fingerprint, state,
		runtime_id, owner_id, generation, lease_until, deadline_at, max_attempts,
		permits_issued, strategy_json, cancel_requested_at, visible_output_at,
		result_json, created_at, updated_at, version
		FROM operations WHERE namespace = ? AND operation_id = ?`,
		string(namespace), string(operationID)).Scan(
		&ns, &opID, &poolID, &rec.Fingerprint, &state,
		&runtimeID, &ownerID, &rec.Generation, &rec.LeaseUntil, &rec.DeadlineAt, &rec.MaxAttempts,
		&rec.PermitsIssued, &rec.StrategyJSON, &cancelAt, &visibleAt,
		&rec.ResultJSON, &rec.CreatedAt, &rec.UpdatedAt, &rec.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ClaimOperationRecord{}, false, nil
	}
	if err != nil {
		return store.ClaimOperationRecord{}, false, err
	}
	rec.Namespace = domain.ID(ns)
	rec.OperationID = domain.ID(opID)
	rec.PoolID = domain.ID(poolID)
	rec.State = domain.OperationState(state)
	rec.RuntimeID = domain.ID(runtimeID)
	rec.OwnerID = domain.ID(ownerID)
	if cancelAt.Valid {
		v := domain.Millis(cancelAt.Int64)
		rec.CancelRequestedAt = &v
	}
	if visibleAt.Valid {
		v := domain.Millis(visibleAt.Int64)
		rec.VisibleOutputAt = &v
	}
	return rec, true, nil
}

func insertClaimOperation(ctx context.Context, q queryer, rec store.ClaimOperationRecord) error {
	_, err := q.ExecContext(ctx, `INSERT INTO operations(
		namespace, operation_id, pool_id, fingerprint, state, runtime_id, owner_id,
		generation, lease_until, deadline_at, max_attempts, permits_issued,
		strategy_json, cancel_requested_at, visible_output_at, result_json,
		created_at, updated_at, version
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CAST(? AS TEXT), ?, ?, CAST(? AS TEXT), ?, ?, ?)`,
		string(rec.Namespace), string(rec.OperationID), string(rec.PoolID), rec.Fingerprint,
		string(rec.State), string(rec.RuntimeID), string(rec.OwnerID), rec.Generation,
		int64(rec.LeaseUntil), int64(rec.DeadlineAt), rec.MaxAttempts, rec.PermitsIssued,
		jsonParam(rec.StrategyJSON), nullableMillis(rec.CancelRequestedAt), nullableMillis(rec.VisibleOutputAt),
		nullableJSON(rec.ResultJSON), int64(rec.CreatedAt), int64(rec.UpdatedAt), rec.Version)
	return err
}

func nullableMillis(v *domain.Millis) any {
	if v == nil {
		return nil
	}
	return int64(*v)
}

func nullableJSON(v []byte) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

func (w *writerTx) GetClaimOperation(namespace, operationID domain.ID) (store.ClaimOperationRecord, bool, error) {
	return getClaimOperation(w.ctx, w.tx, namespace, operationID)
}

func (w *writerTx) InsertClaimOperation(rec store.ClaimOperationRecord) error {
	return insertClaimOperation(w.ctx, w.tx, rec)
}
