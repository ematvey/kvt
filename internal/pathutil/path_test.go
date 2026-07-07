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

func TestIsConceptMarkdownPath(t *testing.T) {
	tests := map[string]bool{
		"people/alice.md":      true,
		"_howto.md":            false,
		"index.md":             false,
		"people/index.md":      false,
		"people/_howto.md":     true,
		"people/readme.txt":    false,
		"people/alice.md.back": false,
	}
	for path, want := range tests {
		if got := IsConceptMarkdownPath(path); got != want {
			t.Fatalf("IsConceptMarkdownPath(%q) = %v, want %v", path, got, want)
		}
	}
}
