package quotasource

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
	"ant/server/internal/supply"
)

func demoSource() DemoSource {
	return DemoSource{Now: time.Now, Endpoints: map[string]catalog.Endpoint{
		"zai":      {Base: "https://api.z.ai/api/coding/paas/v4", Protocol: "openai-chat"},
		"deepseek": {Base: "https://api.deepseek.com", Protocol: "openai-chat"},
		// a module with several products: the sample is Kimi Code's endpoint
		"kimi": {Base: "https://api.kimi.com/coding/v1", Protocol: "openai-chat", Product: "code"},
		// a provider whose products are called at different places: by provider/mode
		"aliyun/free-quota": {Base: "https://dashscope.aliyuncs.com/compatible-mode/v1", Protocol: "openai-chat", Product: "free-quota"},
	}}
}

// The demo reads nothing by construction: it has no network to read with.
func TestDemoSourceHasNoNetwork(t *testing.T) {
	fetch := reflect.TypeOf(observed.Fetch(nil))
	ty := reflect.TypeOf(DemoSource{})
	for i := range ty.NumField() {
		if ty.Field(i).Type == fetch {
			t.Fatalf("DemoSource has a Fetch field (%s)", ty.Field(i).Name)
		}
	}
}

// Only providers given an endpoint are sampled; offers come from those
// endpoints and draw on what the sample account holds (never an aside).
func TestDemoSourceSamplesProvidersWithEndpoints(t *testing.T) {
	d := demoSource()
	creds, err := d.Discover(context.Background())
	if err != nil || len(creds) != 4 {
		t.Fatalf("credentials = %+v %v", creds, err)
	}
	for _, c := range creds {
		if c.Origin != supply.OriginFound || (c.ProviderID != "zai" && c.ProviderID != "deepseek" && c.ProviderID != "kimi" && c.ID != "demo-aliyun-free") {
			t.Fatalf("credential = %+v", c)
		}
		r := d.Collect(context.Background(), c)
		if r.Outcome != supply.OutcomeOK || r.Inventory == nil || len(r.Offers) == 0 {
			t.Fatalf("%s: %+v", c.ID, r)
		}
		for _, o := range r.Offers {
			e := d.Endpoints[string(c.ProviderID)]
			if c.ProviderID == "aliyun" {
				e = d.Endpoints["aliyun/free-quota"]
			}
			if o.Endpoint != e.Base || len(o.Draws) == 0 {
				t.Fatalf("%s offer = %+v", c.ID, o)
			}
			for _, ref := range o.Draws {
				for _, p := range r.Inventory.Packages {
					for _, res := range p.Resources {
						if res.TemplateRef == ref && res.Aside {
							t.Fatalf("%s draws on an aside meter %s", c.ID, ref)
						}
					}
				}
			}
		}
		if d.Name(string(c.ID)) == string(c.ID) {
			t.Fatalf("no card name for %s", c.ID)
		}
	}
	if got, _ := (DemoSource{Now: time.Now}).Discover(context.Background()); len(got) != 0 {
		t.Fatalf("without endpoints nothing is sampled: %+v", got)
	}
}

// Bailian's sample free quota: each model's offer, through the preview's
// rule, draws on that model's grant only (the sample draws every meter it
// holds; meters scoped to another model are never drawn).
func TestDemoSourceFreeQuotaIsPerModel(t *testing.T) {
	d := demoSource()
	r := d.Collect(context.Background(), supply.Credential{ID: "demo-aliyun-free"})
	if r.Outcome != supply.OutcomeOK || len(r.Offers) < 2 || r.Inventory.Packages[0].Kind != "free_grant" {
		t.Fatalf("free quota sample = %+v", r)
	}
	for _, res := range r.Inventory.Packages[0].Resources {
		if !strings.Contains(string(res.ScopeJSON), `"kind":"model"`) {
			t.Errorf("%s isn't scoped to its model: %s", res.TemplateRef, res.ScopeJSON)
		}
	}
}
