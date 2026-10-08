package quota

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func resetModelCache() {
	modelCache.Lock()
	modelCache.m = map[string]modelEntry{}
	modelCache.Unlock()
}

func ids(ms []Model) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.ID+":"+m.Media)
	}
	return strings.Join(out, ",")
}

func TestCodexModelsForTheChatGPTSignIn(t *testing.T) {
	resetModelCache()
	var version string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			version = r.URL.Query().Get("client_version")
			w.Write([]byte(`{"models":[{"slug":"gpt-5.5-codex","display_name":"GPT-5.5 Codex","visibility":"list"},{"slug":"internal","visibility":"hide"}]}`))
			return
		}
		w.Write([]byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_at":1791400000}}}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["chatgpt.usage"], l.URLs["chatgpt.models"] = srv.URL, srv.URL+"/models"
	writeFile(t, l.path(".codex", "auth.json"), `{"tokens":{"access_token":"`+jwt(map[string]any{"exp": float64(testNow.Add(time.Hour).Unix())})+`","account_id":"acct-1"}}`)
	writeFile(t, l.path(".codex", "version.json"), `{"latest_version":"0.141.0"}`)
	a := readChatGPT(context.Background(), l, discoverChatGPT(l)[0])
	if ids(a.Models) != "gpt-5.5-codex:text" || a.Models[0].Name != "GPT-5.5 Codex" || version != "0.141.0" {
		t.Fatalf("%+v %q", a.Models, version)
	}
}

func TestClaudeFromTheKeychainListsModels(t *testing.T) {
	resetModelCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer kc" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5"},{"id":"claude-sonnet-5-5"}]}`))
			return
		}
		w.Write([]byte(`{"five_hour":{"utilization":10},"seven_day":{"utilization":20}}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["claude.usage"], l.URLs["claude.models"] = srv.URL, srv.URL+"/v1/models"
	oldHas, oldRead := claudeKeychainHas, claudeKeychainRead
	defer func() { claudeKeychainHas, claudeKeychainRead = oldHas, oldRead }()
	claudeKeychainHas = func() bool { return true }
	claudeKeychainRead = func() ([]byte, error) {
		return []byte(`{"claudeAiOauth":{"accessToken":"kc","expiresAt":9999999999999,"subscriptionType":"max"}}`), nil
	}
	cs := discoverClaudeSignIn(l)
	if len(cs) != 1 || cs[0].Source != "Keychain · Claude Code" {
		t.Fatalf("%+v", cs)
	}
	cs[0].Way = "sign-in"
	a := readClaudeOAuth(context.Background(), l, cs[0])
	if a.Status != StatusOK || a.Plan != "Max" || ids(a.Models) != "claude-opus-5-5:text,claude-sonnet-5-5:text" {
		t.Fatalf("%+v", a)
	}
}

func TestCopilotModelsThroughItsToken(t *testing.T) {
	resetModelCache()
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			w.Write([]byte(`{"login":"octo","copilot_plan":"individual","quota_snapshots":{}}`))
		case "/token":
			if r.Header.Get("Authorization") != "token gho_x" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"token":"tid=copilot","endpoints":{"api":"` + srvURL + `"}}`))
		case "/models":
			if r.Header.Get("Authorization") != "Bearer tid=copilot" || r.Header.Get("Copilot-Integration-Id") == "" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"data":[{"id":"gpt-5.5","name":"GPT-5.5","model_picker_enabled":true,"capabilities":{"type":"chat"}},
				{"id":"claude-sonnet-5-5","model_picker_enabled":true,"capabilities":{"type":"chat"},"policy":{"state":"disabled"}},
				{"id":"text-embedding-3-small","model_picker_enabled":false,"capabilities":{"type":"embeddings"}}]}`))
		}
	}))
	defer srv.Close()
	srvURL = srv.URL
	l := testLocal(t, nil)
	l.URLs["copilot"], l.URLs["copilot.token"] = srv.URL+"/user", srv.URL+"/token"
	a := readCopilot(context.Background(), l, Connection{Way: "token", Values: map[string]string{"token": "gho_x"}})
	if a.Status != StatusOK || ids(a.Models) != "gpt-5.5:text" {
		t.Fatalf("%+v", a)
	}
}

func TestKeyModelsAndCache(t *testing.T) {
	resetModelCache()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/balance":
			w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"10.00","granted_balance":"0.00","topped_up_balance":"10.00"}]}`))
		case "/models":
			calls++
			w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-v4"},{"id":"deepseek-v4-reasoner"},{"id":"text-embedding-x"}]}`))
		}
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["deepseek.balance"], l.URLs["deepseek.models"] = srv.URL+"/user/balance", srv.URL+"/models"
	c := Connection{Provider: "deepseek", Mode: "api", Values: map[string]string{"apiKey": "sk-1"}}
	a := readDeepSeek(context.Background(), l, c)
	if a.Status != StatusOK || ids(a.Models) != "deepseek-v4:text,deepseek-v4-reasoner:text,text-embedding-x:embedding" {
		t.Fatalf("%+v", a)
	}
	readDeepSeek(context.Background(), l, c)
	if calls != 1 {
		t.Fatalf("listed %d times within the cache's time", calls)
	}
	l.Now = func() time.Time { return testNow.Add(7 * time.Hour) }
	readDeepSeek(context.Background(), l, c)
	if calls != 2 {
		t.Fatalf("listed %d times after the cache's time", calls)
	}
}

func TestBailianCLIKeyListsTokenPlanModels(t *testing.T) {
	resetModelCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/compatible-mode/v1/models" && r.Header.Get("Authorization") == "Bearer sk-sp-1" {
			w.Write([]byte(`{"data":[{"id":"qwen3.8-max"},{"id":"wan3.0-video"}]}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	writeFile(t, l.path(".bailian", "config.json"), `{"active_config":"token-plan","token-plan":{"access_token":"tok","api_key":"sk-sp-1","base_url":"`+srv.URL+`/compatible-mode/v1"}}`)
	read := withBLConfig(func(_ context.Context, l Local, c Connection) Account {
		a := Account{Provider: "aliyun", Mode: c.Mode, Status: StatusOK}
		return a
	})
	a := read(context.Background(), l, Connection{Provider: "aliyun", Mode: "token-plan", Way: "bl", Values: map[string]string{"blConfig": l.path(".bailian", "config.json")}})
	if ids(a.Models) != "qwen3.8-max:text,wan3.0-video:video" {
		t.Fatalf("%+v", a.Models)
	}
}
