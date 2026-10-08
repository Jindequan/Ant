package quotasource

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
	"ant/server/internal/quota"
	"ant/server/internal/supply"
)

// Native is a provider module (design 12) and the network its collector
// reads through. Providers with one are read by their module; the others
// by their quota reader.
type Native struct {
	Module catalog.Module
	Fetch  observed.Fetch
}

const modelsTTL = 6 * time.Hour

type cachedModels struct {
	at     time.Time
	models []observed.UpstreamModel
}

// natives holds the modules by provider id and each credential's last
// model list (models change rarely: design 11 §3.4).
type natives struct {
	byProvider map[string]Native
	mu         sync.Mutex
	models     map[string]cachedModels
}

func (n *natives) of(provider string) (Native, bool) {
	if n == nil {
		return Native{}, false
	}
	m, ok := n.byProvider[provider]
	return m, ok && m.Module.Supply != nil
}

// collect reads one connection with its provider's module and returns the
// page's card and the engine's result.
func (n *natives) collect(ctx context.Context, providers []quota.Provider, c quota.Connection, now time.Time, nat Native) (quota.Account, supply.Result) {
	a := quota.NewAccount(providers, c, now)
	a.Live = true
	s := catalog.Session{Values: c.Values}
	inv, err := nat.Module.Supply.Collector.Inventory(ctx, s, nat.Fetch)
	if err != nil {
		a.Status, a.Message = quota.StatusError, err.Error()
		r := supply.Result{Outcome: supply.OutcomeError, Detail: err.Error()}
		if errors.Is(err, catalog.ErrSignedOut) {
			a.Status, a.Message = quota.StatusSignedOut, strings.TrimPrefix(err.Error(), catalog.ErrSignedOut.Error()+": ")
			r = supply.Result{Outcome: supply.OutcomeSignedOut, Detail: a.Message}
		}
		return a, r
	}
	a.Status = quota.StatusOK
	project(&a, inv)
	listed := n.listModels(ctx, c, s, nat, now)
	a.Models = make([]quota.Model, len(listed))
	for i, m := range listed {
		a.Models[i] = quota.Model{ID: m.ID, Name: m.Name, Media: m.Media}
	}
	r := result(a) // cadence from what the card shows
	r.Inventory = &inv
	if listed != nil {
		r.Offers = catalog.Offers(nat.Module.Supply, listed)
	}
	return a, r
}

// listModels is the credential's model list, read at most every modelsTTL;
// nil when it was never read.
func (n *natives) listModels(ctx context.Context, c quota.Connection, s catalog.Session, nat Native, now time.Time) []observed.UpstreamModel {
	n.mu.Lock()
	cached, ok := n.models[c.ID]
	n.mu.Unlock()
	if !ok || now.Sub(cached.at) > modelsTTL {
		if ms, err := nat.Module.Supply.Collector.Models(ctx, s, nat.Fetch); err == nil && len(ms) > 0 {
			cached = cachedModels{at: now, models: ms}
			n.mu.Lock()
			n.models[c.ID] = cached
			n.mu.Unlock()
		}
	}
	return cached.models
}

// project shows an inventory on its card: windows for allowances, balances
// for money, the plan, the note, and the display-only grouping.
func project(a *quota.Account, inv observed.Inventory) {
	a.Identity, a.Message = inv.DisplayGroup, inv.Note
	for _, p := range inv.Packages {
		if p.Plan != "" && a.Plan == "" {
			a.Plan = p.Plan
		}
		if p.Kind == "payg" && len(p.Resources) == 0 {
			a.Kind, a.ModeName = quota.KindAPI, "API key"
		}
		for _, r := range p.Resources {
			if r.Currency != "" {
				a.Balances = append(a.Balances, balanceOf(r))
				continue
			}
			a.Windows = append(a.Windows, windowOf(r))
		}
	}
}

func windowOf(r observed.ResourceReading) quota.Window {
	w := quota.Window{Name: r.Label, Aside: r.Aside, ResetEstimated: r.ResetBasis == "computed"}
	if r.WindowEnd != nil {
		t := time.UnixMilli(int64(*r.WindowEnd)).UTC()
		w.ResetsAt = &t
	}
	if r.Unit == "percent" {
		if r.Used != nil {
			v := scaled(*r.Used, r.QuantumExp)
			w.UsedPercent = &v
		}
		return w
	}
	w.Unit = r.Unit
	w.Used, w.Limit, w.Left = scaledPtr(r.Used, r.QuantumExp), scaledPtr(r.Limit, r.QuantumExp), scaledPtr(r.Remaining, r.QuantumExp)
	return w
}

func scaledPtr(u *domain.Units, exp int) *float64 {
	if u == nil {
		return nil
	}
	v := scaled(*u, exp)
	return &v
}

func balanceOf(r observed.ResourceReading) quota.Balance {
	b := quota.Balance{Currency: r.Currency}
	if r.Remaining != nil {
		b.Amount = decimal(*r.Remaining, r.QuantumExp)
	}
	var ev struct {
		Granted  string `json:"granted"`
		ToppedUp string `json:"toppedUp"`
	}
	_ = json.Unmarshal(r.Evidence, &ev)
	b.Granted, b.ToppedUp = ev.Granted, ev.ToppedUp
	return b
}

func scaled(u domain.Units, exp int) float64 {
	f := float64(u)
	for ; exp < 0; exp++ {
		f /= 10
	}
	for ; exp > 0; exp-- {
		f *= 10
	}
	return f
}

// decimal writes units at their quantum exponent exactly: 2340 at −2 is
// "23.40", never through floating point.
func decimal(u domain.Units, exp int) string {
	neg := u < 0
	if neg {
		u = -u
	}
	s := strconv.FormatInt(int64(u), 10)
	if exp >= 0 {
		s += strings.Repeat("0", exp)
	} else {
		if len(s) <= -exp {
			s = strings.Repeat("0", -exp-len(s)+1) + s
		}
		s = s[:len(s)+exp] + "." + s[len(s)+exp:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
