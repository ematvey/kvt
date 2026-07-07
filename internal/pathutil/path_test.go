package pathutil

import "testing"

func TestNormalizeRejectsUnsafePathWithSuggestion(t *testing.T) {
	_, err := Normalize("people/John Smith.md")
	if err == nil {
		t.Fatalf("expected invalid path")
	}
	if got := Suggest("people/John Smith.md"); got != "people/john-smith.md" {
		t.Fatalf("suggestion = %q", got)
	}
}

func TestNormalizeAcceptsBundleRelativePath(t *testing.T) {
	p, err := Normalize("people/alice.md")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if p.String() != "people/alice.md" {
		t.Fatalf("path = %q", p.String())
	}
}
