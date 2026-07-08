package access

import (
	"errors"
	"testing"
)

func TestGlobSegmentSemanticsAndDenyPrecedence(t *testing.T) {
	policy, err := New([]string{"notes/*.md", "public/**"}, []string{"drafts/**"}, []string{"public/secrets/**"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tests := []struct {
		name     string
		path     string
		readable bool
		writable bool
	}{
		{name: "single segment star", path: "notes/a.md", readable: true},
		{name: "star does not cross slash", path: "notes/archive/a.md", readable: false},
		{name: "double star crosses slash", path: "public/archive/a.md", readable: true},
		{name: "deny wins", path: "public/secrets/a.md", readable: false},
		{name: "write double star", path: "drafts/one/two.md", writable: true},
		{name: "read does not imply write", path: "notes/a.md", readable: true, writable: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanRead(policy, tt.path); got != tt.readable {
				t.Fatalf("CanRead(%q) = %v, want %v", tt.path, got, tt.readable)
			}
			if got := CanWrite(policy, tt.path); got != tt.writable {
				t.Fatalf("CanWrite(%q) = %v, want %v", tt.path, got, tt.writable)
			}
		})
	}
}

func TestDoubleStarSlashMatchesRootAndNestedFiles(t *testing.T) {
	policy, err := New([]string{"**/*.md"}, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, path := range []string{"root.md", "one/two.md"} {
		if !CanRead(policy, path) {
			t.Fatalf("CanRead(%q) = false, want true", path)
		}
	}
}

func TestMissingPolicyIsUnrestrictedAndEmptyPolicyDenies(t *testing.T) {
	if err := CheckRead(nil, "anything.md"); err != nil {
		t.Fatalf("nil read: %v", err)
	}
	if err := CheckWrite(nil, "anything.md"); err != nil {
		t.Fatalf("nil write: %v", err)
	}
	empty, err := New(nil, nil, nil)
	if err != nil {
		t.Fatalf("New empty: %v", err)
	}
	if err := CheckRead(empty, "anything.md"); !IsDenied(err) {
		t.Fatalf("empty read err = %v", err)
	}
	if err := CheckWrite(empty, "anything.md"); !IsDenied(err) {
		t.Fatalf("empty write err = %v", err)
	}
}

func TestInvalidGlobRejected(t *testing.T) {
	for _, bad := range []string{"/absolute/**", "../secret/**", "a//b", "bad["} {
		if _, err := New([]string{bad}, nil, nil); err == nil {
			t.Fatalf("New(%q) succeeded", bad)
		}
	}
}

func TestFilterStringsAndLogAllowed(t *testing.T) {
	policy, err := New([]string{"notes/**"}, nil, []string{"notes/private/**"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := FilterStrings([]string{"notes/a.md", "notes/private/a.md", "systems/db.md"}, policy, Read)
	if len(got) != 1 || got[0] != "notes/a.md" {
		t.Fatalf("filtered = %#v", got)
	}
	if LogAllowed(policy) {
		t.Fatalf("restricted policy should not allow log")
	}
	unrestricted, err := New([]string{"**"}, nil, nil)
	if err != nil {
		t.Fatalf("New unrestricted: %v", err)
	}
	if !LogAllowed(unrestricted) {
		t.Fatalf("unrestricted read should allow log")
	}
	if !LogAllowed(nil) {
		t.Fatalf("missing policy should preserve log behavior")
	}
	if !errors.Is(ErrDenied, ErrDenied) {
		t.Fatalf("ErrDenied sanity")
	}
}
