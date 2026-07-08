package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/service"
	"github.com/ematvey/kvt/internal/testutil"
)

func TestConceptLifecycleOverHTTP(t *testing.T) {
	svc := newHTTPTestService(t, config.Default())
	handler := NewServer(svc, config.Default())

	create := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
		"path":    "people/alice.md",
		"content": "---\ntype: Person\ntitle: Alice\ndescription: DBA\n---\nBody\n",
	}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("POST status = %d body=%s", create.Code, create.Body.String())
	}
	var created map[string]any
	decodeBody(t, create, &created)
	if created["path"] != "people/alice.md" || created["hash"] == "" {
		t.Fatalf("created = %#v", created)
	}

	read := doJSON(t, handler, http.MethodGet, "/concepts/people/alice.md", nil, "")
	if read.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", read.Code, read.Body.String())
	}
	var got map[string]any
	decodeBody(t, read, &got)
	if got["path"] != "people/alice.md" || !strings.Contains(got["content"].(string), "Alice") {
		t.Fatalf("read = %#v", got)
	}

	edit := doJSON(t, handler, http.MethodPatch, "/concepts/people/alice.md", map[string]any{
		"base_hash":  created["hash"],
		"old_string": "Body",
		"new_string": "Updated body",
	}, "")
	if edit.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d body=%s", edit.Code, edit.Body.String())
	}
	var edited map[string]any
	decodeBody(t, edit, &edited)
	if !strings.Contains(edited["content"].(string), "Updated body") {
		t.Fatalf("edited = %#v", edited)
	}

	remove := doJSON(t, handler, http.MethodDelete, "/concepts/people/alice.md", map[string]any{
		"base_hash": edited["hash"],
	}, "")
	if remove.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d body=%s", remove.Code, remove.Body.String())
	}
	missing := doJSON(t, handler, http.MethodGet, "/concepts/people/alice.md", nil, "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d body=%s", missing.Code, missing.Body.String())
	}
}

func TestBearerAuthWhenConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.APIKeys = []string{"secret"}
	svc := newHTTPTestService(t, cfg)
	handler := NewServer(svc, cfg)

	unauthorized := doJSON(t, handler, http.MethodGet, "/health", nil, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	authorized := doJSON(t, handler, http.MethodGet, "/health", nil, "secret")
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d body=%s", authorized.Code, authorized.Body.String())
	}
}

func TestQueryAndMetadataRoutesOverHTTP(t *testing.T) {
	cfg := config.Default()
	svc := newHTTPTestService(t, cfg)
	handler := NewServer(svc, cfg)

	create := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
		"path":    "systems/db.md",
		"content": "---\ntype: System\ntitle: DB\ndescription: Primary database\n---\nThe primary database serves production traffic.\n",
	}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("POST status = %d body=%s", create.Code, create.Body.String())
	}

	assertOKWithKey(t, doJSON(t, handler, http.MethodGet, "/summary", nil, ""), "document_count")
	assertOKWithKey(t, doJSON(t, handler, http.MethodPost, "/search", map[string]any{"query": "primary database", "limit": 10}, ""), "hits")
	assertOKWithKey(t, doJSON(t, handler, http.MethodPost, "/grep", map[string]any{"query": "production", "limit": 10}, ""), "matches")
	assertOKWithKey(t, doJSON(t, handler, http.MethodGet, "/concepts?type=System", nil, ""), "documents")
	assertOKWithKey(t, doJSON(t, handler, http.MethodGet, "/log?limit=5", nil, ""), "entries")
	assertOKWithKey(t, doJSON(t, handler, http.MethodGet, "/history/systems/db.md?limit=5", nil, ""), "entries")
	assertOKWithKey(t, doJSON(t, handler, http.MethodPost, "/validate", map[string]any{}, ""), "errors")
	assertOKWithKey(t, doJSON(t, handler, http.MethodGet, "/types", nil, ""), "types")
}

