# Request-Scoped Glob Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add request-scoped glob access controls to both REST and MCP so callers can restrict which vault paths each operation may read or mutate.

**Architecture:** Create a small `internal/access` package for glob parsing and policy checks. Thread `*access.Policy` through service request structs so REST and MCP share enforcement. REST and MCP only parse request params; the service layer decides whether a path is readable or writable and filters read-like result sets.

**Tech Stack:** Go 1.25.0, `net/http`, `database/sql`, `github.com/modelcontextprotocol/go-sdk/mcp`, `gopkg.in/yaml.v3`.

## Global Constraints

- Request `access` is a sandbox, not authenticated server-side ACLs.
- Missing `access` means current unrestricted behavior.
- Present but empty `access` means no read/write access.
- `deny_globs` wins over `read_globs` and `write_globs`.
- Glob patterns match normalized bundle-relative paths.
- `*` and `?` match within one segment; `**` matches across path separators.
- REST invalid glob syntax returns `400 Bad Request`.
- REST denied access returns `403 Forbidden`.
- MCP uses the same service policy as REST.
- Do not add `kvt_push`.
- Preserve existing untracked `README.md` and `AGENTS.md` unless updating docs in the final task.

---

## File Structure

- Create `internal/access/policy.go`: `Policy`, `New`, `CheckRead`, `CheckWrite`, `Filter*`, glob compiler, and access errors.
- Create `internal/access/policy_test.go`: pure unit tests for glob semantics and policy behavior.
- Modify `internal/service/types.go`: add `Access *access.Policy` to request structs and add service-level `ListRequest`/`GrepRequest`.
- Modify `internal/service/read.go`, `write.go`, `index.go`, `ops.go`: enforce read/write policy and filter read-like outputs.
- Modify `internal/httpapi/server.go`: parse JSON/query access params and map access errors to HTTP status.
- Modify `internal/httpapi/server_test.go`: REST access tests.
- Modify `internal/mcp/tools.go`: add `access` input fields and pass policies to service.
- Modify `internal/mcp/server_test.go`: MCP access tests.
- Modify `README.md`, `AGENTS.md`: document request-scoped glob access after implementation.

---

### Task 1: Core Access Policy

**Files:**
- Create: `internal/access/policy.go`
- Create: `internal/access/policy_test.go`

**Interfaces:**
- Produces: `type Policy struct`
- Produces: `func New(readGlobs, writeGlobs, denyGlobs []string) (*Policy, error)`
- Produces: `func CheckRead(policy *Policy, path string) error`
- Produces: `func CheckWrite(policy *Policy, path string) error`
- Produces: `func CanRead(policy *Policy, path string) bool`
- Produces: `func CanWrite(policy *Policy, path string) bool`
- Produces: `func FilterStrings(paths []string, policy *Policy, mode Mode) []string`
- Produces: `var ErrDenied`
- Produces: `func IsDenied(err error) bool`
- Produces: `func LogAllowed(policy *Policy) bool`

- [ ] **Step 1: Write failing glob and policy tests**

Add `internal/access/policy_test.go`:

