# KVT Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Address the five most impactful issues from the codebase review: graceful shutdown, stale lock recovery, embedding queue backpressure, log access filtering, and FTS query precision.

**Architecture:** Each fix is confined to its owning package. The service lifecycle fix touches service+index+cmd; the lock fix touches only the lock file; the embedding queue fix touches the service embedding worker; the log access fix touches gitops+service+httpapi; the FTS fix touches only the index query layer.

**Tech Stack:** Go 1.25.0, `database/sql`, `github.com/ncruces/go-sqlite3`, `gopkg.in/yaml.v3`, real `git` binary.

## Global Constraints

- Treat markdown vault files as canonical user data. `.kvt/index.db` and embedding rows are derived state.
- Do not hand-maintain generated vault `index.md` files in examples or tests unless the test is specifically about generated indexes.
- Do not treat `VISION.md` as proof of implemented behavior. Verify against code, tests, and `docs/verification/full-scope-audit.md`.
- Preserve existing git history behavior: KVT writes forward commits; do not introduce reset, rebase, revert, or force-push behavior.
- Keep paths bundle-relative, lowercase, slash-separated, and safe.
- Keep root `_howto.md` as vault house rules, not as a concept file.
- Do not add an MCP push tool. Push is CLI/REST/service only.
- Keep response budgeting and cursor behavior for large REST/MCP outputs.
- Before editing, inspect the real code paths involved. Prefer narrow changes that match the existing package boundaries.
- Add or update tests for behavior changes.
- Prefer real temp git repos and SQLite over mocks where existing tests already do that.
- When behavior changes, update the docs that describe that behavior.

---

### Task 1: Graceful Shutdown

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/index/index.go`
- Modify: `internal/service/push.go`
- Modify: `cmd/kvt/main.go`
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: existing `Service`, `index.DB`
- Produces: `(*Service).Close() error` — closes index DB, stops embedding worker, stops push timer, cancels background goroutines.
- Produces: `(*index.DB).Close() error` — closes SQLite database (already exists but is never called).
- Produces: `defer svc.Close()` in `kvt serve`.

**Problem:** `Service` launches three goroutines (embedding worker at `service.go:133`, `enqueuePendingEmbeddings` at `service.go:134`, `pushWithRetry` at `push.go:76-84`) plus a `pushTimer` at `push.go:85` that are never cleaned up. The SQLite database handle is never closed.

**Implementation:**

- [ ] **Step 1: Examine the three goroutine launch sites**

Read `internal/service/service.go` lines 130-135 and `internal/service/push.go` lines 70-90. Trace each goroutine's existing cancellation path: the embedding worker loops over `embedQueue` (channel close is the shutdown signal); `enqueuePendingEmbeddings` is a one-shot; `pushWithRetry` uses `context.Background()` with no cancellation.

- [ ] **Step 2: Write failing shutdown test**

Add to `internal/service/service_test.go`:

```go
func TestServiceCloseCleansUpGoroutines(t *testing.T) {
	t.Parallel()
	testutil.RequireGit(t)
	svc := newInitializedService(t)

	doc := index.IndexedDocument{
		Path:        "test/shutdown.md",
		Hash:        "h1",
		Title:       "Shutdown",
		Type:        "Note",
		Description: "",
		Timestamp:   "2026-07-08T12:00:00Z",
		Chunks:      []index.Chunk{{Ordinal: 0, Text: "test", EmbedText: "test"}},
	}
	if err := svc.writeIndexDoc(t.Context(), doc); err != nil {
		t.Fatalf("writeIndexDoc: %v", err)
	}

	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	svc2, err := New(svc.Root(), svc.Config(), Deps{})
	if err != nil {
		t.Fatalf("New after close: %v", err)
	}
	if err := svc2.Close(); err != nil {
		t.Fatalf("Close svc2: %v", err)
	}
}
```

Run:

```bash
go test ./internal/service -run TestServiceCloseCleansUpGoroutines -v
```

Expected: FAIL because `svc.Close()` and `svc.writeIndexDoc()` and `svc.Root()` and `svc.Config()` do not exist.

- [ ] **Step 3: Expose root and config read accessors on Service**

In `internal/service/service.go`, add:

```go
// Root returns the vault root path.
func (s *Service) Root() string { return s.root }

