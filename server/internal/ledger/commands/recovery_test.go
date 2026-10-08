package commands_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/ledger/commands"
)

// LED-04 recovery tests: the K3 abort matrix rows (prepare后改序, authorize后
// 改序, consume 回复丢失/崩溃, prepared 取消), cancel intent semantics, lease
// renewal vs sweep competition, sweep takeover per 03-EXECUTION §7/§9, the
// six-way result competition on recovery paths (A15), old-owner rejection
// (A04/A20) and fail-closed integrity. All on synthetic local SQLite, real
// store, no network. The shared fixture lives in settle_test.go.

var (
	cancelAuth = domain.AuthContext{PrincipalID: "synthetic", Namespace: "ns", Permissions: []domain.Permission{domain.PermCancel}}
	manageAuth = domain.AuthContext{PrincipalID: "runtime", Namespace: "ns", Permissions: []domain.Permission{domain.PermManage}}
)

func (f *settleFixture) text(t *testing.T, q string) string {
	t.Helper()
	var s string
	if err := f.db.QueryRow(q).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}

func (f *settleFixture) claimOperation(t *testing.T, opID domain.ID) domain.OwnerToken {
	t.Helper()
	input := domain.ExecuteInput{OperationID: opID, PoolID: "pool", Request: domain.DomainRequest{Domain: "ai/text", CapabilityProfile: "profile", Payload: json.RawMessage(`{"prompt":"synthetic"}`)}, DeadlineAt: 10000, MaxAttempts: 3}
	fingerprint, err := domain.SemanticFingerprint(domain.FingerprintInput{Domain: input.Request.Domain, PoolID: input.PoolID, CapabilityProfile: input.Request.CapabilityProfile, Payload: input.Request.Payload})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := commands.Claim(context.Background(), f.st, f.auth, input, fingerprint, commands.ClaimPolicy{RuntimeID: "runtime", Now: f.clock.Now(), LeaseMillis: 8000, MaxRuntimeAttempts: 3})
	if err != nil || claim.Kind != "new" {
		t.Fatalf("claim %s: %+v %v", opID, claim, err)
	}
	return *claim.Owner
}

// freshRunningOperation settles the fixture's dispatched attempt a1 and opens
// a second running operation, so recovery flows start from a clean owner.
func freshRunningOperation(t *testing.T) (*settleFixture, domain.OwnerToken) {
	t.Helper()
	f := settleFixtureNew(t)
	if _, err := f.settle(goodReport()); err != nil {
		t.Fatal(err)
	}
	return f, f.claimOperation(t, "op2")
}

func (f *settleFixture) prepare(t *testing.T, owner domain.OwnerToken, id domain.ID) {
	t.Helper()
	p := f.plan
	p.PlanID = "plan-" + id
	reply, err := f.prep.TryPrepare(context.Background(), f.auth, owner, id, p, nil, "digest", nil, f.clock.Now())
	if err != nil || reply.Kind != "prepared" {
		t.Fatalf("prepare %s: %+v %v", id, reply, err)
	}
}

