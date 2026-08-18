package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"

	"github.com/PierpaoloPernici/lazycaddy/internal/selection"
)

// textSelectionPane identifies the pane that owns an active text
// selection. Only the source pane, the main log pane and the diff modal
// body are text panes; the tree, header, footer and every other modal are
// deliberately not selectable.
type textSelectionPane int

const (
	// textPaneNone means no text pane owns a selection.
	textPaneNone textSelectionPane = iota
	// textPaneSource is the raw source pane of the main view.
	textPaneSource
	// textPaneLogs is the full-screen main log pane.
	textPaneLogs
	// textPaneDiff is the diff modal body.
	textPaneDiff
)

// textSelect is the model-side pane-aware text selection: the owning pane
// plus the UI-independent Selectable anchor/cursor. Only one selection is
// active at a time; switching panes re-anchors it.
type textSelect struct {
	pane  textSelectionPane
	state selection.Selectable
}

// textPaneGeometry describes where a text pane's viewport content sits on
// screen and how big it is. x/y are the screen cell of the first content
// row (inside border and padding, gutter included); width/height are the
// viewport's content dimensions in cells.
type textPaneGeometry struct {
	x, y, width, height int
}

// paneOriginRow is the screen row where the pane/modal area begins: the
// header plus the optional load-error line.
func (m *Model) paneOriginRow() int {
	row := 1 // header
	if m.err != nil {
		row++
	}
	return row
}

// paneCellWidth mirrors the cell accounting used by the renderers
// (renderStyledLine, wrapBytes and the viewport truncation): lipgloss
// width with a minimum of one cell per rune. Injecting it into
// selection.Pane keeps coordinate mapping consistent with what is drawn,
// including wide (CJK) characters.
func paneCellWidth(r rune) int {
	w := lipgloss.Width(string(r))
	if w < 1 {
		return 1
	}
	return w
}

// lineOffsets returns the byte offset of each line in src when the lines
// are obtained by splitting src on "\n" (strings.Split semantics,
// including the trailing empty line for a source that ends with a
// newline).
func lineOffsets(src []byte) []int {
	offsets := make([]int, 0, 8)
	off := 0
	for i, b := range src {
		if b == '\n' {
			offsets = append(offsets, off)
			off = i + 1
		}
	}
	offsets = append(offsets, off)
	return offsets
}

// sourcePaneGeometry returns the screen rectangle of the source
// viewport's content area in the main tree/source layout. The formulas
// mirror syncSource: the content is inside the pane border and padding,
// below the pane title.
func (m *Model) sourcePaneGeometry() textPaneGeometry {
	width, height := m.width, m.height
	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}
	paneH := m.paneContentH(height)
	treeW := width * 2 / 5
	srcW := width - treeW - 2*paneStyle.GetHorizontalBorderSize()
	if srcW < 1 {
		srcW = 1
	}
	contentW := srcW - 4 // border (2) + horizontal padding (2)
	if contentW < 1 {
		contentW = 1
	}
	contentH := paneH - 4 // border (2) + title (1) + separator (1)
	if contentH < 1 {
		contentH = 1
	}
	return textPaneGeometry{
		x:      treeW + 4,             // tree border (1) + source border (1) + padding (1)
		y:      m.paneOriginRow() + 2, // top border (1) + title (1)
		width:  contentW,
		height: contentH,
	}
}

// logPaneGeometry returns the screen rectangle of the log viewport's
// content area. The formulas mirror logView/syncLogViewport, including
// the blank separator row between the pane title and the viewport.
func (m *Model) logPaneGeometry() textPaneGeometry {
	width, height := m.width, m.height
	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}
	paneH := m.paneContentH(height)
	contentW := width - 6 // (width-2) pane, minus border (2) and padding (2)
	if contentW < 1 {
		contentW = 1
	}
	contentH := paneH - 4 // border (2) + title (1) + separator (1)
	if contentH < 1 {
		contentH = 1
	}
	return textPaneGeometry{
		x:      2,                     // left border (1) + left padding (1)
		y:      m.paneOriginRow() + 3, // top border (1) + title (1) + separator (1)
		width:  contentW,
		height: contentH,
	}
}