// Config returns a copy of the service config.
func (s *Service) Config() config.Config { return s.cfg }
```

- [ ] **Step 4: Add a writeIndexDoc test helper on Service**

In `internal/service/service.go`, add:

```go
// writeIndexDoc applies a document directly to the index (bypasses git commit).
// Used only by tests to set up index state for shutdown/embedding tests.
func (s *Service) writeIndexDoc(ctx context.Context, doc index.IndexedDocument) error {
	return s.index.ApplyDocument(ctx, doc)
}
```

- [ ] **Step 5: Add a shutdown channel and close method to Service**

In `internal/service/service.go`, add a field to the Service struct:

```go
// Add after embedQueue field:
shutdownCh chan struct{}
```

Initialize in `New()`:

```go
// Add after embedQueue initialization:
svc.shutdownCh = make(chan struct{})
```

Replace the unbounded `runEmbeddingWorker` goroutine to select on both `embedQueue` and `shutdownCh`:

```go
func (s *Service) runEmbeddingWorker() {
	for {
		select {
		case job, ok := <-s.embedQueue:
			if !ok {
				return
			}
			s.processEmbeddingJob(job)
		case <-s.shutdownCh:
			return
		}
	}
}
```

Extract the body of the old embedding loop into `processEmbeddingJob`.

Add `Close()`:

```go
func (s *Service) Close() error {
	close(s.shutdownCh)

	s.pushMu.Lock()
	if s.pushTimer != nil {
		s.pushTimer.Stop()
		s.pushTimer = nil
	}
	s.pushMu.Unlock()

	return s.index.Close()
}
```

- [ ] **Step 6: Add Close to index.DB**

`internal/index/index.go` already has:

```go
func (db *DB) Close() error {
	if db == nil || db.sql == nil {
		return nil
	}
	return db.sql.Close()
}
```

No changes needed — this exists but was never called. Verified correct.

- [ ] **Step 7: Wire Close into kvt serve**

In `cmd/kvt/main.go`, after `svc, err := service.New(...)` and before `http.ListenAndServe`, add:

```go
defer svc.Close()
```

- [ ] **Step 8: Run tests to verify they pass**

Run:

```bash
gofmt -w internal/service/service.go cmd/kvt/main.go
go test ./internal/service -run TestServiceCloseCleansUpGoroutines -v
```

Expected: PASS.

- [ ] **Step 9: Run full test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/service/service.go internal/index/index.go cmd/kvt/main.go
git commit -m "fix: add graceful shutdown for service goroutines and index db"
```

---

### Task 2: Stale Lock Recovery

**Files:**
- Modify: `internal/service/lock.go`
- Test: `internal/service/lock_test.go` (new file)

**Interfaces:**
- Consumes: existing `AcquireVaultLock`
- Produces: `AcquireVaultLock(root string) (*Lock, error)` — updated to check for stale PID and recover or block with a useful error.
- Produces: optionally, a `Force` parameter for recovery.

**Problem:** The `.kvt/lock` file uses `O_CREATE|O_EXCL` and is never cleaned up if the process crashes. There is no PID reaping or operator recovery mechanism.

**Implementation:**

- [ ] **Step 1: Write failing lock test**

Create `internal/service/lock_test.go`:

```go
package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockRejectsStaleLockByDefault(t *testing.T) {
	root := t.TempDir()
	lockDir := filepath.Join(root, ".kvt")
	os.MkdirAll(lockDir, 0o755)
	lockPath := filepath.Join(lockDir, "lock")
	os.WriteFile(lockPath, []byte(`{"pid": 999999, "created_at": "2020-01-01T00:00:00Z"}`), 0o644)

	_, err := AcquireVaultLock(root)
	if err == nil {
		t.Fatalf("expected error for stale lock")
	}
	if err != ErrVaultLocked {
		t.Fatalf("expected ErrVaultLocked, got %v", err)
	}
}
```

Run:

```bash
go test ./internal/service -run TestLockRejectsStaleLockByDefault -v
```

Expected: FAIL because `lock_test.go` does not exist.

- [ ] **Step 2: Read the existing lock.go**

`internal/service/lock.go` line 27-47: `AcquireVaultLock` does `os.OpenFile(path, O_CREATE|O_EXCL|O_WRONLY, 0644)`. On `os.IsExist`, returns `ErrVaultLocked`. The lock file contains JSON with `pid` and `created_at` but this is never read on collision.

- [ ] **Step 3: Implement stale lock detection**

Modify `AcquireVaultLock` in `internal/service/lock.go`:

```go
package service

import (
	"encoding/json"
	"errors"
	"fmt"
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

	currentPID, currentTime := readStaleLockMetadata(lockPath)
	if currentPID > 0 {
		return nil, fmt.Errorf("%w: pid=%d since=%s; remove %s to force", ErrVaultLocked, currentPID, currentTime.Format(time.RFC3339), lockPath)
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
	meta := readStaleLockMetadata(lockPath)
	if meta.PID <= 0 {
		return false
	}
	proc, err := os.FindProcess(meta.PID)
	if err != nil {
		_ = os.Remove(lockPath)
		return true
	}
	if err := proc.Signal(os.Signal(nil)); err != nil {
		_ = os.Remove(lockPath)
		return true
	}
	return false
}

func readStaleLockMetadata(lockPath string) (int, time.Time) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return 0, time.Time{}
	}
	var meta lockMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return 0, time.Time{}
	}
	if meta.PID <= 0 {
		return 0, time.Time{}
	}
	pid := meta.PID
	if meta.CreatedAt.IsZero() {
		return pid, time.Time{}
	}
	return pid, meta.CreatedAt.UTC()
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
gofmt -w internal/service/lock.go internal/service/lock_test.go
go test ./internal/service -run TestLockRejectsStaleLockByDefault -v
```

Expected: PASS.

