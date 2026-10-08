package resources

// Wall-clock resolution machinery for the frozen window semantics (D-B5-v2,
// docs/evidence/T1-REWORK-2/S1-freeze-decisions.md): every boundary is
// resolved from the local wall clock of the rule zone by explicit candidate
// enumeration — all instants whose wall clock equals the target civil time
// are collected and the frozen policy picks one (earliest for a fold; the
// transition instant for a gap). Go's time.Date is used only to seed probe
// instants; its normalization choice is never trusted, because the Berlin
// 2026-10-25 fold counterexample (REVIEW-T1-REWORK-CODEX B5) showed it picks
// the FIRST occurrence in New York but the SECOND in Berlin.

import (
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	// foldScanWindow is the candidate-enumeration radius frozen by D-B5-v2:
	// every zone offset in effect within ±48h of the probe is a candidate
	// for realizing a wall-clock stamp. A civil time occurs at most once per
	// zone offset, so the result is bounded at 0 (gap), 1 (normal) or 2
	// (fold) instants for every real zone.
	foldScanWindow = 48 * time.Hour

	// maxScanExpansion and maxZoneSegments are defensive ceilings against a
	// broken time zone database; neither is reachable with tzdata.
	maxScanExpansion = 8
	maxZoneSegments  = 16
)

// wallClock is a time-of-day on the local wall clock at millisecond
// precision — exactly the precision anchors support (D-B5-v2).
type wallClock struct {
	hour, minute, second, ms int
}

func (w wallClock) nanos() int64 {
	return ((int64(w.hour)*3600 + int64(w.minute)*60 + int64(w.second)) * int64(time.Second)) +
		int64(w.ms)*int64(time.Millisecond)
}

// civilStamp is a civil date plus wall-clock time: a position on the local
// wall clock that may occur zero times (spring gap), once, or twice (fall
// fold) as a real instant in the zone.
type civilStamp struct {
	date  civilDate
	clock wallClock
}

func civilStampOf(t time.Time) civilStamp {
	return civilStamp{
		date:  civilDateOf(t),
		clock: wallClock{t.Hour(), t.Minute(), t.Second(), t.Nanosecond() / 1_000_000},
	}
}

// asTime seeds a probe instant near the stamp. Its normalization at folds or
// gaps is unspecified — that is exactly the time.Date choice this code must
// not depend on; callers only use it as a search center.
func (s civilStamp) asTime(loc *time.Location) time.Time {
	return time.Date(s.date.y, s.date.m, s.date.d, s.clock.hour, s.clock.minute, s.clock.second, s.clock.ms*1_000_000, loc)
}

// zoneOffsetsAround returns every UTC offset (seconds east) that loc assumes
// at any instant within ±window of center, walking the zone period by period
// via ZoneBounds. Zone periods are half-open [start, end): at exactly `end`
// the next offset is already in effect.
func zoneOffsetsAround(center time.Time, loc *time.Location, window time.Duration) []int {
	lo, hi := center.Add(-window), center.Add(window)
	seen := make(map[int]bool, 4)
	var offs []int
	add := func(t time.Time) {
		if _, off := t.In(loc).Zone(); !seen[off] {
			seen[off] = true
			offs = append(offs, off)
		}
	}
	t := lo
	for i := 0; i < maxZoneSegments; i++ {
		add(t)
		if !t.Before(hi) {
			return offs
		}
		_, end := t.In(loc).ZoneBounds()
		if end.IsZero() || !end.Before(hi) {
			break
		}
		t = end
	}
	add(hi)
	return offs
}

