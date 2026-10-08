package resources

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"ant/server/internal/domain"
)

func msOf(t time.Time) domain.Millis { return domain.Millis(t.UnixMilli()) }

func utc(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func utcSec(y int, mo time.Month, d, h, mi, s, ms int) time.Time {
	return time.Date(y, mo, d, h, mi, s, ms*1_000_000, time.UTC)
}

func mustTZ(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		// No skip: a tzdata-less environment is a hard failure for these
		// boundary tests, not a pass.
		t.Fatalf("timezone data unavailable for %s: %v", name, err)
	}
	return loc
}

func wantKey(rev string, start, end int64) domain.ID {
	return domain.ID(fmt.Sprintf("%s:%d:%d", rev, start, end))
}

// --- Conformance tests (original contract, unchanged semantics) ---

func TestWindowAtAsiaSingaporeDayBoundary(t *testing.T) {
	loc := mustTZ(t, "Asia/Singapore")
	local := time.Date(2026, 5, 10, 14, 30, 0, 0, loc) // SGT = UTC+8, no DST
	rule := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "Asia/Singapore"}
	wantStart := utc(2026, 5, 9, 16, 0).UnixMilli()
	wantEnd := utc(2026, 5, 10, 16, 0).UnixMilli()
	key, start, end, err := WindowAt(msOf(local), rule, "rev1")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != wantStart || end != wantEnd {
		t.Fatalf("want [%d,%d), got [%d,%d)", wantStart, wantEnd, start, end)
	}
	if key != wantKey("rev1", wantStart, wantEnd) {
		t.Fatalf("want key %s, got %s", wantKey("rev1", wantStart, wantEnd), key)
	}
}

func TestWindowAtNewYorkDSTDays(t *testing.T) {
	loc := mustTZ(t, "America/New_York")
	rule := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "America/New_York"}

	// Spring forward 2026-03-08: the day still starts at local midnight
	// (EST, UTC-5); it ends at next local midnight which is already EDT
	// (UTC-4): 23 wall-clock hours elapsed.
	before := time.Date(2026, 3, 8, 1, 30, 0, 0, loc)
	_, start, end, err := WindowAt(msOf(before), rule, "r")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2026, 3, 8, 5, 0).UnixMilli() || end != utc(2026, 3, 9, 4, 0).UnixMilli() {
		t.Fatalf("spring-forward day wrong: [%d,%d)", start, end)
	}

	// Summer (EDT, UTC-4): local midnight = 04:00Z.
	summer := time.Date(2026, 7, 15, 12, 0, 0, 0, loc)
	_, start, end, err = WindowAt(msOf(summer), rule, "r")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2026, 7, 15, 4, 0).UnixMilli() || end != utc(2026, 7, 16, 4, 0).UnixMilli() {
		t.Fatalf("summer day wrong: [%d,%d)", start, end)
	}

	// Fall back 2026-11-01: local midnight is EDT (UTC-4) at the start of the
	// day and EST (UTC-5) at its end: 25 wall-clock hours.
	fall := time.Date(2026, 11, 1, 1, 30, 0, 0, loc)
	_, start, end, err = WindowAt(msOf(fall), rule, "r")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2026, 11, 1, 4, 0).UnixMilli() || end != utc(2026, 11, 2, 5, 0).UnixMilli() {
		t.Fatalf("fall-back day wrong: [%d,%d)", start, end)
	}
}

