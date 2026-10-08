package runtime

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"ant/server/internal/domain"
)

var ErrInvalidSupervisor = errors.New("execution supervisor configuration is invalid")

type LifecycleLedger interface {
	RenewLease(context.Context, domain.AuthContext, []domain.OwnerToken, domain.Millis) error
	ReadControlSignals(context.Context, domain.AuthContext, domain.ID, int) ([]domain.ControlSignal, error)
	Sweep(context.Context, domain.AuthContext, domain.Millis, int) error
}

type SupervisorClock interface {
	Now() domain.Millis
}

type SupervisorConfig struct {
	HeartbeatInterval time.Duration
	LeaseMillis       domain.Millis
	SweepInterval     time.Duration
	CommandTimeout    time.Duration
	RecoveryAuth      domain.AuthContext
	OnError           func(error)
}

type Supervisor struct {
	root    context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	host    *ExecutionHost
	ledger  LifecycleLedger
	clock   SupervisorClock
	cfg     SupervisorConfig
	mu      sync.Mutex
	started bool
	closed  bool
}

func NewSupervisor(parent context.Context, host *ExecutionHost, commands LifecycleLedger, clock SupervisorClock, cfg SupervisorConfig) (*Supervisor, error) {
	if parent == nil || host == nil || commands == nil || clock == nil ||
		cfg.HeartbeatInterval <= 0 || cfg.LeaseMillis <= 0 || cfg.SweepInterval <= 0 || cfg.CommandTimeout <= 0 ||
		cfg.RecoveryAuth.PrincipalID == "" || !slices.Contains(cfg.RecoveryAuth.Permissions, domain.PermManage) {
		return nil, ErrInvalidSupervisor
	}
	root, cancel := context.WithCancel(parent)
	return &Supervisor{root: root, cancel: cancel, done: make(chan struct{}), host: host, ledger: commands, clock: clock, cfg: cfg}, nil
}

// Start launches one bounded process-level loop. All operation renewals and
// cancel reads are batched by namespace; no per-operation ticker is created.
func (s *Supervisor) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.started {
		return
	}
	s.started = true
	go s.run()
}

func (s *Supervisor) run() {
	defer close(s.done)
	heartbeat := time.NewTicker(s.cfg.HeartbeatInterval)
	defer heartbeat.Stop()
	sweep := time.NewTicker(s.cfg.SweepInterval)
	defer sweep.Stop()
	for {
		select {
		case <-s.root.Done():
			return
		case <-heartbeat.C:
			s.heartbeat()
		case <-sweep.C:
			s.sweep()
		}
	}
}

type namespaceTasks struct {
	auth   domain.AuthContext
	owners []domain.OwnerToken
	keys   []domain.OperationKey
}

func (s *Supervisor) heartbeat() {
	groups := make(map[domain.ID]*namespaceTasks)
	for _, task := range s.host.ownedTasks() {
		group := groups[task.owner.Key.Namespace]
		if group == nil {
			group = &namespaceTasks{auth: task.auth}
			groups[task.owner.Key.Namespace] = group
		}
		group.owners = append(group.owners, task.owner)
		group.keys = append(group.keys, task.key)
	}
	for namespace, group := range groups {
		ctx, cancel := context.WithTimeout(s.root, s.cfg.CommandTimeout)
		renewErr := s.ledger.RenewLease(ctx, group.auth, group.owners, s.cfg.LeaseMillis)
		var signals []domain.ControlSignal
		var readErr error
		if renewErr == nil {
			signals, readErr = s.ledger.ReadControlSignals(ctx, group.auth, namespace, DefaultMaxActiveOperations)
		}
		cancel()
		if renewErr != nil || readErr != nil {
			if renewErr != nil {
				s.report(renewErr)
			}
			if readErr != nil {
				s.report(readErr)
			}
			// Without durable ownership/control state, stop local work so it
			// cannot issue another external request under an unrenewed lease.
			for _, key := range group.keys {
				s.host.Cancel(key)
			}
			continue
		}
		for _, signal := range signals {
			if signal.CancelRequested && signal.Key.Namespace == namespace {
				s.host.Cancel(signal.Key)
			}
		}
	}
}

func (s *Supervisor) sweep() {
	ctx, cancel := context.WithTimeout(s.root, s.cfg.CommandTimeout)
	defer cancel()
	if err := s.ledger.Sweep(ctx, s.cfg.RecoveryAuth, s.clock.Now(), DefaultSweepBatch); err != nil && s.root.Err() == nil {
		// Sweeping is retried on the next fixed tick. It never authorizes work;
		// active execution remains guarded by renewal and dispatch CAS.
		s.report(err)
	}
}

func (s *Supervisor) report(err error) {
	if s.cfg.OnError != nil {
		s.cfg.OnError(err)
	}
}

const DefaultSweepBatch = 100

func (s *Supervisor) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalidSupervisor
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
	}
	started := s.started
	s.mu.Unlock()
	if !started {
		return nil
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
