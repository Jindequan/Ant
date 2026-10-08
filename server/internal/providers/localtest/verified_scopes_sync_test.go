package localtest

import (
	"os"
	"testing"
)

// The embedded verified-scope snapshot must stay byte-identical to the
// canonical contracts file (same material drives Go and Web).
func TestEmbeddedVerifiedScopesMatchCanonicalFile(t *testing.T) {
	canonical, err := os.ReadFile("../../../../contracts/providers/provider-alpha.verified-scopes.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != string(verifiedScopesJSON) {
		t.Fatal("embedded provider-alpha.verified-scopes.json diverged from contracts/providers copy")
	}
}
