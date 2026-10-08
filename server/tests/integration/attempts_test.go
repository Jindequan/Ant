package integration

import (
	"strings"
	"testing"
)

func TestSingleActiveAttemptPerOperation(t *testing.T) {
	_, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 1, "of-1")
	seedOperation(t, db, "ns-1", "op-1", "pool-1")
	seedPlan(t, db)

	insertAttempt(t, db, "att-1", "ns-1", "op-1", "prepared")

	// The partial unique index attempts_one_active admits only one
	// prepared/executing attempt per (namespace, operation_id).
	dupErr := execErr(t, db, "INSERT INTO attempts(attempt_id, namespace, operation_id, binding_id, account_id, offer_id, plan_id, generation, request_digest, guard_refs_json, phase, lease_until, created_at, updated_at) VALUES('att-2', 'ns-1', 'op-1', 'bind-1', 'acct-1', 'of-1', 'plan-1', 1, 'digest-2', '{}', 'prepared', 1000, 2, 2)")
	if !strings.Contains(dupErr.Error(), "UNIQUE") {
		t.Fatalf("want UNIQUE violation from attempts_one_active, got: %v", dupErr)
	}

	// A terminal attempt frees the slot.
	execOK(t, db, `UPDATE attempts SET phase = 'terminal', external_outcome = 'known_success',
		report_json = '{"ok":true}', report_hash = 'rhash-1' WHERE attempt_id = 'att-1'`)
	insertAttempt(t, db, "att-3", "ns-1", "op-1", "prepared")
}

func TestPermitGrantConsumeCAS(t *testing.T) {
	_, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 1, "of-1")
	seedOperation(t, db, "ns-1", "op-1", "pool-1")
	seedPlan(t, db)
	insertAttempt(t, db, "att-1", "ns-1", "op-1", "prepared")
	execOK(t, db, `INSERT INTO dispatch_permits(permit_id, attempt_id, runtime_id, generation, control_version, credential_revision, expires_at, request_digest, status, granted_at)
		VALUES('p-1', 'att-1', 'rt-1', 1, 1, 'rev-1', 10000, 'digest-1', 'granted', 1)`)

	// First consume-permit wins the CAS granted → consumed.
	res, err := db.Exec(`UPDATE dispatch_permits SET status = 'consumed', consumed_at = 2
		WHERE permit_id = 'p-1' AND status = 'granted'`)
	if err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("first consume changed %d rows, want 1", n)
	}

	// The send right is single-use: the second CAS matches nothing.
	res2, err := db.Exec(`UPDATE dispatch_permits SET status = 'consumed', consumed_at = 3
		WHERE permit_id = 'p-1' AND status = 'granted'`)
	if err != nil {
		t.Fatalf("second consume: %v", err)
	}
	if n, _ := res2.RowsAffected(); n != 0 {
		t.Fatalf("second consume changed %d rows, want 0", n)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM dispatch_permits WHERE permit_id = 'p-1' AND status = 'consumed' AND consumed_at = 2"); got != 1 {
		t.Fatalf("permit state was mutated by the second CAS: %d matching rows", got)
	}
}