// diffPaneGeometry returns the screen rectangle of the diff viewport's
// content area. The formulas mirror diffView/syncDiffContent.
func (m *Model) diffPaneGeometry() textPaneGeometry {
	width, height := m.width, m.height
	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}
	paneH := m.paneContentH(height)
	contentW := width - 4 // (width-2) pane, minus border (2) and padding (2)
	if contentW < 1 {
		contentW = 1
	}
	contentH := paneH - 3 // border (2) + title (1)
	if contentH < 1 {
		contentH = 1
	}
	return textPaneGeometry{
		x:      2,                     // left border (1) + left padding (1)
		y:      m.paneOriginRow() + 2, // top border (1) + title (1)
		width:  contentW,
		height: contentH,
	}
}

// geometryFor returns the screen rectangle of a text pane.
func (m *Model) geometryFor(pane textSelectionPane) (textPaneGeometry, bool) {
	switch pane {
	case textPaneSource:
		return m.sourcePaneGeometry(), true
	case textPaneLogs:
		return m.logPaneGeometry(), true
	case textPaneDiff:
		return m.diffPaneGeometry(), true
	}
	return textPaneGeometry{}, false
}

// sourceTextPane builds the selection.Pane for the source viewport: the
// document lines with a line-number gutter, the viewport's scroll and
// height, and no wrapping (the source viewport truncates, it never
// wraps). Source is the exact document bytes, so RangeBytes returns exact
// source bytes.
func (m *Model) sourceTextPane() *selection.Pane {
	var src []byte
	if m.sourceDoc != nil {
		src = m.sourceDoc.Source
	} else if sel := m.selectedItem(); sel != nil && sel.doc != nil {
		src = sel.doc.Source
	}
	lines := strings.Split(string(src), "\n")
	geo := m.sourcePaneGeometry()
	return &selection.Pane{
		Source:       src,
		Lines:        lines,
		Offsets:      lineOffsets(src),
		GutterWidth:  sourceGutterWidth(len(lines)),
		Height:       geo.height,
		ContentWidth: max(0, geo.width-sourceGutterWidth(len(lines))),
		Scroll:       m.viewport.YOffset,
		CellWidth:    paneCellWidth,
	}
}

// logTextPane builds the selection.Pane for the log viewport: one plain
// (unstyled) compact line per entry, truncated to the rendered width, with
// a 2-cell cursor gutter and the viewport's scroll and height. Source is
// the joined plain lines, so RangeBytes returns exactly the visible log
// text without ANSI sequences or UI decorations.
func (m *Model) logTextPane() *selection.Pane {
	geo := m.logPaneGeometry()
	lineW := geo.width - 2 // 2-cell cursor gutter
	if lineW < 1 {
		lineW = 1
	}
	var lines []string
	if len(m.logLines) == 0 {
		lines = []string{"no log entries yet — waiting for the first poll"}
	} else {
		lines = make([]string, len(m.logLines))
		for i, entry := range m.logLines {
			lines[i] = compactLogPlainLine(entry, lineW)
		}
	}
	src := []byte(strings.Join(lines, "\n"))
	return &selection.Pane{
		Source:       src,
		Lines:        lines,
		Offsets:      lineOffsets(src),
		GutterWidth:  2,
		Height:       geo.height,
		ContentWidth: max(0, geo.width-2),
		Scroll:       m.logViewport.YOffset,
		CellWidth:    paneCellWidth,
	}
}

// diffTextPane builds the selection.Pane for the diff modal body: one
// plain line per diff line, truncated to the rendered width (the "…"
// decorations are part of the visible text and therefore part of a copied
// selection; the "> " current-hunk marker is deliberately excluded so a
// copied diff does not depend on the hunk cursor). There is no gutter.
func (m *Model) diffTextPane() *selection.Pane {
	geo := m.diffPaneGeometry()
	bodyW := geo.width // the geometry clamps to at least 1 cell
	var lines []string
	if !m.diffHasChanges() {
		lines = []string{"no changes — the working copy matches the source"}
	} else {
		lines = make([]string, len(m.diffLines))
		for i, l := range m.diffLines {
			lines[i] = m.renderDiffLine(l.Text, bodyW)
		}
	}
	src := []byte(strings.Join(lines, "\n"))
	return &selection.Pane{
		Source:       src,
		Lines:        lines,
		Offsets:      lineOffsets(src),
		GutterWidth:  0,
		Height:       geo.height,
		ContentWidth: geo.width,
		Scroll:       m.diffViewport.YOffset,
		CellWidth:    paneCellWidth,
	}
}

