package sqlite

// Package-internal statement-cache tests: they need the write pool's driver
// (to count prepares) and the cache itself. Each pins one of the claims in
// the stmtCache comment.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	drv "modernc.org/sqlite"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

// openCacheTestStore opens a migrated store with an anti-hang budget: these
// tests assert statement reuse, not -race speed against the 50ms product
// budget (M-3).
func openCacheTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "ant.db"), Config{Clock: internalTestClock{}, CommandBudget: 10 * time.Second})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err := Migrate(context.Background(), st); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st.(*Store)
}

// receiptCommand runs one statement of each kind the cache serves: Exec
// (InsertReceipt), QueryRow (GetReceipt) and Query (ListDueRecoveryOperations).
func receiptCommand(i int) store.WriteCommand {
	id := domain.ID(fmt.Sprintf("cmd-%d", i))
	return store.WriteCommand{ID: "cache/" + string(id), Run: func(tx store.WriterTx) error {
		if err := tx.InsertReceipt(store.ReceiptRecord{Scope: "cache", CommandID: id, Kind: "test", InputHash: "h", ResultJSON: []byte(`{}`), CreatedAt: 1}); err != nil {
			return err
		}
		if _, found, err := tx.GetReceipt("cache", id); err != nil || !found {
			return fmt.Errorf("read back receipt: found=%v err=%v", found, err)
		}
		_, err := tx.(store.RecoveryWriterTx).ListDueRecoveryOperations(0, 10)
		return err
	}}
}

// settle waits until the writer finished filling the cache after the last
// command: fill runs after a result is delivered, before the next dequeue.
func settle(t *testing.T, st *Store) int {
	t.Helper()
	submit(t, st, store.WriteCommand{ID: "cache/settle", Run: func(store.WriterTx) error { return nil }})
	st.stmts.mu.Lock()
	defer st.stmts.mu.Unlock()
	return len(st.stmts.stmts)
}

func submit(t *testing.T, st *Store, cmd store.WriteCommand) {
	t.Helper()
	if err := st.SubmitWrite(context.Background(), cmd); err != nil {
		t.Fatalf("submit %s: %v", cmd.ID, err)
	}
}

// countingDriver wraps the sqlite driver and counts the prepares database/sql
// asks for. Plain ExecContext/QueryContext calls (uncached SQL) do not go
// through Prepare, so after warm-up the count moves only if database/sql
// prepares a statement again.
type countingDriver struct{ prepares atomic.Int64 }

type countingConn struct {
	inner interface {
		driver.Conn
		driver.ConnPrepareContext
		driver.ConnBeginTx
		driver.ExecerContext
		driver.QueryerContext
		driver.Pinger
		driver.SessionResetter
		driver.Validator
	}
	d *countingDriver
}

func (d *countingDriver) Open(name string) (driver.Conn, error) {
	c, err := (&drv.Driver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &countingConn{inner: c.(interface {
		driver.Conn
		driver.ConnPrepareContext
		driver.ConnBeginTx
		driver.ExecerContext
		driver.QueryerContext
		driver.Pinger
		driver.SessionResetter
		driver.Validator
	}), d: d}, nil
}

func (c *countingConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}
func (c *countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.d.prepares.Add(1)
	return c.inner.PrepareContext(ctx, query)
}
func (c *countingConn) Close() error { return c.inner.Close() }
func (c *countingConn) Begin() (driver.Tx, error) {
	return c.inner.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *countingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.inner.BeginTx(ctx, opts)
}
func (c *countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.inner.ExecContext(ctx, query, args)
}
func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.inner.QueryContext(ctx, query, args)
}
func (c *countingConn) Ping(ctx context.Context) error         { return c.inner.Ping(ctx) }
func (c *countingConn) ResetSession(ctx context.Context) error { return c.inner.ResetSession(ctx) }
func (c *countingConn) IsValid() bool                          { return c.inner.IsValid() }

var countingSQLite = &countingDriver{}

func init() { sql.Register("ant-counting-sqlite", countingSQLite) }

// TestStatementCacheReusesConnectionHandle pins the database/sql claim: a
// cached statement bound with Tx.StmtContext runs on the handle prepared on
// the single write connection; the transaction never prepares it again.
func TestStatementCacheReusesConnectionHandle(t *testing.T) {
	writeDriverName = "ant-counting-sqlite"
	t.Cleanup(func() { writeDriverName = "sqlite" })
	st := openCacheTestStore(t)

	countingSQLite.prepares.Store(0)
	submit(t, st, receiptCommand(0)) // misses, runs uncached, fill prepares
	n := settle(t, st)
	filled := countingSQLite.prepares.Load()
	if filled != 3 || n != 3 {
		t.Fatalf("first command: %d prepares, %d cached; want the command's 3 statements prepared once each", filled, n)
	}
	for i := 1; i <= 20; i++ {
		submit(t, st, receiptCommand(i))
	}
	if got := countingSQLite.prepares.Load(); got != filled {
		t.Fatalf("20 cached commands prepared %d more statements, want 0", got-filled)
	}

	// The probe is sensitive: a statement prepared on the transaction itself
	// is counted.
	submit(t, st, store.WriteCommand{ID: "cache/tx-prepared", Run: func(tx store.WriterTx) error {
		ps, err := tx.(*writerTx).tx.(*stmtTx).tx.Prepare("SELECT COUNT(*) FROM command_receipts")
		if err != nil {
			return err
		}
		var n int
		return ps.QueryRow().Scan(&n)
	}})
	if got := countingSQLite.prepares.Load(); got != filled+1 {
		t.Fatalf("transaction prepare moved the count to %d, want %d", got, filled+1)
	}
}

