package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/config"
)

// quitAfterFirstUpdate wraps a real Bubble Tea model and quits the program
// as soon as the first Update has run. It lets the RunE integration test
// drive the entire command wiring (loader, validator, backup, watcher,
// monitor, UI model, probe) headlessly: the real model's Init commands run
// and the first message flows through Update before the program exits.
type quitAfterFirstUpdate struct {
	inner   tea.Model
	updated bool
}

func (q quitAfterFirstUpdate) Init() tea.Cmd { return q.inner.Init() }

func (q quitAfterFirstUpdate) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	inner, cmd := q.inner.Update(msg)
	q.inner = inner
	q.updated = true
	return q, tea.Sequence(cmd, tea.Quit)
}

func (q quitAfterFirstUpdate) View() string { return q.inner.View() }

// headlessProgram builds a Bubble Tea program over an in-memory input and
// output (no terminal), wrapping the model so the program quits after the
// first Update instead of waiting for a keypress.
func headlessProgram(model tea.Model) *tea.Program {
	return tea.NewProgram(
		quitAfterFirstUpdate{inner: model},
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(io.Discard),
	)
}

// fakeCaddyScript writes an executable fake `caddy` script to dir. The
// script answers `caddy version` with a fixed version and touches marker
// so tests can prove the runtime probe really invoked the binary.
func fakeCaddyScript(t *testing.T, dir, marker string) string {
	t.Helper()
	script := filepath.Join(dir, "caddy")
	content := "#!/bin/sh\ntouch " + marker + "\necho v2.11.4\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake caddy: %v", err)
	}
	return script
}

// TestRunE_WiresAndRunsHeadless executes the full cobra command wiring —
// flag parsing, path resolution, validator/backup/rollbacker/editor
// construction, the runtime probe, the file watcher, the UI model and the
// headless TUI run — against a temporary Caddyfile, and verifies the
// startup probe really invoked the configured binary.
func TestRunE_WiresAndRunsHeadless(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(cfgPath, []byte("example.test {\n\trespond ok\n}\n"), 0o600); err != nil {
		t.Fatalf("write Caddyfile: %v", err)
	}
	backupDir := filepath.Join(dir, "backups")
	marker := filepath.Join(dir, "probe-ran")
	fakeCaddy := fakeCaddyScript(t, dir, marker)

	original := teaProgram
	teaProgram = headlessProgram
	defer func() { teaProgram = original }()

	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)
	cmd.SetArgs([]string{
		"--config", cfgPath,
		"--write",
		"--backup-dir", backupDir,
		"--caddy-path", fakeCaddy,
		"--admin-endpoint", "http://127.0.0.1:1",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if settings.ReadOnly {
		t.Error("ReadOnly = true after --write, want false")
	}
	if settings.ConfigPath != cfgPath {
		t.Errorf("ConfigPath = %q, want %q", settings.ConfigPath, cfgPath)
	}
	if settings.BinaryPath != fakeCaddy {
		t.Errorf("BinaryPath = %q, want %q", settings.BinaryPath, fakeCaddy)
	}
	// The runtime probe must have invoked the configured binary during the
	// headless run; the marker proves it without asserting on internals.
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("fake caddy was never invoked by the runtime probe: %v", err)
	}
}

// TestRunE_ReadOnlyDefault verifies the default (no --write) stays
// read-only through the full wiring: the run completes headlessly and the
// read-only flag is never flipped.
func TestRunE_ReadOnlyDefault(t *testing.T) {
	original := teaProgram
	teaProgram = headlessProgram
	defer func() { teaProgram = original }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(cfgPath, []byte("example.test {\n\trespond ok\n}\n"), 0o600); err != nil {
		t.Fatalf("write Caddyfile: %v", err)
	}
	marker := filepath.Join(dir, "probe-ran")

	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)
	cmd.SetArgs([]string{
		"--config", cfgPath,
		"--caddy-path", fakeCaddyScript(t, dir, marker),
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !settings.ReadOnly {
		t.Error("ReadOnly = false without --write, want true")
	}
}

// TestRunE_MissingConfigCompletes verifies the documented contract that a
// missing config file surfaces as the top-level state error without
// aborting the run: the headless TUI still starts and exits cleanly, and
// the process does not hang or panic.
func TestRunE_MissingConfigCompletes(t *testing.T) {
	original := teaProgram
	teaProgram = headlessProgram
	defer func() { teaProgram = original }()

	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)
	cmd.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "does-not-exist.Caddyfile")})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute with a missing config must complete headlessly, got: %v", err)
	}
	if !settings.ReadOnly {
		t.Error("ReadOnly = false without --write, want true")
	}
}
