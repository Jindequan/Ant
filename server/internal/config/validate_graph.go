package config

import (
	"strings"
	"time"

	"ant/server/internal/domain"
)

// Typed reference graph validation (R02). Order of passes: identity/shape →
// entity-local rules → cross-entity edges → commercial identity (R03) →
// evidence typing. Every edge below has a negative fixture in
// contracts/fixtures/ (config-document-invalid-*) and in the shared audit
// config-cases; a new edge without a negative example is not "done".
func ValidateStructural(doc *ConfigDocument) []domain.FieldIssue {
	v := &validator{}
	v.run(doc)
	return v.issues
}

type validator struct {
	issues []domain.FieldIssue
}

func (v *validator) issue(path, key string) {
	v.issues = append(v.issues, domain.FieldIssue{Path: path, MessageKey: "ant.config." + key})
}

func (v *validator) run(doc *ConfigDocument) {
	if doc.SchemaVersion != SchemaVersion {
		v.issue("/schemaVersion", "schemaVersionUnsupported")
	}
	// Root arrays must be PRESENT (nil = absent = invalid); an empty array is
	// an explicit "none" and acceptable. This mirrors the JSON Schema
	// "required" on the Go side (R02: shape parity).
	for path, present := range map[string]bool{
		"/accounts":          doc.Accounts != nil,
		"/bindings":          doc.Bindings != nil,
		"/resources":         doc.Resources != nil,
		"/entitlementGroups": doc.EntitlementGroups != nil,
		"/offers":            doc.Offers != nil,
		"/pools":             doc.Pools != nil,
		"/grants":            doc.Grants != nil,
		"/evidence":          doc.Evidence != nil,
		"/unsupportedDrafts": doc.UnsupportedDrafts != nil,
	} {
		if !present {
			v.issue(path, "requiredArrayMissing")
		}
	}

	unsupported := map[domain.ID]bool{}
	for i := range doc.UnsupportedDrafts {
		unsupported[doc.UnsupportedDrafts[i].ProviderTypeID] = true
	}

	accounts := map[domain.ID]bool{}
	for i := range doc.Accounts {
		a := &doc.Accounts[i]
		p := "/accounts/" + itoa(i)
		if !validID(a.AccountID) || accounts[a.AccountID] {
			v.issue(p+"/accountId", "invalidOrDuplicateId")
		}
		accounts[a.AccountID] = true
		if !validID(a.ProviderTypeID) {
			v.issue(p+"/providerTypeId", "invalidId")
		}
		if unsupported[a.ProviderTypeID] {
			v.issue(p+"/providerTypeId", "unsupportedProviderExecutable")
		}
		v.checkNoSecrets(p+"/providerExtensions", a.ProviderExtensions)
	}

	bindings := map[domain.ID]bool{}
	bindingAccounts := map[domain.ID]domain.ID{}
	for i := range doc.Bindings {
		b := &doc.Bindings[i]
		p := "/bindings/" + itoa(i)
		if !validID(b.BindingID) || bindings[b.BindingID] {
			v.issue(p+"/bindingId", "invalidOrDuplicateId")
		}
		bindings[b.BindingID] = true
		if !accounts[b.AccountID] {
			v.issue(p+"/accountId", "unknownAccount")
		} else {
			bindingAccounts[b.BindingID] = b.AccountID
		}
		if b.Domain == "" || b.Endpoint == "" {
			v.issue(p, "domainAndEndpointRequired")
		}
		if !validID(b.CredentialRef) || !validID(b.CredentialRevision) {
			v.issue(p+"/credentialRef", "credentialRefRequired")
		}
		v.checkIDList(p+"/capabilityProfiles", b.CapabilityProfiles, nil)
		v.checkNoSecrets(p+"/providerExtensions", b.ProviderExtensions)
	}

	resources := map[domain.ID]bool{}
	resourceByID := map[domain.ID]*ResourceDef{}
	for i := range doc.Resources {
		r := &doc.Resources[i]
		p := "/resources/" + itoa(i)
		if !validID(r.ResourceID) || resources[r.ResourceID] {
			v.issue(p+"/resourceId", "invalidOrDuplicateId")
		}
		resources[r.ResourceID] = true
		resourceByID[r.ResourceID] = r
		// ownerId must resolve according to ownerKind (R02 edge).
		switch r.OwnerKind {
		case "account":
			if !accounts[r.OwnerID] {
				v.issue(p+"/ownerId", "unknownOwner")
			}
		case "binding":
			if !bindings[r.OwnerID] {
				v.issue(p+"/ownerId", "unknownOwner")
			}
		default:
			v.issue(p+"/ownerKind", "ownerKindInvalid")
		}
		if !validID(r.MetricID) {
			v.issue(p+"/metricId", "invalidId")
		}
		if !validUnit(r.Unit) {
			v.issue(p+"/unit", "unitInvalid")
		}
		if r.Currency != "" && len(r.Currency) != 3 {
			v.issue(p+"/currency", "currencyInvalid")
		}
		if r.QuantumExp < 0 || r.QuantumExp > 18 {
			v.issue(p+"/quantumExp", "quantumExpRange")
		}
		if r.Kind != "counter" && r.Kind != "occupancy" {
			v.issue(p+"/kind", "kindInvalid")
		}
		if r.CommercialRole != "limit" && r.CommercialRole != "entitlement" {
			v.issue(p+"/commercialRole", "commercialRoleInvalid")
		}
		if r.ProviderFactID != "" && !validID(r.ProviderFactID) {
			v.issue(p+"/providerFactId", "invalidId")
		}
		if q, err := domain.ParseUnits(r.ConfiguredCapacity); err != nil || q < 0 {
			v.issue(p+"/configuredCapacity", "quantityInvalid")
		}
		if !validID(r.CapacityEvidenceRef) {
			v.issue(p+"/capacityEvidenceRef", "capacityEvidenceRequired")
		}
		v.checkWindow(p+"/window", &r.Window)
		v.checkNoSecrets(p+"/refillRule", r.RefillRule)
		v.checkNoSecrets(p+"/providerExtensions", r.ProviderExtensions)
	}

	// binding.resourceRefs existence needs the full resource set (R02 edge).
	for i := range doc.Bindings {
		v.checkIDList("/bindings/"+itoa(i)+"/resourceRefs", doc.Bindings[i].ResourceRefs, resources)
	}

	v.checkEntitlementGroups(doc, resources, resourceByID)
	v.checkEntitlementGroupMembership(doc)

	offers := map[domain.ID]bool{}
	for i := range doc.Offers {
		o := &doc.Offers[i]
		p := "/offers/" + itoa(i)
		if !validID(o.OfferID) || offers[o.OfferID] {
			v.issue(p+"/offerId", "invalidOrDuplicateId")
		}
		offers[o.OfferID] = true
		bIdx := bindingIndexByID(doc, o.BindingID)
		if !validID(o.BindingID) || bIdx < 0 {
			v.issue(p+"/bindingId", "unknownBinding")
		} else {
			b := &doc.Bindings[bIdx]
			// The offer executes on its binding's path: domain and capability
			// must match the binding declaration (R02 edges).
			if o.Domain != b.Domain {
				v.issue(p+"/domain", "offerDomainMismatch")
			}
			if !containsID(b.CapabilityProfiles, o.CapabilityProfile) {
				v.issue(p+"/capabilityProfile", "offerCapabilityMismatch")
			}
			if !validID(o.CapabilityProfile) {
				v.issue(p+"/capabilityProfile", "invalidId")
			}
		}
		if o.Domain == "" {
			v.issue(p, "domainAndCapabilityRequired")
		}
		v.checkIDList(p+"/resourceRefs", o.ResourceRefs, resources)
		if len(o.ResourceRefs) == 0 {
			v.issue(p+"/resourceRefs", "bundleRequired")
		}
		// The offer's bundle cannot exceed what its binding references.
		if bIdx >= 0 {
			allowed := map[domain.ID]bool{}
			for _, id := range doc.Bindings[bIdx].ResourceRefs {
				allowed[id] = true
			}
			for j, id := range o.ResourceRefs {
				if !allowed[id] {
					v.issue(p+"/resourceRefs/"+itoa(j), "resourceNotOnBinding")
				}
			}
		}
		v.checkIDList(p+"/requiredEvidenceRefs", o.RequiredEvidenceRefs, v.evidenceKinds(doc, "bound"))
	}

	v.checkPools(doc, offers)
	v.checkGrants(doc)
	v.checkEvidence(doc, accounts, bindings, resources, resourceByID)

	for i := range doc.UnsupportedDrafts {
		d := &doc.UnsupportedDrafts[i]
		p := "/unsupportedDrafts/" + itoa(i)
		if !validID(d.DraftID) || !validID(d.ProviderTypeID) {
			v.issue(p, "invalidId")
		}
	}
}