func (f *settleFixture) authorize(t *testing.T, owner domain.OwnerToken, id domain.ID) ledger.DispatchPermit {
	t.Helper()
	f.prepare(t, owner, id)
	permit, err := f.prep.AuthorizeDispatch(context.Background(), f.auth, owner, id, f.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	return permit
}

func (f *settleFixture) dispatchPermit(t *testing.T, owner domain.OwnerToken, id domain.ID) ledger.DispatchPermit {
	t.Helper()
	permit := f.authorize(t, owner, id)
	if err := f.prep.ConsumeDispatchPermit(context.Background(), f.auth, owner, permit, "digest", f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	return permit
}

func (f *settleFixture) abort(t *testing.T, owner domain.OwnerToken, id domain.ID, proof ledger.UnsentProof, permit ledger.DispatchPermit) (ledger.AbortOutcome, error) {
	t.Helper()
	return f.prep.AbortBeforeDispatch(context.Background(), f.auth, commands.AbortRequest{Owner: owner, Attempt: id, Proof: proof, Permit: permit})
}

func TestAbortClosesPreparedAttempt(t *testing.T) {
	f, owner := freshRunningOperation(t)
	f.prepare(t, owner, "a2")
	outcome, err := f.abort(t, owner, "a2", ledger.ProofNone, ledger.DispatchPermit{})
	if err != nil || outcome.Kind != ledger.AbortClosePrepared || !outcome.ReplanAllowed || !outcome.ReleaseCounter || !outcome.ReleaseOccupancy {
		t.Fatalf("%+v %v", outcome, err)
	}
	if f.text(t, `SELECT phase||'/'||external_outcome FROM attempts WHERE attempt_id='a2'`) != "terminal/not_executed" ||
		f.n(t, `SELECT COUNT(*) FROM reservations WHERE attempt_id='a2' AND phase='released'`) != 2 ||
		f.n(t, `SELECT reserved_units FROM resource_states WHERE resource_id='counter'`) != 0 ||
		f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 2 || // a1's settled charge stays
		f.n(t, `SELECT reserved_units+active_units FROM resource_states WHERE resource_id='slot'`) != 0 ||
		f.text(t, `SELECT state FROM operations WHERE operation_id='op2'`) != "running" {
		t.Fatal("prepared close left durable drift")
	}
	if f.n(t, `SELECT COUNT(*) FROM resource_events WHERE command_scope='abort/ns' AND event_kind='release'`) != 2 ||
		f.n(t, `SELECT COUNT(*) FROM command_receipts WHERE scope='abort/ns'`) != 1 {
		t.Fatal("abort events/receipt missing")
	}
	again, err := f.abort(t, owner, "a2", ledger.ProofNone, ledger.DispatchPermit{})
	if err != nil || !reflect.DeepEqual(again, outcome) {
		t.Fatalf("replay changed conclusion: %+v vs %+v (%v)", again, outcome, err)
	}
	if f.n(t, `SELECT COUNT(*) FROM command_receipts WHERE scope='abort/ns'`) != 1 {
		t.Fatal("replay duplicated receipt")
	}
	// Replan: the single-active-attempt slot is free again (K3 下次 prepare).
	f.prepare(t, owner, "a3")
}

func TestCompleteWithoutAttemptPersistsResolvedResultAndReplays(t *testing.T) {
	f, owner := freshRunningOperation(t)
	reasons := []domain.Reason{{Group: "eligibility", Code: "member_disabled", ScopeRef: "offer-a"}}
	result, err := f.prep.CompleteWithoutAttempt(context.Background(), f.auth, owner, reasons)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != domain.Failed || result.Code != domain.ResultCodeAdmissionBlocked || result.OperationID != owner.Key.OperationID || len(result.Attempts) != 0 || len(result.Reasons) != 1 || result.Reasons[0] != reasons[0] {
		t.Fatalf("resolved result = %+v", result)
	}
	if f.text(t, `SELECT state FROM operations WHERE operation_id='op2'`) != "failed" || f.n(t, `SELECT COUNT(*) FROM operation_events WHERE operation_id='op2' AND event_type='operation_terminal'`) != 1 || f.n(t, `SELECT COUNT(*) FROM audit_log WHERE target_ref='op2' AND action='complete_without_attempt'`) != 1 {
		t.Fatal("terminal state, event, and audit were not committed together")
	}
	replay, err := f.prep.CompleteWithoutAttempt(context.Background(), f.auth, owner, []domain.Reason{{Group: "changed", Code: "ignored"}})
	if err != nil || !reflect.DeepEqual(replay, result) {
		t.Fatalf("completion replay = %+v, want %+v: %v", replay, result, err)
	}
	if f.n(t, `SELECT COUNT(*) FROM operation_events WHERE operation_id='op2' AND event_type='operation_terminal'`) != 1 || f.n(t, `SELECT COUNT(*) FROM audit_log WHERE target_ref='op2' AND action='complete_without_attempt'`) != 1 {
		t.Fatal("replay duplicated terminal side effects")
	}
}

func TestCompleteWithoutAttemptRejectsActiveAttempt(t *testing.T) {
	f := settleFixtureNew(t)
	_, err := f.prep.CompleteWithoutAttempt(context.Background(), f.auth, f.owner, []domain.Reason{{Group: "admission", Code: "blocked"}})
	if !errors.Is(err, commands.ErrCompleteWithoutAttempt) {
		t.Fatalf("CompleteWithoutAttempt() error = %v, want active-attempt rejection", err)
	}
	if f.text(t, `SELECT state FROM operations WHERE operation_id='op'`) != "running" {
		t.Fatal("active operation was incorrectly terminalized")
	}
}

func TestCompleteWithoutAttemptCarriesSettledAttemptFactsForward(t *testing.T) {
	f := settleFixtureNew(t)
	f.service = commands.NewSettlementService(f.st, "runtime", f.clock, commands.SettlementPolicy{
		AllowContinue: true,
		RetryAllowed:  func(domain.ExecutionPlan, domain.ExecutionReport) bool { return true },
	}, f.prep)
	report := goodReport()
	report.Outcome = "known_failure"
	report.RetrySafety = domain.ProvedSafe
	settled, err := f.settle(report)
	if err != nil || !settled.Continue {
		t.Fatalf("settlement did not continue: %+v %v", settled, err)
	}
	reasons := []domain.Reason{{Group: "eligibility", Code: "member_disabled"}}
	result, err := f.prep.CompleteWithoutAttempt(context.Background(), f.auth, f.owner, reasons)
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != domain.ResultCodeAdmissionBlocked || !reflect.DeepEqual(result.Attempts, []domain.ID{"a1"}) || len(result.Resources) != 2 || result.Resources[0].Consumed == nil || *result.Resources[0].Consumed != 2 {
		t.Fatalf("terminal result lost prior attempt facts: %+v", result)
	}
}

func TestAbortRevokesGrantedPermit(t *testing.T) {
	f, owner := freshRunningOperation(t)
	permit := f.authorize(t, owner, "a2")
	outcome, err := f.abort(t, owner, "a2", ledger.ProofNone, ledger.DispatchPermit{})
	if err != nil || outcome.Kind != ledger.AbortRevokeGranted || !outcome.RevokePermit {
		t.Fatalf("%+v %v", outcome, err)
	}
	if f.text(t, `SELECT status FROM dispatch_permits WHERE attempt_id='a2'`) != "revoked" {
		t.Fatal("permit not revoked")
	}
	if f.n(t, `SELECT permits_issued FROM operations WHERE operation_id='op2'`) != 1 || // granted budget kept
		f.n(t, `SELECT active_units+reserved_units FROM resource_states WHERE resource_id='slot'`) != 0 ||
		f.n(t, `SELECT reserved_units FROM resource_states WHERE resource_id='counter'`) != 0 ||
		f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 2 {
		t.Fatal("revoke path misapplied resources")
	}
	// Old owner loses send standing: the revoked permit is rejected by the
	// runtime registry fast gate before any durable check.
	if err := f.prep.ConsumeDispatchPermit(context.Background(), f.auth, owner, permit, "digest", f.clock.Now()); !errors.Is(err, ledger.ErrPermitUnknown) {
		t.Fatalf("terminal permit record should be released, got %v", err)
	}
	if _, err := f.prep.AuthorizeDispatch(context.Background(), f.auth, owner, "a2", f.clock.Now()); !errors.Is(err, commands.ErrPreparationStale) {
		t.Fatalf("authorize after abort: %v", err)
	}
	// Zero send: nothing was consumed, replan keeps working.
	f.prepare(t, owner, "a3")
}

func TestAbortAfterConsumeWithoutProofConserves(t *testing.T) {
	f := settleFixtureNew(t) // a1 prepared+authorized+consumed, operation running
	if ack, err := f.prep.RequestCancel(context.Background(), cancelAuth, f.owner.Key, "c1"); err != nil || ack.Status != "recorded" {
		t.Fatalf("cancel: %+v %v", ack, err)
	}
	if f.text(t, `SELECT phase FROM attempts WHERE attempt_id='a1'`) != "executing" {
		t.Fatal("executing cancel must not close the attempt")
	}
	outcome, err := f.abort(t, f.owner, "a1", ledger.ProofNone, ledger.DispatchPermit{})
	if err != nil || outcome.Kind != ledger.AbortConserveUnknown || outcome.ReplanAllowed {
		t.Fatalf("%+v %v", outcome, err)
	}
	// No proof of non-execution: conservative unknown, never a release.
	if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 3 ||
		f.n(t, `SELECT reserved_units FROM resource_states WHERE resource_id='counter'`) != 0 ||
		f.n(t, `SELECT uncertain_units FROM resource_states WHERE resource_id='slot'`) != 1 ||
		f.n(t, `SELECT active_units FROM resource_states WHERE resource_id='slot'`) != 0 ||
		f.n(t, `SELECT COUNT(*) FROM encumbrances WHERE kind='occupancy' AND status='open'`) != 1 {
		t.Fatal("conservation misapplied")
	}
	var result domain.OperationResult
	if err := json.Unmarshal([]byte(f.text(t, `SELECT result_json FROM operations WHERE operation_id='op'`)), &result); err != nil || result.State != domain.Unknown {
		t.Fatalf("unknown must outrank cancel: %+v %v", result, err)
	}
	if !stringsContainsCode(result.Reasons, "cancel_requested") {
		t.Fatal("cancel diagnostic lost on unknown result")
	}
	if f.text(t, `SELECT external_outcome FROM attempts WHERE attempt_id='a1'`) != "unknown" {
		t.Fatal("attempt not conserved as unknown")
	}
	// No automatic re-dispatch: the operation is terminal for planning.
	if _, err := f.prep.TryPrepare(context.Background(), f.auth, f.owner, domain.ID("a2"), f.plan, nil, "digest", nil, f.clock.Now()); !errors.Is(err, commands.ErrPreparationStale) {
		t.Fatalf("prepare after unknown: %v", err)
	}
}

func TestAbortReliableUnsentProof(t *testing.T) {
	f, owner := freshRunningOperation(t)
	// Fail-closed negatives first (they leave no durable effect).
	if _, err := f.abort(t, owner, "a2", ledger.ProofReliableUnsent, ledger.DispatchPermit{}); !errors.Is(err, ledger.ErrAbortInvalidState) {
		t.Fatalf("missing handle accepted: %v", err)
	}
	grantedPermit := f.authorize(t, owner, "a2")
	if _, err := f.abort(t, owner, "a2", ledger.ProofReliableUnsent, grantedPermit); !errors.Is(err, ledger.ErrAbortInvalidState) {
		t.Fatalf("granted permit accepted as unsent proof: %v", err)
	}
	// Foreign attempt: the handle of a2 presented for a1.
	if _, err := f.abort(t, f.owner, "a1", ledger.ProofReliableUnsent, grantedPermit); !errors.Is(err, ledger.ErrAbortInvalidState) {
		t.Fatalf("foreign attempt handle accepted: %v", err)
	}
	if f.n(t, `SELECT COUNT(*) FROM attempts WHERE namespace='ns' AND operation_id='op2' AND phase='terminal'`) != 0 {
		t.Fatal("negative paths terminalized an attempt")
	}
	// Close the granted attempt (revoke), then prove reliable unsent on a
	// consumed attempt with its own handle.
	if outcome, err := f.abort(t, owner, "a2", ledger.ProofNone, ledger.DispatchPermit{}); err != nil || outcome.Kind != ledger.AbortRevokeGranted {
		t.Fatalf("cleanup revoke: %+v %v", outcome, err)
	}
	permit := f.dispatchPermit(t, owner, "a3")
	outcome, err := f.abort(t, owner, "a3", ledger.ProofReliableUnsent, permit)
	if err != nil || outcome.Kind != ledger.AbortSettleNotExecuted || !outcome.ReplanAllowed {
		t.Fatalf("%+v %v", outcome, err)
	}
	if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 2 || // only a1's charge
		f.n(t, `SELECT reserved_units FROM resource_states WHERE resource_id='counter'`) != 0 ||
		f.n(t, `SELECT active_units+uncertain_units FROM resource_states WHERE resource_id='slot'`) != 0 ||
		f.n(t, `SELECT COUNT(*) FROM encumbrances`) != 0 ||
		f.n(t, `SELECT COUNT(*) FROM attempts WHERE attempt_id='a3' AND phase='terminal' AND external_outcome='not_executed'`) != 1 {
		t.Fatal("reliable-unsent settlement misapplied")
	}
	f.prepare(t, owner, "a4")
}

func TestCancelPreparedClosesAndCancels(t *testing.T) {
	f, owner := freshRunningOperation(t)
	f.prepare(t, owner, "a2")
	ack, err := f.prep.RequestCancel(context.Background(), cancelAuth, owner.Key, "c1")
	if err != nil || ack.Status != "recorded" {
		t.Fatalf("%+v %v", ack, err)
	}
	if f.n(t, `SELECT COUNT(*) FROM attempts WHERE attempt_id='a2' AND phase='terminal' AND external_outcome='not_executed'`) != 1 ||
		f.n(t, `SELECT COUNT(*) FROM reservations WHERE attempt_id='a2' AND phase='released'`) != 2 ||
		f.n(t, `SELECT reserved_units+active_units FROM resource_states WHERE resource_id='slot'`) != 0 {
		t.Fatal("prepared cancel did not close and release")
	}
	var result domain.OperationResult
	if err := json.Unmarshal([]byte(f.text(t, `SELECT result_json FROM operations WHERE operation_id='op2'`)), &result); err != nil || result.State != domain.Cancelled || result.Code != domain.ResultCodeCancelled {
		t.Fatalf("cancel result wrong: %+v %v", result, err)
	}
	if again, err := f.prep.RequestCancel(context.Background(), cancelAuth, owner.Key, "c1"); err != nil || again.Status != "recorded" {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if late, err := f.prep.RequestCancel(context.Background(), cancelAuth, owner.Key, "c2"); err != nil || late.Status != "alreadyTerminal" {
		t.Fatalf("terminal cancel: %+v %v", late, err)
	}
}

func TestCancelExecutingIntentOnlyThenSuccessWins(t *testing.T) {
	f := settleFixtureNew(t)
	ack, err := f.prep.RequestCancel(context.Background(), cancelAuth, f.owner.Key, "c1")
	if err != nil || ack.Status != "recorded" {
		t.Fatalf("%+v %v", ack, err)
	}
	if f.text(t, `SELECT phase FROM attempts WHERE attempt_id='a1'`) != "executing" || f.text(t, `SELECT state FROM operations WHERE operation_id='op'`) != "running" {
		t.Fatal("executing cancel must stay intent-only")
	}
	reply, err := f.settle(goodReport())
	if err != nil || reply.Result == nil || reply.Result.State != domain.Succeeded {
		t.Fatalf("success must outrank cancel: %+v %v", reply, err)
	}
}

func TestCancelAcks(t *testing.T) {
	f := settleFixtureNew(t)
	ack, err := f.prep.RequestCancel(context.Background(), cancelAuth, domain.OperationKey{Namespace: "ns", OperationID: "missing"}, "c1")
	if err != nil || ack.Status != "notFound" {
		t.Fatalf("%+v %v", ack, err)
	}
	if _, err := f.settle(goodReport()); err != nil {
		t.Fatal(err)
	}
	ack, err = f.prep.RequestCancel(context.Background(), cancelAuth, f.owner.Key, "c2")
	if err != nil || ack.Status != "alreadyTerminal" {
		t.Fatalf("%+v %v", ack, err)
	}
	if _, err := f.prep.RequestCancel(context.Background(), f.auth, f.owner.Key, "c3"); !errors.Is(err, commands.ErrCancelForbidden) {
		t.Fatalf("execute-only principal may cancel: %v", err)
	}
}

func TestCancelEventEscapesCommandID(t *testing.T) {
	f := settleFixtureNew(t)
	commandID := domain.ID(`cancel/"\\id`)
	if _, err := f.prep.RequestCancel(context.Background(), cancelAuth, f.owner.Key, commandID); err != nil {
		t.Fatal(err)
	}
	raw := f.text(t, `SELECT payload_json FROM operation_events WHERE event_type='cancel_requested'`)
	var event struct {
		CommandID domain.ID `json:"commandId"`
	}
	if err := json.Unmarshal([]byte(raw), &event); err != nil || event.CommandID != commandID {
		t.Fatalf("cancel event must preserve an arbitrary command ID as valid JSON: %+v %v; raw=%s", event, err, raw)
	}
}

func TestRenewLeaseCompetesWithSweep(t *testing.T) {
	f := settleFixtureNew(t)
	f.clock.now.Store(100)
	if err := f.prep.RenewLease(context.Background(), f.auth, []domain.OwnerToken{f.owner}, 5000); err != nil {
		t.Fatal(err)
	}
	if f.n(t, `SELECT lease_until FROM operations WHERE operation_id='op'`) != 5100 ||
		f.n(t, `SELECT lease_until FROM attempts WHERE attempt_id='a1'`) != 5100 {
		t.Fatal("renew did not extend lease")
	}
	stale := f.owner
	stale.Generation = 2
	if err := f.prep.RenewLease(context.Background(), f.auth, []domain.OwnerToken{stale}, 5000); !errors.Is(err, commands.ErrRenewStale) {
		t.Fatalf("wrong generation renewed: %v", err)
	}
	// Renewal committed first: at 5000 the operation is not due, sweep no-ops.
	if err := f.prep.Sweep(context.Background(), manageAuth, 5000, 10); err != nil {
		t.Fatal(err)
	}
	if f.n(t, `SELECT generation FROM operations WHERE operation_id='op'`) != 1 {
		t.Fatal("sweep took over a renewed operation")
	}
	if _, err := f.settle(goodReport()); err != nil {
		t.Fatal(err)
	}
	if err := f.prep.RenewLease(context.Background(), f.auth, []domain.OwnerToken{f.owner}, 5000); !errors.Is(err, commands.ErrRenewStale) {
		t.Fatalf("terminal operation renewed: %v", err)
	}
}

func TestRenewLeaseCapsBeforeAdditionOverflow(t *testing.T) {
	f := settleFixtureNew(t)
	f.clock.now.Store(100)
	if err := f.prep.RenewLease(context.Background(), f.auth, []domain.OwnerToken{f.owner}, domain.Millis(1<<63-1)); err != nil {
		t.Fatal(err)
	}
	if got := f.n(t, `SELECT lease_until FROM operations WHERE operation_id='op'`); got != 10000 {
		t.Fatalf("overflowing duration must cap at deadline, got %d", got)
	}
	if got := f.n(t, `SELECT lease_until FROM attempts WHERE attempt_id='a1'`); got != 10000 {
		t.Fatalf("attempt advisory lease must match capped deadline, got %d", got)
	}
}

func TestSweepPreparedReleasesAndCompletesInterrupted(t *testing.T) {
	f, owner := freshRunningOperation(t)
	f.prepare(t, owner, "a2")
	f.clock.now.Store(9000) // past lease 8020, before deadline 10000
	if err := f.prep.Sweep(context.Background(), manageAuth, 9000, 10); err != nil {
		t.Fatal(err)
	}
	if f.n(t, `SELECT COUNT(*) FROM attempts WHERE attempt_id='a2' AND phase='terminal' AND external_outcome='not_executed'`) != 1 ||
		f.n(t, `SELECT COUNT(*) FROM reservations WHERE attempt_id='a2' AND phase='released'`) != 2 ||
		f.n(t, `SELECT reserved_units+active_units FROM resource_states WHERE resource_id='slot'`) != 0 {
		t.Fatal("sweep did not release the prepared reservation")
	}
	var result domain.OperationResult
	if err := json.Unmarshal([]byte(f.text(t, `SELECT result_json FROM operations WHERE operation_id='op2'`)), &result); err != nil || result.State != domain.Failed ||
		result.Code != domain.ResultCodeExecutionInterrupted || !stringsContainsCode(result.Reasons, domain.ReasonOwnerLeaseExpiredBeforeDispatch) {
		t.Fatalf("stable interrupted reason missing: %+v", result)
	}
	if f.n(t, `SELECT generation FROM operations WHERE operation_id='op2'`) != 2 {
		t.Fatal("generation not bumped")
	}
	// Old owner permanently loses standing (A04).
	if _, err := f.prep.TryPrepare(context.Background(), f.auth, owner, domain.ID("a3"), f.plan, nil, "digest", nil, 9000); !errors.Is(err, commands.ErrPreparationStale) {
		t.Fatalf("old owner prepared after sweep: %v", err)
	}
	// Same-ID claim replays the stable terminal result.
	input := domain.ExecuteInput{OperationID: "op2", PoolID: "pool", Request: domain.DomainRequest{Domain: "ai/text", CapabilityProfile: "profile", Payload: json.RawMessage(`{"prompt":"synthetic"}`)}, DeadlineAt: 10000, MaxAttempts: 3}
	fingerprint, _ := domain.SemanticFingerprint(domain.FingerprintInput{Domain: input.Request.Domain, PoolID: input.PoolID, CapabilityProfile: input.Request.CapabilityProfile, Payload: input.Request.Payload})
	claim, err := commands.Claim(context.Background(), f.st, f.auth, input, fingerprint, commands.ClaimPolicy{RuntimeID: "runtime", Now: 9000, LeaseMillis: 8000, MaxRuntimeAttempts: 3})
	if err != nil || claim.Kind != "replay" || claim.Result == nil || claim.Result.State != domain.Failed {
		t.Fatalf("replay after sweep: %+v %v", claim, err)
	}
}

func TestSweepExecutingGrantedRevokesAndParksAtDeadline(t *testing.T) {
	f, owner := freshRunningOperation(t)
	permit := f.authorize(t, owner, "a2") // granted, never consumed
	f.clock.now.Store(9000)
	if err := f.prep.Sweep(context.Background(), manageAuth, 9000, 10); err != nil {
		t.Fatal(err)
	}
	if f.text(t, `SELECT status FROM dispatch_permits WHERE attempt_id='a2'`) != "revoked" ||
		f.n(t, `SELECT COUNT(*) FROM attempts WHERE attempt_id='a2' AND phase='terminal' AND external_outcome='not_executed'`) != 1 ||
		f.n(t, `SELECT active_units FROM resource_states WHERE resource_id='slot'`) != 0 {
		t.Fatal("granted permit not revoked zero-send")
	}
	// Nothing pending, but a permit was issued: the resolver keeps the orphan
	// running under a new generation, parked until its deadline.
	if f.text(t, `SELECT state FROM operations WHERE operation_id='op2'`) != "running" ||
		f.n(t, `SELECT generation FROM operations WHERE operation_id='op2'`) != 2 ||
		f.n(t, `SELECT lease_until FROM operations WHERE operation_id='op2'`) != 10000 {
		t.Fatal("orphan not parked correctly")
	}
	if err := f.prep.ConsumeDispatchPermit(context.Background(), f.auth, owner, permit, "digest", 9000); !errors.Is(err, ledger.ErrPermitUnknown) {
		t.Fatalf("swept terminal permit must be removed from registry, got %v", err)
	}
	f.clock.now.Store(10000)
	if err := f.prep.Sweep(context.Background(), manageAuth, 10000, 10); err != nil {
		t.Fatal(err)
	}
	var result domain.OperationResult
	if err := json.Unmarshal([]byte(f.text(t, `SELECT result_json FROM operations WHERE operation_id='op2'`)), &result); err != nil || result.State != domain.Failed || result.Code != domain.ResultCodeDeadlineExceeded {
		t.Fatalf("deadline completion wrong: %+v %v", result, err)
	}
}

func TestSweepExecutingConsumedConservesUnknown(t *testing.T) {
	f := settleFixtureNew(t) // a1 executing, permit consumed
	f.clock.now.Store(9000)
	if err := f.prep.Sweep(context.Background(), manageAuth, 9000, 10); err != nil {
		t.Fatal(err)
	}
	if f.text(t, `SELECT external_outcome FROM attempts WHERE attempt_id='a1'`) != "unknown" ||
		f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 3 ||
		f.n(t, `SELECT uncertain_units FROM resource_states WHERE resource_id='slot'`) != 1 ||
		f.n(t, `SELECT COUNT(*) FROM encumbrances WHERE status='open'`) != 1 ||
		f.n(t, `SELECT permits_issued FROM operations WHERE operation_id='op'`) != 1 {
		t.Fatal("sweep conservation wrong")
	}
	var result domain.OperationResult
	if err := json.Unmarshal([]byte(f.text(t, `SELECT result_json FROM operations WHERE operation_id='op'`)), &result); err != nil || result.State != domain.Unknown {
		t.Fatalf("lease expiry must not become not-executed: %+v", result)
	}
	if f.n(t, `SELECT generation FROM operations WHERE operation_id='op'`) != 2 {
		t.Fatal("generation not bumped")
	}
	// A05: the late report of a swept attempt is retained, never re-applied.
	if _, err := f.settleAs(f.owner, goodReport()); !errors.Is(err, commands.ErrSettlementConflict) {
		t.Fatalf("late report accepted: %v", err)
	}
	if f.n(t, `SELECT COUNT(*) FROM reconciliations`) != 1 || f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 3 {
		t.Fatal("late report changed accounting")
	}
}

func TestSweepBatchLimitAndInterruptedCompletion(t *testing.T) {
	f := settleFixtureNew(t)
	f.claimOperation(t, "op2")
	f.claimOperation(t, "op3")
	f.clock.now.Store(9000)
	if err := f.prep.Sweep(context.Background(), manageAuth, 9000, 1); err != nil {
		t.Fatal(err)
	}
	if f.text(t, `SELECT state FROM operations WHERE operation_id='op'`) != "unknown" {
		t.Fatal("first due operation not swept")
	}
	if f.text(t, `SELECT state FROM operations WHERE operation_id='op2'`) != "running" || f.text(t, `SELECT state FROM operations WHERE operation_id='op3'`) != "running" {
		t.Fatal("batch limit exceeded")
	}
	if err := f.prep.Sweep(context.Background(), manageAuth, 9000, 1); err != nil {
		t.Fatal(err)
	}
	var result domain.OperationResult
	if err := json.Unmarshal([]byte(f.text(t, `SELECT result_json FROM operations WHERE operation_id='op2'`)), &result); err != nil ||
		result.State != domain.Failed || result.Code != domain.ResultCodeExecutionInterrupted {
		t.Fatalf("no-attempt sweep must complete interrupted: %+v", result)
	}
	if err := f.prep.Sweep(context.Background(), manageAuth, 9000, 100); err != nil {
		t.Fatal(err)
	}
	if f.text(t, `SELECT state FROM operations WHERE operation_id='op3'`) != "failed" {
		t.Fatal("op3 not completed")
	}
	// Idempotent: nothing is due anymore.
	if err := f.prep.Sweep(context.Background(), manageAuth, 11000, 100); err != nil {
		t.Fatal(err)
	}
	if f.n(t, `SELECT COUNT(*) FROM audit_log WHERE action='sweep'`) != 3 {
		t.Fatal("sweep audit trail wrong")
	}
}

func TestRecoveryFailsClosedOnDrift(t *testing.T) {
	f := settleFixtureNew(t)
	f.exec(t, `UPDATE resources SET metric_id='tampered' WHERE resource_id='counter'`)
	if _, err := f.abort(t, f.owner, "a1", ledger.ProofNone, ledger.DispatchPermit{}); err == nil {
		t.Fatal("identity drift accepted")
	}
	if f.text(t, `SELECT phase FROM attempts WHERE attempt_id='a1'`) != "executing" ||
		f.n(t, `SELECT committed_units+reserved_units FROM resource_states WHERE resource_id='counter'`) != 3 ||
		f.n(t, `SELECT COUNT(*) FROM command_receipts WHERE scope='abort/ns'`) != 0 {
		t.Fatal("failed abort left side effects")
	}
	g := settleFixtureNew(t)
	g.exec(t, `UPDATE reservations SET phase='released' WHERE attempt_id='a1' AND kind='counter'`)
	if _, err := g.abort(t, g.owner, "a1", ledger.ProofNone, ledger.DispatchPermit{}); err == nil {
		t.Fatal("reservation drift accepted")
	}
	if g.n(t, `SELECT active_units FROM resource_states WHERE resource_id='slot'`) != 1 ||
		g.text(t, `SELECT phase FROM attempts WHERE attempt_id='a1'`) != "executing" {
		t.Fatal("failed abort left side effects")
	}
}

func stringsContainsCode(reasons []domain.Reason, code string) bool {
	for _, r := range reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}
