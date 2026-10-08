package deepseek

import (
	"encoding/json"
	"fmt"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"ant/server/internal/domain"
)

type extensionContext string

const (
	accountContext  extensionContext = "account"
	bindingContext  extensionContext = "binding"
	resourceContext extensionContext = "resource"
	offerContext    extensionContext = "offer"
)

// Catalog registers DeepSeek's protocol and resource vocabulary. It carries
// no account scopes: balances and provider-specific capacity facts must be
// obtained from the real account verification path.
type Catalog struct {
	descriptor domain.ProviderDescriptor
	schemas    map[extensionContext]*jsonschema.Schema
}

func NewCatalog() (*Catalog, error) {
	descriptor := domain.ProviderDescriptor{
		TypeID: ProviderTypeID, AdapterRevision: AdapterRevision,
		SupportedDomains: []string{"ai/text"},
		ConfigSchema:     json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"properties":{"accountRef":{"type":"string","pattern":"^[A-Za-z0-9._:-]{1,128}$"}}}`),
		ResourceSchema:   json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"properties":{"billingDimension":{"type":"string","enum":["usd_cents","concurrent_requests"]}}}`),
		CapabilitySchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"properties":{"model":{"const":"deepseek-flash"},"maxOutputTokens":{"type":"integer","minimum":1,"maximum":393216}}}`),
		ExtensionSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"properties":{"endpointClass":{"const":"chat"},"model":{"const":"deepseek-flash"},"maxOutputTokens":{"type":"integer","minimum":1,"maximum":393216}}}`),
		EntitlementFacts: []domain.EntitlementFact{{
			FactID: "deepseek-account-balance", MetricID: "metric.usd.cents", Unit: "usd_cents",
			Description: "Provider-reported account balance; capacity must be verified for the specific account and observation time",
		}},
		Semantics: []domain.RegisteredSemantics{
			{SemanticsRef: "sem.entitlement-pool", AcceptedRevisions: []domain.ID{"semrev-1"}, ResourceKind: "counter", CommercialRole: "entitlement"},
			{SemanticsRef: "sem.occupancy-slot", AcceptedRevisions: []domain.ID{"semrev-1"}, ResourceKind: "occupancy", CommercialRole: "limit"},
		},
		Profiles: []domain.RegisteredProfile{{
			ProfileID: "profile.chat.basic", Domain: "ai/text",
			RequiredDimensions: []domain.RequiredDimension{
				{MetricID: "metric.usd.cents", Role: "entitlement"},
				{MetricID: "metric.concurrency.request", Role: "occupancy"},
			},
		}},
	}

	contexts := map[extensionContext]json.RawMessage{
		accountContext: descriptor.ConfigSchema, bindingContext: descriptor.ExtensionSchema,
		resourceContext: descriptor.ResourceSchema, offerContext: descriptor.CapabilitySchema,
	}
	schemas := make(map[extensionContext]*jsonschema.Schema, len(contexts))
	for name, raw := range contexts {
		var document any
		if err := json.Unmarshal(raw, &document); err != nil {
			return nil, fmt.Errorf("deepseek %s schema json: %w", name, err)
		}
		compiler := jsonschema.NewCompiler()
		resourceName := "deepseek-" + string(name) + ".schema.json"
		if err := compiler.AddResource(resourceName, document); err != nil {
			return nil, fmt.Errorf("deepseek %s schema: %w", name, err)
		}
		compiled, err := compiler.Compile(resourceName)
		if err != nil {
			return nil, fmt.Errorf("compile deepseek %s schema: %w", name, err)
		}
		schemas[name] = compiled
	}
	return &Catalog{descriptor: descriptor, schemas: schemas}, nil
}

func (c *Catalog) Descriptor(typeID domain.ID) (domain.ProviderDescriptor, bool) {
	if c == nil || typeID != ProviderTypeID {
		return domain.ProviderDescriptor{}, false
	}
	return cloneDeepSeekDescriptor(c.descriptor), true
}

func (c *Catalog) Descriptors() []domain.ProviderDescriptor {
	if c == nil {
		return nil
	}
	return []domain.ProviderDescriptor{cloneDeepSeekDescriptor(c.descriptor)}
}

func (c *Catalog) SupportsDomain(typeID domain.ID, domainName string) bool {
	return c != nil && typeID == ProviderTypeID && domainName == "ai/text"
}

func (c *Catalog) ValidateExtensionsInContext(typeID domain.ID, context string, raw json.RawMessage) []domain.FieldIssue {
	if c == nil || typeID != ProviderTypeID {
		return []domain.FieldIssue{{Path: "", MessageKey: "ant.config.providerTypeNotRegistered"}}
	}
	if len(raw) == 0 {
		return nil
	}
	schema, ok := c.schemas[extensionContext(context)]
	if !ok {
		return []domain.FieldIssue{{Path: "", MessageKey: "ant.config.extensionSchemaViolation"}}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return []domain.FieldIssue{{Path: "", MessageKey: "ant.config.extensionJSONInvalid"}}
	}
	if err := schema.Validate(value); err != nil {
		return []domain.FieldIssue{{Path: "", MessageKey: "ant.config.extensionSchemaViolation"}}
	}
	return nil
}

func (c *Catalog) ResolveScopeFact(domain.ID, domain.ID) (domain.VerifiedResourceFact, bool) {
	return domain.VerifiedResourceFact{}, false
}

func (c *Catalog) ResolveDimensionalFact(domain.ID, domain.ID, domain.ID, string, func(domain.VerifiedResourceFact) bool) (domain.VerifiedResourceFact, int, bool) {
	return domain.VerifiedResourceFact{}, 0, false
}

func cloneDeepSeekDescriptor(value domain.ProviderDescriptor) domain.ProviderDescriptor {
	value.SupportedDomains = append([]string(nil), value.SupportedDomains...)
	value.ConfigSchema = append(json.RawMessage(nil), value.ConfigSchema...)
	value.ResourceSchema = append(json.RawMessage(nil), value.ResourceSchema...)
	value.CapabilitySchema = append(json.RawMessage(nil), value.CapabilitySchema...)
	value.ExtensionSchema = append(json.RawMessage(nil), value.ExtensionSchema...)
	value.EntitlementFacts = append([]domain.EntitlementFact(nil), value.EntitlementFacts...)
	value.Semantics = append([]domain.RegisteredSemantics(nil), value.Semantics...)
	for i := range value.Semantics {
		value.Semantics[i].AcceptedRevisions = append([]domain.ID(nil), value.Semantics[i].AcceptedRevisions...)
	}
	value.Profiles = append([]domain.RegisteredProfile(nil), value.Profiles...)
	for i := range value.Profiles {
		value.Profiles[i].RequiredDimensions = append([]domain.RequiredDimension(nil), value.Profiles[i].RequiredDimensions...)
	}
	return value
}

var _ interface {
	Descriptor(domain.ID) (domain.ProviderDescriptor, bool)
	Descriptors() []domain.ProviderDescriptor
	SupportsDomain(domain.ID, string) bool
	ValidateExtensionsInContext(domain.ID, string, json.RawMessage) []domain.FieldIssue
	ResolveScopeFact(domain.ID, domain.ID) (domain.VerifiedResourceFact, bool)
	ResolveDimensionalFact(domain.ID, domain.ID, domain.ID, string, func(domain.VerifiedResourceFact) bool) (domain.VerifiedResourceFact, int, bool)
} = (*Catalog)(nil)
