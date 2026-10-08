package quota

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSunoSignInIsDoneOnlyWhenClerkSaysSo(t *testing.T) {
	done := sunoSignIn.Done
	if !done([]siteCookie{{"auth.suno.com", "__client", "c1"}, {".suno.com", "__client_uat", "1791"}}) {
		t.Fatal("signed-in Suno cookies not taken as signed in")
	}
	if done([]siteCookie{{"auth.suno.com", "__client", "c1"}, {".suno.com", "__client_uat", "0"}}) {
		t.Fatal("a signed-out Clerk client taken as signed in")
	}
}

// antProfileCookies writes Ant's own window profile, encrypted as Chrome
// encrypts it with its Keychain key.
func antProfileCookies(t *testing.T, l Local, cookies [][3]string) {
	t.Helper()
	if _, _, err := findChrome(l); err != nil {
		t.Skip("no Chromium browser installed")
	}
	file := filepath.Join(antProfile(l), "Default", "Cookies")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE meta (key TEXT, value TEXT)`, `INSERT INTO meta VALUES ('version', '24')`,
		`CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, last_access_utc INTEGER)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range cookies {
		if _, err := db.Exec(`INSERT INTO cookies VALUES (?, ?, '', ?, 1)`, c[0], c[1], encryptChromium(t, "chrome-pw", c[0], c[2])); err != nil {
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

func TestSignedInSessionPrefersAntsProfile(t *testing.T) {
	l := testLocal(t, nil)
	s := *sunoSignIn
	stored := encodeSession([]siteCookie{{"auth.suno.com", "__client", "old"}, {".suno.com", "__client_uat", "1"}})
	antProfileCookies(t, l, [][3]string{{"auth.suno.com", "__client", "fresh"}, {".suno.com", "__client_uat", "2"}, {"other.com", "__client", "x"}})
	got, ok := signedInSession(context.Background(), l, s, stored)
	if !ok || cookieValue(got.cookies, "__client") != "fresh" || len(got.cookies) != 2 {
		t.Fatalf("with the profile: %+v %v", got, ok)
	}
	// signed out in the profile: the session kept at sign-in is used
	antProfileCookies(t, testLocal(t, nil), nil)
	if got, ok := signedInSession(context.Background(), testLocal(t, nil), s, stored); !ok || cookieValue(got.cookies, "__client") != "old" {
		t.Fatalf("without the profile: %+v %v", got, ok)
	}
}

func TestSigningInKeepsTheSessionAndReadsWithIt(t *testing.T) {
	srv := sunoServer(t, `{"total_credits_left":30,"period":"day"}`)
	defer srv.Close()
	l := sunoLocal(t, srv)
	old := signInRunner
	signInRunner = func(_ context.Context, _ Local, s SignIn) ([]siteCookie, error) {
		if s.Domain != "suno.com" {
			t.Fatalf("signed in to %q", s.Domain)
		}
		return []siteCookie{{"auth.suno.com", "__client", "client-cookie"}, {".suno.com", "__client_uat", "1791"}}, nil
	}
	t.Cleanup(func() { signInRunner = old })
	store := NewStore(l.AccountsFile())
	s := NewService(l, store, Options{})
	code, out := call(t, s.Handler(), "POST", "/api/signin", `{"provider":"suno","mode":"account"}`, nil)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	a := accountsIn(out)[0]
	if a["status"] != "ok" || a["source"] != "Signed in with Ant" || a["user"] != "me@example.com" {
		t.Fatalf("account = %v", a)
	}
	// the session never reaches the page
	code, view := call(t, s.Handler(), "GET", "/api/accounts/"+a["id"].(string), "", nil)
	if code != 200 || strings.Contains(jsonString(view), "client-cookie") {
		t.Fatalf("%d %v", code, view)
	}
	d, _ := store.Data()
	if len(d.Accounts) != 1 || !strings.Contains(d.Accounts[0].Values["session"], "client-cookie") {
		t.Fatalf("stored = %+v", d)
	}
	if code, _ := call(t, s.Handler(), "POST", "/api/signin", `{"provider":"deepseek","mode":"api"}`, nil); code != 400 {
		t.Fatalf("deepseek sign-in: %d", code)
	}
	if code, out := call(t, s.Handler(), "POST", "/api/signin/finish", "", nil); code != 200 || out["open"] != false {
		t.Fatalf("finish with no window: %d %v", code, out)
	}
}

func TestOneAlibabaSignInConnectsEachInstance(t *testing.T) {
	srv := bailianConsoleServer(t, `{"per1MonthPercentage":0.4,"per1MonthResetTime":1792252800000}`)
	defer srv.Close()
	cli := bailianCLIServer(t)
	defer cli.Close()
	l := testLocal(t, nil) // no browser sign-in on this machine
	l.URLs["aliyun.console"], l.URLs["aliyun.gateway"], l.URLs["aliyun.cli"] = srv.URL, srv.URL, cli.URL
	old := openURL
	t.Cleanup(func() { openURL = old })
	openURL = func(page string) error { // the console hands the token back
		u, _ := url.Parse(page)
		go func() {
			if r, err := http.Post("http://"+u.Query().Get("notice"), "application/json", strings.NewReader(`{"access_token":"tok"}`)); err == nil {
				r.Body.Close()
			}
		}()
		return nil
	}
	store := NewStore(l.AccountsFile())
	s := NewService(l, store, Options{})
	code, out := call(t, s.Handler(), "POST", "/api/signin", `{"provider":"aliyun","mode":"payg"}`, nil)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	modes := map[string]string{}
	for _, a := range accountsIn(out) {
		modes[a["mode"].(string)] = a["status"].(string)
	}
	// pay-as-you-go needs an AccessKey: the console sign-in doesn't open bills
	if len(modes) != 3 || modes["token-plan"] != "ok" || modes["payg"] != "not_connected" || modes["free-quota"] != "ok" {
		t.Fatalf("instances = %v", modes)
	}
	// each instance is managed on its own
	if code, out := call(t, s.Handler(), "DELETE", "/api/accounts/aliyun-free-quota-signin", "", nil); code != 200 || len(accountsIn(out)) != 2 {
		t.Fatalf("%d %v", code, out)
	}
}

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }
