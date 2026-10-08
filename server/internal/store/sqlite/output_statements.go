package sqlite

import (
	"database/sql"
	"errors"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

func (w *writerTx) GetOutputChunk(attemptID domain.ID, sequence int64) (store.OutputChunkRecord, bool, error) {
	var rec store.OutputChunkRecord
	err := w.tx.QueryRowContext(w.ctx, `SELECT attempt_id,sequence,data,sha256,created_at FROM output_chunks WHERE attempt_id=? AND sequence=?`, string(attemptID), sequence).
		Scan(&rec.AttemptID, &rec.Sequence, &rec.Data, &rec.SHA256, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.OutputChunkRecord{}, false, nil
	}
	return rec, err == nil, err
}

func (w *writerTx) CountOperationOutput(key domain.OperationKey) (int64, int64, error) {
	var chunks, bytes int64
	err := w.tx.QueryRowContext(w.ctx, `SELECT COUNT(*),COALESCE(SUM(length(o.data)),0) FROM output_chunks o JOIN attempts a USING(attempt_id) WHERE a.namespace=? AND a.operation_id=?`, string(key.Namespace), string(key.OperationID)).Scan(&chunks, &bytes)
	return chunks, bytes, err
}

func (w *writerTx) AppendOutputChunk(key domain.OperationKey, rec store.OutputChunkRecord) error {
	res, err := w.tx.ExecContext(w.ctx, `INSERT INTO output_chunks(attempt_id,sequence,data,sha256,created_at)
	 SELECT a.attempt_id,?,?,?,? FROM attempts a WHERE a.attempt_id=? AND a.namespace=? AND a.operation_id=?`,
		rec.Sequence, rec.Data, rec.SHA256, int64(rec.CreatedAt), string(rec.AttemptID), string(key.Namespace), string(key.OperationID))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("sqlite: output attempt does not belong to operation")
	}
	return nil
}

func (w *writerTx) MarkOutputVisible(key domain.OperationKey, at domain.Millis) error {
	res, err := w.tx.ExecContext(w.ctx, `UPDATE operations SET visible_output_at=COALESCE(visible_output_at,?),updated_at=?,version=version+1 WHERE namespace=? AND operation_id=? AND state='running'`, int64(at), int64(at), string(key.Namespace), string(key.OperationID))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("sqlite: operation is not visible-output eligible")
	}
	return nil
}

func (r *readerTx) CountOperationOutput(key domain.OperationKey) (int64, int64, error) {
	var chunks, bytes int64
	err := r.tx.QueryRowContext(r.ctx, `SELECT COUNT(*),COALESCE(SUM(length(o.data)),0) FROM output_chunks o JOIN attempts a USING(attempt_id) WHERE a.namespace=? AND a.operation_id=?`, string(key.Namespace), string(key.OperationID)).Scan(&chunks, &bytes)
	return chunks, bytes, err
}

func (r *readerTx) ListOperationOutput(key domain.OperationKey, afterSequence int64, limit int) ([]store.OutputChunkRecord, error) {
	if key.Namespace == "" || key.OperationID == "" || afterSequence < -1 || limit < 1 || limit > 101 {
		return nil, errors.New("sqlite: invalid output query")
	}
	rows, err := r.tx.QueryContext(r.ctx, `SELECT o.attempt_id,o.sequence,o.data,o.sha256,o.created_at FROM output_chunks o JOIN attempts a USING(attempt_id) WHERE a.namespace=? AND a.operation_id=? AND o.sequence>? ORDER BY o.sequence,o.created_at,o.attempt_id LIMIT ?`, string(key.Namespace), string(key.OperationID), afterSequence, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]store.OutputChunkRecord, 0, limit)
	for rows.Next() {
		var rec store.OutputChunkRecord
		if err := rows.Scan(&rec.AttemptID, &rec.Sequence, &rec.Data, &rec.SHA256, &rec.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