func TestWindowAtMonthBoundaries(t *testing.T) {
	rule := domain.WindowRule{Kind: "fixed", Period: "month", TimeZone: "UTC"}
	// January 31 belongs to the January window, which ends Feb 1.
	_, start, end, err := WindowAt(msOf(utc(2026, 1, 31, 10, 0)), rule, "m")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2026, 1, 1, 0, 0).UnixMilli() || end != utc(2026, 2, 1, 0, 0).UnixMilli() {
		t.Fatalf("January window wrong: [%d,%d)", start, end)
	}
	// 2026 is not a leap year: the February window is 28 days, ending Mar 1.
	_, start, end, err = WindowAt(msOf(utc(2026, 2, 1, 0, 30)), rule, "m")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2026, 2, 1, 0, 0).UnixMilli() || end != utc(2026, 3, 1, 0, 0).UnixMilli() {
		t.Fatalf("February window wrong: [%d,%d)", start, end)
	}
	// Leap year: the February 2024 window is 29 calendar days (never a
	// 30-day approximation).
	_, start, end, err = WindowAt(msOf(utc(2024, 2, 29, 12, 0)), rule, "m")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2024, 2, 1, 0, 0).UnixMilli() || end != utc(2024, 3, 1, 0, 0).UnixMilli() {
		t.Fatalf("leap February window wrong: [%d,%d)", start, end)
	}
	if end-start != 29*24*int64(time.Hour/time.Millisecond) {
		t.Fatalf("leap February window must be 29 days, got %d ms", end-start)
	}
}

func TestWindowAtISOWeekBoundaries(t *testing.T) {
	rule := domain.WindowRule{Kind: "fixed", Period: "week", TimeZone: "UTC"}
	// 2026-10-14 is a Wednesday; its ISO week starts Monday 2026-10-12.
	_, start, end, err := WindowAt(msOf(utc(2026, 10, 14, 12, 0)), rule, "w")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2026, 10, 12, 0, 0).UnixMilli() || end != utc(2026, 10, 19, 0, 0).UnixMilli() {
		t.Fatalf("Wednesday week wrong: [%d,%d)", start, end)
	}
	// Sunday belongs to the week that started six days earlier.
	_, start, end, err = WindowAt(msOf(utc(2026, 10, 11, 23, 0)), rule, "w")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2026, 10, 5, 0, 0).UnixMilli() || end != utc(2026, 10, 12, 0, 0).UnixMilli() {
		t.Fatalf("Sunday week wrong: [%d,%d)", start, end)
	}
}

func TestWindowAtAnchorOffset(t *testing.T) {
	rule := domain.WindowRule{
		Kind: "fixed", Period: "day", TimeZone: "UTC",
		AnchorISO: "1970-01-01T09:00:00Z", // daily windows start at 09:00Z
	}
	// 10:00Z is inside the window that started 09:00Z the same day.
	key, start, end, err := WindowAt(msOf(utc(2026, 3, 8, 10, 0)), rule, "a")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2026, 3, 8, 9, 0).UnixMilli() || end != utc(2026, 3, 9, 9, 0).UnixMilli() {
		t.Fatalf("anchored day wrong: [%d,%d)", start, end)
	}
	if key != wantKey("a", start, end) {
		t.Fatalf("anchored key wrong: %s", key)
	}
	// 08:00Z belongs to the window that started 09:00Z the previous day.
	_, start, end, err = WindowAt(msOf(utc(2026, 3, 8, 8, 0)), rule, "a")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2026, 3, 7, 9, 0).UnixMilli() || end != utc(2026, 3, 8, 9, 0).UnixMilli() {
		t.Fatalf("anchored day before boundary wrong: [%d,%d)", start, end)
	}
	// Hour rule with a half-past anchor.
	hourRule := domain.WindowRule{Kind: "fixed", Period: "hour", TimeZone: "UTC", AnchorISO: "2000-01-01T00:30:00Z"}
	_, start, end, err = WindowAt(msOf(utc(2026, 3, 8, 10, 45)), hourRule, "a")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2026, 3, 8, 10, 30).UnixMilli() || end != utc(2026, 3, 8, 11, 30).UnixMilli() {
		t.Fatalf("anchored hour wrong: [%d,%d)", start, end)
	}
}

