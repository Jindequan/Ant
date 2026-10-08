package kimi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
)

// Production origins.
const (
	CodeBase = "https://api.kimi.com"    // Kimi Code
	CNBase   = "https://api.moonshot.cn" // platform.kimi.com, CNY
	AIBase   = "https://api.moonshot.ai" // platform.kimi.ai, USD
)

// Products, as the connection's mode names them.
const (
	ProductCode = "code"
	ProductAPI  = "api"
)

// Meters. A Kimi Code call draws on every window the plan reports; usages
// lists the shorter ones in limits[] (one today), numbered after the first.
const (
	MeterShort = "window/short"
	MeterWeek  = "window/week"
)

var codeDraws = []string{MeterShort, MeterShort + "-2", MeterShort + "-3", MeterWeek}

// Region is a Moonshot platform: where its key works and what it's paid in.
type Region struct {
	ID, Base, Currency, Console string
}

// regions are the two platforms; keys don't work across them.
func regions(cnBase, aiBase string) map[string]Region {
	return map[string]Region{
		"cn": {ID: "cn", Base: cnBase, Currency: "CNY", Console: "platform.kimi.com"},
		"ai": {ID: "ai", Base: aiBase, Currency: "USD", Console: "platform.kimi.ai"},
	}
}

// Module is Kimi: Kimi Code at codeBase and the Moonshot platform at cnBase
// and aiBase (CodeBase, CNBase, AIBase in production).
func Module(codeBase, cnBase, aiBase string) catalog.Module {
	codeBase, cnBase, aiBase = strings.TrimRight(codeBase, "/"), strings.TrimRight(cnBase, "/"), strings.TrimRight(aiBase, "/")
	rs := regions(cnBase, aiBase)
	return catalog.Module{ID: "kimi", Name: "Kimi", Supply: &catalog.Supply{
		Origins: []string{codeBase, cnBase, aiBase},
		Endpoints: []catalog.Endpoint{
			{Base: codeBase + "/coding/v1", Protocol: "openai-chat", Draws: codeDraws, Product: ProductCode},
			{Base: cnBase + "/v1", Protocol: "openai-chat", Draws: []string{"balance/cny"}, Product: ProductAPI, Region: "cn"},
			{Base: aiBase + "/v1", Protocol: "openai-chat", Draws: []string{"balance/usd"}, Product: ProductAPI, Region: "ai"},
		},
		Collector: collector{code: codeBase, regions: rs},
	}}
}

type collector struct {
	code    string
	regions map[string]Region
}

// get sends one read with the key as a Bearer token. Ant's own client
// identity is sent, never a coding tool's (Kimi's docs warn that changing
// a tool's User-Agent may suspend its benefits).
func (c collector) get(ctx context.Context, url, who, console string, s catalog.Session, fetch observed.Fetch) (observed.Response, error) {
	key := strings.TrimSpace(s.Values["apiKey"])
	if key == "" {
		return observed.Response{}, fmt.Errorf("%w: Add the API key.", catalog.ErrSignedOut)
	}
	res, err := fetch(ctx, observed.Request{URL: url, Header: map[string]string{"Authorization": "Bearer " + key, "Accept": "application/json"}})
	if err != nil {
		return res, fmt.Errorf("Couldn't reach %s: %v", who, err)
	}
	if res.Status == 401 || res.Status == 403 {
		return res, fmt.Errorf("%w: %s refused the key. Check it in %s.", catalog.ErrSignedOut, who, console)
	}
	if res.Status < 200 || res.Status > 299 {
		return res, fmt.Errorf("%s answered %d", who, res.Status)
	}
	return res, nil
}

func (c collector) region(s catalog.Session) (Region, error) {
	r, ok := c.regions[s.Values["region"]]
	if !ok {
		return Region{}, fmt.Errorf("Choose the platform the key is from: platform.kimi.com (China) or platform.kimi.ai (international).")
	}
	return r, nil
}

func (c collector) Inventory(ctx context.Context, s catalog.Session, fetch observed.Fetch) (observed.Inventory, error) {
	switch s.Product {
	case ProductCode:
		res, err := c.get(ctx, c.code+"/coding/v1/usages", "Kimi Code", "your Kimi Code console", s, fetch)
		if err != nil {
			return observed.Inventory{}, err
		}
		ws, err := ParseUsages(res.Body)
		if err != nil {
			return observed.Inventory{}, err
		}
		return codeInventory(ws), nil
	case ProductAPI:
		r, err := c.region(s)
		if err != nil {
			return observed.Inventory{}, err
		}
		res, err := c.get(ctx, r.Base+"/v1/users/me/balance", "Moonshot", r.Console+" › API keys", s, fetch)
		if err != nil {
			return observed.Inventory{}, err
		}
		b, err := ParseBalance(res.Body)
		if err != nil {
			return observed.Inventory{}, err
		}
		return balanceInventory(b, r), nil
	}
	return observed.Inventory{}, fmt.Errorf("Kimi has no product %q", s.Product)
}

