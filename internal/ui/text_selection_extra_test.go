package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/PierpaoloPernici/lazycaddy/internal/selection"
)

// Tests for the branch coverage of internal/ui/text_selection.go: geometry
// defaults and clamps, pane fallbacks, stale-selection clearing, mouse
// drag/release guards and the overlay/color helpers.

// TestPaneGeometry_ZeroSizeDefaults verifies the 80x24 fallback used when
// the model was never resized.
func TestPaneGeometry_ZeroSizeDefaults(t *testing.T) {
	m := &Model{}
	for name, geo := range map[string]textPaneGeometry{
		"source": m.sourcePaneGeometry(),
		"log":    m.logPaneGeometry(),
		"diff":   m.diffPaneGeometry(),
	} {
		if geo.width < 1 || geo.height < 1 {
			t.Errorf("%s geometry with zero size = %+v, want sane defaults", name, geo)
		}
	}
}

// TestPaneGeometry_TinyClamps verifies the 1-cell minimums for absurdly
// small windows.
func TestPaneGeometry_TinyClamps(t *testing.T) {
	m := &Model{width: 2, height: 1}
	for name, geo := range map[string]textPaneGeometry{
		"source": m.sourcePaneGeometry(),
		"log":    m.logPaneGeometry(),
		"diff":   m.diffPaneGeometry(),
	} {
		if geo.width < 1 || geo.height < 1 {
			t.Errorf("%s geometry at 2x1 = %+v, want at least 1x1", name, geo)
		}
	}
}

// TestPaneOriginRow_ErrorLine verifies the optional load-error line shifts
// the pane origin down by one.
func TestPaneOriginRow_ErrorLine(t *testing.T) {
	m := &Model{}
	if got := m.paneOriginRow(); got != 1 {
		t.Errorf("paneOriginRow without error = %d, want 1", got)
	}
	m = &Model{err: errors.New("boom")}
	if got := m.paneOriginRow(); got != 2 {
		t.Errorf("paneOriginRow with error = %d, want 2", got)
	}
}

// TestGeometryFor_UnknownPane verifies the unknown-pane case is absent.
func TestGeometryFor_UnknownPane(t *testing.T) {
	m := &Model{}
	if _, ok := m.geometryFor(textPaneNone); ok {
		t.Error("geometryFor(textPaneNone) must be absent")
	}
}

// TestTextPaneFor_UnknownPane verifies the empty-Pane fallback.
func TestTextPaneFor_UnknownPane(t *testing.T) {
	m := &Model{}
	if p := m.textPaneFor(textPaneNone); p.Source != nil {
		t.Error("unknown pane must produce an empty Pane")
	}
}

// TestSourceTextPane_FallbackToSelection verifies the source pane falls
// back to the selected document when the rendered document is not set.
func TestSourceTextPane_FallbackToSelection(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.sourceDoc = nil
	p := m.sourceTextPane()
	if len(p.Lines) == 0 || p.Lines[0] != "example.test {" {
		t.Errorf("source pane fallback lines = %v, want the selected document", p.Lines)
	}
}

// TestLogTextPane_TinyWidth verifies the 1-cell minimum line width and a
// 2-cell gutter even for an absurdly small window.
func TestLogTextPane_TinyWidth(t *testing.T) {
	m := &Model{width: 2, height: 5}
	p := m.logTextPane()
	if p.GutterWidth != 2 || len(p.Lines) != 1 {
		t.Errorf("tiny log pane = %+v, want a 2-cell gutter and the placeholder line", p)
	}
}

// TestDiffTextPane_NoChanges verifies the empty-state placeholder line and
// the tiny-width minimum.
func TestDiffTextPane_NoChanges(t *testing.T) {
	m := &Model{width: 80, height: 24}
	p := m.diffTextPane()
	if len(p.Lines) != 1 || !strings.Contains(p.Lines[0], "no changes") {
		t.Errorf("diff pane with no changes = %q, want the placeholder", p.Lines)
	}
	small := &Model{width: 1, height: 1}
	if q := small.diffTextPane(); q.ContentWidth < 1 {
		t.Errorf("tiny diff pane = %+v, want >=1 content cell", q)
	}
}

