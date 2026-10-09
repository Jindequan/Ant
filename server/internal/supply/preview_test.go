package supply

import (
	"context"
	"database/sql"
	"fmt"
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

// modelWindow is a plan window scoped to one model (MiniMax-style).
func modelWindow(ref, model, label string, leftPct int64, endsIn, now domain.Millis) observed.ResourceReading {
	r := planWindow(ref, label, leftPct, endsIn, now)
	r.ScopeJSON = []byte(`{"kind":"model","model":"` + model + `"}`)
	return r
}

// Design 13 §4.1: scope:model draws the meters of the offer's model only,
// matched canonically; a plain ref never draws another model's meter.
func TestRoutePreviewModelScopedMeters(t *testing.T) {
	src := &scriptedSource{results: map[domain.ID]Result{}}
	e, _, clock := newTestEngine(t, src, DefaultPolicy())
	now := clock.Now()
	offer := func(model string, draws ...string) observed.Offer {
		return observed.Offer{Endpoint: "https://api.example.com/v1", Protocol: "openai-chat", UpstreamModel: model, ModelID: observed.ModelID(model), Draws: draws}
	}
	src.creds = []Credential{found("k")}
	src.results["k"] = Result{Outcome: OutcomeOK, Inventory: &observed.Inventory{Packages: []observed.PackageReading{{TemplateRef: "mm/plan", Kind: "subscription", Basis: "observed",
		Resources: []observed.ResourceReading{
			modelWindow("window/text", "MiniMax/MiniMax-M3", "Text · 5 hours", 60, 3*hour+24*hour, now),
			modelWindow("window/speech", "speech-2.6", "Speech · Day", 0, 2*hour, now),
		}}}},
		Offers: []observed.Offer{
			offer("MiniMax-M3", observed.DrawModelScope),
			offer("speech-2.6", observed.DrawModelScope),
			offer("other-model", observed.DrawModelScope),
			offer("sneaky", "window/text"), // names a meter scoped to MiniMax-M3
		}}
	if err := e.RunNow(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for model, want := range map[string]string{
		"minimax-m3":  "use:Plan: Text · 5 hours 60% left", // its own window only, matched through the vendor prefix
		"speech-2.6":  "not:Speech · Day used up · resets in 2h",
		"other-model": "not:Nothing it draws on was read", // no grant or window of its own
		"sneaky":      "not:Nothing it draws on was read",
	} {
		p, err := e.Preview(context.Background(), model)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		switch {
		case len(p.Eligible) == 1 && len(p.Excluded) == 0:
			got = "use:" + p.Eligible[0].Reason
		case len(p.Excluded) == 1 && len(p.Eligible) == 0:
			got = "not:" + p.Excluded[0].Reason
		default:
			got = fmt.Sprintf("%+v", p)
		}
		if got != want {
			t.Errorf("%s: %s, want %s", model, got, want)
		}
	}
}

// grantPkg is a new-user free grant: one meter per model, in tokens, ending
// at its expiry (Bailian-style).
func grantPkg(now domain.Millis, grants ...observed.ResourceReading) observed.PackageReading {
	return observed.PackageReading{TemplateRef: "free/new-user", Kind: "free_grant", Basis: "observed", Resources: grants}
}

func grant(model string, left, limit int64, endsIn, now domain.Millis) observed.ResourceReading {
	end := now + endsIn
	return observed.ResourceReading{TemplateRef: "grant/" + model, Label: model, MetricID: "tokens", Unit: "tokens", QuantumExp: 0,
		SemanticsRef: "sem.entitlement-pool", SemanticsRevision: "semrev-1", WindowJSON: []byte(`{"kind":"lifetime"}`),
		ScopeJSON: []byte(`{"kind":"model","model":"` + model + `"}`), ResetBasis: "none", WindowEnd: &end, Limit: u(limit), Remaining: u(left)}
}

func cnyBalance(cents int64) observed.ResourceReading {
	return observed.ResourceReading{TemplateRef: "balance/cny", Label: "Balance", MetricID: "balance", Unit: "CNY", Currency: "CNY", QuantumExp: -2,
		SemanticsRef: "sem.entitlement-pool", SemanticsRevision: "semrev-1", WindowJSON: []byte(`{"kind":"lifetime"}`), ScopeJSON: []byte(`{"kind":"all"}`),
		ResetBasis: "none", Remaining: u(cents)}
}

// Design 13 §4.1: a free grant with room is used first (class free, the
// soonest-ending first, behind use-before-lost); used up, it is skipped when
// the route draws on anything else, and excludes the route when it doesn't.
func TestRoutePreviewFreeGrants(t *testing.T) {
	src := &scriptedSource{results: map[domain.ID]Result{}}
	e, _, clock := newTestEngine(t, src, DefaultPolicy())
	now := clock.Now()
	const day = 24 * hour
	usable := true
	add := func(id string, draws []string, pkgs ...observed.PackageReading) {
		src.creds = append(src.creds, found(id))
		src.results[domain.ID(id)] = Result{Outcome: OutcomeOK, Inventory: &observed.Inventory{Packages: pkgs},
			Offers: []observed.Offer{{Endpoint: "https://dashscope.example/v1", Protocol: "openai-chat", UpstreamModel: "qwen3-max", ModelID: "qwen3-max", Draws: draws}}}
	}
	scoped := []string{observed.DrawModelScope}
	withBalance := []string{observed.DrawModelScope, "balance/cny"}
	withPlan := []string{observed.DrawModelScope, "window/short", "window/week"}
	prepaid := observed.PackageReading{TemplateRef: "x/balance", Kind: "prepaid", Basis: "observed", Usable: &usable, Resources: []observed.ResourceReading{cnyBalance(2340)}}
	plan := func(leftPct int64) observed.PackageReading {
		return observed.PackageReading{TemplateRef: "x/plan", Kind: "subscription", Basis: "observed",
			Resources: []observed.ResourceReading{planWindow("window/short", "5 hours", leftPct, 4*day, now), planWindow("window/week", "Week", 90, 6*day, now)}}
	}

	add("grant-late", scoped, grantPkg(now, grant("qwen3-max", 820000, 1000000, 41*day, now), grant("qwen-plus", 5, 10, 2*day, now)))
	add("grant-soon", scoped, grantPkg(now, grant("Qwen/Qwen3-Max", 100, 1000, 10*day, now)))
	promo := grantPkg(now, grant("qwen3-max", 700, 1000, 20*day, now))
	promo.TemplateRef = "free/promotion"
	add("two-grants", scoped, grantPkg(now, grant("qwen3-max", 500, 1000, 30*day, now)), promo) // described and ordered by the one ending first
	add("lose", scoped, grantPkg(now, grant("qwen3-max", 500, 1000, 10*hour, now)))
	add("spent+balance", withBalance, grantPkg(now, grant("qwen3-max", 0, 1000000, 41*day, now)), prepaid)
	add("spent+plan", withPlan, grantPkg(now, grant("qwen3-max", 0, 1000000, 41*day, now)), plan(40))
	add("plan", []string{"window/short", "window/week"}, plan(50))
	add("spent", scoped, grantPkg(now, grant("qwen3-max", 0, 1000000, 41*day, now)))
	add("grant+balance", withBalance, grantPkg(now, grant("qwen3-max", 3000, 1000000, 60*day, now)), prepaid)
	if err := e.RunNow(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	p, err := e.Preview(context.Background(), "qwen3-max")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	reasons := map[string]string{}
	for _, c := range p.Eligible {
		order = append(order, string(c.CredentialID)+":"+c.Class)
		reasons[string(c.CredentialID)] = c.Reason
	}
	want := "lose:use_before_lost grant-soon:free two-grants:free grant-late:free grant+balance:free plan:plan spent+plan:plan spent+balance:prepaid"
	if strings.Join(order, " ") != want {
		t.Fatalf("order:\n got %s\nwant %s", strings.Join(order, " "), want)
	}
	for id, want := range map[string]string{
		"lose":          "Use before it's lost: qwen3-max 500 tokens left, ends in 10h",
		"grant-late":    "Free: qwen3-max 820,000 tokens left, ends in 41 days", // its own model's grant, not qwen-plus's (ending sooner)
		"grant-soon":    "Free: Qwen/Qwen3-Max 100 tokens left, ends in 10 days",
		"two-grants":    "Free: qwen3-max 700 tokens left, ends in 20 days",
		"grant+balance": "Free: qwen3-max 3,000 tokens left, ends in 60 days", // the grant first, the balance not touched
		"spent+balance": "Prepaid: 23.40 CNY left",
		"spent+plan":    "Plan: 5 hours 40% left, Week 90% left",
	} {
		if reasons[id] != want {
			t.Errorf("%s: %q, want %q", id, reasons[id], want)
		}
	}
	if len(p.Excluded) != 1 || p.Excluded[0].CredentialID != "spent" || p.Excluded[0].Reason != "qwen3-max used up" {
		t.Fatalf("excluded = %+v", p.Excluded)
	}
}

// A credential no longer found (removed by the user, signed out of the app
// that held it) is marked hidden in the store and offers nothing: what a
// router reads never includes an account that's gone. Found again, it's back.
func TestGoneCredentialsLeaveTheStore(t *testing.T) {
	src := &scriptedSource{results: map[domain.ID]Result{}}
	e, path, clock := newTestEngine(t, src, DefaultPolicy())
	now := clock.Now()
	src.creds = []Credential{found("kept"), found("removed")}
	src.results["kept"] = zaiResult("glm-5.3", planWindow("window/short", "5 hours", 50, 4*hour, now), planWindow("window/week", "Week", 50, 72*hour, now))
	src.results["removed"] = zaiResult("glm-5.3", planWindow("window/short", "5 hours", 90, 4*hour, now), planWindow("window/week", "Week", 90, 72*hour, now))
	if err := e.RunNow(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.Preview(context.Background(), "glm-5.3"); len(p.Eligible) != 2 {
		t.Fatalf("before: %+v", p)
	}
	src.creds = []Credential{found("kept")} // the user removed one
	if err := e.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, _ := e.Preview(context.Background(), "glm-5.3")
	if len(p.Eligible) != 1 || p.Eligible[0].CredentialID != "kept" || len(p.Excluded) != 0 {
		t.Fatalf("after removing: %+v", p)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status string
	var offers int
	_ = db.QueryRow(`SELECT status FROM supply_credentials WHERE credential_id='removed'`).Scan(&status)
	_ = db.QueryRow(`SELECT COUNT(*) FROM supply_offers WHERE credential_id='removed'`).Scan(&offers)
	if status != "hidden" || offers != 0 {
		t.Fatalf("removed credential: status %q, %d offers", status, offers)
	}
	// found again: read and offered again
	src.creds = []Credential{found("kept"), found("removed")}
	if err := e.RunNow(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.Preview(context.Background(), "glm-5.3"); len(p.Eligible) != 2 {
		t.Fatalf("found again: %+v", p)
	}
}
