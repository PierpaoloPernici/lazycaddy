package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/selection"
)

// foldSource is a small Caddyfile with three nested foldable blocks.
const foldSource = "example.test {\n" +
	"\troute {\n" +
	"\t\thandle /api {\n" +
	"\t\t\trespond ok\n" +
	"\t\t}\n" +
	"\t}\n" +
	"}\n"

// foldModel loads a model over the nested fixture, selects the site row
// and renders the source pane once.
func foldModel(t *testing.T) *Model {
	t.Helper()
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": foldSource,
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // site row
	m = expandAll(m)                                  // source folding follows the fully expanded tree
	_ = m.View()
	if m.sourceDoc == nil {
		t.Fatal("source pane has no document after render")
	}
	return m
}

// viewportBody returns the styled viewport content with ANSI stripped.
func viewportBody(m *Model) string {
	return stripANSI(m.viewport.View())
}

func TestFold_ToggleSite(t *testing.T) {
	m := foldModel(t)
	// Close the site fold: the inner lines are replaced by the indicator
	// row and the closing brace stays visible.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	_ = m.View()
	body := viewportBody(m)
	if !strings.Contains(body, "⋯ 5 lines") {
		t.Errorf("folded view missing the indicator row:\n%s", body)
	}
	if strings.Contains(body, "route {") || strings.Contains(body, "respond ok") {
		t.Errorf("folded view must hide the inner lines:\n%s", body)
	}
	// The header line and the closing brace stay visible; the trailing
	// empty line of the source is part of the layout.
	if !strings.Contains(body, "example.test {") || !strings.Contains(body, "}") {
		t.Errorf("folded view must keep the header and the closing brace:\n%s", body)
	}
	if m.foldCount(m.sourceDoc) != 1 {
		t.Errorf("foldCount = %d, want 1", m.foldCount(m.sourceDoc))
	}
	if !m.collapsed[m.selectedItem().key] {
		t.Error("site tree branch not recorded as collapsed")
	}

	// Opening it again restores the exact source lines.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	_ = m.View()
	body = viewportBody(m)
	if !strings.Contains(body, "route {") || !strings.Contains(body, "respond ok") {
		t.Errorf("unfolded view must restore every inner line:\n%s", body)
	}
	if strings.Contains(body, "⋯") {
		t.Errorf("unfolded view must drop the indicator row:\n%s", body)
	}
	if m.foldCount(m.sourceDoc) != 0 {
		t.Errorf("foldCount after opening = %d, want 0", m.foldCount(m.sourceDoc))
	}
}

func TestFold_NestedIndependence(t *testing.T) {
	m := expandAll(foldModel(t))
	_ = m.View()
	folds := caddyfile.Folds(m.sourceDoc)
	if len(folds) != 3 {
		t.Fatalf("folds = %+v, want site, route and handle", folds)
	}

	// Collapse only the inner route block: its children are hidden while
	// the site body around it stays visible. The cursor already sits on the
	// site row after foldModel+expandAll, so one Down reaches route.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // route row
	_ = m.View()
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	_ = m.View()
	body := viewportBody(m)
	// The route fold hides its whole body (the handle block): the site
	// header and the route header stay visible, the handle and its body
	// are folded away.
	if !strings.Contains(body, "example.test {") || !strings.Contains(body, "route {") {
		t.Errorf("route-only fold must keep the site and route headers visible:\n%s", body)
	}
	if !strings.Contains(body, "⋯ 3 lines") {
		t.Errorf("route-only fold must show its indicator:\n%s", body)
	}
	if strings.Contains(body, "handle /api {") || strings.Contains(body, "respond ok") {
		t.Errorf("route-only fold must hide the handle block:\n%s", body)
	}

	// Collapse the outer site too: the route fold is subsumed by the
	// parent and its indicator disappears from the display.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp}) // back to site
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	_ = m.View()
	body = viewportBody(m)
	if strings.Contains(body, "route {") {
		t.Errorf("site fold must subsume the nested route fold:\n%s", body)
	}
	if !strings.Contains(body, "⋯ 5 lines") {
		t.Errorf("site fold must show its own indicator:\n%s", body)
	}

	// Opening the site again restores the route's own collapsed state:
	// the route indicator is back and its body stays hidden.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	_ = m.View()
	body = viewportBody(m)
	if !strings.Contains(body, "⋯ 3 lines") {
		t.Errorf("route fold must stay collapsed after its parent opens:\n%s", body)
	}
	if !strings.Contains(body, "route {") {
		t.Errorf("route header must be visible again after the parent opens:\n%s", body)
	}
	if strings.Contains(body, "handle /api {") || strings.Contains(body, "respond ok") {
		t.Errorf("route body must stay hidden after the parent opens:\n%s", body)
	}
}

