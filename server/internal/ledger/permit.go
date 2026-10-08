package ledger

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"

	"ant/server/internal/domain"
)

// K2: DispatchPermit and PreparedHTTPRequest handles are created only by
// their owning package, carry an unpredictable token plus private identity,
// and are registered so binding facts (attempt, runtime, digest) can be read
// back through controlled accessors. No public constructor, no JSON fields,
// no reflection/unsafe escapes; an HTTP caller can never reconstruct one.

var (
	ErrPermitUnknown    = errors.New("permit: unknown handle")
	ErrPermitRuntime    = errors.New("permit: handle not issued by this runtime")
	ErrPermitNotGranted = errors.New("permit: not in granted state")
	ErrPermitDigest     = errors.New("permit: request digest mismatch")
	ErrPermitCredential = errors.New("permit: credential revision mismatch")
)

// DispatchPermit is granted by Ledger.AuthorizeDispatch after the executing
// transition; exactly one exists per attempt. Binding facts live in the
// registry (and durably in dispatch_permits), not in client-visible fields.
type DispatchPermit struct {
	permitID  domain.ID
	attemptID domain.ID
	runtimeID domain.ID
	nonce     domain.ID
}

// Accessors are the controlled read path for the registry identity; they
// never expose the nonce to other packages.
func (p DispatchPermit) ID() domain.ID        { return p.permitID }
func (p DispatchPermit) AttemptID() domain.ID { return p.attemptID }
func (p DispatchPermit) RuntimeID() domain.ID { return p.runtimeID }

func (p DispatchPermit) zero() bool { return p.permitID == "" }

type permitRecord struct {
	attemptID          domain.ID
	runtimeID          domain.ID
	nonce              domain.ID
	requestDigest      string
	credentialRevision domain.ID
	status             string // granted / consumed / revoked
}

// PermitRegistry is the in-process registry backing issued permits. The
// durable permit row (dispatch_permits) remains the authoritative CAS state;
// this registry is the runtime-local fast gate described by K2/03-EXECUTION §4.
type PermitRegistry struct {
	mu        sync.Mutex
	byID      map[domain.ID]*permitRecord
	byAttempt map[domain.ID]domain.ID
}

func NewPermitRegistry() *PermitRegistry {
	return &PermitRegistry{byID: map[domain.ID]*permitRecord{}, byAttempt: map[domain.ID]domain.ID{}}
}

func randomToken() domain.ID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("ledger: entropy unavailable: " + err.Error())
	}
	return domain.ID(hex.EncodeToString(b[:]))
}

// Issue registers a granted permit for the attempt. Exactly one live permit
// per attempt; a second issue for the same attempt is rejected (I09).
func (r *PermitRegistry) Issue(attemptID, runtimeID domain.ID, requestDigest string, credentialRevision domain.ID) (DispatchPermit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if attemptID == "" || runtimeID == "" {
		return DispatchPermit{}, ErrPermitUnknown
	}
	if _, exists := r.byAttempt[attemptID]; exists {
		return DispatchPermit{}, ErrPermitNotGranted // one permit per attempt
	}
	p := DispatchPermit{
		permitID:  randomToken(),
		attemptID: attemptID,
		runtimeID: runtimeID,
		nonce:     randomToken(),
	}
	r.byID[p.permitID] = &permitRecord{
		attemptID: attemptID, runtimeID: runtimeID, nonce: p.nonce,
		requestDigest: requestDigest, credentialRevision: credentialRevision,
		status: "granted",
	}
	r.byAttempt[attemptID] = p.permitID
	return p, nil
}

// Validate checks a presented permit against the registry without consuming.
// All three expected binding facts are REQUIRED (R07 rework): an empty
// runtime, digest or credential revision is a validation failure, never a
// check waiver. Unknown, foreign-runtime, digest or credential mismatch are
// all rejected.
func (r *PermitRegistry) Validate(p DispatchPermit, runtimeID domain.ID, requestDigest string, credentialRevision domain.ID) error {
	if runtimeID == "" || requestDigest == "" || credentialRevision == "" {
		// A missing expectation cannot prove pairing — fail closed.
		return ErrPermitDigest
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.byID[p.permitID]
	if !ok || p.zero() || p.nonce == "" {
		return ErrPermitUnknown
	}
	if p.attemptID != rec.attemptID || p.runtimeID != rec.runtimeID || p.nonce != rec.nonce {
		return ErrPermitUnknown // forged or mutated handle
	}
	if runtimeID != rec.runtimeID {
		return ErrPermitRuntime
	}
	if requestDigest != rec.requestDigest {
		return ErrPermitDigest
	}
	if credentialRevision != rec.credentialRevision {
		return ErrPermitCredential
	}
	if rec.status != "granted" {
		return ErrPermitNotGranted
	}
	return nil
}

// MarkConsumed performs the local CAS: only a currently granted permit can
// transition to consumed; reuse or a revoked/consumed handle is rejected.
// The ledger store performs the same CAS durably (dispatch_permits).
func (r *PermitRegistry) MarkConsumed(p DispatchPermit, runtimeID domain.ID, requestDigest string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.byID[p.permitID]
	if !ok || p.zero() || p.nonce != rec.nonce || p.attemptID != rec.attemptID {
		return ErrPermitUnknown
	}
	if runtimeID != rec.runtimeID {
		return ErrPermitRuntime
	}
	if requestDigest != rec.requestDigest {
		return ErrPermitDigest
	}
	if rec.status != "granted" {
		return ErrPermitNotGranted
	}
	rec.status = "consumed"
	return nil
}

// MarkRevoked closes a granted permit without consumption (K3 abort path);
// used by AbortBeforeDispatch and sweep. Returns the previous status.
func (r *PermitRegistry) MarkRevoked(p DispatchPermit) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.byID[p.permitID]
	if !ok {
		return "", ErrPermitUnknown
	}
	prev := rec.status
	if prev == "granted" {
		rec.status = "revoked"
	}
	return prev, nil
}

// ReleaseTerminal removes the in-memory permit record after the durable
// attempt has reached a terminal state. Callers must invoke this only after
// the terminal transaction commits; the durable dispatch_permits row remains
// authoritative for audit/recovery. Removing both indexes bounds registry
// memory and makes any stale handle fail closed as unknown.
func (r *PermitRegistry) ReleaseTerminal(attemptID domain.ID) bool {
	if r == nil || attemptID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	permitID, ok := r.byAttempt[attemptID]
	if !ok {
		return false
	}
	delete(r.byID, permitID)
	delete(r.byAttempt, attemptID)
	return true
}
