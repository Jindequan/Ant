package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/providers/localtest"
)

func TestProviderCatalogRequiresAuthenticationAndReturnsDescriptorsOnly(t *testing.T) {
	catalog, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	env := newEnv(t, domain.PermRead)
	h := AuthMiddleware(env.auth, ProviderCatalogHandler(catalog))

	unauthorized := env.do(t, h, http.MethodGet, "/v1/catalog/providers", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", unauthorized.Code)
	}
	response := env.do(t, h, http.MethodGet, "/v1/catalog/providers", env.token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, body %s", response.Code, response.Body.String())
	}
	var descriptors []map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &descriptors); err != nil {
		t.Fatalf("decode descriptors: %v", err)
	}
	if len(descriptors) != 1 {
		t.Fatalf("descriptor count = %d, want 1", len(descriptors))
	}
	for _, field := range []string{"configSchema", "resourceSchema", "capabilitySchema", "extensionSchema", "entitlementFacts", "semantics", "profiles"} {
		if len(descriptors[0][field]) == 0 {
			t.Errorf("descriptor missing %s", field)
		}
	}
	if _, leaked := descriptors[0]["scopes"]; leaked {
		t.Fatal("catalog response must not expose verified account scope snapshots")
	}
}

func TestProviderCatalogRequiresReadPermission(t *testing.T) {
	catalog, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	env := newEnv(t, domain.PermExecute)
	h := AuthMiddleware(env.auth, ProviderCatalogHandler(catalog))
	response := env.do(t, h, http.MethodGet, "/v1/catalog/providers", env.token, "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestProviderDescriptorListDoesNotExposeMutableCatalogStorage(t *testing.T) {
	catalog, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	first := catalog.Descriptors()
	first[0].ConfigSchema[0] = 'x'
	second := catalog.Descriptors()
	if second[0].ConfigSchema[0] == 'x' {
		t.Fatal("mutating a returned descriptor changed the catalog")
	}
}

func TestProviderScopeCatalogIsAuthenticatedAndExplicitlyFixtureData(t *testing.T) {
	catalog, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	env := newEnv(t, domain.PermRead)
	h := AuthMiddleware(env.auth, ProviderScopeHandler(catalog))
	if got := env.do(t, h, http.MethodGet, "/v1/catalog/scopes", "", "").Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", got)
	}
	response := env.do(t, h, http.MethodGet, "/v1/catalog/scopes", env.token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", response.Code, response.Body.String())
	}
	var view VerifiedScopeCatalogView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Fixture || len(view.Scopes) != 3 {
		t.Fatalf("fixture=%v scopes=%d", view.Fixture, len(view.Scopes))
	}
	for _, scope := range view.Scopes {
		if scope.ProviderTypeID != "provider-alpha" || len(scope.Facts) == 0 {
			t.Fatalf("unexpected scope: %+v", scope)
		}
	}
}

func TestProviderScopeListDoesNotExposeMutableCatalogStorage(t *testing.T) {
	catalog, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	first := catalog.VerifiedScopes()
	first[0].Facts[0].CapacityBound++
	second := catalog.VerifiedScopes()
	if first[0].Facts[0].CapacityBound == second[0].Facts[0].CapacityBound {
		t.Fatal("mutating returned scope changed catalog storage")
	}
}
