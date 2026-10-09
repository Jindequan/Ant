package quota

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 7, 15, 30, 0, 0, time.UTC)

// TestMain keeps tests off the user's screen: no test opens the default
// browser or a Chrome window unless it replaces these itself.
func TestMain(m *testing.M) {
	openURL = func(string) error { return errors.New("tests don't open browsers") }
	signInRunner = func(context.Context, Local, SignIn, string) ([]siteCookie, error) {
		return nil, errors.New("tests don't open sign-in windows")
	}
	claudeLookPath = func() string { return "" }
	claudeRun = func(context.Context, string) ([]byte, error) { return nil, errors.New("tests don't run Claude Code") }
	copilotKeychain = func(string) string { return "" }
	claudeKeychainHas = func() bool { return false }
	claudeKeychainRead = func() ([]byte, error) { return nil, errors.New("tests don't read the Keychain") }
	ghLookPath = func() string { return "" }
	ghToken = func(context.Context, string) string { return "" }
	os.Exit(m.Run())
}

// localOnly lets tests reach only their own servers: a URL a test didn't
// point at one fails fast instead of calling the real provider.
type localOnly struct{}

func (localOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if h := r.URL.Hostname(); h != "127.0.0.1" && h != "localhost" && h != "::1" {
		return nil, errors.New("tests don't call " + h)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func testLocal(t *testing.T, env map[string]string) Local {
	t.Helper()
	return Local{
		Home:   t.TempDir(),
		Getenv: func(k string) string { return env[k] },
		HTTP:   &http.Client{Timeout: 5 * time.Second, Transport: localOnly{}},
		Now:    func() time.Time { return testNow },
		URLs:   map[string]string{},
		// tests that read browser stores model a user who allowed every site
		Browser: func(string) bool { return true },
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func jwt(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

func zcodeConfig(t *testing.T, l Local, providers string) {
	writeFile(t, l.path(".zcode", "v2", "config.json"), `{"provider":{`+providers+`}}`)
}

// --- Z.ai ---

func TestZAIFoundInZCodeAndClaudeSettingsIsOneAccount(t *testing.T) {
	l := testLocal(t, nil)
	zcodeConfig(t, l, `
		"builtin:zai-coding-plan":{"name":"Z.ai - Coding Plan","options":{"apiKey":"abc.secret","baseURL":"https://api.z.ai/api/anthropic"}},
		"builtin:zai":{"name":"Z.ai - API Key","options":{"apiKey":"abc.secret","baseURL":"https://api.z.ai/api/anthropic"}},
		"builtin:bigmodel":{"name":"Bigmodel","options":{"apiKey":"","baseURL":"https://open.bigmodel.cn/api/anthropic"}}`)
	writeFile(t, l.path(".claude", "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://api.z.ai/api/anthropic","ANTHROPIC_AUTH_TOKEN":"abc.secret"}}`)
	s := newTestService(l, NewStore(""))
	conns, err := s.connections()
	if err != nil {
		t.Fatal(err)
	}
	var zai []Connection
	for _, c := range conns {
		if c.Provider == "zai" {
			zai = append(zai, c)
		}
	}
	if len(zai) != 1 || zai[0].Source != "ZCode" || zai[0].Values["region"] != "global" {
		t.Fatalf("zai connections = %+v", zai)
	}
}

func TestZAIReadsPlanWindows(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write(fixture(t, "../providers/zai/testdata/plan.json"))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["zai.global"] = srv.URL
	a := readZAI(context.Background(), l, Connection{ID: "z", Provider: "zai", Mode: "coding-plan", Values: map[string]string{"apiKey": "abc.secret", "region": "global"}})
	if gotAuth != "abc.secret" {
		t.Fatalf("Authorization = %q, want the bare key", gotAuth)
	}
	if a.Status != StatusOK || a.Kind != KindSubscription || a.Plan != "Pro" || !a.Live {
		t.Fatalf("account = %+v", a)
	}
	if len(a.Windows) != 3 || a.Windows[0].Name != "5 hours" || *a.Windows[0].UsedPercent != 12 || a.Windows[1].Name != "Week" || !a.Windows[2].Aside {
		t.Fatalf("windows = %+v", a.Windows)
	}
}

func TestTwoZAIKeysOfOnePlanAreOneAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// the plan is the account's: both keys read the same windows
		_, _ = w.Write([]byte(`{"success":true,"data":{"level":"max","limits":[
			{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":1,"nextResetTime":1791400000038},
			{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":19,"nextResetTime":1791900000984}]}}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["zai.global"] = srv.URL
	zcodeConfig(t, l, `
		"builtin:zai-coding-plan":{"name":"Z.ai - Coding Plan","options":{"apiKey":"key-one","baseURL":"https://api.z.ai/api/anthropic"}},
		"builtin:zai":{"name":"Z.ai - API Key","options":{"apiKey":"key-two","baseURL":"https://api.z.ai/api/anthropic"}}`)
	s := newTestService(l, NewStore(l.AccountsFile()))
	snap := s.Refresh(context.Background())
	if len(snap.Accounts) != 1 || len(snap.Accounts[0].Keys) != 2 || snap.Accounts[0].Source != "ZCode · 2 keys" {
		t.Fatalf("accounts = %+v", snap.Accounts)
	}
	// removing the account hides both keys
	if code, out := call(t, s.Handler(), "DELETE", "/api/accounts/"+snap.Accounts[0].ID, "", nil); code != 200 || len(accountsIn(out)) != 0 {
		t.Fatalf("%d %v", code, out)
	}
}

func TestZAIRefusedKeyAndKeyWithoutPlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "payg" {
			_, _ = w.Write(fixture(t, "../providers/zai/testdata/no-plan.json"))
			return
		}
		if r.Header.Get("Authorization") == "expired" { // Z.ai's real refusal: HTTP 200
			_, _ = w.Write(fixture(t, "../providers/zai/testdata/refused.json"))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["zai.global"] = srv.URL
	a := readZAI(context.Background(), l, Connection{Values: map[string]string{"apiKey": "bad", "region": "global"}})
	if a.Status != StatusSignedOut {
		t.Fatalf("account = %+v", a)
	}
	if a := readZAI(context.Background(), l, Connection{Values: map[string]string{"apiKey": "expired", "region": "global"}}); a.Status != StatusSignedOut {
		t.Fatalf("refused in the body: %+v", a)
	}
	a = readZAI(context.Background(), l, Connection{Values: map[string]string{"apiKey": "payg", "region": "global"}})
	if a.Status != StatusOK || a.Kind != KindAPI || a.Message == "" {
		t.Fatalf("account = %+v", a)
	}
}

// --- ChatGPT ---

func TestChatGPTReadsCodexSignInAndUsage(t *testing.T) {
	var gotAuth, gotAccount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAccount = r.Header.Get("Authorization"), r.Header.Get("chatgpt-account-id")
		_, _ = w.Write([]byte(`{"plan_type":"plus","rate_limit":{
			"primary_window":{"used_percent":72,"limit_window_seconds":18000,"reset_at":1791400000},
			"secondary_window":{"used_percent":20,"limit_window_seconds":604800,"reset_after_seconds":7200}},
			"credits":{"has_credits":true,"unlimited":false,"balance":"12.5"}}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["chatgpt.usage"] = srv.URL
	access := jwt(map[string]any{"exp": float64(testNow.Add(time.Hour).Unix())})
	id := jwt(map[string]any{"email": "me@example.com", "https://api.openai.com/auth": map[string]any{
		"chatgpt_plan_type": "plus", "chatgpt_subscription_active_until": "2026-10-20T00:00:00Z"}})
	writeFile(t, l.path(".codex", "auth.json"), `{"tokens":{"id_token":"`+id+`","access_token":"`+access+`","account_id":"acct-1"}}`)

	conns := discoverChatGPT(l)
	if len(conns) != 1 || conns[0].Source != "~/.codex/auth.json" {
		t.Fatalf("conns = %+v", conns)
	}
	a := readChatGPT(context.Background(), l, conns[0])
	if gotAuth != "Bearer "+access || gotAccount != "acct-1" {
		t.Fatalf("headers %q %q", gotAuth, gotAccount)
	}
	if a.Status != StatusOK || a.Plan != "Plus" || a.User != "me@example.com" || a.RenewsAt == nil {
		t.Fatalf("account = %+v", a)
	}
	if len(a.Windows) != 2 || !a.Windows[1].ResetsAt.Equal(testNow.Add(2*time.Hour)) || a.Balances[0].Amount != "12.5" {
		t.Fatalf("windows = %+v balances = %+v", a.Windows, a.Balances)
	}
}

// A second ChatGPT account is a Codex sign-in in its own folder
// (CODEX_HOME), added on the page; it reads like the first and stays its
// own account.
func TestChatGPTAnotherAccountInItsOwnCodexFolder(t *testing.T) {
	var gotAccount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccount = r.Header.Get("chatgpt-account-id")
		_, _ = w.Write([]byte(`{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_at":1791400000}}}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["chatgpt.usage"] = srv.URL
	access := jwt(map[string]any{"exp": float64(testNow.Add(time.Hour).Unix())})
	id := jwt(map[string]any{"email": "work@example.com"})
	writeFile(t, l.path(".codex-work", "auth.json"), `{"tokens":{"id_token":"`+id+`","access_token":"`+access+`","account_id":"acct-work"}}`)
	s := newTestService(l, NewStore(""))
	code, out := call(t, s.Handler(), "POST", "/api/accounts", `{"provider":"chatgpt","mode":"plan","way":"folder","values":{"folder":"~/.codex-work"}}`, nil)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	var work map[string]any
	for _, a := range accountsIn(out) {
		if a["provider"] == "chatgpt" {
			work = a
		}
	}
	if work == nil || work["status"] != "ok" || work["user"] != "work@example.com" || gotAccount != "acct-work" {
		t.Fatalf("second ChatGPT account = %v (account header %q)", work, gotAccount)
	}
	a := readChatGPT(context.Background(), l, Connection{ID: "x", Provider: "chatgpt", Mode: "plan", Way: "folder", Values: map[string]string{"folder": "~/.codex-missing"}})
	if a.Status != StatusSignedOut || !strings.Contains(a.Message, "CODEX_HOME=~/.codex-missing codex login") {
		t.Fatalf("empty folder = %s %q", a.Status, a.Message)
	}
}

func TestChatGPTExpiredTokenIsReportedWithoutCalling(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["chatgpt.usage"] = srv.URL
	writeFile(t, l.path(".codex", "auth.json"), `{"tokens":{"access_token":"`+jwt(map[string]any{"exp": float64(testNow.Add(-time.Hour).Unix())})+`"}}`)
	a := readChatGPT(context.Background(), l, discoverChatGPT(l)[0])
	if a.Status != StatusSignedOut || called {
		t.Fatalf("account = %+v, called = %v", a, called)
	}
}

// --- DeepSeek, OpenCode ---

func TestDeepSeekFoundInZCodeReadsBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(fixture(t, "../providers/deepseek/testdata/balance.json"))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["deepseek.balance"] = srv.URL
	zcodeConfig(t, l, `"x":{"name":"DeepSeek","options":{"apiKey":"sk-test","baseURL":"https://api.deepseek.com"}}`)
	conns := discoverDeepSeek(l)
	if len(conns) != 1 || conns[0].Source != "ZCode" {
		t.Fatalf("conns = %+v", conns)
	}
	a := readDeepSeek(context.Background(), l, conns[0])
	if a.Status != StatusOK || a.Kind != KindAPI || a.Balances[0] != (Balance{Currency: "CNY", Amount: "23.40", Granted: "10.00", ToppedUp: "13.40"}) {
		t.Fatalf("account = %+v", a)
	}
}

func TestOpenCodeFoundInZCodeReadsWindows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"usage":{"rolling":{"percent":37,"resetsAt":"2026-10-07T18:00:00.000Z"},"weekly":{"percent":10,"resetsAt":"2026-10-10T00:00:00Z"}}}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["opencode.usage"] = srv.URL
	zcodeConfig(t, l, `"x":{"name":"OPENCODE","options":{"apiKey":"sk-oc","baseURL":"https://opencode.ai/zen/go/v1"}}`)
	conns := discoverOpenCode(l)
	if len(conns) != 1 {
		t.Fatalf("conns = %+v", conns)
	}
	a := readOpenCode(context.Background(), l, conns[0])
	if a.Status != StatusOK || len(a.Windows) != 2 || a.Windows[0].Name != "5 hours" || a.Windows[0].ResetsAt == nil {
		t.Fatalf("account = %+v", a)
	}
}

// --- Suno ---

func sunoServer(t *testing.T, billing string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/client":
			if r.Header.Get("Authorization") != "client-cookie" {
				_, _ = w.Write([]byte(`{"response":null}`))
				return
			}
			_, _ = w.Write([]byte(`{"response":{"last_active_session_id":"sess_1","sessions":[{"id":"sess_1","user":{"email_addresses":[{"email_address":"me@example.com"}]}}]}}`))
		case r.URL.Path == "/v1/client/sessions/sess_1/tokens" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"jwt":"session-jwt"}`))
		case r.URL.Path == "/api/billing/info/" && r.Header.Get("Authorization") == "Bearer session-jwt":
			_, _ = w.Write([]byte(billing))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func sunoLocal(t *testing.T, srv *httptest.Server) Local {
	l := testLocal(t, nil)
	l.URLs["suno.auth"], l.URLs["suno.studio"] = srv.URL, srv.URL
	return l
}

func TestSunoFreeCreditsResetAtUTCMidnight(t *testing.T) {
	srv := sunoServer(t, `{"total_credits_left":40,"period":"day","monthly_limit":50,"monthly_usage":120,"is_active":false}`)
	defer srv.Close()
	a := readSuno(context.Background(), sunoLocal(t, srv), Connection{Values: map[string]string{"cookie": "ajs_id=x; __client=client-cookie"}})
	if a.Status != StatusOK || a.Kind != KindFree || a.User != "me@example.com" {
		t.Fatalf("account = %+v", a)
	}
	w := a.Windows[0]
	if *w.Left != 40 || *w.Limit != 50 || w.Used != nil || !w.ResetEstimated || !w.ResetsAt.Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("window = %+v", w)
	}
}

func TestSunoSubscriptionAndEndedSession(t *testing.T) {
	srv := sunoServer(t, `{"total_credits_left":2300,"period":"month","monthly_limit":2500,"monthly_usage":200,"is_active":true,"plan":{"name":"Pro Plan"},"renews_on":"2026-10-21"}`)
	defer srv.Close()
	a := readSuno(context.Background(), sunoLocal(t, srv), Connection{Values: map[string]string{"cookie": "client-cookie"}})
	if a.Kind != KindSubscription || a.Plan != "Pro Plan" || a.RenewsAt == nil || *a.Windows[0].Used != 200 {
		t.Fatalf("account = %+v", a)
	}
	a = readSuno(context.Background(), sunoLocal(t, srv), Connection{Values: map[string]string{"cookie": "stale"}})
	if a.Status != StatusSignedOut {
		t.Fatalf("account = %+v", a)
	}
}

func encryptChromium(t *testing.T, password, host, value string) []byte {
	t.Helper()
	key, _ := pbkdf2.Key(sha1.New, password, []byte("saltysalt"), 1003, 16)
	sum := sha256.Sum256([]byte(host))
	plain := append(sum[:], value...)
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(key)
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, bytes.Repeat([]byte{' '}, aes.BlockSize)).CryptBlocks(out, plain)
	return append([]byte("v10"), out...)
}

func TestSunoCookieIsFoundInChromeProfile(t *testing.T) {
	l := testLocal(t, nil)
	file := l.path("Library", "Application Support", "Google", "Chrome", "Default", "Cookies")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE meta (key TEXT, value TEXT)`,
		`INSERT INTO meta VALUES ('version', '24')`,
		`CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, last_access_utc INTEGER)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO cookies VALUES (?, '__client', '', ?, 13400000000000000)`,
		"auth.suno.com", encryptChromium(t, "chrome-pw", "auth.suno.com", "client-cookie")); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	old := keychainPassword
	keychainPassword = func(service string) (string, error) {
		if service != "Chrome Safe Storage" {
			t.Fatalf("keychain item %q", service)
		}
		return "chrome-pw", nil
	}
	keyMu.Lock()
	keyMemo = map[string][]byte{}
	keyMu.Unlock()
	t.Cleanup(func() { keychainPassword = old })

	conns := discoverSuno(l)
	if len(conns) != 1 || conns[0].Source != "Chrome · Default" || conns[0].Values["cookie"] != "client-cookie" {
		t.Fatalf("conns = %+v", conns)
	}
	if len(discoverSuno(testLocal(t, nil))) != 0 {
		t.Fatal("found Suno with no browser")
	}
}

// --- Alibaba Cloud ---

// The signature vector test moved with the signer (providers/aliyun).

func TestPayAsYouGoWithTheCLIsAccessKeyReadsTheOfficialBill(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// the replies are the module's canonical fixtures
		if q.Get("AccessKeyId") != "LTAIx" || q.Get("Signature") == "" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write(aliyunFixture(t, "bss-refused.json"))
			return
		}
		switch q.Get("Action") {
		case "QueryAccountBalance":
			_, _ = w.Write(aliyunFixture(t, "bss-balance.json"))
		case "QueryBillOverview":
			if q.Get("BillingCycle") == "2026-09" {
				_, _ = w.Write(aliyunFixture(t, "bss-bill-2026-09.json"))
				return
			}
			_, _ = w.Write(aliyunFixture(t, "bss-bill-empty.json"))
		}
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["aliyun.business"] = srv.URL + "/"
	writeFile(t, l.path(".aliyun", "config.json"), `{"current":"default","profiles":[{"name":"default","mode":"AK","access_key_id":"LTAIx","access_key_secret":"s"}]}`)
	conns := discoverAliyunAccessKey(l)
	if len(conns) != 1 || conns[0].Mode != "payg" || conns[0].Source != "~/.aliyun/config.json" {
		t.Fatalf("conns = %+v", conns)
	}
	a := readBailianPayg(context.Background(), l, conns[0])
	// exact: 1.5 + 0.004321, no rounding to cents
	if a.Status != StatusOK || a.Balances[0].Amount != "1234.50" || a.Groups[0].Balances[1].Amount != "1.504321" || !strings.Contains(a.Groups[0].Note, "last month ¥12.304321") {
		t.Fatalf("account = %+v", a)
	}
	conns[0].Values["accessKeyId"] = "other"
	a = readBailianPayg(context.Background(), l, conns[0])
	if a.Status == StatusOK || !strings.Contains(a.Message, "InvalidAccessKeyId") {
		t.Fatalf("refused = %+v", a)
	}
}

// --- the service and its API ---

func call(t *testing.T, h http.Handler, method, path, body string, header map[string]string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Ant", "1")
	for k, v := range header {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func accountsIn(snap map[string]any) []map[string]any {
	var out []map[string]any
	for _, a := range snap["accounts"].([]any) {
		out = append(out, a.(map[string]any))
	}
	return out
}

func TestAddEditAndRemoveAccountsOnThePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"5.00","granted_balance":"0.00","topped_up_balance":"5.00"}]}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["deepseek.balance"] = srv.URL
	zcodeConfig(t, l, `"x":{"name":"DeepSeek","options":{"apiKey":"sk-found","baseURL":"https://api.deepseek.com"}}`)
	store := NewStore(l.AccountsFile())
	s := newTestService(l, store)
	h := s.Handler()
	s.Refresh(context.Background())

	// a missing field is named
	code, out := call(t, h, "POST", "/api/accounts", `{"provider":"deepseek","mode":"api","values":{}}`, nil)
	if code != 400 || out["error"] != "API key is needed" {
		t.Fatalf("%d %v", code, out)
	}
	// ChatGPT can't be added by hand: it says how to connect
	code, out = call(t, h, "POST", "/api/accounts", `{"provider":"chatgpt","mode":"plan","values":{}}`, nil)
	if code != 400 || !strings.Contains(out["error"].(string), "codex login") {
		t.Fatalf("%d %v", code, out)
	}
	// a pasted key is read at once
	code, out = call(t, h, "POST", "/api/accounts", `{"provider":"deepseek","mode":"api","label":"Work","values":{"apiKey":"sk-mine"}}`, nil)
	if code != 200 || len(accountsIn(out)) != 2 {
		t.Fatalf("%d %v", code, out)
	}
	var added map[string]any
	for _, a := range accountsIn(out) {
		if a["origin"] == "added" {
			added = a
		}
	}
	if added == nil || added["label"] != "Work" || added["status"] != "ok" {
		t.Fatalf("added = %v", added)
	}
	id := added["id"].(string)

	// the edit form never gets the secret back
	code, view := call(t, h, "GET", "/api/accounts/"+id, "", nil)
	if code != 200 || view["values"].(map[string]any)["apiKey"] != nil || len(view["secretsSet"].([]any)) != 1 {
		t.Fatalf("%d %v", code, view)
	}
	// editing the label keeps the key
	code, _ = call(t, h, "PUT", "/api/accounts/"+id, `{"label":"Personal","values":{"apiKey":""}}`, nil)
	d, _ := store.Data()
	if code != 200 || d.Accounts[0].Label != "Personal" || d.Accounts[0].Values["apiKey"] != "sk-mine" {
		t.Fatalf("%d %+v", code, d)
	}
	info, _ := os.Stat(l.AccountsFile())
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("accounts file mode %v", info.Mode())
	}

	// pasting the key ZCode already has is the same account, not a second
	code, out = call(t, h, "POST", "/api/accounts", `{"provider":"deepseek","mode":"api","values":{"apiKey":"sk-found"}}`, nil)
	if code != 200 || len(accountsIn(out)) != 2 {
		t.Fatalf("%d %v", code, out)
	}
	// removing a found account hides it; removing an added one deletes it
	foundID := fingerprint("deepseek", "api", "sk-found")
	if code, out = call(t, h, "DELETE", "/api/accounts/"+foundID, "", nil); code != 200 || len(accountsIn(out)) != 1 {
		t.Fatalf("%d %v", code, out)
	}
	if code, out = call(t, h, "DELETE", "/api/accounts/"+id, "", nil); code != 200 || out["accounts"] != nil && len(accountsIn(out)) != 0 {
		t.Fatalf("%d %v", code, out)
	}
	if snap := s.Refresh(context.Background()); len(snap.Accounts) != 0 {
		t.Fatalf("hidden account came back: %+v", snap.Accounts)
	}
}

func TestChangesAreGuarded(t *testing.T) {
	h := NewService(testLocal(t, nil), NewStore(""), Options{Demo: true}).Handler()
	req := httptest.NewRequest(http.MethodGet, "http://evil.example/api/quota", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host: %d", rec.Code)
	}
	// another machine naming localhost, as it can when -addr isn't loopback
	req = httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/quota", nil)
	req.RemoteAddr = "192.168.1.20:50000"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign peer: %d", rec.Code)
	}
	if code, _ := call(t, h, "POST", "/api/quota/refresh", "", map[string]string{"X-Ant": ""}); code != 403 {
		t.Fatalf("no header: %d", code)
	}
	if code, _ := call(t, h, "POST", "/api/accounts", `{}`, map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Fatalf("foreign origin: %d", code)
	}
	if code, _ := call(t, h, "POST", "/api/accounts", `{"provider":"x","extra":1}`, nil); code != 400 {
		t.Fatalf("unknown field: %d", code)
	}
	code, out := call(t, h, "POST", "/api/quota/refresh", "", map[string]string{"Origin": "http://localhost:8080"})
	if code != 200 || out["demo"] != true || len(accountsIn(out)) != 23 {
		t.Fatalf("refresh: %d %v", code, out)
	}
	code, _ = call(t, h, "GET", "/api/providers", "", nil)
	if code != 200 {
		t.Fatalf("providers: %d", code)
	}
}

func TestServiceStopsWithContext(t *testing.T) {
	s := NewService(testLocal(t, nil), NewStore(""), Options{Interval: time.Millisecond, Demo: true})
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	time.Sleep(10 * time.Millisecond)
	cancel()
	s.Wait()
	if len(s.Snapshot().Accounts) == 0 {
		t.Fatal("no snapshot after refresh")
	}
}

func TestTheOldBailianAccountBecomesItsInstances(t *testing.T) {
	out := migrate([]Connection{
		{ID: "aliyun-console-signin", Provider: "aliyun", Mode: "console", Values: map[string]string{"session": "s", "dashscopeKey": "k"}},
		{ID: "aliyun-payg-signin", Provider: "aliyun", Mode: "payg", Values: map[string]string{"session": "newer"}},
		{ID: "z", Provider: "zai", Mode: "coding-plan"},
	})
	modes := map[string]string{}
	for _, c := range out {
		modes[c.Mode] = c.Values["session"]
	}
	if len(out) != 4 || modes["token-plan"] != "s" || modes["free-quota"] != "s" || modes["payg"] != "newer" || modes["console"] != "" {
		t.Fatalf("migrated = %+v", out)
	}
}

// fixture reads a canonical provider reply, shared with the provider
// modules' own tests (one file, not copies).
func fixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