// providerTypeOf resolves a resource's provider type via its owner
// (account, or the binding's account); "" when the owner is unknown (that
// mismatch is already reported by the structural pass).
func (v *validator) providerTypeOf(doc *ConfigDocument, r *ResourceDef) domain.ID {
	var accountID domain.ID
	switch r.OwnerKind {
	case "account":
		accountID = r.OwnerID
	case "binding":
		for i := range doc.Bindings {
			if doc.Bindings[i].BindingID == r.OwnerID {
				accountID = doc.Bindings[i].AccountID
				break
			}
		}
	}
	for i := range doc.Accounts {
		if doc.Accounts[i].AccountID == accountID {
			return doc.Accounts[i].ProviderTypeID
		}
	}
	return ""
}

func (v *validator) checkEntitlementGroups(doc *ConfigDocument, resources map[domain.ID]bool, byID map[domain.ID]*ResourceDef) {
	groups := map[domain.ID]bool{}
	groupIdentity := map[string]bool{} // scopeKind|scopeId|metricId|unit
	for i := range doc.EntitlementGroups {
		g := &doc.EntitlementGroups[i]
		p := "/entitlementGroups/" + itoa(i)
		if !validID(g.GroupID) || groups[g.GroupID] {
			v.issue(p+"/groupId", "invalidOrDuplicateId")
		}
		groups[g.GroupID] = true
		if g.ScopeKind != "account" && g.ScopeKind != "binding" {
			v.issue(p+"/scopeKind", "scopeKindInvalid")
		}
		// scopeId must resolve per scopeKind (R02 edge).
		switch g.ScopeKind {
		case "account":
			if !accountExists(doc, g.ScopeID) {
				v.issue(p+"/scopeId", "unknownScope")
			}
		case "binding":
			if !bindingExists(doc, g.ScopeID) {
				v.issue(p+"/scopeId", "unknownScope")
			}
		}
		if !validID(g.MetricID) || !validUnit(g.Unit) {
			v.issue(p, "invalidGroupIdentity")
		}
		identity := g.ScopeKind + "|" + string(g.ScopeID) + "|" + string(g.MetricID) + "|" + g.Unit
		if groupIdentity[identity] {
			v.issue(p, "duplicateEntitlementGroup")
		}
		groupIdentity[identity] = true
		orders := map[int64]bool{}
		seen := map[domain.ID]bool{}
		var first *ResourceDef
		if g.SelectorEvidenceID != "" {
			// The selector evidence must exist, be of the selector kind, AND
			// certify THIS group: subjectRef == groupId (B3/S4 P1-1 — one
			// group's authority evidence must not back another group's order).
			if ev := v.evidenceByID(doc, g.SelectorEvidenceID); ev == nil {
				v.issue(p+"/selectorEvidenceId", "unknownEvidence")
			} else if ev.Kind != "entitlement_selector" {
				v.issue(p+"/selectorEvidenceId", "evidenceSubjectTypeMismatch")
			} else if ev.SubjectRef != g.GroupID {
				v.issue(p+"/selectorEvidenceId", "evidenceNotApplicable")
			}
		}
		for j := range g.Order {
			s := &g.Order[j]
			sp := p + "/order/" + itoa(j)
			if !resources[s.ResourceID] || seen[s.ResourceID] {
				v.issue(sp+"/resourceId", "unknownOrDuplicateResource")
				continue
			}
			seen[s.ResourceID] = true
			if orders[s.ProviderOrder] {
				v.issue(sp+"/providerOrder", "providerOrderDuplicate")
			}
			orders[s.ProviderOrder] = true
			r := byID[s.ResourceID]
			if r == nil {
				continue
			}
			// Group members are the declared independent buckets: same
			// metric/unit as the group, owned by the scope, enrolled in the
			// group, distinct providerFactIds, order owned by the group.
			if r.MetricID != g.MetricID || r.Unit != g.Unit {
				v.issue(sp+"/resourceId", "groupMetricUnitMismatch")
			}
			// Quantity-space equality (B3): same unit TEXT with different
			// precision or currency is not the same quantity space; implicit
			// mixing is forbidden. Group members participate in one
			// waterfall, so they must be directly comparable.
			if first != nil {
				if first.QuantumExp != r.QuantumExp {
					v.issue(sp+"/resourceId", "groupQuantumMismatch")
				}
				if first.Currency != r.Currency {
					v.issue(sp+"/resourceId", "groupCurrencyMismatch")
				}
			} else {
				first = r
			}
			if g.ScopeKind == "account" && (r.OwnerKind != "account" || r.OwnerID != g.ScopeID) {
				v.issue(sp+"/resourceId", "groupScopeOwnerMismatch")
			}
			if g.ScopeKind == "binding" && (r.OwnerKind != "binding" || r.OwnerID != g.ScopeID) {
				v.issue(sp+"/resourceId", "groupScopeOwnerMismatch")
			}
			if r.EntitlementGroupID != g.GroupID {
				v.issue(sp+"/resourceId", "resourceNotInGroup")
			}
			if r.ProviderOrder != nil {
				v.issue("/resources/"+resourceIndex(doc, s.ResourceID)+"/providerOrder", "providerOrderOwnedByGroup")
			}
		}
	}
}

