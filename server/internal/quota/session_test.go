package quota

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func setExpiry(t *testing.T, file, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// A sign-in ends when its session cookie expires: read from Ant's own
// profile for that sign-in, or from the everyday browser.
func TestSessionEndsWhenItsCookieExpires(t *testing.T) {
	ends := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	p := sunoProvider()
	m := p.Modes[0]

	// signed in with Ant: Chromium keeps µs since 1601
	l := testLocal(t, nil)
	antProfileCookies(t, l, "k1", [][3]string{{"auth.suno.com", "__client", "c"}, {".suno.com", "__client_uat", "1"}})
	setExpiry(t, filepath.Join(antProfile(l, "k1"), "Default", "Cookies"),
		`UPDATE cookies SET has_expires = 1, expires_utc = ? WHERE name = '__client'`, ends.Unix()*1_000_000+11644473600*1_000_000)
	c := Connection{ID: "suno-account-signin-k1", Provider: "suno", Mode: "account", Way: "sign-in", Origin: OriginAdded,
		Values: map[string]string{"session": encodeSession([]siteCookie{{"auth.suno.com", "__client", "c"}}), "profile": "k1"}}
	if got := sessionEnds(context.Background(), l, m, c); got == nil || !got.Equal(ends) {
		t.Fatalf("Ant's profile: %v, want %v", got, ends)
	}
	// a cookie that lasts only while the browser is open says nothing
	setExpiry(t, filepath.Join(antProfile(l, "k1"), "Default", "Cookies"), `UPDATE cookies SET has_expires = 0`)
	if got := sessionEnds(context.Background(), l, m, c); got != nil {
		t.Fatalf("session-long cookie: %v", got)
	}

	// the everyday browser: newer Firefox keeps milliseconds
	l = testLocal(t, nil)
	if err := NewStore(l.AccountsFile()).SetBrowserSite("suno.com", true); err != nil {
		t.Fatal(err)
	}
	l.Browser = NewStore(l.AccountsFile()).BrowserAllowed
	firefoxProfile(t, l, ".suno.com", "__client", "c", time.Now().UnixMicro())
	setExpiry(t, l.path("Library", "Application Support", "Firefox", "Profiles", "abc.default", "cookies.sqlite"),
		`UPDATE moz_cookies SET expiry = ?`, ends.UnixMilli())
	found := Connection{ID: "suno-browser", Provider: "suno", Mode: "account", Way: "browser", Origin: OriginFound}
	if got := sessionEnds(context.Background(), l, m, found); got == nil || !got.Equal(ends) {
		t.Fatalf("browser: %v, want %v", got, ends)
	}

	// a pasted cookie is never read again: unknown
	pasted := Connection{ID: "x", Provider: "suno", Mode: "account", Way: "cookie", Origin: OriginAdded, Values: map[string]string{"cookie": "__client=c"}}
	if got := sessionEnds(context.Background(), l, m, pasted); got != nil {
		t.Fatalf("pasted: %v", got)
	}
}
