package integration

// R09 acceptance: one absolute deadline per command, shared by the first
// attempt, every busy wait and every retry; overdue commands never commit;
// real lock contention returns within the budget and leaves the writer
// serviceable. Every test logs its measured wall time next to the declared
// budget so "reported error" and "actual completion time" stay separate
// findings.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/store"
	"ant/server/internal/store/sqlite"
)

// TestBudgetNonCooperativeCallbackPinsHonestBoundary documents what the
// budget cannot promise: a callback that ignores the command context cannot
// be preempted. The writer regains control when Run returns (~80ms, not at
// the 5ms budget); at that first checkpoint the overdue command is rolled
// back and reported as store.ErrCommandBudget. Nothing commits after the
// deadline and no watchdog goroutine keeps the transaction alive in the
// background -- the occupancy overrun is decided by the callback, and that
// is exactly why blocking inside Run is forbidden by the writer contract.
func TestBudgetNonCooperativeCallbackPinsHonestBoundary(t *testing.T) {
	ctx := context.Background()
	st, _ := openTestStore(t, testConfig())
	rev := domain.ID("rev-noncoop")

	start := time.Now()
	err := st.SubmitWrite(ctx, store.WriteCommand{
		ID:     "slow-callback",
		Budget: 5 * time.Millisecond,
		Run: func(tx store.WriterTx) error {
			if err := tx.InsertConfigRevision(store.ConfigRevisionRecord{
				Revision: rev, DefinitionJSON: []byte(`{}`), CreatedAt: 1,
			}); err != nil {
				return err
			}
			time.Sleep(80 * time.Millisecond) // ignores the deadline entirely
			return nil
		},
	})
	elapsed := time.Since(start)
	t.Logf("declaredBudget=5ms callbackSleep=80ms actualElapsed=%v err=%v", elapsed, err)

	if elapsed < 80*time.Millisecond {
		t.Fatalf("returned after %v, before the non-cooperative callback could finish: impossible, the callback owns its own return time", elapsed)
	}
	if !errors.Is(err, store.ErrCommandBudget) {
		t.Fatalf("err = %v, want store.ErrCommandBudget: an overdue command must not be reported as success", err)
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("writer occupied %v: the refusal must happen at the first checkpoint after Run returns", elapsed)
	}
	// The overdue command left nothing behind.
	assertNoRevision(t, st, rev)
}

// TestBudgetCooperativeCallbackExitsWithinBudget proves the cooperative path:
// a callback selecting on the command context observes the deadline and the
// writer is free again right at the budget, far before the callback's
// would-be 80ms wait.
func TestBudgetCooperativeCallbackExitsWithinBudget(t *testing.T) {
	ctx := context.Background()
	st, _ := openTestStore(t, testConfig())
	rev := domain.ID("rev-coop")

	start := time.Now()
	err := st.SubmitWrite(ctx, store.WriteCommand{
		ID:     "cooperative-callback",
		Budget: 5 * time.Millisecond,
		Run: func(tx store.WriterTx) error {
			if err := tx.InsertConfigRevision(store.ConfigRevisionRecord{
				Revision: rev, DefinitionJSON: []byte(`{}`), CreatedAt: 1,
			}); err != nil {
				return err
			}
			cmdCtx := tx.(sqlite.CommandContextTx).CommandContext()
			select {
			case <-cmdCtx.Done():
				return cmdCtx.Err() // cooperate: abandon at the deadline
			case <-time.After(80 * time.Millisecond):
				return nil
			}
		},
	})
	elapsed := time.Since(start)
	t.Logf("declaredBudget=5ms wouldBlockFor=80ms actualElapsed=%v err=%v", elapsed, err)

	if elapsed >= 60*time.Millisecond {
		t.Fatalf("cooperative callback returned after %v; selecting on the command context must exit at the 5ms deadline", elapsed)
	}
	if !errors.Is(err, store.ErrCommandBudget) {
		t.Fatalf("err = %v, want store.ErrCommandBudget", err)
	}
	assertNoRevision(t, st, rev)
	// The writer is immediately serviceable again.
	if err := st.SubmitWrite(ctx, noopCmd("after-coop")); err != nil {
		t.Fatalf("writer unusable after cooperative abort: %v", err)
	}
}

