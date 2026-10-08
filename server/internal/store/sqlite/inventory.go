package sqlite

import (
	"ant/server/internal/domain"
	"ant/server/internal/store"
	"context"
	"database/sql"
	"errors"
)

func getInventory(ctx context.Context, q queryer, ns domain.ID) (store.InventoryRecord, bool, error) {
	var r store.InventoryRecord
	err := q.QueryRowContext(ctx, "SELECT namespace,version,definition_json,updated_at FROM supply_inventory WHERE namespace=?", string(ns)).Scan(&r.Namespace, &r.Version, &r.DefinitionJSON, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}
func (r *readerTx) GetInventory(ns domain.ID) (store.InventoryRecord, bool, error) {
	return getInventory(r.ctx, r.tx, ns)
}
func (w *writerTx) GetInventory(ns domain.ID) (store.InventoryRecord, bool, error) {
	return getInventory(w.ctx, w.tx, ns)
}
func (w *writerTx) SaveInventoryCAS(expected int64, r store.InventoryRecord) (bool, error) {
	var result sql.Result
	var err error
	if expected == 0 {
		result, err = w.tx.ExecContext(w.ctx, "INSERT INTO supply_inventory(namespace,version,definition_json,updated_at) VALUES(?,?,CAST(? AS TEXT),?) ON CONFLICT(namespace) DO NOTHING", string(r.Namespace), r.Version, r.DefinitionJSON, int64(r.UpdatedAt))
	} else {
		result, err = w.tx.ExecContext(w.ctx, "UPDATE supply_inventory SET version=?,definition_json=CAST(? AS TEXT),updated_at=? WHERE namespace=? AND version=?", r.Version, r.DefinitionJSON, int64(r.UpdatedAt), string(r.Namespace), expected)
	}
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
