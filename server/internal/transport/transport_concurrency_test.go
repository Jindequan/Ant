package transport

import (
	"errors"
	"sync"
	"testing"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
)

// Regression for review P1-1: HandleTable is called concurrently by the
// Router/ExecutionHost; every path must be race-free and the one-shot marker
// must stay exclusive under parallel MarkUsed.
func TestHandleTableConcurrentAccess(t *testing.T) {
	table := NewHandleTable()
	encoded := adapters.EncodedRequest{
		Endpoint: "https://fixture.invalid/v1/messages",
		Method:   "POST",
		Headers:  map[string]string{"Content-Type": "application/json"},
		Body:     []byte(`{"i":0}`),
	}
	plan := domain.ExecutionPlan{CredentialRevision: "rev-1"}

	const workers = 8
	const perWorker = 50
	var wg sync.WaitGroup
	errCh := make(chan error, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				prepared, err := table.Prepare(encoded, plan)
				if err != nil {
					errCh <- err
					return
				}
				// mark exactly once, then discard; racing discard/MarkUsed must stay race-free
				if _, err := table.MarkUsed(prepared); err != nil {
					errCh <- err
					return
				}
				if _, err := table.MarkUsed(prepared); !errors.Is(err, ErrHandleUsed) {
					errCh <- err
					return
				}
				table.Discard(prepared)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent access failure: %v", err)
	}
}

// The one-shot marker is exclusive even under parallel MarkUsed of the same
// handle: exactly one caller wins, all others see ErrHandleUsed.
func TestHandleTableMarkUsedExclusive(t *testing.T) {
	table := NewHandleTable()
	encoded := adapters.EncodedRequest{Endpoint: "https://fixture.invalid/a", Method: "POST"}
	prepared, err := table.Prepare(encoded, domain.ExecutionPlan{CredentialRevision: "r"})
	if err != nil {
		t.Fatal(err)
	}
	const racers = 16
	var wg sync.WaitGroup
	var wins int64
	var mu sync.Mutex
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := table.MarkUsed(prepared); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("exactly one MarkUsed must win, got %d", wins)
	}
}