func TestReadConceptLineRangeAndWarningsOverHTTP(t *testing.T) {
	cfg := config.Default()
	svc := newHTTPTestService(t, cfg)
	handler := NewServer(svc, cfg)

	create := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
		"path":    "notes/a.md",
		"content": "---\ntype: Mystery\ntitle: A\n---\nline one\nline two\nline three\n",
	}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("POST status = %d body=%s", create.Code, create.Body.String())
	}
	full := doJSON(t, handler, http.MethodGet, "/concepts/notes/a.md", nil, "")
	if full.Code != http.StatusOK {
		t.Fatalf("full GET status = %d body=%s", full.Code, full.Body.String())
	}
	var fullPayload map[string]any
	decodeBody(t, full, &fullPayload)
	lineTwo := 0
	for i, line := range strings.Split(fullPayload["content"].(string), "\n") {
		if line == "line two" {
			lineTwo = i + 1
			break
		}
	}
	if lineTwo == 0 {
		t.Fatalf("line two missing from %#v", fullPayload)
	}

	rangePath := "/concepts/notes/a.md?start_line=" + strconv.Itoa(lineTwo) + "&end_line=" + strconv.Itoa(lineTwo)
	ranged := doJSON(t, handler, http.MethodGet, rangePath, nil, "")
	if ranged.Code != http.StatusOK {
		t.Fatalf("range GET status = %d body=%s", ranged.Code, ranged.Body.String())
	}
	var rangedPayload map[string]any
	decodeBody(t, ranged, &rangedPayload)
	if strings.TrimSpace(rangedPayload["content"].(string)) != "line two" {
		t.Fatalf("range payload = %#v", rangedPayload)
	}
	warnings, ok := rangedPayload["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatalf("warnings = %#v", rangedPayload["warnings"])
	}

	for _, path := range []string{
		"/concepts/notes/a.md?start_line=nope",
		"/concepts/notes/a.md?start_line=-1",
		"/concepts/notes/a.md?start_line=7&end_line=6",
	} {
		res := doJSON(t, handler, http.MethodGet, path, nil, "")
		if res.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d body=%s", path, res.Code, res.Body.String())
		}
	}
}

func TestListAndGrepReturnPaginationCursor(t *testing.T) {
	cfg := config.Default()
	svc := newHTTPTestService(t, cfg)
	handler := NewServer(svc, cfg)
	for _, item := range []string{"a", "b"} {
		create := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
			"path":    "notes/" + item + ".md",
			"content": "---\ntype: Note\ntitle: " + item + "\n---\nshared body\n",
		}, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("POST %s status = %d body=%s", item, create.Code, create.Body.String())
		}
	}

	list := doJSON(t, handler, http.MethodGet, "/concepts?limit=1", nil, "")
	assertOKWithKey(t, list, "next_cursor")
	grep := doJSON(t, handler, http.MethodPost, "/grep", map[string]any{"query": "shared", "limit": 1}, "")
	assertOKWithKey(t, grep, "next_cursor")
}

func TestPushRouteOverHTTP(t *testing.T) {
	svc, _ := newHTTPServiceWithBareRemote(t)
	handler := NewServer(svc, config.Default())
	if _, err := svc.Write(t.Context(), service.WriteRequest{Path: "notes/a.md", Content: "---\ntype: Note\ntitle: A\n---\nA\n"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	res := doJSON(t, handler, http.MethodPost, "/push", map[string]any{"remote_name": "origin"}, "")
	if res.Code != http.StatusOK {
		t.Fatalf("push status = %d body=%s", res.Code, res.Body.String())
	}
	var payload map[string]any
	decodeBody(t, res, &payload)
	if payload["pushed_commits"].(float64) == 0 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestBudgetedRoutesApplyMaxResponseChars(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.MaxResponseChars = 650
	svc := newHTTPTestService(t, cfg)
	handler := NewServer(svc, cfg)
	longBody := "needle " + strings.Repeat("alpha ", 700)
	for _, item := range []string{"a", "b", "c", "d", "e", "f"} {
		create := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
			"path":    "notes/" + item + ".md",
			"content": "---\ntype: Note\ntitle: " + item + "\ndescription: " + strings.Repeat(item, 120) + "\n---\n" + longBody + "\n",
		}, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("POST %s status = %d body=%s", item, create.Code, create.Body.String())
		}
	}
	edit := doJSON(t, handler, http.MethodPatch, "/concepts/notes/a.md", map[string]any{
		"old_string": "needle",
		"new_string": "changed-needle",
	}, "")
	if edit.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d body=%s", edit.Code, edit.Body.String())
	}

	assertBudgetedHTTPResponse(t, doJSON(t, handler, http.MethodGet, "/concepts?limit=20", nil, ""), cfg.Limits.MaxResponseChars)
	assertBudgetedHTTPResponse(t, doJSON(t, handler, http.MethodPost, "/grep", map[string]any{"query": "alpha", "limit": 20}, ""), cfg.Limits.MaxResponseChars)
	assertBudgetedHTTPResponse(t, doJSON(t, handler, http.MethodGet, "/log?limit=20", nil, ""), cfg.Limits.MaxResponseChars)
	assertBudgetedHTTPResponse(t, doJSON(t, handler, http.MethodGet, "/history/notes/a.md?limit=20", nil, ""), cfg.Limits.MaxResponseChars)
}

