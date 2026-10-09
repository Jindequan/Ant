package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/ledger/commands"
	"ant/server/internal/store"
	"ant/server/internal/store/sqlite"
)

func claimAuth() domain.AuthContext {
	return domain.AuthContext{PrincipalID: "principal-1", Namespace: "ns-1", Permissions: []domain.Permission{domain.PermExecute}}
}

func claimInput(payload string) domain.ExecuteInput {
	return domain.ExecuteInput{
		OperationID: "op-1",
		PoolID:      "pool-1",
		Request: domain.DomainRequest{
			Domain: "ai/text", CapabilityProfile: "cap-profile", Payload: json.RawMessage(payload),
		},
		DeadlineAt: 1000, MaxAttempts: 2,
	}
}

func claimFingerprint(t *testing.T, input domain.ExecuteInput) string {
	t.Helper()
	fingerprint, err := domain.SemanticFingerprint(domain.FingerprintInput{
		Domain: input.Request.Domain, PoolID: input.PoolID,
		CapabilityProfile: input.Request.CapabilityProfile,
		RequiredAffinity:  input.Affinity, Payload: input.Request.Payload,
	})
	if err != nil {
		t.Fatalf("semantic fingerprint: %v", err)
	}
	return fingerprint
}

func activateSeedConfig(t *testing.T, st store.Store) {
	t.Helper()
	err := st.SubmitWrite(context.Background(), store.WriteCommand{
		ID: "activate-seed",
		Run: func(tx store.WriterTx) error {
			return tx.SetActiveConfig(store.ActiveConfigRecord{Revision: "rev-seed", Version: 1})
		},
	})
	if err != nil {
		t.Fatalf("activate seed config: %v", err)
	}
}

func TestClaimIsIdempotentAndConflictsOnDifferentSemantics(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 1)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	activateSeedConfig(t, st)

	in := claimInput(`{"prompt":"hello"}`)
	fingerprint := claimFingerprint(t, in)
	policy := commands.ClaimPolicy{RuntimeID: "runtime-1", Now: 10, LeaseMillis: 100, MaxRuntimeAttempts: 3}
	first, err := commands.Claim(ctx, st, claimAuth(), in, fingerprint, policy)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first.Kind != "new" || first.Owner == nil || first.Operation == nil || first.Operation.State != domain.Running {
		t.Fatalf("first claim = %+v, want new owner and running view", first)
	}

	// Deadline and attempt budget are first-claim strategy, not semantic
	// identity. Reusing the ID cannot overwrite the stored strategy or owner.
	retry := in
	retry.DeadlineAt = 900
	retry.MaxAttempts = 1
	second, err := commands.Claim(ctx, st, claimAuth(), retry, fingerprint,
		commands.ClaimPolicy{RuntimeID: "runtime-2", Now: 11, LeaseMillis: 100, MaxRuntimeAttempts: 3})
	if err != nil {
		t.Fatalf("same-semantics retry: %v", err)
	}
	if second.Kind != "inProgress" || second.Owner != nil {
		t.Fatalf("same-semantics retry = %+v, want inProgress without a second owner", second)
	}
	// Policy limits only gate creation. A retry past its original deadline with
	// unusable current runtime policy still resolves the existing operation.
	deadlineRetry := retry
	deadlineRetry.DeadlineAt = 1
	deadlineRetry.MaxAttempts = 0
	resolved, err := commands.Claim(ctx, st, claimAuth(), deadlineRetry, fingerprint,
		commands.ClaimPolicy{Now: 2000})
	if err != nil || resolved.Kind != "inProgress" || resolved.Owner != nil {
		t.Fatalf("expired same-ID retry = %+v, %v; want inProgress without owner", resolved, err)
	}
	// An expired owner is not silently replaced by a retry; LED-04 recovery
	// must first reconcile the durable lease and execution evidence.
	expired, err := commands.Claim(ctx, st, claimAuth(), retry, fingerprint,
		commands.ClaimPolicy{RuntimeID: "runtime-3", Now: 200, LeaseMillis: 100, MaxRuntimeAttempts: 3})
	if err != nil || expired.Kind != "inProgress" || expired.Owner != nil {
		t.Fatalf("expired-owner retry = %+v, %v; want inProgress without owner", expired, err)
	}

	different := claimInput(`{"prompt":"different"}`)
	conflict, err := commands.Claim(ctx, st, claimAuth(), different, claimFingerprint(t, different), policy)
	if err != nil {
		t.Fatalf("different-semantics retry: %v", err)
	}
	if conflict.Kind != "conflict" || conflict.Owner != nil {
		t.Fatalf("different-semantics retry = %+v, want conflict", conflict)
	}

	db = openRawDB(t, path)
	defer db.Close()
	if got := queryInt(t, db, `SELECT COUNT(*) FROM operations WHERE namespace='ns-1' AND operation_id='op-1'`); got != 1 {
		t.Fatalf("operation rows=%d, want exactly 1", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM command_receipts WHERE scope='claim/ns-1' AND command_id='op-1'`); got != 1 {
		t.Fatalf("claim receipt rows=%d, want exactly 1", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM operations WHERE runtime_id='runtime-2'`); got != 0 {
		t.Fatalf("retry replaced original owner runtime; rows=%d", got)
	}
}