// TestRetriesShareOneWholeCommandBudget pins the rework defect: retries must
// not re-arm a fresh per-attempt timer. Budget=30ms, MaxRetries=2, every
// attempt burns ~20ms and reports busy: the shared deadline refuses the third
// attempt (2 runs, ~40ms total) where the old per-attempt timeout ran 3 times
// for ~63ms.
func TestRetriesShareOneWholeCommandBudget(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	cfg.MaxRetries = 2
	st, _ := openTestStore(t, cfg)

	runs := 0
	start := time.Now()
	err := st.SubmitWrite(ctx, store.WriteCommand{
		ID:     "retries",
		Budget: 30 * time.Millisecond,
		Run: func(store.WriterTx) error {
			runs++
			time.Sleep(20 * time.Millisecond)
			return fmt.Errorf("database is locked") // busy-classified failure
		},
	})
	elapsed := time.Since(start)
	t.Logf("declaredBudget=30ms maxRetries=2 perAttemptBusy=20ms actualElapsed=%v runs=%d err=%v", elapsed, runs, err)

	if runs != 2 {
		t.Fatalf("runs = %d, want 2: the shared 30ms budget must refuse the third attempt", runs)
	}
	if !errors.Is(err, store.ErrCommandBudget) {
		t.Fatalf("err = %v, want store.ErrCommandBudget", err)
	}
	if elapsed >= 80*time.Millisecond {
		t.Fatalf("elapsed %v: three 20ms attempts would mean the budget was re-billed per try", elapsed)
	}
}

// TestRealWriteLockContentionExhaustsBudget drives the budget against the
// real driver: an independent connection holds BEGIN EXCLUSIVE, so the
// writer's BEGIN IMMEDIATE cannot get the lock at all. Attempts burn the one
// budget through real SQLITE_BUSY waits (each capped by
// min(busy_timeout, remaining)), the command fails while the foreign lock is
// still held, the control lane advances right after release, and nothing was
// written. lockHold deliberately outlasts the budget so "returned error"
// cannot be confused with "returned because the lock went away".
func TestRealWriteLockContentionExhaustsBudget(t *testing.T) {
	const (
		budget   = 25 * time.Millisecond
		lockHold = 400 * time.Millisecond
	)
	ctx := context.Background()
	cfg := testConfig()
	cfg.BusyTimeout = 10 * time.Millisecond
	cfg.MaxRetries = 5 // let the budget, not the retry counter, bind
	st, path := openTestStore(t, cfg)

	raw := openRawDB(t, path) // independent writable connection
	lockConn, err := raw.Conn(ctx)
	if err != nil {
		t.Fatalf("raw conn: %v", err)
	}
	defer func() { _ = lockConn.Close() }()
	if _, err := lockConn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("begin exclusive: %v", err)
	}
	defer func() { _, _ = lockConn.ExecContext(context.Background(), "ROLLBACK") }()

	rev := domain.ID("rev-lock")
	start := time.Now()
	submitErr := st.SubmitWrite(ctx, store.WriteCommand{
		ID:     "locked-out",
		Budget: budget,
		Run: func(tx store.WriterTx) error {
			// Unreachable while the lock is held (BeginTx precedes Run); if a
			// regression ever runs it, the read-back below must catch it.
			return tx.InsertConfigRevision(store.ConfigRevisionRecord{
				Revision: rev, DefinitionJSON: []byte(`{}`), CreatedAt: 1,
			})
		},
	})
	elapsed := time.Since(start)
	t.Logf("budget=%v busyTimeout=10ms maxRetries=5 actualElapsed=%v err=%v (lock still held %v more)",
		budget, elapsed, submitErr, lockHold-elapsed)

	if elapsed >= lockHold {
		t.Fatalf("command returned only at %v, after the lock window: the budget did not bound the busy waits", elapsed)
	}
	if !errors.Is(submitErr, store.ErrCommandBudget) {
		t.Fatalf("err = %v, want store.ErrCommandBudget", submitErr)
	}

	// Release the lock; the control lane must advance immediately -- the
	// writer was neither permanently occupied nor poisoned by the aborts.
	if _, err := lockConn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("release lock: %v", err)
	}
	ctlStart := time.Now()
	if err := st.SubmitWrite(ctx, store.WriteCommand{
		ID:      "ctl-after-lock",
		Control: true,
		Budget:  5 * time.Second,
		Run: func(tx store.WriterTx) error {
			return tx.InsertReceipt(store.ReceiptRecord{
				Scope: "lock", CommandID: "ctl-1", Kind: "probe",
				InputHash: "hash-1", ResultJSON: []byte(`{}`), CreatedAt: 1,
			})
		},
	}); err != nil {
		t.Fatalf("control command after lock contention: %v", err)
	}
	t.Logf("control command committed %v after lock release", time.Since(ctlStart))

	// No business residue from the failed command.
	assertNoRevision(t, st, rev)
}

func assertNoRevision(t *testing.T, st store.Store, rev domain.ID) {
	t.Helper()
	err := st.Read(context.Background(), func(rx store.ReaderTx) error {
		_, ok, err := rx.GetConfigRevision(rev)
		if err != nil {
			return err
		}
		if ok {
			return fmt.Errorf("revision %s was committed although its command failed", rev)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read back %s: %v", rev, err)
	}
}
