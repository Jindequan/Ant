package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
)

// DefaultBase is DeepSeek's API origin.
const DefaultBase = "https://api.deepseek.com"

// SupplyModule is DeepSeek as a supply module (design 12): the prepaid
// balance and the models the key can call, read at base (DefaultBase in
// production). Calls draw on the balance of whichever currency is charged.
func SupplyModule(base string) catalog.Module {
	base = strings.TrimRight(base, "/")
	return catalog.Module{ID: "deepseek", Name: "DeepSeek", Supply: &catalog.Supply{
		Origins:   []string{base},
		Endpoints: []catalog.Endpoint{{Base: base, Protocol: "openai-chat", Draws: []string{"balance/cny", "balance/usd"}}},
		Collector: supplyCollector{base: base},
	}}
}

type supplyCollector struct{ base string }

func (c supplyCollector) get(ctx context.Context, path string, s catalog.Session, fetch observed.Fetch) (observed.Response, error) {
	key := strings.TrimSpace(s.Values["apiKey"])
	if key == "" {
		return observed.Response{}, fmt.Errorf("%w: Add the API key.", catalog.ErrSignedOut)
	}
	res, err := fetch(ctx, observed.Request{URL: c.base + path, Header: map[string]string{"Authorization": "Bearer " + key, "Accept": "application/json"}})
	if err != nil {
		return res, err
	}
	if res.Status == 401 || res.Status == 403 {
		return res, fmt.Errorf("%w: DeepSeek refused the key. Check it in your API keys.", catalog.ErrSignedOut)
	}
	if res.Status < 200 || res.Status > 299 {
		return res, fmt.Errorf("DeepSeek answered %d", res.Status)
	}
	return res, nil
}

// Inventory reads the balance (GET /user/balance) with the shared exact
// parser: one prepaid package, a meter per currency in cents.
func (c supplyCollector) Inventory(ctx context.Context, s catalog.Session, fetch observed.Fetch) (observed.Inventory, error) {
	res, err := c.get(ctx, "/user/balance", s, fetch)
	if err != nil {
		return observed.Inventory{}, err
	}
	obs, err := ParseBalanceObservation(res.Body, 0) // the engine records when it read
	if err != nil {
		return observed.Inventory{}, fmt.Errorf("DeepSeek answered something Ant doesn't understand.")
	}
	usable := obs.IsAvailable
	p := observed.PackageReading{TemplateRef: "deepseek/balance", Kind: "prepaid", Basis: "observed", Plan: "API balance", Usable: &usable}
	for _, b := range obs.Balances {
		cur := strings.ToUpper(b.Currency)
		total := b.TotalCents
		ev, _ := json.Marshal(map[string]string{"granted": cents(b.GrantedCents), "toppedUp": cents(b.ToppedUpCents)})
		p.Resources = append(p.Resources, observed.ResourceReading{
			TemplateRef: "balance/" + strings.ToLower(cur), Label: "Balance", MetricID: "balance", Unit: cur, Currency: cur, QuantumExp: -2,
			SemanticsRef: "sem.entitlement-pool", SemanticsRevision: "semrev-1",
			WindowJSON: []byte(`{"kind":"lifetime"}`), ScopeJSON: []byte(`{"kind":"all"}`),
			ResetBasis: "none", Remaining: &total, Evidence: ev,
		})
	}
	inv := observed.Inventory{Packages: []observed.PackageReading{p}}
	if !usable {
		inv.Note = "Balance is too low for API calls. Top up to keep using it."
	}
	return inv, nil
}

// Models are what the key can call (GET /models).
func (c supplyCollector) Models(ctx context.Context, s catalog.Session, fetch observed.Fetch) ([]observed.UpstreamModel, error) {
	res, err := c.get(ctx, "/models", s, fetch)
	if err != nil {
		return nil, err
	}
	return catalog.ParseOpenAIModels(res.Body)
}

func cents(u domain.Units) string {
	sign := ""
	if u < 0 {
		sign, u = "-", -u
	}
	return fmt.Sprintf("%s%d.%02d", sign, u/100, u%100)
}
