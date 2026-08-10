package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	tea "github.com/charmbracelet/bubbletea"
)

// TestModelSave_NoSaverShowsWriteHint verifies that pressing s without
// a configured saver (read-only mode) surfaces a status hint about
// --write and does not open the confirmation modal.
func TestModelSave_NoSaverShowsWriteHint(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.showSaveConfirm {
		t.Error("showSaveConfirm = true, want false without saver")
	}
	if !strings.Contains(m.statusMessage, "--write") {
		t.Errorf("statusMessage = %q, want --write hint", m.statusMessage)
	}
}

// TestModelSave_NoWorkingCopyShowsHint verifies that pressing s before
// a working copy exists surfaces a status hint instead of opening the
// confirmation modal.
func TestModelSave_NoWorkingCopyShowsHint(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver)
	m = resize(m, 80, 24)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.showSaveConfirm {
		t.Error("showSaveConfirm = true, want false before validation")
	}
	if !strings.Contains(m.statusMessage, "working copy") {
		t.Errorf("statusMessage = %q, want working copy hint", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestModelSave_FailedValidationBlocksSave verifies that a failed
// validation marks the working copy as invalid and prevents the save
// confirmation from opening.
func TestModelSave_FailedValidationBlocksSave(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{formatted: []byte("formatted working copy"), diagnostics: diags, err: errors.New("caddy exit 1")}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.showSaveConfirm {
		t.Error("showSaveConfirm = true, want false after failed validation")
	}
	if m.workingValidated {
		t.Error("workingValidated = true, want false after failed validation")
	}
	if !strings.Contains(m.statusMessage, "validation failed") {
		t.Errorf("statusMessage = %q, want validation failure", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestModelSave_NoChangesShowsHint verifies that pressing s when the
// working copy matches the loaded source surfaces a "no changes"
// status instead of opening the confirmation modal.
func TestModelSave_NoChangesShowsHint(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.showSaveConfirm {
		t.Error("showSaveConfirm = true, want false")
	}
	if !strings.Contains(m.statusMessage, "no changes") {
		t.Errorf("statusMessage = %q, want no changes hint", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestModelSave_OpensConfirmation verifies the happy path: a
// successful validation that changes the working copy opens the save
// confirmation modal, which names the target path and backup dir.
func TestModelSave_OpensConfirmation(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if !m.showSaveConfirm {
		t.Fatal("showSaveConfirm = false, want true")
	}
	view := m.View()
	if !strings.Contains(view, "config/Caddyfile") {
		t.Errorf("View missing config path:\n%s", view)
	}
	if !strings.Contains(view, "Backup dir") {
		t.Errorf("View missing backup dir label:\n%s", view)
	}
	if !strings.Contains(view, "config/backups") {
		t.Errorf("View missing backup dir:\n%s", view)
	}
	if !strings.Contains(view, "Enter save") {
		t.Errorf("View missing Enter save hint:\n%s", view)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0 before confirm", saver.calls)
	}
}

// TestModelSave_EscCancels verifies that Esc closes the save
// confirmation modal without calling the saver.
func TestModelSave_EscCancels(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if !m.showSaveConfirm {
		t.Fatal("confirm modal not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showSaveConfirm {
		t.Error("showSaveConfirm = true after Esc, want false")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0 after cancel", saver.calls)
	}
	if !strings.Contains(m.statusMessage, "cancelled") {
		t.Errorf("statusMessage = %q, want cancelled hint", m.statusMessage)
	}
}

// TestModelSave_EnterTriggersSave verifies that Enter from the
// confirmation modal returns an async save command. Running the
// command invokes the saver with the real path, original bytes and
// working bytes, and delivering the result refreshes the loaded
// snapshot and root source.
func TestModelSave_EnterTriggersSave(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if !m.showSaveConfirm {
		t.Fatal("confirm modal not open")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter must return a tea.Cmd")
	}
	if !m.saving {
		t.Error("saving = false after Enter, want true")
	}
	msg := cmd()
	result, ok := msg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", msg)
	}
	if saver.capturedPath != "config/Caddyfile" {
		t.Errorf("capturedPath = %q, want config/Caddyfile", saver.capturedPath)
	}
	if string(saver.capturedOriginal) != src {
		t.Errorf("capturedOriginal = %q, want %q", saver.capturedOriginal, src)
	}
	if string(saver.capturedWorking) != formatted {
		t.Errorf("capturedWorking = %q, want %q", saver.capturedWorking, formatted)
	}
	updated, _ = m.Update(result)
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "saved") {
		t.Errorf("statusMessage = %q, want saved", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "config/backups/Caddyfile.bak") {
		t.Errorf("statusMessage = %q, want backup path", m.statusMessage)
	}
	if string(m.loadedBytes) != formatted {
		t.Errorf("loadedBytes = %q, want %q", m.loadedBytes, formatted)
	}
	if string(m.state.Graph.Root.Source) != formatted {
		t.Errorf("Root.Source = %q, want %q", m.state.Graph.Root.Source, formatted)
	}
	if m.saving {
		t.Error("saving = true after result, want false")
	}
}

// TestModelSave_ConflictStatus verifies that the saver reporting
// app.ErrConflict surfaces a "changed on disk" status.
func TestModelSave_ConflictStatus(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{err: app.ErrConflict}
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
	if !strings.Contains(m.statusMessage, "changed on disk") {
		t.Errorf("statusMessage = %q, want conflict message", m.statusMessage)
	}
}

// TestModelSave_SaveErrorShowsBackup verifies that a structured
// app.SaveError surfaces both the backup path and the underlying
// error in the status line.
func TestModelSave_SaveErrorShowsBackup(t *testing.T) {
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
	if !strings.Contains(m.statusMessage, "backup: config/backups/Caddyfile.bak") {
		t.Errorf("statusMessage = %q, want backup path", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "boom") {
		t.Errorf("statusMessage = %q, want boom", m.statusMessage)
	}
}

// TestModelSave_GenericErrorStatus verifies that an unclassified save
// error surfaces a generic "save failed" status.
func TestModelSave_GenericErrorStatus(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{err: errors.New("boom")}
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
	if !strings.HasPrefix(m.statusMessage, "✗ save failed") {
		t.Errorf("statusMessage = %q, want save failed prefix", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "boom") {
		t.Errorf("statusMessage = %q, want boom", m.statusMessage)
	}
}

// TestModelSave_BusyIgnored verifies that a second s press while a
// save is in flight is ignored.
func TestModelSave_BusyIgnored(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{result: app.SaveResult{BackupPath: "x"}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	updated, cmd1 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if !m.saving {
		t.Error("saving = false, want true")
	}
	if cmd1 == nil {
		t.Fatal("Enter must return cmd")
	}
	_, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd2 != nil {
		t.Error("s while saving must return nil cmd")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d before cmd1 executes, want 0", saver.calls)
	}
	cmd1()
	if saver.calls != 1 {
		t.Errorf("saver.calls = %d, want 1 (second s must not trigger)", saver.calls)
	}
}

// TestModelFooter_SaveConfirmContext verifies that the bottom footer
// shows the save-confirmation keys (not the global keymap) while the
// save modal is open.
func TestModelFooter_SaveConfirmContext(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	view := stripANSI(m.View())
	if !strings.Contains(view, "Enter save") {
		t.Errorf("footer should show Enter save, got:\n%s", view)
	}
	if strings.Contains(view, "v format & validate") {
		t.Errorf("footer must not show v format & validate, got:\n%s", view)
	}
}

// TestModelReload_NoReloaderShowsHint verifies that pressing r without
// a configured reloader surfaces a status hint and does not open the
// confirmation modal.
func TestModelReload_NoReloaderShowsHint(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}) // no reloader
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil {
		t.Errorf("expected nil cmd when reloader is nil, got %v", cmd)
	}
	if !strings.Contains(m.statusMessage, "reload unavailable") {
		t.Errorf("statusMessage = %q, want reload unavailable hint", m.statusMessage)
	}
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true, want false without reloader")
	}
}

// TestModelReload_NoWorkingCopyShowsHint verifies that pressing r
// before a working copy exists surfaces a status hint instead of
// opening the confirmation modal.
func TestModelReload_NoWorkingCopyShowsHint(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, reloader)
	m = resize(m, 80, 24)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true, want false before validation")
	}
	if !strings.Contains(m.statusMessage, "working copy") {
		t.Errorf("statusMessage = %q, want working copy hint", m.statusMessage)
	}
	if reloader.calls != 0 {
		t.Errorf("reloader.calls = %d, want 0", reloader.calls)
	}
}

// TestModelReload_NotValidatedBlocks verifies that a failed validation
// marks the working copy as invalid and prevents the reload
// confirmation from opening.
func TestModelReload_NotValidatedBlocks(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("formatted"), err: errors.New("caddy exit 1")}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true, want false after failed validation")
	}
	if !strings.Contains(m.statusMessage, "validation failed") {
		t.Errorf("statusMessage = %q, want validation failure", m.statusMessage)
	}
	if reloader.calls != 0 {
		t.Errorf("reloader.calls = %d, want 0", reloader.calls)
	}
}

// TestModelReload_UnsavedChangesBlock verifies that a working copy that
// differs from the file on disk (not yet saved) blocks reload with a
// "save first" hint.
func TestModelReload_UnsavedChangesBlock(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true, want false when working copy differs from disk")
	}
	if !strings.Contains(m.statusMessage, "save") {
		t.Errorf("statusMessage = %q, want hint about saving first", m.statusMessage)
	}
	if reloader.calls != 0 {
		t.Errorf("reloader.calls = %d, want 0", reloader.calls)
	}
}

// TestModelReload_AlreadyLoadedBlocks verifies that a second r press
// after a successful reload is a no-op with an "already loaded" hint.
func TestModelReload_AlreadyLoadedBlocks(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{result: app.ReloadResult{Endpoint: "http://localhost:2019", LoadedAt: time.Now()}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	m.Update(cmd()) // deliver the successful reload result
	if m.loaded != loadedMatches {
		t.Fatalf("precondition: loaded = %v, want loadedMatches", m.loaded)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true, want false when already loaded")
	}
	if !strings.Contains(m.statusMessage, "already loaded") {
		t.Errorf("statusMessage = %q, want already-loaded hint", m.statusMessage)
	}
	if reloader.calls != 1 {
		t.Errorf("reloader.calls = %d, want 1 (second r must not trigger a reload)", reloader.calls)
	}
}

// TestModelReload_OpensConfirmation verifies the happy path: a
// successful validation that leaves the working copy identical to the
// saved bytes opens the reload-confirmation modal.
func TestModelReload_OpensConfirmation(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !m.showReloadConfirm {
		t.Fatal("showReloadConfirm = false, want true")
	}
	if reloader.calls != 0 {
		t.Errorf("reloader.calls = %d, want 0 before confirm", reloader.calls)
	}
}

// TestModelReload_ConfirmNamesEndpoint verifies that the confirmation
// modal names the Admin API endpoint and the config path, so the
// operator can review the network target before confirming.
func TestModelReload_ConfirmNamesEndpoint(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	// stateFor builds settings with only ConfigPath; set the endpoint
	// explicitly so the modal body renders it.
	state.Settings.AdminEndpoint = "http://localhost:2019"
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !m.showReloadConfirm {
		t.Fatal("showReloadConfirm = false, want true")
	}
	view := m.View()
	visible := stripANSI(view)
	for _, want := range []string{"RELOAD CONFIG", "http://localhost:2019", "config/Caddyfile", "Enter reload", "Esc cancel"} {
		if !strings.Contains(visible, want) {
			t.Errorf("reload modal missing %q:\n%s", want, visible)
		}
	}
	if strings.Contains(visible, "Reload config · Enter reload") {
		t.Errorf("reload modal still uses the old inline title:\n%s", visible)
	}
	if !strings.Contains(view, "http://localhost:2019") {
		t.Errorf("View missing Admin API endpoint:\n%s", view)
	}
}

// TestModelReload_EscCancels verifies that Esc closes the reload
// confirmation modal without calling the reloader.
func TestModelReload_EscCancels(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !m.showReloadConfirm {
		t.Fatal("confirm modal not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true after Esc, want false")
	}
	if reloader.calls != 0 {
		t.Errorf("reloader.calls = %d, want 0 after cancel", reloader.calls)
	}
	if !strings.Contains(m.statusMessage, "cancelled") {
		t.Errorf("statusMessage = %q, want cancelled hint", m.statusMessage)
	}
}

// TestModelReload_EnterTriggersReload verifies that Enter from the
// confirmation modal returns an async reload command. Running the
// command invokes the reloader with the real path and the loaded
// (on-disk) bytes.
func TestModelReload_EnterTriggersReload(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !m.showReloadConfirm {
		t.Fatal("confirm modal not open")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter must return a tea.Cmd")
	}
	if !m.reloading {
		t.Error("reloading = false after Enter, want true")
	}
	msg := cmd()
	if _, ok := msg.(reloadResultMsg); !ok {
		t.Fatalf("got %T, want reloadResultMsg", msg)
	}
	if reloader.capturedPath != "config/Caddyfile" {
		t.Errorf("capturedPath = %q, want config/Caddyfile", reloader.capturedPath)
	}
	if string(reloader.capturedSaved) != src {
		t.Errorf("capturedSaved = %q, want %q", reloader.capturedSaved, src)
	}
}

// TestModelReload_SuccessSetsLoaded verifies that a successful reload
// marks the configuration as loaded and records the confirmation time.
func TestModelReload_SuccessSetsLoaded(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{result: app.ReloadResult{Endpoint: "http://localhost:2019", LoadedAt: time.Now()}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	m.Update(cmd()) // deliver the successful reload result
	if m.loaded != loadedMatches {
		t.Errorf("loaded = %v, want loadedMatches", m.loaded)
	}
	if m.loadedAt.IsZero() {
		t.Error("loadedAt is zero, want the reload timestamp")
	}
	if !strings.HasPrefix(m.statusMessage, "✓") {
		t.Errorf("statusMessage = %q, want success glyph", m.statusMessage)
	}
	if m.reloading {
		t.Error("reloading = true after result, want false")
	}
}

// TestModelReload_FailureUnreachable verifies that an unreachable Admin
// API maps to the loadedUnreachable state.
func TestModelReload_FailureUnreachable(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	updated, _ := m.Update(reloadResultMsg{Err: &app.ReloadError{
		Endpoint: "http://localhost:2019",
		Err:      fmt.Errorf("%w", app.ErrAdminUnreachable),
	}})
	m = updated.(*Model)
	if m.loaded != loadedUnreachable {
		t.Errorf("loaded = %v, want loadedUnreachable", m.loaded)
	}
	if !strings.HasPrefix(m.statusMessage, "✗") {
		t.Errorf("statusMessage = %q, want error glyph", m.statusMessage)
	}
}

// TestModelReload_FailureRejected verifies that a rejected reload
// (adapt or Admin API rejection) maps to the loadedStale state.
func TestModelReload_FailureRejected(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	updated, _ := m.Update(reloadResultMsg{Err: &app.ReloadError{
		Endpoint: "http://localhost:2019",
		Err:      fmt.Errorf("%w", app.ErrAdminRejected),
	}})
	m = updated.(*Model)
	if m.loaded != loadedStale {
		t.Errorf("loaded = %v, want loadedStale", m.loaded)
	}
	if !strings.HasPrefix(m.statusMessage, "✗") {
		t.Errorf("statusMessage = %q, want error glyph", m.statusMessage)
	}
}

// TestModelReload_BusyIgnored verifies that a second r press while a
// reload is in flight is ignored.
func TestModelReload_BusyIgnored(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	m.reloading = true
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil {
		t.Error("r while reloading must return nil cmd")
	}
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true, want false while reloading")
	}
	if reloader.calls != 0 {
		t.Errorf("reloader.calls = %d, want 0", reloader.calls)
	}
}

// TestModelReload_SaveTransitionsToStale verifies that a successful
// save marks the running configuration stale: the file on disk changed,
// so until a reload proves otherwise the running config no longer
// matches it.
func TestModelReload_SaveTransitionsToStale(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	m.Update(cmd()) // deliver the successful save result
	if m.loaded != loadedStale {
		t.Errorf("loaded = %v, want loadedStale after save", m.loaded)
	}
	if !m.loadedAt.IsZero() {
		t.Error("loadedAt must be zero after save (running config no longer matches)")
	}
}

// TestModelReload_FooterShowsKey verifies that the bottom footer shows
// the r reload key only when a reloader is configured.
func TestModelReload_FooterShowsKey(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	// The footer stays navigation-only even when a reloader is configured.
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, reloader)
	m = resize(m, 120, 30)
	if !strings.Contains(stripANSI(m.View()), "? commands") {
		t.Errorf("View missing command-palette hint with reloader configured:\n%s", m.View())
	}
	// Without a reloader the key must be absent.
	m2 := newLoadedModel(t, fakeLoader{state: state})
	m2 = resize(m2, 120, 30)
	if !strings.Contains(stripANSI(m2.View()), "? commands") {
		t.Errorf("View missing command-palette hint without a reloader:\n%s", m2.View())
	}
}

// TestModelReload_HeaderBadgeLoaded verifies that the header shows the
// LOADED badge after a successful reload.
func TestModelReload_HeaderBadgeLoaded(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{result: app.ReloadResult{Endpoint: "http://localhost:2019", LoadedAt: time.Now()}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 120, 30)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	m.Update(cmd())
	view := m.View()
	if !strings.Contains(view, "LOADED") {
		t.Errorf("View missing LOADED badge:\n%s", view)
	}
}

// TestModelReload_HeaderBadgeStale verifies that the header shows the
// STALE badge after a save that has not been reloaded.
func TestModelReload_HeaderBadgeStale(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	m.Update(cmd())
	view := m.View()
	if !strings.Contains(view, "STALE") {
		t.Errorf("View missing STALE badge:\n%s", view)
	}
}

// TestModelReload_HeaderBadgeUnknown verifies that the initial loaded
// state is shown as UNKNOWN when reloading is possible, and stays hidden
// in read-only sessions without a reloader (where the state has no
// meaning).
func TestModelReload_HeaderBadgeUnknown(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 120, 30)
	if !strings.Contains(m.View(), "UNKNOWN") {
		t.Errorf("View missing UNKNOWN badge in the initial state:\n%s", m.View())
	}
	// Without a reloader the badge must not appear at all.
	m = newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	if strings.Contains(m.View(), "UNKNOWN") {
		t.Errorf("View shows UNKNOWN badge without a reloader:\n%s", m.View())
	}
}

// refreshLoader reloads the initial state once and then switches to the
// configured failure mode, so a save-triggered structural refresh can fail
// deterministically after a successful Load.
type refreshLoader struct {
	state           *app.State
	refreshErr      error
	refreshNilGraph bool
	calls           int
}

func (l *refreshLoader) LoadState() (*app.State, error) {
	l.calls++
	if l.calls > 1 {
		if l.refreshErr != nil {
			return nil, l.refreshErr
		}
		if l.refreshNilGraph {
			st := *l.state
			st.Graph = nil
			return &st, nil
		}
	}
	return l.state, nil
}

func TestSave_StartGuardsWhileBusy(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{})
	m.workingBytes = []byte("changed\n")
	m.workingValidated = true

	// A save already in flight is not re-entered.
	m.saving = true
	m.startSave()
	if m.showSaveConfirm {
		t.Fatal("startSave while saving opened the confirmation modal")
	}
	if m.statusMessage != "" {
		t.Fatalf("startSave while saving set a status: %q", m.statusMessage)
	}

	// A reload in flight also defers the save.
	m.saving = false
	m.reloading = true
	m.startSave()
	if m.showSaveConfirm {
		t.Fatal("startSave while reloading opened the confirmation modal")
	}

	// Without a loaded graph the save is a no-op.
	m.reloading = false
	m.state = nil
	updated, cmd := m.startSave()
	if updated != m || cmd != nil {
		t.Fatalf("startSave without a graph returned (%v, %v), want no-op", updated != m, cmd != nil)
	}
}

func TestReload_NoGraphIsNoOp(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeReloader{})
	m.workingBytes = []byte("example.test {\n\trespond ok\n}\n")
	m.workingValidated = true
	m.state = nil
	updated, cmd := m.startReload()
	if updated != m || cmd != nil {
		t.Fatalf("startReload without a graph returned (%v, %v), want no-op", updated != m, cmd)
	}
}

