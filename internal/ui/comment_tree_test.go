package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
)

// commentFixture has top-level groups at the header, between blocks and
// at the footer, plus an inside-block comment that must never become a
// row.
const commentFixture = "# header one\n" +
	"# header two\n" +
	"example.test {\n" +
	"\t# inside block\n" +
	"\trespond ok\n" +
	"}\n" +
	"# between\n" +
	"example.net {\n" +
	"}\n" +
	"# footer\n"

func commentState(t *testing.T) *app.State {
	t.Helper()
	return stateFor(t, "config/Caddyfile", fsReader(map[string]string{"config/Caddyfile": commentFixture}))
}

// TestTree_CommentBranchCollapsedByDefault verifies the virtual comments
// branch starts collapsed: a single row under the document, after the
// structural rows, with no group leaves visible.
func TestTree_CommentBranchCollapsedByDefault(t *testing.T) {
	m := newLoadedModel(t, fakeLoader{state: commentState(t)})
	m = resize(m, 120, 30)
	want := []string{"Caddyfile", "example.test", "example.net", "comments (3)"}
	if got := itemLabels(m.items); got != strings.Join(want, ", ") {
		t.Fatalf("items = %v, want %v", got, strings.Join(want, ", "))
	}
	if !m.items[0].hasChildren {
		t.Error("document row must be expandable when top-level comments exist")
	}
	if m.items[3].label != "comments (3)" || !m.items[3].hasChildren {
		t.Errorf("comments branch = %+v, want an expandable comments (3) row", m.items[3])
	}
}

// TestTree_CommentBranchExpandShowsLeaves verifies expanding the branch
// shows one leaf per group with its line span, preview and, when
// available, the block it documents.
func TestTree_CommentBranchExpandShowsLeaves(t *testing.T) {
	m := newLoadedModel(t, fakeLoader{state: commentState(t)})
	m = resize(m, 120, 30)
	m = expandAll(m)
	want := []string{
		"Caddyfile",
		"example.test",
		"example.net",
		"comments (3)",
		"lines 1-2 · header one → example.test",
		"lines 7-7 · between → example.net",
		"lines 10-10 · footer",
	}
	if got := itemLabels(m.items); got != strings.Join(want, ", ") {
		t.Fatalf("items = %v, want %v", got, strings.Join(want, ", "))
	}
	sel := m.items[4]
	if sel.hasNode || sel.comment == nil {
		t.Fatalf("comment leaf = {hasNode:%v comment:%v}, want a comment row", sel.hasNode, sel.comment)
	}
	if sel.comment.Lines != 2 {
		t.Errorf("comment leaf lines = %d, want 2", sel.comment.Lines)
	}
}

// TestTree_BareCommentLeafLabel verifies a bare "#" group renders with a
// placeholder preview instead of an empty label.
func TestTree_BareCommentLeafLabel(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "#\nexample.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = expandAll(m)
	labels := itemLabels(m.items)
	if !strings.Contains(labels, "lines 1-1 · #") {
		t.Errorf("labels = %v, want the bare-comment placeholder", labels)
	}
}

// TestTree_CommentSelectionRevealsRange verifies selecting a comment leaf
// reveals its exact line range in the source pane and titles the pane
// with the comment span.
func TestTree_CommentSelectionRevealsRange(t *testing.T) {
	var src strings.Builder
	src.WriteString("example.test {\n\trespond ok\n}\n")
	for i := 0; i < 70; i++ {
		src.WriteString("# padding\n")
	}
	src.WriteString("pbs.example.test {\n\trespond pbs\n}\n")
	state := stateFor(t, "config/Caddyfile", func(string) ([]byte, error) {
		return []byte(src.String()), nil
	})
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 12)
	// Rows: doc, example.test, pbs.example.test, comments (1). Expand the
	// comments branch, then land on its leaf.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // pbs.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // comments (1) branch
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // comment leaf
	if sel := m.selectedItem(); sel.comment == nil {
		t.Fatalf("expected a comment leaf, got %q", sel.label)
	}
	m.View()
	if m.viewport.YOffset == 0 {
		t.Errorf("YOffset = 0, want a reveal of the comment group (lines 4-73)")
	}
	if !strings.Contains(m.sourceTitle, "comment (lines 4-73)") {
		t.Errorf("source title = %q, want the comment line span", m.sourceTitle)
	}
}

// TestCopy_CommentGroupCopiesExactBytes verifies y on a comment leaf
// copies exactly the group's source bytes.
func TestCopy_CommentGroupCopiesExactBytes(t *testing.T) {
	clip := &fakeClipboard{}
	m := newLoadedModel(t, fakeLoader{state: commentState(t)}, clip)
	m = resize(m, 120, 30)
	m = expandAll(m)
	// Select the header comment leaf: doc, example.test, example.net,
	// comments branch, then the leaf.
	for i := 0; i < 4; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if sel := m.selectedItem(); sel.comment == nil {
		t.Fatalf("expected a comment leaf, got %q", sel.label)
	}
	m = pressCopy(t, m)
	if got := string(clip.content); got != "# header one\n# header two\n" {
		t.Errorf("copied = %q, want the exact comment group bytes", got)
	}
}

// TestTree_ExpandAllWithComments verifies the + key expands the virtual
// comments branch along with the structural branches.
func TestTree_ExpandAllWithComments(t *testing.T) {
	m := newLoadedModel(t, fakeLoader{state: commentState(t)})
	m = resize(m, 120, 30)
	labels := itemLabels(m.items)
	if strings.Contains(labels, "lines 1-2") {
		t.Fatalf("comment leaves visible before expand-all: %v", labels)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'+'}})
	labels = itemLabels(m.items)
	if !strings.Contains(labels, "lines 1-2 · header one → example.test") {
		t.Errorf("comment leaves not expanded by +: %v", labels)
	}
}

// TestCopy_CommentGroupInvalidRange verifies y on a comment leaf whose
// range is corrupt refuses instead of slicing out of bounds.
func TestCopy_CommentGroupInvalidRange(t *testing.T) {
	clip := &fakeClipboard{}
	m := newLoadedModel(t, fakeLoader{state: commentState(t)}, clip)
	m = resize(m, 120, 30)
	m = expandAll(m)
	for i := 0; i < 4; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if sel := m.selectedItem(); sel.comment == nil {
		t.Fatalf("expected a comment leaf, got %q", sel.label)
	}
	m.items[m.cursor].comment.Range.Start = -1
	m = pressCopy(t, m)
	if clip.calls != 0 || len(clip.content) != 0 {
		t.Fatal("copy with an invalid comment range must not touch the clipboard")
	}
	if !strings.Contains(m.statusMessage, "comment range is invalid") {
		t.Errorf("status = %q, want the invalid-range error", m.statusMessage)
	}
}

// TestTree_CollapseAllCollapsesCommentBranch verifies the - key
// collapses the comments branch along with the structural branches.
func TestTree_CollapseAllCollapsesCommentBranch(t *testing.T) {
	m := newLoadedModel(t, fakeLoader{state: commentState(t)})
	m = resize(m, 120, 30)
	m = expandAll(m)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-")})
	labels := itemLabels(m.items)
	if strings.Contains(labels, "lines 1-2") {
		t.Errorf("comment leaves still visible after collapse-all: %v", labels)
	}
	if !strings.Contains(labels, "comments (3)") {
		t.Errorf("comments branch missing after collapse-all: %v", labels)
	}
}
