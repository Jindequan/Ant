package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/store/sqlite"
)

func TestOperationQueriesAreNamespaceScopedAndKeysetStable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "operations.db")
	clock := &testClock{now: 1_790_000_000_000}
	st, err := sqlite.Open(path, sqlite.Config{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(ctx, st); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(ctx); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	seed.SetMaxOpenConns(1)
	if _, err := seed.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	for _, ns := range []string{"ns-one", "ns-two"} {
		if _, err := seed.Exec(`INSERT INTO resource_pools(pool_id,namespace,name,enabled,version,definition_json) VALUES(?,?,?,?,?,?)`, "pool-"+ns, ns, "Pool", 1, 1, `{}`); err != nil {
			t.Fatal(err)
		}
	}
	terminal := json.RawMessage(`{"operationId":"op-done","state":"succeeded","code":"ok","reasons":[],"retrySafety":"unsafe","attempts":["attempt-done"],"resources":[{"resourceId":"res-main","windowKey":"w1","metricId":"tokens","unit":"token","consumed":"5","consumptionBasis":"provider_report","occupancy":"not_applicable","evidenceRefs":[]}],"decisionRefs":[],"outputRef":"operation/op-done","outputAvailability":"available","suggestedAction":"none"}`)
	insertOperationForQueryTest(t, seed, "ns-one", "op-done", "pool-ns-one", "succeeded", 30, terminal)
	insertOperationForQueryTest(t, seed, "ns-one", "op-live", "pool-ns-one", "running", 20, nil)
	insertOperationForQueryTest(t, seed, "ns-one", "op-live-2", "pool-ns-one", "running", 10, nil)
	insertOperationForQueryTest(t, seed, "ns-two", "other-only", "pool-ns-two", "running", 40, nil)
	for _, event := range []struct {
		sequence int
		typ      string
		payload  string
	}{{1, "accepted", `{"state":"running"}`}, {2, "cancel_requested", `{"commandId":"cancel"}`}} {
		if _, err := seed.Exec(`INSERT INTO operation_events(namespace,operation_id,sequence,event_type,payload_json,created_at) VALUES(?,?,?,?,?,?)`, "ns-one", "op-live", event.sequence, event.typ, event.payload, 10+event.sequence); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := seed.Exec(`INSERT INTO attempts(attempt_id,namespace,operation_id,binding_id,account_id,offer_id,plan_id,generation,request_digest,guard_refs_json,phase,external_outcome,provider_request_id,lease_until,created_at,updated_at,version)
		VALUES('attempt-done','ns-one','op-done','binding','account','offer','plan',1,'digest','[]','terminal','known_success','provider-request',40,1,30,1)`); err != nil {
		t.Fatal(err)
	}
	decisionTrace := `{"hookRegistryRevision":"revision-sha256","hookRegistryManifest":{"apiVersion":"v1","registrations":[]},"plannerCandidateOrder":["offer"],"hookCandidateOrder":["offer"],"selectedOfferId":"offer","poolId":"pool-ns-one","poolVersion":3,"memberPriority":7,"configRevision":"config-r3","candidates":[{"offerId":"offer","priority":7,"reasons":[]}]}`
	for _, row := range []struct {
		id, stage, trace string
		createdAt        int64
	}{
		{"prepare/ns-one/op-done/attempt-done", "prepare", decisionTrace, 30},
		{"dispatch/ns-one/op-done/attempt-done", "dispatch", `{}`, 31},
	} {
		if _, err := seed.Exec(`INSERT INTO decision_records(decision_id,namespace,operation_id,attempt_id,stage,snapshot_json,trace_truncated,created_at,trace_json) VALUES(?,?,?,?,?,'{}',0,?,?)`, row.id, "ns-one", "op-done", "attempt-done", row.stage, row.createdAt, row.trace); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := seed.Exec(`INSERT INTO decision_records(decision_id,namespace,operation_id,stage,snapshot_json,trace_truncated,created_at) VALUES('legacy-decision','ns-one','op-live','prepare','{}',0,24)`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO accounts(account_id,provider_owner_ref,enabled,version,definition_json) VALUES('account','owner',1,1,'{}')`, nil},
		{`INSERT INTO bindings(binding_id,account_id,adapter_ref,domain,endpoint,credential_ref,credential_revision,enabled,version,definition_json) VALUES('binding','account','adapter','ai/text','https://provider.invalid','credential','rev',1,1,'{}')`, nil},
		{`INSERT INTO resources(resource_id,owner_kind,owner_id,metric_id,unit,quantum_exp,kind,semantics_ref,semantics_revision,definition_json) VALUES('res-main','binding','binding','tokens','token',0,'counter','sem','rev','{}')`, nil},
		{`INSERT INTO resource_states(resource_id,window_key,start_ms,end_ms,capacity_units,committed_units,adjustment_units,reserved_units,active_units,uncertain_units,adjustment_version,version) VALUES('res-main','w1',0,100,100,5,0,0,0,0,0,1)`, nil},
		{`INSERT INTO resource_offers(offer_id,binding_id,domain,capability_profile,enabled,version,definition_json) VALUES('offer','binding','ai/text','profile.chat.basic',1,1,'{}')`, nil},
		{`INSERT INTO offer_resources(offer_id,resource_id) VALUES('offer','res-main')`, nil},
		{`INSERT INTO pool_members(pool_id,offer_id,priority,enabled,version) VALUES('pool-ns-one','offer',0,1,1)`, nil},
		{`INSERT INTO resource_events(event_id,command_scope,command_id,resource_id,window_key,attempt_id,event_kind,committed_delta,adjustment_delta,evidence_json,recorded_at) VALUES('usage-event','op-done','attempt-done','res-main','w1','attempt-done','consume',5,0,'[]',30)`, nil},
		{`INSERT INTO holds(hold_id,scope_kind,scope_id,reason,status,evidence_json,created_at) VALUES('hold-open','resource','res-main','manual_review','open','["source-1"]',31)`, nil},
		{`INSERT INTO encumbrances(encumbrance_id,attempt_id,resource_id,window_key,kind,units,status,evidence_json,created_at) VALUES('enc-open','attempt-done','res-main','w1','occupancy',1,'open','[]',31)`, nil},
	} {
		if _, err := seed.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatalf("seed admin projection: %v", err)
		}
	}
	if _, err := seed.Exec(`INSERT INTO command_receipts(scope,command_id,kind,input_hash,result_json,created_at) VALUES('scope','adjust','adjustment','hash','{}',32)`); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(`INSERT INTO resource_events(event_id,command_scope,command_id,resource_id,window_key,event_kind,adjustment_delta,evidence_json,recorded_at) VALUES('adjust-event','scope','adjust','res-main','w1','adjust',-2,'{"originalEventId":"usage-event"}',32)`); err != nil {
		t.Fatal(err)
	}
	for seq, body := range []string{"first", "second"} {
		digest := sha256.Sum256([]byte(body))
		if _, err := seed.Exec(`INSERT INTO output_chunks(attempt_id,sequence,data,sha256,created_at) VALUES(?,?,?,?,?)`, "attempt-done", seq+1, []byte(body), hex.EncodeToString(digest[:]), 25+seq); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := seed.Exec(`UPDATE operations SET visible_output_at=24 WHERE namespace='ns-one' AND operation_id='op-done'`); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	queries, err := NewOperationQueries(st)
	if err != nil {
		t.Fatal(err)
	}
	auth := domain.AuthContext{PrincipalID: "reader", Namespace: "ns-one", Permissions: []domain.Permission{domain.PermRead}}
	manager := domain.AuthContext{PrincipalID: "manager", Namespace: "ns-one", Permissions: []domain.Permission{domain.PermRead, domain.PermManage}}
	managedDetail, err := queries.Get(ctx, manager, "op-done")
	if err != nil || len(managedDetail.AdjustableCharges) != 1 || managedDetail.AdjustableCharges[0].EventID != "usage-event" || managedDetail.AdjustableCharges[0].RemainingReversible != 3 {
		t.Fatalf("managed adjustable charges = %+v err=%v", managedDetail.AdjustableCharges, err)
	}
	if len(managedDetail.ManagedHolds) != 1 || managedDetail.ManagedHolds[0].HoldID != "hold-open" || managedDetail.ManagedHolds[0].CanClear {
		t.Fatalf("managed hold projection = %+v", managedDetail.ManagedHolds)
	}
	readDetail, err := queries.Get(ctx, auth, "op-done")
	if err != nil || len(readDetail.AdjustableCharges) != 0 || len(readDetail.ManagedHolds) != 0 {
		t.Fatalf("read-only admin projection = %+v err=%v", readDetail, err)
	}
	page, err := queries.List(ctx, auth, "", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Operations) != 1 || page.Operations[0].OperationID != "op-done" || page.Operations[0].RequestDomain != "ai/text" || page.Operations[0].Result == nil || page.NextCursor == "" {
		t.Fatalf("first page = %+v", page)
	}
	if len(page.Operations[0].Attempts) != 1 || page.Operations[0].Attempts[0].ProviderRequestID != "provider-request" {
		t.Fatalf("attempt projection = %+v", page.Operations[0].Attempts)
	}
	operationJSON, err := json.Marshal(page.Operations[0])
	if err != nil || strings.Contains(string(operationJSON), "must not be projected") || strings.Contains(string(operationJSON), "payload") {
		t.Fatalf("operation list projected request content: %s err=%v", operationJSON, err)
	}
	output, err := queries.Output(ctx, auth, "op-done", 0, 1)
	if err != nil || len(output.Chunks) != 1 || output.Chunks[0].Sequence != 1 || string(output.Chunks[0].Data) != "first" || output.NextSequence == nil || *output.NextSequence != 1 || !output.Complete || output.Availability != "available" {
		t.Fatalf("first output page = %+v err=%v", output, err)
	}
	output, err = queries.Output(ctx, auth, "op-done", *output.NextSequence, 10)
	if err != nil || len(output.Chunks) != 1 || output.Chunks[0].Sequence != 2 || string(output.Chunks[0].Data) != "second" || output.NextSequence != nil {
		t.Fatalf("reconnected output page = %+v err=%v", output, err)
	}
	if _, err := queries.Output(ctx, auth, "other-only", 0, 10); !errors.Is(err, ErrOperationQueryNotFound) {
		t.Fatalf("cross-namespace output error = %v", err)
	}
	second, err := queries.List(ctx, auth, page.NextCursor, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Operations) != 1 || second.Operations[0].OperationID != "op-live" || second.NextCursor == "" {
		t.Fatalf("second page = %+v", second)
	}
	third, err := queries.List(ctx, auth, second.NextCursor, 1, "")
	if err != nil || len(third.Operations) != 1 || third.Operations[0].OperationID != "op-live-2" || third.NextCursor != "" {
		t.Fatalf("third page = %+v err=%v", third, err)
	}
	if _, err := queries.Get(ctx, auth, "other-only"); !errors.Is(err, ErrOperationQueryNotFound) {
		t.Fatalf("cross-namespace get error = %v", err)
	}
	decisions, err := queries.Decisions(ctx, auth, "op-done")
	if err != nil || len(decisions) != 2 || decisions[0].Stage != "prepare" || decisions[1].Stage != "dispatch" || decisions[1].AttemptID != "attempt-done" || decisions[1].PoolID != "pool-ns-one" || decisions[1].PoolVersion != 3 || decisions[1].OfferID != "offer" || decisions[1].MemberPriority != 7 || decisions[1].ConfigRevision != "config-r3" || decisions[1].HookRegistryRevision != "revision-sha256" || len(decisions[1].HookCandidateOrder) != 1 || decisions[1].HookCandidateOrder[0] != "offer" || len(decisions[1].Candidates) != 1 {
		t.Fatalf("persisted decisions = %+v err=%v", decisions, err)
	}
	if legacy, err := queries.Decisions(ctx, auth, "op-live"); err != nil || len(legacy) != 0 {
		t.Fatalf("legacy empty trace should be reported as no saved decision detail: %+v err=%v", legacy, err)
	}
	if _, err := queries.Decisions(ctx, auth, "other-only"); !errors.Is(err, ErrOperationQueryNotFound) {
		t.Fatalf("cross-namespace decisions error = %v", err)
	}
	eventPage, err := queries.Events(ctx, auth, "op-live", 0, 1)
	if err != nil || len(eventPage.Events) != 1 || eventPage.Events[0].Sequence != 1 || eventPage.Events[0].Type != "accepted" || eventPage.LastSequence != 2 || eventPage.State != domain.Running {
		t.Fatalf("first event page = %+v err=%v", eventPage, err)
	}
	eventPage, err = queries.Events(ctx, auth, "op-live", eventPage.Events[0].Sequence, 10)
	if err != nil || len(eventPage.Events) != 1 || eventPage.Events[0].Sequence != 2 || eventPage.Events[0].Type != "cancel_requested" {
		t.Fatalf("reconnected event page = %+v err=%v", eventPage, err)
	}
	if _, err := queries.Events(ctx, auth, "op-live", 3, 10); !errors.Is(err, ErrOperationQueryInvalid) {
		t.Fatalf("future event cursor error = %v", err)
	}
	if _, err := queries.Events(ctx, auth, "other-only", 0, 10); !errors.Is(err, ErrOperationQueryNotFound) {
		t.Fatalf("cross-namespace events error = %v", err)
	}
	if _, err := queries.List(ctx, auth, "not-a-cursor", 1, ""); !errors.Is(err, ErrOperationQueryInvalid) {
		t.Fatalf("invalid cursor error = %v", err)
	}
	filtered, err := queries.List(ctx, auth, "", 1, string(domain.Running))
	if err != nil || len(filtered.Operations) != 1 || filtered.Operations[0].OperationID != "op-live" || filtered.NextCursor == "" {
		t.Fatalf("running filter = %+v err=%v", filtered, err)
	}
	filteredNext, err := queries.List(ctx, auth, filtered.NextCursor, 1, string(domain.Running))
	if err != nil || len(filteredNext.Operations) != 1 || filteredNext.Operations[0].OperationID != "op-live-2" || filteredNext.NextCursor != "" {
		t.Fatalf("running filter next page = %+v err=%v", filteredNext, err)
	}
	if _, err := queries.List(ctx, auth, page.NextCursor, 1, string(domain.Running)); !errors.Is(err, ErrOperationQueryInvalid) {
		t.Fatalf("cursor/filter mismatch error = %v", err)
	}
	if _, err := queries.List(ctx, auth, "", 10, "not-a-state"); !errors.Is(err, ErrOperationQueryInvalid) {
		t.Fatalf("invalid state filter error = %v", err)
	}
	if _, err := queries.List(ctx, domain.AuthContext{PrincipalID: "execute-only", Namespace: "ns-one", Permissions: []domain.Permission{domain.PermExecute}}, "", 10, ""); !errors.Is(err, ErrOperationQueryForbidden) {
		t.Fatalf("execute-only list error = %v", err)
	}
	if _, err := queries.Decisions(ctx, domain.AuthContext{PrincipalID: "execute-only", Namespace: "ns-one", Permissions: []domain.Permission{domain.PermExecute}}, "op-done"); !errors.Is(err, ErrOperationQueryForbidden) {
		t.Fatalf("execute-only decisions error = %v", err)
	}
}

func insertOperationForQueryTest(t *testing.T, db *sql.DB, namespace, operationID, poolID, state string, updatedAt int64, result []byte) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO operations(namespace,operation_id,pool_id,fingerprint,state,runtime_id,owner_id,generation,lease_until,deadline_at,max_attempts,permits_issued,strategy_json,cancel_requested_at,visible_output_at,result_json,created_at,updated_at,version)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, namespace, operationID, poolID, "digest", state, "runtime", "owner", 1, updatedAt+10, updatedAt+100, 3, 0, `{"request":{"domain":"ai/text","capabilityProfile":"profile.chat.basic","payload":{"prompt":"must not be projected"}}}`, nil, nil, nullableTestJSON(result), updatedAt-1, updatedAt, 1)
	if err != nil {
		t.Fatal(err)
	}
}

func nullableTestJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}
