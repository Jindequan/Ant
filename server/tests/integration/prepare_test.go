package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/ledger/commands"
	"ant/server/internal/store"
)

type prepareFixture struct {
	st    store.Store
	path  string
	s     *commands.PreparationService
	clock *preparationTestClock
	owner domain.OwnerToken
	plan  domain.ExecutionPlan
}

type preparationTestClock struct {
	now    atomic.Int64
	hinted chan domain.Millis
}

func (c *preparationTestClock) Now() domain.Millis { return domain.Millis(c.now.Load()) }
func (c *preparationTestClock) SetNow(now domain.Millis) {
	c.now.Store(int64(now))
	if c.hinted != nil {
		select {
		case c.hinted <- now:
		default:
		}
	}
}

// Test callers advance the injected clock explicitly. Production commands read
// time from the clock inside the writer callback and never mutate it from args.
func (f prepareFixture) TryPrepare(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, id domain.ID, plan domain.ExecutionPlan, guards []domain.GuardEvidence, digest string, now domain.Millis) (ledger.PrepareReply, error) {
	f.clock.SetNow(now)
	return f.s.TryPrepare(ctx, auth, owner, id, plan, guards, digest, nil, now)
}

func (f prepareFixture) AuthorizeDispatch(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, id domain.ID, now domain.Millis) (ledger.DispatchPermit, error) {
	f.clock.SetNow(now)
	return f.s.AuthorizeDispatch(ctx, auth, owner, id, now)
}

func (f prepareFixture) ConsumeDispatchPermit(ctx context.Context, auth domain.AuthContext, owner domain.OwnerToken, permit ledger.DispatchPermit, digest string, now domain.Millis) error {
	f.clock.SetNow(now)
	return f.s.ConsumeDispatchPermit(ctx, auth, owner, permit, digest, now)
}

