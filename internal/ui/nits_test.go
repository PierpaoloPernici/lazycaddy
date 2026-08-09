package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// TestDeleteValidationFailureRecorded verifies that a delete that fails
// validation is recorded in the error history with a safe next action.
func TestDeleteValidationFailureRecorded(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)

	m, _ = press(m, deleteValidatedMsg{
		Path: "config/Caddyfile",
		Diagnostics: []validator.Diagnostic{
			{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
		},
	})
	if len(m.errorHistory) == 0 {
		t.Fatal("delete-validation failure was not recorded")
	}
	entry := m.errorHistory[len(m.errorHistory)-1]
	if entry.Op != "delete" {
		t.Errorf("entry.Op = %q, want delete", entry.Op)
	}
	if entry.Next == "" {
		t.Error("entry.Next empty, want a safe next action")
	}
	if m.showDiagnostics == false {
		t.Fatal("diagnostics modal did not open for the delete failure")
	}
}

// TestDeleteInfrastructureErrorRecorded verifies that a hard delete
// validation error (no diagnostics) is recorded in the error history.
func TestDeleteInfrastructureErrorRecorded(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)

	m, _ = press(m, deleteValidatedMsg{Path: "config/Caddyfile", Err: errors.New("caddy exploded")})
	if len(m.errorHistory) == 0 {
		t.Fatal("delete infrastructure error was not recorded")
	}
	if entry := m.errorHistory[len(m.errorHistory)-1]; entry.Op != "delete" {
		t.Errorf("entry.Op = %q, want delete", entry.Op)
	}
}

// TestFormatValidateFailureRecorded verifies that a format+validate
// failure with error diagnostics is recorded in the error history.
func TestFormatValidateFailureRecorded(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())

	if len(m.errorHistory) == 0 {
		t.Fatal("format & validate failure was not recorded")
	}
	entry := m.errorHistory[len(m.errorHistory)-1]
	if entry.Op != "format & validate" {
		t.Errorf("entry.Op = %q, want format & validate", entry.Op)
	}
	if entry.Next == "" {
		t.Error("entry.Next empty, want a safe next action")
	}
}

// TestFormatValidateInfrastructureErrorRecorded verifies that a hard
// format+validate failure without diagnostics (missing binary, timeout)
// is recorded in the error history.
func TestFormatValidateInfrastructureErrorRecorded(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	formatter := &fakeFormatter{err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())

	if len(m.errorHistory) == 0 {
		t.Fatal("format & validate infrastructure error was not recorded")
	}
	if entry := m.errorHistory[len(m.errorHistory)-1]; entry.Op != "format & validate" {
		t.Errorf("entry.Op = %q, want format & validate", entry.Op)
	}
}

// TestUnsavedConfirm_ReadOnlySaveKeepsModal verifies that pressing s in
// the unsaved-changes modal in read-only mode keeps the modal open, so
// the operator never loses the discard/cancel options.
func TestUnsavedConfirm_ReadOnlySaveKeepsModal(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}) // no saver → read-only
	m = resize(m, 120, 30)
	m.workingBytes = []byte("changed\n")

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.showUnsavedConfirm {
		t.Fatal("unsaved prompt did not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if !m.showUnsavedConfirm {
		t.Fatal("unsaved prompt closed after s, want it to stay (read-only save cannot proceed)")
	}
	if m.showSaveConfirm {
		t.Fatal("save confirmation opened in read-only mode")
	}
	if m.quit {
		t.Fatal("quit = true, want to stay in the app")
	}
	// The operator can still discard or cancel.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if !m.quit {
		t.Fatal("discard from the kept modal did not quit")
	}
	if m.workingBytes != nil {
		t.Fatal("workingBytes survived the discard")
	}
}

// TestUnsavedConfirm_NotValidatedSaveKeepsModal verifies that pressing s
// when startSave returns early because the working copy did not validate
// keeps the unsaved-confirm modal open.
func TestUnsavedConfirm_NotValidatedSaveKeepsModal(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{})
	m = resize(m, 120, 30)
	// A working copy that exists but failed validation cannot be saved.
	m.workingBytes = []byte("changed\n")
	m.workingValidated = false

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.showUnsavedConfirm {
		t.Fatal("unsaved prompt did not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if !m.showUnsavedConfirm {
		t.Fatal("unsaved prompt closed after s, want it to stay (working copy not validated)")
	}
	if m.showSaveConfirm {
		t.Fatal("save confirmation opened for an unvalidated working copy")
	}
	if m.quit {
		t.Fatal("quit = true, want to stay in the app")
	}
}

// TestModelDiff_EmptyDiffNoChangesTitle verifies that a per-document D
// diff where the in-memory source matches the on-disk bytes is titled
// "No changes" instead of "Diff current changes".
func TestModelDiff_EmptyDiffNoChangesTitle(t *testing.T) {
	fs := map[string]string{
		"Caddyfile":   "example.test {\n\timport common.conf\n}\n",
		"common.conf": "# v1\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	m := newLoadedModel(t, loader, fsReader(fs))
	m = resize(m, 80, 24)
	// Select the imported document; its on-disk bytes match the loaded
	// source, so the diff is empty.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	if !m.showDiff {
		t.Fatal("showDiff = false, want the empty-diff modal")
	}
	if !strings.HasPrefix(m.diffTitle, "No changes ·") {
		t.Errorf("diffTitle = %q, want the 'No changes' title", m.diffTitle)
	}
	if strings.Contains(m.diffTitle, "Diff current changes") {
		t.Errorf("diffTitle = %q, must not claim 'Diff current changes'", m.diffTitle)
	}
	view := m.View()
	if !strings.Contains(view, "no changes") {
		t.Errorf("empty diff missing the 'no changes' body:\n%s", view)
	}
}

// TestModelDiff_HunkMarker verifies the current hunk is marked with a
// "> " prefix in the diff body, following the n/N hunk cursor.
func TestModelDiff_HunkMarker(t *testing.T) {
	m := newLoadedModel(t, fakeLoader{state: stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))})
	m = resize(m, 120, 30)
	m.diffLines = []diff.Line{
		{Kind: diff.KindFileHeader, Text: "--- a"},
		{Kind: diff.KindFileHeader, Text: "+++ b"},
		{Kind: diff.KindHunkHeader, Text: "@@ -1,1 +1,1 @@"},
		{Kind: diff.KindAdd, Text: "+a"},
		{Kind: diff.KindHunkHeader, Text: "@@ -5,1 +5,1 @@"},
		{Kind: diff.KindRemove, Text: "-b"},
	}
	m.diffTitle = "Diff · a"
	m.showDiff = true
	m.syncDiffContent()

	// Cursor starts on the first hunk; its header carries the marker.
	view := m.View()
	if !strings.Contains(view, "> @@ -1,1 +1,1 @@") {
		t.Errorf("first hunk missing the marker:\n%s", view)
	}
	// Moving to the second hunk moves the marker.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	view = m.View()
	if !strings.Contains(view, "> @@ -5,1 +5,1 @@") {
		t.Errorf("second hunk missing the marker after n:\n%s", view)
	}
}
