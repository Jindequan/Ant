package supply

import (
	"encoding/json"
	"testing"
)

func example() Document {
	return Document{SchemaVersion: 1, Accounts: []Account{{ID: "account", ProviderID: "provider", ProviderName: "Provider", Name: "Team"}}, Packages: []Package{{ID: "subscription", AccountID: "account", Name: "Monthly", Kind: "subscription", Capability: "ai.chat", Status: "configured", TimeZone: "Asia/Singapore", ResourceRefs: []string{"tokens", "rate"}}, {ID: "credits", AccountID: "account", Name: "Credits", Kind: "credit", Capability: "ai.chat", Status: "configured", TimeZone: "Asia/Singapore", ExpiresOn: "2027-01-01", ResourceRefs: []string{"balance", "rate"}}}, Resources: []Resource{{ID: "tokens", AccountID: "account", PackageID: "subscription", Name: "Monthly tokens", Metric: "tokens", Unit: "tokens", Quantity: "9007199254740993", Role: "allowance", Reset: Reset{Kind: "calendar", Period: "month", TimeZone: "Asia/Singapore"}}, {ID: "balance", AccountID: "account", PackageID: "credits", Name: "Purchased credits", Metric: "money", Unit: "USD", Quantity: "25.0100", Role: "allowance", Reset: Reset{Kind: "none"}}, {ID: "rate", AccountID: "account", Name: "Account requests", Metric: "requests", Unit: "requests", Quantity: "120", Role: "rate", Reset: Reset{Kind: "rolling", Seconds: 60}}}, Pools: []Pool{{ID: "pool", Name: "AI", Capability: "ai.chat", PackageRefs: []string{"subscription", "credits"}}}}
}
func TestInventoryModelsIndependentPackagesAndSharedLimitsExactly(t *testing.T) {
	d := example()
	if err := d.Normalize(); err != nil {
		t.Fatal(err)
	}
	if d.Resources[0].Quantity != "9007199254740993" || d.Resources[1].Quantity != "25.01" {
		t.Fatal("decimal lost")
	}
	if len(d.Accounts) != 1 || len(d.Packages) != 2 || d.Resources[2].PackageID != "" {
		t.Fatal("supply ownership lost")
	}
}
func TestInventoryRejectsBrokenCommercialAndResourceGraphs(t *testing.T) {
	cases := map[string]func(*Document){
		"cross account sharing":    func(d *Document) { d.Resources[2].AccountID = "other" },
		"foreign package resource": func(d *Document) { d.Packages[1].ResourceRefs = append(d.Packages[1].ResourceRefs, "tokens") },
		"duplicate IDs":            func(d *Document) { d.Resources[1].ID = "tokens" },
		"duplicate pool supply":    func(d *Document) { d.Pools[0].PackageRefs = append(d.Pools[0].PackageRefs, "credits") },
		"incompatible capability":  func(d *Document) { d.Packages[1].Capability = "email.send" },
		"archived pool member":     func(d *Document) { d.Packages[1].Status = "archived" },
		"bad timezone":             func(d *Document) { d.Resources[0].Reset.TimeZone = "not-a-zone" },
		"bad expiry":               func(d *Document) { d.Packages[1].ExpiresOn = "2027-02-30" },
		"bad rolling duration":     func(d *Document) { d.Resources[2].Reset.Seconds = 0 },
		"inconsistent window":      func(d *Document) { d.Resources[1].Reset.Period = "month" },
		"exponent quantity":        func(d *Document) { d.Resources[0].Quantity = "3e6" },
		"free spending":            func(d *Document) { d.Packages[1].Kind = "payg" },
		"money unit":               func(d *Document) { d.Resources[1].Unit = "tokens" },
		"fractional concurrency": func(d *Document) {
			d.Resources[1].Metric = "concurrency"
			d.Resources[1].Unit = "slots"
			d.Resources[1].Role = "concurrency"
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			d := example()
			change(&d)
			if d.Normalize() == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
}
func TestInventoryProtectsDurableOwnerIdentity(t *testing.T) {
	old := example()
	raw, _ := json.Marshal(old)
	var next Document
	_ = json.Unmarshal(raw, &next)
	next.Accounts[0].ProviderID = "different"
	if CompatibleUpdate(old, next) {
		t.Fatal("repurposed account")
	}
	_ = json.Unmarshal(raw, &next)
	next.Resources[0].Unit = "requests"
	if CompatibleUpdate(old, next) {
		t.Fatal("repurposed resource")
	}
	_ = json.Unmarshal(raw, &next)
	next.Resources[0].Quantity = "3000000"
	next.Packages[1].Status = "archived"
	next.Pools[0].PackageRefs = []string{"subscription"}
	if !CompatibleUpdate(old, next) || next.Normalize() != nil {
		t.Fatal("safe edit/archive rejected")
	}
}

func TestSharedLimitsCannotBeOptedOutAndRetiredResourcesKeepIdentity(t *testing.T) {
	d := example()
	d.Packages[1].ResourceRefs = []string{"balance"}
	if d.Normalize() == nil {
		t.Fatal("account constraint opt-out accepted")
	}
	d = example()
	d.Resources = append(d.Resources, Resource{ID: "old", AccountID: "account", PackageID: "subscription", Name: "Removed quota", Metric: "tokens", Unit: "tokens", Quantity: "20", Role: "allowance", Reset: Reset{Kind: "none"}, Retired: true})
	if d.Normalize() != nil {
		t.Fatal("retired record rejected")
	}
	d.Packages[0].ResourceRefs = append(d.Packages[0].ResourceRefs, "old")
	if d.Normalize() == nil {
		t.Fatal("retired resource referenced")
	}
	d.Resources[3].Retired = false
	if d.Normalize() != nil {
		t.Fatal("restore rejected")
	}
}
