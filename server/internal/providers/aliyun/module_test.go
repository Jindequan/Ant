package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// fakeBailian answers the CLI gateway for the token "tok" (China site) and
// "intl-tok" (international site), and the Token Plan's model list.
type fakeBailian struct {
	srv, intl *httptest.Server
	mu        sync.Mutex
	calls     []string // "host-kind api region"
	plan      string   // subscription fixture
}

func newFake(t *testing.T) (*fakeBailian, catalog.Module, observed.Fetch) {
	t.Helper()
	f := &fakeBailian{plan: "subscription.json"}
	handler := func(site string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/compatible-mode/v1/models" {
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
			f.mu.Lock()
			f.calls = append(f.calls, site+" "+p.Api+" "+r.PostForm.Get("region"))
			f.mu.Unlock()
			want := map[string]string{"cn": "Bearer tok", "intl": "Bearer intl-tok"}[site]
			if r.Method != http.MethodPost || r.URL.Path != "/cli/api.json" || r.Header.Get("Authorization") != want {
				_, _ = w.Write(fixture(t, "not-logged-in.json"))
				return
			}
			if cp, _ := p.Data["cornerstoneParam"].(map[string]any); cp["protocol"] != "V2" {
				t.Errorf("cornerstoneParam = %v", p.Data["cornerstoneParam"])
			}
			switch p.Api {
			case APISubscription:
				_, _ = w.Write(fixture(t, f.plan))
			case APIUsage:
				_, _ = w.Write(fixture(t, "usage.json"))
			case APIAddons:
				_, _ = w.Write(fixture(t, "addons.json"))
			case APIEquity:
				if p.Data["queryTokenPlanEquityInfoRequest"] == nil {
					t.Error("equity call without its request")
				}
				_, _ = w.Write(fixture(t, "equity.json"))
			case APIListModels:
				if p.Data["input"].(map[string]any)["pageNo"].(float64) == 1 {
					_, _ = w.Write(fixture(t, "models-page1.json"))
				} else {
					_, _ = w.Write(fixture(t, "models-page2.json"))
				}
			case APIFreeQuota:
				models, _ := p.Data["queryFreeTierQuotaRequest"].(map[string]any)["models"].([]any)
				if len(models) != 4 {
					t.Errorf("asked for %v", models)
				}
				_, _ = w.Write(fixture(t, "free-quota.json"))
			default:
				t.Errorf("unexpected call %s", p.Api)
			}
		}
	}
	f.srv = httptest.NewTLSServer(handler("cn"))
	f.intl = httptest.NewTLSServer(handler("intl"))
	t.Cleanup(f.srv.Close)
	t.Cleanup(f.intl.Close)
	m := Module(Bases{Gateway: f.srv.URL, GatewayIntl: f.intl.URL, TokenPlan: f.srv.URL, Business: f.srv.URL, BusinessIntl: f.intl.URL})
	fetch, err := transport.NewObservationFetch(m.Supply.Origins, f.srv.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	return f, m, fetch
}

