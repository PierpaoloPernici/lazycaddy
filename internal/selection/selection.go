// Package selection provides a UI-independent text selection model for
// pane-based views (source content, logs, diffs, diagnostics). It maps
// visible terminal cells to exact source positions through a pane's
// gutter, scrolling and wrapping geometry, and converts a selected visible
// range into exact source bytes. It does not depend on Bubble Tea or any
// terminal library, and it never renders anything.
package selection

import (
	"bytes"
	"strconv"
	"unicode/utf8"
)

// Position is a source position: a logical line index and a byte offset
// within that line's rendered content (line terminators excluded).
type Position struct {
	Line   int
	Offset int
}

// Pane is a scrollable, optionally wrapping text region with a fixed
// gutter and a byte-exact backing buffer. A pane renders one viewport;
// selections can never cross panes because every coordinate maps back into
// this pane's own lines and bytes.
//
// Views populate Lines and Offsets consistently: Offsets[i] is the byte
// offset of Lines[i] inside Source, and Lines[i] excludes its line
// terminator. For source views Source is the document bytes; for log,
// diff and diagnostic views the view provides the same contract over its
// own backing buffer.
type Pane struct {
	// Source is the full backing bytes the pane renders.
	Source []byte
	// Lines are the logical lines rendered by the pane, without line
	// terminators.
	Lines []string
	// Offsets[i] is the byte offset of Lines[i] within Source.
	Offsets []int
	// GutterWidth is the cell width reserved for line numbers. Column
	// coordinates count gutter cells, so the first content cell is
	// GutterWidth.
	GutterWidth int
	// WrapWidth is the cell width at which lines wrap. 0 disables
	// wrapping: each line occupies one row and Offset scrolls it
	// horizontally.
	WrapWidth int
	// Scroll is the first visible logical line.
	Scroll int
	// Offset is the horizontal scroll in cells, applied only when
	// wrapping is disabled.
	Offset int
	// Height is the number of visible rows (the content area; the gutter
	// is horizontal, not vertical).
	Height int
	// ContentWidth is the number of visible cells after the gutter. It is
	// used for rendering selections on empty logical lines, which have no
	// source rune from which to derive a span. A zero value preserves the
	// historical behavior for callers that do not provide viewport width.
	ContentWidth int
	// RowLines, when non-nil, maps each content row (0-based, relative to
	// Scroll) to the logical line it renders. Rows with no source position
	// (fold indicator rows) map to -1 and are never selectable. It lets a
	// pane render a folded source view — where display rows are no longer
	// line-index-sequential because hidden lines are replaced by indicator
	// rows — while Lines, Offsets and Source stay on the full backing
	// buffer, so RangeBytes always returns exact source bytes. Without it
	// the pane assumes one content row per logical line (the historical
	// contract).
	RowLines []int
	// CellWidth measures a rune in terminal cells. nil means one cell per
	// rune, which is the conservative default (tabs count as one
	// character); views with wide-character knowledge can inject a width
	// function.
	CellWidth func(r rune) int
}

func (p *Pane) cellWidth() func(r rune) int {
	if p.CellWidth != nil {
		return p.CellWidth
	}
	return func(r rune) int { return 1 }
}

// lineWidth returns the cell width of a logical line's content.
func (p *Pane) lineWidth(line int) int {
	if line < 0 || line >= len(p.Lines) {
		return 0
	}
	w := 0
	for _, r := range p.Lines[line] {
		w += p.cellWidth()(r)
	}
	return w
}

// Rows returns the number of visible rows a logical line occupies with the
// current wrap settings: one row per line when wrapping is disabled, or
// the wrapped segment count otherwise. An empty line still occupies one
// row.
func (p *Pane) Rows(line int) int {
	if p.WrapWidth > 0 {
		w := p.lineWidth(line)
		segments := w / p.WrapWidth
		if w%p.WrapWidth != 0 {
			segments++
		}
		if segments < 1 {
			segments = 1
		}
		return segments
	}
	return 1
}