func TestSave_StructuralRefreshFailureSurfacesTreeError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		loader *refreshLoader
	}{
		{name: "load error", loader: &refreshLoader{refreshErr: errors.New("refresh boom")}},
		{name: "nil graph", loader: &refreshLoader{refreshNilGraph: true}},
		{name: "no loader", loader: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := map[string]string{"config/Caddyfile": "example.test {\n\trespond ok\n}\n"}
			state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(fs))
			var m *Model
			if tc.loader != nil {
				tc.loader.state = state
				m = newLoadedModel(t, tc.loader, &fakeSaver{})
			} else {
				m = newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{})
				m.loader = nil
			}
			m.pendingEdit = &pendingEdit{
				path:     "config/Caddyfile",
				original: []byte("example.test {\n\trespond ok\n}\n"),
				content:  []byte("example.test {\n\trespond ok\n}\nnew.test {\n}\n"),
			}
			updated, _ := m.handleSaveResult(saveResultMsg{Result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}})
			m = updated.(*Model)
			if !strings.Contains(m.statusMessage, "tree refresh failed") {
				t.Errorf("statusMessage = %q, want a tree refresh failure", m.statusMessage)
			}
			if m.pendingEdit != nil {
				t.Error("pendingEdit survived a structural save")
			}
		})
	}
}

