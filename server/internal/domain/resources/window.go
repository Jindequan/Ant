// Package resources holds the pure ResourceSemantics rules of
// docs/design/04-RESOURCE-ALGORITHMS.md. Inputs are injected time, validated
// rules, transaction snapshots, requirements and reports; outputs are
// ResourceChangePlans (or window keys / availability). No clock reads, no
// network, no SQL, no global state. Every plan returned here is validated
// again by ValidateChange and the Ledger before commit (I01–I10); admission
// refusals come back as plans with Admitted=false plus reasons, while errors
// signal illegal calls or arithmetic overflow.
package resources

import (
	"errors"
	"fmt"
	"time"

	"ant/server/internal/domain"
)

// ErrRollingNoBounds is returned by WindowAt for rolling rules: their key is
// stable for the rule revision (see WindowAt) but they have no fixed
// [start,end) bounds — net consumption within the window is injected by the
// caller via ResourceContext.RollingUsed (04 §5).
var ErrRollingNoBounds = errors.New("window: rolling rules have no fixed bounds")

// ErrAnchorPrecision is returned by WindowAt when rule.AnchorISO carries
// sub-millisecond precision. Frozen precision contract (D-B5-v2 in
// docs/evidence/T1-REWORK-2/S1-freeze-decisions.md): anchors support exactly
// millisecond precision; finer input is rejected explicitly here and at the
// config admission boundary instead of being silently truncated. Callers can
// match this error with errors.Is.
var ErrAnchorPrecision = errors.New("window: anchor precision finer than one millisecond")

