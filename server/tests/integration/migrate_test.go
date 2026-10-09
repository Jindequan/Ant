package integration

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ant/server/internal/store"
	"ant/server/internal/store/sqlite"
)

func TestMigrateFreshDatabaseCreatesEveryTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ant.db")
	st, err := sqlite.Open(path, testConfig())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close(context.Background()) }()

	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db := openRawDB(t, path)
	got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if got != currentTableCount {
		t.Fatalf("table count = %d, want %d", got, currentTableCount)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ant.db")
	st, err := sqlite.Open(path, testConfig())
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
	if got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"); got != currentTableCount {
		t.Fatalf("table count = %d, want %d", got, currentTableCount)
	}
}

func TestMigrateConcurrentAppliesExactlyOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ant.db")
	const racers = 2
	stores := make([]store.Store, 0, racers)
	for i := 0; i < racers; i++ {
		st, err := sqlite.Open(path, testConfig())
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
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations"); got != currentSchemaVersion {
		t.Fatalf("schema_migrations rows = %d, want exactly %d migrations", got, currentSchemaVersion)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"); got != currentTableCount {
		t.Fatalf("table count = %d, want %d", got, currentTableCount)
	}
}

// TestMigrateRejectsFutureSchema pins R13: a database written by a newer
// schema version must be refused with an upgrade path and left untouched --
// the old binary must never interpret (or "migrate down") structures it does
// not know.
func TestMigrateRejectsFutureSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "future.db")

	// Hand-build a future-version database: only the version table with one
	// row newer than this binary, plus a marker table it knows nothing about.
	future, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open future db: %v", err)
	}
	execOK(t, future, "CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL) STRICT")
	execOK(t, future, fmt.Sprintf("INSERT INTO schema_migrations(version, applied_at) VALUES(%d, 42)", currentSchemaVersion+1))
	execOK(t, future, "CREATE TABLE future_binary_table(x INTEGER)")
	if err := future.Close(); err != nil {
		t.Fatalf("close future db: %v", err)
	}

	st, err := sqlite.Open(path, testConfig())
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

	// Untouched: still exactly one future row, marker table intact, and no v1
	// DDL applied (exactly the two hand-built user tables remain).
	db := openRawDB(t, path)
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations"); got != 1 {
		t.Fatalf("schema_migrations rows = %d, want 1 (Migrate must not write to a future-version database)", got)
	}
	if got := queryInt(t, db, "SELECT MAX(version) FROM schema_migrations"); got != currentSchemaVersion+1 {
		t.Fatalf("max schema version = %d, want %d (no downgrade, no extra rows)", got, currentSchemaVersion+1)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"); got != 2 {
		t.Fatalf("user tables = %d, want 2 (schema_migrations + future_binary_table; no v1 DDL ran)", got)
	}
}

// TestMigrateRejectsFutureVersionOverExistingCurrent verifies a current
// database that later gains a newer version row is refused, data intact.
func TestMigrateRejectsFutureVersionOverExistingCurrent(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig()) // migrated to the current version

	db := openRawDB(t, path)
	execOK(t, db, fmt.Sprintf("INSERT INTO schema_migrations(version, applied_at) VALUES(%d, 1)", currentSchemaVersion+1))

	err := sqlite.Migrate(ctx, st)
	if err == nil || !strings.Contains(err.Error(), "upgrade required") {
		t.Fatalf("Migrate over a future-marked database = %v, want an upgrade-required refusal", err)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM schema_migrations"); got != currentSchemaVersion+1 {
		t.Fatalf("schema_migrations rows = %d, want v1..v%d plus one future row (nothing added)", got, currentSchemaVersion)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"); got != currentTableCount {
		t.Fatalf("table count = %d, want %d (schema unchanged by the refusal)", got, currentTableCount)
	}
}

func TestMigrateUpgradesVersionOneDatabase(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	execOK(t, db, "DROP INDEX secret_refs_owner")
	execOK(t, db, "DROP TABLE secret_refs")
	execOK(t, db, "ALTER TABLE decision_records DROP COLUMN trace_json")
	dropSupplyStore(t, db)
	execOK(t, db, "DELETE FROM schema_migrations WHERE version IN (2,3,4,5,6,7,8)")
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatalf("upgrade v1 to current: %v", err)
	}
	if got := queryInt(t, db, "SELECT MAX(version) FROM schema_migrations"); got != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, currentSchemaVersion)
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
	dropSupplyStore(t, db)
	execOK(t, db, "DELETE FROM schema_migrations WHERE version IN (3,4,5,6,7,8)")
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatalf("upgrade v2 to current: %v", err)
	}
	if got := queryInt(t, db, "SELECT MAX(version) FROM schema_migrations"); got != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, currentSchemaVersion)
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
	st, err := sqlite.Open(path, testConfig())
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
	st, err := sqlite.Open(path, testConfig())
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

