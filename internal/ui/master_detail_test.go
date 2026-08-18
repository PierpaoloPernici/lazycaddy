package ui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/backup"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// Master-detail arrow convention: → opens a deeper detail/child view, ←
// returns to the parent list. The arrows are context-scoped: the main
// tree's ←/→ collapse/expand is untouched (covered by the tree tests).

// TestDiagnosticsModal_RightOpensDetailLeftBack verifies the →/← aliases in
// the caddy diagnostics modal: → opens the detail view, ← returns to the
// list, and ← from the list closes the modal back to the source pane.
func TestDiagnosticsModal_RightOpensDetailLeftBack(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "boom", Severity: validator.SeverityError},
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	m = openDiagnosticsModal(m, diags)
	if !m.showDiagnostics {
		t.Fatal("modal not open")
	}

	// → opens the detail for the selected diagnostic.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if !m.showDetail {
		t.Fatal("Right should open the diagnostic detail")
	}
	// ← returns to the list; the modal stays open.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.showDetail {
		t.Error("showDetail = true after Left from detail, want false")
	}
	if !m.showDiagnostics {
		t.Error("showDiagnostics = false after Left from detail, want true")
	}
	// ← from the list closes the modal.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.showDiagnostics {
		t.Error("showDiagnostics = true after Left from list, want false")
	}
}

// TestLogView_RightOpensDetailLeftBack verifies the →/← aliases in the log
// viewer: → opens the selected entry's detail, ← returns to the list.
func TestLogView_RightOpensDetailLeftBack(t *testing.T) {
	state := logStateFor(t)
	raw := `{"level":"info","ts":1760000000.5,"msg":"handled request","status":200}`
	entry, err := logs.ParseEntry([]byte(raw))
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{entry} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if !m.showLogs {
		t.Fatal("log view not open")
	}
	// → opens the detail (alias for Enter).
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if !m.logDetailOpen {
		t.Fatal("Right should open the log entry detail")
	}
	// ← returns to the log list; the viewer stays open.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.logDetailOpen {
		t.Error("logDetailOpen = true after Left from detail, want false")
	}
	if !m.showLogs {
		t.Error("showLogs = false after Left from detail, want true")
	}
}

// TestBackups_RightOpensCompare verifies that → in the backup history opens
// the comparison diff, exactly like Enter.
func TestBackups_RightOpensCompare(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	rb := &fakeRollbacker{
		entries: []backup.Entry{
			backupEntry(t, "/backups", "2026-08-01T20-10-00-002-Caddyfile", 2, "config/Caddyfile"),
		},
	}
	m := newLoadedModel(t, fakeLoader{state: state}, rb)
	m = resize(m, 80, 24)
	m = pressB(t, m)
	if !m.showBackups {
		t.Fatal("showBackups = false after B")
	}
	updated, cmd := m.updateBackupsKey(tea.KeyMsg{Type: tea.KeyRight})
	rm := updated.(*Model)
	if cmd == nil {
		t.Fatal("Right on a backup should return the compare command")
	}
	rm = keyPress(t, rm, cmd())
	if !rm.showDiff || rm.backupComparing == false {
		t.Errorf("after Right+compare: showDiff=%v backupComparing=%v, want true/true", rm.showDiff, rm.backupComparing)
	}
}
