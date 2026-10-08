package catalog

import (
	"testing"

	"ant/server/internal/config"
	"ant/server/internal/domain"
	"ant/server/internal/providers/localtest"
)

type mutableDirectory struct {
	config.ProviderCatalog
	entries []domain.ProviderDescriptor
}

func (d *mutableDirectory) Descriptors() []domain.ProviderDescriptor { return d.entries }
func (d *mutableDirectory) Descriptor(id domain.ID) (domain.ProviderDescriptor, bool) {
	for _, entry := range d.entries {
		if entry.TypeID == id {
			return entry, true
		}
	}
	return domain.ProviderDescriptor{}, false
}

func TestCatalogFreezesSourceMetadataAndVisitsMultiProviderSourceOnce(t *testing.T) {
	fixture, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	first, _ := fixture.Descriptor("provider-alpha")
	second := cloneDescriptor(first)
	second.TypeID = "provider-beta"
	source := &mutableDirectory{ProviderCatalog: fixture, entries: []domain.ProviderDescriptor{first, second}}
	c, err := New(source)
	if err != nil {
		t.Fatal(err)
	}
	fact, found := c.ResolveScopeFact("acct-demo-1", "alpha-usd-grant-promo")
	if !found || fact.ProviderTypeID != "provider-alpha" {
		t.Fatalf("same source visited more than once: %+v %v", fact, found)
	}
	source.entries[0].AdapterRevision = "mutated"
	source.entries[0].SupportedDomains[0] = "unknown"
	if d, found := c.Descriptor("provider-alpha"); !found || d.AdapterRevision == "mutated" || !c.SupportsDomain("provider-alpha", "ai/text") || c.SupportsDomain("provider-alpha", "unknown") {
		t.Fatalf("source mutation escaped snapshot: %+v", d)
	}
	list := c.Descriptors()
	list[0].Semantics[0].AcceptedRevisions[0] = "mutated"
	if again, _ := c.Descriptor(list[0].TypeID); again.Semantics[0].AcceptedRevisions[0] == "mutated" {
		t.Fatal("nested metadata aliases readback")
	}
}

func TestCatalogRejectsTypedNilSources(t *testing.T) {
	var source *localtest.Catalog
	if _, err := New(source); err != ErrInvalidCatalog {
		t.Fatalf("typed nil: %v", err)
	}
}
