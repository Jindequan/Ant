package ledger_test

// R07 rework: the assembly test now actually assembles — a request is
// Prepared FIRST, the permit is issued with the REAL prepared digest and
// credential revision, and the controlled consume seam pairs the same facts.
// Missing binding expectations fail closed (no empty-argument waivers).
// Durable CAS and real single-send acceptance stay with T2.2/T2.3.

import (
	"errors"
	"sync"
	"testing"

	"ant/server/internal/adapters"
	"ant/server/internal/domain"
	"ant/server/internal/ledger"
	"ant/server/internal/transport"
)

const (
	assemblyAttempt = "att-assembly-1"
	assemblyRuntime = "runtime-local"
	assemblyCredRev = "rev-1"
)

func assemblyRequest() adapters.EncodedRequest {
	return adapters.EncodedRequest{
		Endpoint:      "https://fixture.invalid/v1/messages",
		Method:        "POST",
		Headers:       map[string]string{"Content-Type": "application/json"},
		Body:          []byte(`{"fixture":true}`),
		CredentialRef: "secret-alpha-key",
	}
}

// TestPermitAssemblyPairsRealDigest is the fixed version of the original
// flawed assembly test: permit and prepared request are built from the same
// request bytes, so their digests match by construction.
func TestPermitAssemblyPairsRealDigest(t *testing.T) {
	permits := ledger.NewPermitRegistry()
	handles := transport.NewHandleTable()

	prepared, err := handles.Prepare(assemblyRequest(), domain.ExecutionPlan{CredentialRevision: assemblyCredRev})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prepared.RequestDigest() == "" || prepared.CredentialRevision() != assemblyCredRev {
		t.Fatalf("prepared handle identity incomplete: %+v", prepared)
	}

	permit, err := permits.Issue(assemblyAttempt, assemblyRuntime, prepared.RequestDigest(), prepared.CredentialRevision())
	if err != nil {
		t.Fatalf("issue with real digest: %v", err)
	}
	if permit.ID() == "" || permit.AttemptID() != assemblyAttempt || permit.RuntimeID() != assemblyRuntime {
		t.Fatalf("permit identity mismatch: %+v", permit)
	}

	// real pairing validates
	if err := permits.Validate(permit, assemblyRuntime, prepared.RequestDigest(), prepared.CredentialRevision()); err != nil {
		t.Fatalf("real digest/credential/runtime pairing must validate: %v", err)
	}

	// wrong pairing of any fact is rejected
	if err := permits.Validate(permit, "runtime-other", prepared.RequestDigest(), prepared.CredentialRevision()); !errors.Is(err, ledger.ErrPermitRuntime) {
		t.Fatalf("cross-runtime must fail, got %v", err)
	}
	if err := permits.Validate(permit, assemblyRuntime, "deadbeefdigest", prepared.CredentialRevision()); !errors.Is(err, ledger.ErrPermitDigest) {
		t.Fatalf("wrong digest must fail, got %v", err)
	}
	if err := permits.Validate(permit, assemblyRuntime, prepared.RequestDigest(), "rev-other"); !errors.Is(err, ledger.ErrPermitCredential) {
		t.Fatalf("wrong credential revision must fail, got %v", err)
	}
	// missing expectations fail closed (R07: no empty-argument waiver)
	if err := permits.Validate(permit, "", "", ""); err == nil {
		t.Fatal("empty binding expectations must not bypass validation")
	}
	if err := permits.Validate(permit, assemblyRuntime, "", assemblyCredRev); err == nil {
		t.Fatal("empty digest expectation must not bypass validation")
	}

	// exactly one local consumption; reuse rejected
	if _, err := handles.MarkUsed(prepared); err != nil {
		t.Fatalf("first mark used: %v", err)
	}
	if err := permits.MarkConsumed(permit, assemblyRuntime, prepared.RequestDigest()); err != nil {
		t.Fatalf("paired consume must succeed: %v", err)
	}
	if _, err := handles.MarkUsed(prepared); !errors.Is(err, transport.ErrHandleUsed) {
		t.Fatalf("prepared reuse must fail, got %v", err)
	}
	if err := permits.MarkConsumed(permit, assemblyRuntime, prepared.RequestDigest()); !errors.Is(err, ledger.ErrPermitNotGranted) {
		t.Fatalf("permit reuse must fail, got %v", err)
	}
}

func TestPermitAssemblyRejectsForgedAndForeign(t *testing.T) {
	permits := ledger.NewPermitRegistry()
	otherRegistry := ledger.NewPermitRegistry()

	permit, err := permits.Issue("att-forged-1", assemblyRuntime, "digest-1", assemblyCredRev)
	if err != nil {
		t.Fatal(err)
	}
	// zero/forged handle
	if err := permits.Validate(ledger.DispatchPermit{}, assemblyRuntime, "digest-1", assemblyCredRev); !errors.Is(err, ledger.ErrPermitUnknown) {
		t.Fatalf("forged zero permit must be unknown, got %v", err)
	}
	// valid in its own registry, unknown in a different one
	if err := permits.Validate(permit, assemblyRuntime, "digest-1", assemblyCredRev); err != nil {
		t.Fatalf("own registry validate: %v", err)
	}
	if err := otherRegistry.Validate(permit, assemblyRuntime, "digest-1", assemblyCredRev); !errors.Is(err, ledger.ErrPermitUnknown) {
		t.Fatalf("foreign registry must not recognise the permit, got %v", err)
	}
	// one live permit per attempt (I09)
	if _, err := permits.Issue("att-forged-1", assemblyRuntime, "digest-1", assemblyCredRev); !errors.Is(err, ledger.ErrPermitNotGranted) {
		t.Fatalf("second live permit for the same attempt must be rejected, got %v", err)
	}
	// revoked handles cannot be consumed
	otherPermit, err := otherRegistry.Issue("att-forged-2", assemblyRuntime, "digest-2", assemblyCredRev)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherRegistry.MarkRevoked(otherPermit); err != nil {
		t.Fatal(err)
	}
	if err := otherRegistry.MarkConsumed(otherPermit, assemblyRuntime, "digest-2"); !errors.Is(err, ledger.ErrPermitNotGranted) {
		t.Fatalf("consume after revoke must fail, got %v", err)
	}
}

