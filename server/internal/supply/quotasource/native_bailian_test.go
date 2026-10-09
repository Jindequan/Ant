package quotasource

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ant/server/internal/providers/aliyun"
	"ant/server/internal/quota"
	"ant/server/internal/runtime"
	"ant/server/internal/store/sqlite"
	"ant/server/internal/supply"
	"ant/server/internal/transport"
)

// Bailian, the fourth module (design 13 C5), read end to end: the Token
// Plan and the free quota through the Bailian CLI's sign-in (its token,
// read at each read), pay-as-you-go through an AccessKey in the
// environment. Cards, stored facts, offers, and the route preview: a model
// with a free grant uses it; a model without one isn't offered by it.
func TestNativeModuleReadsBailian(t *testing.T) {
	fx := func(name string) []byte { return fixture(t, "../../providers/aliyun/testdata/"+name) }
	gw := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/compatible-mode/v1/models" { // the Token Plan's model list, with the profile's key
			if r.Header.Get("Authorization") != "Bearer sk-sp-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen3-coder-plus"},{"id":"qwen3-max"}]}`))
			return
		}
		_ = r.ParseForm()
		var p struct {
			Api  string
			Data map[string]any
		}
		_ = json.Unmarshal([]byte(r.PostForm.Get("params")), &p)
		if r.Header.Get("Authorization") != "Bearer tok" {
			_, _ = w.Write(fx("not-logged-in.json"))
			return
		}
		switch p.Api {
		case aliyun.APISubscription:
			_, _ = w.Write(fx("subscription.json"))
		case aliyun.APIUsage:
			_, _ = w.Write(fx("usage.json"))
		case aliyun.APIAddons:
			_, _ = w.Write(fx("addons.json"))
		case aliyun.APIEquity:
			_, _ = w.Write(fx("equity.json"))
		case aliyun.APIListModels:
			if p.Data["input"].(map[string]any)["pageNo"].(float64) == 1 {
				_, _ = w.Write(fx("models-page1.json"))
			} else {
				_, _ = w.Write(fx("models-page2.json"))
			}
		case aliyun.APIFreeQuota:
			_, _ = w.Write(fx("free-quota.json"))
		}
	}))
	defer gw.Close()
	bss := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		all := map[string]string{}
		for k := range q {
			if k != "Signature" {
				all[k] = q.Get(k)
			}
		}
		if signed, _ := url.ParseQuery(aliyun.RPCQuery("s", all)); q.Get("AccessKeyId") != "LTAIx" || signed.Get("Signature") != q.Get("Signature") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(fx("bss-refused.json"))
			return
		}
		switch {
		case q.Get("Action") == "QueryAccountBalance":
			_, _ = w.Write(fx("bss-balance.json"))
		case q.Get("BillingCycle") == "2026-09":
			_, _ = w.Write(fx("bss-bill-2026-09.json"))
		default:
			_, _ = w.Write(fx("bss-bill-empty.json"))
		}
	}))
	defer bss.Close()
	m := aliyun.Module(aliyun.Bases{Gateway: gw.URL, GatewayIntl: "https://intl.example", TokenPlan: gw.URL, DashScope: "https://dashscope.example",
		Business: bss.URL, BusinessIntl: "https://business-intl.example"})
	fetch, err := transport.NewObservationFetch(m.Supply.Origins, gw.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	profile := filepath.Join(home, ".bailian", "config.json")
	if err := os.MkdirAll(filepath.Dir(profile), 0o700); err != nil {
		t.Fatal(err)
	}
	writeProfile := func(token string) {
		b, _ := json.Marshal(map[string]string{"access_token": token, "console_region": "cn-beijing", "api_key": "sk-sp-1", "base_url": gw.URL + "/compatible-mode/v1"})
		if err := os.WriteFile(profile, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeProfile("tok")
	env := map[string]string{"ALIBABA_CLOUD_ACCESS_KEY_ID": "LTAIx", "ALIBABA_CLOUD_ACCESS_KEY_SECRET": "s"}
	at := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	local := quota.Local{Home: home, Getenv: func(k string) string { return env[k] }, HTTP: &http.Client{Transport: loopbackOnly{}}, Now: func() time.Time { return at }, URLs: map[string]string{}}
	var providers []quota.Provider
	for _, p := range quota.Providers() {
		if p.ID == "aliyun" {
			providers = append(providers, p)
		}
	}
	src := New(local, quota.NewStore(""), providers, Native{Module: m, Fetch: fetch})
	path := filepath.Join(t.TempDir(), "ant.db")
	st, err := sqlite.Open(path, sqlite.Config{Clock: runtime.NewClock(), CommandBudget: fixtureCommandBudget})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close(context.Background()) }()
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	engine := supply.NewEngine("local", st, src, runtime.NewClock(), supply.DefaultPolicy(), nil)
	reader := Reader{Source: src, Engine: engine}
	page := quota.NewService(local, quota.NewStore(""), quota.Options{Reader: reader, Router: reader.Routes()})
	snap := page.Refresh(context.Background())

	cards := map[string]quota.Account{}
	for _, a := range snap.Accounts {
		cards[a.Mode] = a
	}
	if len(snap.Accounts) != 3 {
		t.Fatalf("cards = %+v", snap.Accounts)
	}
	plan, payg, free := cards["token-plan"], cards["payg"], cards["free-quota"]
	if plan.Status != quota.StatusOK || plan.Plan != "Pro" || len(plan.Windows) != 3 || plan.Windows[0].Name != "5 hours" || *plan.Windows[1].UsedPercent != 50 ||
		plan.Windows[2].Name != "Add-on credits" || *plan.Windows[2].Left != 120.5 || len(plan.Groups) != 1 || plan.Groups[0].Title != "Tool benefits" ||
		len(plan.Groups[0].Windows) != 2 || !plan.Groups[0].Windows[0].Aside || plan.Message != aliyun.ToolsNote || len(plan.Models) != 2 {
		t.Fatalf("Token Plan card = %+v", plan)
	}
	if payg.Status != quota.StatusOK || len(payg.Balances) != 1 || payg.Balances[0].Amount != "1234.50" || len(payg.Groups) != 1 ||
		payg.Groups[0].Title != "Charged by Bailian" || payg.Groups[0].Balances[0].Label != "This month" || payg.Groups[0].Balances[1].Amount != "1.504321" ||
		!strings.Contains(payg.Message, "last month ¥12.304321") {
		t.Fatalf("pay-as-you-go card = %+v", payg)
	}
	if free.Status != quota.StatusOK || len(free.Windows) != 2 || free.Windows[0].Name != "kimi-k3" || *free.Windows[0].Left != 820000 ||
		free.Message != "2 models with free quota · 1 more used up or expired." || len(free.Models) != 2 {
		t.Fatalf("free quota card = %+v", free)
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
		`SELECT COUNT(*) FROM supply_accounts WHERE provider_id='aliyun'`:                                                                                          3,
		`SELECT COUNT(*) FROM supply_accounts WHERE provider_identity IN ('sfm_tokenplansolo_public_cn-x','payg:1400')`:                                            2,
		`SELECT COUNT(*) FROM supply_packages WHERE kind='free_grant'`:                                                                                             1,
		`SELECT COUNT(*) FROM supply_resources WHERE template_ref='grant/kimi-k3' AND scope_json='{"kind":"model","model":"kimi-k3"}'`:                             1,
		`SELECT COUNT(*) FROM supply_offers WHERE model_id='kimi-k3' AND endpoint='https://dashscope.example/compatible-mode/v1' AND draws_json='["scope:model"]'`: 1,
		`SELECT COUNT(*) FROM supply_offers WHERE model_id='qwen3-max' AND endpoint='` + gw.URL + `/compatible-mode/v1'`:                                           1,
		`SELECT COUNT(*) FROM supply_offers WHERE model_id='qwen3-max' AND endpoint LIKE 'https://dashscope%'`:                                                     0,
	} {
		if n := count(q); n != want {
			t.Errorf("%d, want %d: %s", n, want, q)
		}
	}

	h := page.Handler()
	route := func(model string) (use, notUsed []any) {
		t.Helper()
		req := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/route?model="+model, nil)
		req.RemoteAddr = "127.0.0.1:50000"
		req.Header.Set("X-Ant", "1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 {
			t.Fatalf("route %s: %d %s", model, rec.Code, rec.Body.String())
		}
		return out["use"].([]any), out["notUsed"].([]any)
	}
	use, notUsed := route("Kimi-K3")
	if len(use) != 1 || len(notUsed) != 0 || use[0].(map[string]any)["class"] != "free" ||
		!strings.HasPrefix(use[0].(map[string]any)["reason"].(string), "Free: kimi-k3 820,000 tokens left, ends in ") {
		t.Fatalf("kimi-k3 route = %v %v", use, notUsed)
	}
	// the plan serves qwen3-max; the free quota has no grant for it, so it isn't offered at all
	use, notUsed = route("qwen3-max")
	if len(use) != 1 || len(notUsed) != 0 || use[0].(map[string]any)["class"] != "plan" ||
		!strings.HasPrefix(use[0].(map[string]any)["reason"].(string), "Plan: 5 hours 80% left, Week 50% left") {
		t.Fatalf("qwen3-max route = %v %v", use, notUsed)
	}

	// bl signs out: the next read says so, without asking Bailian
	writeProfile("")
	if err := engine.RunNow(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for _, a := range reader.Latest().Accounts {
		if a.Mode != "payg" && (a.Status != quota.StatusSignedOut || a.Message != "The Bailian CLI is signed out. Run bl login.") {
			t.Errorf("%s after bl logout = %s %q", a.Mode, a.Status, a.Message)
		}
	}
}
