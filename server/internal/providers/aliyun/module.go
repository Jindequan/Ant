package aliyun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
)

// Products, as the connection's mode names them.
const (
	ProductTokenPlan = "token-plan"
	ProductPayg      = "payg"
	ProductFreeQuota = "free-quota"
)

// Bases are the origins the module reads and the bases its models are
// called at (DefaultBases in production; tests point them at fakes).
type Bases struct {
	Gateway      string // CLI gateway, China site (aliyun.com)
	GatewayIntl  string // CLI gateway, international site (alibabacloud.com)
	TokenPlan    string // Token Plan's model endpoint origin (China)
	DashScope    string // pay-as-you-go model endpoint origin (China, Beijing), where free quota is spent
	Business     string // billing API (BssOpenApi), China site
	BusinessIntl string // billing API, international site
}

// DefaultBases, from Bailian's CLI and Alibaba Cloud's docs (design 13 §5.1).
func DefaultBases() Bases {
	return Bases{
		Gateway:      "https://bailian-cs.console.aliyun.com",
		GatewayIntl:  "https://bailian-cs.console.alibabacloud.com",
		TokenPlan:    "https://token-plan.cn-beijing.maas.aliyuncs.com",
		DashScope:    "https://dashscope.aliyuncs.com",
		Business:     "https://business.aliyuncs.com",
		BusinessIntl: "https://business.ap-southeast-1.aliyuncs.com",
	}
}

// Token Plan windows a call draws on; the usage reply names the windows a
// tier has (a month, and a week or 5 hours on some tiers).
var tokenPlanDraws = []string{"window/5hour", "window/1day", "window/1week", "window/7day", "window/1month"}

// Module is Alibaba Cloud Bailian.
func Module(b Bases) catalog.Module {
	trim := func(s string) string { return strings.TrimRight(s, "/") }
	b = Bases{Gateway: trim(b.Gateway), GatewayIntl: trim(b.GatewayIntl), TokenPlan: trim(b.TokenPlan), DashScope: trim(b.DashScope), Business: trim(b.Business), BusinessIntl: trim(b.BusinessIntl)}
	china := map[string]string{"site": ""} // the international site lists Token Plan for teams only
	return catalog.Module{ID: "aliyun", Name: "Alibaba Cloud Bailian", Supply: &catalog.Supply{
		Origins: []string{b.Gateway, b.GatewayIntl, b.TokenPlan, b.Business, b.BusinessIntl},
		Endpoints: []catalog.Endpoint{
			{Base: b.TokenPlan + "/compatible-mode/v1", Protocol: "openai-chat", Draws: tokenPlanDraws, Product: ProductTokenPlan, When: china},
			// free quota is spent through the account's own keys at DashScope: a call draws on its model's grant
			{Base: b.DashScope + "/compatible-mode/v1", Protocol: "openai-chat", Draws: []string{observed.DrawModelScope}, Product: ProductFreeQuota, When: china},
		},
		Requires: map[string][]string{
			ProductTokenPlan: {"token"},
			ProductFreeQuota: {"token"},
			ProductPayg:      {"accessKeyId", "accessKeySecret"},
		},
		Collector: collector{b: b},
	}}
}

type collector struct{ b Bases }

func (c collector) gateway(s catalog.Session, fetch observed.Fetch) gateway {
	host, region := c.b.Gateway, s.Values["region"]
	if s.Values["site"] == "international" {
		host = c.b.GatewayIntl
	}
	if region == "" {
		region = "cn-beijing"
	}
	return gateway{host: host, token: s.Values["token"], region: region, fetch: fetch}
}

func (c collector) Inventory(ctx context.Context, s catalog.Session, fetch observed.Fetch) (observed.Inventory, error) {
	switch s.Product {
	case ProductTokenPlan:
		if strings.TrimSpace(s.Values["token"]) == "" {
			return observed.Inventory{}, fmt.Errorf("%w: Sign in with Alibaba Cloud.", catalog.ErrSignedOut)
		}
		return tokenPlan(ctx, c.gateway(s, fetch))
	case ProductPayg:
		return c.payg(ctx, s, fetch)
	case ProductFreeQuota:
		if strings.TrimSpace(s.Values["token"]) == "" {
			return observed.Inventory{}, fmt.Errorf("%w: Sign in with Alibaba Cloud to list each model's free quota.", catalog.ErrSignedOut)
		}
		inv, _, err := freeQuota(ctx, c.gateway(s, fetch), s.Now)
		return inv, err
	}
	return observed.Inventory{}, fmt.Errorf("Bailian's module doesn't read %q yet", s.Product)
}