// WindowAt resolves the deterministic window containing now for one rule
// revision (04 §1).
//
//	windowKey = "<revision>:<startMs>:<endMs>" over [start,end)   (fixed)
//	windowKey = "<revision>:rolling" + ErrRollingNoBounds         (rolling)
//	windowKey = "<revision>:lifetime", bounds 0 and unused        (lifetime)
//
// Frozen calendar semantics (D-B5-v2, replacing the first-round W1 freeze
// after REVIEW-T1-REWORK-CODEX B5): every fixed period is constructed on the
// LOCAL CALENDAR of rule.TimeZone (IANA; "" means UTC) — never on a 1970
// epoch-offset grid, which leaked historical zone offsets into current
// windows (Asia/Kathmandu +05:45 produced 12:15-based hours in 2026).
//
// rule.AnchorISO, when set, is an RFC3339 instant (optional fractional
// seconds at millisecond precision; finer precision returns
// ErrAnchorPrecision) whose LOCAL WALL CLOCK in the rule zone is the anchor
// within each period:
//
//	minute: the anchor's second+subsecond phase (boundaries carry :ss.mmm);
//	hour:   the anchor's minute+second+subsecond phase;
//	day:    the anchor's time of day (hh:mm:ss.mmm);
//	week:   additionally the anchor's WEEKDAY (without an anchor: ISO week,
//	        Monday 00:00 local);
//	month:  additionally the anchor's DAY OF MONTH, clamped to the month end
//	        for short months (day 31 → Apr 30; never a 30-day approximation);
//	        without an anchor: the calendar month from local day 1 00:00.
//	Without an anchor, minute/hour/day are the natural period starts
//	(:00.000 / :00.000 / local midnight).
//
// DST policy (frozen; resolved by explicit candidate enumeration in
// window_fold.go — time.Date's normalization choice is never trusted, the
// Berlin 2026-10-25 fold counterexample proved it picks the first occurrence
// in New York but the second in Berlin):
//   - a wall-clock time shown twice (fall-back) resolves to its EARLIEST
//     occurrence (Berlin's first 02:00 +02:00) for day/week/month boundaries;
//   - a wall-clock time that does not exist (spring-forward gap) resolves to
//     the first valid instant after the jump — exactly the transition instant;
//   - window end = the next period's start under the same policy, so
//     fall-back days are naturally 25h and spring-forward days 23h.
//
// minute/hour boundaries: every occurrence of the anchor phase on the local
// wall clock is a boundary, and consecutive occurrences bound one window.
// This is the only self-consistent reading of the freeze for slot periods:
// resolving each civil slot to its earliest occurrence would produce EMPTY
// windows at gap slots (the "02:00" hour on a 02:00→03:00 day would be
// [03:00, 03:00)), while the boundary sequence keeps every window non-empty,
// seamless and phase-correct. A fall-back day then contains 25 hourly
// windows — two for the repeated wall-clock hour, matching its 25h day
// window — and a gap hour yields one 2-real-hour window ending at the
// transition. Containment (start<=now<end), seamlessness, determinism and
// the reset phase hold unconditionally (property-tested).
//
// Configuration support closure: the WindowDef validator (internal/config,
// integrator-owned, checkWindow) accepts Kind=fixed with Period ∈
// {minute,hour,day,week,month} and an optional RFC3339 anchorISO — exactly
// the set implemented here. Any divergence between that set and this
// implementation is a defect to report to the integrator, not to paper over
// at run time.
func WindowAt(now domain.Millis, rule domain.WindowRule, ruleRevision domain.ID) (windowKey domain.ID, startMs, endMs int64, err error) {
	rev := string(ruleRevision)
	switch rule.Kind {
	case "lifetime":
		return domain.ID(rev + ":lifetime"), 0, 0, nil
	case "rolling":
		return domain.ID(rev + ":rolling"), 0, 0, ErrRollingNoBounds
	case "fixed":
		// handled below
	default:
		return "", 0, 0, fmt.Errorf("window: unknown kind %q", rule.Kind)
	}

	loc, err := time.LoadLocation(rule.TimeZone) // "" loads UTC
	if err != nil {
		return "", 0, 0, fmt.Errorf("window: invalid time zone %q: %w", rule.TimeZone, err)
	}

	anchor, err := parseAnchor(rule.AnchorISO, loc)
	if err != nil {
		return "", 0, 0, err
	}

	switch rule.Period {
	case "minute":
		return slotWindow(now, rev, anchor, loc, time.Minute, "minute")
	case "hour":
		return slotWindow(now, rev, anchor, loc, time.Hour, "hour")
	case "day", "week", "month":
		// calendar periods, handled below
	default:
		return "", 0, 0, fmt.Errorf("window: unknown period %q", rule.Period)
	}

	t := time.UnixMilli(int64(now)).In(loc)
	date := civilDateOf(t)
	if clockMillis(t) < anchor.clockMillisOfDay() {
		// now's wall clock is before the anchor position within its period:
		// the containing period started on the previous civil date.
		date = date.addDays(-1)
	}

	switch rule.Period {
	case "day":
		return civilWindow(now, rev, date, anchor, loc, 1)
	case "week":
		// Step back to the anchor's weekday (Monday without an anchor: ISO week).
		date = date.addDays(-mod7(date.weekday() - anchor.weekday))
		return civilWindow(now, rev, date, anchor, loc, 7)
	case "month":
		return monthWindow(now, rev, date, anchor, loc)
	default:
		return "", 0, 0, fmt.Errorf("window: unknown period %q", rule.Period) // unreachable
	}
}

// anchorSpec is the resolved within-period anchor position. Without
// AnchorISO it is the natural period start: clock 00:00:00.000, Monday,
// day 1 (D-B5-v2).
type anchorSpec struct {
	clock      wallClock // hh:mm:ss.mmm in the rule zone
	weekday    int       // time.Weekday: 0=Sunday … 6=Saturday
	dayOfMonth int
}

func (a anchorSpec) clockMillisOfDay() int {
	return (a.clock.hour*3600+a.clock.minute*60+a.clock.second)*1000 + a.clock.ms
}

