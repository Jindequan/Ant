package resources

import (
	"ant/server/internal/domain"
	"fmt"
	"testing"
)

type rule struct {
	kind, period, zone, anchor string
	duration                   int64
}

func normalize(r rule) (WindowRuleIdentity, error) {
	return NormalizeWindowRuleIdentity(domain.WindowRule{Kind: r.kind, Period: r.period, TimeZone: r.zone, AnchorISO: r.anchor, DurationMillis: r.duration})
}
func fixed(p, z, a string) rule { return rule{kind: "fixed", period: p, zone: z, anchor: a} }
func TestWindowRuleIdentity(t *testing.T) {
	count := 0
	check := func(name string, a, b rule, want bool) {
		x, e := normalize(a)
		y, f := normalize(b)
		if e != nil || f != nil || ((x == y) != want) {
			t.Fatalf("%s: %v %v %v %v", name, x, y, e, f)
		}
		count++
		fmt.Printf("PASS %s equal=%v identity=%+v / %+v\n", name, want, x, y)
	}
	check("rolling self", rule{kind: "rolling", duration: 60000}, rule{kind: "rolling", duration: 60000}, true)
	check("rolling duration", rule{kind: "rolling", duration: 60000}, rule{kind: "rolling", duration: 60001}, false)
	check("lifetime", rule{kind: "lifetime"}, rule{kind: "lifetime"}, true)
	for _, z := range []string{"", "UTC", "Etc/UTC", "Etc/GMT", "GMT"} {
		check("UTC alias "+z, fixed("day", z, ""), fixed("day", "UTC", ""), true)
	}
	for _, p := range []string{"minute", "hour", "day", "week", "month"} {
		check("default "+p, fixed(p, "Asia/Singapore", ""), fixed(p, "Asia/Singapore", "2026-06-01T00:00:00+08:00"), true)
	}
	check("minute ignores date hour minute", fixed("minute", "UTC", "2026-01-01T01:20:12.345Z"), fixed("minute", "UTC", "2025-05-15T21:40:12.345Z"), true)
	check("hour ignores date hour", fixed("hour", "UTC", "2026-01-01T01:20:12.345Z"), fixed("hour", "UTC", "2025-05-15T21:20:12.345Z"), true)
	check("day ignores date", fixed("day", "UTC", "2026-01-01T01:20:12.345Z"), fixed("day", "UTC", "2025-05-15T01:20:12.345Z"), true)
	check("millisecond difference", fixed("hour", "UTC", "2026-01-01T01:20:12.345Z"), fixed("hour", "UTC", "2026-01-01T01:20:12.346Z"), false)
	check("zone conversion crosses date", fixed("week", "Asia/Singapore", "2026-05-31T16:00:00Z"), fixed("week", "Asia/Singapore", ""), true)
	check("trailing zero precision", fixed("minute", "UTC", "2026-01-01T00:00:01.123000Z"), fixed("minute", "UTC", "2026-01-01T00:00:01.123Z"), true)
	check("week Thursday differs", fixed("week", "UTC", "2026-01-01T00:00:00Z"), fixed("week", "UTC", ""), false)
	check("month day15 differs", fixed("month", "UTC", "2026-01-15T00:00:00Z"), fixed("month", "UTC", ""), false)
	check("month day30 day31 distinct", fixed("month", "UTC", "2026-01-30T00:00:00Z"), fixed("month", "UTC", "2026-01-31T00:00:00Z"), false)
	check("Casablanca Lagos", fixed("day", "Africa/Casablanca", ""), fixed("day", "Africa/Lagos", ""), false)
	for n, r := range []rule{{kind: "unknown"}, {kind: "rolling"}, {kind: "rolling", duration: -1}, fixed("year", "UTC", ""), fixed("day", "No/SuchZone", ""), fixed("day", "Local", ""), fixed("day", "UTC", "malformed"), fixed("day", "UTC", "2026-01-01T00:00:00.000001Z")} {
		if _, e := normalize(r); e == nil {
			t.Fatal("invalid rule accepted")
		}
		count++
		fmt.Printf("PASS refusal %d %+v\n", n, r)
	}
	fmt.Printf("PASS total=%d; no WindowAt date sampling\n", count)
}
