# Full-Scope KVT Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the complete KVT Go service described by `VISION.md` and `docs/superpowers/specs/2026-07-07-full-scope-kvt-design.md`.

**Architecture:** One API-neutral service layer owns vault semantics. CLI, REST, and MCP adapters call the same service methods so path validation, ontology validation, timestamp authority, git commits, index updates, conflict handling, search, and response budgets are consistent everywhere.

**Tech Stack:** Go 1.25.0, `net/http`, `database/sql`, `github.com/mattn/go-sqlite3`, `github.com/asg017/sqlite-vec-go-bindings/cgo`, `github.com/modelcontextprotocol/go-sdk/mcp`, `gopkg.in/yaml.v3`, real `git` binary, Docker.

## Global Constraints

- Source of truth is OKF v0.1 markdown in a git repository.
- Runtime and derived state live under `.kvt/` and `.kvt/` is git-ignored.
- Vault path comes from `--vault` or `KVT_VAULT`; it is not stored in config.
- Paths are bundle-relative, forward-slash separated, lowercase, and each segment matches `[a-z0-9_][a-z0-9._-]*`.
- `index.md` is service-owned, deterministic, readable, and excluded from search indexing.
- Root `index.md` declares `okf_version`.
- The service overwrites client-supplied `timestamp` on every write/edit.
- Writes are serialized; reads do not wait behind unrelated pending writes.
- One write operation produces exactly one forward git commit.
- KVT never resets, reverts, rebases, force-pushes, or rewrites history.
- Git operations shell out to the real `git` binary.
- Configured pushes are asynchronous, fast-forward only, and never fail the originating write.
- Keyword index updates are synchronous with writes.
- Embeddings are asynchronous; when unavailable, search degrades to FTS-only.
- Hybrid search uses FTS, vector search, weighted RRF, and optional best-effort LLM rerank.
- MCP tool names use the fixed `kvt_` prefix.
- `POST /push` exists in REST; there is no MCP push tool.
- Unbounded responses obey `limits.max_response_chars` and return explicit truncation plus cursor.
- Tests use real git and real SQLite for integration coverage.

---

## File Structure

Create these top-level files:

- `cmd/kvt/main.go`: CLI entry point.
- `Dockerfile`: production image with the `kvt` binary and `git`.
- `compose.yaml`: local example with `/workspace` vault mount.
- `.gitignore`: ignore local build outputs and `.kvt` only when this repository itself is used as a vault during tests.

Create these internal packages:

- `internal/config`: config defaults, YAML loading, env overrides.
- `internal/pathutil`: path validation and slug suggestions.
- `internal/frontmatter`: markdown frontmatter parse/render/hash/timestamp.
- `internal/ontology`: ontology schema and validation.
- `internal/vault`: vault filesystem operations, summary, links, `index.md`.
- `internal/gitops`: shell-out git operations.
- `internal/chunk`: markdown chunking.
- `internal/index`: SQLite schema, sync indexing, FTS, vec, reconciliation.
- `internal/embed`: embedder interface, OpenAI-compatible client, Ollama client.
- `internal/rerank`: LLM rerank interface and OpenAI-compatible client.
- `internal/search`: FTS/vector query orchestration, RRF, result aggregation.
- `internal/responsebudget`: truncation and cursor helpers.
- `internal/service`: typed application service requests/responses.
- `internal/httpapi`: REST server and auth.
- `internal/mcp`: MCP server, tools, resources, prompt, instructions.
- `internal/testutil`: temp vaults, git helpers, deterministic HTTP stubs.

Create tests beside packages using `_test.go` files. Integration tests that need the real `git` binary and SQLite live in `internal/service`, `internal/index`, `internal/httpapi`, and `internal/mcp`.

---

### Task 1: Project Scaffold and Baseline Tooling

**Files:**
- Create: `cmd/kvt/main.go`
- Create: `internal/version/version.go`
- Create: `internal/testutil/require.go`
- Modify: `go.mod`
- Test: `cmd/kvt/main_test.go`

**Interfaces:**
- Produces: `version.Version string`
- Produces: `testutil.RequireGit(t testing.TB)`
- Produces: CLI command `kvt version`

- [ ] **Step 1: Add a failing CLI smoke test**

```go
package main

import (
	"bytes"
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
```

- [ ] **Step 2: Run the failing test**

Run: `go test ./cmd/kvt`

Expected: FAIL because `run` does not exist.

- [ ] **Step 3: Add baseline CLI implementation**

```go
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ematvey/kvt/internal/version"
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) >= 2 && args[1] == "version" {
		fmt.Fprintln(stdout, version.Version)
		return 0
	}
	fmt.Fprintln(stderr, "usage: kvt <init|serve|reindex|validate|push|version>")
	return 2
}
```

```go
package version

const Version = "dev"
```

- [ ] **Step 4: Add test utility for git-dependent tests**

```go
package testutil

import (
	"os/exec"
	"testing"
)

func RequireGit(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git binary is required: %v", err)
	}
}
```

