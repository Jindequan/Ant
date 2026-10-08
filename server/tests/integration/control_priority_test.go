package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ant/server/internal/store"
)

// K-gate for the reserved control lane: with the normal queue full of slow
// commands, a control command must still be picked up at the next
// scheduling round (at most 8 control commands run between two normal ones,
// and each round takes up to 8 control first), well before the queued normal
// commands run.
func TestControlLaneNotStarvedByFullNormalQueue(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig()
	cfg.WriterQueueLimit = 8
	cfg.ControlReserved = 4 // normal lane capacity = 4
	st, _ := openTestStore(t, cfg)

	const normalSleep = 40 * time.Millisecond
	var (
		mu   sync.Mutex
		done = map[string]time.Time{}
	)
	record := func(name string) {
		mu.Lock()
		done[name] = time.Now()
		mu.Unlock()
	}
	slowNormal := func(name string) store.WriteCommand {
		return store.WriteCommand{
			ID:     name,
			Budget: 30 * time.Second,
			Run: func(store.WriterTx) error {
				time.Sleep(normalSleep)
				record(name)
				return nil
			},
		}
	}

	started := make(chan struct{})
	first := store.WriteCommand{
		ID:     "n-0",
		Budget: 30 * time.Second,
		Run: func(store.WriterTx) error {
			close(started)
			time.Sleep(normalSleep)
			record("n-0")
			return nil
		},
	}
	firstErr := make(chan error, 1)
	go func() { firstErr <- st.SubmitWrite(ctx, first) }()
	<-started // writer goroutine is inside n-0

	// Fill the normal lane (4 slots) while the writer is busy in n-0.
	for i := 1; i <= 4; i++ {
		if err := st.SubmitWrite(queuedCtx(), slowNormal(fmt.Sprintf("n-%d", i))); !errors.Is(err, context.Canceled) {
			t.Fatalf("queue n-%d = %v, want context.Canceled", i, err)
		}
	}

	controlSubmitted := time.Now()
	controlErr := make(chan error, 1)
	go func() {
		controlErr <- st.SubmitWrite(ctx, store.WriteCommand{
			ID:      "ctl",
			Control: true,
			Budget:  30 * time.Second,
			Run:     func(store.WriterTx) error { record("ctl"); return nil },
		})
	}()
	if err := <-controlErr; err != nil {
		t.Fatalf("control command: %v", err)
	}
	// n-last completes only after every earlier normal command (single
	// writer, FIFO lane); it proves n-1..n-4 all ran.
	if err := st.SubmitWrite(ctx, slowNormal("n-last")); err != nil {
		t.Fatalf("drain probe: %v", err)
	}
	if err := <-firstErr; err != nil {
		t.Fatalf("first normal command: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	ctlAt, ok := done["ctl"]
	if !ok {
		t.Fatal("control command never recorded")
	}
	n1At, ok := done["n-1"]
	if !ok {
		t.Fatal("n-1 never recorded")
	}
	// Hard ordering: the control command beat the first queued normal one
	// (writer takes control at the top of the round, normal afterwards).
	if !ctlAt.Before(n1At) {
		t.Fatalf("control completed at %v, not before n-1 at %v: reserved lane lost", ctlAt, n1At)
	}
	// Sanity bound: control waited only for the in-flight normal command.
	if lat := ctlAt.Sub(controlSubmitted); lat > 2*normalSleep+250*time.Millisecond {
		t.Fatalf("control latency %v too high (normal sleep %v)", lat, normalSleep)
	}
}
