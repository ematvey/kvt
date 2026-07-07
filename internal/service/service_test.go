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

func TestWriteIdenticalContentWithSameClockStillCreatesCommit(t *testing.T) {
	testutil.RequireGit(t)
	root := t.TempDir()
	if _, err := Init(t.Context(), InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg, err := config.Load(root, "")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	fixedNow := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	svc, err := New(root, cfg, Deps{
		Now: func() time.Time {
			return fixedNow
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	content := "---\ntype: Note\ntitle: Same\n---\nBody\n"

	first, err := svc.Write(t.Context(), WriteRequest{
		Path:    "notes/same.md",
		Content: content,
		Agent:   "test-agent",
	})
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	second, err := svc.Write(t.Context(), WriteRequest{
		Path:     "notes/same.md",
		Content:  content,
		BaseHash: first.Hash,
		Agent:    "test-agent",
	})
	if err != nil {
		t.Fatalf("second write: %v", err)
	}

	if second.Hash == first.Hash {
		t.Fatalf("hash did not change across successful writes")
	}
	if second.Commit.Hash == first.Commit.Hash {
		t.Fatalf("commit did not advance: %q", second.Commit.Hash)
	}
	if got := gitOutput(t, root, "rev-list", "--count", "HEAD"); got != "3\n" {
		t.Fatalf("commit count = %q", got)
	}
}

func TestWriteIdenticalContentAcrossServiceRestartStillCreatesCommit(t *testing.T) {
	testutil.RequireGit(t)
	root := t.TempDir()
	if _, err := Init(t.Context(), InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg, err := config.Load(root, "")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	fixedNow := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	newFixedService := func() *Service {
		t.Helper()
		svc, err := New(root, cfg, Deps{Now: func() time.Time { return fixedNow }})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return svc
	}
	content := "---\ntype: Note\ntitle: Same\n---\nBody\n"

	first, err := newFixedService().Write(t.Context(), WriteRequest{
		Path:    "notes/same.md",
		Content: content,
		Agent:   "test-agent",
	})
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	second, err := newFixedService().Write(t.Context(), WriteRequest{
		Path:     "notes/same.md",
		Content:  content,
		BaseHash: first.Hash,
		Agent:    "test-agent",
	})
	if err != nil {
		t.Fatalf("second write after restart: %v", err)
	}

	if second.Hash == first.Hash {
		t.Fatalf("hash did not change across restarted service")
	}
	if second.Commit.Hash == first.Commit.Hash {
		t.Fatalf("commit did not advance: %q", second.Commit.Hash)
	}
	if got := gitOutput(t, root, "rev-list", "--count", "HEAD"); got != "3\n" {
		t.Fatalf("commit count = %q", got)
	}
}

func TestWriteRejectsMissingAndWrongTypeRefs(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	writeFile(t, filepath.Join(h.root, "_ontology.yaml"), ""+
		"types:\n"+
		"  Person:\n"+
		"    required: [title]\n"+
		"  System:\n"+
		"    required: [title]\n"+
		"  Incident:\n"+
		"    required: [title, affects]\n"+
		"    fields:\n"+
		"      affects: {ref: System}\n")

	_, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "incidents/missing.md",
		Content: "---\ntype: Incident\ntitle: Missing\naffects: systems/missing.md\n---\nBody\n",
		Agent:   "test-agent",
	})
	if err == nil || !strings.Contains(err.Error(), "missing ref target") {
		t.Fatalf("expected missing ref validation error, got %v", err)
	}

	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "people/alice.md",
		Content: "---\ntype: Person\ntitle: Alice\n---\nBody\n",
		Agent:   "test-agent",
	}); err != nil {
		t.Fatalf("write person: %v", err)
	}
	_, err = h.service.Write(t.Context(), WriteRequest{
		Path:    "incidents/wrong-type.md",
		Content: "---\ntype: Incident\ntitle: Wrong Type\naffects: people/alice.md\n---\nBody\n",
		Agent:   "test-agent",
	})
	if err == nil || !strings.Contains(err.Error(), "must have type") {
		t.Fatalf("expected wrong-type ref validation error, got %v", err)
	}
}

func TestWriteAdvisoryValidationReturnsWarningsAndCommits(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	writeFile(t, filepath.Join(h.root, "_ontology.yaml"), ""+
		"types:\n"+
		"  Person:\n"+
		"    required: [title]\n"+
		"rules:\n"+
		"  - path: people/**\n"+
		"    type: Person\n")

	resp, err := h.service.Write(t.Context(), WriteRequest{
		Path:           "people/alice.md",
		Content:        "---\ntype: Person\n---\nBody\n",
		Agent:          "test-agent",
		ValidationMode: ValidationModeAdvisory,
	})
	if err != nil {
		t.Fatalf("Write advisory: %v", err)
	}
	if len(resp.Warnings) == 0 {
		t.Fatalf("expected advisory warnings")
	}
	if got := gitOutput(t, h.root, "rev-list", "--count", "HEAD"); got != "2\n" {
		t.Fatalf("commit count = %q", got)
	}
}

func TestValidateAdvisoryModeReturnsErrorsAsWarnings(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	writeFile(t, filepath.Join(h.root, "_ontology.yaml"), ""+
		"types:\n"+
		"  Person:\n"+
		"    required: [title]\n")
	writeFile(t, filepath.Join(h.root, "people", "alice.md"), "---\ntype: Person\n---\nBody\n")

	strict, err := h.service.Validate(t.Context(), ValidateRequest{})
	if err != nil {
		t.Fatalf("Validate strict: %v", err)
	}
	if len(strict.Errors) == 0 {
		t.Fatalf("expected strict errors")
	}

	advisory, err := h.service.Validate(t.Context(), ValidateRequest{ValidationMode: ValidationModeAdvisory})
	if err != nil {
		t.Fatalf("Validate advisory: %v", err)
	}
	if len(advisory.Errors) != 0 || len(advisory.Warnings) == 0 {
		t.Fatalf("advisory = %#v", advisory)
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

func TestReadReturnsBacklinksFromIndex(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)

	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "systems/db.md",
		Content: "---\ntype: System\ntitle: DB\ndescription: Primary\n---\n",
		Agent:   "test-agent",
	}); err != nil {
		t.Fatalf("write system: %v", err)
	}
	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "people/alice.md",
		Content: "---\ntype: Person\ntitle: Alice\ndescription: DBA\n---\nSee [DB](../systems/db.md).\n",
		Agent:   "test-agent",
	}); err != nil {
		t.Fatalf("write person: %v", err)
	}

	got, err := h.service.Read(t.Context(), ReadRequest{Path: "systems/db.md"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Backlinks) != 1 || got.Backlinks[0].FromPath != "people/alice.md" {
		t.Fatalf("backlinks = %#v", got.Backlinks)
	}
}

