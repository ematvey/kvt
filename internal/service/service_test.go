package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/testutil"
)

func TestWriteCommitsTimestampAndRejectsStaleBaseHash(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)

	first, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "people/alice.md",
		Content: "---\ntype: Person\ntitle: Alice\ndescription: DBA\ntimestamp: stale\n---\nBody\n",
		Agent:   "test-agent",
	})
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if first.Path != "people/alice.md" {
		t.Fatalf("path = %q", first.Path)
	}
	if strings.Contains(first.Content, "timestamp: stale") {
		t.Fatalf("expected service timestamp overwrite:\n%s", first.Content)
	}
	if !strings.Contains(first.Content, "timestamp: \"2026-07-07T12:00:00Z\"") {
		t.Fatalf("missing authoritative timestamp:\n%s", first.Content)
	}

	second, err := h.service.Write(t.Context(), WriteRequest{
		Path:     "people/alice.md",
		Content:  "---\ntype: Person\ntitle: Alice\ndescription: Lead DBA\n---\nBody\n",
		BaseHash: first.Hash,
		Agent:    "test-agent",
	})
	if err != nil {
		t.Fatalf("second write: %v", err)
	}

	_, err = h.service.Write(t.Context(), WriteRequest{
		Path:     "people/alice.md",
		Content:  "---\ntype: Person\ntitle: Alice\ndescription: Old\n---\nBody\n",
		BaseHash: first.Hash,
		Agent:    "test-agent",
	})
	if !IsConflict(err) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if first.Hash == second.Hash {
		t.Fatalf("hash should change")
	}
	if got := gitOutput(t, h.root, "rev-list", "--count", "HEAD"); got != "3\n" {
		t.Fatalf("commit count = %q", got)
	}
}

func TestEditRequiresUniqueOldString(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)

	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "notes/repeat.md",
		Content: "---\ntype: Note\ntitle: Repeat\n---\nhello hello\n",
		Agent:   "test-agent",
	}); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	_, err := h.service.Edit(t.Context(), EditRequest{
		Path:      "notes/repeat.md",
		OldString: "hello",
		NewString: "hi",
		Agent:     "test-agent",
	})
	if !IsAmbiguousEdit(err) {
		t.Fatalf("expected ambiguous edit, got %v", err)
	}
}

func TestDeleteRemovesConceptRegeneratesIndexesAndCommits(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)

	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "people/alice.md",
		Content: "---\ntype: Person\ntitle: Alice\ndescription: DBA\n---\nBody\n",
		Agent:   "test-agent",
	}); err != nil {
		t.Fatalf("write alice: %v", err)
	}
	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "people/bob.md",
		Content: "---\ntype: Person\ntitle: Bob\ndescription: SRE\n---\nBody\n",
		Agent:   "test-agent",
	}); err != nil {
		t.Fatalf("write bob: %v", err)
	}

	resp, err := h.service.Delete(t.Context(), DeleteRequest{
		Path:  "people/alice.md",
		Agent: "test-agent",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if resp.Path != "people/alice.md" {
		t.Fatalf("path = %q", resp.Path)
	}
	if _, err := os.Stat(filepath.Join(h.root, "people", "alice.md")); !os.IsNotExist(err) {
		t.Fatalf("expected deleted file, stat err = %v", err)
	}
	peopleIndex := readFile(t, filepath.Join(h.root, "people", "index.md"))
	if strings.Contains(peopleIndex, "[Alice](alice.md)") {
		t.Fatalf("people index still mentions deleted concept:\n%s", peopleIndex)
	}
	if !strings.Contains(peopleIndex, "[Bob](bob.md) - SRE") {
		t.Fatalf("people index missing remaining concept:\n%s", peopleIndex)
	}
	if got := gitOutput(t, h.root, "show", "--pretty=format:", "--name-only", "HEAD"); got != "people/alice.md\npeople/index.md\n" {
		t.Fatalf("head files = %q", got)
	}
}

type serviceHarness struct {
	root    string
	service *Service
}

func newServiceHarness(t *testing.T) serviceHarness {
	t.Helper()
	root := t.TempDir()
	if _, err := Init(t.Context(), InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg, err := config.Load(root, "")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	nowValues := []time.Time{
		time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 7, 12, 0, 1, 0, time.UTC),
		time.Date(2026, 7, 7, 12, 0, 2, 0, time.UTC),
		time.Date(2026, 7, 7, 12, 0, 3, 0, time.UTC),
	}
	index := 0
	svc, err := New(root, cfg, Deps{
		Now: func() time.Time {
			if index >= len(nowValues) {
				return nowValues[len(nowValues)-1]
			}
			now := nowValues[index]
			index++
			return now
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return serviceHarness{root: root, service: svc}
}
