package sqlite

// Package-internal migration tests: the failure-path assertions need the
// write-pool connection (to read back per-connection PRAGMA state) and
// migrateScript (to inject a corrupted script without touching the embed).

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"ant/server/internal/domain"
)

type internalTestClock struct{}

func (internalTestClock) Now() domain.Millis { return domain.Millis(time.Now().UnixMilli()) }

func newInternalStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "ant.db"), Config{Clock: internalTestClock{}})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	impl, ok := st.(*Store)
	if !ok {
		t.Fatalf("Open returned %T", st)
	}
	return impl
}

// busyTimeoutOf reads PRAGMA busy_timeout from the write pool. The pool holds
// exactly one connection, so this observes the very connection the migration
// used. Direct PRAGMA readback was chosen over asserting a WAL/journal
// variable because busy_timeout is per-connection state -- it is exactly the
// thing the deferred restore must put back, on the same connection.
func busyTimeoutOf(t *testing.T, st *Store) int64 {
	t.Helper()
	conn, err := st.writeDB.Conn(context.Background())
	if err != nil {
		t.Fatalf("write conn: %v", err)
	}
	defer func() { _ = conn.Close() }()
	var ms int64
	if err := conn.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&ms); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	return ms
}

// TestMigrateFailureLeavesNoHalfMigrationAndRestoresBusyTimeout injects a
// corrupt script (first statement applies, second fails mid-batch) and pins
// the R13 failure path: no half migration survives, the widened
// migrationLockWait is rolled back to the configured busy timeout even though
// the migration failed, and the database stays migratable afterwards.
func TestMigrateFailureLeavesNoHalfMigrationAndRestoresBusyTimeout(t *testing.T) {
	ctx := context.Background()
	st := newInternalStore(t)

	corrupt := "CREATE TABLE half_migrated(id INTEGER NOT NULL); SYNTHETIC INVALID SQL --"
	err := st.migrateScript(ctx, corrupt)
	if err == nil {
		t.Fatal("corrupt migration script accepted")
	}
	t.Logf("corrupt script err: %v", err)

	// Nothing survived the rollback: neither the half-applied table nor a
	// version row.
	var tables int
	if err := st.readDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'",
	).Scan(&tables); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if tables != 0 {
		t.Fatalf("%d tables survived a failed migration, want 0", tables)
	}

	// The deferred restore ran on the failure path: the same pooled
	// connection is back at the configured wait (default 10ms), not the
	// widened 5000ms.
	if got := busyTimeoutOf(t, st); got != st.cfg.BusyTimeout.Milliseconds() {
		t.Fatalf("busy_timeout after failed migration = %dms, want configured %dms", got, st.cfg.BusyTimeout.Milliseconds())
	}

	// No poisoned state: the real schema migrates cleanly afterwards, is a
	// no-op on repeat, and the restore holds on the success path too.
	if err := st.migrateScript(ctx, schemaSQL); err != nil {
		t.Fatalf("migrate after failed attempt: %v", err)
	}
	if err := st.migrateScript(ctx, schemaSQL); err != nil {
		t.Fatalf("repeat migrate: %v", err)
	}
	if got := busyTimeoutOf(t, st); got != st.cfg.BusyTimeout.Milliseconds() {
		t.Fatalf("busy_timeout after successful migration = %dms, want configured %dms", got, st.cfg.BusyTimeout.Milliseconds())
	}
}