// civilInstants returns every instant at which the local wall clock in loc
// equals the stamp, ascending: zero entries when the stamp falls in a
// spring-forward gap, one normally, two in a fall-back fold. Candidate
// enumeration per D-B5-v2 (±48h 扫描全部匹配实例): the stamp is constructed
// under each zone offset active within ±48h of a probe and kept only when
// that offset is really in effect at the constructed instant — time.Date's
// fold choice is never consulted.
func civilInstants(s civilStamp, loc *time.Location) []time.Time {
	probe := s.asTime(loc)
	var out []time.Time
	for _, off := range zoneOffsetsAround(probe, loc, foldScanWindow) {
		t := time.Date(s.date.y, s.date.m, s.date.d, s.clock.hour, s.clock.minute, s.clock.second, s.clock.ms*1_000_000, time.FixedZone("", off))
		if _, got := t.In(loc).Zone(); got == off {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// civilAtOrAfter reports whether the local wall clock of l (already
// converted into the rule zone) is at or after the stamp.
func civilAtOrAfter(l time.Time, s civilStamp) bool {
	y, m, d := l.Date()
	switch {
	case y != s.date.y:
		return y > s.date.y
	case m != s.date.m:
		return m > s.date.m
	case d != s.date.d:
		return d > s.date.d
	}
	got := (int64(l.Hour())*3600+int64(l.Minute())*60+int64(l.Second()))*int64(time.Second) + int64(l.Nanosecond())
	return got >= s.clock.nanos()
}

// firstValidAfter returns the first instant whose local wall clock in loc is
// at or after the stamp. For a stamp inside a spring-forward gap this is
// exactly the transition instant — the frozen gap policy ("不存在取跳变后
// 第一个有效时刻"); IANA transitions sit on whole seconds, so the result is
// second-exact and window lengths stay exact.
func firstValidAfter(s civilStamp, loc *time.Location) (time.Time, error) {
	geq := func(t time.Time) bool { return civilAtOrAfter(t.In(loc), s) }
	probe := s.asTime(loc)
	lo, hi := probe.Add(-foldScanWindow), probe.Add(foldScanWindow)
	for i := 0; i < maxScanExpansion && geq(lo); i++ {
		lo = lo.Add(-foldScanWindow)
	}
	for i := 0; i < maxScanExpansion && !geq(hi); i++ {
		hi = hi.Add(foldScanWindow)
	}
	if geq(lo) || !geq(hi) {
		return time.Time{}, fmt.Errorf("window: wall clock %v never occurs in zone %s (broken tz database)", s, loc)
	}
	for hi.Sub(lo) > time.Nanosecond {
		mid := lo.Add(hi.Sub(lo) / 2)
		if geq(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi, nil
}

// --- minute/hour phase grids (exact closed-form boundary enumeration) ---

// maxSegmentSteps bounds the zone-segment walks below. One step past now's
// own segment is reachable only when a transition falls inside the current
// period; the remainder is a defensive ceiling against broken tzdata.
const maxSegmentSteps = 8

// zoneSegment is the half-open real-time interval [lo, hi) (milliseconds) on
// which loc assumes one constant UTC offset. Zero ZoneBounds (no known
// further transition) map to ±epoch sentinels.
type zoneSegment struct{ lo, hi int64 }

func zoneSegmentBounds(t time.Time) zoneSegment {
	s, e := t.ZoneBounds()
	lo, hi := int64(math.MinInt64)+1, int64(math.MaxInt64)
	if !s.IsZero() {
		lo = s.UnixMilli()
	}
	if !e.IsZero() {
		hi = e.UnixMilli()
	}
	return zoneSegment{lo, hi}
}

func (s zoneSegment) prev(loc *time.Location) (zoneSegment, bool) {
	if s.lo <= math.MinInt64+1 {
		return zoneSegment{}, false
	}
	// Transitions sit on whole seconds, so lo-1ms is inside the previous
	// segment; at exactly a bound the next segment is in effect.
	return zoneSegmentBounds(time.UnixMilli(s.lo - 1).In(loc)), true
}

func (s zoneSegment) next(loc *time.Location) (zoneSegment, bool) {
	if s.hi >= math.MaxInt64 {
		return zoneSegment{}, false
	}
	return zoneSegmentBounds(time.UnixMilli(s.hi).In(loc)), true
}

func (s zoneSegment) offsetMs(loc *time.Location) int64 {
	_, off := time.UnixMilli(s.lo).In(loc).Zone() // lo is inside [lo, hi)
	return int64(off) * 1000
}

// phaseBoundaryRange returns the consecutive boundaries of the wall-clock
// phase grid around nowMs: start is the latest grid point ≤ nowMs, end the
// earliest grid point > nowMs. Grid points are EVERY instant whose local
// wall clock shows the anchor phase — i.e. all candidate realizations of the
// phase, as the freeze's ±48h scan would collect them: a fall-back keeps
// both occurrences (the repeated wall-clock hour is two real windows), a
// spring gap simply has none (windows spanning a gap end/start at the
// surrounding grid points). On each constant-offset segment the phase
// matches exactly the instants of a real-time arithmetic progression
// (t + offset ≡ phase mod period), so the enumeration is closed-form per
// segment, exact, and provably independent of time.Date's fold choice.
func phaseBoundaryRange(nowMs int64, loc *time.Location, periodMs, phaseMs int64) (int64, int64, error) {
	seg := zoneSegmentBounds(time.UnixMilli(nowMs).In(loc))

	startMs, s, ok := lastGridAtOrBefore(seg, nowMs, periodMs, phaseMs, loc)
	if !ok {
		return 0, 0, fmt.Errorf("window: no phase boundary at or before %d in zone %s (broken tz database)", nowMs, loc)
	}
	endMs, ok := firstGridAfter(s, nowMs, periodMs, phaseMs, loc)
	if !ok {
		return 0, 0, fmt.Errorf("window: no phase boundary after %d in zone %s (broken tz database)", nowMs, loc)
	}
	return startMs, endMs, nil
}

// gridTarget is the grid residue (mod periodMs) of real-time instants on
// segment s: t ≡ phase − offset (mod period).
func gridTarget(s zoneSegment, phaseMs, periodMs int64, loc *time.Location) int64 {
	return modI64(phaseMs-s.offsetMs(loc), periodMs)
}

// lastGridAtOrBefore returns the latest grid point ≤ cap on seg or, if that
// segment holds none, on the closest preceding segment that does; the
// returned segment is the one that produced the point.
func lastGridAtOrBefore(seg zoneSegment, cap, periodMs, phaseMs int64, loc *time.Location) (int64, zoneSegment, bool) {
	s := seg
	for i := 0; i < maxSegmentSteps; i++ {
		limit := cap
		if s.hi-1 < limit {
			limit = s.hi - 1
		}
		g := limit - modI64(limit-gridTarget(s, phaseMs, periodMs, loc), periodMs)
		if g >= s.lo {
			return g, s, true
		}
		p, ok := s.prev(loc)
		if !ok {
			return 0, s, false
		}
		s = p
	}
	return 0, s, false
}

// firstGridAfter returns the earliest grid point > afterMs on seg or on the
// closest following segment that holds one.
func firstGridAfter(seg zoneSegment, afterMs, periodMs, phaseMs int64, loc *time.Location) (int64, bool) {
	s := seg
	for i := 0; i < maxSegmentSteps; i++ {
		low := s.lo
		if low < afterMs+1 {
			low = afterMs + 1
		}
		g := low + modI64(gridTarget(s, phaseMs, periodMs, loc)-low, periodMs)
		if g < s.hi {
			return g, true
		}
		n, ok := s.next(loc)
		if !ok {
			return 0, false
		}
		s = n
	}
	return 0, false
}

func modI64(n, m int64) int64 {
	r := n % m
	if r < 0 {
		r += m
	}
	return r
}