// TestActiveTextSelection_StalePaneCleared verifies a selection behind a
// modal or another view is dropped, never copied against the wrong pane.
func TestActiveTextSelection_StalePaneCleared(t *testing.T) {
	m := matcherModel(t, "example.test {\n}\n")
	m = resize(m, 80, 24)
	geo := m.sourcePaneGeometry()
	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	if m.textSel.pane != textPaneSource {
		t.Fatal("source selection not anchored")
	}
	// The source selection is stale once a modal covers the main panes.
	m.showLogs = true
	if _, ok := m.activeTextSelection(); ok {
		t.Error("selection behind the log view must be stale")
	}
	if m.textSel.pane != textPaneNone {
		t.Error("stale selection must be cleared")
	}
	// A log selection is stale when the log view closes or its detail
	// modal opens.
	m.textSel.pane = textPaneLogs
	m.textSel.state.MoveTo(selection.Position{})
	m.showLogs = false
	if _, ok := m.activeTextSelection(); ok {
		t.Error("log selection with the log view closed must be stale")
	}
	m.showLogs = true
	m.logDetailOpen = true
	m.textSel.pane = textPaneLogs
	m.textSel.state.MoveTo(selection.Position{})
	if _, ok := m.activeTextSelection(); ok {
		t.Error("log selection behind the detail modal must be stale")
	}
	// A diff selection is stale when the diff modal closes.
	m.textSel.pane = textPaneDiff
	m.textSel.state.MoveTo(selection.Position{})
	m.showDiff = false
	if _, ok := m.activeTextSelection(); ok {
		t.Error("diff selection with the diff closed must be stale")
	}
	if m.textSel.pane != textPaneNone {
		t.Error("stale diff selection must be cleared")
	}
}

// TestTextSelectionBytes_Unmappable verifies a range that does not map to
// the pane's buffer yields nil.
func TestTextSelectionBytes_Unmappable(t *testing.T) {
	m := matcherModel(t, "example.test {\n}\n")
	r := selection.Range{
		Start: selection.Position{Line: 999, Offset: 0},
		End:   selection.Position{Line: 999, Offset: 1},
	}
	if got := m.textSelectionBytes(textPaneSource, r); got != nil {
		t.Errorf("out-of-range bytes = %q, want nil", got)
	}
}

// TestSelectionSpans_NoRange verifies the no-range case is absent even
// when the pane matches.
func TestSelectionSpans_NoRange(t *testing.T) {
	m := matcherModel(t, "example.test {\n}\n")
	m.textSel.pane = textPaneSource
	if _, ok := m.selectionSpans(textPaneSource); ok {
		t.Error("selectionSpans without an anchored selection must be absent")
	}
}

// TestPanePositionAt_RejectsOutside verifies the un-clamped rejection of
// points outside the content rectangle.
func TestPanePositionAt_RejectsOutside(t *testing.T) {
	m := matcherModel(t, "example.test {\n}\n")
	m = resize(m, 80, 24)
	geo := m.sourcePaneGeometry()
	if _, ok := m.panePositionAt(textPaneSource, geo, -5, geo.y, false); ok {
		t.Error("point left of the pane must be rejected without clamping")
	}
	if _, ok := m.panePositionAt(textPaneSource, geo, geo.x+geo.width, geo.y+geo.height, false); ok {
		t.Error("point past the pane corner must be rejected without clamping")
	}
}

// TestPanePositionAt_ClampsAbove verifies the clamped py<0 pull-back for
// drag points above the content rectangle.
func TestPanePositionAt_ClampsAbove(t *testing.T) {
	m := matcherModel(t, "example.test {\n}\n")
	m = resize(m, 80, 24)
	geo := m.sourcePaneGeometry()
	pos, ok := m.panePositionAt(textPaneSource, geo, geo.x, geo.y-5, true)
	if !ok || pos.Line != 0 {
		t.Errorf("clamped point above the pane = %+v ok=%v, want line 0", pos, ok)
	}
}

// TestMouseDragRelease_NoSelection verifies drag and release are inert
// without an active selection.
func TestMouseDragRelease_NoSelection(t *testing.T) {
	m := matcherModel(t, "example.test {\n}\n")
	m.textSel.pane = textPaneNone
	m.mouseDrag(10, 10)
	m.mouseRelease(10, 10)
	if m.textSel.pane != textPaneNone {
		t.Error("drag/release without a selection must stay inert")
	}
}

