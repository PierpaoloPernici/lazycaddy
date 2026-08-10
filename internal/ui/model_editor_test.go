package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	tea "github.com/charmbracelet/bubbletea"
)

// editorStateFor returns a writable state whose root imports one site
// file, so the editor flow exercises a document that is not the root.
func editorStateFor(t *testing.T) *app.State {
	t.Helper()
	return writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": "a.example.test {\n\trespond ok\n}\n",
	}))
}

// pressEditorKey drives a full $EDITOR round-trip from the e keypress
// through the delivered editorDoneMsg, assuming a clean editor exit with
// code 0. It mutates m in place and returns the done message for the
// caller to deliver.
func pressEditorKey(t *testing.T, m *Model) editorDoneMsg {
	t.Helper()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd == nil {
		t.Fatal("e must return a command")
	}
	msg := cmd()
	ready, ok := msg.(editorReadyMsg)
	if !ok {
		t.Fatalf("got %T, want editorReadyMsg", msg)
	}
	m.Update(ready)
	updated, cmd := m.Update(editorExecMsg{Err: nil})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("editorExecMsg must return the complete command")
	}
	doneMsg := cmd()
	done, ok := doneMsg.(editorDoneMsg)
	if !ok {
		t.Fatalf("got %T, want editorDoneMsg", doneMsg)
	}
	return done
}

// TestEditorKey_DisabledOnDocumentRow verifies the explicit decision: with
// a document row selected (depth 0, no node) the e command is disabled and
// never falls back to opening the whole file.
func TestEditorKey_DisabledOnDocumentRow(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	editor := &fakeEditor{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	if m.selectedItem().hasNode {
		t.Fatal("precondition: the root document row must have no node")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd != nil {
		t.Errorf("e on a document row returned a command, want none")
	}
	if editor.prepareCalls != 0 {
		t.Errorf("Prepare called %d times on a document row, want 0", editor.prepareCalls)
	}
	if m.editing {
		t.Error("editing = true, want false")
	}
	if m.editorSession != nil {
		t.Error("editorSession set, want nil")
	}
}

// TestEditorKey_DisabledInReadOnly verifies that the e command is ignored
// in read-only mode, mirroring the save flow.
func TestEditorKey_DisabledInReadOnly(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	editor := &fakeEditor{}
	m := newLoadedModel(t, fakeLoader{state: state}, editor)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // select the node row
	if !m.selectedItem().hasNode {
		t.Fatal("precondition: a node row must be selected")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd != nil {
		t.Errorf("e in read-only mode returned a command, want none")
	}
	if editor.prepareCalls != 0 {
		t.Errorf("Prepare called %d times in read-only mode, want 0", editor.prepareCalls)
	}
	if m.editing {
		t.Error("editing = true, want false")
	}
}

// TestEditorKey_DisabledWithoutEditor verifies that the e command is
// ignored when no app.Editor is wired, surfacing a hint.
func TestEditorKey_DisabledWithoutEditor(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver) // no editor
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})       // select the node row
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd != nil {
		t.Errorf("e without an editor returned a command, want none")
	}
	if m.editing {
		t.Error("editing = true, want false")
	}
	if !strings.Contains(m.statusMessage, "no editor configured") {
		t.Errorf("statusMessage = %q, want the editor hint", m.statusMessage)
	}
}