```go
package access

import (
	"errors"
	"testing"
)

func TestGlobSemantics(t *testing.T) {
	policy, err := New([]string{"notes/*.md", "public/**", "**/*.md"}, []string{"drafts/**"}, []string{"public/secrets/**"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tests := []struct {
		name      string
		path      string
		readable  bool
		writable  bool
	}{
		{name: "single segment star", path: "notes/a.md", readable: true},
		{name: "star does not cross slash", path: "notes/archive/a.md", readable: true},
		{name: "double star crosses slash", path: "public/archive/a.md", readable: true},
		{name: "deny wins", path: "public/secrets/a.md", readable: false},
		{name: "write double star", path: "drafts/one/two.md", writable: true},
		{name: "read does not imply write", path: "notes/a.md", readable: true, writable: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanRead(policy, tt.path); got != tt.readable {
				t.Fatalf("CanRead(%q) = %v, want %v", tt.path, got, tt.readable)
			}
			if got := CanWrite(policy, tt.path); got != tt.writable {
				t.Fatalf("CanWrite(%q) = %v, want %v", tt.path, got, tt.writable)
			}
		})
	}
}

func TestMissingPolicyIsUnrestrictedAndEmptyPolicyDenies(t *testing.T) {
	if err := CheckRead(nil, "anything.md"); err != nil {
		t.Fatalf("nil read: %v", err)
	}
	if err := CheckWrite(nil, "anything.md"); err != nil {
		t.Fatalf("nil write: %v", err)
	}
	empty, err := New(nil, nil, nil)
	if err != nil {
		t.Fatalf("New empty: %v", err)
	}
	if err := CheckRead(empty, "anything.md"); !IsDenied(err) {
		t.Fatalf("empty read err = %v", err)
	}
	if err := CheckWrite(empty, "anything.md"); !IsDenied(err) {
		t.Fatalf("empty write err = %v", err)
	}
}

func TestInvalidGlobRejected(t *testing.T) {
	for _, bad := range []string{"/absolute/**", "../secret/**", "a//b", "bad["} {
		if _, err := New([]string{bad}, nil, nil); err == nil {
			t.Fatalf("New(%q) succeeded", bad)
		}
	}
}

func TestFilterStringsAndLogAllowed(t *testing.T) {
	policy, err := New([]string{"notes/**"}, nil, []string{"notes/private/**"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := FilterStrings([]string{"notes/a.md", "notes/private/a.md", "systems/db.md"}, policy, Read)
	if len(got) != 1 || got[0] != "notes/a.md" {
		t.Fatalf("filtered = %#v", got)
	}
	if LogAllowed(policy) {
		t.Fatalf("restricted policy should not allow log")
	}
	unrestricted, err := New([]string{"**"}, nil, nil)
	if err != nil {
		t.Fatalf("New unrestricted: %v", err)
	}
	if !LogAllowed(unrestricted) {
		t.Fatalf("unrestricted read should allow log")
	}
	if !LogAllowed(nil) {
		t.Fatalf("missing policy should preserve log behavior")
	}
	if !errors.Is(ErrDenied, ErrDenied) {
		t.Fatalf("ErrDenied sanity")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/access
```

Expected: fail because `internal/access` does not exist.

- [ ] **Step 3: Implement `internal/access/policy.go`**

Create `internal/access/policy.go`:

```go
package access

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

type Mode int

const (
	Read Mode = iota
	Write
)

var ErrDenied = errors.New("access denied")

type Policy struct {
	readGlobs  []compiledGlob
	writeGlobs []compiledGlob
	denyGlobs  []compiledGlob
	readRaw     []string
	writeRaw    []string
	denyRaw     []string
}

type compiledGlob struct {
	raw string
	re  *regexp.Regexp
}

func New(readGlobs, writeGlobs, denyGlobs []string) (*Policy, error) {
	read, readRaw, err := compileGlobs(readGlobs)
	if err != nil {
		return nil, err
	}
	write, writeRaw, err := compileGlobs(writeGlobs)
	if err != nil {
		return nil, err
	}
	deny, denyRaw, err := compileGlobs(denyGlobs)
	if err != nil {
		return nil, err
	}
	return &Policy{
		readGlobs:  read,
		writeGlobs: write,
		denyGlobs:  deny,
		readRaw:     readRaw,
		writeRaw:    writeRaw,
		denyRaw:     denyRaw,
	}, nil
}

func CheckRead(policy *Policy, path string) error {
	if CanRead(policy, path) {
		return nil
	}
	return fmt.Errorf("%w: read %s", ErrDenied, path)
}

func CheckWrite(policy *Policy, path string) error {
	if CanWrite(policy, path) {
		return nil
	}
	return fmt.Errorf("%w: write %s", ErrDenied, path)
}

func CanRead(policy *Policy, path string) bool {
	return allowed(policy, path, Read)
}

func CanWrite(policy *Policy, path string) bool {
	return allowed(policy, path, Write)
}

func FilterStrings(paths []string, policy *Policy, mode Mode) []string {
	if policy == nil {
		return append([]string(nil), paths...)
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		switch mode {
		case Read:
			if CanRead(policy, path) {
				out = append(out, path)
			}
		case Write:
			if CanWrite(policy, path) {
				out = append(out, path)
			}
		}
	}
	return out
}

func LogAllowed(policy *Policy) bool {
	if policy == nil {
		return true
	}
	if len(policy.denyGlobs) != 0 {
		return false
	}
	return len(policy.readRaw) == 1 && policy.readRaw[0] == "**"
}

func IsDenied(err error) bool {
	return errors.Is(err, ErrDenied)
}

func allowed(policy *Policy, candidate string, mode Mode) bool {
	if policy == nil {
		return true
	}
	candidate = normalizeCandidate(candidate)
	if candidate == "" {
		return false
	}
	if matchesAny(policy.denyGlobs, candidate) {
		return false
	}
	switch mode {
	case Read:
		return matchesAny(policy.readGlobs, candidate)
	case Write:
		return matchesAny(policy.writeGlobs, candidate)
	default:
		return false
	}
}

func matchesAny(globs []compiledGlob, candidate string) bool {
	for _, glob := range globs {
		if glob.re.MatchString(candidate) {
			return true
		}
	}
	return false
}

func compileGlobs(values []string) ([]compiledGlob, []string, error) {
	out := make([]compiledGlob, 0, len(values))
	raw := make([]string, 0, len(values))
	for _, value := range values {
		normalized, err := normalizePattern(value)
		if err != nil {
			return nil, nil, err
		}
		re, err := regexp.Compile(globRegexp(normalized))
		if err != nil {
			return nil, nil, fmt.Errorf("invalid glob %q: %w", value, err)
		}
		out = append(out, compiledGlob{raw: normalized, re: re})
		raw = append(raw, normalized)
	}
	return out, raw, nil
}

func normalizePattern(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return "", fmt.Errorf("glob is empty")
	}
	if strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("glob %q must be bundle-relative", value)
	}
	if strings.HasPrefix(value, "./") || strings.Contains(value, "/./") ||
		strings.Contains(value, "/../") || strings.HasPrefix(value, "../") ||
		strings.HasSuffix(value, "/.") || strings.HasSuffix(value, "/..") ||
		strings.Contains(value, "//") {
		return "", fmt.Errorf("glob %q must be canonical", value)
	}
	if _, err := regexp.Compile(globRegexp(value)); err != nil {
		return "", fmt.Errorf("invalid glob %q: %w", value, err)
	}
	return value, nil
}

func normalizeCandidate(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "//") {
		return ""
	}
	cleaned := path.Clean(value)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return ""
	}
	return cleaned
}

func globRegexp(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch == '*' {
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
				continue
			}
			b.WriteString("[^/]*")
			continue
		}
		if ch == '?' {
			b.WriteString("[^/]")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(ch)))
	}
	b.WriteString("$")
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
go test ./internal/access
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/access
git commit -m "feat: add request access policy"
```

---

### Task 2: Service Enforcement

**Files:**
- Modify: `internal/service/types.go`
- Modify: `internal/service/read.go`
- Modify: `internal/service/write.go`
- Modify: `internal/service/index.go`
- Modify: `internal/service/ops.go`
- Modify: `internal/service/service_test.go`

**Interfaces:**
- Consumes: `*access.Policy`, `access.CheckRead`, `access.CheckWrite`, `access.CanRead`, `access.LogAllowed`.
- Produces: `service.ListRequest`, `service.GrepRequest`.

- [ ] **Step 1: Write failing service tests**

Append tests to `internal/service/service_test.go`:

```go
func TestAccessPolicyRestrictsReadAndWrite(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	writePolicy, err := access.New(nil, []string{"drafts/**"}, nil)
	if err != nil {
		t.Fatalf("access.New: %v", err)
	}
	readPolicy, err := access.New([]string{"drafts/**"}, nil, nil)
	if err != nil {
		t.Fatalf("access.New: %v", err)
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
	if _, err := h.service.Read(t.Context(), ReadRequest{Path: "drafts/a.md", Access: readPolicy}); err != nil {
		t.Fatalf("read allowed: %v", err)
	}
	deniedRead, _ := access.New([]string{"notes/**"}, nil, nil)
	if _, err := h.service.Read(t.Context(), ReadRequest{Path: written.Path, Access: deniedRead}); !access.IsDenied(err) {
		t.Fatalf("read outside policy err = %v", err)
	}
}

func TestAccessPolicyFiltersDiscoveryToolsAndHistory(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarness(t)
	for _, item := range []struct{ path, title string }{
		{"public/a.md", "Public"},
		{"private/a.md", "Private"},
	} {
		if _, err := h.service.Write(t.Context(), WriteRequest{
			Path:    item.path,
			Content: "---\ntype: Note\ntitle: " + item.title + "\n---\nshared needle\n",
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
	if _, err := h.service.History(t.Context(), HistoryRequest{Path: "private/a.md", Access: policy}); !access.IsDenied(err) {
		t.Fatalf("history outside policy err = %v", err)
	}
	if _, err := h.service.Log(t.Context(), LogRequest{Access: policy}); !access.IsDenied(err) {
		t.Fatalf("restricted log err = %v", err)
	}
	unrestricted, _ := access.New([]string{"**"}, nil, nil)
	if _, err := h.service.Log(t.Context(), LogRequest{Access: unrestricted, Limit: 1}); err != nil {
		t.Fatalf("unrestricted log: %v", err)
	}
}
```

Add import in `internal/service/service_test.go`:

```go
"github.com/ematvey/kvt/internal/access"
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/service -run 'TestAccessPolicyRestrictsReadAndWrite|TestAccessPolicyFiltersDiscoveryToolsAndHistory'
```

Expected: fail because request structs and service methods do not expose access policy.

- [ ] **Step 3: Add access fields and service request wrappers**

In `internal/service/types.go`, import access:

```go
	"github.com/ematvey/kvt/internal/access"
```

Update request structs:

```go
type ReadRequest struct {
	Path      string
	StartLine int
	EndLine   int
	Access    *access.Policy
}

type WriteRequest struct {
	Path           string
	Content        string
	BaseHash       string
	Agent          string
	ValidationMode ValidationMode
	Access         *access.Policy
}

type EditRequest struct {
	Path           string
	BaseHash       string
	OldString      string
	NewString      string
	ReplaceAll     bool
	Agent          string
	ValidationMode ValidationMode
	Access         *access.Policy
}

type DeleteRequest struct {
	Path     string
	BaseHash string
	Agent    string
	Access   *access.Policy
}

type SearchRequest struct {
	Query      string
	PathPrefix string
	Limit      int
	Access     *access.Policy
}

type ListRequest struct {
	Type       string
	PathPrefix string
	FieldKey   string
	FieldValue string
	Limit      int
	Cursor     string
	Access     *access.Policy
}

type GrepRequest struct {
	Query      string
	PathPrefix string
	Limit      int
	Cursor     string
	Access     *access.Policy
}
```

In `internal/service/ops.go`, add `Access *access.Policy` to `LogRequest` and `HistoryRequest`.

- [ ] **Step 4: Enforce read/write path checks**

In `internal/service/read.go`, after `normalizeConceptPath`:

```go
if err := access.CheckRead(req.Access, docPath.String()); err != nil {
	return ReadResponse{}, err
}
```

In `internal/service/write.go`, after each `normalizeConceptPath` in `Write`, `Edit`, and `Delete`:

```go
if err := access.CheckWrite(req.Access, docPath.String()); err != nil {
	return WriteResponse{}, err
}
```

For `Delete`, return `DeleteResponse{}` on error.

- [ ] **Step 5: Enforce discovery filtering and log/history checks**

In `internal/service/index.go`, change signatures:

```go
func (s *Service) List(ctx context.Context, req ListRequest) (index.ListResponse, error)
func (s *Service) Grep(ctx context.Context, req GrepRequest) (index.GrepResponse, error)
```

Call the index package, then filter:

