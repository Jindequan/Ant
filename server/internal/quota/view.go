package quota

import (
	"context"
	"math/big"
	"slices"
	"sort"
	"strings"
	"time"

	"ant/server/internal/domain/usefirst"
)

// View is what the page and the panel draw: the latest snapshot with what
// it means already said (design 15), so neither keeps a rule of its own.
type View struct {
	Snapshot
	Glance Glance `json:"glance"`
}

// Glance is the top of the page and of the panel.
type Glance struct {
	// UseFirst: what would be lost unless used soon, soonest first.
	UseFirst []UseFirst `json:"useFirst"`
	// NeedsYou: the accounts that can't be read until the user acts.
	NeedsYou []string `json:"needsYou"`
	// Held: money held, one amount per currency (credits aren't money).
	Held []Balance `json:"held"`
	// Counts of readable accounts by how they're paid for.
	Subscriptions int `json:"subscriptions"`
	Free          int `json:"free"`
	APIs          int `json:"apis"`
	// NextRenewal: the subscription that renews or ends soonest.
	NextRenewal *Renewal `json:"nextRenewal,omitempty"`
}

// UseFirst is one window to use before it's lost.
type UseFirst struct {
	Account string       `json:"account"` // the card's id
	Window  Window       `json:"window"`
	Why     usefirst.Why `json:"why"`
	At      time.Time    `json:"at"`
}

// Renewal is when a subscription renews, or ends without renewing.
type Renewal struct {
	Account string    `json:"account"`
	At      time.Time `json:"at"`
	Ends    bool      `json:"ends,omitempty"`
}

// View is the snapshot with every window's percent left and use-first
// reason, and the glance.
func (s *Service) View(ctx context.Context) View {
	snap := s.Snapshot()
	return view(snap, s.insightsOf(ctx, snap), s.local.Now())
}

// insightsOf is the history's outlooks for snap, read again only when the
// accounts were (reading them walks every account's readings).
func (s *Service) insightsOf(ctx context.Context, snap Snapshot) map[string][]Insight {
	if s.demo {
		return demoInsights(snap.Accounts, s.local.Now())
	}
	if s.insights == nil {
		return nil
	}
	s.mu.Lock()
	if s.insightsAt.Equal(snap.RefreshedAt) && s.insightsCache != nil {
		defer s.mu.Unlock()
		return s.insightsCache
	}
	s.mu.Unlock()
	got, err := s.insights.Insights(ctx)
	if err != nil {
		return nil // the view stands without history
	}
	s.mu.Lock()
	s.insightsAt, s.insightsCache = snap.RefreshedAt, got
	s.mu.Unlock()
	return got
}

// view says what snap means at now. It copies what it marks: snap's
// accounts are the service's.
func view(snap Snapshot, insights map[string][]Insight, now time.Time) View {
	out := View{Snapshot: snap, Glance: Glance{UseFirst: []UseFirst{}, NeedsYou: []string{}, Held: []Balance{}}}
	out.Accounts = make([]Account, len(snap.Accounts))
	held := map[string]*big.Rat{}
	places := map[string]int{}
	for i, a := range snap.Accounts {
		a.Windows = mark(a.Windows, insightsFor(a, insights), now)
		a.Groups = slices.Clone(a.Groups)
		for j := range a.Groups {
			a.Groups[j].Windows = mark(a.Groups[j].Windows, insightsFor(a, insights), now)
		}
		out.Accounts[i] = a
		switch a.Status {
		case StatusSignedOut, StatusError:
			out.Glance.NeedsYou = append(out.Glance.NeedsYou, a.ID)
			continue
		case StatusOK:
		default:
			continue
		}
		for _, w := range allWindowsOf(a) {
			if w.UseFirst != usefirst.No {
				out.Glance.UseFirst = append(out.Glance.UseFirst, UseFirst{Account: a.ID, Window: w, Why: w.UseFirst, At: *w.ResetsAt})
			}
		}
		switch a.Kind {
		case KindSubscription:
			out.Glance.Subscriptions++
			if r := renewal(a); r != nil && (out.Glance.NextRenewal == nil || r.At.Before(out.Glance.NextRenewal.At)) {
				out.Glance.NextRenewal = r
			}
		case KindFree:
			out.Glance.Free++
		case KindAPI:
			out.Glance.APIs++
		}
		for _, b := range heldBy(a) {
			n, ok := new(big.Rat).SetString(b.Amount)
			if !ok || b.Currency == "credits" {
				continue
			}
			if held[b.Currency] == nil {
				held[b.Currency] = new(big.Rat)
			}
			held[b.Currency].Add(held[b.Currency], n)
			places[b.Currency] = max(places[b.Currency], decimals(b.Amount))
		}
	}
	sort.SliceStable(out.Glance.UseFirst, func(i, j int) bool { return out.Glance.UseFirst[i].At.Before(out.Glance.UseFirst[j].At) })
	for c, n := range held {
		out.Glance.Held = append(out.Glance.Held, Balance{Currency: c, Amount: n.FloatString(places[c])})
	}
	sort.Slice(out.Glance.Held, func(i, j int) bool { return out.Glance.Held[i].Currency < out.Glance.Held[j].Currency })
	return out
}

// mark returns a copy of ws with each window's percent left and use-first
// reason.
func mark(ws []Window, ins []Insight, now time.Time) []Window {
	ws = slices.Clone(ws)
	for i := range ws {
		ws[i].PercentLeft = percentLeft(ws[i])
		ws[i].UseFirst = usefirst.Of(allowanceOf(ws[i], findWindowInsight(ins, ws[i])), now)
	}
	return ws
}

// heldBy is the money an account holds: an API account's balances, and any
// account's unlabelled group balances. A labelled group amount ("Last
// month") is what was spent, not what's held.
func heldBy(a Account) []Balance {
	var out []Balance
	if a.Kind == KindAPI {
		out = append(out, a.Balances...)
	}
	for _, g := range a.Groups {
		for _, b := range g.Balances {
			if b.Label == "" {
				out = append(out, b)
			}
		}
	}
	return out
}

func renewal(a Account) *Renewal {
	switch {
	case a.RenewsAt != nil:
		return &Renewal{Account: a.ID, At: *a.RenewsAt}
	case a.EndsAt != nil:
		return &Renewal{Account: a.ID, At: *a.EndsAt, Ends: true}
	}
	return nil
}

func decimals(amount string) int {
	if i := strings.IndexByte(amount, '.'); i >= 0 {
		return len(amount) - i - 1
	}
	return 0
}
