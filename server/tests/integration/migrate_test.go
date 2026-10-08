package integration

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ant/server/internal/runtime"
	"ant/server/internal/store"
	"ant/server/internal/store/sqlite"
)

func TestMigrateFreshDatabaseCreates36Tables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ant.db")
	st, err := sqlite.Open(path, sqlite.Config{Clock: runtime.NewClock()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close(context.Background()) }()

	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db := openRawDB(t, path)
	got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if got != 36 {
		t.Fatalf("table count = %d, want 36", got)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ant.db")
	st, err := sqlite.Open(path, sqlite.Config{Clock: runtime.NewClock()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close(ctx) }()

	for i := 0; i < 2; i++ {
		if err := sqlite.Migrate(ctx, st); err != nil {
			t.Fatalf("migrate run %d: %v", i+1, err)
		}
	}
	db := openRawDB(t, path)
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations WHERE version = 2"); got != 1 {
		t.Fatalf("version rows for v2 = %d, want 1 (second Migrate must be a no-op)", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"); got != 36 {
		t.Fatalf("table count = %d, want 36", got)
	}
}

func TestMigrateConcurrentAppliesExactlyOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ant.db")
	const racers = 2
	stores := make([]store.Store, 0, racers)
	for i := 0; i < racers; i++ {
		st, err := sqlite.Open(path, sqlite.Config{Clock: runtime.NewClock()})
		if err != nil {
			t.Fatalf("open store %d: %v", i, err)
		}
		stores = append(stores, st)
		defer func() { _ = st.Close(ctx) }()
	}

	errs := make(chan error, racers)
	for _, st := range stores {
		go func(s store.Store) { errs <- sqlite.Migrate(ctx, s) }(st)
	}
	for i := 0; i < racers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
	db := openRawDB(t, path)
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations"); got != 4 {
		t.Fatalf("schema_migrations rows = %d, want exactly four migrations", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"); got != 36 {
		t.Fatalf("table count = %d, want 36", got)
	}
}

// TestMigrateRejectsFutureSchema pins R13: a database written by a newer
// schema version must be refused with an upgrade path and left untouched --
// the old binary must never interpret (or "migrate down") structures it does
// not know.
func TestMigrateRejectsFutureSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "future.db")

	// Hand-build a future version 4 database: only the version table with one v4
	// row, plus a marker table a v1 binary knows nothing about.
	future, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open future db: %v", err)
	}
	execOK(t, future, "CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL) STRICT")
	execOK(t, future, "INSERT INTO schema_migrations(version, applied_at) VALUES(5, 42)")
	execOK(t, future, "CREATE TABLE future_binary_table(x INTEGER)")
	if err := future.Close(); err != nil {
		t.Fatalf("close future db: %v", err)
	}

	st, err := sqlite.Open(path, sqlite.Config{Clock: runtime.NewClock()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close(context.Background()) }()

	migrateErr := sqlite.Migrate(ctx, st)
	if migrateErr == nil {
		t.Fatal("Migrate accepted a future schema version")
	}
	t.Logf("future schema err: %v", migrateErr)
	if !strings.Contains(migrateErr.Error(), "upgrade required") {
		t.Fatalf("error %q does not carry the upgrade path", migrateErr)
	}

	// Untouched: still exactly one v4 row, marker table intact, and no v1
	// DDL applied (exactly the two hand-built user tables remain).
	db := openRawDB(t, path)
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations"); got != 1 {
		t.Fatalf("schema_migrations rows = %d, want 1 (Migrate must not write to a future-version database)", got)
	}
	if got := queryInt(t, db, "SELECT MAX(version) FROM schema_migrations"); got != 5 {
		t.Fatalf("max schema version = %d, want 5 (no downgrade, no extra rows)", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"); got != 2 {
		t.Fatalf("user tables = %d, want 2 (schema_migrations + future_binary_table; no v1 DDL ran)", got)
	}
}

// TestMigrateRejectsFutureVersionOverExistingV4 verifies a v4 database that
// later gains a v5 row is refused, with existing data intact.
func TestMigrateRejectsFutureVersionOverExistingV4(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig()) // migrated to v4

	db := openRawDB(t, path)
	execOK(t, db, "INSERT INTO schema_migrations(version, applied_at) VALUES(5, 1)")

	err := sqlite.Migrate(ctx, st)
	if err == nil || !strings.Contains(err.Error(), "upgrade required") {
		t.Fatalf("Migrate over a v4-marked database = %v, want an upgrade-required refusal", err)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations"); got != 5 {
		t.Fatalf("schema_migrations rows = %d, want v1+v2+v3+v4+v5 rows (nothing added)", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"); got != 36 {
		t.Fatalf("table count = %d, want 36 (schema unchanged by the refusal)", got)
	}
}

func TestMigrateUpgradesVersionOneDatabase(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	execOK(t, db, "DROP INDEX secret_refs_owner")
	execOK(t, db, "DROP TABLE secret_refs")
	execOK(t, db, "ALTER TABLE decision_records DROP COLUMN trace_json")
	execOK(t, db, "DROP TABLE supply_inventory")
	execOK(t, db, "DELETE FROM schema_migrations WHERE version IN (2,3,4)")
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatalf("upgrade v1 to v4: %v", err)
	}
	if got := queryInt(t, db, "SELECT MAX(version) FROM schema_migrations"); got != 4 {
		t.Fatalf("schema version = %d, want 4", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='secret_refs'"); got != 1 {
		t.Fatalf("secret_refs table count = %d, want 1", got)
	}
}

