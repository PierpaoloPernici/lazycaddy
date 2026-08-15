package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
)

// runEditorLaunchCmd drives a model already holding an editor launch
// command (from a comment insertion) through the ready/exec/complete
// chain, returning the updated model and the editorDoneMsg.
func runEditorLaunchCmd(t *testing.T, m *Model, launch tea.Cmd) (*Model, editorDoneMsg) {
	t.Helper()
	if launch == nil {
		t.Fatal("expected an editor launch command")
	}
	msg := launch()
	ready, ok := msg.(editorReadyMsg)
	if !ok {
		t.Fatalf("got %T, want editorReadyMsg", msg)
	}
	updated, _ := m.Update(ready)
	m = updated.(*Model)
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
	return m, done
}

// confirmAndSaveCommentInsert confirms the opened diff with Enter and
// applies the returned save result, mirroring runEditorSave.
func confirmAndSaveCommentInsert(t *testing.T, m *Model) *Model {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the diff must return the save command")
	}
	res, ok := cmd().(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", res)
	}
	m.Update(res)
	return m
}

// TestCommentInsert_FromDocumentRowTop verifies a on a document row opens
// the placement picker, the top placement inserts the new group at offset
// 0 with the seeded template, and the save re-anchors the selection on the
// new group with its branch expanded.
func TestCommentInsert_FromDocumentRowTop(t *testing.T) {
	src := "example.test {\n\trespond ok\n}\n"
	composed := "# top comment\nexample.test {\n\trespond ok\n}\n"
	fs := map[string]string{"config/Caddyfile": src}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(src),
		Content:      []byte(composed),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, &fakeFormatter{}, saver, editor)
	m = resize(m, 120, 30)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	if m.structuredAddMode != structuredAddCommentPlacement {
		t.Fatalf("mode = %v, want structuredAddCommentPlacement", m.structuredAddMode)
	}
	updated, launch := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // at the top of the file
	m = updated.(*Model)
	m, done := runEditorLaunchCmd(t, m, launch)
	if editor.prepareInsertCalls != 1 || editor.capturedInsertPos != 0 {
		t.Fatalf("PrepareInsert calls=%d pos=%d, want 1 call at offset 0", editor.prepareInsertCalls, editor.capturedInsertPos)
	}
	if editor.capturedTemplate != commentTemplate {
		t.Errorf("template = %q, want %q", editor.capturedTemplate, commentTemplate)
	}
	m.Update(done)
	if !m.showDiff {
		t.Fatal("comment insert must open the diff")
	}
	m = confirmAndSaveCommentInsert(t, m)
	if got := string(m.state.Graph.Root.Source); got != composed {
		t.Errorf("root Source = %q, want %q", got, composed)
	}
	sel := m.selectedItem()
	if sel.comment == nil || sel.comment.StartLine != 1 {
		t.Fatalf("selection after save = %q, want the new comment group at line 1", sel.label)
	}
	if m.collapsed[commentsKey(m.state.Graph.Root)] {
		t.Error("comments branch must be expanded after a comment insert")
	}
}

// TestCommentInsert_FromBlockAfter verifies a top-level block adds the
// comment entry to the directive picker and the "after this block"
// placement inserts at the block's end.
func TestCommentInsert_FromBlockAfter(t *testing.T) {
	src := "example.test {\n\trespond ok\n}\n"
	composed := "example.test {\n\trespond ok\n}\n# after block\n"
	fs := map[string]string{"config/Caddyfile": src}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(src),
		Content:      []byte(composed),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, &fakeFormatter{}, saver, editor)
	m = resize(m, 120, 30)

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // example.test
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	if m.structuredAddMode != structuredAddPicker {
		t.Fatalf("mode = %v, want the directive picker", m.structuredAddMode)
	}
	last := len(m.structuredAddItems) - 1
	if m.structuredAddItems[last] != structuredAddCommentEntry {
		t.Fatalf("last picker item = %q, want the comment entry", m.structuredAddItems[last])
	}
	m.structuredAddCursor = last
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.structuredAddMode != structuredAddCommentPlacement {
		t.Fatalf("mode = %v, want comment placement", m.structuredAddMode)
	}
	// The placement picker lists before/after; choose after.
	m.structuredAddCursor = 1
	wantPos := m.state.Graph.Root.Nodes[0].Range.End
	updated, launch := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	m, done := runEditorLaunchCmd(t, m, launch)
	if editor.capturedInsertPos != wantPos {
		t.Fatalf("PrepareInsert pos = %d, want %d (end of the site block)", editor.capturedInsertPos, wantPos)
	}
	m.Update(done)
	if !m.showDiff {
		t.Fatal("comment insert must open the diff")
	}
	m = confirmAndSaveCommentInsert(t, m)
	sel := m.selectedItem()
	if sel.comment == nil || sel.comment.StartLine != 4 {
		t.Fatalf("selection after save = %q, want the new group at line 4", sel.label)
	}
}

