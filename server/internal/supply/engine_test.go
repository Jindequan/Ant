package supply

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/store/sqlite"
)

type fixedClock struct{ now atomic.Int64 }

func (c *fixedClock) Now() domain.Millis { return domain.Millis(c.now.Load()) }

// fakeSource returns creds and answers each Collect with result; when gate
// is set, every Collect waits on it.
type fakeSource struct {
	mu       sync.Mutex
	creds    []Credential
	result   Result
	gate     chan struct{}
	calls    map[domain.ID]int
	running  atomic.Int32
	maxSeen  atomic.Int32
	collects atomic.Int32
}

func (f *fakeSource) Discover(context.Context) ([]Credential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Credential(nil), f.creds...), nil
}

func (f *fakeSource) Collect(ctx context.Context, c Credential) Result {
	n := f.running.Add(1)
	defer f.running.Add(-1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[domain.ID]int{}
	}
	f.calls[c.ID]++
	gate, r := f.gate, f.result
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	f.collects.Add(1)
	return r
}

func found(id string) Credential {
	return Credential{ID: domain.ID(id), ProviderID: "zai", Kind: "api_key", Origin: OriginFound, Strategy: "quota/env", Locator: []byte(`{"source":"$ZAI_API_KEY"}`)}
}

func newTestEngine(t *testing.T, src Source, p Policy) (*Engine, string, *fixedClock) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ant.db")
	clock := &fixedClock{}
	clock.now.Store(1_000_000)
	st, err := sqlite.Open(path, sqlite.Config{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	return NewEngine("local", st, src, clock, p, nil), path, clock
}

func rawQueryInt(t *testing.T, path, q string, args ...any) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPolicyNextDue(t *testing.T) {
	p := DefaultPolicy()
	now := domain.Millis(10_000_000)
	soon := now + ms(10*time.Minute)
	later := now + ms(2*time.Hour)
	cases := []struct {
		name     string
		r        Result
		failures int
		want     time.Duration
	}{
		{"ok", Result{Outcome: OutcomeOK}, 0, 5 * time.Minute},
		{"ok, a window resets soon", Result{Outcome: OutcomeOK, NextReset: &soon}, 0, time.Minute},
		{"ok, the next reset is far", Result{Outcome: OutcomeOK, NextReset: &later}, 0, 5 * time.Minute},
		{"ok, balances only", Result{Outcome: OutcomeOK, BalanceOnly: true}, 0, 30 * time.Minute},
		{"signed out", Result{Outcome: OutcomeSignedOut}, 1, time.Hour},
		{"first error", Result{Outcome: OutcomeError}, 1, 5 * time.Minute},
		{"third error", Result{Outcome: OutcomeError}, 3, 20 * time.Minute},
		{"many errors stop at the cap", Result{Outcome: OutcomeError}, 30, time.Hour},
	}
	for _, c := range cases {
		if got := p.NextDue(now, c.r, c.failures); got != now+ms(c.want) {
			t.Errorf("%s: due in %v, want %v", c.name, time.Duration(got-now)*time.Millisecond, c.want)
		}
	}
}

// Found credentials are recorded with their run and status; added ones are
// read but not recorded until the accounts.json import.
func TestRunNowRecordsFoundCredentialsOnly(t *testing.T) {
	added := Credential{ID: "added-1", ProviderID: "deepseek", Kind: "api_key", Origin: OriginAdded, Strategy: "quota/api", Locator: []byte(`{}`)}
	src := &fakeSource{creds: []Credential{found("found-1"), added}, result: Result{Outcome: OutcomeOK}}
	e, path, _ := newTestEngine(t, src, DefaultPolicy())
	if err := e.RunNow(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if src.calls["found-1"] != 1 || src.calls["added-1"] != 1 {
		t.Fatalf("collect calls = %v, want one each", src.calls)
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_credentials WHERE credential_id='found-1' AND status='ok' AND verified_at IS NOT NULL AND locator_json='{"source":"$ZAI_API_KEY"}'`); n != 1 {
		t.Fatal("found credential not recorded as verified")
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM collector_runs WHERE credential_id='found-1' AND outcome='ok' AND finished_at>=started_at`); n != 1 {
		t.Fatal("found credential's run not recorded")
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_credentials WHERE credential_id='added-1'`); n != 0 {
		t.Fatal("added credential recorded before the import")
	}
}

func TestFailuresAreRecordedAndClipped(t *testing.T) {
	long := make([]byte, 2000)
	for i := range long {
		long[i] = 'x'
	}
	src := &fakeSource{creds: []Credential{found("f")}, result: Result{Outcome: "weird", Detail: string(long)}}
	e, path, _ := newTestEngine(t, src, DefaultPolicy())
	if err := e.RunNow(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM collector_runs WHERE outcome='error' AND length(detail)=?`, detailLimit); n != 1 {
		t.Fatal("unknown outcome not recorded as a clipped error")
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_credentials WHERE status='error' AND verified_at IS NULL`); n != 1 {
		t.Fatal("credential status not error")
	}
}

func TestOneReadPerCredentialAtATime(t *testing.T) {
	gate := make(chan struct{})
	src := &fakeSource{creds: []Credential{found("f")}, result: Result{Outcome: OutcomeOK}, gate: gate}
	e, _, _ := newTestEngine(t, src, DefaultPolicy())
	if err := e.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.RunNow(context.Background(), []domain.ID{"f"})
		}()
	}
	waitFor(t, func() bool { return src.running.Load() == 1 })
	time.Sleep(20 * time.Millisecond) // give the other calls time to (wrongly) start a read
	close(gate)
	wg.Wait()
	if src.calls["f"] != 1 {
		t.Fatalf("concurrent RunNow read %d times, want 1", src.calls["f"])
	}
}

func TestReadsAreCappedAcrossCredentials(t *testing.T) {
	gate := make(chan struct{})
	var creds []Credential
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		creds = append(creds, found(id))
	}
	p := DefaultPolicy()
	p.MaxInFlight = 2
	src := &fakeSource{creds: creds, result: Result{Outcome: OutcomeOK}, gate: gate}
	e, _, _ := newTestEngine(t, src, p)
	done := make(chan struct{})
	go func() { _ = e.RunNow(context.Background(), nil); close(done) }()
	waitFor(t, func() bool { return src.running.Load() == 2 })
	time.Sleep(20 * time.Millisecond)
	if n := src.running.Load(); n != 2 {
		t.Fatalf("%d reads running, want the cap of 2", n)
	}
	close(gate)
	<-done
	if src.maxSeen.Load() != 2 || src.collects.Load() != 5 {
		t.Fatalf("max concurrent %d, collects %d; want 2 and 5", src.maxSeen.Load(), src.collects.Load())
	}
}

func TestRediscoveryKeepsSchedulesAndDropsVanished(t *testing.T) {
	src := &fakeSource{creds: []Credential{found("keep"), found("gone")}, result: Result{Outcome: OutcomeOK}}
	e, _, clock := newTestEngine(t, src, DefaultPolicy())
	if err := e.RunNow(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	src.mu.Lock()
	src.creds = []Credential{found("keep"), found("new")}
	src.mu.Unlock()
	clock.now.Add(1000)
	if err := e.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.slots["gone"]; ok {
		t.Fatal("a vanished credential is still scheduled")
	}
	if e.slots["keep"].due <= clock.Now() || e.slots["keep"].status != OutcomeOK {
		t.Fatalf("known credential lost its schedule: %+v", e.slots["keep"])
	}
	if e.slots["new"].due > clock.Now() {
		t.Fatal("a new credential is not due now")
	}
}

func TestStartReadsDueCredentialsAndStops(t *testing.T) {
	src := &fakeSource{creds: []Credential{found("a"), found("b")}, result: Result{Outcome: OutcomeOK}}
	e, _, _ := newTestEngine(t, src, DefaultPolicy())
	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx)
	waitFor(t, func() bool { return src.collects.Load() == 2 })
	cancel()
	e.Wait()
	if src.calls["a"] != 1 || src.calls["b"] != 1 {
		t.Fatalf("calls = %v; each credential is read once, then not before it is due", src.calls)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}
