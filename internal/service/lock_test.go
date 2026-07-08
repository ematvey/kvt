package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLockRecoversFromStaleLock(t *testing.T) {
	root := t.TempDir()
	lockDir := filepath.Join(root, ".kvt")
	os.MkdirAll(lockDir, 0o755)
	lockPath := filepath.Join(lockDir, "lock")
	os.WriteFile(lockPath, []byte(`{"pid": 999999, "created_at": "2020-01-01T00:00:00Z"}`), 0o644)

	lock, err := AcquireVaultLock(root)
	if err != nil {
		t.Fatalf("expected stale lock to be recovered, got %v", err)
	}
	if lock == nil {
		t.Fatalf("expected non-nil lock")
	}
	// Verify the old lock file was replaced
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("lock file should exist: %v", err)
	}
	if strings.Contains(string(data), "999999") {
		t.Fatalf("lock file still references old pid")
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestLockNormalOperation(t *testing.T) {
	root := t.TempDir()
	lock, err := AcquireVaultLock(root)
	if err != nil {
		t.Fatalf("AcquireVaultLock: %v", err)
	}
	if lock == nil {
		t.Fatalf("expected non-nil lock")
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestLockDoubleAcquireRejected(t *testing.T) {
	root := t.TempDir()
	lock1, err := AcquireVaultLock(root)
	if err != nil {
		t.Fatalf("first AcquireVaultLock: %v", err)
	}
	defer lock1.Release()

	_, err = AcquireVaultLock(root)
	if err == nil {
		t.Fatalf("expected error for second lock")
	}
	if !errors.Is(err, ErrVaultLocked) {
		t.Fatalf("expected ErrVaultLocked, got %v", err)
	}
}
