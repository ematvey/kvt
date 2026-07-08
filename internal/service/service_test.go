package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/ematvey/kvt/internal/access"
	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/index"
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

func TestWriteDoesNotEnqueueEmbeddingWhenCommitFails(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	h.service.embedQueue = make(chan embeddingJob, 1)
	forceVectorAvailableForTest(t, h.service.index)

	lockPath := filepath.Join(h.root, ".git", "index.lock")
	if err := os.WriteFile(lockPath, []byte("locked"), 0o644); err != nil {
		t.Fatalf("write git lock: %v", err)
	}
	defer os.Remove(lockPath)

	_, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "notes/uncommitted.md",
		Content: "---\ntype: Note\ntitle: Uncommitted\n---\nBody\n",
		Agent:   "test-agent",
	})
	if err == nil {
		t.Fatalf("expected commit failure")
	}
	select {
	case job := <-h.service.embedQueue:
		t.Fatalf("queued embedding for failed commit: %#v", job)
	default:
	}
	pending, err := h.service.index.PendingEmbeddingDocuments(t.Context(), true)
	if err != nil {
		t.Fatalf("PendingEmbeddingDocuments: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending embeddings after failed commit = %#v", pending)
	}
}

func TestReconcileQueuesAppliedDocumentsForEmbedding(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	h.service.embedQueue = make(chan embeddingJob, 1)
	writeFile(t, filepath.Join(h.root, "notes", "external.md"), "---\ntype: Note\ntitle: External\n---\nBody\n")

	result, err := h.service.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Applied != 1 {
		t.Fatalf("applied = %d", result.Applied)
	}
	select {
	case job := <-h.service.embedQueue:
		if job.path != "notes/external.md" || len(job.chunks) == 0 {
			t.Fatalf("job = %#v", job)
		}
	default:
		t.Fatalf("expected embedding job for reconciled document")
	}
}

