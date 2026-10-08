package resources

import (
	"testing"
	"time"

	"ant/server/internal/domain"
)

// --- Frozen DST fold/gap policy ---

// TestWindowAtFallBackSecondHourContained is the fall-back-second-hour-
// contained audit probe: both 01:30 wall-clock occurrences of the 2026-11-01
// fall-back lie inside their own hour windows, in two DISTINCT adjacent
// windows (the repeated wall-clock hour is two real hour windows — a
// fall-back day contains 25 hourly windows, matching its 25h day window).
func TestWindowAtFallBackSecondHourContained(t *testing.T) {
	rule := domain.WindowRule{Kind: "fixed", Period: "hour", TimeZone: "America/New_York"}
	second, _ := time.Parse(time.RFC3339, "2026-11-01T01:30:00-05:00")
	_, start, end, err := WindowAt(msOf(second), rule, "rev-1")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if second.UnixMilli() < start || second.UnixMilli() >= end {
		t.Fatalf("second 01:30 -05:00 not contained: now=%d window=[%d,%d)", second.UnixMilli(), start, end)
	}
	// Both 01:30 wall-clock occurrences are contained, in two distinct
	// adjacent windows (the repeated hour is two real windows).
	first, _ := time.Parse(time.RFC3339, "2026-11-01T01:30:00-04:00")
	_, fStart, fEnd, err := WindowAt(msOf(first), rule, "rev-1")
	if err != nil {
		t.Fatalf("WindowAt(first): %v", err)
	}
	if first.UnixMilli() < fStart || first.UnixMilli() >= fEnd {
		t.Fatalf("first 01:30 -04:00 not contained")
	}
	if fEnd != start {
		t.Fatalf("fall-back hour windows must be seamless: first ends %d, second starts %d", fEnd, start)
	}
	if fStart == start {
		t.Fatal("the two 01:30 occurrences must fall into distinct windows")
	}
}

// TestWindowAtDSTPolicies pins the frozen fold/gap policy as user-visible
// WindowAt behavior. Rework note: the first-round version of this test also
// pinned Go's time.Date normalization internals ("documented Go semantics"),
// but D-B5-v2 forbids depending on that choice — the Berlin fold
// counterexample proved it inconsistent across zones — so boundary
// resolution now enumerates candidates explicitly (window_fold.go) and the
// Go-internals pins were dropped.
func TestWindowAtDSTPolicies(t *testing.T) {
	loc := mustTZ(t, "America/New_York")

	// Policy: a gap anchor resolves to the FIRST VALID instant at/after the
	// requested wall clock (03:00 -04:00), never to a pre-gap instant.
	gapAnchor := domain.WindowRule{
		Kind: "fixed", Period: "day", TimeZone: "America/New_York",
		AnchorISO: "2026-01-01T02:30:00-05:00",
	}
	_, start, end, err := WindowAt(msOf(time.Date(2026, 3, 8, 12, 0, 0, 0, loc)), gapAnchor, "g")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	wantStart := utc(2026, 3, 8, 7, 0).UnixMilli() // 2026-03-08T03:00:00-04:00
	wantEnd := utc(2026, 3, 9, 6, 30).UnixMilli()  // 2026-03-09T02:30:00-04:00
	if start != wantStart || end != wantEnd {
		t.Fatalf("gap anchor must start at the first valid instant 03:00 -04:00: want [%d,%d), got [%d,%d)", wantStart, wantEnd, start, end)
	}

	// Policy: an ambiguous anchor resolves to the earlier offset (the FIRST
	// 01:30, -04:00), and both wall-clock occurrences of 01:30 fall inside
	// that day window.
	ambAnchor := domain.WindowRule{
		Kind: "fixed", Period: "day", TimeZone: "America/New_York",
		AnchorISO: "2026-01-01T01:30:00-05:00",
	}
	secondOccurrence, _ := time.Parse(time.RFC3339, "2026-11-01T01:30:00-05:00")
	_, start, end, err = WindowAt(msOf(secondOccurrence), ambAnchor, "g")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	wantStart = utc(2026, 11, 1, 5, 30).UnixMilli() // first 01:30 (-04:00)
	if start != wantStart {
		t.Fatalf("ambiguous anchor must resolve to the earlier offset: want start %d, got %d", wantStart, start)
	}
	if secondOccurrence.UnixMilli() >= end {
		t.Fatal("second 01:30 occurrence must be inside the day window")
	}
}

