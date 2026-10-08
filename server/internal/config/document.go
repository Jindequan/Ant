// Package config defines the versioned ConfigDocument (K1): the single root
// structure for accounts, bindings, resources, entitlement groups, offers,
// pools, grants, evidence references and non-executable unsupported drafts.
// Secrets are never part of the document — bindings reference credentialRefs
// only, and provider extensions are closed, schema-checked objects.
//
// Validation layering (R02): raw JSON shape (strict decode) → typed
// reference graph (ValidateStructural) → provider catalogue / schema checks
// (Validate with a ProviderCatalog). Saving a draft with issues is allowed;
// validated/activated states require zero issues (enforced by ConfigService
// in T2.1 — the boundary entry here is the single authority both sides feed).
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"ant/server/internal/domain"
	"ant/server/internal/jsonx"
)

const SchemaVersion = 1

type ConfigDocument struct {
	SchemaVersion     int                `json:"schemaVersion"`
	Accounts          []AccountDef       `json:"accounts"`
	Bindings          []BindingDef       `json:"bindings"`
	Resources         []ResourceDef      `json:"resources"`
	EntitlementGroups []EntitlementGroup `json:"entitlementGroups"`
	Offers            []OfferDef         `json:"offers"`
	Pools             []PoolDef          `json:"pools"`
	Grants            []GrantDef         `json:"grants"`
	Evidence          []EvidenceDef      `json:"evidence"`
	UnsupportedDrafts []UnsupportedDraft `json:"unsupportedDrafts"`
}

type AccountDef struct {
	AccountID          domain.ID       `json:"accountId"`
	ProviderTypeID     domain.ID       `json:"providerTypeId"`
	DisplayName        string          `json:"displayName"`
	Enabled            bool            `json:"enabled"`
	ProviderExtensions json.RawMessage `json:"providerExtensions,omitempty"`
}

type BindingDef struct {
	BindingID          domain.ID       `json:"bindingId"`
	AccountID          domain.ID       `json:"accountId"`
	Domain             string          `json:"domain"`
	Endpoint           string          `json:"endpoint"`
	CredentialRef      domain.ID       `json:"credentialRef"`
	CredentialRevision domain.ID       `json:"credentialRevision"`
	Enabled            bool            `json:"enabled"`
	CapabilityProfiles []domain.ID     `json:"capabilityProfiles"`
	ResourceRefs       []domain.ID     `json:"resourceRefs"`
	ProviderExtensions json.RawMessage `json:"providerExtensions,omitempty"`
}

type ResourceDef struct {
	ResourceID     domain.ID `json:"resourceId"`
	OwnerKind      string    `json:"ownerKind"` // account|binding
	OwnerID        domain.ID `json:"ownerId"`
	MetricID       domain.ID `json:"metricId"`
	Unit           string    `json:"unit"`
	QuantumExp     int       `json:"quantumExp"`
	Kind           string    `json:"kind"`           // counter|occupancy
	CommercialRole string    `json:"commercialRole"` // limit|entitlement
	// ProviderFactID (R03, optional): the provider-verified commercial bucket
	// identity this resource accounts for. It comes from the registered
	// provider descriptor's EntitlementFacts — never user-invented or random.
	// When set, (owner, providerFactID) identifies ONE physical bucket; a
	// second resource with the same pair is a duplicate. Independent buckets
	// of the same owner/metric are declared by distinct providerFactIDs and
	// (for waterfall ordering) membership in one entitlement group with
	// distinct providerOrder steps.
	ProviderFactID          domain.ID       `json:"providerFactId,omitempty"`
	SemanticsRef            domain.ID       `json:"semanticsRef"`
	SemanticsRevision       domain.ID       `json:"semanticsRevision"`
	Window                  WindowDef       `json:"window"`
	Metered                 bool            `json:"metered"`
	Currency                string          `json:"currency,omitempty"`
	ProviderOrder           *int64          `json:"providerOrder,omitempty"`
	ConfiguredCapacity      string          `json:"configuredCapacity"` // canonical decimal string, >= 0
	CapacityEvidenceRef     domain.ID       `json:"capacityEvidenceRef"`
	InitialStateEvidenceRef domain.ID       `json:"initialStateEvidenceRef,omitempty"`
	ExpiresAtMillis         *int64          `json:"expiresAtMillis,omitempty"`
	RefillRule              json.RawMessage `json:"refillRule,omitempty"`
	EntitlementGroupID      domain.ID       `json:"entitlementGroupId,omitempty"`
	ProviderExtensions      json.RawMessage `json:"providerExtensions,omitempty"`
}

