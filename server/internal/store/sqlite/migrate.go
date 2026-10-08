package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strconv"

	"ant/server/internal/store"
)

// The initial schema. go:embed patterns cannot reference the parent
// directory, so the canonical migration file under server/migrations/ has a
// byte-identical twin in this package (001_initial.sql); migrate_test.go
// fails the build-time contract if the two ever diverge.
//
//go:embed 001_initial.sql
var schemaSQL string

//go:embed 002_secret_ownership.sql
var secretOwnershipSQL string

//go:embed 003_decision_trace.sql
var decisionTraceSQL string

//go:embed 004_supply_inventory.sql
var supplyInventorySQL string

//go:embed 005_drop_supply_inventory.sql
var dropSupplyInventorySQL string

//go:embed 006_supply_store.sql
var supplyStoreSQL string

// SchemaSQL returns the embedded initial schema script (used by tests to
// assert the embedded copy matches the canonical file).
func SchemaSQL() string { return schemaSQL }

const (
	// schemaVersion is the version this package applies; it must never
	// decrease relative to server/migrations.
	schemaVersion = 6
	// 34 initial tables, secret_refs (v2), supply_inventory added (v4) and
	// dropped (v5), and the six supply-store tables (v6).
	expectedTableCount = 41
	// migrationLockWait bounds the BEGIN IMMEDIATE wait during migration.
	// The configured busy timeout (10ms) is far too short for the losing
	// side of an initializer race: it must wait out the winner's whole DDL
	// transaction and then observe the version row, not fail with
	// SQLITE_BUSY (02-STORAGE §5).
	migrationLockWait = "5000"
)

// Migrate applies pending schema migrations to st. It must be called once
// before the first SubmitWrite; repeated calls are no-ops, and concurrent
// callers (or processes) serialize on BEGIN IMMEDIATE -- the loser observes
// the version row inside its transaction and skips. A database written by a
// newer schema version is refused with an "upgrade required" error before
// any data is touched (R13): an old binary must never interpret persistent
// structures it does not know.
func Migrate(ctx context.Context, st store.Store) error {
	impl, ok := st.(*Store)
	if !ok {
		return fmt.Errorf("sqlite: migrate requires the sqlite store, got %T: %w", st, store.ErrUnavailable)
	}
	return impl.migrate(ctx)
}

// futureSchemaError is the refusal for a database whose schema version
// exceeds everything this binary supports. The message carries the upgrade
// path: move to a binary that supports the stored version.
func futureSchemaError(found int64) error {
	return fmt.Errorf("sqlite: schema version %d is newer than supported version %d: upgrade required (upgrade the binary before opening this database)",
		found, schemaVersion)
}

func (s *Store) migrate(ctx context.Context) error {
	return s.migrateScript(ctx, schemaSQL)
}

// migrateScript is migrate against an explicit script: production passes the
// embedded schema; tests inject a corrupted script to exercise the failure
// path without touching the embed.
func (s *Store) migrateScript(ctx context.Context, script string) error {
	// Fast path (no write lock taken): already applied, or written by a
	// newer binary -- the latter is refused before any lock or write.
	version, err := schemaVersionOf(ctx, s.readDB)
	if err != nil {
		return fmt.Errorf("sqlite: check schema version: %w", err)
	}
	switch {
	case version > schemaVersion:
		return futureSchemaError(version)
	case version == schemaVersion:
		return nil
	}

	// The write pool holds exactly one connection; taking a dedicated Conn
	// pins it for the whole migration and serializes concurrent Migrate
	// calls behind database/sql's pool acquisition.
	conn, err := s.writeDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: acquire migration connection: %w", store.ErrUnavailable, err)
	}
	defer func() { _ = conn.Close() }()

	// Widen the busy timeout for the DDL window only; the deferred restore
	// runs on the background context so a canceled/deadline ctx cannot skip
	// it -- both the success and every failure path put the configured wait
	// back before the connection returns to the pool (R13).
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout = "+migrationLockWait); err != nil {
		return fmt.Errorf("sqlite: set migration busy_timeout: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(),
			"PRAGMA busy_timeout = "+strconv.FormatInt(s.cfg.BusyTimeout.Milliseconds(), 10))
	}()

	return s.applyMigration(ctx, conn, []migration{{version: 1, script: script}, {version: 2, script: secretOwnershipSQL}, {version: 3, script: decisionTraceSQL}, {version: 4, script: supplyInventorySQL}, {version: 5, script: dropSupplyInventorySQL}, {version: 6, script: supplyStoreSQL}})
}

type migration struct {
	version int64
	script  string
}

// applyMigration runs the whole script inside one BEGIN IMMEDIATE
// transaction and writes the schema_migrations row last: any failure rolls
// back every DDL statement and leaves no version row, so no half migration
// can survive (02-STORAGE §5).
func (s *Store) applyMigration(ctx context.Context, conn *sql.Conn, migrations []migration) error {
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("sqlite: begin migration: %w", err)
	}
	commit := false
	defer func() {
		if !commit {
			// Background context: a canceled/deadline ctx must not prevent
			// the rollback of a half-applied migration.
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	// Re-check under the write lock: a concurrent initializer may have won
	// the race while this goroutine waited for the connection/lock. The
	// future-version guard applies here too -- losing a race against a newer
	// binary is still a refusal, not a downgrade.
	version, err := schemaVersionOf(ctx, conn)
	if err != nil {
		return fmt.Errorf("sqlite: re-check schema version: %w", err)
	}
	switch {
	case version > schemaVersion:
		return futureSchemaError(version)
	case version == schemaVersion:
		return nil // winner already applied it; rollback is a no-op
	}

	for _, migration := range migrations {
		if migration.version <= version {
			continue
		}
		// Each script is one multi-statement batch; all pending migrations
		// commit together so startup never exposes a partially upgraded DB.
		if _, err := conn.ExecContext(ctx, migration.script); err != nil {
			return fmt.Errorf("sqlite: apply schema version %d: %w", migration.version, err)
		}
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)",
			migration.version, int64(s.cfg.Clock.Now())); err != nil {
			return fmt.Errorf("sqlite: record schema version %d: %w", migration.version, err)
		}
	}

	if err := verifyTables(ctx, conn); err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("sqlite: commit migration: %w", err)
	}
	commit = true
	return nil
}

// schemaVersionOf returns the highest applied schema version, or 0 when the
// database has never been migrated.
func schemaVersionOf(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}) (int64, error) {
	var tables int
	if err := q.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'",
	).Scan(&tables); err != nil {
		return 0, err
	}
	if tables == 0 {
		return 0, nil
	}
	var version int64
	if err := q.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version),0) FROM schema_migrations",
	).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

// verifyTables fails the migration unless the expected table set materialized
// (see expectedTableCount).
func verifyTables(ctx context.Context, conn *sql.Conn) error {
	var tables int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'",
	).Scan(&tables); err != nil {
		return fmt.Errorf("sqlite: verify table count: %w", err)
	}
	if tables != expectedTableCount {
		return fmt.Errorf("sqlite: migration produced %d tables, want %d", tables, expectedTableCount)
	}
	return nil
}