func TestMigrateUpgradesVersionThreeThroughInventoryAndDrop(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	dropSupplyStore(t, db)
	execOK(t, db, "DELETE FROM schema_migrations WHERE version IN (4,5,6,7,8)")
	execOK(t, db, `INSERT INTO config_revisions(revision,definition_json,created_at) VALUES('preserved','{"existing":"untouched"}',1)`)
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatal(err)
	}
	var definition string
	if err := db.QueryRow("SELECT definition_json FROM config_revisions WHERE revision='preserved'").Scan(&definition); err != nil || definition != `{"existing":"untouched"}` {
		t.Fatalf("legacy data changed: %q %v", definition, err)
	}
	if got := queryInt(t, db, "SELECT MAX(version) FROM schema_migrations"); got != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, currentSchemaVersion)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE name='supply_inventory'"); got != 0 {
		t.Fatal("supply_inventory survived the upgrade")
	}
}

// A v4 database holding rejected-inventory rows upgrades with the table
// dropped (ruling R7, one-way) and every other table's data intact.
func TestMigrateDropsSupplyInventoryFromVersionFour(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	dropSupplyStore(t, db)
	v4, err := os.ReadFile("../../migrations/004_supply_inventory.sql")
	if err != nil {
		t.Fatal(err)
	}
	execOK(t, db, string(v4))
	execOK(t, db, "DELETE FROM schema_migrations WHERE version IN (5,6,7,8)")
	execOK(t, db, `INSERT INTO supply_inventory(namespace,version,definition_json,updated_at) VALUES('ns',1,'{}',1)`)
	execOK(t, db, `INSERT INTO config_revisions(revision,definition_json,created_at) VALUES('kept','{}',1)`)
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatalf("upgrade v4 to current: %v", err)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE name='supply_inventory'"); got != 0 {
		t.Fatal("supply_inventory survived the upgrade")
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM config_revisions WHERE revision='kept'"); got != 1 {
		t.Fatal("unrelated data lost")
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"); got != currentTableCount {
		t.Fatalf("table count = %d, want %d", got, currentTableCount)
	}
}

func TestLaterMigrationCopiesMatchCanonicalFiles(t *testing.T) {
	for _, name := range []string{"004_supply_inventory.sql", "005_drop_supply_inventory.sql", "006_supply_store.sql", "007_supply_offers.sql", "008_supply_cards.sql"} {
		canonical, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		embedded, err := os.ReadFile("../../internal/store/sqlite/" + name)
		if err != nil || !bytes.Equal(canonical, embedded) {
			t.Fatalf("%s: migration copies diverged", name)
		}
	}
}

const (
	currentSchemaVersion = 8
	currentTableCount    = 43
)

// dropSupplyStore removes everything v6 created (children first, then its
// index on secret_refs), so a current database can be rolled back to look
// like an older version.
func dropSupplyStore(t *testing.T, db *sql.DB) {
	t.Helper()
	execOK(t, db, "DROP TABLE supply_cards")  // v8
	execOK(t, db, "DROP TABLE supply_offers") // v7
	for _, table := range []string{"supply_readings", "collector_runs", "supply_credentials", "supply_resources", "supply_packages", "supply_accounts"} {
		execOK(t, db, "DROP TABLE "+table)
	}
	execOK(t, db, "DROP INDEX IF EXISTS secret_refs_namespace_ref")
}

// A v6 database with recorded credentials upgrades to the current version:
// offers (v7) and cards (v8) appear, and every existing credential is
// unpaused.
func TestMigrateUpgradesVersionSixKeepingCredentials(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v6.db")
	raw := openRawDB(t, path)
	execOK(t, raw, "CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL) STRICT")
	for v, name := range []string{"001_initial.sql", "002_secret_ownership.sql", "003_decision_trace.sql", "004_supply_inventory.sql", "005_drop_supply_inventory.sql", "006_supply_store.sql"} {
		script, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if v == 0 {
			// the initial script creates schema_migrations itself
			execOK(t, raw, "DROP TABLE schema_migrations")
		}
		execOK(t, raw, string(script))
		execOK(t, raw, fmt.Sprintf("INSERT INTO schema_migrations(version, applied_at) VALUES(%d, 1)", v+1))
	}
	execOK(t, raw, `INSERT INTO supply_credentials(namespace,credential_id,provider_id,kind,origin,strategy,locator_json,status,created_at)
		VALUES('local','zai-1','zai','api_key','found','quota/default','{}','ok',1)`)
	st, err := sqlite.Open(path, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close(ctx) }()
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatalf("upgrade v6 to current: %v", err)
	}
	if got := queryInt(t, raw, "SELECT paused FROM supply_credentials WHERE credential_id='zai-1'"); got != 0 {
		t.Fatalf("paused = %d, want 0", got)
	}
	for _, table := range []string{"supply_offers", "supply_cards"} {
		if got := queryInt(t, raw, "SELECT COUNT(*) FROM sqlite_master WHERE name='"+table+"'"); got != 1 {
			t.Fatal(table + " missing")
		}
	}
}
