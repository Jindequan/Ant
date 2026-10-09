package quota

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
func antProfileCookies(t *testing.T, l Local, profileKey string, cookies [][3]string) {
	t.Helper()
	if _, _, err := findChrome(l); err != nil {
		t.Skip("no Chromium browser installed")
	}
	file := filepath.Join(antProfile(l, profileKey), "Default", "Cookies")
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
	antProfileCookies(t, l, "", [][3]string{{"auth.suno.com", "__client", "fresh"}, {".suno.com", "__client_uat", "2"}, {"other.com", "__client", "x"}})
	got, ok := signedInSession(context.Background(), l, s, stored, "")
	if !ok || cookieValue(got.cookies, "__client") != "fresh" || len(got.cookies) != 2 {
		t.Fatalf("with the profile: %+v %v", got, ok)
	}
	// signed out in the profile: the session kept at sign-in is used
	antProfileCookies(t, testLocal(t, nil), "", nil)
	if got, ok := signedInSession(context.Background(), testLocal(t, nil), s, stored, ""); !ok || cookieValue(got.cookies, "__client") != "old" {
		t.Fatalf("without the profile: %+v %v", got, ok)
	}
}

func TestSigningInKeepsTheSessionAndReadsWithIt(t *testing.T) {
	srv := sunoServer(t, `{"total_credits_left":30,"period":"day"}`)
	defer srv.Close()
	l := sunoLocal(t, srv)
	old := signInRunner
	signInRunner = func(_ context.Context, _ Local, s SignIn, _ string) ([]siteCookie, error) {
		if s.Domain != "suno.com" {
			t.Fatalf("signed in to %q", s.Domain)
		}
		return []siteCookie{{"auth.suno.com", "__client", "client-cookie"}, {".suno.com", "__client_uat", "1791"}}, nil
	}
	t.Cleanup(func() { signInRunner = old })
	store := NewStore(l.AccountsFile())
	s := newTestService(l, store)
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
	s := newTestService(l, store)
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
	idOf := func(out map[string]any, mode string) []string {
		var ids []string
		for _, a := range accountsIn(out) {
			if a["mode"] == mode {
				ids = append(ids, a["id"].(string))
			}
		}
		return ids
	}
	keysOf := func(out map[string]any, mode string) []string { // every sign-in a card stands for
		var ids []string
		for _, a := range accountsIn(out) {
			if a["mode"] == mode {
				for _, k := range a["keys"].([]any) {
					ids = append(ids, k.(string))
				}
			}
		}
		return ids
	}
	first := idOf(out, "free-quota")
	kept := func() int {
		n := 0
		d, err := store.Data()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range d.Accounts {
			if c.Provider == "aliyun" {
				n++
			}
		}
		return n
	}
	// a second sign-in adds accounts (one provider, many accounts); this
	// one is the same Alibaba Cloud account again, so its Token Plan (same
	// instance) and its free quota (same grants) still show as one card each
	code, out = call(t, s.Handler(), "POST", "/api/signin", `{"provider":"aliyun","mode":"payg"}`, nil)
	if code != 200 || kept() != 6 || len(idOf(out, "token-plan")) != 1 || len(idOf(out, "free-quota")) != 1 {
		t.Fatalf("second sign-in: %d, %d kept, cards %v", code, kept(), modesOf(out))
	}
	// signing in again renews that account: nothing is added, its id stays
	code, out = call(t, s.Handler(), "POST", "/api/signin", `{"provider":"aliyun","mode":"free-quota","id":"`+first[0]+`"}`, nil)
	if code != 200 || kept() != 6 || !slices.Contains(keysOf(out, "free-quota"), first[0]) {
		t.Fatalf("signing in again: %d, %d kept, %v", code, kept(), keysOf(out, "free-quota"))
	}
	// each instance is managed on its own: removing one card removes the
	// sign-ins folded into it, and nothing else
	if code, _ := call(t, s.Handler(), "DELETE", "/api/accounts/"+idOf(out, "free-quota")[0], "", nil); code != 200 || kept() != 4 {
		t.Fatalf("%d, %d kept", code, kept())
	}
}

func modesOf(out map[string]any) []string {
	var m []string
	for _, a := range accountsIn(out) {
		m = append(m, a["mode"].(string)+":"+a["id"].(string))
	}
	return m
}

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }

// Two accounts of one site keep two sessions: each sign-in gets a Chrome
// profile of its own, and signing in again uses that account's profile.
func TestEachSignInHasItsOwnProfile(t *testing.T) {
	srv := sunoServer(t, `{"total_credits_left":30,"period":"day"}`)
	defer srv.Close()
	l := sunoLocal(t, srv)
	var keys []string
	old := signInRunner
	signInRunner = func(_ context.Context, _ Local, _ SignIn, key string) ([]siteCookie, error) {
		keys = append(keys, key)
		return []siteCookie{{"auth.suno.com", "__client", "client-" + key}, {".suno.com", "__client_uat", "1791"}}, nil
	}
	t.Cleanup(func() { signInRunner = old })
	store := NewStore(l.AccountsFile())
	s := newTestService(l, store)
	for range 2 {
		if code, out := call(t, s.Handler(), "POST", "/api/signin", `{"provider":"suno","mode":"account"}`, nil); code != 200 {
			t.Fatalf("%d %v", code, out)
		}
	}
	d, _ := store.Data()
	var ids []string
	for _, c := range d.Accounts {
		if c.Provider == "suno" {
			ids = append(ids, c.ID)
			if c.Values["profile"] == "" || !strings.HasSuffix(c.ID, "-signin-"+c.Values["profile"]) {
				t.Fatalf("connection %s keeps profile %q", c.ID, c.Values["profile"])
			}
		}
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] == keys[1] || len(ids) != 2 {
		t.Fatalf("profiles %v, accounts %v", keys, ids)
	}
	if antProfile(l, keys[0]) == antProfile(l, keys[1]) || antProfile(l, "") == antProfile(l, keys[0]) {
		t.Fatal("two sign-ins share a profile")
	}
	// signing in again opens the account's own profile
	if code, out := call(t, s.Handler(), "POST", "/api/signin", `{"provider":"suno","mode":"account","id":"`+ids[1]+`"}`, nil); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	if keys[2] != strings.TrimPrefix(ids[1][strings.LastIndex(ids[1], "-signin-"):], "-signin-") {
		t.Fatalf("signed in again with profile %q for %s", keys[2], ids[1])
	}
}
