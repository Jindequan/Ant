package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ant/server/internal/config"
	"ant/server/internal/domain"
	"ant/server/internal/providers/localtest"
	"ant/server/internal/store"
	"ant/server/internal/store/sqlite"
)

type testClock struct{ now domain.Millis }

func (c *testClock) Now() domain.Millis { return c.now }

func newServiceFixture(t *testing.T) (*ConfigService, *testClock, domain.AuthContext) {
	t.Helper()
	svc, clock, auth, _ := newServiceFixtureWithDBPath(t)
	return svc, clock, auth
}

func newServiceFixtureWithDBPath(t *testing.T) (*ConfigService, *testClock, domain.AuthContext, string) {
	t.Helper()
	clock := &testClock{now: 1_790_000_000_000}
	dbPath := filepath.Join(t.TempDir(), "config.db")
	st, err := sqlite.Open(dbPath, sqlite.Config{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	// The auth-session persistence leaf is not part of CFG-08. Seed the
	// foreign-key parent rows as an integration fixture only.
	seed, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(`INSERT INTO principals(principal_id, display_name, enabled, created_at) VALUES
		('principal-1', 'test principal', 1, 1), ('other', 'other principal', 1, 1)`); err != nil {
		_ = seed.Close()
		t.Fatal(err)
	}
	// This opaque fixture row models an already-sealed credential revision.
	// Ownership is required both for draft validation and activation; the
	// service never decrypts the value in either path.
	if _, err := seed.Exec(`INSERT INTO secret_records(secret_ref, revision, nonce, ciphertext, master_key_ref, mask, created_at)
		VALUES('secret-alpha-key', 'rev-1', X'00', X'00', 'localtest-master', 'sk-f…', 1)`); err != nil {
		_ = seed.Close()
		t.Fatal(err)
	}
	if _, err := seed.Exec(`INSERT INTO secret_refs(secret_ref, principal_id, namespace, current_revision, mask, created_at, updated_at)
		VALUES('secret-alpha-key', 'principal-1', 'ns-demo', 'rev-1', 'sk-f…', 1, 1)`); err != nil {
		_ = seed.Close()
		t.Fatal(err)
	}
	if _, err := seed.Exec(`INSERT INTO secret_records(secret_ref, revision, nonce, ciphertext, master_key_ref, mask, created_at)
		VALUES('secret-other-key', 'other-rev', X'00', X'00', 'localtest-master', 'sk-o…', 1)`); err != nil {
		_ = seed.Close()
		t.Fatal(err)
	}
	if _, err := seed.Exec(`INSERT INTO secret_refs(secret_ref, principal_id, namespace, current_revision, mask, created_at, updated_at)
		VALUES('secret-other-key', 'other', 'ns-other', 'other-rev', 'sk-o…', 1, 1)`); err != nil {
		_ = seed.Close()
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(context.Background()); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	catalog, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewConfigService(st, catalog, clock)
	if err != nil {
		t.Fatal(err)
	}
	auth := domain.AuthContext{PrincipalID: "principal-1", Namespace: "ns-demo", Permissions: []domain.Permission{domain.PermConfigure}}
	return svc, clock, auth, dbPath
}

func validDefinition(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "evidence", "CODEX-T1-REWORK2-AUDIT", "config-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string          `json:"name"`
		Document json.RawMessage `json:"document"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Name == "control-completed-verified-fact" {
			return c.Document
		}
	}
	t.Fatal("complete verified-fact config control missing")
	return nil
}

func TestSaveDraftPersistsInvalidDraftAndRejectsSecretMaterial(t *testing.T) {
	svc, _, auth := newServiceFixture(t)
	draft, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{
		DraftID: "draft-invalid", Definition: json.RawMessage(`{"unexpected":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Status != "editing" || draft.Version != 1 || len(draft.FieldIssues) == 0 {
		t.Fatalf("invalid draft should persist with repair issues: %#v", draft)
	}
	if _, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{
		DraftID: "draft-secret", Definition: json.RawMessage(`{"apiKey":"sk-live-example"}`),
	}); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("secret-bearing draft error = %v, want ErrConfigInvalid", err)
	}
	for _, raw := range []string{
		`{"credentials":{"value":"sk-live-example"}}`,
		`{"auth":{"token":"sk-live-example"}}`,
		`{"credential_value":"sk-live-example"}`,
	} {
		if _, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{
			DraftID: "draft-secret-object", Definition: json.RawMessage(raw),
		}); !errors.Is(err, ErrConfigInvalid) {
			t.Errorf("secret-bearing draft %s error = %v, want ErrConfigInvalid", raw, err)
		}
	}
	if _, err := svc.GetDraft(context.Background(), domain.AuthContext{
		PrincipalID: "other", Namespace: "ns-demo", Permissions: []domain.Permission{domain.PermConfigure},
	}, "draft-invalid"); !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("other principal GetDraft error = %v, want not found", err)
	}
}

