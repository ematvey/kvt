package config

import "testing"

func TestDefaultMatchesLocalMode(t *testing.T) {
	cfg := Default()
	if cfg.Server.HTTPPort != 8200 {
		t.Fatalf("HTTPPort = %d", cfg.Server.HTTPPort)
	}
	if len(cfg.Auth.APIKeys) != 0 {
		t.Fatalf("local mode should default to open API")
	}
	if cfg.Git.Branch != "main" {
		t.Fatalf("branch = %q", cfg.Git.Branch)
	}
}
