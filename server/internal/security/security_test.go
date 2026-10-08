package security

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"ant/server/internal/domain"
)

// --- identity / permission context (design 03 §2, 05 §5) ---

const (
	nsA = domain.ID("ns-alpha")
	nsB = domain.ID("ns-beta")
)

func newTestProvider(t *testing.T) (*AuthProvider, *MemoryTokenStore, string) {
	t.Helper()
	store := NewMemoryTokenStore()
	token, err := BootstrapAdmin(store, "admin-1", nsA,
		[]domain.Permission{domain.PermExecute, domain.PermRead, domain.PermCancel, domain.PermManage, domain.PermConfigure})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return NewAuthProvider(store), store, token
}

func TestBootstrapTokenStoredAsHashOnly(t *testing.T) {
	provider, store, token := newTestProvider(t)
	if strings.Contains(token, " ") {
		t.Fatalf("bootstrap token contains separator: %q", token)
	}
	// The store is keyed by SHA-256 of the bearer; the plaintext must not
	// appear anywhere in the store's in-memory records.
	for hash := range store.records {
		var dump []byte
		dump, _ = json.Marshal(hash)
		if bytes.Contains(dump, []byte(token)) {
			t.Fatalf("token plaintext found in store under hash %x", hash)
		}
	}
	if _, err := provider.Authenticate(token); err != nil {
		t.Fatalf("authenticate after bootstrap: %v", err)
	}
}

func TestBootstrapAdminIsOneShotEvenAfterRevocation(t *testing.T) {
	store := NewMemoryTokenStore()
	first, err := BootstrapAdmin(store, "admin-1", nsA, []domain.Permission{domain.PermManage})
	if err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if err := store.Revoke(HashToken(first)); err != nil {
		t.Fatalf("revoke bootstrap: %v", err)
	}
	if _, err := BootstrapAdmin(store, "admin-2", nsB, []domain.Permission{domain.PermManage}); err == nil {
		t.Fatal("second bootstrap succeeded after revocation")
	}
}

func TestConcurrentBootstrapAdminCreatesExactlyOneToken(t *testing.T) {
	store := NewMemoryTokenStore()
	const attempts = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := BootstrapAdmin(store, "admin-1", nsA, []domain.Permission{domain.PermManage}); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successful bootstrap attempts = %d, want exactly one", successes)
	}
}

func TestMemoryTokenStoreConcurrentAccess(t *testing.T) {
	store := NewMemoryTokenStore()
	const workers = 32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := fmt.Sprintf("fake-token-%d", i)
			hash := HashToken(token)
			_ = store.Put(hash, TokenRecord{PrincipalID: "p", Namespace: nsA, Permissions: []domain.Permission{domain.PermRead}, Kind: TokenKindAPIToken})
			_, _, _ = store.Lookup(hash)
			_ = store.Revoke(hash)
		}(i)
	}
	wg.Wait()
}

