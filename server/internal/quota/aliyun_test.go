package quota

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
)

// chromeProfile writes a Chrome cookie store with the given (host, name,
// value) cookies, all encrypted as Chrome encrypts them.
func chromeProfile(t *testing.T, l Local, cookies [][3]string) {
	t.Helper()
	file := l.path("Library", "Application Support", "Google", "Chrome", "Default", "Cookies")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE meta (key TEXT, value TEXT)`,
		`INSERT INTO meta VALUES ('version', '24')`,
		`CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, last_access_utc INTEGER, has_expires INTEGER DEFAULT 0, expires_utc INTEGER DEFAULT 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range cookies {
		if _, err := db.Exec(`INSERT INTO cookies (host_key, name, value, encrypted_value, last_access_utc) VALUES (?, ?, '', ?, 13400000000000000)`, c[0], c[1], encryptChromium(t, "chrome-pw", c[0], c[2])); err != nil {
			t.Fatal(err)
		}
	}
	old := keychainPassword
	keychainPassword = func(string) (string, error) { return "chrome-pw", nil }
	keyMu.Lock()
	keyMemo = map[string][]byte{}
	keyMu.Unlock()
	t.Cleanup(func() { keychainPassword = old })
}

// bailianConsoleServer answers like the Bailian console for a signed-in
// session carrying the "ticket" cookie, as observed on 2026-10-07.
func bailianConsoleServer(t *testing.T, usage string) *httptest.Server {
	t.Helper()
	gw := func(data string) string {
		return `{"code":"200","data":{"DataV2":{"ret":["SUCCESS::接口调用成功"],"data":{"msg":"Success.","code":"SUCCESS","data":` + data + `,"success":true}},"success":true,"httpStatus":200,"errorCode":"","errorMsg":""},"httpStatusCode":"200","successResponse":true}`
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signedIn := strings.Contains(r.Header.Get("Cookie"), "login_aliyunid_ticket=ticket")
		if r.Method == http.MethodGet {
			if !signedIn {
				_, _ = w.Write([]byte(`<html>请登录</html>`))
				return
			}
			_, _ = w.Write([]byte(`<script>window.ALIYUN_CONSOLE_CONFIG = {
            IS_CERTIFIED: "true",
            SEC_TOKEN: "csrf-1",
        }</script>`))
			return
		}
		_ = r.ParseForm()
		if !signedIn || r.PostForm.Get("sec_token") != "csrf-1" || r.PostForm.Get("region") != "cn-beijing" {
			_, _ = w.Write([]byte(`{"code":"ConsoleNeedLogin","message":"login"}`))
			return
		}
		switch r.URL.Query().Get("action") {
		case "GetFundAccountAvailableAmount":
			_, _ = w.Write([]byte(`{"code":"200","data":{"AvailableAmount":"0.01","Currency":"CNY"}}`))
			return
		case "QueryBillOverview":
			var p struct{ BillingCycle string }
			_ = json.Unmarshal([]byte(r.PostForm.Get("params")), &p)
			items := `[]`
			if p.BillingCycle == "2026-09" {
				items = `[{"ProductCode":"sfm","SubscriptionType":"PayAsYouGo","PretaxAmount":1.5,"PretaxGrossAmount":"12.30","Currency":"CNY","OwnerID":"1400"},
					{"ProductCode":"sfm","SubscriptionType":"Subscription","PretaxAmount":139,"PretaxGrossAmount":139,"Currency":"CNY","OwnerID":"1400"},
					{"ProductCode":"domain","SubscriptionType":"Subscription","PretaxAmount":82,"Currency":"CNY","OwnerID":"1400"}]`
			}
			_, _ = w.Write([]byte(`{"code":"200","data":{"Code":"Success","Data":{"Items":{"Item":` + items + `}},"Success":true}}`))
			return
		}
		var params struct {
			Api  string
			Data map[string]any
		}
		if json.Unmarshal([]byte(r.PostForm.Get("params")), &params) != nil || params.Data["cornerstoneParam"] == nil {
			_, _ = w.Write([]byte(`{"code":"200","data":{"success":false,"errorCode":"Bad Request","errorMsg":"Bad Request"}}`))
			return
		}
		switch params.Api {
		case "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/subscription":
			_, _ = w.Write([]byte(gw(`{"instanceCode":"sfm_tokenplansolo_public_cn-x","specCode":"standard","remainingDays":10,"startTime":1789658369000,"endTime":1792252800000,"autoRenewFlag":false,"status":"VALID"}`)))
		case "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage":
			_, _ = w.Write([]byte(gw(usage)))
		case "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/addon/summary":
			_, _ = w.Write([]byte(gw(`{"remainingCredits":0,"activeCount":0,"totalCredits":0}`)))
		case "zeldaEasy.bailian-commerce.tokenPlan.queryTokenPlanEquityInfo":
			if params.Data["queryTokenPlanEquityInfoRequest"] == nil {
				_, _ = w.Write([]byte(`{"code":"200","data":{"success":false,"errorMsg":"queryTokenPlanEquityInfoRequest must not be null"}}`))
				return
			}
			_, _ = w.Write([]byte(gw(`{"tokenPlanEquityInfos":[
				{"availableQuota":20,"equityType":"tokenplan_harnesstool_image_generation","totalQuota":20,"instanceEndTime":1792252800000},
				{"availableQuota":18000,"equityType":"tokenplan_harnesstool_asr","totalQuota":18000,"instanceEndTime":1792252800000}],"userId":"1"}`)))
		default:
			t.Errorf("Ant called %q", params.Api)
			_, _ = w.Write([]byte(`{"code":"200","data":{"success":false,"errorMsg":"unexpected"}}`))
		}
	}))
}

