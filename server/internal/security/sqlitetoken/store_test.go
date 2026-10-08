package sqlitetoken

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/runtime"
	"ant/server/internal/security"
	"ant/server/internal/store/sqlite"
)

func openAuthStore(t *testing.T) (*sqlite.Store, *Store) {
	t.Helper()
	clock := runtime.NewClock()
	path := filepath.Join(t.TempDir(), "auth.db")
	st, err := sqlite.Open(path, sqlite.Config{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		_ = st.Close(context.Background())
		t.Fatal(err)
	}
	tokens, err := New(st, clock)
	if err != nil {
		_ = st.Close(context.Background())
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(context.Background()); err != nil {
			t.Errorf("close auth store: %v", err)
		}
	})
	return st.(*sqlite.Store), tokens
}

func TestBootstrapPersistsOnlyHashAndRevokeIsDurable(t *testing.T) {
	_, tokens := openAuthStore(t)
	plain, err := security.BootstrapAdmin(tokens, "admin", "tenant-a", []domain.Permission{domain.PermConfigure, domain.PermRead})
	if err != nil {
		t.Fatal(err)
	}
	provider := security.NewAuthProvider(tokens)
	principal, err := provider.Authenticate(plain)
	if err != nil || principal.PrincipalID != "admin" || principal.Namespace != "tenant-a" || len(principal.Permissions) != 2 {
		t.Fatalf("persisted bootstrap did not authenticate: principal=%+v err=%v", principal, err)
	}
	if err := provider.RevokeToken(plain); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Authenticate(plain); err == nil {
		t.Fatal("revoked token authenticated")
	}
	if _, found, err := tokens.Lookup(security.HashToken(plain)); err != nil || found {
		t.Fatalf("revoked session remained readable: found=%v err=%v", found, err)
	}
}

func TestBootstrapIsExactlyOnceAndPermissionSetIsNotWidened(t *testing.T) {
	_, tokens := openAuthStore(t)
	if _, err := security.BootstrapAdmin(tokens, "admin", "tenant-a", []domain.Permission{domain.PermRead}); err != nil {
		t.Fatal(err)
	}
	if _, err := security.BootstrapAdmin(tokens, "admin-2", "tenant-a", []domain.Permission{domain.PermConfigure}); err == nil {
		t.Fatal("second bootstrap must fail")
	}
	if err := tokens.Put(security.HashToken("narrower-session"), security.TokenRecord{
		PrincipalID: "admin", Namespace: "tenant-a", Permissions: []domain.Permission{domain.PermConfigure}, Kind: security.TokenKindSession,
		ExpiresAt: domain.Millis(1<<63 - 1), CSRFHash: "csrf-hash",
	}); err != ErrGrantMismatch {
		t.Fatalf("token with widened grants must fail, got %v", err)
	}
	if principal, err := security.NewAuthProvider(tokens).Authenticate("narrower-session"); err == nil {
		t.Fatalf("failed token issuance created an authenticating token: %+v", principal)
	}
}

func TestConcurrentBootstrapExchangeCreatesOneSession(t *testing.T) {
	_, tokens := openAuthStore(t)
	bootstrap, err := security.BootstrapAdmin(tokens, "admin", "tenant-a", []domain.Permission{domain.PermRead})
	if err != nil {
		t.Fatal(err)
	}
	bootstrapHash := security.HashToken(bootstrap)
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			csrf := security.HashToken(string(rune('a' + i)))
			csrfText := hashText(csrf)
			err := tokens.ExchangeBootstrap(bootstrapHash, security.HashToken(string(rune('x'+i))), security.TokenRecord{
				PrincipalID: "admin", Namespace: "tenant-a", Permissions: []domain.Permission{domain.PermRead},
				Kind: security.TokenKindSession, ExpiresAt: domain.Millis(1<<63 - 1), CSRFHash: csrfText,
			})
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successful exchanges=%d, want exactly one", successes)
	}
	if _, found, err := tokens.Lookup(bootstrapHash); err != nil || found {
		t.Fatalf("bootstrap token survived exchange: found=%v err=%v", found, err)
	}
}