func TestAuthenticateDerivesFromServerGrantsOnly(t *testing.T) {
	provider, _, token := newTestProvider(t)
	p, err := provider.Authenticate(token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if p.PrincipalID != "admin-1" || p.Namespace != nsA {
		t.Fatalf("unexpected principal: %+v", p)
	}
	// Permissions arrive as a copy: mutating them must not affect the store.
	p.Permissions[0] = "hijacked"
	p2, err := provider.Authenticate(token)
	if err != nil {
		t.Fatalf("re-authenticate: %v", err)
	}
	if p2.Permissions[0] != domain.PermExecute {
		t.Fatalf("store record mutated through returned slice: %v", p2.Permissions)
	}
}

type aliasingTokenStore struct{ record TokenRecord }

func (s *aliasingTokenStore) Put(_ TokenHash, record TokenRecord) error {
	s.record = record
	return nil
}
func (s *aliasingTokenStore) PutBootstrapIfAbsent(_ TokenHash, record TokenRecord) error {
	s.record = record
	return nil
}
func (s *aliasingTokenStore) ExchangeBootstrap(_ TokenHash, _ TokenHash, record TokenRecord) error {
	s.record = record
	return nil
}
func (s *aliasingTokenStore) Lookup(TokenHash) (TokenRecord, bool, error) {
	return s.record, true, nil // deliberately aliases the store's permission slice
}
func (s *aliasingTokenStore) Revoke(TokenHash) error { return nil }

func TestAuthenticateCopiesGrantsFromTokenStorePort(t *testing.T) {
	store := &aliasingTokenStore{record: TokenRecord{
		PrincipalID: "admin-1",
		Namespace:   nsA,
		Permissions: []domain.Permission{domain.PermRead},
		Kind:        TokenKindAPIToken,
	}}
	provider := NewAuthProvider(store)
	principal, err := provider.Authenticate("fake-token")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	principal.Permissions[0] = domain.PermManage
	if store.record.Permissions[0] != domain.PermRead {
		t.Fatalf("returned principal mutated backing TokenStore grants: %v", store.record.Permissions)
	}
}

func TestTokenStoresRejectInvalidRecordsAndAuthenticationFailsClosed(t *testing.T) {
	valid := TokenRecord{PrincipalID: "p", Namespace: nsA, Permissions: []domain.Permission{domain.PermRead}, Kind: TokenKindAPIToken}
	invalid := []TokenRecord{
		{Namespace: nsA, Permissions: []domain.Permission{domain.PermRead}, Kind: TokenKindAPIToken},
		{PrincipalID: "p", Permissions: []domain.Permission{domain.PermRead}, Kind: TokenKindAPIToken},
		{PrincipalID: "p", Namespace: nsA, Kind: TokenKindAPIToken},
		{PrincipalID: "p", Namespace: nsA, Permissions: []domain.Permission{"root"}, Kind: TokenKindAPIToken},
		{PrincipalID: "p", Namespace: nsA, Permissions: []domain.Permission{domain.PermRead}, Kind: "unknown"},
	}
	store := NewMemoryTokenStore()
	for i, record := range invalid {
		if err := store.Put(HashToken(fmt.Sprintf("invalid-%d", i)), record); err == nil {
			t.Fatalf("Put accepted invalid token record %d: %+v", i, record)
		}
		if err := store.PutBootstrapIfAbsent(HashToken(fmt.Sprintf("invalid-bootstrap-%d", i)), record); err == nil {
			t.Fatalf("PutBootstrapIfAbsent accepted invalid token record %d: %+v", i, record)
		}
		provider := NewAuthProvider(&aliasingTokenStore{record: record})
		if _, err := provider.Authenticate("fake-token"); err != ErrUnauthenticated {
			t.Fatalf("Authenticate accepted invalid token record %d: %v", i, err)
		}
	}
	if err := store.Put(HashToken("valid"), valid); err != nil {
		t.Fatalf("Put rejected valid record: %v", err)
	}
	bootstrap := valid
	bootstrap.Kind = TokenKindBootstrap
	if err := store.Put(HashToken("bootstrap-not-one-shot"), bootstrap); err == nil {
		t.Fatal("Put accepted bootstrap record outside PutBootstrapIfAbsent")
	}
}

func TestAuthenticateFailsClosedUniformly(t *testing.T) {
	provider, _, token := newTestProvider(t)
	for _, bearer := range []string{"", "not-a-token", token + "x"} {
		if _, err := provider.Authenticate(bearer); err != ErrUnauthenticated {
			t.Fatalf("Authenticate(%q) = %v, want ErrUnauthenticated", bearer, err)
		}
	}
	// After revocation the same token fails with the same single error; no
	// distinct "was revoked" signal exists to leak credential state.
	if err := provider.RevokeToken(token); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := provider.Authenticate(token); err != ErrUnauthenticated {
		t.Fatalf("revoked token: %v, want ErrUnauthenticated", err)
	}
}

type failingLookupStore struct{}

func (failingLookupStore) Put(TokenHash, TokenRecord) error                          { return nil }
func (failingLookupStore) PutBootstrapIfAbsent(TokenHash, TokenRecord) error         { return nil }
func (failingLookupStore) ExchangeBootstrap(TokenHash, TokenHash, TokenRecord) error { return nil }
func (failingLookupStore) Lookup(TokenHash) (TokenRecord, bool, error) {
	return TokenRecord{}, false, errors.New("private storage diagnostic")
}
func (failingLookupStore) Revoke(TokenHash) error { return nil }

func TestAuthenticateCollapsesStoreFailure(t *testing.T) {
	provider := NewAuthProvider(failingLookupStore{})
	if _, err := provider.Authenticate("fake-token"); err != ErrUnauthenticated {
		t.Fatalf("store failure exposed: %v", err)
	}
}

func TestCanDeniesCrossNamespaceEveryPermission(t *testing.T) {
	provider, _, token := newTestProvider(t)
	p, err := provider.Authenticate(token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	all := []domain.Permission{domain.PermExecute, domain.PermRead, domain.PermCancel, domain.PermManage, domain.PermConfigure}
	for _, perm := range all {
		if !Can(p, perm, nsA) {
			t.Fatalf("own namespace %s: %s should be granted", nsA, perm)
		}
		if Can(p, perm, nsB) {
			t.Fatalf("cross-namespace %s: %s must be denied (read/cancel/manage included)", nsB, perm)
		}
	}
	// Unknown permission names never grant, even on the own namespace.
	if Can(p, domain.Permission("root"), nsA) {
		t.Fatal("unknown permission granted")
	}
	// Empty principal denies everything.
	var zero Principal
	if Can(zero, domain.PermRead, nsA) {
		t.Fatal("zero principal granted")
	}
}

// --- secret boundary (design 05 §5) ---

func newTestService(t *testing.T) (*SecretService, *MemorySecretStore, []byte) {
	t.Helper()
	key := bytes.Repeat([]byte{0x42}, 32)
	svc, err := NewSecretService(key, NewMemorySecretStore())
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return svc, nil, key
}

func TestSecretWriteResolveRoundTrip(t *testing.T) {
	svc, _, _ := newTestService(t)
	view, err := svc.Write("cred-1", "rev-1", map[string]string{"api_key": "sk-fake-local-abcdef123456"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if view.Ref != "cred-1" || view.Revision != "rev-1" {
		t.Fatalf("view identity: %+v", view)
	}
	got, err := svc.Resolve(t.Context(), "cred-1", "rev-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got["api_key"] != "sk-fake-local-abcdef123456" {
		t.Fatalf("roundtrip mismatch")
	}
	// Mutating the resolved map must not corrupt the stored record.
	got["api_key"] = "tampered"
	again, err := svc.Resolve(t.Context(), "cred-1", "rev-1")
	if err != nil || again["api_key"] != "sk-fake-local-abcdef123456" {
		t.Fatalf("stored record mutated through resolved map: %v %v", again, err)
	}
}

func TestSecretRevisionCannotBeOverwritten(t *testing.T) {
	svc, _, _ := newTestService(t)
	first := "sk-fake-local-original-1234"
	if _, err := svc.Write("cred-1", "rev-1", map[string]string{"api_key": first}); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	if _, err := svc.Write("cred-1", "rev-1", map[string]string{"api_key": "sk-fake-local-replacement-5678"}); err != ErrSecretRevisionExists {
		t.Fatalf("second write for same revision = %v, want ErrSecretRevisionExists", err)
	}
	resolved, err := svc.Resolve(t.Context(), "cred-1", "rev-1")
	if err != nil {
		t.Fatalf("resolve original revision: %v", err)
	}
	if resolved["api_key"] != first {
		t.Fatalf("existing revision changed after rejected overwrite: %q", resolved["api_key"])
	}
}

func TestConcurrentSecretRevisionCreationIsOneShot(t *testing.T) {
	svc, _, _ := newTestService(t)
	const attempts = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	conflicts := 0
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			value := fmt.Sprintf("sk-fake-local-%08d", i)
			_, err := svc.Write("cred-1", "rev-1", map[string]string{"api_key": value})
			mu.Lock()
			defer mu.Unlock()
			switch err {
			case nil:
				successes++
			case ErrSecretRevisionExists:
				conflicts++
			default:
				t.Errorf("concurrent write: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if successes != 1 || conflicts != attempts-1 {
		t.Fatalf("writes: %d successes, %d conflicts; want 1 and %d", successes, conflicts, attempts-1)
	}
}

func TestSecretBoundToRefAndRevision(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.Write("cred-1", "rev-1", map[string]string{"api_key": "sk-fake-local-value-1"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Unknown revision and unknown ref are the same single error: revision
	// existence cannot be probed.
	if _, err := svc.Resolve(t.Context(), "cred-1", "rev-2"); err != ErrSecretUnknown {
		t.Fatalf("unknown revision: %v, want ErrSecretUnknown", err)
	}
	if _, err := svc.Resolve(t.Context(), "cred-9", "rev-1"); err != ErrSecretUnknown {
		t.Fatalf("unknown ref: %v, want ErrSecretUnknown", err)
	}
	// A record sealed under rev-1 must not open under a re-registered
	// rev-2 binding: the AAD check must fail, not return cleartext.
	store := NewMemorySecretStore()
	key := bytes.Repeat([]byte{0x42}, 32)
	svc2, err := NewSecretService(key, store)
	if err != nil {
		t.Fatalf("service2: %v", err)
	}
	sealedRef, sealedRev := domain.ID("cred-1"), domain.ID("rev-1")
	view, err := svc2.Write(sealedRef, sealedRev, map[string]string{"api_key": "sk-fake-local-value-2"})
	if err != nil {
		t.Fatalf("write2: %v", err)
	}
	_ = view
	raw, err := store.GetSealed(sealedRef, sealedRev)
	if err != nil {
		t.Fatalf("get sealed: %v", err)
	}
	if err := store.PutSealed(sealedRef, "rev-2", raw); err != nil {
		t.Fatalf("re-seal under other revision: %v", err)
	}
	if _, err := svc2.Resolve(t.Context(), sealedRef, "rev-2"); err != ErrSecretSeal {
		t.Fatalf("record moved across revisions: %v, want ErrSecretSeal", err)
	}
	// Wrong master key fails closed too.
	otherKey := bytes.Repeat([]byte{0x43}, 32)
	svc3, err := NewSecretService(otherKey, store)
	if err != nil {
		t.Fatalf("service3: %v", err)
	}
	if _, err := svc3.Resolve(t.Context(), sealedRef, sealedRev); err != ErrSecretSeal {
		t.Fatalf("wrong master key: %v, want ErrSecretSeal", err)
	}
}

func TestSecretViewMaskNeverCarriesCleartext(t *testing.T) {
	svc, _, _ := newTestService(t)
	plain := "sk-fake-local-abcdef123456"
	view, err := svc.Write("cred-1", "rev-1", map[string]string{"api_key": plain})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if strings.Contains(view.Mask, plain) || len(view.Mask) > 8 {
		t.Fatalf("mask leaks cleartext: %q", view.Mask)
	}
	if !strings.HasSuffix(view.Mask, "3456") {
		t.Fatalf("mask should end with last four: %q", view.Mask)
	}
	short, err := svc.Write("cred-2", "rev-1", map[string]string{"api_key": "short"})
	if err != nil {
		t.Fatalf("write short: %v", err)
	}
	if short.Mask != "****" {
		t.Fatalf("short value must be fully masked: %q", short.Mask)
	}
}

func TestSecretCleartextRedactedInStringAndLogs(t *testing.T) {
	plain := "sk-fake-local-topsecret"
	clear := NewSecretCleartext(map[string]string{"api_key": plain})
	if clear.String() == plain || !strings.Contains(clear.String(), "REDACTED") {
		t.Fatalf("String leaks: %q", clear.String())
	}
	// SecretCleartext is the only sanctioned carrier of credential
	// material. However it reaches a structured log — as a field value or
	// through generic formatting — its contents stay redacted.
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("resolved credential", "secret", clear)
	logger.Info("formatted", "viaString", fmt.Sprintf("%v", clear))
	logger.Info("go-syntax-format", "viaGoString", fmt.Sprintf("%#v", clear))
	logged := buf.String()
	if strings.Contains(logged, plain) {
		t.Fatalf("log leaks plaintext: %s", logged)
	}
	if !strings.Contains(logged, "[REDACTED]") {
		t.Fatalf("log lacks redaction marker: %s", logged)
	}
	// Get returns one field only through the explicit accessor; formatting
	// the whole credential container never reveals its backing values.
	if _, ok := clear.Get("api_key"); !ok {
		t.Fatal("Get lost the field")
	}
}

func TestMemorySecretStoreConcurrentAccess(t *testing.T) {
	store := NewMemorySecretStore()
	const workers = 32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ref := domain.ID(fmt.Sprintf("cred-%d", i))
			revision := domain.ID("rev-1")
			value := []byte(fmt.Sprintf("sealed-%d", i))
			_ = store.PutSealed(ref, revision, value)
			_, _ = store.GetSealed(ref, revision)
		}(i)
	}
	wg.Wait()
}

func TestSecretWriteRejectsInvalidInput(t *testing.T) {
	svc, _, _ := newTestService(t)
	cases := []struct {
		name     string
		ref      domain.ID
		revision domain.ID
		values   map[string]string
	}{
		{"empty ref", "", "rev-1", map[string]string{"api_key": "x"}},
		{"empty revision", "cred-1", "", map[string]string{"api_key": "x"}},
		{"empty values", "cred-1", "rev-1", map[string]string{}},
		{"empty key", "cred-1", "rev-1", map[string]string{"": "x"}},
		{"oversized value", "cred-1", "rev-1", map[string]string{"api_key": strings.Repeat("x", 1<<21)}},
	}
	for _, tc := range cases {
		if _, err := svc.Write(tc.ref, tc.revision, tc.values); err != ErrSecretInvalid {
			t.Fatalf("%s: %v, want ErrSecretInvalid", tc.name, err)
		}
	}
}
