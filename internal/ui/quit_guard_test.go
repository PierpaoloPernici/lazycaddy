package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

// unsavedWorkingModel returns a writable model with an unvalidated set of
// in-memory changes, so every test starts from the "unsaved edits exist"
// state.
func unsavedWorkingModel(t *testing.T) *Model {
	t.Helper()
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m.workingBytes = []byte("example.test {\n\trespond ok\n}\n")
	m.workingValidated = true
	return m
}

// TestModelQuit_UnsavedPromptsAndDoesNotQuit verifies that a quit request
// with unsaved edits opens the confirmation modal and does NOT quit.
func TestModelQuit_UnsavedPromptsAndDoesNotQuit(t *testing.T) {
	m := unsavedWorkingModel(t)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = updated.(*Model)
	if m.quit {
		t.Fatal("quit = true, want the unsaved prompt to intercept")
	}
	if !m.showUnsavedConfirm {
		t.Fatal("showUnsavedConfirm = false, want the prompt")
	}
	if cmd != nil {
		t.Fatalf("got a command, want nil (no quit)")
	}
	view := m.View()
	if !strings.Contains(view, "Unsaved changes") {
		t.Errorf("prompt view missing the title:\n%s", view)
	}
	if !strings.Contains(view, "s save") || !strings.Contains(view, "d discard & quit") {
		t.Errorf("prompt view missing the save/discard options:\n%s", view)
	}
}

// TestModelQuit_NoUnsavedNoPrompt verifies that quitting without unsaved
// edits still quits immediately, without the confirmation modal.
func TestModelQuit_NoUnsavedNoPrompt(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = updated.(*Model)
	if !m.quit {
		t.Fatal("quit = false, want an immediate quit")
	}
	if m.showUnsavedConfirm {
		t.Fatal("unsaved prompt opened without unsaved edits")
	}
	if cmd == nil || cmd() == nil {
		t.Error("expected tea.Quit command")
	}
}

// TestModelQuit_SaveFromPromptStays verifies that s from the unsaved
// prompt opens the save confirmation (save is async, the user stays in
// the app) instead of quitting.
func TestModelQuit_SaveFromPromptStays(t *testing.T) {
	m := unsavedWorkingModel(t)
	// Wire a saver so the save confirmation can open.
	m = newLoadedModel(t, fakeLoader{state: writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))}, &fakeSaver{})
	m = resize(m, 120, 30)
	m.workingBytes = []byte("example.test {\n\trespond ok\n}\n")
	m.workingValidated = true

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.showUnsavedConfirm {
		t.Fatal("unsaved prompt did not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.quit {
		t.Fatal("quit = true after s from the prompt, want to stay in the app")
	}
	if m.showUnsavedConfirm {
		t.Fatal("unsaved prompt stayed open after s")
	}
	if !m.showSaveConfirm {
		t.Fatal("showSaveConfirm = false, want the save confirmation")
	}
}

