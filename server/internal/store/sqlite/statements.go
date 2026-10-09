package sqlite

// Typed transaction surfaces: every SQL statement of the store lives here.
// Ledger/config handlers only orchestrate the store.WriterTx and
// store.ReaderTx methods (02-STORAGE §3); all statements run on the single
// *sql.Tx of the owning command transaction -- never on the pool.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

// queryer is the shared statement surface of an open transaction; *sql.Tx
// implements it for both the write transaction and the read snapshot.
type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// writerTx carries the command's budget context: every statement respects the
// whole-command deadline, and an expired budget aborts the transaction.
type writerTx struct {
	ctx context.Context
	tx  queryer // stmtTx: the command's *sql.Tx behind the statement cache
}

// CommandContext exposes the command-wide deadline context so a callback can
// cooperate with the whole-command budget (writer.go contract, R09). Every
// statement already runs under the same context.
func (w *writerTx) CommandContext() context.Context { return w.ctx }

// readerTx is a consistent snapshot on the query-only pool.
type readerTx struct {
	ctx context.Context
	tx  *sql.Tx
}

// jsonParam adapts a Go []byte JSON column value to the STRICT schema: []byte
// binds as BLOB, and a BLOB is a type violation in a TEXT column of a STRICT
// table, so the value is cast to TEXT in SQL while the Go model stays []byte.
func jsonParam(b []byte) any { return b } // used inside CAST(? AS TEXT)

func scanBool(v int64) bool { return v != 0 }

// --- config revisions / activation pointer (K1 save-read-back) ---

func insertConfigRevision(ctx context.Context, q queryer, rec store.ConfigRevisionRecord) error {
	_, err := q.ExecContext(ctx,
		"INSERT INTO config_revisions(revision, definition_json, created_at) VALUES(?, CAST(? AS TEXT), ?)",
		string(rec.Revision), jsonParam(rec.DefinitionJSON), int64(rec.CreatedAt))
	return err
}

func getConfigRevision(ctx context.Context, q queryer, revision domain.ID) (store.ConfigRevisionRecord, bool, error) {
	var rec store.ConfigRevisionRecord
	var def []byte
	err := q.QueryRowContext(ctx,
		"SELECT revision, definition_json, created_at FROM config_revisions WHERE revision = ?",
		string(revision)).Scan(&rec.Revision, &def, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ConfigRevisionRecord{}, false, nil
	}
	if err != nil {
		return store.ConfigRevisionRecord{}, false, err
	}
	rec.Revision = domain.ID(rec.Revision)
	rec.DefinitionJSON = def
	return rec, true, nil
}

func getActiveConfig(ctx context.Context, q queryer) (store.ActiveConfigRecord, bool, error) {
	var rec store.ActiveConfigRecord
	var revision string
	err := q.QueryRowContext(ctx,
		"SELECT revision, version FROM active_config WHERE singleton = 1").Scan(&revision, &rec.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ActiveConfigRecord{}, false, nil
	}
	if err != nil {
		return store.ActiveConfigRecord{}, false, err
	}
	rec.Revision = domain.ID(revision)
	return rec, true, nil
}

func setActiveConfig(ctx context.Context, q queryer, rec store.ActiveConfigRecord) error {
	// Single-row activation pointer; the FK enforces that the referenced
	// revision was inserted earlier in this same transaction.
	_, err := q.ExecContext(ctx,
		"INSERT INTO active_config(singleton, revision, version) VALUES(1, ?, ?) "+
			"ON CONFLICT(singleton) DO UPDATE SET revision = excluded.revision, version = excluded.version",
		string(rec.Revision), rec.Version)
	return err
}

func getConfigDraft(ctx context.Context, q queryer, draftID domain.ID) (store.ConfigDraftRecord, bool, error) {
	var rec store.ConfigDraftRecord
	var baseRevision sql.NullString
	err := q.QueryRowContext(ctx,
		"SELECT draft_id, principal_id, base_revision, version, status, definition_json, issues_json, created_at, updated_at "+
			"FROM config_drafts WHERE draft_id = ?", string(draftID)).Scan(
		&rec.DraftID, &rec.PrincipalID, &baseRevision, &rec.Version, &rec.Status,
		&rec.DefinitionJSON, &rec.IssuesJSON, &rec.CreatedAt, &rec.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ConfigDraftRecord{}, false, nil
	}
	if err != nil {
		return store.ConfigDraftRecord{}, false, err
	}
	if baseRevision.Valid {
		rec.BaseRevision = domain.ID(baseRevision.String)
	}
	return rec, true, nil
}

