package chunk

import (
	"strings"
	"testing"
)

func TestSplitKeepsCodeBlocksAtomicAndAddsBreadcrumb(t *testing.T) {
	doc := Document{
		Path:            "systems/db.md",
		Title:           "DB",
		Type:            "System",
		FrontmatterText: "title: DB\ntype: System",
		Body:            "# Runbook\n\nIntro\n\n```sql\nselect 1;\n```\n\n## Restart\n\nSteps\n",
	}

	chunks, err := Split(doc)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if len(chunks) < 3 {
		t.Fatalf("chunk count = %d, want at least 3", len(chunks))
	}
	if chunks[0].Ordinal != 0 || strings.TrimSpace(chunks[0].EmbedText) == "" {
		t.Fatalf("frontmatter chunk missing embed text: %#v", chunks[0])
	}
	if !strings.Contains(chunks[2].EmbedText, "Runbook > Restart") {
		t.Fatalf("missing breadcrumb in embed text: %#v", chunks[2])
	}
	for _, c := range chunks {
		if strings.Contains(c.Text, "```sql") && !strings.Contains(c.Text, "select 1;") {
			t.Fatalf("code block split incorrectly: %#v", c)
		}
	}
}

func TestSplitIncludesHeadingsInSearchText(t *testing.T) {
	doc := Document{
		Path:  "systems/db.md",
		Title: "DB",
		Type:  "System",
		Body:  "# Runbook\n\nIntro\n\n## Restart Procedure\n\nSteps\n",
	}

	chunks, err := Split(doc)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	found := false
	for _, chunk := range chunks {
		if strings.Contains(chunk.Text, "Restart Procedure") {
			found = true
			if !strings.Contains(chunk.Text, "Steps") {
				t.Fatalf("heading chunk missing body: %#v", chunk)
			}
		}
	}
	if !found {
		t.Fatalf("heading missing from search text: %#v", chunks)
	}
}
