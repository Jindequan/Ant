package quotasource

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
	"ant/server/internal/quota"
	"ant/server/internal/supply"
)

// recorder is a module collector that remembers the sessions it read.
type recorder struct {
	mu   sync.Mutex
	seen []catalog.Session
	inv  observed.Inventory
}

func (r *recorder) Inventory(_ context.Context, s catalog.Session, _ observed.Fetch) (observed.Inventory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, s)
	return r.inv, nil
}

func (r *recorder) Models(context.Context, catalog.Session, observed.Fetch) ([]observed.UpstreamModel, error) {
	return nil, nil
}

// Design 13 §5.1: the module reads only the sessions holding what it
// requires, with values resolved at read time and the engine's clock; a
// session it doesn't read stays with the quota reader; further packages
// are the card's groups.
func TestNativeModuleReadsOnlyWhatItRequires(t *testing.T) {
	cliToken := "token-1" // what the CLI holds now; resolved at each read
	quotaReads := map[string]int{}
	var mu sync.Mutex
	read := func(_ context.Context, l quota.Local, c quota.Connection) quota.Account {
		mu.Lock()
		quotaReads[c.ID]++
		mu.Unlock()
		return quota.Account{ID: c.ID, Provider: "fake", Mode: c.Mode, Status: quota.StatusOK, ReadAt: l.Now()}
	}
	conn := func(id, way string, v map[string]string) quota.Connection {
		return quota.Connection{ID: id, Provider: "fake", Mode: "plan", Way: way, Origin: quota.OriginFound, Source: "test", Values: v}
	}
	providers := []quota.Provider{{ID: "fake", Name: "Fake", Modes: []quota.Mode{{ID: "plan", Name: "Plan", Kind: quota.KindSubscription, Live: true, Ways: []quota.Way{
		{ID: "cli", Name: "CLI", Read: read,
			Discover: func(quota.Local) []quota.Connection {
				return []quota.Connection{conn("fake-cli", "cli", map[string]string{"config": "path"})}
			},
			Resolve: func(quota.Local, quota.Connection) (map[string]string, error) {
				return map[string]string{"token": cliToken}, nil
			}},
		{ID: "browser", Name: "Browser", Read: read,
			Discover: func(quota.Local) []quota.Connection {
				return []quota.Connection{conn("fake-browser", "browser", map[string]string{"session": "cookies"})}
			}},
	}}}}}
	rec := &recorder{inv: observed.Inventory{Packages: []observed.PackageReading{
		{TemplateRef: "fake/plan", Kind: "subscription", Basis: "observed", Plan: "Pro", Resources: []observed.ResourceReading{
			{TemplateRef: "window/month", Label: "Month", MetricID: "m", Unit: "percent", QuantumExp: -2, SemanticsRef: "s", SemanticsRevision: "v1",
				WindowJSON: []byte(`{"kind":"reported","name":"Month"}`), ScopeJSON: []byte(`{"kind":"all"}`), ResetBasis: "none", Used: n(1000), Limit: n(10000), Remaining: n(9000)}}},
		{TemplateRef: "fake/tools", Kind: "subscription", Basis: "observed", Plan: "Tool benefits", Resources: []observed.ResourceReading{
			{TemplateRef: "tool/search", Label: "Web search", Aside: true, MetricID: "calls", Unit: "calls", QuantumExp: 0, SemanticsRef: "s", SemanticsRevision: "v1",
				WindowJSON: []byte(`{"kind":"lifetime"}`), ScopeJSON: []byte(`{"kind":"all"}`), ResetBasis: "none", Limit: n(100), Remaining: n(80)}}},
	}}}
	m := catalog.Module{ID: "fake", Name: "Fake", Supply: &catalog.Supply{Collector: rec, Requires: map[string][]string{"plan": {"token"}}}}
	at := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	local := quota.Local{Home: t.TempDir(), Getenv: func(string) string { return "" }, HTTP: &http.Client{Transport: loopbackOnly{}}, Now: func() time.Time { return at }, URLs: map[string]string{}}
	src := New(local, quota.NewStore(""), providers, Native{Module: m})
	creds, err := src.Discover(context.Background())
	if err != nil || len(creds) != 2 {
		t.Fatalf("credentials = %+v %v", creds, err)
	}
	results := map[domain.ID]supply.Result{}
	for _, c := range creds {
		results[c.ID] = src.Collect(context.Background(), c)
	}
	// the CLI session holds a token once resolved: read by the module, with
	// the resolved values and the engine's clock
	if len(rec.seen) != 1 || rec.seen[0].Values["token"] != "token-1" || rec.seen[0].Values["config"] != "" || !rec.seen[0].Now.Equal(at) || rec.seen[0].Product != "plan" {
		t.Fatalf("module sessions = %+v", rec.seen)
	}
	if quotaReads["fake-browser"] != 1 || quotaReads["fake-cli"] != 0 {
		t.Fatalf("quota reads = %v: the browser session stays with the quota reader, the CLI one doesn't", quotaReads)
	}
	if r := results["fake-cli"]; r.Outcome != supply.OutcomeOK || r.Inventory == nil {
		t.Fatalf("cli result = %+v", r)
	}
	card := src.latest["fake-cli"]
	if card.Plan != "Pro" || len(card.Windows) != 1 || card.Windows[0].Name != "Month" || len(card.Groups) != 1 ||
		card.Groups[0].Title != "Tool benefits" || len(card.Groups[0].Windows) != 1 || *card.Groups[0].Windows[0].Left != 80 || !card.Groups[0].Windows[0].Aside {
		t.Fatalf("card = %+v", card)
	}
}

// When the app holding the values has signed out, the module isn't asked
// and the card says so.
func TestNativeModuleResolveSignedOut(t *testing.T) {
	read := func(_ context.Context, l quota.Local, c quota.Connection) quota.Account {
		t.Error("the quota reader shouldn't read a module's provider here")
		return quota.Account{}
	}
	providers := []quota.Provider{{ID: "fake", Name: "Fake", Modes: []quota.Mode{{ID: "plan", Name: "Plan", Kind: quota.KindSubscription, Live: true, Ways: []quota.Way{
		{ID: "cli", Name: "CLI", Read: read,
			Discover: func(quota.Local) []quota.Connection {
				return []quota.Connection{{ID: "fake-cli", Provider: "fake", Mode: "plan", Way: "cli", Origin: quota.OriginFound, Values: map[string]string{"config": "p"}}}
			},
			Resolve: func(quota.Local, quota.Connection) (map[string]string, error) {
				return nil, errors.Join(quota.ErrSignedOut, errors.New("x"))
			}},
	}}}}}
	rec := &recorder{}
	m := catalog.Module{ID: "fake", Name: "Fake", Supply: &catalog.Supply{Collector: rec, Requires: map[string][]string{"plan": {"token"}}}}
	local := quota.Local{Home: t.TempDir(), Getenv: func(string) string { return "" }, HTTP: &http.Client{Transport: loopbackOnly{}}, Now: time.Now, URLs: map[string]string{}}
	src := New(local, quota.NewStore(""), providers, Native{Module: m})
	creds, _ := src.Discover(context.Background())
	r := src.Collect(context.Background(), creds[0])
	if r.Outcome != supply.OutcomeSignedOut || len(rec.seen) != 0 || src.latest["fake-cli"].Status != quota.StatusSignedOut {
		t.Fatalf("result %+v, module sessions %d, card %+v", r, len(rec.seen), src.latest["fake-cli"])
	}
}

func n(v int64) *domain.Units {
	x := domain.Units(v)
	return &x
}