func (v *validator) checkPools(doc *ConfigDocument, offers map[domain.ID]bool) {
	pools := map[domain.ID]bool{}
	for i := range doc.Pools {
		pl := &doc.Pools[i]
		p := "/pools/" + itoa(i)
		if !validID(pl.PoolID) || pools[pl.PoolID] {
			v.issue(p+"/poolId", "invalidOrDuplicateId")
		}
		pools[pl.PoolID] = true
		if pl.Namespace == "" {
			v.issue(p+"/namespace", "namespaceRequired")
		}
		members := map[domain.ID]bool{}
		for j := range pl.Members {
			m := &pl.Members[j]
			mp := p + "/members/" + itoa(j)
			if !offers[m.OfferID] || members[m.OfferID] {
				v.issue(mp+"/offerId", "unknownOrDuplicateOffer")
			}
			members[m.OfferID] = true
			if m.Priority < 0 || m.Priority > 2147483647 {
				v.issue(mp+"/priority", "priorityRange")
			}
		}
	}
}

func (v *validator) checkGrants(doc *ConfigDocument) {
	seen := map[string]bool{}
	for i := range doc.Grants {
		g := &doc.Grants[i]
		p := "/grants/" + itoa(i)
		switch g.Permission {
		case string(domain.PermExecute), string(domain.PermRead), string(domain.PermCancel),
			string(domain.PermManage), string(domain.PermConfigure):
		default:
			v.issue(p+"/permission", "permissionInvalid")
		}
		switch g.ScopeKind {
		case "namespace", "binding", "account", "pool", "offer":
		default:
			v.issue(p+"/scopeKind", "scopeKindInvalid")
		}
		if g.Namespace == "" || !validID(g.PrincipalID) || !validID(g.ScopeID) {
			v.issue(p, "invalidGrantIdentity")
		}
		// The scope target must EXIST and belong to the grant's namespace
		// where the target carries one (B3: dangling targets and namespace
		// mismatches are authorization errors, not cosmetic).
		switch g.ScopeKind {
		case "namespace":
			if g.ScopeID != g.Namespace {
				v.issue(p+"/scopeId", "grantScopeNamespaceMismatch")
			}
		case "pool":
			idx := poolIndexByID(doc, g.ScopeID)
			if idx < 0 {
				v.issue(p+"/scopeId", "unknownGrantScope")
			} else if doc.Pools[idx].Namespace != g.Namespace {
				v.issue(p+"/scopeId", "grantScopeNamespaceMismatch")
			}
		case "offer":
			if !offerExists(doc, g.ScopeID) {
				v.issue(p+"/scopeId", "unknownGrantScope")
			}
		case "binding":
			if !bindingExists(doc, g.ScopeID) {
				v.issue(p+"/scopeId", "unknownGrantScope")
			}
		case "account":
			if !accountExists(doc, g.ScopeID) {
				v.issue(p+"/scopeId", "unknownGrantScope")
			}
		}
		key := string(g.PrincipalID) + "|" + string(g.Namespace) + "|" + g.Permission + "|" + g.ScopeKind + "|" + string(g.ScopeID)
		if seen[key] {
			v.issue(p, "duplicateGrant")
		}
		seen[key] = true
	}
}

