package integration

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

// errHashConflict marks the caller-side decision "same idempotency key, but a
// different input hash — reject instead of replaying".
var errHashConflict = errors.New("conflicting input hash for recorded receipt")

// blindInsertCmd inserts one receipt without the Get-dedup step.
func blindInsertCmd(scope, commandID domain.ID, inputHash string) store.WriteCommand {
	return store.WriteCommand{
		ID:     "receipt/" + string(commandID),
		Budget: 5 * time.Second,
		Run: func(tx store.WriterTx) error {
			return tx.InsertReceipt(store.ReceiptRecord{
				Scope:      scope,
				CommandID:  commandID,
				Kind:       "settle",
				InputHash:  inputHash,
				ResultJSON: []byte(`{"ok":true}`),
				CreatedAt:  1,
			})
		},
	}
}

// dedupCmd is the caller-side protocol under test: Get → compare inputHash →
// replay (no write) on match, reject on mismatch, insert when absent.
func dedupCmd(scope, commandID domain.ID, inputHash string) store.WriteCommand {
	return store.WriteCommand{
		ID:     "settle/" + string(commandID),
		Budget: 5 * time.Second,
		Run: func(tx store.WriterTx) error {
			rec, ok, err := tx.GetReceipt(scope, commandID)
			if err != nil {
				return err
			}
			if !ok {
				return tx.InsertReceipt(store.ReceiptRecord{
					Scope:      scope,
					CommandID:  commandID,
					Kind:       "settle",
					InputHash:  inputHash,
					ResultJSON: []byte(`{}`),
					CreatedAt:  2,
				})
			}
			if rec.InputHash != inputHash {
				return errHashConflict
			}
			return nil // replay the recorded result, no re-write
		},
	}
}

func TestReceiptDuplicatePKRejected(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	scope, cmd := domain.ID("settle"), domain.ID("att-1")

	if err := st.SubmitWrite(ctx, blindInsertCmd(scope, cmd, "hash-1")); err != nil {
		t.Fatalf("first receipt insert: %v", err)
	}
	// Same (scope, command_id) must fail on the primary key: the store never
	// silently overwrites an idempotency key.
	err := st.SubmitWrite(ctx, blindInsertCmd(scope, cmd, "hash-2"))
	if err == nil {
		t.Fatal("duplicate (scope, command_id) receipt was accepted, want PK violation")
	}
	if !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("want UNIQUE constraint error, got: %v", err)
	}
	db := openRawDB(t, path)
	if got := queryInt(t, db, "SELECT COUNT(*) FROM command_receipts WHERE scope = ? AND command_id = ?", string(scope), string(cmd)); got != 1 {
		t.Fatalf("receipt rows = %d, want 1", got)
	}
}

func TestReceiptGetThenCompareGuardsReplay(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	scope, cmd := domain.ID("settle"), domain.ID("att-1")
	if err := st.SubmitWrite(ctx, blindInsertCmd(scope, cmd, "hash-1")); err != nil {
		t.Fatalf("seed receipt: %v", err)
	}

	if err := st.SubmitWrite(ctx, dedupCmd(scope, cmd, "hash-2")); !errors.Is(err, errHashConflict) {
		t.Fatalf("conflicting hash = %v, want errHashConflict", err)
	}
	if err := st.SubmitWrite(ctx, dedupCmd(scope, cmd, "hash-1")); err != nil {
		t.Fatalf("same-hash replay rejected: %v", err)
	}

	db := openRawDB(t, filepath.Clean(path))
	var hash string
	if err := db.QueryRow("SELECT input_hash FROM command_receipts WHERE scope = ? AND command_id = ?",
		string(scope), string(cmd)).Scan(&hash); err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	if hash != "hash-1" {
		t.Fatalf("receipt hash = %s, want the original hash-1 (replay must not re-insert)", hash)
	}
}
