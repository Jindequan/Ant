package config

// Permanent regression for the second Codex rework round (B1–B4): the FULL
// boundary entry (root schema → typed graph → catalogue) against the shared
// probe corpus. The corpus lives under docs/evidence/ and is bound into the
// source manifest (see scripts/snapshot.py coverage). Business expectations
// come from the root schema and the commercial/evidence invariants — never
// from "both sides agree".

import (
	"encoding/json"
	"os"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/providers/localtest"
)

func TestRework2FullBoundaryConfigCases(t *testing.T) {
	b, err := os.ReadFile("../../../docs/evidence/CODEX-T1-REWORK-AUDIT/config-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string          `json:"name"`
		Document json.RawMessage `json:"document"`
		Expected bool            `json:"expected"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 16 {
		t.Fatalf("shared corpus drifted: %d cases, want 16", len(cases))
	}
	for _, c := range cases {
		doc := c.Document
		// S4 P2-3 adaptation (sanctioned input completion, business assertion
		// unchanged): full-boundary-valid's single entitlement resource now
		// MUST declare its providerFactId explicitly because the catalog has
		// registered facts for its (metric, unit) — a fact-less shadow ledger
		// beside known buckets is not executable. We bind it to the verified
		// grant-promo fact.
		if c.Name == "full-boundary-valid" {
			doc = injectFactIds(t, doc, []string{"alpha-usd-grant-promo"})
		}
		if c.Name == "valid-catalog-two-facts" {
			// D3's fixed snapshot requires prepaid claims to describe its own
			// verified 1000 units, not the promo's 500. Complete this historical
			// positive input locally; preserve corpus bytes and its expectation.
			var parsed map[string]any
			if err := json.Unmarshal(doc, &parsed); err != nil {
				t.Fatal(err)
			}
			prepaid := findResource(parsed, "res-alpha-usd-second").(map[string]any)
			bound := deepCopyMap(findEvidence(parsed, "ev-alpha-usd-bound").(map[string]any))
			bound["evidenceId"] = "ev-second-bound"
			bound["payload"].(map[string]any)["bound"] = "1000"
			parsed["evidence"] = append(parsed["evidence"].([]any), bound)
			prepaid["capacityEvidenceRef"] = "ev-second-bound"
			findEvidence(parsed, "ev-second-initial").(map[string]any)["payload"].(map[string]any)["remaining"] = "1000"
			doc, err = json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}
		}
		t.Run(c.Name, func(t *testing.T) {
			res := Validate(doc, cat)
			accepted := len(res.Issues) == 0
			if accepted != c.Expected {
				t.Fatalf("accepted=%t expected=%t issues=%+v", accepted, c.Expected, res.Issues)
			}
			if c.Name == "valid-catalog-two-facts" {
				assertEvidenceCompiled(t, res, "res-alpha-usd-second", 500, 1000)
				keys := map[domain.ID]bool{}
				for _, resource := range res.Compiled.Resources {
					if keys[resource.PhysicalKey] {
						t.Fatal("independent facts collapsed onto one physical key")
					}
					keys[resource.PhysicalKey] = true
				}
			}
		})
	}
	t.Run("default-slot-with-registered-facts-rejected", func(t *testing.T) {
		// the un-adapted original is a draft with a repair reason now
		var cases2 []struct {
			Name     string          `json:"name"`
			Document json.RawMessage `json:"document"`
		}
		if err := json.Unmarshal(b, &cases2); err != nil {
			t.Fatal(err)
		}
		for _, c := range cases2 {
			if c.Name != "full-boundary-valid" {
				continue
			}
			res := Validate(c.Document, cat)
			// Q1 v3: two registered facts of the same dimensions make the
			// fact-less resource AMBIGUOUS (must declare factId explicitly);
			// either way it is not executable.
			if !hasIssue(res.Issues, "ant.config.ambiguousFact") {
				t.Fatalf("fact-less entitlement beside two registered facts must be ambiguous: %+v", res.Issues)
			}
		}
	})
}

// B1 clone family beyond the shared corpus: the default slot cannot be
// duplicated by renaming, regrouping or reordering; explicit facts cannot be
// cloned either; mixing default slot with facts is ambiguous.
// TestRework2CloneVariants (Q1 semantics): explicit factId reuse within the
// SAME billing scope is a clone regardless of group/resourceId renaming, and
// reorganizing consumption structure does not change resolution. The
// obsolete default-slot and anchor-spelling variants were removed — their
// intent is covered by TestRework3WindowIdentityVariants (Q1 resolution
// identity), since Q1 revoked the classification/default-slot model.
// TestRework2CloneVariants (Q1 semantics): physical-key uniqueness is the
// identity layer behind the structural gates. Where a structural gate (unique
// group member) fires first, the assertion still requires the identity key
// duplicateCommercialFact — the resolution layer detects the clone for every
// structurally expressible shape, with references fully repaired.
func TestRework2CloneVariants(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}

	// twoMembers returns the control document with a duplicated entitlement
	// resource (same factId, same scope) fully wired: group member, its own
	// initial-state and capacity evidence subjects.
	twoMembers := func(t *testing.T, cloneID string) map[string]any {
		t.Helper()
		var doc map[string]any
		loadCorpus(t, "control-completed-verified-fact", &doc)
		grant := findResource(doc, "res-alpha-usd-entitlement").(map[string]any)
		clone := deepCopyMap(grant)
		clone["resourceId"] = cloneID
		clone["configuredCapacity"] = "100"
		doc["resources"] = append(doc["resources"].([]any), clone)
		group := doc["entitlementGroups"].([]any)[0].(map[string]any)
		group["order"] = append(group["order"].([]any), map[string]any{
			"resourceId": cloneID, "providerOrder": int64(2),
		})
		initial := findEvidence(doc, "ev-alpha-usd-initial").(map[string]any)
		cloneInitial := deepCopyMap(initial)
		cloneInitial["evidenceId"] = "ev-alpha-usd-initial-" + cloneID
		cloneInitial["subjectRef"] = cloneID
		cloneInitial["payload"] = map[string]any{"remaining": "100"}
		doc["evidence"] = append(doc["evidence"].([]any), cloneInitial)
		clone["initialStateEvidenceRef"] = "ev-alpha-usd-initial-" + cloneID
		cap := findEvidence(doc, "ev-alpha-usd-bound").(map[string]any)
		cloneCap := deepCopyMap(cap)
		cloneCap["evidenceId"] = "ev-alpha-usd-bound-" + cloneID
		doc["evidence"] = append(doc["evidence"].([]any), cloneCap)
		clone["capacityEvidenceRef"] = "ev-alpha-usd-bound-" + cloneID
		return doc
	}

	t.Run("same-group-clone-same-factid", func(t *testing.T) {
		doc := twoMembers(t, "res-alpha-usd-clone")
		blob, _ := json.Marshal(doc)
		res := Validate(blob, cat)
		if !hasIssue(res.Issues, "ant.config.duplicateCommercialFact") {
			t.Fatalf("same physical key twice must be detected as clone: %+v", res.Issues)
		}
	})

	t.Run("renamed-resourceid-same-factid-still-clone", func(t *testing.T) {
		doc := twoMembers(t, "res-alpha-usd-third")
		blob, _ := json.Marshal(doc)
		res := Validate(blob, cat)
		if !hasIssue(res.Issues, "ant.config.duplicateCommercialFact") {
			t.Fatalf("renamed resourceId with same factId must still be a clone: %+v", res.Issues)
		}
	})

	t.Run("reorganization-stays-legal", func(t *testing.T) {
		// renaming a resource and changing its providerOrder does not change
		// resolution: a fully repaired document stays legal.
		var doc map[string]any
		loadCorpus(t, "control-completed-verified-fact", &doc)
		grant := findResource(doc, "res-alpha-usd-entitlement").(map[string]any)
		grant["resourceId"] = "res-alpha-usd-entitlement-renamed"
		group := doc["entitlementGroups"].([]any)[0].(map[string]any)
		order := group["order"].([]any)
		order[0].(map[string]any)["resourceId"] = "res-alpha-usd-entitlement-renamed"
		order[0].(map[string]any)["providerOrder"] = int64(7)
		for _, rawEv := range doc["evidence"].([]any) {
			ev := rawEv.(map[string]any)
			if ev["subjectRef"] == "res-alpha-usd-entitlement" {
				ev["subjectRef"] = "res-alpha-usd-entitlement-renamed"
			}
		}
		// complete every remaining reference to the renamed resource
		bindings := doc["bindings"].([]any)
		bRefs := bindings[0].(map[string]any)["resourceRefs"].([]any)
		for i, ref := range bRefs {
			if ref == "res-alpha-usd-entitlement" {
				bRefs[i] = "res-alpha-usd-entitlement-renamed"
			}
		}
		for _, rawOffer := range doc["offers"].([]any) {
			oRefs := rawOffer.(map[string]any)["resourceRefs"].([]any)
			for i, ref := range oRefs {
				if ref == "res-alpha-usd-entitlement" {
					oRefs[i] = "res-alpha-usd-entitlement-renamed"
				}
			}
		}
		blob, _ := json.Marshal(doc)
		if res := Validate(blob, cat); len(res.Issues) != 0 {
			t.Fatalf("reorganization must stay legal: %+v", res.Issues)
		}
	})
}

// twoFactsBase derives a two-bucket document: the shared control case plus a
// prepaid entitlement resource (verified fact alpha-usd-prepaid) joined to
// the entitlement group with providerOrder 2.
func twoFactsBase(t *testing.T) json.RawMessage {
	t.Helper()
	var doc map[string]any
	loadCorpus(t, "control-completed-verified-fact", &doc)
	grant := findResource(doc, "res-alpha-usd-entitlement").(map[string]any)
	prepaid := deepCopyMap(grant)
	prepaid["resourceId"] = "res-alpha-usd-prepaid"
	prepaid["providerFactId"] = "alpha-usd-prepaid"
	prepaid["configuredCapacity"] = "300"
	initial := findEvidence(doc, "ev-alpha-usd-initial").(map[string]any)
	initialRemaining := initial["payload"].(map[string]any)["remaining"]
	prepaid["initialStateEvidenceRef"] = ""
	doc["resources"] = append(doc["resources"].([]any), prepaid)
	for _, rawEv := range doc["evidence"].([]any) {
		ev := rawEv.(map[string]any)
		if ev["kind"] == "entitlement_selector" {
			continue
		}
	}
	_ = initialRemaining
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func injectFactIds(t *testing.T, doc json.RawMessage, facts []string) json.RawMessage {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatal(err)
	}
	injectFactIdsIntoDoc(parsed, facts)
	out, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func injectFactIdsIntoDoc(doc map[string]any, facts []string) {
	resources, _ := doc["resources"].([]any)
	idx := 0
	for _, rawRes := range resources {
		res, ok := rawRes.(map[string]any)
		if !ok || res["commercialRole"] != "entitlement" {
			continue
		}
		if idx < len(facts) {
			res["providerFactId"] = facts[idx]
			idx++
		}
	}
}

func hasIssue(issues []domain.FieldIssue, key string) bool {
	for _, i := range issues {
		if i.MessageKey == key {
			return true
		}
	}
	return false
}

func deepCopyMap(in map[string]any) map[string]any {
	blob, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(blob, &out)
	return out
}
