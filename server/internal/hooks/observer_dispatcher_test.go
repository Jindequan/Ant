package hooks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func observerRegistry(t *testing.T, observer Observer) *Registry {
	t.Helper()
	r := NewRegistry()
	if observer != nil {
		if err := r.Register(Registration{
			Name: "dispatcher-observer", Version: "v1", Point: PointOperationCommitted, Observer: observer,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	return r
}

func newTestDispatcher(t *testing.T, registry *Registry, options ObserverDispatcherOptions, handler ObserverResultHandler) *ObserverDispatcher {
	t.Helper()
	d, err := NewObserverDispatcher(context.Background(), registry, options, handler)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := d.Close(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("dispatcher cleanup Close() error = %v", err)
		}
	})
	return d
}

func TestNewObserverDispatcherValidation(t *testing.T) {
	sealed := observerRegistry(t, nil)
	unsealed := NewRegistry()
	valid := ObserverDispatcherOptions{Workers: 1, QueueCapacity: 1, Timeout: time.Second}
	cases := []struct {
		name     string
		parent   context.Context
		registry *Registry
		options  ObserverDispatcherOptions
		handler  ObserverResultHandler
	}{
		{"nil parent", nil, sealed, valid, func(ObserverResult) {}},
		{"nil registry", context.Background(), nil, valid, func(ObserverResult) {}},
		{"unsealed registry", context.Background(), unsealed, valid, func(ObserverResult) {}},
		{"zero workers", context.Background(), sealed, ObserverDispatcherOptions{QueueCapacity: 1, Timeout: time.Second}, func(ObserverResult) {}},
		{"zero queue", context.Background(), sealed, ObserverDispatcherOptions{Workers: 1, Timeout: time.Second}, func(ObserverResult) {}},
		{"zero timeout", context.Background(), sealed, ObserverDispatcherOptions{Workers: 1, QueueCapacity: 1}, func(ObserverResult) {}},
		{"too many workers", context.Background(), sealed, ObserverDispatcherOptions{Workers: MaxObserverWorkers + 1, QueueCapacity: 1, Timeout: time.Second}, func(ObserverResult) {}},
		{"too much queue", context.Background(), sealed, ObserverDispatcherOptions{Workers: 1, QueueCapacity: MaxObserverQueueCapacity + 1, Timeout: time.Second}, func(ObserverResult) {}},
		{"nil handler", context.Background(), sealed, valid, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := NewObserverDispatcher(tc.parent, tc.registry, tc.options, tc.handler)
			if d != nil || !errors.Is(err, ErrInvalidObserverDispatcher) {
				t.Fatalf("NewObserverDispatcher() = (%v, %v), want nil and ErrInvalidObserverDispatcher", d, err)
			}
		})
	}
}

