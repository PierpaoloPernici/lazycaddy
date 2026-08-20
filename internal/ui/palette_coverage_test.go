package ui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

func TestIsCommandVisible_AllViews(t *testing.T) {
	m := &Model{}
	// Homepage (no logs, no runtime, no tls)
	if !m.isCommandVisible(uiCommand{ID: commandValidate}) {
		t.Error("homepage should show validate")
	}
	if m.isCommandVisible(uiCommand{ID: commandLogFollow}) {
		t.Error("homepage should hide logFollow")
	}
	// Logs view
	m.showLogs = true
	m.logSource = &fakeLogSource{}
	if !m.isCommandVisible(uiCommand{ID: commandLogFollow}) {
		t.Error("logs should show logFollow")
	}
	if m.isCommandVisible(uiCommand{ID: commandValidate}) {
		t.Error("logs should hide validate")
	}
	if m.isCommandVisible(uiCommand{ID: commandEdit}) {
		t.Error("logs should hide edit")
	}
	// Log detail
	m.logDetailOpen = true
	if !m.isCommandVisible(uiCommand{ID: commandMoveSelection}) {
		t.Error("log detail should show move")
	}
	if m.isCommandVisible(uiCommand{ID: commandLogFilter}) {
		t.Error("log detail should hide logFilter")
	}
	m.logDetailOpen = false
	m.showLogs = false
	// Runtime
	m.showRuntime = true
	if m.isCommandVisible(uiCommand{ID: commandLogFollow}) {
		t.Error("runtime should hide logFollow")
	}
	if m.isCommandVisible(uiCommand{ID: commandPalette}) {
		// global should be visible
	} else {
		t.Error("global should be visible in runtime")
	}
	m.showRuntime = false
	m.showTLS = true
	if m.isCommandVisible(uiCommand{ID: commandValidate}) {
		t.Error("TLS should hide validate")
	}
	// Modals
	m.showTLS = false
	m.showDiagnostics = true
	if !m.isCommandVisible(uiCommand{ID: commandMoveSelection}) {
		t.Error("diagnostics should show move")
	}
	if m.isCommandVisible(uiCommand{ID: commandLogFollow}) {
		t.Error("diagnostics should hide logFollow")
	}
}

type fakeLogSource struct{}

func (f *fakeLogSource) Next(ctx context.Context) ([]logs.Entry, error) { return nil, nil }
func (f *fakeLogSource) History() []logs.Entry                          { return nil }
func (f *fakeLogSource) Close() error                                   { return nil }

var _ app.LogSource = (*fakeLogSource)(nil)

func TestRunCommand_Covers(t *testing.T) {
	m := &Model{}
	// Test a few runCommand branches
	m.runCommand(commandMoveSelection)
	m.runCommand(commandToggleBranch)
	m.runCommand(commandExpandAll)
	m.runCommand(commandCollapseAll)
	m.runCommand(commandMatcherNext)
	m.runCommand(commandReviewInline)
	m.runCommand(commandValidate)
	m.runCommand(commandDiff)
	m.runCommand(commandSave)
	m.runCommand(commandReload)
	m.runCommand(commandLogs)
	m.runCommand(commandRuntime)
	m.runCommand(commandTLS)
	m.runCommand(commandLogFollow)
	m.runCommand(commandLogFilter)
	m.runCommand(commandLogClearFilter)
	m.runCommand(commandLogPause)
	m.runCommand(commandLogDetail)
	m.runCommand(commandEdit)
	m.runCommand(commandAdd)
	m.runCommand(commandCopy)
	m.runCommand(commandSearch)
	m.runCommand(commandHelp)
	m.runCommand(commandQuit)
	m.runCommand(commandPalette)
}

func TestPalette_Toggle(t *testing.T) {
	m := &Model{}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !m.showCommandPalette {
		t.Error("palette should open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showCommandPalette {
		t.Error("palette should close")
	}
}