func TestHealthAndSummaryDoNotProbeMissingPushRemote(t *testing.T) {
	cfg := config.Default()
	svc := newHTTPTestService(t, cfg)
	handler := NewServer(svc, cfg)
	if _, err := svc.Write(t.Context(), service.WriteRequest{Path: "notes/a.md", Content: "---\ntype: Note\ntitle: A\n---\nA\n"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	health := doJSON(t, handler, http.MethodGet, "/health", nil, "")
	assertOKWithEmptyPushError(t, health)
	summary := doJSON(t, handler, http.MethodGet, "/summary", nil, "")
	assertOKWithEmptyPushError(t, summary)
}

func TestInvalidCursorReturnsBadRequest(t *testing.T) {
	cfg := config.Default()
	svc := newHTTPTestService(t, cfg)
	handler := NewServer(svc, cfg)
	create := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
		"path":    "systems/db.md",
		"content": "---\ntype: System\ntitle: DB\n---\nBody\n",
	}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("POST status = %d body=%s", create.Code, create.Body.String())
	}

	log := doJSON(t, handler, http.MethodGet, "/log?cursor=bad", nil, "")
	if log.Code != http.StatusBadRequest {
		t.Fatalf("log status = %d body=%s", log.Code, log.Body.String())
	}
	history := doJSON(t, handler, http.MethodGet, "/history/systems/db.md?cursor=bad", nil, "")
	if history.Code != http.StatusBadRequest {
		t.Fatalf("history status = %d body=%s", history.Code, history.Body.String())
	}
}

func TestRESTRequestAccessRestrictsReadAndWrite(t *testing.T) {
	svc := newHTTPTestService(t, config.Default())
	handler := NewServer(svc, config.Default())

	emptyAccess := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
		"path":    "public/empty.md",
		"content": "---\ntype: Note\ntitle: Empty\n---\nA\n",
		"access":  map[string]any{},
	}, "")
	if emptyAccess.Code != http.StatusForbidden {
		t.Fatalf("empty access status = %d body=%s", emptyAccess.Code, emptyAccess.Body.String())
	}
	denied := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
		"path":    "private/a.md",
		"content": "---\ntype: Note\ntitle: A\n---\nA\n",
		"access":  map[string]any{"write_globs": []string{"public/**"}},
	}, "")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied write status = %d body=%s", denied.Code, denied.Body.String())
	}
	created := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
		"path":    "public/a.md",
		"content": "---\ntype: Note\ntitle: A\n---\nneedle\n",
		"access":  map[string]any{"write_globs": []string{"public/**"}},
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
	var listPayload map[string]any
	decodeBody(t, list, &listPayload)
	documents := listPayload["documents"].([]any)
	if len(documents) != 1 || jsonPath(t, documents[0]) != "public/a.md" {
		t.Fatalf("documents = %#v", documents)
	}
	grep := doJSON(t, handler, http.MethodPost, "/grep", map[string]any{
		"query":  "needle",
		"access": map[string]any{"read_globs": []string{"public/**"}},
	}, "")
	if grep.Code != http.StatusOK {
		t.Fatalf("grep status = %d body=%s", grep.Code, grep.Body.String())
	}
	var grepPayload map[string]any
	decodeBody(t, grep, &grepPayload)
	matches := grepPayload["matches"].([]any)
	if len(matches) != 1 || jsonPath(t, matches[0]) != "public/a.md" {
		t.Fatalf("matches = %#v", matches)
	}
	search := doJSON(t, handler, http.MethodPost, "/search", map[string]any{
		"query":  "needle",
		"access": map[string]any{"read_globs": []string{"public/**"}},
	}, "")
	if search.Code != http.StatusOK {
		t.Fatalf("search status = %d body=%s", search.Code, search.Body.String())
	}
	var searchPayload map[string]any
	decodeBody(t, search, &searchPayload)
	hits := searchPayload["hits"].([]any)
	if len(hits) != 1 || jsonPath(t, hits[0]) != "public/a.md" {
		t.Fatalf("hits = %#v", hits)
	}
	bad := doJSON(t, handler, http.MethodGet, "/concepts?read_glob=../bad/**", nil, "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad glob status = %d body=%s", bad.Code, bad.Body.String())
	}
	logAllowed := doJSON(t, handler, http.MethodGet, "/log?read_glob=public/**", nil, "")
	if logAllowed.Code != http.StatusOK {
		t.Fatalf("log status = %d body=%s", logAllowed.Code, logAllowed.Body.String())
	}
}