func TestMigrateUpgradesVersionTwoDatabase(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	execOK(t, db, "ALTER TABLE decision_records DROP COLUMN trace_json")
	execOK(t, db, "DROP TABLE supply_inventory")
	execOK(t, db, "DELETE FROM schema_migrations WHERE version IN (3,4)")
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatalf("upgrade v2 to v4: %v", err)
	}
	if got := queryInt(t, db, "SELECT MAX(version) FROM schema_migrations"); got != 4 {
		t.Fatalf("schema version = %d, want 4", got)
	}
	columns := queryInt(t, db, `SELECT COUNT(*) FROM pragma_table_info('decision_records') WHERE name='trace_json'`)
	if columns != 1 {
		t.Fatalf("decision trace column count = %d, want 1", columns)
	}
}

func TestDecisionTraceMigrationCopyMatchesCanonicalFile(t *testing.T) {
	canonical, err := os.ReadFile("../../migrations/003_decision_trace.sql")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := os.ReadFile("../../internal/store/sqlite/003_decision_trace.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, embedded) {
		t.Fatal("embedded 003_decision_trace.sql diverges from canonical migration")
	}
}

func TestMigrateEmbeddedSchemaMatchesCanonicalFile(t *testing.T) {
	canon, err := os.ReadFile("../../migrations/001_initial.sql")
	if err != nil {
		t.Fatalf("read canonical migration: %v", err)
	}
	if !bytes.Equal(canon, []byte(sqlite.SchemaSQL())) {
		t.Fatal("embedded 001_initial.sql diverges from server/migrations/001_initial.sql; keep the byte-identical twins in sync")
	}
}

func TestSecretOwnershipMigrationCopyMatchesCanonicalFile(t *testing.T) {
	canonical, err := os.ReadFile("../../migrations/002_secret_ownership.sql")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := os.ReadFile("../../internal/store/sqlite/002_secret_ownership.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, embedded) {
		t.Fatal("embedded 002_secret_ownership.sql diverges from canonical migration")
	}
}

func TestForeignKeyConstraintEnforced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ant.db")
	st, err := sqlite.Open(path, sqlite.Config{Clock: runtime.NewClock()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close(context.Background()) }()
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	db := openRawDB(t, path)
	bindingErr := execErr(t, db, `INSERT INTO bindings(binding_id, account_id, adapter_ref, domain, endpoint, credential_ref, credential_revision, enabled, version, definition_json)
		VALUES('bind-x', 'missing-account', 'adapter', 'ai/text', 'https://fixture.invalid', 'cred', 'rev-1', 1, 1, '{}')`)
	if !strings.Contains(bindingErr.Error(), "FOREIGN KEY") {
		t.Fatalf("want FOREIGN KEY violation for dangling binding, got: %v", bindingErr)
	}
}

func TestStrictTablesRejectWrongTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ant.db")
	st, err := sqlite.Open(path, sqlite.Config{Clock: runtime.NewClock()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close(context.Background()) }()
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 1)
	// generation is INTEGER in a STRICT table: a TEXT value must be rejected
	// instead of being coerced.
	typeErr := execErr(t, db, `INSERT INTO operations(namespace, operation_id, pool_id, fingerprint, state, runtime_id, owner_id, generation, lease_until, deadline_at, max_attempts, strategy_json, created_at, updated_at)
		VALUES('ns-1', 'op-strict', 'pool-1', 'fp-1', 'running', 'rt-1', 'owner-1', 'not-a-number', 1000, 2000, 3, '{}', 1, 1)`)
	t.Logf("STRICT rejection message: %v", typeErr)
	if got := queryInt(t, db, "SELECT COUNT(*) FROM operations"); got != 0 {
		t.Fatalf("wrong-typed insert was stored (%d rows), want strict rejection", got)
	}
}

func TestSupplyInventoryMigrationPreservesVersionThreeData(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	execOK(t, db, "DROP TABLE supply_inventory")
	execOK(t, db, "DELETE FROM schema_migrations WHERE version=4")
	execOK(t, db, `INSERT INTO config_revisions(revision,definition_json,created_at) VALUES('preserved','{"existing":"untouched"}',1)`)
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatal(err)
	}
	var definition string
	if err := db.QueryRow("SELECT definition_json FROM config_revisions WHERE revision='preserved'").Scan(&definition); err != nil || definition != `{"existing":"untouched"}` {
		t.Fatalf("legacy data changed: %q %v", definition, err)
	}
	if queryInt(t, db, "SELECT MAX(version) FROM schema_migrations") != 4 {
		t.Fatal("version not advanced")
	}
	canonical, err := os.ReadFile("../../migrations/004_supply_inventory.sql")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := os.ReadFile("../../internal/store/sqlite/004_supply_inventory.sql")
	if err != nil || !bytes.Equal(canonical, embedded) {
		t.Fatal("migration copies diverged")
	}
}
