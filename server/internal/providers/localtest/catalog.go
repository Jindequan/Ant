// Package localtest provides the local/test provider catalogue used by
// contract tests and (later) composition roots in clearly-marked non-production
// setups. It demonstrates the S1 freeze (B4): the descriptor delivers ALL
// schema classes (config/resource/capability/extension) plus provider-verified
// entitlement fact identities with real metadata — nothing here accepts
// free-form JSON, invented bucket ids, or fact references whose metric/unit
// disagree with the resource. NOT a production provider; real vendor
// verification stays with T0/T1.4.
package localtest

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"ant/server/internal/domain"
)

//go:embed provider-alpha.descriptor.json
var descriptorJSON []byte

//go:embed provider-alpha.verified-scopes.json
var verifiedScopesJSON []byte

// Catalog serves provider-alpha from the embedded descriptor. Schemas are
// compiled once at construction; validation is pure afterwards.
// contextSchemaName maps each extension context to its schema class (Q1/C3):
// account→configSchema, resource→resourceSchema, binding/offer→extensionSchema.
// The generic whitelist is no longer a cross-object passport.
type extensionContext string

const (
	ctxAccount  extensionContext = "account"
	ctxBinding  extensionContext = "binding"
	ctxResource extensionContext = "resource"
	ctxOffer    extensionContext = "offer"
)

type Catalog struct {
	descriptors    map[domain.ID]domain.ProviderDescriptor
	contextSchemas map[domain.ID]map[extensionContext]*jsonschema.Schema
	scopes         map[domain.ID]*domain.VerifiedScope
	sharedScopes   []*domain.VerifiedScope
}

func New() (*Catalog, error) {
	return newWithData(descriptorJSON, verifiedScopesJSON)
}

// newWithData keeps snapshot validation testable without changing embedded data.
func newWithData(descriptorData, scopeData []byte) (*Catalog, error) {
	var raw domain.ProviderDescriptor
	if err := json.Unmarshal(descriptorData, &raw); err != nil {
		return nil, fmt.Errorf("localtest descriptor: %w", err)
	}
	if len(raw.ConfigSchema) == 0 || len(raw.ResourceSchema) == 0 ||
		len(raw.CapabilitySchema) == 0 || len(raw.ExtensionSchema) == 0 {
		return nil, fmt.Errorf("localtest descriptor must deliver all four schema classes")
	}

	var scopesRaw struct {
		Scopes []domain.VerifiedScope `json:"scopes"`
	}
	if err := json.Unmarshal(scopeData, &scopesRaw); err != nil {
		return nil, fmt.Errorf("verified scopes: %w", err)
	}

	// Compile every schema class once (Q1/C3): all four are actually consumed
	// via the context mapping, not just delivered.
	classes := map[extensionContext]json.RawMessage{
		ctxAccount:  raw.ConfigSchema,
		ctxResource: raw.ResourceSchema,
		ctxOffer:    raw.CapabilitySchema,
		ctxBinding:  raw.ExtensionSchema,
	}
	compiled := map[extensionContext]*jsonschema.Schema{}
	for ctxName, classRaw := range classes {
		var doc any
		if err := json.Unmarshal(classRaw, &doc); err != nil {
			return nil, fmt.Errorf("%s schema json: %w", ctxName, err)
		}
		compiler := jsonschema.NewCompiler()
		name := string(ctxName) + ".schema.json"
		if err := compiler.AddResource(name, doc); err != nil {
			return nil, fmt.Errorf("%s schema: %w", ctxName, err)
		}
		sch, err := compiler.Compile(name)
		if err != nil {
			return nil, fmt.Errorf("compile %s schema: %w", ctxName, err)
		}
		compiled[ctxName] = sch
	}

	c := &Catalog{
		descriptors:    map[domain.ID]domain.ProviderDescriptor{},
		contextSchemas: map[domain.ID]map[extensionContext]*jsonschema.Schema{},
		scopes:         map[domain.ID]*domain.VerifiedScope{},
	}
	c.descriptors[raw.TypeID] = raw
	c.contextSchemas[raw.TypeID] = map[extensionContext]*jsonschema.Schema{
		ctxAccount:  compiled[ctxAccount],
		ctxResource: compiled[ctxResource],
		ctxOffer:    compiled[ctxOffer],
		ctxBinding:  compiled[ctxBinding],
	}
	for i := range scopesRaw.Scopes {
		sc := &scopesRaw.Scopes[i]
		if sc.ScopeID == "" || sc.ProviderTypeID == "" {
			return nil, fmt.Errorf("verified scope %d: scopeId and providerTypeId are required", i)
		}
		if sc.ProviderTypeID != raw.TypeID {
			return nil, fmt.Errorf("verified scope %s: providerTypeId is not registered", sc.ScopeID)
		}
		if sc.Kind != "account" && sc.Kind != "shared" {
			return nil, fmt.Errorf("verified scope %s: unsupported kind %q", sc.ScopeID, sc.Kind)
		}
		if _, exists := c.scopes[sc.ScopeID]; exists {
			return nil, fmt.Errorf("verified scope %s: duplicate scopeId", sc.ScopeID)
		}
		for j := range sc.Facts {
			fact := &sc.Facts[j]
			if fact.ScopeID != "" && fact.ScopeID != sc.ScopeID {
				return nil, fmt.Errorf("verified scope %s fact %s: scopeId contradicts parent", sc.ScopeID, fact.FactID)
			}
			if fact.ProviderTypeID != "" && fact.ProviderTypeID != sc.ProviderTypeID {
				return nil, fmt.Errorf("verified scope %s fact %s: providerTypeId contradicts parent", sc.ScopeID, fact.FactID)
			}
			fact.ScopeID = sc.ScopeID
			fact.ProviderTypeID = sc.ProviderTypeID
		}
		c.scopes[sc.ScopeID] = sc
		if sc.Kind == "shared" {
			c.sharedScopes = append(c.sharedScopes, sc)
		}
	}
	return c, nil
}

