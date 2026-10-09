package sqlite

// Writer command contract (02-STORAGE §3; rework R09 — the documented home
// for the WriteCommand.Run semantics while internal/store/store.go stays
// frozen).
//
// Budget ownership. A command's whole-command budget starts when the writer
// goroutine dequeues it, not at SubmitWrite: queue admission is bounded by
// lane capacity (store.ErrQueueFull), never by time, and a caller may abandon
// its queue slot via ctx without affecting the command. At dequeue the writer
// computes one absolute deadline = now + Budget (Config.CommandBudget when
// Budget is unset). The first attempt, every busy wait and every retry share
// that deadline: a retry never re-arms a fresh timer, it inherits the
// remaining time only.
//
// Run obligations. Run executes on the writer goroutine inside a single
// BEGIN IMMEDIATE transaction, so every millisecond inside Run is a
// millisecond the whole store cannot commit anything else. It must:
//   - not do network I/O, not wait on consumers or other goroutines, and not
//     sleep without bound;
//   - return every statement error: all statements run under the command
//     deadline, so a spent budget surfaces as context.DeadlineExceeded;
//   - block only cooperatively: select on the command context obtained via
//     CommandContextTx (tx.(CommandContextTx).CommandContext()) so the writer
//     reclaims control at the deadline.
//
// Guarantee boundary. The driver cancels the SQL phases (BEGIN IMMEDIATE
// lock wait, statements, commit) at the deadline, and the busy handler is
// capped by min(Config.BusyTimeout, remaining budget). A command never
// commits after its deadline: Run returning success past the deadline is
// rolled back and reported as store.ErrCommandBudget. What the budget cannot
// promise: preempting a callback that ignores the context — the writer
// regains control at the first checkpoint after Run returns, so observed
// occupancy can exceed the budget by exactly the callback's overrun. That
// case is why blocking inside Run is forbidden above; it is deliberately NOT
// hidden by a watchdog goroutine — there is no path that reports a timeout
// while the old transaction keeps running in the background.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	drv "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"ant/server/internal/store"
)

// CommandContextTx is implemented by the WriterTx handed to Run. A callback
// that must block selects on CommandContext() so it observes the whole-command
// deadline (see the writer contract above). The accessor lives in this
// package instead of the frozen store.WriterTx interface; T2 may promote it
// once second-party callbacks exist.
type CommandContextTx interface {
	CommandContext() context.Context
}

// queueCommand couples an enqueued WriteCommand with its one-shot result
// channel. done is buffered(1) so the writer never blocks on a caller that
// already left via ctx cancellation.
type queueCommand struct {
	cmd         store.WriteCommand
	maintenance func(context.Context, *sql.DB) error
	budget      time.Duration
	done        chan error
}

// readTimeout bounds a whole read snapshot (02-STORAGE §3: 查询有超时).
const readTimeout = 5 * time.Second

