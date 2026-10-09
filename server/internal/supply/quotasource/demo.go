package quotasource

import (
	"context"
	"strings"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
	"ant/server/internal/quota"
	"ant/server/internal/supply"
)

// DemoSource feeds the real engine with the sample accounts of the
// providers that have modules (Z.ai, DeepSeek, Kimi, Bailian), so demo
// mode shows the real route preview without reading anything. Each sample
// account's meters and models become an inventory and offers the way a
// module's would.
type DemoSource struct {
	Now func() time.Time
	// Endpoints are the modules' endpoints, by "provider/mode" where a
	// provider's products are called at different places (Bailian's Token
	// Plan and free quota), else by provider.
	Endpoints map[string]catalog.Endpoint
}

func (d DemoSource) endpoint(a quota.Account) (catalog.Endpoint, bool) {
	if e, ok := d.Endpoints[a.Provider+"/"+a.Mode]; ok {
		return e, true
	}
	e, ok := d.Endpoints[a.Provider]
	return e, ok
}

func (d DemoSource) accounts() map[domain.ID]quota.Account {
	out := map[domain.ID]quota.Account{}
	for _, a := range quota.DemoAccounts(d.Now()) {
		if _, ok := d.endpoint(a); ok {
			out[domain.ID(a.ID)] = a
		}
	}
	return out
}

func (d DemoSource) Discover(context.Context) ([]supply.Credential, error) {
	var out []supply.Credential
	for id, a := range d.accounts() {
		out = append(out, supply.Credential{ID: id, ProviderID: domain.ID(a.Provider), Kind: "api_key", Origin: supply.OriginFound,
			Strategy: "demo", Locator: []byte(`{"source":"sample data"}`)})
	}
	return out, nil
}

func (d DemoSource) Collect(_ context.Context, c supply.Credential) supply.Result {
	a, ok := d.accounts()[c.ID]
	if !ok {
		return supply.Result{Outcome: supply.OutcomeError, Detail: "sample account gone"}
	}
	r := result(a)
	if r.Outcome != supply.OutcomeOK {
		return r
	}
	inv := inventory(a)
	// the module's endpoint, drawing on what this sample account holds
	e, _ := d.endpoint(a)
	e.Draws = nil
	for _, p := range inv.Packages {
		for _, res := range p.Resources {
			if !res.Aside {
				e.Draws = append(e.Draws, res.TemplateRef)
			}
		}
	}
	listed := make([]observed.UpstreamModel, len(a.Models))
	for i, m := range a.Models {
		listed[i] = observed.UpstreamModel{ID: m.ID, Name: m.Name, Media: m.Media}
	}
	r.Offers = catalog.Offers(&catalog.Supply{Endpoints: []catalog.Endpoint{e}}, catalog.Session{Product: e.Product}, listed)
	return r
}

// Name is how a sample account's card is called.
func (d DemoSource) Name(id string) string {
	if a, ok := d.accounts()[domain.ID(id)]; ok {
		return strings.TrimSpace(firstOf(a.Label, a.Name))
	}
	return id
}
