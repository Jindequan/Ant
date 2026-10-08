package main

import (
	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/planning"
	"ant/server/internal/providers/deepseek"
	"ant/server/internal/providers/localtest"
)

// Add providers here as complete registrations. The composition root remains
// responsible for explicit transport endpoint allowlists and account admission.
// Registration itself performs no provider calls and invents no account facts.
func builtInProviders(clock application.Clock) (*application.ProviderRegistry, *localtest.Catalog, error) {
	fixture, err := localtest.New()
	if err != nil {
		return nil, nil, err
	}
	realCatalog, err := deepseek.NewCatalog()
	if err != nil {
		return nil, nil, err
	}
	fixtureDescriptor, _ := fixture.Descriptor("provider-alpha")
	fixtureAdapter, err := localtest.NewExecutionAdapter(fixtureDescriptor, clock)
	if err != nil {
		return nil, nil, err
	}
	realDescriptor, _ := realCatalog.Descriptor(deepseek.ProviderTypeID)
	realAdapter, err := deepseek.New(realDescriptor, clock, func(domain.DomainRequest, domain.ExecutionPlan) error { return nil }, deepseek.PeakUsageProjector)
	if err != nil {
		return nil, nil, err
	}
	registry, err := application.NewProviderRegistry(
		application.ProviderRegistration{Catalog: fixture, Adapter: fixtureAdapter, Policies: []planning.ProviderPolicy{application.LocalTestTextPolicy{}}},
		application.ProviderRegistration{Catalog: realCatalog, Adapter: realAdapter, Policies: []planning.ProviderPolicy{application.DeepSeekTextPolicy{}}},
	)
	if err != nil {
		return nil, nil, err
	}
	return registry, fixture, nil
}
