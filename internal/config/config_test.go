package config

import (
	"testing"
	"time"
)

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	if s.ConfigPath != "Caddyfile" {
		t.Errorf("ConfigPath = %q, want Caddyfile", s.ConfigPath)
	}
	if !s.ReadOnly {
		t.Errorf("ReadOnly = false, want true for the inspector milestone")
	}
	if s.BinaryPath != "" {
		t.Errorf("BinaryPath = %q, want empty (must be opted in via --caddy-path)", s.BinaryPath)
	}
	if s.ValidatorTimeout != 0 {
		t.Errorf("ValidatorTimeout = %s, want 0 (validator uses its own 5s default)", s.ValidatorTimeout)
	}
	if s.AdminEndpoint != "http://localhost:2019" {
		t.Errorf("AdminEndpoint = %q, want http://localhost:2019", s.AdminEndpoint)
	}
	if s.AdminTimeout != 30*time.Second {
		t.Errorf("AdminTimeout = %s, want 30s (runtime client default)", s.AdminTimeout)
	}
}