func (c *Catalog) Descriptor(typeID domain.ID) (domain.ProviderDescriptor, bool) {
	d, ok := c.descriptors[typeID]
	return d, ok
}

// Descriptors returns the registered provider descriptors in stable order.
// Callers may serialize the result, so every mutable slice and raw schema is
// copied to keep the catalog immutable after construction.
func (c *Catalog) Descriptors() []domain.ProviderDescriptor {
	ids := make([]string, 0, len(c.descriptors))
	for id := range c.descriptors {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	out := make([]domain.ProviderDescriptor, 0, len(ids))
	for _, id := range ids {
		d := c.descriptors[domain.ID(id)]
		d.SupportedDomains = append([]string(nil), d.SupportedDomains...)
		d.ConfigSchema = append(json.RawMessage(nil), d.ConfigSchema...)
		d.ResourceSchema = append(json.RawMessage(nil), d.ResourceSchema...)
		d.CapabilitySchema = append(json.RawMessage(nil), d.CapabilitySchema...)
		d.ExtensionSchema = append(json.RawMessage(nil), d.ExtensionSchema...)
		d.EntitlementFacts = append([]domain.EntitlementFact(nil), d.EntitlementFacts...)
		d.Semantics = append([]domain.RegisteredSemantics(nil), d.Semantics...)
		for i := range d.Semantics {
			d.Semantics[i].AcceptedRevisions = append([]domain.ID(nil), d.Semantics[i].AcceptedRevisions...)
		}
		d.Profiles = append([]domain.RegisteredProfile(nil), d.Profiles...)
		for i := range d.Profiles {
			d.Profiles[i].RequiredDimensions = append([]domain.RequiredDimension(nil), d.Profiles[i].RequiredDimensions...)
		}
		out = append(out, d)
	}
	return out
}

// VerifiedScopes returns copies of the controlled local fixture scopes in
// stable order. This data is explicitly test evidence, never discovered
// account data from a live provider.
func (c *Catalog) VerifiedScopes() []domain.VerifiedScope {
	ids := make([]string, 0, len(c.scopes))
	for id := range c.scopes {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	out := make([]domain.VerifiedScope, 0, len(ids))
	for _, id := range ids {
		scope := *c.scopes[domain.ID(id)]
		scope.Aliases = append([]domain.ID(nil), scope.Aliases...)
		scope.Facts = append([]domain.VerifiedResourceFact(nil), scope.Facts...)
		out = append(out, scope)
	}
	return out
}

// SupportsDomain reports whether the provider type handles a business domain.
func (c *Catalog) SupportsDomain(typeID domain.ID, domainName string) bool {
	d, ok := c.descriptors[typeID]
	if !ok {
		return false
	}
	for _, dom := range d.SupportedDomains {
		if dom == domainName {
			return true
		}
	}
	return false
}

// ValidateExtensionsInContext validates an extension object against the
// CONTEXT schema (account/resource/binding/offer) — the generic extension
// whitelist alone is not a cross-object passport (Q1/C3).
func (c *Catalog) ValidateExtensionsInContext(typeID domain.ID, context string, raw json.RawMessage) []domain.FieldIssue {
	return c.validateExtensionsInContext(typeID, extensionContext(context), raw)
}

// ResolveScopeFact resolves a fact instance for a scope: direct scope first,
// then shared scopes aliasing the scope id. Dimensional ambiguity is the
// caller's concern; here factId picks exactly one instance.
func (c *Catalog) ResolveScopeFact(scopeID domain.ID, factID domain.ID) (domain.VerifiedResourceFact, bool) {
	if sc, ok := c.scopes[scopeID]; ok {
		for _, f := range sc.Facts {
			if f.FactID == factID {
				return f, true
			}
		}
	}
	for _, sc := range c.sharedScopes {
		aliased := false
		for _, a := range sc.Aliases {
			if a == scopeID {
				aliased = true
				break
			}
		}
		if !aliased {
			continue
		}
		for _, f := range sc.Facts {
			if f.FactID == factID {
				return f, true
			}
		}
	}
	return domain.VerifiedResourceFact{}, false
}

// ResolveDimensionalFact resolves a fact instance by dimensions
// (metricId+unit+canonical window) within the scope and its shared scopes.
// Exactly one match must exist; counts are returned for caller diagnostics.
func (c *Catalog) ResolveDimensionalFact(scopeID domain.ID, providerType domain.ID, metricID domain.ID, unit string, windowEquals func(domain.VerifiedResourceFact) bool) (domain.VerifiedResourceFact, int, bool) {
	matches := []domain.VerifiedResourceFact{}
	check := func(sc *domain.VerifiedScope) {
		if sc.ProviderTypeID != providerType {
			return
		}
		for _, f := range sc.Facts {
			if f.MetricID == metricID && f.Unit == unit && windowEquals(f) {
				matches = append(matches, f)
			}
		}
	}
	if sc, ok := c.scopes[scopeID]; ok {
		check(sc)
	}
	for _, sc := range c.sharedScopes {
		for _, a := range sc.Aliases {
			if a == scopeID {
				check(sc)
				break
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], 1, true
	}
	if len(matches) == 0 {
		return domain.VerifiedResourceFact{}, 0, false
	}
	return domain.VerifiedResourceFact{}, len(matches), false
}

// validateExtensionsInContext is the internal context-aware extension check.
func (c *Catalog) validateExtensionsInContext(typeID domain.ID, context extensionContext, raw json.RawMessage) []domain.FieldIssue {
	if _, ok := c.descriptors[typeID]; !ok {
		return []domain.FieldIssue{{Path: "", MessageKey: "ant.config.providerTypeNotRegistered"}}
	}
	if len(raw) == 0 {
		return nil
	}
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		return []domain.FieldIssue{{Path: "", MessageKey: "ant.config.extensionJSONInvalid"}}
	}
	if err := c.contextSchemas[typeID][context].Validate(node); err != nil {
		return []domain.FieldIssue{{Path: "", MessageKey: "ant.config.extensionSchemaViolation"}}
	}
	return nil
}
