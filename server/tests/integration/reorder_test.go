package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

// errStaleVersion marks the K4 command outcome "CAS found an unexpected pool
// version; the whole transaction must roll back".
var errStaleVersion = errors.New("integration: stale pool version")

type reorderResult struct{ controlVersion int64 }

// member builds one pool_members row for the new ordering.
func member(poolID, offerID domain.ID, priority int64) store.PoolMemberRecord {
	return store.PoolMemberRecord{PoolID: poolID, OfferID: offerID, Priority: priority, Enabled: true, Version: 1}
}

// reorderCommand is the K4 full-group reorder: version CAS on the pool, then
// members + config snapshot + activation + control version + receipt + audit
// in the same transaction. A CAS miss aborts everything (applied=false).
func reorderCommand(poolID domain.ID, expectedVersion int64, revision domain.ID, members []store.PoolMemberRecord, result *reorderResult) store.WriteCommand {
	return store.WriteCommand{
		ID:     "reorder/" + string(revision),
		Budget: 5 * time.Second,
		Run: func(tx store.WriterTx) error {
			if _, ok, err := tx.GetPool(poolID); err != nil {
				return err
			} else if !ok {
				return fmt.Errorf("pool %s missing", poolID)
			}
			applied, err := tx.ReplacePoolMembersCAS(poolID, expectedVersion, expectedVersion+1, members)
			if err != nil {
				return err
			}
			if !applied {
				return errStaleVersion
			}
			if err := tx.InsertConfigRevision(store.ConfigRevisionRecord{
				Revision:       revision,
				DefinitionJSON: []byte(`{"kind":"pool.reorder"}`),
				CreatedAt:      1,
			}); err != nil {
				return err
			}
			if err := tx.SetActiveConfig(store.ActiveConfigRecord{Revision: revision, Version: expectedVersion + 1}); err != nil {
				return err
			}
			cv, err := tx.BumpControlVersion()
			if err != nil {
				return err
			}
			if err := tx.InsertReceipt(store.ReceiptRecord{
				Scope:      "reorder",
				CommandID:  revision,
				Kind:       "pool.reorder",
				InputHash:  fmt.Sprintf("%s:v%d", poolID, expectedVersion),
				ResultJSON: []byte(`{"ok":true}`),
				CreatedAt:  1,
			}); err != nil {
				return err
			}
			if err := tx.InsertAudit(store.AuditRecord{
				AuditID:     revision,
				PrincipalID: "principal-1",
				Namespace:   "ns-1",
				Action:      "pool.reorder",
				TargetRef:   poolID,
				EvidenceRef: revision,
				CreatedAt:   1,
			}); err != nil {
				return err
			}
			result.controlVersion = cv
			return nil
		},
	}
}

