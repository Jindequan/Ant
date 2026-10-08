package api

import (
	"net/http"

	"ant/server/internal/domain"
)

type ProviderDescriptorSource interface {
	Descriptors() []domain.ProviderDescriptor
}

type VerifiedScopeSource interface {
	VerifiedScopes() []domain.VerifiedScope
}

type VerifiedScopeCatalogView struct {
	Fixture bool                   `json:"fixture"`
	Scopes  []domain.VerifiedScope `json:"scopes"`
}

// ProviderCatalogHandler exposes registered descriptor schemas to authenticated
// readers. It deliberately excludes verified account scopes and their facts;
// those belong to the separate provider verification boundary.
func ProviderCatalogHandler(source ProviderDescriptorSource) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/catalog/providers" {
			http.NotFound(w, r)
			return
		}
		_, _, ok := configPrincipalFor(w, r, domain.PermRead)
		if !ok {
			return
		}
		writeConfigJSON(w, http.StatusOK, source.Descriptors())
	})
}

// ProviderScopeHandler is enabled only by the localtest composition root. The
// fixture bit is mandatory so clients cannot mistake demo capacities for live
// evidence or customer data.
func ProviderScopeHandler(source VerifiedScopeSource) http.Handler {
	return ProviderScopeCatalogHandler(source, true)
}

// ProviderScopeCatalogHandler keeps fixture identity explicit. An empty real
// catalogue means no scopes have been verified; it never manufactures facts.
func ProviderScopeCatalogHandler(source VerifiedScopeSource, fixture bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/catalog/scopes" {
			http.NotFound(w, r)
			return
		}
		_, _, ok := configPrincipalFor(w, r, domain.PermRead)
		if !ok {
			return
		}
		writeConfigJSON(w, http.StatusOK, VerifiedScopeCatalogView{Fixture: fixture, Scopes: source.VerifiedScopes()})
	})
}
