package pathutil

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
)

var segmentPattern = regexp.MustCompile(`^[a-z0-9_][a-z0-9._-]*$`)

const HouseHowtoPath = "_howto.md"

type Path string

func (p Path) String() string {
	return string(p)
}

type Error struct {
	Raw        string
	Suggestion string
	Reason     string
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("invalid path %q: %s (suggest %q)", e.Raw, e.Reason, e.Suggestion)
}

func Normalize(raw string) (Path, error) {
	clean := strings.TrimSpace(raw)
	clean = strings.ReplaceAll(clean, "\\", "/")
	clean = strings.ToLower(clean)
	suggestion := Suggest(raw)
	if err := validate(clean); err != nil {
		return "", &Error{Raw: raw, Suggestion: suggestion, Reason: err.Error()}
	}
	return Path(clean), nil
}

func Suggest(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, "\\", "/")
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, "/")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		part = slugSegment(part)
		if part == "" || part == "." || part == ".." {
			continue
		}
		cleaned = append(cleaned, part)
	}
	return strings.Join(cleaned, "/")
}

// StoragePath lowercases a path for filesystem storage.
// Accepts any-case input, returns lowercase.
func StoragePath(raw string) string {
	return strings.ToLower(raw)
}

func IsConceptMarkdownPath(rel string) bool {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "" || rel == "." {
		return false
	}
	rel = path.Clean(rel)
	base := path.Base(rel)
	if path.Ext(base) != ".md" {
		return false
	}
	if base == "index.md" {
		return false
	}
	return rel != HouseHowtoPath
}

// IsConceptMarkdownPathWithIndex is like IsConceptMarkdownPath but allows
// index.md as a concept document when allowIndex is true.
func IsConceptMarkdownPathWithIndex(rel string, allowIndex bool) bool {
	if allowIndex {
		rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
		if rel == "" || rel == "." {
			return false
		}
		rel = path.Clean(rel)
		base := path.Base(rel)
		if path.Ext(base) != ".md" {
			return false
		}
		return rel != HouseHowtoPath
	}
	return IsConceptMarkdownPath(rel)
}

func validate(raw string) error {
	if raw == "" {
		return fmt.Errorf("path is empty")
	}
	if strings.HasPrefix(raw, "/") {
		return fmt.Errorf("path must be bundle-relative")
	}
	if strings.Contains(raw, "\\") {
		return fmt.Errorf("path must use forward slashes")
	}
	if strings.HasPrefix(raw, "./") || strings.Contains(raw, "/./") || strings.Contains(raw, "/../") || strings.HasSuffix(raw, "/.") || strings.HasSuffix(raw, "/..") {
		return fmt.Errorf("path must not contain dot segments")
	}
	if path.Clean(raw) != raw {
		return fmt.Errorf("path must be canonical")
	}
	parts := strings.Split(raw, "/")
	for _, part := range parts {
		if part == "" {
			return fmt.Errorf("path has empty segment")
		}
		if !segmentPattern.MatchString(part) {
			return fmt.Errorf("segment %q is not allowed", part)
		}
	}
	return nil
}

func slugSegment(s string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			lastHyphen = false
		case r == '_' || r == '.' || r == '-':
			if b.Len() > 0 {
				b.WriteRune(r)
				lastHyphen = r == '-'
			}
		case unicode.IsSpace(r):
			if b.Len() > 0 && !lastHyphen {
				b.WriteRune('-')
				lastHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	out = strings.Trim(out, ".")
	return out
}
