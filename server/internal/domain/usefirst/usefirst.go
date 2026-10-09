// Package usefirst is Ant's one rule for what will be lost unless it's used
// soon. The router, the notifications, the page and the panel all ask it;
// none keeps a rule of its own (design 15).
package usefirst

import "time"

const (
	// ResetWithin and MinLeft: a window renewing this soon with at least
	// this much left loses what's left.
	ResetWithin = 24 * time.Hour
	MinLeft     = 30.0 // percent
	// GrantWithin: a grant ending this soon, with anything left, is worth
	// using first.
	GrantWithin = 14 * 24 * time.Hour
)

// Why says what would be lost.
type Why string

const (
	No          Why = ""
	BeforeReset Why = "reset" // a window renews and what's left goes
	BeforeEnd   Why = "end"   // a grant ends and what's left goes
)

// Allowance is what the rule needs to know about one window or grant.
type Allowance struct {
	PercentLeft *float64  // 0–100; nil when unknown
	At          time.Time // when it resets or ends; zero when unknown
	Ends        bool      // At ends it instead of renewing it
	Aside       bool      // using it up doesn't stop the main use
	RunsOut     bool      // at the recent pace it's used up before At
}

// Of says whether a should be used first, and why.
func Of(a Allowance, now time.Time) Why {
	if a.Aside || a.RunsOut || a.At.IsZero() || !a.At.After(now) {
		return No
	}
	until := a.At.Sub(now)
	if a.Ends {
		if until <= GrantWithin && (a.PercentLeft == nil || *a.PercentLeft > 0) {
			return BeforeEnd
		}
		return No
	}
	if until <= ResetWithin && a.PercentLeft != nil && *a.PercentLeft >= MinLeft {
		return BeforeReset
	}
	return No
}

// Today says whether a is lost within a day: the router's question, for
// windows and grants alike, so a grant ending in ten days doesn't outrank
// a plan window resetting tonight.
func Today(a Allowance, now time.Time) bool {
	if a.Aside || a.RunsOut || a.At.IsZero() || !a.At.After(now) || a.PercentLeft == nil {
		return false
	}
	return a.At.Sub(now) <= ResetWithin && *a.PercentLeft >= MinLeft
}

// PercentLeft is what's left of an allowance, 0–100, from what a provider
// reports: the percent used, or what's left or used of a limit.
func PercentLeft(usedPercent, left, used, limit *float64) *float64 {
	var p float64
	switch {
	case usedPercent != nil:
		p = 100 - *usedPercent
	case left != nil && limit != nil && *limit > 0:
		p = *left / *limit * 100
	case used != nil && limit != nil && *limit > 0:
		p = (*limit - *used) / *limit * 100
	default:
		return nil
	}
	p = min(100, max(0, p))
	return &p
}