// rowsBefore returns the total visible rows occupied by the lines in
// [Scroll, line).
func (p *Pane) rowsBefore(line int) int {
	if line < p.Scroll {
		return 0
	}
	n := 0
	for i := p.Scroll; i < line && i < len(p.Lines); i++ {
		n += p.Rows(i)
	}
	return n
}

// cellToOffset maps a cell column within a logical line to a byte offset.
// Cells inside a wide rune snap to the rune's start; cells beyond the line
// clamp to the line end.
func (p *Pane) cellToOffset(line, cell int) int {
	if line < 0 || line >= len(p.Lines) {
		return 0
	}
	text := p.Lines[line]
	if cell < 0 {
		cell = 0
	}
	acc := 0
	for i := 0; i < len(text); {
		if cell <= acc {
			return i
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		w := p.cellWidth()(r)
		if cell < acc+w {
			return i
		}
		acc += w
		i += size
	}
	return len(text)
}

// offsetToCell maps a byte offset within a logical line to a cell column,
// clamped to the line content.
func (p *Pane) offsetToCell(line, offset int) int {
	if line < 0 || line >= len(p.Lines) {
		return 0
	}
	text := p.Lines[line]
	if offset < 0 {
		offset = 0
	}
	if offset > len(text) {
		offset = len(text)
	}
	acc := 0
	for i := 0; i < offset; {
		r, size := utf8.DecodeRuneInString(text[i:])
		acc += p.cellWidth()(r)
		i += size
	}
	return acc
}

// Position maps a visible cell to a source position. Rows are content rows
// (0-based); columns count gutter cells, so the first content column is
// GutterWidth. ok is false for gutter cells, rows outside the viewport,
// padding rows below the last line, a position hidden by the horizontal
// scroll, or — when RowLines is set — a row that renders no source
// position (a fold indicator). Cells beyond the end of a line's segment
// clamp to that segment's end, so selecting to the end of a line is exact.
func (p *Pane) Position(row, col int) (Position, bool) {
	if row < 0 || row >= p.Height {
		return Position{}, false
	}
	if col < p.GutterWidth {
		return Position{}, false
	}
	contentCol := col - p.GutterWidth
	if p.RowLines != nil {
		abs := p.Scroll + row
		if abs < 0 || abs >= len(p.RowLines) {
			return Position{}, false
		}
		line := p.RowLines[abs]
		if line < 0 || line >= len(p.Lines) {
			// Fold indicator row (or a stale row after a content change):
			// no source position to select.
			return Position{}, false
		}
		// Folded source panes never wrap: each visible line occupies
		// exactly one row.
		cellInLine := p.Offset + contentCol
		if cellInLine < 0 {
			cellInLine = 0
		}
		return Position{Line: line, Offset: p.cellToOffset(line, cellInLine)}, true
	}
	line := p.Scroll
	remaining := row
	for line < len(p.Lines) {
		rows := p.Rows(line)
		if remaining < rows {
			break
		}
		remaining -= rows
		line++
	}
	if line >= len(p.Lines) {
		return Position{}, false
	}
	if p.WrapWidth > 0 {
		segStart := remaining * p.WrapWidth
		segCells := p.WrapWidth
		if w := p.lineWidth(line); segStart+segCells > w {
			segCells = w - segStart
		}
		if contentCol > segCells {
			contentCol = segCells
		}
		cellInLine := segStart + contentCol
		return Position{Line: line, Offset: p.cellToOffset(line, cellInLine)}, true
	}
	cellInLine := p.Offset + contentCol
	if cellInLine < 0 {
		cellInLine = 0
	}
	return Position{Line: line, Offset: p.cellToOffset(line, cellInLine)}, true
}

// PositionNearest maps a viewport cell to the nearest selectable source
// position when the exact row holds none: it scans outward from the row
// for the closest visible line (skipping fold indicator rows) and maps the
// column onto it. It is used to clamp mouse drags in folded panes so a
// drag crossing an indicator row never jumps the cursor to a foreign
// position. ok is false when the pane has no visible line at all.
func (p *Pane) PositionNearest(row, col int) (Position, bool) {
	if p.RowLines == nil {
		return p.Position(row, col)
	}
	if col < p.GutterWidth {
		col = p.GutterWidth
	}
	contentCol := col - p.GutterWidth
	if row < 0 {
		row = 0
	}
	if row >= len(p.RowLines) {
		row = len(p.RowLines) - 1
	}
	for d := 0; d < len(p.RowLines); d++ {
		for _, abs := range [2]int{row - d, row + d} {
			if abs < 0 || abs >= len(p.RowLines) {
				continue
			}
			line := p.RowLines[abs]
			if line < 0 || line >= len(p.Lines) {
				continue
			}
			cellInLine := p.Offset + contentCol
			if cellInLine < 0 {
				cellInLine = 0
			}
			return Position{Line: line, Offset: p.cellToOffset(line, cellInLine)}, true
		}
	}
	return Position{}, false
}

// rowOfLine returns the absolute content row of a logical line in a
// folded pane, or -1 when the line is hidden (or the pane has no row
// mapping). RowLines preserves source order, so a linear scan is exact and
// stays within the same complexity as the sequential rowsBefore walk it
// replaces.
func (p *Pane) rowOfLine(line int) int {
	if p.RowLines == nil || line < 0 {
		return -1
	}
	for abs, ln := range p.RowLines {
		if ln == line {
			return abs
		}
	}
	return -1
}

// Cell returns the visible cell of a source position. ok is false when the
// position is above the scroll line, below the viewport, hidden by the
// horizontal scroll, or — when RowLines is set — hidden by a collapsed
// fold.
func (p *Pane) Cell(pos Position) (row, col int, ok bool) {
	if pos.Line < 0 || pos.Line >= len(p.Lines) {
		return 0, 0, false
	}
	if p.RowLines != nil {
		abs := p.rowOfLine(pos.Line)
		if abs < 0 || abs < p.Scroll || abs >= p.Scroll+p.Height {
			return 0, 0, false
		}
		cellInLine := p.offsetToCell(pos.Line, pos.Offset)
		if cellInLine < p.Offset {
			return 0, 0, false
		}
		return abs - p.Scroll, p.GutterWidth + (cellInLine - p.Offset), true
	}
	if pos.Line < p.Scroll || pos.Line >= len(p.Lines) {
		return 0, 0, false
	}
	rowsBefore := p.rowsBefore(pos.Line)
	if rowsBefore >= p.Height {
		return 0, 0, false
	}
	if p.WrapWidth > 0 {
		cellInLine := p.offsetToCell(pos.Line, pos.Offset)
		seg := 0
		if cellInLine > 0 {
			seg = (cellInLine - 1) / p.WrapWidth
		}
		row = rowsBefore + seg
		col = p.GutterWidth + (cellInLine - seg*p.WrapWidth)
		if row >= p.Height {
			return 0, 0, false
		}
		return row, col, true
	}
	cellInLine := p.offsetToCell(pos.Line, pos.Offset)
	if cellInLine < p.Offset {
		return 0, 0, false
	}
	row = rowsBefore
	col = p.GutterWidth + (cellInLine - p.Offset)
	if row >= p.Height {
		return 0, 0, false
	}
	return row, col, true
}

// Range is a byte range of source positions. It is normalized by
// RangeBytes: Start precedes End.
type Range struct {
	Start, End Position
}

// CellSpan is a contiguous run of cells on one viewport row covered by a
// selection range. Row is a content row (0-based, the same row numbering
// Position uses); ColStart and ColEnd are content columns counting gutter
// cells, so a span maps directly onto the viewport line it was derived
// from. Gutter cells never appear in a span: ColStart is always at least
// GutterWidth.
type CellSpan struct {
	Row      int
	ColStart int
	ColEnd   int
}

// CellsInRange returns the viewport cells covered by r, normalized and
// clamped to the pane's lines, as one span per covered row. Rows above
// the scroll line and below the viewport height are skipped, so the
// result is ready to be painted over the rendered viewport without any
// further viewport math. With wrapping disabled each logical line yields
// at most one span; with wrapping a covered line yields one span per
// wrapped segment.
func (p *Pane) CellsInRange(r Range) []CellSpan {
	start, end := r.Start, r.End
	if end.Line < start.Line || (end.Line == start.Line && end.Offset < start.Offset) {
		start, end = end, start
	}
	if start.Line < 0 {
		start = Position{}
	}
	if start.Offset < 0 {
		start.Offset = 0
	}
	if end.Line >= len(p.Lines) {
		if len(p.Lines) == 0 {
			return nil
		}
		end.Line = len(p.Lines) - 1
		end.Offset = len(p.Lines[end.Line])
	}
	if end.Line < 0 {
		return nil
	}
	if end.Offset > len(p.Lines[end.Line]) {
		end.Offset = len(p.Lines[end.Line])
	}
	if start == end {
		return nil
	}
	if start.Line > end.Line {
		return nil
	}
	var spans []CellSpan
	for line := start.Line; line <= end.Line; line++ {
		if line < p.Scroll {
			continue
		}
		var rowStart int
		if p.RowLines != nil {
			abs := p.rowOfLine(line)
			if abs < 0 {
				// The line is hidden by a collapsed fold: it renders no
				// cells and contributes no span (its bytes still belong to
				// the exact source range reported by RangeBytes).
				continue
			}
			rowStart = abs - p.Scroll
		} else {
			rowStart = p.rowsBefore(line)
		}
		if rowStart >= p.Height {
			break // every following line is below the viewport too
		}
		var bStart, bEnd int
		switch {
		case line == start.Line && line == end.Line:
			bStart, bEnd = start.Offset, end.Offset
		case line == start.Line:
			bStart, bEnd = start.Offset, len(p.Lines[line])
		case line == end.Line:
			bStart, bEnd = 0, end.Offset
		default:
			bStart, bEnd = 0, len(p.Lines[line])
		}
		cellStart := p.offsetToCell(line, bStart)
		cellEnd := p.offsetToCell(line, bEnd)
		// A multi-line selection is also a visible text selection, not
		// merely a set of source runes. Every row crossed before the end
		// row continues to the right edge of the viewport content. This
		// keeps non-empty and empty intermediate rows visually consistent
		// while leaving the exact source range unchanged for copying.
		if p.WrapWidth <= 0 && p.ContentWidth > 0 && line < end.Line {
			if line != start.Line {
				cellStart = 0
			}
			cellEnd = p.ContentWidth
		}
		spans = append(spans, p.cellsForLine(line, rowStart, cellStart, cellEnd)...)
	}
	return spans
}

// cellsForLine returns the spans of one logical line covered by the cell
// range [cellStart, cellEnd), one per wrapped segment. Rows at or beyond
// the viewport height are dropped.
func (p *Pane) cellsForLine(line, rowStart, cellStart, cellEnd int) []CellSpan {
	if cellStart >= cellEnd {
		return nil
	}
	if p.WrapWidth <= 0 {
		return []CellSpan{{Row: rowStart, ColStart: p.GutterWidth + cellStart, ColEnd: p.GutterWidth + cellEnd}}
	}
	lineW := p.lineWidth(line)
	var spans []CellSpan
	for seg := 0; ; seg++ {
		segStart := seg * p.WrapWidth
		segEnd := segStart + p.WrapWidth
		if segEnd > lineW {
			segEnd = lineW
		}
		if segStart >= lineW {
			break
		}
		lo := cellStart
		if lo < segStart {
			lo = segStart
		}
		hi := cellEnd
		if hi > segEnd {
			hi = segEnd
		}
		if lo < hi {
			row := rowStart + seg
			if row < p.Height {
				spans = append(spans, CellSpan{Row: row, ColStart: p.GutterWidth + lo, ColEnd: p.GutterWidth + hi})
			}
		}
		if segEnd >= lineW {
			break
		}
	}
	return spans
}

// contentEnd returns the byte offset one past the content of line i,
// excluding its line terminator.
func (p *Pane) contentEnd(i int) int {
	if i+1 < len(p.Offsets) {
		end := p.Offsets[i+1]
		if end > 0 && p.Source[end-1] == '\n' {
			end--
		}
		if end > 0 && p.Source[end-1] == '\r' {
			end--
		}
		return end
	}
	return len(p.Source)
}

// RangeBytes returns the exact source bytes covered by r: line content
// without terminators, joined with "\n" across lines. The range is
// normalized (Start <= End), clamped to the pane's lines, and never
// reaches into a neighboring pane. ok is false when either position falls
// outside the pane's lines.
func (p *Pane) RangeBytes(r Range) ([]byte, bool) {
	start, end := r.Start, r.End
	if end.Line < start.Line || (end.Line == start.Line && end.Offset < start.Offset) {
		start, end = end, start
	}
	if start.Line < 0 || end.Line >= len(p.Lines) {
		return nil, false
	}
	if start.Offset < 0 {
		start.Offset = 0
	}
	if start.Offset > len(p.Lines[start.Line]) {
		start.Offset = len(p.Lines[start.Line])
	}
	if end.Offset > len(p.Lines[end.Line]) {
		end.Offset = len(p.Lines[end.Line])
	}
	if start.Line == end.Line {
		base := p.Offsets[start.Line]
		return p.Source[base+start.Offset : base+end.Offset], true
	}
	var buf bytes.Buffer
	base := p.Offsets[start.Line]
	buf.Write(p.Source[base+start.Offset : p.contentEnd(start.Line)])
	for i := start.Line + 1; i < end.Line; i++ {
		buf.WriteByte('\n')
		buf.Write(p.Source[p.Offsets[i]:p.contentEnd(i)])
	}
	buf.WriteByte('\n')
	buf.Write(p.Source[p.Offsets[end.Line] : p.Offsets[end.Line]+end.Offset])
	return buf.Bytes(), true
}

// Selectable is the keyboard-driven selection state of a pane: an anchor
// plus a cursor. Without an anchor, moving the cursor never selects
// (keyboard fallback when mouse tracking is unavailable); SelectTo anchors
// the selection at the first position and extends it.
type Selectable struct {
	// Anchor is the fixed end of the selection, nil when no selection is
	// active.
	Anchor *Position
	// Cursor is the moving end (and the plain caret when no selection is
	// active).
	Cursor Position
}

// MoveTo moves the cursor and clears any selection (plain arrow movement).
func (s *Selectable) MoveTo(pos Position) {
	s.Cursor = pos
	s.Anchor = nil
}

// SelectTo moves the cursor and anchors the selection at the first anchor
// (shift-arrow movement). The first SelectTo after a plain MoveTo anchors
// at the previous cursor position.
func (s *Selectable) SelectTo(pos Position) {
	if s.Anchor == nil {
		a := s.Cursor
		s.Anchor = &a
	}
	s.Cursor = pos
}

// Clear drops the selection and keeps the cursor.
func (s *Selectable) Clear() {
	s.Anchor = nil
}

// Range returns the normalized selection range, ok=false when no selection
// is active.
func (s *Selectable) Range() (Range, bool) {
	if s.Anchor == nil {
		return Range{}, false
	}
	return Range{Start: *s.Anchor, End: s.Cursor}, true
}

// String renders the selectable state for debugging.
func (s *Selectable) String() string {
	if s.Anchor == nil {
		return "cursor at " + s.Cursor.String()
	}
	return "selection " + s.Anchor.String() + " -> " + s.Cursor.String()
}

func (p Position) String() string {
	return "line " + strconv.Itoa(p.Line) + " offset " + strconv.Itoa(p.Offset)
}
