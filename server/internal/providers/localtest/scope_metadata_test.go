package localtest

import (
	"encoding/json"
	"strings"
	"testing"

	"ant/server/internal/domain"
)

func scopeSnapshot(t *testing.T) []domain.VerifiedScope {
	t.Helper()
	var raw struct {
		Scopes []domain.VerifiedScope `json:"scopes"`
	}
	if err := json.Unmarshal(verifiedScopesJSON, &raw); err != nil {
		t.Fatal(err)
	}
	return raw.Scopes
}

func catalogWithScopes(t *testing.T, scopes []domain.VerifiedScope) (*Catalog, error) {
	t.Helper()
	data, err := json.Marshal(struct {
		Scopes []domain.VerifiedScope `json:"scopes"`
	}{scopes})
	if err != nil {
		t.Fatal(err)
	}
	return newWithData(descriptorJSON, data)
}

func TestScopeMetadataResolution(t *testing.T) {
	scopes := scopeSnapshot(t)
	// Bindings can be verified aliases of a shared billing scope. They must
	// retain that scope's identity, just as account aliases do.
	scopes[2].Aliases = append(scopes[2].Aliases, "binding-demo-1")
	cat, err := catalogWithScopes(t, scopes)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                string
		access, fact, scope domain.ID
	}{
		{"account", "acct-demo-1", "alpha-daily-token-limit", "acct-demo-1"},
		{"second_account", "acct-demo-2", "alpha-usd-grant-promo", "acct-demo-2"},
		{"shared_direct", "billing-alpha-shared", "alpha-shared-tokens", "billing-alpha-shared"},
		{"shared_account_alias", "acct-demo-1", "alpha-shared-tokens", "billing-alpha-shared"},
		{"shared_second_account_alias", "acct-demo-2", "alpha-shared-tokens", "billing-alpha-shared"},
		{"shared_binding_alias", "binding-demo-1", "alpha-shared-tokens", "billing-alpha-shared"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fact, ok := cat.ResolveScopeFact(tc.access, tc.fact)
			if !ok || fact.ScopeID != tc.scope || fact.ProviderTypeID != "provider-alpha" {
				t.Fatalf("resolved fact: %+v, found=%v", fact, ok)
			}
			dimensional, count, ok := cat.ResolveDimensionalFact(tc.access, "provider-alpha", fact.MetricID, fact.Unit, func(other domain.VerifiedResourceFact) bool { return other.FactID == tc.fact })
			if !ok || count != 1 || dimensional.ScopeID != tc.scope || dimensional.ProviderTypeID != "provider-alpha" {
				t.Fatalf("dimensional fact: %+v, count=%d, found=%v", dimensional, count, ok)
			}
			_, count, ok = cat.ResolveDimensionalFact(tc.access, "other-provider", fact.MetricID, fact.Unit, func(domain.VerifiedResourceFact) bool { return true })
			if ok || count != 0 {
				t.Fatal("foreign provider resolved")
			}
		})
	}
}

func TestScopeMetadataExplicitMatch(t *testing.T) {
	scopes := scopeSnapshot(t)
	for i := range scopes {
		for j := range scopes[i].Facts {
			scopes[i].Facts[j].ScopeID = scopes[i].ScopeID
			scopes[i].Facts[j].ProviderTypeID = scopes[i].ProviderTypeID
		}
	}
	if _, err := catalogWithScopes(t, scopes); err != nil {
		t.Fatal(err)
	}
}

func TestScopeMetadataRejectsInvalidSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		change        func([]domain.VerifiedScope)
	}{
		{"child_scope", "scopeId contradicts parent", func(s []domain.VerifiedScope) { s[0].Facts[0].ScopeID = "acct-demo-2" }},
		{"shared_child_account_substitution", "scopeId contradicts parent", func(s []domain.VerifiedScope) { s[2].Facts[0].ScopeID = "acct-demo-1" }},
		{"child_provider", "providerTypeId contradicts parent", func(s []domain.VerifiedScope) { s[0].Facts[0].ProviderTypeID = "other-provider" }},
		{"missing_parent_scope", "are required", func(s []domain.VerifiedScope) { s[0].ScopeID = "" }},
		{"missing_parent_provider", "are required", func(s []domain.VerifiedScope) { s[0].ProviderTypeID = "" }},
		{"unknown_parent_provider", "not registered", func(s []domain.VerifiedScope) { s[0].ProviderTypeID = "other-provider" }},
		{"duplicate_parent_scope", "duplicate scopeId", func(s []domain.VerifiedScope) { s[1].ScopeID = s[0].ScopeID }},
		{"unknown_parent_kind", "unsupported kind", func(s []domain.VerifiedScope) { s[2].Kind = "binding" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scopes := scopeSnapshot(t)
			tc.change(scopes)
			cat, err := catalogWithScopes(t, scopes)
			if cat != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("catalog=%v error=%v", cat, err)
			}
		})
	}
}