type WindowDef struct {
	Kind           string `json:"kind"` // fixed|rolling|lifetime
	Period         string `json:"period,omitempty"`
	TimeZone       string `json:"timeZone,omitempty"`
	AnchorISO      string `json:"anchorISO,omitempty"`
	DurationMillis int64  `json:"durationMillis,omitempty"`
}

type EntitlementGroup struct {
	GroupID            domain.ID              `json:"groupId"`
	ScopeKind          string                 `json:"scopeKind"` // account|binding
	ScopeID            domain.ID              `json:"scopeId"`
	MetricID           domain.ID              `json:"metricId"`
	Unit               string                 `json:"unit"`
	Order              []EntitlementOrderStep `json:"order"`
	SelectorEvidenceID domain.ID              `json:"selectorEvidenceId,omitempty"`
}

type EntitlementOrderStep struct {
	ResourceID    domain.ID `json:"resourceId"`
	ProviderOrder int64     `json:"providerOrder"`
}

type OfferDef struct {
	OfferID              domain.ID   `json:"offerId"`
	BindingID            domain.ID   `json:"bindingId"`
	Domain               string      `json:"domain"`
	CapabilityProfile    domain.ID   `json:"capabilityProfile"`
	Enabled              bool        `json:"enabled"`
	ResourceRefs         []domain.ID `json:"resourceRefs"`
	RequiredEvidenceRefs []domain.ID `json:"requiredEvidenceRefs"`
}

type PoolDef struct {
	PoolID    domain.ID       `json:"poolId"`
	Namespace domain.ID       `json:"namespace"`
	Name      string          `json:"name"`
	Enabled   bool            `json:"enabled"`
	Members   []PoolMemberDef `json:"members"`
}

type PoolMemberDef struct {
	OfferID  domain.ID `json:"offerId"`
	Priority int64     `json:"priority"`
	Enabled  bool      `json:"enabled"`
}

type GrantDef struct {
	PrincipalID domain.ID `json:"principalId"`
	Namespace   domain.ID `json:"namespace"`
	Permission  string    `json:"permission"`
	ScopeKind   string    `json:"scopeKind"` // namespace|binding|account|pool|offer
	ScopeID     domain.ID `json:"scopeId"`
}

type EvidenceDef struct {
	EvidenceID       domain.ID         `json:"evidenceId"`
	Kind             string            `json:"kind"` // bound|initial_state|hard_stop|guard|entitlement_selector
	SubjectRef       domain.ID         `json:"subjectRef"`
	SourceRef        string            `json:"sourceRef"`
	VerifiedAtMillis domain.Millis     `json:"verifiedAtMillis"`
	ExpiresAtMillis  domain.Millis     `json:"expiresAtMillis"`
	Payload          map[string]string `json:"payload"` // string map only; no secret material.
}

// UnsupportedDraft holds descriptions of provider types not implemented yet.
// They are explicitly non-executable: activation ignores them, no binding or
// offer may point at them, and the UI must label them “尚不可执行”.
type UnsupportedDraft struct {
	DraftID          domain.ID     `json:"draftId"`
	ProviderTypeID   domain.ID     `json:"providerTypeId"`
	Description      string        `json:"description"`
	Reason           string        `json:"reason"`
	CapturedAtMillis domain.Millis `json:"capturedAtMillis"`
}

