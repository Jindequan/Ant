package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecretMasterKeyGenerationAndLoading(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "master.key")
	if err := generateMasterKey(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || info.Size() != 32 {
		t.Fatalf("generated key mode=%#o size=%d, want mode 0600 and 32 bytes", info.Mode().Perm(), info.Size())
	}
	key, err := loadMasterKey(path)
	if err != nil || len(key) != 32 {
		t.Fatalf("load generated key: len=%d err=%v", len(key), err)
	}
	for i := range key {
		key[i] = 0
	}
	if err := generateMasterKey(path); err == nil {
		t.Fatal("generation overwrote an existing key")
	}
}

func TestMasterKeyLoaderRejectsUnsafeFiles(t *testing.T) {
	dir := t.TempDir()
	unsafe := filepath.Join(dir, "unsafe.key")
	if err := os.WriteFile(unsafe, make([]byte, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMasterKey(unsafe); err == nil {
		t.Fatal("loader accepted group/world-readable key")
	}
	wrongSize := filepath.Join(dir, "short.key")
	if err := os.WriteFile(wrongSize, make([]byte, 31), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMasterKey(wrongSize); err == nil {
		t.Fatal("loader accepted a non-32-byte key")
	}
	publicDir := filepath.Join(dir, "public")
	if err := os.Mkdir(publicDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(publicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := generateMasterKey(filepath.Join(publicDir, "key")); err == nil {
		t.Fatal("generator accepted a group/world accessible key directory")
	}
}
