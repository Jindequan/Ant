package config

import (
	"ant/server/internal/domain"
	"ant/server/internal/providers/localtest"
	"fmt"
	"testing"
)

// Independent D2 boundary: storage kind and commercial role are orthogonal.
type independentProfileCatalog struct {
	ProviderCatalog
	role string
}

func (c independentProfileCatalog) Descriptor(id domain.ID) (domain.ProviderDescriptor, bool) {
	d, ok := c.ProviderCatalog.Descriptor(id)
	d.Profiles = append([]domain.RegisteredProfile(nil), d.Profiles...)
	for i := range d.Profiles {
		d.Profiles[i].RequiredDimensions = append([]domain.RequiredDimension(nil), d.Profiles[i].RequiredDimensions...)
		for _, dim := range d.Profiles[i].RequiredDimensions {
			if dim.Role == "occupancy" {
				d.Profiles[i].RequiredDimensions = append(d.Profiles[i].RequiredDimensions, domain.RequiredDimension{MetricID: dim.MetricID, Role: c.role})
				break
			}
		}
	}
	return d, ok
}

func TestT1IndependentDualRoleCoverage(t *testing.T) {
	base, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	doc := evidenceControl(t)
	control := compileEvidenceDocument(t, doc, base)
	if control.Compiled == nil || len(control.Issues) != 0 {
		t.Fatalf("complete control: %+v", control)
	}
	for _, role := range []string{"limit", "entitlement", "unknown"} {
		t.Run(role, func(t *testing.T) {
			out := compileEvidenceDocument(t, doc, independentProfileCatalog{base, role})
			if role == "limit" {
				if out.Compiled == nil || len(out.Issues) != 0 {
					t.Fatalf("occupancy/limit must cover both requirements: %+v", out)
				}
				found := false
				for _, r := range out.Compiled.Resources {
					if r.ResourceID == "res-alpha-concurrency" {
						found = true
						if r.Kind != "occupancy" || r.CommercialRole != "limit" || r.ScopeID != "acct-demo-1" || r.Capacity != 1 || r.InitialRemaining != 1 {
							t.Fatalf("compiled dual role: %+v", r)
						}
						t.Logf("compiled dual role: %+v", r)
					}
				}
				if !found {
					t.Fatal("missing occupancy resource")
				}
			} else {
				if out.Compiled != nil {
					t.Fatal("unsupported profile produced executable output")
				}
				for _, issue := range out.Issues {
					if issue.Path == "/offers/0/resourceRefs" && issue.MessageKey == "ant.config.incompleteRequiredBundle" {
						return
					}
				}
				t.Fatalf("missing role-specific issue: %+v", out.Issues)
			}
		})
	}
}

func TestT1IndependentEvidenceWhitespace(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []struct{ id, key, path string }{{"ev-alpha-usd-bound", "bound", "/resources/1/capacityEvidenceRef"}, {"ev-alpha-usd-initial", "remaining", "/resources/1/initialStateEvidenceRef"}} {
		for _, value := range []any{" 500", "500 ", "+500", "5e2", "500.0", 500, nil} {
			t.Run(field.key+fmt.Sprint(value), func(t *testing.T) {
				doc := evidenceControl(t)
				findEvidence(doc, field.id).(map[string]any)["payload"].(map[string]any)[field.key] = value
				r := compileEvidenceDocument(t, doc, cat)
				if r.Compiled != nil {
					t.Fatal("malformed claim compiled")
				}
				// Non-string payload is rejected by the schema; canonical string violations by typed evidence.
				if len(r.Issues) == 0 {
					t.Fatal("missing issue")
				}
				if _, ok := value.(string); ok {
					for _, i := range r.Issues {
						if i.Path == field.path && i.MessageKey == "ant.config.evidenceNotApplicable" {
							return
						}
					}
					t.Fatalf("wrong typed evidence issue: %+v", r.Issues)
				}
			})
		}
	}
}

func TestT1IndependentCalendarIdentityCompile(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, period, zone, trusted, configured string
		accepted                                bool
	}{
		{"offset instant conversion", "day", "Asia/Singapore", "2026-01-01T00:00:00+08:00", "2025-12-31T16:00:00Z", true},
		{"millisecond trailing zeros", "hour", "UTC", "2026-01-01T11:23:45.120Z", "2027-02-01T09:23:45.120000Z", true},
		{"different weekday", "week", "UTC", "2026-01-05T00:00:00Z", "2026-01-06T00:00:00Z", false},
		{"month clamp does not merge", "month", "UTC", "2026-01-30T00:00:00Z", "2026-01-31T00:00:00Z", false},
		{"sub millisecond", "hour", "UTC", "2026-01-01T11:23:45.120Z", "2027-02-01T09:23:45.120001Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trusted := domain.WindowRule{Kind: "fixed", Period: tc.period, TimeZone: tc.zone, AnchorISO: tc.trusted}
			doc := evidenceControl(t)
			r := findResource(doc, "res-alpha-daily-token").(map[string]any)
			r["providerFactId"] = "alpha-daily-token-limit"
			r["window"] = map[string]any{"kind": "fixed", "period": tc.period, "timeZone": tc.zone, "anchorISO": tc.configured}
			out := compileEvidenceDocument(t, doc, windowSnapshotCatalog{cat, trusted})
			if tc.accepted {
				if out.Compiled == nil || len(out.Issues) != 0 {
					t.Fatalf("legal equivalent rejected: %+v", out.Issues)
				}
				for _, cr := range out.Compiled.Resources {
					if cr.ResourceID == "res-alpha-daily-token" {
						if cr.Window != trusted || cr.ScopeID != "acct-demo-1" || cr.Capacity != 1000000 || cr.InitialRemaining != 1000000 {
							t.Fatalf("unexpected output: %+v", cr)
						}
						t.Logf("compiled window: %+v", cr)
						return
					}
				}
				t.Fatal("missing resource")
			}
			if out.Compiled != nil || len(out.Issues) == 0 {
				t.Fatal("invalid window compiled")
			}
			for _, i := range out.Issues {
				if i.Path == "/resources/0/window" {
					return
				}
			}
			t.Fatalf("window issue unlocated: %+v", out.Issues)
		})
	}
}

func TestT1IndependentRollingInt64Boundary(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	doc := evidenceControl(t)
	r := findResource(doc, "res-alpha-daily-token").(map[string]any)
	r["providerFactId"] = "alpha-daily-token-limit"
	r["window"] = map[string]any{"kind": "rolling", "durationMillis": int64(9007199254740992)}
	trusted := domain.WindowRule{Kind: "rolling", DurationMillis: 9007199254740992}
	out := compileEvidenceDocument(t, doc, windowSnapshotCatalog{cat, trusted})
	if out.Compiled == nil || len(out.Issues) != 0 {
		t.Fatalf("positive int64 rolling contract: %+v", out.Issues)
	}
	for _, cr := range out.Compiled.Resources {
		if cr.ResourceID == "res-alpha-daily-token" {
			if cr.Window != trusted {
				t.Fatalf("duration changed: %+v", cr)
			}
			t.Logf("compiled exact int64: %+v", cr)
			return
		}
	}
	t.Fatal("missing resource")
}