// ProviderCatalog is the registered provider directory (R12/S1). Configuration
// support judgments come from here, not from document shape alone. The
// boundary entry Validate consumes it; ValidateStructural stays catalog-free
// so the reference graph is testable on its own.
type ProviderCatalog interface {
	// Descriptor returns the registered descriptor for a provider type,
	// including ALL schema classes, registered semantics and profiles.
	Descriptor(typeID domain.ID) (domain.ProviderDescriptor, bool)
	// SupportsDomain reports whether the type handles a business domain;
	// bindings and offers on unsupported domains are not executable.
	SupportsDomain(typeID domain.ID, domainName string) bool
	// ValidateExtensionsInContext validates an extension object against the
	// CONTEXT schema of the entity class (account/resource/binding/offer);
	// the generic whitelist is not a cross-object passport (Q1/C3).
	ValidateExtensionsInContext(typeID domain.ID, context string, raw json.RawMessage) []domain.FieldIssue
	// ResolveScopeFact resolves a fact instance by scope + factId.
	ResolveScopeFact(scopeID domain.ID, factID domain.ID) (domain.VerifiedResourceFact, bool)
	// ResolveDimensionalFact resolves by (metricId, unit, executable-equal
	// window); ok=false with count>1 means ambiguous, count=0 unresolved.
	ResolveDimensionalFact(scopeID domain.ID, providerType domain.ID, metricID domain.ID, unit string, windowEquals func(domain.VerifiedResourceFact) bool) (domain.VerifiedResourceFact, int, bool)
}

// DocumentValidation is the boundary result. Issues with zero length mean
// the document may proceed to validated/activated states; a non-empty list
// means the draft may be SAVED for repair but is not validated. Compiled is
// non-nil exactly when zero issues: it is the consumable execution input
// (Q1) — structural validity alone never promises executability.
type DocumentValidation struct {
	Doc      *ConfigDocument
	Issues   []domain.FieldIssue
	Compiled *CompiledConfig
}

// Validate is the single Go boundary entry (B2/S1): strict raw-JSON decode →
// ROOT JSON Schema (executable required/null rules; the schema is compiled
// from an embedded copy of contracts/schema/config-document.schema.json,
// byte-equality tested) → typed reference graph → catalogue/context checks.
// Web consumes the same fixtures and expectations; the server stays
// authoritative. Drafts with issues are savable, never executable.
func Validate(data []byte, cat ProviderCatalog) DocumentValidation {
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return DocumentValidation{Issues: []domain.FieldIssue{
			{Path: "/", MessageKey: "ant.config.decodeFailed"},
		}}
	}
	var issues []domain.FieldIssue
	if err := rootSchemaOnce(); err != nil {
		issues = append(issues, domain.FieldIssue{Path: "/", MessageKey: "ant.config.rootSchemaUnavailable"})
		return DocumentValidation{Issues: issues}
	}
	if err := rootSchemaRef.Validate(raw); err != nil {
		issues = append(issues, domain.FieldIssue{
			Path:       schemaErrorPath(err),
			MessageKey: "ant.config.rootSchemaViolation",
		})
		// Shape failures make the typed layers meaningless; stop here but
		// keep the document parseable for the UI to render repair hints.
	}
	var doc ConfigDocument
	if err := jsonx.DecodeStrict(data, &doc); err != nil {
		issues = append(issues, domain.FieldIssue{
			Path:       "/",
			MessageKey: "ant.config.decodeFailed",
		})
		return DocumentValidation{Issues: issues}
	}
	issues = append(issues, ValidateStructural(&doc)...)
	catalogIssues, compiledResources := validateCatalog(&doc, cat)
	issues = append(issues, catalogIssues...)
	res := DocumentValidation{Doc: &doc, Issues: issues}
	if len(issues) == 0 {
		cfg := &CompiledConfig{}
		for _, cr := range compiledResources {
			cfg.Resources = append(cfg.Resources, *cr)
		}
		for i := range doc.Offers {
			o := &doc.Offers[i]
			co := CompiledOffer{OfferID: o.OfferID, Domain: o.Domain, CapabilityProfile: o.CapabilityProfile}
			for _, ref := range o.ResourceRefs {
				if cr, ok := compiledResources[ref]; ok {
					co.PhysicalKeys = append(co.PhysicalKeys, cr.PhysicalKey)
				}
			}
			cfg.Offers = append(cfg.Offers, co)
		}
		res.Compiled = cfg
	}
	return res
}

