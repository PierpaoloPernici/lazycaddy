package main

import (
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/config"
)

// TestNewRootCommand_ParsesLogJournalUnitFlag verifies the --log-journal-unit
// flag is bound to Settings.JournalUnit (and defaults to empty).
func TestNewRootCommand_ParsesLogJournalUnitFlag(t *testing.T) {
	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)

	if settings.JournalUnit != "" {
		t.Errorf("JournalUnit default = %q, want empty", settings.JournalUnit)
	}
	if err := cmd.ParseFlags([]string{"--log-journal-unit", "caddy.service"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if settings.JournalUnit != "caddy.service" {
		t.Errorf("JournalUnit = %q, want caddy.service", settings.JournalUnit)
	}
}

// TestJournalOptions_UsesJournalUnit verifies the journal source options are
// built from the resolved settings, so the unit reaches logs.JournalOptions.
func TestJournalOptions_UsesJournalUnit(t *testing.T) {
	opts := journalOptions(config.Settings{JournalUnit: "caddy.service"})
	if opts.Unit != "caddy.service" {
		t.Errorf("JournalOptions.Unit = %q, want caddy.service", opts.Unit)
	}
}

// TestBuildLogSource_MutuallyExclusive verifies that configuring both
// --log-file and --log-journal-unit is rejected before any source is built.
func TestBuildLogSource_MutuallyExclusive(t *testing.T) {
	settings := config.Settings{LogPath: "access.log", JournalUnit: "caddy.service"}
	src, err := buildLogSource(settings)
	if err == nil {
		t.Fatal("buildLogSource with both options: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want a mutual-exclusion message", err)
	}
	if src != nil {
		t.Error("buildLogSource returned a non-nil source despite the error")
	}
}

// TestNewRootCommand_RejectsBothLogSources drives the cobra command end to
// end: both log-source flags produce the documented error from RunE without
// constructing or running anything (no journalctl, no file tailer). The
// explicit --config bypasses v0.2 path discovery so the command fails on the
// mutual-exclusion check itself rather than on config discovery.
func TestNewRootCommand_RejectsBothLogSources(t *testing.T) {
	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)
	cmd.SetArgs([]string{"--config", "/nonexistent/Caddyfile",
		"--log-file", "access.log", "--log-journal-unit", "caddy.service"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute with both log sources: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("Execute error = %q, want a mutual-exclusion message", err)
	}
}

// TestBuildLogSource_NoSource verifies that with neither option the source
// is nil, keeping the current read-only default (log view disabled).
func TestBuildLogSource_NoSource(t *testing.T) {
	src, err := buildLogSource(config.Settings{})
	if err != nil {
		t.Fatalf("buildLogSource with no options: %v", err)
	}
	if src != nil {
		t.Error("buildLogSource with no options returned a non-nil source, want nil")
	}
}

// TestBuildLogSource_LogFile verifies --log-file still builds the file
// source and that it can be closed (idempotent, best-effort teardown).
func TestBuildLogSource_LogFile(t *testing.T) {
	settings := config.Settings{LogPath: "logs/access.log"}
	src, err := buildLogSource(settings)
	if err != nil {
		t.Fatalf("buildLogSource with LogPath: %v", err)
	}
	if src == nil {
		t.Fatal("buildLogSource with LogPath returned a nil source, want the file source")
	}
	if err := src.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