// TestModelQuit_DiscardQuits verifies that d discards the unsaved edits
// and quits.
func TestModelQuit_DiscardQuits(t *testing.T) {
	m := unsavedWorkingModel(t)
	m.pendingEdit = &pendingEdit{path: "config/Caddyfile"}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.showUnsavedConfirm {
		t.Fatal("unsaved prompt did not open")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = updated.(*Model)
	if !m.quit {
		t.Fatal("quit = false after d, want a quit")
	}
	if m.pendingEdit != nil || m.workingBytes != nil || m.workingValidated {
		t.Error("unsaved state not discarded before quit")
	}
	if cmd == nil || cmd() == nil {
		t.Error("expected tea.Quit command")
	}
}

// TestModelQuit_CancelStays verifies that Esc from the unsaved prompt
// cancels and returns exactly to the prior state (nothing cleared).
func TestModelQuit_CancelStays(t *testing.T) {
	m := unsavedWorkingModel(t)
	pe := &pendingEdit{path: "config/Caddyfile", content: []byte("edited")}
	m.pendingEdit = pe
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.showUnsavedConfirm {
		t.Fatal("unsaved prompt did not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.quit {
		t.Fatal("quit = true after Esc, want to stay")
	}
	if m.showUnsavedConfirm {
		t.Fatal("unsaved prompt stayed open after Esc")
	}
	if m.pendingEdit == nil {
		t.Fatal("pendingEdit cleared by cancellation, want it preserved")
	}
	if m.workingBytes == nil {
		t.Fatal("workingBytes cleared by cancellation, want it preserved")
	}
}

// TestModelNavigationNeverPrompts verifies that pure navigation (cursor
// movement, search open/close, log toggle) never opens the unsaved prompt
// even with unsaved edits.
func TestModelNavigationNeverPrompts(t *testing.T) {
	m := unsavedWorkingModel(t)
	m.pendingEdit = &pendingEdit{path: "config/Caddyfile"}

	// Cursor movement.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.showUnsavedConfirm || m.quit {
		t.Fatal("cursor navigation prompted or quit with unsaved edits")
	}

	// Search open and close.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.searchActive {
		t.Fatal("search did not open")
	}
	if m.showUnsavedConfirm {
		t.Fatal("opening search prompted with unsaved edits")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showUnsavedConfirm || m.quit {
		t.Fatal("closing search prompted or quit")
	}

	// Log view toggle.
	src := app.LogSourceFunc{
		HistoryFn: func() []logs.Entry { return nil },
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
	}
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m = newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	m.workingBytes = []byte("changed\n")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if !m.showLogs {
		t.Fatal("log view did not open")
	}
	if m.showUnsavedConfirm || m.quit {
		t.Fatal("opening the log view prompted or quit with unsaved edits")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showLogs {
		t.Fatal("log view did not close")
	}
	if m.quit || m.showUnsavedConfirm {
		t.Fatal("closing the log view prompted or quit")
	}
}

// TestModelHeader_UnsavedBadge verifies the header shows an explicit
// UNSAVED badge whenever hasUnsavedEdits is true.
func TestModelHeader_UnsavedBadge(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	if strings.Contains(m.View(), "UNSAVED") {
		t.Fatal("UNSAVED badge shown without unsaved edits")
	}
	m.workingBytes = []byte("changed\n")
	if !strings.Contains(m.View(), "UNSAVED") {
		t.Error("UNSAVED badge missing with unsaved edits")
	}
	// Discarding clears the badge.
	m.discardUnsaved()
	if strings.Contains(m.View(), "UNSAVED") {
		t.Error("UNSAVED badge survived a discard")
	}
}

// TestModelSave_RetentionErrSurfaced verifies that a successful save with
// a retention failure surfaces the failure in the status and records it.
func TestModelSave_RetentionErrSurfaced(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{result: app.SaveResult{
		BackupPath:   "config/backups/b",
		RetentionErr: errors.New("cleanup boom"),
	}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "retention cleanup failed") {
		t.Errorf("statusMessage = %q, want the retention failure", m.statusMessage)
	}
	if len(m.errorHistory) == 0 || m.errorHistory[len(m.errorHistory)-1].Op != "save retention" {
		t.Errorf("error history missing the retention failure: %+v", m.errorHistory)
	}
}

// TestSaveFailure_RecoveryHint verifies that a SaveError surfaces the
// recovery backup path and the "press B" hint.
func TestSaveFailure_RecoveryHint(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{err: &app.SaveError{BackupPath: "config/backups/Caddyfile.bak", Err: errors.New("boom")}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "press B") {
		t.Errorf("statusMessage = %q, want the press-B recovery hint", m.statusMessage)
	}
	if len(m.errorHistory) == 0 || !strings.Contains(m.errorHistory[len(m.errorHistory)-1].Next, "recovery backup") {
		t.Errorf("error history missing the recovery next-action: %+v", m.errorHistory)
	}
}

// TestErrorHistory_RecordedAndOpened verifies that failures are recorded
// and the H keybinding opens the scrollable history view.
func TestErrorHistory_RecordedAndOpened(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte("x\n"), nil })
	mon := newFakeMonitor()
	m := newLoadedModel(t, fakeLoader{state: state}, mon)
	m = resize(m, 80, 24)

	// A monitor failure is recorded (instead of only disabling silently).
	m, _ = press(m, externalChangeMsg{err: errors.New("watcher exploded")})
	if len(m.errorHistory) == 0 {
		t.Fatal("monitor failure was not recorded")
	}
	entry := m.errorHistory[len(m.errorHistory)-1]
	if entry.Op != "external change watch" {
		t.Errorf("entry.Op = %q, want the watch operation", entry.Op)
	}
	if !strings.Contains(entry.Message, "watcher exploded") {
		t.Errorf("entry.Message = %q, want the failure detail", entry.Message)
	}
	if entry.Next == "" {
		t.Error("entry.Next empty, want a safe next action")
	}

	// The compact footer points to the palette instead of listing H.
	if strings.Contains(stripANSI(m.View()), "H errors") || !strings.Contains(stripANSI(m.View()), "? commands") {
		t.Errorf("footer should stay navigation-only:\n%s", m.View())
	}

	// H opens the history view.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	if !m.showErrorHistory {
		t.Fatal("showErrorHistory = false after H")
	}
	view := m.View()
	if !strings.Contains(view, "Error history") {
		t.Errorf("history view missing the title:\n%s", view)
	}
	if !strings.Contains(view, "watcher exploded") {
		t.Errorf("history view missing the recorded failure:\n%s", view)
	}
	if !strings.Contains(view, "→") {
		t.Errorf("history view missing the next-action hint:\n%s", view)
	}

	// Esc closes the history view and clears its viewport content.
	if updated, cmd := m.updateErrorHistoryKey(tea.KeyMsg{Type: tea.KeyEscape}); cmd != nil || updated.(*Model).showErrorHistory {
		t.Fatalf("Esc did not close error history: show=%v cmd=%v", updated.(*Model).showErrorHistory, cmd)
	}
}

