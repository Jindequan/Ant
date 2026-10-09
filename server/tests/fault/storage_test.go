package fault_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/ledger/commands"
	"ant/server/internal/store"
	"ant/server/internal/store/sqlite"
)

// fixtureCommandBudget is an anti-hang guard, not a graded threshold: these
// tests are not about the 50ms product budget, which -race builds overrun
// under load (see sqlite.Config.CommandBudget).
const fixtureCommandBudget = 10 * time.Second

type faultClock struct{ now atomic.Int64 }

func (c *faultClock) Now() domain.Millis { return domain.Millis(c.now.Load()) }

type lostAckStore struct {
	store.Store
	drop atomic.Bool
}

func (s *lostAckStore) SubmitWrite(ctx context.Context, command store.WriteCommand) error {
	err := s.Store.SubmitWrite(ctx, command)
	if err == nil && s.drop.CompareAndSwap(true, false) {
		return errors.New("fault: committed response lost")
	}
	return err
}

func openFaultStore(t *testing.T) (store.Store, *sql.DB, *faultClock) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fault.db")
	clock := &faultClock{}
	clock.now.Store(100)
	st, err := sqlite.Open(path, sqlite.Config{Clock: clock, CommandBudget: fixtureCommandBudget})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close(); _ = st.Close(ctx) })
	return st, db, clock
}

