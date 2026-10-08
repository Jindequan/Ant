package supply

import (
	"context"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

const minute = domain.Millis(60_000)

func rd(at domain.Millis, remaining int64, end *domain.Millis) store.SupplyReadingRecord {
	return store.SupplyReadingRecord{ObservedAt: at, Remaining: u(remaining), WindowEnd: end}
}

func TestForecast(t *testing.T) {
	end := 300 * minute // a 5-hour window ending at minute 300
	other := 0 * minute
	cases := []struct {
		name     string
		readings []store.SupplyReadingRecord
		asOf     domain.Millis
		kind     string
		at       domain.Millis // for runs_out
	}{
		{"no readings", nil, 0, "", 0},
		{"too little history", []store.SupplyReadingRecord{rd(0, 100, &end), rd(5*minute, 90, &end)}, 10 * minute, "", 0},
		{"idle", []store.SupplyReadingRecord{rd(0, 100, &end)}, 60 * minute, OutlookIdle, 0},
		// 40 spent in 60 min → 60 left lasts 90 more min: out at 150, before the reset at 300
		{"window runs out", []store.SupplyReadingRecord{rd(0, 100, &end), rd(30*minute, 80, &end), rd(60*minute, 60, &end)}, 60 * minute, OutlookRunsOut, 150 * minute},
		// 10 spent in 60 min → 90 left lasts 540 more min: past the reset
		{"window lasts", []store.SupplyReadingRecord{rd(0, 100, &end), rd(60*minute, 90, &end)}, 60 * minute, OutlookLasts, 0},
		// the previous window's readings don't count toward this window's pace
		{"only the current window", []store.SupplyReadingRecord{rd(0, 100, &other), rd(1, 0, &other), rd(10*minute, 100, &end)}, 70 * minute, OutlookIdle, 0},
		// 30 spent: at minute 60 that pace empties the window at 200; held still
		// until minute 120, the pace halves and the window lasts to its reset
		{"pace at the last change", []store.SupplyReadingRecord{rd(0, 100, &end), rd(60*minute, 70, &end)}, 60 * minute, OutlookRunsOut, 200 * minute},
		{"quiet time slows the pace", []store.SupplyReadingRecord{rd(0, 100, &end), rd(60*minute, 70, &end)}, 120 * minute, OutlookLasts, 0},
		{"used up says nothing more", []store.SupplyReadingRecord{rd(0, 100, &end), rd(60*minute, 0, &end)}, 60 * minute, "", 0},
	}
	for _, c := range cases {
		o := Forecast(c.readings, c.asOf)
		if o.Kind != c.kind || (c.kind == OutlookRunsOut && (o.At == nil || *o.At != c.at)) {
			t.Errorf("%s: %+v (at %v), want %s at %d", c.name, o, o.At, c.kind, c.at)
		}
	}
}

func TestBalanceForecastIgnoresTopUps(t *testing.T) {
	day := domain.Millis(24 * time.Hour / time.Millisecond)
	// 10 a day spent, with a top-up of 50 on day 2: 30 spent over 3 days
	rs := []store.SupplyReadingRecord{rd(0, 100, nil), rd(day, 90, nil), rd(2*day, 130, nil), rd(3*day, 110, nil)}
	o := Forecast(rs, 3*day)
	if o.Kind != OutlookRunsOut || o.PerDay == nil || *o.PerDay != 10 || *o.At != 3*day+11*day {
		t.Fatalf("balance outlook %+v (at %v, per day %v)", o, *o.At, *o.PerDay)
	}
}

// The query gives each credential its account's meters, paced up to the
// account's last good read.
func TestOutlooksThroughTheEngine(t *testing.T) {
	src := &inventorySource{creds: []Credential{found("k")}, inv: map[domain.ID]*Inventory{}}
	e, _, clock := newTestEngine(t, src, DefaultPolicy())
	start := clock.Now()
	end := start + 300*minute
	for i, remaining := range []int64{100, 80, 60} {
		w := window(remaining)
		w.WindowEnd = &end
		src.set("k", &Inventory{Identity: "acct", Packages: []PackageReading{plan("observed", w)}})
		clock.now.Store(int64(start + domain.Millis(i)*30*minute))
		if err := e.RunNow(context.Background(), []domain.ID{"k"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := e.Outlooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := got["k"]
	if len(m) != 1 || m[0].Outlook.Kind != OutlookRunsOut || *m[0].Outlook.At != start+150*minute || len(m[0].Points) != 3 || *m[0].Latest.Remaining != 60 {
		t.Fatalf("outlooks = %+v", got)
	}
}