```go
func (s *Service) List(ctx context.Context, req ListRequest) (index.ListResponse, error) {
	resp, err := s.index.List(ctx, index.ListRequest{
		Type:       req.Type,
		PathPrefix: req.PathPrefix,
		FieldKey:   req.FieldKey,
		FieldValue: req.FieldValue,
		Limit:      req.Limit,
		Cursor:     req.Cursor,
	})
	if err != nil {
		return index.ListResponse{}, err
	}
	if req.Access == nil {
		return resp, nil
	}
	filtered := resp.Documents[:0]
	for _, doc := range resp.Documents {
		if access.CanRead(req.Access, doc.Path) {
			filtered = append(filtered, doc)
		}
	}
	resp.Documents = filtered
	return resp, nil
}
```

Use the same pattern for `Grep` and `Search` hits. For `Search`, filter `resp.Hits` after conversion from `searchpkg.Search`.

In `internal/service/ops.go`, enforce:

```go
if !access.LogAllowed(req.Access) {
	return gitops.LogPage{}, fmt.Errorf("%w: log requires unrestricted read access", access.ErrDenied)
}
```

and in `History`, after normalizing:

```go
if err := access.CheckRead(req.Access, docPath.String()); err != nil {
	return gitops.HistoryPage{}, err
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run:

```bash
gofmt -w internal/service/types.go internal/service/read.go internal/service/write.go internal/service/index.go internal/service/ops.go internal/service/service_test.go
go test ./internal/access ./internal/service -run 'TestAccessPolicy|TestGlob|TestMissing|TestInvalid|TestFilter'
```

Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add internal/access internal/service
git commit -m "feat: enforce request access in service"
```

---

### Task 3: REST Access Parameters

**Files:**
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/server_test.go`

**Interfaces:**
- Consumes: `access.New`
- Consumes: service request `Access` fields.
- Produces: JSON body field `access`.
- Produces: GET query params `read_glob`, `write_glob`, `deny_glob`.

- [ ] **Step 1: Write failing REST tests**

Append to `internal/httpapi/server_test.go`:

```go
func TestRESTRequestAccessRestrictsReadAndWrite(t *testing.T) {
	svc := newHTTPTestService(t, config.Default())
	handler := NewServer(svc, config.Default())

	denied := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
		"path":    "private/a.md",
		"content": "---\ntype: Note\ntitle: A\n---\nA\n",
		"access": map[string]any{"write_globs": []string{"public/**"}},
	}, "")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied write status = %d body=%s", denied.Code, denied.Body.String())
	}
	created := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
		"path":    "public/a.md",
		"content": "---\ntype: Note\ntitle: A\n---\nneedle\n",
		"access": map[string]any{"write_globs": []string{"public/**"}},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("created status = %d body=%s", created.Code, created.Body.String())
	}
	readDenied := doJSON(t, handler, http.MethodGet, "/concepts/public/a.md?read_glob=private/**", nil, "")
	if readDenied.Code != http.StatusForbidden {
		t.Fatalf("read denied status = %d body=%s", readDenied.Code, readDenied.Body.String())
	}
	readAllowed := doJSON(t, handler, http.MethodGet, "/concepts/public/a.md?read_glob=public/**", nil, "")
	if readAllowed.Code != http.StatusOK {
		t.Fatalf("read allowed status = %d body=%s", readAllowed.Code, readAllowed.Body.String())
	}
}

