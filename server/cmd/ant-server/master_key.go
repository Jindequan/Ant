package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func loadMasterKey(path string) ([]byte, error) {
	if err := requirePrivateKeyDirectory(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("master key must be a regular file with mode 0600")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm() != 0o600 {
		return nil, errors.New("master key file changed while opening")
	}
	key, err := io.ReadAll(io.LimitReader(file, 33))
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		for i := range key {
			key[i] = 0
		}
		return nil, errors.New("master key must contain exactly 32 raw bytes")
	}
	return key, nil
}

func generateMasterKey(path string) error {
	if err := requirePrivateKeyDirectory(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create master key file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("set master key permissions: %w", err)
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return fmt.Errorf("generate master key: %w", err)
	}
	if _, err := file.Write(key[:]); err != nil {
		return fmt.Errorf("write master key: %w", err)
	}
	for i := range key {
		key[i] = 0
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync master key: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close master key: %w", err)
	}
	ok = true
	return nil
}

func requirePrivateKeyDirectory(path string) error {
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("inspect master key directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("master key directory must be a directory with no group or world permissions")
	}
	return nil
}