// tokenPlan reads the plan, its windows, add-on credits and tool benefits.
func tokenPlan(ctx context.Context, g gateway) (observed.Inventory, error) {
	var sub Subscription
	if err := g.call(ctx, APISubscription, nil, &sub); err != nil {
		return observed.Inventory{}, err
	}
	if sub.InstanceCode == "" {
		return observed.Inventory{Note: "This Alibaba Cloud account has no Token Plan."}, nil
	}
	inv := observed.Inventory{Identity: sub.InstanceCode} // one instance, however it's signed in
	p := observed.PackageReading{TemplateRef: "aliyun/token-plan", Kind: "subscription", Basis: "observed", Plan: catalog.PlanName(sub.SpecCode), AutoRenew: sub.AutoRenewFlag}
	if end := millis(sub.EndTime); end != nil {
		p.EndsAt = end
		if sub.AutoRenewFlag != nil && *sub.AutoRenewFlag {
			p.RenewsAt = end
		}
	}
	p.StartsAt = millis(sub.StartTime)
	var notes []string
	if sub.Status != "" && !strings.EqualFold(sub.Status, "VALID") {
		notes = append(notes, "The Token Plan is "+strings.ToLower(sub.Status)+".")
	}
	var usage map[string]json.RawMessage
	if err := g.call(ctx, APIUsage, nil, &usage); err != nil {
		notes = append(notes, "Couldn't read usage: "+err.Error())
	}
	for _, w := range ParseUsage(usage) {
		used, ok := catalog.ParseDecimal(w.Used)
		if !ok {
			continue
		}
		usedPct := centi(used) // a fraction at 10^-6 → percent at 10^-2
		r := observed.ResourceReading{TemplateRef: w.Ref(), Label: w.Name, MetricID: "plan.allowance", Unit: "percent", QuantumExp: -2,
			SemanticsRef: "aliyun.token-plan-window", SemanticsRevision: "v1",
			WindowJSON: mustJSON(map[string]string{"kind": "reported", "name": w.Name}), ScopeJSON: []byte(`{"kind":"all"}`),
			ResetBasis: "none", Limit: units(10000), Used: units(usedPct), Remaining: units(10000 - usedPct),
			Evidence: mustJSON(map[string]string{"usedFraction": w.Used})}
		if w.ResetsAt > 0 {
			end := domain.Millis(w.ResetsAt)
			r.WindowEnd, r.ResetBasis = &end, "reported"
		}
		p.Resources = append(p.Resources, r)
	}
	var addons Addons
	if err := g.call(ctx, APIAddons, nil, &addons); err == nil {
		total, okT := catalog.ParseDecimal(addons.TotalCredits.String())
		left, okL := catalog.ParseDecimal(addons.RemainingCredits.String())
		if okT && okL && total > 0 {
			p.Resources = append(p.Resources, observed.ResourceReading{TemplateRef: "credits/addon", Label: "Add-on credits", MetricID: "credits", Unit: "credits", QuantumExp: -6,
				SemanticsRef: "sem.entitlement-pool", SemanticsRevision: "semrev-1", WindowJSON: []byte(`{"kind":"lifetime"}`), ScopeJSON: []byte(`{"kind":"all"}`),
				ResetBasis: "none", Limit: &total, Remaining: &left})
		}
	}
	inv.Packages = append(inv.Packages, p)

	var eq Equities
	if err := g.call(ctx, APIEquity, map[string]any{"queryTokenPlanEquityInfoRequest": map[string]any{"requestId": RequestID()}}, &eq); err != nil {
		notes = append(notes, "Couldn't read tool benefits: "+err.Error())
	}
	toolsPkg := observed.PackageReading{TemplateRef: "aliyun/token-plan-tools", Kind: "subscription", Basis: "observed", Plan: "Tool benefits"}
	for _, e := range eq.Infos {
		name, unit := Tool(e.EquityType)
		total, okT := catalog.ParseDecimal(e.TotalQuota.String())
		left, okL := catalog.ParseDecimal(e.AvailableQuota.String())
		if !okT || !okL {
			continue
		}
		r := observed.ResourceReading{TemplateRef: "tool/" + e.EquityType, Label: name, Aside: true, MetricID: "tool." + domain.ID(e.EquityType), Unit: unit, QuantumExp: -6,
			SemanticsRef: "aliyun.token-plan-tool", SemanticsRevision: "v1",
			WindowJSON: mustJSON(map[string]string{"kind": "reported", "name": name}), ScopeJSON: []byte(`{"kind":"all"}`),
			ResetBasis: "none", Limit: &total, Remaining: &left}
		if end := millis(e.InstanceEndTime); end != nil {
			r.WindowEnd, r.ResetBasis = end, "reported"
		}
		toolsPkg.Resources = append(toolsPkg.Resources, r)
	}
	if len(toolsPkg.Resources) > 0 {
		inv.Packages = append(inv.Packages, toolsPkg)
		notes = append(notes, ToolsNote)
	}
	inv.Note = strings.Join(notes, " ")
	return inv, nil
}

// Models: the Token Plan lists models only through a bl profile's model
// key whose base is the Token Plan's own origin; otherwise none.
func (c collector) Models(ctx context.Context, s catalog.Session, fetch observed.Fetch) ([]observed.UpstreamModel, error) {
	if s.Product == ProductFreeQuota && strings.TrimSpace(s.Values["token"]) != "" {
		// the free quota account serves exactly the models it holds grants for
		_, grants, err := freeQuota(ctx, c.gateway(s, fetch), s.Now)
		return freeQuotaModels(grants), err
	}
	if s.Product != ProductTokenPlan {
		return nil, nil
	}
	key, base := strings.TrimSpace(s.Values["apiKey"]), strings.TrimRight(s.Values["baseURL"], "/")
	if key == "" || !strings.HasPrefix(base+"/", c.b.TokenPlan+"/") {
		return nil, nil
	}
	res, err := fetch(ctx, observed.Request{URL: base + "/models", Header: map[string]string{"Authorization": "Bearer " + key, "Accept": "application/json"}})
	if err != nil {
		return nil, err
	}
	if res.Status < 200 || res.Status > 299 {
		return nil, fmt.Errorf("Bailian answered %d", res.Status)
	}
	return catalog.ParseOpenAIModels(res.Body)
}

// centi is a fraction at 10^-6 as percent at 10^-2, rounded half up
// (0.25 → 2500).
func centi(fraction domain.Units) int64 {
	v := int64(fraction)
	if v < 0 {
		return -((-v + 50) / 100)
	}
	return (v + 50) / 100
}

func units(v int64) *domain.Units {
	u := domain.Units(v)
	return &u
}

func millis(n json.Number) *domain.Millis {
	v, err := n.Int64()
	if err != nil || v <= 0 {
		return nil
	}
	m := domain.Millis(v)
	return &m
}

// RequestID is a random UUID, as the console sends with some calls.
func RequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
