package quotasource

import (
	"ant/server/internal/providers/deepseek"
	"ant/server/internal/transport"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/quota"
	"ant/server/internal/runtime"
	"ant/server/internal/store/sqlite"
	"ant/server/internal/supply"
)

// fixtureCommandBudget is an anti-hang guard, not a graded threshold: these
// tests are not about the 50ms product budget, which -race builds overrun
// under load (see sqlite.Config.CommandBudget).
const fixtureCommandBudget = 10 * time.Second

// Every value a connection may carry as a secret stays out of the locator.
func TestCredentialLocatorNeverHoldsASecret(t *testing.T) {
	secrets := map[string]string{"apiKey": "SECRET-1", "cookie": "SECRET-2", "token": "SECRET-3", "session": "SECRET-4",
		"secretAccessKey": "SECRET-5", "accessKeySecret": "SECRET-6", "refreshToken": "SECRET-7", "sts": "SECRET-8",
		"sessionToken": "SECRET-9", "accessKeyId": "SECRET-10"}
	values := map[string]string{"authPath": "/home/u/.codex/auth.json", "region": "cn-beijing"}
	for k, v := range secrets {
		values[k] = v
	}
	c := credential(quota.Connection{ID: "x", Provider: "deepseek", Mode: "api", Way: "api", Origin: quota.OriginFound, Source: "ZCode", Values: values})
	if strings.Contains(string(c.Locator), "SECRET") {
		t.Fatalf("locator carries a secret: %s", c.Locator)
	}
	var loc map[string]string
	if err := json.Unmarshal(c.Locator, &loc); err != nil || loc["authPath"] != "/home/u/.codex/auth.json" || loc["source"] != "ZCode" || loc["mode"] != "api" {
		t.Fatalf("locator = %s, %v", c.Locator, err)
	}
}

func TestCredentialKinds(t *testing.T) {
	cases := map[string]quota.Connection{
		"api_key":       {Provider: "deepseek", Values: map[string]string{"apiKey": "k"}},
		"oauth":         {Provider: "chatgpt", Values: map[string]string{"authPath": "p"}},
		"session":       {Provider: "kling", Way: "browser"},
		"sts":           {Provider: "volcengine", Values: map[string]string{"veConfig": "p"}},
		"console_token": {Provider: "aliyun", Values: map[string]string{"token": "t"}},
		"none":          {Provider: "figma"},
	}
	for want, c := range cases {
		if got := kind(c); got != want {
			t.Errorf("%s: kind = %s", c.Provider, got)
		}
	}
	if got := kind(quota.Connection{Provider: "copilot", Values: map[string]string{"login": "u", "token": "t"}}); got != "oauth" {
		t.Errorf("copilot's app token: kind = %s, want oauth", got)
	}
}

func TestResultTellsTheScheduler(t *testing.T) {
	soon, later := time.Unix(2000, 0), time.Unix(3000, 0)
	r := result(quota.Account{Status: quota.StatusOK, Windows: []quota.Window{{ResetsAt: &later}, {ResetsAt: &soon, ResetEstimated: true}},
		Groups: []quota.Group{{Windows: []quota.Window{{ResetsAt: &soon}}}}})
	if r.Outcome != supply.OutcomeOK || r.NextReset == nil || *r.NextReset != domain.Millis(soon.UnixMilli()) || r.BalanceOnly {
		t.Fatalf("result = %+v", r)
	}
	if r := result(quota.Account{Status: quota.StatusOK, Balances: []quota.Balance{{Currency: "CNY", Amount: "1"}}}); !r.BalanceOnly || r.NextReset != nil {
		t.Fatalf("balance account = %+v", r)
	}
	if r := result(quota.Account{Status: quota.StatusSignedOut, Message: "sign in again"}); r.Outcome != supply.OutcomeSignedOut || r.Detail != "sign in again" {
		t.Fatalf("signed out = %+v", r)
	}
}

type loopbackOnly struct{}