// Compile is the full-boundary entry that also produces the consumable
// execution input for zero-issue documents.
func Compile(data []byte, cat ProviderCatalog) DocumentValidation {
	return Validate(data, cat)
}

// schemaErrorPath extracts a stable, correctly escaped JSON Pointer for the
// schema violation (C4): walk the cause chain to the DEEPEST error, render
// its instance location tokens with RFC 6901 escaping (~0/~1), and for
// required-type errors append the missing property so the issue locates the
// exact field the user must repair.
func schemaErrorPath(err error) string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return "/"
	}
	deepest := ve
	for len(deepest.Causes) > 0 {
		deepest = deepest.Causes[0]
	}
	var sb strings.Builder
	sb.WriteByte('/')
	for _, tok := range deepest.InstanceLocation {
		sb.WriteString(escapePointerToken(tok))
		sb.WriteByte('/')
	}
	pointer := sb.String()
	pointer = strings.TrimSuffix(pointer, "/")
	if pointer == "" {
		pointer = "/"
	}
	if req, ok := deepest.ErrorKind.(*kind.Required); ok && len(req.Missing) > 0 {
		if !strings.HasSuffix(pointer, "/") {
			pointer += "/"
		}
		pointer += escapePointerToken(req.Missing[0])
	}
	return pointer
}

// escapePointerToken applies RFC 6901 escaping to one reference token.
func escapePointerToken(tok string) string {
	tok = strings.ReplaceAll(tok, "~", "~0")
	tok = strings.ReplaceAll(tok, "/", "~1")
	return tok
}