func TestWindowAtRollingAndLifetime(t *testing.T) {
	key, start, end, err := WindowAt(msOf(utc(2026, 1, 1, 0, 0)), domain.WindowRule{Kind: "rolling"}, "rv")
	if !errors.Is(err, ErrRollingNoBounds) {
		t.Fatalf("rolling must return ErrRollingNoBounds, got %v", err)
	}
	if key != "rv:rolling" || start != 0 || end != 0 {
		t.Fatalf("rolling key wrong: %s [%d,%d)", key, start, end)
	}
	key, start, end, err = WindowAt(msOf(utc(2026, 1, 1, 0, 0)), domain.WindowRule{Kind: "lifetime"}, "lv")
	if err != nil {
		t.Fatalf("lifetime must not error, got %v", err)
	}
	if key != "lv:lifetime" {
		t.Fatalf("lifetime key wrong: %s", key)
	}
}

func TestWindowAtValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		rule domain.WindowRule
	}{
		{"unknown kind", domain.WindowRule{Kind: "bogus", Period: "day"}},
		{"unknown period", domain.WindowRule{Kind: "fixed", Period: "fortnight", TimeZone: "UTC"}},
		{"bad zone", domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "Mars/Olympus"}},
		{"bad anchor", domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "UTC", AnchorISO: "not-a-time"}},
		// Sub-millisecond anchors are rejected, never truncated (D-B5-v2).
		{"sub-millisecond anchor", domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "UTC", AnchorISO: "2026-01-01T09:00:00.500000001Z"}},
	}
	for _, tc := range cases {
		if _, _, _, err := WindowAt(msOf(utc(2026, 1, 1, 0, 0)), tc.rule, "r"); err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
	}
}

// TestWindowAtSingaporeNineOClockAnchor is the singapore-09-anchor audit
// probe: the anchor's LOCAL clock (09:00 SGT) must define the day boundary.
func TestWindowAtSingaporeNineOClockAnchor(t *testing.T) {
	loc := mustTZ(t, "Asia/Singapore")
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, loc)
	rule := domain.WindowRule{
		Kind: "fixed", Period: "day", TimeZone: "Asia/Singapore",
		AnchorISO: "2026-01-01T09:00:00+08:00",
	}
	key, start, end, err := WindowAt(msOf(now), rule, "rev-1")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	wantStart := time.Date(2026, 10, 2, 9, 0, 0, 0, loc)
	wantEnd := time.Date(2026, 10, 3, 9, 0, 0, 0, loc)
	if start != wantStart.UnixMilli() || end != wantEnd.UnixMilli() {
		t.Fatalf("want [%d,%d) (09:00 SGT boundaries), got [%d,%d)",
			wantStart.UnixMilli(), wantEnd.UnixMilli(), start, end)
	}
	if got := time.UnixMilli(start).In(loc); got.Hour() != 9 {
		t.Fatalf("window must start at 09:00 local, got %v", got)
	}
	if key != wantKey("rev-1", start, end) {
		t.Fatalf("key wrong: %s", key)
	}
}

// --- Rework-2 audit probe regressions (REVIEW-T1-REWORK-CODEX B5) ---