func insertConfigDraft(ctx context.Context, q queryer, rec store.ConfigDraftRecord) error {
	var baseRevision any
	if rec.BaseRevision != "" {
		baseRevision = string(rec.BaseRevision)
	}
	_, err := q.ExecContext(ctx,
		"INSERT INTO config_drafts(draft_id, principal_id, base_revision, version, status, definition_json, issues_json, created_at, updated_at) "+
			"VALUES(?, ?, ?, ?, ?, CAST(? AS TEXT), CAST(? AS TEXT), ?, ?)",
		string(rec.DraftID), string(rec.PrincipalID), baseRevision, rec.Version, rec.Status,
		jsonParam(rec.DefinitionJSON), jsonParam(rec.IssuesJSON), int64(rec.CreatedAt), int64(rec.UpdatedAt))
	return err
}

func updateConfigDraftCAS(ctx context.Context, q queryer, principalID domain.ID, expectedVersion int64, rec store.ConfigDraftRecord) (bool, error) {
	var baseRevision any
	if rec.BaseRevision != "" {
		baseRevision = string(rec.BaseRevision)
	}
	result, err := q.ExecContext(ctx,
		"UPDATE config_drafts SET base_revision = ?, version = ?, status = ?, definition_json = CAST(? AS TEXT), "+
			"issues_json = CAST(? AS TEXT), updated_at = ? WHERE draft_id = ? AND principal_id = ? AND version = ?",
		baseRevision, rec.Version, rec.Status, jsonParam(rec.DefinitionJSON), jsonParam(rec.IssuesJSON), int64(rec.UpdatedAt),
		string(rec.DraftID), string(principalID), expectedVersion)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func hasSecretRevision(ctx context.Context, q queryer, namespace, ref, revision domain.ID) (bool, error) {
	var present int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM secret_records r JOIN secret_refs s ON s.secret_ref=r.secret_ref WHERE s.namespace = ? AND r.secret_ref = ? AND r.revision = ?",
		string(namespace), string(ref), string(revision)).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return present == 1, err
}

func getSecretReference(ctx context.Context, q queryer, ref domain.ID) (store.SecretReferenceRecord, bool, error) {
	var rec store.SecretReferenceRecord
	err := q.QueryRowContext(ctx, "SELECT secret_ref, principal_id, namespace, current_revision, mask, created_at, updated_at FROM secret_refs WHERE secret_ref = ?", string(ref)).Scan(
		&rec.Ref, &rec.PrincipalID, &rec.Namespace, &rec.CurrentRevision, &rec.Mask, &rec.CreatedAt, &rec.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.SecretReferenceRecord{}, false, nil
	}
	return rec, err == nil, err
}

func getSecretRevision(ctx context.Context, q queryer, ref, revision domain.ID) (store.SecretRevisionRecord, bool, error) {
	var rec store.SecretRevisionRecord
	err := q.QueryRowContext(ctx, "SELECT secret_ref, revision, nonce, ciphertext, master_key_ref, created_at FROM secret_records WHERE secret_ref = ? AND revision = ?", string(ref), string(revision)).Scan(
		&rec.Ref, &rec.Revision, &rec.Nonce, &rec.Ciphertext, &rec.MasterKeyRef, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.SecretRevisionRecord{}, false, nil
	}
	return rec, err == nil, err
}

func insertSecret(ctx context.Context, q queryer, reference store.SecretReferenceRecord, revision store.SecretRevisionRecord) error {
	if err := insertSecretRevision(ctx, q, revision); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, "INSERT INTO secret_refs(secret_ref, principal_id, namespace, current_revision, mask, created_at, updated_at) VALUES(?,?,?,?,?,?,?)",
		string(reference.Ref), string(reference.PrincipalID), string(reference.Namespace), string(reference.CurrentRevision), reference.Mask, int64(reference.CreatedAt), int64(reference.UpdatedAt))
	return err
}