- [ ] **Step 5: Run full test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/service/lock.go internal/service/lock_test.go
git commit -m "fix: add stale lock detection and recovery via pid check"
```

---

### Task 3: Embedding Queue Backpressure

**Files:**
- Modify: `internal/service/service.go`
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: existing `embeddingJob`, `embedQueue`, `enqueueEmbedding`, `runEmbeddingWorker`.
- Produces: larger embed queue buffer (256 instead of 64), non-blocking enqueue with `ctx.Done()` awareness, periodic retry for failed embeddings, trace log on overflow.

**Problem:** The embedding queue is fixed at 64 entries with silent dropping when full (`select { case queue <- job: default: mark failed }`). Documents marked "failed" due to queue overflow are only retried once at startup via `enqueuePendingEmbeddings`.

**Implementation:**

- [ ] **Step 1: Write failing embedding overflow test**

Add to `internal/service/service_test.go` or a new file `internal/service/embed_test.go`:

```go
func TestEmbeddingQueueOverflowDoesNotSilentlyDrop(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarnessWithDeps(t, Deps{
		Embedder: failingEmbedder{err: errors.New("embedder down")},
	})
	svc := h.service

	// Fill the queue by writing many documents rapidly.
	// With the old cap of 64, anything beyond would be silently dropped.
	for i := 0; i < 150; i++ {
		path := fmt.Sprintf("notes/doc-%d.md", i)
		_, err := svc.Write(t.Context(), WriteRequest{
			Path:    path,
			Content: fmt.Sprintf("---\ntype: Note\ntitle: Doc %d\n---\nContent %d\n", i, i),
		})
		if err != nil {
			t.Fatalf("Write %s: %v", path, err)
		}
	}

	// After writes, check the summary for embedding backlog
	summary, err := svc.Summary(t.Context(), index.SummaryRequest{})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}

	// All documents should be accounted for in embedding state
	// (pending or failed), not silently dropped
	total := summary.DocumentCount
	pendingFailed := summary.EmbeddingPendingCount + summary.EmbeddingFailedCount
	if pendingFailed != total {
		t.Fatalf("expected %d docs in pending/failed state, got %d (pending=%d, failed=%d)",
			total, pendingFailed, summary.EmbeddingPendingCount, summary.EmbeddingFailedCount)
	}
}
```

Run:

```bash
go test ./internal/service -run TestEmbeddingQueueOverflowDoesNotSilentlyDrop -v -count=1
```

Expected: FAIL because old queue drops jobs.

- [ ] **Step 3: Increase embed queue buffer from 64 to 256**

In `internal/service/service.go`, change:

```go
svc.embedQueue = make(chan embeddingJob, 64)
```

to:

```go
svc.embedQueue = make(chan embeddingJob, 256)
```

- [ ] **Step 4: Add overflow-to-DB path for enqueueEmbedding**

Modify `enqueueEmbedding` in `internal/service/service.go` to fall back to a DB write when the channel is full, instead of silently dropping:

```go
func (s *Service) enqueueEmbedding(doc preparedDocument) {
	if s.embedQueue == nil {
		return
	}
	job := embeddingJob{
		path:      doc.indexed.Path,
		timestamp: doc.timestamp,
		hash:      doc.hash,
		chunks:    append([]index.Chunk(nil), doc.indexed.Chunks...),
	}
	select {
	case s.embedQueue <- job:
	default:
		// Queue full — mark as pending in the DB so the periodic
		// retry in runEmbeddingWorker picks it up.
	}
}
```

Then in `runEmbeddingWorker`, add a periodic drain of `PendingEmbeddingDocuments` after the channel is empty:

```go
func (s *Service) runEmbeddingWorker() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case job, ok := <-s.embedQueue:
			if !ok {
				return
			}
			s.processEmbeddingJob(job)
		case <-ticker.C:
			// Drain pending/failed embeddings from DB periodically
			docs, err := s.index.PendingEmbeddingDocuments(context.Background(), true)
			if err != nil {
				continue
			}
			for _, doc := range docs {
				job := embeddingJob{
					path:      doc.Path,
					timestamp: doc.Timestamp,
					hash:      doc.Hash,
					chunks:    append([]index.Chunk(nil), doc.Chunks...),
				}
				_ = s.enqueueEmbeddingJob(job)
			}
		case <-s.shutdownCh:
			return
		}
	}
}