- [ ] **Step 5: Verify baseline**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod cmd/kvt internal/version internal/testutil
git commit -m "feat: add kvt cli scaffold"
```

---

### Task 2: Config, Path, and Frontmatter Primitives

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/pathutil/path.go`
- Create: `internal/pathutil/path_test.go`
- Create: `internal/frontmatter/document.go`
- Create: `internal/frontmatter/document_test.go`
- Modify: `go.mod`

**Interfaces:**
- Produces: `config.Default() config.Config`
- Produces: `config.Load(vaultPath string, explicitPath string) (config.Config, error)`
- Produces: `pathutil.Normalize(raw string) (pathutil.Path, error)`
- Produces: `pathutil.Suggest(raw string) string`
- Produces: `frontmatter.Parse(markdown []byte) (frontmatter.Document, error)`
- Produces: `frontmatter.Render(doc frontmatter.Document) ([]byte, error)`
- Produces: `frontmatter.Hash(content []byte) string`
- Produces: `frontmatter.WithTimestamp(doc frontmatter.Document, now time.Time) frontmatter.Document`

- [ ] **Step 1: Write path tests**

```go
package pathutil

import "testing"

func TestNormalizeRejectsUnsafePathWithSuggestion(t *testing.T) {
	_, err := Normalize("people/John Smith.md")
	if err == nil {
		t.Fatalf("expected invalid path")
	}
	if got := Suggest("people/John Smith.md"); got != "people/john-smith.md" {
		t.Fatalf("suggestion = %q", got)
	}
}

func TestNormalizeAcceptsBundleRelativePath(t *testing.T) {
	p, err := Normalize("people/alice.md")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if p.String() != "people/alice.md" {
		t.Fatalf("path = %q", p.String())
	}
}
```

- [ ] **Step 2: Write frontmatter tests**

```go
package frontmatter

import (
	"testing"
	"time"
)

func TestParseRenderTimestampAndHash(t *testing.T) {
	input := []byte("---\ntype: Person\ntitle: Alice\ntimestamp: old\n---\n# Alice\n")
	doc, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	out, err := Render(WithTimestamp(doc, now))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != "---\ntimestamp: \"2026-07-07T12:00:00Z\"\ntitle: Alice\ntype: Person\n---\n# Alice\n" {
		t.Fatalf("rendered:\n%s", out)
	}
	if Hash(out) == Hash(input) {
		t.Fatalf("expected hash to change after timestamp mutation")
	}
}
```

- [ ] **Step 3: Write config tests**

```go
package config

import "testing"

func TestDefaultMatchesLocalMode(t *testing.T) {
	cfg := Default()
	if cfg.Server.HTTPPort != 8200 {
		t.Fatalf("HTTPPort = %d", cfg.Server.HTTPPort)
	}
	if len(cfg.Auth.APIKeys) != 0 {
		t.Fatalf("local mode should default to open API")
	}
	if cfg.Git.Branch != "main" {
		t.Fatalf("branch = %q", cfg.Git.Branch)
	}
}
```

- [ ] **Step 4: Run tests to verify failure**

Run: `go test ./internal/pathutil ./internal/frontmatter ./internal/config`

Expected: FAIL because packages do not exist.

- [ ] **Step 5: Implement primitives**

Create package files with the interfaces above. Use `gopkg.in/yaml.v3` for stable YAML parsing and rendering. Store frontmatter fields as `map[string]any`; render by sorting keys lexically before writing YAML lines for scalar strings, booleans, numbers, and string slices. Return a typed path error that includes the suggestion string.

- [ ] **Step 6: Add dependency**

Run: `go get gopkg.in/yaml.v3`

Expected: `go.mod` and `go.sum` include `gopkg.in/yaml.v3`.

- [ ] **Step 7: Verify**

Run: `go test ./internal/pathutil ./internal/frontmatter ./internal/config`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/config internal/pathutil internal/frontmatter
git commit -m "feat: add config path and frontmatter primitives"
```

---

### Task 3: Ontology and Vault Filesystem Rules

**Files:**
- Create: `internal/ontology/ontology.go`
- Create: `internal/ontology/ontology_test.go`
- Create: `internal/vault/vault.go`
- Create: `internal/vault/index.go`
- Create: `internal/vault/links.go`
- Create: `internal/vault/vault_test.go`

**Interfaces:**
- Consumes: `frontmatter.Document`, `pathutil.Path`
- Produces: `ontology.Load(root string) (ontology.Schema, error)`
- Produces: `ontology.ValidateDocument(schema Schema, path pathutil.Path, doc frontmatter.Document, mode Mode) ValidationResult`
- Produces: `ontology.ValidateVault(root string, schema Schema) (ValidationReport, error)`
- Produces: `vault.ReadConcept(root string, path pathutil.Path) (vault.Concept, error)`
- Produces: `vault.RegenerateIndexes(root string, affected pathutil.Path, limit int, rootOKFVersion string) ([]string, error)`
- Produces: `vault.ExtractLinks(from pathutil.Path, doc frontmatter.Document, schema ontology.Schema) []vault.Link`

- [ ] **Step 1: Write ontology validation tests**

```go
package ontology

import (
	"testing"

	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/pathutil"
)

