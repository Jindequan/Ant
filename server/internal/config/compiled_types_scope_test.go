package config

import (
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/providers/localtest"
)

type registeredTypeCatalog struct {
	ProviderCatalog
	change func(*domain.ProviderDescriptor)
}

func (c registeredTypeCatalog) Descriptor(id domain.ID) (domain.ProviderDescriptor, bool) {
	d, ok := c.ProviderCatalog.Descriptor(id)
	d.Semantics = append([]domain.RegisteredSemantics(nil), d.Semantics...)
	d.Profiles = append([]domain.RegisteredProfile(nil), d.Profiles...)
	for i := range d.Profiles {
		d.Profiles[i].RequiredDimensions = append([]domain.RequiredDimension(nil), d.Profiles[i].RequiredDimensions...)
	}
	if ok {
		c.change(&d)
	}
	return d, ok
}

func TestCompiledProfileRoles(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"limit", "entitlement", "unknown"} {
		t.Run(role, func(t *testing.T) {
			registry := registeredTypeCatalog{cat, func(d *domain.ProviderDescriptor) {
				for i := range d.Profiles {
					d.Profiles[i].RequiredDimensions = append(d.Profiles[i].RequiredDimensions, domain.RequiredDimension{MetricID: "metric.concurrency.request", Role: role})
				}
			}}
			r := compileEvidenceDocument(t, evidenceControl(t), registry)
			if role == "limit" {
				if len(r.Issues) != 0 || r.Compiled == nil {
					t.Fatalf("occupancy/limit must cover both declared views: %+v", r.Issues)
				}
			} else if r.Compiled != nil || !hasIssue(r.Issues, "ant.config.incompleteRequiredBundle") {
				t.Fatalf("missing %s role was accepted: %+v", role, r)
			}
		})
	}
}

func TestCompiledTrustedResourceTypes(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	doc := evidenceControl(t)
	// No offer/profile can accidentally reject these resource mutations.
	doc["offers"], doc["pools"] = []any{}, []any{}
	grants := []any{}
	for _, g := range doc["grants"].([]any) {
		if g.(map[string]any)["scopeKind"] == "namespace" {
			grants = append(grants, g)
		}
	}
	doc["grants"] = grants
	control := compileEvidenceDocument(t, doc, cat)
	if len(control.Issues) != 0 || control.Compiled == nil {
		t.Fatalf("valid unoffered control: %+v", control.Issues)
	}
	for _, r := range control.Compiled.Resources {
		kind, role := "counter", "limit"
		switch r.ResourceID {
		case "res-alpha-concurrency":
			kind = "occupancy"
		case "res-alpha-usd-entitlement":
			role = "entitlement"
		}
		if r.Kind != kind || r.CommercialRole != role || r.ScopeID != "acct-demo-1" {
			t.Fatalf("untrusted execution type/scope: %+v", r)
		}
	}
	for _, tc := range []struct{ id, field, value, path string }{
		{"res-alpha-concurrency", "kind", "counter", "/resources/2/kind"},
		{"res-alpha-usd-entitlement", "commercialRole", "limit", "/resources/1/commercialRole"},
		{"res-alpha-daily-token", "kind", "occupancy", "/resources/0/kind"},
	} {
		t.Run(tc.id+"-"+tc.field, func(t *testing.T) {
			mutated := deepCopyMap(doc)
			findResource(mutated, tc.id).(map[string]any)[tc.field] = tc.value
			r := compileEvidenceDocument(t, mutated, cat)
			if r.Compiled != nil {
				t.Fatal("invalid type produced execution input")
			}
			for _, issue := range r.Issues {
				if issue.Path == tc.path && issue.MessageKey == "ant.config.providerFactMetadataMismatch" {
					return
				}
			}
			t.Fatalf("missing resource field issue: %+v", r.Issues)
		})
	}
	for _, tc := range []struct {
		name   string
		change func(*domain.ProviderDescriptor)
	}{
		{"untyped-registration", func(d *domain.ProviderDescriptor) { d.Semantics[0].ResourceKind = "" }},
		{"invalid-role-registration", func(d *domain.ProviderDescriptor) { d.Semantics[0].CommercialRole = "occupancy" }},
		{"ambiguous-registration", func(d *domain.ProviderDescriptor) { d.Semantics = append(d.Semantics, d.Semantics[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := compileEvidenceDocument(t, doc, registeredTypeCatalog{cat, tc.change})
			if r.Compiled != nil || !hasIssue(r.Issues, "ant.config.unregisteredResourceSemantics") {
				t.Fatalf("invalid registry trusted: %+v", r)
			}
		})
	}
}

func TestCompiledResolvedScopes(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"acct-demo-1", "acct-demo-2"} {
		t.Run("shared-via-"+account, func(t *testing.T) {
			doc := evidenceControl(t)
			if account == "acct-demo-2" {
				addAccount(t, doc, account)
			}
			r := findResource(doc, "res-alpha-daily-token").(map[string]any)
			r["ownerId"], r["providerFactId"] = account, "alpha-shared-tokens"
			r["window"] = map[string]any{"kind": "fixed", "period": "month", "timeZone": "Asia/Singapore"}
			r["configuredCapacity"] = "200000"
			capEvidence := findEvidence(doc, "ev-alpha-daily-bound").(map[string]any)
			capEvidence["subjectRef"] = account
			capEvidence["payload"].(map[string]any)["bound"] = "500000"
			findEvidence(doc, "ev-alpha-daily-initial").(map[string]any)["payload"].(map[string]any)["remaining"] = "500000"
			compiled := compileEvidenceDocument(t, doc, cat)
			if len(compiled.Issues) != 0 || compiled.Compiled == nil {
				t.Fatalf("shared full compile control: %+v", compiled.Issues)
			}
			for _, cr := range compiled.Compiled.Resources {
				if cr.ResourceID == "res-alpha-daily-token" {
					if cr.ScopeID != "billing-alpha-shared" || cr.PhysicalKey != "phys-alpha:shared:tokens" || cr.Capacity != 200000 || cr.InitialRemaining != 500000 {
						t.Fatalf("shared output replaced by accessing owner: %+v", cr)
					}
				}
			}
		})
	}
}