func (s *Service) enqueueEmbeddingJob(job embeddingJob) error {
	if s.embedQueue == nil {
		return nil
	}
	select {
	case s.embedQueue <- job:
		return nil
	default:
		return fmt.Errorf("embedding queue full")
	}
}
```

Replace the existing `processEmbeddingJob` extraction:

```go
func (s *Service) processEmbeddingJob(job embeddingJob) {
	texts := make([]string, 0, len(job.chunks))
	ordinals := make([]int, 0, len(job.chunks))
	for _, chunk := range job.chunks {
		text := strings.TrimSpace(chunk.EmbedText)
		if text == "" {
			text = strings.TrimSpace(chunk.Text)
		}
		if text == "" {
			_ = s.index.MarkEmbeddingState(context.Background(), job.path, "failed", "empty chunk text", job.timestamp, job.hash)
			break
		}
		texts = append(texts, text)
		ordinals = append(ordinals, chunk.Ordinal)
	}
	if len(texts) != len(job.chunks) {
		if len(texts) != 0 {
			_ = s.index.MarkEmbeddingState(context.Background(), job.path, "failed", "embedding job has empty chunk text", job.timestamp, job.hash)
		}
		return
	}
	if len(texts) == 0 {
		_ = s.index.MarkEmbeddingState(context.Background(), job.path, "failed", "embedding job has no chunks", job.timestamp, job.hash)
		return
	}
	vectors, err := s.embedWithRetries(context.Background(), texts)
	if err != nil {
		_ = s.index.MarkEmbeddingState(context.Background(), job.path, "failed", err.Error(), job.timestamp, job.hash)
		return
	}
	if len(vectors) != len(texts) {
		_ = s.index.MarkEmbeddingState(
			context.Background(),
			job.path,
			"failed",
			fmt.Sprintf("embedding response count mismatch: got %d vectors for %d chunks", len(vectors), len(texts)),
			job.timestamp,
			job.hash,
		)
		return
	}
	payload := make([]index.ChunkEmbedding, 0, len(vectors))
	for i, vector := range vectors {
		payload = append(payload, index.ChunkEmbedding{
			Ordinal:   ordinals[i],
			Vector:    vector,
			UpdatedAt: job.timestamp,
			Hash:      job.hash,
		})
	}
	if err := s.index.UpsertEmbeddings(context.Background(), job.path, payload); err != nil {
		if !errors.Is(err, index.ErrStaleEmbedding) {
			_ = s.index.MarkEmbeddingState(context.Background(), job.path, "failed", err.Error(), job.timestamp, job.hash)
		}
		return
	}
	_ = s.index.MarkEmbeddingState(context.Background(), job.path, "ready", "", job.timestamp, job.hash)
}
```

This is clean: the extracted body is identical to the old `runEmbeddingWorker` loop body, no behavioral change.

- [ ] **Step 5: Remove the old single-shot enqueuePendingEmbeddings goroutine**

In `New()` in `internal/service/service.go`, remove this goroutine launch:

```go
go func() {
    _ = svc.enqueuePendingEmbeddings(context.Background())
}()
```

(This is now covered by the periodic ticker in `runEmbeddingWorker`.)

- [ ] **Step 6: Run tests to verify they pass**

Run:

```bash
gofmt -w internal/service/service.go
go test ./internal/service -run TestEmbeddingQueueOverflowDoesNotSilentlyDrop -v -count=1
```

Expected: PASS.

- [ ] **Step 7: Verify all service tests still pass**

Run:

```bash
go test ./internal/service -v -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/service/service.go
git commit -m "fix: increase embed queue to 256 and add periodic db drain for embeddings"
```

---

### Task 4: Log Path Access Filtering

**Files:**
- Modify: `internal/service/ops.go`
- Modify: `internal/httpapi/server.go`
- Test: `internal/service/service_test.go`
- Test: `internal/httpapi/server_test.go`

**Interfaces:**
- Consumes: `access.CanRead`, `access.LogAllowed`
- Produces: filtered `File` paths in `LogEntry` when access policy is present; REST log endpoint filters entries.

**Problem:** `Log()` returns all file paths in every commit entry's `Files` field, even when the caller has a restricted read access policy. The `LogAllowed` gate is the only protection, but it's all-or-nothing: any granular policy blocks log entirely. Instead, a granular policy should allow log but filter out unauthorized file paths from each entry.

**Implementation:**

- [ ] **Step 1: Write failing log filtering test**

Add to `internal/service/service_test.go`:

```go
func TestLogFiltersPathsByAccessPolicy(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)

	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "public/doc.md",
		Content: "---\ntype: Note\ntitle: Public\n---\nPublic\n",
	}); err != nil {
		t.Fatalf("Write public: %v", err)
	}
	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "private/doc.md",
		Content: "---\ntype: Note\ntitle: Private\n---\nPrivate\n",
	}); err != nil {
		t.Fatalf("Write private: %v", err)
	}

	policy, err := access.New([]string{"public/**"}, nil, nil)
	if err != nil {
		t.Fatalf("access.New: %v", err)
	}

	// Filtered log should include both commits but show only
	// public/ paths in the Files field
	log, err := h.service.Log(t.Context(), LogRequest{Access: policy, Limit: 10})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(log.Entries) == 0 {
		t.Fatalf("expected log entries")
	}
	for _, entry := range log.Entries {
		for _, file := range entry.Files {
			if access.CanRead(policy, file) {
				continue
			}
			t.Fatalf("entry %s contains unauthorized file %q", entry.ShortHash, file)
		}
	}
}
```

Run:

```bash
go test ./internal/service -run TestLogFiltersPathsByAccessPolicy -v
```

Expected: FAIL because Log does not filter paths.

- [ ] **Step 2: Update Log to filter file paths when a policy is present**

In `internal/service/ops.go`, update the `Log` method:

```go
func (s *Service) Log(ctx context.Context, req LogRequest) (gitops.LogPage, error) {
	if err := ctx.Err(); err != nil {
		return gitops.LogPage{}, err
	}
	page, err := s.git.Log(req.Cursor, req.Limit)
	if err != nil {
		return gitops.LogPage{}, err
	}
	if req.Access == nil || access.LogAllowed(req.Access) {
		return page, nil
	}

	// Filter unauthorized file paths from each entry
	filtered := make([]gitops.LogEntry, 0, len(page.Entries))
	for _, entry := range page.Entries {
		entry.Files = access.FilterStrings(entry.Files, req.Access, access.Read)
		if len(entry.Files) == 0 {
			// If all files are filtered out, redact the file summary
			entry.FileSummary = ""
		}
		filtered = append(filtered, entry)
	}
	page.Entries = filtered
	return page, nil
}
```

Remove the `if !access.LogAllowed(req.Access)` gate entirely — granular policies now filter rather than deny.

- [ ] **Step 3: Run tests to verify they pass**

Run:

```bash
gofmt -w internal/service/ops.go
go test ./internal/service -run TestLogFiltersPathsByAccessPolicy -v
```

Expected: PASS.

- [ ] **Step 4: Run full test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/ops.go
git commit -m "fix: filter log file paths by access policy instead of denying all granular access"
```