// parseAnchor extracts the anchor's local clock, weekday and day-of-month in
// the RULE zone (not the anchor literal's own offset): the anchor denotes a
// wall-clock position inside each period, so "2026-01-01T09:00:00+08:00"
// under Asia/Singapore is 09:00 SGT. RFC3339 optional fractional seconds are
// accepted at millisecond precision; anything finer is rejected with
// ErrAnchorPrecision (D-B5-v2) instead of being silently truncated.
func parseAnchor(iso string, loc *time.Location) (anchorSpec, error) {
	if iso == "" {
		return anchorSpec{weekday: int(time.Monday), dayOfMonth: 1}, nil
	}
	a, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return anchorSpec{}, fmt.Errorf("window: invalid anchor %q: %w", iso, err)
	}
	if ns := a.Nanosecond(); ns%1_000_000 != 0 {
		return anchorSpec{}, fmt.Errorf("window: anchor %q carries sub-millisecond precision (%d ns): %w", iso, ns, ErrAnchorPrecision)
	}
	local := a.In(loc)
	return anchorSpec{
		clock:      wallClock{local.Hour(), local.Minute(), local.Second(), local.Nanosecond() / 1_000_000},
		weekday:    int(local.Weekday()),
		dayOfMonth: local.Day(),
	}, nil
}

// slotWindow resolves minute (unit=time.Minute) and hour (unit=time.Hour)
// windows on the local wall clock of the rule zone — never on a 1970 epoch
// grid (the Kathmandu counterexample, REVIEW B5). Boundaries are all
// occurrences of the anchor phase on the wall clock, and consecutive
// occurrences bound one window (see the WindowAt doc comment for why);
// phaseBoundaryRange enumerates them exactly.
func slotWindow(now domain.Millis, rev string, a anchorSpec, loc *time.Location, unit time.Duration, name string) (domain.ID, int64, int64, error) {
	var periodMs, phaseMs int64
	if unit == time.Hour {
		periodMs = 60 * 60_000
		phaseMs = int64(a.clock.minute*60+a.clock.second)*1000 + int64(a.clock.ms)
	} else {
		periodMs = 60_000
		phaseMs = int64(a.clock.second)*1000 + int64(a.clock.ms)
	}
	nowMs := int64(now)
	startMs, endMs, err := phaseBoundaryRange(nowMs, loc, periodMs, phaseMs)
	if err != nil {
		return "", 0, 0, err
	}
	if startMs > nowMs || nowMs >= endMs {
		// Defensive (never observed): the grid around now failed to bracket it.
		return "", 0, 0, fmt.Errorf("window: failed to bracket %s window for revision %q", name, rev)
	}
	return domain.ID(fmt.Sprintf("%s:%d:%d", rev, startMs, endMs)), startMs, endMs, nil
}

// maxCivilSteps bounds the containment iteration below. One step resolves
// containment in all reachable cases (the civil-date pre-shift plus at most
// one step around repeated wall clocks); 512 is a defensive ceiling that can
// only be exhausted by a broken time zone database.
const maxCivilSteps = 512

// civilWindow resolves day (stepDays=1) and week (stepDays=7) windows. Each
// civil period has exactly one window: [resolve(period start), resolve(next
// period start)) with the frozen fold/gap policy, so fall-back days are 25h
// and gap days 23h while the anchor wall clock of every boundary stays
// exact. Start of one window is by construction identical to the end of the
// previous one; the bounded loop only walks to the period containing now.
func civilWindow(now domain.Millis, rev string, date civilDate, a anchorSpec, loc *time.Location, stepDays int) (domain.ID, int64, int64, error) {
	nowMs := int64(now)
	for i := 0; i < maxCivilSteps; i++ {
		start, err := resolvePeriodBoundary(date, a, loc)
		if err != nil {
			return "", 0, 0, err
		}
		end, err := resolvePeriodBoundary(date.addDays(stepDays), a, loc)
		if err != nil {
			return "", 0, 0, err
		}
		startMs, endMs := start.UnixMilli(), end.UnixMilli()
		if nowMs < endMs {
			if startMs <= nowMs {
				return domain.ID(fmt.Sprintf("%s:%d:%d", rev, startMs, endMs)), startMs, endMs, nil
			}
			// now sits before this period's boundary: step back one period.
			date = date.addDays(-stepDays)
			continue
		}
		date = date.addDays(stepDays)
	}
	return "", 0, 0, fmt.Errorf("window: failed to resolve %s window for revision %q", pluralPeriod(stepDays), rev)
}

