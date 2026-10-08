package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"ant/server/internal/domain"
	"ant/server/internal/jsonx"
	"ant/server/internal/providers/localtest"
)

const fixturesDir = "../../../contracts/fixtures"

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path, err := filepath.Abs(filepath.Join(fixturesDir, "..", "schema", "config-document.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	sch, err := compiler.Compile(path)
	if err != nil {
		t.Fatalf("compile config schema: %v", err)
	}
	return sch
}

func loadDoc(t *testing.T, rel string) (*ConfigDocument, map[string]any, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixturesDir, rel))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse fixture %s: %v", rel, err)
	}
	var doc ConfigDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode fixture %s into ConfigDocument: %v", rel, err)
	}
	return &doc, raw, data
}

// K1: the same valid fixture must pass JSON Schema AND the Go structural
// validator AND a strict codec roundtrip.
func TestValidFixturePassesSchemaValidatorAndCodec(t *testing.T) {
	sch := compileSchema(t)
	doc, raw, data := loadDoc(t, "config-document-valid.json")

	if err := sch.Validate(raw); err != nil {
		t.Fatalf("schema validation failed on valid fixture: %v", err)
	}
	if issues := ValidateStructural(doc); len(issues) != 0 {
		t.Fatalf("structural validation failed on valid fixture: %+v", issues)
	}
	var back ConfigDocument
	if err := jsonx.DecodeStrict(data, &back); err != nil {
		t.Fatalf("strict decode failed: %v", err)
	}
	b1, _ := json.Marshal(doc)
	b2, _ := json.Marshal(&back)
	if string(b1) != string(b2) {
		t.Fatal("codec roundtrip changed the document")
	}
}

// K1: every invalid fixture must be rejected by the Go validator; shape
// violations must also fail JSON Schema, while referential-integrity rules
// (unknown refs, duplicate commercial facts) are Go-validator-only by design.
func TestInvalidFixturesRejected(t *testing.T) {
	sch := compileSchema(t)
	cases := []struct {
		rel              string
		wantMessage      string // empty = any rejection counts
		schemaMustReject bool
		goValidator      bool // structural validator sees it after lenient decode; unknown fields are codec-rejected instead
	}{
		{"config-document-invalid-unknown-field.json", "", true, false},
		{"config-document-invalid-dangling-ref.json", "ant.config.unknownRef", false, true},
		{"config-document-invalid-dangling-binding-ref.json", "ant.config.unknownRef", false, true},
		{"config-document-invalid-unknown-entitlement-group.json", "ant.config.unknownEntitlementGroup", false, true},
		{"config-document-invalid-secret-in-evidence.json", "ant.config.secretMaterialForbidden", false, true},
		{"config-document-invalid-secret-in-extension.json", "ant.config.secretMaterialForbidden", true, true},
		{"config-document-invalid-priority-range.json", "ant.config.priorityRange", true, true},
		{"config-document-invalid-quantity.json", "ant.config.quantityInvalid", true, true},
		{"config-document-invalid-unsupported-executable.json", "ant.config.unsupportedProviderExecutable", false, true},
		{"config-document-invalid-unknown-pool-member.json", "ant.config.unknownOrDuplicateOffer", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.rel, func(t *testing.T) {
			doc, raw, _ := loadDoc(t, tc.rel)
			schemaErr := sch.Validate(raw)
			if tc.schemaMustReject && schemaErr == nil {
				t.Errorf("schema should reject %s", tc.rel)
			}
			if !tc.schemaMustReject && schemaErr != nil {
				t.Errorf("schema unexpectedly rejected %s: %v", tc.rel, schemaErr)
			}
			issues := ValidateStructural(doc)
			if tc.goValidator && len(issues) == 0 {
				t.Errorf("structural validator should reject %s", tc.rel)
			}
			if tc.wantMessage != "" && !containsIssue(issues, tc.wantMessage) {
				t.Errorf("expected issue %s, got %+v", tc.wantMessage, issues)
			}
		})
	}
}

func containsIssue(issues []domain.FieldIssue, key string) bool {
	for _, i := range issues {
		if i.MessageKey == key {
			return true
		}
	}
	return false
}

func TestStrictDecodeRejectsUnknownExecutableField(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(fixturesDir, "config-document-invalid-unknown-field.json"))
	if err != nil {
		t.Fatal(err)
	}
	// lenient decode tolerates it; strict decode must reject (unknown
	// executable fields never pass the boundary silently).
	var doc ConfigDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("lenient decode should succeed: %v", err)
	}
	if err := jsonx.DecodeStrict(data, &ConfigDocument{}); err == nil {
		t.Fatal("strict decode must reject unknown root field")
	} else if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Q1: identity fixtures are catalog-layer (resolution), not structural. The
// full boundary must reject them with the identity key.
func TestIdentityFixturesRejectedAtCatalogPhase(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"config-document-invalid-duplicate-commercial-fact.json",
		"config-document-invalid-two-lone-buckets.json",
	} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(fixturesDir, name))
			if err != nil {
				t.Fatal(err)
			}
			res := Validate(data, cat)
			if !containsIssue(res.Issues, "ant.config.duplicateCommercialFact") &&
				!containsIssue(res.Issues, "ant.config.ambiguousFact") &&
				!containsIssue(res.Issues, "ant.config.factUnresolved") {
				t.Fatalf("identity fixture must be rejected at catalog phase: %+v", res.Issues)
			}
		})
	}
}
