package sqlite

import (
	"context"
	"errors"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

func newSupplyStore(t *testing.T) *Store {
	t.Helper()
	st := newInternalStore(t)
	if err := st.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func supplyWrite(t *testing.T, st *Store, fn func(store.SupplyWriterTx) error) error {
	t.Helper()
	return st.SubmitWrite(context.Background(), store.WriteCommand{ID: "supply-test", Run: func(tx store.WriterTx) error {
		stx, ok := tx.(store.SupplyWriterTx)
		if !ok {
			t.Fatalf("writer %T lacks the supply capability", tx)
		}
		return fn(stx)
	}})
}

func units(v int64) *domain.Units { u := domain.Units(v); return &u }

// seedSupply writes one found credential, account, package, resource and an
// open collector run, the minimum a reading needs.
func seedSupply(t *testing.T, st *Store) {
	t.Helper()
	err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error {
		if _, err := tx.UpsertSupplyAccount(store.SupplyAccountRecord{Namespace: "ns", AccountID: "acct-1", ProviderID: "zai", ProviderIdentity: "user@example.com", UpdatedAt: 1}); err != nil {
			return err
		}
		if err := tx.UpsertSupplyCredential(store.SupplyCredentialRecord{Namespace: "ns", CredentialID: "cred-1", ProviderID: "zai", Kind: "api_key", Origin: "found",
			Strategy: "env", LocatorJSON: []byte(`{"env":"ZAI_API_KEY"}`), AccountID: "acct-1", Status: "ok", CreatedAt: 1}); err != nil {
			return err
		}
		if err := tx.UpsertSupplyPackage(store.SupplyPackageRecord{Namespace: "ns", PackageID: "pkg-1", AccountID: "acct-1", TemplateRef: "zai.coding-plan",
			Kind: "subscription", Basis: "observed", Plan: "Pro", Live: true, DefinitionJSON: []byte(`{}`), UpdatedAt: 1}); err != nil {
			return err
		}
		if err := tx.UpsertSupplyResource(store.SupplyResourceRecord{Namespace: "ns", ResourceID: "res-1", PhysicalKey: "pk-1", PackageID: "pkg-1", TemplateRef: "zai.coding-plan/5h",
			MetricID: "prompts", Unit: "count", SemanticsRef: "zai.prompts", SemanticsRevision: "v1",
			WindowJSON: []byte(`{"kind":"rolling","durationMillis":18000000}`), ScopeJSON: []byte(`{"kind":"all"}`), UpdatedAt: 1}); err != nil {
			return err
		}
		return tx.StartCollectorRun(store.CollectorRunRecord{Namespace: "ns", RunID: "run-1", CredentialID: "cred-1", Capability: "inventory", StartedAt: 1})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func reading(id string, at int64, remaining int64) store.SupplyReadingRecord {
	return store.SupplyReadingRecord{Namespace: "ns", ReadingID: domain.ID(id), ResourceID: "res-1", ObservedAt: domain.Millis(at),
		ResetBasis: "reported", Limit: units(150), Remaining: units(remaining), RunID: "run-1", EvidenceJSON: []byte(`{"at":"` + id + `"}`)}
}

func TestSupplyReadingsAppendOnlyOnChangeAndInOrder(t *testing.T) {
	st := newSupplyStore(t)
	seedSupply(t, st)
	steps := []struct {
		r        store.SupplyReadingRecord
		appended bool
		err      error
	}{
		{reading("r1", 10, 100), true, nil},
		{reading("r2", 20, 100), false, nil}, // same value, new evidence: "checked, unchanged"
		{reading("r3", 30, -5), true, nil},   // arrears are a value
		{reading("r4", 25, 1), false, store.ErrSupplyReadingOutOfOrder},
	}
	for _, s := range steps {
		var appended bool
		err := supplyWrite(t, st, func(tx store.SupplyWriterTx) (e error) {
			appended, e = tx.AppendSupplyReadingIfChanged(s.r)
			return e
		})
		if appended != s.appended || !errors.Is(err, s.err) {
			t.Fatalf("%s: appended=%v err=%v, want %v %v", s.r.ReadingID, appended, err, s.appended, s.err)
		}
	}
	err := st.Read(context.Background(), func(rx store.ReaderTx) error {
		sx := rx.(store.SupplyReaderTx)
		latest, ok, err := sx.LatestSupplyReading("ns", "res-1")
		if err != nil || !ok || latest.ReadingID != "r3" || *latest.Remaining != -5 || *latest.Limit != 150 || latest.Used != nil {
			t.Fatalf("latest = %+v %v %v", latest, ok, err)
		}
		snap, ok, err := sx.GetSupplyReading("ns", "r1") // a pinned snapshot stays resolvable
		if err != nil || !ok || *snap.Remaining != 100 {
			t.Fatalf("snapshot r1 = %+v %v %v", snap, ok, err)
		}
		if _, ok, _ := sx.GetSupplyReading("ns", "r2"); ok {
			t.Fatal("an unchanged reading was stored")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSupplyReadingsCannotBeRewritten(t *testing.T) {
	st := newSupplyStore(t)
	seedSupply(t, st)
	if err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error {
		_, err := tx.AppendSupplyReadingIfChanged(reading("r1", 10, 100))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"UPDATE supply_readings SET remaining_units=1", "DELETE FROM supply_readings"} {
		if _, err := st.writeDB.Exec(q); err == nil {
			t.Fatalf("%q succeeded on an append-only table", q)
		}
	}
}

func TestSupplyIdentitiesAreImmutable(t *testing.T) {
	st := newSupplyStore(t)
	seedSupply(t, st)

	var merged domain.ID
	if err := supplyWrite(t, st, func(tx store.SupplyWriterTx) (e error) {
		merged, e = tx.UpsertSupplyAccount(store.SupplyAccountRecord{Namespace: "ns", AccountID: "acct-2", ProviderID: "zai", ProviderIdentity: "user@example.com", UpdatedAt: 2})
		return e
	}); err != nil || merged != "acct-1" {
		t.Fatalf("same provider identity merged into %q, %v; want acct-1", merged, err)
	}

	resource := store.SupplyResourceRecord{Namespace: "ns", ResourceID: "res-1", PhysicalKey: "pk-1", PackageID: "pkg-1", TemplateRef: "zai.coding-plan/5h",
		MetricID: "prompts", Unit: "count", SemanticsRef: "zai.prompts", SemanticsRevision: "v2",
		WindowJSON: []byte(`{"kind":"rolling","durationMillis":18000000}`), ScopeJSON: []byte(`{"kind":"all"}`), UpdatedAt: 2}
	if err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error { return tx.UpsertSupplyResource(resource) }); err != nil {
		t.Fatalf("re-read with the same identity: %v", err)
	}
	for name, change := range map[string]func(*store.SupplyResourceRecord){
		"physical key": func(r *store.SupplyResourceRecord) { r.PhysicalKey = "pk-other" },
		"window":       func(r *store.SupplyResourceRecord) { r.WindowJSON = []byte(`{"kind":"calendar","period":"week"}`) },
		"unit":         func(r *store.SupplyResourceRecord) { r.Unit = "token" },
	} {
		changed := resource
		change(&changed)
		err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error { return tx.UpsertSupplyResource(changed) })
		if !errors.Is(err, store.ErrSupplyIdentityConflict) {
			t.Fatalf("%s change: %v, want identity conflict", name, err)
		}
	}
	err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error {
		return tx.UpsertSupplyCredential(store.SupplyCredentialRecord{Namespace: "ns", CredentialID: "cred-1", ProviderID: "zai", Kind: "api_key", Origin: "added",
			Strategy: "paste", LocatorJSON: []byte(`{}`), Status: "needs_key", CreatedAt: 2})
	})
	if !errors.Is(err, store.ErrSupplyIdentityConflict) {
		t.Fatalf("credential origin change: %v, want identity conflict", err)
	}
}

// Secrets: found credentials never carry a secret_ref; added ones carry one
// unless they are waiting for a master key. Plaintext has no column at all.
func TestSupplyCredentialSecretRules(t *testing.T) {
	st := newSupplyStore(t)
	base := store.SupplyCredentialRecord{Namespace: "ns", ProviderID: "deepseek", Kind: "api_key", Strategy: "paste", LocatorJSON: []byte(`{}`), CreatedAt: 1}
	cases := []struct {
		id, origin, status string
		ok                 bool
	}{
		{"added-ok-without-secret", "added", "ok", false},
		{"added-needs-key", "added", "needs_key", true},
		{"found-ok", "found", "ok", true},
	}
	for _, c := range cases {
		r := base
		r.CredentialID, r.Origin, r.Status = domain.ID(c.id), c.origin, c.status
		err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error { return tx.UpsertSupplyCredential(r) })
		if (err == nil) != c.ok {
			t.Fatalf("%s: err=%v, want ok=%v", c.id, err, c.ok)
		}
	}
}

func TestCollectorRunFinishesOnce(t *testing.T) {
	st := newSupplyStore(t)
	seedSupply(t, st)
	finish := func() error {
		return supplyWrite(t, st, func(tx store.SupplyWriterTx) error { return tx.FinishCollectorRun("ns", "run-1", 5, "ok", "") })
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	if err := finish(); err == nil {
		t.Fatal("a finished run was finished again")
	}
}
