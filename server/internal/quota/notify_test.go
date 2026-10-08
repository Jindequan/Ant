package quota

import (
	"context"
	"strings"
	"testing"
	"time"
)

func at(d time.Duration) *time.Time { t := testNow.Add(d); return &t }

func keys(as []Alert) string {
	var out []string
	for _, a := range as {
		out = append(out, a.Title)
	}
	return strings.Join(out, " | ")
}

func TestAlerts(t *testing.T) {
	off := false
	accounts := []Account{
		{ID: "claude", Name: "Claude", Status: StatusOK, Windows: []Window{
			{Name: "5 hours", UsedPercent: ptr(20.0), ResetsAt: at(time.Hour)},         // 80% left, resets in 1h: lose it
			{Name: "Week", UsedPercent: ptr(10.0), ResetsAt: at(5 * time.Hour)},        // resets later: nothing yet
			{Name: "Opus", UsedPercent: ptr(90.0), ResetsAt: at(30 * time.Minute)},     // 10% left: not worth a ping
			{Name: "MCP", UsedPercent: ptr(0.0), ResetsAt: at(time.Hour), Aside: true}, // aside: never
		}},
		{ID: "zai", Name: "Z.ai", Status: StatusOK, Windows: []Window{
			{Name: "5 hours", UsedPercent: ptr(50.0), ResetsAt: at(time.Hour)},   // would be "lose it", but the pace uses it up
			{Name: "Week", UsedPercent: ptr(60.0), ResetsAt: at(48 * time.Hour)}, // runs out in 3h: running out
		}},
		{ID: "kling", Name: "Kling AI", Status: StatusOK, Windows: []Window{
			{Name: "Daily free credits", Left: ptr(66.0), Limit: ptr(66.0), Unit: "credits", ResetsAt: at(10 * time.Hour), Expires: true},
		}},
		{ID: "deepseek", Name: "DeepSeek", Status: StatusOK, Balances: []Balance{{Currency: "CNY", Amount: "4.00"}}},
		{ID: "copilot", Name: "Copilot", Plan: "Pro", Status: StatusOK, RenewsAt: at(20 * time.Hour)},
		{ID: "figma", Name: "Figma", Plan: "Starter", Status: StatusOK, EndsAt: at(48 * time.Hour), AutoRenew: &off},
		{ID: "suno", Name: "Suno", Status: StatusSignedOut, Message: "Your Suno sign-in has ended. Sign in again."},
		{ID: "broken", Name: "Broken", Status: StatusError, Message: "500"},
	}
	insights := map[string][]Insight{
		"zai": {
			{Window: "5 hours", Outlook: "runs_out", At: at(40 * time.Minute)},
			{Window: "Week", Outlook: "runs_out", At: at(3 * time.Hour)},
		},
		"deepseek": {{Currency: "CNY", Outlook: "runs_out", At: at(40 * time.Hour)}},
	}
	got := keys(alerts(accounts, insights, testNow))
	want := "Claude · 5 hours resets soon | Z.ai · 5 hours is running out | Z.ai · Week is running out | Kling AI · Daily free credits expires soon | " +
		"DeepSeek balance is running low | Copilot renews soon | Figma ends soon | Suno is signed out"
	if got != want {
		t.Fatalf("alerts:\n got %s\nwant %s", got, want)
	}
	for _, a := range alerts(accounts, insights, testNow) {
		if a.Title == "Kling AI · Daily free credits expires soon" && !strings.Contains(a.Body, "66 credits left") {
			t.Fatalf("expiring body: %s", a.Body)
		}
		if a.Title == "Z.ai · Week is running out" && a.Body != "At this pace it runs out in 3h, 1 day before it resets." {
			t.Fatalf("running-out body: %s", a.Body)
		}
	}
}

type recordingNotifier struct{ shown []string }

func (r *recordingNotifier) Notify(title, body string) error {
	r.shown = append(r.shown, title)
	return nil
}

func TestNotificationsAreSentOnceAndCanBeTurnedOff(t *testing.T) {
	l := testLocal(t, nil)
	store := NewStore("")
	reader := &directReader{local: l, store: store, latest: map[string]Account{}}
	n := &recordingNotifier{}
	s := NewService(l, store, Options{Reader: reader, Notifier: n})
	reader.conns = []Connection{{ID: "suno"}}
	reader.latest["suno"] = Account{ID: "suno", Name: "Suno", Status: StatusSignedOut}

	s.notifyOnce(context.Background())
	s.notifyOnce(context.Background())
	if len(n.shown) != 1 {
		t.Fatalf("shown %v, want the sign-out once", n.shown)
	}
	// signed in again, then out again: news again
	reader.latest["suno"] = Account{ID: "suno", Name: "Suno", Status: StatusOK}
	s.notifyOnce(context.Background())
	reader.latest["suno"] = Account{ID: "suno", Name: "Suno", Status: StatusSignedOut}
	s.notifyOnce(context.Background())
	if len(n.shown) != 2 {
		t.Fatalf("shown %v, want a second sign-out", n.shown)
	}

	if err := s.setNotify(false); err != nil || store.NotifyOn() {
		t.Fatalf("turning off: %v", err)
	}
	reader.latest["suno"] = Account{ID: "suno", Name: "Suno", Status: StatusOK}
	s.notifyOnce(context.Background())
	reader.latest["suno"] = Account{ID: "suno", Name: "Suno", Status: StatusSignedOut}
	s.notifyOnce(context.Background())
	if len(n.shown) != 2 {
		t.Fatalf("shown %v while off", n.shown)
	}
	if err := s.setNotify(true); err != nil || len(n.shown) != 3 || n.shown[2] != "Ant" {
		t.Fatalf("turning on shows a test notification: %v %v", err, n.shown)
	}
}

func TestABurstIsSpreadOut(t *testing.T) {
	l := testLocal(t, nil)
	store := NewStore("")
	reader := &directReader{local: l, store: store, latest: map[string]Account{}}
	n := &recordingNotifier{}
	s := NewService(l, store, Options{Reader: reader, Notifier: n})
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		reader.conns = append(reader.conns, Connection{ID: id})
		reader.latest[id] = Account{ID: id, Name: id, Status: StatusSignedOut}
	}
	s.notifyOnce(context.Background())
	if len(n.shown) != maxPerPass {
		t.Fatalf("first pass showed %d", len(n.shown))
	}
	s.notifyOnce(context.Background())
	if len(n.shown) != 7 {
		t.Fatalf("second pass: %d in total, want 7", len(n.shown))
	}
}