// TestEditorFlow_ValidAppliesToDocument walks the happy path against an
// imported document: Prepare targets the imported file (never the root),
// the diff opens with the document path, Enter confirms through the save
// modal and the saver writes the imported path.
func TestEditorFlow_ValidAppliesToDocument(t *testing.T) {
	importedSrc := "a.example.test {\n\trespond ok\n}\n"
	editedSrc := "a.example.test {\n\trespond ok\n\tencode gzip\n}\n"
	fs := map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}
	// The loader reads through the mutable fs so the post-save structural
	// refresh picks up the content the diskSaver wrote.
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(fs))
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(importedSrc),
		Content:      []byte(editedSrc),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/a.caddy.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	// items: root doc, a.caddy doc, a.example.test node.
	if len(m.items) != 3 {
		t.Fatalf("items = %d, want 3", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	if !m.selectedItem().hasNode {
		t.Fatal("precondition: the node row must be selected")
	}

	// Press e: Prepare must target the imported document, never the root.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd == nil {
		t.Fatal("e must return a command")
	}
	if !m.editing {
		t.Error("editing = false after e, want true")
	}
	msg := cmd()
	ready, ok := msg.(editorReadyMsg)
	if !ok {
		t.Fatalf("got %T, want editorReadyMsg", msg)
	}
	if editor.prepareCalls != 1 {
		t.Errorf("Prepare calls = %d, want 1", editor.prepareCalls)
	}
	if editor.capturedDoc == nil || editor.capturedDoc.Path != "config/sites/a.caddy" {
		t.Errorf("Prepare doc path = %v, want the imported config/sites/a.caddy (never the root)", editor.capturedDoc)
	}
	wantRange := state.Graph.Documents[1].Nodes[0].Range
	if editor.capturedRange != wantRange {
		t.Errorf("Prepare range = %+v, want the node range %+v", editor.capturedRange, wantRange)
	}

	// The ready message stores the session and returns the exec command.
	updated, cmd := m.Update(ready)
	m = updated.(*Model)
	if m.editorSession == nil {
		t.Fatal("editorSession not stored on editorReadyMsg")
	}
	if cmd == nil {
		t.Fatal("editorReadyMsg must return the exec command")
	}

	// Simulate the editor exiting cleanly.
	updated, cmd = m.Update(editorExecMsg{Err: nil})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("editorExecMsg must return the complete command")
	}
	doneMsg := cmd()
	done, ok := doneMsg.(editorDoneMsg)
	if !ok {
		t.Fatalf("got %T, want editorDoneMsg", doneMsg)
	}
	if editor.capturedExit != 0 {
		t.Errorf("Complete exit = %d, want 0", editor.capturedExit)
	}

	// Deliver the result: the edit diff opens with the document path.
	updated, _ = m.Update(done)
	m = updated.(*Model)
	if m.editing {
		t.Error("editing = true after the result, want false")
	}
	if !m.showDiff {
		t.Fatal("showDiff = false after a valid edit, want true")
	}
	if m.pendingEdit == nil {
		t.Fatal("pendingEdit not set after a valid edit")
	}
	if m.pendingEdit.path != "config/sites/a.caddy" {
		t.Errorf("pendingEdit.path = %q, want the imported path", m.pendingEdit.path)
	}
	if string(m.pendingEdit.content) != editedSrc {
		t.Errorf("pendingEdit.content = %q, want %q", m.pendingEdit.content, editedSrc)
	}
	if m.diffTitle != "Diff · config/sites/a.caddy" {
		t.Errorf("diffTitle = %q, want the imported document path", m.diffTitle)
	}

	// Enter in the edit diff saves directly: the diff is the single
	// confirmation for an editor edit, and the saver targets the
	// imported document.
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if m.showDiff {
		t.Error("showDiff = true after Enter, want false")
	}
	if m.showSaveConfirm {
		t.Error("showSaveConfirm = true after Enter in the edit diff, want false (the diff is the only confirmation)")
	}
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	if saver.capturedPath != "config/sites/a.caddy" {
		t.Errorf("saver path = %q, want the imported document, never the root", saver.capturedPath)
	}
	if string(saver.capturedOriginal) != importedSrc {
		t.Errorf("saver original = %q, want %q", saver.capturedOriginal, importedSrc)
	}
	if string(saver.capturedWorking) != editedSrc {
		t.Errorf("saver working = %q, want %q", saver.capturedWorking, editedSrc)
	}

	// The delivered save refreshes the imported document in memory and
	// leaves the root untouched.
	updated, _ = m.Update(res)
	m = updated.(*Model)
	if m.pendingEdit != nil {
		t.Error("pendingEdit not cleared after a successful save")
	}
	var importedDoc *caddyfile.Document
	for _, d := range m.state.Graph.Documents {
		if d.Path == "config/sites/a.caddy" {
			importedDoc = d
		}
	}
	if importedDoc == nil {
		t.Fatal("imported document not found in the graph")
	}
	if string(importedDoc.Source) != editedSrc {
		t.Errorf("imported doc Source = %q, want %q", importedDoc.Source, editedSrc)
	}
	if string(m.state.Graph.Root.Source) != "import sites/a.caddy\n" {
		t.Errorf("root Source = %q, want it untouched", m.state.Graph.Root.Source)
	}
	if string(m.loadedBytes) != "import sites/a.caddy\n" {
		t.Errorf("loadedBytes = %q, want the root unchanged after an imported-document save", m.loadedBytes)
	}
}

