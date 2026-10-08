package quota

import (
	"context"
	"strconv"
	"time"
)

// Design tools whose AI allowance their own apps show but no API reports:
// Figma's AI credits (Figma Make, Figma's AI features) and Google Stitch's
// daily credits. Ant shows each allowance's size and when it renews, from
// the published rule and what the user sets up, and says it can't read
// how much is used.

// nextMonthly is the next renewal on the day of month of since (a billing
// date), clamped to the month's last day.
func nextMonthly(since, now time.Time) time.Time {
	day := since.Day()
	y, m, _ := now.Date()
	for i := 0; i < 14; i++ {
		last := time.Date(y, m+1, 0, 0, 0, 0, 0, now.Location()).Day()
		t := time.Date(y, m, min(day, last), 0, 0, 0, 0, now.Location())
		if t.After(now) {
			return t
		}
		m++
	}
	return now
}

func numberValue(s string, def float64) float64 {
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
		return f
	}
	return def
}

// --- Figma: AI credits per seat, monthly; Starter also has a daily cap ---

func figmaProvider() Provider {
	return Provider{ID: "figma", Name: "Figma", Link: "https://www.figma.com/settings", Media: []string{"design"}, Modes: []Mode{{
		ID: "ai-credits", Name: "AI credits", Kind: KindFree, Live: false,
		Rule: "AI credits per seat each month, renewing on the plan's billing date; they don't roll over. On Starter (and View seats) at most 150 a day. Figma Make spends them.",
		Fields: []Field{
			{ID: "plan", Label: "Plan", Type: "select", Options: []Option{{"starter", "Starter (free)"}, {"professional", "Professional"}, {"organization", "Organization"}, {"enterprise", "Enterprise"}}},
			{ID: "credits", Label: "Monthly credits", Type: "number", Optional: true, Placeholder: "500",
				Help: "Your seat's monthly AI credits, as Figma › Settings › AI credits shows them. Starter has 500."},
			{ID: "since", Label: "Billing date", Type: "date", Optional: true, Help: "Any day your plan renewed; credits renew on that day each month."},
		},
		Connect: "Figma shows AI credits only in its own app, so Ant follows the rule: tell it your plan.",
		Read:    readFigma,
	}}}
}

func readFigma(_ context.Context, l Local, c Connection) Account {
	p := figmaProvider()
	now := l.Now()
	a := base(p, p.Modes[0], c, now)
	plan := c.Values["plan"]
	a.Plan = map[string]string{"starter": "Starter", "professional": "Professional", "organization": "Organization", "enterprise": "Enterprise"}[plan]
	if plan != "starter" && plan != "" {
		a.Kind = KindSubscription
	}
	month := Window{Name: "AI credits · month", Limit: ptr(numberValue(c.Values["credits"], 500)), Unit: "credits", ResetEstimated: true}
	if since, ok := dateValue(c.Values["since"]); ok {
		month.ResetsAt = ptr(nextMonthly(since, now))
		if a.Kind == KindSubscription {
			a.RenewsAt = month.ResetsAt
		}
	} else {
		y, m, _ := now.Date()
		month.ResetsAt = ptr(time.Date(y, m+1, 1, 0, 0, 0, 0, now.Location()))
	}
	a.Windows = []Window{month}
	if plan == "starter" || plan == "" {
		y, m, d := now.Date()
		a.Windows = append(a.Windows, Window{Name: "AI credits · today", Limit: ptr(150.0), Unit: "credits",
			ResetsAt: ptr(time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())), ResetEstimated: true})
	}
	a.Status = StatusOK
	a.Message = "Figma doesn't report AI credit use outside its app; these are the allowances and when they renew."
	return a
}

// --- Google Stitch: daily design credits, renewing at 00:00 UTC ---

func stitchProvider() Provider {
	return Provider{ID: "stitch", Name: "Google Stitch", Link: "https://stitch.withgoogle.com/", Media: []string{"design"}, Modes: []Mode{{
		ID: "daily", Name: "Daily credits", Kind: KindFree, Live: false,
		Rule: "Free, with daily credits for designs and a smaller daily allowance of redesigns, renewing at 00:00 UTC; more can't be bought.",
		Fields: []Field{
			{ID: "credits", Label: "Daily design credits", Type: "number", Optional: true, Placeholder: "400", Help: "As Stitch's usage panel shows them (400 when Ant last checked)."},
			{ID: "redesigns", Label: "Daily redesigns", Type: "number", Optional: true, Placeholder: "15"},
		},
		Connect: "Stitch shows its credits only in its own app, behind Google sign-in, so Ant follows the rule.",
		Read:    readStitch,
	}}}
}

func readStitch(_ context.Context, l Local, c Connection) Account {
	p := stitchProvider()
	now := l.Now()
	a := base(p, p.Modes[0], c, now)
	midnight := ptr(utcMidnight(now))
	a.Windows = []Window{
		{Name: "Design credits · today", Limit: ptr(numberValue(c.Values["credits"], 400)), Unit: "credits", ResetsAt: midnight},
		{Name: "Redesigns · today", Limit: ptr(numberValue(c.Values["redesigns"], 15)), Unit: "redesigns", ResetsAt: midnight},
	}
	a.Status = StatusOK
	a.Message = "Stitch doesn't report use outside its app; these are the daily allowances and when they renew."
	return a
}