func plan(values map[string]string) catalog.Session {
	return catalog.Session{Product: ProductTokenPlan, Values: values, Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
}

func TestTokenPlanInventory(t *testing.T) {
	f, m, fetch := newFake(t)
	inv, err := m.Supply.Collector.Inventory(context.Background(), plan(map[string]string{"token": "tok"}), fetch)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Identity != "sfm_tokenplansolo_public_cn-x" || len(inv.Packages) != 2 || inv.Note != ToolsNote {
		t.Fatalf("inventory = %+v", inv)
	}
	p := inv.Packages[0]
	if p.Kind != "subscription" || p.Plan != "Pro" || p.AutoRenew == nil || !*p.AutoRenew || p.EndsAt == nil || *p.EndsAt != 1792252800000 ||
		p.RenewsAt == nil || *p.RenewsAt != 1792252800000 || p.StartsAt == nil || len(p.Resources) != 3 {
		t.Fatalf("plan = %+v", p)
	}
	short, week, addon := p.Resources[0], p.Resources[1], p.Resources[2]
	if short.TemplateRef != "window/5hour" || short.Label != "5 hours" || *short.Used != 2000 || *short.Remaining != 8000 || *short.WindowEnd != 1791400000000 ||
		week.TemplateRef != "window/1week" || week.Label != "Week" || *week.Used != 5000 || string(week.Evidence) != `{"usedFraction":"0.5"}` {
		t.Fatalf("windows = %+v %+v", short, week)
	}
	// exact: 120.5 of 500 credits at 10^-6
	if addon.TemplateRef != "credits/addon" || *addon.Remaining != 120_500_000 || *addon.Limit != 500_000_000 || addon.Aside {
		t.Fatalf("add-on credits = %+v", addon)
	}
	tools := inv.Packages[1]
	if tools.Plan != "Tool benefits" || len(tools.Resources) != 2 || tools.Resources[0].Label != "Image generation MCP" || tools.Resources[0].Unit != "images" ||
		!tools.Resources[0].Aside || *tools.Resources[1].Remaining != 17_940_500_000 || tools.Resources[1].Unit != "seconds" || *tools.Resources[1].WindowEnd != 1792252800000 {
		t.Fatalf("tools = %+v", tools)
	}
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "cn ") || !strings.HasSuffix(c, " cn-beijing") {
			t.Errorf("call %q: want the China gateway, region cn-beijing", c)
		}
	}
	if len(f.calls) != 4 {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestTokenPlanSitesAndRegions(t *testing.T) {
	f, m, fetch := newFake(t)
	if _, err := m.Supply.Collector.Inventory(context.Background(), plan(map[string]string{"token": "intl-tok", "site": "international", "region": "ap-southeast-1"}), fetch); err != nil {
		t.Fatal(err)
	}
	if f.calls[0] != "intl "+APISubscription+" ap-southeast-1" {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestTokenPlanSignedOutAndNoPlan(t *testing.T) {
	f, m, fetch := newFake(t)
	_, err := m.Supply.Collector.Inventory(context.Background(), plan(map[string]string{"token": "expired"}), fetch)
	if !errors.Is(err, catalog.ErrSignedOut) || !strings.Contains(err.Error(), "Sign in again") {
		t.Fatalf("expired token: %v", err)
	}
	// bl's token is renewed with bl login, and the card says so
	_, err = m.Supply.Collector.Inventory(context.Background(), plan(map[string]string{"token": "expired", "via": "bl"}), fetch)
	if !errors.Is(err, catalog.ErrSignedOut) || !strings.Contains(err.Error(), "Run bl login") {
		t.Fatalf("expired bl token: %v", err)
	}
	n := len(f.calls)
	if _, err := m.Supply.Collector.Inventory(context.Background(), plan(map[string]string{"token": " "}), fetch); !errors.Is(err, catalog.ErrSignedOut) || len(f.calls) != n {
		t.Fatalf("no token: %v, %d calls sent", err, len(f.calls)-n)
	}
	f.plan = "no-plan.json"
	inv, err := m.Supply.Collector.Inventory(context.Background(), plan(map[string]string{"token": "tok"}), fetch)
	if err != nil || len(inv.Packages) != 0 || inv.Note != "This Alibaba Cloud account has no Token Plan." {
		t.Fatalf("no plan: %+v %v", inv, err)
	}
}

// Models come only from a bl profile's model key at the Token Plan's own
// origin; anything else lists none and sends nothing.
func TestTokenPlanModels(t *testing.T) {
	f, m, fetch := newFake(t)
	got, err := m.Supply.Collector.Models(context.Background(), plan(map[string]string{"token": "tok", "apiKey": "sk-sp-1", "baseURL": f.srv.URL + "/compatible-mode/v1/"}), fetch)
	if err != nil || len(got) != 2 || got[0].ID != "qwen3-coder-plus" {
		t.Fatalf("models = %+v %v", got, err)
	}
	for _, v := range []map[string]string{
		{"token": "tok"},
		{"token": "tok", "apiKey": "sk-sp-1", "baseURL": "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{"token": "tok", "apiKey": "sk-sp-1", "baseURL": f.srv.URL + ".evil.example/compatible-mode/v1"},
	} {
		if got, err := m.Supply.Collector.Models(context.Background(), plan(v), fetch); err != nil || got != nil {
			t.Errorf("%v: %+v %v", v, got, err)
		}
	}
}

// Offers: the Token Plan endpoint for China-site sessions only.
func TestTokenPlanOffers(t *testing.T) {
	m := Module(DefaultBases())
	models := []observed.UpstreamModel{{ID: "qwen3-coder-plus"}}
	o := catalog.Offers(m.Supply, plan(map[string]string{"token": "t"}), models)
	if len(o) != 1 || o[0].Endpoint != "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1" || !strings.Contains(strings.Join(o[0].Draws, " "), "window/5hour") {
		t.Fatalf("offers = %+v", o)
	}
	if o := catalog.Offers(m.Supply, plan(map[string]string{"token": "t", "site": "international"}), models); len(o) != 0 {
		t.Fatalf("international offers = %+v", o)
	}
}

func TestParseCLIReply(t *testing.T) {
	if _, err := ParseCLIReply([]byte(`<html>`)); !errors.Is(err, ErrChanged) {
		t.Errorf("html: %v", err)
	}
	if _, err := ParseCLIReply(fixture(t, "not-logged-in.json")); !errors.Is(err, catalog.ErrSignedOut) {
		t.Errorf("not logged in: %v", err)
	}
	if _, err := ParseCLIReply([]byte(`{"data":{"success":false,"errorCode":"Throttling","errorMsg":"slow down"}}`)); err == nil || !strings.Contains(err.Error(), "Throttling slow down") {
		t.Errorf("throttled: %v", err)
	}
	b, err := ParseCLIReply(fixture(t, "usage.json"))
	if err != nil || !strings.Contains(string(b), "per5HourPercentage") {
		t.Errorf("usage: %s %v", b, err)
	}
}

func TestParseUsageKeepsSpans(t *testing.T) {
	ws := ParseUsage(map[string]json.RawMessage{
		"per1MonthPercentage": json.RawMessage(`0.25`), "per1MonthResetTime": json.RawMessage(`1792252800000`),
		"per5HourPercentage": json.RawMessage(`0.5`), "per7DayPercentage": json.RawMessage(`0.1`), "note": json.RawMessage(`"x"`),
	})
	var got []string
	for _, w := range ws {
		got = append(got, w.Ref()+"="+w.Name+"@"+w.Used)
	}
	if strings.Join(got, " ") != "window/5hour=5 hours@0.5 window/7day=7 days@0.1 window/1month=Month@0.25" || ws[2].ResetsAt != 1792252800000 || ws[0].ResetsAt != 0 {
		t.Fatalf("windows = %v", got)
	}
}

func free(values map[string]string, now time.Time) catalog.Session {
	return catalog.Session{Product: ProductFreeQuota, Values: values, Now: now}
}

// Each model's grant is a meter scoped to that model, ending with the grant.
func TestFreeQuotaInventory(t *testing.T) {
	_, m, fetch := newFake(t)
	at := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	inv, err := m.Supply.Collector.Inventory(context.Background(), free(map[string]string{"token": "tok"}, at), fetch)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Packages) != 1 || inv.Note != "2 models with free quota · 1 more used up or expired." || inv.DisplayGroup != GrantsGroup([]Grant{{Model: "kimi-k3", Ends: 1794873600000}, {Model: "qwen-image-2.1-pro", Ends: 1798732800000}}) || inv.Identity != "" {
		t.Fatalf("inventory = %+v", inv)
	}
	p := inv.Packages[0]
	if p.Kind != "free_grant" || p.Plan != "New-user free quota" || len(p.Resources) != 2 {
		t.Fatalf("package = %+v", p)
	}
	k, img := p.Resources[0], p.Resources[1]
	if k.TemplateRef != "grant/kimi-k3" || string(k.ScopeJSON) != `{"kind":"model","model":"kimi-k3"}` || k.Unit != "tokens" ||
		*k.Remaining != 820_000_000_000 || *k.Limit != 1_000_000_000_000 || *k.WindowEnd != 1794873600000 || k.ResetBasis != "reported" {
		t.Fatalf("kimi-k3 grant = %+v", k)
	}
	if img.Label != "qwen-image-2.1-pro" || img.Unit != "images" || *img.Remaining != 10_000_000 || string(img.Evidence) != `{"media":"image"}` {
		t.Fatalf("image grant = %+v", img)
	}
	// past kimi-k3's end, its grant is gone: the session's clock decides, not the machine's
	later, err := m.Supply.Collector.Inventory(context.Background(), free(map[string]string{"token": "tok"}, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)), fetch)
	if err != nil || len(later.Packages[0].Resources) != 1 || later.Note != "1 model with free quota · 2 more used up or expired." {
		t.Fatalf("later = %+v %v", later, err)
	}
	models, err := m.Supply.Collector.Models(context.Background(), free(map[string]string{"token": "tok"}, at), fetch)
	if err != nil || len(models) != 2 || models[0].ID != "kimi-k3" || models[0].Media != "text" || models[1].Media != "image" {
		t.Fatalf("models = %+v %v", models, err)
	}
}

func TestFreeQuotaNeedsAToken(t *testing.T) {
	f, m, fetch := newFake(t)
	_, err := m.Supply.Collector.Inventory(context.Background(), free(nil, time.Now()), fetch)
	if !errors.Is(err, catalog.ErrSignedOut) || len(f.calls) != 0 {
		t.Fatalf("no token: %v, %d calls", err, len(f.calls))
	}
	if got, err := m.Supply.Collector.Models(context.Background(), free(nil, time.Now()), fetch); got != nil || err != nil || len(f.calls) != 0 {
		t.Fatalf("models without a token: %v %v", got, err)
	}
}

// Free quota is offered at DashScope, each call drawing on its own model's
// grant; an international-site session is offered nothing.
func TestFreeQuotaOffers(t *testing.T) {
	m := Module(DefaultBases())
	models := []observed.UpstreamModel{{ID: "kimi-k3"}}
	o := catalog.Offers(m.Supply, free(map[string]string{"token": "t"}, time.Now()), models)
	if len(o) != 1 || o[0].Endpoint != "https://dashscope.aliyuncs.com/compatible-mode/v1" || strings.Join(o[0].Draws, " ") != observed.DrawModelScope {
		t.Fatalf("offers = %+v", o)
	}
	if o := catalog.Offers(m.Supply, free(map[string]string{"token": "t", "site": "international"}, time.Now()), models); len(o) != 0 {
		t.Fatalf("international offers = %+v", o)
	}
}

// One account's grants end at the same times however it's reached; another
// account's don't: the card grouping follows the grants (display only).
func TestGrantsGroupRecognisesAnAccount(t *testing.T) {
	a := []Grant{{Model: "kimi-k3", Ends: 1794873600000}, {Model: "qwen3-max", Ends: 1794873600000}}
	reordered := []Grant{a[1], a[0]}
	other := []Grant{{Model: "kimi-k3", Ends: 1794873601000}, {Model: "qwen3-max", Ends: 1794873600000}}
	if GrantsGroup(a) == "" || GrantsGroup(a) != GrantsGroup(reordered) || GrantsGroup(a) == GrantsGroup(other) || GrantsGroup(nil) != "" {
		t.Fatalf("groups: %q %q %q %q", GrantsGroup(a), GrantsGroup(reordered), GrantsGroup(other), GrantsGroup(nil))
	}
}
