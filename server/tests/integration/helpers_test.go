// Package integration runs the sqlite store against real on-disk databases
// (one file per test under t.TempDir()): migrations, triggers, unique/CAS
// guards, the K1/K4 flows and the writer-queue semantics. Standard library
// testing only.
package integration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite" // raw database/sql access for schema-level assertions

	"ant/server/internal/domain"
	"ant/server/internal/runtime"
	"ant/server/internal/store"
	"ant/server/internal/store/sqlite"
)

const validConfigFixture = "../../../contracts/fixtures/config-document-valid.json"

// fixtureCommandBudget is an anti-hang guard, not a graded threshold: tests
// here are not about the 50ms product budget, which -race builds overrun
// under load (see sqlite.Config.CommandBudget). Budget tests set their own.
const fixtureCommandBudget = 10 * time.Second

func testConfig() sqlite.Config {
	return sqlite.Config{Clock: runtime.NewClock(), CommandBudget: fixtureCommandBudget}
}

// openTestStore opens a store on a fresh file database and migrates it. An
// unset CommandBudget gets the fixture budget.
func openTestStore(t *testing.T, cfg sqlite.Config) (store.Store, string) {
	t.Helper()
	if cfg.Clock == nil {
		cfg.Clock = runtime.NewClock()
	}
	if cfg.CommandBudget == 0 {
		cfg.CommandBudget = fixtureCommandBudget
	}
	path := filepath.Join(t.TempDir(), "ant.db")
	st, err := sqlite.Open(path, cfg)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	return st, path
}

// openRawDB opens an independent foreign_keys-enabled connection to the same
// file for schema-level assertions and manual seeding.
func openRawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_busy_timeout=5000&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func execOK(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %s: %v", oneLine(query), err)
	}
}

// execErr asserts the statement fails and returns the error for message
// checks (constraint/trigger violations).
func execErr(t *testing.T, db *sql.DB, query string, args ...any) error {
	t.Helper()
	_, err := db.Exec(query, args...)
	if err == nil {
		t.Fatalf("exec %s: expected an error, got nil", oneLine(query))
	}
	return err
}

func queryInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("query %s: %v", oneLine(query), err)
	}
	return n
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return b
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// seedCatalog inserts the minimal FK skeleton: account, binding, the given
// offers, the pool at the given version, and a seed config revision.
func seedCatalog(t *testing.T, db *sql.DB, namespace, poolID domain.ID, poolVersion int64, offers ...domain.ID) {
	t.Helper()
	execOK(t, db, `INSERT INTO accounts(account_id, provider_owner_ref, enabled, version, definition_json)
		VALUES('acct-1', 'provider-alpha', 1, 1, '{}')`)
	execOK(t, db, `INSERT INTO bindings(binding_id, account_id, adapter_ref, domain, endpoint, credential_ref, credential_revision, enabled, version, definition_json)
		VALUES('bind-1', 'acct-1', 'adapter-alpha', 'ai/text', 'https://fixture.invalid/v1', 'secret-ref', 'rev-1', 1, 1, '{}')`)
	for _, o := range offers {
		execOK(t, db, `INSERT INTO resource_offers(offer_id, binding_id, domain, capability_profile, enabled, version, definition_json)
			VALUES(?, 'bind-1', 'ai/text', 'cap-profile', 1, 1, '{}')`, string(o))
	}
	execOK(t, db, `INSERT INTO resource_pools(pool_id, namespace, name, enabled, version, definition_json)
		VALUES(?, ?, 'pool-1', 1, ?, '{}')`, string(poolID), string(namespace), poolVersion)
	execOK(t, db, `INSERT INTO config_revisions(revision, definition_json, created_at)
		VALUES('rev-seed', '{}', 1)`)
}

func seedOperation(t *testing.T, db *sql.DB, namespace, opID, poolID domain.ID) {
	t.Helper()
	execOK(t, db, `INSERT INTO operations(namespace, operation_id, pool_id, fingerprint, state, runtime_id, owner_id, generation, lease_until, deadline_at, max_attempts, strategy_json, created_at, updated_at)
		VALUES(?, ?, ?, 'fp-1', 'running', 'rt-1', 'owner-1', 1, 1000, 2000, 3, '{}', 1, 1)`,
		string(namespace), string(opID), string(poolID))
}

func seedPlan(t *testing.T, db *sql.DB) {
	t.Helper()
	execOK(t, db, `INSERT INTO execution_plans(plan_id, config_revision, plan_json, plan_hash)
		VALUES('plan-1', 'rev-seed', '{}', 'hash-1')`)
}

func insertAttempt(t *testing.T, db *sql.DB, attemptID, namespace, opID domain.ID, phase string) {
	t.Helper()
	execOK(t, db, `INSERT INTO attempts(attempt_id, namespace, operation_id, binding_id, account_id, offer_id, plan_id, generation, request_digest, guard_refs_json, phase, lease_until, created_at, updated_at)
		VALUES(?, ?, ?, 'bind-1', 'acct-1', 'of-1', 'plan-1', 1, 'digest-1', '{}', ?, 1000, 1, 1)`,
		string(attemptID), string(namespace), string(opID), phase)
}

// queuedCtx returns an already-canceled context: SubmitWrite against it
// enqueues the command and returns immediately (context.Canceled), which
// makes queue-capacity tests deterministic.
func queuedCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