func bailianLocal(t *testing.T, srv *httptest.Server, cookies [][3]string) Local {
	l := testLocal(t, nil)
	l.URLs["aliyun.console"], l.URLs["aliyun.gateway"] = srv.URL, srv.URL
	chromeProfile(t, l, cookies)
	return l
}

var aliyunCookies = [][3]string{
	{".aliyun.com", "login_aliyunid_pk", "1400"},
	{".aliyun.com", "login_aliyunid_ticket", "ticket"},
	{"bailian.console.aliyun.com", "console_only", "x"},
	{"other.example.com", "login_aliyunid_ticket", "wrong-site"},
}

func TestBailianInstancesAreReadFromTheBrowserSignIn(t *testing.T) {
	srv := bailianConsoleServer(t, `{"per1MonthPercentage":1.0,"per1MonthResetTime":1792252800000}`)
	defer srv.Close()
	l := bailianLocal(t, srv, aliyunCookies)
	l.Now = func() time.Time { return time.Date(2026, 10, 7, 15, 30, 0, 0, time.UTC) }
	p := aliyunProvider()
	var conns []Connection
	for _, m := range p.Modes[:2] {
		conns = append(conns, m.withWay("sign-in").Discover(l)...)
	}
	if len(conns) != 2 || conns[0].Source != "Chrome · Default" || p.Modes[2].withWay("sign-in").Discover != nil {
		t.Fatalf("conns = %+v", conns)
	}

	plan := readTokenPlan(context.Background(), l, conns[0])
	if plan.Status != StatusOK || plan.Plan != "Standard" || plan.AutoRenew == nil || *plan.AutoRenew || plan.RenewsAt != nil ||
		plan.EndsAt == nil || !plan.EndsAt.Equal(time.UnixMilli(1792252800000)) {
		t.Fatalf("token plan = %+v", plan)
	}
	if len(plan.Windows) != 1 || plan.Windows[0].Name != "Month" || *plan.Windows[0].UsedPercent != 100 {
		t.Fatalf("plan windows = %+v", plan.Windows)
	}
	tools := plan.Groups[0]
	if len(tools.Windows) != 2 || tools.Windows[0].Name != "Image generation MCP" || tools.Windows[0].Unit != "images" ||
		*tools.Windows[0].Left != 20 || !tools.Windows[0].Expires || tools.Windows[1].Unit != "seconds" {
		t.Fatalf("tools = %+v", tools.Windows)
	}

	payg := readBailianPayg(context.Background(), l, conns[1])
	if payg.Status != StatusOK || payg.Kind != KindAPI || len(payg.Balances) != 1 || payg.Balances[0].Amount != "0.01" {
		t.Fatalf("payg = %+v", payg)
	}
	spend := payg.Groups[0]
	if len(spend.Balances) != 2 || spend.Balances[0].Label != "This month" || spend.Balances[0].Amount != "0.00" ||
		spend.Balances[1].Amount != "1.50" || !strings.Contains(spend.Note, "last month ¥12.30") || payg.Identity != "payg:1400" {
		t.Fatalf("spend = %+v", spend)
	}

	// a browser session can't read free quota (anti-bot guarded): it asks for the Alibaba Cloud sign-in
	free := readBailianFreeQuota(context.Background(), l, Connection{ID: "f", Provider: "aliyun", Mode: "free-quota"})
	if free.Status != StatusSignedOut || !strings.Contains(free.Message, "Sign in") {
		t.Fatalf("free = %+v", free)
	}
}

