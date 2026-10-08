package main

import (
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/providers/chatcompletions"
	"ant/server/internal/providers/deepseek"
)

type providerTestClock struct{}

func (providerTestClock) Now() domain.Millis { return 100 }

func TestBuiltInProviderAssembly(t *testing.T) {
	r, fixture, err := builtInProviders(providerTestClock{})
	if err != nil {
		t.Fatal(err)
	}
	if fixture == nil || len(r.Policies()) != 2 || len(r.Catalog().Descriptors()) != 2 {
		t.Fatal("built-in registrations incomplete")
	}
	for _, id := range []domain.ID{"provider-alpha", deepseek.ProviderTypeID} {
		a, ok := r.Adapter(id)
		d, found := r.Catalog().Descriptor(id)
		if !ok || !found || a.Descriptor().AdapterRevision != d.AdapterRevision {
			t.Fatal("mismatched runtime registration", id)
		}
	}
	if _, ok := r.Catalog().ResolveScopeFact("live-account", "deepseek-account-balance"); ok {
		t.Fatal("startup invented live account facts")
	}
	request := domain.DomainRequest{Domain: "ai/text", CapabilityProfile: "profile.chat.basic", Payload: []byte(`{"model":"deepseek-flash","messages":[{"role":"user","content":"hello"}],"max_tokens":16384}`)}
	if err := chatcompletions.ValidateRequest(request); err != nil {
		t.Fatalf("protocol validator incorrectly imposes local fixture's 8192 cap: %v", err)
	}
}

func TestConfigurationProvidersHaveNoExecutionOrFixtureFacts(t *testing.T) {
	registry, err := configurationProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Policies()) != 0 {
		t.Fatal("configuration-only mode registered an execution policy")
	}
	if _, ok := registry.Adapter(deepseek.ProviderTypeID); ok {
		t.Fatal("configuration-only mode registered an adapter")
	}
	descriptors := registry.Catalog().Descriptors()
	if len(descriptors) != 1 || len(descriptors[0].Profiles) != 0 || len(descriptors[0].Semantics) != 0 {
		t.Fatal("configuration-only descriptors advertise execution", descriptors)
	}
	if _, ok := registry.Catalog().ResolveScopeFact("subscription-1", "deepseek-account-balance"); ok {
		t.Fatal("declared account became a verified scope")
	}
	if scopes := (unverifiedScopes{}).VerifiedScopes(); scopes == nil || len(scopes) != 0 {
		t.Fatal("configuration scope JSON must be an empty array")
	}
}