func insertSecretRevision(ctx context.Context, q queryer, rec store.SecretRevisionRecord) error {
	_, err := q.ExecContext(ctx, "INSERT INTO secret_records(secret_ref, revision, nonce, ciphertext, master_key_ref, mask, created_at) VALUES(?,?,?,?,?,?,?)",
		string(rec.Ref), string(rec.Revision), rec.Nonce, rec.Ciphertext, rec.MasterKeyRef, rec.Mask, int64(rec.CreatedAt))
	return err
}

func advanceSecretRevision(ctx context.Context, q queryer, principal, namespace, ref, expected domain.ID, revision store.SecretRevisionRecord, updatedAt domain.Millis) (bool, error) {
	if err := insertSecretRevision(ctx, q, revision); err != nil {
		return false, err
	}
	result, err := q.ExecContext(ctx, "UPDATE secret_refs SET current_revision=?, mask=?, updated_at=? WHERE secret_ref=? AND principal_id=? AND namespace=? AND current_revision=?",
		string(revision.Revision), revision.Mask, int64(updatedAt), string(ref), string(principal), string(namespace), string(expected))
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// --- pools / members / K4 full-group reorder ---

func getPool(ctx context.Context, q queryer, poolID domain.ID) (store.PoolRecord, bool, error) {
	var rec store.PoolRecord
	var enabled int64
	err := q.QueryRowContext(ctx,
		"SELECT pool_id, namespace, name, enabled, version FROM resource_pools WHERE pool_id = ?",
		string(poolID)).Scan(&rec.PoolID, &rec.Namespace, &rec.Name, &enabled, &rec.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return store.PoolRecord{}, false, nil
	}
	if err != nil {
		return store.PoolRecord{}, false, err
	}
	rec.Enabled = scanBool(enabled)
	return rec, true, nil
}

func listPoolsByNamespace(ctx context.Context, q queryer, namespace domain.ID) ([]store.PoolRecord, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT pool_id, namespace, name, enabled, version FROM resource_pools WHERE namespace = ? ORDER BY pool_id",
		string(namespace))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.PoolRecord
	for rows.Next() {
		var rec store.PoolRecord
		var enabled int64
		if err := rows.Scan(&rec.PoolID, &rec.Namespace, &rec.Name, &enabled, &rec.Version); err != nil {
			return nil, err
		}
		rec.Enabled = scanBool(enabled)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func listPoolMembers(ctx context.Context, q queryer, poolID domain.ID) ([]store.PoolMemberRecord, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT pool_id, offer_id, priority, enabled, version FROM pool_members WHERE pool_id = ? "+
			"ORDER BY priority, offer_id",
		string(poolID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.PoolMemberRecord
	for rows.Next() {
		var rec store.PoolMemberRecord
		var enabled int64
		if err := rows.Scan(&rec.PoolID, &rec.OfferID, &rec.Priority, &enabled, &rec.Version); err != nil {
			return nil, err
		}
		rec.Enabled = scanBool(enabled)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func getOffer(ctx context.Context, q queryer, offerID domain.ID) (store.OfferRecord, bool, error) {
	var rec store.OfferRecord
	var enabled int64
	err := q.QueryRowContext(ctx,
		"SELECT offer_id, binding_id, domain, capability_profile, enabled, version FROM resource_offers WHERE offer_id = ?",
		string(offerID)).Scan(&rec.OfferID, &rec.BindingID, &rec.Domain, &rec.CapabilityProfile, &enabled, &rec.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return store.OfferRecord{}, false, nil
	}
	if err != nil {
		return store.OfferRecord{}, false, err
	}
	rec.Enabled = scanBool(enabled)
	return rec, true, nil
}

func replacePoolMembers(ctx context.Context, q queryer, poolID domain.ID, newVersion int64, members []store.PoolMemberRecord) error {
	res, err := q.ExecContext(ctx,
		"UPDATE resource_pools SET version = ? WHERE pool_id = ?", newVersion, string(poolID))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlite: replace pool members: pool %s not found", poolID)
	}
	return rewriteMembers(ctx, q, poolID, members)
}

// ReplacePoolMembersCAS is the K4 primitive: the members block and the pool
// version move together or not at all. applied=false means the stored version
// differed; nothing was written and the caller aborts the whole command.
func replacePoolMembersCAS(ctx context.Context, q queryer, poolID domain.ID, expectedVersion, newVersion int64, members []store.PoolMemberRecord) (bool, error) {
	res, err := q.ExecContext(ctx,
		"UPDATE resource_pools SET version = ? WHERE pool_id = ? AND version = ?",
		newVersion, string(poolID), expectedVersion)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	return true, rewriteMembers(ctx, q, poolID, members)
}

// rewriteMembers does the full-group replacement: delete then insert every
// member of the new ordering (02-STORAGE §6: 全部顺序更新同事务).
func rewriteMembers(ctx context.Context, q queryer, poolID domain.ID, members []store.PoolMemberRecord) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM pool_members WHERE pool_id = ?", string(poolID)); err != nil {
		return err
	}
	for _, m := range members {
		if _, err := q.ExecContext(ctx,
			"INSERT INTO pool_members(pool_id, offer_id, priority, enabled, version) VALUES(?, ?, ?, ?, ?)",
			string(poolID), string(m.OfferID), m.Priority, boolInt(m.Enabled), m.Version); err != nil {
			return err
		}
	}
	return nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// --- runtime control / receipts / audit ---

func getControlVersion(ctx context.Context, q queryer) (int64, int, error) {
	var version int64
	var guard int64
	err := q.QueryRowContext(ctx,
		"SELECT control_version, restore_guard FROM runtime_control WHERE singleton = 1").Scan(&version, &guard)
	if err != nil {
		return 0, 0, err
	}
	return version, int(guard), nil
}

func bumpControlVersion(ctx context.Context, q queryer) (int64, error) {
	if _, err := q.ExecContext(ctx,
		"UPDATE runtime_control SET control_version = control_version + 1 WHERE singleton = 1"); err != nil {
		return 0, err
	}
	var version int64
	if err := q.QueryRowContext(ctx,
		"SELECT control_version FROM runtime_control WHERE singleton = 1").Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

func setRestoreGuard(ctx context.Context, q queryer, expectedVersion int64, enabled bool) (bool, error) {
	result, err := q.ExecContext(ctx,
		"UPDATE runtime_control SET restore_guard = ?, control_version = control_version + 1 WHERE singleton = 1 AND control_version = ? AND restore_guard <> ?",
		boolInt(enabled), expectedVersion, boolInt(enabled))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func readRestoreGuard(ctx context.Context, q queryer) (int64, bool, error) {
	version, guard, err := getControlVersion(ctx, q)
	if err != nil {
		return 0, false, err
	}
	if version < 1 || (guard != 0 && guard != 1) {
		return 0, false, errors.New("sqlite: invalid runtime control state")
	}
	return version, guard == 1, nil
}

func getReceipt(ctx context.Context, q queryer, scope, commandID domain.ID) (store.ReceiptRecord, bool, error) {
	var rec store.ReceiptRecord
	var kind, hash string
	var result []byte
	err := q.QueryRowContext(ctx,
		"SELECT kind, input_hash, result_json, created_at FROM command_receipts WHERE scope = ? AND command_id = ?",
		string(scope), string(commandID)).Scan(&kind, &hash, &result, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ReceiptRecord{}, false, nil
	}
	if err != nil {
		return store.ReceiptRecord{}, false, err
	}
	rec.Scope, rec.CommandID, rec.Kind, rec.InputHash, rec.ResultJSON =
		scope, commandID, kind, hash, result
	return rec, true, nil
}

// insertReceipt stores the replayable result; a duplicate (scope, command_id)
// violates the primary key and fails the command -- the Get-then-compare
// dedup decision belongs to the caller before this insert.
func insertReceipt(ctx context.Context, q queryer, rec store.ReceiptRecord) error {
	_, err := q.ExecContext(ctx,
		"INSERT INTO command_receipts(scope, command_id, kind, input_hash, result_json, created_at) "+
			"VALUES(?, ?, ?, ?, CAST(? AS TEXT), ?)",
		string(rec.Scope), string(rec.CommandID), rec.Kind, rec.InputHash, jsonParam(rec.ResultJSON), int64(rec.CreatedAt))
	return err
}

func insertAudit(ctx context.Context, q queryer, rec store.AuditRecord) error {
	var evidence any
	if rec.EvidenceRef != "" {
		evidence = string(rec.EvidenceRef) // audit_log.evidence_ref is nullable
	}
	_, err := q.ExecContext(ctx,
		"INSERT INTO audit_log(audit_id, principal_id, namespace, action, target_ref, evidence_ref, created_at) "+
			"VALUES(?, ?, ?, ?, ?, ?, ?)",
		string(rec.AuditID), string(rec.PrincipalID), string(rec.Namespace), rec.Action,
		string(rec.TargetRef), evidence, int64(rec.CreatedAt))
	return err
}

func hasBootstrapAuthSession(ctx context.Context, q queryer) (bool, error) {
	var exists int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM auth_sessions WHERE kind='bootstrap' LIMIT 1").Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func insertPrincipal(ctx context.Context, q queryer, principalID domain.ID, displayName string, createdAt domain.Millis) error {
	_, err := q.ExecContext(ctx,
		"INSERT INTO principals(principal_id, display_name, enabled, created_at) VALUES(?, ?, 1, ?)",
		string(principalID), displayName, int64(createdAt))
	return err
}

func insertNamespaceGrant(ctx context.Context, q queryer, principalID, namespace domain.ID, permission domain.Permission) error {
	_, err := q.ExecContext(ctx,
		"INSERT INTO namespace_grants(principal_id, namespace, permission, scope_kind, scope_id) VALUES(?, ?, ?, 'namespace', ?)",
		string(principalID), string(namespace), string(permission), string(namespace))
	return err
}

func listNamespaceGrants(ctx context.Context, q queryer, principalID, namespace domain.ID) ([]domain.Permission, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT permission FROM namespace_grants WHERE principal_id=? AND namespace=? AND scope_kind='namespace' AND scope_id=? ORDER BY permission",
		string(principalID), string(namespace), string(namespace))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	permissions := []domain.Permission{}
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return nil, err
		}
		permissions = append(permissions, domain.Permission(permission))
	}
	return permissions, rows.Err()
}

func insertAuthSession(ctx context.Context, q queryer, rec store.AuthSessionRecord) error {
	var csrfHash any
	if rec.CSRFHash != "" {
		csrfHash = rec.CSRFHash
	}
	_, err := q.ExecContext(ctx,
		"INSERT INTO auth_sessions(token_hash, principal_id, kind, namespace, expires_at, csrf_hash, created_at) VALUES(?, ?, ?, ?, ?, ?, ?)",
		rec.TokenHash, string(rec.PrincipalID), rec.Kind, string(rec.Namespace), int64(rec.ExpiresAt), csrfHash, int64(rec.CreatedAt))
	return err
}

func revokeAuthSession(ctx context.Context, q queryer, tokenHash string, at domain.Millis) error {
	_, err := q.ExecContext(ctx,
		"UPDATE auth_sessions SET revoked_at=? WHERE token_hash=? AND revoked_at IS NULL", int64(at), tokenHash)
	return err
}

func exchangeBootstrap(ctx context.Context, q queryer, bootstrapHash string, session store.AuthSessionRecord, at domain.Millis) error {
	result, err := q.ExecContext(ctx,
		"UPDATE auth_sessions SET revoked_at=? WHERE token_hash=? AND kind='bootstrap' AND revoked_at IS NULL AND expires_at>?",
		int64(at), bootstrapHash, int64(at))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return store.ErrBootstrapConsumed
	}
	return insertAuthSession(ctx, q, session)
}

func lookupAuthSession(ctx context.Context, q queryer, tokenHash string, now domain.Millis) (store.AuthSessionRecord, bool, error) {
	var rec store.AuthSessionRecord
	var principalID, namespace string
	var csrfHash sql.NullString
	err := q.QueryRowContext(ctx,
		"SELECT s.principal_id, s.kind, s.namespace, s.expires_at, s.csrf_hash, s.created_at "+
			"FROM auth_sessions s JOIN principals p ON p.principal_id=s.principal_id "+
			"WHERE s.token_hash=? AND s.revoked_at IS NULL AND s.expires_at>? AND p.enabled=1",
		tokenHash, int64(now)).Scan(&principalID, &rec.Kind, &namespace, &rec.ExpiresAt, &csrfHash, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.AuthSessionRecord{}, false, nil
	}
	if err != nil {
		return store.AuthSessionRecord{}, false, err
	}
	rec.TokenHash, rec.PrincipalID, rec.Namespace = tokenHash, domain.ID(principalID), domain.ID(namespace)
	if csrfHash.Valid {
		rec.CSRFHash = csrfHash.String
	}
	rows, err := q.QueryContext(ctx,
		"SELECT permission FROM namespace_grants WHERE principal_id=? AND namespace=? AND scope_kind='namespace' AND scope_id=? ORDER BY permission",
		principalID, namespace, namespace)
	if err != nil {
		return store.AuthSessionRecord{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return store.AuthSessionRecord{}, false, err
		}
		rec.Permissions = append(rec.Permissions, domain.Permission(permission))
	}
	if err := rows.Err(); err != nil {
		return store.AuthSessionRecord{}, false, err
	}
	return rec, len(rec.Permissions) != 0, nil
}

// --- store.WriterTx / store.ReaderTx adapters ---

func (w *writerTx) InsertConfigRevision(rec store.ConfigRevisionRecord) error {
	return insertConfigRevision(w.ctx, w.tx, rec)
}
func (w *writerTx) GetConfigRevision(revision domain.ID) (store.ConfigRevisionRecord, bool, error) {
	return getConfigRevision(w.ctx, w.tx, revision)
}
func (w *writerTx) GetActiveConfig() (store.ActiveConfigRecord, bool, error) {
	return getActiveConfig(w.ctx, w.tx)
}
func (w *writerTx) SetActiveConfig(rec store.ActiveConfigRecord) error {
	return setActiveConfig(w.ctx, w.tx, rec)
}
func (w *writerTx) GetConfigDraft(draftID domain.ID) (store.ConfigDraftRecord, bool, error) {
	return getConfigDraft(w.ctx, w.tx, draftID)
}
func (w *writerTx) InsertConfigDraft(rec store.ConfigDraftRecord) error {
	return insertConfigDraft(w.ctx, w.tx, rec)
}
func (w *writerTx) UpdateConfigDraftCAS(principalID domain.ID, expectedVersion int64, rec store.ConfigDraftRecord) (bool, error) {
	return updateConfigDraftCAS(w.ctx, w.tx, principalID, expectedVersion, rec)
}
func (w *writerTx) HasSecretRevision(namespace, ref, revision domain.ID) (bool, error) {
	return hasSecretRevision(w.ctx, w.tx, namespace, ref, revision)
}
func (w *writerTx) GetSecretReference(ref domain.ID) (store.SecretReferenceRecord, bool, error) {
	return getSecretReference(w.ctx, w.tx, ref)
}
func (w *writerTx) InsertSecret(reference store.SecretReferenceRecord, revision store.SecretRevisionRecord) error {
	return insertSecret(w.ctx, w.tx, reference, revision)
}
func (w *writerTx) InsertSecretRevision(revision store.SecretRevisionRecord) error {
	return insertSecretRevision(w.ctx, w.tx, revision)
}
func (w *writerTx) AdvanceSecretRevision(principal, namespace, ref, expectedRevision domain.ID, revision store.SecretRevisionRecord, updatedAt domain.Millis) (bool, error) {
	return advanceSecretRevision(w.ctx, w.tx, principal, namespace, ref, expectedRevision, revision, updatedAt)
}
func (w *writerTx) GetPool(poolID domain.ID) (store.PoolRecord, bool, error) {
	return getPool(w.ctx, w.tx, poolID)
}
func (w *writerTx) ListPoolMembers(poolID domain.ID) ([]store.PoolMemberRecord, error) {
	return listPoolMembers(w.ctx, w.tx, poolID)
}
func (w *writerTx) GetOffer(offerID domain.ID) (store.OfferRecord, bool, error) {
	return getOffer(w.ctx, w.tx, offerID)
}
func (w *writerTx) ReplacePoolMembers(poolID domain.ID, newVersion int64, members []store.PoolMemberRecord) error {
	return replacePoolMembers(w.ctx, w.tx, poolID, newVersion, members)
}
func (w *writerTx) ReplacePoolMembersCAS(poolID domain.ID, expectedVersion, newVersion int64, members []store.PoolMemberRecord) (bool, error) {
	return replacePoolMembersCAS(w.ctx, w.tx, poolID, expectedVersion, newVersion, members)
}
func (w *writerTx) GetControlVersion() (int64, int, error) {
	return getControlVersion(w.ctx, w.tx)
}
func (w *writerTx) SetRestoreGuard(expectedControlVersion int64, enabled bool) (bool, error) {
	return setRestoreGuard(w.ctx, w.tx, expectedControlVersion, enabled)
}
func (w *writerTx) BumpControlVersion() (int64, error) {
	return bumpControlVersion(w.ctx, w.tx)
}
func (w *writerTx) GetReceipt(scope, commandID domain.ID) (store.ReceiptRecord, bool, error) {
	return getReceipt(w.ctx, w.tx, scope, commandID)
}
func (w *writerTx) InsertReceipt(rec store.ReceiptRecord) error {
	return insertReceipt(w.ctx, w.tx, rec)
}
func (w *writerTx) InsertAudit(rec store.AuditRecord) error {
	return insertAudit(w.ctx, w.tx, rec)
}
func (w *writerTx) HasBootstrapAuthSession() (bool, error) {
	return hasBootstrapAuthSession(w.ctx, w.tx)
}
func (w *writerTx) ListNamespaceGrants(principalID, namespace domain.ID) ([]domain.Permission, error) {
	return listNamespaceGrants(w.ctx, w.tx, principalID, namespace)
}
func (w *writerTx) InsertPrincipal(principalID domain.ID, displayName string, createdAt domain.Millis) error {
	return insertPrincipal(w.ctx, w.tx, principalID, displayName, createdAt)
}
func (w *writerTx) InsertNamespaceGrant(principalID, namespace domain.ID, permission domain.Permission) error {
	return insertNamespaceGrant(w.ctx, w.tx, principalID, namespace, permission)
}
func (w *writerTx) InsertAuthSession(rec store.AuthSessionRecord) error {
	return insertAuthSession(w.ctx, w.tx, rec)
}
func (w *writerTx) ExchangeBootstrap(bootstrapHash string, session store.AuthSessionRecord, at domain.Millis) error {
	return exchangeBootstrap(w.ctx, w.tx, bootstrapHash, session, at)
}
func (w *writerTx) RevokeAuthSession(tokenHash string, at domain.Millis) error {
	return revokeAuthSession(w.ctx, w.tx, tokenHash, at)
}

func (r *readerTx) GetConfigRevision(revision domain.ID) (store.ConfigRevisionRecord, bool, error) {
	return getConfigRevision(r.ctx, r.tx, revision)
}
func (r *readerTx) GetReceipt(scope, commandID domain.ID) (store.ReceiptRecord, bool, error) {
	return getReceipt(r.ctx, r.tx, scope, commandID)
}
func (r *readerTx) GetActiveConfig() (store.ActiveConfigRecord, bool, error) {
	return getActiveConfig(r.ctx, r.tx)
}
func (r *readerTx) GetConfigDraft(draftID domain.ID) (store.ConfigDraftRecord, bool, error) {
	return getConfigDraft(r.ctx, r.tx, draftID)
}
func (r *readerTx) GetSecretReference(ref domain.ID) (store.SecretReferenceRecord, bool, error) {
	return getSecretReference(r.ctx, r.tx, ref)
}
func (r *readerTx) GetSecretRevision(ref, revision domain.ID) (store.SecretRevisionRecord, bool, error) {
	return getSecretRevision(r.ctx, r.tx, ref, revision)
}
func (r *readerTx) GetPool(poolID domain.ID) (store.PoolRecord, bool, error) {
	return getPool(r.ctx, r.tx, poolID)
}
func (r *readerTx) ListPoolMembers(poolID domain.ID) ([]store.PoolMemberRecord, error) {
	return listPoolMembers(r.ctx, r.tx, poolID)
}
func (r *readerTx) GetOffer(offerID domain.ID) (store.OfferRecord, bool, error) {
	return getOffer(r.ctx, r.tx, offerID)
}
func (r *readerTx) ListPoolsByNamespace(namespace domain.ID) ([]store.PoolRecord, error) {
	return listPoolsByNamespace(r.ctx, r.tx, namespace)
}
func (r *readerTx) LookupAuthSession(tokenHash string, now domain.Millis) (store.AuthSessionRecord, bool, error) {
	return lookupAuthSession(r.ctx, r.tx, tokenHash, now)
}