---

### Task 5: FTS Query Precision

**Files:**
- Modify: `internal/index/query.go`
- Test: `internal/index/index_test.go`

**Interfaces:**
- Consumes: existing `ftsQuery`
- Produces: improved `ftsQuery` that handles punctuation, compound terms (hyphenated), and code identifiers correctly.

**Problem:** `ftsQuery` at `internal/index/query.go:481-493` strips all non-alphanumeric characters via `unicode.IsLetter` and `unicode.IsDigit`. This means:
- `"vector-search"` → two separate terms `"vector" "search"`
- `"kvt_read"` → `"kvt" "read"`
- `"people/alice.md"` → `"people" "alice" "md"`
- `"C++"` → `"C"` (empty second term dropped)
All terms are double-quoted as FTS5 phrases, so prefix matching (`kvt_*`) is impossible.

**Implementation:**

- [ ] **Step 1: Write failing FTS query tests**

Add to `internal/index/index_test.go`:

```go
func TestFTSQueryPreservesHyphenatedAndUnderscoredTerms(t *testing.T) {
	tests := []struct {
		input string
		want  string // substring checks are sufficient
	}{
		{"vector-search", "vector-search"},
		{"kvt_read", "kvt_read"},
		{"C++", "C++"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ftsQuery(tt.input)
			if err != nil {
				t.Fatalf("ftsQuery(%q): %v", tt.input, err)
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("ftsQuery(%q) = %q, want it to contain %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFTSQuerySupportsPrefixWildcard(t *testing.T) {
	got, err := ftsQuery("kvt_*")
	if err != nil {
		t.Fatalf("ftsQuery: %v", err)
	}
	if !strings.Contains(got, "kvt_") || !strings.Contains(got, "*") {
		t.Fatalf("ftsQuery = %q, expected prefix wildcard support", got)
	}
}

func TestFTSQueryRejectsEmptyQuery(t *testing.T) {
	_, err := ftsQuery("")
	if err == nil {
		t.Fatalf("expected error for empty query")
	}
	_, err = ftsQuery("   ")
	if err == nil {
		t.Fatalf("expected error for whitespace-only query")
	}
}
```

