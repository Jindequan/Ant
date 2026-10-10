package quota

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClaudeCodeUsage(t *testing.T) {
	old := claudeRun
	defer func() { claudeRun = old }()
	claudeRun = func(context.Context, string) ([]byte, error) {
		return []byte(`{"type":"result","is_error":false,"result":"Current session: 12% used · resets 6pm (UTC)\nCurrent week (all models): 40% used · resets Oct 13 at 9am (UTC)\nCurrent week (Opus): 5% used"}`), nil
	}
	l := testLocal(t, nil)
	a := readClaudeCode(context.Background(), l, Connection{ID: "claude-code", Provider: "claude", Mode: "plan", Way: "claude-code", Values: map[string]string{"binary": "claude"}})
	if a.Status != StatusOK || len(a.Windows) != 3 || a.WayName != "Claude Code" {
		t.Fatalf("%+v", a)
	}
	if w := a.Windows[0]; w.Name != "5 hours" || *w.UsedPercent != 12 || w.ResetsAt.Hour() != 18 || w.ResetsAt.Day() != 7 {
		t.Fatalf("session %+v", w)
	}
	if w := a.Windows[1]; w.Name != "Week" || w.ResetsAt == nil || w.ResetsAt.Day() != 13 {
		t.Fatalf("week %+v", w)
	}
	if a.Windows[2].Name != "Week · Opus" {
		t.Fatal(a.Windows[2].Name)
	}

	claudeRun = func(context.Context, string) ([]byte, error) {
		return []byte(`{"type":"result","is_error":false,"result":"/usage is only available for subscription plans."}`), nil
	}
	if a := readClaudeCode(context.Background(), l, Connection{Values: map[string]string{"binary": "claude"}}); a.Status != StatusSignedOut {
		t.Fatalf("%+v", a)
	}
}

func TestClaudeSignInUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("anthropic-beta") == "" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"five_hour":{"utilization":30,"resets_at":"2026-10-07T18:00:00.000+00:00"},"seven_day":{"utilization":55,"resets_at":"2026-10-13T09:00:00Z"},"seven_day_opus":null,"extra_usage":{"is_enabled":false},"note":"x"}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["claude.usage"] = srv.URL
	writeFile(t, l.path(".claude", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"tok","expiresAt":9999999999999,"subscriptionType":"max"}}`)
	cs := discoverClaudeSignIn(l)
	if len(cs) != 1 {
		t.Fatal(cs)
	}
	cs[0].Way = "sign-in"
	a := readClaudeOAuth(context.Background(), l, cs[0])
	if a.Status != StatusOK || len(a.Windows) != 2 || a.Plan != "Max" || *a.Windows[1].UsedPercent != 55 {
		t.Fatalf("%+v", a)
	}
	// this Mac's Claude Code login is one account however it's read
	if a.Identity != claudeLocal {
		t.Fatalf("found sign-in identity = %q", a.Identity)
	}
	a = readClaudeOAuth(context.Background(), l, Connection{Way: "token", Values: map[string]string{"accessToken": "bad"}})
	if a.Status != StatusSignedOut {
		t.Fatalf("%+v", a)
	}
	// pasted tokens are accounts of their own: two of them stay two cards
	pasted := func(id string) Account {
		return readClaudeOAuth(context.Background(), l, Connection{ID: id, Provider: "claude", Mode: "plan", Way: "token", Origin: OriginAdded, Values: map[string]string{"accessToken": "tok"}})
	}
	if cards := mergeSame([]Account{pasted("claude-a"), pasted("claude-b")}); len(cards) != 2 || cards[0].Identity != "" {
		t.Fatalf("two pasted Claude tokens became %d card(s): %+v", len(cards), cards)
	}
}

func TestCopilotWays(t *testing.T) {
	var gotEditor string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEditor = r.Header.Get("Editor-Version")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "token gh") {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"login":"octo","copilot_plan":"individual","quota_reset_date_utc":"2026-11-01T00:00:00Z","quota_snapshots":{"premium_interactions":{"entitlement":300,"quota_remaining":212.5,"percent_remaining":70.8,"unlimited":false},"chat":{"unlimited":true},"completions":{"unlimited":true}}}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["copilot"] = srv.URL
	writeFile(t, l.path(".copilot", "config.json"), "// comment\n{\"lastLoggedInUser\":{\"host\":\"https://github.com\",\"login\":\"Octo\"}}")
	writeFile(t, l.path(".config", "github-copilot", "apps.json"), `{"github.com:Iv1.b507a08c87ecfe98":{"user":"octo","oauth_token":"ghu_editor"}}`)
	writeFile(t, l.path(".config", "gh", "hosts.yml"), "github.com:\n    user: octo\n    oauth_token: gho_cli\n    git_protocol: https\n")

	old := copilotKeychain
	defer func() { copilotKeychain = old }()
	copilotKeychain = func(account string) string {
		if account == "https://github.com:Octo" {
			return "ghu_keychain"
		}
		return ""
	}
	for _, x := range []struct {
		way    string
		cs     []Connection
		editor string
	}{{"cli", discoverCopilotCLI(l), "copilot/1.0.88"}, {"editor", discoverCopilotEditor(l), "vscode/1.140.0"}, {"gh", discoverGitHubCLI(l), "vscode/1.140.0"}} {
		if len(x.cs) != 1 {
			t.Fatalf("%s: %v", x.way, x.cs)
		}
		x.cs[0].Way = x.way
		a := readCopilot(context.Background(), l, x.cs[0])
		if a.Status != StatusOK || a.Plan != "Pro" || len(a.Windows) != 1 || *a.Windows[0].Left != 212.5 || *a.Windows[0].Limit != 300 || a.Identity != "copilot:octo" {
			t.Fatalf("%s: %+v", x.way, a)
		}
		if gotEditor != x.editor {
			t.Fatalf("%s sent %s", x.way, gotEditor)
		}
	}
	// gh keeps its token in the Keychain: gh hands it over
	writeFile(t, l.path(".config", "gh", "hosts.yml"), "github.com:\n    users:\n        octo:\n    git_protocol: https\n    user: octo\n")
	oldLook, oldTok := ghLookPath, ghToken
	defer func() { ghLookPath, ghToken = oldLook, oldTok }()
	ghLookPath = func() string { return "/bin/gh" }
	ghToken = func(context.Context, string) string { return "gho_fromgh" }
	cs := discoverGitHubCLI(l)
	if len(cs) != 1 || cs[0].Values["token"] != "" {
		t.Fatalf("%+v", cs)
	}
	if a := readCopilot(context.Background(), l, cs[0]); a.Status != StatusOK {
		t.Fatalf("%+v", a)
	}
	if a := readCopilot(context.Background(), l, Connection{Way: "token", Values: map[string]string{"token": "bad"}}); a.Status != StatusSignedOut {
		t.Fatalf("%+v", a)
	}
}

func TestGeminiCodeAssist(t *testing.T) {
	var refreshed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			r.ParseForm()
			if r.Form.Get("refresh_token") != "rt" || r.Form.Get("client_id") != "fixture-client" || r.Form.Get("client_secret") != "fixture-secret" {
				w.WriteHeader(400)
				return
			}
			refreshed = true
			w.Write([]byte(`{"access_token":"fresh"}`))
		case "/v1internal:loadCodeAssist":
			if r.Header.Get("Authorization") != "Bearer fresh" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"cloudaicompanionProject":"proj-1","currentTier":{"id":"free-tier","name":"Gemini Code Assist for individuals"}}`))
		case "/v1internal:retrieveUserQuota":
			w.Write([]byte(`{"buckets":[{"modelId":"gemini-2.5-pro","remainingFraction":0.75,"resetTime":"2026-10-08T00:00:00Z"},{"modelId":"gemini-2.5-flash","remainingFraction":1},{"modelId":"gemini-2.5-pro_vertex","remainingFraction":0.1}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	l := testLocal(t, map[string]string{"GEMINI_OAUTH_CLIENT_ID": "fixture-client", "GEMINI_OAUTH_CLIENT_SECRET": "fixture-secret"})
	l.URLs["google.token"] = srv.URL + "/token"
	l.URLs["google.codeassist"] = srv.URL
	writeFile(t, l.path(".gemini", "oauth_creds.json"), `{"access_token":"old","refresh_token":"rt","expiry_date":1}`)
	cs := discoverGemini(l)
	if len(cs) != 1 {
		t.Fatal(cs)
	}
	a := readGemini(context.Background(), l, cs[0])
	if !refreshed || a.Status != StatusOK || len(a.Windows) != 2 || a.Kind != KindFree {
		t.Fatalf("%+v", a)
	}
	if w := a.Windows[1]; w.Name != "gemini-2.5-pro" || *w.UsedPercent != 25 || w.ResetsAt == nil {
		t.Fatalf("%+v", w)
	}
}

func TestVolcengineCLIWaysAndFallback(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if len(methods) == 1 { // the first try answers 502: the reader falls back to GET
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.Contains(auth, "Credential=AKVE/") && !strings.Contains(auth, "Credential=AKARK/") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.Contains(auth, "AKVE") && r.Header.Get("X-Security-Token") != "stve" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"Result":{"QuotaUsage":[{"Level":"session","Percent":10,"ResetTimestamp":1791400000}]}}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["volc.api"], l.URLs["volc.ark"] = srv.URL, srv.URL
	writeFile(t, l.path(".volcengine", "config.json"), `{"current":"default","profiles":{"default":{"access-key":"AKVE","secret-key":"s","session-token":"stve","sts-expiration":"2026-10-07T16:00:00Z"}}}`)
	writeFile(t, l.path(".arkcli", ".env"), "VOLCENGINE_ACCESS_KEY_ID=AKARK\nVOLCENGINE_SECRET_ACCESS_KEY='s'\nVOLCENGINE_REFRESH_TOKEN=rt\n")

	ve := discoverVolcCLI(l)
	if len(ve) != 1 || ve[0].Values["accessKeyId"] != "" {
		t.Fatalf("ve = %+v", ve) // the keys are read at each read, not copied
	}
	a := readVolcCodingPlan(context.Background(), l, ve[0])
	if a.Status != StatusOK || len(a.Windows) != 1 || a.WayName != "Volcengine CLI" || len(methods) != 2 || methods[1] != http.MethodGet {
		t.Fatalf("ve read = %+v, %v", a, methods)
	}
	ark := discoverArkCLI(l)
	if len(ark) != 1 {
		t.Fatal(ark)
	}
	if a := readVolcCodingPlan(context.Background(), l, ark[0]); a.Status != StatusOK || methods[len(methods)-1] != http.MethodPost {
		t.Fatalf("arkcli read = %+v, %v", a, methods)
	}
	// ve's short-lived keys lapse: Ant says to renew them and doesn't touch its refresh token
	l.Now = func() time.Time { return testNow.Add(2 * time.Hour) }
	if a := readVolcCodingPlan(context.Background(), l, ve[0]); a.Status != StatusSignedOut || !strings.Contains(a.Message, "ve") {
		t.Fatalf("expired = %+v", a)
	}
}

func TestBailianCLIConfig(t *testing.T) {
	l := testLocal(t, nil)
	writeFile(t, l.path(".bailian", "config.json"), `{"active_config":"token-plan","token-plan":{"access_token":"tok","console_site":"domestic","console_region":"cn-beijing"}}`)
	if cs := discoverBLConfig("token-plan")(l); len(cs) != 1 || cs[0].Values["token"] != "" {
		t.Fatalf("%+v", cs)
	}
	if cs := discoverBLConfig("payg")(l); len(cs) != 0 {
		t.Fatalf("payg without an AccessKey = %+v", cs)
	}
	v := blProfile(l.path(".bailian", "config.json"))
	if v["token"] != "tok" || v["region"] != "cn-beijing" || v["site"] != "" {
		t.Fatal(v)
	}
	writeFile(t, l.path(".bailian", "config.json"), `{"active_config":"x"}`)
	got := withBLConfig(func(context.Context, Local, Connection) Account { t.Fatal("read without a sign-in"); return Account{} })(
		context.Background(), l, Connection{Provider: "aliyun", Mode: "token-plan", Way: "bl", Values: map[string]string{"blConfig": l.path(".bailian", "config.json")}})
	if got.Status != StatusSignedOut {
		t.Fatalf("%+v", got)
	}
}

func TestOldConnectionsNameTheirWay(t *testing.T) {
	out := migrate([]Connection{
		{ID: "v1", Provider: "volcengine", Mode: "coding-plan", Values: map[string]string{"refreshToken": "r"}},
		{ID: "v2", Provider: "volcengine", Mode: "coding-plan", Values: map[string]string{"accessKeyId": "a"}},
		{ID: "a1", Provider: "aliyun", Mode: "token-plan", Values: map[string]string{"token": "t"}},
		{ID: "s1", Provider: "suno", Mode: "account", Values: map[string]string{"session": "x"}},
		{ID: "s2", Provider: "suno", Mode: "account", Values: map[string]string{"cookie": "x"}},
	})
	if out[0].Way != "sign-in" || out[1].Way != "accesskey" || out[2].Way != "sign-in" || out[3].Way != "sign-in" || out[4].Way != "cookie" {
		t.Fatalf("%+v", out)
	}
}

func TestMiMoPastedCookie(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "serviceToken=st") {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"code":0,"data":{"balance":"12.50","currency":"CNY"}}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["mimo"] = srv.URL
	a := readMiMoBalance(context.Background(), l, Connection{Provider: "xiaomi", Mode: "api", Way: "cookie", Values: map[string]string{"cookie": "Cookie: userId=1; serviceToken=st"}})
	if a.Status != StatusOK || a.Source != "Pasted cookie" {
		t.Fatalf("%+v", a)
	}
	if a := readMiMoBalance(context.Background(), l, Connection{Provider: "xiaomi", Mode: "api", Way: "cookie", Values: map[string]string{}}); a.Status != StatusSignedOut {
		t.Fatalf("%+v", a)
	}
}
