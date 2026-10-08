package kimi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
	"ant/server/internal/transport"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type fake struct {
	code, cn, ai *httptest.Server
	hits         atomic.Int64
	seen         atomic.Value // "host path auth"
}

// newFake runs Kimi Code and the China platform on one server and the
// international platform on another, so a read that goes to the wrong
// region is caught.
func newFake(t *testing.T) (*fake, catalog.Module, observed.Fetch) {
	t.Helper()
	f := &fake{}
	handler := func(region string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.hits.Add(1)
			auth := r.Header.Get("Authorization")
			f.seen.Store(region + " " + r.URL.Path + " " + auth)
			key := strings.TrimPrefix(auth, "Bearer ")
			switch {
			case key == "refused":
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"Invalid Authentication","type":"invalid_authentication_error"}}`))
			case r.URL.Path == "/coding/v1/usages" && region == "cn":
				_, _ = w.Write(fixture(t, "usages.json"))
			case r.URL.Path == "/coding/v1/models" && region == "cn":
				_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"kimi-for-coding"}]}`))
			case r.URL.Path == "/v1/users/me/balance":
				_, _ = w.Write(fixture(t, key+".json"))
			case r.URL.Path == "/v1/models":
				_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"kimi-k3"},{"id":"moonshot-v1-8k"}]}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}
	}
	f.cn = httptest.NewTLSServer(handler("cn"))
	f.ai = httptest.NewTLSServer(handler("ai"))
	t.Cleanup(f.cn.Close)
	t.Cleanup(f.ai.Close)
	f.code = f.cn
	m := Module(f.code.URL, f.cn.URL, f.ai.URL)
	fetch, err := transport.NewObservationFetch(m.Supply.Origins, f.cn.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	return f, m, fetch
}

func session(product, key, region string) catalog.Session {
	v := map[string]string{"apiKey": key}
	if region != "" {
		v["region"] = region
	}
	return catalog.Session{Product: product, Values: v}
}

func ms(s string) domain.Millis {
	t, _ := time.Parse(time.RFC3339, s)
	return domain.Millis(t.UnixMilli())
}

func TestCodeWindows(t *testing.T) {
	f, m, fetch := newFake(t)
	inv, err := m.Supply.Collector.Inventory(context.Background(), session(ProductCode, "sk-kimi-1", ""), fetch)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.seen.Load(); got != "cn /coding/v1/usages Bearer sk-kimi-1" {
		t.Fatalf("request %v", got)
	}
	if len(inv.Packages) != 1 || inv.Packages[0].Kind != "subscription" || inv.Packages[0].Plan != "Kimi Code" || len(inv.Packages[0].Resources) != 2 {
		t.Fatalf("inventory = %+v", inv)
	}
	short, week := inv.Packages[0].Resources[0], inv.Packages[0].Resources[1]
	// limits[0]: 20 allowed, 15 remaining → 25% used; usage: 25 of 100
	if short.TemplateRef != MeterShort || short.Label != "5 hours" || *short.Used != 2500 || *short.Remaining != 7500 || *short.Limit != 10000 ||
		short.ResetBasis != "reported" || *short.WindowEnd != ms("2026-10-07T20:00:00Z") || string(short.Evidence) != `{"limit":"20","source":"observed: /coding/v1/usages","used":"5"}` {
		t.Fatalf("short = %+v evidence %s", short, short.Evidence)
	}
	if week.TemplateRef != MeterWeek || week.Label != "Week" || *week.Used != 2500 || *week.WindowEnd != ms("2026-10-12T00:00:00Z") {
		t.Fatalf("week = %+v", week)
	}
	models, err := m.Supply.Collector.Models(context.Background(), session(ProductCode, "sk-kimi-1", ""), fetch)
	if err != nil || len(models) != 1 || models[0].ID != "kimi-for-coding" {
		t.Fatalf("models = %+v %v", models, err)
	}
}