func TestBacklinksSurviveTargetDeleteAndRecreate(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)

	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "systems/db.md",
		Content: "---\ntype: System\ntitle: DB\ndescription: Primary\n---\n",
		Agent:   "test-agent",
	}); err != nil {
		t.Fatalf("write system: %v", err)
	}
	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "people/alice.md",
		Content: "---\ntype: Person\ntitle: Alice\ndescription: DBA\n---\nSee [DB](../systems/db.md).\n",
		Agent:   "test-agent",
	}); err != nil {
		t.Fatalf("write person: %v", err)
	}
	first, err := h.service.Read(t.Context(), ReadRequest{Path: "systems/db.md"})
	if err != nil {
		t.Fatalf("Read before delete: %v", err)
	}
	if len(first.Backlinks) != 1 {
		t.Fatalf("initial backlinks = %#v", first.Backlinks)
	}

	if _, err := h.service.Delete(t.Context(), DeleteRequest{
		Path:  "systems/db.md",
		Agent: "test-agent",
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "systems/db.md",
		Content: "---\ntype: System\ntitle: DB\ndescription: Restored\n---\n",
		Agent:   "test-agent",
	}); err != nil {
		t.Fatalf("rewrite system: %v", err)
	}

	got, err := h.service.Read(t.Context(), ReadRequest{Path: "systems/db.md"})
	if err != nil {
		t.Fatalf("Read after recreate: %v", err)
	}
	if len(got.Backlinks) != 1 || got.Backlinks[0].FromPath != "people/alice.md" {
		t.Fatalf("backlinks after recreate = %#v", got.Backlinks)
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