func TestHTTPErrorMapping(t *testing.T) {
	svc := newHTTPTestService(t, config.Default())
	handler := NewServer(svc, config.Default())
	create := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
		"path":    "notes/a.md",
		"content": "---\ntype: Note\ntitle: A\n---\nBody\n",
	}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("POST status = %d body=%s", create.Code, create.Body.String())
	}

	conflict := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
		"path":      "notes/a.md",
		"base_hash": "stale",
		"content":   "---\ntype: Note\ntitle: A\n---\nUpdated\n",
	}, "")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d body=%s", conflict.Code, conflict.Body.String())
	}
	var payload map[string]any
	decodeBody(t, conflict, &payload)
	if payload["current_hash"] == "" || payload["current_content"] == "" {
		t.Fatalf("conflict payload = %#v", payload)
	}

	badPath := doJSON(t, handler, http.MethodPost, "/concepts", map[string]any{
		"path":    "../bad.md",
		"content": "---\ntype: Note\ntitle: Bad\n---\nBody\n",
	}, "")
	if badPath.Code != http.StatusBadRequest {
		t.Fatalf("bad path status = %d body=%s", badPath.Code, badPath.Body.String())
	}
}

func jsonPath(t *testing.T, item any) string {
	t.Helper()
	object, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("item is %T, want object: %#v", item, item)
	}
	for _, key := range []string{"path", "Path"} {
		if value, ok := object[key].(string); ok {
			return value
		}
	}
	t.Fatalf("missing path field in %#v", object)
	return ""
}

func newHTTPTestService(t *testing.T, cfg config.Config) *service.Service {
	t.Helper()
	testutil.RequireGit(t)
	root := t.TempDir()
	if _, err := service.Init(t.Context(), service.InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "_ontology.yaml"), []byte("types:\n  System:\n    required: [title]\n  Person:\n    required: [title]\n  Note:\n    required: [title]\nrules: []\nunknown_types: warn\n"), 0o644); err != nil {
		t.Fatalf("write ontology: %v", err)
	}
	svc, err := service.New(root, cfg, service.Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func newHTTPServiceWithBareRemote(t *testing.T) (*service.Service, string) {
	t.Helper()
	testutil.RequireGit(t)
	root := t.TempDir()
	remote := filepath.Join(t.TempDir(), "remote.git")
	runHTTPGit(t, t.TempDir(), "init", "--bare", remote)
	if _, err := service.Init(t.Context(), service.InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	runHTTPGit(t, root, "remote", "add", "origin", remote)
	cfg := config.Default()
	cfg.Git.Push = "off"
	svc, err := service.New(root, cfg, service.Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, remote
}

func runHTTPGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func doJSON(t *testing.T, handler http.Handler, method string, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func decodeBody(t *testing.T, res *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(res.Body.Bytes(), out); err != nil {
		t.Fatalf("decode body %q: %v", res.Body.String(), err)
	}
}

func assertBudgetedHTTPResponse(t *testing.T, res *httptest.ResponseRecorder, maxChars int) {
	t.Helper()
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	if len([]rune(res.Body.String())) > maxChars {
		t.Fatalf("body length = %d, want <= %d: %s", len([]rune(res.Body.String())), maxChars, res.Body.String())
	}
	var payload map[string]any
	decodeBody(t, res, &payload)
	if payload["budget_truncated"] != true {
		t.Fatalf("expected budget_truncated in %#v", payload)
	}
	if payload["next_cursor"] == "" {
		t.Fatalf("expected next_cursor in %#v", payload)
	}
}

func assertOKWithEmptyPushError(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var payload map[string]any
	decodeBody(t, res, &payload)
	push, ok := payload["push"].(map[string]any)
	if !ok {
		t.Fatalf("missing push status in %#v", payload)
	}
	if push["last_error"] != "" {
		t.Fatalf("push status = %#v", push)
	}
}

func assertOKWithKey(t *testing.T, res *httptest.ResponseRecorder, key string) {
	t.Helper()
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var payload map[string]any
	decodeBody(t, res, &payload)
	if _, ok := payload[key]; !ok {
		t.Fatalf("missing key %q in %#v", key, payload)
	}
}
