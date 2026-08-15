package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
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
	// The comment entry is sorted alphabetically with the directives
	// (comment < encode < file_server < … < tls).
	if len(m.structuredAddItems) < 2 || m.structuredAddItems[0] != structuredAddCommentEntry {
		t.Fatalf("first picker item = %v, want the comment entry first", m.structuredAddItems)
	}
	for i := 1; i < len(m.structuredAddItems); i++ {
		if m.structuredAddItems[i-1] > m.structuredAddItems[i] {
			t.Errorf("picker items not sorted at %d: %v", i, m.structuredAddItems)
			break
		}
	}
	m.structuredAddCursor = 0
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

// TestCommentInsert_PickerHelpOnCommentEntry verifies ctrl+h on the
// sorted-first comment entry is refused locally instead of opening a
// non-existent documentation page.
func TestCommentInsert_PickerHelpOnCommentEntry(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	var gotURL string
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m.browser = app.BrowserFunc(func(_ context.Context, url string) error {
		gotURL = url
		return nil
	})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // example.test
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	if len(m.structuredAddItems) == 0 || m.structuredAddItems[0] != structuredAddCommentEntry {
		t.Fatalf("picker items = %v, want the comment entry first", m.structuredAddItems)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	m = updated.(*Model)
	if cmd != nil {
		t.Fatal("ctrl+h on the comment entry must not open a browser command")
	}
	if gotURL != "" {
		t.Errorf("opened URL = %q, want none for the comment entry", gotURL)
	}
	if !strings.Contains(m.statusMessage, "no documentation page") {
		t.Errorf("status = %q, want the no-documentation hint", m.statusMessage)
	}
}

// TestCommentInsert_PlacementNavigation verifies the placement sub-picker
// navigation: up/down move and clamp the cursor, unknown keys are inert,
// and ctrl+c cancels.
func TestCommentInsert_PlacementNavigation(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	if m.structuredAddMode != structuredAddCommentPlacement || m.structuredAddCursor != 0 {
		t.Fatalf("placement state = mode:%v cursor:%d, want placement with cursor 0", m.structuredAddMode, m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.structuredAddCursor != 1 {
		t.Fatalf("down moved cursor to %d, want 1", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // clamped at the last item
	if m.structuredAddCursor != 1 {
		t.Fatalf("down clamped cursor to %d, want 1", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.structuredAddCursor != 0 {
		t.Fatalf("up moved cursor to %d, want 0", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlK})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp}) // clamped at the first item
	if m.structuredAddCursor != 0 {
		t.Fatalf("up clamped cursor to %d, want 0", m.structuredAddCursor)
	}
	// An unknown key leaves the picker open and the cursor unchanged.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !m.showStructuredAdd || m.structuredAddCursor != 0 {
		t.Fatalf("unknown key changed the placement state: show=%v cursor=%d", m.showStructuredAdd, m.structuredAddCursor)
	}
	// ctrl+c cancels the flow.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.showStructuredAdd {
		t.Fatal("ctrl+c must close the placement picker")
	}
}

// TestCommentInsert_PlacementViewRenders verifies the placement sub-picker
// renders its options and the footer advertises the placement keys.
func TestCommentInsert_PlacementViewRenders(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	view := stripANSI(m.View())
	for _, want := range []string{"Insert comment", commentPlacementTop, commentPlacementBottom, "choose placement"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

// TestCommentInsert_FromDocumentRowBottom verifies the bottom placement
// inserts at the end of the document.
func TestCommentInsert_FromDocumentRowBottom(t *testing.T) {
	src := "example.test {\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	editor := &fakeEditor{}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{}, editor)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // at the bottom of the file
	updated, launch := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	launch()
	if editor.capturedInsertPos != len(src) {
		t.Fatalf("PrepareInsert pos = %d, want %d (end of the document)", editor.capturedInsertPos, len(src))
	}
}

// TestCommentInsert_FromBlockBefore verifies the before-this-block
// placement inserts at the block start.
func TestCommentInsert_FromBlockBefore(t *testing.T) {
	src := "example.test {\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	editor := &fakeEditor{}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{}, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // example.test
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	m.structuredAddCursor = 0 // the comment entry, sorted first
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m.structuredAddCursor = 0 // before this block
	updated, launch := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	launch()
	wantPos := state.Graph.Root.Nodes[0].Range.Start
	if editor.capturedInsertPos != wantPos {
		t.Fatalf("PrepareInsert pos = %d, want %d (start of the site block)", editor.capturedInsertPos, wantPos)
	}
}

// TestCommentInsert_UnknownPlacementRejected verifies an unrecognized
// placement string is refused instead of guessing an offset.
func TestCommentInsert_UnknownPlacementRejected(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	editor := &fakeEditor{}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{}, editor)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	m.structuredAddItems = []string{"somewhere else"}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd != nil {
		t.Fatal("unknown placement must not launch an editor")
	}
	if !strings.Contains(m.statusMessage, "unknown placement") {
		t.Errorf("status = %q, want the unknown-placement error", m.statusMessage)
	}
	if editor.prepareInsertCalls != 0 {
		t.Errorf("PrepareInsert called %d times, want 0", editor.prepareInsertCalls)
	}
}

// TestCommentInsert_PlacementCursorOutOfRange verifies a stale cursor does
// not launch an editor.
func TestCommentInsert_PlacementCursorOutOfRange(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	editor := &fakeEditor{}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{}, editor)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	m.structuredAddCursor = 99
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd != nil {
		t.Fatal("an out-of-range placement cursor must not launch an editor")
	}
	if editor.prepareInsertCalls != 0 {
		t.Errorf("PrepareInsert called %d times, want 0", editor.prepareInsertCalls)
	}
}

// TestCommentInsert_CursorOutOfRangeOnDocRow verifies a stale tree cursor
// (no selection) refuses a with the add-unavailable hint.
func TestCommentInsert_CursorOutOfRangeOnDocRow(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	m.cursor = 99
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	if m.showStructuredAdd {
		t.Fatal("a with no selection must not open a picker")
	}
	if !strings.Contains(m.statusMessage, "add unavailable") {
		t.Errorf("status = %q, want the add-unavailable hint", m.statusMessage)
	}
}

// TestIsTopLevelNodeNilDocument verifies the defensive nil handling.
func TestIsTopLevelNodeNilDocument(t *testing.T) {
	if isTopLevelNode(nil, caddyfile.Node{Name: "x"}) {
		t.Error("isTopLevelNode(nil, …) = true, want false")
	}
}

// TestCommentInsert_UnavailableOnNestedNonHandlerBlock verifies a on a
// nested non-handler block (a log block inside a site) refuses both the
// directive flow and the comment flow: comments inside blocks are not
// first-class groups.
func TestCommentInsert_UnavailableOnNestedNonHandlerBlock(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\tlog {\n\t\toutput file /tmp/lazycaddy-test.log\n\t}\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	m = expandAll(m)
	for i := 0; i < 2; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // example.test, log
	}
	if sel := m.selectedItem(); !sel.hasNode || sel.node.Name != "log" {
		t.Fatalf("expected the nested log block, got %q", sel.label)
	}
	if m.canAddComment() {
		t.Error("canAddComment = true on a nested non-handler block")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	if m.showStructuredAdd {
		t.Fatal("a on a nested non-handler block must not open a picker")
	}
}

// TestCommentInsert_PlacementViewFromPicker verifies the placement view
// rendered from a top-level block (via the picker entry) names the block
// as its target.
func TestCommentInsert_PlacementViewFromPicker(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // example.test
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	m.structuredAddCursor = 0 // the comment entry
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	view := stripANSI(m.View())
	for _, want := range []string{"Target: example.test", commentPlacementBefore, commentPlacementAfter} {
		if !strings.Contains(view, want) {
			t.Errorf("placement view missing %q:\n%s", want, view)
		}
	}
}

// TestCommentInsert_PlacementViewTiny verifies the placement picker still
// renders in a very small terminal (row-count clamps).
func TestCommentInsert_PlacementViewTiny(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 30, 6)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	view := stripANSI(m.View())
	if !strings.Contains(view, "Insert comment") {
		t.Errorf("tiny placement view missing the title:\n%s", view)
	}
}

// TestStructuredAddUnavailableOnCommentsBranch verifies a on the virtual
// comments branch row is refused: the branch is a container, not a
// document, so the header/footer placement picker is not offered.
func TestStructuredAddUnavailableOnCommentsBranch(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": commentFixture,
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	// doc, example.test, example.net, comments (3) branch.
	for i := 0; i < 3; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if sel := m.selectedItem(); sel.label != "comments (3)" {
		t.Fatalf("expected the comments branch, got %q", sel.label)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(*Model)
	if m.showStructuredAdd {
		t.Fatal("a on the comments branch must not open a picker")
	}
	if !strings.Contains(m.statusMessage, "add unavailable") {
		t.Errorf("status = %q, want the add-unavailable hint", m.statusMessage)
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
	m.structuredAddCursor = 0                          // the comment entry, sorted first
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // into placement
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.showStructuredAdd || m.structuredAddMode != structuredAddPicker {
		t.Fatalf("Esc from block placement must return to the picker; show=%v mode=%v", m.showStructuredAdd, m.structuredAddMode)
	}
	// The directive items, comment entry included, are restored.
	if len(m.structuredAddItems) == 0 || m.structuredAddItems[0] != structuredAddCommentEntry {
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
