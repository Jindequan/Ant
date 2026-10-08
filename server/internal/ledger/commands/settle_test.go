package commands_test

import (
	"ant/server/internal/domain"
	"ant/server/internal/domain/metering"
	"ant/server/internal/ledger"
	"ant/server/internal/ledger/commands"
	"ant/server/internal/store"
	"ant/server/internal/store/sqlite"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type settleClock struct{ now atomic.Int64 }

func (c *settleClock) Now() domain.Millis { return domain.Millis(c.now.Load()) }

// queuedSubmitContext observes the caller context read that SubmitWrite makes
// only after admitting a command into its bounded writer lane. It provides a
// deterministic queue barrier without timing sleeps.
type queuedSubmitContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *queuedSubmitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

type queueObservedStore struct {
	store.Store
	observed chan struct{}
}

func (s *queueObservedStore) SubmitWrite(ctx context.Context, cmd store.WriteCommand) error {
	return s.Store.SubmitWrite(&queuedSubmitContext{Context: ctx, observed: s.observed}, cmd)
}

type settleFixture struct {
	st      store.Store
	db      *sql.DB
	clock   *settleClock
	service *commands.SettlementService
	prep    *commands.PreparationService
	owner   domain.OwnerToken
	permit  ledger.DispatchPermit
	plan    domain.ExecutionPlan
	auth    domain.AuthContext
}

func (f *settleFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, e := f.db.Exec(q, args...); e != nil {
		t.Fatalf("%s: %v", q, e)
	}
}
func (f *settleFixture) n(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if e := f.db.QueryRow(q, args...).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

// settleResourceSpec is one synthetic resource row plus its reservation bound.
type settleResourceSpec struct {
	id       domain.ID
	kind     string // counter / occupancy
	metric   domain.ID
	unit     string
	role     string // limit / entitlement
	order    *int64 // provider entitlement order
	window   domain.WindowRule
	stateKey domain.ID
	capacity domain.Units
	bound    domain.Units
}

// settleFixtureBuild builds one synthetic local settlement fixture: config,
// account, binding and offer rows over the given resources, a claim and one
// dispatched attempt a1. The clock starts at 20ms; the writer reads it inside
// every command. mode "provider_entitlement_waterfall" builds one waterfall
// requirement with upper as requested quantity over all specs in order;
// every other mode builds one all_constraints requirement per spec.
func settleFixtureBuild(t *testing.T, lease, deadline domain.Millis, mode string, upper domain.Units, specs []settleResourceSpec) *settleFixture {
	t.Helper()
	c := &settleClock{}
	c.now.Store(20)
	path := filepath.Join(t.TempDir(), "settle.db")
	st, e := sqlite.Open(path, sqlite.Config{Clock: c, CommandBudget: 2 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if e = sqlite.Migrate(context.Background(), st); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String()+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &settleFixture{st: st, db: db, clock: c, auth: domain.AuthContext{PrincipalID: "synthetic", Namespace: "ns", Permissions: []domain.Permission{domain.PermExecute}}}
	f.exec(t, `INSERT INTO config_revisions VALUES('rev','{}',1)`)
	f.exec(t, `INSERT INTO active_config VALUES(1,'rev',1)`)
	f.exec(t, `INSERT INTO accounts VALUES('acct','synthetic',1,1,'{}')`)
	f.exec(t, `INSERT INTO bindings VALUES('bind','acct','synthetic','ai/text','https://fixture.invalid','fake-ref','cred',1,1,'{}')`)
	f.exec(t, `INSERT INTO resource_offers VALUES('offer','bind','ai/text','profile',1,1,'{}')`)
	f.exec(t, `INSERT INTO resource_pools VALUES('pool','ns','synthetic',1,1,'{}')`)
	f.exec(t, `INSERT INTO pool_members VALUES('pool','offer',0,1,1)`)
	f.plan = domain.ExecutionPlan{ConfigRevision: "rev", DomainRevision: "dr", AdapterRevision: "ar", ProviderTypeID: "synthetic", Endpoint: "https://fixture.invalid", CredentialRef: "fake-ref", PoolID: "pool", PoolVersion: 1, OfferID: "offer", AccountID: "acct", BindingID: "bind", CredentialRevision: "cred", WindowRuleRevision: "sr", ReportMappingRevision: "mapping"}
	if mode == "provider_entitlement_waterfall" {
		ids := make([]domain.ID, 0, len(specs))
		for _, s := range specs {
			ids = append(ids, s.id)
		}
		f.plan.Requirements = []domain.ResourceRequirement{{MetricID: specs[0].metric, Unit: specs[0].unit, UpperBound: upper, EvidenceID: domain.ID("ev-" + string(specs[0].id)), Mode: mode, ResourceIDs: ids}}
		f.plan.Bounds = []domain.BoundEvidence{{EvidenceID: domain.ID("ev-" + string(specs[0].id)), MetricID: specs[0].metric, Unit: specs[0].unit, UpperBound: upper, EnforcementSource: "synthetic enforced", MappingRevision: "mapping", EndpointRef: "fixture", SourceRef: "fixture", VerifiedAt: 1, ExpiresAt: 100000000}}
	}
	for _, s := range specs {
		d := domain.ResourceDefinition{ResourceID: s.id, OwnerKind: "account", OwnerID: "acct", MetricID: s.metric, Unit: s.unit, Kind: s.kind, CommercialRole: s.role, SemanticsRef: domain.ID("sem." + s.kind), SemanticsRevision: "sr", Window: s.window, ConfiguredCapacity: s.capacity, CapacityEvidenceRef: domain.ID("ev-" + string(s.id)), InitialStateEvidenceRef: "init", ProviderOrder: s.order}
		raw, _ := json.Marshal(d)
		f.exec(t, `INSERT INTO resources VALUES(?,'account','acct',?,?,0,?,?,'sr',CAST(? AS TEXT))`, s.id, s.metric, s.unit, s.kind, string(d.SemanticsRef), raw)
		f.exec(t, `INSERT INTO offer_resources VALUES('offer',?)`, s.id)
		f.exec(t, `INSERT INTO resource_states(resource_id,window_key,capacity_units) VALUES(?,?,?)`, s.id, s.stateKey, s.capacity)
		if mode != "provider_entitlement_waterfall" {
			f.plan.Requirements = append(f.plan.Requirements, domain.ResourceRequirement{MetricID: s.metric, Unit: s.unit, UpperBound: s.bound, EvidenceID: d.CapacityEvidenceRef, Mode: mode, ResourceIDs: []domain.ID{s.id}})
			f.plan.Bounds = append(f.plan.Bounds, domain.BoundEvidence{EvidenceID: d.CapacityEvidenceRef, MetricID: s.metric, Unit: s.unit, UpperBound: s.bound, EnforcementSource: "synthetic enforced", MappingRevision: "mapping", EndpointRef: "fixture", SourceRef: "fixture", VerifiedAt: 1, ExpiresAt: 100000000})
		}
	}
	input := domain.ExecuteInput{OperationID: "op", PoolID: "pool", Request: domain.DomainRequest{Domain: "ai/text", CapabilityProfile: "profile", Payload: json.RawMessage(`{"prompt":"synthetic"}`)}, DeadlineAt: deadline, MaxAttempts: 3}
	fingerprint, e := domain.SemanticFingerprint(domain.FingerprintInput{Domain: input.Request.Domain, PoolID: input.PoolID, CapabilityProfile: input.Request.CapabilityProfile, Payload: input.Request.Payload})
	if e != nil {
		t.Fatal(e)
	}
	claim, e := commands.Claim(context.Background(), st, f.auth, input, fingerprint, commands.ClaimPolicy{RuntimeID: "runtime", Now: 20, LeaseMillis: lease, MaxRuntimeAttempts: 3})
	if e != nil {
		t.Fatal(e)
	}
	f.owner = *claim.Owner
	f.prep = commands.NewPreparationService(st, "runtime", c)
	f.service = commands.NewSettlementService(st, "runtime", c, commands.SettlementPolicy{}, f.prep)
	f.dispatch(t, "a1")
	return f
}

func settleFixtureNew(t *testing.T) *settleFixture {
	t.Helper()
	lifetime := domain.WindowRule{Kind: "lifetime"}
	return settleFixtureBuild(t, 8000, 10000, "all_constraints", 0, []settleResourceSpec{
		{id: "counter", kind: "counter", metric: "tokens", unit: "token", role: "limit", window: lifetime, stateKey: "sr:lifetime", capacity: 20, bound: 3},
		{id: "slot", kind: "occupancy", metric: "concurrency", unit: "slot", role: "limit", window: lifetime, stateKey: "sr:lifetime", capacity: 20, bound: 1},
	})
}
func (f *settleFixture) dispatch(t *testing.T, id domain.ID) {
	t.Helper()
	p := f.plan
	p.PlanID = "plan-" + id
	pr, e := f.prep.TryPrepare(context.Background(), f.auth, f.owner, id, p, nil, "digest", nil, 20)
	if e != nil || pr.Kind != "prepared" {
		t.Fatalf("prepare %+v %v", pr, e)
	}
	permit, e := f.prep.AuthorizeDispatch(context.Background(), f.auth, f.owner, id, 20)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.prep.ConsumeDispatchPermit(context.Background(), f.auth, f.owner, permit, "digest", 20); e != nil {
		t.Fatal(e)
	}
	f.permit = permit
}
func goodReport() domain.ExecutionReport {
	u := domain.Units(2)
	return domain.ExecutionReport{ReportID: "report", AttemptID: "a1", Outcome: "known_success", RetrySafety: domain.Unsafe, ExternalEnded: true, EndEvidenceRefs: []domain.ID{"synthetic-end"}, Metrics: []domain.MetricReport{{MetricID: "tokens", Unit: "token", Actual: &u, EvidenceRefs: []domain.ID{"synthetic-usage"}}}}
}
func (f *settleFixture) settle(r domain.ExecutionReport) (ledger.SettleReply, error) {
	return f.service.Settle(context.Background(), f.auth, f.owner, r)
}
func (f *settleFixture) settleAs(o domain.OwnerToken, r domain.ExecutionReport) (ledger.SettleReply, error) {
	return f.service.Settle(context.Background(), f.auth, o, r)
}
func TestSettlementAtomicReplayConflict(t *testing.T) {
	f := settleFixtureNew(t)
	r := goodReport()
	r.Metrics[0].Conservative = true
	reply, e := f.settle(r)
	if e != nil || reply.Result == nil || reply.Result.State != domain.Succeeded {
		t.Fatalf("%+v %v", reply, e)
	}
	bases := make(map[domain.ID]domain.ConsumptionBasis, len(reply.Result.Resources))
	for _, resource := range reply.Result.Resources {
		bases[resource.MetricID] = resource.ConsumptionBasis
	}
	if bases["tokens"] != domain.ConsumptionConservativeBound || bases["concurrency"] != domain.ConsumptionNotApplicable {
		t.Fatalf("settlement consumption bases = %v; want conservative_bound for an explicit upper-bound projection and not_applicable for occupancy", bases)
	}
	if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 2 || f.n(t, `SELECT reserved_units FROM resource_states WHERE resource_id='counter'`) != 0 || f.n(t, `SELECT active_units+uncertain_units FROM resource_states WHERE resource_id='slot'`) != 0 {
		t.Fatal("bad resource totals")
	}
	if f.n(t, `SELECT COUNT(*) FROM resource_events WHERE command_scope='settle/ns'`) != 2 || f.n(t, `SELECT COUNT(*) FROM operation_events`) != 5 || f.n(t, `SELECT COUNT(*) FROM attempts WHERE phase='terminal' AND report_hash IS NOT NULL`) != 1 {
		t.Fatal("durable facts incomplete before reply")
	}
	if got := f.text(t, `SELECT group_concat(event_type, ',') FROM (SELECT event_type FROM operation_events WHERE namespace='ns' AND operation_id='op' ORDER BY sequence)`); got != "accepted,attempt_prepared,dispatch_authorized,attempt_settled,operation_terminal" {
		t.Fatalf("durable operation event order = %q", got)
	}
	if err := f.prep.ConsumeDispatchPermit(context.Background(), f.auth, f.owner, f.permit, "digest", f.clock.Now()); !errors.Is(err, ledger.ErrPermitUnknown) {
		t.Fatalf("settled attempt permit should be removed from runtime registry, got %v", err)
	}
	f.clock.now.Store(99999)
	again, e := f.settle(r)
	if e != nil || !reflect.DeepEqual(reply, again) {
		t.Fatalf("replay changed %+v %v", again, e)
	}
	r.RawDigest = "conflict"
	_, e = f.settle(r)
	if !errors.Is(e, commands.ErrSettlementConflict) {
		t.Fatal(e)
	}
	if f.n(t, `SELECT COUNT(*) FROM reconciliations`) != 1 || f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 2 {
		t.Fatal("conflict overwrote result")
	}
	_, _ = f.settle(r)
	if f.n(t, `SELECT COUNT(*) FROM reconciliations`) != 1 {
		t.Fatal("duplicate conflict evidence")
	}
}

func TestSettlementOutOfRangeMetricRetainedAndIsolatedAtomically(t *testing.T) {
	f := settleFixtureNew(t)
	r := goodReport()
	r.Metrics[0].Actual = nil
	r.Metrics[0].ActualOutOfRangeDecimal = "9223372036854775808"
	reply, err := f.settle(r)
	if err != nil || reply.Result == nil {
		t.Fatalf("settle out-of-range report: reply=%+v err=%v", reply, err)
	}
	hasAccountingReason := false
	for _, reason := range reply.Result.Reasons {
		if reason.Group == domain.ReasonGroupAccounting && reason.Code == domain.ReasonActualOutOfRange {
			hasAccountingReason = true
		}
	}
	if reply.Result.State != domain.Unknown || reply.Result.Code != domain.ResultCodeUnknownOutcome || !hasAccountingReason {
		t.Fatalf("out-of-range metric did not require accounting review: %+v", reply.Result)
	}
	if len(reply.Result.UnrepresentableMetrics) != 1 || reply.Result.UnrepresentableMetrics[0].ActualOutOfRangeDecimal != r.Metrics[0].ActualOutOfRangeDecimal {
		t.Fatalf("exact value missing from operation result: %+v", reply.Result.UnrepresentableMetrics)
	}
	if reply.Result.SuggestedAction != "review_usage" {
		t.Fatalf("operation result must direct the caller to review usage, got %q", reply.Result.SuggestedAction)
	}
	if reply.Result.RetrySafety != domain.Undetermined {
		t.Fatalf("unrepresentable usage must not report retry as safe, got %q", reply.Result.RetrySafety)
	}
	var counter *domain.ResourceOutcome
	for i := range reply.Result.Resources {
		if reply.Result.Resources[i].ResourceID == "counter" {
			counter = &reply.Result.Resources[i]
		}
	}
	if counter == nil || counter.Consumed != nil || counter.ConsumptionBasis != domain.ConsumptionUnrepresentable {
		t.Fatalf("resource outcome must not report zero as actual consumption: %+v", counter)
	}
	if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 0 ||
		f.n(t, `SELECT reserved_units FROM resource_states WHERE resource_id='counter'`) != 0 ||
		f.n(t, `SELECT COUNT(*) FROM reservations WHERE resource_id='counter' AND phase='settled'`) != 1 ||
		f.n(t, `SELECT COUNT(*) FROM holds WHERE scope_id='counter' AND reason='actual_out_of_range' AND status='open'`) != 1 ||
		f.n(t, `SELECT COUNT(*) FROM resource_events WHERE resource_id='counter' AND committed_delta<>0`) != 0 {
		t.Fatal("out-of-range settlement must release, hold, and never invent committed units")
	}
	var persisted []byte
	if err := f.db.QueryRow(`SELECT report_json FROM attempts WHERE attempt_id='a1'`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	var stored domain.ExecutionReport
	if err := json.Unmarshal(persisted, &stored); err != nil || len(stored.Metrics) != 1 || stored.Metrics[0].ActualOutOfRangeDecimal != r.Metrics[0].ActualOutOfRangeDecimal {
		t.Fatalf("exact report not recoverable from report_json: %+v err=%v raw=%s", stored, err, persisted)
	}
	var storedResult domain.OperationResult
	if err := f.db.QueryRow(`SELECT result_json FROM operations WHERE namespace='ns' AND operation_id='op'`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(persisted, &storedResult); err != nil || len(storedResult.UnrepresentableMetrics) != 1 || storedResult.UnrepresentableMetrics[0].ActualOutOfRangeDecimal != r.Metrics[0].ActualOutOfRangeDecimal {
		t.Fatalf("exact review fact not durable in operation result: %+v err=%v", storedResult, err)
	}

	// A new operation against the same resource sees the durable hold before it
	// can create another reservation or dispatch permit.
	input := domain.ExecuteInput{OperationID: "op2", PoolID: "pool", Request: domain.DomainRequest{Domain: "ai/text", CapabilityProfile: "profile", Payload: json.RawMessage(`{"prompt":"synthetic"}`)}, DeadlineAt: 10000, MaxAttempts: 3}
	fingerprint, err := domain.SemanticFingerprint(domain.FingerprintInput{Domain: input.Request.Domain, PoolID: input.PoolID, CapabilityProfile: input.Request.CapabilityProfile, Payload: input.Request.Payload})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := commands.Claim(context.Background(), f.st, f.auth, input, fingerprint, commands.ClaimPolicy{RuntimeID: "runtime", Now: f.clock.Now(), LeaseMillis: 2000000, MaxRuntimeAttempts: 3})
	if err != nil || claim.Owner == nil {
		t.Fatalf("claim follow-up operation: %+v err=%v", claim, err)
	}
	plan := f.plan
	plan.PlanID = "plan-op2"
	prepared, err := f.prep.TryPrepare(context.Background(), f.auth, *claim.Owner, "a2", plan, nil, "digest-op2", nil, f.clock.Now())
	if err != nil || prepared.Kind != "blocked" {
		t.Fatalf("resource hold must block follow-up prepare, got %+v err=%v", prepared, err)
	}

	beforeEvents := f.n(t, `SELECT COUNT(*) FROM resource_events WHERE command_scope='settle/ns'`)
	again, err := f.settle(r)
	if err != nil || !reflect.DeepEqual(reply, again) {
		t.Fatalf("same out-of-range report did not replay exactly: %+v err=%v", again, err)
	}
	if f.n(t, `SELECT COUNT(*) FROM holds WHERE scope_id='counter' AND reason='actual_out_of_range' AND status='open'`) != 1 || f.n(t, `SELECT COUNT(*) FROM resource_events WHERE command_scope='settle/ns'`) != beforeEvents {
		t.Fatal("same report replay duplicated accounting effects")
	}
	r.Metrics[0].ActualOutOfRangeDecimal = "9223372036854775809"
	if _, err := f.settle(r); !errors.Is(err, commands.ErrSettlementConflict) {
		t.Fatalf("conflicting report must be retained as evidence only, got %v", err)
	}
	if f.n(t, `SELECT COUNT(*) FROM reconciliations`) != 1 || f.n(t, `SELECT COUNT(*) FROM holds WHERE scope_id='counter' AND reason='actual_out_of_range' AND status='open'`) != 1 {
		t.Fatal("conflicting report changed accepted report or duplicated the hold")
	}
	if err := f.db.QueryRow(`SELECT report_json FROM attempts WHERE attempt_id='a1'`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(persisted, &stored); err != nil || stored.Metrics[0].ActualOutOfRangeDecimal != "9223372036854775808" {
		t.Fatalf("conflicting report overwrote the accepted exact value: %+v err=%v", stored, err)
	}
}

func TestCalculatedOutOfRangeUsageSurvivesSQLiteSettlement(t *testing.T) {
	f := settleFixtureNew(t)
	r := goodReport()
	d := metering.Dimension{MetricID: r.Metrics[0].MetricID, Unit: r.Metrics[0].Unit}
	calculator, err := metering.New(metering.Definition{ID: "oversized-usage", Revision: f.plan.ReportMappingRevision, Formulas: []metering.Formula{{Output: d, Rounding: metering.Exact, Terms: []metering.Term{{Input: metering.Dimension{MetricID: "source-usage", Unit: "unit"}, Numerator: 2, Denominator: 1}}}}})
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := calculator.Project(domain.ExecutionPlan{ReportMappingRevision: f.plan.ReportMappingRevision, Requirements: []domain.ResourceRequirement{{MetricID: d.MetricID, Unit: d.Unit}}}, []metering.Sample{{Dimension: metering.Dimension{MetricID: "source-usage", Unit: "unit"}, Amount: math.MaxInt64}}, f.clock.Now(), []domain.ID{"verified-source-usage"})
	if err != nil {
		t.Fatal(err)
	}
	r.Metrics[0] = metrics[0]
	reply, err := f.settle(r)
	if err != nil || reply.Result == nil || reply.Result.State != domain.Unknown || len(reply.Result.UnrepresentableMetrics) != 1 || reply.Result.UnrepresentableMetrics[0].ActualOutOfRangeDecimal != "18446744073709551614" {
		t.Fatalf("calculated usage not isolated: %+v %v", reply, err)
	}
	var raw []byte
	if err := f.db.QueryRow(`SELECT report_json FROM attempts WHERE attempt_id='a1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored domain.ExecutionReport
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Metrics[0].Actual != nil || stored.Metrics[0].ActualOutOfRangeDecimal != "18446744073709551614" {
		t.Fatalf("exact calculated usage lost in SQLite: %+v %v", stored, err)
	}
	if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 0 || f.n(t, `SELECT COUNT(*) FROM holds WHERE status='open'`) == 0 {
		t.Fatal("overflow wrapped or reopened capacity")
	}
	before := f.n(t, `SELECT COUNT(*) FROM resource_events`)
	again, err := f.settle(r)
	if err != nil || !reflect.DeepEqual(reply, again) || before != f.n(t, `SELECT COUNT(*) FROM resource_events`) {
		t.Fatal("calculated report replay changed accounting", err)
	}
}

func TestOneCalculatedUsageSettlesMinuteAndDayLimits(t *testing.T) {
	f := settleFixtureBuild(t, 8000, 10000, "all_constraints", 0, []settleResourceSpec{
		{id: "minute", kind: "counter", metric: "tokens", unit: "token", role: "limit", window: domain.WindowRule{Kind: "fixed", Period: "minute"}, stateKey: "sr:0:60000", capacity: 20, bound: 3},
		{id: "day", kind: "counter", metric: "tokens", unit: "token", role: "limit", window: domain.WindowRule{Kind: "fixed", Period: "day"}, stateKey: "sr:0:86400000", capacity: 200, bound: 3},
	})
	calculator, err := metering.New(metering.Definition{ID: "shared-counter", Revision: f.plan.ReportMappingRevision, Formulas: []metering.Formula{{Output: metering.Dimension{MetricID: "tokens", Unit: "token"}, Rounding: metering.Exact, Terms: []metering.Term{{Input: metering.Dimension{MetricID: "provider.tokens", Unit: "token"}, Numerator: 1, Denominator: 1}}}}})
	if err != nil {
		t.Fatal(err)
	}
	r := goodReport()
	r.Metrics, err = calculator.Project(f.plan, []metering.Sample{{Dimension: metering.Dimension{MetricID: "provider.tokens", Unit: "token"}, Amount: 2}}, f.clock.Now(), []domain.ID{"reported-once"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Metrics) != 1 {
		t.Fatal("same counter reported twice")
	}
	reply, err := f.settle(r)
	if err != nil || reply.Result == nil || reply.Result.State != domain.Succeeded {
		t.Fatalf("multi-window settlement: %+v %v", reply, err)
	}
	for _, id := range []string{"minute", "day"} {
		if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id=?`, id) != 2 || f.n(t, `SELECT reserved_units FROM resource_states WHERE resource_id=?`, id) != 0 {
			t.Fatalf("shared usage miscounted in %s window", id)
		}
	}
	before := f.n(t, `SELECT COUNT(*) FROM resource_events`)
	_, err = f.settle(r)
	if err != nil || f.n(t, `SELECT COUNT(*) FROM resource_events`) != before {
		t.Fatal("multi-window replay counted twice", err)
	}
}

func TestSettlementOutOfRangeFailureRollsBackAllFacts(t *testing.T) {
	f := settleFixtureNew(t)
	r := goodReport()
	r.Metrics[0].Actual = nil
	r.Metrics[0].ActualOutOfRangeDecimal = "9223372036854775808"
	f.exec(t, `CREATE TRIGGER reject_out_of_range_hold BEFORE INSERT ON holds WHEN NEW.reason='actual_out_of_range' BEGIN SELECT RAISE(ABORT,'injected hold failure'); END`)
	if _, err := f.settle(r); err == nil {
		t.Fatal("injected hold failure unexpectedly committed")
	}
	if f.n(t, `SELECT reserved_units FROM resource_states WHERE resource_id='counter'`) != 3 ||
		f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 0 ||
		f.n(t, `SELECT COUNT(*) FROM resource_events WHERE command_scope='settle/ns'`) != 0 ||
		f.n(t, `SELECT COUNT(*) FROM holds WHERE reason='actual_out_of_range'`) != 0 ||
		f.n(t, `SELECT COUNT(*) FROM attempts WHERE attempt_id='a1' AND phase='executing' AND report_hash IS NULL`) != 1 {
		t.Fatal("failed settlement partially persisted report, resource, hold, event, or reservation facts")
	}
}

func TestSettlementRejectsMalformedOutOfRangeMetric(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*domain.MetricReport)
	}{
		{"representable", func(m *domain.MetricReport) { m.Actual = nil; m.ActualOutOfRangeDecimal = "9223372036854775807" }},
		{"leading-zero", func(m *domain.MetricReport) { m.Actual = nil; m.ActualOutOfRangeDecimal = "09223372036854775808" }},
		{"negative", func(m *domain.MetricReport) { m.Actual = nil; m.ActualOutOfRangeDecimal = "-9223372036854775808" }},
		{"both-forms", func(m *domain.MetricReport) { m.ActualOutOfRangeDecimal = "9223372036854775808" }},
		{"no-evidence", func(m *domain.MetricReport) {
			m.Actual = nil
			m.ActualOutOfRangeDecimal = "9223372036854775808"
			m.EvidenceRefs = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := settleFixtureNew(t)
			r := goodReport()
			tc.mutate(&r.Metrics[0])
			if _, err := f.settle(r); !errors.Is(err, commands.ErrSettlementInvalid) {
				t.Fatalf("malformed report accepted: %v", err)
			}
			if f.n(t, `SELECT COUNT(*) FROM resource_events WHERE command_scope='settle/ns'`) != 0 || f.n(t, `SELECT COUNT(*) FROM holds WHERE reason='actual_out_of_range'`) != 0 || f.n(t, `SELECT COUNT(*) FROM attempts WHERE attempt_id='a1' AND report_hash IS NULL`) != 1 {
				t.Fatal("invalid report mutated settlement state")
			}
		})
	}
}
func TestSettlementUnknownAndSuccessConserveOccupancy(t *testing.T) {
	for _, outcome := range []string{"unknown", "known_success"} {
		t.Run(outcome, func(t *testing.T) {
			f := settleFixtureNew(t)
			r := goodReport()
			r.Outcome = outcome
			r.ExternalEnded = false
			r.EndEvidenceRefs = nil
			r.Metrics = nil
			reply, e := f.settle(r)
			if e != nil {
				t.Fatal(e)
			}
			want := domain.Unknown
			if outcome == "known_success" {
				want = domain.Succeeded
			}
			if reply.Result.State != want || f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 3 || f.n(t, `SELECT uncertain_units FROM resource_states WHERE resource_id='slot'`) != 1 || f.n(t, `SELECT active_units FROM resource_states WHERE resource_id='slot'`) != 0 || f.n(t, `SELECT COUNT(*) FROM encumbrances WHERE kind='occupancy' AND status='open'`) != 1 {
				t.Fatalf("bad conservation %+v", reply)
			}
		})
	}
}
func TestSettlementRollbackAtEveryDurableBoundary(t *testing.T) {
	for _, table := range []string{"resource_events", "encumbrances", "holds", "operation_events", "command_receipts"} {
		t.Run(table, func(t *testing.T) {
			f := settleFixtureNew(t)
			f.exec(t, fmt.Sprintf(`CREATE TRIGGER fail_settle BEFORE INSERT ON %s BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`, table))
			r := goodReport()
			if table == "encumbrances" {
				r.ExternalEnded = false
				r.EndEvidenceRefs = nil
				r.Outcome = "unknown"
			}
			if table == "holds" {
				v := domain.Units(4)
				r.Metrics[0].Actual = &v
			}
			reply, e := f.settle(r)
			if e == nil || reply.Result != nil {
				t.Fatal("failure returned completion")
			}
			if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 0 || f.n(t, `SELECT reserved_units FROM resource_states WHERE resource_id='counter'`) != 3 || f.n(t, `SELECT active_units FROM resource_states WHERE resource_id='slot'`) != 1 || f.n(t, `SELECT COUNT(*) FROM attempts WHERE phase='executing' AND report_hash IS NULL`) != 1 || f.n(t, `SELECT COUNT(*) FROM resource_events WHERE command_scope='settle/ns'`) != 0 || f.n(t, `SELECT COUNT(*) FROM operation_events`) != 3 {
				t.Fatal("partial settlement")
			}
			f.exec(t, `DROP TRIGGER fail_settle`)
			if _, e = f.settle(r); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestSettlementOverrunAcceptedAndHeld(t *testing.T) {
	f := settleFixtureNew(t)
	r := goodReport()
	v := domain.Units(100)
	r.Metrics[0].Actual = &v
	reply, e := f.settle(r)
	if e != nil || reply.Result.State != domain.Succeeded || f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 100 || f.n(t, `SELECT COUNT(*) FROM holds WHERE scope_id='counter' AND status='open'`) != 1 {
		t.Fatalf("overrun %+v %v", reply, e)
	}
}
func TestSettlementConcurrentReportsExactlyOnce(t *testing.T) {
	f := settleFixtureNew(t)
	var wg sync.WaitGroup
	var success atomic.Int64
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := f.settle(goodReport()); e == nil {
				success.Add(1)
			} else {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 40 || f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 2 || f.n(t, `SELECT COUNT(*) FROM resource_events WHERE command_scope='settle/ns'`) != 2 {
		t.Fatal("duplicate accounting")
	}
}
func TestSettlementContinueThenFinalAllFacts(t *testing.T) {
	f := settleFixtureNew(t)
	f.service = commands.NewSettlementService(f.st, "runtime", f.clock, commands.SettlementPolicy{AllowContinue: true, RetryAllowed: func(domain.ExecutionPlan, domain.ExecutionReport) bool { return true }}, f.prep)
	r := goodReport()
	r.Outcome = "known_failure"
	r.RetrySafety = domain.ProvedSafe
	reply, e := f.settle(r)
	if e != nil || !reply.Continue || reply.Result != nil {
		t.Fatalf("continue %+v %v", reply, e)
	}
	f.dispatch(t, "a2")
	r = goodReport()
	r.AttemptID = "a2"
	r.ReportID = "report2"
	reply, e = f.settle(r)
	if e != nil || reply.Result.State != domain.Succeeded || len(reply.Result.Attempts) != 2 || len(reply.Result.Resources) != 4 || f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 4 {
		t.Fatalf("final %+v %v", reply, e)
	}
}
func TestSettlementCancelDeadlineAndPolicyDoNotEraseFacts(t *testing.T) {
	for _, change := range []string{"cancel", "deadline", "disable"} {
		t.Run(change, func(t *testing.T) {
			f := settleFixtureNew(t)
			switch change {
			case "cancel":
				f.exec(t, `UPDATE operations SET cancel_requested_at=21`)
			case "deadline":
				f.exec(t, `UPDATE operations SET deadline_at=21`)
				f.clock.now.Store(30)
			case "disable":
				f.exec(t, `UPDATE bindings SET enabled=0`)
				f.exec(t, `UPDATE runtime_control SET restore_guard=1`)
			}
			reply, e := f.settle(goodReport())
			if e != nil || reply.Result.State != domain.Succeeded || f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 2 {
				t.Fatalf("facts lost %+v %v", reply, e)
			}
		})
	}
}
func TestSettlementInvalidReportsAndStaleEvidence(t *testing.T) {
	mutations := []func(*domain.ExecutionReport){func(r *domain.ExecutionReport) { r.Metrics[0].Unit = "wrong" }, func(r *domain.ExecutionReport) { v := domain.Units(-1); r.Metrics[0].Actual = &v }, func(r *domain.ExecutionReport) { r.Metrics[0].Conservative = true; r.Metrics[0].Actual = nil }, func(r *domain.ExecutionReport) { r.Metrics = append(r.Metrics, r.Metrics[0]) }, func(r *domain.ExecutionReport) { r.EndEvidenceRefs = nil }, func(r *domain.ExecutionReport) { r.Outcome = "invented" }, func(r *domain.ExecutionReport) { r.RetrySafety = "invented" }, func(r *domain.ExecutionReport) { at := domain.Millis(999); r.Metrics[0].ChargedAt = &at }}
	for i, m := range mutations {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f := settleFixtureNew(t)
			r := goodReport()
			m(&r)
			if _, e := f.settle(r); !errors.Is(e, commands.ErrSettlementInvalid) {
				t.Fatal(e)
			}
			if f.n(t, `SELECT reserved_units FROM resource_states WHERE resource_id='counter'`) != 3 {
				t.Fatal("invalid report mutated state")
			}
		})
	}
	f := settleFixtureNew(t)
	f.clock.now.Store(9000)
	if _, e := f.settle(goodReport()); !errors.Is(e, commands.ErrSettlementConflict) {
		t.Fatal(e)
	}
	if f.n(t, `SELECT COUNT(*) FROM reconciliations`) != 1 || f.n(t, `SELECT active_units FROM resource_states WHERE resource_id='slot'`) != 1 {
		t.Fatal("late evidence released slot")
	}
}

// TestSettlementContinuationGatedByHoldsAndPolicy closes LED-03 review item 1: the
// trusted retry allowlist and every blocking fact (account/binding/credential/
// resource/domain holds, open encumbrances, cancellation, deadline, attempt
// budget, output) refuse continuation while the dispatched facts still settle.
func TestSettlementContinuationGatedByHoldsAndPolicy(t *testing.T) {
	allow := commands.SettlementPolicy{AllowContinue: true, RetryAllowed: func(domain.ExecutionPlan, domain.ExecutionReport) bool { return true }}
	hold := func(scope, id, until string) string {
		return `INSERT INTO holds(hold_id,scope_kind,scope_id,reason,until_at,status,evidence_json,created_at) VALUES('h','` + scope + `','` + id + `','synthetic',` + until + `,'open','[]',20)`
	}
	cases := []struct {
		name   string
		sql    []string
		policy commands.SettlementPolicy
		report func(*domain.ExecutionReport)
	}{
		{"allowed", nil, allow, nil},
		{"policy-closed", nil, commands.SettlementPolicy{}, nil},
		{"retry-allowlist-nil", nil, commands.SettlementPolicy{AllowContinue: true}, nil},
		{"retry-allowlist-refused", nil, commands.SettlementPolicy{AllowContinue: true, RetryAllowed: func(domain.ExecutionPlan, domain.ExecutionReport) bool { return false }}, nil},
		{"not-proved-safe", nil, allow, func(r *domain.ExecutionReport) { r.RetrySafety = domain.Undetermined }},
		{"not-external-ended", nil, allow, func(r *domain.ExecutionReport) { r.ExternalEnded = false; r.EndEvidenceRefs = nil }},
		{"cancel-requested", []string{`UPDATE operations SET cancel_requested_at=21`}, allow, nil},
		{"deadline-passed", []string{`UPDATE operations SET deadline_at=20`}, allow, nil},
		{"attempt-budget-spent", []string{`UPDATE operations SET max_attempts=1`}, allow, nil},
		{"visible-output", []string{`UPDATE operations SET visible_output_at=21`}, allow, nil},
		{"stored-output", []string{`INSERT INTO output_chunks(attempt_id,sequence,data,sha256,created_at) VALUES('a1',0,x'00','h',20)`}, allow, nil},
		{"account-hold", []string{hold("account", "acct", "NULL")}, allow, nil},
		{"binding-hold", []string{hold("binding", "bind", "NULL")}, allow, nil},
		{"credential-hold", []string{hold("credential", "fake-ref", "NULL")}, allow, nil},
		{"domain-hold", []string{hold("domain", "ai/text", "NULL")}, allow, nil},
		{"resource-hold", []string{hold("resource", "counter", "NULL")}, allow, nil},
		{"open-encumbrance", []string{`INSERT INTO encumbrances(encumbrance_id,attempt_id,resource_id,window_key,kind,units,status,evidence_json,created_at) VALUES('e','a1','counter','sr:lifetime','window_attribution',1,'open','[]',20)`}, allow, nil},
		{"expired-hold-continues", []string{hold("account", "acct", "19")}, allow, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := settleFixtureNew(t)
			f.service = commands.NewSettlementService(f.st, "runtime", f.clock, tc.policy, f.prep)
			for _, q := range tc.sql {
				f.exec(t, q)
			}
			r := goodReport()
			r.Outcome = "known_failure"
			r.RetrySafety = domain.ProvedSafe
			if tc.report != nil {
				tc.report(&r)
			}
			reply, e := f.settle(r)
			if e != nil {
				t.Fatal(e)
			}
			if want := tc.name == "allowed" || tc.name == "expired-hold-continues"; reply.Continue != want {
				t.Fatalf("continue=%v, want %v", reply.Continue, want)
			}
			if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 2 || f.n(t, `SELECT COUNT(*) FROM attempts WHERE phase='terminal' AND report_hash IS NOT NULL`) != 1 {
				t.Fatal("dispatched facts not settled")
			}
		})
	}
	f := settleFixtureNew(t)
	f.service = commands.NewSettlementService(f.st, "runtime", f.clock, allow, f.prep)
	f.exec(t, hold("account", "acct", "NULL"))
	r := goodReport()
	r.Outcome = "known_failure"
	r.RetrySafety = domain.ProvedSafe
	reply, e := f.settle(r)
	if e != nil || reply.Result == nil || reply.Result.State != domain.Failed {
		t.Fatalf("held continuation must resolve failed, got %+v %v", reply, e)
	}
}

// TestSettlementWindowChargedAtAttribution closes LED-03 review item 2: fixed and
// rolling charged_at attribution, with out-of-window charges isolated.
func TestSettlementWindowChargedAtAttribution(t *testing.T) {
	fixed := domain.WindowRule{Kind: "fixed", Period: "minute"}
	rolling := domain.WindowRule{Kind: "rolling", DurationMillis: 30000}
	cases := []struct {
		name       string
		rule       domain.WindowRule
		stateKey   domain.ID
		clock      domain.Millis
		chargedAt  domain.Millis
		attributed bool
	}{
		{"fixed-inside", fixed, "sr:0:60000", 20, 10, true},
		{"fixed-outside-isolated", fixed, "sr:0:60000", 65000, 65000, false},
		{"rolling-inside", rolling, "sr:rolling", 20, 10, true},
		{"rolling-boundary-isolated", rolling, "sr:rolling", 40000, 10000, false},
		{"rolling-outside-isolated", rolling, "sr:rolling", 40000, 1000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := settleFixtureBuild(t, 2000000, 10000000, "all_constraints", 0, []settleResourceSpec{{id: "counter", kind: "counter", metric: "tokens", unit: "token", role: "limit", window: tc.rule, stateKey: tc.stateKey, capacity: 20, bound: 2}})
			f.clock.now.Store(int64(tc.clock))
			at := tc.chargedAt
			r := goodReport()
			r.Metrics[0].ChargedAt = &at
			reply, e := f.settle(r)
			if e != nil || reply.Result == nil || reply.Result.State != domain.Succeeded {
				t.Fatalf("%+v %v", reply, e)
			}
			want := 0
			if !tc.attributed {
				want = 1
			}
			if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 2 || f.n(t, `SELECT reserved_units FROM resource_states WHERE resource_id='counter'`) != 0 || f.n(t, `SELECT COUNT(*) FROM encumbrances WHERE kind='window_attribution' AND status='open'`) != want || f.n(t, `SELECT COUNT(*) FROM holds WHERE reason='window_attribution_unverified' AND status='open'`) != want {
				t.Fatalf("attribution isolation mismatch (want %d)", want)
			}
		})
	}
}

// TestSettlementWaterfallOrderAndOverrun closes LED-03 review item 2: multi-bucket
// attribution follows provider order (not stored plan order) and a real
// overrun on the last bucket is recorded in full and held.
func TestSettlementWaterfallOrderAndOverrun(t *testing.T) {
	lo, hi := int64(0), int64(1)
	build := func(t *testing.T) *settleFixture {
		return settleFixtureBuild(t, 8000, 10000, "provider_entitlement_waterfall", 4, []settleResourceSpec{
			{id: "wb", kind: "counter", metric: "tokens", unit: "token", role: "entitlement", order: &hi, window: domain.WindowRule{Kind: "lifetime"}, stateKey: "sr:lifetime", capacity: 3, bound: 4},
			{id: "wa", kind: "counter", metric: "tokens", unit: "token", role: "entitlement", order: &lo, window: domain.WindowRule{Kind: "lifetime"}, stateKey: "sr:lifetime", capacity: 3, bound: 4},
		})
	}
	t.Run("provider-order-not-plan-order", func(t *testing.T) {
		f := build(t)
		reply, e := f.settle(goodReport())
		if e != nil || reply.Result == nil || reply.Result.State != domain.Succeeded {
			t.Fatalf("%+v %v", reply, e)
		}
		if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='wa'`) != 2 || f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='wb'`) != 0 {
			t.Fatal("allocation ignored provider order")
		}
	})
	t.Run("overrun-held-on-last-bucket", func(t *testing.T) {
		f := build(t)
		v := domain.Units(5)
		r := goodReport()
		r.Metrics[0].Actual = &v
		reply, e := f.settle(r)
		if e != nil || reply.Result.State != domain.Succeeded {
			t.Fatalf("%+v %v", reply, e)
		}
		if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='wa'`) != 3 || f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='wb'`) != 2 || f.n(t, `SELECT COUNT(*) FROM holds WHERE scope_id='wb' AND status='open'`) != 1 {
			t.Fatal("overrun not recorded in full or not held")
		}
	})
}

// TestSettlementIdentityAndMappingIntegrity closes LED-03 review item 3: persisted
// columns, certified definition JSON, plan window/ownership/mapping revisions
// and the report mapping must agree before settlement facts are accepted.
func TestSettlementIdentityAndMappingIntegrity(t *testing.T) {
	cases := []struct {
		name  string
		sql   string
		exact bool // must surface as commands.ErrClaimIntegrity
	}{
		{"column-drift", `UPDATE resources SET metric_id='other' WHERE resource_id='counter'`, false},
		{"definition-drift", `UPDATE resources SET definition_json=json_set(definition_json,'$.Unit','other') WHERE resource_id='counter'`, false},
		{"kind-drift", `UPDATE resources SET kind='occupancy' WHERE resource_id='counter'`, false},
		{"owner-drift", `UPDATE resources SET owner_id='other' WHERE resource_id='counter'`, false},
		{"semantics-drift", `UPDATE resources SET semantics_revision='other' WHERE resource_id='counter'`, false},
		{"plan-mapping-drift", `UPDATE execution_plans SET plan_json=json_set(plan_json,'$.ReportMappingRevision','other') WHERE plan_id='plan-a1'`, true},
		{"plan-metric-drift", `UPDATE resources SET metric_id='other',definition_json=json_set(definition_json,'$.MetricID','other') WHERE resource_id='counter'`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := settleFixtureNew(t)
			f.exec(t, tc.sql)
			_, e := f.settle(goodReport())
			if e == nil || tc.exact && !errors.Is(e, commands.ErrClaimIntegrity) {
				t.Fatalf("settlement accepted inconsistent facts: %v", e)
			}
			if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 0 || f.n(t, `SELECT COUNT(*) FROM resource_events WHERE command_scope='settle/ns'`) != 0 || f.n(t, `SELECT COUNT(*) FROM attempts WHERE phase='executing' AND report_hash IS NULL`) != 1 {
				t.Fatal("integrity failure mutated state")
			}
		})
	}
}

// TestSettlementOwnerGenerationAndTerminalEvidence closes part of handoff
// item 4: foreign operation keys are forbidden, wrong owner/generation and
// terminal-without-receipt states keep facts and append reconciliation
// evidence exactly once.
func TestSettlementOwnerGenerationAndTerminalEvidence(t *testing.T) {
	f := settleFixtureNew(t)
	foreign := f.owner
	foreign.Key.OperationID = "op2"
	if _, e := f.settleAs(foreign, goodReport()); !errors.Is(e, commands.ErrPreparationForbidden) {
		t.Fatal(e)
	}
	mutations := []func(*domain.OwnerToken){func(o *domain.OwnerToken) { o.OwnerID = "other" }, func(o *domain.OwnerToken) { o.Generation = 2 }}
	for _, m := range mutations {
		o := f.owner
		m(&o)
		if _, e := f.settleAs(o, goodReport()); !errors.Is(e, commands.ErrSettlementConflict) {
			t.Fatal(e)
		}
	}
	if f.n(t, `SELECT COUNT(*) FROM reconciliations`) != 1 || f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 0 || f.n(t, `SELECT COUNT(*) FROM attempts WHERE phase='executing'`) != 1 {
		t.Fatal("late-owner evidence mishandled")
	}
	reply, e := f.settle(goodReport())
	if e != nil || reply.Result == nil {
		t.Fatalf("%+v %v", reply, e)
	}
	// Inject a terminal attempt whose settle receipt is gone (the receipt row
	// itself is FK-referenced by resource_events): the repeat must be treated
	// as late evidence, never as a re-applied settlement.
	f.exec(t, `UPDATE command_receipts SET kind='claim' WHERE scope='settle/ns'`)
	late := goodReport()
	late.RawDigest = "distinct"
	if _, e = f.settle(late); !errors.Is(e, commands.ErrSettlementConflict) {
		t.Fatal(e)
	}
	if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 2 || f.n(t, `SELECT COUNT(*) FROM reconciliations`) != 2 || f.n(t, `SELECT COUNT(*) FROM operation_events WHERE event_type='operation_terminal'`) != 1 {
		t.Fatal("terminal state was overwritten or re-applied")
	}
}

// TestSettlementConflictingReportsRace closes part of LED-03 review item 4: two
// conflicting reports racing for one attempt yield exactly one applied
// settlement; the losing side retains conflict evidence, never overwrites.
func TestSettlementConflictingReportsRace(t *testing.T) {
	f := settleFixtureNew(t)
	a := goodReport()
	b := goodReport()
	seven := domain.Units(7)
	b.Metrics[0].Actual = &seven
	b.ReportID = "b"
	var wg sync.WaitGroup
	var ok, conflict atomic.Int64
	for i := 0; i < 40; i++ {
		r := a
		if i%2 == 1 {
			r = b
		}
		wg.Add(1)
		go func(r domain.ExecutionReport) {
			defer wg.Done()
			if _, e := f.settle(r); e == nil {
				ok.Add(1)
			} else if errors.Is(e, commands.ErrSettlementConflict) {
				conflict.Add(1)
			} else {
				t.Error(e)
			}
		}(r)
	}
	wg.Wait()
	if ok.Load() != 20 || conflict.Load() != 20 {
		t.Fatalf("ok=%d conflict=%d", ok.Load(), conflict.Load())
	}
	committed := f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`)
	if committed != 2 && committed != 7 {
		t.Fatalf("both reports applied: %d", committed)
	}
	if f.n(t, `SELECT COUNT(*) FROM command_receipts WHERE kind='settle'`) != 1 || f.n(t, `SELECT COUNT(*) FROM resource_events WHERE command_scope='settle/ns'`) != 2 || f.n(t, `SELECT COUNT(*) FROM reconciliations`) != 1 {
		t.Fatal("conflicting race left inconsistent facts")
	}
}

// TestSettlementCASFailureInjection closes part of LED-03 review item 4: a CAS
// update on operations, attempts or reservations that applies zero rows fails
// the whole settlement with no partial durable state, and a retry succeeds.
func TestSettlementCASFailureInjection(t *testing.T) {
	for _, table := range []string{"operations", "attempts", "reservations"} {
		t.Run(table, func(t *testing.T) {
			f := settleFixtureNew(t)
			f.exec(t, fmt.Sprintf(`CREATE TRIGGER fail_cas BEFORE UPDATE ON %s BEGIN SELECT RAISE(IGNORE); END`, table))
			reply, e := f.settle(goodReport())
			if !errors.Is(e, commands.ErrPreparationStale) || reply.Result != nil {
				t.Fatalf("%+v %v", reply, e)
			}
			if f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 0 || f.n(t, `SELECT reserved_units FROM resource_states WHERE resource_id='counter'`) != 3 || f.n(t, `SELECT active_units FROM resource_states WHERE resource_id='slot'`) != 1 || f.n(t, `SELECT COUNT(*) FROM attempts WHERE phase='executing' AND report_hash IS NULL`) != 1 || f.n(t, `SELECT COUNT(*) FROM resource_events WHERE command_scope='settle/ns'`) != 0 || f.n(t, `SELECT COUNT(*) FROM operation_events`) != 3 || f.n(t, `SELECT COUNT(*) FROM command_receipts WHERE kind='settle'`) != 0 {
				t.Fatal("partial settlement after CAS failure")
			}
			f.exec(t, `DROP TRIGGER fail_cas`)
			if reply, e = f.settle(goodReport()); e != nil || reply.Result == nil {
				t.Fatalf("retry after CAS failure: %+v %v", reply, e)
			}
		})
	}
}

// TestSettlementQueuedLeaseExpiry closes part of LED-03 review item 4: the writer
// clock rules. A settlement queued behind another write command evaluates the
// lease at execution time; an operation whose lease expired while queued is a
// late report: evidence retained, no slot released, nothing charged.
func TestSettlementQueuedLeaseExpiry(t *testing.T) {
	f := settleFixtureNew(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	gate := make(chan error, 1)
	go func() {
		gate <- f.st.SubmitWrite(context.Background(), store.WriteCommand{ID: "gate", Run: func(store.WriterTx) error { close(entered); <-release; return nil }})
	}()
	<-entered
	queued := make(chan struct{})
	f.service = commands.NewSettlementService(&queueObservedStore{Store: f.st, observed: queued}, "runtime", f.clock, commands.SettlementPolicy{}, f.prep)
	done := make(chan error, 1)
	go func() {
		_, e := f.settle(goodReport())
		done <- e
	}()
	<-queued                // SubmitWrite has enqueued the settlement command behind the gate.
	f.clock.now.Store(9000) // operation lease (8020) expires while queued
	close(release)
	if e := <-gate; e != nil {
		t.Fatal(e)
	}
	if e := <-done; !errors.Is(e, commands.ErrSettlementConflict) {
		t.Fatal(e)
	}
	if f.n(t, `SELECT active_units FROM resource_states WHERE resource_id='slot'`) != 1 || f.n(t, `SELECT committed_units FROM resource_states WHERE resource_id='counter'`) != 0 || f.n(t, `SELECT COUNT(*) FROM reconciliations`) != 1 || f.n(t, `SELECT COUNT(*) FROM attempts WHERE phase='executing'`) != 1 {
		t.Fatal("expired lease settled or released slots")
	}
}

// TestSettlementOutputAvailabilityFromChunksOnly closes LED-03 review item 5:
// availability is projected from existing output_chunks rows only; settlement
// never writes output and exposes the stable operation-scoped OutputRef only
// when at least one durable chunk exists.
func TestSettlementOutputAvailabilityFromChunksOnly(t *testing.T) {
	t.Run("no-chunks-unavailable", func(t *testing.T) {
		f := settleFixtureNew(t)
		reply, e := f.settle(goodReport())
		if e != nil || reply.Result == nil || reply.Result.OutputAvailability != "none" || reply.Result.OutputRef != "" {
			t.Fatalf("%+v %v", reply, e)
		}
	})
	t.Run("chunks-stored", func(t *testing.T) {
		f := settleFixtureNew(t)
		f.exec(t, `INSERT INTO output_chunks(attempt_id,sequence,data,sha256,created_at) VALUES('a1',0,x'64757374','h',20)`)
		reply, e := f.settle(goodReport())
		if e != nil || reply.Result == nil || reply.Result.OutputAvailability != "available" || reply.Result.OutputRef != "operation/op" {
			t.Fatalf("%+v %v", reply, e)
		}
		if f.n(t, `SELECT COUNT(*) FROM output_chunks`) != 1 {
			t.Fatal("settlement wrote output rows")
		}
	})
}

func TestOutputAppendIsDurableIdempotentAndStopsFailover(t *testing.T) {
	f := settleFixtureNew(t)
	output := commands.NewOutputService(f.st, "runtime", f.clock)
	chunk := domain.OutputChunk{Sequence: 1, Data: []byte("first response")}
	if err := output.AppendOutput(context.Background(), f.auth, f.owner, "a1", chunk); err != nil {
		t.Fatal(err)
	}
	if err := output.AppendOutput(context.Background(), f.auth, f.owner, "a1", chunk); err != nil {
		t.Fatalf("identical append did not replay: %v", err)
	}
	if err := output.AppendOutput(context.Background(), f.auth, f.owner, "a1", domain.OutputChunk{Sequence: 1, Data: []byte("changed")}); !errors.Is(err, commands.ErrOutputConflict) {
		t.Fatalf("conflicting sequence error = %v", err)
	}
	if err := output.AppendOutput(context.Background(), f.auth, f.owner, "a1", domain.OutputChunk{Sequence: 3, Data: []byte("gap")}); !errors.Is(err, commands.ErrOutputConflict) {
		t.Fatalf("sequence gap error = %v", err)
	}
	if f.n(t, `SELECT COUNT(*) FROM output_chunks WHERE attempt_id='a1'`) != 1 || f.n(t, `SELECT COUNT(*) FROM operation_events WHERE event_type='output_stored'`) != 1 {
		t.Fatal("append did not commit exactly one output chunk and event")
	}
	if f.text(t, `SELECT CAST(payload_json AS TEXT) FROM operation_events WHERE event_type='output_stored'`) == string(chunk.Data) {
		t.Fatal("output body leaked into the event payload")
	}
	if f.n(t, `SELECT visible_output_at FROM operations WHERE operation_id='op'`) != int(f.clock.Now()) {
		t.Fatal("output was not made visible in the append transaction")
	}
	f.service = commands.NewSettlementService(f.st, "runtime", f.clock, commands.SettlementPolicy{AllowContinue: true, RetryAllowed: func(domain.ExecutionPlan, domain.ExecutionReport) bool { return true }}, f.prep)
	report := goodReport()
	report.Outcome, report.RetrySafety = "known_failure", domain.ProvedSafe
	reply, err := f.settle(report)
	if err != nil || reply.Continue || reply.Result == nil || reply.Result.OutputAvailability != "available" || reply.Result.OutputRef != "operation/op" {
		t.Fatalf("persisted output must terminate failover: reply=%+v err=%v", reply, err)
	}
}
