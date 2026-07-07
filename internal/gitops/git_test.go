package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusReportsBranchDetachedAndDirtyState(t *testing.T) {
	root := initRepoWithCommit(t)

	status, err := Status(root, "main")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Branch != "main" {
		t.Fatalf("branch = %q", status.Branch)
	}
	if status.Detached {
		t.Fatalf("expected attached HEAD")
	}
	if status.Dirty {
		t.Fatalf("expected clean worktree")
	}
	if !status.BranchOK {
		t.Fatalf("expected branch to match")
	}

	writeFile(t, filepath.Join(root, "dirty.md"), "dirty\n")
	status, err = Status(root, "main")
	if err != nil {
		t.Fatalf("Status dirty: %v", err)
	}
	if !status.Dirty {
		t.Fatalf("expected dirty worktree")
	}

	runGit(t, root, "checkout", "--detach")
	status, err = Status(root, "main")
	if err != nil {
		t.Fatalf("Status detached: %v", err)
	}
	if !status.Detached {
		t.Fatalf("expected detached HEAD")
	}
	if status.BranchOK {
		t.Fatalf("detached HEAD should not match expected branch")
	}
}

func TestCommitStagesChangesAndSetsAuthor(t *testing.T) {
	root := initRepoWithCommit(t)
	writeFile(t, filepath.Join(root, "a.md"), "---\ntype: Note\ntitle: A\n---\nB\n")

	result, err := Commit(root, CommitOptions{
		Message:     "update note",
		AuthorName:  "kvt",
		AuthorEmail: "kvt@local",
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Hash == "" || result.ShortHash == "" {
		t.Fatalf("result = %#v", result)
	}
	if !result.Changed {
		t.Fatalf("expected commit to report changes")
	}

	got := gitOutput(t, root, "log", "-1", "--format=%an <%ae>%n%s")
	if !strings.Contains(got, "kvt <kvt@local>") {
		t.Fatalf("author output = %q", got)
	}
	if !strings.Contains(got, "update note") {
		t.Fatalf("subject output = %q", got)
	}
}

func TestCommitWithPathsDoesNotAdoptPreStagedUnrelatedFiles(t *testing.T) {
	root := initRepoWithCommit(t)
	writeFile(t, filepath.Join(root, "a.md"), "---\ntype: Note\ntitle: A\n---\nScoped\n")
	writeFile(t, filepath.Join(root, "b.md"), "---\ntype: Note\ntitle: B\n---\nB\n")
	runGit(t, root, "add", "b.md")

	result, err := Commit(root, CommitOptions{
		Message: "update a only",
		Paths:   []string{"a.md"},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !result.Changed {
		t.Fatalf("expected commit to report changes")
	}

	if got := gitOutput(t, root, "show", "--pretty=format:", "--name-only", "HEAD"); got != "a.md\n" {
		t.Fatalf("head files = %q", got)
	}
	if got := gitOutput(t, root, "diff", "--cached", "--name-only"); got != "b.md\n" {
		t.Fatalf("cached diff = %q", got)
	}
}

func TestCommitWithPathsIgnoresUnrelatedCachedChangesWhenTargetsAreUnchanged(t *testing.T) {
	root := initRepoWithCommit(t)
	writeFile(t, filepath.Join(root, "b.md"), "---\ntype: Note\ntitle: B\n---\nB\n")
	runGit(t, root, "add", "b.md")

	headBefore := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	result, err := Commit(root, CommitOptions{
		Message: "should not commit",
		Paths:   []string{"a.md"},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Changed {
		t.Fatalf("expected no path-scoped changes, got %#v", result)
	}

	headAfter := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	if headAfter != headBefore {
		t.Fatalf("head changed from %q to %q", headBefore, headAfter)
	}
	if got := gitOutput(t, root, "diff", "--cached", "--name-only"); got != "b.md\n" {
		t.Fatalf("cached diff = %q", got)
	}
}

func TestCommitWithPathsDoesNotForceAddIgnoredRuntimeState(t *testing.T) {
	root := initRepoWithCommit(t)
	writeFile(t, filepath.Join(root, ".gitignore"), ".kvt/\n")
	runGit(t, root, "add", ".gitignore")
	runGit(t, root, "commit", "-m", "ignore runtime state")
	writeFile(t, filepath.Join(root, ".kvt", "config.yaml"), "runtime: true\n")

	headBefore := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	result, err := Commit(root, CommitOptions{
		Message: "should not force runtime config",
		Paths:   []string{".kvt/config.yaml"},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Changed {
		t.Fatalf("expected ignored runtime config to stay uncommitted, got %#v", result)
	}
	headAfter := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	if headAfter != headBefore {
		t.Fatalf("head changed from %q to %q", headBefore, headAfter)
	}
	if got := gitOutput(t, root, "ls-files", "--cached", "--", ".kvt/config.yaml"); got != "" {
		t.Fatalf("runtime config tracked = %q", got)
	}
}

func TestCommitWithPathsNeverCommitsTrackedRuntimeState(t *testing.T) {
	root := initRepoWithCommit(t)
	writeFile(t, filepath.Join(root, ".kvt", "config.yaml"), "runtime: old\n")
	runGit(t, root, "add", ".kvt/config.yaml")
	runGit(t, root, "commit", "-m", "legacy tracked runtime state")
	writeFile(t, filepath.Join(root, ".gitignore"), ".kvt/\n")
	runGit(t, root, "add", ".gitignore")
	runGit(t, root, "commit", "-m", "ignore runtime state")
	writeFile(t, filepath.Join(root, ".kvt", "config.yaml"), "runtime: new\n")

	headBefore := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	result, err := Commit(root, CommitOptions{
		Message: "should not commit tracked runtime config",
		Paths:   []string{".kvt/config.yaml"},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Changed {
		t.Fatalf("expected tracked runtime config to stay uncommitted, got %#v", result)
	}
	headAfter := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	if headAfter != headBefore {
		t.Fatalf("head changed from %q to %q", headBefore, headAfter)
	}
	if got := gitOutput(t, root, "diff", "--name-only", "HEAD", "--", ".kvt/config.yaml"); got != ".kvt/config.yaml\n" {
		t.Fatalf("runtime config diff = %q", got)
	}
}

func TestCommitWithPathsDoesNotBecomeUnscopedWhenAllPathsIgnored(t *testing.T) {
	root := initRepoWithCommit(t)
	writeFile(t, filepath.Join(root, ".gitignore"), ".kvt/\n")
	runGit(t, root, "add", ".gitignore")
	runGit(t, root, "commit", "-m", "ignore runtime state")
	writeFile(t, filepath.Join(root, "b.md"), "---\ntype: Note\ntitle: B\n---\nB\n")
	runGit(t, root, "add", "b.md")
	writeFile(t, filepath.Join(root, ".kvt", "config.yaml"), "runtime: true\n")

	headBefore := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	result, err := Commit(root, CommitOptions{
		Message: "should not commit ignored runtime config",
		Paths:   []string{".kvt/config.yaml"},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Changed {
		t.Fatalf("expected ignored-only pathspec to produce no commit, got %#v", result)
	}
	headAfter := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	if headAfter != headBefore {
		t.Fatalf("head changed from %q to %q", headBefore, headAfter)
	}
	if got := gitOutput(t, root, "diff", "--cached", "--name-only"); got != "b.md\n" {
		t.Fatalf("cached diff = %q", got)
	}
}

func TestCommitWithPathsFiltersIgnoredRuntimeStateBeforeCommit(t *testing.T) {
	root := initRepoWithCommit(t)
	writeFile(t, filepath.Join(root, ".gitignore"), ".kvt/\n")
	runGit(t, root, "add", ".gitignore")
	runGit(t, root, "commit", "-m", "ignore runtime state")
	writeFile(t, filepath.Join(root, "a.md"), "---\ntype: Note\ntitle: A\n---\nScoped\n")
	writeFile(t, filepath.Join(root, ".kvt", "config.yaml"), "runtime: true\n")

	result, err := Commit(root, CommitOptions{
		Message: "update a only",
		Paths:   []string{"a.md", ".kvt/config.yaml"},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !result.Changed {
		t.Fatalf("expected a.md commit")
	}
	if got := gitOutput(t, root, "show", "--pretty=format:", "--name-only", "HEAD"); got != "a.md\n" {
		t.Fatalf("head files = %q", got)
	}
	if got := gitOutput(t, root, "ls-files", "--cached", "--", ".kvt/config.yaml"); got != "" {
		t.Fatalf("runtime config tracked = %q", got)
	}
}

func TestLogReturnsTerseEntries(t *testing.T) {
	root := initRepoWithCommit(t)

	page, err := Log(root, "", 10)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("entries = %d", len(page.Entries))
	}
	if page.Entries[0].ShortHash == "" || page.Entries[0].Subject == "" {
		t.Fatalf("entry = %#v", page.Entries[0])
	}
	if page.Entries[0].FileSummary == "" {
		t.Fatalf("expected file summary in %#v", page.Entries[0])
	}
}

func TestHistoryReturnsDiffForPath(t *testing.T) {
	root := initRepoWithCommit(t)
	writeFile(t, filepath.Join(root, "a.md"), "---\ntype: Note\ntitle: A\n---\nUpdated\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "update a")

	page, err := History(root, "a.md", "", 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("entries = %d", len(page.Entries))
	}
	if page.Entries[0].Diff == "" {
		t.Fatalf("expected diff in %#v", page.Entries[0])
	}
	if !strings.Contains(page.Entries[0].Diff, "+Updated") {
		t.Fatalf("diff = %q", page.Entries[0].Diff)
	}
}

func TestPushUsesDefaultGitPushSemantics(t *testing.T) {
	root := initRepoWithCommit(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	runGit(t, root, "remote", "add", "origin", remote)

	if err := Push(root, "origin", "main"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	got := strings.TrimSpace(gitOutput(t, remote, "rev-parse", "refs/heads/main"))
	want := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	if got != want {
		t.Fatalf("remote head = %q, want %q", got, want)
	}
}

func initRepoWithCommit(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.name", "test")
	runGit(t, root, "config", "user.email", "test@example.com")
	writeFile(t, filepath.Join(root, "a.md"), "---\ntype: Note\ntitle: A\n---\nA\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "initial")
	return root
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
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

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
