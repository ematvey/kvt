package service

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
