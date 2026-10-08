package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

// K1 gate: saving a config document and reading it back must round-trip the
// exact bytes through the same typed store surface the services use.
func TestConfigSaveReadBackBytesIdentical(t *testing.T) {
	ctx := context.Background()
	st, _ := openTestStore(t, testConfig())
	fixture := readFixture(t, validConfigFixture)
	rev := domain.ID("rev-1")

	// Empty store: no active config, unknown revision not found.
	err := st.Read(ctx, func(rx store.ReaderTx) error {
		if _, ok, err := rx.GetActiveConfig(); err != nil {
			return err
		} else if ok {
			return errors.New("fresh store already has an active config")
		}
		if _, ok, err := rx.GetConfigRevision(rev); err != nil {
			return err
		} else if ok {
			return errors.New("fresh store already has the revision")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("pre-state read: %v", err)
	}

	if err := st.SubmitWrite(ctx, store.WriteCommand{
		ID:     "config-save",
		Budget: 2 * time.Second,
		Run: func(tx store.WriterTx) error {
			if err := tx.InsertConfigRevision(store.ConfigRevisionRecord{
				Revision:       rev,
				DefinitionJSON: fixture,
				CreatedAt:      42,
			}); err != nil {
				return err
			}
			return tx.SetActiveConfig(store.ActiveConfigRecord{Revision: rev, Version: 3})
		},
	}); err != nil {
		t.Fatalf("submit config save: %v", err)
	}

	err = st.Read(ctx, func(rx store.ReaderTx) error {
		act, ok, err := rx.GetActiveConfig()
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("active config missing after activation")
		}
		if act.Revision != rev || act.Version != 3 {
			return fmt.Errorf("active config = %s/%d, want rev-1/3", act.Revision, act.Version)
		}
		rec, ok, err := rx.GetConfigRevision(rev)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("revision missing after insert")
		}
		if !bytes.Equal(rec.DefinitionJSON, fixture) {
			return fmt.Errorf("definition bytes differ: got %d bytes, want %d", len(rec.DefinitionJSON), len(fixture))
		}
		if rec.CreatedAt != 42 {
			return fmt.Errorf("created_at = %d, want 42", rec.CreatedAt)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("roundtrip read: %v", err)
	}

	// ReaderTx covers the namespace listing too.
	err = st.Read(ctx, func(rx store.ReaderTx) error {
		pools, err := rx.ListPoolsByNamespace("ns-1")
		if err != nil {
			return err
		}
		if len(pools) != 0 {
			return fmt.Errorf("expected no pools, got %d", len(pools))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("list pools: %v", err)
	}
}