func TestValidateRequiredEnumPatternAndRef(t *testing.T) {
	schema := Schema{
		Types: map[string]TypeDef{
			"Incident": {
				Required: []string{"title", "severity", "status"},
				Fields: map[string]FieldDef{
					"severity": {Enum: []string{"low", "medium", "high", "critical"}},
					"status":   {Enum: []string{"open", "investigating", "resolved"}},
				},
			},
		},
		UnknownTypes: UnknownWarn,
	}
	doc := frontmatter.Document{Fields: map[string]any{
		"type": "Incident", "title": "DB down", "severity": "urgent", "status": "open",
	}}
	p, _ := pathutil.Normalize("incidents/db-down.md")
	result := ValidateDocument(schema, p, doc, Strict)
	if len(result.Errors) != 1 {
		t.Fatalf("errors = %#v", result.Errors)
	}
	if result.Errors[0].Field != "severity" {
		t.Fatalf("field = %q", result.Errors[0].Field)
	}
}
```

- [ ] **Step 2: Write index regeneration and link tests**

```go
package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ematvey/kvt/internal/pathutil"
)

func TestRegenerateIndexesDeterministic(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "people", "alice.md"), "---\ntype: Person\ntitle: Alice\ndescription: DBA\n---\nBody\n")
	mustWrite(t, filepath.Join(root, "systems", "db.md"), "---\ntype: System\ntitle: DB\ndescription: Primary\n---\nBody\n")
	p, _ := pathutil.Normalize("people/alice.md")
	changed, err := RegenerateIndexes(root, p, 50, "0.1")
	if err != nil {
		t.Fatalf("RegenerateIndexes: %v", err)
	}
	if len(changed) == 0 {
		t.Fatalf("expected changed indexes")
	}
	rootIndex, _ := os.ReadFile(filepath.Join(root, "index.md"))
	if string(rootIndex) != "---\nokf_version: \"0.1\"\ntype: Index\n---\n# Index\n\n## Directories\n\n- [people/](people/index.md)\n- [systems/](systems/index.md)\n" {
		t.Fatalf("root index:\n%s", rootIndex)
	}
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internal/ontology ./internal/vault`

Expected: FAIL because packages are incomplete.

- [ ] **Step 4: Implement ontology and vault filesystem rules**

Implement schema structs, YAML loading, unknown-type policy, field constraints, path glob rules, deterministic child listing, root and directory `index.md` rendering, and link extraction for markdown links plus ontology `ref` fields.

- [ ] **Step 5: Verify**

Run: `go test ./internal/ontology ./internal/vault`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ontology internal/vault
git commit -m "feat: add ontology and vault filesystem rules"
```

---

### Task 4: Git Operations, Init, Locking, and History

**Files:**
- Create: `internal/gitops/git.go`
- Create: `internal/gitops/git_test.go`
- Create: `internal/service/init.go`
- Create: `internal/service/lock.go`
- Create: `internal/service/init_test.go`
- Modify: `cmd/kvt/main.go`

**Interfaces:**
- Consumes: `config.Config`
- Produces: `gitops.Client`
- Produces: `gitops.Status(root string, branch string) (gitops.WorktreeStatus, error)`
- Produces: `gitops.Commit(root string, opts gitops.CommitOptions) (gitops.CommitResult, error)`
- Produces: `gitops.Log(root string, cursor string, limit int) (gitops.LogPage, error)`
- Produces: `gitops.History(root string, path string, cursor string, limit int) (gitops.HistoryPage, error)`
- Produces: `service.Init(ctx context.Context, req service.InitRequest) (service.InitResult, error)`
- Produces: `service.AcquireVaultLock(root string) (*service.Lock, error)`

- [ ] **Step 1: Write init integration test**

```go
package service

import (
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
```

- [ ] **Step 2: Write git history test**

```go
package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
}

func initRepoWithCommit(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.name", "test")
	runGit(t, root, "config", "user.email", "test@example.com")
	writeFile(t, root+"/a.md", "---\ntype: Note\ntitle: A\n---\nA\n")
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

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internal/gitops ./internal/service`

Expected: FAIL because gitops and init are incomplete.

- [ ] **Step 4: Implement gitops**

Use `exec.CommandContext` with `cmd.Dir = root`. Implement branch checks, detached HEAD detection, dirty status, author fallback environment, `git add`, `git commit`, `git log`, `git show`, `git diff`, and `git push --ff-only`.

- [ ] **Step 5: Implement init and lock**

Implement empty bootstrap, adoption of existing markdown git repos, `.gitignore` `.kvt/` entry, default `.kvt/config.yaml`, starter `_ontology.yaml`, root `index.md`, idempotence, and `.kvt/lock` exclusive creation with process metadata.

- [ ] **Step 6: Wire `kvt init`**

`cmd/kvt/main.go` must parse `init --vault <path> --defaults` and call `service.Init`.

- [ ] **Step 7: Verify**