// TestShiftTextCursor_Clamps verifies the keyboard selection clamps: up at
// the top, right past the line end, and down past the last line extends to
// its end.
func TestShiftTextCursor_Clamps(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m = resize(m, 80, 24)
	m.ensureTextCursor(textPaneSource)
	// shift+up at the top clamps to line 0.
	m.shiftTextCursor(0, -1)
	if cur := m.textSel.state.Cursor; cur.Line != 0 {
		t.Errorf("up at top = line %d, want 0", cur.Line)
	}
	// shift+left before the start clamps to offset 0.
	m.shiftTextCursor(-99, 0)
	if cur := m.textSel.state.Cursor; cur.Offset != 0 {
		t.Errorf("left past start = offset %d, want 0", cur.Offset)
	}
	// shift+right past the end clamps to the line length.
	m.shiftTextCursor(99, 0)
	cur := m.textSel.state.Cursor
	if cur.Offset != len("example.test {") {
		t.Errorf("right past end = offset %d, want %d", cur.Offset, len("example.test {"))
	}
	// shift+down past the last line extends to the end of the last line.
	lines := m.sourceTextPane().Lines
	m.shiftTextCursor(0, 99)
	cur = m.textSel.state.Cursor
	last := len(lines) - 1
	if cur.Line != last || cur.Offset != len(lines[last]) {
		t.Errorf("down past end = line %d offset %d, want %d/%d", cur.Line, cur.Offset, last, len(lines[last]))
	}
}

// TestRevealTextCursor_Scrolls verifies the owning viewport scrolls to
// keep the cursor line visible, and the unknown-pane case is a no-op.
func TestRevealTextCursor_Scrolls(t *testing.T) {
	src := strings.Builder{}
	for i := 0; i < 40; i++ {
		src.WriteString("example.test {\n\trespond ok\n}\n")
	}
	m := matcherModel(t, src.String())
	m = resize(m, 80, 24)
	m.ensureTextCursor(textPaneSource)
	// Jump far below the viewport: scrolls down.
	m.revealTextCursor(textPaneSource, 100)
	if m.viewport.YOffset == 0 {
		t.Error("reveal below the viewport must scroll down")
	}
	// Jump above the viewport: scrolls up.
	m.viewport.SetYOffset(50)
	m.revealTextCursor(textPaneSource, 2)
	if m.viewport.YOffset > 2 {
		t.Errorf("reveal above the viewport must scroll up, yOffset = %d", m.viewport.YOffset)
	}
	// Unknown pane: no-op.
	m.revealTextCursor(textPaneNone, 5)
}

// TestShiftSelectionDelta verifies the shift+arrow delta mapping.
func TestShiftSelectionDelta(t *testing.T) {
	tests := []struct {
		key     tea.KeyType
		dx, dy  int
		shifted bool
	}{
		{tea.KeyShiftUp, 0, -1, true},
		{tea.KeyShiftDown, 0, 1, true},
		{tea.KeyShiftLeft, -1, 0, true},
		{tea.KeyShiftRight, 1, 0, true},
		{tea.KeyUp, 0, 0, false},
	}
	for _, tt := range tests {
		dx, dy, ok := shiftSelectionDelta(tea.KeyMsg{Type: tt.key})
		if ok != tt.shifted || dx != tt.dx || dy != tt.dy {
			t.Errorf("shiftSelectionDelta(%v) = (%d,%d,%v), want (%d,%d,%v)", tt.key, dx, dy, ok, tt.dx, tt.dy, tt.shifted)
		}
	}
}

// TestRenderSelectionOverlay_Guards verifies the empty/zero guards and the
// skipping of out-of-range spans.
func TestRenderSelectionOverlay_Guards(t *testing.T) {
	text := "hello"
	if got := renderSelectionOverlay(text, 10, 5, nil); got != text {
		t.Errorf("empty spans = %q, want text unchanged", got)
	}
	if got := renderSelectionOverlay(text, 0, 5, []selection.CellSpan{{Row: 0, ColStart: 0, ColEnd: 1}}); got != text {
		t.Errorf("zero width = %q, want text unchanged", got)
	}
	got := renderSelectionOverlay("ab", 2, 1, []selection.CellSpan{
		{Row: 5, ColStart: 0, ColEnd: 1}, // row outside
		{Row: 0, ColStart: 0, ColEnd: 9}, // col beyond the width
	})
	if stripANSI(got) != "ab" {
		t.Errorf("out-of-range spans must not change the text, got %q", stripANSI(got))
	}
}

