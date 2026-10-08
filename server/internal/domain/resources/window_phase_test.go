package resources

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	"ant/server/internal/domain"
)

// --- Reset-phase oracle (S3 matrix: independent phase assertions) ---

// phaseZones covers the six zones mandated by the S3 window matrix (plus
// Singapore). minuteDays lists local days whose following 26h contain a zone
// transition — the only days on which minute slots can deviate from the
// plain wall clock (one plain day for zones without DST).
var phaseZones = []struct {
	name         string
	dayAnchorISO string   // 2026-01-01T09:00 local
	minuteDays   [][3]int // {y, m, d} local
}{
	{"UTC", "2026-01-01T09:00:00Z", [][3]int{{2026, 10, 2}}},
	{"Asia/Kolkata", "2026-01-01T09:00:00+05:30", [][3]int{{2026, 10, 2}}},
	{"Asia/Kathmandu", "2026-01-01T09:00:00+05:45", [][3]int{{2026, 10, 2}}},
	{"Pacific/Chatham", "2026-01-01T09:00:00+13:45", [][3]int{{2026, 4, 5}, {2026, 9, 27}}},
	{"America/New_York", "2026-01-01T09:00:00-05:00", [][3]int{{2026, 3, 8}, {2026, 11, 1}}},
	{"Europe/Berlin", "2026-01-01T09:00:00+01:00", [][3]int{{2026, 3, 29}, {2026, 10, 25}}},
}

// hopWindows walks the window sequence by querying WindowAt at each window's
// end, asserting containment, seamlessness and positivity for every window
// in [from, until) and handing each (start, end) to check.
func hopWindows(t *testing.T, rule domain.WindowRule, from, until time.Time, maxHops int, check func(start, end time.Time)) {
	t.Helper()
	now := from.UnixMilli()
	untilMs := until.UnixMilli()
	prevEnd := int64(math.MinInt64)
	for i := 0; i < maxHops && now < untilMs; i++ {
		_, s, e, err := WindowAt(domain.Millis(now), rule, "phase")
		if err != nil {
			t.Fatalf("WindowAt(%d): %v", now, err)
		}
		if s > now || now >= e {
			t.Fatalf("containment violated: now=%d outside [%d,%d)", now, s, e)
		}
		if prevEnd != math.MinInt64 && s != prevEnd {
			t.Fatalf("partition not seamless: previous window ended %d, next starts %d", prevEnd, s)
		}
		if e <= s {
			t.Fatalf("empty window [%d,%d)", s, e)
		}
		check(time.UnixMilli(s), time.UnixMilli(e))
		prevEnd = e
		now = e
	}
	if now < untilMs {
		t.Fatalf("hop walk exhausted %d hops before reaching %d", maxHops, untilMs)
	}
}

// atZoneTransition reports whether l (already converted into its zone) is
// exactly the instant at which the zone's offset changes. It is the only
// sanctioned wall clock for a gap-resolved boundary (D-B5-v2: 不存在的墙钟取
// 跳变后第一个有效时刻), e.g. Pacific/Chatham's 03:00 slot on a 02:45→03:45
// day resolves to the 03:45 transition.
func atZoneTransition(l time.Time) bool {
	zs, _ := l.ZoneBounds()
	return l.Equal(zs)
}

func requirePhase(t *testing.T, label string, l time.Time, transitionOK bool, h, mi, s, ms int) {
	t.Helper()
	got := wallClock{l.Hour(), l.Minute(), l.Second(), l.Nanosecond() / 1_000_000}
	if got == (wallClock{h, mi, s, ms}) {
		return
	}
	if transitionOK && atZoneTransition(l) {
		return
	}
	t.Fatalf("%s: window start %v has wall clock %02d:%02d:%02d.%03d, want %02d:%02d:%02d.%03d (not a transition instant)",
		label, l, got.hour, got.minute, got.second, got.ms, h, mi, s, ms)
}

