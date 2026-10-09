package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"ant/server/internal/domain"
	"ant/server/internal/security"
	"ant/server/internal/store/sqlite"
)

func TestSecretCreateRotateAndOwnership(t *testing.T) {
	clock := &testClock{now: 1_790_000_000_000}
	dbPath := filepath.Join(t.TempDir(), "secrets.db")
	st, err := sqlite.Open(dbPath, sqlite.Config{Clock: clock, CommandBudget: fixtureCommandBudget})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	seed, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(`INSERT INTO principals(principal_id, display_name, enabled, created_at) VALUES
		('principal-1', 'owner', 1, 1), ('other', 'other', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(context.Background()); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	cipher, err := security.NewSecretCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewSecretCommands(st, cipher, clock, "test-key-v1")
	if err != nil {
		t.Fatal(err)
	}
	owner := domain.AuthContext{PrincipalID: "principal-1", Namespace: "ns-demo", Permissions: []domain.Permission{domain.PermConfigure}}
	view, err := service.Create(context.Background(), owner, "create-1", "sk-abcdefghi")
	if err != nil {
		t.Fatal(err)
	}
	if view.Ref == "" || view.Revision == "" || view.Mask != "…fghi" {
		t.Fatalf("secret metadata = %#v", view)
	}
	encoded, _ := json.Marshal(view)
	if string(encoded) == "" || strings.Contains(string(encoded), "abcdefghi") {
		t.Fatalf("metadata leaked secret value: %s", encoded)
	}
	replayed, err := service.Create(context.Background(), owner, "create-1", "sk-abcdefghi")
	if err != nil || replayed != view {
		t.Fatalf("create replay = %#v, %v; want %#v", replayed, err, view)
	}
	if _, err := service.Get(context.Background(), domain.AuthContext{PrincipalID: "other", Namespace: "ns-demo", Permissions: owner.Permissions}, view.Ref); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("cross-principal metadata read = %v, want not found", err)
	}
	otherNamespace := owner
	otherNamespace.Namespace = "ns-other"
	if _, err := service.Get(context.Background(), otherNamespace, view.Ref); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("cross-namespace metadata read = %v, want not found", err)
	}
	clear, err := service.Resolve(context.Background(), view.Ref, view.Revision)
	if err != nil || clear["value"] != "sk-abcdefghi" {
		t.Fatalf("resolve = %#v, %v", clear, err)
	}
	clock.now++
	rotated, err := service.Rotate(context.Background(), owner, view.Ref, "rotate-1", view.Revision, "sk-newsecret123")
	if err != nil || rotated.Ref != view.Ref || rotated.Revision == view.Revision {
		t.Fatalf("rotate = %#v, %v", rotated, err)
	}
	if _, err := service.Rotate(context.Background(), owner, view.Ref, "rotate-stale", view.Revision, "sk-stale-secret"); !errors.Is(err, ErrSecretConflict) {
		t.Fatalf("stale rotation = %v, want conflict", err)
	}
	old, err := service.Resolve(context.Background(), view.Ref, view.Revision)
	if err != nil || old["value"] != "sk-abcdefghi" {
		t.Fatalf("old revision did not remain immutable: %#v, %v", old, err)
	}
	current, err := service.Get(context.Background(), owner, view.Ref)
	if err != nil || current != rotated {
		t.Fatalf("current metadata = %#v, %v; want %#v", current, err, rotated)
	}
}
