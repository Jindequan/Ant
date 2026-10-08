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
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("signed in after %v with %d cookies", time.Since(start).Round(time.Millisecond), len(cs))
}
