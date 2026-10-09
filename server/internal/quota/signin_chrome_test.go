package quota

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestRealChromeSignIn opens a real Chrome window; run with ANT_REAL_CHROME=1.
func TestRealChromeSignIn(t *testing.T) {
	if os.Getenv("ANT_REAL_CHROME") == "" {
		t.Skip("set ANT_REAL_CHROME=1 to open a real Chrome window")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "ant_test", Value: "signed-in", Path: "/", HttpOnly: true})
		_, _ = w.Write([]byte("<h1>Signed in</h1>"))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	start := time.Now()
	// the page sets its cookie at once; "I'm signed in" makes Chrome write it
	go func() { time.Sleep(4 * time.Second); FinishSignIn() }()
	cs, err := runSignIn(context.Background(), l, SignIn{URL: srv.URL, Domain: "127.0.0.1", Done: func(cs []siteCookie) bool {
		return cookieValue(cs, "ant_test") == "signed-in"
	}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("signed in after %v with %d cookies", time.Since(start).Round(time.Millisecond), len(cs))
}

// TestRealChromeKeepAlive: a quiet visit in a sign-in's profile saves the
// session the site extends, with its new expiry. Run with ANT_REAL_CHROME=1.
func TestRealChromeKeepAlive(t *testing.T) {
	if os.Getenv("ANT_REAL_CHROME") == "" {
		t.Skip("set ANT_REAL_CHROME=1 to run real Chrome")
	}
	extended := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	visited := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visited = true // the site extends the session on each visit
		http.SetCookie(w, &http.Cookie{Name: "ant_session", Value: "renewed", Path: "/", HttpOnly: true, Expires: extended})
		_, _ = w.Write([]byte("<h1>Usage</h1>"))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	if err := os.MkdirAll(antProfile(l, "keep"), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := visitQuietly(context.Background(), l, srv.URL+"/usage", "keep"); err != nil {
		t.Fatal(err)
	}
	cs, ends, err := profileCookies(context.Background(), l, "127.0.0.1", "keep")
	if err != nil || !visited || cookieValue(cs, "ant_session") != "renewed" || !ends["ant_session"].Equal(extended.UTC()) {
		t.Fatalf("after the visit: visited %v, cookies %+v, ends %v, err %v (want %v)", visited, cs, ends, err, extended.UTC())
	}
	t.Logf("visited in %v; the session now ends %v", time.Since(start).Round(time.Millisecond), ends["ant_session"])
}