// textPaneFor builds the selection.Pane for a text pane on demand. The
// pane is derived from the current model state (document, log lines, diff
// lines and viewport scroll), so both mouse mapping and overlay rendering
// always see the same geometry as the renderer.
func (m *Model) textPaneFor(pane textSelectionPane) *selection.Pane {
	switch pane {
	case textPaneSource:
		return m.sourceTextPane()
	case textPaneLogs:
		return m.logTextPane()
	case textPaneDiff:
		return m.diffTextPane()
	}
	return &selection.Pane{}
}

// clearTextSelection drops any active text selection and its pane.
func (m *Model) clearTextSelection() {
	m.textSel.pane = textPaneNone
	m.textSel.state.Clear()
}

// activeTextSelection returns the selection when its pane is the one
// currently rendered. A selection whose pane was hidden (a modal opened,
// the view switched, the diff closed) is stale: it is dropped and
// reported absent so it can never be copied against the wrong content.
func (m *Model) activeTextSelection() (selection.Range, bool) {
	switch m.textSel.pane {
	case textPaneSource:
		if !m.showingMainPanes() {
			m.clearTextSelection()
			return selection.Range{}, false
		}
	case textPaneLogs:
		if !m.showLogs || m.logDetailOpen {
			m.clearTextSelection()
			return selection.Range{}, false
		}
	case textPaneDiff:
		if !m.showDiff {
			m.clearTextSelection()
			return selection.Range{}, false
		}
	case textPaneNone:
		return selection.Range{}, false
	}
	return m.textSel.state.Range()
}

// textSelectionBytes returns the exact bytes covered by r in the given
// pane's backing buffer, or nil when the range cannot be mapped (the pane
// changed since the selection was made). The bytes are a snapshot: the
// caller must copy them before handing them to an asynchronous adapter.
func (m *Model) textSelectionBytes(pane textSelectionPane, r selection.Range) []byte {
	bytes, ok := m.textPaneFor(pane).RangeBytes(r)
	if !ok {
		return nil
	}
	return bytes
}

// selectionSpans returns the viewport cells the active selection of pane
// covers, ready for the overlay renderer.
func (m *Model) selectionSpans(pane textSelectionPane) ([]selection.CellSpan, bool) {
	if m.textSel.pane != pane {
		return nil, false
	}
	r, ok := m.textSel.state.Range()
	if !ok {
		return nil, false
	}
	return m.textPaneFor(pane).CellsInRange(r), true
}

// showingMainPanes reports whether the main tree/source layout is the
// rendered context, with no modal, overlay or other screen on top of it.
// Only then can the source pane own a text selection.
func (m *Model) showingMainPanes() bool {
	return !(m.showUnsavedConfirm || m.showChangeConflict || m.showDiff ||
		m.showSaveConfirm || m.showReloadConfirm || m.showRollbackConfirm ||
		m.showBackups || m.showDiagnostics || m.showLogs || m.searchActive ||
		m.showErrorHistory || m.showStructuredAdd || m.showCommandPalette ||
		m.logDetailOpen)
}

// updateMouse handles mouse activity. Left-button presses and drags inside a
// text pane create or extend a selection. A right-button press copies an
// existing selection owned by that pane; without an active selection it is
// ignored. Wheel events and every non-text region remain inert so tree
// navigation, modal behavior and keyboard navigation stay unchanged.
func (m *Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonRight:
		pane, _, ok := m.textPaneAt(msg.X, msg.Y)
		if ok && pane == m.textSel.pane {
			if _, selected := m.activeTextSelection(); selected {
				return m.startCopy()
			}
		}
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		m.mousePress(msg.X, msg.Y)
	case msg.Action == tea.MouseActionMotion && msg.Button == tea.MouseButtonLeft:
		m.mouseDrag(msg.X, msg.Y)
	case msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonNone:
		m.mouseRelease(msg.X, msg.Y)
	}
	return m, nil
}

// textPaneAt reports which text pane, if any, owns the screen point, and
// that pane's content rectangle. The tree pane, header, footer, pane
// borders and every modal are not text panes.
func (m *Model) textPaneAt(x, y int) (textSelectionPane, textPaneGeometry, bool) {
	if m.showDiff {
		geo := m.diffPaneGeometry()
		return textPaneDiff, geo, m.pointInPane(x, y, geo)
	}
	if m.showLogs && !m.logDetailOpen {
		geo := m.logPaneGeometry()
		return textPaneLogs, geo, m.pointInPane(x, y, geo)
	}
	if m.showingMainPanes() {
		geo := m.sourcePaneGeometry()
		return textPaneSource, geo, m.pointInPane(x, y, geo)
	}
	return textPaneNone, textPaneGeometry{}, false
}