// TestWindowAtRework2AuditProbes pins the three B5 counterexamples from
// docs/evidence/CODEX-T1-REWORK-AUDIT/rework_audit_test.go.txt
// (TestCodexReworkWindowSemantics) as permanent regressions. The pre-rework
// implementation failed all three: the 1970 epoch grid shifted Kathmandu
// hours to :15, Go's time.Date fold choice put the Berlin start on the
// previous day, and fractional anchors were silently truncated to .000.
func TestWindowAtRework2AuditProbes(t *testing.T) {
	t.Run("current-zone-natural-hour", func(t *testing.T) {
		loc := mustTZ(t, "Asia/Kathmandu")
		now := time.Date(2026, 10, 2, 12, 20, 0, 0, loc)
		_, s, e, err := WindowAt(msOf(now),
			domain.WindowRule{Kind: "fixed", Period: "hour", TimeZone: "Asia/Kathmandu"}, "rev-1")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		wantStart := time.Date(2026, 10, 2, 12, 0, 0, 0, loc)
		wantEnd := time.Date(2026, 10, 2, 13, 0, 0, 0, loc)
		if s != wantStart.UnixMilli() || e != wantEnd.UnixMilli() {
			t.Fatalf("natural hour must be [12:00,13:00) local: want [%d,%d), got [%d,%d)",
				wantStart.UnixMilli(), wantEnd.UnixMilli(), s, e)
		}
		// Independent wall-clock assertion (the probe asserted Hour/Minute
		// only; the defective grid produced 12:15 starts).
		if got := time.UnixMilli(s).In(loc); got.Minute() != 0 || got.Second() != 0 || got.Nanosecond() != 0 {
			t.Fatalf("natural hour must start at :00:00.000 local, got %v", got)
		}
	})

	t.Run("berlin-fall-back-earlier-anchor", func(t *testing.T) {
		now, _ := time.Parse(time.RFC3339, "2026-10-25T02:30:00+02:00") // first 02:30
		expected, _ := time.Parse(time.RFC3339, "2026-10-25T02:00:00+02:00")
		rule := domain.WindowRule{
			Kind: "fixed", Period: "day", TimeZone: "Europe/Berlin",
			AnchorISO: "2026-01-01T02:00:00+01:00",
		}
		_, s, e, err := WindowAt(msOf(now), rule, "rev-1")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if s != expected.UnixMilli() {
			t.Fatalf("ambiguous day anchor must resolve to the FIRST 02:00 (+02:00): want %d, got %d",
				expected.UnixMilli(), s)
		}
		loc := mustTZ(t, "Europe/Berlin")
		got := time.UnixMilli(s).In(loc)
		if got.Hour() != 2 || got.Minute() != 0 || got.Second() != 0 {
			t.Fatalf("start wall clock must be 02:00:00 local, got %v", got)
		}
		if _, off := got.Zone(); off != 2*3600 {
			t.Fatalf("first 02:00 carries the pre-transition offset +02:00, got %ds", off)
		}
		wantEnd := time.Date(2026, 10, 26, 2, 0, 0, 0, loc) // next day's first 02:00 (CET)
		if e != wantEnd.UnixMilli() {
			t.Fatalf("fall-back day window must be 25h: want end %d, got %d", wantEnd.UnixMilli(), e)
		}
	})

	t.Run("fractional-anchor-not-truncated", func(t *testing.T) {
		now, _ := time.Parse(time.RFC3339, "2026-10-02T12:00:00Z")
		expected, _ := time.Parse(time.RFC3339, "2026-10-02T09:00:00.500Z")
		rule := domain.WindowRule{
			Kind: "fixed", Period: "day", TimeZone: "UTC",
			AnchorISO: "2026-01-01T09:00:00.500Z",
		}
		_, s, e, err := WindowAt(msOf(now), rule, "rev-1")
		if err != nil {
			t.Fatalf("WindowAt: %v", err)
		}
		if s != expected.UnixMilli() {
			t.Fatalf("millisecond anchor precision must be preserved: want %d, got %d", expected.UnixMilli(), s)
		}
		wantEnd, _ := time.Parse(time.RFC3339, "2026-10-03T09:00:00.500Z")
		if e != wantEnd.UnixMilli() {
			t.Fatalf("end must carry the same .500 phase: want %d, got %d", wantEnd.UnixMilli(), e)
		}
	})
}

// --- Anchor precision (D-B5-v2: millisecond support, sub-ms rejection) ---

