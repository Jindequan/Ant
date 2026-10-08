package commands_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/ledger/commands"
)

func TestReconcileUnknownAttemptAppendsEvidenceAndCorrectsWithoutRewritingResult(t *testing.T) {
	f := settleFixtureNew(t)
	original := goodReport()
	original.Outcome = "unknown"
	original.RetrySafety = domain.Undetermined
	original.ExternalEnded = false
	original.EndEvidenceRefs = nil
	original.Metrics[0].Actual = nil
	settled, err := f.settle(original)
	if err != nil || settled.Result == nil || settled.Result.State != domain.Unknown {
		t.Fatalf("settle unknown = %+v, %v", settled, err)
	}
	var resultBefore []byte
	if err = f.db.QueryRow(`SELECT result_json FROM operations WHERE operation_id='op'`).Scan(&resultBefore); err != nil {
		t.Fatal(err)
	}
	service := commands.NewReconciliationService(f.st, f.clock)
	actual := domain.Units(2)
	input := domain.ReconciliationInput{
		CommandID: "review-1", AttemptID: "a1", ConfirmedOutcome: "known_success", RetrySafety: domain.Unsafe,
		EvidenceRefs: []domain.ID{"operator-review"},
		Corrections: []domain.ResourceOutcome{
			{ResourceID: "counter", WindowKey: "sr:lifetime", MetricID: "tokens", Unit: "token", Consumed: &actual, ConsumptionBasis: domain.ConsumptionAdjustment, Occupancy: "not_applicable"},
			{ResourceID: "slot", WindowKey: "sr:lifetime", MetricID: "concurrency", Unit: "slot", Consumed: nil, ConsumptionBasis: domain.ConsumptionNotApplicable, Occupancy: "released"},
		},
	}
	manager := domain.AuthContext{PrincipalID: "operator", Namespace: "ns", Permissions: []domain.Permission{domain.PermManage}}
	view, err := service.Reconcile(context.Background(), manager, input)
	if err != nil {
		t.Fatal(err)
	}
	if view.ConfirmedOutcome != "known_success" || view.ReconciliationID == "" || len(view.Corrections) != 2 {
		t.Fatalf("reconciliation = %+v", view)
	}
	if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 3 ||
		f.n(t, `SELECT adjustment_units FROM resource_states WHERE resource_id='counter'`) != -1 ||
		f.n(t, `SELECT uncertain_units FROM resource_states WHERE resource_id='slot'`) != 0 ||
		f.n(t, `SELECT COUNT(*) FROM encumbrances WHERE attempt_id='a1' AND kind='occupancy' AND status='released'`) != 1 {
		t.Fatal("late evidence did not correct conservative accounting and release the matching occupancy encumbrance")
	}
	var resultAfter []byte
	if err = f.db.QueryRow(`SELECT result_json FROM operations WHERE operation_id='op'`).Scan(&resultAfter); err != nil {
		t.Fatal(err)
	}
	var before, after domain.OperationResult
	if json.Unmarshal(resultBefore, &before) != nil || json.Unmarshal(resultAfter, &after) != nil || !reflect.DeepEqual(before, after) || after.State != domain.Unknown {
		t.Fatal("reconciliation rewrote the immutable terminal operation result")
	}
	if f.n(t, `SELECT COUNT(*) FROM reconciliations WHERE attempt_id='a1'`) != 1 || f.n(t, `SELECT COUNT(*) FROM command_receipts WHERE scope='reconcile/ns/a1' AND command_id='review-1'`) != 1 || f.n(t, `SELECT COUNT(*) FROM operation_events WHERE event_type='reconciled'`) != 1 {
		t.Fatal("reconciliation audit, receipt, or lifecycle event missing")
	}
	replay, err := service.Reconcile(context.Background(), manager, input)
	if err != nil || !reflect.DeepEqual(replay, view) || f.n(t, `SELECT COUNT(*) FROM reconciliations WHERE attempt_id='a1'`) != 1 {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	corrected := domain.Units(1)
	input.CommandID = "review-2"
	input.Corrections[0].Consumed = &corrected
	if _, err = service.Reconcile(context.Background(), manager, input); err != nil {
		t.Fatalf("second evidence correction: %v", err)
	}
	if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 3 || f.n(t, `SELECT adjustment_units FROM resource_states WHERE resource_id='counter'`) != -2 ||
		f.n(t, `SELECT COUNT(*) FROM reconciliations WHERE attempt_id='a1'`) != 2 || f.n(t, `SELECT uncertain_units FROM resource_states WHERE resource_id='slot'`) != 0 {
		t.Fatal("a later correction was not based on the previously reconciled amount")
	}
	input.CommandID = "review-wrong-attempt"
	input.AttemptID = "other"
	if _, err = service.Reconcile(context.Background(), manager, input); err == nil {
		t.Fatal("unknown unrelated attempt was reconciled")
	}
}

func TestReconcileUsesPreviouslyReportedUsageAsCorrectionBaseline(t *testing.T) {
	f := settleFixtureNew(t)
	original := goodReport()
	original.Outcome = "unknown"
	original.RetrySafety = domain.Undetermined
	original.ExternalEnded = false
	original.EndEvidenceRefs = nil
	previous := domain.Units(1)
	original.Metrics[0].Actual = &previous
	if reply, err := f.settle(original); err != nil || reply.Result == nil || reply.Result.State != domain.Unknown {
		t.Fatalf("settle unknown with usage = %+v, %v", reply, err)
	}
	actual := domain.Units(2)
	input := domain.ReconciliationInput{
		CommandID: "review-actual", AttemptID: "a1", ConfirmedOutcome: "known_success", RetrySafety: domain.Unsafe,
		EvidenceRefs: []domain.ID{"provider-record"}, Corrections: []domain.ResourceOutcome{
			{ResourceID: "counter", WindowKey: "sr:lifetime", MetricID: "tokens", Unit: "token", Consumed: &actual, ConsumptionBasis: domain.ConsumptionAdjustment, Occupancy: "not_applicable"},
			{ResourceID: "slot", WindowKey: "sr:lifetime", MetricID: "concurrency", Unit: "slot", ConsumptionBasis: domain.ConsumptionNotApplicable, Occupancy: "released"},
		},
	}
	service := commands.NewReconciliationService(f.st, f.clock)
	manager := domain.AuthContext{PrincipalID: "operator", Namespace: "ns", Permissions: []domain.Permission{domain.PermManage}}
	if _, err := service.Reconcile(context.Background(), manager, input); err != nil {
		t.Fatal(err)
	}
	if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 1 ||
		f.n(t, `SELECT adjustment_units FROM resource_states WHERE resource_id='counter'`) != 1 {
		t.Fatal("reconciliation was applied against the reservation bound instead of the amount already reported")
	}
}

func TestReconcileClearsResolvedOutOfRangeUsageHoldWithoutFalseOverrun(t *testing.T) {
	f := settleFixtureNew(t)
	original := goodReport()
	original.Metrics[0].Actual = nil
	original.Metrics[0].ActualOutOfRangeDecimal = "9223372036854775808"
	if reply, err := f.settle(original); err != nil || reply.Result == nil || reply.Result.State != domain.Unknown {
		t.Fatalf("settle out-of-range report: %+v %v", reply, err)
	}
	actual := domain.Units(2) // the corrected amount is representable and within the original bound of 3
	input := domain.ReconciliationInput{
		CommandID: "review-range", AttemptID: "a1", ConfirmedOutcome: "known_success", RetrySafety: domain.Unsafe,
		EvidenceRefs: []domain.ID{"provider-invoice"}, Corrections: []domain.ResourceOutcome{
			{ResourceID: "counter", WindowKey: "sr:lifetime", MetricID: "tokens", Unit: "token", Consumed: &actual, ConsumptionBasis: domain.ConsumptionAdjustment, Occupancy: "not_applicable"},
			{ResourceID: "slot", WindowKey: "sr:lifetime", MetricID: "concurrency", Unit: "slot", ConsumptionBasis: domain.ConsumptionNotApplicable, Occupancy: "released"},
		},
	}
	service := commands.NewReconciliationService(f.st, f.clock)
	manager := domain.AuthContext{PrincipalID: "operator", Namespace: "ns", Permissions: []domain.Permission{domain.PermManage}}
	if _, err := service.Reconcile(context.Background(), manager, input); err != nil {
		t.Fatal(err)
	}
	if f.n(t, `SELECT adjustment_units FROM resource_states WHERE resource_id='counter'`) != 2 ||
		f.n(t, `SELECT COUNT(*) FROM holds WHERE scope_id='counter' AND status='open'`) != 0 ||
		f.n(t, `SELECT COUNT(*) FROM holds WHERE scope_id='counter' AND reason='actual_out_of_range' AND status='cleared'`) != 1 {
		t.Fatal("resolved out-of-range usage kept its stale hold or invented an overrun hold")
	}
}

func TestReconciliationRollsBackEveryDurableWriteBoundary(t *testing.T) {
	for _, table := range []string{"resource_events", "encumbrances", "holds", "reconciliations", "command_receipts", "audit_log", "operation_events"} {
		t.Run(table, func(t *testing.T) {
			f := settleFixtureNew(t)
			original := goodReport()
			original.Outcome = "unknown"
			original.RetrySafety = domain.Undetermined
			original.ExternalEnded = false
			original.EndEvidenceRefs = nil
			original.Metrics[0].Actual = nil
			if reply, err := f.settle(original); err != nil || reply.Result == nil || reply.Result.State != domain.Unknown {
				t.Fatalf("setup unknown operation: %+v %v", reply, err)
			}
			actual := domain.Units(2)
			if table == "holds" {
				actual = 4 // exercise a new overrun hold before the injected failure
			}
			input := domain.ReconciliationInput{
				CommandID: "rollback-" + domain.ID(table), AttemptID: "a1", ConfirmedOutcome: "known_success", RetrySafety: domain.Unsafe,
				EvidenceRefs: []domain.ID{"provider-record"}, Corrections: []domain.ResourceOutcome{
					{ResourceID: "counter", WindowKey: "sr:lifetime", MetricID: "tokens", Unit: "token", Consumed: &actual, ConsumptionBasis: domain.ConsumptionAdjustment, Occupancy: "not_applicable"},
					{ResourceID: "slot", WindowKey: "sr:lifetime", MetricID: "concurrency", Unit: "slot", ConsumptionBasis: domain.ConsumptionNotApplicable, Occupancy: "released"},
				},
			}
			trigger := "fail_reconcile"
			mode := "INSERT"
			if table == "encumbrances" {
				mode = "UPDATE"
			}
			f.exec(t, `CREATE TRIGGER `+trigger+` BEFORE `+mode+` ON `+table+` BEGIN SELECT RAISE(ABORT,'synthetic reconciliation failure'); END`)
			service := commands.NewReconciliationService(f.st, f.clock)
			manager := domain.AuthContext{PrincipalID: "operator", Namespace: "ns", Permissions: []domain.Permission{domain.PermManage}}
			if _, err := service.Reconcile(context.Background(), manager, input); err == nil {
				t.Fatal("injected transaction failure unexpectedly succeeded")
			}
			if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 3 ||
				f.n(t, `SELECT adjustment_units FROM resource_states WHERE resource_id='counter'`) != 0 ||
				f.n(t, `SELECT uncertain_units FROM resource_states WHERE resource_id='slot'`) != 1 ||
				f.n(t, `SELECT COUNT(*) FROM encumbrances WHERE attempt_id='a1' AND kind='occupancy' AND status='open'`) != 1 ||
				f.n(t, `SELECT COUNT(*) FROM reconciliations WHERE attempt_id='a1'`) != 0 ||
				f.n(t, `SELECT COUNT(*) FROM command_receipts WHERE command_id=?`, input.CommandID) != 0 ||
				f.n(t, `SELECT COUNT(*) FROM operation_events WHERE event_type='reconciled'`) != 0 {
				t.Fatal("reconciliation left partial durable state after injected failure")
			}
			f.exec(t, `DROP TRIGGER `+trigger)
			if _, err := service.Reconcile(context.Background(), manager, input); err != nil {
				t.Fatalf("retry after rolled-back command: %v", err)
			}
		})
	}
}
