package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"ant/server/internal/domain"
)

func (w *writerTx) ApplyReconciliationDelta(d domain.ResourceDelta, attempt, scope, command domain.ID, refs []domain.ID, now domain.Millis) (bool, error) {
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
	_, err = w.tx.ExecContext(w.ctx, `INSERT INTO resource_events(event_id,command_scope,command_id,resource_id,window_key,attempt_id,event_kind,committed_delta,adjustment_delta,reserved_delta,active_delta,uncertain_delta,charged_at,evidence_json,recorded_at) VALUES(?,?,?,?,?,?,'reconcile',?,?,?,?,?,NULL,CAST(? AS TEXT),?)`, string(scope)+"/"+string(command)+"/"+string(d.ResourceID)+"/"+string(d.WindowKey), string(scope), string(command), string(d.ResourceID), string(d.WindowKey), string(attempt), int64(d.Committed), int64(d.Adjustment), int64(d.Reserved), int64(d.Active), int64(d.Uncertain), raw, int64(now))
	return err == nil, err
}

func (w *writerTx) GetLatestReconciliationCorrections(attempt domain.ID) ([]domain.ResourceOutcome, bool, error) {
	var raw []byte
	err := w.tx.QueryRowContext(w.ctx, `SELECT correction_json FROM reconciliations WHERE attempt_id=? AND correction_json<>'[]' ORDER BY rowid DESC LIMIT 1`, string(attempt)).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var out []domain.ResourceOutcome
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func (w *writerTx) ReleaseReconciledOccupancy(attempt, resource domain.ID, units domain.Units, refs []domain.ID, now domain.Millis) (bool, error) {
	var id string
	var evidenceJSON []byte
	err := w.tx.QueryRowContext(w.ctx, `SELECT encumbrance_id,evidence_json FROM encumbrances WHERE attempt_id=? AND resource_id=? AND kind='occupancy' AND units=? AND status='open'`, string(attempt), string(resource), int64(units)).Scan(&id, &evidenceJSON)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	evidenceJSON, err = appendEvidenceRefs(evidenceJSON, refs)
	if err != nil {
		return false, err
	}
	res, err := w.tx.ExecContext(w.ctx, `UPDATE encumbrances SET status='released',released_at=?,evidence_json=CAST(? AS TEXT) WHERE encumbrance_id=? AND status='open'`, int64(now), evidenceJSON, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (w *writerTx) InsertReconciliationHold(attempt, resource, command domain.ID, reason string, refs []domain.ID, now domain.Millis) error {
	raw, err := json.Marshal(refs)
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, `INSERT INTO holds(hold_id,scope_kind,scope_id,reason,status,evidence_json,created_at) VALUES(?,'resource',?,?,'open',CAST(? AS TEXT),?)`, "reconcile/"+string(attempt)+"/"+string(command)+"/"+string(resource)+"/"+reason, string(resource), reason, raw, int64(now))
	return err
}

func (w *writerTx) ReleaseReconciledAttribution(attempt, resource domain.ID, refs []domain.ID, now domain.Millis) (bool, error) {
	var id string
	var evidenceJSON []byte
	err := w.tx.QueryRowContext(w.ctx, `SELECT encumbrance_id,evidence_json FROM encumbrances WHERE attempt_id=? AND resource_id=? AND kind='window_attribution' AND status='open'`, string(attempt), string(resource)).Scan(&id, &evidenceJSON)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	evidenceJSON, err = appendEvidenceRefs(evidenceJSON, refs)
	if err != nil {
		return false, err
	}
	res, err := w.tx.ExecContext(w.ctx, `UPDATE encumbrances SET status='released',released_at=?,evidence_json=CAST(? AS TEXT) WHERE encumbrance_id=? AND status='open'`, int64(now), evidenceJSON, id)
	if err != nil {
		return false, err
	}
	changed, err := res.RowsAffected()
	if err != nil || changed != 1 {
		return false, err
	}
	holdID := "settle/" + string(attempt) + "/" + string(resource) + "/window_attribution_unverified"
	var holdEvidenceJSON []byte
	if err = w.tx.QueryRowContext(w.ctx, `SELECT evidence_json FROM holds WHERE hold_id=? AND status='open'`, holdID).Scan(&holdEvidenceJSON); err != nil {
		return false, err
	}
	holdEvidenceJSON, err = appendEvidenceRefs(holdEvidenceJSON, refs)
	if err != nil {
		return false, err
	}
	hold, err := w.tx.ExecContext(w.ctx, `UPDATE holds SET status='cleared',cleared_at=?,evidence_json=CAST(? AS TEXT) WHERE hold_id=? AND status='open'`, int64(now), holdEvidenceJSON, holdID)
	if err != nil {
		return false, err
	}
	changed, err = hold.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed != 1 {
		return false, fmt.Errorf("reconciliation: open attribution encumbrance lacks its hold")
	}
	return true, nil
}

func (w *writerTx) ResolveReconciliationHolds(attempt, resource domain.ID, refs []domain.ID, now domain.Millis) error {
	rows, err := w.tx.QueryContext(w.ctx, `SELECT hold_id,evidence_json FROM holds WHERE scope_kind='resource' AND scope_id=? AND status='open'`, string(resource))
	if err != nil {
		return err
	}
	type holdFact struct {
		id       string
		evidence []byte
	}
	var targets []holdFact
	settlePrefix := "settle/" + string(attempt) + "/" + string(resource) + "/"
	reconcilePrefix := "reconcile/" + string(attempt) + "/"
	for rows.Next() {
		var fact holdFact
		if err = rows.Scan(&fact.id, &fact.evidence); err != nil {
			rows.Close()
			return err
		}
		if !strings.HasPrefix(fact.id, settlePrefix) && !strings.HasPrefix(fact.id, reconcilePrefix) {
			continue
		}
		if strings.HasSuffix(fact.id, "/actual_out_of_range") || strings.HasSuffix(fact.id, "/actual_exceeded_reservation") || strings.HasSuffix(fact.id, "/reconciled_usage_overrun") {
			targets = append(targets, fact)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, fact := range targets {
		evidence, e := appendEvidenceRefs(fact.evidence, refs)
		if e != nil {
			return e
		}
		res, e := w.tx.ExecContext(w.ctx, `UPDATE holds SET status='cleared',cleared_at=?,evidence_json=CAST(? AS TEXT) WHERE hold_id=? AND status='open'`, int64(now), evidence, fact.id)
		if e != nil {
			return e
		}
		if n, e := res.RowsAffected(); e != nil || n != 1 {
			return fmt.Errorf("reconciliation: accounting hold changed during clear")
		}
	}
	return nil
}

func appendEvidenceRefs(raw []byte, refs []domain.ID) ([]byte, error) {
	var evidence []domain.ID
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return nil, err
	}
	seen := make(map[domain.ID]bool, len(evidence)+len(refs))
	for _, id := range evidence {
		seen[id] = true
	}
	for _, id := range refs {
		if id != "" && !seen[id] {
			evidence = append(evidence, id)
			seen[id] = true
		}
	}
	return json.Marshal(evidence)
}

func (w *writerTx) InsertReconciliation(id, attempt, principal domain.ID, reportHash string, evidenceJSON, correctionJSON []byte, now domain.Millis) error {
	_, err := w.tx.ExecContext(w.ctx, `INSERT INTO reconciliations(reconciliation_id,attempt_id,principal_id,report_hash,evidence_json,correction_json,created_at) VALUES(?,?,?,?,CAST(? AS TEXT),CAST(? AS TEXT),?)`, string(id), string(attempt), string(principal), reportHash, evidenceJSON, correctionJSON, int64(now))
	return err
}