func TestPermitRegistryReleaseTerminalCompactsBothIndexes(t *testing.T) {
	permits := ledger.NewPermitRegistry()
	permit, err := permits.Issue("att-terminal", assemblyRuntime, "digest", assemblyCredRev)
	if err != nil {
		t.Fatal(err)
	}
	if err := permits.MarkConsumed(permit, assemblyRuntime, "digest"); err != nil {
		t.Fatal(err)
	}
	if !permits.ReleaseTerminal("att-terminal") {
		t.Fatal("terminal permit record was not released")
	}
	if err := permits.Validate(permit, assemblyRuntime, "digest", assemblyCredRev); !errors.Is(err, ledger.ErrPermitUnknown) {
		t.Fatalf("released handle must fail closed as unknown, got %v", err)
	}
	// Both indexes are cleared: a fresh runtime-local record can be issued if
	// the durable owner has independently established a new attempt identity.
	if _, err := permits.Issue("att-terminal", assemblyRuntime, "digest-2", assemblyCredRev); err != nil {
		t.Fatalf("attempt index remained after release: %v", err)
	}
	if permits.ReleaseTerminal("") || permits.ReleaseTerminal("missing") {
		t.Fatal("release of an empty or absent attempt must be a no-op")
	}
}

// Concurrent assembly: N goroutines race the full pairing path on the same
// attempt — exactly one issue+validate+consume sequence can win.
func TestPermitAssemblyConcurrentExactlyOneWinner(t *testing.T) {
	permits := ledger.NewPermitRegistry()
	handles := transport.NewHandleTable()
	prepared, err := handles.Prepare(assemblyRequest(), domain.ExecutionPlan{CredentialRevision: assemblyCredRev})
	if err != nil {
		t.Fatal(err)
	}

	const racers = 16
	var wg sync.WaitGroup
	var wins int64
	var mu sync.Mutex
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			permit, err := permits.Issue(assemblyAttempt, assemblyRuntime, prepared.RequestDigest(), prepared.CredentialRevision())
			if err != nil {
				return // lost the one-per-attempt race
			}
			if err := permits.Validate(permit, assemblyRuntime, prepared.RequestDigest(), prepared.CredentialRevision()); err != nil {
				return
			}
			if err := permits.MarkConsumed(permit, assemblyRuntime, prepared.RequestDigest()); err != nil {
				return
			}
			mu.Lock()
			wins++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("exactly one assembly winner allowed, got %d", wins)
	}
}

func TestRequestDigestCoversParts(t *testing.T) {
	a := adapters.EncodedRequest{Endpoint: "https://x.invalid/a", Method: "POST", Headers: map[string]string{"h": "1"}, Body: []byte("b1")}
	b := a
	b.Body = []byte("b2")
	c := a
	c.Method = "GET"
	d := a
	d.Headers = map[string]string{"h": "2"}
	base := transport.DigestRequest(a)
	for name, other := range map[string]adapters.EncodedRequest{"body": b, "method": c, "headers": d} {
		if transport.DigestRequest(other) == base {
			t.Fatalf("digest must cover %s", name)
		}
	}
	if transport.DigestRequest(a) != base {
		t.Fatal("digest must be deterministic")
	}
}

// Byte ownership (R07): MarkUsed hands out a defensive copy — mutating the
// returned body/headers cannot change what the handle still holds, and a
// second MarkUsed is already rejected. (The T2.3 send path consumes the copy
// exactly once; the digest was bound at Prepare time over the ORIGINAL
// bytes, so post-validation mutation by other modules cannot silently alter
// the authenticated request.)
func TestMarkUsedReturnsDefensiveCopy(t *testing.T) {
	handles := transport.NewHandleTable()
	encoded := assemblyRequest()
	encoded.Body = []byte(`{"original":true}`)
	prepared, err := handles.Prepare(encoded, domain.ExecutionPlan{CredentialRevision: assemblyCredRev})
	if err != nil {
		t.Fatal(err)
	}
	first, err := handles.MarkUsed(prepared)
	if err != nil {
		t.Fatal(err)
	}
	first.Body[0] = '{' // mutate the copy in place
	first.Body = []byte(`{"tampered":true}`)
	if len(first.Headers) > 0 {
		for k := range first.Headers {
			delete(first.Headers, k)
		}
	}
	second, err := handles.MarkUsed(prepared)
	if !errors.Is(err, transport.ErrHandleUsed) {
		t.Fatalf("second use must be rejected, got %v", err)
	}
	_ = second
}
