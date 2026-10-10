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

func TestSupplyOffersAndPause(t *testing.T) {
	st := newSupplyStore(t)
	seedSupply(t, st)
	offer := func(id, model string) store.SupplyOfferRecord {
		return store.SupplyOfferRecord{Namespace: "ns", OfferID: domain.ID(id), CredentialID: "cred-1", Endpoint: "https://api.z.ai/api/coding/paas/v4",
			Protocol: "openai-chat", UpstreamModel: model, ModelID: model, DrawsJSON: []byte(`["window/short","window/week"]`), VerifiedAt: 1}
	}
	for _, set := range [][]store.SupplyOfferRecord{{offer("o1", "glm-5.3"), offer("o2", "glm-5.3-air")}, {offer("o1", "glm-5.3")}} {
		if err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error { return tx.ReplaceSupplyOffers("ns", "cred-1", set) }); err != nil {
			t.Fatal(err)
		}
	}
	other := offer("o3", "glm-5.3")
	other.CredentialID = "cred-2"
	if err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error {
		return tx.ReplaceSupplyOffers("ns", "cred-1", []store.SupplyOfferRecord{other})
	}); !errors.Is(err, store.ErrSupplyIdentityConflict) {
		t.Fatalf("an offer of another credential: %v", err)
	}
	var n int
	if err := supplyWrite(t, st, func(tx store.SupplyWriterTx) (e error) {
		n, e = tx.SetSupplyPaused("ns", []domain.ID{"cred-1", "missing"}, true)
		return e
	}); err != nil || n != 1 {
		t.Fatalf("pause: %d %v", n, err)
	}
	// an upsert (the engine recording a read) leaves the user's pause alone
	if err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error {
		return tx.UpsertSupplyCredential(store.SupplyCredentialRecord{Namespace: "ns", CredentialID: "cred-1", ProviderID: "zai", Kind: "api_key", Origin: "found",
			Strategy: "env", LocatorJSON: []byte(`{}`), AccountID: "acct-1", Status: "ok", CreatedAt: 1})
	}); err != nil {
		t.Fatal(err)
	}
	err := st.Read(context.Background(), func(rx store.ReaderTx) error {
		sx := rx.(store.SupplyReaderTx)
		offers, err := sx.ListSupplyOffers("ns", "glm-5.3")
		if err != nil || len(offers) != 1 || offers[0].OfferID != "o1" {
			t.Fatalf("glm-5.3 offers = %+v %v", offers, err)
		}
		if air, _ := sx.ListSupplyOffers("ns", "glm-5.3-air"); len(air) != 0 {
			t.Fatal("a replaced offer survived")
		}
		creds, err := sx.ListSupplyCredentials("ns")
		if err != nil || len(creds) != 1 || !creds[0].Paused {
			t.Fatalf("credentials = %+v %v", creds, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSupplyRouteOrderUsesOptimisticRevisionAndPersists(t *testing.T) {
	st := newSupplyStore(t)
	seedSupply(t, st)
	write := func(id string, offerIDs []domain.ID, version int64) (int64, error) {
		var next int64
		err := st.SubmitWrite(context.Background(), store.WriteCommand{ID: id, Run: func(tx store.WriterTx) error {
			var e error
			next, e = tx.(store.SupplyWriterTx).ReplaceSupplyRouteOrder("ns", "shared-model", offerIDs, version, 10+domain.Millis(version))
			return e
		}})
		return next, err
	}
	if next, err := write("route-order-1", []domain.ID{"offer-b", "offer-a"}, 0); err != nil || next != 1 {
		t.Fatalf("initial order = %d, %v", next, err)
	}
	if _, err := write("route-order-duplicate", []domain.ID{"offer-a", "offer-a"}, 1); !errors.Is(err, store.ErrSupplyRouteOrderInvalid) {
		t.Fatalf("duplicate offer IDs accepted: %v", err)
	}
	if _, err := write("route-order-stale", []domain.ID{"offer-a", "offer-b"}, 0); !errors.Is(err, store.ErrSupplyRouteOrderConflict) {
		t.Fatalf("stale order revision accepted: %v", err)
	}
	if next, err := write("route-order-2", []domain.ID{"offer-a", "offer-b"}, 1); err != nil || next != 2 {
		t.Fatalf("replacement order = %d, %v", next, err)
	}
	err := st.Read(context.Background(), func(rx store.ReaderTx) error {
		rec, ok, err := rx.(store.SupplyReaderTx).GetSupplyRouteOrder("ns", "shared-model")
		if err != nil || !ok || rec.Revision != 2 || len(rec.OfferIDs) != 2 || rec.OfferIDs[0] != "offer-a" || rec.OfferIDs[1] != "offer-b" {
			t.Fatalf("stored route order = %+v, %v, %v", rec, ok, err)
		}
		if _, ok, err := rx.(store.SupplyReaderTx).GetSupplyRouteOrder("ns", "another-model"); err != nil || ok {
			t.Fatalf("route order leaked across models: %v %v", ok, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSupplyRouteOrderConcurrentCASHasOneWinner(t *testing.T) {
	st := newSupplyStore(t)
	seedSupply(t, st)
	if err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error {
		_, err := tx.ReplaceSupplyRouteOrder("ns", "shared-model", []domain.ID{"offer-a", "offer-b"}, 0, 10)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	type result struct {
		next int64
		err  error
	}
	results := make(chan result, 2)
	for i, ids := range [][]domain.ID{{"offer-b", "offer-a"}, {"offer-a", "offer-b"}} {
		go func(i int, ids []domain.ID) {
			var next int64
			err := st.SubmitWrite(context.Background(), store.WriteCommand{ID: "route-order-race-" + string(rune('a'+i)), Run: func(tx store.WriterTx) error {
				var e error
				next, e = tx.(store.SupplyWriterTx).ReplaceSupplyRouteOrder("ns", "shared-model", ids, 1, 20)
				return e
			}})
			results <- result{next, err}
		}(i, ids)
	}
	passed, conflicted := 0, 0
	for range 2 {
		r := <-results
		if r.err == nil && r.next == 2 {
			passed++
		} else if errors.Is(r.err, store.ErrSupplyRouteOrderConflict) {
			conflicted++
		} else {
			t.Fatalf("unexpected concurrent order result: %+v", r)
		}
	}
	if passed != 1 || conflicted != 1 {
		t.Fatalf("CAS winners=%d conflicts=%d", passed, conflicted)
	}
}

// A credential's card is kept, replaced by its next read, and removed; a
// card needs its credential.
func TestSupplyCardsKeepTheLastRead(t *testing.T) {
	st := newSupplyStore(t)
	seedSupply(t, st)
	put := func(c store.SupplyCardRecord) error {
		return supplyWrite(t, st, func(tx store.SupplyWriterTx) error { return tx.PutSupplyCard(c) })
	}
	list := func() []store.SupplyCardRecord {
		var out []store.SupplyCardRecord
		if err := st.Read(context.Background(), func(rx store.ReaderTx) (err error) {
			out, err = rx.(store.SupplyReaderTx).ListSupplyCards("ns")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, c := range []store.SupplyCardRecord{
		{Namespace: "ns", CredentialID: "cred-1", CardJSON: []byte(`{"status":"error"}`), ReadAt: 1},
		{Namespace: "ns", CredentialID: "cred-1", CardJSON: []byte(`{"status":"ok"}`), ReadAt: 2},
	} {
		if err := put(c); err != nil {
			t.Fatal(err)
		}
	}
	if got := list(); len(got) != 1 || string(got[0].CardJSON) != `{"status":"ok"}` || got[0].ReadAt != 2 {
		t.Fatalf("cards = %+v", got)
	}
	if err := put(store.SupplyCardRecord{Namespace: "ns", CredentialID: "nobody", CardJSON: []byte(`{}`), ReadAt: 3}); !errors.Is(err, store.ErrSupplyMissingReference) {
		t.Fatalf("a card without its credential: %v", err)
	}
	if err := supplyWrite(t, st, func(tx store.SupplyWriterTx) error { return tx.DeleteSupplyCard("ns", "cred-1") }); err != nil {
		t.Fatal(err)
	}
	if got := list(); len(got) != 0 {
		t.Fatalf("a deleted card stayed: %+v", got)
	}
}
