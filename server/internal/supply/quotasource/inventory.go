package quotasource

import (
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/quota"
)

// Adapter facts are honest about what they are: quota readers report
// windows by name, not as domain.WindowRule, so every window is
// "unmodeled" and every meter carries adapter semantics. The fact source of
// phase 5 exposes only native (phase 4) templates, never these.
const (
	semanticsWindow  = "quota.adapter.window"
	semanticsBalance = "quota.adapter.balance"
	semanticsRev     = "v1"
	// quantumExp is one fixed precision for every adapter meter: a meter's
	// precision is part of its identity and must not depend on the value.
	quantumExp = -6
)

// reportedIdentity is a's identity only where the provider reports one.
// Z.ai's identity is inferred from coincident reset times and Claude's is a
// constant: both still group cards on the page, but the store never merges
// accounts on them (design 11 §2.6).
func reportedIdentity(a quota.Account) string {
	switch a.Provider {
	case "aliyun", "copilot": // Bailian instance / account owner, GitHub login
		return a.Identity
	}
	return ""
}

// inventory is what a good read says the account holds.
func inventory(a quota.Account) *observed.Inventory {
	inv := &observed.Inventory{Identity: reportedIdentity(a), DisplayName: firstOf(a.User, a.Label)}
	base := "quota/" + a.Provider + "/" + a.Mode
	main := observed.PackageReading{TemplateRef: base, Kind: packageKind(a), Basis: basis(a), Plan: a.Plan,
		RenewsAt: millis(a.RenewsAt), EndsAt: millis(a.EndsAt), AutoRenew: a.AutoRenew}
	main.Resources = meters(a.Windows, a.Balances)
	inv.Packages = append(inv.Packages, main)
	for _, g := range a.Groups {
		p := observed.PackageReading{TemplateRef: base + "/" + slug(g.Title), Kind: main.Kind, Basis: main.Basis, Plan: g.Title}
		p.Resources = meters(g.Windows, g.Balances)
		inv.Packages = append(inv.Packages, p)
	}
	return inv
}

func basis(a quota.Account) string {
	if a.Live {
		return "observed"
	}
	return "declared" // shown from the mode's rule and the user's setup, not read
}

func packageKind(a quota.Account) string {
	switch a.Kind {
	case quota.KindSubscription:
		return "subscription"
	case quota.KindFree:
		return "free_grant"
	}
	if len(a.Balances) > 0 {
		return "prepaid"
	}
	return "payg"
}

func meters(windows []quota.Window, balances []quota.Balance) []observed.ResourceReading {
	var out []observed.ResourceReading
	for _, w := range windows {
		out = append(out, windowMeter(w))
	}
	for _, b := range balances {
		out = append(out, balanceMeter(b))
	}
	return out
}

func windowMeter(w quota.Window) observed.ResourceReading {
	r := observed.ResourceReading{TemplateRef: "window/" + slug(w.Name), MetricID: domain.ID(slug(w.Name)), Unit: firstOf(w.Unit, "count"),
		QuantumExp: quantumExp, SemanticsRef: semanticsWindow, SemanticsRevision: semanticsRev,
		WindowJSON: mustJSON(struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		}{"unmodeled", w.Name}),
		ScopeJSON: scope(w.Model), ResetBasis: "none", WindowEnd: millis(w.ResetsAt)}
	if w.ResetsAt != nil {
		r.ResetBasis = "reported"
		if w.ResetEstimated {
			r.ResetBasis = "computed"
		}
	}
	if w.Used == nil && w.Limit == nil && w.Left == nil && w.UsedPercent != nil {
		r.Unit = "percent"
		r.Used, r.Limit, r.Remaining = units(w.UsedPercent), units(ptr(100.0)), units(ptr(100-*w.UsedPercent))
	} else {
		r.Used, r.Limit, r.Remaining = units(w.Used), units(w.Limit), units(w.Left)
	}
	r.Evidence = mustJSON(struct {
		Name           string   `json:"name"`
		UsedPercent    *float64 `json:"usedPercent,omitempty"`
		Media          string   `json:"media,omitempty"`
		Expires        bool     `json:"expires,omitempty"`
		ResetEstimated bool     `json:"resetEstimated,omitempty"`
		Aside          bool     `json:"aside,omitempty"`
	}{w.Name, w.UsedPercent, w.Media, w.Expires, w.ResetEstimated, w.Aside})
	return r
}

func balanceMeter(b quota.Balance) observed.ResourceReading {
	name := firstOf(b.Label, b.Currency)
	r := observed.ResourceReading{TemplateRef: "balance/" + slug(name), MetricID: "balance", Unit: b.Currency, Currency: b.Currency,
		QuantumExp: quantumExp, SemanticsRef: semanticsBalance, SemanticsRevision: semanticsRev,
		WindowJSON: []byte(`{"kind":"lifetime"}`), ScopeJSON: scope(""), ResetBasis: "none"}
	r.Remaining = decimalUnits(b.Amount)
	r.Evidence = mustJSON(struct {
		Label    string `json:"label,omitempty"`
		Amount   string `json:"amount"`
		Granted  string `json:"granted,omitempty"`
		ToppedUp string `json:"toppedUp,omitempty"`
	}{b.Label, b.Amount, b.Granted, b.ToppedUp})
	return r
}

func scope(model string) []byte {
	if model == "" {
		return []byte(`{"kind":"all"}`)
	}
	return mustJSON(struct {
		Kind  string `json:"kind"`
		Model string `json:"model"`
	}{"model", model})
}

// units is f at quantumExp, or nil when unknown or out of range.
func units(f *float64) *domain.Units {
	if f == nil || math.IsNaN(*f) || math.IsInf(*f, 0) || math.Abs(*f) > 9e12 {
		return nil
	}
	u := domain.Units(math.Round(*f * 1e6))
	return &u
}

// decimalUnits converts an exact decimal string ("23.40", "-1.5") to
// quantumExp without going through floating point; extra digits round half
// away from zero. Malformed input gives nil.
func decimalUnits(s string) *domain.Units {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" || len(whole) > 12 || !digits(whole) || !digits(frac) {
		return nil
	}
	round := len(frac) > 6 && frac[6] >= '5'
	frac = (frac + "000000")[:6]
	var v int64
	for _, c := range whole + frac {
		v = v*10 + int64(c-'0')
	}
	if round {
		v++
	}
	if neg {
		v = -v
	}
	u := domain.Units(v)
	return &u
}

func digits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func millis(t *time.Time) *domain.Millis {
	if t == nil {
		return nil
	}
	m := domain.Millis(t.UnixMilli())
	return &m
}

func firstOf(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func ptr[T any](v T) *T { return &v }