// TestStatementCacheKeepsCommandDeadline pins the writer.go contract on the
// cached path: the driver interrupts a cached statement that is still running
// when the command deadline passes. (An already-expired deadline is refused
// by database/sql before the driver runs; the interrupt is what needs the
// per-call ctx.)
func TestStatementCacheKeepsCommandDeadline(t *testing.T) {
	st := openCacheTestStore(t)
	const count = "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < ?) SELECT count(*) FROM c"
	countTo := func(n int64, budget time.Duration, stmtErr *error) store.WriteCommand {
		return store.WriteCommand{ID: "cache/count", Budget: budget, Run: func(tx store.WriterTx) error {
			var got int64
			*stmtErr = tx.(*writerTx).tx.QueryRowContext(tx.(CommandContextTx).CommandContext(), count, n).Scan(&got)
			return *stmtErr
		}}
	}
	var warm error
	for range 2 {
		submit(t, st, countTo(10, 10*time.Second, &warm))
	}
	if settle(t, st) == 0 {
		t.Fatal("the counting statement was not cached")
	}

	// ~1e12 rows: minutes of work in any build, unless interrupted.
	var stmtErr error
	start := time.Now()
	err := st.SubmitWrite(context.Background(), countTo(1e12, 50*time.Millisecond, &stmtErr))
	if !errors.Is(stmtErr, context.DeadlineExceeded) {
		t.Fatalf("cached statement past the deadline returned %v after %v, want context.DeadlineExceeded", stmtErr, time.Since(start))
	}
	if !errors.Is(err, store.ErrCommandBudget) {
		t.Fatalf("command returned %v, want store.ErrCommandBudget", err)
	}
	submit(t, st, countTo(10, 10*time.Second, &warm)) // the interrupted handle is reusable
}

// TestStatementCacheNestedQueryRunsUncached pins the shared-handle guard: a
// cached query run again while its own rows are still open must not reset
// the outer cursor.
func TestStatementCacheNestedQueryRunsUncached(t *testing.T) {
	st := openCacheTestStore(t)
	const upTo = "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < ?) SELECT x FROM c"
	list := func(ctx context.Context, q queryer, n int) ([]int, error) {
		rows, err := q.QueryContext(ctx, upTo, n)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var xs []int
		for rows.Next() {
			var x int
			if err := rows.Scan(&x); err != nil {
				return nil, err
			}
			xs = append(xs, x)
		}
		return xs, rows.Err()
	}
	for range 2 {
		submit(t, st, store.WriteCommand{ID: "cache/warm", Run: func(tx store.WriterTx) error {
			_, err := list(tx.(CommandContextTx).CommandContext(), tx.(*writerTx).tx, 1)
			return err
		}})
	}
	if settle(t, st) == 0 {
		t.Fatal("the nested query was not cached")
	}

	var outer, inner []int
	submit(t, st, store.WriteCommand{ID: "cache/nested", Run: func(tx store.WriterTx) error {
		ctx, q := tx.(CommandContextTx).CommandContext(), tx.(*writerTx).tx
		rows, err := q.QueryContext(ctx, upTo, 3)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x int
			if err := rows.Scan(&x); err != nil {
				return err
			}
			outer = append(outer, x)
			xs, err := list(ctx, q, 2)
			if err != nil {
				return err
			}
			inner = append(inner, xs...)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// Once the outer rows are closed, the cached handle serves again.
		_ = rows.Close()
		xs, err := list(ctx, q, 1)
		inner = append(inner, xs...)
		return err
	}})
	if fmt.Sprint(outer) != "[1 2 3]" || fmt.Sprint(inner) != "[1 2 1 2 1 2 1]" {
		t.Fatalf("nested iteration: outer %v inner %v, want [1 2 3] and [1 2 1 2 1 2 1]", outer, inner)
	}
}

// TestStatementCacheSurvivesSchemaChange pins the SQLITE_SCHEMA claim: a
// cached statement keeps working after the schema it was prepared against
// changes on the same connection.
func TestStatementCacheSurvivesSchemaChange(t *testing.T) {
	st := openCacheTestStore(t)
	for i := range 2 {
		submit(t, st, receiptCommand(i))
	}
	submit(t, st, store.WriteCommand{ID: "cache/ddl", Run: func(tx store.WriterTx) error {
		_, err := tx.(*writerTx).tx.ExecContext(tx.(CommandContextTx).CommandContext(),
			"ALTER TABLE command_receipts ADD COLUMN cache_probe TEXT")
		return err
	}})
	submit(t, st, receiptCommand(2))
}

// TestStatementCacheAllowsBackupAndClose pins the maintenance and shutdown
// claims: no cached statement is left stepping between commands, so VACUUM
// INTO succeeds on the writer connection, commands keep using the cache
// afterwards, and Close releases the cache with the pools.
func TestStatementCacheAllowsBackupAndClose(t *testing.T) {
	st := openCacheTestStore(t)
	for i := range 2 {
		submit(t, st, receiptCommand(i))
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := st.Backup(context.Background(), filepath.Join(dir, "backup.sqlite")); err != nil {
		t.Fatalf("backup with a warm cache: %v", err)
	}
	submit(t, st, receiptCommand(2))
	if settle(t, st) == 0 {
		t.Fatal("cache is empty after backup")
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if n := len(st.stmts.stmts); n != 0 {
		t.Fatalf("Close left %d cached statements", n)
	}
	if err := st.SubmitWrite(context.Background(), receiptCommand(3)); !errors.Is(err, store.ErrQueueClosed) {
		t.Fatalf("submit after close = %v, want ErrQueueClosed", err)
	}
}
