package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sync"
)

// maxCachedStatements bounds the write-pool statement cache. The store's
// static statements are far fewer; the bound only matters for SQL built at
// runtime (disableMissing's IN-list arity), whose variants beyond it run
// uncached.
const maxCachedStatements = 512

// stmtCache keeps one prepared statement per SQL text on the write pool, so a
// command's transaction binds and steps instead of re-parsing its SQL every
// time (M-3: parsing was about half the writer's CPU).
//
// Why this is sound for the single writer connection:
//   - writeDB has exactly one connection, so a statement prepared on the pool
//     is prepared on that connection, and Tx.StmtContext reuses the
//     connection-level handle instead of preparing again (database/sql looks
//     the connection up in the statement's per-connection list; were the
//     connection ever replaced, it re-prepares on the new one).
//   - modernc.org/sqlite keeps the sqlite3_stmt of a single-statement Stmt
//     and resets it after each Exec and on Rows.Close; the transaction end
//     closes any rows a command left open, so no cached statement is still
//     stepping when maintenance (VACUUM INTO) runs.
//   - sqlite3_prepare_v2 statements re-prepare themselves on SQLITE_SCHEMA,
//     so a migration after the cache was filled cannot run stale plans.
//   - statements still run under the command deadline: the per-call ctx goes
//     to the driver, which interrupts the connection when it ends.
//
// A cached handle is shared: running its SQL again while rows from it are
// still open would reset the outer cursor. stmtTx therefore runs such a
// nested use uncached (see stmtTx.stmt), so callers keep the plain-SQL
// semantics.
//
// Statements cannot be prepared while a command holds the only connection,
// so a miss runs uncached and is recorded; the writer prepares the recorded
// misses between commands (fill). Store.Close closes the cache after the
// writer drained.
type stmtCache struct {
	db *sql.DB

	mu      sync.Mutex
	stmts   map[string]*sql.Stmt
	pending map[string]struct{}
}

func newStmtCache(db *sql.DB) *stmtCache {
	return &stmtCache{db: db, stmts: map[string]*sql.Stmt{}, pending: map[string]struct{}{}}
}

// lookup returns the prepared statement for query, or nil after recording
// the miss for the next fill.
func (c *stmtCache) lookup(query string) *sql.Stmt {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st, ok := c.stmts[query]; ok {
		return st
	}
	if len(c.stmts)+len(c.pending) < maxCachedStatements {
		c.pending[query] = struct{}{}
	}
	return nil
}

// fill prepares the recorded misses. It runs on the writer goroutine between
// commands, when no transaction holds the connection; ctx bounds the wait
// for the connection (a concurrent Migrate may pin it). A statement that
// fails to prepare stays uncached and is retried after its next use.
func (c *stmtCache) fill(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for query := range c.pending {
		delete(c.pending, query)
		st, err := c.db.PrepareContext(ctx, query)
		if err != nil {
			continue
		}
		c.stmts[query] = st
	}
}

func (c *stmtCache) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var errs []error
	for query, st := range c.stmts {
		errs = append(errs, st.Close())
		delete(c.stmts, query)
	}
	clear(c.pending)
	return errors.Join(errs...)
}

// stmtTx is the statement surface of a command transaction: cached
// statements are bound to the transaction with Tx.StmtContext, misses run as
// plain SQL text. Every call passes its ctx (the command deadline) through.
type stmtTx struct {
	tx    *sql.Tx
	cache *stmtCache
	// open holds the last Rows each cached query returned in this
	// transaction; while they are open, that query's handle is busy.
	open map[string]*sql.Rows
}

// stmt returns the cached statement for query bound to the transaction, or
// nil when query must run as plain SQL: not cached yet, or its handle is
// still stepping for rows this transaction has not closed.
func (t *stmtTx) stmt(ctx context.Context, query string) *sql.Stmt {
	if rows, ok := t.open[query]; ok {
		if _, err := rows.Columns(); err == nil {
			return nil // still open
		}
		delete(t.open, query)
	}
	st := t.cache.lookup(query)
	if st == nil {
		return nil
	}
	return t.tx.StmtContext(ctx, st)
}

func (t *stmtTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if st := t.stmt(ctx, query); st != nil {
		return st.ExecContext(ctx, args...)
	}
	return t.tx.ExecContext(ctx, query, args...)
}

// QueryRowContext needs no tracking: every caller scans the Row at once,
// which closes it.
func (t *stmtTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if st := t.stmt(ctx, query); st != nil {
		return st.QueryRowContext(ctx, args...)
	}
	return t.tx.QueryRowContext(ctx, query, args...)
}

func (t *stmtTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	st := t.stmt(ctx, query)
	if st == nil {
		return t.tx.QueryContext(ctx, query, args...)
	}
	rows, err := st.QueryContext(ctx, args...)
	if err == nil {
		if t.open == nil {
			t.open = map[string]*sql.Rows{}
		}
		t.open[query] = rows
	}
	return rows, err
}