Run: `go test ./internal/gitops ./internal/service ./cmd/kvt`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/kvt internal/gitops internal/service
git commit -m "feat: add git backed vault initialization"
```

---

### Task 5: Core Service Read, Write, Edit, Delete, Validate

**Files:**
- Create: `internal/service/types.go`
- Create: `internal/service/service.go`
- Create: `internal/service/write.go`
- Create: `internal/service/read.go`
- Create: `internal/service/validate.go`
- Create: `internal/service/service_test.go`
- Modify: `cmd/kvt/main.go`

**Interfaces:**
- Consumes: `config.Config`, `gitops`, `vault`, `ontology`, `frontmatter`
- Produces: `service.Service`
- Produces: `New(root string, cfg config.Config, deps Deps) (*Service, error)`
- Produces: `(*Service).Read(ctx context.Context, req ReadRequest) (ReadResponse, error)`
- Produces: `(*Service).Write(ctx context.Context, req WriteRequest) (WriteResponse, error)`
- Produces: `(*Service).Edit(ctx context.Context, req EditRequest) (WriteResponse, error)`
- Produces: `(*Service).Delete(ctx context.Context, req DeleteRequest) (DeleteResponse, error)`
- Produces: `(*Service).Validate(ctx context.Context, req ValidateRequest) (ValidateResponse, error)`

- [ ] **Step 1: Write conflict and timestamp tests**

```go
package service

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/testutil"
)

