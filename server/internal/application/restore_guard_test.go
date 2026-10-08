package application

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/store/sqlite"
)

func TestRestoreGuardIsAuditedVersionedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{now: 1_800_000_000_000}
	dbPath := filepath.Join(t.TempDir(), "restore-guard.db")
	st, err := sqlite.Open(dbPath, sqlite.Config{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close(context.Background()) }()
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatal(err)
	}
	seed, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(`INSERT INTO principals(principal_id,display_name,enabled,created_at) VALUES('ops-1','Operations',1,1)`); err != nil {
		_ = seed.Close()
		t.Fatalf("seed principal: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	service, err := NewRestoreGuardService(st, clock)
	if err != nil {
		t.Fatal(err)
	}
	auth := domain.AuthContext{PrincipalID: "ops-1", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermManage}}
	state, err := service.Get(ctx, auth)
	if err != nil || state.Enabled || state.ControlVersion != 1 {
		t.Fatalf("initial state = %+v, err=%v", state, err)
	}
	if _, err := service.Get(ctx, domain.AuthContext{PrincipalID: "read-only", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermRead}}); !errors.Is(err, ErrRestoreGuardForbidden) {
		t.Fatalf("read-only guard access err=%v", err)
	}

	closeInput := RestoreGuardInput{CommandID: "close-1", ExpectedControlVersion: 1, Enabled: true, EvidenceRef: "incident-review-1"}
	closed, err := service.Set(ctx, auth, closeInput)
	if err != nil || !closed.Enabled || closed.ControlVersion != 2 {
		t.Fatalf("close state = %+v, err=%v", closed, err)
	}
	// A lost HTTP reply followed by an exact retry returns the committed receipt.
	replayed, err := service.Set(ctx, auth, closeInput)
	if err != nil || replayed != closed {
		t.Fatalf("replay = %+v, err=%v; want %+v", replayed, err, closed)
	}
	if _, err := service.Set(ctx, auth, RestoreGuardInput{CommandID: "stale-open", ExpectedControlVersion: 1, Enabled: false, EvidenceRef: "incident-review-2"}); !errors.Is(err, ErrRestoreGuardConflict) {
		t.Fatalf("stale control version err=%v", err)
	}
	if _, err := service.Set(ctx, auth, RestoreGuardInput{CommandID: "close-1", ExpectedControlVersion: 1, Enabled: false, EvidenceRef: "incident-review-2"}); !errors.Is(err, ErrRestoreGuardConflict) {
		t.Fatalf("same command ID with different input err=%v", err)
	}

	clock.now++
	opened, err := service.Set(ctx, auth, RestoreGuardInput{CommandID: "open-1", ExpectedControlVersion: 2, Enabled: false, EvidenceRef: "reconciliation-1"})
	if err != nil || opened.Enabled || opened.ControlVersion != 3 {
		t.Fatalf("open state = %+v, err=%v", opened, err)
	}
	current, err := service.Get(ctx, auth)
	if err != nil || current != opened {
		t.Fatalf("current state = %+v, err=%v; want %+v", current, err, opened)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var guardAudits, receipts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action LIKE 'runtime.restore_guard.%'`).Scan(&guardAudits); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM command_receipts WHERE scope='runtime/restore-guard'`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if guardAudits != 2 || receipts != 2 {
		t.Fatalf("audits=%d receipts=%d, want 2 each (replay has no duplicate effect)", guardAudits, receipts)
	}
}

func TestRestoreGuardRequiresManageAndEvidence(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{now: 1}
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "restore-guard-auth.db"), sqlite.Config{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close(context.Background()) }()
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatal(err)
	}
	service, err := NewRestoreGuardService(st, clock)
	if err != nil {
		t.Fatal(err)
	}
	manage := domain.AuthContext{PrincipalID: "ops", Namespace: "ns", Permissions: []domain.Permission{domain.PermManage}}
	if _, err := service.Set(ctx, domain.AuthContext{PrincipalID: "reader", Namespace: "ns", Permissions: []domain.Permission{domain.PermRead}}, RestoreGuardInput{CommandID: "close", ExpectedControlVersion: 1, Enabled: true, EvidenceRef: "review"}); !errors.Is(err, ErrRestoreGuardForbidden) {
		t.Fatalf("unauthorized set err=%v", err)
	}
	for _, input := range []RestoreGuardInput{
		{CommandID: "", ExpectedControlVersion: 1, Enabled: true, EvidenceRef: "review"},
		{CommandID: "close", ExpectedControlVersion: 1, Enabled: true, EvidenceRef: ""},
		{CommandID: "close", ExpectedControlVersion: 0, Enabled: true, EvidenceRef: "review"},
	} {
		if _, err := service.Set(ctx, manage, input); !errors.Is(err, ErrRestoreGuardInvalid) {
			t.Fatalf("invalid input %+v err=%v", input, err)
		}
	}
}