// --- Multi-region fold/gap matrix (S3: 窗口符合重置规则) ---

// TestWindowAtDSTFoldGapMultiRegion exercises the frozen fold/gap policy in
// two hemispheres and both transition directions: gap anchors resolve to the
// first valid instant (exactly the transition), fold anchors to the earliest
// occurrence; fold days are 25h, gap days 23h; repeated wall-clock hours in
// minute/hour periods are two real windows.
func TestWindowAtDSTFoldGapMultiRegion(t *testing.T) {
	t.Run("America/New_York", func(t *testing.T) {
		loc := mustTZ(t, "America/New_York")

		// Hour period, spring gap 2026-03-08 (02:00 EST → 03:00 EDT): the
		// nonexistent 02:00 slot resolves to the transition, so the wall
		// hour 01:xx is ONE 2-real-hour window [01:00 EST, 03:00 EDT).
		hourRule := domain.WindowRule{Kind: "fixed", Period: "hour", TimeZone: "America/New_York"}
		now := time.Date(2026, 3, 8, 1, 30, 0, 0, loc) // 01:30 EST
		_, s, e, err := WindowAt(msOf(now), hourRule, "h")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if s != utc(2026, 3, 8, 6, 0).UnixMilli() || e != utc(2026, 3, 8, 7, 0).UnixMilli() {
			t.Fatalf("gap hour must be [01:00 EST, 03:00 EDT) = 2 real hours, got [%d,%d)", s, e)
		}

		// Day period, no anchor: gap day 23h, fold day 25h.
		dayRule := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "America/New_York"}
		_, s, e, err = WindowAt(msOf(time.Date(2026, 3, 8, 12, 0, 0, 0, loc)), dayRule, "d")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if e-s != 23*int64(time.Hour/time.Millisecond) {
			t.Fatalf("spring-forward day must be 23h, got %d ms", e-s)
		}
		_, s, e, err = WindowAt(msOf(time.Date(2026, 11, 1, 12, 0, 0, 0, loc)), dayRule, "d")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if e-s != 25*int64(time.Hour/time.Millisecond) {
			t.Fatalf("fall-back day must be 25h, got %d ms", e-s)
		}
	})

	t.Run("Europe/Berlin", func(t *testing.T) {
		loc := mustTZ(t, "Europe/Berlin")

		// Spring gap 2026-03-29 (02:00 CET → 03:00 CEST): anchor 02:30 does
		// not exist; the day starts at the transition 03:00 CEST (01:00Z).
		gap := domain.WindowRule{
			Kind: "fixed", Period: "day", TimeZone: "Europe/Berlin",
			AnchorISO: "2026-01-01T02:30:00+01:00",
		}
		_, s, e, err := WindowAt(msOf(time.Date(2026, 3, 29, 12, 0, 0, 0, loc)), gap, "g")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if want := utc(2026, 3, 29, 1, 0).UnixMilli(); s != want {
			t.Fatalf("gap anchor must start at 03:00 CEST (01:00Z): want %d, got %d", want, s)
		}
		if want := utc(2026, 3, 30, 0, 30).UnixMilli(); e != want { // 02:30 CEST next day
			t.Fatalf("gap-anchor day end wrong: want %d, got %d", want, e)
		}

		// Day period, no anchor: gap day 23h, fold day 25h.
		dayRule := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "Europe/Berlin"}
		_, s, e, err = WindowAt(msOf(time.Date(2026, 3, 29, 12, 0, 0, 0, loc)), dayRule, "d")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if s != utc(2026, 3, 28, 23, 0).UnixMilli() || e != utc(2026, 3, 29, 22, 0).UnixMilli() {
			t.Fatalf("spring-forward day must be [Mar 28 23:00Z, Mar 29 22:00Z), got [%d,%d)", s, e)
		}
		_, s, e, err = WindowAt(msOf(time.Date(2026, 10, 25, 12, 0, 0, 0, loc)), dayRule, "d")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if s != utc(2026, 10, 24, 22, 0).UnixMilli() || e != utc(2026, 10, 25, 23, 0).UnixMilli() {
			t.Fatalf("fall-back day must be [Oct 24 22:00Z, Oct 25 23:00Z) = 25h, got [%d,%d)", s, e)
		}

		// Hour period on the fold day: the repeated wall-clock hour 02:xx is
		// two real hour windows, each starting at a 02:00 wall clock (the
		// first at +02:00, the second at +01:00).
		hourRule := domain.WindowRule{Kind: "fixed", Period: "hour", TimeZone: "Europe/Berlin"}
		first, _ := time.Parse(time.RFC3339, "2026-10-25T02:30:00+02:00")
		_, s, e, err = WindowAt(msOf(first), hourRule, "h")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if s != utc(2026, 10, 25, 0, 0).UnixMilli() || e != utc(2026, 10, 25, 1, 0).UnixMilli() {
			t.Fatalf("first 02:30 must sit in [02:00+02:00, 02:00+01:00), got [%d,%d)", s, e)
		}
		second, _ := time.Parse(time.RFC3339, "2026-10-25T02:30:00+01:00")
		_, s, e, err = WindowAt(msOf(second), hourRule, "h")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if s != utc(2026, 10, 25, 1, 0).UnixMilli() || e != utc(2026, 10, 25, 2, 0).UnixMilli() {
			t.Fatalf("second 02:30 must sit in [02:00+01:00, 03:00+01:00), got [%d,%d)", s, e)
		}
	})
}