// TestCommentInsert_FromCommentGroupAppend verifies a on a comment leaf
// opens the editor directly at the end of the group, with no picker.
func TestCommentInsert_FromCommentGroupAppend(t *testing.T) {
	src := "# header\nexample.test {\n\trespond ok\n}\n"
	composed := "# header\n# appended\nexample.test {\n\trespond ok\n}\n"
	fs := map[string]string{"config/Caddyfile": src}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(src),
		Content:      []byte(composed),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, &fakeFormatter{}, saver, editor)
	m = resize(m, 120, 30)
	m = expandAll(m)
	// doc, example.test, comments branch, leaf.
	for i := 0; i < 3; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	sel := m.selectedItem()
	if sel.comment == nil {
		t.Fatalf("expected a comment leaf, got %q", sel.label)
	}
	groupEnd := sel.comment.Range.End
	updated, launch := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	if m.showStructuredAdd {
		t.Fatal("comment-group append must not open a picker")
	}
	m, done := runEditorLaunchCmd(t, m, launch)
	if editor.prepareInsertCalls != 1 || editor.capturedInsertPos != groupEnd {
		t.Fatalf("PrepareInsert pos = %d, want %d (end of the group)", editor.capturedInsertPos, groupEnd)
	}
	m.Update(done)
	m = confirmAndSaveCommentInsert(t, m)
	sel = m.selectedItem()
	if sel.comment == nil || sel.comment.Lines != 2 {
		t.Fatalf("selection after save = %q, want the merged 2-line group", sel.label)
	}
}

// TestCommentInsert_RejectsNonCommentContent verifies an insertion whose
// composed bytes are not comments is rejected with the E instruction.
func TestCommentInsert_RejectsNonCommentContent(t *testing.T) {
	src := "example.test {\n}\n"
	composed := "handle /x {\n}\nexample.test {\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(src),
		Content:      []byte(composed),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, saver, editor)
	m = resize(m, 120, 30)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	updated, launch := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // at the top
	m = updated.(*Model)
	m, done := runEditorLaunchCmd(t, m, launch)
	m.Update(done)
	if m.showDiff || m.pendingEdit != nil {
		t.Fatal("non-comment insertion must not become pending")
	}
	if !strings.Contains(m.statusMessage, "must contain only # comment lines") {
		t.Errorf("status = %q, want the comment-only instruction", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver called %d times, want 0", saver.calls)
	}
}

// TestCommentInsert_EscNavigation verifies Esc from the placement picker
// returns to the directive picker when it came from one, and cancels when
// it came from a document row.
func TestCommentInsert_EscNavigation(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	editor := &fakeEditor{}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{}, editor)
	m = resize(m, 120, 30)

	// From a block: Esc returns to the directive picker.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	last := len(m.structuredAddItems) - 1
	m.structuredAddCursor = last
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // into placement
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.showStructuredAdd || m.structuredAddMode != structuredAddPicker {
		t.Fatalf("Esc from block placement must return to the picker; show=%v mode=%v", m.showStructuredAdd, m.structuredAddMode)
	}
	// The directive items, comment entry included, are restored.
	restored := len(m.structuredAddItems) - 1
	if restored < 0 || m.structuredAddItems[restored] != structuredAddCommentEntry {
		t.Fatalf("picker items not restored after Esc: %v", m.structuredAddItems)
	}
	// Close the picker and go back to the document row.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp}) // document row
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showStructuredAdd {
		t.Fatal("Esc from a document placement must close the flow")
	}
	if editor.prepareInsertCalls != 0 {
		t.Errorf("PrepareInsert called %d times, want 0", editor.prepareInsertCalls)
	}
}

// TestCommentInsert_BOMTopSkipsBOM verifies the top placement inserts
// after a leading byte order mark.
func TestCommentInsert_BOMTopSkipsBOM(t *testing.T) {
	src := "\xEF\xBB\xBFexample.test {\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	editor := &fakeEditor{}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{}, editor)
	m = resize(m, 120, 30)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	updated, launch := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // at the top
	m = updated.(*Model)
	if launch == nil {
		t.Fatal("top placement must open the editor")
	}
	launch()
	if editor.prepareInsertCalls != 1 || editor.capturedInsertPos != 3 {
		t.Fatalf("PrepareInsert pos = %d, want 3 (after the BOM)", editor.capturedInsertPos)
	}
}