// pointInPane reports whether a screen point falls inside a content
// rectangle.
func (m *Model) pointInPane(x, y int, geo textPaneGeometry) bool {
	return x >= geo.x && x < geo.x+geo.width && y >= geo.y && y < geo.y+geo.height
}

// panePositionAt maps a screen point to a source position through the
// pane's own geometry. Without clamping, points outside the content
// rectangle (and gutter cells) are rejected; with clamping, out-of-rect
// points are pulled back to the pane edge, gutter cells snap to the line
// start, and padding rows below the content extend to the end of the last
// line. This keeps a drag from jumping to a neighboring pane while still
// selecting to the end of the content.
func (m *Model) panePositionAt(pane textSelectionPane, geo textPaneGeometry, x, y int, clamp bool) (selection.Position, bool) {
	p := m.textPaneFor(pane)
	px, py := x-geo.x, y-geo.y
	if clamp {
		if px < 0 {
			px = 0
		}
		if px >= geo.width {
			px = geo.width - 1
		}
		if px < p.GutterWidth {
			px = p.GutterWidth
		}
		if py < 0 {
			py = 0
		}
		if py >= geo.height {
			py = geo.height - 1
		}
		pos, ok := p.Position(py, px)
		if !ok && len(p.Lines) > 0 {
			// Padding row below the content: extend to the end of the
			// last line so a drag past the text still selects it.
			last := len(p.Lines) - 1
			return selection.Position{Line: last, Offset: len(p.Lines[last])}, true
		}
		return pos, ok
	} else if px < 0 || px >= geo.width || py < 0 || py >= geo.height {
		return selection.Position{}, false
	}
	return p.Position(py, px)
}

// mousePress starts a fresh selection at the press point, or clears any
// selection when the press falls outside every text pane.
func (m *Model) mousePress(x, y int) {
	pane, geo, ok := m.textPaneAt(x, y)
	if !ok {
		m.clearTextSelection()
		return
	}
	pos, ok := m.panePositionAt(pane, geo, x, y, false)
	if !ok {
		m.clearTextSelection()
		return
	}
	m.textSel.pane = pane
	m.textSel.state.MoveTo(pos)
	m.textSel.state.SelectTo(pos)
}

// mouseDrag extends the active selection toward the drag point, clamped to
// the owning pane so the selection can never cross into the tree or
// another view. The pane is never textPaneNone here and the clamped
// position always resolves for the pane's non-empty lines, so both lookups
// are safe by construction.
func (m *Model) mouseDrag(x, y int) {
	if m.textSel.pane == textPaneNone {
		return
	}
	geo, _ := m.geometryFor(m.textSel.pane)
	pos, _ := m.panePositionAt(m.textSel.pane, geo, x, y, true)
	m.textSel.state.SelectTo(pos)
}

// mouseRelease finalizes the selection. Some terminals omit the last drag
// motion event, so the release point is applied when it still falls inside
// the pane (the clamped position resolves the same way as a drag).
func (m *Model) mouseRelease(x, y int) {
	if m.textSel.pane == textPaneNone {
		return
	}
	geo, _ := m.geometryFor(m.textSel.pane)
	pos, _ := m.panePositionAt(m.textSel.pane, geo, x, y, true)
	m.textSel.state.SelectTo(pos)
}

// currentTextPane reports the text pane that owns keyboard selection in
// the current context: the diff modal body while a diff is open, the log
// pane while the log view is open, and the source pane otherwise.
func (m *Model) currentTextPane() textSelectionPane {
	switch {
	case m.showDiff:
		return textPaneDiff
	case m.showLogs && !m.logDetailOpen:
		return textPaneLogs
	default:
		return textPaneSource
	}
}

// ensureTextCursor seeds the keyboard cursor at the top-left visible cell
// of pane when the active selection moves to a new pane.
func (m *Model) ensureTextCursor(pane textSelectionPane) {
	if m.textSel.pane == pane {
		return
	}
	p := m.textPaneFor(pane)
	// Position at the first content cell is always resolvable: every text
	// pane has at least one line and a clamped gutter.
	pos, _ := p.Position(0, p.GutterWidth)
	m.textSel.pane = pane
	m.textSel.state.MoveTo(pos)
}