// Several shorter windows: each its own meter, all drawn by a call.
func TestCodeShortWindowsAreNumbered(t *testing.T) {
	ws, err := ParseUsages([]byte(`{"limits":[
		{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":"20","remaining":"20"}},
		{"window":{"duration":1,"timeUnit":"TIME_UNIT_DAY"},"detail":{"limit":50,"used":10}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	inv := codeInventory(ws)
	var refs []string
	for _, r := range inv.Packages[0].Resources {
		refs = append(refs, r.TemplateRef+"="+r.Label)
	}
	if strings.Join(refs, " ") != "window/short=5 hours window/short-2=24 hours" {
		t.Fatalf("refs %v", refs)
	}
	for _, r := range []string{"window/short", "window/short-2"} {
		if !contains(codeDraws, r) {
			t.Errorf("%s isn't drawn", r)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func TestBalanceByRegion(t *testing.T) {
	f, m, fetch := newFake(t)
	for _, c := range []struct{ region, currency, ref string }{{"cn", "CNY", "balance/cny"}, {"ai", "USD", "balance/usd"}} {
		inv, err := m.Supply.Collector.Inventory(context.Background(), session(ProductAPI, "balance", c.region), fetch)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.seen.Load(); got != c.region+" /v1/users/me/balance Bearer balance" {
			t.Fatalf("%s: request %v", c.region, got)
		}
		p := inv.Packages[0]
		r := p.Resources[0]
		// the official example, every digit kept: 49.58894 at 10^-6
		if p.Kind != "prepaid" || p.Usable == nil || !*p.Usable || r.TemplateRef != c.ref || r.Currency != c.currency || r.QuantumExp != -6 ||
			*r.Remaining != 49588940 || string(r.Evidence) != `{"granted":"46.58893","toppedUp":"3.00001"}` || inv.Note != "" {
			t.Fatalf("%s: %+v %+v evidence %s note %q", c.region, p, r, r.Evidence, inv.Note)
		}
	}
	models, err := m.Supply.Collector.Models(context.Background(), session(ProductAPI, "balance", "ai"), fetch)
	if err != nil || len(models) != 2 || f.seen.Load() != "ai /v1/models Bearer balance" {
		t.Fatalf("models = %+v %v %v", models, err, f.seen.Load())
	}
}

func TestBalanceArrearsAndUsedUp(t *testing.T) {
	_, m, fetch := newFake(t)
	inv, err := m.Supply.Collector.Inventory(context.Background(), session(ProductAPI, "arrears", "cn"), fetch)
	if err != nil {
		t.Fatal(err)
	}
	p := inv.Packages[0]
	if !*p.Usable || *p.Resources[0].Remaining != 2500000 || inv.Note != "Cash is in arrears: -1.25 CNY." ||
		string(p.Resources[0].Evidence) != `{"granted":"2.50","toppedUp":"-1.25"}` {
		t.Fatalf("arrears: %+v note %q evidence %s", p, inv.Note, p.Resources[0].Evidence)
	}
	inv, err = m.Supply.Collector.Inventory(context.Background(), session(ProductAPI, "empty", "cn"), fetch)
	if err != nil || *inv.Packages[0].Usable || *inv.Packages[0].Resources[0].Remaining != 0 || inv.Note != "The balance is used up. Top up to keep using the API." {
		t.Fatalf("empty: %+v %v", inv, err)
	}
}

func TestRefusedAndMissingKeys(t *testing.T) {
	f, m, fetch := newFake(t)
	for _, s := range []catalog.Session{session(ProductCode, "refused", ""), session(ProductAPI, "refused", "ai")} {
		if _, err := m.Supply.Collector.Inventory(context.Background(), s, fetch); !errors.Is(err, catalog.ErrSignedOut) {
			t.Fatalf("%s: %v, want signed out", s.Product, err)
		}
		if _, err := m.Supply.Collector.Models(context.Background(), s, fetch); !errors.Is(err, catalog.ErrSignedOut) {
			t.Fatalf("%s models: %v, want signed out", s.Product, err)
		}
	}
	before := f.hits.Load()
	for _, s := range []catalog.Session{session(ProductCode, " ", ""), session(ProductAPI, "", "cn")} {
		if _, err := m.Supply.Collector.Inventory(context.Background(), s, fetch); !errors.Is(err, catalog.ErrSignedOut) {
			t.Fatalf("no key: %v", err)
		}
	}
	// a key without a platform isn't sent anywhere: the regions don't share keys
	if _, err := m.Supply.Collector.Inventory(context.Background(), session(ProductAPI, "balance", ""), fetch); err == nil || !strings.Contains(err.Error(), "Choose the platform") {
		t.Fatalf("no region: %v", err)
	}
	if f.hits.Load() != before {
		t.Fatalf("%d requests sent without a key or platform", f.hits.Load()-before)
	}
}

func TestParseBalanceRejects(t *testing.T) {
	for _, body := range []string{
		`{"code":1,"data":{"available_balance":1,"voucher_balance":1,"cash_balance":0},"status":true}`,
		`{"code":0,"data":{"available_balance":1,"voucher_balance":1,"cash_balance":0},"status":false}`,
		`{"code":0,"status":true}`,
		`{"code":0,"data":{"available_balance":1e3,"voucher_balance":1,"cash_balance":0},"status":true}`,
		`{"error":{"message":"Invalid Authentication"}}`,
		`<html>`,
	} {
		if _, err := ParseBalance([]byte(body)); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
	b, err := ParseBalance(fixture(t, "balance.json"))
	if err != nil || b != (Balance{Available: "49.58894", Voucher: "46.58893", Cash: "3.00001"}) {
		t.Fatalf("balance = %+v %v", b, err)
	}
}

// Each credential's offers come from its own product and platform.
func TestOffersByProductAndRegion(t *testing.T) {
	m := Module(CodeBase, CNBase, AIBase)
	models := []observed.UpstreamModel{{ID: "kimi-for-coding"}}
	for _, c := range []struct {
		s     catalog.Session
		base  string
		draws string
	}{
		{session(ProductCode, "k", ""), "https://api.kimi.com/coding/v1", "window/short window/short-2 window/short-3 window/week"},
		{session(ProductAPI, "k", "cn"), "https://api.moonshot.cn/v1", "balance/cny"},
		{session(ProductAPI, "k", "ai"), "https://api.moonshot.ai/v1", "balance/usd"},
	} {
		o := catalog.Offers(m.Supply, c.s, models)
		if len(o) != 1 || o[0].Endpoint != c.base || strings.Join(o[0].Draws, " ") != c.draws || o[0].ModelID != "kimi-for-coding" {
			t.Fatalf("%s/%s offers = %+v", c.s.Product, c.s.Values["region"], o)
		}
	}
}

func TestMoney(t *testing.T) {
	for in, want := range map[string]string{"3": "3.00", "3.1": "3.10", "49.58894": "49.58894", "-1.25": "-1.25", "0": "0.00"} {
		if got := Money(in); got != want {
			t.Errorf("Money(%q) = %q, want %q", in, got, want)
		}
	}
}
