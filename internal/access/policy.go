package access

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

type Mode int

const (
	Read Mode = iota
	Write
)

var ErrDenied = errors.New("access denied")

type Policy struct {
	readGlobs  []compiledGlob
	writeGlobs []compiledGlob
	denyGlobs  []compiledGlob
	readRaw    []string
}

type compiledGlob struct {
	raw string
	re  *regexp.Regexp
}

func New(readGlobs, writeGlobs, denyGlobs []string) (*Policy, error) {
	read, readRaw, err := compileGlobs(readGlobs)
	if err != nil {
		return nil, err
	}
	write, _, err := compileGlobs(writeGlobs)
	if err != nil {
		return nil, err
	}
	deny, _, err := compileGlobs(denyGlobs)
	if err != nil {
		return nil, err
	}
	return &Policy{
		readGlobs:  read,
		writeGlobs: write,
		denyGlobs:  deny,
		readRaw:    readRaw,
	}, nil
}

func CheckRead(policy *Policy, docPath string) error {
	if CanRead(policy, docPath) {
		return nil
	}
	return fmt.Errorf("%w: read %s", ErrDenied, docPath)
}

func CheckWrite(policy *Policy, docPath string) error {
	if CanWrite(policy, docPath) {
		return nil
	}
	return fmt.Errorf("%w: write %s", ErrDenied, docPath)
}

func CanRead(policy *Policy, docPath string) bool {
	return allowed(policy, docPath, Read)
}

func CanWrite(policy *Policy, docPath string) bool {
	return allowed(policy, docPath, Write)
}

func FilterStrings(paths []string, policy *Policy, mode Mode) []string {
	if policy == nil {
		return append([]string(nil), paths...)
	}
	out := make([]string, 0, len(paths))
	for _, docPath := range paths {
		switch mode {
		case Read:
			if CanRead(policy, docPath) {
				out = append(out, docPath)
			}
		case Write:
			if CanWrite(policy, docPath) {
				out = append(out, docPath)
			}
		}
	}
	return out
}

func LogAllowed(policy *Policy) bool {
	if policy == nil {
		return true
	}
	return len(policy.denyGlobs) == 0 && len(policy.readRaw) == 1 && policy.readRaw[0] == "**"
}

func IsDenied(err error) bool {
	return errors.Is(err, ErrDenied)
}

func allowed(policy *Policy, candidate string, mode Mode) bool {
	if policy == nil {
		return true
	}
	candidate = normalizeCandidate(candidate)
	if candidate == "" || matchesAny(policy.denyGlobs, candidate) {
		return false
	}
	switch mode {
	case Read:
		return matchesAny(policy.readGlobs, candidate)
	case Write:
		return matchesAny(policy.writeGlobs, candidate)
	default:
		return false
	}
}

func matchesAny(globs []compiledGlob, candidate string) bool {
	for _, glob := range globs {
		if glob.re.MatchString(candidate) {
			return true
		}
	}
	return false
}

func compileGlobs(values []string) ([]compiledGlob, []string, error) {
	out := make([]compiledGlob, 0, len(values))
	raw := make([]string, 0, len(values))
	for _, value := range values {
		normalized, err := normalizePattern(value)
		if err != nil {
			return nil, nil, err
		}
		re, err := regexp.Compile(globRegexp(normalized))
		if err != nil {
			return nil, nil, fmt.Errorf("invalid glob %q: %w", value, err)
		}
		out = append(out, compiledGlob{raw: normalized, re: re})
		raw = append(raw, normalized)
	}
	return out, raw, nil
}

func normalizePattern(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return "", fmt.Errorf("glob is empty")
	}
	if strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("glob %q must be bundle-relative", value)
	}
	if strings.Contains(value, "//") {
		return "", fmt.Errorf("glob %q must be canonical", value)
	}
	if strings.ContainsAny(value, "[]") {
		return "", fmt.Errorf("glob %q uses unsupported syntax", value)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("glob %q must be canonical", value)
		}
	}
	return value, nil
}

func normalizeCandidate(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "//") {
		return ""
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}

func globRegexp(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("$")
	return b.String()
}
