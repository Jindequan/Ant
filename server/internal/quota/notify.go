package quota

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Alert is one thing worth telling the user now. Key identifies the event,
// so each is sent once: it names the account, the row and the period (a
// window's reset, a renewal date), and a new period is a new alert.
type Alert struct {
	Key, Title, Body string
}

const (
	loseWithin     = 2 * time.Hour  // an allowance resetting this soon with this much left
	loseLeft       = 30.0           // percent
	expireWithin   = 24 * time.Hour // a grant ending this soon with anything left
	runOutWithin   = 6 * time.Hour  // a window running dry this soon, before it resets
	balanceWithin  = 72 * time.Hour // a balance lasting less than this
	renewWithin    = 24 * time.Hour
	endingWithin   = 72 * time.Hour // a plan ending, auto-renew off
	notifyInterval = time.Minute
)

// alerts says what deserves a notification now.
func alerts(accounts []Account, insights map[string][]Insight, prices map[string]Price, now time.Time) []Alert {
	var out []Alert
	for _, a := range accounts {
		name := a.Label
		if name == "" {
			name = a.Name
		}
		if a.Status == StatusSignedOut {
			out = append(out, Alert{Key: a.ID + "|signed-out", Title: name + " is signed out", Body: firstLine(a.Message, "Sign in again to keep reading it.")})
			continue
		}
		if a.Status != StatusOK {
			continue
		}
		ins := insightsFor(a, insights)
		for _, w := range allWindowsOf(a) {
			if w.Aside || w.ResetsAt == nil || !w.ResetsAt.After(now) {
				continue
			}
			until := w.ResetsAt.Sub(now)
			left := percentLeft(w)
			in := findWindowInsight(ins, w)
			runsOut := in != nil && in.Outlook == "runs_out" && in.At != nil
			period := w.ResetsAt.UTC().Format(time.RFC3339)
			switch {
			case runsOut && in.At.Sub(now) <= runOutWithin:
				out = append(out, Alert{Key: a.ID + "|" + w.Name + "|" + w.Model + "|runs-out|" + period, Title: name + " · " + w.Name + " is running out",
					Body: fmt.Sprintf("At this pace it runs out in %s, %s before it %s.", span(in.At.Sub(now)), span(w.ResetsAt.Sub(*in.At)), verb(w))})
			case runsOut:
				// used up before it resets: nothing will be lost
			case w.Expires && until <= expireWithin && left != nil && *left > 0:
				out = append(out, Alert{Key: a.ID + "|" + w.Name + "|" + w.Model + "|expires|" + period, Title: name + " · " + w.Name + " expires soon",
					Body: fmt.Sprintf("%s left, and it ends in %s. Use it before then.", leftText(w, *left), span(until))})
			case !w.Expires && until <= loseWithin && left != nil && *left >= loseLeft:
				out = append(out, Alert{Key: a.ID + "|" + w.Name + "|" + w.Model + "|resets|" + period, Title: name + " · " + w.Name + " resets soon",
					Body: fmt.Sprintf("%s left, and it resets in %s. Use it or lose it.", leftText(w, *left), span(until))})
			}
		}
		for _, in := range ins {
			if in.Currency == "" || in.Outlook != "runs_out" || in.At == nil || in.At.Sub(now) > balanceWithin {
				continue
			}
			out = append(out, Alert{Key: a.ID + "|" + in.Currency + "|" + in.Label + "|low|" + in.At.UTC().Format("2006-01-02"),
				Title: name + " balance is running low", Body: fmt.Sprintf("At this pace it lasts about %s.", span(in.At.Sub(now)))})
		}
		switch {
		case a.RenewsAt != nil && a.RenewsAt.After(now) && a.RenewsAt.Sub(now) <= renewWithin:
			cost := ""
			if p, ok := prices[a.ID]; ok {
				cost = fmt.Sprintf(" for %s %s", p.Amount, p.Currency)
			}
			out = append(out, Alert{Key: a.ID + "|renews|" + a.RenewsAt.UTC().Format(time.RFC3339), Title: name + " renews soon",
				Body: fmt.Sprintf("%s renews in %s%s.", firstLine(a.Plan, name), span(a.RenewsAt.Sub(now)), cost)})
		case a.RenewsAt == nil && a.EndsAt != nil && a.AutoRenew != nil && !*a.AutoRenew && a.EndsAt.After(now) && a.EndsAt.Sub(now) <= endingWithin:
			out = append(out, Alert{Key: a.ID + "|ends|" + a.EndsAt.UTC().Format(time.RFC3339), Title: name + " ends soon",
				Body: fmt.Sprintf("%s ends in %s, and auto-renew is off.", firstLine(a.Plan, name), span(a.EndsAt.Sub(now)))})
		}
	}
	return out
}

func allWindowsOf(a Account) []Window {
	ws := append([]Window(nil), a.Windows...)
	for _, g := range a.Groups {
		ws = append(ws, g.Windows...)
	}
	return ws
}

func insightsFor(a Account, insights map[string][]Insight) []Insight {
	out := append([]Insight(nil), insights[a.ID]...)
	for _, k := range a.Keys {
		if k != a.ID {
			out = append(out, insights[k]...)
		}
	}
	return out
}

func findWindowInsight(ins []Insight, w Window) *Insight {
	for i := range ins {
		if ins[i].Window == w.Name && ins[i].Model == w.Model {
			return &ins[i]
		}
	}
	return nil
}

// percentLeft is how much of a window is left, 0–100, or nil when unknown.
func percentLeft(w Window) *float64 {
	var p float64
	switch {
	case w.UsedPercent != nil:
		p = 100 - *w.UsedPercent
	case w.Left != nil && w.Limit != nil && *w.Limit > 0:
		p = *w.Left / *w.Limit * 100
	case w.Used != nil && w.Limit != nil && *w.Limit > 0:
		p = (*w.Limit - *w.Used) / *w.Limit * 100
	default:
		return nil
	}
	p = math.Max(0, math.Min(100, p))
	return &p
}

func leftText(w Window, pct float64) string {
	if w.Left != nil {
		unit := ""
		if w.Unit != "" {
			unit = " " + w.Unit
		}
		return strings.TrimSuffix(fmt.Sprintf("%g", math.Round(*w.Left*100)/100), ".0") + unit
	}
	return fmt.Sprintf("%.0f%%", pct)
}

func verb(w Window) string {
	if w.Expires {
		return "ends"
	}
	return "resets"
}

// span is a duration the way a person says it: "3h 20m", "2 days".
func span(d time.Duration) string {
	m := int(math.Round(d.Minutes()))
	switch {
	case m < 1:
		return "under a minute"
	case m < 60:
		return fmt.Sprintf("%dm", m)
	case m < 24*60:
		if m%60 == 0 {
			return fmt.Sprintf("%dh", m/60)
		}
		return fmt.Sprintf("%dh %dm", m/60, m%60)
	case m < 48*60:
		return "1 day"
	}
	return fmt.Sprintf("%d days", m/(24*60))
}

func firstLine(s, fallback string) string {
	if s = strings.TrimSpace(s); s == "" {
		return fallback
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Notifier shows a notification on this computer.
type Notifier interface {
	Notify(title, body string) error
}

// MacNotifier shows a macOS notification. The texts go to osascript as
// arguments, never as script, so no text can run as AppleScript.
type MacNotifier struct{}

func (MacNotifier) Notify(title, body string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "osascript",
		"-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run",
		title, body).Run()
}
