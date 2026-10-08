package quota

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

// Without the user's permission for a site, Ant opens no browser cookie
// store and asks the Keychain for nothing (ruling R6).
func TestBrowserIsNotOpenedWithoutPermission(t *testing.T) {
	l := testLocal(t, nil)
	chromeProfile(t, l, [][3]string{{".manus.im", "session_id", "sid"}})
	var keychain atomic.Int32
	inner := keychainPassword
	keychainPassword = func(s string) (string, error) { keychain.Add(1); return inner(s) }
	l.Browser = nil // nothing allowed

	if _, err := l.findBrowserSession(context.Background(), "manus.im", "session_id"); err != errNoCookie {
		t.Fatalf("session without permission: %v", err)
	}
	if _, err := l.findBrowserCookie(context.Background(), "manus.im", "session_id"); err != errNoCookie {
		t.Fatalf("cookie without permission: %v", err)
	}
	conns, err := Connections(l, NewStore(""), Providers())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range conns {
		if c.Way == "browser" {
			t.Fatalf("found %s in a browser nobody allowed", c.ID)
		}
	}
	if keychain.Load() != 0 {
		t.Fatal("the Keychain was asked without permission")
	}
}

// Allowing a site on the page makes its browser sign-in show up; removing
// that account takes the permission back instead of hiding it.
func TestAllowingAndStoppingABrowserSite(t *testing.T) {
	l := testLocal(t, nil)
	chromeProfile(t, l, [][3]string{{".manus.im", "session_id", "sid"}})
	store := NewStore(l.AccountsFile())
	l.Browser = store.BrowserAllowed
	s := newTestService(l, store)
	h := s.Handler()

	if code, _ := call(t, h, "POST", "/api/browser", `{"provider":"manus","mode":"credits","way":"api","allow":true}`, nil); code != 400 {
		t.Fatalf("a way that doesn't read a browser: %d, want 400", code)
	}
	code, out := call(t, h, "POST", "/api/browser", `{"provider":"manus","mode":"credits","way":"browser","allow":true}`, nil)
	if code != 200 || !strings.Contains(strings.Join(anyStrings(out["browserSites"]), ","), "manus.im") {
		t.Fatalf("allow: %d %v", code, out["browserSites"])
	}
	var id string
	for _, a := range accountsIn(out) {
		if a["way"] == "browser" && a["provider"] == "manus" {
			id = a["id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("no browser account after allowing: %v", out)
	}
	code, out = call(t, h, "DELETE", "/api/accounts/"+id, "", nil)
	if code != 200 || len(anyStrings(out["browserSites"])) != 0 {
		t.Fatalf("remove: %d %v", code, out["browserSites"])
	}
	d, _ := store.Data()
	if len(d.Hidden) != 0 || len(d.BrowserSites) != 0 {
		t.Fatalf("stored = %+v; removing takes the permission back, it doesn't hide", d)
	}
}

func anyStrings(v any) []string {
	var out []string
	for _, x := range asSlice(v) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asSlice(v any) []any { s, _ := v.([]any); return s }
