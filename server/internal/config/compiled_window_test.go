package config

import (
	"encoding/json"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/providers/localtest"
)

// Adjust only one trusted fixture window; schema, quantities, scope and
// registration remain the real local/test catalogue. No corpus is modified.
type windowSnapshotCatalog struct {
	ProviderCatalog
	window domain.WindowRule
}

func (c windowSnapshotCatalog) change(f domain.VerifiedResourceFact) domain.VerifiedResourceFact {
	if f.FactID == "alpha-daily-token-limit" {
		f.WindowKind, f.WindowPeriod, f.WindowTimeZone, f.WindowAnchorISO, f.WindowDurationMs = c.window.Kind, c.window.Period, c.window.TimeZone, c.window.AnchorISO, c.window.DurationMillis
	}
	return f
}
func (c windowSnapshotCatalog) ResolveScopeFact(scope, id domain.ID) (domain.VerifiedResourceFact, bool) {
	f, ok := c.ProviderCatalog.ResolveScopeFact(scope, id)
	return c.change(f), ok
}
func (c windowSnapshotCatalog) ResolveDimensionalFact(scope, provider, metric domain.ID, unit string, match func(domain.VerifiedResourceFact) bool) (domain.VerifiedResourceFact, int, bool) {
	f, n, ok := c.ProviderCatalog.ResolveDimensionalFact(scope, provider, metric, unit, func(f domain.VerifiedResourceFact) bool { return match(c.change(f)) })
	return c.change(f), n, ok
}
func TestCompiledWindowIdentity(t *testing.T) {
	base, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	fixed := func(p, z, a string) domain.WindowRule {
		return domain.WindowRule{Kind: "fixed", Period: p, TimeZone: z, AnchorISO: a}
	}
	cases := []struct {
		name                string
		trusted, configured domain.WindowRule
		accepted            bool
	}{
		{"rolling reflexivity", domain.WindowRule{Kind: "rolling", DurationMillis: 60000}, domain.WindowRule{Kind: "rolling", DurationMillis: 60000}, true},
		{"rolling duration mismatch", domain.WindowRule{Kind: "rolling", DurationMillis: 60000}, domain.WindowRule{Kind: "rolling", DurationMillis: 60001}, false},
		{"Casablanca Lagos", fixed("day", "Africa/Casablanca", ""), fixed("day", "Africa/Lagos", ""), false},
		{"hour date ignored", fixed("hour", "UTC", "2026-01-01T01:15:00.123Z"), fixed("hour", "UTC", "2025-05-15T21:15:00.123Z"), true},
		{"hour millisecond mismatch", fixed("hour", "UTC", "2026-01-01T01:15:00.123Z"), fixed("hour", "UTC", "2025-05-15T21:15:00.124Z"), false},
	}
	for _, z := range []string{"", "UTC", "Etc/UTC", "Etc/GMT", "GMT"} {
		cases = append(cases, struct {
			name                string
			trusted, configured domain.WindowRule
			accepted            bool
		}{"UTC alias " + z, fixed("day", "UTC", ""), fixed("day", z, ""), true})
	}
	for _, p := range []string{"minute", "hour", "day", "week", "month"} {
		zone, anchor := "Asia/Singapore", "2026-06-01T00:00:00+08:00"
		if p == "month" {
			// Existing shared SGT monthly fact would correctly be ambiguous.
			zone, anchor = "UTC", "2026-06-01T00:00:00Z"
		}
		cases = append(cases, struct {
			name                string
			trusted, configured domain.WindowRule
			accepted            bool
		}{"natural phase " + p, fixed(p, zone, ""), fixed(p, zone, anchor), true})
	}
	for _, tc := range cases {
		for _, explicit := range []bool{false, true} {
			name := tc.name + " dimensional"
			if explicit {
				name = tc.name + " explicit"
			}
			t.Run(name, func(t *testing.T) {
				var doc map[string]any
				loadCorpus(t, "control-completed-verified-fact", &doc)
				r := findResource(doc, "res-alpha-daily-token").(map[string]any)
				r["window"] = map[string]any{"kind": tc.configured.Kind}
				w := r["window"].(map[string]any)
				if tc.configured.Kind == "fixed" {
					w["period"], w["timeZone"] = tc.configured.Period, tc.configured.TimeZone
					if tc.configured.AnchorISO != "" {
						w["anchorISO"] = tc.configured.AnchorISO
					}
				}
				if tc.configured.Kind == "rolling" {
					w["durationMillis"] = tc.configured.DurationMillis
				}
				if explicit {
					r["providerFactId"] = "alpha-daily-token-limit"
				} else {
					delete(r, "providerFactId")
				}
				raw, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				result := Compile(raw, windowSnapshotCatalog{ProviderCatalog: base, window: tc.trusted})
				if !tc.accepted {
					if result.Compiled != nil || len(result.Issues) == 0 {
						t.Fatalf("invalid rule compiled: %+v", result)
					}
					located := false
					path := "/resources/0/providerFactId"
					if explicit {
						path = "/resources/0/window"
					}
					for _, issue := range result.Issues {
						if issue.Path == path {
							located = true
						}
					}
					if !located {
						t.Fatalf("missing %s issue: %+v", path, result.Issues)
					}
					return
				}
				if len(result.Issues) != 0 || result.Compiled == nil {
					t.Fatalf("legal control rejected: %+v", result.Issues)
				}
				for _, out := range result.Compiled.Resources {
					if out.ResourceID != "res-alpha-daily-token" {
						continue
					}
					if out.Window != tc.trusted || out.PhysicalKey != "phys-alpha:acct-demo-1:daily-token" || out.Capacity != 1000000 || out.InitialRemaining != 1000000 {
						t.Fatalf("unexpected trusted output: %+v", out)
					}
					t.Logf("window=%+v physicalKey=%s capacity=%d remaining=%d", out.Window, out.PhysicalKey, out.Capacity, out.InitialRemaining)
					return
				}
				t.Fatal("missing compiled resource")
			})
		}
	}
}