func TestSaveDraftCASConflictDoesNotOverwrite(t *testing.T) {
	svc, _, auth := newServiceFixture(t)
	draft, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{
		DraftID: "draft-cas", Definition: json.RawMessage(`{"first":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{
		DraftID: draft.DraftID, ExpectedVersion: 0, Definition: json.RawMessage(`{"second":true}`),
	}); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("create with existing ID = %v, want conflict", err)
	}
	updated, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{
		DraftID: draft.DraftID, ExpectedVersion: 1, Definition: json.RawMessage(`{"second":true}`),
	})
	if err != nil || updated.Version != 2 {
		t.Fatalf("expected CAS update = %#v, %v", updated, err)
	}
	if _, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{
		DraftID: draft.DraftID, ExpectedVersion: 1, Definition: json.RawMessage(`{"stale":true}`),
	}); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("stale update = %v, want conflict", err)
	}
	got, err := svc.GetDraft(context.Background(), auth, draft.DraftID)
	if err != nil || string(got.Definition) != `{"second":true}` || got.Version != 2 {
		t.Fatalf("stale write changed draft: %#v, %v", got, err)
	}
}

func TestSaveDraftCommandReceiptReplaysAndDetectsChangedMeaning(t *testing.T) {
	svc, _, auth := newServiceFixture(t)
	in := ConfigDraftInput{
		CommandID: "save-command-1", DraftID: "draft-replay-1", Definition: json.RawMessage(`{"version":"1"}`),
	}
	first, err := svc.SaveDraft(context.Background(), auth, in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := svc.SaveDraft(context.Background(), auth, in)
	if err != nil {
		t.Fatalf("same command replay: %v", err)
	}
	if replay.DraftID != first.DraftID || replay.Version != first.Version || string(replay.Definition) != string(first.Definition) {
		t.Fatalf("replay changed the original receipt: first=%+v replay=%+v", first, replay)
	}
	changed := in
	changed.Definition = json.RawMessage(`{"version":"2"}`)
	if _, err := svc.SaveDraft(context.Background(), auth, changed); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("same command with changed input must conflict, got %v", err)
	}
	current, err := svc.GetDraft(context.Background(), auth, in.DraftID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 1 || string(current.Definition) != string(in.Definition) {
		t.Fatalf("changed replay mutated persisted draft: %+v", current)
	}
}

func TestReadPermissionCanInspectButCannotChangeConfig(t *testing.T) {
	svc, _, auth := newServiceFixture(t)
	created, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{
		DraftID: "readable-draft", Definition: json.RawMessage(`{"version":"1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := domain.AuthContext{PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Permissions: []domain.Permission{domain.PermRead}}
	got, err := svc.GetDraft(context.Background(), reader, created.DraftID)
	if err != nil || got.Version != created.Version {
		t.Fatalf("read permission could not inspect draft: draft=%+v err=%v", got, err)
	}
	if _, err := svc.SaveDraft(context.Background(), reader, ConfigDraftInput{
		DraftID: created.DraftID, ExpectedVersion: created.Version, Definition: created.Definition,
	}); !errors.Is(err, ErrConfigForbidden) {
		t.Fatalf("read-only principal modified configuration: %v", err)
	}
}

func TestValidateAndActivateUseCASAndAtomicRevision(t *testing.T) {
	svc, clock, auth := newServiceFixture(t)
	raw := validDefinition(t)
	draft, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{DraftID: "draft-valid", Definition: raw})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := svc.ValidateDraft(context.Background(), auth, draft.DraftID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if validated.Status != "validated" || len(validated.FieldIssues) != 0 || validated.Version != 2 {
		t.Fatalf("validated draft = %#v", validated)
	}
	if _, err := svc.ValidateDraft(context.Background(), auth, draft.DraftID, draft.Version); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("stale validate error = %v, want conflict", err)
	}
	input := domain.ActivationInput{CommandID: "activate-1", DraftID: draft.DraftID, ExpectedDraftVersion: validated.Version}
	activated, err := svc.ActivateDraft(context.Background(), auth, input)
	if err != nil {
		t.Fatal(err)
	}
	if activated.ConfigRevision == "" || activated.ControlVersion != 2 {
		t.Fatalf("activation = %#v", activated)
	}
	// A same-command retry replays the durable result after the draft became
	// activated; it does not attempt a second version transition.
	replayed, err := svc.ActivateDraft(context.Background(), auth, input)
	if err != nil || replayed != activated {
		t.Fatalf("replay = %#v, %v; want %#v", replayed, err, activated)
	}
	if _, err := svc.ActivateDraft(context.Background(), auth, domain.ActivationInput{
		CommandID: input.CommandID, DraftID: input.DraftID, ExpectedDraftVersion: input.ExpectedDraftVersion,
		ExpectedConfigRevision: "different",
	}); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("same command with changed input = %v, want conflict", err)
	}
	read, err := svc.Read(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if read.BaseRevision != activated.ConfigRevision || read.Status != "activated" {
		t.Fatalf("read model = %#v", read)
	}
	got, err := svc.GetDraft(context.Background(), auth, draft.DraftID)
	if err != nil || got.Status != "activated" || got.Version != 3 {
		t.Fatalf("draft after activation = %#v, %v", got, err)
	}
	_ = clock
}

func TestReorderPoolUpdatesLiveAndCompiledPriorityAtomically(t *testing.T) {
	svc, clock, auth, dbPath := newServiceFixtureWithDBPath(t)
	ctx := context.Background()
	draft, err := svc.SaveDraft(ctx, auth, ConfigDraftInput{DraftID: "pool-order-draft", Definition: validDefinition(t)})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := svc.ValidateDraft(ctx, auth, draft.DraftID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	activated, err := svc.ActivateDraft(ctx, auth, domain.ActivationInput{CommandID: "pool-order-activate", DraftID: draft.DraftID, ExpectedDraftVersion: validated.Version})
	if err != nil {
		t.Fatal(err)
	}
	input := domain.ReorderPoolInput{
		CommandID: "pool-order-1", ExpectedPoolVersion: 1, ExpectedConfigRevision: activated.ConfigRevision,
		Entries: []domain.ReorderPoolEntry{{OfferID: "offer-alpha-chat-a", Priority: 19}, {OfferID: "offer-alpha-chat-b", Priority: 19}},
	}
	first, err := svc.ReorderPool(ctx, auth, "pool-main", input)
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if first.Version != 2 || len(first.Members) != 2 || first.Members[0].Priority != 19 || first.Members[1].Priority != 19 {
		t.Fatalf("reorder result lost pool version or equal priorities: %+v", first)
	}
	if first.Members[0].Version != 1 || first.Members[1].Version != 1 {
		t.Fatalf("reorder changed member versions outside the pool CAS: %+v", first.Members)
	}
	queries, err := NewPoolQueries(svc.store, svc.catalog, clock)
	if err != nil {
		t.Fatal(err)
	}
	readAuth := domain.AuthContext{PrincipalID: auth.PrincipalID, Namespace: auth.Namespace, Permissions: []domain.Permission{domain.PermRead}}
	view, err := queries.Get(ctx, readAuth, "pool-main")
	if err != nil || view.Pool.Version != first.Version || len(view.Groups) != 1 || len(view.Groups[0].Members[0].Resources) != 3 {
		t.Fatalf("post-order pool view did not read back committed projection: view=%+v err=%v", view, err)
	}
	for _, resource := range view.Groups[0].Members[0].Resources {
		if resource.Available == nil {
			t.Fatalf("reordering lost verified resource availability for %s", resource.ResourceID)
		}
	}

	replay, err := svc.ReorderPool(ctx, auth, "pool-main", input)
	if err != nil || replay.Version != first.Version || len(replay.Members) != len(first.Members) {
		t.Fatalf("same command did not replay original result: %+v, %v", replay, err)
	}
	changed := input
	changed.Entries = []domain.ReorderPoolEntry{{OfferID: "offer-alpha-chat-a", Priority: 1}, {OfferID: "offer-alpha-chat-b", Priority: 2}}
	if _, err := svc.ReorderPool(ctx, auth, "pool-main", changed); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("same command with a different order = %v, want conflict", err)
	}
	badCoverage := domain.ReorderPoolInput{CommandID: "pool-order-invalid", ExpectedPoolVersion: 2, ExpectedConfigRevision: mustReadActiveRevision(t, svc, auth), Entries: []domain.ReorderPoolEntry{{OfferID: "offer-alpha-chat-a", Priority: 3}, {OfferID: "offer-alpha-chat-a", Priority: 4}}}
	if _, err := svc.ReorderPool(ctx, auth, "pool-main", badCoverage); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("duplicate member submission = %v, want invalid", err)
	}
	rollback := domain.ReorderPoolInput{CommandID: "pool-order-rollback", ExpectedPoolVersion: 2, ExpectedConfigRevision: mustReadActiveRevision(t, svc, auth), Entries: []domain.ReorderPoolEntry{{OfferID: "offer-alpha-chat-a", Priority: 23}, {OfferID: "offer-alpha-chat-b", Priority: 24}}}
	seedDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seedDB.Exec(`INSERT INTO audit_log(audit_id,principal_id,namespace,action,target_ref,created_at) VALUES(?,?,?,?,?,?)`, "pool-reorder/principal-1/pool-order-rollback", auth.PrincipalID, auth.Namespace, "seed.conflict", "pool-main", 1); err != nil {
		_ = seedDB.Close()
		t.Fatal(err)
	}
	_ = seedDB.Close()
	if _, err := svc.ReorderPool(ctx, auth, "pool-main", rollback); err == nil {
		t.Fatal("audit write failure should abort the entire reorder")
	}
	stale := domain.ReorderPoolInput{CommandID: "pool-order-stale", ExpectedPoolVersion: 1, ExpectedConfigRevision: activated.ConfigRevision, Entries: input.Entries}
	if _, err := svc.ReorderPool(ctx, auth, "pool-main", stale); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("stale pool/config version = %v, want conflict", err)
	}

	var active store.ActiveConfigRecord
	var members []store.PoolMemberRecord
	if err := svc.store.Read(ctx, func(rx store.ReaderTx) error {
		var found bool
		var readErr error
		active, found, readErr = rx.GetActiveConfig()
		if readErr != nil || !found {
			return readErr
		}
		members, readErr = rx.ListPoolMembers("pool-main")
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if active.Revision == activated.ConfigRevision || len(members) != 2 || members[0].Priority != 19 || members[1].Priority != 19 {
		t.Fatalf("active config and live member set diverged after reorder: active=%+v members=%+v", active, members)
	}
	if err := svc.store.Read(ctx, func(rx store.ReaderTx) error {
		pool, found, err := rx.GetPool("pool-main")
		if err != nil {
			return err
		}
		if !found || pool.Version != 2 {
			return errors.New("pool version changed during rolled-back reorder")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	verifyDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var rollbackReceipts int
	if err := verifyDB.QueryRow(`SELECT COUNT(*) FROM command_receipts WHERE scope=? AND command_id=?`, "pool-reorder/principal-1/pool-main", "pool-order-rollback").Scan(&rollbackReceipts); err != nil {
		_ = verifyDB.Close()
		t.Fatal(err)
	}
	_ = verifyDB.Close()
	if rollbackReceipts != 0 {
		t.Fatalf("rolled-back reorder left %d command receipts", rollbackReceipts)
	}
	if err := svc.store.Read(ctx, func(rx store.ReaderTx) error {
		revision, found, err := rx.GetConfigRevision(active.Revision)
		if err != nil || !found {
			return err
		}
		var doc config.ConfigDocument
		if err := json.Unmarshal(revision.DefinitionJSON, &doc); err != nil {
			return err
		}
		for _, member := range doc.Pools[0].Members {
			if member.Priority != 19 {
				return errors.New("compiled active config did not preserve submitted priority")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func mustReadActiveRevision(t *testing.T, svc *ConfigService, auth domain.AuthContext) domain.ID {
	t.Helper()
	read, err := svc.Read(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	return read.BaseRevision
}

func TestActivationRejectsCredentialOwnedByAnotherNamespace(t *testing.T) {
	svc, _, auth := newServiceFixture(t)
	var document config.ConfigDocument
	if err := json.Unmarshal(validDefinition(t), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Bindings) == 0 {
		t.Fatal("fixture has no binding")
	}
	document.Bindings[0].CredentialRef = "secret-other-key"
	document.Bindings[0].CredentialRevision = "other-rev"
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{DraftID: "draft-cross-namespace-secret", Definition: raw})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := svc.ValidateDraft(context.Background(), auth, draft.DraftID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.ActivateDraft(context.Background(), auth, domain.ActivationInput{
		CommandID: "activate-cross-namespace-secret", DraftID: draft.DraftID, ExpectedDraftVersion: validated.Version,
	})
	if !errors.Is(err, ErrConfigNotExecutable) {
		t.Fatalf("activation with another namespace's credential = %v, want not executable", err)
	}
}

func TestActivationRechecksEvidenceExpiryAndKeepsPriorRevision(t *testing.T) {
	svc, clock, auth := newServiceFixture(t)
	first, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{DraftID: "draft-first", Definition: validDefinition(t)})
	if err != nil {
		t.Fatal(err)
	}
	first, err = svc.ValidateDraft(context.Background(), auth, first.DraftID, first.Version)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := svc.ActivateDraft(context.Background(), auth, domain.ActivationInput{
		CommandID: "activate-first", DraftID: first.DraftID, ExpectedDraftVersion: first.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{
		DraftID: "draft-second", BaseRevision: initial.ConfigRevision, Definition: validDefinition(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err = svc.ValidateDraft(context.Background(), auth, second.DraftID, second.Version)
	if err != nil || second.Status != "validated" {
		t.Fatalf("validate second = %#v, %v", second, err)
	}
	clock.now = 1_791_000_000_000 // fixture evidence expired after validation.
	if _, err := svc.ActivateDraft(context.Background(), auth, domain.ActivationInput{
		CommandID: "activate-second", DraftID: second.DraftID,
		ExpectedConfigRevision: initial.ConfigRevision, ExpectedDraftVersion: second.Version,
	}); !errors.Is(err, ErrConfigNotExecutable) {
		t.Fatalf("activation with expired evidence = %v, want not executable", err)
	}
	current, err := svc.Read(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if current.BaseRevision != initial.ConfigRevision || current.Version != initial.ControlVersion {
		t.Fatalf("failed activation changed active config: %#v; original=%#v", current, initial)
	}
}

func TestValidateRejectsOtherNamespaceWithoutChangingActiveConfig(t *testing.T) {
	svc, _, auth := newServiceFixture(t)
	raw := validDefinition(t)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["pools"].([]any)[0].(map[string]any)["namespace"] = "ns-other"
	raw, _ = json.Marshal(doc)
	draft, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{DraftID: "draft-other-ns", Definition: raw})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := svc.ValidateDraft(context.Background(), auth, draft.DraftID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if validated.Status != "editing" || len(validated.FieldIssues) == 0 {
		t.Fatalf("cross-namespace draft should stay in editing with issues: %#v", validated)
	}
}

func TestConfigNamespaceCannotTakeOverGlobalActiveRevision(t *testing.T) {
	svc, _, auth := newServiceFixture(t)
	staleDraft, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{DraftID: "draft-ns-demo-stale", Definition: validDefinition(t)})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{DraftID: "draft-ns-demo", Definition: validDefinition(t)})
	if err != nil {
		t.Fatal(err)
	}
	draft, err = svc.ValidateDraft(context.Background(), auth, draft.DraftID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	active, err := svc.ActivateDraft(context.Background(), auth, domain.ActivationInput{
		CommandID: "activate-ns-demo", DraftID: draft.DraftID, ExpectedDraftVersion: draft.Version,
	})
	if err != nil {
		t.Fatal(err)
	}

	otherNamespace := auth
	otherNamespace.Namespace = "ns-other"
	if _, err := svc.SaveDraft(context.Background(), otherNamespace, ConfigDraftInput{
		DraftID: "draft-ns-other", BaseRevision: active.ConfigRevision, Definition: validDefinition(t),
	}); !errors.Is(err, ErrConfigForbidden) {
		t.Fatalf("other namespace draft creation = %v, want forbidden", err)
	}
	if _, err := svc.SaveDraft(context.Background(), otherNamespace, ConfigDraftInput{
		DraftID: staleDraft.DraftID, ExpectedVersion: staleDraft.Version, Definition: validDefinition(t),
	}); !errors.Is(err, ErrConfigForbidden) {
		t.Fatalf("other namespace draft update = %v, want forbidden", err)
	}
	if _, err := svc.Read(context.Background(), otherNamespace); !errors.Is(err, ErrConfigForbidden) {
		t.Fatalf("other namespace read of active revision = %v, want forbidden", err)
	}
}

func TestActivateRejectsMissingCredentialRevision(t *testing.T) {
	svc, _, auth := newServiceFixture(t)
	var doc map[string]any
	if err := json.Unmarshal(validDefinition(t), &doc); err != nil {
		t.Fatal(err)
	}
	doc["bindings"].([]any)[0].(map[string]any)["credentialRevision"] = "rev-missing"
	raw, _ := json.Marshal(doc)
	draft, err := svc.SaveDraft(context.Background(), auth, ConfigDraftInput{DraftID: "draft-missing-secret", Definition: raw})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := svc.ValidateDraft(context.Background(), auth, draft.DraftID, draft.Version)
	if err != nil || validated.Status != "validated" {
		t.Fatalf("validate draft = %#v, %v", validated, err)
	}
	if _, err := svc.ActivateDraft(context.Background(), auth, domain.ActivationInput{
		CommandID: "activate-missing-secret", DraftID: draft.DraftID, ExpectedDraftVersion: validated.Version,
	}); !errors.Is(err, ErrConfigNotExecutable) {
		t.Fatalf("activation with missing credential revision = %v, want not executable", err)
	}
	if _, err := svc.Read(context.Background(), auth); !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("missing-credential activation unexpectedly installed active config: %v", err)
	}
}

func TestDeclaredSubscriptionResourcesPersistWithoutCredentialsAndCannotActivate(t *testing.T) {
	svc, _, auth, dbPath := newServiceFixtureWithDBPath(t)
	ctx := context.Background()
	raw := json.RawMessage(`{"schemaVersion":1,"accounts":[{"accountId":"subscription-1","providerTypeId":"future-provider","displayName":"Monthly subscription","enabled":true}],"bindings":[],"resources":[{"resourceId":"monthly-tokens","ownerKind":"account","ownerId":"subscription-1","metricId":"metric.tokens.total","unit":"token","quantumExp":0,"kind":"counter","commercialRole":"limit","semanticsRef":"sem.pending","semanticsRevision":"pending","window":{"kind":"fixed","period":"month","timeZone":"Asia/Singapore"},"metered":false,"configuredCapacity":"9007199254740993","capacityEvidenceRef":""}],"entitlementGroups":[],"offers":[],"pools":[],"grants":[],"evidence":[],"unsupportedDrafts":[]}`)
	saved, err := svc.SaveDraft(ctx, auth, ConfigDraftInput{CommandID: "save-declared", DraftID: "declared-subscription", Definition: raw})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "editing" || len(saved.FieldIssues) == 0 {
		t.Fatal("declaration silently became executable", saved)
	}
	reread, err := svc.GetDraft(ctx, auth, saved.DraftID)
	if err != nil || string(reread.Definition) != string(raw) {
		t.Fatalf("draft readback lost exact data: %v", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var persisted string
	if err := db.QueryRow("SELECT definition_json FROM config_drafts WHERE draft_id = ?", string(saved.DraftID)).Scan(&persisted); err != nil || persisted != string(raw) {
		t.Fatalf("independent SQLite readback changed declaration: %v", err)
	}
	var resources int
	if err := db.QueryRow("SELECT COUNT(*) FROM resources").Scan(&resources); err != nil || resources != 0 {
		t.Fatalf("draft created live resources: %d, %v", resources, err)
	}
	checked, err := svc.ValidateDraft(ctx, auth, saved.DraftID, saved.Version)
	if err != nil {
		t.Fatal(err)
	}
	if checked.Status != "editing" || len(checked.FieldIssues) == 0 {
		t.Fatal("unsupported declaration was validated", checked)
	}
	if _, err := svc.ActivateDraft(ctx, auth, domain.ActivationInput{CommandID: "activate-declared", DraftID: saved.DraftID, ExpectedDraftVersion: checked.Version}); !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("unvalidated draft activation = %v", err)
	}
	if _, err := svc.Read(ctx, auth); !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("declaration affected active configuration: %v", err)
	}
}