// TestErrorHistory_Bounded verifies the history is capped at
// errorHistoryMax entries.
func TestErrorHistory_Bounded(t *testing.T) {
	m := unsavedWorkingModel(t)
	for i := 0; i < errorHistoryMax+20; i++ {
		m.recordError("op", fmt.Sprintf("msg %d", i), "next")
	}
	if len(m.errorHistory) != errorHistoryMax {
		t.Fatalf("errorHistory = %d entries, want %d", len(m.errorHistory), errorHistoryMax)
	}
	if m.errorHistory[0].Message != fmt.Sprintf("msg %d", 20) {
		t.Errorf("oldest retained entry = %q, want the oldest within the bound", m.errorHistory[0].Message)
	}
}

// TestErrorHistory_Scrolls verifies the history viewport scrolls with
// PgUp/PgDown when the history overflows a short window.
func TestErrorHistory_Scrolls(t *testing.T) {
	m := unsavedWorkingModel(t)
	for i := 0; i < 30; i++ {
		m.recordError("op", fmt.Sprintf("msg %d", i), "next")
	}
	m = resize(m, 80, 12)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	// The viewport is sized on the first render; render once so the
	// content overflows and scrolling is possible.
	m.View()
	initial := m.errorHistoryViewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.errorHistoryViewport.YOffset <= initial {
		t.Errorf("PgDown did not advance the history scroll: %d -> %d", initial, m.errorHistoryViewport.YOffset)
	}
	after := m.errorHistoryViewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.errorHistoryViewport.YOffset >= after {
		t.Errorf("PgUp did not retreat the history scroll: %d -> %d", after, m.errorHistoryViewport.YOffset)
	}
}

// TestEditorCancelled_SnapshotSurfaced verifies that a cancelled editor
// edit surfaces the recovery snapshot path in the status and history.
func TestEditorCancelled_SnapshotSurfaced(t *testing.T) {
	state := editorStateFor(t)
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte("a.example.test {\n\trespond ok\n}\n"),
		Cancelled:    true,
		SnapshotPath: "editor-snapshot-9",
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	done := pressEditorKey(t, m)
	m.Update(done)
	if !strings.Contains(m.statusMessage, "recovery snapshot: editor-snapshot-9") {
		t.Errorf("statusMessage = %q, want the recovery snapshot path", m.statusMessage)
	}
	if len(m.errorHistory) == 0 || !strings.Contains(m.errorHistory[len(m.errorHistory)-1].Next, "editor-snapshot-9") {
		t.Errorf("error history missing the snapshot next-action: %+v", m.errorHistory)
	}
}
