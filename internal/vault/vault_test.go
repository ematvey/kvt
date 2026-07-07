package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/ontology"
	"github.com/ematvey/kvt/internal/pathutil"
)

func TestReadConceptParsesFrontmatter(t *testing.T) {
	root := t.TempDir()
	mustWriteVaultFile(t, filepath.Join(root, "people", "alice.md"), ""+
		"---\n"+
		"type: Person\n"+
		"title: Alice\n"+
		"description: DBA\n"+
		"---\n"+
		"# Alice\n")

	p, _ := pathutil.Normalize("people/alice.md")
	concept, err := ReadConcept(root, p)
	if err != nil {
		t.Fatalf("ReadConcept: %v", err)
	}
	if concept.Path != p {
		t.Fatalf("path = %q", concept.Path)
	}
	if got := concept.Document.Fields["title"]; got != "Alice" {
		t.Fatalf("title = %#v", got)
	}
	if string(concept.Document.Body) != "# Alice\n" {
		t.Fatalf("body = %q", concept.Document.Body)
	}
}

func TestRegenerateIndexesDeterministic(t *testing.T) {
	root := t.TempDir()
	mustWriteVaultFile(t, filepath.Join(root, "people", "alice.md"), "---\ntype: Person\ntitle: Alice\ndescription: DBA\n---\nBody\n")
	mustWriteVaultFile(t, filepath.Join(root, "people", "bob.md"), "---\ntype: Person\ntitle: Bob\ndescription: SRE\n---\nBody\n")
	mustWriteVaultFile(t, filepath.Join(root, "systems", "db.md"), "---\ntype: System\ntitle: DB\ndescription: Primary\n---\nBody\n")
	p, _ := pathutil.Normalize("people/alice.md")

	changed, err := RegenerateIndexes(root, p, 50, "0.1")
	if err != nil {
		t.Fatalf("RegenerateIndexes: %v", err)
	}
	if len(changed) != 2 {
		t.Fatalf("changed = %#v", changed)
	}

	rootIndex, err := os.ReadFile(filepath.Join(root, "index.md"))
	if err != nil {
		t.Fatalf("read root index: %v", err)
	}
	if string(rootIndex) != "---\nokf_version: \"0.1\"\ntype: Index\n---\n# Index\n\n## Directories\n\n- [people/](people/index.md)\n- [systems/](systems/index.md)\n" {
		t.Fatalf("root index:\n%s", rootIndex)
	}

	peopleIndex, err := os.ReadFile(filepath.Join(root, "people", "index.md"))
	if err != nil {
		t.Fatalf("read people index: %v", err)
	}
	if string(peopleIndex) != "---\ntype: Index\n---\n# people/\n\n## Concepts\n\n- [Alice](alice.md) - DBA\n- [Bob](bob.md) - SRE\n" {
		t.Fatalf("people index:\n%s", peopleIndex)
	}
}

func TestExtractLinksFindsMarkdownLinksAndRefs(t *testing.T) {
	from, _ := pathutil.Normalize("incidents/outage.md")
	doc := frontmatter.Document{
		Fields: map[string]any{
			"type":    "Incident",
			"affects": "systems/db.md",
		},
		Body: []byte("[DB](../systems/db.md)\n[External](https://example.com)\n"),
	}
	schema := ontology.Schema{
		Types: map[string]ontology.TypeDef{
			"Incident": {
				Fields: map[string]ontology.FieldDef{
					"affects": {Ref: "System"},
				},
			},
		},
	}

	links := ExtractLinks(from, doc, schema)
	if len(links) != 2 {
		t.Fatalf("links = %#v", links)
	}
	if links[0].To.String() != "systems/db.md" || links[1].To.String() != "systems/db.md" {
		t.Fatalf("links = %#v", links)
	}
	kinds := []string{links[0].Kind, links[1].Kind}
	if !contains(kinds, "body") || !contains(kinds, "ref") {
		t.Fatalf("kinds = %#v", kinds)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func mustWriteVaultFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