// SubmitWrite enqueues one durable command on its lane (control or normal)
// and blocks until the command committed or definitively failed. A full lane
// rejects with store.ErrQueueFull before ownership is taken; a caller whose
// ctx ends early gets ctx.Err() back, but the command keeps running to
// commit or fail on its own budget -- "submitted" never proves "rolled back"
// (02-STORAGE §3); only the receipt does. The budget clock starts at dequeue:
// see the writer contract at the top of this file.
func (s *Store) SubmitWrite(ctx context.Context, cmd store.WriteCommand) error {
	qc := &queueCommand{cmd: cmd, done: make(chan error, 1)}

	s.mu.RLock()
	if s.submissionsClosed {
		s.mu.RUnlock()
		return store.ErrQueueClosed
	}
	lane := s.normalCh
	if cmd.Control {
		lane = s.controlCh
	}
	select {
	case lane <- qc:
	default:
		s.mu.RUnlock()
		return store.ErrQueueFull
	}
	s.mu.RUnlock()

	select {
	case err := <-qc.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// submitMaintenance serializes bounded non-transactional SQLite maintenance
// with all WriteCommands on the same writer loop. Once accepted, maintenance
// runs to a definite result even if the caller stops waiting; callers must
// inspect their destination after an early context return.
func (s *Store) submitMaintenance(ctx context.Context, budget time.Duration, run func(context.Context, *sql.DB) error) error {
	if ctx == nil || run == nil || budget <= 0 {
		return errors.New("sqlite: invalid maintenance command")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	qc := &queueCommand{maintenance: run, budget: budget, done: make(chan error, 1)}
	s.mu.RLock()
	if s.submissionsClosed {
		s.mu.RUnlock()
		return store.ErrQueueClosed
	}
	select {
	case s.normalCh <- qc:
		s.mu.RUnlock()
	default:
		s.mu.RUnlock()
		return store.ErrQueueFull
	}
	select {
	case err := <-qc.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Read runs fn against a consistent snapshot from the query-only pool: one
// pooled connection inside a deferred read transaction, so every query in fn
// sees the same committed state.
func (s *Store) Read(ctx context.Context, fn func(store.ReaderTx) error) error {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	tx, err := s.readDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin read snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // read-only: rollback merely releases the snapshot
	return fn(&readerTx{ctx: ctx, tx: tx})
}

// Close stops accepting submissions, drains both lanes within the ctx
// deadline, waits for the writer goroutine, then closes the pools. If the
// deadline expires the pools are left open (the loop keeps draining
// budget-bounded commands) and ctx.Err() is reported.
func (s *Store) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.submissionsClosed {
		s.submissionsClosed = true
		// Closing both lanes wakes the blocked writer and lets it drain the
		// buffered commands: receives on a closed channel still yield the
		// queued values before reporting !ok.
		close(s.controlCh)
		close(s.normalCh)
	}
	s.mu.Unlock()

	select {
	case <-s.loopDone:
	case <-ctx.Done():
		return fmt.Errorf("sqlite: writer drain exceeded close deadline: %w", ctx.Err())
	}

	// The writer has exited, so no command can bind a cached statement any
	// more; close them before the pool that owns their connection.
	return errors.Join(s.stmts.close(), s.writeDB.Close(), s.readDB.Close())
}

// writerLoop serializes every durable change. Fairness per 01-ARCHITECTURE
// §3: each round takes at most 8 control commands, then must attempt one
// normal command; if none is pending it moves on without blocking, so a
// flooded normal lane cannot starve control, and the cap of 8 keeps control
// from starving normal. When both lanes are empty the loop parks on both.
func (s *Store) writerLoop() {
	defer close(s.loopDone)
	controlOpen, normalOpen := true, true
	for controlOpen || normalOpen {
		// Phase 1: up to 8 control commands.
		for took := 0; took < 8 && controlOpen; {
			select {
			case qc, ok := <-s.controlCh:
				if !ok {
					controlOpen = false
					continue
				}
				s.execCommand(qc)
				took++
			default:
				took = 8 // nothing pending right now
			}
		}
		// Phase 2: exactly one normal command, if one is pending.
		if normalOpen {
			select {
			case qc, ok := <-s.normalCh:
				if !ok {
					normalOpen = false
				} else {
					s.execCommand(qc)
				}
			default:
			}
		}
		// Phase 3: nothing immediately available and both lanes live -- park.
		// A Close() closing the lanes wakes this select.
		if controlOpen && normalOpen {
			select {
			case qc, ok := <-s.controlCh:
				if ok {
					s.execCommand(qc)
				} else {
					controlOpen = false
				}
			case qc, ok := <-s.normalCh:
				if ok {
					s.execCommand(qc)
				} else {
					normalOpen = false
				}
			}
		}
	}
}

// execCommand runs one queueCommand to a definite outcome (commit, failure,
// budget or retry exhaustion) and delivers the result exactly once. The
// absolute deadline is fixed once here, at dequeue time; no attempt can
// extend it.
func (s *Store) execCommand(qc *queueCommand) {
	if qc.maintenance != nil {
		s.execMaintenance(qc)
		return
	}
	deadline := time.Now().Add(s.commandBudget(qc.cmd))
	var err error
	for attempt := 0; ; attempt++ {
		err = s.runOnce(qc.cmd, deadline)
		// busy/locked: the transaction was rolled back in full; retry the
		// whole command, at most cfg.MaxRetries times and only while the
		// shared budget still has time left (02-STORAGE §3).
		if err == nil || attempt >= s.cfg.MaxRetries || !isBusyLocked(err) {
			break
		}
		// A retry needs at least one granular unit of busy-wait budget left:
		// SQLite busy_timeout is integral milliseconds, and an attempt with
		// less than that would collapse to a free instant SQLITE_BUSY --
		// retrying it would spend no budget and let the retry counter, not
		// the deadline, decide the outcome.
		if time.Until(deadline) < time.Millisecond {
			err = fmt.Errorf("%w: spent after %d attempt(s), not retrying", store.ErrCommandBudget, attempt+1)
			break
		}
	}
	s.restoreBusyTimeout()
	qc.done <- err
	s.fillStatements()
}

// fillStatements prepares the statements the last command ran uncached
// (stmtcache.go). It runs after the result is delivered and before the next
// dequeue, so it costs no command any budget; the bound only caps the wait
// for the connection.
func (s *Store) fillStatements() {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.CommandBudget)
	defer cancel()
	s.stmts.fill(ctx)
}

func (s *Store) execMaintenance(qc *queueCommand) {
	deadline := time.Now().Add(qc.budget)
	var err error
	for attempt := 0; ; attempt++ {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		if setErr := s.applyBusyTimeout(min(s.cfg.BusyTimeout, time.Until(deadline))); setErr != nil {
			err = setErr
		} else {
			err = qc.maintenance(ctx, s.writeDB)
		}
		if ctx.Err() != nil {
			err = budgetError(ctx, ctx.Err())
		}
		cancel()
		if err == nil || attempt >= s.cfg.MaxRetries || !isBusyLocked(err) || time.Until(deadline) < time.Millisecond {
			break
		}
	}
	s.restoreBusyTimeout()
	qc.done <- err
}

// commandBudget resolves the per-command budget: an explicit positive
// command budget wins, otherwise the configured default applies (both are
// validated positive at Open).
func (s *Store) commandBudget(cmd store.WriteCommand) time.Duration {
	if cmd.Budget > 0 {
		return cmd.Budget
	}
	return s.cfg.CommandBudget
}

// runOnce executes one attempt of cmd inside a single BEGIN IMMEDIATE
// transaction under the command-wide deadline. The deadline context derives
// from the writer's background context, never from the (long gone) caller
// ctx: once dequeued the command belongs to the writer, and caller
// cancellation must not abort it (02-STORAGE §3).
func (s *Store) runOnce(cmd store.WriteCommand, deadline time.Time) error {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	// The driver's busy handler must never outlive the budget: cap it by the
	// remaining time (a spent budget yields busy_timeout=0, i.e. immediate
	// SQLITE_BUSY). Failing to apply the cap fails the attempt -- running
	// with an unbounded busy wait would silently violate the deadline.
	if err := s.applyBusyTimeout(min(s.cfg.BusyTimeout, time.Until(deadline))); err != nil {
		return err
	}

	// _txlock=immediate on the write pool turns this into BEGIN IMMEDIATE,
	// acquiring the write lock up front (02-STORAGE §3).
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return budgetError(ctx, err)
	}
	if err := cmd.Run(&writerTx{ctx: ctx, tx: &stmtTx{tx: tx, cache: s.stmts}}); err != nil {
		_ = tx.Rollback()
		return budgetError(ctx, err)
	}
	// Commit refusal: a command whose deadline has passed never lands, even
	// when its non-cooperative Run reports success. The callback decides when
	// the writer regains control, never whether an overdue command commits.
	if ctx.Err() != nil {
		_ = tx.Rollback()
		return budgetError(ctx, ctx.Err())
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return budgetError(ctx, err)
	}
	return nil
}

// applyBusyTimeout sets the SQLite busy handler of the single writer
// connection to min(configured busy timeout, remaining budget) for the
// upcoming attempt. Every attempt re-asserts the pragma before BeginTx;
// execCommand restores the configured value once the command is finished, so
// the connection never carries a stale widened wait.
func (s *Store) applyBusyTimeout(remaining time.Duration) error {
	limit := min(s.cfg.BusyTimeout, remaining)
	if limit < 0 {
		limit = 0
	}
	if _, err := s.writeDB.ExecContext(context.Background(),
		"PRAGMA busy_timeout = "+fmt.Sprint(limit.Milliseconds())); err != nil {
		return fmt.Errorf("sqlite: set attempt busy_timeout: %w", err)
	}
	return nil
}

// restoreBusyTimeout puts the configured busy timeout back after a command
// (best effort: the pragma is per-connection state hygiene, not a command
// outcome; the next attempt re-asserts it anyway).
func (s *Store) restoreBusyTimeout() {
	_, _ = s.writeDB.ExecContext(context.Background(),
		"PRAGMA busy_timeout = "+fmt.Sprint(s.cfg.BusyTimeout.Milliseconds()))
}

// budgetError maps a spent command budget onto store.ErrCommandBudget while
// keeping the cause for diagnostics. The checkpoint inspects the command
// context itself, so a cooperative callback returning the context error and
// a driver abort at the deadline are classified alike.
func budgetError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %v", store.ErrCommandBudget, err)
	}
	return err
}

// isBusyLocked reports whether err is SQLite SQLITE_BUSY/SQLITE_LOCKED. The
// typed check uses the driver's *sqlite.Error.Code(); the string fallback
// covers wrapped forms that lost the type (e.g. via errors.Join).
func isBusyLocked(err error) bool {
	if err == nil {
		return false
	}
	var serr *drv.Error
	if errors.As(err, &serr) {
		if code := serr.Code(); code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED {
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
}
