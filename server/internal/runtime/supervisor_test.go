package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"ant/server/internal/domain"
)

type lifecycleLedgerStub struct {
	mu         sync.Mutex
	renewCalls int
	readCalls  int
	sweepCalls int
	signal     *domain.ControlSignal
}

func (s *lifecycleLedgerStub) RenewLease(_ context.Context, _ domain.AuthContext, owners []domain.OwnerToken, lease domain.Millis) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(owners) == 0 || lease <= 0 {
		return ErrInvalidSupervisor
	}
	s.renewCalls++
	return nil
}

func (s *lifecycleLedgerStub) ReadControlSignals(_ context.Context, _ domain.AuthContext, namespace domain.ID, limit int) ([]domain.ControlSignal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readCalls++
	if limit < 1 || (s.signal != nil && s.signal.Key.Namespace != namespace) {
		return nil, ErrInvalidSupervisor
	}
	if s.signal == nil {
		return nil, nil
	}
	return []domain.ControlSignal{*s.signal}, nil
}

func (s *lifecycleLedgerStub) Sweep(context.Context, domain.AuthContext, domain.Millis, int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepCalls++
	return nil
}

type supervisorClockStub struct{}

func (supervisorClockStub) Now() domain.Millis { return 100 }

func TestSupervisorRenewsPollsCancellationAndSweeps(t *testing.T) {
	host, err := NewExecutionHost(context.Background(), HostConfig{MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	owner, auth := testOwner()
	started := make(chan struct{})
	finished := make(chan struct{})
	reservation, err := host.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	deadline := domain.Millis(time.Now().Add(time.Minute).UnixMilli())
	if err := reservation.Start(auth, owner, deadline, func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	}); err != nil {
		t.Fatal(err)
	}
	<-started

	stub := &lifecycleLedgerStub{signal: &domain.ControlSignal{Key: owner.Key, CancelRequested: true, ControlVersion: 2}}
	recoveryAuth := domain.AuthContext{PrincipalID: "runtime", Permissions: []domain.Permission{domain.PermManage}}
	supervisor, err := NewSupervisor(context.Background(), host, stub, supervisorClockStub{}, SupervisorConfig{
		HeartbeatInterval: 5 * time.Millisecond, LeaseMillis: 100, SweepInterval: 5 * time.Millisecond,
		CommandTimeout: time.Second, RecoveryAuth: recoveryAuth,
	})
	if err != nil {
		t.Fatal(err)
	}
	supervisor.Start()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("durable cancel signal did not stop the active task")
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := supervisor.Close(shutdown); err != nil {
		t.Fatalf("Supervisor.Close() = %v", err)
	}
	if err := host.Close(shutdown); err != nil {
		t.Fatalf("Host.Close() = %v", err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.renewCalls == 0 || stub.readCalls == 0 || stub.sweepCalls == 0 {
		t.Fatalf("renew=%d read=%d sweep=%d", stub.renewCalls, stub.readCalls, stub.sweepCalls)
	}
}

func TestSupervisorRejectsMissingTrustedRecoveryIdentity(t *testing.T) {
	host, err := NewExecutionHost(context.Background(), HostConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	_, err = NewSupervisor(context.Background(), host, &lifecycleLedgerStub{}, supervisorClockStub{}, SupervisorConfig{
		HeartbeatInterval: time.Second, LeaseMillis: 100, SweepInterval: time.Second,
		CommandTimeout: time.Second,
	})
	if err != ErrInvalidSupervisor {
		t.Fatalf("NewSupervisor() error = %v, want ErrInvalidSupervisor", err)
	}
}