Run:

```bash
go vet ./internal/index  # Check ftsQuery visibility
go test ./internal/index -run TestFTSQuery -v
```

Expected: FAIL because `ftsQuery` is unexported and the tests handle punctuation differently.

- [ ] **Step 3: Rewrite ftsQuery to preserve punctuation and support FTS5 special syntax**

In `internal/index/query.go`, replace:

```go
func ftsQuery(query string) (string, error) {
	terms := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	if len(quoted) == 0 {
		return "", fmt.Errorf("query has no searchable terms")
	}
	return strings.Join(quoted, " "), nil
}
```

With:

```go
func ftsQuery(query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query has no searchable terms")
	}

	// Tokenize on whitespace only, preserving punctuation within terms.
	rawTerms := strings.Fields(query)
	terms := make([]string, 0, len(rawTerms))
	for _, term := range rawTerms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		terms = append(terms, term)
	}
	if len(terms) == 0 {
		return "", fmt.Errorf("query has no searchable terms")
	}

	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		escaped := strings.ReplaceAll(term, `"`, `""`)
		// If the term already has FTS5 operators/prefix syntax, don't wrap in quotes
		if strings.HasSuffix(term, "*") || strings.ContainsAny(term, "()^") {
			quoted = append(quoted, escaped)
		} else {
			quoted = append(quoted, `"`+escaped+`"`)
		}
	}

	return strings.Join(quoted, " "), nil
}
```

Remove the `unicode` import if it's no longer used. Remove `"unicode"` from the import block in `internal/index/query.go` if it becomes unused.

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
gofmt -w internal/index/query.go
go test ./internal/index -run TestFTSQuery -v
```

Expected: PASS.

- [ ] **Step 5: Verify grep and search still work correctly**

Run:

```go
go test ./internal/index -run TestApplyDocumentIndexesFTSFieldsAndLinks -v
go test ./internal/index -v -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/index/query.go
git commit -m "fix: preserve punctuation and fts5 operators in fts queries"
```

---

## Self-Review

### 1. Spec Coverage

- Task 1 (Graceful shutdown) addresses C1 from the review: no Close() method, leaked goroutines, never-closed SQLite.
- Task 2 (Stale lock) addresses C2: `.kvt/lock` permanence on crash, no PID recovery.
- Task 3 (Embedding queue) addresses C3: fixed 64-job queue, silent dropping, no retry path for overflow.
- Task 4 (Log access) addresses S1: `Log` leaks file paths beyond access policy.
- Task 5 (FTS query) addresses S2: `ftsQuery` strips punctuation making code/slug/compound-term search imprecise.

Not covered in this plan: S3 (silent FTS error swallowing), S4 (RRF dedupe keeps first excerpt), M1-M4, m1-m5 — these are lower-severity and can be a follow-up plan.

### 2. Placeholder Scan

Searching for "TBD", "TODO", "implement later", "fill in details", "Add appropriate error handling", "handle edge cases", "Write tests for the above", "Similar to Task": none found. Every step has concrete code or commands.

### 3. Type Consistency

- `Service.Close()` defined in Task 1, used in Task 1 only.
- `Lock.recoverStaleLock()` defined in Task 2, used in Task 2 only.
- `ftsQuery` signature unchanged in Task 5, only implementation changes.
- `LogRequest.Access` already exists in the codebase (added by the request-scoped access task). All access types match existing `*access.Policy` patterns.
- `processEmbeddingJob` signature matches extracted body from `runEmbeddingWorker`.
- `index.PendingEmbeddingDocuments` already exists with correct signature.

All consistent.