var (
	rootSchemaRef     *jsonschema.Schema
	rootSchemaInitErr error
	rootSchemaInit    = sync.OnceValue(func() error {
		var doc any
		if err := json.Unmarshal(rootConfigSchema, &doc); err != nil {
			rootSchemaInitErr = fmt.Errorf("root schema json: %w", err)
			return rootSchemaInitErr
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("config-document.schema.json", doc); err != nil {
			rootSchemaInitErr = fmt.Errorf("root schema: %w", err)
			return rootSchemaInitErr
		}
		rootSchemaRef, rootSchemaInitErr = compiler.Compile("config-document.schema.json")
		return rootSchemaInitErr
	})
)

func rootSchemaOnce() error { return rootSchemaInit() }

// validateCatalog applies registered-directory checks and the Q1 resolution
// identity: provider types must be registered, context schemas applied,
// domains supported, every resource resolved to exactly one verified fact
// instance whose dimensions constrain the configuration, and offers compiled
// against the profile's required dimensions. Nil catalog fails closed.
func validateCatalog(doc *ConfigDocument, cat ProviderCatalog) ([]domain.FieldIssue, map[domain.ID]*CompiledResource) {
	if cat == nil {
		return []domain.FieldIssue{{Path: "/", MessageKey: "ant.config.catalogUnavailable"}}, nil
	}
	v := &validator{}
	supported := map[domain.ID]bool{}
	for i := range doc.UnsupportedDrafts {
		supported[doc.UnsupportedDrafts[i].ProviderTypeID] = true
	}
	accountByID := map[domain.ID]*AccountDef{}
	for i := range doc.Accounts {
		accountByID[doc.Accounts[i].AccountID] = &doc.Accounts[i]
	}
	for i := range doc.Accounts {
		a := &doc.Accounts[i]
		p := "/accounts/" + itoa(i)
		if _, ok := cat.Descriptor(a.ProviderTypeID); !ok {
			key := "ant.config.providerTypeNotRegistered"
			if supported[a.ProviderTypeID] {
				key = "ant.config.unsupportedProviderExecutable"
			}
			v.issue(p+"/providerTypeId", key)
			continue
		}
		if iss := cat.ValidateExtensionsInContext(a.ProviderTypeID, "account", a.ProviderExtensions); len(iss) > 0 {
			v.issues = append(v.issues, prefixIssues(p+"/providerExtensions", iss)...)
		}
	}
	bindingAccount := map[domain.ID]domain.ID{}
	for i := range doc.Bindings {
		b := &doc.Bindings[i]
		p := "/bindings/" + itoa(i)
		acct := accountByID[b.AccountID]
		if acct == nil {
			continue // unknownAccount already reported structurally
		}
		bindingAccount[b.BindingID] = b.AccountID
		if !cat.SupportsDomain(acct.ProviderTypeID, b.Domain) {
			v.issue(p+"/domain", "unsupportedDomain")
		}
		// Registered capability profile (Q1/C3): every declared profile must
		// exist in the catalogue AND match the binding's domain (D-C3-v3).
		// Iterating the declarations (never indexing before the length check)
		// means an empty or missing declaration is a structural issue already,
		// never a panic here.
		if desc, ok := cat.Descriptor(acct.ProviderTypeID); ok {
			for _, cp := range b.CapabilityProfiles {
				registered := false
				for _, prof := range desc.Profiles {
					if prof.ProfileID == cp {
						registered = true
						if prof.Domain != b.Domain {
							v.issue(p+"/capabilityProfiles", "profileDomainMismatch")
						}
						break
					}
				}
				if !registered {
					v.issue(p+"/capabilityProfiles", "unregisteredCapabilityProfile")
				}
			}
		}
		if iss := cat.ValidateExtensionsInContext(acct.ProviderTypeID, "binding", b.ProviderExtensions); len(iss) > 0 {
			v.issues = append(v.issues, prefixIssues(p+"/providerExtensions", iss)...)
		}
	}

	// Resource resolution identity (Q1/C1): every resource resolves to
	// exactly one verified fact instance whose dimensions constrain it.
	compiledResources := map[domain.ID]*CompiledResource{}
	for i := range doc.Resources {
		r := &doc.Resources[i]
		p := resourcePath(doc, r.ResourceID)
		fact, ok := resolveResource(doc, cat, r, v)
		if !ok {
			continue
		}
		// duplicate physical key across resources = clone
		for _, other := range compiledResources {
			if other.PhysicalKey == fact.PhysicalKey {
				v.issue(p+"/providerFactId", "duplicateCommercialFact")
			}
		}
		verifyFactConstrainsResource(v, p, r, fact)
		compileConstraintErrors(v, p, r, fact, func(id domain.ID) *EvidenceDef {
			for j := range doc.Evidence {
				if doc.Evidence[j].EvidenceID == id {
					return &doc.Evidence[j]
				}
			}
			return nil
		})
		// Registered semantics (Q1/C3): the semantics reference and revision
		// must be registered for this provider.
		providerType := providerTypeOfAccount(doc, func() domain.ID {
			if r.OwnerKind == "account" {
				return r.OwnerID
			}
			return bindingAccountID(doc, r.OwnerID)
		}())
		var trustedKind, trustedRole string
		if fact.ProviderTypeID != providerType {
			v.issue(p+"/ownerId", "providerFactMetadataMismatch")
		}
		if desc, ok := cat.Descriptor(providerType); ok {
			matches := 0
			for _, sem := range desc.Semantics {
				if sem.SemanticsRef != fact.SemanticsRef {
					continue
				}
				for _, rev := range sem.AcceptedRevisions {
					if rev == fact.SemanticsRevision {
						matches++
						trustedKind, trustedRole = sem.ResourceKind, sem.CommercialRole
						break
					}
				}
			}
			if matches != 1 || (trustedKind != "counter" && trustedKind != "occupancy") ||
				(trustedRole != "limit" && trustedRole != "entitlement") {
				v.issue(p+"/semanticsRef", "unregisteredResourceSemantics")
			} else {
				if r.Kind != trustedKind {
					v.issue(p+"/kind", "providerFactMetadataMismatch")
				}
				if r.CommercialRole != trustedRole {
					v.issue(p+"/commercialRole", "providerFactMetadataMismatch")
				}
			}
		}
		if iss := cat.ValidateExtensionsInContext(providerType, "resource", r.ProviderExtensions); len(iss) > 0 {
			v.issues = append(v.issues, prefixIssues(p+"/providerExtensions", iss)...)
		}
		var providerOrder *int64 = r.ProviderOrder
		if r.EntitlementGroupID != "" {
			providerOrder = nil
			for groupIndex := range doc.EntitlementGroups {
				group := &doc.EntitlementGroups[groupIndex]
				if group.GroupID != r.EntitlementGroupID {
					continue
				}
				for _, step := range group.Order {
					if step.ResourceID == r.ResourceID {
						order := step.ProviderOrder
						providerOrder = &order
						break
					}
				}
			}
		}
		compiledResources[r.ResourceID] = &CompiledResource{
			ResourceID:         r.ResourceID,
			PhysicalKey:        fact.PhysicalKey,
			ScopeID:            fact.ScopeID,
			OwnerKind:          r.OwnerKind,
			OwnerID:            r.OwnerID,
			MetricID:           fact.MetricID,
			Unit:               fact.Unit,
			QuantumExp:         fact.QuantumExp,
			Currency:           fact.Currency,
			Metered:            fact.Metered,
			Kind:               trustedKind,
			CommercialRole:     trustedRole,
			ProviderOrder:      providerOrder,
			EntitlementGroupID: r.EntitlementGroupID,
			SemanticsRef:       fact.SemanticsRef,
			SemanticsRevision:  fact.SemanticsRevision,
			Window:             fact.WindowRule(),
			Capacity:           mustCapacity(r.ConfiguredCapacity),
			InitialRemaining:   fact.InitialRemaining,
			EvidenceRefs:       resourceEvidenceRefs(r),
		}
	}

	// Offer compilation (Q1/C3): registered profile, supported domain, and
	// full required-dimension coverage by resolved resources.
	for i := range doc.Offers {
		o := &doc.Offers[i]
		p := "/offers/" + itoa(i)
		bIdx := bindingIndexByID(doc, o.BindingID)
		if bIdx < 0 {
			continue
		}
		acct := accountByID[doc.Bindings[bIdx].AccountID]
		if acct == nil {
			continue
		}
		if !cat.SupportsDomain(acct.ProviderTypeID, o.Domain) {
			v.issue(p+"/domain", "unsupportedDomain")
		}
		desc, descOK := cat.Descriptor(acct.ProviderTypeID)
		if !descOK {
			continue
		}
		var profile *domain.RegisteredProfile
		for pi := range desc.Profiles {
			if desc.Profiles[pi].ProfileID == o.CapabilityProfile {
				profile = &desc.Profiles[pi]
				break
			}
		}
		if profile == nil {
			v.issue(p+"/capabilityProfile", "unregisteredCapabilityProfile")
			continue
		}
		if profile.Domain != o.Domain {
			v.issue(p+"/capabilityProfile", "profileDomainMismatch")
		}
		// Occupancy is a storage kind; limit/entitlement are commercial roles.
		// An occupancy/limit resource satisfies both corresponding views of
		// its metric. Never discard its commercial role when checking a bundle.
		covered := map[string]bool{} // metricId|role -> covered
		for _, ref := range o.ResourceRefs {
			if cr, ok := compiledResources[ref]; ok {
				covered[string(cr.MetricID)+"|"+cr.CommercialRole] = true
				if cr.Kind == "occupancy" {
					covered[string(cr.MetricID)+"|occupancy"] = true
				}
			}
		}
		missing := false
		for _, dim := range profile.RequiredDimensions {
			if !covered[string(dim.MetricID)+"|"+dim.Role] {
				missing = true
			}
		}
		if missing {
			v.issue(p+"/resourceRefs", "incompleteRequiredBundle")
		}
	}
	return v.issues, compiledResources
}

func mustCapacity(raw string) domain.Units {
	q, err := domain.ParseUnits(raw)
	if err != nil {
		return 0
	}
	return q
}

func resourceEvidenceRefs(r *ResourceDef) []domain.ID {
	var refs []domain.ID
	if r.CapacityEvidenceRef != "" {
		refs = append(refs, r.CapacityEvidenceRef)
	}
	if r.InitialStateEvidenceRef != "" {
		refs = append(refs, r.InitialStateEvidenceRef)
	}
	return refs
}

func prefixIssues(prefix string, issues []domain.FieldIssue) []domain.FieldIssue {
	out := make([]domain.FieldIssue, 0, len(issues))
	for _, i := range issues {
		out = append(out, domain.FieldIssue{Path: prefix + i.Path, MessageKey: i.MessageKey})
	}
	return out
}