// resolvePeriodBoundary resolves the anchor wall clock on a civil date under
// the frozen fold/gap policy: the EARLIEST occurrence when the wall clock is
// ambiguous (first repeat), the transition instant when it does not exist.
func resolvePeriodBoundary(date civilDate, a anchorSpec, loc *time.Location) (time.Time, error) {
	stamp := civilStamp{date: date, clock: a.clock}
	if insts := civilInstants(stamp, loc); len(insts) > 0 {
		return insts[0], nil
	}
	return firstValidAfter(stamp, loc)
}

// monthWindow resolves calendar month windows whose boundaries are the anchor
// day-of-month (clamped to the month end) at the anchor clock.
func monthWindow(now domain.Millis, rev string, date civilDate, a anchorSpec, loc *time.Location) (domain.ID, int64, int64, error) {
	nowMs := int64(now)
	monthStart := func(y int, m time.Month) (time.Time, error) {
		return resolvePeriodBoundary(civilDate{y, m, clampDay(y, m, a.dayOfMonth)}, a, loc)
	}
	y, m := date.y, date.m
	start, err := monthStart(y, m)
	if err != nil {
		return "", 0, 0, err
	}
	for i := 0; i < maxCivilSteps; i++ {
		ny, nm := nextMonth(y, m)
		end, err := monthStart(ny, nm)
		if err != nil {
			return "", 0, 0, err
		}
		startMs, endMs := start.UnixMilli(), end.UnixMilli()
		if nowMs < endMs {
			if startMs <= nowMs {
				return domain.ID(fmt.Sprintf("%s:%d:%d", rev, startMs, endMs)), startMs, endMs, nil
			}
			y, m = prevMonth(y, m) // now sits before this month's anchor day
			start, err = monthStart(y, m)
			if err != nil {
				return "", 0, 0, err
			}
			continue
		}
		y, m = ny, nm
		start = end
	}
	return "", 0, 0, fmt.Errorf("window: failed to resolve month window for revision %q", rev)
}

func pluralPeriod(stepDays int) string {
	if stepDays == 7 {
		return "week"
	}
	return "day"
}

// civilDate is a pure calendar date; date arithmetic runs on UTC noon to be
// independent of any zone's DST rules.
type civilDate struct {
	y int
	m time.Month
	d int
}

func civilDateOf(t time.Time) civilDate { return civilDate{t.Year(), t.Month(), t.Day()} }

func (c civilDate) addDays(n int) civilDate {
	t := time.Date(c.y, c.m, c.d, 12, 0, 0, 0, time.UTC).AddDate(0, 0, n)
	return civilDate{t.Year(), t.Month(), t.Day()}
}

func (c civilDate) weekday() int {
	return int(time.Date(c.y, c.m, c.d, 12, 0, 0, 0, time.UTC).Weekday())
}

// clockMillis is now's wall-clock position within its day at millisecond
// precision, used for the civil-period pre-shift comparison.
func clockMillis(t time.Time) int {
	return (t.Hour()*3600+t.Minute()*60+t.Second())*1000 + t.Nanosecond()/1_000_000
}

// clampDay maps an anchor day-of-month onto month (y,m), clamping to the
// month end for short months (day 31 → Apr 30, day 29/30 → Feb 28/29).
func clampDay(y int, m time.Month, day int) int {
	last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		return last
	}
	if day < 1 {
		return 1
	}
	return day
}

func nextMonth(y int, m time.Month) (int, time.Month) {
	if m == time.December {
		return y + 1, time.January
	}
	return y, m + 1
}

func prevMonth(y int, m time.Month) (int, time.Month) {
	if m == time.January {
		return y - 1, time.December
	}
	return y, m - 1
}

func mod7(n int) int { return ((n % 7) + 7) % 7 }
