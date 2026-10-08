package main

import (
	"log/slog"
	"net/http"

	"ant/server/internal/api"
	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/providers/deepseek"
	"ant/server/internal/security"
	"ant/server/internal/store"
)

// The configuration-only composition contains no adapter, transport, secret
// cipher, execution host or supervisor. Register schemas here before adding
// provider execution in the separate runtime composition.
func configurationProviders() (*application.ProviderRegistry, error) {
	catalog, err := deepseek.NewCatalog()
	if err != nil {
		return nil, err
	}
	return application.NewProviderRegistry(application.ProviderRegistration{Catalog: catalog})
}

type unverifiedScopes struct{}

func (unverifiedScopes) VerifiedScopes() []domain.VerifiedScope { return []domain.VerifiedScope{} }

func resourceConfigurationHandler(st store.Store, clock application.Clock, auth *security.AuthProvider, logger *slog.Logger) (http.Handler, error) {
	providers, err := configurationProviders()
	if err != nil {
		return nil, err
	}
	catalog := providers.Catalog()
	configs, err := application.NewConfigService(st, catalog, clock)
	if err != nil {
		return nil, err
	}
	pools, err := application.NewPoolQueries(st, catalog, clock)
	if err != nil {
		return nil, err
	}
	operations, err := application.NewOperationQueries(st)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/catalog/providers", api.ProviderCatalogHandler(catalog))
	mux.Handle("/v1/catalog/scopes", api.ProviderScopeCatalogHandler(unverifiedScopes{}, false))
	mux.Handle("/v1/pools", api.PoolsHandler(pools, nil))
	mux.Handle("/v1/pools/", api.PoolsHandler(pools, nil))
	mux.Handle("/v1/operations", api.OperationQueryHandler(operations, nil))
	mux.Handle("/v1/operations/", api.OperationQueryHandler(operations, nil))
	mux.Handle("/", api.ConfigHandler(configs))
	return api.AuthMiddleware(auth, api.LogRequest(logger, mux)), nil
}
