package ontology

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/pathutil"
)

func TestLoadParsesSchema(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "_ontology.yaml"), ""+
		"types:\n"+
		"  Person:\n"+
		"    required: [title, description]\n"+
		"    optional: [email]\n"+
		"  Incident:\n"+
		"    required: [title, severity, status]\n"+
		"    fields:\n"+
		"      severity: {enum: [low, medium, high, critical]}\n"+
		"      affects: {ref: System}\n"+
		"rules:\n"+
		"  - path: people/**\n"+
		"    type: Person\n"+
		"unknown_types: reject\n")

	schema, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if schema.UnknownTypes != UnknownReject {
		t.Fatalf("unknown_types = %q", schema.UnknownTypes)
	}
	if got := schema.Types["Incident"].Fields["affects"].Ref; got != "System" {
		t.Fatalf("affects ref = %q", got)
	}
	if len(schema.Rules) != 1 || schema.Rules[0].Path != "people/**" {
		t.Fatalf("rules = %#v", schema.Rules)
	}
}

func TestValidateRequiredEnumPatternAndRef(t *testing.T) {
	schema := Schema{
		Types: map[string]TypeDef{
			"Incident": {
				Required: []string{"title", "severity", "status", "ticket"},
				Fields: map[string]FieldDef{
					"severity": {Enum: []string{"low", "medium", "high", "critical"}},
					"status":   {Enum: []string{"open", "investigating", "resolved"}},
					"ticket":   {Pattern: "^INC-[0-9]+$"},
					"affects":  {Ref: "System"},
				},
			},
		},
		UnknownTypes: UnknownWarn,
	}
	doc := frontmatter.Document{Fields: map[string]any{
		"type":     "Incident",
		"severity": "urgent",
		"status":   "open",
		"ticket":   "broken",
		"affects":  "Systems/DB.md",
	}}
	p, _ := pathutil.Normalize("incidents/db-down.md")
	result := ValidateDocument(schema, p, doc, Strict)
	if len(result.Errors) != 4 {
		t.Fatalf("errors = %#v", result.Errors)
	}
	assertIssueFields(t, result.Errors, "title", "severity", "ticket", "affects")
}

func TestValidatePathRulesAndAdvisoryMode(t *testing.T) {
	schema := Schema{
		Types: map[string]TypeDef{
			"Person": {
				Required: []string{"title"},
			},
		},
		Rules: []Rule{
			{Path: "people/**", Type: "Person"},
		},
	}

	personPath, _ := pathutil.Normalize("people/alice.md")
	advisory := ValidateDocument(schema, personPath, frontmatter.Document{
		Fields: map[string]any{"type": "Person"},
	}, Advisory)
	if len(advisory.Errors) != 0 || len(advisory.Warnings) != 1 {
		t.Fatalf("advisory = %#v", advisory)
	}
	assertIssueFields(t, advisory.Warnings, "title")

	unknown := ValidateDocument(schema, personPath, frontmatter.Document{
		Fields: map[string]any{"type": "Alien", "title": "Visitor"},
	}, Strict)
	if len(unknown.Errors) != 1 || len(unknown.Warnings) != 1 {
		t.Fatalf("unknown = %#v", unknown)
	}
	assertIssueFields(t, unknown.Errors, "type")
	assertIssueFields(t, unknown.Warnings, "type")
}

func TestValidateDocumentAdvisoryUsesWarningForUnknownType(t *testing.T) {
	schema := Schema{
		Types: map[string]TypeDef{
			"Person": {Required: []string{"title"}},
		},
		UnknownTypes: UnknownReject,
	}

	p, _ := pathutil.Normalize("people/alice.md")
	result := ValidateDocument(schema, p, frontmatter.Document{
		Fields: map[string]any{"type": "Alien"},
	}, Advisory)
	if len(result.Errors) != 0 || len(result.Warnings) != 1 {
		t.Fatalf("result = %#v", result)
	}
	assertIssueFields(t, result.Warnings, "type")
}

func TestValidateVaultReportsMalformedBodyLinks(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "people", "alice.md"), ""+
		"---\n"+
		"type: Person\n"+
		"title: Alice\n"+
		"---\n"+
		"[Bad](../Systems/DB.md)\n"+
		"[AlsoBad](foo//bar.md)\n")
	mustWriteFile(t, filepath.Join(root, "people", "foo", "bar.md"), ""+
		"---\n"+
		"type: Person\n"+
		"title: Nested\n"+
		"---\n"+
		"Body\n")
	mustWriteFile(t, filepath.Join(root, "systems", "db.md"), ""+
		"---\n"+
		"type: System\n"+
		"title: DB\n"+
		"---\n"+
		"Body\n")

	schema := Schema{
		Types: map[string]TypeDef{
			"Person": {Required: []string{"title"}},
			"System": {Required: []string{"title"}},
		},
	}

	report, err := ValidateVault(root, schema)
	if err != nil {
		t.Fatalf("ValidateVault: %v", err)
	}
	if len(report.Errors) != 2 {
		t.Fatalf("errors = %#v", report.Errors)
	}
	assertIssueFields(t, report.Errors, "body")
}

func TestValidateVaultAllowsLinksToReadableIndexFiles(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "people", "index.md"), ""+
		"---\n"+
		"type: Index\n"+
		"---\n"+
		"# people/\n")
	mustWriteFile(t, filepath.Join(root, "people", "alice.md"), ""+
		"---\n"+
		"type: Person\n"+
		"title: Alice\n"+
		"---\n"+
		"[People](index.md)\n")

	schema := Schema{
		Types: map[string]TypeDef{
			"Person": {Required: []string{"title"}},
		},
	}

	report, err := ValidateVault(root, schema)
	if err != nil {
		t.Fatalf("ValidateVault: %v", err)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("errors = %#v", report.Errors)
	}
}

func TestValidateVaultReportsBrokenBodyLinksAndRefTargets(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "people", "alice.md"), ""+
		"---\n"+
		"type: Person\n"+
		"title: Alice\n"+
		"---\n"+
		"[Missing](../systems/missing.md)\n")
	mustWriteFile(t, filepath.Join(root, "incidents", "outage.md"), ""+
		"---\n"+
		"type: Incident\n"+
		"title: Outage\n"+
		"affects: people/alice.md\n"+
		"---\n"+
		"Body\n")

	schema := Schema{
		Types: map[string]TypeDef{
			"Person": {Required: []string{"title"}},
			"System": {Required: []string{"title"}},
			"Incident": {
				Required: []string{"title"},
				Fields: map[string]FieldDef{
					"affects": {Ref: "System"},
				},
			},
		},
	}

	report, err := ValidateVault(root, schema)
	if err != nil {
		t.Fatalf("ValidateVault: %v", err)
	}
	if len(report.Errors) != 2 {
		t.Fatalf("errors = %#v", report.Errors)
	}
	assertIssueFields(t, report.Errors, "body", "affects")
}

func assertIssueFields(t *testing.T, issues []Issue, want ...string) {
	t.Helper()
	got := map[string]bool{}
	for _, issue := range issues {
		got[issue.Field] = true
	}
	for _, field := range want {
		if !got[field] {
			t.Fatalf("issues %#v missing field %q", issues, field)
		}
	}
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
