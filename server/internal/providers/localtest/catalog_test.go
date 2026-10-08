package localtest

import (
	"os"
	"testing"
)

// The embedded descriptor must stay byte-identical to the canonical file in
// contracts/providers/ — the same material drives Go and Web validation.
func TestEmbeddedDescriptorMatchesCanonicalFile(t *testing.T) {
	canonical, err := os.ReadFile("../../../../contracts/providers/provider-alpha.descriptor.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != string(descriptorJSON) {
		t.Fatal("embedded provider-alpha.descriptor.json diverged from contracts/providers copy")
	}
}

func TestCatalogRejectsUnknownExtensionFields(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	// context schemas: planName is account-level; resource context requires
	// resourceSchema fields (Q1/C3).
	if iss := c.ValidateExtensionsInContext("provider-alpha", "account", []byte(`{"region":"test","planName":"fixture"}`)); len(iss) != 0 {
		t.Fatalf("legal account extensions rejected: %v", iss)
	}
	if iss := c.ValidateExtensionsInContext("provider-alpha", "resource", []byte(`{"planRef":"plan-1","billingDimension":"tokens"}`)); len(iss) != 0 {
		t.Fatalf("legal resource extensions rejected: %v", iss)
	}
	if iss := c.ValidateExtensionsInContext("provider-alpha", "resource", []byte(`{"planName":"fixture-only"}`)); len(iss) == 0 {
		t.Fatal("account-only field on resource context must be rejected")
	}
	if iss := c.ValidateExtensionsInContext("provider-alpha", "resource", []byte(`{"region":"test","nested":{"password":"x"}}`)); len(iss) == 0 {
		t.Fatal("unknown nested field must be rejected (closed schema)")
	}
	if iss := c.ValidateExtensionsInContext("provider-alpha", "resource", []byte(`{"region":"prod"}`)); len(iss) == 0 {
		t.Fatal("enum violation must be rejected")
	}
	if iss := c.ValidateExtensionsInContext("provider-unknown", "resource", []byte(`{"anything":1}`)); len(iss) == 0 {
		t.Fatal("unknown provider type must not pass")
	}
	fact, ok := c.ResolveScopeFact("acct-demo-1", "alpha-usd-grant-promo")
	if !ok || fact.MetricID != "metric.usd.cents" || fact.Unit != "usd_cents" || fact.PhysicalKey == "" {
		t.Fatalf("scope fact must resolve with metadata: %+v ok=%v", fact, ok)
	}
	fact2, ok := c.ResolveScopeFact("acct-demo-2", "alpha-usd-grant-promo")
	if !ok || fact2.PhysicalKey == fact.PhysicalKey {
		t.Fatal("independent billing scopes must resolve distinct physical keys")
	}
	if _, ok := c.ResolveScopeFact("acct-demo-1", "invented-fact"); ok {
		t.Fatal("invented fact must not resolve")
	}
	// B4: the delivered descriptor carries all four schema classes.
	d, ok := c.Descriptor("provider-alpha")
	if !ok || len(d.ConfigSchema) == 0 || len(d.ResourceSchema) == 0 ||
		len(d.CapabilitySchema) == 0 || len(d.ExtensionSchema) == 0 {
		t.Fatalf("descriptor must deliver all schema classes: %+v", d)
	}
	if !c.SupportsDomain("provider-alpha", "ai/text") {
		t.Fatal("declared domain must be supported")
	}
	if c.SupportsDomain("provider-alpha", "mail/send") {
		t.Fatal("undeclared domain must not be supported")
	}
}