// --- Wall-clock resolution unit tests (candidate enumeration) ---

// TestCivilInstantsAndFirstValid pins the candidate-enumeration primitive
// itself: a civil time occurs once normally, twice in a fold (both kept, in
// order), never in a gap; firstValidAfter lands exactly on the transition
// instant for gap stamps.
func TestCivilInstantsAndFirstValid(t *testing.T) {
	ny := mustTZ(t, "America/New_York")

	normal := civilStamp{civilDate{2026, time.June, 15}, wallClock{12, 30, 0, 0}} // 12:30 EDT
	got := civilInstants(normal, ny)
	if len(got) != 1 || got[0].UnixMilli() != utc(2026, 6, 15, 16, 30).UnixMilli() {
		t.Fatalf("normal civil time must occur exactly once (16:30Z), got %v", got)
	}

	fold := civilStamp{civilDate{2026, time.November, 1}, wallClock{1, 30, 0, 0}} // 01:30 twice
	got = civilInstants(fold, ny)
	if len(got) != 2 ||
		got[0].UnixMilli() != utc(2026, 11, 1, 5, 30).UnixMilli() || // -04:00
		got[1].UnixMilli() != utc(2026, 11, 1, 6, 30).UnixMilli() { // -05:00
		t.Fatalf("fold civil time must yield both occurrences in order, got %v", got)
	}

	gap := civilStamp{civilDate{2026, time.March, 8}, wallClock{2, 30, 0, 0}} // does not exist
	if got = civilInstants(gap, ny); len(got) != 0 {
		t.Fatalf("gap civil time must yield zero occurrences, got %v", got)
	}
	first, err := firstValidAfter(gap, ny)
	if err != nil {
		t.Fatalf("firstValidAfter: %v", err)
	}
	if first.UnixNano() != utc(2026, 3, 8, 7, 0).UnixNano() { // exactly 03:00 EDT
		t.Fatalf("gap resolution must be exactly the transition instant 07:00Z, got %v", first)
	}

	ber := mustTZ(t, "Europe/Berlin")
	berlinFold := civilStamp{civilDate{2026, time.October, 25}, wallClock{2, 0, 0, 0}}
	got = civilInstants(berlinFold, ber)
	if len(got) != 2 ||
		got[0].UnixMilli() != utc(2026, 10, 25, 0, 0).UnixMilli() || // 02:00 +02:00
		got[1].UnixMilli() != utc(2026, 10, 25, 1, 0).UnixMilli() { // 02:00 +01:00
		t.Fatalf("Berlin fold 02:00 must yield [00:00Z, 01:00Z], got %v", got)
	}
	berlinGap := civilStamp{civilDate{2026, time.March, 29}, wallClock{2, 30, 0, 0}}
	if got = civilInstants(berlinGap, ber); len(got) != 0 {
		t.Fatalf("Berlin gap 02:30 must yield zero occurrences, got %v", got)
	}
	first, err = firstValidAfter(berlinGap, ber)
	if err != nil {
		t.Fatalf("firstValidAfter: %v", err)
	}
	if first.UnixNano() != utc(2026, 3, 29, 1, 0).UnixNano() { // exactly 03:00 CEST
		t.Fatalf("Berlin gap resolution must be exactly the transition 01:00Z, got %v", first)
	}
}

// --- Zones whose DST transitions sit at local midnight ---

