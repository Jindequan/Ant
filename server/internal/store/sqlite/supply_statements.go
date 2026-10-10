package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ant/server/internal/domain"
	"ant/server/internal/store"

	drv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Supply store statements (design 11 V0.2.1 §2.4, migration 006).

var (
	_ store.SupplyReaderTx = (*readerTx)(nil)
	_ store.SupplyWriterTx = (*writerTx)(nil)
)

func nullableID(v domain.ID) any {
	if v == "" {
		return nil
	}
	return string(v)
}

func nullableUnits(v *domain.Units) any {
	if v == nil {
		return nil
	}
	return int64(*v)
}

func nullableBool(v *bool) any {
	if v == nil {
		return nil
	}
	if *v {
		return int64(1)
	}
	return int64(0)
}

func millisPtr(v sql.NullInt64) *domain.Millis {
	if !v.Valid {
		return nil
	}
	m := domain.Millis(v.Int64)
	return &m
}

func unitsPtr(v sql.NullInt64) *domain.Units {
	if !v.Valid {
		return nil
	}
	u := domain.Units(v.Int64)
	return &u
}

func (w *writerTx) EnsureLocalOwner(principalID domain.ID, displayName string, at domain.Millis) error {
	_, err := w.tx.ExecContext(w.ctx, `INSERT INTO principals(principal_id,display_name,enabled,created_at) VALUES(?,?,1,?) ON CONFLICT(principal_id) DO NOTHING`,
		string(principalID), displayName, int64(at))
	return err
}

func (w *writerTx) UpsertSupplyCredential(r store.SupplyCredentialRecord) error {
	result, err := w.tx.ExecContext(w.ctx, `INSERT INTO supply_credentials(namespace,credential_id,provider_id,kind,origin,strategy,locator_json,secret_ref,account_id,status,status_detail,verified_at,created_at)
VALUES(?,?,?,?,?,?,CAST(? AS TEXT),?,?,?,?,?,?) ON CONFLICT(namespace,credential_id) DO UPDATE SET
 kind=excluded.kind, strategy=excluded.strategy, locator_json=excluded.locator_json, secret_ref=excluded.secret_ref,
 account_id=excluded.account_id, status=excluded.status, status_detail=excluded.status_detail, verified_at=excluded.verified_at
 WHERE supply_credentials.provider_id=excluded.provider_id AND supply_credentials.origin=excluded.origin`,
		string(r.Namespace), string(r.CredentialID), string(r.ProviderID), r.Kind, r.Origin, r.Strategy, jsonParam(r.LocatorJSON),
		nullableID(r.SecretRef), nullableID(r.AccountID), r.Status, r.StatusDetail, nullableMillis(r.VerifiedAt), int64(r.CreatedAt))
	return identityApplied(result, err) // a credential id never changes provider or origin
}

