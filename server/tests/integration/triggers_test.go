package integration

import (
	"strings"
	"testing"
)

func TestTerminalOperationIsImmutable(t *testing.T) {
	_, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 1)
	seedOperation(t, db, "ns-1", "op-1", "pool-1")

	// running → terminal stays legal.
	execOK(t, db, `UPDATE operations SET state = 'succeeded', result_json = '{"state":"succeeded"}'
		WHERE namespace = 'ns-1' AND operation_id = 'op-1'`)

	stateErr := execErr(t, db, `UPDATE operations SET state = 'failed'
		WHERE namespace = 'ns-1' AND operation_id = 'op-1'`)
	if !strings.Contains(stateErr.Error(), "terminal operation is immutable") {
		t.Fatalf("want terminal-state trigger ABORT, got: %v", stateErr)
	}
	resultErr := execErr(t, db, `UPDATE operations SET result_json = '{"state":"succeeded","v":2}'
		WHERE namespace = 'ns-1' AND operation_id = 'op-1'`)
	if !strings.Contains(resultErr.Error(), "terminal result is immutable") {
		t.Fatalf("want terminal-result trigger ABORT, got: %v", resultErr)
	}
}

func TestRunningOperationStaysMutable(t *testing.T) {
	_, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 1)
	seedOperation(t, db, "ns-1", "op-1", "pool-1")

	// The terminal-immutability triggers only fire from a terminal state;
	// a running operation may still move to a terminal one.
	execOK(t, db, `UPDATE operations SET state = 'cancelled', result_json = '{"state":"cancelled"}'
		WHERE namespace = 'ns-1' AND operation_id = 'op-1'`)
	if got := queryInt(t, db, "SELECT COUNT(*) FROM operations WHERE state = 'cancelled'"); got != 1 {
		t.Fatalf("cancelled rows = %d, want 1", got)
	}
}

func TestOperationEventsAppendOnly(t *testing.T) {
	_, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 1)
	seedOperation(t, db, "ns-1", "op-1", "pool-1")
	execOK(t, db, `INSERT INTO operation_events(namespace, operation_id, sequence, event_type, payload_json, created_at)
		VALUES('ns-1', 'op-1', 1, 'accepted', '{}', 1)`)

	updErr := execErr(t, db, `UPDATE operation_events SET payload_json = '{"tampered":true}'
		WHERE namespace = 'ns-1' AND operation_id = 'op-1' AND sequence = 1`)
	if !strings.Contains(updErr.Error(), "append-only") {
		t.Fatalf("want append-only trigger on UPDATE, got: %v", updErr)
	}
	delErr := execErr(t, db, `DELETE FROM operation_events
		WHERE namespace = 'ns-1' AND operation_id = 'op-1' AND sequence = 1`)
	if !strings.Contains(delErr.Error(), "append-only") {
		t.Fatalf("want append-only trigger on DELETE, got: %v", delErr)
	}
}

func TestResourceEventsAppendOnly(t *testing.T) {
	_, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 1)
	execOK(t, db, `INSERT INTO resources(resource_id, owner_kind, owner_id, metric_id, unit, quantum_exp, kind, semantics_ref, semantics_revision, definition_json)
		VALUES('res-1', 'account', 'acct-1', 'tokens', 'token', 0, 'counter', 'sem-1', 'rev-1', '{}')`)
	execOK(t, db, `INSERT INTO resource_states(resource_id, window_key, capacity_units)
		VALUES('res-1', 'w-1', 100)`)
	execOK(t, db, `INSERT INTO command_receipts(scope, command_id, kind, input_hash, result_json, created_at)
		VALUES('scope-1', 'cmd-1', 'settle', 'hash-1', '{}', 1)`)
	execOK(t, db, `INSERT INTO resource_events(event_id, command_scope, command_id, resource_id, window_key, event_kind, evidence_json, recorded_at)
		VALUES('ev-1', 'scope-1', 'cmd-1', 'res-1', 'w-1', 'reserve', '{}', 1)`)

	updErr := execErr(t, db, `UPDATE resource_events SET committed_delta = 5 WHERE event_id = 'ev-1'`)
	if !strings.Contains(updErr.Error(), "append-only") {
		t.Fatalf("want append-only trigger on UPDATE, got: %v", updErr)
	}
	delErr := execErr(t, db, `DELETE FROM resource_events WHERE event_id = 'ev-1'`)
	if !strings.Contains(delErr.Error(), "append-only") {
		t.Fatalf("want append-only trigger on DELETE, got: %v", delErr)
	}
}
