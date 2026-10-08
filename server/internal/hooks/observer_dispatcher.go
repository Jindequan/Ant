package hooks

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

const (
	MaxObserverWorkers       = 64
	MaxObserverQueueCapacity = 16 * 1024
)

// ObserverDispatcherOptions bounds post-commit observer concurrency and
// buffering. Timeout is a cooperative per-event deadline for Registry.Notify.
type ObserverDispatcherOptions struct {
	Workers       int
	QueueCapacity int
	Timeout       time.Duration
}

// ObserverResult reports one event's observer failures after its callbacks
// finish. The handler runs on a dispatcher worker and must return promptly.
type ObserverResult struct {
	Event    Event
	Failures []error
}

type ObserverResultHandler func(ObserverResult)

// ObserverDispatcherStats is a point-in-time operational snapshot.
type ObserverDispatcherStats struct {
	Submitted        uint64
	QueueFull        uint64
	ObserverFailures uint64
	HandlerPanics    uint64
	QueueDepth       int
	Closed           bool
}

// ObserverDispatcher runs post-commit observers on a fixed worker pool. Its
// in-memory queue is bounded and volatile; it is not a durable outbox.
type ObserverDispatcher struct {
	registry *Registry
	queue    chan Event
	handler  ObserverResultHandler
	timeout  time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}

	mu      sync.Mutex
	closed  bool
	workers sync.WaitGroup

	submitted        atomic.Uint64
	queueFull        atomic.Uint64
	observerFailures atomic.Uint64
	handlerPanics    atomic.Uint64
}

// NewObserverDispatcher constructs a fixed-size post-commit worker pool.
// The registry must already be sealed. Parent cancellation stops callback
// contexts; queued entries may be reported with cancellation failures.
func NewObserverDispatcher(parent context.Context, registry *Registry, options ObserverDispatcherOptions, handler ObserverResultHandler) (*ObserverDispatcher, error) {
	if parent == nil || registry == nil || handler == nil ||
		options.Workers <= 0 || options.Workers > MaxObserverWorkers ||
		options.QueueCapacity <= 0 || options.QueueCapacity > MaxObserverQueueCapacity ||
		options.Timeout <= 0 {
		return nil, ErrInvalidObserverDispatcher
	}
	if _, err := registry.Revision(); err != nil {
		return nil, ErrInvalidObserverDispatcher
	}
	ctx, cancel := context.WithCancel(parent)
	d := &ObserverDispatcher{
		registry: registry,
		queue:    make(chan Event, options.QueueCapacity),
		handler:  handler,
		timeout:  options.Timeout,
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	d.workers.Add(options.Workers)
	for range options.Workers {
		go d.worker()
	}
	go func() {
		d.workers.Wait()
		close(d.done)
	}()
	return d, nil
}

// Submit validates and snapshots a committed event, then enqueues it without
// waiting. A full queue fails immediately with ErrObserverQueueFull.
func (d *ObserverDispatcher) Submit(event Event) error {
	if d == nil {
		return ErrInvalidObserverDispatcher
	}
	if err := d.registry.requireCapability(event.Point, CapabilityObserver); err != nil {
		return err
	}
	registered, err := d.registry.registrations(event.Point)
	if err != nil {
		return err
	}
	hasObserver := false
	for _, item := range registered {
		if item.Observer != nil {
			hasObserver = true
			break
		}
	}
	if !hasObserver {
		return nil
	}
	if _, oversized := oversizedEventPayload(registered, CapabilityObserver, len(event.Payload), d.registry.options.MaxEventPayloadBytes); oversized {
		return ErrEventTooLarge
	}
	revision, err := d.registry.Revision()
	if err != nil {
		return err
	}
	event.APIVersion = RegistryAPIVersion
	event.RegistryRevision = revision
	event = event.clone()

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.ctx.Err() != nil {
		return ErrObserverDispatcherClosed
	}
	select {
	case d.queue <- event:
		d.submitted.Add(1)
		return nil
	default:
		d.queueFull.Add(1)
		return ErrObserverQueueFull
	}
}

// Close stops accepting events, drains queued work, and waits for workers.
// A deadline is required; expiry cancels cooperative observer contexts.
func (d *ObserverDispatcher) Close(ctx context.Context) error {
	if d == nil || ctx == nil {
		return ErrInvalidContext
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrDeadlineRequired
	}
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		close(d.queue)
	}
	d.mu.Unlock()
	select {
	case <-d.done:
		d.cancel()
		return nil
	case <-ctx.Done():
		d.cancel()
		return ctx.Err()
	}
}

// Stats returns counters and queue depth without exposing mutable internals.
func (d *ObserverDispatcher) Stats() ObserverDispatcherStats {
	if d == nil {
		return ObserverDispatcherStats{}
	}
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	return ObserverDispatcherStats{
		Submitted:        d.submitted.Load(),
		QueueFull:        d.queueFull.Load(),
		ObserverFailures: d.observerFailures.Load(),
		HandlerPanics:    d.handlerPanics.Load(),
		QueueDepth:       len(d.queue),
		Closed:           closed,
	}
}

func (d *ObserverDispatcher) worker() {
	defer d.workers.Done()
	for event := range d.queue {
		ctx, cancel := context.WithTimeout(d.ctx, d.timeout)
		failures := d.registry.Notify(ctx, event.clone())
		cancel()
		d.observerFailures.Add(uint64(len(failures)))
		result := ObserverResult{Event: event.clone(), Failures: slices.Clone(failures)}
		if d.callHandler(result) {
			d.handlerPanics.Add(1)
		}
	}
}

func (d *ObserverDispatcher) callHandler(result ObserverResult) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	d.handler(result)
	return false
}