func TestReorderAppliesAllChangesAtomically(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 3, "of-1", "of-2")
	execOK(t, db, `INSERT INTO pool_members(pool_id, offer_id, priority, enabled, version) VALUES('pool-1','of-1',10,1,1)`)
	execOK(t, db, `INSERT INTO pool_members(pool_id, offer_id, priority, enabled, version) VALUES('pool-1','of-2',20,1,1)`)

	newOrder := []store.PoolMemberRecord{member("pool-1", "of-2", 1), member("pool-1", "of-1", 2)}
	var res reorderResult
	if err := st.SubmitWrite(ctx, reorderCommand("pool-1", 3, "rev-2", newOrder, &res)); err != nil {
		t.Fatalf("reorder submit: %v", err)
	}
	if res.controlVersion != 2 {
		t.Fatalf("control version after first bump = %d, want 2", res.controlVersion)
	}

	err := st.Read(ctx, func(rx store.ReaderTx) error {
		pool, ok, err := rx.GetPool("pool-1")
		if err != nil {
			return err
		}
		if !ok || pool.Version != 4 {
			return fmt.Errorf("pool version = %d/ok=%v, want 4", pool.Version, ok)
		}
		members, err := rx.ListPoolMembers("pool-1")
		if err != nil {
			return err
		}
		if len(members) != 2 || members[0].OfferID != "of-2" || members[0].Priority != 1 || members[1].OfferID != "of-1" || members[1].Priority != 2 {
			return fmt.Errorf("members = %+v, want [of-2:1 of-1:2]", members)
		}
		act, ok, err := rx.GetActiveConfig()
		if err != nil {
			return err
		}
		if !ok || act.Revision != "rev-2" || act.Version != 4 {
			return fmt.Errorf("active config = %+v ok=%v, want rev-2/4", act, ok)
		}
		if _, ok, err := rx.GetConfigRevision("rev-2"); err != nil {
			return err
		} else if !ok {
			return errors.New("reorder config revision missing")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("post-reorder read: %v", err)
	}

	// Receipt is replayable; audit row exists; no duplicate revision rows.
	if got := queryInt(t, db, "SELECT COUNT(*) FROM command_receipts WHERE scope='reorder' AND command_id='rev-2'"); got != 1 {
		t.Fatalf("receipts = %d, want 1", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM audit_log WHERE audit_id='rev-2'"); got != 1 {
		t.Fatalf("audit rows = %d, want 1", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM config_revisions"); got != 2 {
		t.Fatalf("config revisions = %d, want 2 (rev-seed + rev-2)", got)
	}

	// Control version is stable across commands (no double bump).
	var cv int64
	if err := st.SubmitWrite(ctx, store.WriteCommand{ID: "cv-check", Budget: 2 * time.Second, Run: func(tx store.WriterTx) error {
		v, _, err := tx.GetControlVersion()
		cv = v
		return err
	}}); err != nil {
		t.Fatalf("control version check: %v", err)
	}
	if cv != res.controlVersion {
		t.Fatalf("control version drifted: %d, want %d", cv, res.controlVersion)
	}
}

func TestReorderCASMissRollsBackEverything(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 3, "of-1", "of-2")
	execOK(t, db, `INSERT INTO pool_members(pool_id, offer_id, priority, enabled, version) VALUES('pool-1','of-1',10,1,1)`)
	execOK(t, db, `INSERT INTO pool_members(pool_id, offer_id, priority, enabled, version) VALUES('pool-1','of-2',20,1,1)`)

	// First reorder succeeds: v3 → v4, rev-2 active.
	var first reorderResult
	if err := st.SubmitWrite(ctx, reorderCommand("pool-1", 3, "rev-2", []store.PoolMemberRecord{member("pool-1", "of-2", 1), member("pool-1", "of-1", 2)}, &first)); err != nil {
		t.Fatalf("first reorder: %v", err)
	}

	// Another writer moved the pool forward; the stale expectedVersion must
	// lose the CAS.
	execOK(t, db, "UPDATE resource_pools SET version = 9 WHERE pool_id = 'pool-1'")
	lateOrder := []store.PoolMemberRecord{member("pool-1", "of-1", 5)}
	err := st.SubmitWrite(ctx, reorderCommand("pool-1", 3, "rev-3", lateOrder, &reorderResult{}))
	if !errors.Is(err, errStaleVersion) {
		t.Fatalf("stale reorder error = %v, want errStaleVersion", err)
	}

	err = st.Read(ctx, func(rx store.ReaderTx) error {
		pool, ok, err := rx.GetPool("pool-1")
		if err != nil {
			return err
		}
		if !ok || pool.Version != 9 {
			return fmt.Errorf("pool version = %d ok=%v, want the externally set 9 (failed command must not touch it)", pool.Version, ok)
		}
		members, err := rx.ListPoolMembers("pool-1")
		if err != nil {
			return err
		}
		if len(members) != 2 || members[0].OfferID != "of-2" || members[1].OfferID != "of-1" {
			return fmt.Errorf("members changed after CAS miss: %+v", members)
		}
		act, ok, err := rx.GetActiveConfig()
		if err != nil {
			return err
		}
		if !ok || act.Revision != "rev-2" {
			return fmt.Errorf("active config changed after CAS miss: %+v", act)
		}
		if _, ok, err := rx.GetConfigRevision("rev-3"); err != nil {
			return err
		} else if ok {
			return errors.New("rev-3 survived a rolled-back command")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("post-miss read: %v", err)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM audit_log"); got != 1 {
		t.Fatalf("audit rows = %d, want 1 (no audit for the rolled-back command)", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM command_receipts"); got != 1 {
		t.Fatalf("receipts = %d, want 1 (no receipt for the rolled-back command)", got)
	}
	var cv int64
	if err := st.SubmitWrite(ctx, store.WriteCommand{ID: "cv-check", Budget: 2 * time.Second, Run: func(tx store.WriterTx) error {
		v, _, err := tx.GetControlVersion()
		cv = v
		return err
	}}); err != nil {
		t.Fatalf("control version check: %v", err)
	}
	if cv != first.controlVersion {
		t.Fatalf("control version moved on a rolled-back command: %d, want %d", cv, first.controlVersion)
	}
}

func TestReorderConcurrentExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 3, "of-1", "of-2")
	execOK(t, db, `INSERT INTO pool_members(pool_id, offer_id, priority, enabled, version) VALUES('pool-1','of-1',10,1,1)`)
	execOK(t, db, `INSERT INTO pool_members(pool_id, offer_id, priority, enabled, version) VALUES('pool-1','of-2',20,1,1)`)

	orderA := []store.PoolMemberRecord{member("pool-1", "of-1", 1), member("pool-1", "of-2", 2)}
	orderB := []store.PoolMemberRecord{member("pool-1", "of-2", 1), member("pool-1", "of-1", 2)}
	var resA, resB reorderResult
	errs := make(chan error, 2)
	go func() { errs <- st.SubmitWrite(ctx, reorderCommand("pool-1", 3, "rev-a", orderA, &resA)) }()
	go func() { errs <- st.SubmitWrite(ctx, reorderCommand("pool-1", 3, "rev-b", orderB, &resB)) }()

	wins := 0
	winner := domain.ID("rev-a")
	var winnerCV int64
	for i := 0; i < 2; i++ {
		err := <-errs
		switch {
		case err == nil:
			wins++
		case errors.Is(err, errStaleVersion):
		default:
			t.Fatalf("unexpected reorder error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("winners = %d, want exactly 1", wins)
	}
	if resA.controlVersion != 0 {
		winner, winnerCV = "rev-a", resA.controlVersion
	}
	if resB.controlVersion != 0 {
		winner, winnerCV = "rev-b", resB.controlVersion
	}

	err := st.Read(ctx, func(rx store.ReaderTx) error {
		pool, ok, err := rx.GetPool("pool-1")
		if err != nil {
			return err
		}
		if !ok || pool.Version != 4 {
			return fmt.Errorf("pool version = %d ok=%v, want 4 (single increment)", pool.Version, ok)
		}
		members, err := rx.ListPoolMembers("pool-1")
		if err != nil {
			return err
		}
		if len(members) != 2 {
			return fmt.Errorf("members = %+v, want exactly 2", members)
		}
		act, ok, err := rx.GetActiveConfig()
		if err != nil {
			return err
		}
		if !ok || string(act.Revision) != string(winner) {
			return fmt.Errorf("active config = %+v, want winner %s", act, winner)
		}
		want := orderA
		if winner == "rev-b" {
			want = orderB
		}
		for i, m := range members {
			if m.OfferID != want[i].OfferID || m.Priority != want[i].Priority {
				return fmt.Errorf("members %+v do not match winner ordering %+v", members, want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("post-race read: %v", err)
	}
	if winnerCV != 2 {
		t.Fatalf("winner control version = %d, want 2 (one bump total)", winnerCV)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM config_revisions"); got != 2 {
		t.Fatalf("config revisions = %d, want 2 (rev-seed + exactly one winner revision)", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM audit_log"); got != 1 {
		t.Fatalf("audit rows = %d, want 1", got)
	}
	if got := queryInt(t, db, "SELECT COUNT(*) FROM command_receipts"); got != 1 {
		t.Fatalf("receipts = %d, want 1", got)
	}
}
