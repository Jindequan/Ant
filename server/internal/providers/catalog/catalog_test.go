package catalog_test

import (
	"encoding/json"
	"testing"

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