// codeInventory: one subscription; its windows as percent at 10^-2, the
// counts behind them kept as evidence (the usages endpoint is observed,
// not documented).
func codeInventory(ws []Window) observed.Inventory {
	p := observed.PackageReading{TemplateRef: "kimi/code", Kind: "subscription", Basis: "observed", Plan: "Kimi Code"}
	short := 0
	for _, w := range ws {
		ref := MeterWeek
		if w.Short {
			short++
			ref = MeterShort
			if short > 1 {
				ref += "-" + strconv.Itoa(short)
			}
		}
		r := observed.ResourceReading{TemplateRef: ref, Label: w.Name, MetricID: "plan.allowance", Unit: "percent", QuantumExp: -2,
			SemanticsRef: "kimi.code-window", SemanticsRevision: "v1",
			WindowJSON: mustJSON(map[string]string{"kind": "reported", "name": w.Name}), ScopeJSON: []byte(`{"kind":"all"}`),
			ResetBasis: "none", Limit: centi(100), Used: centi(w.UsedPercent), Remaining: centi(100 - w.UsedPercent),
			Evidence: mustJSON(map[string]string{"limit": w.Limit, "used": w.Used, "source": "observed: /coding/v1/usages"})}
		if w.ResetsAt > 0 {
			end := domain.Millis(w.ResetsAt)
			r.WindowEnd, r.ResetBasis = &end, "reported"
		}
		p.Resources = append(p.Resources, r)
	}
	return observed.Inventory{Packages: []observed.PackageReading{p}}
}

// balanceInventory: one prepaid package in the region's currency, exact at
// 10^-6; vouchers and cash as evidence. Calls fail once available is 0.
func balanceInventory(b Balance, r Region) observed.Inventory {
	avail, _ := catalog.ParseDecimal(b.Available)
	usable := avail > 0
	ev := mustJSON(map[string]string{"granted": Money(b.Voucher), "toppedUp": Money(b.Cash)})
	p := observed.PackageReading{TemplateRef: "kimi/balance-" + r.ID, Kind: "prepaid", Basis: "observed", Plan: "API balance", Usable: &usable,
		Resources: []observed.ResourceReading{{TemplateRef: "balance/" + strings.ToLower(r.Currency), Label: "Balance", MetricID: "balance",
			Unit: r.Currency, Currency: r.Currency, QuantumExp: -6, SemanticsRef: "sem.entitlement-pool", SemanticsRevision: "semrev-1",
			WindowJSON: []byte(`{"kind":"lifetime"}`), ScopeJSON: []byte(`{"kind":"all"}`), ResetBasis: "none", Remaining: &avail, Evidence: ev}}}
	return observed.Inventory{Packages: []observed.PackageReading{p}, Note: Notes(b, r.Currency)}
}

// Notes is what a person should know about a balance: used up (calls fail),
// cash in arrears; "" when neither.
func Notes(b Balance, currency string) string {
	var notes []string
	if avail, _ := catalog.ParseDecimal(b.Available); avail <= 0 {
		notes = append(notes, "The balance is used up. Top up to keep using the API.")
	}
	if strings.HasPrefix(b.Cash, "-") {
		notes = append(notes, "Cash is in arrears: "+Money(b.Cash)+" "+currency+".")
	}
	return strings.Join(notes, " ")
}

// Models are what the key can call at its product's endpoint.
func (c collector) Models(ctx context.Context, s catalog.Session, fetch observed.Fetch) ([]observed.UpstreamModel, error) {
	url, who, console := c.code+"/coding/v1/models", "Kimi Code", "your Kimi Code console"
	if s.Product == ProductAPI {
		r, err := c.region(s)
		if err != nil {
			return nil, err
		}
		url, who, console = r.Base+"/v1/models", "Moonshot", r.Console+" › API keys"
	} else if s.Product != ProductCode {
		return nil, fmt.Errorf("Kimi has no product %q", s.Product)
	}
	res, err := c.get(ctx, url, who, console, s, fetch)
	if err != nil {
		return nil, err
	}
	return catalog.ParseOpenAIModels(res.Body)
}

func centi(percent float64) *domain.Units {
	u := domain.Units(math.Round(percent * 100))
	return &u
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
