package quota

import (
	"testing"
	"time"

	"ant/server/internal/domain/usefirst"
)

// TestTheViewSaysWhatItMeans: the glance the page and the panel draw is
// computed once, here, by the one use-first rule (design 15).
func TestTheViewSaysWhatItMeans(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	at := func(h int) *time.Time { x := now.Add(time.Duration(h) * time.Hour); return &x }
	snap := Snapshot{Accounts: []Account{
		{ID: "plan", Kind: KindSubscription, Status: StatusOK, RenewsAt: at(240), Windows: []Window{
			{Name: "5-hour", UsedPercent: ptr(20.0), ResetsAt: at(3)},            // 80% left, resets soon: use first
			{Name: "Weekly", UsedPercent: ptr(20.0), ResetsAt: at(100)},          // not soon
			{Name: "Tools", UsedPercent: ptr(0.0), ResetsAt: at(1), Aside: true}, // aside: never
		}},
		{ID: "paced", Kind: KindSubscription, Status: StatusOK, EndsAt: at(48), Windows: []Window{
			{Name: "Daily", UsedPercent: ptr(10.0), ResetsAt: at(5)}, // the pace uses it up first
		}},
		{ID: "grants", Kind: KindFree, Status: StatusOK, Groups: []Group{{Title: "Free", Windows: []Window{
			{Name: "Image", Model: "wan", Left: ptr(5.0), Limit: ptr(100.0), ResetsAt: at(200), Expires: true}, // ends in 8 days
			{Name: "Video", Model: "kling", Left: ptr(0.0), Limit: ptr(10.0), ResetsAt: at(2), Expires: true},  // used up: nothing to lose
		}}}},
		{ID: "key", Kind: KindAPI, Status: StatusOK, Balances: []Balance{{Currency: "CNY", Amount: "10.10"}, {Currency: "credits", Amount: "500"}}},
		{ID: "console", Kind: KindSubscription, Status: StatusOK, Groups: []Group{{Title: "Billing", Balances: []Balance{
			{Currency: "CNY", Amount: "12.591"}, {Label: "Last month", Currency: "CNY", Amount: "99"}}}}},
		{ID: "gone", Kind: KindAPI, Status: StatusSignedOut, Balances: []Balance{{Currency: "USD", Amount: "5"}}},
		{ID: "broken", Kind: KindFree, Status: StatusError},
	}}
	insights := map[string][]Insight{"paced": {{Window: "Daily", Outlook: "runs_out"}}}

	v := view(snap, insights, now)
	g := v.Glance
	if len(g.UseFirst) != 2 || g.UseFirst[0].Window.Name != "5-hour" || g.UseFirst[0].Why != usefirst.BeforeReset ||
		g.UseFirst[1].Window.Name != "Image" || g.UseFirst[1].Why != usefirst.BeforeEnd {
		t.Fatalf("use first = %+v", g.UseFirst)
	}
	if len(g.NeedsYou) != 2 || g.NeedsYou[0] != "gone" || g.NeedsYou[1] != "broken" {
		t.Errorf("needs you = %v", g.NeedsYou)
	}
	if len(g.Held) != 1 || g.Held[0] != (Balance{Currency: "CNY", Amount: "22.691"}) {
		t.Errorf("held = %+v; want CNY 22.691 (a key's balance and a console's, not last month's spend, not credits, not a signed-out key's)", g.Held)
	}
	if g.Subscriptions != 3 || g.Free != 1 || g.APIs != 1 {
		t.Errorf("counts = %d/%d/%d", g.Subscriptions, g.Free, g.APIs)
	}
	if g.NextRenewal == nil || g.NextRenewal.Account != "paced" || !g.NextRenewal.Ends {
		t.Errorf("next renewal = %+v", g.NextRenewal)
	}
	w := v.Accounts[0].Windows[0]
	if w.PercentLeft == nil || *w.PercentLeft != 80 || w.UseFirst != usefirst.BeforeReset {
		t.Errorf("marked window = %+v", w)
	}
	if snap.Accounts[0].Windows[0].PercentLeft != nil || snap.Accounts[2].Groups[0].Windows[0].UseFirst != "" {
		t.Error("the view marked the service's own accounts")
	}
}
