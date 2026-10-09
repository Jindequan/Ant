package quota

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Ant visits the site of each sign-in made in its window once a day, in
// that sign-in's own profile; never a pasted cookie's; not when turned off.
func TestKeepAliveVisitsEachSignInOnceADay(t *testing.T) {
	srv := sunoServer(t, `{"total_credits_left":30,"period":"day"}`)
	defer srv.Close()
	l := sunoLocal(t, srv)
	now := testNow
	l.Now = func() time.Time { return now }
	old := signInRunner
	signInRunner = func(_ context.Context, _ Local, _ SignIn, key string) ([]siteCookie, error) {
		return []siteCookie{{"auth.suno.com", "__client", "client-cookie"}, {".suno.com", "__client_uat", "1791"}}, nil
	}
	t.Cleanup(func() { signInRunner = old })
	var visits []string
	oldVisit := keepAliveRunner
	keepAliveRunner = func(_ context.Context, _ Local, url, key string) error {
		visits = append(visits, url+" @"+key)
		return nil
	}
	t.Cleanup(func() { keepAliveRunner = oldVisit })

	store := NewStore(l.AccountsFile())
	s := newTestService(l, store)
	code, out := call(t, s.Handler(), "POST", "/api/signin", `{"provider":"suno","mode":"account"}`, nil)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	// a pasted cookie is never visited
	if code, out := call(t, s.Handler(), "POST", "/api/accounts", `{"provider":"suno","mode":"account","way":"cookie","values":{"cookie":"__client=x"}}`, nil); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	var id, profile string
	d, _ := store.Data()
	for _, c := range d.Accounts {
		if c.Values["profile"] != "" {
			id, profile = c.ID, c.Values["profile"]
		}
	}

	s.keepAliveOnce(context.Background())
	if len(visits) != 1 || visits[0] != "https://suno.com/create @"+profile {
		t.Fatalf("visits = %v", visits)
	}
	now = now.Add(5 * time.Hour) // the same day: no second visit
	s.keepAliveOnce(context.Background())
	now = now.Add(16 * time.Hour) // a day later
	s.keepAliveOnce(context.Background())
	if len(visits) != 2 {
		t.Fatalf("visits after a day = %v", visits)
	}
	// the account says so, and the user turns it off
	_, out = call(t, s.Handler(), "POST", "/api/quota/refresh", "", nil)
	for _, a := range accountsIn(out) {
		if a["id"] == id && (a["keepAlive"] != true || a["keptAt"] == nil) {
			t.Fatalf("account = %v", a)
		}
		if a["id"] != id && a["keepAlive"] != nil {
			t.Fatalf("a pasted cookie says keepAlive: %v", a)
		}
	}
	if code, out := call(t, s.Handler(), "POST", "/api/keepalive", `{"id":"`+id+`","on":false}`, nil); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	now = now.Add(48 * time.Hour)
	s.keepAliveOnce(context.Background())
	if len(visits) != 2 {
		t.Fatalf("visited while off: %v", visits)
	}
	if code, out := call(t, s.Handler(), "POST", "/api/keepalive", `{"id":"nobody","on":true}`, nil); code != 400 || !strings.Contains(out["error"].(string), "no such account") {
		t.Fatalf("unknown account: %d %v", code, out)
	}
}