func TestClaimRequiresExecutePermissionAndRestoreGuardClear(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 1)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	activateSeedConfig(t, st)

	in := claimInput(`{"prompt":"guarded"}`)
	policy := commands.ClaimPolicy{RuntimeID: "runtime-1", Now: 10, LeaseMillis: 100, MaxRuntimeAttempts: 3}
	unauthorized := claimAuth()
	unauthorized.Permissions = []domain.Permission{domain.PermRead}
	if _, err := commands.Claim(ctx, st, unauthorized, in, claimFingerprint(t, in), policy); err != commands.ErrClaimForbidden {
		t.Fatalf("claim without execute permission = %v, want forbidden", err)
	}
	invalidPolicy := policy
	invalidPolicy.MaxRuntimeAttempts = 0
	if _, err := commands.Claim(ctx, st, claimAuth(), in, claimFingerprint(t, in), invalidPolicy); err != commands.ErrClaimInvalid {
		t.Fatalf("new claim with invalid runtime policy = %v, want invalid", err)
	}

	db = openRawDB(t, path)
	execOK(t, db, `UPDATE runtime_control SET restore_guard=1 WHERE singleton=1`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := commands.Claim(ctx, st, claimAuth(), in, claimFingerprint(t, in), policy); err != commands.ErrClaimNotReady {
		t.Fatalf("claim while restore guard is set = %v, want not ready", err)
	}
	db = openRawDB(t, path)
	defer db.Close()
	if got := queryInt(t, db, `SELECT COUNT(*) FROM operations WHERE namespace='ns-1' AND operation_id='op-1'`); got != 0 {
		t.Fatalf("rejected claim persisted operation row: %d", got)
	}
}

func TestClaimReceiptFailureRollsBackOperation(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 1)
	execOK(t, db, `CREATE TRIGGER fail_claim_receipt BEFORE INSERT ON command_receipts
		WHEN NEW.scope='claim/ns-1' BEGIN SELECT RAISE(ABORT, 'receipt failure'); END`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	activateSeedConfig(t, st)
	in := claimInput(`{"prompt":"rollback"}`)
	_, err := commands.Claim(ctx, st, claimAuth(), in, claimFingerprint(t, in),
		commands.ClaimPolicy{RuntimeID: "runtime-1", Now: 10, LeaseMillis: 100, MaxRuntimeAttempts: 3})
	if err == nil {
		t.Fatal("claim succeeded despite receipt insert failure")
	}
	db = openRawDB(t, path)
	defer db.Close()
	if got := queryInt(t, db, `SELECT COUNT(*) FROM operations WHERE namespace='ns-1' AND operation_id='op-1'`); got != 0 {
		t.Fatalf("operation survived transaction rollback: %d rows", got)
	}
}

func TestClaimTerminalRetryReplaysPersistedResult(t *testing.T) {
	ctx := context.Background()
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 1)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	activateSeedConfig(t, st)
	in := claimInput(`{"prompt":"terminal"}`)
	fingerprint := claimFingerprint(t, in)
	policy := commands.ClaimPolicy{RuntimeID: "runtime-1", Now: 10, LeaseMillis: 100, MaxRuntimeAttempts: 3}
	first, err := commands.Claim(ctx, st, claimAuth(), in, fingerprint, policy)
	if err != nil || first.Kind != "new" {
		t.Fatalf("initial claim = %+v, %v", first, err)
	}

	db = openRawDB(t, path)
	execOK(t, db, `UPDATE operations SET state='succeeded', result_json=CAST(? AS TEXT), updated_at=20
		WHERE namespace='ns-1' AND operation_id='op-1'`,
		`{"operationId":"op-1","state":"succeeded","code":"ok"}`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	retry, err := commands.Claim(ctx, st, claimAuth(), in, fingerprint, policy)
	if err != nil {
		t.Fatalf("terminal retry: %v", err)
	}
	if retry.Kind != "replay" || retry.Owner != nil || retry.Result == nil || retry.Result.Code != "ok" || retry.Operation == nil || retry.Operation.Result == nil {
		t.Fatalf("terminal retry = %+v, want persisted replay without owner", retry)
	}
	// A terminal operation is still replayable after its original deadline;
	// current runtime policy is irrelevant to an existing ID.
	lateRetry := in
	lateRetry.DeadlineAt = 1
	lateRetry.MaxAttempts = 0
	late, err := commands.Claim(ctx, st, claimAuth(), lateRetry, fingerprint, commands.ClaimPolicy{Now: 2000})
	if err != nil || late.Kind != "replay" || late.Owner != nil || late.Result == nil || late.Result.Code != "ok" {
		t.Fatalf("late terminal retry = %+v, %v; want replay without owner", late, err)
	}
}

