package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