func TestRestoreGuardAuditFailureRollsBackAndCanRetry(t *testing.T) {
	ctx := context.Background()
	st, db, clock := openFaultStore(t)
	service, err := application.NewRestoreGuardService(st, clock)
	if err != nil {
		t.Fatal(err)
	}
	auth := domain.AuthContext{PrincipalID: "manager", Namespace: "ns", Permissions: []domain.Permission{domain.PermManage}}
	input := application.RestoreGuardInput{CommandID: "close-after-restore", ExpectedControlVersion: 1, Enabled: true, EvidenceRef: "backup-check-42"}
	if _, err := db.Exec(`CREATE TRIGGER fail_restore_audit BEFORE INSERT ON audit_log WHEN NEW.action='runtime.restore_guard.close' BEGIN SELECT RAISE(ABORT,'injected audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Set(ctx, auth, input); !errors.Is(err, application.ErrRestoreGuardStore) {
		t.Fatalf("Set with audit failure = %v, want store failure", err)
	}
	assertRuntimeState(t, db, false, 1, 0, 0)
	if _, err := db.Exec(`DROP TRIGGER fail_restore_audit`); err != nil {
		t.Fatal(err)
	}
	state, err := service.Set(ctx, auth, input)
	if err != nil || !state.Enabled || state.ControlVersion != 2 {
		t.Fatalf("retry after rollback = %+v, %v", state, err)
	}
	assertRuntimeState(t, db, true, 2, 1, 1)
}

func TestRestoreGuardLostResponseReplaysWithoutRepeatingEffects(t *testing.T) {
	ctx := context.Background()
	base, db, clock := openFaultStore(t)
	dropper := &lostAckStore{Store: base}
	dropper.drop.Store(true)
	service, err := application.NewRestoreGuardService(dropper, clock)
	if err != nil {
		t.Fatal(err)
	}
	auth := domain.AuthContext{PrincipalID: "manager", Namespace: "ns", Permissions: []domain.Permission{domain.PermManage}}
	input := application.RestoreGuardInput{CommandID: "close-lost-ack", ExpectedControlVersion: 1, Enabled: true, EvidenceRef: "backup-check-43"}
	if _, err := service.Set(ctx, auth, input); !errors.Is(err, application.ErrRestoreGuardStore) {
		t.Fatalf("first Set = %v, want lost response mapped to store error", err)
	}
	state, err := service.Set(ctx, auth, input)
	if err != nil || !state.Enabled || state.ControlVersion != 2 {
		t.Fatalf("same-command retry = %+v, %v", state, err)
	}
	assertRuntimeState(t, db, true, 2, 1, 1)
}

func TestRestoringOlderSnapshotClosesDispatchBeforePublishingDatabase(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(dir, "old.db")
	oldStore, err := sqlite.Open(oldPath, sqlite.Config{Clock: &faultClock{}, CommandBudget: fixtureCommandBudget})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(ctx, oldStore); err != nil {
		t.Fatal(err)
	}
	oldDB, err := sql.Open("sqlite", oldPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO resource_pools(pool_id,namespace,name,enabled,version,definition_json) VALUES('pool','ns','Recovery test',1,1,'{}')`,
		`INSERT INTO config_revisions(revision,definition_json,created_at) VALUES('rev-1','{}',1)`,
		`INSERT INTO active_config(singleton,revision,version) VALUES(1,'rev-1',1)`,
	} {
		if _, err := oldDB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := oldDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := oldStore.SubmitWrite(ctx, store.WriteCommand{Run: func(tx store.WriterTx) error {
		return tx.InsertReceipt(store.ReceiptRecord{Scope: "snapshot", CommandID: "old-row", Kind: "fixture", InputHash: "old", ResultJSON: []byte(`{"old":true}`), CreatedAt: 10})
	}}); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(dir, "snapshot.db")
	if err := oldStore.(*sqlite.Store).Backup(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := oldStore.Close(ctx); err != nil {
		t.Fatal(err)
	}

	targetPath := filepath.Join(dir, "live.db")
	liveStore, err := sqlite.Open(targetPath, sqlite.Config{Clock: &faultClock{}, CommandBudget: fixtureCommandBudget})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(ctx, liveStore); err != nil {
		t.Fatal(err)
	}
	if err := liveStore.SubmitWrite(ctx, store.WriteCommand{Run: func(tx store.WriterTx) error {
		return tx.InsertReceipt(store.ReceiptRecord{Scope: "live", CommandID: "post-snapshot", Kind: "fixture", InputHash: "new", ResultJSON: []byte(`{"new":true}`), CreatedAt: 20})
	}}); err != nil {
		t.Fatal(err)
	}
	if err := liveStore.Close(ctx); err != nil {
		t.Fatal(err)
	}

	if err := sqlite.RestoreBackup(ctx, backupPath, targetPath, 30); err != nil {
		t.Fatalf("restore older backup: %v", err)
	}
	restored, err := sqlite.Open(targetPath, sqlite.Config{Clock: &faultClock{}, CommandBudget: fixtureCommandBudget})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close(ctx)
	if err := sqlite.Migrate(ctx, restored); err != nil {
		t.Fatal(err)
	}
	guardService, err := application.NewRestoreGuardService(restored, &faultClock{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := guardService.Get(ctx, domain.AuthContext{PrincipalID: "manager", Namespace: "ns", Permissions: []domain.Permission{domain.PermManage}})
	if err != nil || !state.Enabled {
		t.Fatalf("restored guard state = %+v, %v; old snapshot must not reopen dispatch", state, err)
	}
	input := domain.ExecuteInput{OperationID: "after-restore", PoolID: "pool", Request: domain.DomainRequest{Domain: "ai/text", CapabilityProfile: "profile", Payload: json.RawMessage(`{"prompt":"synthetic"}`)}, DeadlineAt: 1000, MaxAttempts: 1}
	fingerprint, err := domain.SemanticFingerprint(domain.FingerprintInput{Domain: input.Request.Domain, PoolID: input.PoolID, CapabilityProfile: input.Request.CapabilityProfile, Payload: input.Request.Payload})
	if err != nil {
		t.Fatal(err)
	}
	_, err = commands.Claim(ctx, restored, domain.AuthContext{PrincipalID: "executor", Namespace: "ns", Permissions: []domain.Permission{domain.PermExecute}}, input, fingerprint, commands.ClaimPolicy{RuntimeID: "runtime", Now: 100, LeaseMillis: 500, MaxRuntimeAttempts: 2})
	if !errors.Is(err, commands.ErrClaimNotReady) {
		t.Fatalf("claim after old-snapshot restore = %v, want dispatch blocked by restore guard", err)
	}

	raw, err := sql.Open("sqlite", targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var oldRows, liveRows, closeAudits, restoreReceipts int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM command_receipts WHERE scope='snapshot' AND command_id='old-row'`).Scan(&oldRows); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM command_receipts WHERE scope='live' AND command_id='post-snapshot'`).Scan(&liveRows); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='runtime.restore_guard.close' AND principal_id='system:restore'`).Scan(&closeAudits); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM command_receipts WHERE scope='runtime/restore-guard' AND kind='restore_guard'`).Scan(&restoreReceipts); err != nil {
		t.Fatal(err)
	}
	if oldRows != 1 || liveRows != 0 || closeAudits != 1 || restoreReceipts != 1 {
		t.Fatalf("restore content: snapshot rows=%d live rows=%d close audits=%d receipts=%d", oldRows, liveRows, closeAudits, restoreReceipts)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	preserved := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "live.db.pre-restore-") {
			preserved = true
		}
	}
	if !preserved {
		t.Fatal("restore did not preserve replaced database for recovery")
	}
}

func assertRuntimeState(t *testing.T, db *sql.DB, wantEnabled bool, wantVersion, wantReceipts, wantAudits int64) {
	t.Helper()
	var enabled, version, receipts, audits int64
	if err := db.QueryRow(`SELECT restore_guard,control_version FROM runtime_control WHERE singleton=1`).Scan(&enabled, &version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM command_receipts WHERE scope='runtime/restore-guard'`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='runtime.restore_guard.close'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if (enabled == 1) != wantEnabled || version != wantVersion || receipts != wantReceipts || audits != wantAudits {
		t.Fatalf("runtime_control=(enabled=%d,version=%d) receipts=%d audits=%d, want (%t,%d,%d,%d)", enabled, version, receipts, audits, wantEnabled, wantVersion, wantReceipts, wantAudits)
	}
}
