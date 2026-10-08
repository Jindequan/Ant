package application

import (
	"ant/server/internal/domain"
	"ant/server/internal/domain/supply"
	"ant/server/internal/store/sqlite"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func declaredSupply() supply.Document {
	return supply.Document{SchemaVersion: 1, Accounts: []supply.Account{{ID: "account", ProviderID: "deepseek-standard", ProviderName: "DeepSeek", Name: "Team"}}, Packages: []supply.Package{{ID: "monthly", AccountID: "account", Name: "Monthly plan", Kind: "subscription", Capability: "ai.chat", Status: "configured", TimeZone: "Asia/Singapore", ResourceRefs: []string{"monthly-tokens"}}, {ID: "credits", AccountID: "account", Name: "Purchased credit", Kind: "credit", Capability: "ai.chat", Status: "configured", TimeZone: "UTC", ExpiresOn: "2027-01-01", ResourceRefs: []string{"credit"}}}, Resources: []supply.Resource{{ID: "monthly-tokens", AccountID: "account", PackageID: "monthly", Name: "Tokens", Metric: "tokens", Unit: "tokens", Quantity: "9007199254740993", Role: "allowance", Reset: supply.Reset{Kind: "calendar", Period: "month", TimeZone: "Asia/Singapore"}}, {ID: "credit", AccountID: "account", PackageID: "credits", Name: "Credits", Metric: "money", Unit: "USD", Quantity: "25.0100", Role: "allowance", Reset: supply.Reset{Kind: "none"}}}, Pools: []supply.Pool{{ID: "pool", Name: "AI", Capability: "ai.chat", PackageRefs: []string{"monthly", "credits"}}}}
}
func cloneSupply(d supply.Document) supply.Document {
	b, _ := json.Marshal(d)
	var copy supply.Document
	_ = json.Unmarshal(b, &copy)
	return copy
}
func TestSupplyInventorySQLitePersistenceReplayIsolationAndArchive(t *testing.T) {
	ctx := context.Background()
	cfg, clock, auth, path := newServiceFixtureWithDBPath(t)
	svc, _ := NewInventoryService(cfg.store, clock)
	empty, err := svc.Read(ctx, auth)
	if err != nil || empty.Version != 0 || len(empty.Document.Packages) != 0 {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	input := InventoryInput{CommandID: "initial", Document: declaredSupply()}
	saved, err := svc.Save(ctx, auth, input)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 1 || saved.Document.Resources[0].Quantity != "9007199254740993" || saved.Document.Resources[1].Quantity != "25.01" || input.Document.Resources[1].Quantity != "25.0100" {
		t.Fatalf("quantities or caller input changed: %+v", saved)
	}
	replay, err := svc.Save(ctx, auth, input)
	if err != nil || replay.Version != 1 {
		t.Fatalf("receipt replay: %v %+v", err, replay)
	}
	changed := input
	changed.Document = cloneSupply(input.Document)
	changed.Document.Resources[1].Quantity = "99"
	if _, err = svc.Save(ctx, auth, changed); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("command repurposing: %v", err)
	}
	other := auth
	other.Namespace = "other-namespace"
	isolated, err := svc.Read(ctx, other)
	if err != nil || len(isolated.Document.Accounts) != 0 {
		t.Fatalf("namespace leak: %v %+v", err, isolated)
	}
	read := auth
	read.Permissions = []domain.Permission{domain.PermRead}
	if _, err = svc.Save(ctx, read, input); !errors.Is(err, ErrConfigForbidden) {
		t.Fatal("reader wrote")
	}
	archived := cloneSupply(saved.Document)
	archived.Packages[1].Status = "archived"
	archived.Pools[0].PackageRefs = []string{"monthly"}
	next, err := svc.Save(ctx, auth, InventoryInput{CommandID: "archive", ExpectedVersion: 1, Document: archived})
	if err != nil || next.Version != 2 {
		t.Fatalf("archive: %v", err)
	}
	if _, err = svc.Save(ctx, auth, InventoryInput{CommandID: "stale", ExpectedVersion: 1, Document: saved.Document}); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("stale write: %v", err)
	}
	if err = cfg.store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(path, sqlite.Config{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(ctx)
	svc, _ = NewInventoryService(reopened, clock)
	actual, err := svc.Read(ctx, auth)
	if err != nil || actual.Version != 2 || actual.Document.Packages[1].Status != "archived" || actual.Document.Resources[0].Quantity != "9007199254740993" {
		t.Fatalf("restart: %v %+v", err, actual)
	}
	restored := cloneSupply(actual.Document)
	restored.Packages[1].Status = "configured"
	restored.Pools[0].PackageRefs = []string{"credits", "monthly"}
	actual, err = svc.Save(ctx, auth, InventoryInput{CommandID: "restore", ExpectedVersion: 2, Document: restored})
	if err != nil || actual.Document.Pools[0].PackageRefs[0] != "credits" {
		t.Fatalf("restore/order: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var definitions string
	if err = db.QueryRow("SELECT definition_json FROM supply_inventory WHERE namespace=?", string(auth.Namespace)).Scan(&definitions); err != nil {
		t.Fatal(err)
	}
	var raw supply.Document
	_ = json.Unmarshal([]byte(definitions), &raw)
	if raw.Resources[0].Quantity != "9007199254740993" {
		t.Fatal("SQL precision lost")
	}
	var live int
	if err = db.QueryRow("SELECT COUNT(*) FROM resources").Scan(&live); err != nil || live != 0 {
		t.Fatalf("declaration became executable: %d %v", live, err)
	}
}
func TestSupplyInventoryConcurrentSaveAndAtomicAuditFailure(t *testing.T) {
	ctx := context.Background()
	cfg, clock, auth, path := newServiceFixtureWithDBPath(t)
	svc, _ := NewInventoryService(cfg.store, clock)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []domain.ID{"one", "two"} {
		wg.Add(1)
		go func(id domain.ID) {
			defer wg.Done()
			_, err := svc.Save(ctx, auth, InventoryInput{CommandID: id, Document: declaredSupply()})
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConfigConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS success=%d conflict=%d", success, conflict)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER reject_inventory_audit BEFORE INSERT ON audit_log WHEN NEW.action='inventory.save' BEGIN SELECT RAISE(ABORT,'test audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	current, _ := svc.Read(ctx, auth)
	current.Document.Resources[0].Quantity = "1"
	_, err = svc.Save(ctx, auth, InventoryInput{CommandID: "failed", ExpectedVersion: 1, Document: current.Document})
	if !errors.Is(err, ErrConfigStore) {
		t.Fatalf("audit failure: %v", err)
	}
	actual, _ := svc.Read(ctx, auth)
	if actual.Version != 1 || actual.Document.Resources[0].Quantity != "9007199254740993" {
		t.Fatal("partial commit")
	}
	var receipts int
	_ = db.QueryRow("SELECT COUNT(*) FROM command_receipts WHERE command_id='failed'").Scan(&receipts)
	if receipts != 0 {
		t.Fatal("failed receipt committed")
	}
}