func (v *validator) checkEvidence(doc *ConfigDocument, accounts, bindings, resources map[domain.ID]bool, byID map[domain.ID]*ResourceDef) {
	ids := map[domain.ID]string{} // evidenceID -> kind
	for i := range doc.Evidence {
		e := &doc.Evidence[i]
		p := "/evidence/" + itoa(i)
		if !validID(e.EvidenceID) || ids[e.EvidenceID] != "" {
			v.issue(p+"/evidenceId", "invalidOrDuplicateId")
		}
		ids[e.EvidenceID] = e.Kind
		switch e.Kind {
		case "bound", "initial_state", "hard_stop", "guard", "entitlement_selector":
		default:
			v.issue(p+"/kind", "evidenceKindInvalid")
		}
		if !validID(e.SubjectRef) || !validSourceRef(e.SourceRef) {
			v.issue(p, "invalidEvidenceIdentity")
		}
		if e.ExpiresAtMillis <= e.VerifiedAtMillis {
			v.issue(p+"/expiresAtMillis", "evidenceExpiryInvalid")
		}
		for k := range e.Payload {
			if secretKeyNames[strings.ToLower(k)] {
				v.issue(p+"/payload/"+k, "secretMaterialForbidden")
			}
		}
		subjectKnown := accounts[e.SubjectRef] || bindings[e.SubjectRef] ||
			resources[e.SubjectRef] || poolExists(doc, e.SubjectRef) || offerExists(doc, e.SubjectRef) ||
			groupExists(doc, e.SubjectRef)
		if !subjectKnown {
			v.issue(p+"/subjectRef", "unknownEvidenceSubject")
		}
		switch e.Kind {
		case "bound", "hard_stop":
			if !accounts[e.SubjectRef] && !bindings[e.SubjectRef] {
				v.issue(p+"/subjectRef", "evidenceSubjectTypeMismatch")
			}
		case "initial_state":
			if !resources[e.SubjectRef] {
				v.issue(p+"/subjectRef", "evidenceSubjectTypeMismatch")
			}
		case "entitlement_selector":
			if !groupExists(doc, e.SubjectRef) {
				v.issue(p+"/subjectRef", "evidenceSubjectTypeMismatch")
			}
		}
	}
	// Resource evidence applicability (B3): the evidence must EXIST, carry
	// the right kind, AND actually support this resource's assertion —
	// correct subject CLASS alone is not enough.
	bindingAccount := map[domain.ID]domain.ID{}
	for i := range doc.Bindings {
		bindingAccount[doc.Bindings[i].BindingID] = doc.Bindings[i].AccountID
	}
	byIndex := func(id domain.ID) *EvidenceDef {
		for i := range doc.Evidence {
			if doc.Evidence[i].EvidenceID == id {
				return &doc.Evidence[i]
			}
		}
		return nil
	}
	for i := range doc.Resources {
		r := &doc.Resources[i]
		p := "/resources/" + itoa(i)
		if r.CapacityEvidenceRef != "" {
			ev := byIndex(r.CapacityEvidenceRef)
			kind, ok := ids[r.CapacityEvidenceRef]
			if !ok {
				v.issue(p+"/capacityEvidenceRef", "unknownEvidence")
			} else if kind != "bound" && kind != "hard_stop" {
				v.issue(p+"/capacityEvidenceRef", "evidenceSubjectTypeMismatch")
			} else if ev != nil {
				// Capacity evidence certifies the OWNER: account-owned
				// resources need the owner account exactly; binding-owned
				// resources accept their account's evidence (account covers
				// its bindings). Metric and unit must match the resource —
				// a concurrency bound cannot back a token limit.
				subjectOK := ev.SubjectRef == r.OwnerID
				if !subjectOK && r.OwnerKind == "binding" && bindingAccount[r.OwnerID] != "" {
					subjectOK = ev.SubjectRef == bindingAccount[r.OwnerID]
				}
				if !subjectOK {
					v.issue(p+"/capacityEvidenceRef", "evidenceNotApplicable")
				}
				// Metric is REQUIRED on capacity evidence (S4 P2-2, D-B3-v2
				// rev2): an owner holds many metrics — evidence without a
				// dimension cannot support any specific resource. Unit, when
				// declared, must match as well.
				if m, ok2 := ev.Payload["metricId"]; !ok2 {
					v.issue(p+"/capacityEvidenceRef", "evidenceNotApplicable")
				} else if m != string(r.MetricID) {
					v.issue(p+"/capacityEvidenceRef", "evidenceNotApplicable")
				}
				// bound is REQUIRED (Q3 C2): a capacity claim without a
				// verified quantity constrains nothing and cannot back a
				// resource.
				if _, ok2 := ev.Payload["bound"]; !ok2 {
					v.issue(p+"/capacityEvidenceRef", "evidenceNotApplicable")
				}
				if u, ok2 := ev.Payload["unit"]; ok2 && u != r.Unit {
					v.issue(p+"/capacityEvidenceRef", "evidenceNotApplicable")
				}
				// Explicit dimension claims in the evidence must agree with
				// the resource declaration (C2: wrong precision, currency,
				// window and rule version are incompatible, not ignorable).
				if q, ok2 := ev.Payload["quantumExp"]; ok2 {
					if qNum, qerr := domain.ParseUnits(q); qerr != nil || int(qNum) != r.QuantumExp {
						v.issue(p+"/capacityEvidenceRef", "evidenceNotApplicable")
					}
				}
				if cur, ok2 := ev.Payload["currency"]; ok2 && cur != r.Currency {
					v.issue(p+"/capacityEvidenceRef", "evidenceNotApplicable")
				}
				if wk, ok2 := ev.Payload["windowKind"]; ok2 && wk != r.Window.Kind {
					v.issue(p+"/capacityEvidenceRef", "evidenceNotApplicable")
				}
				if wd, ok2 := ev.Payload["windowDurationMillis"]; ok2 && wd != itoa64(r.Window.DurationMillis) {
					v.issue(p+"/capacityEvidenceRef", "evidenceNotApplicable")
				}
				if sr, ok2 := ev.Payload["semanticsRevision"]; ok2 && sr != string(r.SemanticsRevision) {
					v.issue(p+"/capacityEvidenceRef", "evidenceNotApplicable")
				}
			}
		}
		if r.InitialStateEvidenceRef != "" {
			ev := byIndex(r.InitialStateEvidenceRef)
			kind, ok := ids[r.InitialStateEvidenceRef]
			if !ok {
				v.issue(p+"/initialStateEvidenceRef", "unknownEvidence")
			} else if kind != "initial_state" {
				v.issue(p+"/initialStateEvidenceRef", "evidenceSubjectTypeMismatch")
			} else if ev != nil {
				// Initial state is THE resource's own opening balance: the
				// subject must be the resource itself and the metric must
				// match (a USD opening balance cannot initialize a token
				// resource).
				if ev.SubjectRef != r.ResourceID {
					v.issue(p+"/initialStateEvidenceRef", "evidenceNotApplicable")
				}
				if m, ok2 := ev.Payload["metricId"]; ok2 && m != string(r.MetricID) {
					v.issue(p+"/initialStateEvidenceRef", "evidenceNotApplicable")
				}
				// remaining is REQUIRED on initial_state (Q3 C2): an opening
				// balance without a quantity states nothing.
				if _, ok2 := ev.Payload["remaining"]; !ok2 {
					v.issue(p+"/initialStateEvidenceRef", "evidenceNotApplicable")
				}
			}
		}
	}
	// Group selector evidence applicability (B3/S4 P1-1): the selector must
	// certify THIS group and its declared metric.
	for i := range doc.EntitlementGroups {
		g := &doc.EntitlementGroups[i]
		if g.SelectorEvidenceID == "" {
			continue
		}
		if ev := byIndex(g.SelectorEvidenceID); ev != nil {
			if ev.SubjectRef != g.GroupID {
				v.issue("/entitlementGroups/"+itoa(i)+"/selectorEvidenceId", "evidenceNotApplicable")
			}
			if m, ok := ev.Payload["metricId"]; ok && m != string(g.MetricID) {
				v.issue("/entitlementGroups/"+itoa(i)+"/selectorEvidenceId", "evidenceNotApplicable")
			}
		}
	}
}