func TestObserverDispatcherSubmitIsBoundedAndSnapshotsEvent(t *testing.T) {
	started := make(chan struct{})
	var startedOnce sync.Once
	release := make(chan struct{})
	observedPayload := make(chan string, 2)
	r := observerRegistry(t, func(ctx context.Context, event Event) error {
		observedPayload <- string(event.Payload)
		startedOnce.Do(func() { close(started) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	results := make(chan ObserverResult, 2)
	d := newTestDispatcher(t, r, ObserverDispatcherOptions{Workers: 1, QueueCapacity: 1, Timeout: time.Second}, func(result ObserverResult) {
		results <- result
	})

	payload := []byte("first")
	first := Event{Point: PointOperationCommitted, Payload: payload}
	if err := d.Submit(first); err != nil {
		t.Fatal(err)
	}
	payload[0] = 'X'
	<-started
	if got := <-observedPayload; got != "first" {
		t.Fatalf("observer payload = %q, want immutable snapshot %q", got, "first")
	}
	if err := d.Submit(Event{Point: PointOperationCommitted, Payload: []byte("queued")}); err != nil {
		t.Fatalf("second Submit() error = %v, want queued", err)
	}
	if err := d.Submit(Event{Point: PointOperationCommitted}); !errors.Is(err, ErrObserverQueueFull) {
		t.Fatalf("full queue Submit() error = %v, want ErrObserverQueueFull", err)
	}
	stats := d.Stats()
	if stats.Submitted != 2 || stats.QueueFull != 1 || stats.QueueDepth != 1 {
		t.Fatalf("Stats() = %+v, want submitted=2 queueFull=1 queueDepth=1", stats)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := <-results; string(got.Event.Payload) != "first" || got.Event.APIVersion != RegistryAPIVersion || got.Event.RegistryRevision == "" {
		t.Fatalf("first result event = %+v", got.Event)
	}
	if got := <-results; string(got.Event.Payload) != "queued" {
		t.Fatalf("queued result event payload = %q, want queued", got.Event.Payload)
	}
	if !d.Stats().Closed {
		t.Fatal("Stats().Closed = false after Close")
	}
}

func TestObserverDispatcherPostCommitValidationAndNoObserverNoop(t *testing.T) {
	r := observerRegistry(t, nil)
	d := newTestDispatcher(t, r, ObserverDispatcherOptions{Workers: 1, QueueCapacity: 1, Timeout: time.Second}, func(ObserverResult) {})
	if err := d.Submit(Event{Point: PointOperationCommitted, Payload: []byte("oversized but unused")}); err != nil {
		t.Fatalf("Submit without observer = %v, want no-op", err)
	}
	if err := d.Submit(Event{Point: PointRequestValidated}); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("pre-commit Submit() error = %v, want ErrUnsupportedCapability", err)
	}
	options := DefaultRegistryOptions()
	options.MaxEventPayloadBytes = 0
	limited, err := NewRegistryWithOptions(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := limited.Register(Registration{
		Name: "limited-observer", Version: "v1", Point: PointOperationCommitted,
		Observer: func(context.Context, Event) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := limited.Seal(); err != nil {
		t.Fatal(err)
	}
	limitedDispatcher := newTestDispatcher(t, limited, ObserverDispatcherOptions{Workers: 1, QueueCapacity: 1, Timeout: time.Second}, func(ObserverResult) {})
	if err := limitedDispatcher.Submit(Event{Point: PointOperationCommitted, Payload: []byte("x")}); !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("oversized Submit() error = %v, want ErrEventTooLarge", err)
	}
}

func TestObserverDispatcherHandlerPanicIsIsolated(t *testing.T) {
	var calls atomic.Int32
	r := observerRegistry(t, func(context.Context, Event) error { return nil })
	d := newTestDispatcher(t, r, ObserverDispatcherOptions{Workers: 1, QueueCapacity: 2, Timeout: time.Second}, func(ObserverResult) {
		calls.Add(1)
		panic("handler detail")
	})
	for range 2 {
		if err := d.Submit(Event{Point: PointOperationCommitted}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	stats := d.Stats()
	if calls.Load() != 2 || stats.HandlerPanics != 2 {
		t.Fatalf("handler calls=%d stats=%+v, want two isolated panics", calls.Load(), stats)
	}
}

func TestObserverDispatcherReportsObserverFailures(t *testing.T) {
	r := observerRegistry(t, func(context.Context, Event) error { return errors.New("private observer detail") })
	results := make(chan ObserverResult, 1)
	d := newTestDispatcher(t, r, ObserverDispatcherOptions{Workers: 1, QueueCapacity: 1, Timeout: time.Second}, func(result ObserverResult) {
		results <- result
	})
	if err := d.Submit(Event{Point: PointOperationCommitted}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	result := <-results
	if len(result.Failures) != 1 || !errors.Is(result.Failures[0], ErrObserverFailed) {
		t.Fatalf("result failures = %v, want one sanitized ErrObserverFailed", result.Failures)
	}
	if strings.Contains(result.Failures[0].Error(), "private observer detail") {
		t.Fatalf("observer error leaked callback detail: %v", result.Failures[0])
	}
	if d.Stats().ObserverFailures != 1 {
		t.Fatalf("observer failure count = %d, want 1", d.Stats().ObserverFailures)
	}
}

func TestObserverDispatcherCloseRequiresDeadlineAndDrains(t *testing.T) {
	called := make(chan struct{}, 1)
	r := observerRegistry(t, func(context.Context, Event) error {
		called <- struct{}{}
		return nil
	})
	d := newTestDispatcher(t, r, ObserverDispatcherOptions{Workers: 1, QueueCapacity: 1, Timeout: time.Second}, func(ObserverResult) {})
	if err := d.Close(context.Background()); !errors.Is(err, ErrDeadlineRequired) {
		t.Fatalf("Close without deadline = %v, want ErrDeadlineRequired", err)
	}
	if err := d.Submit(Event{Point: PointOperationCommitted}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-called:
	default:
		t.Fatal("Close returned before queued observer ran")
	}
	if err := d.Submit(Event{Point: PointOperationCommitted}); !errors.Is(err, ErrObserverDispatcherClosed) {
		t.Fatalf("Submit after Close = %v, want ErrObserverDispatcherClosed", err)
	}
}

func TestObserverDispatcherCloseTimeoutCancelsCooperativeObserver(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	r := observerRegistry(t, func(ctx context.Context, _ Event) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	})
	d := newTestDispatcher(t, r, ObserverDispatcherOptions{Workers: 1, QueueCapacity: 1, Timeout: time.Minute}, func(ObserverResult) {})
	if err := d.Submit(Event{Point: PointOperationCommitted}); err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := d.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close timeout error = %v, want context.DeadlineExceeded", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("observer did not receive cooperative cancellation")
	}
}

func TestObserverDispatcherConcurrentSubmitAndClose(t *testing.T) {
	r := observerRegistry(t, func(context.Context, Event) error { return nil })
	d := newTestDispatcher(t, r, ObserverDispatcherOptions{Workers: 2, QueueCapacity: 8, Timeout: time.Second}, func(ObserverResult) {})
	start := make(chan struct{})
	var submitters sync.WaitGroup
	var unexpected atomic.Int32
	for range 32 {
		submitters.Add(1)
		go func() {
			defer submitters.Done()
			<-start
			err := d.Submit(Event{Point: PointOperationCommitted})
			if err != nil && !errors.Is(err, ErrObserverDispatcherClosed) && !errors.Is(err, ErrObserverQueueFull) {
				unexpected.Add(1)
			}
		}()
	}
	close(start)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	submitters.Wait()
	if unexpected.Load() != 0 {
		t.Fatalf("got %d unexpected Submit errors during Close", unexpected.Load())
	}
}
