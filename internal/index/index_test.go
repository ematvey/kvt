package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyDocumentIndexesFTSFieldsAndLinks(t *testing.T) {
	db := openTempDB(t)
	err := db.ApplyDocument(t.Context(), IndexedDocument{
		Path:        "people/alice.md",
		Hash:        "h1",
		Title:       "Alice",
		Type:        "Person",
		Description: "DBA",
		Fields: map[string][]string{
			"tag": {"dba"},
		},
		Chunks: []Chunk{
			{Ordinal: 0, Text: "title Alice type Person"},
			{Ordinal: 1, Text: "Alice owns the primary database"},
		},
		Links: []Link{
			{FromPath: "people/alice.md", ToPath: "systems/db.md", Kind: "body"},
		},
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
	if grep.Matches[0].Path != "people/alice.md" {
		t.Fatalf("path = %q", grep.Matches[0].Path)
	}

	list, err := db.List(t.Context(), ListRequest{Type: "Person"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Documents) != 1 || list.Documents[0].Path != "people/alice.md" {
		t.Fatalf("list = %#v", list.Documents)
	}

	backlinks, err := db.Backlinks(t.Context(), "systems/db.md")
	if err != nil {
		t.Fatalf("Backlinks: %v", err)
	}
	if len(backlinks) != 1 || backlinks[0].FromPath != "people/alice.md" {
		t.Fatalf("backlinks = %#v", backlinks)
	}
}

func TestSchemaUsesFTS5(t *testing.T) {
	db := openTempDB(t)

	var sql string
	if err := db.sql.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'kb_fts'`).Scan(&sql); err != nil {
		t.Fatalf("schema query: %v", err)
	}
	if !strings.Contains(strings.ToLower(sql), "fts5") {
		t.Fatalf("kb_fts schema = %q", sql)
	}
}

func TestVectorSearchStatementUsesSQLiteVecKNNShape(t *testing.T) {
	query, args := vectorSearchStatement([]float32{1, 0}, 25)
	normalized := strings.ToLower(query)
	if !strings.Contains(normalized, "v.embedding match ?") {
		t.Fatalf("query missing vector match:\n%s", query)
	}
	if !strings.Contains(normalized, "k = ?") {
		t.Fatalf("query missing k constraint:\n%s", query)
	}
	if strings.Contains(normalized, " limit ") {
		t.Fatalf("query should not add LIMIT to vec0 KNN search:\n%s", query)
	}
	if strings.Contains(normalized, " like ") {
		t.Fatalf("query should not filter vec0 metadata with LIKE:\n%s", query)
	}
	if strings.Count(normalized, "order by") != 1 || !strings.Contains(normalized, "order by v.distance asc") {
		t.Fatalf("query should order only by distance:\n%s", query)
	}
	if len(args) != 2 || args[0] != "[1,0]" || args[1] != 25 {
		t.Fatalf("args = %#v", args)
	}
}

func TestApplyDocumentClearsExistingVectorRows(t *testing.T) {
	db := openTempDB(t)
	createFakeVectorTable(t, db)
	db.vecAvailable = true
	insertFakeVectorRow(t, db, "people/alice.md", 0)
	insertFakeVectorRow(t, db, "people/bob.md", 0)

	if err := db.ApplyDocument(t.Context(), IndexedDocument{
		Path:  "people/alice.md",
		Hash:  "h1",
		Title: "Alice",
		Type:  "Person",
		Chunks: []Chunk{
			{Ordinal: 0, Text: "Alice owns the primary database"},
		},
	}); err != nil {
		t.Fatalf("ApplyDocument: %v", err)
	}

	if got := fakeVectorRowCount(t, db, "people/alice.md"); got != 0 {
		t.Fatalf("alice vector rows = %d", got)
	}
	if got := fakeVectorRowCount(t, db, "people/bob.md"); got != 1 {
		t.Fatalf("bob vector rows = %d", got)
	}
}

func TestRemoveDocumentDeletesRowsButPreservesInboundLinks(t *testing.T) {
	db := openTempDB(t)
	if err := db.ApplyDocument(t.Context(), IndexedDocument{
		Path:  "people/alice.md",
		Hash:  "h1",
		Title: "Alice",
		Type:  "Person",
		Chunks: []Chunk{
			{Ordinal: 0, Text: "Alice owns the primary database"},
		},
		Links: []Link{
			{FromPath: "people/alice.md", ToPath: "systems/db.md", Kind: "body"},
		},
	}); err != nil {
		t.Fatalf("ApplyDocument: %v", err)
	}
	if err := db.ApplyDocument(t.Context(), IndexedDocument{
		Path:  "systems/db.md",
		Hash:  "h2",
		Title: "DB",
		Type:  "System",
		Chunks: []Chunk{
			{Ordinal: 0, Text: "postgres cluster"},
		},
	}); err != nil {
		t.Fatalf("Apply target document: %v", err)
	}

	if err := db.RemoveDocument(t.Context(), "systems/db.md"); err != nil {
		t.Fatalf("RemoveDocument: %v", err)
	}

	grep, err := db.Grep(t.Context(), GrepRequest{Query: "postgres", Limit: 10})
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if len(grep.Matches) != 0 {
		t.Fatalf("matches = %#v", grep.Matches)
	}

	backlinks, err := db.Backlinks(t.Context(), "systems/db.md")
	if err != nil {
		t.Fatalf("Backlinks: %v", err)
	}
	if len(backlinks) != 1 || backlinks[0].FromPath != "people/alice.md" {
		t.Fatalf("backlinks = %#v", backlinks)
	}
}

func TestRemoveDocumentClearsExistingVectorRows(t *testing.T) {
	db := openTempDB(t)
	createFakeVectorTable(t, db)
	db.vecAvailable = true
	if err := db.ApplyDocument(t.Context(), IndexedDocument{
		Path:  "people/alice.md",
		Hash:  "h1",
		Title: "Alice",
		Type:  "Person",
		Chunks: []Chunk{
			{Ordinal: 0, Text: "Alice owns the primary database"},
		},
	}); err != nil {
		t.Fatalf("ApplyDocument: %v", err)
	}
	insertFakeVectorRow(t, db, "people/alice.md", 0)

	if err := db.RemoveDocument(t.Context(), "people/alice.md"); err != nil {
		t.Fatalf("RemoveDocument: %v", err)
	}

	if got := fakeVectorRowCount(t, db, "people/alice.md"); got != 0 {
		t.Fatalf("alice vector rows = %d", got)
	}
}

func TestReconcileIndexesVaultAndSkipsServiceOwnedPaths(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "people", "alice.md"), "---\ntype: Person\ntitle: Alice\ndescription: DBA\n---\nSee [DB](../systems/db.md).\n")
	mustWriteFile(t, filepath.Join(root, "systems", "db.md"), "---\ntype: System\ntitle: DB\ndescription: Primary\n---\n")
	mustWriteFile(t, filepath.Join(root, "people", "index.md"), "---\ntype: Index\n---\n# People\n")
	mustWriteFile(t, filepath.Join(root, ".kvt", "ignored.md"), "---\ntype: Note\ntitle: Ignored\n---\n")
	mustWriteFile(t, filepath.Join(root, ".git", "ignored.md"), "---\ntype: Note\ntitle: Ignored\n---\n")

	db := openTempDBAt(t, filepath.Join(root, ".kvt", "index.db"))
	result, err := db.Reconcile(t.Context(), root)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Applied != 2 || result.Removed != 0 {
		t.Fatalf("result = %#v", result)
	}

	list, err := db.List(t.Context(), ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Documents) != 2 {
		t.Fatalf("documents = %#v", list.Documents)
	}
	for _, doc := range list.Documents {
		if filepath.Base(doc.Path) == "index.md" {
			t.Fatalf("indexed service-owned document: %#v", doc)
		}
	}

	summary, err := db.Summary(t.Context(), SummaryRequest{})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.DocumentCount != 2 {
		t.Fatalf("document count = %d", summary.DocumentCount)
	}
	if summary.CountsByType["Person"] != 1 || summary.CountsByType["System"] != 1 {
		t.Fatalf("counts = %#v", summary.CountsByType)
	}
}

func openTempDB(t *testing.T) *DB {
	t.Helper()
	return openTempDBAt(t, filepath.Join(t.TempDir(), "index.db"))
}

func openTempDBAt(t *testing.T, dbPath string) *DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	db, err := Open(dbPath, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
	return db
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func createFakeVectorTable(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.sql.Exec(`
		CREATE TABLE kb_vec (
			chunk_id TEXT PRIMARY KEY,
			path TEXT NOT NULL,
			ordinal INTEGER NOT NULL,
			embedding TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create fake kb_vec: %v", err)
	}
}

func insertFakeVectorRow(t *testing.T, db *DB, docPath string, ordinal int) {
	t.Helper()
	if _, err := db.sql.Exec(`
		INSERT INTO kb_vec(chunk_id, path, ordinal, embedding) VALUES(?, ?, ?, ?)
	`, docPath+"#stale", docPath, ordinal, "[1,0]"); err != nil {
		t.Fatalf("insert fake vector row: %v", err)
	}
}

func fakeVectorRowCount(t *testing.T, db *DB, docPath string) int {
	t.Helper()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM kb_vec WHERE path = ?`, docPath).Scan(&count); err != nil {
		t.Fatalf("count fake vector rows: %v", err)
	}
	return count
}
