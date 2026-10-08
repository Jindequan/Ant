package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
	"ant/server/internal/providers/deepseek"
	"ant/server/internal/providers/localtest"
)

func TestCatalogComposesProviderDirectoriesWithoutInventingScopes(t *testing.T) {
	fixture, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	deepSeek, err := deepseek.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	composed, err := catalog.New(fixture, deepSeek)
	if err != nil {
		t.Fatal(err)
	}
	entries := composed.Descriptors()
	if len(entries) != 2 || entries[0].TypeID != "deepseek-standard" || entries[1].TypeID != "provider-alpha" {
		t.Fatalf("provider directory = %+v", entries)
	}
	deepSeekDescriptor, ok := composed.Descriptor("deepseek-standard")
	if !ok || deepSeekDescriptor.AdapterRevision != "deepseek-chat-v1" || len(deepSeekDescriptor.Profiles) != 1 {
		t.Fatalf("DeepSeek descriptor missing from composed directory: %+v", deepSeekDescriptor)
	}
	if issue := composed.ValidateExtensionsInContext("deepseek-standard", "account", json.RawMessage(`{"accountRef":"acct-123"}`)); len(issue) != 0 {
		t.Fatalf("valid DeepSeek account extension rejected: %v", issue)
	}
	if issue := composed.ValidateExtensionsInContext("deepseek-standard", "account", json.RawMessage(`{"apiKey":"secret"}`)); len(issue) == 0 {
		t.Fatal("credential material must remain outside provider extension JSON")
	}
	if issue := composed.ValidateExtensionsInContext("deepseek-standard", "binding", json.RawMessage(`{"endpointClass":"chat","model":"deepseek-flash","maxOutputTokens":1024}`)); len(issue) != 0 {
		t.Fatalf("valid DeepSeek binding capability rejected: %v", issue)
	}
	if _, ok := composed.ResolveScopeFact("acct-live", "deepseek-account-balance"); ok {
		t.Fatal("catalogue must not invent a live DeepSeek account balance")
	}
	if fact, ok := composed.ResolveScopeFact("acct-demo-1", "alpha-usd-grant-promo"); !ok || fact.ProviderTypeID != "provider-alpha" {
		t.Fatalf("local fixture fact was lost in composition: %+v ok=%v", fact, ok)
	}
	deepSeekDescriptor.ConfigSchema[0] = '!'
	if again, _ := composed.Descriptor("deepseek-standard"); again.ConfigSchema[0] == '!' {
		t.Fatal("descriptor copies must not mutate the composed directory")
	}
}

func TestCatalogRejectsDuplicateProviderRegistration(t *testing.T) {
	fixture, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.New(fixture, fixture); err == nil {
		t.Fatal("duplicate provider type registration must fail")
	}
}

func TestModelIDNormalizesOnlyCaseAndVendorPrefix(t *testing.T) {
	for in, want := range map[string]string{"GLM-5.3": "glm-5.3", "zhipu/glm-5.3": "glm-5.3", " deepseek-v4 ": "deepseek-v4", "openrouter/anthropic/claude-x": "claude-x", "gpt-5.5-2026-01-01": "gpt-5.5-2026-01-01"} {
		if got := catalog.ModelID(in); got != want {
			t.Errorf("ModelID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlanName(t *testing.T) {
	for in, want := range map[string]string{"pro": "Pro", "  max ": "Max", "": "", "Lite": "Lite"} {
		if got := catalog.PlanName(in); got != want {
			t.Errorf("PlanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOffersFollowProductAndRegion(t *testing.T) {
	s := &catalog.Supply{Endpoints: []catalog.Endpoint{
		{Base: "https://api.kimi.com/coding/v1", Protocol: "openai-chat", Product: "code"},
		{Base: "https://api.moonshot.cn/v1", Protocol: "openai-chat", Product: "api", Region: "cn"},
		{Base: "https://api.moonshot.ai/v1", Protocol: "openai-chat", Product: "api", Region: "ai"},
		{Base: "https://shared.example/v1", Protocol: "openai-chat"}, // no selector: every credential
	}}
	models := []observed.UpstreamModel{{ID: "kimi-k3"}}
	bases := func(session catalog.Session) string {
		var out []string
		for _, o := range catalog.Offers(s, session, models) {
			out = append(out, o.Endpoint)
		}
		return strings.Join(out, " ")
	}
	for session, want := range map[*catalog.Session]string{
		{Product: "code"}: "https://api.kimi.com/coding/v1 https://shared.example/v1",
		{Product: "api", Values: map[string]string{"region": "cn"}}: "https://api.moonshot.cn/v1 https://shared.example/v1",
		{Product: "api", Values: map[string]string{"region": "ai"}}: "https://api.moonshot.ai/v1 https://shared.example/v1",
		{}: "https://shared.example/v1",
	} {
		if got := bases(*session); got != want {
			t.Errorf("%+v: %s, want %s", *session, got, want)
		}
	}
}

func TestParseDecimalIsExact(t *testing.T) {
	cases := map[string]int64{"23.40": 23_400_000, "0": 0, "-1.5": -1_500_000, "0.0000004": 0, "0.0000005": 1, "12": 12_000_000, "49.58894": 49_588_940, "999999999999.999999": 999_999_999_999_999_999}
	for in, want := range cases {
		if got, ok := catalog.ParseDecimal(in); !ok || int64(got) != want {
			t.Errorf("%s → %d %v, want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "abc", "1.2.3", "1e5", ".5", "+1", "1234567890123.0", "--1"} {
		if got, ok := catalog.ParseDecimal(bad); ok {
			t.Errorf("%q → %d, want refused", bad, got)
		}
	}
}

func TestWindowName(t *testing.T) {
	for d, want := range map[time.Duration]string{5 * time.Hour: "5 hours", 7 * 24 * time.Hour: "Week", 30 * 24 * time.Hour: "30 days",
		24 * time.Hour: "24 hours", 90 * time.Minute: "90 minutes", 0: "Allowance"} {
		if got := catalog.WindowName(d); got != want {
			t.Errorf("WindowName(%v) = %q, want %q", d, got, want)
		}
	}
}