// TestWindowAtResetPhase asserts the reset phase INDEPENDENTLY of the
// partition properties: every window boundary lands on the agreed local wall
// clock for each period × timezone (day+anchor 09:00 → every start at local
// 09:00:00.000; hour without anchor → minute=second=0; …), including across
// DST transitions and in fractional-offset zones.
func TestWindowAtResetPhase(t *testing.T) {
	const h = int64(time.Hour / time.Millisecond)
	for _, z := range phaseZones {
		t.Run(z.name, func(t *testing.T) {
			loc := mustTZ(t, z.name)
			jan1 := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
			jan17 := time.Date(2027, 1, 1, 0, 0, 0, 0, loc)

			// hour, no anchor: every reset at local hh:00:00.000 (or exactly
			// at a transition instant for gap slots). Full year covers both
			// transitions per zone.
			hourRule := domain.WindowRule{Kind: "fixed", Period: "hour", TimeZone: z.name}
			hopWindows(t, hourRule, jan1, jan17, 20000, func(start, _ time.Time) {
				l := start.In(loc)
				requirePhase(t, "hour", l, true, l.Hour(), 0, 0, 0)
			})

			// minute, no anchor: every reset at local mm::ss.000 across the
			// transition days (the only days minute slots can distort).
			minuteRule := domain.WindowRule{Kind: "fixed", Period: "minute", TimeZone: z.name}
			for _, d := range z.minuteDays {
				from := time.Date(d[0], time.Month(d[1]), d[2], 0, 0, 0, 0, loc)
				hopWindows(t, minuteRule, from, from.Add(26*time.Hour), 4000, func(start, _ time.Time) {
					l := start.In(loc)
					requirePhase(t, "minute", l, true, l.Hour(), l.Minute(), 0, 0)
				})
			}

			// day anchored at 09:00 local: every start strictly at
			// 09:00:00.000 local; day lengths are exactly 23/24/25h.
			dayRule := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: z.name, AnchorISO: z.dayAnchorISO}
			hopWindows(t, dayRule, jan1, jan17, 400, func(start, end time.Time) {
				l := start.In(loc)
				requirePhase(t, "day+anchor09", l, false, 9, 0, 0, 0)
				switch length := end.UnixMilli() - start.UnixMilli(); length {
				case 23 * h, 24 * h, 25 * h:
				default:
					t.Fatalf("day window length must be 23/24/25h, got %d ms", length)
				}
			})

			// day, no anchor: local midnight (or the transition when midnight
			// itself is skipped).
			dayNatural := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: z.name}
			hopWindows(t, dayNatural, jan1, jan17, 400, func(start, _ time.Time) {
				l := start.In(loc)
				requirePhase(t, "day", l, true, 0, 0, 0, 0)
			})

			// week, no anchor: Monday 00:00:00.000 local; 167/168/169h weeks.
			weekRule := domain.WindowRule{Kind: "fixed", Period: "week", TimeZone: z.name}
			hopWindows(t, weekRule, jan1, jan17, 60, func(start, end time.Time) {
				l := start.In(loc)
				if l.Weekday() != time.Monday {
					t.Fatalf("week must start on Monday local, got %v (%v)", l.Weekday(), l)
				}
				requirePhase(t, "week", l, true, 0, 0, 0, 0)
				switch length := end.UnixMilli() - start.UnixMilli(); length {
				case 167 * h, 168 * h, 169 * h:
				default:
					t.Fatalf("week window length must be 167/168/169h, got %d ms", length)
				}
			})

			// month, no anchor: local day 1 at 00:00:00.000.
			monthRule := domain.WindowRule{Kind: "fixed", Period: "month", TimeZone: z.name}
			hopWindows(t, monthRule, jan1, jan17, 14, func(start, _ time.Time) {
				l := start.In(loc)
				if l.Day() != 1 {
					t.Fatalf("month must start on local day 1, got %v", l)
				}
				requirePhase(t, "month", l, true, 0, 0, 0, 0)
			})
		})
	}
}

// --- Property tests: containment, seamless partition, determinism ---

var windowPropertyZones = []struct {
	name      string
	anchorISO string // local to the zone; Thursday 09:30 in each
}{
	{"UTC", "2026-01-15T09:30:00Z"},
	{"Asia/Kolkata", "2026-01-15T09:30:00+05:30"},    // half-hour offset
	{"Asia/Kathmandu", "2026-01-15T09:30:00+05:45"},  // quarter-hour offset, changed in 1986
	{"Pacific/Chatham", "2026-01-15T09:30:00+13:45"}, // quarter-hour offset with DST
	{"America/New_York", "2026-01-15T09:30:00-05:00"},
	{"Europe/Berlin", "2026-01-15T09:30:00+01:00"},
	{"Asia/Singapore", "2026-01-15T09:30:00+08:00"},
}