func TestReconcileUsesBlockingEmbeddingEnqueue(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	h.service.embedQueue = make(chan embeddingJob, 1)
	writeFile(t, filepath.Join(h.root, "notes", "a.md"), "---\ntype: Note\ntitle: A\n---\nA\n")
	writeFile(t, filepath.Join(h.root, "notes", "b.md"), "---\ntype: Note\ntitle: B\n---\nB\n")
	errCh := make(chan error, 1)
	go func() {
		_, err := h.service.Reconcile(t.Context())
		errCh <- err
	}()

	select {
	case err := <-errCh:
		t.Fatalf("reconcile completed before queue drained: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	first := <-h.service.embedQueue
	if first.path != "notes/a.md" {
		t.Fatalf("first job = %#v", first)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("reconcile did not complete")
	}
	second := <-h.service.embedQueue
	if second.path != "notes/b.md" {
		t.Fatalf("second job = %#v", second)
	}
}

func TestServiceStartupQueuesPendingEmbeddings(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	writeFile(t, filepath.Join(h.root, "notes", "pending.md"), "---\ntype: Note\ntitle: Pending\n---\nBody\n")
	result, err := h.service.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(result.AppliedDocuments) != 1 {
		t.Fatalf("applied documents = %#v", result.AppliedDocuments)
	}
	if err := h.service.index.MarkEmbeddingState(t.Context(), "notes/pending.md", "pending", "", result.AppliedDocuments[0].Timestamp, result.AppliedDocuments[0].Hash); err != nil {
		t.Fatalf("MarkEmbeddingState: %v", err)
	}

	restarted, err := New(h.root, h.service.cfg, Deps{
		Now:      h.service.now,
		Embedder: waitingEmbedder{started: make(chan struct{})},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	restarted.embedQueue = make(chan embeddingJob, 1)
	if err := restarted.enqueuePendingEmbeddings(t.Context()); err != nil {
		t.Fatalf("enqueuePendingEmbeddings: %v", err)
	}

	select {
	case job := <-restarted.embedQueue:
		if job.path != "notes/pending.md" || len(job.chunks) == 0 {
			t.Fatalf("job = %#v", job)
		}
	default:
		t.Fatalf("expected pending embedding job")
	}
}

func TestEnqueuePendingEmbeddingsDoesNotDropBacklogBeyondQueueCapacity(t *testing.T) {
	h := newServiceHarness(t)
	h.service.embedQueue = make(chan embeddingJob, 1)
	docs := []index.EmbeddingJobDocument{
		{Path: "a.md", Timestamp: "t1", Chunks: []index.Chunk{{Ordinal: 0, Text: "a"}}},
		{Path: "b.md", Timestamp: "t2", Chunks: []index.Chunk{{Ordinal: 0, Text: "b"}}},
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- h.service.enqueueEmbeddingDocuments(t.Context(), docs)
	}()

	select {
	case err := <-errCh:
		t.Fatalf("enqueue completed before worker drained queue: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	first := <-h.service.embedQueue
	if first.path != "a.md" {
		t.Fatalf("first job = %#v", first)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("enqueueEmbeddingDocuments: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("enqueue did not complete")
	}
	second := <-h.service.embedQueue
	if second.path != "b.md" {
		t.Fatalf("second job = %#v", second)
	}
}

func TestRunEmbeddingWorkerFailsEmptyChunkJobs(t *testing.T) {
	db := openTempDBForServiceTest(t)
	svc := &Service{
		index:      db,
		embedder:   stubServiceEmbedder{vectors: [][]float32{}},
		embedQueue: make(chan embeddingJob, 1),
	}
	if err := db.ApplyDocument(t.Context(), index.IndexedDocument{
		Path:      "notes/empty.md",
		Hash:      "h1",
		Title:     "Empty",
		Type:      "Note",
		Timestamp: "2026-07-07T12:00:00Z",
		Chunks: []index.Chunk{
			{Ordinal: 0, Text: ""},
		},
	}); err != nil {
		t.Fatalf("ApplyDocument: %v", err)
	}

	svc.embedQueue <- embeddingJob{
		path:      "notes/empty.md",
		timestamp: "2026-07-07T12:00:00Z",
		chunks:    []index.Chunk{{Ordinal: 0, Text: ""}},
	}
	close(svc.embedQueue)
	svc.runEmbeddingWorker()

	summary, err := db.Summary(t.Context(), index.SummaryRequest{})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.EmbeddingFailedCount != 1 {
		t.Fatalf("failed count = %d", summary.EmbeddingFailedCount)
	}
}

func TestRunEmbeddingWorkerFailsPartiallyEmbeddableJobs(t *testing.T) {
	db := openTempDBForServiceTest(t)
	svc := &Service{
		index:      db,
		embedder:   stubServiceEmbedder{vectors: [][]float32{{1, 0}}},
		embedQueue: make(chan embeddingJob, 1),
	}
	if err := db.ApplyDocument(t.Context(), index.IndexedDocument{
		Path:      "notes/mixed.md",
		Hash:      "h1",
		Title:     "Mixed",
		Type:      "Note",
		Timestamp: "2026-07-07T12:00:00Z",
		Chunks: []index.Chunk{
			{Ordinal: 0, Text: "body"},
			{Ordinal: 1, Text: ""},
		},
	}); err != nil {
		t.Fatalf("ApplyDocument: %v", err)
	}

	svc.embedQueue <- embeddingJob{
		path:      "notes/mixed.md",
		timestamp: "2026-07-07T12:00:00Z",
		chunks: []index.Chunk{
			{Ordinal: 0, Text: "body"},
			{Ordinal: 1, Text: ""},
		},
	}
	close(svc.embedQueue)
	svc.runEmbeddingWorker()

	summary, err := db.Summary(t.Context(), index.SummaryRequest{})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.EmbeddingFailedCount != 1 {
		t.Fatalf("failed count = %d", summary.EmbeddingFailedCount)
	}
}

func TestEmbedWithRetriesRetriesTransientFailures(t *testing.T) {
	embedder := &flakyServiceEmbedder{
		failures: 2,
		vector:   []float32{1, 0},
	}
	svc := &Service{
		embedder:              embedder,
		embeddingMaxAttempts:  3,
		embeddingBackoffDelay: func(int) time.Duration { return 0 },
	}

	vectors, err := svc.embedWithRetries(t.Context(), []string{"alpha"})
	if err != nil {
		t.Fatalf("embedWithRetries: %v", err)
	}
	if embedder.calls != 3 {
		t.Fatalf("calls = %d", embedder.calls)
	}
	if len(vectors) != 1 || len(vectors[0]) != 2 {
		t.Fatalf("vectors = %#v", vectors)
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

func TestReadSupportsLineRangesAndValidationWarnings(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "notes/a.md",
		Content: "---\ntype: Note\ntitle: A\n---\nline one\nline two\nline three\n",
		Agent:   "test-agent",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	full, err := h.service.Read(t.Context(), ReadRequest{Path: "notes/a.md"})
	if err != nil {
		t.Fatalf("Read full: %v", err)
	}
	lines := strings.Split(full.Content, "\n")
	lineTwo := 0
	for i, line := range lines {
		if line == "line two" {
			lineTwo = i + 1
			break
		}
	}
	if lineTwo == 0 {
		t.Fatalf("line two missing from %q", full.Content)
	}

	got, err := h.service.Read(t.Context(), ReadRequest{Path: "notes/a.md", StartLine: lineTwo, EndLine: lineTwo})
	if err != nil {
		t.Fatalf("Read range: %v", err)
	}
	if strings.TrimSpace(got.Content) != "line two" {
		t.Fatalf("content = %q", got.Content)
	}
	if len(got.Warnings) == 0 || !strings.Contains(got.Warnings[0].Message, "unknown type") {
		t.Fatalf("warnings = %#v", got.Warnings)
	}
	empty, err := h.service.Read(t.Context(), ReadRequest{Path: "notes/a.md", StartLine: 999})
	if err != nil {
		t.Fatalf("Read range past EOF: %v", err)
	}
	if empty.Content != "" {
		t.Fatalf("past EOF content = %q", empty.Content)
	}
}

func TestTypesIncludeDocumentCounts(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	writeFile(t, filepath.Join(h.root, "_ontology.yaml"), "types:\n  System:\n    required: [title]\nrules: []\nunknown_types: warn\n")
	for _, path := range []string{"systems/db.md", "systems/cache.md"} {
		if _, err := h.service.Write(t.Context(), WriteRequest{
			Path:    path,
			Content: "---\ntype: System\ntitle: S\ndescription: service\n---\nBody\n",
			Agent:   "test-agent",
		}); err != nil {
			t.Fatalf("Write %s: %v", path, err)
		}
	}

	types, err := h.service.Types(t.Context())
	if err != nil {
		t.Fatalf("Types: %v", err)
	}
	for _, typ := range types.Types {
		if typ.Name == "System" {
			if typ.Count != 2 {
				t.Fatalf("System count = %d", typ.Count)
			}
			return
		}
	}
	t.Fatalf("System type missing in %#v", types.Types)
}

func TestHouseHowtoIsNotTreatedAsConcept(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	writeFile(t, filepath.Join(h.root, "_howto.md"), "House rule: link incidents to systems.\n")
	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "notes/a.md",
		Content: "---\ntype: Note\ntitle: A\n---\nA\n",
		Agent:   "test-agent",
	}); err != nil {
		t.Fatalf("Write with _howto.md present: %v", err)
	}
	if _, err := h.service.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile with _howto.md present: %v", err)
	}
	validate, err := h.service.Validate(t.Context(), ValidateRequest{})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(validate.Errors) != 0 {
		t.Fatalf("validation errors = %#v", validate.Errors)
	}
	rootIndex := readFile(t, filepath.Join(h.root, "index.md"))
	if strings.Contains(rootIndex, "_howto.md") {
		t.Fatalf("root index includes _howto.md:\n%s", rootIndex)
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

func TestAccessPolicyRestrictsReadAndMutations(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	writePolicy, err := access.New(nil, []string{"drafts/**"}, nil)
	if err != nil {
		t.Fatalf("access.New write: %v", err)
	}
	readPolicy, err := access.New([]string{"drafts/**"}, nil, nil)
	if err != nil {
		t.Fatalf("access.New read: %v", err)
	}

	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "notes/a.md",
		Content: "---\ntype: Note\ntitle: A\n---\nA\n",
		Access:  writePolicy,
	}); !access.IsDenied(err) {
		t.Fatalf("write outside policy err = %v", err)
	}
	written, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "drafts/a.md",
		Content: "---\ntype: Note\ntitle: A\n---\nA\n",
		Access:  writePolicy,
	})
	if err != nil {
		t.Fatalf("write allowed: %v", err)
	}
	if _, err := h.service.Read(t.Context(), ReadRequest{Path: written.Path, Access: readPolicy}); err != nil {
		t.Fatalf("read allowed: %v", err)
	}
	deniedRead, err := access.New([]string{"notes/**"}, nil, nil)
	if err != nil {
		t.Fatalf("access.New denied read: %v", err)
	}
	if _, err := h.service.Read(t.Context(), ReadRequest{Path: written.Path, Access: deniedRead}); !access.IsDenied(err) {
		t.Fatalf("read outside policy err = %v", err)
	}
	deniedWrite, err := access.New(nil, []string{"notes/**"}, nil)
	if err != nil {
		t.Fatalf("access.New denied write: %v", err)
	}
	if _, err := h.service.Edit(t.Context(), EditRequest{
		Path:      written.Path,
		OldString: "A",
		NewString: "B",
		Access:    deniedWrite,
	}); !access.IsDenied(err) {
		t.Fatalf("edit outside policy err = %v", err)
	}
	if _, err := h.service.Delete(t.Context(), DeleteRequest{
		Path:   written.Path,
		Access: deniedWrite,
	}); !access.IsDenied(err) {
		t.Fatalf("delete outside policy err = %v", err)
	}
}

func TestAccessPolicyFiltersDiscoveryToolsAndHistory(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	for _, item := range []struct {
		path  string
		title string
		body  string
	}{
		{"public/a.md", "Public", "shared needle\n"},
		{"private/a.md", "Private", "shared needle [Public](../public/a.md)\n"},
	} {
		if _, err := h.service.Write(t.Context(), WriteRequest{
			Path:    item.path,
			Content: "---\ntype: Note\ntitle: " + item.title + "\n---\n" + item.body,
		}); err != nil {
			t.Fatalf("Write %s: %v", item.path, err)
		}
	}
	policy, err := access.New([]string{"public/**"}, nil, nil)
	if err != nil {
		t.Fatalf("access.New: %v", err)
	}
	list, err := h.service.List(t.Context(), ListRequest{Access: policy})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Documents) != 1 || list.Documents[0].Path != "public/a.md" {
		t.Fatalf("list = %#v", list.Documents)
	}
	grep, err := h.service.Grep(t.Context(), GrepRequest{Query: "needle", Access: policy})
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if len(grep.Matches) != 1 || grep.Matches[0].Path != "public/a.md" {
		t.Fatalf("grep = %#v", grep.Matches)
	}
	search, err := h.service.Search(t.Context(), SearchRequest{Query: "needle", Access: policy})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(search.Hits) != 1 || search.Hits[0].Path != "public/a.md" {
		t.Fatalf("search = %#v", search.Hits)
	}
	read, err := h.service.Read(t.Context(), ReadRequest{Path: "public/a.md", Access: policy})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(read.Backlinks) != 0 {
		t.Fatalf("restricted read leaked backlinks = %#v", read.Backlinks)
	}
	if _, err := h.service.History(t.Context(), HistoryRequest{Path: "private/a.md", Access: policy}); !access.IsDenied(err) {
		t.Fatalf("history outside policy err = %v", err)
	}
	if _, err := h.service.Log(t.Context(), LogRequest{Access: policy}); !access.IsDenied(err) {
		t.Fatalf("restricted log err = %v", err)
	}
	unrestricted, err := access.New([]string{"**"}, nil, nil)
	if err != nil {
		t.Fatalf("access.New unrestricted: %v", err)
	}
	if _, err := h.service.Log(t.Context(), LogRequest{Access: unrestricted, Limit: 1}); err != nil {
		t.Fatalf("unrestricted log: %v", err)
	}
}

type serviceHarness struct {
	root    string
	service *Service
}

type flakyServiceEmbedder struct {
	failures int
	calls    int
	vector   []float32
}

func (f *flakyServiceEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls++
	if f.calls <= f.failures {
		return nil, errors.New("temporary embedder failure")
	}
	vectors := make([][]float32, 0, len(texts))
	for range texts {
		vectors = append(vectors, append([]float32(nil), f.vector...))
	}
	return vectors, nil
}

type stubServiceEmbedder struct {
	vectors [][]float32
	err     error
}

func (s stubServiceEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.vectors, nil
}

type waitingEmbedder struct {
	started chan struct{}
}

func (w waitingEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	if w.started != nil {
		close(w.started)
	}
	select {}
}

func forceVectorAvailableForTest(t *testing.T, db *index.DB) {
	t.Helper()
	value := reflect.ValueOf(db).Elem()
	vecField := value.FieldByName("vecAvailable")
	reflect.NewAt(vecField.Type(), unsafe.Pointer(vecField.UnsafeAddr())).Elem().SetBool(true)

	sqlField := value.FieldByName("sql")
	sqlDB := reflect.NewAt(sqlField.Type(), unsafe.Pointer(sqlField.UnsafeAddr())).Elem().Interface().(*sql.DB)
	if _, err := sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS kb_vec (
			chunk_id TEXT PRIMARY KEY,
			path TEXT NOT NULL,
			ordinal INTEGER NOT NULL,
			embedding TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create fake kb_vec: %v", err)
	}
}

func openTempDBForServiceTest(t *testing.T) *index.DB {
	t.Helper()
	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"), index.Options{})
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close index: %v", err)
		}
	})
	forceVectorAvailableForTest(t, db)
	return db
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