func (v *validator) checkWindow(p string, w *WindowDef) {
	switch w.Kind {
	case "fixed":
		switch w.Period {
		case "minute", "hour", "day", "week", "month":
		default:
			v.issue(p+"/period", "windowPeriodInvalid")
		}
		if _, err := time.LoadLocation(w.TimeZone); err != nil {
			v.issue(p+"/timeZone", "timeZoneInvalid")
		}
		if w.AnchorISO != "" {
			t, err := time.Parse(time.RFC3339Nano, w.AnchorISO)
			if err != nil {
				v.issue(p+"/anchorISO", "anchorInvalid")
			} else if t.Nanosecond()%1_000_000 != 0 {
				// Admission matches WindowAt exactly (B5/S4 P2-4): anchors
				// support millisecond precision; finer precision is an
				// explicit rejection, never a silent truncation.
				v.issue(p+"/anchorISO", "anchorPrecisionUnsupported")
			}
		}
	case "rolling":
		if w.DurationMillis <= 0 {
			v.issue(p+"/durationMillis", "windowDurationInvalid")
		}
	case "lifetime":
	default:
		v.issue(p+"/kind", "windowKindInvalid")
	}
}

func (v *validator) evidenceKinds(doc *ConfigDocument, kind string) map[domain.ID]bool {
	out := map[domain.ID]bool{}
	for i := range doc.Evidence {
		if doc.Evidence[i].Kind == kind {
			out[doc.Evidence[i].EvidenceID] = true
		}
	}
	return out
}

