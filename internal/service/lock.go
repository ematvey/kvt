package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var ErrVaultLocked = errors.New("vault is locked")

type Lock struct {
	path string
}

type lockMetadata struct {
	PID       int       `json:"pid"`
	CreatedAt time.Time `json:"created_at"`
}

func AcquireVaultLock(root string) (*Lock, error) {
	lockDir := filepath.Join(root, ".kvt")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(lockDir, "lock")

	lock, err := tryAcquire(lockPath)
	if err == nil {
		return lock, nil
	}
	if !errors.Is(err, ErrVaultLocked) {
		return nil, err
	}

	if recovered := recoverStaleLock(lockPath); recovered {
		lock, err := tryAcquire(lockPath)
		if err != nil {
			return nil, err
		}
		return lock, nil
	}

	meta, err := readStaleLockMetadata(lockPath)
	if err == nil && meta.PID > 0 {
		return nil, fmt.Errorf("%w: pid=%d since=%s; remove %s to force", ErrVaultLocked, meta.PID, meta.CreatedAt.UTC().Format(time.RFC3339), lockPath)
	}
	return nil, ErrVaultLocked
}

func tryAcquire(lockPath string) (*Lock, error) {
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrVaultLocked
		}
		return nil, err
	}
	defer file.Close()

	data, err := json.MarshalIndent(lockMetadata{
		PID:       os.Getpid(),
		CreatedAt: time.Now().UTC(),
	}, "", "  ")
	if err != nil {
		_ = os.Remove(lockPath)
		return nil, err
	}
	data = append(data, '\n')
	if _, err := file.Write(data); err != nil {
		_ = os.Remove(lockPath)
		return nil, err
	}
	return &Lock{path: lockPath}, nil
}

func recoverStaleLock(lockPath string) bool {
	meta, err := readStaleLockMetadata(lockPath)
	if err != nil {
		// Can't parse lock metadata — treat as corrupt, remove it
		_ = os.Remove(lockPath)
		return true
	}
	if meta.PID <= 0 {
		return false
	}
	if err := syscall.Kill(meta.PID, 0); err != nil {
		_ = os.Remove(lockPath)
		return true
	}
	// Process is still alive — lock is valid
	return false
}

func readStaleLockMetadata(lockPath string) (lockMetadata, error) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return lockMetadata{}, err
	}
	var meta lockMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return lockMetadata{}, err
	}
	return meta, nil
}

func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	l.path = ""
	return nil
}
