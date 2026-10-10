package quotasource

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ant/server/internal/providers/kimi"
	"ant/server/internal/quota"
	"ant/server/internal/store/sqlite"
	"ant/server/internal/supply"
	"ant/server/internal/transport"
)

// Kimi, the third module (design 13 A3), read end to end: a Kimi Code key
// and a China-platform key in the environment, an international-platform
// key in OpenCode. Each is read by its own product and platform, stored,
// offered at its own endpoint, and previewed.
func TestNativeModuleReadsKimi(t *testing.T) {
	kimiFixture := func(name string) []byte { return fixture(t, "../../providers/kimi/testdata/"+name) }
	var mu sync.Mutex // the engine reads credentials in parallel
	var seen []string
	handler := func(platform string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			mu.Lock()
			seen = append(seen, platform+" "+r.URL.Path+" "+key)
			mu.Unlock()
			switch r.URL.Path {
			case "/coding/v1/usages":
				_, _ = w.Write(kimiFixture("usages.json"))
			case "/coding/v1/models":
				_, _ = w.Write([]byte(`{"data":[{"id":"kimi-for-coding"}]}`))
			case "/v1/users/me/balance":
				_, _ = w.Write(kimiFixture(map[string]string{"sk-cn": "balance.json", "sk-ai": "arrears.json"}[key]))
			case "/v1/models":
				_, _ = w.Write([]byte(`{"data":[{"id":"kimi-k3"}]}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}
	}
	cn := httptest.NewTLSServer(handler("cn")) // also Kimi Code's origin
	defer cn.Close()
	ai := httptest.NewTLSServer(handler("ai"))
	defer ai.Close()
	m := kimi.Module(cn.URL, cn.URL, ai.URL)
	fetch, err := transport.NewObservationFetch(m.Supply.Origins, cn.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"KIMI_CODE_API_KEY": "sk-kimi-1", "MOONSHOT_API_KEY": "sk-cn"}
	home := t.TempDir()
	auth := filepath.Join(home, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(auth), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(auth, []byte(`{"moonshotai":{"type":"api","key":"sk-ai"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	clock, fixtureNow := previewFixtureClock()
	local := quota.Local{Home: home, Getenv: func(k string) string { return env[k] }, HTTP: &http.Client{Transport: loopbackOnly{}}, Now: func() time.Time { return fixtureNow }, URLs: map[string]string{}}
	var providers []quota.Provider
	for _, p := range quota.Providers() {
		if p.ID == "kimi" {
			providers = append(providers, p)
		}
	}
	accounts := quota.NewStore("")
	src := New(local, accounts, providers, Native{Module: m, Fetch: fetch})
	path := filepath.Join(t.TempDir(), "ant.db")
	st, err := sqlite.Open(path, sqlite.Config{Clock: clock, CommandBudget: fixtureCommandBudget})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close(context.Background()) }()
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	engine := supply.NewEngine("local", st, src, clock, supply.DefaultPolicy(), nil)
	reader := Reader{Source: src, Engine: engine}
	page := quota.NewService(local, accounts, quota.Options{Reader: reader, Router: reader.Routes()})
	h := page.Handler()
	call := func(method, target, body string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, "http://127.0.0.1:8080"+target, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:50000"
		req.Header.Set("X-Ant", "1")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, target, rec.Code, rec.Body.String())
		}
		return out
	}

	snap := page.Refresh(context.Background())

	cards := map[string]quota.Account{}
	for _, a := range snap.Accounts {
		key := a.Mode
		if a.Mode == "api" {
			key = a.Balances[0].Currency
		}
		cards[key] = a
	}
	if len(snap.Accounts) != 3 {
		t.Fatalf("cards = %+v", snap.Accounts)
	}
	code, cny, usd := cards["code"], cards["CNY"], cards["USD"]
	if code.Status != quota.StatusOK || code.Plan != "Kimi Code" || len(code.Windows) != 2 || code.Windows[0].Name != "5 hours" || *code.Windows[0].UsedPercent != 25 ||
		code.Windows[1].Name != "Week" || len(code.Models) != 1 || code.Models[0].ID != "kimi-for-coding" {
		t.Fatalf("Kimi Code card = %+v", code)
	}
	// each card opens where its account is managed: its own platform
	if code.Link != "https://www.kimi.com/code" || cny.Link != "https://platform.moonshot.cn/console/info" || usd.Link != "https://platform.moonshot.ai/console/info" {
		t.Errorf("links: %s, %s, %s", code.Link, cny.Link, usd.Link)
	}
	// every digit the platform reports, as people write money
	if cny.Status != quota.StatusOK || cny.Balances[0] != (quota.Balance{Currency: "CNY", Amount: "49.58894", Granted: "46.58893", ToppedUp: "3.00001"}) || cny.Message != "" {
		t.Fatalf("China platform card = %+v", cny)
	}
	if usd.Status != quota.StatusOK || usd.Balances[0] != (quota.Balance{Currency: "USD", Amount: "2.50", Granted: "2.50", ToppedUp: "-1.25"}) || usd.Message != "Cash is in arrears: -1.25 USD." {
		t.Fatalf("international card = %+v", usd)
	}
	// each key went to its own platform and nowhere else
	mu.Lock()
	sent := slices.Clone(seen)
	mu.Unlock()
	for _, want := range []string{"cn /coding/v1/usages sk-kimi-1", "cn /v1/users/me/balance sk-cn", "ai /v1/users/me/balance sk-ai"} {
		if !strings.Contains(strings.Join(sent, "\n"), want) {
			t.Errorf("no request %q in %v", want, sent)
		}
	}
	for _, s := range sent {
		if (strings.HasSuffix(s, " sk-ai") && !strings.HasPrefix(s, "ai ")) || (strings.HasSuffix(s, " sk-cn") && !strings.HasPrefix(s, "cn /v1/")) {
			t.Errorf("a key left its platform: %s", s)
		}
	}

	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	count := func(q string) int {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM supply_accounts WHERE provider_id='kimi'`: 3,
		`SELECT COUNT(*) FROM supply_readings r JOIN supply_resources s USING(namespace,resource_id) WHERE s.template_ref='window/short' AND s.unit='percent' AND r.remaining_units=7500`:       1,
		`SELECT COUNT(*) FROM supply_readings r JOIN supply_resources s USING(namespace,resource_id) WHERE s.template_ref='balance/cny' AND s.quantum_exp=-6 AND r.remaining_units=49588940`:    1,
		`SELECT COUNT(*) FROM supply_readings r JOIN supply_resources s USING(namespace,resource_id) WHERE s.template_ref='balance/usd' AND s.quantum_exp=-6 AND r.remaining_units=2500000`:     1,
		`SELECT COUNT(*) FROM supply_offers WHERE model_id='kimi-for-coding' AND endpoint LIKE '%/coding/v1' AND draws_json='["window/short","window/short-2","window/short-3","window/week"]'`: 1,
		`SELECT COUNT(*) FROM supply_offers WHERE model_id='kimi-k3' AND endpoint='` + cn.URL + `/v1' AND draws_json='["balance/cny"]'`:                                                         1,
		`SELECT COUNT(*) FROM supply_offers WHERE model_id='kimi-k3' AND endpoint='` + ai.URL + `/v1' AND draws_json='["balance/usd"]'`:                                                         1,
	} {
		if n := count(q); n != want {
			t.Errorf("%d, want %d: %s", n, want, q)
		}
	}

	route := call("GET", "/api/route?model=kimi-for-coding", "")
	use := route["use"].([]any)
	if len(use) != 1 || len(route["notUsed"].([]any)) != 0 || !strings.Contains(use[0].(map[string]any)["reason"].(string), "5 hours 75% left, Week 75% left") {
		t.Fatalf("kimi-for-coding route = %v", route)
	}
	route = call("GET", "/api/route?model=Kimi-K3", "")
	use = route["use"].([]any)
	var reasons []string
	for _, u := range use {
		reasons = append(reasons, u.(map[string]any)["class"].(string)+": "+u.(map[string]any)["reason"].(string))
	}
	got := strings.Join(reasons, " | ")
	if len(use) != 2 || !strings.Contains(got, "prepaid: Prepaid: 49.58894 CNY left") || !strings.Contains(got, "prepaid: Prepaid: 2.50 USD left") {
		t.Fatalf("kimi-k3 route = %s %v", got, route)
	}
}
