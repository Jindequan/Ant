package supply

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/store"
)

// scriptedSource answers each credential with a prepared result.
type scriptedSource struct {
	mu      sync.Mutex
	creds   []Credential
	results map[domain.ID]Result
}

func (s *scriptedSource) Discover(context.Context) ([]Credential, error) { return s.creds, nil }
func (s *scriptedSource) Collect(_ context.Context, c Credential) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.results[c.ID]
}

const hour = 60 * minute

func planWindow(ref, label string, leftPct int64, endsIn domain.Millis, now domain.Millis) observed.ResourceReading {
	end := now + endsIn
	return observed.ResourceReading{TemplateRef: ref, Label: label, MetricID: "plan.allowance", Unit: "percent", QuantumExp: -2,
		SemanticsRef: "zai.plan-window", SemanticsRevision: "v1", WindowJSON: []byte(`{"kind":"reported","name":"` + label + `"}`),
		ScopeJSON: []byte(`{"kind":"all"}`), ResetBasis: "reported", WindowEnd: &end, Limit: u(10000), Remaining: u(leftPct * 100)}
}

func zaiResult(model string, short, week observed.ResourceReading) Result {
	return Result{Outcome: OutcomeOK, Inventory: &observed.Inventory{Packages: []observed.PackageReading{{TemplateRef: "zai/coding-plan", Kind: "subscription", Basis: "observed", Plan: "Pro",
		Resources: []observed.ResourceReading{short, week}}}},
		Offers: []observed.Offer{{Endpoint: "https://api.z.ai/api/coding/paas/v4", Protocol: "openai-chat", UpstreamModel: model, ModelID: model, Draws: []string{"window/short", "window/week"}}}}
}

func balanceResult(cents int64, usable bool) Result {
	return Result{Outcome: OutcomeOK, Inventory: &observed.Inventory{Packages: []observed.PackageReading{{TemplateRef: "deepseek/balance", Kind: "prepaid", Basis: "observed", Usable: &usable,
		Resources: []observed.ResourceReading{{TemplateRef: "balance/cny", Label: "Balance", MetricID: "balance", Unit: "CNY", Currency: "CNY", QuantumExp: -2,
			SemanticsRef: "sem.entitlement-pool", SemanticsRevision: "semrev-1", WindowJSON: []byte(`{"kind":"lifetime"}`), ScopeJSON: []byte(`{"kind":"all"}`),
			ResetBasis: "none", Remaining: u(cents)}}}}},
		Offers: []observed.Offer{{Endpoint: "https://api.deepseek.com", Protocol: "openai-chat", UpstreamModel: "deepseek-v4", ModelID: "deepseek-v4", Draws: []string{"balance/cny", "balance/usd"}}}}
}