func TestWriteCommitsTimestampAndRejectsStaleBaseHash(t *testing.T) {
	svc := newInitializedService(t)
	first, err := svc.Write(t.Context(), WriteRequest{
		Path:    "people/alice.md",
		Content: "---\ntype: Person\ntitle: Alice\ndescription: DBA\n---\nBody\n",
		Agent:   "test-agent",
	})
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	second, err := svc.Write(t.Context(), WriteRequest{
		Path:     "people/alice.md",
		Content:  "---\ntype: Person\ntitle: Alice\ndescription: Lead DBA\n---\nBody\n",
		BaseHash: first.Hash,
		Agent:    "test-agent",
	})
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	_, err = svc.Write(t.Context(), WriteRequest{
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
}
```

- [ ] **Step 2: Write edit uniqueness test**

```go
func TestEditRequiresUniqueOldString(t *testing.T) {
	svc := newInitializedService(t)
	_, _ = svc.Write(t.Context(), WriteRequest{
		Path: "notes/repeat.md",
		Content: "---\ntype: Note\ntitle: Repeat\n---\nhello hello\n",
	})
	_, err := svc.Edit(t.Context(), EditRequest{
		Path:      "notes/repeat.md",
		OldString: "hello",
		NewString: "hi",
	})
	if !IsAmbiguousEdit(err) {
		t.Fatalf("expected ambiguous edit, got %v", err)
	}
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internal/service`

Expected: FAIL because service methods are incomplete.

- [ ] **Step 4: Implement service types and write lock**

Define typed request/response structs, error types with machine-readable codes, single-writer mutex, content hash preconditions, exact edit replacement, `replace_all`, delete semantics, validation mode, index regeneration, and one git commit per write.

- [ ] **Step 5: Wire CLI validate**

`cmd/kvt/main.go` must parse `validate --vault <path>` and call `Service.Validate`. Exit code is non-zero when validation has errors.

- [ ] **Step 6: Verify**

Run: `go test ./internal/service ./cmd/kvt`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/kvt internal/service
git commit -m "feat: add core vault read write and validation service"
```

---

### Task 6: SQLite Index, List, Grep, Summary, Reconciliation

**Files:**
- Create: `internal/index/index.go`
- Create: `internal/index/schema.go`
- Create: `internal/index/sync.go`
- Create: `internal/index/query.go`
- Create: `internal/index/reconcile.go`
- Create: `internal/index/index_test.go`
- Modify: `internal/service/service.go`
- Modify: `internal/service/read.go`
- Modify: `internal/service/types.go`

**Interfaces:**
- Consumes: `frontmatter.Document`, `vault.Link`
- Produces: `index.Open(path string, opts Options) (*index.DB, error)`
- Produces: `(*DB).ApplyDocument(ctx context.Context, doc IndexedDocument) error`
- Produces: `(*DB).RemoveDocument(ctx context.Context, path string) error`
- Produces: `(*DB).List(ctx context.Context, req ListRequest) (ListResponse, error)`
- Produces: `(*DB).Grep(ctx context.Context, req GrepRequest) (GrepResponse, error)`
- Produces: `(*DB).Backlinks(ctx context.Context, path string) ([]Link, error)`
- Produces: `(*DB).Summary(ctx context.Context, req SummaryRequest) (SummaryResponse, error)`
- Produces: `(*DB).Reconcile(ctx context.Context, root string) (ReconcileResult, error)`

- [ ] **Step 1: Write SQLite schema and FTS tests**

```go
package index

import "testing"

func TestApplyDocumentIndexesFTSFieldsAndLinks(t *testing.T) {
	db := openTempDB(t)
	err := db.ApplyDocument(t.Context(), IndexedDocument{
		Path: "people/alice.md",
		Hash: "h1",
		Title: "Alice",
		Type: "Person",
		Fields: map[string][]string{"tag": []string{"dba"}},
		Chunks: []Chunk{{Ordinal: 0, Text: "title Alice type Person"}, {Ordinal: 1, Text: "Alice owns the primary database"}},
		Links: []Link{{FromPath: "people/alice.md", ToPath: "systems/db.md", Kind: "body"}},
	})
	if err != nil {
		t.Fatalf("ApplyDocument: %v", err)
	}
	grep, err := db.Grep(t.Context(), GrepRequest{Query: "primary database", Limit: 10})
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if len(grep.Matches) != 1 {
		t.Fatalf("matches = %d", len(grep.Matches))
	}
}

func openTempDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir()+"/index.db", Options{EnableVector: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
```

- [ ] **Step 2: Write service summary/list/backlink test**

```go
func TestReadReturnsBacklinksFromIndex(t *testing.T) {
	svc := newInitializedService(t)
	_, _ = svc.Write(t.Context(), WriteRequest{Path: "systems/db.md", Content: "---\ntype: System\ntitle: DB\ndescription: Primary\n---\n"})
	_, _ = svc.Write(t.Context(), WriteRequest{Path: "people/alice.md", Content: "---\ntype: Person\ntitle: Alice\ndescription: DBA\n---\nSee [DB](../systems/db.md).\n"})
	got, err := svc.Read(t.Context(), ReadRequest{Path: "systems/db.md"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Backlinks) != 1 || got.Backlinks[0].FromPath != "people/alice.md" {
		t.Fatalf("backlinks = %#v", got.Backlinks)
	}
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internal/index ./internal/service`

Expected: FAIL because indexing is incomplete.

- [ ] **Step 4: Add SQLite dependencies**

Run: `go get github.com/mattn/go-sqlite3 github.com/asg017/sqlite-vec-go-bindings/cgo`

Expected: `go.mod` and `go.sum` include SQLite dependencies.

- [ ] **Step 5: Implement index schema and sync updates**

Use `database/sql` and `sqlite3`. Create `kb_docs`, `kb_chunks`, `kb_fts`, `kb_links`, `kb_fields`, and `kb_meta`. Enable FTS5. Create `kb_vec` when sqlite-vec loads successfully; record unavailable vector support as degraded index capability.

- [ ] **Step 6: Wire service to index**

On write/edit/delete, call index update/removal in the same service operation after filesystem mutation and before commit. Exclude every `index.md` from search rows. Return backlinks, list, grep, summary, and freshness status through service methods.

- [ ] **Step 7: Add reconciliation**

Scan the vault while skipping `.git/` and `.kvt/`, compare content hashes, apply changed docs, remove missing docs, and report changed/removed counts.

- [ ] **Step 8: Verify**

Run: `go test ./internal/index ./internal/service`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum internal/index internal/service
git commit -m "feat: add sqlite vault index and reconciliation"
```

---

### Task 7: Chunking, Embeddings, Vector Search, RRF, Rerank

**Files:**
- Create: `internal/chunk/chunk.go`
- Create: `internal/chunk/chunk_test.go`
- Create: `internal/embed/embed.go`
- Create: `internal/embed/openai.go`
- Create: `internal/embed/ollama.go`
- Create: `internal/embed/embed_test.go`
- Create: `internal/rerank/rerank.go`
- Create: `internal/rerank/openai.go`
- Create: `internal/search/search.go`
- Create: `internal/search/rrf.go`
- Create: `internal/search/search_test.go`
- Modify: `internal/index/query.go`
- Modify: `internal/service/service.go`

**Interfaces:**
- Produces: `chunk.Split(doc chunk.Document) ([]chunk.Chunk, error)`
- Produces: `embed.Embedder` interface with `Embed(ctx context.Context, texts []string) ([][]float32, error)`
- Produces: `rerank.Reranker` interface with `Rerank(ctx context.Context, query string, candidates []rerank.Candidate) ([]rerank.Score, error)`
- Produces: `search.Search(ctx context.Context, req SearchRequest) (SearchResponse, error)`
- Produces: async embedding worker owned by `service.Service`

- [ ] **Step 1: Write chunking tests**

```go
package chunk

import (
	"strings"
	"testing"
)

func TestSplitKeepsCodeBlocksAtomicAndAddsBreadcrumb(t *testing.T) {
	doc := Document{
		Path: "systems/db.md",
		Title: "DB",
		Type: "System",
		FrontmatterText: "title: DB\ntype: System",
		Body: "# Runbook\n\nIntro\n\n```sql\nselect 1;\n```\n\n## Restart\n\nSteps\n",
	}
	chunks, err := Split(doc)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if chunks[0].Ordinal != 0 || chunks[0].EmbedText == "" {
		t.Fatalf("frontmatter chunk missing: %#v", chunks)
	}
	for _, c := range chunks {
		if contains(c.Text, "```sql") && !contains(c.Text, "select 1;") {
			t.Fatalf("code block split incorrectly: %#v", c)
		}
	}
}

func contains(s string, sub string) bool {
	return strings.Contains(s, sub)
}
```

- [ ] **Step 2: Write RRF and degraded search tests**

```go
package search

import "testing"

func TestRRFWeightsRanksWithoutScoreNormalization(t *testing.T) {
	got := FuseRRF([]RankedList{
		{Weight: 0.5, Hits: []Hit{{DocPath: "a"}, {DocPath: "b"}}},
		{Weight: 0.5, Hits: []Hit{{DocPath: "b"}, {DocPath: "c"}}},
	}, 60)
	if got[0].DocPath != "b" {
		t.Fatalf("top = %#v", got)
	}
}
```

- [ ] **Step 3: Write embedder client tests with HTTP stubs**

```go
package embed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleEmbedsTexts(t *testing.T) {
	server := openAIEmbeddingStub(t, [][]float32{{1, 0}, {0, 1}})
	client := NewOpenAICompatible(server.URL, "test-model", "", 2)
	got, err := client.Embed(t.Context(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 2 || len(got[0]) != 2 {
		t.Fatalf("vectors = %#v", got)
	}
}

func openAIEmbeddingStub(t *testing.T, vectors [][]float32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		items := make([]map[string]any, len(vectors))
		for i, v := range vectors {
			items[i] = map[string]any{"index": i, "embedding": v}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
	}))
}
```

- [ ] **Step 4: Run tests to verify failure**

Run: `go test ./internal/chunk ./internal/embed ./internal/rerank ./internal/search ./internal/service`

Expected: FAIL because intelligence packages are incomplete.

- [ ] **Step 5: Implement chunker and adapters**

Implement heading-aware split, frontmatter chunk zero, breadcrumb embed text, token approximation by whitespace, paragraph splitting for large sections, atomic fenced code blocks and markdown table blocks, OpenAI-compatible `/v1/embeddings`, Ollama `/api/embed`, and OpenAI-compatible rerank prompt over `/v1/chat/completions`.

- [ ] **Step 6: Implement async embedding queue**

Store pending embedding status in `kb_docs`. Add a service worker that retries with exponential backoff, records last error, and writes vectors to `kb_vec` when vec support is enabled. Health reports pending and failed embedding counts.

- [ ] **Step 7: Implement hybrid search**

Run FTS and vector chunk search, aggregate by best chunk, fuse with weighted RRF, apply best-effort rerank, return matching section excerpts, and mark degraded mode when vector or rerank contribution is unavailable.

- [ ] **Step 8: Verify**

Run: `go test ./internal/chunk ./internal/embed ./internal/rerank ./internal/search ./internal/index ./internal/service`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/chunk internal/embed internal/rerank internal/search internal/index internal/service
git commit -m "feat: add hybrid search and embedding pipeline"
```

---

### Task 8: REST API, Auth, Health, and CLI Serve

**Files:**
- Create: `internal/httpapi/server.go`
- Create: `internal/httpapi/routes.go`
- Create: `internal/httpapi/auth.go`
- Create: `internal/httpapi/server_test.go`
- Modify: `internal/service/types.go`
- Modify: `cmd/kvt/main.go`

**Interfaces:**
- Consumes: `service.Service`
- Produces: `httpapi.NewServer(svc *service.Service, cfg config.Config) http.Handler`
- Produces: REST routes from `VISION.md`
- Produces: `kvt serve --vault <path>`
- Produces: `kvt reindex --vault <path>`

- [ ] **Step 1: Write REST route tests**

```go
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/service"
)

func TestConceptLifecycleOverHTTP(t *testing.T) {
	svc := newHTTPTestService(t)
	handler := NewServer(svc, openConfig())
	body := `{"path":"people/alice.md","content":"---\ntype: Person\ntitle: Alice\ndescription: DBA\n---\nBody\n"}`
	req := httptest.NewRequest(http.MethodPost, "/concepts", strings.NewReader(body))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("POST status = %d body=%s", res.Code, res.Body.String())
	}
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/concepts/people/alice.md", nil))
	if read.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", read.Code, read.Body.String())
	}
}

func newHTTPTestService(t *testing.T) *service.Service {
	t.Helper()
	root := t.TempDir()
	if _, err := service.Init(t.Context(), service.InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	svc, err := service.New(root, config.Default(), service.Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func openConfig() config.Config {
	return config.Default()
}
```

- [ ] **Step 2: Write auth test**

```go
func TestBearerAuthWhenConfigured(t *testing.T) {
	svc := newHTTPTestService(t)
	cfg := openConfig()
	cfg.Auth.APIKeys = []string{"secret"}
	handler := NewServer(svc, cfg)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/health", nil))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", res.Code)
	}
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internal/httpapi ./cmd/kvt`

Expected: FAIL because REST server is incomplete.

- [ ] **Step 4: Implement REST server**

Implement every route in `VISION.md`, greedy concept/history paths, JSON request/response structs, error-to-status mapping, optional bearer auth, health payload, response-budget truncation, and pagination cursors for log/history/list/grep.

- [ ] **Step 5: Implement `serve` and `reindex` commands**

`serve` loads config, acquires vault lock, verifies branch, opens the service, starts REST on configured port, and starts MCP according to transport config. `reindex` calls service rebuild and exits non-zero on rebuild errors.

- [ ] **Step 6: Verify**

Run: `go test ./internal/httpapi ./internal/service ./cmd/kvt`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/kvt internal/httpapi internal/service
git commit -m "feat: add rest api and server commands"
```

---

### Task 9: MCP Server, Tooling Contract, Howto, Skill Artifact

**Files:**
- Create: `internal/mcp/server.go`
- Create: `internal/mcp/tools.go`
- Create: `internal/mcp/howto.go`
- Create: `internal/mcp/server_test.go`
- Create: `SKILL.md`
- Modify: `internal/service/types.go`
- Modify: `cmd/kvt/main.go`
- Modify: `go.mod`

**Interfaces:**
- Consumes: `service.Service`
- Produces: `mcp.NewServer(svc *service.Service, cfg config.Config) (*mcp.Server, error)`
- Produces: MCP tools `kvt_summary`, `kvt_howto`, `kvt_search`, `kvt_grep`, `kvt_list`, `kvt_read`, `kvt_types`, `kvt_log`, `kvt_history`, `kvt_write`, `kvt_edit`, `kvt_delete`, `kvt_validate`
- Produces: MCP resource and prompt for howto content
- Produces: repository-level `SKILL.md`

- [ ] **Step 1: Add MCP dependency**

Run: `go get github.com/modelcontextprotocol/go-sdk`

Expected: `go.mod` and `go.sum` include `github.com/modelcontextprotocol/go-sdk`.

- [ ] **Step 2: Write MCP tool registration test**

```go
package mcp

import (
	"strings"
	"testing"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/service"
)

func TestServerRegistersKVTTools(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	names := RegisteredToolNames(server)
	for _, want := range []string{"kvt_summary", "kvt_howto", "kvt_search", "kvt_read", "kvt_write", "kvt_validate"} {
		if !names[want] {
			t.Fatalf("missing tool %s in %#v", want, names)
		}
	}
}

func newMCPTestService(t *testing.T) *service.Service {
	t.Helper()
	root := t.TempDir()
	if _, err := service.Init(t.Context(), service.InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	svc, err := service.New(root, config.Default(), service.Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func openConfig() config.Config {
	return config.Default()
}
```

- [ ] **Step 3: Write howto test**

```go
func TestHowtoMentionsServiceOwnedFilesAndConflictRetry(t *testing.T) {
	text := DefaultHowto()
	for _, want := range []string{"index.md", "timestamp", "base_hash", "kvt_search"} {
		if !strings.Contains(text, want) {
			t.Fatalf("howto missing %q", want)
		}
	}
}
```

- [ ] **Step 4: Run tests to verify failure**

Run: `go test ./internal/mcp`

Expected: FAIL because MCP server is incomplete.

- [ ] **Step 5: Implement MCP server**

Use the official MCP Go SDK. Register concise instructions, all read/write tools, howto resource, howto prompt, typed JSON schemas, and tool descriptions that explain when to prefer each tool. Route calls to service methods. Do not expose push as a tool.

- [ ] **Step 6: Add repository coding-agent skill**

Create `SKILL.md` with direct-file OKF authoring guidance: required frontmatter `type`, normalized paths, service-owned `index.md` and `timestamp`, link conventions, `_howto.md`, and `kvt validate` before committing.

- [ ] **Step 7: Verify**

Run: `go test ./internal/mcp ./cmd/kvt`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum cmd/kvt internal/mcp SKILL.md
git commit -m "feat: add mcp server and agent guidance"
```

---

### Task 10: Response Budgets, Push Modes, Docker, End-to-End Tests

**Files:**
- Create: `internal/responsebudget/budget.go`
- Create: `internal/responsebudget/budget_test.go`
- Create: `internal/service/push.go`
- Create: `internal/service/push_test.go`
- Create: `Dockerfile`
- Create: `compose.yaml`
- Modify: `cmd/kvt/main.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/mcp/tools.go`

**Interfaces:**
- Produces: `responsebudget.Apply(text string, cursor string, limit int) (Page, error)`
- Produces: `(*Service).Push(ctx context.Context, req PushRequest) (PushResponse, error)`
- Produces: async push worker for `off`, `on_change`, `debounced`
- Produces: `kvt push --vault <path>`

- [ ] **Step 1: Write response budget test**

```go
package responsebudget

import "testing"

func TestApplyReturnsExplicitCursor(t *testing.T) {
	page, err := Apply("abcdef", "", 3)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if page.Text != "abc" || !page.Truncated || page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}
}
```

- [ ] **Step 2: Write push integration test**

```go
package service

import "testing"

func TestManualPushFastForwardOnly(t *testing.T) {
	svc, remote := newServiceWithBareRemote(t)
	_, err := svc.Write(t.Context(), WriteRequest{Path: "notes/a.md", Content: "---\ntype: Note\ntitle: A\n---\nA\n"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	res, err := svc.Push(t.Context(), PushRequest{RemoteName: "origin"})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res.PushedCommits == 0 {
		t.Fatalf("expected pushed commits, remote=%s", remote)
	}
}

func newServiceWithBareRemote(t *testing.T) (*Service, string) {
	t.Helper()
	testutil.RequireGit(t)
	root := t.TempDir()
	remote := t.TempDir() + "/remote.git"
	runGit(t, t.TempDir(), "init", "--bare", remote)
	if _, err := Init(t.Context(), InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	runGit(t, root, "remote", "add", "origin", remote)
	cfg := config.Default()
	cfg.Git.Push = "off"
	svc, err := New(root, cfg, Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, remote
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internal/responsebudget ./internal/service ./internal/httpapi ./internal/mcp`

Expected: FAIL because push and response budget handling are incomplete.

- [ ] **Step 4: Implement response budgets**

Apply budget helpers to `kvt_log`, `kvt_history`, `kvt_grep`, `kvt_list`, and matching REST routes. Cursors are opaque base64url JSON offsets.

- [ ] **Step 5: Implement push modes**

Track commits ahead, last push time, last push error, manual push status, automatic `on_change`, and debounced push with exponential backoff. Use `git push --ff-only <remote> <branch>`.

- [ ] **Step 6: Add Docker artifacts**

`Dockerfile` builds the Go binary and installs `git` in the runtime image. `compose.yaml` runs `kvt serve --vault /workspace`, maps port 8200, bind-mounts `./vault:/workspace`, and documents optional SSH agent and embedder/LLM environment variables.

- [ ] **Step 7: Verify full test suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 8: Build binary and image**

Run: `go build ./cmd/kvt`

Expected: PASS and binary `kvt` is created in repo root.

Run: `docker build -t kvt:local .`

Expected: PASS when Docker is available. If Docker is unavailable on the host, record the command and the host error in the final verification notes.

- [ ] **Step 9: Commit**

```bash
git add cmd/kvt internal/responsebudget internal/service internal/httpapi internal/mcp Dockerfile compose.yaml
git commit -m "feat: add response budgets push modes and docker packaging"
```

---

### Task 11: Completion Audit Against `VISION.md`

**Files:**
- Create: `docs/verification/full-scope-audit.md`
- Modify: code and tests only for gaps found by this audit

**Interfaces:**
- Consumes: all implemented packages and all tests
- Produces: requirement-by-requirement audit evidence

- [ ] **Step 1: Derive requirement checklist**

Run: `rg -n "^##|^###|^- |^[0-9]+\\." VISION.md`

Expected: section and bullet outline for every explicit behavior.

- [ ] **Step 2: Write audit document**

Create `docs/verification/full-scope-audit.md` with columns:

```markdown
| Requirement | Evidence | Status |
|-------------|----------|--------|
| OKF markdown source of truth | `internal/service/service_test.go::TestWriteCommitsTimestampAndRejectsStaleBaseHash` plus filesystem inspection | complete |
```

- [ ] **Step 3: Run authoritative verification**

Run: `go test ./...`

Expected: PASS.

Run: `go build ./cmd/kvt`

Expected: PASS.

Run: `./kvt init --vault "$(mktemp -d)" --defaults`

Expected: PASS and printed initialized vault path.

- [ ] **Step 4: Close every incomplete audit row**

For each audit row whose status is not `complete`, add or fix code and tests in the owning task package, then rerun the exact package test and `go test ./...`.

- [ ] **Step 5: Commit audit and final fixes**

```bash
git add docs/verification internal cmd go.mod go.sum Dockerfile compose.yaml SKILL.md
git commit -m "test: add full scope verification audit"
```

---

## Execution Notes

Use TDD for every task:

1. Add the failing test named in the task.
2. Run the exact package test and confirm failure.
3. Implement only the code needed for that task's interfaces.
4. Run the exact package test and `go test ./...` when the task touches shared contracts.
5. Commit before moving to the next task.

When a task needs a helper that was not named here, place it in the smallest package that owns the behavior and add its public signature to that task's commit message body. Do not change transport behavior directly in CLI, REST, or MCP adapters when the behavior belongs in `internal/service`.

## Self-Review

Spec coverage:

- OKF vault, path rules, `index.md`, timestamp, and ontology validation are covered by Tasks 2, 3, 5, and 11.
- Git lifecycle, branch checks, commit semantics, history, locking, and push are covered by Tasks 4, 5, 10, and 11.
- SQLite docs/chunks/FTS/links/fields/meta and reconciliation are covered by Task 6.
- Chunking, embeddings, vector search, RRF, rerank, and degraded search are covered by Task 7.
- MCP tools, howto, prompt/resource, and repository `SKILL.md` are covered by Task 9.
- REST routes, auth, health, and CLI commands are covered by Tasks 4, 5, 8, and 10.
- Docker and operational packaging are covered by Task 10.
- Requirement-by-requirement verification is covered by Task 11.

Placeholder scan:

- The plan contains concrete task handoffs, named interfaces, exact commands, and test checkpoints.

Type consistency:

- Core interfaces use `service.Service` as the integration point.
- Path values enter domain code through `pathutil.Path`.
- Markdown content enters validation and indexing through `frontmatter.Document`.
- Transport packages consume service request and response structs instead of duplicating domain rules.