// TestWindowAtMidnightTransitionZone stresses day windows in a zone whose DST
// transitions happen AT local midnight (America/Santiago: 23h and 25h days),
// where the civil midnight itself can sit next to a gap.
func TestWindowAtMidnightTransitionZone(t *testing.T) {
	rule := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "America/Santiago"}
	// Around the 2026-04-04 (25h) and 2026-09-05 (23h) transitions.
	walkWindows(t, rule, time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC), 96, time.Hour)
	walkWindows(t, rule, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC), 96, time.Hour)
	// Santiago springs forward AT midnight (2026-09-05 24:00, -04 → -03):
	// local Sep 6 00:00 does not exist, so day Sep 5 keeps its full 24 real
	// hours and ends at the transition instant; day Sep 6 (starting at the
	// transition, wall 01:00 -03) is the 23h day.
	loc := mustTZ(t, "America/Santiago")
	_, start, end, err := WindowAt(msOf(time.Date(2026, 9, 5, 12, 0, 0, 0, loc)), rule, "s")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if end-start != 24*int64(time.Hour/time.Millisecond) {
		t.Fatalf("Santiago 2026-09-05 must be a 24h window (the gap sits at its end), got %d ms", end-start)
	}
	_, start, end, err = WindowAt(msOf(time.Date(2026, 9, 6, 12, 0, 0, 0, loc)), rule, "s")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if end-start != 23*int64(time.Hour/time.Millisecond) {
		t.Fatalf("Santiago 2026-09-06 must be a 23h window, got %d ms", end-start)
	}
	// Window lengths must be EXACT: no sub-second slop from the gap search.
	if (end-start)%int64(time.Second/time.Millisecond) != 0 {
		t.Fatalf("window length must be second-exact, got %d ms", end-start)
	}
}

// --- Historical offset changes and fractional offsets (S3 matrix) ---

// TestWindowAtHistoricalOffsetChanges proves current windows derive from the
// CURRENT zone rules, never a historical epoch offset: Kathmandu moved from
// +05:30 to +05:45 in 1986, and the pre-rework 1970 grid shifted every 2026
// hour boundary to :15. Pacific/Chatham (+12:45 winter / +13:45 summer) is
// verified likewise across both of its offsets.
func TestWindowAtHistoricalOffsetChanges(t *testing.T) {
	t.Run("Asia/Kathmandu", func(t *testing.T) {
		loc := mustTZ(t, "Asia/Kathmandu")
		hourRule := domain.WindowRule{Kind: "fixed", Period: "hour", TimeZone: "Asia/Kathmandu"}
		for h := 0; h < 24; h++ {
			now := time.Date(2026, 10, 2, h, 20, 0, 0, loc)
			_, s, e, err := WindowAt(msOf(now), hourRule, "k")
			if err != nil {
				t.Fatalf("WindowAt(%02d:20): %v", h, err)
			}
			wantStart := time.Date(2026, 10, 2, h, 0, 0, 0, loc)
			wantEnd := wantStart.Add(time.Hour)
			if s != wantStart.UnixMilli() || e != wantEnd.UnixMilli() {
				t.Fatalf("hour %02d must start at local :00 (1986 +05:30→+05:45 change must not leak), got [%d,%d)", h, s, e)
			}
		}
		// Day window: exact UTC pins +05:45 handling (local midnight = 18:15Z).
		dayRule := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "Asia/Kathmandu"}
		now := time.Date(2026, 10, 2, 12, 20, 0, 0, loc)
		_, s, e, err := WindowAt(msOf(now), dayRule, "k")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if s != utc(2026, 10, 1, 18, 15).UnixMilli() || e != utc(2026, 10, 2, 18, 15).UnixMilli() {
			t.Fatalf("Kathmandu day must be [Oct 1 18:15Z, Oct 2 18:15Z), got [%d,%d)", s, e)
		}
		// Minute window: natural local minute.
		minuteRule := domain.WindowRule{Kind: "fixed", Period: "minute", TimeZone: "Asia/Kathmandu"}
		now = time.Date(2026, 10, 2, 12, 20, 30, 0, loc)
		_, s, e, err = WindowAt(msOf(now), minuteRule, "k")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		wantStart := time.Date(2026, 10, 2, 12, 20, 0, 0, loc)
		if s != wantStart.UnixMilli() || e != wantStart.Add(time.Minute).UnixMilli() {
			t.Fatalf("minute window must be local [12:20:00, 12:21:00), got [%d,%d)", s, e)
		}
	})

	t.Run("Pacific/Chatham", func(t *testing.T) {
		loc := mustTZ(t, "Pacific/Chatham")
		hourRule := domain.WindowRule{Kind: "fixed", Period: "hour", TimeZone: "Pacific/Chatham"}
		dayRule := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "Pacific/Chatham"}
		// June: standard +12:45. October: daylight +13:45. Both must reset on
		// current local wall clocks.
		for _, day := range []struct {
			y  int
			mo time.Month
			d  int
		}{{2026, time.June, 15}, {2026, time.October, 2}} {
			for h := 0; h < 24; h++ {
				now := time.Date(day.y, day.mo, day.d, h, 20, 0, 0, loc)
				_, s, e, err := WindowAt(msOf(now), hourRule, "c")
				if err != nil {
					t.Fatalf("WindowAt(%v %02d:20): %v", day, h, err)
				}
				wantStart := time.Date(day.y, day.mo, day.d, h, 0, 0, 0, loc)
				if s != wantStart.UnixMilli() || e != wantStart.Add(time.Hour).UnixMilli() {
					t.Fatalf("Chatham hour %02d must start at local :00, got [%d,%d)", h, s, e)
				}
			}
			now := time.Date(day.y, day.mo, day.d, 9, 0, 0, 0, loc)
			_, s, e, err := WindowAt(msOf(now), dayRule, "c")
			if err != nil {
				t.Fatalf("WindowAt(day): %v", err)
			}
			wantStart := time.Date(day.y, day.mo, day.d, 0, 0, 0, 0, loc)
			if s != wantStart.UnixMilli() || e != wantStart.Add(24*time.Hour).UnixMilli() {
				t.Fatalf("Chatham day must start at local midnight, got [%d,%d)", s, e)
			}
		}
	})
}