func preparationFixture(t *testing.T, capacity domain.Units) prepareFixture {
	t.Helper()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	defer db.Close()
	seedCatalog(t, db, "ns-1", "pool-1", 1, "of-1")
	execOK(t, db, `INSERT INTO pool_members(pool_id,offer_id,priority,enabled,version) VALUES('pool-1','of-1',10,1,1)`)
	plan := domain.ExecutionPlan{PlanID: "p1", ConfigRevision: "rev-seed", DomainRevision: "dr1", AdapterRevision: "ar1", ProviderTypeID: "provider-alpha", Endpoint: "https://fixture.invalid/v1", CredentialRef: "secret-ref", PoolID: "pool-1", PoolVersion: 1, OfferID: "of-1", MemberPriority: 10, AccountID: "acct-1", BindingID: "bind-1", CredentialRevision: "rev-1", WindowRuleRevision: "sr1", ReportMappingRevision: "mr1"}
	for _, tc := range []struct{ id, kind, metric, unit string }{{"r1", "counter", "tokens", "token"}, {"r2", "occupancy", "concurrency", "slot"}} {
		d := domain.ResourceDefinition{ResourceID: domain.ID(tc.id), OwnerKind: "account", OwnerID: "acct-1", MetricID: domain.ID(tc.metric), Unit: tc.unit, Kind: tc.kind, CommercialRole: "limit", SemanticsRef: "sem." + domain.ID(tc.kind), SemanticsRevision: "sr1", Window: domain.WindowRule{Kind: "lifetime"}, CapacityEvidenceRef: domain.ID("ev-" + tc.id), InitialStateEvidenceRef: domain.ID("init-" + tc.id), ConfiguredCapacity: capacity}
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		execOK(t, db, `INSERT INTO resources(resource_id,owner_kind,owner_id,metric_id,unit,quantum_exp,kind,semantics_ref,semantics_revision,definition_json) VALUES(?,'account','acct-1',?,?,0,?,?, 'sr1',CAST(? AS TEXT))`, tc.id, tc.metric, tc.unit, tc.kind, string(d.SemanticsRef), raw)
		execOK(t, db, `INSERT INTO offer_resources(offer_id,resource_id) VALUES('of-1',?)`, tc.id)
		execOK(t, db, `INSERT INTO resource_states(resource_id,window_key,capacity_units) VALUES(?,'sr1:lifetime',?)`, tc.id, int64(capacity))
		plan.Requirements = append(plan.Requirements, domain.ResourceRequirement{MetricID: d.MetricID, Unit: d.Unit, UpperBound: 1, EvidenceID: d.CapacityEvidenceRef, Mode: "all_constraints", ResourceIDs: []domain.ID{d.ResourceID}})
		plan.Bounds = append(plan.Bounds, domain.BoundEvidence{EvidenceID: d.CapacityEvidenceRef, MetricID: d.MetricID, Unit: d.Unit, UpperBound: 1, EnforcementSource: "localtest enforced", MappingRevision: "mr1", EndpointRef: "localtest-endpoint", SourceRef: "fixture", VerifiedAt: 1, ExpiresAt: 9000})
	}
	activateSeedConfig(t, st)
	input := claimInput(`{"prompt":"local preparation"}`)
	input.DeadlineAt = 10000
	input.MaxAttempts = 2
	reply, err := commands.Claim(context.Background(), st, claimAuth(), input, claimFingerprint(t, input), commands.ClaimPolicy{RuntimeID: "rt1", Now: 10, LeaseMillis: 9000, MaxRuntimeAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	clock := &preparationTestClock{hinted: make(chan domain.Millis, 8)}
	clock.SetNow(10)
	return prepareFixture{st: st, path: path, s: commands.NewPreparationService(st, "rt1", clock), clock: clock, owner: *reply.Owner, plan: plan}
}
func (f prepareFixture) prepare(t *testing.T, id domain.ID) (ledger.PrepareReply, error) {
	t.Helper()
	p := f.plan
	p.PlanID = "plan-" + id
	return f.TryPrepare(context.Background(), claimAuth(), f.owner, id, p, nil, "digest", 20)
}

func assertPreparationBlocked(t *testing.T, reply ledger.PrepareReply, err error) {
	t.Helper()
	if err != nil || reply.Kind != "blocked" || len(reply.Reasons) == 0 {
		t.Fatalf("want durable blocked result, got reply=%+v err=%v", reply, err)
	}
}
func TestPreparationQuotePrepareAuthorizeConsume(t *testing.T) {
	f := preparationFixture(t, 2)
	ctx := context.Background()
	quote, err := f.s.Quote(ctx, claimAuth(), f.plan, 15)
	if err != nil || len(quote) != 2 || quote[0].Reserved != 0 {
		t.Fatalf("quote %+v %v", quote, err)
	}
	reply, err := f.prepare(t, "a1")
	if err != nil || reply.Kind != "prepared" || len(reply.Attempt.Allocations) != 2 {
		t.Fatalf("prepare %+v %v", reply, err)
	}
	again, err := f.prepare(t, "a1")
	if err != nil || len(again.Attempt.Allocations) != 2 {
		t.Fatalf("replay %+v %v", again, err)
	}
	db := openRawDB(t, f.path)
	defer db.Close()
	if got := queryInt(t, db, `SELECT SUM(reserved_units) FROM resource_states`); got != 2 {
		t.Fatalf("reserved=%d", got)
	}
	permit, err := f.AuthorizeDispatch(ctx, claimAuth(), f.owner, "a1", 30)
	if err != nil || permit.ID() == "" {
		t.Fatalf("authorize %v", err)
	}
	againPermit, err := f.AuthorizeDispatch(ctx, claimAuth(), f.owner, "a1", 31)
	if err != nil || againPermit.ID() != permit.ID() {
		t.Fatalf("authorize replay %v", err)
	}
	if got := queryInt(t, db, `SELECT permits_issued FROM operations`); got != 1 {
		t.Fatalf("issued=%d", got)
	}
	if got := queryInt(t, db, `SELECT active_units FROM resource_states WHERE resource_id='r2'`); got != 1 {
		t.Fatalf("active=%d", got)
	}
	if err = f.ConsumeDispatchPermit(ctx, claimAuth(), f.owner, permit, "digest", 40); err != nil {
		t.Fatal(err)
	}
	if err = f.ConsumeDispatchPermit(ctx, claimAuth(), f.owner, permit, "digest", 41); err == nil {
		t.Fatal("reused permit accepted")
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM dispatch_permits WHERE status='consumed'`); got != 1 {
		t.Fatalf("consumed=%d", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM resource_events`); got != 3 {
		t.Fatalf("events=%d", got)
	}
}

func TestPreparationQuoteDoesNotRequireOpeningBalanceForOccupancy(t *testing.T) {
	f := preparationFixture(t, 2)
	db := openRawDB(t, f.path)
	var raw string
	if err := db.QueryRow(`SELECT definition_json FROM resources WHERE resource_id='r2'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var definition domain.ResourceDefinition
	if err := json.Unmarshal([]byte(raw), &definition); err != nil {
		t.Fatal(err)
	}
	definition.InitialStateEvidenceRef = ""
	updated, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE resources SET definition_json=CAST(? AS TEXT) WHERE resource_id='r2'`, updated); err != nil {
		t.Fatal(err)
	}
	db.Close()

	quote, err := f.s.Quote(context.Background(), claimAuth(), f.plan, 15)
	if err != nil || len(quote) != 2 {
		t.Fatalf("occupancy resource without opening balance quote=%+v err=%v", quote, err)
	}
	prepared, err := f.prepare(t, "without-opening-balance")
	if err != nil || prepared.Kind != "prepared" {
		t.Fatalf("occupancy resource without opening balance prepare=%+v err=%v", prepared, err)
	}
}

func seedEntitlementWaterfall(t *testing.T, f *prepareFixture, capacities []int64) {
	t.Helper()
	db := openRawDB(t, f.path)
	for index, item := range []struct {
		id    string
		order int64
	}{{id: "r3", order: 10}, {id: "r4", order: 20}} {
		capacity := capacities[index]
		order := item.order
		definition := domain.ResourceDefinition{
			ResourceID: domain.ID(item.id), OwnerKind: "account", OwnerID: "acct-1",
			MetricID: "credits", Unit: "cent", Kind: "counter", CommercialRole: "entitlement",
			SemanticsRef: "sem.entitlement", SemanticsRevision: "sr1", Window: domain.WindowRule{Kind: "lifetime"},
			CapacityEvidenceRef: domain.ID("ev-" + item.id), InitialStateEvidenceRef: domain.ID("init-" + item.id),
			ConfiguredCapacity: domain.Units(capacity), ProviderOrder: &order, ProviderEntitlementGroupID: "credits-group",
		}
		raw, err := json.Marshal(definition)
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO resources(resource_id,owner_kind,owner_id,metric_id,unit,quantum_exp,kind,semantics_ref,semantics_revision,definition_json) VALUES(?,'account','acct-1','credits','cent',0,'counter','sem.entitlement','sr1',CAST(? AS TEXT))`, item.id, raw); err != nil {
			db.Close()
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO offer_resources(offer_id,resource_id) VALUES('of-1',?)`, item.id); err != nil {
			db.Close()
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO resource_states(resource_id,window_key,capacity_units) VALUES(?,'sr1:lifetime',?)`, item.id, capacity); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	var strategy domain.ExecuteInput
	var strategyJSON string
	if err := db.QueryRow(`SELECT strategy_json FROM operations WHERE operation_id='op-1'`).Scan(&strategyJSON); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strategyJSON), &strategy); err != nil {
		db.Close()
		t.Fatal(err)
	}
	strategy.AllowMetered = true
	strategyJSONBytes, err := json.Marshal(strategy)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE operations SET strategy_json=CAST(? AS TEXT) WHERE operation_id='op-1'`, strategyJSONBytes); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	f.plan.Requirements = append(f.plan.Requirements, domain.ResourceRequirement{
		MetricID: "credits", Unit: "cent", UpperBound: 7, EvidenceID: "ev-group",
		Mode: "provider_entitlement_waterfall", ResourceIDs: []domain.ID{"r3", "r4"},
	})
	f.plan.Bounds = append(f.plan.Bounds, domain.BoundEvidence{
		EvidenceID: "ev-group", MetricID: "credits", Unit: "cent", UpperBound: 7,
		EnforcementSource: "provider grant buckets", MappingRevision: "mr1", EndpointRef: "bind-1",
		SourceRef: "fixture:credits-group", VerifiedAt: 1, ExpiresAt: 9_000,
	})
}

func TestPreparationAllocatesEntitlementWaterfallInConfiguredOrder(t *testing.T) {
	t.Run("provider order", func(t *testing.T) {
		f := preparationFixture(t, 10)
		seedEntitlementWaterfall(t, &f, []int64{4, 5})
		reply, err := f.prepare(t, "waterfall")
		if err != nil || reply.Kind != "prepared" || reply.Attempt == nil {
			t.Fatalf("waterfall prepare=%+v err=%v", reply, err)
		}
		db := openRawDB(t, f.path)
		defer db.Close()
		var first, second int64
		if err := db.QueryRow(`SELECT units FROM reservations WHERE attempt_id='waterfall' AND resource_id='r3'`).Scan(&first); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT units FROM reservations WHERE attempt_id='waterfall' AND resource_id='r4'`).Scan(&second); err != nil {
			t.Fatal(err)
		}
		if first != 4 || second != 3 {
			t.Fatalf("waterfall allocations r3=%d r4=%d, want provider order 4 then 3", first, second)
		}
	})

	t.Run("insufficient balance leaves no reservations", func(t *testing.T) {
		f := preparationFixture(t, 10)
		seedEntitlementWaterfall(t, &f, []int64{4, 5})
		f.plan.Requirements[len(f.plan.Requirements)-1].UpperBound = 10
		f.plan.Bounds[len(f.plan.Bounds)-1].UpperBound = 10
		reply, err := f.prepare(t, "waterfall-insufficient")
		assertPreparationBlocked(t, reply, err)
		db := openRawDB(t, f.path)
		defer db.Close()
		if got := queryInt(t, db, `SELECT COUNT(*) FROM reservations WHERE attempt_id='waterfall-insufficient'`); got != 0 {
			t.Fatalf("insufficient waterfall wrote %d partial reservations", got)
		}
		if got := queryInt(t, db, `SELECT SUM(reserved_units) FROM resource_states WHERE resource_id IN ('r3','r4')`); got != 0 {
			t.Fatalf("insufficient waterfall left %d units reserved in entitlement state", got)
		}
	})
}

func TestPreparationRejectsAuthorizationAfterPoolOrderChanges(t *testing.T) {
	f := preparationFixture(t, 2)
	ctx := context.Background()
	if _, err := f.prepare(t, "a1"); err != nil {
		t.Fatal(err)
	}

	// Apply a real full-group reorder, including the pool version, active config
	// revision, control version, receipt, and audit record.
	var reordered reorderResult
	err := f.st.SubmitWrite(ctx, reorderCommand("pool-1", 1, "rev-2", []store.PoolMemberRecord{member("pool-1", "of-1", 11)}, &reordered))
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if reordered.controlVersion != 2 {
		t.Fatalf("control version=%d, want 2", reordered.controlVersion)
	}
	if _, err := f.AuthorizeDispatch(ctx, claimAuth(), f.owner, "a1", 30); !errors.Is(err, commands.ErrPreparationStale) {
		t.Fatalf("authorize after pool reorder=%v, want stale", err)
	}

	db := openRawDB(t, f.path)
	defer db.Close()
	if got := queryInt(t, db, `SELECT COUNT(*) FROM dispatch_permits WHERE attempt_id='a1'`); got != 0 {
		t.Fatalf("permits after stale authorization=%d, want 0", got)
	}
	if got := queryInt(t, db, `SELECT reserved_units FROM resource_states WHERE resource_id='r1'`); got != 1 {
		t.Fatalf("reservation after stale authorization=%d, want 1 for safe recovery", got)
	}
	outcome, err := f.s.AbortBeforeDispatch(ctx, claimAuth(), commands.AbortRequest{Owner: f.owner, Attempt: "a1", Proof: ledger.ProofNone})
	if err != nil || outcome.Kind != ledger.AbortClosePrepared || !outcome.ReplanAllowed {
		t.Fatalf("abort prepared attempt after reorder: %+v err=%v", outcome, err)
	}
	if got := queryInt(t, db, `SELECT reserved_units+active_units+uncertain_units FROM resource_states WHERE resource_id='r1'`); got != 0 {
		t.Fatalf("resource state after safe abort=%d, want 0", got)
	}
	f.plan.ConfigRevision = "rev-2"
	f.plan.PoolVersion = 2
	f.plan.MemberPriority = 11
	if reply, err := f.prepare(t, "a2"); err != nil || reply.Kind != "prepared" {
		t.Fatalf("prepare from new pool order: reply=%+v err=%v", reply, err)
	}
}

func TestPreparationUsesWriterTimeAfterQueueDelay(t *testing.T) {
	f := preparationFixture(t, 2)
	started, release := make(chan struct{}), make(chan struct{})
	blockErr := make(chan error, 1)
	go func() {
		blockErr <- f.st.SubmitWrite(context.Background(), store.WriteCommand{ID: "test-writer-delay", Run: func(store.WriterTx) error {
			close(started)
			<-release
			return nil
		}})
	}()
	<-started
	for {
		select {
		case <-f.clock.hinted:
		default:
			goto drained
		}
	}
drained:
	result := make(chan error, 1)
	go func() {
		p := f.plan
		p.PlanID = "plan-queued-expired"
		_, err := f.TryPrepare(context.Background(), claimAuth(), f.owner, "queued-expired", p, nil, "digest", 20)
		result <- err
	}()
	select {
	case <-f.clock.hinted: // synchronous hint immediately before SubmitWrite queues.
	case <-time.After(time.Second):
		t.Fatal("prepare did not reach the queue")
	}
	f.clock.SetNow(10001) // deadline/lease pass while the command waits behind the blocker.
	close(release)
	if err := <-blockErr; err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, commands.ErrPreparationStale) {
		t.Fatalf("queued expired prepare = %v, want stale", err)
	}
}

func TestPreparationCanceledAuthorizeCanRetryWithoutPermitRace(t *testing.T) {
	f := preparationFixture(t, 2)
	if _, err := f.prepare(t, "a1"); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	blockErr := make(chan error, 1)
	go func() {
		blockErr <- f.st.SubmitWrite(context.Background(), store.WriteCommand{ID: "test-authorize-cancel-block", Run: func(store.WriterTx) error {
			close(started)
			<-release
			return nil
		}})
	}()
	<-started
	for {
		select {
		case <-f.clock.hinted:
		default:
			goto drained
		}
	}
drained:
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := f.AuthorizeDispatch(ctx, claimAuth(), f.owner, "a1", 30)
		first <- err
	}()
	select {
	case <-f.clock.hinted:
	case <-time.After(time.Second):
		t.Fatal("first authorize did not reach SubmitWrite")
	}
	time.Sleep(10 * time.Millisecond) // allow SubmitWrite to enqueue behind the held writer.
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled authorize=%v", err)
	}
	second := make(chan struct {
		permit ledger.DispatchPermit
		err    error
	}, 1)
	go func() {
		p, err := f.AuthorizeDispatch(context.Background(), claimAuth(), f.owner, "a1", 31)
		second <- struct {
			permit ledger.DispatchPermit
			err    error
		}{p, err}
	}()
	select {
	case <-f.clock.hinted:
	case <-time.After(time.Second):
		t.Fatal("retry authorize did not reach SubmitWrite")
	}
	time.Sleep(10 * time.Millisecond)
	close(release)
	if err := <-blockErr; err != nil {
		t.Fatal(err)
	}
	got := <-second
	if got.err != nil || got.permit.ID() == "" {
		t.Fatalf("retry authorize=%v permit=%q", got.err, got.permit.ID())
	}
	db := openRawDB(t, f.path)
	defer db.Close()
	if n := queryInt(t, db, `SELECT COUNT(*) FROM dispatch_permits`); n != 1 {
		t.Fatalf("permit count=%d", n)
	}
}

func TestPreparationAuthorizeRejectsFreshObservationAndExpiredResource(t *testing.T) {
	for _, tc := range []struct {
		name string
		sql  string
	}{
		{"provider_observation_zero", `INSERT INTO observations(observation_id,resource_id,window_key,remaining_units,committed_at_start,adjustment_version_at_start,observed_at,expires_at,valid,source_ref,evidence_json) VALUES('obs-zero','r1','sr1:lifetime',0,0,0,25,100,1,'fixture','{}')`},
		{"resource_expired", `UPDATE resources SET definition_json=json_set(definition_json,'$.ExpiresAt',25) WHERE resource_id='r1'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := preparationFixture(t, 2)
			if _, err := f.prepare(t, "a1"); err != nil {
				t.Fatal(err)
			}
			db := openRawDB(t, f.path)
			defer db.Close()
			execOK(t, db, tc.sql)
			if _, err := f.AuthorizeDispatch(context.Background(), claimAuth(), f.owner, "a1", 30); !errors.Is(err, commands.ErrPreparationBlocked) {
				t.Fatalf("authorize after changed cap = %v, want blocked", err)
			}
			if n := queryInt(t, db, `SELECT COUNT(*) FROM dispatch_permits`); n != 0 {
				t.Fatalf("permit minted despite changed cap=%d", n)
			}
			if n := queryInt(t, db, `SELECT reserved_units FROM resource_states WHERE resource_id='r2'`); n != 1 {
				t.Fatalf("partial reservation transition=%d", n)
			}
		})
	}
}

func TestPreparationResourceReceiptFailuresRollback(t *testing.T) {
	for _, target := range []string{"resource_events", "command_receipts"} {
		t.Run(target, func(t *testing.T) {
			f := preparationFixture(t, 2)
			db := openRawDB(t, f.path)
			defer db.Close()
			if target == "resource_events" {
				execOK(t, db, `CREATE TRIGGER fail_prepare BEFORE INSERT ON resource_events WHEN NEW.resource_id='r2' BEGIN SELECT RAISE(ABORT,'resource failure'); END`)
			} else {
				execOK(t, db, `CREATE TRIGGER fail_prepare BEFORE INSERT ON command_receipts WHEN NEW.kind='prepare' BEGIN SELECT RAISE(ABORT,'receipt failure'); END`)
			}
			if _, err := f.prepare(t, "a1"); err == nil {
				t.Fatal("injected failure accepted")
			}
			for _, table := range []string{"attempts", "execution_plans", "reservations", "resource_events", "decision_records"} {
				if n := queryInt(t, db, `SELECT COUNT(*) FROM `+table); n != 0 {
					t.Fatalf("%s partial write=%d", table, n)
				}
			}
			if n := queryInt(t, db, `SELECT SUM(reserved_units) FROM resource_states`); n != 0 {
				t.Fatalf("partial reserved=%d", n)
			}
		})
	}
}
func TestPreparationAuthorizationFailureRetryDoesNotLeakPermit(t *testing.T) {
	f := preparationFixture(t, 2)
	if _, err := f.prepare(t, "a1"); err != nil {
		t.Fatal(err)
	}
	db := openRawDB(t, f.path)
	defer db.Close()
	execOK(t, db, `CREATE TRIGGER fail_authorize BEFORE INSERT ON command_receipts WHEN NEW.kind='authorize' BEGIN SELECT RAISE(ABORT,'authorization receipt failure'); END`)
	p, err := f.AuthorizeDispatch(context.Background(), claimAuth(), f.owner, "a1", 30)
	if err == nil || p.ID() != "" {
		t.Fatal("failed authorization returned permit")
	}
	if n := queryInt(t, db, `SELECT permits_issued FROM operations`); n != 0 {
		t.Fatalf("budget partially updated=%d", n)
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM dispatch_permits`); n != 0 {
		t.Fatalf("partial permit=%d", n)
	}
	if n := queryInt(t, db, `SELECT active_units FROM resource_states WHERE resource_id='r2'`); n != 0 {
		t.Fatalf("partial activation=%d", n)
	}
	execOK(t, db, `DROP TRIGGER fail_authorize`)
	p, err = f.AuthorizeDispatch(context.Background(), claimAuth(), f.owner, "a1", 31)
	if err != nil || p.ID() == "" {
		t.Fatalf("retry stuck %v", err)
	}
	if err = f.ConsumeDispatchPermit(context.Background(), claimAuth(), f.owner, p, "digest", 40); err != nil {
		t.Fatal(err)
	}
}
func TestPreparationCurrentGuardsAndVersionFailures(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{{"pool_version", `UPDATE resource_pools SET version=2`}, {"binding_disabled", `UPDATE bindings SET enabled=0`}, {"credential_revision", `UPDATE bindings SET credential_revision='rev-2'`}, {"account_hold", `INSERT INTO holds(hold_id,scope_kind,scope_id,reason,status,evidence_json,created_at) VALUES('h','account','acct-1','held','open','[]',1)`}, {"guard_omitted", `INSERT INTO guard_states(scope,evidence_id,revision,allowed,observed_at,expires_at,source_ref,evidence_json) VALUES('bind-1','guard',1,0,1,9000,'fixture','{}')`}, {"restore", `UPDATE runtime_control SET restore_guard=1`}} {
		t.Run(tc.name, func(t *testing.T) {
			f := preparationFixture(t, 2)
			db := openRawDB(t, f.path)
			defer db.Close()
			execOK(t, db, tc.sql)
			reply, err := f.prepare(t, "a1")
			assertPreparationBlocked(t, reply, err)
			replay, err := f.prepare(t, "a1")
			assertPreparationBlocked(t, replay, err)
			if n := queryInt(t, db, `SELECT COUNT(*) FROM decision_records WHERE stage='rejected'`); n != 1 {
				t.Fatalf("rejected decision rows=%d", n)
			}
			if n := queryInt(t, db, `SELECT COUNT(*) FROM attempts`); n != 0 {
				t.Fatalf("attempt leak=%d", n)
			}
		})
	}
}
func TestPreparationConsumeRechecksCurrentPolicy(t *testing.T) {
	for _, sql := range []string{`UPDATE bindings SET enabled=0`, `UPDATE runtime_control SET control_version=control_version+1`, `UPDATE bindings SET credential_revision='rev-2'`, `UPDATE operations SET cancel_requested_at=35`, `UPDATE operations SET generation=2`, `UPDATE operations SET state='succeeded',result_json='{}'`, `UPDATE resource_states SET capacity_units=0 WHERE resource_id='r2'`} {
		t.Run(sql, func(t *testing.T) {
			f := preparationFixture(t, 2)
			if _, err := f.prepare(t, "a1"); err != nil {
				t.Fatal(err)
			}
			p, err := f.AuthorizeDispatch(context.Background(), claimAuth(), f.owner, "a1", 30)
			if err != nil {
				t.Fatal(err)
			}
			db := openRawDB(t, f.path)
			defer db.Close()
			execOK(t, db, sql)
			if err = f.ConsumeDispatchPermit(context.Background(), claimAuth(), f.owner, p, "digest", 40); err == nil {
				t.Fatal("stale policy consumed")
			}
			if n := queryInt(t, db, `SELECT COUNT(*) FROM dispatch_permits WHERE status='consumed'`); n != 0 {
				t.Fatalf("consumed despite stale policy=%d", n)
			}
		})
	}
}
func TestPreparationConcurrentCapacityAndSingleConsume(t *testing.T) {
	f := preparationFixture(t, 50)
	ctx := context.Background()
	owners := make([]domain.OwnerToken, 100)
	for i := range owners {
		in := claimInput(`{"prompt":"parallel"}`)
		in.OperationID = domain.ID(fmt.Sprintf("parallel-%d", i))
		in.DeadlineAt = 10000
		r, err := commands.Claim(ctx, f.st, claimAuth(), in, claimFingerprint(t, in), commands.ClaimPolicy{RuntimeID: "rt1", Now: 10, LeaseMillis: 9000, MaxRuntimeAttempts: 3})
		if err != nil {
			t.Fatal(err)
		}
		owners[i] = *r.Owner
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	var first domain.OwnerToken
	var aid domain.ID
	for i, o := range owners {
		wg.Add(1)
		go func(i int, o domain.OwnerToken) {
			defer wg.Done()
			p := f.plan
			p.PlanID = domain.ID(fmt.Sprintf("parallel-plan-%d", i))
			id := domain.ID(fmt.Sprintf("parallel-attempt-%d", i))
			r, err := f.TryPrepare(ctx, claimAuth(), o, id, p, nil, "digest", 20)
			mu.Lock()
			defer mu.Unlock()
			if err == nil && r.Kind == "prepared" {
				count++
				first = o
				aid = id
			} else if err != nil && !errors.Is(err, commands.ErrPreparationBlocked) {
				t.Errorf("prepare error=%v", err)
			} else if err == nil && r.Kind != "blocked" {
				t.Errorf("unexpected prepare reply=%+v", r)
			}
		}(i, o)
	}
	wg.Wait()
	if count != 50 {
		t.Fatalf("admitted=%d want50", count)
	}
	permit, err := f.AuthorizeDispatch(ctx, claimAuth(), first, aid, 30)
	if err != nil {
		t.Fatal(err)
	}
	count = 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := f.ConsumeDispatchPermit(ctx, claimAuth(), first, permit, "digest", 40)
			if err == nil {
				mu.Lock()
				count++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if count != 1 {
		t.Fatalf("consume count=%d", count)
	}
	db := openRawDB(t, f.path)
	defer db.Close()
	if n := queryInt(t, db, `SELECT COUNT(*) FROM attempts`); n != 50 {
		t.Fatalf("attempts=%d", n)
	}
	if n := queryInt(t, db, `SELECT reserved_units+active_units FROM resource_states WHERE resource_id='r2'`); n != 50 {
		t.Fatalf("occupancy=%d", n)
	}
}
func TestPreparationPlanIdentityQuantityAndBudgetReject(t *testing.T) {
	for index, mutate := range []func(*domain.ExecutionPlan){func(p *domain.ExecutionPlan) { p.Requirements[0].Unit = "wrong" }, func(p *domain.ExecutionPlan) { p.Requirements[0].UpperBound = -1 }, func(p *domain.ExecutionPlan) { p.Requirements[0].ResourceIDs = []domain.ID{"invented"} }, func(p *domain.ExecutionPlan) { p.Requirements = p.Requirements[:1] }, func(p *domain.ExecutionPlan) { p.WindowRuleRevision = "invented" }, func(p *domain.ExecutionPlan) { p.Bounds[0].ExpiresAt = 15 }, func(p *domain.ExecutionPlan) { p.ConfigRevision = "other" }, func(p *domain.ExecutionPlan) { p.ProviderTypeID = "other-provider" }, func(p *domain.ExecutionPlan) { p.Endpoint = "https://other.example/v1" }, func(p *domain.ExecutionPlan) { p.CredentialRef = "other-secret" }} {
		t.Run(fmt.Sprintf("invalid_plan_%d", index), func(t *testing.T) {
			f := preparationFixture(t, 2)
			mutate(&f.plan)
			reply, err := f.prepare(t, "a1")
			assertPreparationBlocked(t, reply, err)
		})
	}
	f := preparationFixture(t, 2)
	db := openRawDB(t, f.path)
	defer db.Close()
	execOK(t, db, `UPDATE operations SET permits_issued=max_attempts`)
	reply, err := f.prepare(t, "a1")
	assertPreparationBlocked(t, reply, err)
	if n := queryInt(t, db, `SELECT COUNT(*) FROM decision_records WHERE stage='rejected'`); n != 1 {
		t.Fatalf("budget rejection not persisted=%d", n)
	}
}

// A16: changes after prepare must not leave either activation or a permit.
func TestPreparationAuthorizeRechecksAndRollsBack(t *testing.T) {
	for _, sql := range []string{`UPDATE resource_pools SET version=2`, `UPDATE bindings SET enabled=0`, `UPDATE operations SET cancel_requested_at=25`, `UPDATE resource_states SET capacity_units=0 WHERE resource_id='r2'`, `UPDATE accounts SET definition_json='{"allowMetered":false}'`} {
		t.Run(sql, func(t *testing.T) {
			f := preparationFixture(t, 2)
			db := openRawDB(t, f.path)
			defer db.Close()
			if sql == `UPDATE accounts SET definition_json='{"allowMetered":false}'` { // metered limit needs both first-claim and current policy
				execOK(t, db, `UPDATE operations SET strategy_json=json_set(strategy_json,'$.allowMetered',json('true'))`)
				execOK(t, db, `UPDATE resources SET definition_json=json_set(definition_json,'$.Metered',json('true')) WHERE resource_id='r1'`)
			}
			if _, err := f.prepare(t, "a1"); err != nil {
				t.Fatal(err)
			}
			execOK(t, db, sql)
			p, err := f.AuthorizeDispatch(context.Background(), claimAuth(), f.owner, "a1", 30)
			if err == nil || p.ID() != "" {
				t.Fatal("changed policy authorized")
			}
			if n := queryInt(t, db, `SELECT COUNT(*) FROM dispatch_permits`); n != 0 {
				t.Fatalf("partial permit=%d", n)
			}
			if n := queryInt(t, db, `SELECT permits_issued FROM operations`); n != 0 {
				t.Fatalf("partial budget=%d", n)
			}
			if n := queryInt(t, db, `SELECT active_units FROM resource_states WHERE resource_id='r2'`); n != 0 {
				t.Fatalf("partial activation=%d", n)
			}
		})
	}
}
func TestPreparationConsumeReceiptFailureIsSpentLocally(t *testing.T) {
	f := preparationFixture(t, 2)
	if _, err := f.prepare(t, "a1"); err != nil {
		t.Fatal(err)
	}
	p, err := f.AuthorizeDispatch(context.Background(), claimAuth(), f.owner, "a1", 30)
	if err != nil {
		t.Fatal(err)
	}
	db := openRawDB(t, f.path)
	defer db.Close()
	execOK(t, db, `CREATE TRIGGER fail_consume BEFORE INSERT ON command_receipts WHEN NEW.kind='consume' BEGIN SELECT RAISE(ABORT,'consume receipt failure'); END`)
	if err = f.ConsumeDispatchPermit(context.Background(), claimAuth(), f.owner, p, "digest", 40); err == nil {
		t.Fatal("consume receipt failure succeeded")
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM dispatch_permits WHERE status='granted'`); n != 1 {
		t.Fatalf("durable CAS did not roll back=%d", n)
	}
	execOK(t, db, `DROP TRIGGER fail_consume`)
	if err = f.ConsumeDispatchPermit(context.Background(), claimAuth(), f.owner, p, "digest", 41); err == nil {
		t.Fatal("failed/ambiguous consumption regained local send authority")
	}
}
func TestPreparationGuardRevisionAndUnknownIsolation(t *testing.T) {
	f := preparationFixture(t, 2)
	db := openRawDB(t, f.path)
	defer db.Close()
	execOK(t, db, `INSERT INTO guard_states(scope,evidence_id,revision,allowed,observed_at,expires_at,source_ref,evidence_json) VALUES('bind-1','guard',1,1,1,100,'fixture','{}')`)
	g := domain.GuardEvidence{ID: "guard", Scope: "bind-1", Revision: 1, Allowed: true, ObservedAt: 1, ExpiresAt: 100, SourceRef: "fixture"}
	p := f.plan
	p.PlanID = "guard-plan"
	if _, err := f.TryPrepare(context.Background(), claimAuth(), f.owner, "a1", p, []domain.GuardEvidence{g}, "digest", 20); err != nil {
		t.Fatal(err)
	}
	execOK(t, db, `UPDATE guard_states SET revision=2`)
	if _, err := f.AuthorizeDispatch(context.Background(), claimAuth(), f.owner, "a1", 30); err == nil {
		t.Fatal("revised guard accepted")
	}
	// Unknown occupancy in another attempt retains capacity across new requests.
	other := preparationFixture(t, 2)
	otherDB := openRawDB(t, other.path)
	defer otherDB.Close()
	execOK(t, otherDB, `UPDATE resource_states SET uncertain_units=2 WHERE resource_id='r2'`)
	reply, err := other.prepare(t, "a2")
	assertPreparationBlocked(t, reply, err)
}
func TestPreparationWindowMigrationAtomicity(t *testing.T) {
	for _, targetCapacity := range []int{2, 0} {
		t.Run(fmt.Sprintf("capacity_%d", targetCapacity), func(t *testing.T) {
			f := preparationFixture(t, 2)
			db := openRawDB(t, f.path)
			defer db.Close()
			execOK(t, db, `UPDATE resources SET definition_json=json_set(definition_json,'$.Window',json('{"Kind":"fixed","Period":"minute","TimeZone":"UTC"}')) WHERE resource_id='r1'`)
			execOK(t, db, `UPDATE resource_states SET window_key='sr1:0:60000' WHERE resource_id='r1'`)
			execOK(t, db, `INSERT INTO resource_states(resource_id,window_key,capacity_units) VALUES('r1','sr1:60000:120000',?)`, targetCapacity)
			execOK(t, db, `UPDATE operations SET deadline_at=200000,lease_until=200000`)
			f.plan.Bounds[0].ExpiresAt = 190000
			f.plan.Bounds[1].ExpiresAt = 190000
			if _, err := f.prepare(t, "a1"); err != nil {
				t.Fatal(err)
			}
			p, err := f.AuthorizeDispatch(context.Background(), claimAuth(), f.owner, "a1", 60001)
			if targetCapacity == 0 {
				if err == nil || p.ID() != "" {
					t.Fatal("migration without capacity granted")
				}
				if n := queryInt(t, db, `SELECT reserved_units FROM resource_states WHERE resource_id='r1' AND window_key='sr1:0:60000'`); n != 1 {
					t.Fatalf("old reserve lost=%d", n)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if n := queryInt(t, db, `SELECT reserved_units FROM resource_states WHERE resource_id='r1' AND window_key='sr1:0:60000'`); n != 0 {
				t.Fatalf("old reserve retained=%d", n)
			}
			if n := queryInt(t, db, `SELECT reserved_units FROM resource_states WHERE resource_id='r1' AND window_key='sr1:60000:120000'`); n != 1 {
				t.Fatalf("new reserve missing=%d", n)
			}
			if err = f.ConsumeDispatchPermit(context.Background(), claimAuth(), f.owner, p, "digest", 60002); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestPreparationWaterfallAndRollingSnapshots(t *testing.T) {
	t.Run("waterfall", func(t *testing.T) {
		f := preparationFixture(t, 2)
		db := openRawDB(t, f.path)
		defer db.Close()
		execOK(t, db, `UPDATE resources SET definition_json=json_set(definition_json,'$.CommercialRole','entitlement','$.ProviderOrder',1,'$.Metered',json('true')) WHERE resource_id='r1'`)
		f.plan.Requirements[0].Mode = "provider_entitlement_waterfall"
		reply, err := f.prepare(t, "a1")
		assertPreparationBlocked(t, reply, err)
		if reply.Reasons[0].Code != "preparation_blocked" {
			t.Fatalf("metered path reason=%+v", reply.Reasons)
		}
		execOK(t, db, `UPDATE operations SET strategy_json=json_set(strategy_json,'$.allowMetered',json('true'))`)
		if reply, err := f.prepare(t, "a2"); err != nil || reply.Kind != "prepared" {
			t.Fatal(err)
		}
		p, err := f.AuthorizeDispatch(context.Background(), claimAuth(), f.owner, "a2", 30)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.ConsumeDispatchPermit(context.Background(), claimAuth(), f.owner, p, "digest", 40); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rolling", func(t *testing.T) {
		f := preparationFixture(t, 2)
		db := openRawDB(t, f.path)
		defer db.Close()
		execOK(t, db, `UPDATE resources SET definition_json=json_set(definition_json,'$.Window',json('{"Kind":"rolling","DurationMillis":100}')) WHERE resource_id='r1'`)
		execOK(t, db, `UPDATE resource_states SET window_key='sr1:rolling',committed_units=2 WHERE resource_id='r1'`)
		execOK(t, db, `INSERT INTO command_receipts(scope,command_id,kind,input_hash,result_json,created_at) VALUES('seed','usage','seed','x','{}',1)`)
		execOK(t, db, `INSERT INTO resource_events(event_id,command_scope,command_id,resource_id,window_key,event_kind,committed_delta,charged_at,evidence_json,recorded_at) VALUES('old','seed','usage','r1','sr1:rolling','consume',2,1,'[]',1)`)
		q, err := f.s.Quote(context.Background(), claimAuth(), f.plan, 150)
		if err != nil || q[0].Available != 2 {
			t.Fatalf("rolling quote %+v %v", q, err)
		}
		p := f.plan
		p.PlanID = "rolling-plan"
		if _, err = f.TryPrepare(context.Background(), claimAuth(), f.owner, "a1", p, nil, "digest", 150); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPreparationDeltaGateRejectsInvariantViolations(t *testing.T) {
	snap := domain.ResourceSnapshot{ResourceID: "r", WindowKey: "w", Kind: "counter", Version: 1, Capacity: 10}
	for _, d := range []domain.ResourceDelta{
		{ResourceID: "other", WindowKey: "w", ExpectedVersion: 1, Reserved: 1},
		{ResourceID: "r", WindowKey: "other", ExpectedVersion: 1, Reserved: 1},
		{ResourceID: "r", WindowKey: "w", ExpectedVersion: 2, Reserved: 1},
		{ResourceID: "r", WindowKey: "w", ExpectedVersion: 1, Reserved: -1},
		{ResourceID: "r", WindowKey: "w", ExpectedVersion: 1, Reserved: 1, Committed: 1},
		{ResourceID: "r", WindowKey: "w", ExpectedVersion: 1, Reserved: 1, Uncertain: -1},
		{ResourceID: "r", WindowKey: "w", ExpectedVersion: 1, Reserved: 1, Active: 1},
	} {
		if err := ledger.ValidatePreparationChange(snap, d, 1, "reserve"); err == nil {
			t.Fatalf("illegal delta accepted: %+v", d)
		}
	}
}
