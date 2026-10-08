package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

func noopCmd(id string) store.WriteCommand {
	return store.WriteCommand{ID: id, Budget: 10 * time.Second, Run: func(store.WriterTx) error { return nil }}
}

func TestQueueFullRejectsWhenNormalLaneFull(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	cfg.WriterQueueLimit = 8
	cfg.ControlReserved = 1 // normal lane capacity = 7
	st, _ := openTestStore(t, cfg)

	started := make(chan struct{})
	gate := make(chan struct{})
	hold := store.WriteCommand{
		ID:     "hold",
		Budget: 30 * time.Second,
		Run: func(store.WriterTx) error {
			close(started)
			<-gate
			return nil
		},
	}
	holdErr := make(chan error, 1)
	go func() { holdErr <- st.SubmitWrite(ctx, hold) }()
	<-started // writer goroutine is now inside the hold command

	// Fill the normal lane; a canceled caller ctx makes SubmitWrite return
	// right after the enqueue (the command itself still runs).
	const normalCapacity = 7
	var queued sync.WaitGroup
	queued.Add(normalCapacity + 1) // seven normal + one control
	for i := 0; i < normalCapacity; i++ {
		cmd := noopCmd(fmt.Sprintf("n-%d", i))
		cmd.Run = func(store.WriterTx) error { queued.Done(); return nil }
		if err := st.SubmitWrite(queuedCtx(), cmd); !errors.Is(err, context.Canceled) {
			t.Fatalf("queued submit %d = %v, want context.Canceled", i, err)
		}
	}
	err := st.SubmitWrite(queuedCtx(), noopCmd("n-over"))
	if !errors.Is(err, store.ErrQueueFull) {
		t.Fatalf("over-capacity submit = %v, want store.ErrQueueFull", err)
	}
	// The control lane still has room while normal is full.
	control := store.WriteCommand{ID: "c-0", Control: true, Budget: 10 * time.Second, Run: func(store.WriterTx) error { queued.Done(); return nil }}
	if err := st.SubmitWrite(queuedCtx(), control); !errors.Is(err, context.Canceled) {
		t.Fatalf("control submit while normal full = %v, want context.Canceled", err)
	}

	close(gate)
	if err := <-holdErr; err != nil {
		t.Fatalf("hold command: %v", err)
	}
	// Wait until the writer drained every queued command, then capacity must
	// be available again.
	queued.Wait()
	if err := st.SubmitWrite(ctx, noopCmd("after-drain")); err != nil {
		t.Fatalf("submit after drain: %v", err)
	}
}

func TestCallerCancelDoesNotAbortCommittedCommand(t *testing.T) {
	ctx := context.Background()
	st, _ := openTestStore(t, testConfig())

	started := make(chan struct{})
	release := make(chan struct{})
	cmd := store.WriteCommand{
		ID:     "cancel-probe",
		Budget: 10 * time.Second,
		Run: func(tx store.WriterTx) error {
			close(started)
			// The receipt is the proof of commit after the caller left.
			if err := tx.InsertReceipt(store.ReceiptRecord{
				Scope:      "cancel",
				CommandID:  "c-1",
				Kind:       "probe",
				InputHash:  "hash-1",
				ResultJSON: []byte(`{}`),
				CreatedAt:  1,
			}); err != nil {
				return err
			}
			<-release
			return nil
		},
	}
	callCtx, cancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() { errCh <- st.SubmitWrite(callCtx, cmd) }()
	<-started
	cancel()
	close(release)

	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("submit result = %v, want context.Canceled", err)
	}
	// The command ran to commit regardless of the caller's ctx: the receipt
	// exists, so no one may assume a rollback.
	var found bool
	if err := st.SubmitWrite(ctx, store.WriteCommand{ID: "verify", Budget: 10 * time.Second, Run: func(tx store.WriterTx) error {
		_, ok, err := tx.GetReceipt("cancel", "c-1")
		found = ok
		return err
	}}); err != nil {
		t.Fatalf("verify submit: %v", err)
	}
	if !found {
		t.Fatal("receipt missing after caller cancellation: command must complete once submitted")
	}
}

func TestCommandBudgetRollsBackWholeCommand(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	cfg.CommandBudget = 10 * time.Millisecond
	st, _ := openTestStore(t, cfg)

	revA, revB := domain.ID("rev-a"), domain.ID("rev-b")
	err := st.SubmitWrite(ctx, store.WriteCommand{
		ID: "budget", // Budget unset → cfg.CommandBudget (10ms) applies
		Run: func(tx store.WriterTx) error {
			if err := tx.InsertConfigRevision(store.ConfigRevisionRecord{Revision: revA, DefinitionJSON: []byte(`{}`), CreatedAt: 1}); err != nil {
				return err
			}
			time.Sleep(150 * time.Millisecond)
			return tx.InsertConfigRevision(store.ConfigRevisionRecord{Revision: revB, DefinitionJSON: []byte(`{}`), CreatedAt: 2})
		},
	})
	if !errors.Is(err, store.ErrCommandBudget) {
		t.Fatalf("budget overrun error = %v, want store.ErrCommandBudget", err)
	}

	// One transaction per command: the budget abort rolls back BOTH inserts.
	err = st.Read(ctx, func(rx store.ReaderTx) error {
		for _, rev := range []domain.ID{revA, revB} {
			if _, ok, err := rx.GetConfigRevision(rev); err != nil {
				return err
			} else if ok {
				return fmt.Errorf("revision %s survived a budget abort", rev)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("post-abort read: %v", err)
	}

	// The writer is healthy after the abort.
	if err := st.SubmitWrite(ctx, store.WriteCommand{ID: "healthy", Budget: 2 * time.Second, Run: func(tx store.WriterTx) error {
		return tx.InsertConfigRevision(store.ConfigRevisionRecord{Revision: domain.ID("rev-c"), DefinitionJSON: []byte(`{}`), CreatedAt: 3})
	}}); err != nil {
		t.Fatalf("store unusable after budget abort: %v", err)
	}
}