func TestBailianEndedSignInIsSignedOut(t *testing.T) {
	srv := bailianConsoleServer(t, `{}`)
	defer srv.Close()
	l := bailianLocal(t, srv, [][3]string{{".aliyun.com", "login_aliyunid_pk", "1400"}, {".aliyun.com", "login_aliyunid_ticket", "expired"}})
	a := readTokenPlan(context.Background(), l, discoverBailian("token-plan")(l)[0])
	if a.Status != StatusSignedOut {
		t.Fatalf("account = %+v", a)
	}
	if len(discoverBailian("payg")(testLocal(t, nil))) != 0 {
		t.Fatal("found Bailian with no browser sign-in")
	}
}

func TestBrowserSessionSendsOnlyTheSitesCookies(t *testing.T) {
	s := browserSession{cookies: []siteCookie{
		{".aliyun.com", "a", "1"}, {"bailian.console.aliyun.com", "b", "2"}, {".console.aliyun.com", "c", "3"}, {"evil-aliyun.com", "d", "4"},
	}}
	if got := s.header("bailian-cs.console.aliyun.com"); got != "a=1; c=3" {
		t.Fatalf("header = %q", got)
	}
	if got := s.header("bailian.console.aliyun.com"); got != "a=1; b=2; c=3" {
		t.Fatalf("header = %q", got)
	}
	u, _ := url.Parse(bailianFreeQuota)
	if u.Host != "bailian.console.aliyun.com" {
		t.Fatal(u)
	}
}

// bailianCLIServer answers like Bailian's CLI gateway for the token "tok".
func bailianCLIServer(t *testing.T) *httptest.Server {
	t.Helper()
	ok := func(data string) string {
		return `{"code":"200","data":{"DataV2":{"data":{"success":true,"data":` + data + `}},"success":true}}`
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/api.json" || r.Header.Get("Authorization") != "Bearer tok" {
			_, _ = w.Write([]byte(`{"code":"200","data":{"success":false,"errorCode":"NotLogined"}}`))
			return
		}
		_ = r.ParseForm()
		var p struct {
			Api  string
			Data map[string]any
		}
		_ = json.Unmarshal([]byte(r.PostForm.Get("params")), &p)
		if cp, _ := p.Data["cornerstoneParam"].(map[string]any); cp["protocol"] != "V2" || r.PostForm.Get("region") != "cn-beijing" {
			t.Errorf("params = %v region = %q", p.Data, r.PostForm.Get("region"))
		}
		switch p.Api {
		case "zeldaHttp.dashscopeModel./zelda/api/v1/modelCenter/listFoundationModels":
			page := p.Data["input"].(map[string]any)["pageNo"].(float64)
			if page == 1 {
				_, _ = w.Write(aliyunFixture(t, "models-page1.json"))
			} else {
				_, _ = w.Write(aliyunFixture(t, "models-page2.json"))
			}
		case "zeldaEasy.bailian-commerce.freeTrial.queryFreeTierQuota":
			models := p.Data["queryFreeTierQuotaRequest"].(map[string]any)["models"].([]any)
			if len(models) != 4 {
				t.Errorf("asked for %v", models)
			}
			_, _ = w.Write(aliyunFixture(t, "free-quota.json"))
		// the Token Plan's replies are the module's canonical fixtures
		case "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/subscription":
			_, _ = w.Write(aliyunFixture(t, "subscription.json"))
		case "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage":
			_, _ = w.Write(aliyunFixture(t, "usage.json"))
		case "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/addon/summary":
			_, _ = w.Write(aliyunFixture(t, "addons.json"))
		case "zeldaEasy.bailian-commerce.tokenPlan.queryTokenPlanEquityInfo":
			_, _ = w.Write(aliyunFixture(t, "equity.json"))
		default:
			_, _ = w.Write([]byte(ok(`{}`)))
		}
	}))
}

