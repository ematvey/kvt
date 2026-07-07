package frontmatter

import (
	"reflect"
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

func TestRenderNestedMapField(t *testing.T) {
	doc := Document{
		Fields: map[string]any{
			"meta": map[string]any{
				"a": "b",
			},
		},
		Body: []byte("# Body\n"),
	}

	out, err := Render(doc)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	parsed, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse rendered document: %v", err)
	}

	nested, ok := parsed.Fields["meta"]
	if !ok {
		t.Fatalf("parsed fields missing meta key")
	}
	got, ok := nested.(map[string]any)
	if !ok {
		t.Fatalf("meta has type %T, want map[string]any", nested)
	}
	want := map[string]any{
		"a": "b",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected meta value %#v", got)
	}
}