func TestRoutePreview(t *testing.T) {
	src := &scriptedSource{results: map[domain.ID]Result{}}
	e, _, clock := newTestEngine(t, src, DefaultPolicy())
	now := clock.Now()
	add := func(id string, r Result) {
		src.creds = append(src.creds, found(id))
		src.results[domain.ID(id)] = r
	}
	// the stale one is read first; the clock then moves on two hours
	add("plan-stale", zaiResult("glm-5.3", planWindow("window/short", "5 hours", 70, 30*hour, now), planWindow("window/week", "Week", 40, 120*hour, now)))
	if err := e.RunNow(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	clock.now.Add(int64(2 * hour))
	now = clock.Now()
	add("lose", zaiResult("glm-5.3", planWindow("window/short", "5 hours", 80, 2*hour, now), planWindow("window/week", "Week", 60, 72*hour, now)))
	add("plan-tight", zaiResult("glm-5.3", planWindow("window/short", "5 hours", 20, 2*hour, now), planWindow("window/week", "Week", 90, 72*hour, now)))
	add("used-up", zaiResult("glm-5.3", planWindow("window/short", "5 hours", 0, 2*hour, now), planWindow("window/week", "Week", 90, 72*hour, now)))
	add("paused", zaiResult("glm-5.3", planWindow("window/short", "5 hours", 90, 2*hour, now), planWindow("window/week", "Week", 90, 72*hour, now)))
	add("signed-out", Result{Outcome: OutcomeSignedOut, Detail: "The key was refused."})
	add("no-plan", Result{Outcome: OutcomeOK, Inventory: &observed.Inventory{Packages: []observed.PackageReading{{TemplateRef: "zai/pay-as-you-go", Kind: "payg", Basis: "observed"}}},
		Offers: []observed.Offer{{Endpoint: "https://api.z.ai/api/coding/paas/v4", Protocol: "openai-chat", UpstreamModel: "GLM-5.3", ModelID: "glm-5.3", Draws: []string{"window/short", "window/week"}}}})
	add("ds-ok", balanceResult(2340, true))
	add("ds-low", balanceResult(1, false))
	ids := []domain.ID{"lose", "plan-tight", "used-up", "paused", "no-plan", "ds-ok", "ds-low"}
	if err := e.RunNow(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	// signed out: the credential is recorded by discovery, then refused
	if err := e.RunNow(context.Background(), []domain.ID{"signed-out"}); err != nil {
		t.Fatal(err)
	}
	if err := e.write(context.Background(), "pause", func(tx store.SupplyWriterTx) error {
		_, err := tx.SetSupplyPaused("local", []domain.ID{"paused"}, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	p, err := e.Preview(context.Background(), "glm-5.3")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range p.Eligible {
		order = append(order, string(c.CredentialID)+":"+c.Class)
	}
	want := "lose:use_before_lost plan-stale:plan plan-tight:plan no-plan:payg"
	if strings.Join(order, " ") != want {
		t.Fatalf("order:\n got %s\nwant %s", strings.Join(order, " "), want)
	}
	reasons := map[string]string{}
	for _, c := range p.Eligible {
		reasons[string(c.CredentialID)] = c.Reason + "|" + c.Stale
	}
	for _, x := range p.Excluded {
		reasons[string(x.CredentialID)] = x.Reason
	}
	for id, want := range map[string]string{
		"lose":       "Use before it's lost: 5 hours 80% left, resets in 2h|",
		"plan-stale": "Plan: 5 hours 70% left, Week 40% left|last checked 2h ago",
		"plan-tight": "Plan: 5 hours 20% left, Week 90% left|",
		"no-plan":    "Pay-as-you-go: no plan on this key, billed per call|",
		"used-up":    "5 hours used up · resets in 2h",
		"paused":     "Paused by you",
	} {
		if reasons[id] != want {
			t.Errorf("%s: %q, want %q", id, reasons[id], want)
		}
	}
	if len(p.Excluded) != 2 {
		t.Errorf("excluded = %+v; signed-out has no glm offer recorded (never read), so only used-up and paused", p.Excluded)
	}

	ds, err := e.Preview(context.Background(), "deepseek-v4")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Eligible) != 1 || ds.Eligible[0].CredentialID != "ds-ok" || ds.Eligible[0].Class != ClassPrepaid || ds.Eligible[0].Reason != "Prepaid: 23.40 CNY left" ||
		len(ds.Excluded) != 1 || ds.Excluded[0].Reason != "Balance too low to call" {
		t.Fatalf("deepseek preview = %+v", ds)
	}
	if none, err := e.Preview(context.Background(), "nobody-serves-this"); err != nil || len(none.Eligible)+len(none.Excluded) != 0 {
		t.Fatalf("unknown model = %+v %v", none, err)
	}
}

// A credential refused after its offers were recorded is excluded with why.
func TestRoutePreviewSignedOutAfterReading(t *testing.T) {
	src := &scriptedSource{results: map[domain.ID]Result{}}
	e, _, clock := newTestEngine(t, src, DefaultPolicy())
	now := clock.Now()
	src.creds = []Credential{found("k")}
	src.results["k"] = zaiResult("glm-5.3", planWindow("window/short", "5 hours", 50, 4*hour, now), planWindow("window/week", "Week", 50, 72*hour, now))
	if err := e.RunNow(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	src.results["k"] = Result{Outcome: OutcomeSignedOut, Detail: "The key was refused."}
	clock.now.Add(int64(time.Minute))
	if err := e.RunNow(context.Background(), []domain.ID{"k"}); err != nil {
		t.Fatal(err)
	}
	p, err := e.Preview(context.Background(), "glm-5.3")
	if err != nil || len(p.Excluded) != 1 || p.Excluded[0].Reason != "Signed out: The key was refused." {
		t.Fatalf("preview = %+v %v", p, err)
	}
}

// Balances: the one that lasts longer at its recent pace comes first; one
// with no spending lasts longest.
func TestRoutePreviewOrdersBalancesByHowLongTheyLast(t *testing.T) {
	src := &scriptedSource{results: map[domain.ID]Result{}}
	e, _, clock := newTestEngine(t, src, DefaultPolicy())
	src.creds = []Credential{found("fast"), found("slow"), found("idle")}
	for step, amounts := range [][3]int64{{10000, 10000, 500}, {6000, 9000, 500}} { // fast spends 40/2h, slow 10/2h
		src.results["fast"], src.results["slow"], src.results["idle"] = balanceResult(amounts[0], true), balanceResult(amounts[1], true), balanceResult(amounts[2], true)
		if step > 0 {
			clock.now.Add(int64(2 * hour))
		}
		if err := e.RunNow(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	p, err := e.Preview(context.Background(), "deepseek-v4")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range p.Eligible {
		order = append(order, string(c.CredentialID))
	}
	if strings.Join(order, " ") != "idle slow fast" || !strings.Contains(p.Eligible[2].Reason, "lasts ~") {
		t.Fatalf("order %v, reasons %+v", order, p.Eligible)
	}
}

// With one drawn meter used up and another only declared, the reason is
// the used-up one: design 12 §5 orders "used up" before "declared only",
// whatever order the meters come in.
func TestRoutePreviewReasonOrderAcrossMeters(t *testing.T) {
	src := &scriptedSource{results: map[domain.ID]Result{}}
	e, _, clock := newTestEngine(t, src, DefaultPolicy())
	now := clock.Now()
	declared := planWindow("window/week", "Week", 90, 72*hour, now)
	r := zaiResult("glm-5.3", planWindow("window/short", "5 hours", 0, 2*hour, now), declared)
	// the week comes from a package the user declared: listed first, never read
	inv := r.Inventory
	inv.Packages = []observed.PackageReading{
		{TemplateRef: "zai/declared", Kind: "subscription", Basis: "declared", Resources: []observed.ResourceReading{declared}},
		{TemplateRef: "zai/coding-plan", Kind: "subscription", Basis: "observed", Resources: []observed.ResourceReading{planWindow("window/short", "5 hours", 0, 2*hour, now)}},
	}
	r.Offers[0].Draws = []string{"window/week", "window/short"} // the declared meter is judged first
	src.creds = []Credential{found("k")}
	src.results["k"] = r
	if err := e.RunNow(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	p, err := e.Preview(context.Background(), "glm-5.3")
	if err != nil || len(p.Excluded) != 1 || p.Excluded[0].Reason != "5 hours used up · resets in 2h" {
		t.Fatalf("preview = %+v %v", p, err)
	}
}
