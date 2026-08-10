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
// padding rows below the last line, or a position hidden by the horizontal
// scroll. Cells beyond the end of a line's segment clamp to that segment's
// end, so selecting to the end of a line is exact.
func (p *Pane) Position(row, col int) (Position, bool) {
	if row < 0 || row >= p.Height {
		return Position{}, false
	}
	if col < p.GutterWidth {
		return Position{}, false
	}
	contentCol := col - p.GutterWidth
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

// Cell returns the visible cell of a source position. ok is false when the
// position is above the scroll line, below the viewport, or hidden by the
// horizontal scroll.
func (p *Pane) Cell(pos Position) (row, col int, ok bool) {
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