func (loopbackOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if h := r.URL.Hostname(); h != "127.0.0.1" && h != "localhost" {
		return nil, errors.New("tests don't call " + h)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// The page shows what the engine read, and the engine records it: one
// found DeepSeek key, read through the real quota reader.
func TestThePageShowsWhatTheEngineRead(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(fixture(t, "../../providers/deepseek/testdata/balance.json"))
	}))
	defer srv.Close()
	env := map[string]string{"DEEPSEEK_API_KEY": "sk-test"}
	local := quota.Local{Home: t.TempDir(), Getenv: func(k string) string { return env[k] },
		HTTP: &http.Client{Timeout: 5 * time.Second, Transport: loopbackOnly{}}, Now: time.Now,
		URLs: map[string]string{"deepseek.balance": srv.URL}}
	var providers []quota.Provider
	for _, p := range quota.Providers() {
		if p.ID == "deepseek" { // the others would look at this machine's real sign-ins
			providers = append(providers, p)
		}
	}
	accounts := quota.NewStore("")
	src := New(local, accounts, providers)

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
	page := quota.NewService(local, accounts, quota.Options{Reader: Reader{Source: src, Engine: engine}})

	snap := page.Refresh(context.Background())
	if len(snap.Accounts) != 1 || snap.Accounts[0].Status != quota.StatusOK || snap.Accounts[0].Balances[0].Amount != "23.40" || calls != 1 {
		t.Fatalf("snapshot = %+v (provider calls %d)", snap, calls)
	}

	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status, locator string
	if err := db.QueryRow(`SELECT status, locator_json FROM supply_credentials WHERE provider_id='deepseek' AND origin='found'`).Scan(&status, &locator); err != nil {
		t.Fatal(err)
	}
	if status != "ok" || strings.Contains(locator, "sk-test") || !strings.Contains(locator, `"source":"$DEEPSEEK_API_KEY"`) {
		t.Fatalf("credential row: status %q locator %s", status, locator)
	}
	var runs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM collector_runs WHERE outcome='ok'`).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("runs = %d, %v", runs, err)
	}
	// the balance is a reading of the account's prepaid package, exact to the cent
	var remaining int64
	var evidence, kind, identity string
	err = db.QueryRow(`SELECT r.remaining_units, r.evidence_json, p.kind, a.provider_identity FROM supply_readings r
		JOIN supply_resources s USING(namespace,resource_id) JOIN supply_packages p USING(namespace,package_id)
		JOIN supply_accounts a USING(namespace,account_id)`).Scan(&remaining, &evidence, &kind, &identity)
	if err != nil || remaining != 23_400_000 || kind != "prepaid" || !strings.Contains(evidence, `"granted":"10.00"`) || !strings.HasPrefix(identity, "credential:") {
		t.Fatalf("reading: %d %s %s %s %v", remaining, evidence, kind, identity, err)
	}
}

// fixture reads a canonical provider reply (internal/providers/*/testdata).
// Removing an account takes its card away at once: the page's read after
// the removal finds the accounts again instead of reading the removed one
// from the last list (a removed Bailian account used to stay on screen).
func TestRemovedAccountLeavesThePageAtOnce(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixture(t, "../../providers/deepseek/testdata/balance.json"))
	}))
	defer srv.Close()
	m := deepseek.SupplyModule(srv.URL)
	fetch, _ := transport.NewObservationFetch(m.Supply.Origins, srv.Client().Transport)
	env := map[string]string{"DEEPSEEK_API_KEY": "sk-found"}
	local := quota.Local{Home: t.TempDir(), Getenv: func(k string) string { return env[k] },
		HTTP: &http.Client{Timeout: 5 * time.Second, Transport: loopbackOnly{}}, Now: time.Now, URLs: map[string]string{}}
	var providers []quota.Provider
	for _, p := range quota.Providers() {
		if p.ID == "deepseek" {
			providers = append(providers, p)
		}
	}
	accounts := quota.NewStore(filepath.Join(t.TempDir(), "accounts.json"))
	src := New(local, accounts, providers, Native{Module: m, Fetch: fetch})
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "ant.db"), sqlite.Config{Clock: runtime.NewClock(), CommandBudget: fixtureCommandBudget})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close(context.Background()) }()
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	engine := supply.NewEngine("local", st, src, runtime.NewClock(), supply.DefaultPolicy(), nil)
	page := quota.NewService(local, accounts, quota.Options{Reader: Reader{Source: src, Engine: engine}})
	snap := page.Refresh(context.Background())
	if len(snap.Accounts) != 1 {
		t.Fatalf("cards before = %+v", snap.Accounts)
	}
	req := httptest.NewRequest(http.MethodDelete, "http://127.0.0.1:8080/api/accounts/"+snap.Accounts[0].ID, nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("X-Ant", "1")
	rec := httptest.NewRecorder()
	page.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `"provider":"deepseek"`) {
		t.Fatalf("after removing: %d %s", rec.Code, rec.Body.String())
	}
}

func fixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
