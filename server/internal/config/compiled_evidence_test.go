package config

import (
	"encoding/json"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/providers/localtest"
)

// Only the trusted snapshot is changed for zero-state controls. All other
// catalog behavior remains real, including schema, identity and semantics.
type evidenceSnapshotCatalog struct {
	ProviderCatalog
	bound, remaining domain.Units
}

func (c evidenceSnapshotCatalog) ResolveScopeFact(scope, id domain.ID) (domain.VerifiedResourceFact, bool) {
	f, ok := c.ProviderCatalog.ResolveScopeFact(scope, id)
	if ok && id == "alpha-usd-grant-promo" {
		f.CapacityBound, f.InitialRemaining = c.bound, c.remaining
	}
	return f, ok
}

func evidenceControl(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	loadCorpus(t, "control-completed-verified-fact", &doc)
	return doc
}

func compileEvidenceDocument(t *testing.T, doc map[string]any, cat ProviderCatalog) DocumentValidation {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return Compile(raw, cat)
}

func assertEvidenceCompiled(t *testing.T, result DocumentValidation, resourceID domain.ID, capacity, remaining domain.Units) {
	t.Helper()
	if len(result.Issues) != 0 || result.Compiled == nil {
		t.Fatalf("control must compile: %+v", result.Issues)
	}
	for _, r := range result.Compiled.Resources {
		if r.ResourceID != resourceID {
			continue
		}
		if r.Capacity != capacity || r.InitialRemaining != remaining || len(r.EvidenceRefs) == 0 {
			t.Fatalf("unexpected trusted output: %+v", r)
		}
		return
	}
	t.Fatalf("missing compiled resource %s", resourceID)
}

func TestCompiledEvidenceClaims(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	// Establish a successful complete-entry control before any mutation.
	assertEvidenceCompiled(t, compileEvidenceDocument(t, evidenceControl(t), cat), "res-alpha-usd-entitlement", 500, 500)
	for _, claim := range []struct{ kind, id, field, path string }{
		{"bound", "ev-alpha-usd-bound", "bound", "/resources/1/capacityEvidenceRef"},
		{"hard_stop", "ev-alpha-conc-bound", "bound", "/resources/2/capacityEvidenceRef"},
		{"initial_state", "ev-alpha-usd-initial", "remaining", "/resources/1/initialStateEvidenceRef"},
	} {
		t.Run(claim.kind, func(t *testing.T) {
			base := evidenceControl(t)
			findEvidence(base, claim.id).(map[string]any)["kind"] = claim.kind
			if control := compileEvidenceDocument(t, base, cat); len(control.Issues) != 0 || control.Compiled == nil {
				t.Fatalf("typed control: %+v", control.Issues)
			}
			for _, mutation := range []struct {
				name    string
				value   any
				missing bool
				key     string
			}{
				{name: "missing", missing: true, key: "ant.config.evidenceNotApplicable"},
				{name: "negative", value: "-1", key: "ant.config.evidenceNotApplicable"},
				{name: "negative-zero", value: "-0", key: "ant.config.evidenceNotApplicable"},
				{name: "leading-zero", value: "01", key: "ant.config.evidenceNotApplicable"},
				{name: "malformed", value: "abc", key: "ant.config.evidenceNotApplicable"},
				{name: "empty", value: "", key: "ant.config.evidenceNotApplicable"},
				{name: "overflow", value: "9223372036854775808", key: "ant.config.evidenceNotApplicable"},
				{name: "zero-conflict", value: "0", key: "ant.config.evidenceConflictsVerifiedFact"},
				{name: "above-snapshot", value: "501", key: "ant.config.evidenceConflictsVerifiedFact"},
				{name: "different-positive-snapshot", value: "2", key: "ant.config.evidenceConflictsVerifiedFact"},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					doc := deepCopyMap(base)
					payload := findEvidence(doc, claim.id).(map[string]any)["payload"].(map[string]any)
					if mutation.missing {
						delete(payload, claim.field)
					} else {
						payload[claim.field] = mutation.value
					}
					result := compileEvidenceDocument(t, doc, cat)
					if result.Compiled != nil {
						t.Fatal("invalid evidence produced executable config")
					}
					for _, issue := range result.Issues {
						if issue.Path == claim.path && issue.MessageKey == mutation.key {
							return
						}
					}
					t.Fatalf("missing field issue %s %s: %+v", claim.path, mutation.key, result.Issues)
				})
			}
		})
	}
	t.Run("conservative-capacity-preserves-snapshot", func(t *testing.T) {
		doc := evidenceControl(t)
		findResource(doc, "res-alpha-usd-entitlement").(map[string]any)["configuredCapacity"] = "200"
		assertEvidenceCompiled(t, compileEvidenceDocument(t, doc, cat), "res-alpha-usd-entitlement", 200, 500)
	})
	for _, state := range []struct {
		name             string
		bound, remaining domain.Units
	}{
		{"zero-remaining", 500, 0}, {"zero-bound-and-remaining", 0, 0},
	} {
		t.Run(state.name, func(t *testing.T) {
			doc := evidenceControl(t)
			findResource(doc, "res-alpha-usd-entitlement").(map[string]any)["configuredCapacity"] = "0"
			findEvidence(doc, "ev-alpha-usd-bound").(map[string]any)["payload"].(map[string]any)["bound"] = domain.FormatUnits(state.bound)
			findEvidence(doc, "ev-alpha-usd-initial").(map[string]any)["payload"].(map[string]any)["remaining"] = "0"
			assertEvidenceCompiled(t, compileEvidenceDocument(t, doc, evidenceSnapshotCatalog{cat, state.bound, state.remaining}), "res-alpha-usd-entitlement", 0, 0)
		})
	}
}