// TestEditorFlow_CancelledNoSave verifies that a cancelled result never
// reaches the diff and never touches the saver.
func TestEditorFlow_CancelledNoSave(t *testing.T) {
	state := editorStateFor(t)
	editor := &fakeEditor{result: app.EditResult{
		Original:  []byte("a.example.test {\n\trespond ok\n}\n"),
		Cancelled: true,
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	done := pressEditorKey(t, m)
	m.Update(done)
	if m.showDiff {
		t.Error("showDiff = true for a cancelled edit, want false")
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit set for a cancelled edit, want nil")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
	if m.editing {
		t.Error("editing = true, want false")
	}
}

// TestEditorFlow_InvalidShowsDiagnostics verifies that an edit that fails
// validation opens the diagnostics modal and is never savable.
func TestEditorFlow_InvalidShowsDiagnostics(t *testing.T) {
	state := editorStateFor(t)
	diags := []validator.Diagnostic{
		{Path: "config/sites/a.caddy", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	editor := &fakeEditor{result: app.EditResult{
		Original:    []byte("a.example.test {\n\trespond ok\n}\n"),
		Diagnostics: diags,
		Changed:     true,
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	done := pressEditorKey(t, m)
	m.Update(done)
	if !m.showDiagnostics {
		t.Fatal("showDiagnostics = false, want true for an invalid edit")
	}
	if len(m.diagnostics) != 1 {
		t.Errorf("diagnostics = %v, want the editor diagnostics", m.diagnostics)
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit set for an invalid edit, want nil (not savable)")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestEditorFlow_NoChanges verifies that an edit leaving the range intact
// surfaces a no-changes status and opens nothing.
func TestEditorFlow_NoChanges(t *testing.T) {
	state := editorStateFor(t)
	editor := &fakeEditor{result: app.EditResult{
		Original: []byte("a.example.test {\n\trespond ok\n}\n"),
		Changed:  false,
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	done := pressEditorKey(t, m)
	m.Update(done)
	if m.showDiff {
		t.Error("showDiff = true for a no-change edit, want false")
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit set for a no-change edit, want nil")
	}
	if !strings.Contains(m.statusMessage, "no changes") {
		t.Errorf("statusMessage = %q, want a no-changes hint", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestEditorFlow_DiscardViaEsc verifies that Esc from the edit diff closes
// it and discards the pending edit without saving.
func TestEditorFlow_DiscardViaEsc(t *testing.T) {
	state := editorStateFor(t)
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte("a.example.test {\n\trespond ok\n}\n"),
		Content:      []byte("a.example.test {\n\trespond ok\n\tencode gzip\n}\n"),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	done := pressEditorKey(t, m)
	m.Update(done)
	if !m.showDiff {
		t.Fatal("showDiff = false, want true before discarding")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showDiff {
		t.Error("showDiff = true after Esc, want false")
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit not discarded after Esc, want nil")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestEditorFlow_CouldNotStart verifies that a launch failure (as opposed
// to a non-zero editor exit) surfaces a status line and applies nothing.
func TestEditorFlow_CouldNotStart(t *testing.T) {
	state := editorStateFor(t)
	editor := &fakeEditor{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	msg := cmd()
	ready, ok := msg.(editorReadyMsg)
	if !ok {
		t.Fatalf("got %T, want editorReadyMsg", msg)
	}
	m.Update(ready)
	updated, cmd := m.Update(editorExecMsg{Err: errors.New("exec: no such file")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("editorExecMsg must return the complete command")
	}
	doneMsg := cmd()
	done, ok := doneMsg.(editorDoneMsg)
	if !ok {
		t.Fatalf("got %T, want editorDoneMsg", doneMsg)
	}
	m.Update(done)
	if !strings.Contains(m.statusMessage, "could not start editor") {
		t.Errorf("statusMessage = %q, want a launch failure hint", m.statusMessage)
	}
	if m.editing {
		t.Error("editing = true, want false")
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit set despite the launch failure")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestEditorFlow_FooterUsesCommandPalette verifies that operational editor
// actions no longer expand the normal navigation footer.
func TestEditorFlow_FooterUsesCommandPalette(t *testing.T) {
	state := editorStateFor(t)
	editor := &fakeEditor{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	// On a document row the key is hidden (no node range to edit).
	if strings.Contains(stripANSI(m.View()), "e edit") {
		t.Errorf("footer shows 'e edit' on a document row:\n%s", m.View())
	}
	// On a node row the footer remains navigation-only.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	if strings.Contains(stripANSI(m.View()), "e edit") || !strings.Contains(stripANSI(m.View()), "? commands") {
		t.Errorf("footer should expose navigation and the palette only:\n%s", m.View())
	}
	// In read-only mode the key is hidden even on a node row.
	readOnly := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m2 := newLoadedModel(t, fakeLoader{state: readOnly}, editor)
	m2 = resize(m2, 120, 30)
	m2 = keyPress(t, m2, tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(stripANSI(m2.View()), "e edit") {
		t.Errorf("footer shows 'e edit' in read-only mode:\n%s", m2.View())
	}
}

// TestEditorFlow_FailedSaveReopensDiff verifies that a failed save of a
// pending editor edit reopens the diff modal — so the operator can retry
// with Enter or discard with Esc — and keeps the pendingEdit intact
// alongside the error status message.
func TestEditorFlow_FailedSaveReopensDiff(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{name: "conflict", err: app.ErrConflict, wantMsg: "changed on disk"},
		{name: "save error with backup", err: &app.SaveError{BackupPath: "config/backups/a.caddy.bak", Err: errors.New("boom")}, wantMsg: "save failed"},
		{name: "generic error", err: errors.New("boom"), wantMsg: "save failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := editorStateFor(t)
			editor := &fakeEditor{result: app.EditResult{
				Original:     []byte("a.example.test {\n\trespond ok\n}\n"),
				Content:      []byte("a.example.test {\n\trespond ok\n\tencode gzip\n}\n"),
				Changed:      true,
				SnapshotPath: "snap-1",
			}}
			saver := &fakeSaver{err: tt.err}
			m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
			m = resize(m, 120, 30)
			m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
			m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
			done := pressEditorKey(t, m)
			m.Update(done)
			if !m.showDiff {
				t.Fatal("precondition: diff must be open")
			}
			// Enter in the edit diff saves directly (the diff is the only
			// confirmation for an editor edit).
			updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
			m = updated.(*Model)
			if m.showDiff {
				t.Error("showDiff = true after Enter, want false")
			}
			if cmd == nil {
				t.Fatal("Enter must return the save command")
			}
			msg := cmd() // saveResultMsg carrying tt.err
			updated, _ = m.Update(msg)
			m = updated.(*Model)
			if !m.showDiff {
				t.Fatalf("diff not reopened after a failed save (statusMessage = %q)", m.statusMessage)
			}
			if m.pendingEdit == nil {
				t.Error("pendingEdit cleared after a failed save, want retained")
			}
			if !strings.Contains(m.statusMessage, tt.wantMsg) {
				t.Errorf("statusMessage = %q, want it to contain %q", m.statusMessage, tt.wantMsg)
			}
			// Enter retries the save from the reopened diff.
			updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
			m = updated.(*Model)
			if cmd == nil {
				t.Fatal("retry Enter must return the save command")
			}
			retryMsg := cmd()
			if saver.calls != 2 {
				t.Errorf("saver.calls = %d, want 2 (Enter retries the save)", saver.calls)
			}
			updated, _ = m.Update(retryMsg)
			m = updated.(*Model)
			if !m.showDiff {
				t.Fatal("diff must be reopened after the failed retry")
			}
			// Esc discards the pending edit.
			m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
			if m.pendingEdit != nil {
				t.Error("pendingEdit not discarded after Esc from the reopened diff")
			}
		})
	}
}

// TestEditorFlow_SaveShortcutOpensConfirmForPendingEdit verifies that the
// s keybinding opens the save confirmation for a pending editor edit even
// when no root working copy exists: the pending edit was already validated
// by the editor flow and targets its own document.
func TestEditorFlow_SaveShortcutOpensConfirmForPendingEdit(t *testing.T) {
	state := editorStateFor(t)
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver)
	m = resize(m, 120, 30)
	// No working copy exists; only the pending edit is set.
	m.pendingEdit = &pendingEdit{
		path:         "config/sites/a.caddy",
		original:     []byte("a.example.test {\n\trespond ok\n}\n"),
		content:      []byte("a.example.test {\n\trespond ok\n\tencode gzip\n}\n"),
		snapshotPath: "snap-1",
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd != nil {
		t.Errorf("s returned a command, want none (confirmation is a modal, not a cmd)")
	}
	if !m.showSaveConfirm {
		t.Fatal("showSaveConfirm = false, want true: a pending edit saves regardless of the root working copy")
	}
}

// TestEditorKey_DisabledWhileReloading verifies that the e command is
// ignored while a reload is in flight, mirroring the save/reload mutual
// exclusion.
func TestEditorKey_DisabledWhileReloading(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	editor := &fakeEditor{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // select the node row
	m.reloading = true
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd != nil {
		t.Error("e while reloading returned a command, want none")
	}
	if editor.prepareCalls != 0 {
		t.Errorf("Prepare called %d times while reloading, want 0", editor.prepareCalls)
	}
	if m.editing {
		t.Error("editing = true, want false")
	}
}

// TestEditorFlow_WarningsOnlyNotSaved verifies that an edit whose only
// diagnostics are warnings (no errors) is not savable and surfaces a
// warnings status instead of an empty modal, mirroring the
// format+validate flow.
func TestEditorFlow_WarningsOnlyNotSaved(t *testing.T) {
	state := editorStateFor(t)
	diags := []validator.Diagnostic{
		{Path: "config/sites/a.caddy", Line: 1, Message: "deprecated directive", Severity: validator.SeverityWarning},
	}
	editor := &fakeEditor{result: app.EditResult{
		Original:    []byte("a.example.test {\n\trespond ok\n}\n"),
		Diagnostics: diags,
		Changed:     true,
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	done := pressEditorKey(t, m)
	m.Update(done)
	if m.showDiagnostics {
		t.Error("showDiagnostics = true for warning-only findings, want false")
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit set for a warnings-only edit, want nil (not savable)")
	}
	if !strings.Contains(m.statusMessage, "warnings") {
		t.Errorf("statusMessage = %q, want a warnings hint", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestModelSave_KeepsSourceRevealed is a regression test for the source
// pane jumping back to the top after a save: handleSaveResult used to
// reset the source pane via sourceDoc = nil, which reloaded the content
// but never re-revealed the still-selected node, so the viewport stayed
// pinned at the top. The fix sets a one-shot sourceRefresh flag that
// forces a content reload plus a re-reveal on the next render.
func TestModelSave_KeepsSourceRevealed(t *testing.T) {
	importedSrc := "top.example.test {\n\trespond top\n}\n" +
		strings.Repeat("# padding\n", 70) +
		"target.example.test {\n\trespond target\n}\n"
	editedSrc := "top.example.test {\n\trespond top\n}\n" +
		strings.Repeat("# padding\n", 70) +
		"target.example.test {\n\trespond edited\n}\n"
	fs := map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}
	// The loader reads through the mutable fs so the post-save structural
	// refresh picks up the content the diskSaver wrote.
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(importedSrc),
		Content:      []byte(editedSrc),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/a.caddy.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	// A short window so the target node (line 74) starts below the first
	// screen: a correct reveal produces YOffset > 0, while the bug leaves
	// the viewport pinned at the top.
	m = resize(m, 120, 30)

	// items: root doc, a.caddy doc, top.example.test, target.example.test.
	if len(m.items) != 4 {
		t.Fatalf("items = %d, want 4", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // top.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // target.example.test
	if !m.selectedItem().hasNode {
		t.Fatal("precondition: the target node must be selected")
	}
	// Render: the reveal scrolls the source pane onto the selected node.
	m.View()
	if m.viewport.YOffset == 0 {
		t.Fatalf("precondition: reveal must scroll to the target node, YOffset = %d", m.viewport.YOffset)
	}

	// Complete the editor round-trip and save it. Enter in the edit diff
	// saves directly — the diff is the single confirmation for an editor
	// edit.
	done := pressEditorKey(t, m)
	m.Update(done) // the edit diff opens
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	updated, _ = m.Update(res)
	m = updated.(*Model)

	// After the save the source pane must stay on the edited section: the
	// document bytes were replaced in place, but the selected node is
	// re-revealed instead of the viewport being reset to the top.
	m.View()
	if m.viewport.YOffset == 0 {
		t.Errorf("viewport YOffset = 0 after save, want it to stay revealed on the edited section")
	}
	if string(m.state.Graph.Documents[1].Source) != editedSrc {
		t.Errorf("imported doc Source = %q, want the saved bytes", m.state.Graph.Documents[1].Source)
	}
}

// runEditorSave drives the full editor round-trip to a delivered saveResultMsg
// against the model under test and returns it for the caller to deliver.
func runEditorSave(t *testing.T, m *Model) saveResultMsg {
	t.Helper()
	done := pressEditorKey(t, m)
	m.Update(done) // the edit diff opens
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	return res
}

// TestEditorSave_AddsSiteToRoot verifies that after a successful editor
// save that adds a site to the root document, the tree is rebuilt with the
// new node row, the graph source carries the new site, the selection is
// re-anchored and the source pane shows the new content. Without the fix
// the new node never appears in m.items.
func TestEditorSave_AddsSiteToRoot(t *testing.T) {
	fs := map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond ok\n}\n",
	}
	edited := "a.example.test {\n\trespond ok\n}\nnew.example.test {\n\trespond new\n}\n"
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(fs["config/Caddyfile"]),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	// items: root doc, a.example.test node.
	if len(m.items) != 2 {
		t.Fatalf("items = %d, want 2", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	if !m.selectedItem().hasNode {
		t.Fatal("precondition: the node row must be selected")
	}

	res := runEditorSave(t, m)
	updated, _ := m.Update(res)
	m = updated.(*Model)

	// The tree now contains the new site row.
	if len(m.items) != 3 {
		t.Errorf("items = %d after save, want 3 (root + a + new); items = %v", len(m.items), itemLabels(m.items))
	}
	foundNew := false
	for _, it := range m.items {
		if it.hasNode && it.node.Name == "new.example.test" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Errorf("tree missing the new site row; items = %v", itemLabels(m.items))
	}
	// The graph source carries the new site.
	if !strings.Contains(string(m.state.Graph.Root.Source), "new.example.test") {
		t.Errorf("root Source = %q, want it to contain the new site", m.state.Graph.Root.Source)
	}
	// The selection is re-anchored on the edited node.
	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.node.Name != "a.example.test" {
		t.Errorf("selection = %+v, want the edited node row", sel)
	}
	// After a render the source pane shows the new structure.
	m.View()
	if !strings.Contains(stripANSI(m.viewport.View()), "new.example.test") {
		t.Errorf("source pane does not show the new site:\n%s", m.viewport.View())
	}
}

// TestEditorSave_AddsSectionToImported verifies that a successful editor
// save that adds a section to an imported file rebuilds the tree with the
// new node of the imported document, keeps the root intact and shows the
// new content in the source pane.
func TestEditorSave_AddsSectionToImported(t *testing.T) {
	importedSrc := "a.example.test {\n\trespond ok\n}\n"
	edited := "a.example.test {\n\trespond ok\n}\nb.example.test {\n\trespond b\n}\n"
	fs := map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(importedSrc),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/a.caddy.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	// items: root doc, a.caddy doc, a.example.test node.
	if len(m.items) != 3 {
		t.Fatalf("items = %d, want 3", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node

	res := runEditorSave(t, m)
	updated, _ := m.Update(res)
	m = updated.(*Model)

	// The tree contains the new site of the imported file.
	if len(m.items) != 4 {
		t.Errorf("items = %d after save, want 4 (root + a.caddy + a + b); items = %v", len(m.items), itemLabels(m.items))
	}
	foundNew := false
	for _, it := range m.items {
		if it.hasNode && it.node.Name == "b.example.test" && it.doc != nil && it.doc.Path == "config/sites/a.caddy" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Errorf("tree missing the new imported site; items = %v", itemLabels(m.items))
	}
	// The imported document carries the new content and the root is intact.
	var importedDoc *caddyfile.Document
	for _, d := range m.state.Graph.Documents {
		if d.Path == "config/sites/a.caddy" {
			importedDoc = d
		}
	}
	if importedDoc == nil {
		t.Fatal("imported document not found after the reload")
	}
	if string(importedDoc.Source) != edited {
		t.Errorf("imported doc Source = %q, want %q", importedDoc.Source, edited)
	}
	if string(m.state.Graph.Root.Source) != "import sites/a.caddy\n" {
		t.Errorf("root Source = %q, want it untouched", m.state.Graph.Root.Source)
	}
	// After a render the source pane shows the new structure.
	m.View()
	if !strings.Contains(stripANSI(m.viewport.View()), "b.example.test") {
		t.Errorf("source pane does not show the new site:\n%s", m.viewport.View())
	}
}

// TestEditorSave_TreeAndSourceUpdated pins the post-save structural sync
// explicitly: both the item count and the rendered source pane must reflect
// the new tree after an editor save that adds a section to the root.
func TestEditorSave_TreeAndSourceUpdated(t *testing.T) {
	fs := map[string]string{
		"config/Caddyfile": "one.example.test {\n\trespond one\n}\n",
	}
	edited := "one.example.test {\n\trespond one\n}\ntwo.example.test {\n\trespond two\n}\n"
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(fs["config/Caddyfile"]),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // one.example.test node

	before := len(m.items)
	if before != 2 {
		t.Fatalf("items = %d before save, want 2", before)
	}
	res := runEditorSave(t, m)
	updated, _ := m.Update(res)
	m = updated.(*Model)

	if len(m.items) != before+1 {
		t.Errorf("items = %d after save, want %d (one more node row)", len(m.items), before+1)
	}
	m.View()
	visible := stripANSI(m.viewport.View())
	if !strings.Contains(visible, "one.example.test") || !strings.Contains(visible, "two.example.test") {
		t.Errorf("source pane does not show the full new structure:\n%s", visible)
	}
}

// pressFullEditorKey drives the full-document editor round-trip from the E
// keypress through the delivered editorDoneMsg, assuming a clean editor
// exit with code 0. It mutates m in place.
func pressFullEditorKey(t *testing.T, m *Model) editorDoneMsg {
	t.Helper()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	if cmd == nil {
		t.Fatal("E must return a command")
	}
	msg := cmd()
	ready, ok := msg.(editorReadyMsg)
	if !ok {
		t.Fatalf("got %T, want editorReadyMsg", msg)
	}
	m.Update(ready)
	updated, cmd := m.Update(editorExecMsg{Err: nil})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("editorExecMsg must return the complete command")
	}
	doneMsg := cmd()
	done, ok := doneMsg.(editorDoneMsg)
	if !ok {
		t.Fatalf("got %T, want editorDoneMsg", doneMsg)
	}
	return done
}

// runFullEditorSave drives the full-document editor round-trip to a
// delivered saveResultMsg against the model under test and returns it for
// the caller to deliver.
func runFullEditorSave(t *testing.T, m *Model) saveResultMsg {
	t.Helper()
	done := pressFullEditorKey(t, m)
	m.Update(done) // the edit diff opens
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	return res
}

// TestEditorFullKey_EditsRoot verifies that E on the root document row
// prepares a full-document edit and saves it through the normal pipeline.
func TestEditorFullKey_EditsRoot(t *testing.T) {
	src := "example.test {\n\trespond ok\n}\n"
	edited := "example.test {\n\trespond ok\n\tencode gzip\n}\n"
	fs := map[string]string{"config/Caddyfile": src}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(src),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	done := pressFullEditorKey(t, m)
	m.Update(done) // the edit diff opens
	if editor.prepareFullCalls != 1 {
		t.Errorf("PrepareFull calls = %d, want 1", editor.prepareFullCalls)
	}
	if editor.capturedFullDoc == nil || editor.capturedFullDoc.Path != "config/Caddyfile" {
		t.Errorf("PrepareFull doc = %v, want the root document", editor.capturedFullDoc)
	}
	if !m.showDiff {
		t.Fatal("showDiff = false after a valid full edit, want true")
	}
	if m.pendingEdit == nil || m.pendingEdit.path != "config/Caddyfile" {
		t.Fatalf("pendingEdit = %+v, want the root path", m.pendingEdit)
	}

	// Enter saves directly to the root document.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	if saver.capturedPath != "config/Caddyfile" {
		t.Errorf("saver path = %q, want the root", saver.capturedPath)
	}
	if string(saver.capturedOriginal) != src {
		t.Errorf("saver original = %q, want %q", saver.capturedOriginal, src)
	}
	if string(saver.capturedWorking) != edited {
		t.Errorf("saver working = %q, want %q", saver.capturedWorking, edited)
	}
	updated, _ = m.Update(res)
	m = updated.(*Model)
	if string(m.state.Graph.Root.Source) != edited {
		t.Errorf("root Source = %q, want %q", m.state.Graph.Root.Source, edited)
	}
}

// TestEditorFullKey_EditsImportedFile verifies that E on an imported
// document row edits that file, leaving the root untouched.
func TestEditorFullKey_EditsImportedFile(t *testing.T) {
	importedSrc := "a.example.test {\n\trespond ok\n}\n"
	edited := "a.example.test {\n\trespond ok\n\tencode gzip\n}\n"
	fs := map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(importedSrc),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/a.caddy.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	// items: root doc, a.caddy doc, a.example.test node. Select the
	// imported document row.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	sel := m.selectedItem()
	if sel == nil || sel.hasNode || sel.doc == nil || sel.doc.Path != "config/sites/a.caddy" {
		t.Fatalf("selection = %+v, want the imported document row", sel)
	}

	done := pressFullEditorKey(t, m)
	m.Update(done)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	if saver.capturedPath != "config/sites/a.caddy" {
		t.Errorf("saver path = %q, want the imported file", saver.capturedPath)
	}
	updated, _ = m.Update(res)
	m = updated.(*Model)
	var importedDoc *caddyfile.Document
	for _, d := range m.state.Graph.Documents {
		if d.Path == "config/sites/a.caddy" {
			importedDoc = d
		}
	}
	if importedDoc == nil || string(importedDoc.Source) != edited {
		t.Errorf("imported doc Source = %q, want %q", importedDoc.Source, edited)
	}
	if string(m.state.Graph.Root.Source) != "import sites/a.caddy\n" {
		t.Errorf("root Source = %q, want it untouched", m.state.Graph.Root.Source)
	}
}

// TestEditorFull_EditsCommentsOutsideBlocks verifies that a full-document
// edit can change comments that live outside any block.
func TestEditorFull_EditsCommentsOutsideBlocks(t *testing.T) {
	src := "# header comment\n\nexample.test {\n\trespond ok\n}\n\n# footer comment\n"
	edited := "# edited header comment\n\nexample.test {\n\trespond ok\n}\n\n# footer comment\n"
	fs := map[string]string{"config/Caddyfile": src}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(src),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	res := runFullEditorSave(t, m)
	updated, _ := m.Update(res)
	m = updated.(*Model)
	if string(m.state.Graph.Root.Source) != edited {
		t.Errorf("root Source = %q, want the edited comment content %q", m.state.Graph.Root.Source, edited)
	}
}

// TestEditorFull_EmptyResultGoesThrough verifies that an empty full-file
// edit is NOT a cancellation: it reaches the diff and is savable.
func TestEditorFull_EmptyResultGoesThrough(t *testing.T) {
	src := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(src),
		Content:      []byte{},
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)

	done := pressFullEditorKey(t, m)
	m.Update(done)
	if m.showDiff == false {
		t.Fatal("empty full edit must reach the diff, not be cancelled")
	}
	if m.pendingEdit == nil {
		t.Fatal("pendingEdit not set for an empty full edit")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter must return the save command")
	}
	cmd()
	if len(saver.capturedWorking) != 0 {
		t.Errorf("saver working = %q, want the empty document", saver.capturedWorking)
	}
}

// TestEditorFull_InvalidShowsDiagnostics verifies that an invalid full
// edit opens the diagnostics modal and is never savable.
func TestEditorFull_InvalidShowsDiagnostics(t *testing.T) {
	src := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	editor := &fakeEditor{result: app.EditResult{
		Original:    []byte(src),
		Diagnostics: diags,
		Changed:     true,
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)

	done := pressFullEditorKey(t, m)
	m.Update(done)
	if !m.showDiagnostics {
		t.Fatal("showDiagnostics = false, want true for an invalid full edit")
	}
	if m.showDiff {
		t.Error("showDiff = true for an invalid full edit, want false")
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit set for an invalid full edit, want nil")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestEditorFull_AfterSaveRebuildsTree verifies that after a structural
// full-document save the tree is rebuilt with the new site and the source
// pane shows it.
func TestEditorFull_AfterSaveRebuildsTree(t *testing.T) {
	fs := map[string]string{"config/Caddyfile": "a.example.test {\n\trespond a\n}\n"}
	edited := "a.example.test {\n\trespond a\n}\nnew.example.test {\n\trespond new\n}\n"
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(fs["config/Caddyfile"]),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	res := runFullEditorSave(t, m)
	updated, _ := m.Update(res)
	m = updated.(*Model)

	if len(m.items) != 3 {
		t.Errorf("items = %d after save, want 3 (root + a + new); items = %v", len(m.items), itemLabels(m.items))
	}
	foundNew := false
	for _, it := range m.items {
		if it.hasNode && it.node.Name == "new.example.test" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Errorf("tree missing the new site; items = %v", itemLabels(m.items))
	}
	m.View()
	if !strings.Contains(stripANSI(m.viewport.View()), "new.example.test") {
		t.Errorf("source pane does not show the new site:\n%s", m.viewport.View())
	}
}

// TestSecondNodeEditAfterStructuralAdd is a stale-range regression: after a
// structural full-document save rebuilds the tree, a subsequent node edit
// must target the freshly reloaded range, not the pre-edit one.
func TestSecondNodeEditAfterStructuralAdd(t *testing.T) {
	fs := map[string]string{"config/Caddyfile": "a.example.test {\n\trespond a\n}\n"}
	edited := "a.example.test {\n\trespond a\n}\nnew.example.test {\n\trespond new\n}\n"
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(fs["config/Caddyfile"]),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node

	// First: E adds a site and saves; the refresh rebuilds the tree.
	res := runFullEditorSave(t, m)
	updated, _ := m.Update(res)
	m = updated.(*Model)
	if len(m.items) != 3 {
		t.Fatalf("items = %d after E save, want 3; items = %v", len(m.items), itemLabels(m.items))
	}

	// Select the newly added node and edit it with e: the captured range
	// must come from the reloaded graph.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // new.example.test
	freshRange := m.state.Graph.Root.Nodes[1].Range
	editor.result = app.EditResult{
		Original:     []byte(edited),
		Content:      []byte(edited + "third.example.test {\n\trespond third\n}\n"),
		Changed:      true,
		SnapshotPath: "snap-2",
	}
	done := pressEditorKey(t, m)
	m.Update(done) // the edit diff opens
	if editor.capturedRange != freshRange {
		t.Errorf("capturedRange = %+v, want the fresh range %+v (stale ranges would fail)", editor.capturedRange, freshRange)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res2, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	updated, _ = m.Update(res2)
	m = updated.(*Model)
	if !strings.Contains(string(m.state.Graph.Root.Source), "third.example.test") {
		t.Errorf("root Source = %q, want it to contain the third site", m.state.Graph.Root.Source)
	}
}

// TestEditorFull_FooterShowsKey verifies that the footer lists E full edit
// on document and node rows (when writable with an editor) and hides it in
// read-only mode; e edit stays node-only.
func TestEditorFull_FooterShowsKey(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\n",
	}))
	editor := &fakeEditor{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	// The document row exposes only navigation and the palette.
	if strings.Contains(stripANSI(m.View()), "full edit") || !strings.Contains(stripANSI(m.View()), "? commands") {
		t.Errorf("footer should stay navigation-only:\n%s", m.View())
	}
	// The node row remains just as compact.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(stripANSI(m.View()), "full edit") || strings.Contains(stripANSI(m.View()), "e edit") {
		t.Errorf("footer should not list editor actions on a node row:\n%s", m.View())
	}
	// Read-only: neither.
	readOnly := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\n",
	}))
	m2 := newLoadedModel(t, fakeLoader{state: readOnly}, editor)
	m2 = resize(m2, 120, 30)
	if strings.Contains(stripANSI(m2.View()), "E full edit") || strings.Contains(stripANSI(m2.View()), "e edit") {
		t.Errorf("footer shows edit keys in read-only mode:\n%s", m2.View())
	}
}