// TestWindowAtAnchorPrecision checks both sides of the frozen precision
// contract: sub-millisecond anchors are rejected with ErrAnchorPrecision for
// every period (config admission matches via errors.Is), while millisecond
// anchors are accepted and their sub-second phase is preserved in minute and
// hour boundaries.
func TestWindowAtAnchorPrecision(t *testing.T) {
	periods := []string{"minute", "hour", "day", "week", "month"}
	for _, period := range periods {
		rule := domain.WindowRule{Kind: "fixed", Period: period, TimeZone: "UTC", AnchorISO: "2026-01-01T09:00:00.500000001Z"}
		if _, _, _, err := WindowAt(msOf(utc(2026, 10, 2, 12, 0)), rule, "p"); !errors.Is(err, ErrAnchorPrecision) {
			t.Fatalf("%s: sub-millisecond anchor must return ErrAnchorPrecision, got %v", period, err)
		}
		rule.AnchorISO = "2026-01-01T09:00:00.999999Z" // finer than ms but not ms-aligned
		if _, _, _, err := WindowAt(msOf(utc(2026, 10, 2, 12, 0)), rule, "p"); !errors.Is(err, ErrAnchorPrecision) {
			t.Fatalf("%s: 999999ns anchor must return ErrAnchorPrecision, got %v", period, err)
		}
		rule.AnchorISO = "2026-01-01T09:00:00.500Z" // exactly millisecond precision
		if _, _, _, err := WindowAt(msOf(utc(2026, 10, 2, 12, 0)), rule, "p"); err != nil {
			t.Fatalf("%s: millisecond anchor must be accepted, got %v", period, err)
		}
	}

	// The millisecond phase is preserved in minute slots (anchor phase =
	// second+ms) and hour slots (minute+second+ms).
	minuteRule := domain.WindowRule{Kind: "fixed", Period: "minute", TimeZone: "UTC", AnchorISO: "2026-01-01T00:00:30.250Z"}
	_, s, e, err := WindowAt(msOf(utcSec(2026, 10, 2, 12, 0, 30, 400)), minuteRule, "p")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if want := utcSec(2026, 10, 2, 12, 0, 30, 250).UnixMilli(); s != want {
		t.Fatalf("minute slot must keep the :30.250 phase: want %d, got %d", want, s)
	}
	if want := utcSec(2026, 10, 2, 12, 1, 30, 250).UnixMilli(); e != want {
		t.Fatalf("minute slot end must keep the :30.250 phase: want %d, got %d", want, e)
	}
	hourRule := domain.WindowRule{Kind: "fixed", Period: "hour", TimeZone: "UTC", AnchorISO: "2026-01-01T00:15:20.500Z"}
	_, s, e, err = WindowAt(msOf(utc(2026, 10, 2, 10, 45)), hourRule, "p")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if want := utcSec(2026, 10, 2, 10, 15, 20, 500).UnixMilli(); s != want {
		t.Fatalf("hour slot must keep the 15:20.500 phase: want %d, got %d", want, s)
	}
	if want := utcSec(2026, 10, 2, 11, 15, 20, 500).UnixMilli(); e != want {
		t.Fatalf("hour slot end must keep the 15:20.500 phase: want %d, got %d", want, e)
	}
}

// --- Anchored week / month semantics ---

func TestWindowAtAnchoredWeekUsesAnchorWeekday(t *testing.T) {
	loc := mustTZ(t, "America/New_York")
	// Anchor is a Thursday (2026-01-15); week windows run Thu 09:30 local.
	rule := domain.WindowRule{
		Kind: "fixed", Period: "week", TimeZone: "America/New_York",
		AnchorISO: "2026-01-15T09:30:00-05:00",
	}
	// 2026-10-14 is a Wednesday: its window started Thursday 2026-10-08
	// 09:30 EDT and ends Thursday 2026-10-15 09:30 EDT.
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, loc)
	_, start, end, err := WindowAt(msOf(now), rule, "w")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	wantStart := time.Date(2026, 10, 8, 9, 30, 0, 0, loc)
	wantEnd := time.Date(2026, 10, 15, 9, 30, 0, 0, loc)
	if start != wantStart.UnixMilli() || end != wantEnd.UnixMilli() {
		t.Fatalf("want anchored week [%d,%d), got [%d,%d)", wantStart.UnixMilli(), wantEnd.UnixMilli(), start, end)
	}
	if got := time.UnixMilli(start).In(loc).Weekday(); got != time.Thursday {
		t.Fatalf("anchored week must start on the anchor weekday (Thursday), got %v", got)
	}
}

