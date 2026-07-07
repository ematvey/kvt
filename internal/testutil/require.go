package testutil

import (
	"os/exec"
	"testing"
)

func RequireGit(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git binary is required: %v", err)
	}
}
