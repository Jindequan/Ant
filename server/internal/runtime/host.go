package runtime

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"ant/server/internal/domain"
)

var (
	ErrHostClosed      = errors.New("execution host is closed")
	ErrHostBusy        = errors.New("execution host is at capacity")
	ErrReservationUsed = errors.New("execution host reservation is no longer available")
	ErrInvalidTask     = errors.New("execution host task is invalid")
)

const DefaultMaxActiveOperations = 128

// TaskFunc performs the complete operation coordination, including durable
// settlement. Its context belongs to the server process, not the HTTP request
// that submitted the operation.
type TaskFunc func(context.Context)

type HostConfig struct {
	MaxActive   int
	OnTaskPanic func()
}

type hostTask struct {
	cancel context.CancelFunc
	auth   domain.AuthContext
	owner  domain.OwnerToken
}

// ExecutionHost owns bounded, process-lifetime operation goroutines. Capacity
// can be reserved before the durable claim, so an overloaded request does not
// leave behind an operation that the process cannot accept.
type ExecutionHost struct {
	mu       sync.Mutex
	root     context.Context
	cancel   context.CancelFunc
	capacity chan struct{}
	tasks    map[domain.OperationKey]hostTask
	onPanic  func()
	closed   bool
	wg       sync.WaitGroup
}

// Reservation is a one-shot capacity claim. Call Release when the durable
// operation claim fails; Start consumes it when the operation is attached.
type Reservation struct {
	host *ExecutionHost
	mu   sync.Mutex
	used bool
}

func NewExecutionHost(parent context.Context, cfg HostConfig) (*ExecutionHost, error) {
	if cfg.MaxActive == 0 {
		cfg.MaxActive = DefaultMaxActiveOperations
	}
	if parent == nil || cfg.MaxActive < 1 {
		return nil, ErrInvalidTask
	}
	root, cancel := context.WithCancel(parent)
	return &ExecutionHost{
		root: root, cancel: cancel, capacity: make(chan struct{}, cfg.MaxActive),
		tasks: make(map[domain.OperationKey]hostTask), onPanic: cfg.OnTaskPanic,
	}, nil
}

// Reserve returns immediately. A request is either admitted before Claim or
// rejected as busy; the host never queues unbounded pending operations.
func (h *ExecutionHost) Reserve() (*Reservation, error) {
	if h == nil {
		return nil, ErrHostClosed
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.root.Err() != nil {
		return nil, ErrHostClosed
	}
	select {
	case h.capacity <- struct{}{}:
		return &Reservation{host: h}, nil
	default:
		return nil, ErrHostBusy
	}
}

// Release returns reserved capacity when no task was attached to it. It is
// safe to call more than once and after host shutdown.
func (r *Reservation) Release() {
	if r == nil || r.host == nil {
		return
	}
	r.mu.Lock()
	if r.used {
		r.mu.Unlock()
		return
	}
	r.used = true
	r.mu.Unlock()
	select {
	case <-r.host.capacity:
	default:
	}
}

// Start attaches the newly claimed operation to a reserved slot. The runner
// executes with a server-owned context bounded by the operation deadline.
func (r *Reservation) Start(auth domain.AuthContext, owner domain.OwnerToken, deadline domain.Millis, run TaskFunc) error {
	if r == nil || r.host == nil || owner.Key.Namespace == "" || owner.Key.OperationID == "" || owner.OwnerID == "" ||
		owner.RuntimeID == "" || owner.Generation < 1 || auth.PrincipalID == "" || auth.Namespace != owner.Key.Namespace ||
		!slices.Contains(auth.Permissions, domain.PermExecute) || deadline <= 0 || run == nil {
		return ErrInvalidTask
	}
	r.mu.Lock()
	if r.used {
		r.mu.Unlock()
		return ErrReservationUsed
	}
	h := r.host
	h.mu.Lock()
	if h.closed || h.root.Err() != nil {
		h.mu.Unlock()
		r.used = true
		r.mu.Unlock()
		select {
		case <-h.capacity:
		default:
		}
		return ErrHostClosed
	}
	if _, exists := h.tasks[owner.Key]; exists {
		h.mu.Unlock()
		r.mu.Unlock()
		return ErrReservationUsed
	}
	ctx, cancel := context.WithDeadline(h.root, time.UnixMilli(int64(deadline)))
	h.tasks[owner.Key] = hostTask{cancel: cancel, auth: auth, owner: owner}
	h.wg.Add(1)
	r.used = true
	h.mu.Unlock()
	r.mu.Unlock()

	go func() {
		defer h.wg.Done()
		defer cancel()
		defer func() {
			h.mu.Lock()
			delete(h.tasks, owner.Key)
			h.mu.Unlock()
			<-h.capacity
		}()
		if taskPanicked(run, ctx) && h.onPanic != nil {
			// Panic values may contain request bodies or credentials. The callback
			// receives no value and runs behind a second recovery boundary.
			func() {
				defer func() { _ = recover() }()
				h.onPanic()
			}()
		}
	}()
	return nil
}

func taskPanicked(run TaskFunc, ctx context.Context) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	run(ctx)
	return false
}

type ownedTask struct {
	key   domain.OperationKey
	auth  domain.AuthContext
	owner domain.OwnerToken
}

func (h *ExecutionHost) ownedTasks() []ownedTask {
	h.mu.Lock()
	defer h.mu.Unlock()
	tasks := make([]ownedTask, 0, len(h.tasks))
	for key, task := range h.tasks {
		tasks = append(tasks, ownedTask{key: key, auth: task.auth, owner: task.owner})
	}
	return tasks
}

// Cancel asks the active local task to stop. The durable cancel intent must be
// recorded by Ledger first; this method only interrupts this process's I/O.
func (h *ExecutionHost) Cancel(key domain.OperationKey) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	task, ok := h.tasks[key]
	h.mu.Unlock()
	if ok {
		task.cancel()
	}
	return ok
}

// Close stops new reservations, cancels active work and waits up to ctx's
// deadline for task coordinators to return. A timeout does not claim that an
// external provider stopped; durable recovery remains responsible for that.
func (h *ExecutionHost) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalidTask
	}
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		h.cancel()
		for _, task := range h.tasks {
			task.cancel()
		}
	}
	h.mu.Unlock()
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *ExecutionHost) Active() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.tasks)
}
