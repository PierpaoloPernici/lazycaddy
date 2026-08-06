package config

import "testing"

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	if s.ConfigPath != "Caddyfile" {
		t.Errorf("ConfigPath = %q, want Caddyfile", s.ConfigPath)
	}
	if !s.ReadOnly {
		t.Errorf("ReadOnly = false, want true for the inspector milestone")
	}
}
