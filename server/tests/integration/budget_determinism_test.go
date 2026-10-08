package integration

// D-B7-v2 (T1 rework round 2): budget determinism proven WITHOUT wall-clock
// grading. The probe evidence that motivated these tests is kept as history:
//
//   - docs/evidence/CODEX-T1-REWORK-AUDIT/baseline-go.log — the first full
//     -race run failed TestCodexRegressionRetryBudget on its hardcoded 50ms
//     ceiling (took 51.967584ms, runs=2);
//   - docs/evidence/CODEX-T1-REWORK-AUDIT/retry-budget-repeat.log — ten
//     directed reruns of the same test all passed.
//
// Neither log can show WHY the timing wobbled, so the assertions below
// replace the threshold with structural proof: every attempt reads the
// command context's absolute deadline (sqlite.CommandContextTx, see
// internal/store/sqlite/writer.go) and retries must observe the SAME
// instant — a retry inherits the remaining budget, it never re-arms a fresh
// timer. Measured wall times are recorded via t.Logf only. The 30s context
// passed to SubmitWrite is a coarse anti-hang guard, never a graded
// threshold: if the store wedges, the caller gets ctx.Err() back and the
// assertions fail loudly.

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

// TestBudgetRetriesNeverRearmDeadline pins the shared-deadline invariant with
// a deliberately unspendable budget: every attempt returns busy instantly, so
// the outcome is decided by the retry counter alone and no scheduling jitter
// can influence it. MaxRetries bounds the runs at exactly k+1, and every
// attempt observes one identical absolute deadline.
func TestBudgetRetriesNeverRearmDeadline(t *testing.T) {
	const (
		budget     = 2 * time.Second // unspendable: attempts fail busy without sleeping
		maxRetries = 4
	)
	cfg := testConfig()
	cfg.MaxRetries = maxRetries
	st, _ := openTestStore(t, cfg)
	rev := domain.ID("rev-rearm")
	antiHang, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		deadlines []int64 // one UnixMilli per attempt, from the command context
		runs      int
	)
	err := st.SubmitWrite(antiHang, store.WriteCommand{
		ID: "rearm-probe", Budget: budget,
		Run: func(tx store.WriterTx) error {
			runs++
			if dl, ok := tx.(sqlite.CommandContextTx).CommandContext().Deadline(); ok {
				deadlines = append(deadlines, dl.UnixMilli())
			}
			if err := tx.InsertConfigRevision(store.ConfigRevisionRecord{
				Revision: rev, DefinitionJSON: []byte(`{}`), CreatedAt: 1,
			}); err != nil {
				return err
			}
			return fmt.Errorf("database is locked") // busy-classified: retry, budget untouched
		},
	})
	t.Logf("budget=%v maxRetries=%d runs=%d err=%v", budget, maxRetries, runs, err)

	if runs != maxRetries+1 {
		t.Fatalf("runs = %d, want exactly %d: instant busy failures must be bounded by MaxRetries, not by the clock", runs, maxRetries+1)
	}
	if len(deadlines) != runs {
		t.Fatalf("deadline observations = %d, want one per attempt (%d)", len(deadlines), runs)
	}
	for i, dl := range deadlines {
		if dl != deadlines[0] {
			t.Fatalf("attempt %d observed deadline %d but attempt 0 observed %d: a retry re-armed a fresh timer instead of inheriting the shared deadline", i, dl, deadlines[0])
		}
	}
	// The budget was never spent, so MaxRetries exhaustion must surface the
	// busy error itself — not a budget error.
	if err == nil || errors.Is(err, store.ErrCommandBudget) {
		t.Fatalf("err = %v: with an unspendable budget the exhaustion must report the busy error, not a budget error", err)
	}
	// Every attempt wrote and rolled back: zero residue.
	assertNoRevision(t, st, rev)
}

// TestBudgetCooperativeTimeoutBoundedAndClean proves the cooperative timeout
// path: a callback selecting on the command context abandons exactly at the
// shared absolute deadline, the command reports store.ErrCommandBudget, it is
// not retried (a cooperative timeout is not busy/locked), nothing commits,
// and the writer serves the next control command immediately. The elapsed
// wall time is logged, never graded.
func TestBudgetCooperativeTimeoutBoundedAndClean(t *testing.T) {
	const budget = 25 * time.Millisecond
	cfg := testConfig()
	cfg.MaxRetries = 5 // retries must be irrelevant: the shared deadline binds first
	st, _ := openTestStore(t, cfg)
	rev := domain.ID("rev-coop-determinism")
	antiHang, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		observedDeadline time.Time
		runs             int
	)
	start := time.Now()
	err := st.SubmitWrite(antiHang, store.WriteCommand{
		ID: "coop-deadline", Budget: budget,
		Run: func(tx store.WriterTx) error {
			runs++
			cmdCtx := tx.(sqlite.CommandContextTx).CommandContext()
			if dl, ok := cmdCtx.Deadline(); ok {
				observedDeadline = dl
			}
			if err := tx.InsertConfigRevision(store.ConfigRevisionRecord{
				Revision: rev, DefinitionJSON: []byte(`{}`), CreatedAt: 1,
			}); err != nil {
				return err
			}
			select {
			case <-cmdCtx.Done():
				return cmdCtx.Err() // cooperate: abandon at the shared deadline
			case <-time.After(10 * time.Second):
				// Reachable only if the writer stops enforcing the command
				// deadline (regression): the assertions below fail loudly.
				return nil
			}
		},
	})
	elapsed := time.Since(start)
	t.Logf("budget=%v elapsed=%v (logged, not graded) runs=%d err=%v", budget, elapsed, runs, err)

	if observedDeadline.IsZero() {
		t.Fatal("the command context exposed no deadline; the cooperative path cannot be bounded without it")
	}
	if !errors.Is(err, store.ErrCommandBudget) {
		t.Fatalf("err = %v, want store.ErrCommandBudget: an overdue cooperative command must never report success", err)
	}
	if runs != 1 {
		t.Fatalf("runs = %d, want 1: a cooperative timeout is not busy/locked and must not be retried", runs)
	}
	assertNoRevision(t, st, rev)
	// The writer regains control right after the cooperative abort.
	if err := st.SubmitWrite(context.Background(), store.WriteCommand{
		ID: "after-coop-deadline", Control: true, Budget: time.Second,
		Run: func(tx store.WriterTx) error {
			return tx.InsertReceipt(store.ReceiptRecord{
				Scope: "budget-determinism", CommandID: "after-coop", Kind: "probe",
				InputHash: "hash-1", ResultJSON: []byte(`{}`), CreatedAt: 1,
			})
		},
	}); err != nil {
		t.Fatalf("writer must remain serviceable after a cooperative timeout: %v", err)
	}
}
