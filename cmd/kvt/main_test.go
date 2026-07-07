package main

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/index"
	"github.com/ematvey/kvt/internal/service"
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

func TestRunInitWithDefaults(t *testing.T) {
	root := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"kvt", "init", "--vault", root, "--defaults"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".kvt", "config.yaml")); err != nil {
		t.Fatalf("config missing: %v", err)
	}
}

func TestRunInitUsesKVTVaultFallback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KVT_VAULT", root)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"kvt", "init", "--defaults"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".kvt", "config.yaml")); err != nil {
		t.Fatalf("config missing: %v", err)
	}
}

func TestRunValidateReturnsNonZeroWhenValidationFails(t *testing.T) {
	root := t.TempDir()
	if _, err := service.Init(t.Context(), service.InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes", "bad.md"), []byte("Body without type\n"), 0o644); err != nil {
		t.Fatalf("write bad doc: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"kvt", "validate", "--vault", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("notes/bad.md")) {
		t.Fatalf("expected path in validation output: %q", stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("missing required field")) {
		t.Fatalf("expected validation detail in output: %q", stderr.String())
	}
}

func TestRunReindexRebuildsDerivedIndex(t *testing.T) {
	root := t.TempDir()
	if _, err := service.Init(t.Context(), service.InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "systems"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "systems", "db.md"), []byte("---\ntype: System\ntitle: DB\n---\nPrimary database\n"), 0o644); err != nil {
		t.Fatalf("write concept: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"kvt", "reindex", "--vault", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	corruptFTSRows(t, root, "systems/db.md")

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"kvt", "reindex", "--vault", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("second exit code = %d, stderr = %q", code, stderr.String())
	}

	cfg, err := config.Load(root, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	svc, err := service.New(root, cfg, service.Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	list, err := svc.List(t.Context(), index.ListRequest{Type: "System"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Documents) != 1 || list.Documents[0].Path != "systems/db.md" {
		t.Fatalf("documents = %#v", list.Documents)
	}
	grep, err := svc.Grep(t.Context(), index.GrepRequest{Query: "Primary", Limit: 10})
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if len(grep.Matches) != 1 || grep.Matches[0].Path != "systems/db.md" {
		t.Fatalf("grep matches = %#v", grep.Matches)
	}
}

func TestRunServeRejectsUninitializedVault(t *testing.T) {
	root := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"kvt", "serve", "--vault", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "kvt init") {
		t.Fatalf("expected init guidance, stderr = %q", stderr.String())
	}
}

func TestServeHandlerMountsStreamableMCP(t *testing.T) {
	root := t.TempDir()
	if _, err := service.Init(t.Context(), service.InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg := config.Default()
	cfg.Server.MCPTransport = "streamable-http"
	svc, err := service.New(root, cfg, service.Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	handler, err := buildServeHandler(svc, cfg)
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if res.Code == http.StatusNotFound {
		t.Fatalf("mcp route was not mounted")
	}
}

func TestServeHandlerProtectsStreamableMCPWithBearerAuth(t *testing.T) {
	root := t.TempDir()
	if _, err := service.Init(t.Context(), service.InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg := config.Default()
	cfg.Server.MCPTransport = "streamable-http"
	cfg.Auth.APIKeys = []string{"secret"}
	svc, err := service.New(root, cfg, service.Deps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	handler, err := buildServeHandler(svc, cfg)
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	authorizedReq := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	authorizedReq.Header.Set("Authorization", "Bearer secret")
	authorized := httptest.NewRecorder()
	handler.ServeHTTP(authorized, authorizedReq)
	if authorized.Code == http.StatusUnauthorized || authorized.Code == http.StatusNotFound {
		t.Fatalf("authorized status = %d body=%s", authorized.Code, authorized.Body.String())
	}
}

func corruptFTSRows(t *testing.T, root string, docPath string) {
	t.Helper()
	dbPath := filepath.Join(root, ".kvt", "index.db")
	dsn := url.URL{Scheme: "file", Path: dbPath}
	db, err := sql.Open("sqlite3", dsn.String())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM kb_fts WHERE path = ?`, docPath); err != nil {
		t.Fatalf("corrupt fts rows: %v", err)
	}
}