// TestSelectionHighlightColor_NoColorFallback verifies the dark variant is
// used when the terminal carries no color profile.
func TestSelectionHighlightColor_NoColorFallback(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	defer lipgloss.SetColorProfile(termenv.ANSI256)
	if got := selectionHighlightColor(); got != selectionBackgroundDarkColor {
		t.Errorf("no-color profile should use the dark variant, got %v", got)
	}
}

// TestSelectionHighlightColor_AnsiProfile verifies the primary path: under
// an ANSI profile the probe carries a real background color.
func TestSelectionHighlightColor_AnsiProfile(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	if got := selectionHighlightColor(); got == nil {
		t.Error("ANSI profile should resolve a real background color")
	}
}

// TestLastSelectableLine covers the three branches of the folded-pane
// last-selectable-line lookup: an empty unfolded pane, a folded pane with
// a visible tail, and a folded pane whose rows are all indicators.
func TestLastSelectableLine(t *testing.T) {
	m := &Model{}
	// An empty pane has no selectable line.
	if got := m.lastSelectableLine(&selection.Pane{}); got != -1 {
		t.Errorf("lastSelectableLine(empty) = %d, want -1", got)
	}
	// A folded pane resolves the last visible source line: rows
	// [0, -1, 1] over three lines make line 1 the tail.
	p := &selection.Pane{Lines: []string{"a", "b", "c"}, Offsets: []int{0, 2, 4}, RowLines: []int{0, -1, 1}}
	if got := m.lastSelectableLine(p); got != 1 {
		t.Errorf("lastSelectableLine(folded) = %d, want 1", got)
	}
	// A pane whose rows are all indicators reports no selectable line.
	all := &selection.Pane{Lines: []string{"a"}, Offsets: []int{0}, RowLines: []int{-1}}
	if got := m.lastSelectableLine(all); got != -1 {
		t.Errorf("lastSelectableLine(all-indicator) = %d, want -1", got)
	}
}

// TestFoldRowAtPanePoint_OutOfBounds verifies the fold indicator lookup
// rejects points outside the pane rectangle.
func TestFoldRowAtPanePoint_OutOfBounds(t *testing.T) {
	m := foldModel(t)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // site folded
	_ = m.View()
	geo := m.sourcePaneGeometry()
	if got := m.foldRowAtPanePoint(geo, geo.x, geo.y-1); got != -1 {
		t.Errorf("foldRowAtPanePoint above the pane = %d, want -1", got)
	}
	if got := m.foldRowAtPanePoint(geo, geo.x, geo.y+geo.height); got != -1 {
		t.Errorf("foldRowAtPanePoint below the pane = %d, want -1", got)
	}
}

// TestFold_ShiftDownPastLastVisibleLine verifies shift+down past the last
// visible source line of a folded pane extends the selection to that
// line's end instead of jumping into hidden content.
func TestFold_ShiftDownPastLastVisibleLine(t *testing.T) {
	m := foldModel(t)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // site folded
	_ = m.View()
	m.ensureTextCursor(textPaneSource)
	// From the header, shift+down lands on the closing brace (line 6).
	m.shiftTextCursor(0, 1)
	if cur := m.textSel.state.Cursor; cur.Line != 6 {
		t.Fatalf("shift+down across the fold = line %d, want 6", cur.Line)
	}
	// One more step reaches the trailing empty line, the last visible row.
	m.shiftTextCursor(0, 1)
	if cur := m.textSel.state.Cursor; cur.Line != 7 {
		t.Fatalf("shift+down to the trailing line = line %d, want 7", cur.Line)
	}
	// A step past it clamps to the end of that line.
	lines := m.sourceTextPane().Lines
	m.shiftTextCursor(0, 1)
	cur := m.textSel.state.Cursor
	if cur.Line != 7 || cur.Offset != len(lines[7]) {
		t.Errorf("shift+down past the last visible line = line %d offset %d, want 7/%d",
			cur.Line, cur.Offset, len(lines[7]))
	}
}