func listSupplyCredentials(ctx context.Context, q queryer, ns domain.ID) ([]store.SupplyCredentialRecord, error) {
	rows, err := q.QueryContext(ctx, `SELECT namespace,credential_id,provider_id,kind,origin,strategy,locator_json,COALESCE(secret_ref,''),COALESCE(account_id,''),status,status_detail,verified_at,created_at,paused
FROM supply_credentials WHERE namespace=? ORDER BY provider_id,credential_id`, string(ns))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SupplyCredentialRecord
	for rows.Next() {
		var r store.SupplyCredentialRecord
		var locator string
		var verified sql.NullInt64
		var paused int64
		if err := rows.Scan(&r.Namespace, &r.CredentialID, &r.ProviderID, &r.Kind, &r.Origin, &r.Strategy, &locator, &r.SecretRef, &r.AccountID, &r.Status, &r.StatusDetail, &verified, &r.CreatedAt, &paused); err != nil {
			return nil, err
		}
		r.LocatorJSON, r.VerifiedAt, r.Paused = []byte(locator), millisPtr(verified), paused == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

func (r *readerTx) ListSupplyCredentials(ns domain.ID) ([]store.SupplyCredentialRecord, error) {
	return listSupplyCredentials(r.ctx, r.tx, ns)
}

func (w *writerTx) UpsertSupplyAccount(r store.SupplyAccountRecord) (domain.ID, error) {
	_, err := w.tx.ExecContext(w.ctx, `INSERT INTO supply_accounts(namespace,account_id,provider_id,provider_identity,display_name,updated_at)
VALUES(?,?,?,?,?,?) ON CONFLICT(namespace,provider_id,provider_identity) DO UPDATE SET display_name=excluded.display_name, updated_at=excluded.updated_at`,
		string(r.Namespace), string(r.AccountID), string(r.ProviderID), r.ProviderIdentity, r.DisplayName, int64(r.UpdatedAt))
	if err != nil {
		return "", supplyError(err)
	}
	var id string
	err = w.tx.QueryRowContext(w.ctx, `SELECT account_id FROM supply_accounts WHERE namespace=? AND provider_id=? AND provider_identity=?`,
		string(r.Namespace), string(r.ProviderID), r.ProviderIdentity).Scan(&id)
	return domain.ID(id), err
}

func (w *writerTx) UpsertSupplyPackage(r store.SupplyPackageRecord) error {
	result, err := w.tx.ExecContext(w.ctx, `INSERT INTO supply_packages(namespace,package_id,account_id,template_ref,kind,basis,plan,starts_at,renews_at,ends_at,auto_renew,live,definition_json,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,CAST(? AS TEXT),?) ON CONFLICT(namespace,package_id) DO UPDATE SET
 plan=excluded.plan, starts_at=excluded.starts_at, renews_at=excluded.renews_at, ends_at=excluded.ends_at, auto_renew=excluded.auto_renew,
 live=excluded.live, definition_json=excluded.definition_json, updated_at=excluded.updated_at
 WHERE supply_packages.account_id=excluded.account_id AND supply_packages.template_ref=excluded.template_ref
   AND supply_packages.kind=excluded.kind AND supply_packages.basis=excluded.basis`,
		string(r.Namespace), string(r.PackageID), string(r.AccountID), r.TemplateRef, r.Kind, r.Basis, r.Plan,
		nullableMillis(r.StartsAt), nullableMillis(r.RenewsAt), nullableMillis(r.EndsAt), nullableBool(r.AutoRenew), boolInt(r.Live),
		jsonParam(r.DefinitionJSON), int64(r.UpdatedAt))
	return identityApplied(result, err)
}

func (w *writerTx) UpsertSupplyResource(r store.SupplyResourceRecord) error {
	result, err := w.tx.ExecContext(w.ctx, `INSERT INTO supply_resources(namespace,resource_id,physical_key,package_id,template_ref,metric_id,unit,quantum_exp,currency,semantics_ref,semantics_revision,window_json,scope_json,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,CAST(? AS TEXT),CAST(? AS TEXT),?) ON CONFLICT(namespace,resource_id) DO UPDATE SET
 semantics_revision=excluded.semantics_revision, updated_at=excluded.updated_at
 WHERE supply_resources.physical_key=excluded.physical_key AND supply_resources.package_id=excluded.package_id
   AND supply_resources.metric_id=excluded.metric_id AND supply_resources.unit=excluded.unit
   AND supply_resources.quantum_exp=excluded.quantum_exp AND supply_resources.currency=excluded.currency
   AND supply_resources.template_ref=excluded.template_ref AND supply_resources.semantics_ref=excluded.semantics_ref
   AND supply_resources.window_json=excluded.window_json AND supply_resources.scope_json=excluded.scope_json`,
		string(r.Namespace), string(r.ResourceID), string(r.PhysicalKey), string(r.PackageID), r.TemplateRef, string(r.MetricID), r.Unit,
		r.QuantumExp, r.Currency, string(r.SemanticsRef), string(r.SemanticsRevision), jsonParam(r.WindowJSON), jsonParam(r.ScopeJSON), int64(r.UpdatedAt))
	return identityApplied(result, err)
}

// supplyError maps the constraint failures a supply write can hit to typed
// errors; CHECK failures (invalid enum or shape) stay driver errors because
// they are programming errors, not data conflicts.
func supplyError(err error) error {
	var serr *drv.Error
	if err == nil || !errors.As(err, &serr) {
		return err
	}
	switch serr.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
		return fmt.Errorf("%w: %v", store.ErrSupplyIdentityConflict, err)
	case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
		return fmt.Errorf("%w: %v", store.ErrSupplyMissingReference, err)
	}
	return err
}

// identityApplied turns an upsert whose identity guard refused the update
// (zero rows affected) into ErrSupplyIdentityConflict.
func identityApplied(result sql.Result, err error) error {
	if err != nil {
		return supplyError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return store.ErrSupplyIdentityConflict
	}
	return nil
}

func listSupplyResources(ctx context.Context, q queryer, ns domain.ID) ([]store.SupplyResourceRecord, error) {
	rows, err := q.QueryContext(ctx, `SELECT namespace,resource_id,physical_key,package_id,template_ref,metric_id,unit,quantum_exp,currency,semantics_ref,semantics_revision,window_json,scope_json,updated_at
FROM supply_resources WHERE namespace=? ORDER BY package_id,resource_id`, string(ns))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SupplyResourceRecord
	for rows.Next() {
		var r store.SupplyResourceRecord
		var window, scope string
		if err := rows.Scan(&r.Namespace, &r.ResourceID, &r.PhysicalKey, &r.PackageID, &r.TemplateRef, &r.MetricID, &r.Unit, &r.QuantumExp, &r.Currency, &r.SemanticsRef, &r.SemanticsRevision, &window, &scope, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.WindowJSON, r.ScopeJSON = []byte(window), []byte(scope)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (r *readerTx) ListSupplyResources(ns domain.ID) ([]store.SupplyResourceRecord, error) {
	return listSupplyResources(r.ctx, r.tx, ns)
}

func (w *writerTx) StartCollectorRun(r store.CollectorRunRecord) error {
	_, err := w.tx.ExecContext(w.ctx, `INSERT INTO collector_runs(namespace,run_id,credential_id,capability,started_at,detail) VALUES(?,?,?,?,?,?)`,
		string(r.Namespace), string(r.RunID), string(r.CredentialID), r.Capability, int64(r.StartedAt), r.Detail)
	return supplyError(err)
}

func (w *writerTx) FinishCollectorRun(ns, runID domain.ID, finishedAt domain.Millis, outcome, detail string) error {
	result, err := w.tx.ExecContext(w.ctx, `UPDATE collector_runs SET finished_at=?, outcome=?, detail=? WHERE namespace=? AND run_id=? AND finished_at IS NULL AND started_at<=?`,
		int64(finishedAt), outcome, detail, string(ns), string(runID), int64(finishedAt))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return store.ErrCollectorRunNotOpen
	}
	return nil
}

const supplyReadingColumns = `namespace,reading_id,resource_id,observed_at,window_start,window_end,reset_basis,limit_units,used_units,remaining_units,run_id,evidence_json`

func scanSupplyReading(row interface{ Scan(...any) error }) (store.SupplyReadingRecord, error) {
	var r store.SupplyReadingRecord
	var start, end, limit, used, remaining sql.NullInt64
	var evidence string
	err := row.Scan(&r.Namespace, &r.ReadingID, &r.ResourceID, &r.ObservedAt, &start, &end, &r.ResetBasis, &limit, &used, &remaining, &r.RunID, &evidence)
	r.WindowStart, r.WindowEnd = millisPtr(start), millisPtr(end)
	r.Limit, r.Used, r.Remaining = unitsPtr(limit), unitsPtr(used), unitsPtr(remaining)
	r.EvidenceJSON = []byte(evidence)
	return r, err
}

// latestSupplyReading needs no tiebreaker: the append rule makes observed_at
// unique per resource, so the index alone orders the result.
func latestSupplyReading(ctx context.Context, q queryer, ns, resourceID domain.ID) (store.SupplyReadingRecord, bool, error) {
	r, err := scanSupplyReading(q.QueryRowContext(ctx, `SELECT `+supplyReadingColumns+` FROM supply_readings
WHERE namespace=? AND resource_id=? ORDER BY observed_at DESC LIMIT 1`, string(ns), string(resourceID)))
	if errors.Is(err, sql.ErrNoRows) {
		return store.SupplyReadingRecord{}, false, nil
	}
	return r, err == nil, err
}

func (r *readerTx) LatestSupplyReading(ns, resourceID domain.ID) (store.SupplyReadingRecord, bool, error) {
	return latestSupplyReading(r.ctx, r.tx, ns, resourceID)
}

func (w *writerTx) LatestSupplyReading(ns, resourceID domain.ID) (store.SupplyReadingRecord, bool, error) {
	return latestSupplyReading(w.ctx, w.tx, ns, resourceID)
}

func (r *readerTx) GetSupplyReading(ns, readingID domain.ID) (store.SupplyReadingRecord, bool, error) {
	rec, err := scanSupplyReading(r.tx.QueryRowContext(r.ctx, `SELECT `+supplyReadingColumns+` FROM supply_readings WHERE namespace=? AND reading_id=?`,
		string(ns), string(readingID)))
	if errors.Is(err, sql.ErrNoRows) {
		return store.SupplyReadingRecord{}, false, nil
	}
	return rec, err == nil, err
}

func (r *readerTx) ListSupplyPackages(ns domain.ID) ([]store.SupplyPackageRecord, error) {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT namespace,package_id,account_id,template_ref,kind,basis,plan,starts_at,renews_at,ends_at,auto_renew,live,definition_json,updated_at
FROM supply_packages WHERE namespace=? ORDER BY account_id,package_id`, string(ns))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SupplyPackageRecord
	for rows.Next() {
		var p store.SupplyPackageRecord
		var starts, renews, ends, autoRenew sql.NullInt64
		var live int64
		var def string
		if err := rows.Scan(&p.Namespace, &p.PackageID, &p.AccountID, &p.TemplateRef, &p.Kind, &p.Basis, &p.Plan, &starts, &renews, &ends, &autoRenew, &live, &def, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.StartsAt, p.RenewsAt, p.EndsAt, p.Live, p.DefinitionJSON = millisPtr(starts), millisPtr(renews), millisPtr(ends), live == 1, []byte(def)
		if autoRenew.Valid {
			b := autoRenew.Int64 == 1
			p.AutoRenew = &b
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *readerTx) ListSupplyReadings(ns, resourceID domain.ID, since domain.Millis) ([]store.SupplyReadingRecord, error) {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT `+supplyReadingColumns+` FROM supply_readings
WHERE namespace=? AND resource_id=? AND observed_at>=? ORDER BY observed_at`, string(ns), string(resourceID), int64(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SupplyReadingRecord
	for rows.Next() {
		rec, err := scanSupplyReading(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *readerTx) ListSupplyOffers(ns domain.ID, modelID string) ([]store.SupplyOfferRecord, error) {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT namespace,offer_id,credential_id,endpoint,protocol,upstream_model,model_id,draws_json,verified_at
FROM supply_offers WHERE namespace=? AND model_id=? ORDER BY credential_id,endpoint`, string(ns), modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SupplyOfferRecord
	for rows.Next() {
		var o store.SupplyOfferRecord
		var draws string
		if err := rows.Scan(&o.Namespace, &o.OfferID, &o.CredentialID, &o.Endpoint, &o.Protocol, &o.UpstreamModel, &o.ModelID, &draws, &o.VerifiedAt); err != nil {
			return nil, err
		}
		o.DrawsJSON = []byte(draws)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *readerTx) GetSupplyRouteOrder(ns domain.ID, modelID string) (store.SupplyRouteOrderRecord, bool, error) {
	var rec store.SupplyRouteOrderRecord
	var raw string
	err := r.tx.QueryRowContext(r.ctx, `SELECT namespace,model_id,revision,offer_ids_json,updated_at
FROM supply_route_orders WHERE namespace=? AND model_id=?`, string(ns), modelID).
		Scan(&rec.Namespace, &rec.ModelID, &rec.Revision, &raw, &rec.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.SupplyRouteOrderRecord{Namespace: ns, ModelID: modelID, OfferIDs: []domain.ID{}}, false, nil
	}
	if err != nil {
		return store.SupplyRouteOrderRecord{}, false, err
	}
	if err := json.Unmarshal([]byte(raw), &rec.OfferIDs); err != nil {
		return store.SupplyRouteOrderRecord{}, false, err
	}
	if rec.OfferIDs == nil {
		rec.OfferIDs = []domain.ID{}
	}
	return rec, true, nil
}

func (w *writerTx) ReplaceSupplyRouteOrder(ns domain.ID, modelID string, offerIDs []domain.ID, expectedRevision int64, at domain.Millis) (int64, error) {
	if ns == "" || modelID == "" || len(modelID) > 256 || expectedRevision < 0 {
		return 0, store.ErrSupplyRouteOrderInvalid
	}
	seen := make(map[domain.ID]bool, len(offerIDs))
	for _, id := range offerIDs {
		if id == "" || seen[id] {
			return 0, store.ErrSupplyRouteOrderInvalid
		}
		seen[id] = true
	}
	var current int64
	err := w.tx.QueryRowContext(w.ctx, `SELECT revision FROM supply_route_orders WHERE namespace=? AND model_id=?`, string(ns), modelID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		current = 0
	} else if err != nil {
		return 0, err
	}
	if current != expectedRevision {
		return current, store.ErrSupplyRouteOrderConflict
	}
	raw, err := json.Marshal(offerIDs)
	if err != nil {
		return 0, err
	}
	next := current + 1
	_, err = w.tx.ExecContext(w.ctx, `INSERT INTO supply_route_orders(namespace,model_id,revision,offer_ids_json,updated_at)
VALUES(?,?,?,?,?) ON CONFLICT(namespace,model_id) DO UPDATE SET revision=excluded.revision,offer_ids_json=excluded.offer_ids_json,updated_at=excluded.updated_at`,
		string(ns), modelID, next, string(raw), int64(at))
	if err != nil {
		return 0, err
	}
	return next, nil
}

func (w *writerTx) ReplaceSupplyOffers(ns, credentialID domain.ID, offers []store.SupplyOfferRecord) error {
	if _, err := w.tx.ExecContext(w.ctx, `DELETE FROM supply_offers WHERE namespace=? AND credential_id=?`, string(ns), string(credentialID)); err != nil {
		return err
	}
	for _, o := range offers {
		if o.Namespace != ns || o.CredentialID != credentialID {
			return store.ErrSupplyIdentityConflict
		}
		_, err := w.tx.ExecContext(w.ctx, `INSERT INTO supply_offers(namespace,offer_id,credential_id,endpoint,protocol,upstream_model,model_id,draws_json,verified_at)
VALUES(?,?,?,?,?,?,?,CAST(? AS TEXT),?)`, string(o.Namespace), string(o.OfferID), string(o.CredentialID), o.Endpoint, o.Protocol, o.UpstreamModel, o.ModelID, jsonParam(o.DrawsJSON), int64(o.VerifiedAt))
		if err != nil {
			return supplyError(err)
		}
	}
	return nil
}

func (r *readerTx) ListSupplyCards(ns domain.ID) ([]store.SupplyCardRecord, error) {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT namespace,credential_id,card_json,read_at FROM supply_cards WHERE namespace=? ORDER BY credential_id`, string(ns))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.SupplyCardRecord
	for rows.Next() {
		var c store.SupplyCardRecord
		var card string
		if err := rows.Scan(&c.Namespace, &c.CredentialID, &card, &c.ReadAt); err != nil {
			return nil, err
		}
		c.CardJSON = []byte(card)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (w *writerTx) PutSupplyCard(c store.SupplyCardRecord) error {
	_, err := w.tx.ExecContext(w.ctx, `INSERT INTO supply_cards(namespace,credential_id,card_json,read_at) VALUES(?,?,CAST(? AS TEXT),?)
ON CONFLICT(namespace,credential_id) DO UPDATE SET card_json=excluded.card_json, read_at=excluded.read_at`,
		string(c.Namespace), string(c.CredentialID), jsonParam(c.CardJSON), int64(c.ReadAt))
	return supplyError(err)
}

func (w *writerTx) DeleteSupplyCard(ns, credentialID domain.ID) error {
	_, err := w.tx.ExecContext(w.ctx, `DELETE FROM supply_cards WHERE namespace=? AND credential_id=?`, string(ns), string(credentialID))
	return err
}

func (w *writerTx) SetSupplyPaused(ns domain.ID, credentialIDs []domain.ID, paused bool) (int, error) {
	n := 0
	for _, id := range credentialIDs {
		result, err := w.tx.ExecContext(w.ctx, `UPDATE supply_credentials SET paused=? WHERE namespace=? AND credential_id=?`, boolInt(paused), string(ns), string(id))
		if err != nil {
			return n, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return n, err
		}
		n += int(changed)
	}
	return n, nil
}

func (w *writerTx) AppendSupplyReadingIfChanged(r store.SupplyReadingRecord) (bool, error) {
	latest, found, err := latestSupplyReading(w.ctx, w.tx, r.Namespace, r.ResourceID)
	if err != nil {
		return false, err
	}
	if found {
		if r.ObservedAt <= latest.ObservedAt {
			return false, store.ErrSupplyReadingOutOfOrder
		}
		if sameReadingValue(latest, r) {
			return false, nil
		}
	}
	_, err = w.tx.ExecContext(w.ctx, `INSERT INTO supply_readings(`+supplyReadingColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,CAST(? AS TEXT))`,
		string(r.Namespace), string(r.ReadingID), string(r.ResourceID), int64(r.ObservedAt), nullableMillis(r.WindowStart), nullableMillis(r.WindowEnd),
		r.ResetBasis, nullableUnits(r.Limit), nullableUnits(r.Used), nullableUnits(r.Remaining), string(r.RunID), jsonParam(r.EvidenceJSON))
	if err != nil {
		return false, supplyError(err)
	}
	return true, nil
}

// sameReadingValue compares what a reading says, not when or how it was read
// (observed_at, run and evidence differ on every read).
func sameReadingValue(a, b store.SupplyReadingRecord) bool {
	return eqMillis(a.WindowStart, b.WindowStart) && eqMillis(a.WindowEnd, b.WindowEnd) && a.ResetBasis == b.ResetBasis &&
		eqUnits(a.Limit, b.Limit) && eqUnits(a.Used, b.Used) && eqUnits(a.Remaining, b.Remaining)
}

func eqMillis(a, b *domain.Millis) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
func eqUnits(a, b *domain.Units) bool   { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