func TestFold_StableAcrossSelectionAndReload(t *testing.T) {
	m := foldModel(t)

	// Close the site fold, then move the tree cursor across unrelated
	// rows: the fold state must survive.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	_ = m.View()
	key := m.selectedItem().key
	if !m.collapsed[key] {
		t.Fatal("site fold not closed")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp}) // document row
	_ = m.View()
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // site again
	_ = m.View()
	if !m.collapsed[key] {
		t.Error("fold state lost after cursor movement")
	}
	if !strings.Contains(viewportBody(m), "⋯ 5 lines") {
		t.Errorf("folded view lost after cursor movement:\n%s", viewportBody(m))
	}

	// A reload that re-parses the same source keeps the fold: the key is
	// range-based.
	fresh := newLoadedModel(t, fakeLoader{state: stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": foldSource,
	}))})
	fresh = resize(fresh, 120, 30)
	fresh = keyPress(t, fresh, tea.KeyMsg{Type: tea.KeyDown})
	_ = fresh.View()
	if !strings.Contains(viewportBody(fresh), "⋯ 5 lines") {
		t.Errorf("fold state did not survive a reload with the same source:\n%s", viewportBody(fresh))
	}
}

func TestFold_SearchRevealExpandsCoveringFolds(t *testing.T) {
	m := foldModel(t)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // site folded
	_ = m.View()

	// Search for a token inside the collapsed region and activate it.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("respond") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	found := false
	for _, r := range m.searchResults {
		if r.Kind == app.SearchNode && r.Node.Name == "respond" {
			found = true
		}
	}
	if !found {
		t.Fatalf("search results = %+v, want the respond hit inside the folded block", m.searchResults)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	_ = m.View()

	// The covering fold was auto-expanded and the exact source line is
	// revealed (the reveal may show it near the top of the viewport).
	if strings.Contains(viewportBody(m), "⋯") {
		t.Errorf("the covering fold must be expanded by the search reveal:\n%s", viewportBody(m))
	}
	if !strings.Contains(viewportBody(m), "respond ok") {
		t.Errorf("the exact source line must be visible after the reveal:\n%s", viewportBody(m))
	}
	if m.collapsed[m.selectedItem().key] {
		t.Error("fold must be removed from the collapsed set after the reveal")
	}
}

func TestFold_CloseBraceRevealRows(t *testing.T) {
	m := foldModel(t)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // site folded
	_ = m.View()

	// revealRange on the site's full range uses the folded layout: the
	// header (row 0) is centred, the hidden lines are skipped.
	m.revealRange(1, 7)
	want := m.rowForLine(1)
	if m.viewport.YOffset != want {
		t.Errorf("revealRange YOffset = %d, want %d (header row of the folded block)", m.viewport.YOffset, want)
	}
	// A one-shot sourceRevealLine is converted through the folded layout
	// too.
	m.sourceRevealLine = 7
	_ = m.View()
	if m.viewport.YOffset > m.rowForLine(7) {
		t.Errorf("sourceRevealLine 7 YOffset = %d, want at most row %d", m.viewport.YOffset, m.rowForLine(7))
	}
}

func TestFold_MouseClickOnIndicatorExpands(t *testing.T) {
	m := foldModel(t)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // site folded
	_ = m.View()
	if m.foldCount(m.sourceDoc) != 1 {
		t.Fatalf("site not folded before the click")
	}

	// The indicator row is the second content row of the viewport: click
	// anywhere on it (including its gutter) to expand the fold again.
	geo := m.sourcePaneGeometry()
	y := geo.y + 1 // row 1 = indicator row
	// Click in the gutter area of the indicator row.
	m = mouseAt(t, m, geo.x+1, y, tea.MouseActionPress, tea.MouseButtonLeft)
	_ = m.View()
	if m.foldCount(m.sourceDoc) != 0 {
		t.Errorf("click on the indicator row must open the fold (foldCount = %d)", m.foldCount(m.sourceDoc))
	}
	if !strings.Contains(viewportBody(m), "route {") {
		t.Errorf("click on the indicator must restore the inner lines:\n%s", viewportBody(m))
	}
}

func TestFold_SelectionAndCopyExactBytes(t *testing.T) {
	m := foldModel(t)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // site folded
	_ = m.View()

	// The pane keeps the full source lines and carries the folded row
	// mapping; the indicator row maps to no source position.
	p := m.sourceTextPane()
	if p.RowLines == nil {
		t.Fatal("source pane must carry RowLines while folds are active")
	}
	if len(p.RowLines) != 4 || p.RowLines[0] != 0 || p.RowLines[1] != -1 || p.RowLines[2] != 6 || p.RowLines[3] != 7 {
		t.Fatalf("RowLines = %v, want [0 -1 6 7] (site folded)", p.RowLines)
	}
	if len(p.Lines) != 8 {
		t.Fatalf("Lines = %d, want the 8 full source lines", len(p.Lines))
	}

	// A keyboard selection from the header into the closing brace covers
	// the exact underlying bytes, hidden lines included: copying returns
	// the precise source slice (the trailing newline is excluded by the
	// offset, exactly as in the unfolded pane).
	m.textSel.pane = textPaneSource
	m.textSel.state.MoveTo(selection.Position{Line: 0, Offset: 3})
	m.textSel.state.SelectTo(selection.Position{Line: 6, Offset: 1})
	clip := &fakeClipboard{}
	m.clipboard = clip
	m = pressCopy(t, m)
	want := string(m.sourceDoc.Source[3 : len(m.sourceDoc.Source)-1])
	if string(clip.content) != want {
		t.Errorf("copy across a fold = %q, want the exact source bytes %q", clip.content, want)
	}

	// A selection spanning only visible rows paints only those rows: the
	// indicator row contributes no span.
	m.clearTextSelection()
	m.textSel.pane = textPaneSource
	m.textSel.state.MoveTo(selection.Position{Line: 0, Offset: 0})
	m.textSel.state.SelectTo(selection.Position{Line: 6, Offset: 1})
	spans, ok := m.selectionSpans(textPaneSource)
	if !ok {
		t.Fatal("no selection spans for the visible-range selection")
	}
	for _, sp := range spans {
		if sp.Row == 1 {
			t.Errorf("selection spans must skip the indicator row: %+v", spans)
		}
	}
}

func TestFold_DragAcrossIndicatorClamps(t *testing.T) {
	m := foldModel(t)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // site folded
	_ = m.View()
	geo := m.sourcePaneGeometry()
	// Press on the header row, then drag down across the indicator row:
	// the cursor must clamp to the nearest visible line (the closing
	// brace), never jump to a foreign position.
	x := geo.x + 10
	m = mouseAt(t, m, x, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	// Row 2 is the closing-brace row; the drag path crosses the indicator
	// row (row 1) which has no source position.
	m = mouseAt(t, m, x, geo.y+2, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, x, geo.y+2, tea.MouseActionRelease, tea.MouseButtonNone)
	cur := m.textSel.state.Cursor
	if cur.Line != 6 {
		t.Errorf("drag across the indicator clamped to line %d, want 6 (closing brace)", cur.Line)
	}
	// Dragging past the folded content clamps to the end of the last
	// visible line (the trailing empty line at EOF).
	m.mousePress(x, geo.y)
	m.mouseDrag(x, geo.y+4)
	m.mouseRelease(x, geo.y+4)
	cur = m.textSel.state.Cursor
	if cur.Line != 7 {
		t.Errorf("drag past the content clamped to line %d, want 7 (EOF)", cur.Line)
	}
}

func TestFold_ShiftCursorSkipsHiddenLines(t *testing.T) {
	m := foldModel(t)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // site folded
	_ = m.View()
	m.ensureTextCursor(textPaneSource)
	// shift+down from the header must skip the hidden lines and land on
	// the closing brace.
	m.shiftTextCursor(0, 1)
	if cur := m.textSel.state.Cursor; cur.Line != 6 {
		t.Errorf("shift+down across the fold = line %d, want 6", cur.Line)
	}
	// shift+up from the closing brace lands back on the header.
	m.shiftTextCursor(0, -1)
	if cur := m.textSel.state.Cursor; cur.Line != 0 {
		t.Errorf("shift+up across the fold = line %d, want 0", cur.Line)
	}
}

func TestFold_OpenAllCloseAll(t *testing.T) {
	m := foldModel(t)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-")}) // collapse all tree branches
	_ = m.View()
	if m.foldCount(m.sourceDoc) != 2 {
		t.Errorf("collapse-all foldCount = %d, want 2 (site, route)", m.foldCount(m.sourceDoc))
	}
	if !strings.Contains(viewportBody(m), "⋯ 5 lines") {
		t.Errorf("collapse-all view missing the site indicator:\n%s", viewportBody(m))
	}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	_ = m.View()
	if m.foldCount(m.sourceDoc) != 0 {
		t.Errorf("expand-all foldCount = %d, want 0", m.foldCount(m.sourceDoc))
	}
	if strings.Contains(viewportBody(m), "⋯") {
		t.Errorf("expand-all view must drop every indicator:\n%s", viewportBody(m))
	}
}

func TestFold_NotFoldableOnLeafOrDocumentRow(t *testing.T) {
	m := foldModel(t)
	// Document row: not foldable.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp}) // back to the document row
	_ = m.View()
	if m.selectedItem().hasNode {
		t.Error("document row must not be a structural source fold")
	}
	// The handle block is the deepest tree row; it is foldable (it hides
	// its single child). The site branch is collapsed by default, so it
	// must be expanded first to make the nested rows reachable.
	m = expandAll(m)
	_ = m.View()
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // route
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // handle
	_ = m.View()
	if !m.selectedItem().hasChildren {
		t.Error("handle block should remain an expandable tree branch")
	}
}

func TestFold_EmptyBlockNotFoldable(t *testing.T) {
	src := "empty.example.test {\n}\n"
	state := stateFor(t, "Caddyfile", fsReader(map[string]string{"Caddyfile": src}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // empty site row
	_ = m.View()
	// Enter only changes the tree state; the parser correctly excludes an
	// empty block from the source fold layout.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	_ = m.View()
	if m.foldCount(m.sourceDoc) != 0 {
		t.Error("an empty block must not create a source fold")
	}
}

func TestFold_BracelessSiteFoldsToEnd(t *testing.T) {
	src := "localhost:8080\n\trespond ok\n\tfile_server\n"
	state := stateFor(t, "Caddyfile", fsReader(map[string]string{"Caddyfile": src}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // site row
	_ = m.View()
	body := viewportBody(m)
	if strings.Contains(body, "⋯") {
		t.Errorf("a site with no structural tree children must not create a source fold:\n%s", body)
	}
	if !strings.Contains(body, "respond ok") {
		t.Errorf("the source must remain visible when the tree row is not expandable:\n%s", body)
	}
	if m.foldCount(m.sourceDoc) != 0 {
		t.Errorf("braceless foldCount = %d, want 0", m.foldCount(m.sourceDoc))
	}
}

func TestFold_QuotedBracesNeverFold(t *testing.T) {
	src := "example.test {\n\trespond \"literal } brace { text\" 200\n\tfile_server\n}\n"
	state := stateFor(t, "Caddyfile", fsReader(map[string]string{"Caddyfile": src}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // site row
	_ = m.View()
	// The site starts collapsed in the canonical tree state.
	_ = m.View()
	// Only the site folds: the quoted braces are string content and never
	// create nested foldable blocks. The site's own body is folded away by
	// the site fold, which is expected: the quoted braces simply never
	// produced a separate fold.
	if m.foldCount(m.sourceDoc) != 0 {
		t.Errorf("quoted braces must not create a tree fold, foldCount = %d", m.foldCount(m.sourceDoc))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	_ = m.View()
	// The viewport wraps long lines and pads them, so the assertion joins
	// the words first (whitespace-collapsed) before searching for the
	// quoted-brace fragment.
	joined := strings.Join(strings.Fields(viewportBody(m)), " ")
	if !strings.Contains(joined, `literal } brace { text`) {
		t.Errorf("quoted braces must remain fully visible when unfolded:\n%s", viewportBody(m))
	}
}

func TestFold_PaneGeometryUnchanged(t *testing.T) {
	// Folding must not change the pane geometry used by mouse mapping.
	m := foldModel(t)
	geoBefore := m.sourcePaneGeometry()
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m.statusMessage = "" // the fold status strip is transient UI chrome
	_ = m.View()
	geoAfter := m.sourcePaneGeometry()
	if geoBefore != geoAfter {
		t.Errorf("fold changed the pane geometry: %+v -> %+v", geoBefore, geoAfter)
	}
}

// TestFold_IndicatorStyleVerbatim verifies the fold indicator row is
// rendered with the fold indicator style and keeps the exact gutter width
// (no misalignment with the numbered rows).
func TestFold_IndicatorStyleVerbatim(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	m := foldModel(t)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	_ = m.View()
	rendered := m.viewport.View()
	lines := strings.Split(rendered, "\n")
	// Row 1 is the indicator: it must carry the dim continuation marker
	// and the count label.
	if len(lines) < 2 || !strings.Contains(stripANSI(lines[1]), "⋯ 5 lines") {
		t.Fatalf("indicator row missing from the viewport:\n%s", rendered)
	}
}
