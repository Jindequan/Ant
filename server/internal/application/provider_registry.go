package application

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"ant/server/internal/adapters"
	"ant/server/internal/config"
	"ant/server/internal/domain"
	providercatalog "ant/server/internal/providers/catalog"
)

var ErrProviderRegistration = errors.New("provider registry: inconsistent registration")

// ProviderRegistration binds a single provider's vocabulary and executable
// components at startup. A catalog without an adapter or policies is useful
// for drafts; executable registrations must cover every advertised profile.
type ProviderRegistration struct {
	Catalog  config.ProviderCatalog
	Adapter  adapters.ProviderAdapter
	Policies []ProviderSpecificCandidatePlanPolicy
}

// ProviderRegistry is immutable after construction. Extensions still have
// no ledger/transport authority; it implements only the adapter lookup port.
type ProviderRegistry struct {
	catalog  *providercatalog.Catalog
	adapters map[domain.ID]adapters.ProviderAdapter
	policies []CandidatePlanPolicy
}

// Freeze registration identity while retaining the trusted policy callback.
// Changing a policy object's metadata later cannot reroute a registered key.
type registeredProviderPolicy struct {
	CandidatePlanPolicy
	provider   domain.ID
	domainName string
	profile    domain.ID
}

// A draft-only catalog keeps its form vocabulary but does not advertise
// executable profiles or semantics to the configuration compiler.
type draftProviderCatalog struct {
	config.ProviderCatalog
	descriptor domain.ProviderDescriptor
}

func (c draftProviderCatalog) Descriptor(id domain.ID) (domain.ProviderDescriptor, bool) {
	if id != c.descriptor.TypeID {
		return domain.ProviderDescriptor{}, false
	}
	return c.descriptor, true
}
func (c draftProviderCatalog) Descriptors() []domain.ProviderDescriptor {
	return []domain.ProviderDescriptor{c.descriptor}
}

func (p registeredProviderPolicy) ProviderTypeID() domain.ID    { return p.provider }
func (p registeredProviderPolicy) Domain() string               { return p.domainName }
func (p registeredProviderPolicy) CapabilityProfile() domain.ID { return p.profile }

func NewProviderRegistry(registrations ...ProviderRegistration) (*ProviderRegistry, error) {
	if len(registrations) == 0 || len(registrations) > 128 {
		return nil, ErrProviderRegistration
	}
	sources := make([]config.ProviderCatalog, 0, len(registrations))
	type policyEntry struct {
		provider domain.ID
		domain   string
		profile  domain.ID
		policy   CandidatePlanPolicy
	}
	entries := make([]policyEntry, 0)
	lookup := make(map[domain.ID]adapters.ProviderAdapter, len(registrations))
	for _, registration := range registrations {
		directory, ok := registration.Catalog.(interface {
			Descriptors() []domain.ProviderDescriptor
		})
		if !ok || nilComponent(directory) {
			return nil, ErrProviderRegistration
		}
		descriptors := directory.Descriptors()
		if len(descriptors) != 1 {
			return nil, ErrProviderRegistration
		}
		descriptor := descriptors[0]
		if registration.Adapter == nil && len(registration.Policies) == 0 {
			descriptor.Profiles = []domain.RegisteredProfile{}
			descriptor.Semantics = []domain.RegisteredSemantics{}
			sources = append(sources, draftProviderCatalog{ProviderCatalog: registration.Catalog, descriptor: descriptor})
			continue
		}
		sources = append(sources, registration.Catalog)
		if nilComponent(registration.Adapter) || len(registration.Policies) == 0 || len(registration.Policies) > 128 {
			return nil, fmt.Errorf("%w: missing adapter or policies", ErrProviderRegistration)
		}
		if !reflect.DeepEqual(registration.Adapter.Descriptor(), descriptor) {
			return nil, fmt.Errorf("%w: adapter descriptor mismatch", ErrProviderRegistration)
		}
		profiles := make(map[string]bool, len(descriptor.Profiles))
		for _, profile := range descriptor.Profiles {
			key := candidatePolicyKey(profile.Domain, profile.ProfileID)
			if profile.Domain == "" || profile.ProfileID == "" || profiles[key] || !slices.Contains(descriptor.SupportedDomains, profile.Domain) {
				return nil, fmt.Errorf("%w: invalid profile", ErrProviderRegistration)
			}
			profiles[key] = true
		}
		if len(profiles) != len(registration.Policies) {
			return nil, fmt.Errorf("%w: incomplete profile policies", ErrProviderRegistration)
		}
		for _, policy := range registration.Policies {
			if nilComponent(policy) {
				return nil, ErrProviderRegistration
			}
			key := candidatePolicyKey(policy.Domain(), policy.CapabilityProfile())
			if policy.ProviderTypeID() != descriptor.TypeID || !profiles[key] {
				return nil, fmt.Errorf("%w: policy identity mismatch", ErrProviderRegistration)
			}
			delete(profiles, key)
			entries = append(entries, policyEntry{descriptor.TypeID, policy.Domain(), policy.CapabilityProfile(), policy})
		}
		lookup[descriptor.TypeID] = registration.Adapter
	}
	catalog, err := providercatalog.New(sources...)
	if err != nil {
		return nil, fmt.Errorf("%w: catalog rejected", ErrProviderRegistration)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].provider != entries[j].provider {
			return entries[i].provider < entries[j].provider
		}
		if entries[i].domain != entries[j].domain {
			return entries[i].domain < entries[j].domain
		}
		return entries[i].profile < entries[j].profile
	})
	policies := make([]CandidatePlanPolicy, 0, len(entries))
	for _, entry := range entries {
		policies = append(policies, registeredProviderPolicy{CandidatePlanPolicy: entry.policy, provider: entry.provider, domainName: entry.domain, profile: entry.profile})
	}
	return &ProviderRegistry{catalog: catalog, adapters: lookup, policies: policies}, nil
}

func (r *ProviderRegistry) Catalog() *providercatalog.Catalog {
	if r == nil {
		return nil
	}
	return r.catalog
}

func (r *ProviderRegistry) Policies() []CandidatePlanPolicy {
	if r == nil {
		return nil
	}
	return slices.Clone(r.policies)
}

func (r *ProviderRegistry) Adapter(id domain.ID) (adapters.ProviderAdapter, bool) {
	if r == nil {
		return nil, false
	}
	a, ok := r.adapters[id]
	return a, ok
}

func nilComponent(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

var _ AdapterLookup = (*ProviderRegistry)(nil)
