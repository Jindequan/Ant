package commands_test

import (
	"context"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/ledger/commands"
)

func managerAuth() domain.AuthContext {
	return domain.AuthContext{PrincipalID: "operator", Namespace: "ns", Permissions: []domain.Permission{domain.PermManage}}
}

func TestObservationRefreshChecksWatermarksAndReplays(t *testing.T) {
	f := settleFixtureNew(t)
	service := commands.NewAdminService(f.st, f.clock)
	evidence := domain.ObservationEvidence{
		ResourceID: "counter", SourceRef: "provider-snapshot-1", ObservedAt: 20, ExpiresAt: 1000,
		Remaining: 1, CommittedAtStart: 0, AdjustmentVersionAtStart: 0,
	}
	first, err := service.RecordObservation(context.Background(), managerAuth(), "observe-1", evidence)
	if err != nil || first.Status != "stored" || len(first.Reasons) != 0 {
		t.Fatalf("refresh=%+v err=%v", first, err)
	}
	if f.n(t, `SELECT COUNT(*) FROM observations WHERE resource_id='counter' AND valid=1`) != 1 || f.n(t, `SELECT COUNT(*) FROM audit_log WHERE action='resource.observation.refresh'`) != 1 {
		t.Fatal("observation or audit was not persisted")
	}
	replay, err := service.RecordObservation(context.Background(), managerAuth(), "observe-1", evidence)
	if err != nil || replay.Status != "stored" || f.n(t, `SELECT COUNT(*) FROM observations`) != 1 {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	stale := evidence
	stale.AdjustmentVersionAtStart = 9
	stale.SourceRef = "provider-snapshot-stale"
	result, err := service.RecordObservation(context.Background(), managerAuth(), "observe-stale", stale)
	if err != nil || result.Status != "rejectedStale" || len(result.Reasons) != 1 || result.Reasons[0].Code != "adjustment_changed" {
		t.Fatalf("stale refresh=%+v err=%v", result, err)
	}
	if f.n(t, `SELECT COUNT(*) FROM observations WHERE valid=1`) != 1 || f.n(t, `SELECT COUNT(*) FROM command_receipts WHERE kind='resource_observation'`) != 2 {
		t.Fatal("stale evidence changed admission state or was not receipted")
	}
}

func TestAdjustmentIsEvidenceBoundIdempotentAndCannotOverCorrect(t *testing.T) {
	f := settleFixtureNew(t)
	if _, err := f.settle(goodReport()); err != nil {
		t.Fatal(err)
	}
	service := commands.NewAdminService(f.st, f.clock)
	input := domain.AdjustmentInput{
		CommandID: "adjust-1", ResourceID: "counter", WindowKey: "sr:lifetime", Delta: -1,
		OriginalEventID: "settle/ns/a1/counter/sr:lifetime", EvidenceRefs: []domain.ID{"invoice-correction"},
	}
	ack, err := service.Adjust(context.Background(), managerAuth(), input)
	if err != nil || ack.Status != "applied" || f.n(t, `SELECT adjustment_units FROM resource_states WHERE resource_id='counter'`) != -1 || f.n(t, `SELECT adjustment_version FROM resource_states WHERE resource_id='counter'`) != 1 {
		t.Fatalf("adjust=%+v err=%v", ack, err)
	}
	ack, err = service.Adjust(context.Background(), managerAuth(), input)
	if err != nil || ack.Status != "replayed" || f.n(t, `SELECT COUNT(*) FROM resource_events WHERE event_kind='adjust'`) != 1 {
		t.Fatalf("replay=%+v err=%v", ack, err)
	}
	input.CommandID = "adjust-too-far"
	input.Delta = -2 // the named charge has only one unit left after its first correction
	if _, err = service.Adjust(context.Background(), managerAuth(), input); err != commands.ErrAdminConflict {
		t.Fatalf("over-correction error=%v", err)
	}
	if f.n(t, `SELECT adjustment_units FROM resource_states WHERE resource_id='counter'`) != -1 || f.n(t, `SELECT COUNT(*) FROM resource_events WHERE event_kind='adjust'`) != 1 {
		t.Fatal("invalid over-correction changed the ledger")
	}
}

func TestPositiveAdjustmentOverCapacityCreatesHoldAndClearHoldRespectsUnknownOccupancy(t *testing.T) {
	f := settleFixtureNew(t)
	if _, err := f.settle(goodReport()); err != nil {
		t.Fatal(err)
	}
	service := commands.NewAdminService(f.st, f.clock)
	input := domain.AdjustmentInput{CommandID: "adjust-overrun", ResourceID: "counter", WindowKey: "sr:lifetime", Delta: 19, EvidenceRefs: []domain.ID{"provider-invoice"}}
	if _, err := service.Adjust(context.Background(), managerAuth(), input); err != nil {
		t.Fatal(err)
	}
	if f.n(t, `SELECT COUNT(*) FROM holds WHERE reason='admin_adjustment_overrun' AND status='open' AND scope_id='counter'`) != 1 {
		t.Fatal("over-capacity correction did not hold the resource")
	}
	clear := domain.ClearHoldInput{CommandID: "clear-overrun", HoldID: "admin-adjustment/ns/adjust-overrun/counter", EvidenceIDs: []domain.ID{"provider-invoice-review"}}
	ack, err := service.ClearHold(context.Background(), managerAuth(), clear)
	if err != nil || ack.Status != "applied" || f.n(t, `SELECT COUNT(*) FROM holds WHERE hold_id=? AND status='cleared'`, clear.HoldID) != 1 {
		t.Fatalf("clear hold=%+v err=%v", ack, err)
	}
	ack, err = service.ClearHold(context.Background(), managerAuth(), clear)
	if err != nil || ack.Status != "replayed" {
		t.Fatalf("clear replay=%+v err=%v", ack, err)
	}

	g := settleFixtureNew(t)
	unknown := goodReport()
	unknown.Outcome, unknown.RetrySafety, unknown.ExternalEnded = "unknown", domain.Undetermined, false
	unknown.EndEvidenceRefs = nil
	unknown.Metrics[0].Actual = nil
	if reply, settleErr := g.settle(unknown); settleErr != nil || reply.Result == nil || reply.Result.State != domain.Unknown {
		t.Fatalf("unknown setup=%+v err=%v", reply, settleErr)
	}
	g.exec(t, `INSERT INTO holds(hold_id,scope_kind,scope_id,reason,status,evidence_json,created_at) VALUES('unknown-hold','resource','slot','manual_review','open','[]',20)`)
	clearUnknown := domain.ClearHoldInput{CommandID: "clear-unknown", HoldID: "unknown-hold", EvidenceIDs: []domain.ID{"operator-review"}}
	gService := commands.NewAdminService(g.st, g.clock)
	if _, err = gService.ClearHold(context.Background(), managerAuth(), clearUnknown); err != commands.ErrAdminConflict {
		t.Fatalf("unknown occupancy clear error=%v", err)
	}
	if g.n(t, `SELECT COUNT(*) FROM holds WHERE hold_id='unknown-hold' AND status='cleared'`) != 0 || g.n(t, `SELECT COUNT(*) FROM encumbrances WHERE attempt_id='a1' AND status='open'`) == 0 {
		t.Fatal("hold clear released or hid unknown occupancy")
	}
}
