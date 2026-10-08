package supply

import (
	"context"
	"strings"
	"sync"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
)

func u(v int64) *domain.Units { x := domain.Units(v); return &x }

func window(remaining int64) observed.ResourceReading {
	return observed.ResourceReading{TemplateRef: "5h", MetricID: "prompts", Unit: "count", QuantumExp: -6,
		SemanticsRef: "quota.adapter.window", SemanticsRevision: "v1",
		WindowJSON: []byte(`{"kind":"unmodeled","name":"5 hours"}`), ScopeJSON: []byte(`{"kind":"all"}`),
		ResetBasis: "reported", Limit: u(150_000_000), Remaining: u(remaining)}
}

func plan(basis string, resources ...observed.ResourceReading) observed.PackageReading {
	return observed.PackageReading{TemplateRef: "quota/zai/coding-plan", Kind: "subscription", Basis: basis, Plan: "Pro", Resources: resources}
}

// inventorySource answers each credential with its own inventory.
type inventorySource struct {
	mu    sync.Mutex
	creds []Credential
	inv   map[domain.ID]*observed.Inventory
}

func (s *inventorySource) Discover(context.Context) ([]Credential, error) { return s.creds, nil }
func (s *inventorySource) Collect(_ context.Context, c Credential) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Result{Outcome: OutcomeOK, Inventory: s.inv[c.ID]}
}
func (s *inventorySource) set(id domain.ID, inv *observed.Inventory) {
	s.mu.Lock()
	s.inv[id] = inv
	s.mu.Unlock()
}

func TestReconcileMergesOnlyOnReportedIdentity(t *testing.T) {
	src := &inventorySource{creds: []Credential{found("key-1"), found("key-2"), found("key-3")}, inv: map[domain.ID]*observed.Inventory{
		"key-1": {Identity: "instance-42", Packages: []observed.PackageReading{plan("observed", window(100_000_000))}},
		"key-2": {Identity: "instance-42", Packages: []observed.PackageReading{plan("observed", window(100_000_000))}},
		"key-3": {Packages: []observed.PackageReading{plan("observed", window(100_000_000))}}, // no reported identity
	}}
	e, path, clock := newTestEngine(t, src, DefaultPolicy())
	for _, id := range []domain.ID{"key-1", "key-2", "key-3"} {
		clock.now.Add(1000)
		if err := e.RunNow(context.Background(), []domain.ID{id}); err != nil {
			t.Fatal(err)
		}
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_accounts`); n != 2 {
		t.Fatalf("accounts = %d, want 2 (one reported identity, one credential-scoped)", n)
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(DISTINCT account_id) FROM supply_credentials WHERE credential_id IN ('key-1','key-2')`); n != 1 {
		t.Fatal("two keys of one reported account are not one account")
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_accounts WHERE provider_identity='credential:key-3'`); n != 1 {
		t.Fatal("a credential without a reported identity was merged or lost")
	}
	// the merged account's meter is one resource; the second key's equal value adds no reading
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_resources`); n != 2 {
		t.Fatalf("resources = %d, want 2", n)
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_readings`); n != 2 {
		t.Fatalf("readings = %d, want 2", n)
	}
}

func TestReconcileAppendsOnlyChangesAndNeverReadsDeclared(t *testing.T) {
	declared := observed.PackageReading{TemplateRef: "quota/figma/starter", Kind: "free_grant", Basis: "declared", Resources: []observed.ResourceReading{window(5)}}
	src := &inventorySource{creds: []Credential{found("k")}, inv: map[domain.ID]*observed.Inventory{}}
	e, path, clock := newTestEngine(t, src, DefaultPolicy())
	for _, remaining := range []int64{100, 100, 90} {
		src.set("k", &observed.Inventory{Identity: "acct", Packages: []observed.PackageReading{plan("observed", window(remaining)), declared}})
		clock.now.Add(1000)
		if err := e.RunNow(context.Background(), []domain.ID{"k"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_readings`); n != 2 {
		t.Fatalf("readings = %d, want 2 (100, then 90)", n)
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_readings r JOIN supply_resources s USING(namespace,resource_id) JOIN supply_packages p USING(namespace,package_id) WHERE p.basis='declared'`); n != 0 {
		t.Fatal("a declared package got a reading")
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_packages WHERE basis='declared' AND live=0`); n != 1 {
		t.Fatal("declared package not recorded as declared")
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM collector_runs WHERE outcome='ok'`); n != 3 {
		t.Fatal("runs not recorded")
	}
}

// Two reads in the same millisecond: the second's reading is skipped and
// the run says so, without failing.
func TestReconcileSkipsAReadingThatIsNotNewer(t *testing.T) {
	src := &inventorySource{creds: []Credential{found("k")}, inv: map[domain.ID]*observed.Inventory{}}
	e, path, _ := newTestEngine(t, src, DefaultPolicy())
	for _, remaining := range []int64{100, 90} {
		src.set("k", &observed.Inventory{Identity: "acct", Packages: []observed.PackageReading{plan("observed", window(remaining))}})
		if err := e.RunNow(context.Background(), []domain.ID{"k"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_readings`); n != 1 {
		t.Fatalf("readings = %d, want 1", n)
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM collector_runs WHERE outcome='ok' AND detail LIKE '%not newer%skipped%'`); n != 1 {
		t.Fatal("the skip is not noted on an ok run")
	}
}

// A read whose inventory can't be recorded (here: a meter changing its unit)
// still closes its run, as an error saying why; the stored facts stay.
func TestReconcileFailureClosesTheRun(t *testing.T) {
	src := &inventorySource{creds: []Credential{found("k")}, inv: map[domain.ID]*observed.Inventory{}}
	e, path, clock := newTestEngine(t, src, DefaultPolicy())
	src.set("k", &observed.Inventory{Identity: "acct", Packages: []observed.PackageReading{plan("observed", window(100))}})
	if err := e.RunNow(context.Background(), []domain.ID{"k"}); err != nil {
		t.Fatal(err)
	}
	changed := window(50)
	changed.Unit = "token"
	src.set("k", &observed.Inventory{Identity: "acct", Packages: []observed.PackageReading{plan("observed", changed)}})
	clock.now.Add(1000)
	if err := e.RunNow(context.Background(), []domain.ID{"k"}); err != nil {
		t.Fatal(err)
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM collector_runs WHERE outcome='error' AND detail LIKE 'the read could not be recorded:%immutable identity%'`); n != 1 {
		t.Fatal("the failed recording is not on its run")
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM collector_runs WHERE finished_at IS NULL`); n != 0 {
		t.Fatal("a run was left open")
	}
	if n := rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_resources WHERE unit='count'`); n != 1 || rawQueryInt(t, path, `SELECT COUNT(*) FROM supply_readings`) != 1 {
		t.Fatal("stored facts changed")
	}
}

func TestPhysicalKeyIgnoresNamespaceAndAntIDs(t *testing.T) {
	a := stableID("pk-", "zai", "instance-42", "quota/zai/coding-plan", "5h", `{"kind":"all"}`)
	b := stableID("pk-", "zai", "instance-42", "quota/zai/coding-plan", "5h", `{"kind":"all"}`)
	c := stableID("pk-", "zai", "instance-43", "quota/zai/coding-plan", "5h", `{"kind":"all"}`)
	if a != b || a == c || !strings.HasPrefix(string(a), "pk-") {
		t.Fatalf("keys %s %s %s", a, b, c)
	}
}
