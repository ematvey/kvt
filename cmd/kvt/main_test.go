package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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
