package usefirst

import (
	"testing"
	"time"
)

func pct(p float64) *float64 { return &p }

func TestTheOneRule(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	h := func(n float64) time.Time { return now.Add(time.Duration(n * float64(time.Hour))) }
	for _, c := range []struct {
		name  string
		a     Allowance
		want  Why
		today bool
	}{
		{"resets tonight, half left", Allowance{PercentLeft: pct(50), At: h(6)}, BeforeReset, true},
		{"resets tonight, little left", Allowance{PercentLeft: pct(10), At: h(6)}, No, false},
		{"resets in two days", Allowance{PercentLeft: pct(90), At: h(48)}, No, false},
		{"resets tonight, used up first", Allowance{PercentLeft: pct(80), At: h(6), RunsOut: true}, No, false},
		{"an aside window", Allowance{PercentLeft: pct(80), At: h(6), Aside: true}, No, false},
		{"unknown left", Allowance{At: h(6)}, No, false},
		{"grant ends in ten days", Allowance{PercentLeft: pct(5), At: h(240), Ends: true}, BeforeEnd, false},
		{"grant ends today", Allowance{PercentLeft: pct(60), At: h(3), Ends: true}, BeforeEnd, true},
		{"grant used up", Allowance{PercentLeft: pct(0), At: h(24), Ends: true}, No, false},
		{"grant, amount unknown", Allowance{At: h(24), Ends: true}, BeforeEnd, false},
		{"grant ends in a month", Allowance{PercentLeft: pct(90), At: h(720), Ends: true}, No, false},
		{"already past", Allowance{PercentLeft: pct(90), At: h(-1)}, No, false},
	} {
		if got := Of(c.a, now); got != c.want {
			t.Errorf("%s: Of = %q, want %q", c.name, got, c.want)
		}
		if got := Today(c.a, now); got != c.today {
			t.Errorf("%s: Today = %v, want %v", c.name, got, c.today)
		}
	}
}

func TestPercentLeft(t *testing.T) {
	for _, c := range []struct {
		name                     string
		usedPct, left, used, lim *float64
		want                     *float64
	}{
		{"from percent used", pct(30), nil, nil, nil, pct(70)},
		{"from left of limit", nil, pct(25), nil, pct(100), pct(25)},
		{"from used of limit", nil, nil, pct(40), pct(50), pct(20)},
		{"over the limit", nil, nil, pct(80), pct(50), pct(0)},
		{"no limit", nil, pct(5), nil, nil, nil},
	} {
		got := PercentLeft(c.usedPct, c.left, c.used, c.lim)
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
