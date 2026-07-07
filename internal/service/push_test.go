package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/testutil"
)

func TestManualPushFastForwardOnly(t *testing.T) {
	svc, remote := newServiceWithBareRemote(t)
	if _, err := svc.Write(t.Context(), WriteRequest{Path: "notes/a.md", Content: "---\ntype: Note\ntitle: A\n---\nA\n"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	res, err := svc.Push(t.Context(), PushRequest{RemoteName: "origin"})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res.PushedCommits == 0 {
		t.Fatalf("expected pushed commits, remote=%s", remote)
	}
	got := strings.TrimSpace(gitOutput(t, remote, "rev-parse", "refs/heads/main"))
	want := strings.TrimSpace(gitOutput(t, svc.root, "rev-parse", "HEAD"))
	if got != want {
		t.Fatalf("remote head = %q, want %q", got, want)
	}
	status := svc.PushStatus(t.Context())
	if status.CommitsAhead != 0 || status.LastPushedAt == "" || status.LastError != "" {
		t.Fatalf("status = %#v", status)
	}
}

func TestOnChangePushModePushesAfterWrite(t *testing.T) {
	cfg := config.Default()
	cfg.Git.Push = "on_change"
	svc, remote := newServiceWithBareRemoteConfig(t, cfg)

	if _, err := svc.Write(t.Context(), WriteRequest{Path: "notes/a.md", Content: "---\ntype: Note\ntitle: A\n---\nA\n"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	want := strings.TrimSpace(gitOutput(t, svc.root, "rev-parse", "HEAD"))
	waitForRemoteHead(t, remote, want)
}

func TestDebouncedPushModePushesAfterInterval(t *testing.T) {
	cfg := config.Default()
	cfg.Git.Push = "debounced"
	cfg.Git.DebounceInterval = "20ms"
	svc, remote := newServiceWithBareRemoteConfig(t, cfg)

	if _, err := svc.Write(t.Context(), WriteRequest{Path: "notes/a.md", Content: "---\ntype: Note\ntitle: A\n---\nA\n"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	want := strings.TrimSpace(gitOutput(t, svc.root, "rev-parse", "HEAD"))
	waitForRemoteHead(t, remote, want)
}

func TestHealthUsesCachedPushStatusDuringSlowPush(t *testing.T) {
	svc, remote := newServiceWithBareRemote(t)
	writeSlowPreReceiveHook(t, remote)
	if _, err := svc.Write(t.Context(), WriteRequest{Path: "notes/a.md", Content: "---\ntype: Note\ntitle: A\n---\nA\n"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	pushErr := make(chan error, 1)
	go func() {
		_, err := svc.Push(context.Background(), PushRequest{RemoteName: "origin"})
		pushErr <- err
	}()
	time.Sleep(150 * time.Millisecond)

	start := time.Now()
	if _, err := svc.Health(t.Context()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Health blocked behind push for %s", elapsed)
	}
	if err := <-pushErr; err != nil {
		t.Fatalf("Push: %v", err)
	}
}

func newServiceWithBareRemote(t *testing.T) (*Service, string) {
	t.Helper()
	cfg := config.Default()
	cfg.Git.Push = "off"
	return newServiceWithBareRemoteConfig(t, cfg)
}

func newServiceWithBareRemoteConfig(t *testing.T, cfg config.Config) (*Service, string) {
	t.Helper()
	testutil.RequireGit(t)
	root := t.TempDir()
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", remote)
	if _, err := Init(t.Context(), InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	runGit(t, root, "remote", "add", "origin", remote)
	svc, err := New(root, cfg, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, remote
}

func waitForRemoteHead(t *testing.T, remote string, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got, ok := remoteHead(t, remote); ok && got == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	got, _ := remoteHead(t, remote)
	t.Fatalf("remote head = %q, want %q", got, want)
}

func remoteHead(t *testing.T, remote string) (string, bool) {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "refs/heads/main")
	cmd.Dir = remote
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func writeSlowPreReceiveHook(t *testing.T, remote string) {
	t.Helper()
	hook := filepath.Join(remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 1\n"), 0o755); err != nil {
		t.Fatalf("write pre-receive hook: %v", err)
	}
}
