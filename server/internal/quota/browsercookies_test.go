package quota

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// firefoxProfile writes a Firefox cookie store with one cookie last used at
// lastAccessed (µs since 1970, as Firefox keeps it).
func firefoxProfile(t *testing.T, l Local, host, name, value string, lastAccessed int64) {
	t.Helper()
	file := l.path("Library", "Application Support", "Firefox", "Profiles", "abc.default", "cookies.sqlite")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE moz_cookies (host TEXT, name TEXT, value TEXT, lastAccessed INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO moz_cookies VALUES (?, ?, ?, ?)`, host, name, value, lastAccessed); err != nil {
		t.Fatal(err)
	}
}

// The profile used last wins across browsers: every browser's access time
// is compared on one clock. Chrome's cookie (2025-08) is newer than
// Firefox's (2020-01), so Chrome's must be chosen.
func TestBrowserCookieRecencyComparesAcrossBrowsers(t *testing.T) {
	l := testLocal(t, nil)
	chromeProfile(t, l, [][3]string{{".manus.im", "session_id", "from-chrome"}}) // last_access_utc 13400000000000000 (1601-based)
	firefoxProfile(t, l, ".manus.im", "session_id", "from-firefox", 1577836800000000)

	c, err := l.findBrowserCookie(context.Background(), "manus.im", "session_id")
	if err != nil || c.value != "from-chrome" {
		t.Fatalf("findBrowserCookie = %q, %v; want the more recent Chrome cookie", c.value, err)
	}
	s, err := l.findBrowserSession(context.Background(), "manus.im", "session_id")
	if err != nil || s.header("manus.im") != "session_id=from-chrome" {
		t.Fatalf("findBrowserSession = %q, %v; want the Chrome profile", s.header("manus.im"), err)
	}
}
