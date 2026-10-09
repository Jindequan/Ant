package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/security"
	"ant/server/internal/store/sqlite"
)

type balanceFetcherFunc func(context.Context, string) ([]byte, error)

func (f balanceFetcherFunc) FetchBalance(ctx context.Context, key string) ([]byte, error) {
	return f(ctx, key)
}

func TestDeepSeekBalanceAdmissionIsBounded(t *testing.T) {
	service := &DeepSeekBalanceVerification{flights: make(map[string]*balanceFlight)}
	releases := make([]func(), 0, deepSeekBalanceMaxInFlight)
	for i := 0; i < deepSeekBalanceMaxInFlight; i++ {
		release, err := service.acquire(context.Background(), fmt.Sprintf("command-%d", i))
		if err != nil {
			t.Fatalf("acquire slot %d: %v", i, err)
		}
		releases = append(releases, release)
	}
	if _, err := service.acquire(context.Background(), "overflow"); err != ErrDeepSeekBalanceBusy {
		t.Fatalf("overflow admission = %v, want bounded-capacity error", err)
	}
	releases[0]()
	release, err := service.acquire(context.Background(), "after-release")
	if err != nil {
		t.Fatalf("admission did not recover after release: %v", err)
	}
	release()
	for _, release := range releases[1:] {
		release()
	}
}

func TestDeepSeekBalanceObservationIsAuditedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{now: 1_800_000_000_000}
	path := filepath.Join(t.TempDir(), "deepseek-balance.db")
	st, err := sqlite.Open(path, sqlite.Config{Clock: clock, CommandBudget: fixtureCommandBudget})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatal(err)
	}
	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(`INSERT INTO principals(principal_id, display_name, enabled, created_at) VALUES ('p1', 'owner', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	_ = seed.Close()
	t.Cleanup(func() {
		if err := st.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	cipher, err := security.NewSecretCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := NewSecretCommands(st, cipher, clock, "test-key-v1")
	if err != nil {
		t.Fatal(err)
	}
	auth := domain.AuthContext{PrincipalID: "p1", Namespace: "ns1", Permissions: []domain.Permission{domain.PermConfigure}}
	const token = "sk-private-key-value"
	credential, err := secrets.Create(ctx, auth, "create-key", token)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	blockNext := atomic.Bool{}
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	service, err := NewDeepSeekBalanceVerification(st, secrets, balanceFetcherFunc(func(_ context.Context, key string) ([]byte, error) {
		calls.Add(1)
		if key != token {
			t.Fatalf("provider received unexpected key %q", key)
		}
		if blockNext.Swap(false) {
			close(fetchStarted)
			<-releaseFetch
		}
		return []byte(`{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"18.25","granted_balance":"3.25","topped_up_balance":"15.00"}]}`), nil
	}), clock)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Observe(ctx, auth, "observe-1", credential.Ref, credential.Revision)
	if err != nil || !first.IsAvailable || first.ObservedAt != clock.now || len(first.Balances) != 1 || first.Balances[0].TotalCents != 1825 {
		t.Fatalf("first observation = %#v, %v", first, err)
	}
	second, err := service.Observe(ctx, auth, "observe-1", credential.Ref, credential.Revision)
	if err != nil || second.ObservedAt != first.ObservedAt || calls.Load() != 1 {
		t.Fatalf("replay=%#v err=%v calls=%d", second, err, calls.Load())
	}
	service.flightsMu.Lock()
	for i := 0; i < deepSeekBalanceMaxInFlight; i++ {
		service.flights[fmt.Sprintf("occupied-%d", i)] = &balanceFlight{done: make(chan struct{})}
	}
	service.flightsMu.Unlock()
	replayedAtCapacity, err := service.Observe(ctx, auth, "observe-1", credential.Ref, credential.Revision)
	service.flightsMu.Lock()
	clear(service.flights)
	service.flightsMu.Unlock()
	if err != nil || replayedAtCapacity.ObservedAt != first.ObservedAt || calls.Load() != 1 {
		t.Fatalf("capacity blocked durable replay=%#v err=%v calls=%d", replayedAtCapacity, err, calls.Load())
	}
	loaded, err := service.Get(ctx, auth, "observe-1")
	if err != nil || loaded.ObservedAt != first.ObservedAt || calls.Load() != 1 {
		t.Fatalf("read persisted observation=%#v err=%v calls=%d", loaded, err, calls.Load())
	}
	if _, err := service.Get(ctx, domain.AuthContext{PrincipalID: "other", Namespace: "ns1", Permissions: auth.Permissions}, "observe-1"); err != ErrDeepSeekBalanceNotFound {
		t.Fatalf("foreign receipt read = %v", err)
	}
	if _, err := service.Observe(ctx, domain.AuthContext{PrincipalID: "other", Namespace: "ns1", Permissions: auth.Permissions}, "observe-other", credential.Ref, credential.Revision); err != ErrSecretNotFound {
		t.Fatalf("foreign credential = %v", err)
	}
	blockNext.Store(true)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Observe(ctx, auth, "observe-concurrent", credential.Ref, credential.Revision)
			results <- err
		}()
		if i == 0 {
			<-fetchStarted
		}
	}
	close(releaseFetch)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent replay failed: %v", err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("concurrent duplicate made %d total fetches, want 2 total (1 prior + 1 duplicate command)", calls.Load())
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var action string
	if err := db.QueryRow(`SELECT action FROM audit_log WHERE action = 'provider.deepseek.balance.observe'`).Scan(&action); err != nil || action == "" {
		t.Fatalf("missing audit row: %q %v", action, err)
	}
	var result []byte
	if err := db.QueryRow(`SELECT result_json FROM command_receipts WHERE kind = 'deepseek_balance_observe'`).Scan(&result); err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(result, &wire); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), token) || strings.Contains(string(result), "Authorization") {
		t.Fatalf("receipt leaked credential: %s", result)
	}
}
