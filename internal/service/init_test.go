package service

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/testutil"
)

func TestInitEmptyVaultCreatesMainCommitAndConfig(t *testing.T) {
	testutil.RequireGit(t)
	root := t.TempDir()

	result, err := Init(t.Context(), InitRequest{VaultPath: root, Defaults: true})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if result.Branch != "main" {
		t.Fatalf("branch = %q", result.Branch)
	}
	if _, err := os.Stat(filepath.Join(root, ".kvt", "config.yaml")); err != nil {
		t.Fatalf("config missing: %v", err)
	}
	if got := gitOutput(t, root, "rev-list", "--count", "HEAD"); got != "1\n" {
		t.Fatalf("commit count = %q", got)
	}
	if got := readFile(t, filepath.Join(root, ".gitignore")); !strings.Contains(got, ".kvt/") {
		t.Fatalf("gitignore = %q", got)
	}
}

func TestInitAdoptsExistingGitRepoWithoutRewritingTrackedContent(t *testing.T) {
	testutil.RequireGit(t)
	root := t.TempDir()
	runGit(t, root, "init", "-b", "trunk")
	runGit(t, root, "config", "user.name", "test")
	runGit(t, root, "config", "user.email", "test@example.com")
	original := "---\ntype: Note\ntitle: Existing\n---\nBody\n"
	writeFile(t, filepath.Join(root, "notes", "existing.md"), original)
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "seed")

	result, err := Init(t.Context(), InitRequest{VaultPath: root, Defaults: true})
	if err != nil {
		t.Fatalf("Init adopt: %v", err)
	}
	if result.Branch != "trunk" {
		t.Fatalf("branch = %q", result.Branch)
	}
	if got := readFile(t, filepath.Join(root, "notes", "existing.md")); got != original {
		t.Fatalf("existing content changed: %q", got)
	}
	if got := gitOutput(t, root, "rev-list", "--count", "HEAD"); got != "2\n" {
		t.Fatalf("commit count = %q", got)
	}
	cfg, err := config.Load(root, "")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	if cfg.Git.Branch != "trunk" {
		t.Fatalf("cfg branch = %q", cfg.Git.Branch)
	}
	if _, err := os.Stat(filepath.Join(root, "_ontology.yaml")); err != nil {
		t.Fatalf("ontology missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "index.md")); err != nil {
		t.Fatalf("index missing: %v", err)
	}
}

func TestInitIsIdempotent(t *testing.T) {
	testutil.RequireGit(t)
	root := t.TempDir()

	if _, err := Init(t.Context(), InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("first init: %v", err)
	}
	if _, err := Init(t.Context(), InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("second init: %v", err)
	}
	if got := gitOutput(t, root, "rev-list", "--count", "HEAD"); got != "1\n" {
		t.Fatalf("commit count = %q", got)
	}
}

func TestAcquireVaultLockIsExclusiveAndWritesMetadata(t *testing.T) {
	root := t.TempDir()

	lock, err := AcquireVaultLock(root)
	if err != nil {
		t.Fatalf("AcquireVaultLock: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".kvt", "lock"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if !strings.Contains(string(data), "\"pid\"") {
		t.Fatalf("lock metadata = %q", string(data))
	}

	if _, err := AcquireVaultLock(root); !errors.Is(err, ErrVaultLocked) {
		t.Fatalf("second lock err = %v", err)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	second, err := AcquireVaultLock(root)
	if err != nil {
		t.Fatalf("reacquire: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
}

func gitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

func newInitializedService(t *testing.T) *Service {
	t.Helper()
	testutil.RequireGit(t)
	root := t.TempDir()
	if _, err := Init(t.Context(), InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	svc, err := New(root, config.Default(), Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