func TestFreeQuotaIsListedThroughTheCLISignIn(t *testing.T) {
	srv := bailianCLIServer(t)
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["aliyun.cli"] = srv.URL
	a := readBailianFreeQuota(context.Background(), l, Connection{ID: "f", Provider: "aliyun", Mode: "free-quota", Values: map[string]string{"token": "tok"}})
	if a.Status != StatusOK || len(a.Windows) != 2 || !strings.Contains(a.Message, "1 more used up") {
		t.Fatalf("free quota = %+v", a)
	}
	k, img := a.Windows[0], a.Windows[1]
	if k.Name != "kimi-k3" || *k.Left != 820000 || *k.Limit != 1e6 || k.Unit != "tokens" || !k.Expires || k.ResetsAt == nil ||
		img.Name != "qwen-image-2.1-pro" || *img.Left != 10 || img.Unit != "images" {
		t.Fatalf("windows = %+v", a.Windows)
	}
	a = readBailianFreeQuota(context.Background(), l, Connection{Values: map[string]string{"token": "expired"}})
	if a.Status != StatusSignedOut {
		t.Fatalf("expired token = %+v", a)
	}
}

func TestTokenPlanThroughTheCLISignInShowsItsWeek(t *testing.T) {
	srv := bailianCLIServer(t)
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["aliyun.cli"] = srv.URL
	a := readTokenPlan(context.Background(), l, Connection{ID: "p", Provider: "aliyun", Mode: "token-plan", Values: map[string]string{"token": "tok"}})
	if a.Status != StatusOK || a.Plan != "Pro" || a.RenewsAt == nil || len(a.Windows) != 3 ||
		a.Windows[0].Name != "5 hours" || a.Windows[1].Name != "Week" || *a.Windows[1].UsedPercent != 50 ||
		a.Windows[2].Name != "Add-on credits" || *a.Windows[2].Left != 120.5 || *a.Windows[2].Limit != 500 {
		t.Fatalf("token plan = %+v", a)
	}
	if tools := a.Groups[0]; len(tools.Windows) != 2 || tools.Windows[1].Name != "Speech recognition MCP" || *tools.Windows[1].Left != 17940.5 {
		t.Fatalf("tools = %+v", a.Groups)
	}
}

func aliyunFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "providers", "aliyun", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAlibabaCloudLoginTakesTheTokenTheConsoleHandsBack(t *testing.T) {
	l := testLocal(t, nil)
	old := openURL
	t.Cleanup(func() { openURL = old })
	openURL = func(page string) error {
		// play Bailian's console-login page: hand the token to the notice address
		u, err := url.Parse(page)
		if err != nil || u.Host != "bailian.console.aliyun.com" || u.Path != "/console-login" {
			t.Errorf("opened %q", page)
		}
		notice := u.Query().Get("notice") // 127.0.0.1:<port>?state=<state>
		go func() {
			// a stranger without the state is refused
			if r, err := http.Post("http://"+strings.Split(notice, "?")[0]+"/?state=nope", "application/json", strings.NewReader(`{"access_token":"evil"}`)); err == nil {
				if r.StatusCode != http.StatusBadRequest {
					t.Errorf("wrong state answered %d", r.StatusCode)
				}
				r.Body.Close()
			}
			r, err := http.Post("http://"+notice, "application/json", strings.NewReader(`{"data":{"access_token":"tok","console_site":"domestic","console_region":"cn-beijing"}}`))
			if err == nil {
				r.Body.Close()
			}
		}()
		return nil
	}
	v, err := aliyunLogin(context.Background(), l)
	if err != nil || v["token"] != "tok" || v["region"] != "cn-beijing" || v["site"] != "domestic" {
		t.Fatalf("login = %v, %v", v, err)
	}
}

// A sign-in's site reaches the module as bl names it, so the China
// endpoints serve it (design 13 §5.1).
func TestSignInSiteIsNamedAsBlNamesIt(t *testing.T) {
	for in, want := range map[string]string{"domestic": "", "china": "", "": "", "intl": "international", "international": "international"} {
		c := Connection{Provider: "aliyun", Mode: "token-plan", Way: "sign-in", Values: map[string]string{"token": "tok", "site": in}}
		got, err := Resolve(Providers(), testLocal(t, nil), c)
		if err != nil || got.Values["site"] != want || got.Values["token"] != "tok" || c.Values["site"] != in {
			t.Errorf("site %q: %v %v (want %q, the stored values untouched)", in, got.Values, err, want)
		}
	}
}
