package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"ant/server/internal/domain"
)

func TestExecutionHostReservesCapacityBeforeClaim(t *testing.T) {
	host, err := NewExecutionHost(context.Background(), HostConfig{MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())

	first, err := host.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Reserve(); !errors.Is(err, ErrHostBusy) {
		t.Fatalf("second reservation error = %v, want ErrHostBusy", err)
	}
	first.Release()
	first.Release()
	second, err := host.Reserve()
	if err != nil {
		t.Fatalf("capacity was not returned by Release: %v", err)
	}
	second.Release()
}

func TestExecutionHostOwnsContextAndCancelsOnShutdown(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	host, err := NewExecutionHost(context.Background(), HostConfig{MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := host.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finished := make(chan struct{})
	owner, auth := testOwner()
	if err := reservation.Start(auth, owner, domain.Millis(time.Now().Add(time.Minute).UnixMilli()), func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	cancelRequest()
	if !errors.Is(requestCtx.Err(), context.Canceled) {
		t.Fatal("test request context was not cancelled")
	}
	select {
	case <-finished:
		t.Fatal("HTTP request cancellation stopped a server-owned task")
	case <-time.After(10 * time.Millisecond):
	}
	shutdown, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := host.Close(shutdown); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("server shutdown did not cancel active task")
	}
	if _, err := host.Reserve(); !errors.Is(err, ErrHostClosed) {
		t.Fatalf("Reserve after Close() error = %v, want ErrHostClosed", err)
	}
}

func TestExecutionHostCancelTargetsOnlyActiveOperation(t *testing.T) {
	host, err := NewExecutionHost(context.Background(), HostConfig{MaxActive: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	reservation, err := host.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	owner, auth := testOwner()
	started := make(chan struct{})
	finished := make(chan struct{})
	if err := reservation.Start(auth, owner, domain.Millis(time.Now().Add(time.Minute).UnixMilli()), func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	if host.Cancel(domain.OperationKey{Namespace: "other", OperationID: "op"}) {
		t.Fatal("Cancel matched an operation in another namespace")
	}
	if !host.Cancel(owner.Key) {
		t.Fatal("Cancel did not find active operation")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("task did not observe local cancellation")
	}
}

func TestExecutionHostContainsTaskPanicAndReturnsCapacity(t *testing.T) {
	panicked := make(chan struct{})
	host, err := NewExecutionHost(context.Background(), HostConfig{MaxActive: 1, OnTaskPanic: func() { close(panicked) }})
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := host.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	owner, auth := testOwner()
	if err := reservation.Start(auth, owner, domain.Millis(time.Now().Add(time.Minute).UnixMilli()), func(context.Context) {
		panic("sensitive task data")
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-panicked:
	case <-time.After(time.Second):
		t.Fatal("task panic was not observed")
	}
	shutdown, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := host.Close(shutdown); err != nil {
		t.Fatalf("Close after task panic: %v", err)
	}
	if host.Active() != 0 {
		t.Fatalf("active tasks after panic = %d", host.Active())
	}
	if _, err := host.Reserve(); err != ErrHostClosed {
		t.Fatalf("Reserve after panic and Close = %v, want ErrHostClosed", err)
	}
}

func testOwner() (domain.OwnerToken, domain.AuthContext) {
	key := domain.OperationKey{Namespace: "ns", OperationID: "op"}
	owner := domain.OwnerToken{Key: key, RuntimeID: "runtime", OwnerID: "owner", Generation: 1}
	auth := domain.AuthContext{PrincipalID: "principal", Namespace: key.Namespace, Permissions: []domain.Permission{domain.PermExecute}}
	return owner, auth
}
