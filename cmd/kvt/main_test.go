package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ematvey/kvt/internal/service"
)

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	code := run([]string{"kvt", "version"}, &out, &out)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if out.String() == "" {
		t.Fatalf("expected version output")
	}
}

func TestRunInitWithDefaults(t *testing.T) {
	root := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"kvt", "init", "--vault", root, "--defaults"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".kvt", "config.yaml")); err != nil {
		t.Fatalf("config missing: %v", err)
	}
}

func TestRunInitUsesKVTVaultFallback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KVT_VAULT", root)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"kvt", "init", "--defaults"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".kvt", "config.yaml")); err != nil {
		t.Fatalf("config missing: %v", err)
	}
}

func TestRunValidateReturnsNonZeroWhenValidationFails(t *testing.T) {
	root := t.TempDir()
	if _, err := service.Init(t.Context(), service.InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "bad.md"), []byte("Body without type\n"), 0o644); err != nil {
		t.Fatalf("write bad doc: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"kvt", "validate", "--vault", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("notes/bad.md")) {
		t.Fatalf("expected path in validation output: %q", stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("missing required field")) {
		t.Fatalf("expected validation detail in output: %q", stderr.String())
	}
}