func TestWindowAtAnchoredMonthClampsShortMonths(t *testing.T) {
	loc := mustTZ(t, "Asia/Kolkata")
	rule := domain.WindowRule{
		Kind: "fixed", Period: "month", TimeZone: "Asia/Kolkata",
		AnchorISO: "2026-01-31T09:00:00+05:30", // day 31, 09:00 IST
	}
	// The February anchor itself is clamped to Feb 28 09:00 IST; instants
	// before it belong to the January window [Jan 31 09:00, Feb 28 09:00).
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, loc)
	_, start, end, err := WindowAt(msOf(now), rule, "m")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	wantStart := time.Date(2026, 1, 31, 9, 0, 0, 0, loc)
	wantEnd := time.Date(2026, 2, 28, 9, 0, 0, 0, loc)
	if start != wantStart.UnixMilli() || end != wantEnd.UnixMilli() {
		t.Fatalf("pre-anchor instant belongs to the January window: want [%d,%d), got [%d,%d)",
			wantStart.UnixMilli(), wantEnd.UnixMilli(), start, end)
	}
	// From the clamped boundary onward, the February window runs
	// [Feb 28 09:00, Mar 31 09:00).
	now = time.Date(2026, 2, 28, 12, 0, 0, 0, loc)
	_, start, end, err = WindowAt(msOf(now), rule, "m")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	wantStart = time.Date(2026, 2, 28, 9, 0, 0, 0, loc)
	wantEnd = time.Date(2026, 3, 31, 9, 0, 0, 0, loc)
	if start != wantStart.UnixMilli() || end != wantEnd.UnixMilli() {
		t.Fatalf("clamped February boundary wrong: want [%d,%d), got [%d,%d)",
			wantStart.UnixMilli(), wantEnd.UnixMilli(), start, end)
	}
	// Mid-January belongs to the December window [Dec 31, Jan 31).
	now = time.Date(2026, 1, 15, 8, 0, 0, 0, loc)
	_, start, end, err = WindowAt(msOf(now), rule, "m")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	wantStart = time.Date(2025, 12, 31, 9, 0, 0, 0, loc)
	wantEnd = time.Date(2026, 1, 31, 9, 0, 0, 0, loc)
	if start != wantStart.UnixMilli() || end != wantEnd.UnixMilli() {
		t.Fatalf("December window wrong: want [%d,%d), got [%d,%d)",
			wantStart.UnixMilli(), wantEnd.UnixMilli(), start, end)
	}
}

func TestWindowAtAnchoredMonthLeapDay(t *testing.T) {
	rule := domain.WindowRule{
		Kind: "fixed", Period: "month", TimeZone: "UTC",
		AnchorISO: "2024-01-29T00:00:00Z", // day 29
	}
	// Leap year: Feb 29 2024 exists and is the boundary; from that instant
	// on, the February window runs [Feb 29, Mar 29).
	_, start, end, err := WindowAt(msOf(utc(2024, 2, 29, 12, 0)), rule, "m")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2024, 2, 29, 0, 0).UnixMilli() || end != utc(2024, 3, 29, 0, 0).UnixMilli() {
		t.Fatalf("leap-year anchored February wrong: [%d,%d)", start, end)
	}
	// Non-leap year: Feb 29 clamps to Feb 28 2025; the anchored February
	// window is [Feb 28 00:00Z, Mar 29 00:00Z).
	_, start, end, err = WindowAt(msOf(utc(2025, 2, 28, 12, 0)), rule, "m")
	if err != nil {
		t.Fatalf("WindowAt: %v", err)
	}
	if start != utc(2025, 2, 28, 0, 0).UnixMilli() || end != utc(2025, 3, 29, 0, 0).UnixMilli() {
		t.Fatalf("non-leap anchored February must clamp to Feb 28: [%d,%d)", start, end)
	}
}
