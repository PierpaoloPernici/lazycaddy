package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
)

// TestEditorEdit_CommentGroupRangeAndReanchor verifies e on a comment
// leaf edits exactly the group range, opens the diff, saves through the
// normal pipeline and re-anchors the selection on the (resized) comment
// group after the reload.
func TestEditorEdit_CommentGroupRangeAndReanchor(t *testing.T) {
	src := "# header one\n# header two\nexample.test {\n\trespond ok\n}\n"
	// The edit adds a line to the group, so the group range changes.
	edited := "# edited header\n# header two\n# header three\nexample.test {\n\trespond ok\n}\n"
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
	m = expandAll(m)
	// Select the header comment leaf: doc, example.test, comments branch.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	sel := m.selectedItem()
	if sel.comment == nil {
		t.Fatalf("expected a comment leaf, got %q", sel.label)
	}
	wantRange := sel.comment.Range

	done := pressEditorKey(t, m)
	if editor.capturedRange != wantRange {
		t.Errorf("editor range = %+v, want the comment group range %+v", editor.capturedRange, wantRange)
	}
	m.Update(done)
	if !m.showDiff {
		t.Fatal("comment edit must open the diff modal")
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the diff must return a save command")
	}
	res, ok := cmd().(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", res)
	}
	m.Update(res)
	if got := string(m.state.Graph.Root.Source); got != edited {
		t.Errorf("root Source = %q, want %q", got, edited)
	}
	// The selection re-anchors on the same comment group (start line 1),
	// whose range changed with the extra line.
	sel = m.selectedItem()
	if sel.comment == nil || sel.comment.StartLine != 1 || sel.comment.Lines != 3 {
		t.Errorf("selection after save = %q, want the comment group at line 1 with 3 lines", sel.label)
	}
}

// TestEditorEdit_CommentRejectsNonCommentContent verifies a comment edit
// that turns the range into a structural construct is rejected with the
// E instruction and nothing becomes pending or savable.
func TestEditorEdit_CommentRejectsNonCommentContent(t *testing.T) {
	src := "# header\n# two\nexample.test {\n\trespond ok\n}\n"
	composed := "handle /x {\n}\nexample.test {\n\trespond ok\n}\n"
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
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = expandAll(m)
	for i := 0; i < 3; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if sel := m.selectedItem(); sel.comment == nil {
		t.Fatalf("expected a comment leaf, got %q", sel.label)
	}
	done := pressEditorKey(t, m)
	m.Update(done)
	if m.showDiff {
		t.Fatal("non-comment content must not open the diff")
	}
	if m.pendingEdit != nil {
		t.Fatal("pendingEdit must not be set for a rejected comment edit")
	}
	if !strings.Contains(m.statusMessage, "must contain only # comment lines") {
		t.Errorf("status = %q, want the comment-only instruction", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver called %d times, want 0", saver.calls)
	}
}

// TestEditorEdit_CommentCancelled verifies a cancelled comment edit
// changes nothing.
func TestEditorEdit_CommentCancelled(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "# header\nexample.test {\n\trespond ok\n}\n",
	}))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte("# header\nexample.test {\n\trespond ok\n}\n"),
		Cancelled:    true,
		SnapshotPath: "snap-1",
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = expandAll(m)
	for i := 0; i < 3; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	done := pressEditorKey(t, m)
	m.Update(done)
	if m.pendingEdit != nil || m.showDiff {
		t.Fatalf("cancelled comment edit must not open a diff; pendingEdit=%v showDiff=%v", m.pendingEdit != nil, m.showDiff)
	}
	if saver.calls != 0 {
		t.Errorf("saver called %d times, want 0", saver.calls)
	}
}

// TestEditorEdit_CommentsBranchNotEditable verifies the comments branch
// row itself is not a comment edit target.
func TestEditorEdit_CommentsBranchNotEditable(t *testing.T) {
	editor := &fakeEditor{}
	m := newLoadedModel(t, fakeLoader{state: commentState(t)}, editor)
	m = resize(m, 120, 30)
	for i := 0; i < 3; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	sel := m.selectedItem()
	if sel.label != "comments (3)" {
		t.Fatalf("expected the comments branch, got %q", sel.label)
	}
	if m.canEditSelection() {
		t.Error("canEditSelection must be false on the comments branch row")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")}); cmd != nil {
		t.Error("e on the comments branch must not launch an editor")
	}
	if editor.prepareCalls != 0 {
		t.Errorf("Prepare called %d times, want 0", editor.prepareCalls)
	}
}