func (v *validator) evidenceByID(doc *ConfigDocument, id domain.ID) *EvidenceDef {
	for i := range doc.Evidence {
		if doc.Evidence[i].EvidenceID == id {
			return &doc.Evidence[i]
		}
	}
	return nil
}

func (v *validator) checkIDList(p string, ids []domain.ID, allow map[domain.ID]bool) {
	seen := map[domain.ID]bool{}
	for j, id := range ids {
		if !validID(id) || seen[id] {
			v.issue(p+"/"+itoa(j), "invalidOrDuplicateRef")
		}
		if allow != nil && !allow[id] {
			v.issue(p+"/"+itoa(j), "unknownRef")
		}
		seen[id] = true
	}
}

// checkEntitlementGroupMembership validates the resource→group back edge:
// a resource claiming a group must point at an existing group that lists it.
func (v *validator) checkEntitlementGroupMembership(doc *ConfigDocument) {
	groups := map[domain.ID]bool{}
	for i := range doc.EntitlementGroups {
		groups[doc.EntitlementGroups[i].GroupID] = true
	}
	for i := range doc.Resources {
		r := &doc.Resources[i]
		if r.EntitlementGroupID == "" {
			continue
		}
		if !groups[r.EntitlementGroupID] {
			v.issue("/resources/"+itoa(i)+"/entitlementGroupId", "unknownEntitlementGroup")
			continue
		}
		listed := false
		for j := range doc.EntitlementGroups {
			g := &doc.EntitlementGroups[j]
			if g.GroupID != r.EntitlementGroupID {
				continue
			}
			for _, s := range g.Order {
				if s.ResourceID == r.ResourceID {
					listed = true
				}
			}
		}
		if !listed {
			v.issue("/resources/"+itoa(i)+"/entitlementGroupId", "resourceNotInGroup")
		}
	}
}
