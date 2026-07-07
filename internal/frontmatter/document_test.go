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