// TestWindowAtProperties walks dense sample sequences through every
// supported period × zone × anchor combination (the S3 matrix: {minute, hour,
// day, week, month} × fractional/DST zones, anchored and not) and asserts the
// frozen invariants for every sample: start <= now < end, windows are
// seamless (each window starts exactly where the previous one ended),
// non-empty, and WindowAt is deterministic. Dense steps are shorter than the
// minimum window length, so no window can be silently skipped.
func TestWindowAtProperties(t *testing.T) {
	periods := []string{"minute", "hour", "day", "week", "month"}
	for _, zone := range windowPropertyZones {
		for _, anchored := range []bool{false, true} {
			for _, period := range periods {
				rule := domain.WindowRule{Kind: "fixed", Period: period, TimeZone: zone.name}
				if anchored {
					rule.AnchorISO = zone.anchorISO
				}
				name := fmt.Sprintf("%s/%s/anchored=%t", period, zone.name, anchored)
				t.Run(name, func(t *testing.T) {
					switch period {
					case "minute":
						// Cross the New York transitions; 20s < 60s. Minute
						// slots at the Berlin/Chatham/Santiago transitions are
						// covered by the reset-phase walk and the dedicated
						// DST tests.
						walkWindows(t, rule, time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), 6000, 20*time.Second)
						walkWindows(t, rule, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), 6000, 20*time.Second)
					case "hour":
						// 20m < 30m (shortest gap-window).
						walkWindows(t, rule, time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), 700, 20*time.Minute)
						walkWindows(t, rule, time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC), 700, 20*time.Minute)
					case "day":
						// 8h < 23h; 4000 steps cross leap day and six DSTs.
						walkWindows(t, rule, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), 4000, 8*time.Hour)
					case "week":
						// 48h < 7d.
						walkWindows(t, rule, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), 550, 48*time.Hour)
					case "month":
						// 10d < 28d.
						walkWindows(t, rule, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), 140, 240*time.Hour)
					}
					// Sparse random containment over three years (windows may
					// be skipped between samples; only containment holds).
					randomContainment(t, rule, utc(2024, 1, 1, 0, 0).UnixMilli(), utc(2027, 1, 1, 0, 0).UnixMilli(), 300)
				})
			}
		}
	}
}

func walkWindows(t *testing.T, rule domain.WindowRule, from time.Time, samples int, step time.Duration) {
	t.Helper()
	const unset = int64(math.MinInt64)
	prevStart, prevEnd := unset, unset
	nowMs := from.UnixMilli()
	for i := 0; i < samples; i++ {
		now := domain.Millis(nowMs)
		key, start, end, err := WindowAt(now, rule, "prop")
		if err != nil {
			t.Fatalf("WindowAt(%d): %v", nowMs, err)
		}
		if start > nowMs || nowMs >= end {
			t.Fatalf("containment violated: now=%d outside [%d,%d)", nowMs, start, end)
		}
		if end <= start {
			t.Fatalf("empty or negative window [%d,%d)", start, end)
		}
		if start != prevStart {
			if prevStart != unset && start != prevEnd {
				t.Fatalf("partition not seamless: previous window ended %d, next starts %d", prevEnd, start)
			}
			prevStart, prevEnd = start, end
		}
		if key != wantKey("prop", start, end) {
			t.Fatalf("key %s does not match bounds [%d,%d)", key, start, end)
		}
		if i%97 == 0 {
			k2, s2, e2, err2 := WindowAt(now, rule, "prop")
			if err2 != nil || k2 != key || s2 != start || e2 != end {
				t.Fatalf("WindowAt is not deterministic: (%s,%d,%d) vs (%s,%d,%d)", key, start, end, k2, s2, e2)
			}
		}
		nowMs += step.Milliseconds() // step is a Duration (ns); walk in ms
	}
}

func randomContainment(t *testing.T, rule domain.WindowRule, minMs, maxMs int64, samples int) {
	t.Helper()
	rnd := rand.New(rand.NewSource(7))
	for i := 0; i < samples; i++ {
		now := domain.Millis(minMs + rnd.Int63n(maxMs-minMs))
		_, start, end, err := WindowAt(now, rule, "prop")
		if err != nil {
			t.Fatalf("WindowAt(%d): %v", int64(now), err)
		}
		if start > int64(now) || int64(now) >= end {
			t.Fatalf("containment violated: now=%d outside [%d,%d)", int64(now), start, end)
		}
	}
}
