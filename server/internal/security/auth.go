package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"slices"
	"sync"

	"ant/server/internal/domain"
)

// AUTH-01 (T2.1), gated G1-DEV: trusted identity and permission context.
// Design sources: docs/design/03-EXECUTION.md §2 (AuthProvider produces the
// ctx; namespace is derived server-side, never from the request body) and
// docs/design/05-RUNTIME-AND-ADAPTERS.md §5 (one-shot local bootstrap;
// sessions/tokens stored as hashes only; no public registration).
//
// This file is pure logic plus an in-process store: no network, no OS
// environment, no real credentials. The SQLite binding of token/grant
// records is wired by later T2.1 leaves (CFG-08/API-01) against the same
// ports.

// ErrUnauthenticated is returned for any missing, malformed, unknown,
// revoked or otherwise unusable credential. One error for every failure
// shape: the response must not reveal which factor failed.
var ErrUnauthenticated = errors.New("security: unauthenticated")

var errInvalidTokenRecord = errors.New("security: token record invalid")

// TokenKind distinguishes bootstrap/session tokens from rotatable API
// tokens. It is recorded metadata only; authorization never consults it.
type TokenKind string

const (
	TokenKindBootstrap TokenKind = "bootstrap"
	TokenKindSession   TokenKind = "session"
	TokenKindAPIToken  TokenKind = "api_token"
)

// TokenRecord is the server-held grant set for one credential. It is the
// ONLY source of Namespace and Permissions: a request body may echo them
// but can never widen or relocate them (design 03 §2).
type TokenRecord struct {
	PrincipalID domain.ID
	Namespace   domain.ID
	Permissions []domain.Permission
	Kind        TokenKind
	ExpiresAt   domain.Millis
	CSRFHash    string
}

// TokenStore persists token records keyed by the SHA-256 of the bearer
// value; the plaintext token never reaches storage. Implementations must
// treat Lookup misses, revoked entries and store failures uniformly via
// the provider, which maps all of them to ErrUnauthenticated.
type TokenStore interface {
	Put(hash TokenHash, record TokenRecord) error
	// PutBootstrapIfAbsent atomically creates the installation's one bootstrap
	// grant. Durable implementations must enforce this in a transaction.
	PutBootstrapIfAbsent(hash TokenHash, record TokenRecord) error
	// ExchangeBootstrap atomically consumes the one-time bootstrap token and
	// installs the first browser session. A failed exchange leaves neither a
	// session nor a revoked bootstrap credential.
	ExchangeBootstrap(bootstrapHash, sessionHash TokenHash, record TokenRecord) error
	Lookup(hash TokenHash) (TokenRecord, bool, error)
	Revoke(hash TokenHash) error
}

// TokenHash is the SHA-256 digest of a bearer token.
type TokenHash [sha256.Size]byte

// HashToken derives the storage key for a bearer value.
func HashToken(bearer string) TokenHash { return sha256.Sum256([]byte(bearer)) }

// MemoryTokenStore is the in-process TokenStore for local development and
// tests under G1-DEV. It holds hash-keyed records in memory only.
type MemoryTokenStore struct {
	mu               sync.RWMutex
	records          map[TokenHash]TokenRecord
	bootstrapCreated bool
}

// NewMemoryTokenStore returns an empty in-process store.
func NewMemoryTokenStore() *MemoryTokenStore {
	return &MemoryTokenStore{records: make(map[TokenHash]TokenRecord)}
}

// Put stores (or replaces) the record for a token hash.
func (m *MemoryTokenStore) Put(hash TokenHash, record TokenRecord) error {
	if record.Kind == TokenKindBootstrap || !validTokenRecord(record) {
		return errInvalidTokenRecord
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[hash] = cloneRecord(record)
	return nil
}

// PutBootstrapIfAbsent atomically stores the first bootstrap grant only.
func (m *MemoryTokenStore) PutBootstrapIfAbsent(hash TokenHash, record TokenRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if record.Kind != TokenKindBootstrap || !validTokenRecord(record) {
		return errInvalidTokenRecord
	}
	if m.bootstrapCreated {
		return errors.New("security: bootstrap already initialized")
	}
	m.records[hash] = cloneRecord(record)
	m.bootstrapCreated = true
	return nil
}

func (m *MemoryTokenStore) ExchangeBootstrap(bootstrapHash, sessionHash TokenHash, record TokenRecord) error {
	if record.Kind != TokenKindSession || !validTokenRecord(record) || record.CSRFHash == "" || record.ExpiresAt <= 0 {
		return errInvalidTokenRecord
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bootstrap, ok := m.records[bootstrapHash]
	if !ok || bootstrap.Kind != TokenKindBootstrap || bootstrap.PrincipalID != record.PrincipalID || bootstrap.Namespace != record.Namespace {
		return ErrUnauthenticated
	}
	if _, exists := m.records[sessionHash]; exists {
		return errInvalidTokenRecord
	}
	delete(m.records, bootstrapHash)
	m.records[sessionHash] = cloneRecord(record)
	return nil
}

// Lookup returns the record for a hash, if present and not revoked.
func (m *MemoryTokenStore) Lookup(hash TokenHash) (TokenRecord, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	record, ok := m.records[hash]
	return cloneRecord(record), ok, nil
}

// Revoke removes the record; later lookups miss and authenticate fails.
func (m *MemoryTokenStore) Revoke(hash TokenHash) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, hash)
	return nil
}

