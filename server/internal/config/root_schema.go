package config

import (
	_ "embed"
	"os"
	"testing"
)

//go:embed config-document.schema.json
var rootConfigSchema []byte

// The embedded root schema must stay byte-identical to the canonical file in
// contracts/schema/ — both the Go boundary and the Web validator consume the
// same contract (B2/S1).
func TestEmbeddedRootSchemaMatchesCanonicalFile(t *testing.T) {
	canonical, err := os.ReadFile("../../../contracts/schema/config-document.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != string(rootConfigSchema) {
		t.Fatal("embedded config-document.schema.json diverged from contracts/schema copy")
	}
}
