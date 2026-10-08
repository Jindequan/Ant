// Package catalog composes independently validated provider catalogs into
// one immutable directory for configuration and planning.
package catalog

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sort"

	"ant/server/internal/config"
	"ant/server/internal/domain"
)

var ErrInvalidCatalog = errors.New("provider catalog: invalid or duplicate registration")

type descriptorSource interface {
	Descriptors() []domain.ProviderDescriptor
}

type Catalog struct {
	providers   map[domain.ID]config.ProviderCatalog
	byID        map[domain.ID]domain.ProviderDescriptor
	sources     []catalogSource
	descriptors []domain.ProviderDescriptor
}

type catalogSource struct {
	catalog config.ProviderCatalog
	types   map[domain.ID]bool
}

func New(sources ...config.ProviderCatalog) (*Catalog, error) {
	if len(sources) == 0 {
		return nil, ErrInvalidCatalog
	}
	providers := make(map[domain.ID]config.ProviderCatalog)
	byID := make(map[domain.ID]domain.ProviderDescriptor)
	uniqueSources := make([]catalogSource, 0, len(sources))
	descriptors := make([]domain.ProviderDescriptor, 0)
	for _, source := range sources {
		if source == nil || isNil(source) {
			return nil, ErrInvalidCatalog
		}
		directory, ok := source.(descriptorSource)
		if !ok {
			return nil, ErrInvalidCatalog
		}
		entries := directory.Descriptors()
		if len(entries) == 0 {
			return nil, ErrInvalidCatalog
		}
		sourceTypes := make(map[domain.ID]bool, len(entries))
		for _, descriptor := range entries {
			if descriptor.TypeID == "" || descriptor.AdapterRevision == "" {
				return nil, ErrInvalidCatalog
			}
			if _, exists := providers[descriptor.TypeID]; exists {
				return nil, ErrInvalidCatalog
			}
			registered, found := source.Descriptor(descriptor.TypeID)
			if !found || !reflect.DeepEqual(registered, descriptor) {
				return nil, ErrInvalidCatalog
			}
			providers[descriptor.TypeID] = source
			byID[descriptor.TypeID] = cloneDescriptor(descriptor)
			sourceTypes[descriptor.TypeID] = true
			descriptors = append(descriptors, cloneDescriptor(descriptor))
		}
		uniqueSources = append(uniqueSources, catalogSource{catalog: source, types: sourceTypes})
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].TypeID < descriptors[j].TypeID })
	return &Catalog{providers: providers, byID: byID, sources: uniqueSources, descriptors: descriptors}, nil
}

func (c *Catalog) Descriptor(typeID domain.ID) (domain.ProviderDescriptor, bool) {
	if c == nil {
		return domain.ProviderDescriptor{}, false
	}
	descriptor, ok := c.byID[typeID]
	return cloneDescriptor(descriptor), ok
}

func (c *Catalog) Descriptors() []domain.ProviderDescriptor {
	if c == nil {
		return nil
	}
	result := make([]domain.ProviderDescriptor, len(c.descriptors))
	for i, descriptor := range c.descriptors {
		result[i] = cloneDescriptor(descriptor)
	}
	return result
}

func (c *Catalog) SupportsDomain(typeID domain.ID, domainName string) bool {
	if c == nil {
		return false
	}
	descriptor, ok := c.byID[typeID]
	return ok && slices.Contains(descriptor.SupportedDomains, domainName)
}

func (c *Catalog) ValidateExtensionsInContext(typeID domain.ID, context string, raw json.RawMessage) []domain.FieldIssue {
	if c == nil {
		return []domain.FieldIssue{{Path: "", MessageKey: "ant.config.providerTypeNotRegistered"}}
	}
	source, ok := c.providers[typeID]
	if !ok {
		return []domain.FieldIssue{{Path: "", MessageKey: "ant.config.providerTypeNotRegistered"}}
	}
	return source.ValidateExtensionsInContext(typeID, context, raw)
}

func (c *Catalog) ResolveScopeFact(scopeID, factID domain.ID) (domain.VerifiedResourceFact, bool) {
	if c == nil {
		return domain.VerifiedResourceFact{}, false
	}
	var found domain.VerifiedResourceFact
	count := 0
	for _, source := range c.sources {
		fact, ok := source.catalog.ResolveScopeFact(scopeID, factID)
		if ok {
			if !source.types[fact.ProviderTypeID] {
				return domain.VerifiedResourceFact{}, false
			}
			found = fact
			count++
			if count > 1 {
				return domain.VerifiedResourceFact{}, false
			}
		}
	}
	return found, count == 1
}

func isNil(value any) bool {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func (c *Catalog) ResolveDimensionalFact(scopeID, providerType, metricID domain.ID, unit string, windowEquals func(domain.VerifiedResourceFact) bool) (domain.VerifiedResourceFact, int, bool) {
	if c == nil || windowEquals == nil {
		return domain.VerifiedResourceFact{}, 0, false
	}
	source, ok := c.providers[providerType]
	if !ok {
		return domain.VerifiedResourceFact{}, 0, false
	}
	return source.ResolveDimensionalFact(scopeID, providerType, metricID, unit, windowEquals)
}

func cloneDescriptor(value domain.ProviderDescriptor) domain.ProviderDescriptor {
	value.SupportedDomains = slices.Clone(value.SupportedDomains)
	value.ConfigSchema = append(json.RawMessage(nil), value.ConfigSchema...)
	value.ResourceSchema = append(json.RawMessage(nil), value.ResourceSchema...)
	value.CapabilitySchema = append(json.RawMessage(nil), value.CapabilitySchema...)
	value.ExtensionSchema = append(json.RawMessage(nil), value.ExtensionSchema...)
	value.EntitlementFacts = slices.Clone(value.EntitlementFacts)
	value.Semantics = slices.Clone(value.Semantics)
	for i := range value.Semantics {
		value.Semantics[i].AcceptedRevisions = slices.Clone(value.Semantics[i].AcceptedRevisions)
	}
	value.Profiles = slices.Clone(value.Profiles)
	for i := range value.Profiles {
		value.Profiles[i].RequiredDimensions = slices.Clone(value.Profiles[i].RequiredDimensions)
	}
	return value
}

var _ config.ProviderCatalog = (*Catalog)(nil)
