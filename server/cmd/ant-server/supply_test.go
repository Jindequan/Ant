package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ant/server/internal/quota"
	"ant/server/internal/runtime"
	"ant/server/internal/store/sqlite"
)

// The vault needs no setup: the first start creates a private key file, a
// restart reuses it, and a plaintext accounts.json is moved into the
// database with the account still readable.
func TestLocalVaultMovesPlaintextKeysAndSurvivesRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ant")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	accountsPath := filepath.Join(dir, "accounts.json")
	if err := os.WriteFile(accountsPath, []byte(`{"version":1,"accounts":[{"id":"deepseek-1","provider":"deepseek","mode":"api","origin":"added","values":{"apiKey":"sk-plain"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "master.key")
	clock := runtime.NewClock()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "ant.db"), sqlite.Config{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close(context.Background()) }()
	if err := sqlite.Migrate(context.Background(), st); err != nil {
		t.Fatal(err)
	}

	vault, err := newLocalVault(st, clock, keyPath, "local")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(keyPath); err != nil || info.Mode().Perm() != 0o600 || info.Size() != 32 {
		t.Fatalf("key file: %v %v", info, err)
	}
	if err := quota.NewStore(accountsPath).UseVault(vault); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(accountsPath)
	if strings.Contains(string(b), "sk-plain") || !strings.Contains(string(b), "secretRef") {
		t.Fatalf("accounts.json still holds the key:\n%s", b)
	}

	again, err := newLocalVault(st, clock, keyPath, "local") // a restart
	if err != nil {
		t.Fatal(err)
	}
	accounts := quota.NewStore(accountsPath)
	if err := accounts.UseVault(again); err != nil {
		t.Fatal(err)
	}
	d, err := accounts.Data()
	if err != nil || d.Accounts[0].Values["apiKey"] != "sk-plain" {
		t.Fatalf("after restart: %+v %v", d.Accounts, err)
	}
	c := d.Accounts[0]
	c.Values = map[string]string{"apiKey": "sk-rotated"}
	if err := accounts.Put(c); err != nil {
		t.Fatal(err)
	}
	if d, err := accounts.Data(); err != nil || d.Accounts[0].Values["apiKey"] != "sk-rotated" {
		t.Fatalf("after edit: %+v %v", d.Accounts, err)
	}

	other, err := newLocalVault(st, clock, keyPath, "elsewhere") // another namespace can't open them
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Open(d.Accounts[0].SecretRef, d.Accounts[0].SecretRevision); err == nil {
		t.Fatal("a secret opened from another namespace")
	}
}
