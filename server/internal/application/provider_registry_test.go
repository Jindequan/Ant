package application

import (
	"errors"
	"os"
	"sync"
	"testing"

	"ant/server/internal/adapters"
	"ant/server/internal/config"
	"ant/server/internal/domain"
	"ant/server/internal/providers/deepseek"
	"ant/server/internal/providers/localtest"
)

func registryFixtures(t *testing.T) (ProviderRegistration, ProviderRegistration) {
	t.Helper()
	clock := &candidateTestClock{now: 100}
	local, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	real, err := deepseek.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	ld, _ := local.Descriptor("provider-alpha")
	rd, _ := real.Descriptor(deepseek.ProviderTypeID)
	la, err := localtest.NewExecutionAdapter(ld, clock)
	if err != nil {
		t.Fatal(err)
	}
	ra, err := deepseek.New(rd, clock, deepseek.ValidateExecutionPlan, deepseek.PeakUsageProjector)
	if err != nil {
		t.Fatal(err)
	}
	return ProviderRegistration{Catalog: local, Adapter: la, Policies: []ProviderSpecificCandidatePlanPolicy{LocalTestTextPolicy{}}}, ProviderRegistration{Catalog: real, Adapter: ra, Policies: []ProviderSpecificCandidatePlanPolicy{DeepSeekTextPolicy{}}}
}

func TestProviderRegistrySharedProfilesAndImmutableLookup(t *testing.T) {
	a, b := registryFixtures(t)
	r, err := NewProviderRegistry(a, b)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := NewProviderRegistry(b, a)
	if err != nil {
		t.Fatal(err)
	}
	for i, policy := range r.Policies() {
		p := policy.(ProviderSpecificCandidatePlanPolicy)
		if p.ProviderTypeID() != r2.Policies()[i].(ProviderSpecificCandidatePlanPolicy).ProviderTypeID() {
			t.Fatal("registration order changed policy lookup")
		}
		adapter, found := r.Adapter(p.ProviderTypeID())
		if !found || adapter.Descriptor().TypeID != p.ProviderTypeID() {
			t.Fatal("provider adapter mismatch")
		}
		if p.Domain() != "ai/text" || p.CapabilityProfile() != "profile.chat.basic" {
			t.Fatal("shared profile was lost")
		}
	}
	policies := r.Policies()
	policies[0] = nil
	a.Policies[0] = nil
	if r.Policies()[0] == nil {
		t.Fatal("registry aliases policy slices")
	}
	d := r.Catalog().Descriptors()
	d[0].Profiles[0].RequiredDimensions[0].MetricID = "changed"
	if r.Catalog().Descriptors()[0].Profiles[0].RequiredDimensions[0].MetricID == "changed" {
		t.Fatal("catalog aliases metadata")
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, ok := r.Adapter(deepseek.ProviderTypeID); !ok {
					t.Error("concurrent lookup failed")
				}
				_ = r.Policies()
				_ = r.Catalog().Descriptors()
			}
		}()
	}
	wg.Wait()
}

type changedDescriptorAdapter struct {
	adapters.ProviderAdapter
	descriptor domain.ProviderDescriptor
}

func (a changedDescriptorAdapter) Descriptor() domain.ProviderDescriptor { return a.descriptor }

func TestProviderRegistryRejectsIncompleteAndMismatchedComponents(t *testing.T) {
	for name, mutate := range map[string]func(*ProviderRegistration, ProviderRegistration){
		"missing catalog":   func(a *ProviderRegistration, _ ProviderRegistration) { a.Catalog = nil },
		"typed nil catalog": func(a *ProviderRegistration, _ ProviderRegistration) { var c *localtest.Catalog; a.Catalog = c },
		"missing adapter":   func(a *ProviderRegistration, _ ProviderRegistration) { a.Adapter = nil },
		"typed nil adapter": func(a *ProviderRegistration, _ ProviderRegistration) { var c *deepseek.Adapter; a.Adapter = c },
		"missing policy":    func(a *ProviderRegistration, _ ProviderRegistration) { a.Policies = nil },
		"duplicate policy":  func(a *ProviderRegistration, _ ProviderRegistration) { a.Policies = append(a.Policies, a.Policies[0]) },
		"nil policy":        func(a *ProviderRegistration, _ ProviderRegistration) { a.Policies[0] = nil },
		"typed nil policy": func(a *ProviderRegistration, _ ProviderRegistration) {
			var p *providerCandidatePolicyStub
			a.Policies[0] = p
		},
		"foreign adapter": func(a *ProviderRegistration, b ProviderRegistration) { a.Adapter = b.Adapter },
		"foreign policy":  func(a *ProviderRegistration, b ProviderRegistration) { a.Policies = b.Policies },
		"revision drift": func(a *ProviderRegistration, _ ProviderRegistration) {
			d := a.Adapter.Descriptor()
			d.AdapterRevision = "drift"
			a.Adapter = changedDescriptorAdapter{a.Adapter, d}
		},
		"schema drift": func(a *ProviderRegistration, _ ProviderRegistration) {
			d := a.Adapter.Descriptor()
			d.ConfigSchema = []byte(`{}`)
			a.Adapter = changedDescriptorAdapter{a.Adapter, d}
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, b := registryFixtures(t)
			mutate(&a, b)
			if r, err := NewProviderRegistry(a); r != nil || !errors.Is(err, ErrProviderRegistration) {
				t.Fatalf("registry=%v err=%v", r, err)
			}
		})
	}
	a, _ := registryFixtures(t)
	if r, err := NewProviderRegistry(a, a); r != nil || !errors.Is(err, ErrProviderRegistration) {
		t.Fatal("duplicate provider accepted")
	}
	if r, err := NewProviderRegistry(); r != nil || !errors.Is(err, ErrProviderRegistration) {
		t.Fatal("empty registry accepted")
	}
}

func TestProviderRegistryCatalogOnlyCannotExecute(t *testing.T) {
	a, _ := registryFixtures(t)
	a.Adapter = nil
	a.Policies = nil
	r, err := NewProviderRegistry(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Catalog().Descriptor("provider-alpha"); !ok {
		t.Fatal("draft provider missing")
	}
	if _, ok := r.Adapter("provider-alpha"); ok || len(r.Policies()) != 0 {
		t.Fatal("catalog-only provider became executable")
	}
	d, _ := r.Catalog().Descriptor("provider-alpha")
	if d.Profiles == nil || d.Semantics == nil || len(d.Profiles) != 0 || len(d.Semantics) != 0 || len(d.ConfigSchema) == 0 {
		t.Fatal("draft registration must retain forms without executable vocabulary")
	}
	fixture, err := os.ReadFile("../../../contracts/fixtures/config-document-valid.json")
	if err != nil {
		t.Fatal(err)
	}
	if result := config.Compile(fixture, r.Catalog()); len(result.Issues) == 0 {
		t.Fatal("catalog-only integration allowed executable config activation")
	}
}

func TestProviderRegistryFreezesPolicyIdentity(t *testing.T) {
	a, _ := registryFixtures(t)
	policy := &providerCandidatePolicyStub{candidatePolicyStub: &candidatePolicyStub{}, provider: "provider-alpha"}
	a.Policies = []ProviderSpecificCandidatePlanPolicy{policy}
	r, err := NewProviderRegistry(a)
	if err != nil {
		t.Fatal(err)
	}
	policy.provider = "foreign-provider"
	if r.Policies()[0].(ProviderSpecificCandidatePlanPolicy).ProviderTypeID() != "provider-alpha" {
		t.Fatal("mutable policy identity escaped startup snapshot")
	}
}
