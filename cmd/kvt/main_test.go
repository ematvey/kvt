package main

import (
	"bytes"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	code := run([]string{"kvt", "version"}, &out, &out)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if out.String() == "" {
		t.Fatalf("expected version output")
	}
}