// TestWindowAtFractionalHourZones pins day/hour windows in +:30 and +:45
// zones to exact UTC instants — the offsets are not whole hours, so any
// epoch-grid arithmetic shows up immediately here.
func TestWindowAtFractionalHourZones(t *testing.T) {
	t.Run("Asia/Kolkata", func(t *testing.T) {
		loc := mustTZ(t, "Asia/Kolkata") // +05:30, no DST
		dayRule := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "Asia/Kolkata"}
		now := time.Date(2026, 6, 15, 13, 45, 0, 0, loc)
		_, s, e, err := WindowAt(msOf(now), dayRule, "i")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if s != utc(2026, 6, 14, 18, 30).UnixMilli() || e != utc(2026, 6, 15, 18, 30).UnixMilli() {
			t.Fatalf("Kolkata day must be [Jun 14 18:30Z, Jun 15 18:30Z), got [%d,%d)", s, e)
		}
		hourRule := domain.WindowRule{Kind: "fixed", Period: "hour", TimeZone: "Asia/Kolkata"}
		_, s, e, err = WindowAt(msOf(now), hourRule, "i")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if s != utc(2026, 6, 15, 7, 30).UnixMilli() || e != utc(2026, 6, 15, 8, 30).UnixMilli() {
			t.Fatalf("Kolkata hour must be [07:30Z, 08:30Z) = 13:00–14:00 IST, got [%d,%d)", s, e)
		}
	})

	t.Run("Pacific/Chatham", func(t *testing.T) {
		loc := mustTZ(t, "Pacific/Chatham") // +12:45 standard (June)
		dayRule := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "Pacific/Chatham"}
		now := time.Date(2026, 6, 15, 13, 45, 0, 0, loc)
		_, s, e, err := WindowAt(msOf(now), dayRule, "c")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if s != utc(2026, 6, 14, 11, 15).UnixMilli() || e != utc(2026, 6, 15, 11, 15).UnixMilli() {
			t.Fatalf("Chatham day must be [Jun 14 11:15Z, Jun 15 11:15Z), got [%d,%d)", s, e)
		}
		hourRule := domain.WindowRule{Kind: "fixed", Period: "hour", TimeZone: "Pacific/Chatham"}
		_, s, e, err = WindowAt(msOf(now), hourRule, "c")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if s != utc(2026, 6, 15, 0, 15).UnixMilli() || e != utc(2026, 6, 15, 1, 15).UnixMilli() {
			t.Fatalf("Chatham hour must be [00:15Z, 01:15Z) = 13:00–14:00 +12:45, got [%d,%d)", s, e)
		}
	})
}
