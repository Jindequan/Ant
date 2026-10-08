package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

func TestBackupPublishesVerifiedPrivateSnapshotWithoutOverwrite(t *testing.T) {
	st := newInternalStore(t)
	if err := Migrate(context.Background(), st); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.SubmitWrite(context.Background(), store.WriteCommand{Run: func(tx store.WriterTx) error {
		return tx.InsertReceipt(store.ReceiptRecord{
			Scope: "backup-test", CommandID: "receipt-1", Kind: "test",
			InputHash: "hash", ResultJSON: []byte(`{"ok":true}`), CreatedAt: domain.Millis(1),
		})
	}}); err != nil {
		t.Fatalf("insert source record: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "backup.db")
	if err := st.Backup(context.Background(), target); err != nil {
		t.Fatalf("backup: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %o, want 600", info.Mode().Perm())
	}
	db, err := sql.Open("sqlite", target)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("SELECT result_json FROM command_receipts WHERE scope = ? AND command_id = ?", "backup-test", "receipt-1").Scan(&result); err != nil {
		t.Fatalf("read backed up receipt: %v", err)
	}
	if result != `{"ok":true}` {
		t.Fatalf("backed up result = %s", result)
	}
	if err := st.Backup(context.Background(), target); err == nil {
		t.Fatal("backup replaced an existing destination")
	}
}

func TestBackupRejectsSourceAndPublicDirectory(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.db")
	st, err := Open(source, Config{Clock: internalTestClock{}})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close(context.Background())
	if err := Migrate(context.Background(), st); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	concrete := st.(*Store)
	if err := concrete.Backup(context.Background(), source); err == nil {
		t.Fatal("backup accepted the source database as its destination")
	}
	public := filepath.Join(t.TempDir(), "public")
	if err := os.Mkdir(public, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := concrete.Backup(context.Background(), filepath.Join(public, "backup.db")); err == nil {
		t.Fatal("backup accepted a directory accessible to group/world")
	}
}