func TestSaveConfirmView_PendingEditTitle(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{})
	m.pendingEdit = &pendingEdit{path: "config/imported.caddy"}
	view := stripANSI(m.saveConfirmView(80, 24))
	for _, want := range []string{"Save edit", "config/imported.caddy", "applies only to the selected node range"} {
		if !strings.Contains(view, want) {
			t.Errorf("saveConfirmView missing %q:\n%s", want, view)
		}
	}
}

func TestConfirmViews_TinySizes(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, &fakeReloader{})
	for _, size := range []struct{ w, h int }{{0, 0}, {1, 1}, {2, 2}, {40, 4}, {120, 60}} {
		if got := m.saveConfirmView(size.w, size.h); got == "" {
			t.Errorf("saveConfirmView(%d, %d) rendered empty", size.w, size.h)
		}
		if got := m.reloadConfirmView(size.w, size.h); got == "" {
			t.Errorf("reloadConfirmView(%d, %d) rendered empty", size.w, size.h)
		}
	}
}

func TestReload_GenericErrorStatus(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeReloader{err: errors.New("admin api exploded")})
	updated, _ := m.handleReloadResult(reloadResultMsg{Err: errors.New("admin api exploded")})
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "✗ reload failed: admin api exploded") {
		t.Errorf("statusMessage = %q, want the generic reload failure", m.statusMessage)
	}
	if len(m.errorHistory) == 0 {
		t.Error("generic reload failure did not record an error")
	}
}

func TestReload_ConflictErrorMarksUnknown(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	conflict := &app.ReloadError{Endpoint: "http://127.0.0.1:2019/load", Err: app.ErrConflict}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeReloader{err: conflict})
	updated, _ := m.handleReloadResult(reloadResultMsg{Err: conflict})
	m = updated.(*Model)
	if m.loaded != loadedUnknown {
		t.Errorf("loaded = %v after a conflict, want loadedUnknown", m.loaded)
	}
	if !strings.Contains(m.statusMessage, "file changed on disk since save") {
		t.Errorf("statusMessage = %q, want the on-disk conflict message", m.statusMessage)
	}
}
