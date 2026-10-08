package sqlite

import (
	"context"
	"errors"
	"strings"
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

var errNoSupplyCapability = errors.New("writer lacks the supply capability")

// supplyWrite runs fn in one write command. It never calls t.Fatal: the
// callback runs on the writer goroutine, where FailNow is undefined.
func supplyWrite(t *testing.T, st *Store, fn func(store.SupplyWriterTx) error) error {
	t.Helper()
	return st.SubmitWrite(context.Background(), store.WriteCommand{ID: "supply-test", Run: func(tx store.WriterTx) error {
		stx, ok := tx.(store.SupplyWriterTx)
		if !ok {
			return errNoSupplyCapability
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

	if err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error {
		return tx.UpsertSupplyPackage(store.SupplyPackageRecord{Namespace: "ns", PackageID: "pkg-2", AccountID: "acct-1", TemplateRef: "zai.mcp",
			Kind: "subscription", Basis: "observed", Live: true, DefinitionJSON: []byte(`{}`), UpdatedAt: 2})
	}); err != nil {
		t.Fatal(err)
	}
	resource := store.SupplyResourceRecord{Namespace: "ns", ResourceID: "res-1", PhysicalKey: "pk-1", PackageID: "pkg-1", TemplateRef: "zai.coding-plan/5h",
		MetricID: "prompts", Unit: "count", SemanticsRef: "zai.prompts", SemanticsRevision: "v2",
		WindowJSON: []byte(`{"kind":"rolling","durationMillis":18000000}`), ScopeJSON: []byte(`{"kind":"all"}`), UpdatedAt: 2}
	if err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error { return tx.UpsertSupplyResource(resource) }); err != nil {
		t.Fatalf("re-read with the same identity: %v", err)
	}
	for name, change := range map[string]func(*store.SupplyResourceRecord){
		"physical key":  func(r *store.SupplyResourceRecord) { r.PhysicalKey = "pk-other" },
		"package":       func(r *store.SupplyResourceRecord) { r.PackageID = "pkg-2" },
		"template":      func(r *store.SupplyResourceRecord) { r.TemplateRef = "zai.coding-plan/week" },
		"metric":        func(r *store.SupplyResourceRecord) { r.MetricID = "tokens" },
		"unit":          func(r *store.SupplyResourceRecord) { r.Unit = "token" },
		"quantum":       func(r *store.SupplyResourceRecord) { r.QuantumExp = -2 },
		"currency":      func(r *store.SupplyResourceRecord) { r.Currency = "CNY" },
		"semantics ref": func(r *store.SupplyResourceRecord) { r.SemanticsRef = "zai.other" },
		"window":        func(r *store.SupplyResourceRecord) { r.WindowJSON = []byte(`{"kind":"calendar","period":"week"}`) },
		"scope":         func(r *store.SupplyResourceRecord) { r.ScopeJSON = []byte(`{"kind":"model","model":"glm-5.3"}`) },
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

// seedSecret writes an owned secret reference in namespace ns, the way the
// secret commands do (principal, encrypted record, owner row).
func seedSecret(t *testing.T, st *Store, ns, ref string) {
	t.Helper()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT OR IGNORE INTO principals(principal_id,display_name,enabled,created_at) VALUES('p1','admin',1,1)`, nil},
		{`INSERT INTO secret_records(secret_ref,revision,nonce,ciphertext,master_key_ref,mask,created_at) VALUES(?,'r1',x'00',x'00','k','••••',1)`, []any{ref}},
		{`INSERT INTO secret_refs(secret_ref,principal_id,namespace,current_revision,mask,created_at,updated_at) VALUES(?,'p1',?,'r1','••••',1,1)`, []any{ref, ns}},
	} {
		if _, err := st.writeDB.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed secret: %v", err)
		}
	}
}

// Secrets: found credentials never carry a secret_ref; added ones carry one
// of their own namespace unless they are waiting for a master key. There is
// no plaintext column at all.
func TestSupplyCredentialSecretRules(t *testing.T) {
	st := newSupplyStore(t)
	seedSecret(t, st, "ns", "secret-ns")
	seedSecret(t, st, "other", "secret-other")
	base := store.SupplyCredentialRecord{Namespace: "ns", ProviderID: "deepseek", Kind: "api_key", Strategy: "paste", LocatorJSON: []byte(`{}`), CreatedAt: 1}
	cases := []struct {
		id, origin, status, secret string
		want                       error // nil = accepted; errCheck = CHECK constraint
	}{
		{"added-ok-without-secret", "added", "ok", "", errCheck},
		{"added-needs-key", "added", "needs_key", "", nil},
		{"added-with-own-secret", "added", "ok", "secret-ns", nil},
		{"added-with-other-namespace-secret", "added", "ok", "secret-other", store.ErrSupplyMissingReference},
		{"added-with-unknown-secret", "added", "ok", "secret-missing", store.ErrSupplyMissingReference},
		{"found-ok", "found", "ok", "", nil},
		{"found-with-secret", "found", "ok", "secret-ns", errCheck},
	}
	for _, c := range cases {
		r := base
		r.CredentialID, r.Origin, r.Status, r.SecretRef = domain.ID(c.id), c.origin, c.status, domain.ID(c.secret)
		err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error { return tx.UpsertSupplyCredential(r) })
		switch {
		case c.want == nil && err != nil,
			c.want == errCheck && (err == nil || !strings.Contains(err.Error(), "CHECK constraint failed")),
			c.want != nil && c.want != errCheck && !errors.Is(err, c.want):
			t.Fatalf("%s: err=%v, want %v", c.id, err, c.want)
		}
	}
}

var errCheck = errors.New("CHECK constraint")

// The physical key's value is derived globally, but each namespace keeps its
// own observation; within a namespace one key is one resource.
func TestSupplyPhysicalKeyIsUniquePerNamespace(t *testing.T) {
	st := newSupplyStore(t)
	seedSupply(t, st)
	write := func(ns, account, pkg, resource string) error {
		return supplyWrite(t, st, func(tx store.SupplyWriterTx) error {
			if _, err := tx.UpsertSupplyAccount(store.SupplyAccountRecord{Namespace: domain.ID(ns), AccountID: domain.ID(account), ProviderID: "zai", ProviderIdentity: "user@example.com", UpdatedAt: 3}); err != nil {
				return err
			}
			if err := tx.UpsertSupplyPackage(store.SupplyPackageRecord{Namespace: domain.ID(ns), PackageID: domain.ID(pkg), AccountID: domain.ID(account), TemplateRef: "zai.coding-plan" + pkg,
				Kind: "subscription", Basis: "observed", Live: true, DefinitionJSON: []byte(`{}`), UpdatedAt: 3}); err != nil {
				return err
			}
			return tx.UpsertSupplyResource(store.SupplyResourceRecord{Namespace: domain.ID(ns), ResourceID: domain.ID(resource), PhysicalKey: "pk-1", PackageID: domain.ID(pkg),
				TemplateRef: "zai.coding-plan/5h", MetricID: "prompts", Unit: "count", SemanticsRef: "zai.prompts", SemanticsRevision: "v1",
				WindowJSON: []byte(`{"kind":"rolling","durationMillis":18000000}`), ScopeJSON: []byte(`{"kind":"all"}`), UpdatedAt: 3})
		})
	}
	if err := write("other", "acct-1", "pkg-1", "res-1"); err != nil {
		t.Fatalf("a second namespace observing the same account: %v", err)
	}
	if err := write("ns", "acct-1", "pkg-9", "res-9"); !errors.Is(err, store.ErrSupplyIdentityConflict) {
		t.Fatalf("same physical key under a new resource id: %v, want identity conflict", err)
	}
}

func TestSupplyWritesReturnTypedErrors(t *testing.T) {
	st := newSupplyStore(t)
	seedSupply(t, st)
	cases := map[string]struct {
		fn   func(store.SupplyWriterTx) error
		want error
	}{
		"account id reused for another identity": {func(tx store.SupplyWriterTx) error {
			_, err := tx.UpsertSupplyAccount(store.SupplyAccountRecord{Namespace: "ns", AccountID: "acct-1", ProviderID: "zai", ProviderIdentity: "someone-else", UpdatedAt: 2})
			return err
		}, store.ErrSupplyIdentityConflict},
		"package for a missing account": {func(tx store.SupplyWriterTx) error {
			return tx.UpsertSupplyPackage(store.SupplyPackageRecord{Namespace: "ns", PackageID: "pkg-x", AccountID: "nobody", TemplateRef: "t",
				Kind: "prepaid", Basis: "observed", Live: true, DefinitionJSON: []byte(`{}`), UpdatedAt: 2})
		}, store.ErrSupplyMissingReference},
		"second package of one template": {func(tx store.SupplyWriterTx) error {
			return tx.UpsertSupplyPackage(store.SupplyPackageRecord{Namespace: "ns", PackageID: "pkg-dup", AccountID: "acct-1", TemplateRef: "zai.coding-plan",
				Kind: "subscription", Basis: "observed", Live: true, DefinitionJSON: []byte(`{}`), UpdatedAt: 2})
		}, store.ErrSupplyIdentityConflict},
		"reading of a missing run": {func(tx store.SupplyWriterTx) error {
			r := reading("r-x", 99, 1)
			r.RunID = "no-run"
			_, err := tx.AppendSupplyReadingIfChanged(r)
			return err
		}, store.ErrSupplyMissingReference},
		"run for a missing credential": {func(tx store.SupplyWriterTx) error {
			return tx.StartCollectorRun(store.CollectorRunRecord{Namespace: "ns", RunID: "run-x", CredentialID: "nobody", Capability: "inventory", StartedAt: 1})
		}, store.ErrSupplyMissingReference},
		"finish an unknown run": {func(tx store.SupplyWriterTx) error {
			return tx.FinishCollectorRun("ns", "no-run", 5, "ok", "")
		}, store.ErrCollectorRunNotOpen},
	}
	for name, c := range cases {
		if err := supplyWrite(t, st, c.fn); !errors.Is(err, c.want) {
			t.Fatalf("%s: %v, want %v", name, err, c.want)
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
	if err := finish(); !errors.Is(err, store.ErrCollectorRunNotOpen) {
		t.Fatalf("finishing a finished run: %v, want ErrCollectorRunNotOpen", err)
	}
}