func cloneRecord(r TokenRecord) TokenRecord {
	out := r
	out.Permissions = slices.Clone(r.Permissions)
	return out
}

// validTokenRecord keeps malformed persisted identity data from becoming a
// trusted Principal. Stores are server-side inputs, but corruption and
// future token-issuance bugs must fail closed at both write and auth time.
func validTokenRecord(r TokenRecord) bool {
	if r.PrincipalID == "" || r.Namespace == "" || len(r.Permissions) == 0 {
		return false
	}
	switch r.Kind {
	case TokenKindBootstrap, TokenKindSession, TokenKindAPIToken:
	default:
		return false
	}
	for _, permission := range r.Permissions {
		if _, ok := knownPermissions[permission]; !ok {
			return false
		}
	}
	return true
}

// AuthProvider authenticates bearer tokens against server-held grants.
type AuthProvider struct {
	store TokenStore
}

// NewAuthProvider returns a provider over the given token store.
func NewAuthProvider(store TokenStore) *AuthProvider {
	return &AuthProvider{store: store}
}

// BootstrapAdmin mints the one-shot administrator credential of design
// 05 §5: the returned bearer value is shown exactly once here and only its
// hash is retained. Local CLI action; no public registration exists.
func BootstrapAdmin(store TokenStore, principalID, namespace domain.ID, perms []domain.Permission) (string, error) {
	if store == nil || principalID == "" || namespace == "" || len(perms) == 0 {
		return "", errors.New("security: bootstrap input invalid")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("security: bootstrap entropy unavailable")
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	record := TokenRecord{
		PrincipalID: principalID,
		Namespace:   namespace,
		Permissions: cloneRecord(TokenRecord{Permissions: perms}).Permissions,
		Kind:        TokenKindBootstrap,
	}
	if err := store.PutBootstrapIfAbsent(HashToken(token), record); err != nil {
		return "", errors.New("security: bootstrap store unavailable")
	}
	return token, nil
}

// Authenticate resolves the presented bearer token to its server-held
// principal. Every failure shape — empty, unknown, revoked — collapses to
// ErrUnauthenticated so callers cannot distinguish store contents.
func (a *AuthProvider) Authenticate(bearer string) (Principal, error) {
	if a == nil || a.store == nil || bearer == "" {
		return Principal{}, ErrUnauthenticated
	}
	record, ok, err := a.store.Lookup(HashToken(bearer))
	if err != nil || !ok || !validTokenRecord(record) {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{
		PrincipalID: record.PrincipalID,
		Namespace:   record.Namespace,
		// TokenStore is a persistence port; do not let a caller mutate an
		// implementation's backing grant slice through the returned principal.
		Permissions: slices.Clone(record.Permissions),
		Kind:        record.Kind,
		ExpiresAt:   record.ExpiresAt,
		CSRFHash:    record.CSRFHash,
	}, nil
}

// RevokeToken removes a credential; the next Authenticate fails.
func (a *AuthProvider) RevokeToken(bearer string) error {
	if a == nil || a.store == nil || bearer == "" {
		return ErrUnauthenticated
	}
	return a.store.Revoke(HashToken(bearer))
}

// knownPermissions guards against typos widening grants: Can only ever
// grants the five domain permission constants.
var knownPermissions = map[domain.Permission]struct{}{
	domain.PermExecute:   {},
	domain.PermRead:      {},
	domain.PermCancel:    {},
	domain.PermManage:    {},
	domain.PermConfigure: {},
}

// Can reports whether the principal holds perm on exactly the namespace ns.
// Cross-namespace requests are denied unconditionally: grants are scoped to
// the principal's own namespace and cannot be addressed to another one.
func Can(p Principal, perm domain.Permission, ns domain.ID) bool {
	if ns != p.Namespace {
		return false
	}
	if _, known := knownPermissions[perm]; !known {
		return false
	}
	return slices.Contains(p.Permissions, perm)
}

// ConstantTimeEqual is the comparison helper for any value derived from a
// credential-shaped input where a timing side channel could distinguish
// candidates (e.g. future session-id lookups). Hash-keyed map lookups in
// this file already avoid value-dependent branching; this helper exists so
// later wiring does not reintroduce early-exit compares.
func ConstantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
