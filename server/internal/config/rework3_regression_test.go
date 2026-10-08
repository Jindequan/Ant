package config

// Permanent regression for the third Codex rework round (C1–C4): the FULL
// boundary against the shared round-3 corpus, plus window-identity controls
// and clone variants with COMPLETE reference surgery (negative cases break
// only the targeted invariant).

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/domain/resources"
	"ant/server/internal/providers/localtest"
)

func TestRework3FullBoundaryConfigCases(t *testing.T) {
	b, err := os.ReadFile("../../../docs/evidence/CODEX-T1-REWORK2-AUDIT/config-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name               string          `json:"name"`
		Document           json.RawMessage `json:"document"`
		Expected           bool            `json:"expected"`
		ExpectedPathPrefix string          `json:"expectedPathPrefix"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 17 {
		t.Fatalf("shared corpus drifted: %d cases, want 17", len(cases))
	}
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			res := Validate(c.Document, cat)
			accepted := len(res.Issues) == 0
			if accepted != c.Expected {
				t.Fatalf("accepted=%t expected=%t issues=%+v", accepted, c.Expected, res.Issues)
			}
			if c.ExpectedPathPrefix != "" {
				located := false
				for _, i := range res.Issues {
					if hasPathPrefix(i.Path, c.ExpectedPathPrefix) {
						located = true
					}
				}
				if !located {
					t.Fatalf("no issue locates %s: %+v", c.ExpectedPathPrefix, res.Issues)
				}
			}
		})
	}
}

func hasPathPrefix(path, prefix string) bool {
	if prefix == "/" {
		return true
	}
	return len(path) >= len(prefix) && path[:len(prefix)] == prefix
}

// Q3: independent billing scopes each carry their own grant (distinct
// physical keys, both legal); a shared scope aliasing two accounts is ONE
// ledger (same physical key = clone).
func TestRework3ScopeIdentity(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	f1, ok1 := cat.ResolveScopeFact("acct-demo-1", "alpha-usd-grant-promo")
	f2, ok2 := cat.ResolveScopeFact("acct-demo-2", "alpha-usd-grant-promo")
	if !ok1 || !ok2 {
		t.Fatal("independent scope grants must resolve")
	}
	if f1.PhysicalKey == f2.PhysicalKey {
		t.Fatal("independent billing scopes must have distinct physical keys")
	}
	// shared scope: the monthly token fact aliases both accounts; a resource
	// on either account with the monthly window resolves to the SAME key.
	var doc map[string]any
	loadCorpus(t, "control-completed-verified-fact", &doc)
	monthly := deepCopyMap(findResource(doc, "res-alpha-daily-token").(map[string]any))
	monthly["resourceId"] = "res-alpha-monthly-shared"
	monthly["window"] = map[string]any{"kind": "fixed", "period": "month", "timeZone": "Asia/Singapore"}
	delete(monthly, "providerFactId")
	addResource(t, doc, monthly)
	// second account with the same monthly dimension → same shared physical key
	addAccount(t, doc, "acct-demo-2")
	monthly2 := deepCopyMap(monthly)
	monthly2["resourceId"] = "res-alpha-monthly-shared-2"
	addResource(t, doc, monthly2)
	blob, _ := json.Marshal(doc)
	res := Validate(blob, cat)
	if !hasIssue(res.Issues, "ant.config.duplicateCommercialFact") {
		t.Fatalf("shared scope must be ONE ledger across aliasing accounts: %+v", res.Issues)
	}
}

// Q3/C1: equivalent window spellings collapse to one limit; materially
// different windows are unresolved drafts; unit/quantum changes cannot mint
// capacity. Negative variants fix ALL references so only the targeted
// invariant can reject.
func TestRework3WindowIdentityVariants(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}

	cloneDailyLimit := func(t *testing.T, mutate func(clone map[string]any)) json.RawMessage {
		var doc map[string]any
		loadCorpus(t, "control-completed-verified-fact", &doc)
		original := findResource(doc, "res-alpha-daily-token")
		clone := deepCopyMap(original.(map[string]any))
		clone["resourceId"] = "res-alpha-daily-token-clone"
		if mutate != nil {
			mutate(clone)
		}
		addResource(t, doc, clone)
		// complete every reference the clone needs (binding + offer + evidence
		// subject) so ONLY the identity invariant can reject
		bindings := doc["bindings"].([]any)
		bindings[0].(map[string]any)["resourceRefs"] = append(
			bindings[0].(map[string]any)["resourceRefs"].([]any), clone["resourceId"])
		offers := doc["offers"].([]any)
		offers[0].(map[string]any)["resourceRefs"] = append(
			offers[0].(map[string]any)["resourceRefs"].([]any), clone["resourceId"])
		ev := deepCopyMap(findEvidence(doc, "ev-alpha-daily-initial").(map[string]any))
		ev["evidenceId"] = "ev-alpha-daily-initial-clone"
		ev["subjectRef"] = clone["resourceId"]
		doc["evidence"] = append(doc["evidence"].([]any), ev)
		clone["initialStateEvidenceRef"] = "ev-alpha-daily-initial-clone"
		capEv := deepCopyMap(findEvidence(doc, "ev-alpha-daily-bound").(map[string]any))
		capEv["evidenceId"] = "ev-alpha-daily-bound-clone"
		doc["evidence"] = append(doc["evidence"].([]any), capEv)
		clone["capacityEvidenceRef"] = "ev-alpha-daily-bound-clone"
		blob, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}

	t.Run("natural-vs-explicit-midnight-same-ledger", func(t *testing.T) {
		blob := cloneDailyLimit(t, func(clone map[string]any) {
			clone["window"] = map[string]any{"kind": "fixed", "period": "day", "timeZone": "Asia/Singapore", "anchorISO": "2026-01-01T00:00:00+08:00"}
		})
		if res := Validate(blob, cat); !hasIssue(res.Issues, "ant.config.duplicateCommercialFact") {
			t.Fatalf("natural/explicit midnight must be ONE ledger: %+v", res.Issues)
		}
	})
	t.Run("hour-phase-ignores-anchor-hour", func(t *testing.T) {
		blob := cloneDailyLimit(t, func(clone map[string]any) {
			clone["window"] = map[string]any{"kind": "fixed", "period": "hour", "timeZone": "Asia/Singapore", "anchorISO": "2026-01-01T00:15:00+08:00"}
		})
		// hour with :15 phase does not match the fact's natural day window →
		// unresolved draft (NOT a second ledger)
		if res := Validate(blob, cat); !hasIssue(res.Issues, "ant.config.factUnresolved") {
			t.Fatalf("different phase must not resolve: %+v", res.Issues)
		}
	})
	t.Run("zone-alias-cannot-mint", func(t *testing.T) {
		blob := cloneDailyLimit(t, func(clone map[string]any) {
			clone["window"] = map[string]any{"kind": "fixed", "period": "day", "timeZone": "Etc/UTC"}
		})
		// different zone (UTC vs SGT) → different reset rule → not the same
		// physical fact → unresolved draft
		if res := Validate(blob, cat); !hasIssue(res.Issues, "ant.config.factUnresolved") {
			t.Fatalf("different zone must not resolve to the fact: %+v", res.Issues)
		}
	})
	t.Run("unit-change-cannot-mint", func(t *testing.T) {
		blob := cloneDailyLimit(t, func(clone map[string]any) {
			clone["unit"] = "kilobyte"
		})
		if res := Validate(blob, cat); !hasIssue(res.Issues, "ant.config.factUnresolved") {
			t.Fatalf("unit change must not resolve: %+v", res.Issues)
		}
	})
	t.Run("window-only-clone-of-factless-limit-same-window-rejected", func(t *testing.T) {
		// exact same dimensions as the fact → resolves to the same physical
		// key → clone
		blob := cloneDailyLimit(t, nil)
		if res := Validate(blob, cat); !hasIssue(res.Issues, "ant.config.duplicateCommercialFact") {
			t.Fatalf("identical dimensional copy must be a clone: %+v", res.Issues)
		}
	})
}

// Q3/C2: the verified bound caps the configured capacity; conservative lower
// values compile; malformed evidence quantities are rejected.
func TestRework3CapacityConstraints(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	base := func(t *testing.T) map[string]any {
		var doc map[string]any
		loadCorpus(t, "control-completed-verified-fact", &doc)
		return doc
	}
	t.Run("conservative-lower-capacity-compiles", func(t *testing.T) {
		doc := base(t)
		findResource(doc, "res-alpha-daily-token").(map[string]any)["configuredCapacity"] = "400000"
		blob, _ := json.Marshal(doc)
		res := Compile(blob, cat)
		if len(res.Issues) != 0 || res.Compiled == nil {
			t.Fatalf("conservative capacity must compile: %+v", res.Issues)
		}
		for _, cr := range res.Compiled.Resources {
			if cr.ResourceID == "res-alpha-daily-token" && cr.Capacity != 400000 {
				t.Fatalf("compiled capacity must equal the configured value: %+v", cr)
			}
		}
	})
	t.Run("capacity-above-verified-bound-rejected", func(t *testing.T) {
		doc := base(t)
		findResource(doc, "res-alpha-daily-token").(map[string]any)["configuredCapacity"] = "1000000000"
		blob, _ := json.Marshal(doc)
		res := Compile(blob, cat)
		if !hasIssue(res.Issues, "ant.config.capacityExceedsVerifiedBound") {
			t.Fatalf("capacity above verified bound must be rejected: %+v", res.Issues)
		}
		if res.Compiled != nil {
			t.Fatal("non-compilable documents must not produce a CompiledConfig")
		}
	})
	t.Run("malformed-bound-rejected", func(t *testing.T) {
		doc := base(t)
		findEvidence(doc, "ev-alpha-daily-bound").(map[string]any)["payload"].(map[string]any)["bound"] = "not-a-number"
		blob, _ := json.Marshal(doc)
		res := Validate(blob, cat)
		if !hasIssue(res.Issues, "ant.config.evidenceNotApplicable") {
			t.Fatalf("malformed bound must be rejected: %+v", res.Issues)
		}
	})
}

// Q3/C3: the catalogue compiles the complete bundle — removing the billing
// or concurrency dimension from an offer breaks the profile requirement.
func TestRework3BundleCoverage(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	loadCorpus(t, "control-completed-verified-fact", &doc)
	offers := doc["offers"].([]any)
	offers[0].(map[string]any)["resourceRefs"] = []any{"res-alpha-daily-token"} // drop billing+concurrency
	blob, _ := json.Marshal(doc)
	res := Validate(blob, cat)
	if !hasIssue(res.Issues, "ant.config.incompleteRequiredBundle") {
		t.Fatalf("incomplete profile bundle must be rejected: %+v", res.Issues)
	}
}

func loadCorpus(t *testing.T, name string, into any) {
	t.Helper()
	b, err := os.ReadFile("../../../docs/evidence/CODEX-T1-REWORK2-AUDIT/config-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string          `json:"name"`
		Document json.RawMessage `json:"document"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Name == name {
			if err := json.Unmarshal(c.Document, into); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("corpus case %s missing", name)
}

func findResource(doc map[string]any, id string) any {
	return findByID(doc["resources"].([]any), "resourceId", id)
}

func findEvidence(doc map[string]any, id string) any {
	return findByID(doc["evidence"].([]any), "evidenceId", id)
}

func findByID(items []any, key, id string) any {
	for _, item := range items {
		if m, ok := item.(map[string]any); ok && m[key] == id {
			return m
		}
	}
	return nil
}

func addResource(t *testing.T, doc map[string]any, res any) {
	t.Helper()
	doc["resources"] = append(doc["resources"].([]any), res)
}

func addAccount(t *testing.T, doc map[string]any, id string) {
	t.Helper()
	doc["accounts"] = append(doc["accounts"].([]any), map[string]any{
		"accountId": id, "providerTypeId": "provider-alpha",
		"displayName": "第二账户（fixture）", "enabled": true,
	})
}

var _ = domain.Running
var _ = resources.ErrRollingNoBounds
var _ = time.Now