// shiftTextCursor extends (or starts) the keyboard selection by the given
// line/byte delta, mirroring shift+arrow semantics: shift+up/down move
// one logical line, shift+left/right one byte, both clamped to the pane's
// content. Moving below the last line extends the selection to the end of
// the last line, so the final line can always be selected. The owning
// viewport is scrolled to keep the cursor visible.
func (m *Model) shiftTextCursor(dx, dy int) {
	pane := m.currentTextPane()
	m.ensureTextCursor(pane)
	p := m.textPaneFor(pane)
	cur := m.textSel.state.Cursor
	line := cur.Line + dy
	if line < 0 {
		line = 0
	}
	if line >= len(p.Lines) {
		// Below the last line: extend the selection to its end.
		line = len(p.Lines) - 1
		m.textSel.state.SelectTo(selection.Position{Line: line, Offset: len(p.Lines[line])})
		m.revealTextCursor(pane, line)
		return
	}
	off := cur.Offset + dx
	if off < 0 {
		off = 0
	}
	if l := len(p.Lines[line]); off > l {
		off = l
	}
	m.textSel.state.SelectTo(selection.Position{Line: line, Offset: off})
	m.revealTextCursor(pane, line)
}

// revealTextCursor scrolls the owning viewport so the selection cursor
// line stays visible, mirroring revealRange for the source pane.
func (m *Model) revealTextCursor(pane textSelectionPane, line int) {
	var vp *viewport.Model
	switch pane {
	case textPaneSource:
		vp = &m.viewport
	case textPaneLogs:
		vp = &m.logViewport
	case textPaneDiff:
		vp = &m.diffViewport
	default:
		return
	}
	if line < vp.YOffset {
		vp.SetYOffset(line)
	} else if line >= vp.YOffset+vp.Height {
		vp.SetYOffset(line - vp.Height + 1)
	}
}

// shiftSelectionDelta maps a shift+arrow key to its (line, byte) cursor
// delta.
func shiftSelectionDelta(msg tea.KeyMsg) (dx, dy int, ok bool) {
	switch msg.String() {
	case "shift+up":
		return 0, -1, true
	case "shift+down":
		return 0, 1, true
	case "shift+left":
		return -1, 0, true
	case "shift+right":
		return 1, 0, true
	}
	return 0, 0, false
}

// renderSelectionOverlay paints the selection background over the covered
// cells of an already-styled viewport frame. It parses the styled text
// into a cell buffer, tints the covered cells and re-renders, so existing
// syntax, log and diff colors are preserved and no ANSI sequence is split
// or dropped. The result is byte-equivalent to text except for the added
// background sequences. Spans are expected to come from the matching
// selection.Pane in content coordinates (gutter included).
func renderSelectionOverlay(text string, width, height int, spans []selection.CellSpan) string {
	if len(spans) == 0 || width < 1 || height < 1 {
		return text
	}
	buf := cellbuf.NewBuffer(width, height)
	cellbuf.SetContent(buf, text)
	bg := selectionHighlightColor()
	for _, sp := range spans {
		if sp.Row < 0 || sp.Row >= height {
			continue
		}
		for c := sp.ColStart; c < sp.ColEnd; c++ {
			if c < 0 || c >= width {
				continue
			}
			if cell := buf.Cell(c, sp.Row); cell != nil {
				cell.Style.Bg = bg
			}
		}
	}
	return strings.ReplaceAll(cellbuf.Render(buf), "\r\n", "\n")
}

// selectionHighlightColor resolves the selection background to the cell
// color cellbuf expects. The primary path renders a probe cell through the
// selectionBackgroundStyle, so the highlight degrades with the terminal's
// color profile exactly like the rest of the app (mirroring the palette
// overlay technique in fillPaletteCells). Under a NoColor profile the
// probe carries no background, so the dark variant is used directly to
// keep the highlight deterministic (and testable) instead of silently
// disappearing.
func selectionHighlightColor() ansi.Color {
	probe := cellbuf.NewBuffer(1, 1)
	cellbuf.SetContent(probe, selectionBackgroundStyle.Render(" "))
	if cell := probe.Cell(0, 0); cell != nil && cell.Style.Bg != nil {
		return cell.Style.Bg
	}
	return selectionBackgroundDarkColor
}

// selectionBackgroundDarkColor is the dark variant of the selection
// background as a cellbuf color, used when no color profile is active.
var selectionBackgroundDarkColor = ansi.RGBColor{R: 0x1f, G: 0x3b, B: 0x5c}