// TestEditorEdit_CommentShortenedKeepsFollowingBlock verifies a comment
// edit that shrinks the group is checked against the actual edited bytes
// only: the following block must not be swallowed into the comment-only
// check (the original range end no longer bounds the edited content).
func TestEditorEdit_CommentShortenedKeepsFollowingBlock(t *testing.T) {
	src := "# a\n# b\nexample.test {\n\trespond ok\n}\n"
	composed := "# x\nexample.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(src),
		Content:      []byte(composed),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, editor)
	m = resize(m, 120, 30)
	m = expandAll(m)
	for i := 0; i < 3; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if sel := m.selectedItem(); sel.comment == nil {
		t.Fatalf("expected a comment leaf, got %q", sel.label)
	}
	done := pressEditorKey(t, m)
	m.Update(done)
	if !m.showDiff {
		t.Fatalf("a shortened comment edit must open the diff; status = %q", m.statusMessage)
	}
}

// TestEditorEdit_CommentLengthenedNonCommentRejected verifies non-comment
// bytes appended at the end of a lengthened comment group do not escape
// the comment-only check (the original range end no longer bounds the
// edited content).
func TestEditorEdit_CommentLengthenedNonCommentRejected(t *testing.T) {
	src := "# a\nexample.test {\n}\n"
	composed := "# a\nrespond ok\nexample.test {\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(src),
		Content:      []byte(composed),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, editor)
	m = resize(m, 120, 30)
	m = expandAll(m)
	for i := 0; i < 3; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if sel := m.selectedItem(); sel.comment == nil {
		t.Fatalf("expected a comment leaf, got %q", sel.label)
	}
	done := pressEditorKey(t, m)
	m.Update(done)
	if m.showDiff || m.pendingEdit != nil {
		t.Fatal("a lengthened non-comment edit must be rejected")
	}
	if !strings.Contains(m.statusMessage, "must contain only # comment lines") {
		t.Errorf("status = %q, want the comment-only instruction", m.statusMessage)
	}
}

// TestEditorError_ClearsCommentState verifies a Prepare failure during a
// comment edit clears the comment state, so the next (node) round-trip is
// not wrongly checked as a comment edit.
func TestEditorError_ClearsCommentState(t *testing.T) {
	src := "# header\nexample.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	editor := &fakeEditor{prepareErr: errors.New("boom")}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, editor)
	m = resize(m, 120, 30)
	m = expandAll(m)
	for i := 0; i < 3; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if sel := m.selectedItem(); sel.comment == nil {
		t.Fatalf("expected a comment leaf, got %q", sel.label)
	}
	// First round-trip: a comment edit whose Prepare fails.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	msg := cmd()
	errMsg, ok := msg.(editorErrorMsg)
	if !ok {
		t.Fatalf("got %T, want editorErrorMsg", msg)
	}
	m.Update(errMsg)
	if m.commentEditStartLine != 0 || m.commentInsertActive {
		t.Fatalf("comment state not cleared after the error: startLine=%d insert=%v", m.commentEditStartLine, m.commentInsertActive)
	}
	// Second round-trip: a node edit must not be checked as a comment.
	composed := "# header\nexample.test {\n\trespond ok\n\tencode gzip\n}\n"
	editor.prepareErr = nil
	editor.result = app.EditResult{
		Original:     []byte(src),
		Content:      []byte(composed),
		Changed:      true,
		SnapshotPath: "snap-2",
	}
	// Back to the site block row: leaf, comments branch, example.test.
	for i := 0; i < 2; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	}
	if sel := m.selectedItem(); !sel.hasNode {
		t.Fatalf("expected the site block, got %q", sel.label)
	}
	done := pressEditorKey(t, m)
	m.Update(done)
	if !m.showDiff {
		t.Fatalf("node edit must open the diff after a comment error; status = %q", m.statusMessage)
	}
}

// TestCommentContentOK verifies the comment-only content guard accepts
// comment lines and blanks and rejects any non-comment content.
func TestCommentContentOK(t *testing.T) {
	valid := []string{"# a\n# b\n", "# a\n\n# b\n", "  # indented\n", "#\n", "# a\n", "# a # b\n", "\n"}
	for _, tc := range valid {
		if !commentContentOK([]byte(tc)) {
			t.Errorf("commentContentOK(%q) = false, want true", tc)
		}
	}
	invalid := []string{"handle /x {\n}\n", "# a\nrespond ok\n", "  respond ok\n", "# a\n\nx\n", "a b\n"}
	for _, tc := range invalid {
		if commentContentOK([]byte(tc)) {
			t.Errorf("commentContentOK(%q) = true, want false", tc)
		}
	}
}
