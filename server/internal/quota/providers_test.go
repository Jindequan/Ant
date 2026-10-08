package quota

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// keyServer answers path for the bearer key "k", and 401 otherwise.
func keyServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func conn(provider, mode string, values map[string]string) Connection {
	v := map[string]string{"apiKey": "k"}
	for a, b := range values {
		v[a] = b
	}
	return Connection{ID: provider, Provider: provider, Mode: mode, Values: v}
}

func TestKimiCodeWindowsAndMoonshotBalance(t *testing.T) {
	srv := keyServer(t, map[string]string{
		"/coding/v1/usages": `{"usage":{"limit":"100","used":"25","resetTime":"2026-10-12T00:00:00Z"},
			"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":"20","remaining":"15","resetTime":"2026-10-07T20:00:00Z"}}]}`,
		"/v1/users/me/balance": `{"code":0,"data":{"available_balance":49.58,"voucher_balance":46.58,"cash_balance":3}}`,
	})
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["kimi.code"], l.URLs["kimi.api"] = srv.URL+"/coding/v1/usages", srv.URL
	a := readKimiCode(context.Background(), l, conn("kimi", "code", nil))
	if a.Status != StatusOK || len(a.Windows) != 2 || a.Windows[0].Name != "5 hours" || *a.Windows[0].UsedPercent != 25 ||
		a.Windows[1].Name != "Week" || a.Windows[1].ResetsAt == nil {
		t.Fatalf("kimi code = %+v", a)
	}
	a = readMoonshotBalance(context.Background(), l, conn("kimi", "api", map[string]string{"region": "cn"}))
	if a.Status != StatusOK || a.Balances[0] != (Balance{Currency: "CNY", Amount: "49.58", Granted: "46.58", ToppedUp: "3.00"}) {
		t.Fatalf("moonshot = %+v", a)
	}
	a = readMoonshotBalance(context.Background(), l, Connection{Values: map[string]string{"apiKey": "bad"}})
	if a.Status != StatusSignedOut {
		t.Fatalf("refused = %+v", a)
	}
}

func TestMiniMaxPlanGroups(t *testing.T) {
	srv := keyServer(t, map[string]string{"/v1/token_plan/remains": `{"model_remains":[
		{"model_name":"general","start_time":1791380000000,"end_time":1791398000000,"current_interval_remaining_percent":60,"current_interval_status":1,
		 "weekly_end_time":1791900000000,"current_weekly_remaining_percent":90,"current_weekly_status":1},
		{"model_name":"video","start_time":1791380000000,"end_time":1791466400000,"current_interval_remaining_percent":0,"current_interval_status":2,"current_weekly_status":3}],
		"base_resp":{"status_code":0,"status_msg":"success"}}`})
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["minimax"] = srv.URL
	a := readMiniMaxPlan(context.Background(), l, conn("minimax", "token-plan", map[string]string{"region": "cn"}))
	if a.Status != StatusOK || len(a.Windows) != 3 || a.Windows[0].Name != "5 hours" || *a.Windows[0].UsedPercent != 40 ||
		a.Windows[1].Name != "Week" || a.Windows[2].Name != "Video · Day" && a.Windows[2].Name != "Video · 24 hours" || !a.Windows[2].Aside || *a.Windows[2].UsedPercent != 100 {
		t.Fatalf("minimax = %+v", a.Windows)
	}
}

func TestPrepaidBalances(t *testing.T) {
	srv := keyServer(t, map[string]string{
		"/api/v1/credits": `{"data":{"total_credits":20,"total_usage":3.5}}`,
		"/v1/user/info":   `{"code":20000,"data":{"totalBalance":"88.88","balance":"14","chargeBalance":"74.88","email":"me@example.com"}}`,
		"/v1/accounts":    `{"object":"account","balance":26.00,"total_cash_balance":0,"total_voucher_balance":26}`,
	})
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["openrouter"], l.URLs["siliconflow"], l.URLs["stepfun"] = srv.URL, srv.URL, srv.URL
	if a := readOpenRouter(context.Background(), l, conn("openrouter", "credits", nil)); a.Status != StatusOK || a.Balances[0].Amount != "16.50" || a.Balances[0].Currency != "USD" {
		t.Fatalf("openrouter = %+v", a)
	}
	if a := readSiliconFlow(context.Background(), l, conn("siliconflow", "api", nil)); a.Status != StatusOK ||
		a.Balances[0] != (Balance{Currency: "CNY", Amount: "88.88", Granted: "14.00", ToppedUp: "74.88"}) || a.User != "me@example.com" {
		t.Fatalf("siliconflow = %+v", a)
	}
	if a := readStepFun(context.Background(), l, conn("stepfun", "api", map[string]string{"region": "ai"})); a.Status != StatusOK || a.Balances[0].Currency != "USD" || a.Balances[0].Amount != "26.00" {
		t.Fatalf("stepfun = %+v", a)
	}
}

func TestKeysAreFoundInOpenCodeAndSplitByProvider(t *testing.T) {
	l := testLocal(t, map[string]string{"OPENROUTER_API_KEY": "sk-or-env"})
	writeFile(t, l.path(".local", "share", "opencode", "auth.json"), `{
		"kimi-for-coding":{"type":"api","key":"sk-kimi-1"},
		"zhipuai-coding-plan":{"type":"api","key":"glm.cn"},
		"openrouter":{"type":"api","key":"sk-or-1"},
		"anthropic":{"type":"oauth","access":"x"}}`)
	zcodeConfig(t, l, `
		"a":{"name":"Z.ai","options":{"apiKey":"glm.global","baseURL":"https://api.z.ai/api/anthropic"}},
		"b":{"name":"MiniMax","options":{"apiKey":"sk-cp-1","baseURL":"https://api.minimaxi.com/anthropic"}},
		"c":{"name":"MiniMax PAYG","options":{"apiKey":"sk-api-1","baseURL":"https://api.minimaxi.com/anthropic"}}`)
	s := newTestService(l, NewStore(""))
	conns, err := s.connections()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, c := range conns {
		got[c.Provider+"/"+c.Mode] = append(got[c.Provider+"/"+c.Mode], c.Source+":"+c.Values["apiKey"])
	}
	want := map[string]string{
		"kimi/code":            "OpenCode:sk-kimi-1",
		"bigmodel/coding-plan": "OpenCode:glm.cn",
		"zai/coding-plan":      "ZCode:glm.global",
		"minimax/token-plan":   "ZCode:sk-cp-1",
		"openrouter/credits":   "OpenCode:sk-or-1,$OPENROUTER_API_KEY:sk-or-env",
	}
	for k, v := range want {
		if strings.Join(got[k], ",") != v {
			t.Errorf("%s = %v, want %s", k, got[k], v)
		}
	}
}
