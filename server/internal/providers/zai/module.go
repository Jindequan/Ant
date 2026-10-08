package zai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
)

// DefaultBase is Z.ai's API origin (global region).
const DefaultBase = "https://api.z.ai"

// Meters a coding plan holds; calls to its models draw on both windows.
const (
	MeterShort = "window/short" // the hours window (5 hours)
	MeterWeek  = "window/week"
	MeterMCP   = "window/mcp-month"
)

// Module is Z.ai, read at base (DefaultBase in production).
func Module(base string) catalog.Module {
	base = strings.TrimRight(base, "/")
	return catalog.Module{ID: "zai", Name: "Z.ai", Supply: &catalog.Supply{
		Origins:   []string{base},
		Endpoints: []catalog.Endpoint{{Base: base + "/api/coding/paas/v4", Protocol: "openai-chat", Draws: []string{MeterShort, MeterWeek}}},
		Collector: collector{base: base},
	}}
}

type collector struct{ base string }

// Inventory reads the plan behind the key. The quota endpoint takes the
// bare key as the Authorization.
func (c collector) Inventory(ctx context.Context, s catalog.Session, fetch observed.Fetch) (observed.Inventory, error) {
	key := strings.TrimSpace(s.Values["apiKey"])
	if key == "" {
		return observed.Inventory{}, fmt.Errorf("%w: Add the plan's API key.", catalog.ErrSignedOut)
	}
	res, err := fetch(ctx, observed.Request{URL: c.base + "/api/monitor/usage/quota/limit", Header: map[string]string{"Authorization": key, "Accept": "application/json"}})
	if err != nil {
		return observed.Inventory{}, fmt.Errorf("Couldn't read the plan: %v", err)
	}
	if res.Status == 401 || res.Status == 403 {
		return observed.Inventory{}, fmt.Errorf("%w: The key was refused. Check it in your Z.ai API keys.", catalog.ErrSignedOut)
	}
	if res.Status < 200 || res.Status > 299 {
		return observed.Inventory{}, fmt.Errorf("Couldn't read the plan: Z.ai answered %d", res.Status)
	}
	q, err := ParseQuota(res.Body)
	if errors.Is(err, ErrKeyRefused) {
		return observed.Inventory{}, fmt.Errorf("%w: The key was refused. Check it in your Z.ai API keys.", catalog.ErrSignedOut)
	}
	if err != nil {
		return observed.Inventory{}, err
	}
	return inventory(q), nil
}

// inventory is a parsed plan as what the account holds.
func inventory(q Quota) observed.Inventory {
	if len(q.Limits) == 0 {
		return observed.Inventory{Note: "This key has no coding plan; it's billed pay-as-you-go.",
			Packages: []observed.PackageReading{{TemplateRef: "zai/pay-as-you-go", Kind: "payg", Basis: "observed", Plan: "API key"}}}
	}
	p := observed.PackageReading{TemplateRef: "zai/coding-plan", Kind: "subscription", Basis: "observed", Plan: q.Level}
	var resets []string
	for _, l := range q.Limits {
		ref := map[string]string{"hours": MeterShort, "week": MeterWeek, "mcp": MeterMCP}[l.Kind]
		if ref == "" {
			ref = "window/other"
		}
		r := observed.ResourceReading{TemplateRef: ref, Label: l.Name(), Aside: l.Kind == "mcp",
			MetricID: "plan.allowance", Unit: "percent", QuantumExp: -2,
			SemanticsRef: "zai.plan-window", SemanticsRevision: "v1",
			WindowJSON: mustJSON(map[string]string{"kind": "reported", "name": l.Name()}), ScopeJSON: []byte(`{"kind":"all"}`),
			ResetBasis: "none", Limit: centi(100)}
		if l.NextReset > 0 {
			end := domain.Millis(l.NextReset)
			r.WindowEnd, r.ResetBasis = &end, "reported"
			resets = append(resets, strconv.FormatInt(l.NextReset, 10))
		}
		if l.UsedPercent != nil {
			r.Used, r.Remaining = centi(*l.UsedPercent), centi(100-*l.UsedPercent)
		}
		r.Evidence = mustJSON(map[string]any{"kind": l.Kind, "usedPercent": l.UsedPercent, "nextReset": l.NextReset})
		p.Resources = append(p.Resources, r)
	}
	inv := observed.Inventory{Packages: []observed.PackageReading{p}}
	if len(resets) > 0 {
		// every key of one account reads the same windows, reset to the
		// millisecond: one card on the page, never one stored account
		inv.DisplayGroup = "global:" + q.Level + ":" + strings.Join(resets, ",")
	}
	return inv
}

// Models are what the plan's coding endpoint serves.
func (c collector) Models(ctx context.Context, s catalog.Session, fetch observed.Fetch) ([]observed.UpstreamModel, error) {
	key := strings.TrimSpace(s.Values["apiKey"])
	if key == "" {
		return nil, fmt.Errorf("%w: Add the plan's API key.", catalog.ErrSignedOut)
	}
	res, err := fetch(ctx, observed.Request{URL: c.base + "/api/coding/paas/v4/models",
		Header: map[string]string{"Authorization": "Bearer " + key, "Accept": "application/json"}})
	if err != nil {
		return nil, err
	}
	if res.Status < 200 || res.Status > 299 {
		return nil, fmt.Errorf("Z.ai answered %d", res.Status)
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