// TestClaimProcessHelper is invoked by the parent test binary in independent
// processes to exercise SQLite's cross-process BEGIN IMMEDIATE serialization.
func TestClaimProcessHelper(t *testing.T) {
	if os.Getenv("ANT_CLAIM_HELPER") != "1" {
		return
	}
	path := os.Getenv("ANT_CLAIM_DB")
	worker := os.Getenv("ANT_CLAIM_WORKER")
	if path == "" || worker == "" {
		t.Fatal("helper environment incomplete")
	}
	st, err := sqlite.Open(path, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close(context.Background())
	ready := filepath.Join(filepath.Dir(path), "ready-"+worker)
	if err := os.WriteFile(ready, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(filepath.Dir(path), "release")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(release); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper release barrier timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
	in := claimInput(`{"prompt":"cross-process"}`)
	reply, err := commands.Claim(context.Background(), st, claimAuth(), in, claimFingerprint(t, in),
		commands.ClaimPolicy{RuntimeID: domain.ID("runtime-" + worker), Now: 10, LeaseMillis: 100, MaxRuntimeAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	owner := "-"
	if reply.Owner != nil {
		owner = string(reply.Owner.OwnerID)
	}
	fmt.Printf("CLAIM_RESULT %s %s\n", reply.Kind, owner)
}

func TestClaimCrossProcessGrantsExactlyOneOwner(t *testing.T) {
	st, path := openTestStore(t, testConfig())
	db := openRawDB(t, path)
	seedCatalog(t, db, "ns-1", "pool-1", 1)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	activateSeedConfig(t, st)

	commands := make([]*exec.Cmd, 0, 2)
	outputs := make([]*strings.Builder, 0, 2)
	for i := 1; i <= 2; i++ {
		worker := fmt.Sprint(i)
		cmd := exec.Command(os.Args[0], "-test.run=^TestClaimProcessHelper$")
		cmd.Env = append(os.Environ(), "ANT_CLAIM_HELPER=1", "ANT_CLAIM_DB="+path, "ANT_CLAIM_WORKER="+worker)
		var output strings.Builder
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper %s: %v", worker, err)
		}
		commands = append(commands, cmd)
		outputs = append(outputs, &output)
	}
	deadline := time.Now().Add(10 * time.Second)
	for i := 1; i <= 2; i++ {
		ready := filepath.Join(filepath.Dir(path), "ready-"+fmt.Sprint(i))
		for {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			if time.Now().After(deadline) {
				for _, cmd := range commands {
					_ = cmd.Process.Kill()
				}
				t.Fatal("helpers did not reach start barrier")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "release"), []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper %d: %v\n%s", i+1, err, outputs[i].String())
		}
	}
	newCount := 0
	for i, output := range outputs {
		if !strings.Contains(output.String(), "CLAIM_RESULT new ") && !strings.Contains(output.String(), "CLAIM_RESULT inProgress -") {
			t.Fatalf("helper %d returned unexpected result: %s", i+1, output.String())
		}
		if strings.Contains(output.String(), "CLAIM_RESULT new ") {
			newCount++
		}
	}
	if newCount != 1 {
		t.Fatalf("cross-process new owners=%d, want exactly 1; outputs=%q / %q", newCount, outputs[0], outputs[1])
	}
	db = openRawDB(t, path)
	defer db.Close()
	if got := queryInt(t, db, `SELECT COUNT(*) FROM operations WHERE namespace='ns-1' AND operation_id='op-1'`); got != 1 {
		t.Fatalf("operation rows=%d, want exactly 1", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM command_receipts WHERE scope='claim/ns-1' AND command_id='op-1'`); got != 1 {
		t.Fatalf("receipt rows=%d, want exactly 1", got)
	}
}