func TestRESTRequestAccessFiltersDiscoveryAndRejectsInvalidGlob(t *testing.T) {
	svc := newHTTPTestService(t, config.Default())
	handler := NewServer(svc, config.Default())
	for _, path := range []string{"public/a.md", "private/a.md"} {
		res := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
			"path":    path,
			"content": "---\ntype: Note\ntitle: A\n---\nneedle\n",
		}, "")
		if res.Code != http.StatusCreated {
			t.Fatalf("POST %s status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
	list := doJSON(t, handler, http.MethodGet, "/concepts?read_glob=public/**", nil, "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", list.Code, list.Body.String())
	}
	var payload map[string]any
	decodeBody(t, list, &payload)
	documents := payload["documents"].([]any)
	if len(documents) != 1 || documents[0].(map[string]any)["Path"] == "private/a.md" {
		t.Fatalf("documents = %#v", documents)
	}
	grep := doJSON(t, handler, http.MethodPost, "/grep", map[string]any{
		"query":  "needle",
		"access": map[string]any{"read_globs": []string{"public/**"}},
	}, "")
	if grep.Code != http.StatusOK {
		t.Fatalf("grep status = %d body=%s", grep.Code, grep.Body.String())
	}
	bad := doJSON(t, handler, http.MethodGet, "/concepts?read_glob=../bad/**", nil, "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad glob status = %d body=%s", bad.Code, bad.Body.String())
	}
	logDenied := doJSON(t, handler, http.MethodGet, "/log?read_glob=public/**", nil, "")
	if logDenied.Code != http.StatusForbidden {
		t.Fatalf("log denied status = %d body=%s", logDenied.Code, logDenied.Body.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/httpapi -run 'TestRESTRequestAccess'
```

Expected: fail because REST does not parse `access`.

- [ ] **Step 3: Add REST access parsing**

In `internal/httpapi/server.go`, import access:

```go
"github.com/ematvey/kvt/internal/access"
```

Add request struct:

```go
type accessRequest struct {
	ReadGlobs  []string `json:"read_globs"`
	WriteGlobs []string `json:"write_globs"`
	DenyGlobs  []string `json:"deny_globs"`
}
```

Add `Access *accessRequest` to `writeRequest`, `editRequest`, `deleteRequest`, and `searchRequest`.

Add helpers:

```go
func policyFromBody(in *accessRequest) (*access.Policy, error) {
	if in == nil {
		return nil, nil
	}
	return access.New(in.ReadGlobs, in.WriteGlobs, in.DenyGlobs)
}

func policyFromQuery(r *http.Request) (*access.Policy, error) {
	query := r.URL.Query()
	has := false
	for _, key := range []string{"read_glob", "write_glob", "deny_glob"} {
		if len(query[key]) > 0 {
			has = true
		}
	}
	if !has {
		return nil, nil
	}
	return access.New(query["read_glob"], query["write_glob"], query["deny_glob"])
}
```

Add:

```go
func writePolicyError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	writeError(w, http.StatusBadRequest, err.Error(), nil)
	return true
}
```

Use `policyFromBody` for POST/PATCH/DELETE bodies and `policyFromQuery` for GET routes. Pass the policy into service requests.

In `writeServiceError`, before the default case:

```go
if access.IsDenied(err) {
	writeError(w, http.StatusForbidden, err.Error(), nil)
	return
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
gofmt -w internal/httpapi/server.go internal/httpapi/server_test.go
go test ./internal/httpapi -run 'TestRESTRequestAccess'
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi
git commit -m "feat: add request access to rest api"
```

---

### Task 4: MCP Access Parameters

**Files:**
- Modify: `internal/mcp/tools.go`
- Modify: `internal/mcp/server_test.go`

**Interfaces:**
- Consumes: `access.New`
- Consumes: service request `Access` fields.
- Produces: nested MCP input field `access`.

- [ ] **Step 1: Write failing MCP tests**

Append to `internal/mcp/server_test.go`:

```go
func TestMCPRequestAccessRestrictsReadAndWrite(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)
	denied := callToolResult(t, session, "kvt_write", map[string]any{
		"path":    "private/a.md",
		"content": "---\ntype: Note\ntitle: A\n---\nA\n",
		"access":  map[string]any{"write_globs": []string{"public/**"}},
	})
	if !denied.IsError {
		t.Fatalf("denied write result = %#v", denied)
	}
	callToolMap(t, session, "kvt_write", map[string]any{
		"path":    "public/a.md",
		"content": "---\ntype: Note\ntitle: A\n---\nneedle\n",
		"access":  map[string]any{"write_globs": []string{"public/**"}},
	})
	readDenied := callToolResult(t, session, "kvt_read", map[string]any{
		"path":   "public/a.md",
		"access": map[string]any{"read_globs": []string{"private/**"}},
	})
	if !readDenied.IsError {
		t.Fatalf("read denied result = %#v", readDenied)
	}
	readAllowed := callToolMap(t, session, "kvt_read", map[string]any{
		"path":   "public/a.md",
		"access": map[string]any{"read_globs": []string{"public/**"}},
	})
	if readAllowed["path"] != "public/a.md" {
		t.Fatalf("read allowed = %#v", readAllowed)
	}
}

func TestMCPRequestAccessFiltersListAndRejectsLog(t *testing.T) {
	svc := newMCPTestService(t)
	server, err := NewServer(svc, openConfig())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	session := connectMCPClient(t, server)
	for _, path := range []string{"public/a.md", "private/a.md"} {
		callToolMap(t, session, "kvt_write", map[string]any{
			"path":    path,
			"content": "---\ntype: Note\ntitle: A\n---\nneedle\n",
		})
	}
	list := callToolMap(t, session, "kvt_list", map[string]any{
		"access": map[string]any{"read_globs": []string{"public/**"}},
	})
	docs := list["documents"].([]any)
	if len(docs) != 1 || docs[0].(map[string]any)["path"] != "public/a.md" {
		t.Fatalf("docs = %#v", docs)
	}
	logDenied := callToolResult(t, session, "kvt_log", map[string]any{
		"access": map[string]any{"read_globs": []string{"public/**"}},
	})
	if !logDenied.IsError {
		t.Fatalf("log denied = %#v", logDenied)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/mcp -run 'TestMCPRequestAccess'
```

Expected: fail because MCP inputs ignore `access`.

- [ ] **Step 3: Add MCP access input parsing**

In `internal/mcp/tools.go`, import access:

```go
"github.com/ematvey/kvt/internal/access"
```

Add:

```go
type accessInput struct {
	ReadGlobs  []string `json:"read_globs,omitempty" jsonschema:"read allow glob patterns"`
	WriteGlobs []string `json:"write_globs,omitempty" jsonschema:"write allow glob patterns"`
	DenyGlobs  []string `json:"deny_globs,omitempty" jsonschema:"deny glob patterns"`
}

func accessPolicyFromInput(in *accessInput) (*access.Policy, error) {
	if in == nil {
		return nil, nil
	}
	return access.New(in.ReadGlobs, in.WriteGlobs, in.DenyGlobs)
}
```

Add `Access *accessInput` to input structs used by `search`, `grep`,
`list`, `path`, `page`, `history`, `write`, `edit`, `delete`.

In each tool handler that calls service, call `accessPolicyFromInput`
and pass `Access: policy`.

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
gofmt -w internal/mcp/tools.go internal/mcp/server_test.go
go test ./internal/mcp -run 'TestMCPRequestAccess'
```

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp
git commit -m "feat: add request access to mcp tools"
```

---

### Task 5: Documentation and Final Verification

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Optionally modify: `docs/verification/full-scope-audit.md`

**Interfaces:**
- Consumes implemented REST/MCP access behavior.

- [ ] **Step 1: Update README**

Add a section under REST/MCP:

````markdown
## Request-Scoped Access

REST and MCP calls can include an optional request-scoped `access`
object to narrow which paths the operation may read or write.

```json
{
  "access": {
    "read_globs": ["public/**"],
    "write_globs": ["drafts/**"],
    "deny_globs": ["secrets/**"]
  }
}
```

GET routes use repeated query params:

```text
?read_glob=public/**&deny_glob=secrets/**
```

Missing `access` preserves normal unrestricted behavior. Present but
empty `access` denies read/write access. `deny_globs` wins over allow
globs. This is request sandboxing, not authenticated server-side ACLs.
````

- [ ] **Step 2: Update AGENTS.md**

Add under API Notes:

```markdown
- Request-scoped access params use glob fields `read_globs`,
  `write_globs`, and `deny_globs`. Missing access means unrestricted;
  explicit empty access denies read/write. Keep REST and MCP behavior
  routed through the shared service policy.
```

- [ ] **Step 3: Run full verification**

Run:

```bash
go test ./...
go vet ./...
go build ./cmd/kvt
tmp=$(mktemp -d)
./kvt init --vault "$tmp" --defaults
rm -f ./kvt
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 4: Commit**

```bash
git add README.md AGENTS.md docs/verification/full-scope-audit.md
git commit -m "docs: document request scoped access"
```
