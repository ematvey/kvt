package index

import (
	"os"
	"path/filepath"
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

func TestRemoveDocumentDeletesRowsAndInboundLinks(t *testing.T) {
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

	if err := db.RemoveDocument(t.Context(), "people/alice.md"); err != nil {
		t.Fatalf("RemoveDocument: %v", err)
	}

	grep, err := db.Grep(t.Context(), GrepRequest{Query: "primary", Limit: 10})
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
	if len(backlinks) != 0 {
		t.Fatalf("backlinks = %#v", backlinks)
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
