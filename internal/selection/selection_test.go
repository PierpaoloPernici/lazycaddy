package selection

import (
	"testing"
)

// splitLines is the test helper that builds the Lines/Offsets contract. A
// trailing newline does not create an extra empty line.
func splitLines(src []byte) ([]string, []int) {
	var lines []string
	var offsets []int
	start := 0
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			line := src[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, string(line))
			offsets = append(offsets, start)
			start = i + 1
		}
	}
	if start < len(src) {
		line := src[start:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		lines = append(lines, string(line))
		offsets = append(offsets, start)
	} else if len(src) == 0 {
		lines = append(lines, "")
		offsets = append(offsets, 0)
	}
	return lines, offsets
}

func testPane(t *testing.T, src string, height, gutter int) *Pane {
	t.Helper()
	s := []byte(src)
	lines, offsets := splitLines(s)
	return &Pane{Source: s, Lines: lines, Offsets: offsets, Height: height, GutterWidth: gutter}
}

func TestPositionBasicAndUTF8(t *testing.T) {
	p := testPane(t, "ab\ncafé\nx\n", 3, 0)
	// "ab": a at byte 0, b at byte 1, end at byte 2.
	pos, ok := p.Position(0, 0)
	if !ok || pos != (Position{Line: 0, Offset: 0}) {
		t.Errorf("Position(0,0) = %+v, %v", pos, ok)
	}
	pos, _ = p.Position(0, 2)
	if pos != (Position{Line: 0, Offset: 2}) {
		t.Errorf("Position(0,2) = %+v, want end of line 0", pos)
	}
	// "café": é is two bytes; cell 3 is the é, cell 4 is the line end.
	pos, _ = p.Position(1, 3)
	if pos != (Position{Line: 1, Offset: 3}) {
		t.Errorf("Position(1,3) = %+v, want the é (bytes 3-4)", pos)
	}
	pos, _ = p.Position(1, 4)
	if pos != (Position{Line: 1, Offset: 5}) {
		t.Errorf("Position(1,4) = %+v, want end of line 1 at byte 5", pos)
	}
	pos, ok = p.Position(2, 0)
	if !ok || pos != (Position{Line: 2, Offset: 0}) {
		t.Errorf("Position(2,0) = %+v, %v", pos, ok)
	}
	// Rows outside the pane are not positions.
	if _, ok := p.Position(3, 0); ok {
		t.Error("Position(3,0) should be outside the pane")
	}
}

func TestPositionGutter(t *testing.T) {
	p := testPane(t, "hello\nworld\n", 2, 3)
	// Columns 0-2 are the gutter.
	for col := 0; col < 3; col++ {
		if _, ok := p.Position(0, col); ok {
			t.Errorf("gutter column %d mapped to a position", col)
		}
	}
	pos, ok := p.Position(0, 3)
	if !ok || pos != (Position{Line: 0, Offset: 0}) {
		t.Errorf("Position(0,3) = %+v, %v, want start of line 0", pos, ok)
	}
	pos, _ = p.Position(1, 4)
	if pos != (Position{Line: 1, Offset: 1}) {
		t.Errorf("Position(1,4) = %+v, want 'o' of world", pos)
	}
}

func TestPositionScroll(t *testing.T) {
	p := testPane(t, "a\nb\nc\nd\n", 2, 0)
	p.Scroll = 1
	pos, ok := p.Position(0, 0)
	if !ok || pos != (Position{Line: 1, Offset: 0}) {
		t.Errorf("Position(0,0) with scroll 1 = %+v, %v, want line 1", pos, ok)
	}
	pos, _ = p.Position(1, 0)
	if pos != (Position{Line: 2, Offset: 0}) {
		t.Errorf("Position(1,0) with scroll 1 = %+v, want line 2", pos)
	}
	// Line 0 is scrolled out: Cell reports it as invisible.
	if _, _, ok := p.Cell(Position{Line: 0, Offset: 0}); ok {
		t.Error("Cell of scrolled-out line 0 should be invisible")
	}
}

func TestPositionWrap(t *testing.T) {
	p := testPane(t, "abcdefghijklmno\nx\n", 6, 0)
	p.WrapWidth = 6
	// Line 0 wraps into 3 segments (6+6+3); total rows = 4.
	if p.Rows(0) != 3 {
		t.Fatalf("Rows(0) = %d, want 3", p.Rows(0))
	}
	pos, ok := p.Position(0, 0)
	if !ok || pos != (Position{Line: 0, Offset: 0}) {
		t.Errorf("Position(0,0) = %+v, %v", pos, ok)
	}
	// Second segment starts at cell 6.
	pos, _ = p.Position(1, 0)
	if pos != (Position{Line: 0, Offset: 6}) {
		t.Errorf("Position(1,0) = %+v, want byte 6 of line 0", pos)
	}
	// Last segment has 3 cells; cell 3 clamps to the line end (byte 15).
	pos, _ = p.Position(2, 3)
	if pos != (Position{Line: 0, Offset: 15}) {
		t.Errorf("Position(2,3) = %+v, want end of line 0", pos)
	}
	// Line 1 starts on row 3.
	pos, _ = p.Position(3, 0)
	if pos != (Position{Line: 1, Offset: 0}) {
		t.Errorf("Position(3,0) = %+v, want line 1", pos)
	}
}

func TestPositionWrapUTF8(t *testing.T) {
	// Each é is two bytes but one cell; a 5-é line wraps at cell 3.
	p := testPane(t, "ééééé\n", 4, 0)
	p.WrapWidth = 3
	if p.Rows(0) != 2 {
		t.Fatalf("Rows(0) = %d, want 2", p.Rows(0))
	}
	pos, ok := p.Position(1, 0)
	if !ok || pos != (Position{Line: 0, Offset: 6}) {
		t.Errorf("Position(1,0) = %+v, want byte 6 (third é)", pos)
	}
	// Round trip: the cell of the last é's start (byte 8 = fourth é, cell
	// 4, which sits in segment 1 at column 1).
	row, col, ok := p.Cell(Position{Line: 0, Offset: 8})
	if !ok || row != 1 || col != 1 {
		t.Errorf("Cell(byte 8) = (%d,%d), %v, want (1,1)", row, col, ok)
	}
}

func TestPositionHorizontalOffset(t *testing.T) {
	p := testPane(t, "abcdef\nx\n", 2, 0)
	p.Offset = 2 // no wrapping: scroll right
	pos, ok := p.Position(0, 0)
	if !ok || pos != (Position{Line: 0, Offset: 2}) {
		t.Errorf("Position(0,0) with offset 2 = %+v, %v, want byte 2", pos, ok)
	}
	pos, _ = p.Position(0, 4)
	if pos != (Position{Line: 0, Offset: 6}) {
		t.Errorf("Position(0,4) with offset 2 = %+v, want end of line", pos)
	}
	// The first two cells are hidden by the horizontal scroll.
	if _, _, ok := p.Cell(Position{Line: 0, Offset: 0}); ok {
		t.Error("Cell(byte 0) should be hidden by horizontal scroll")
	}
	row, col, ok := p.Cell(Position{Line: 0, Offset: 3})
	if !ok || row != 0 || col != 1 {
		t.Errorf("Cell(byte 3) = (%d,%d), %v, want (0,1)", row, col, ok)
	}
}

func TestPositionBeyondContentClamps(t *testing.T) {
	p := testPane(t, "ab\n", 2, 0)
	// Cells past the line end clamp to the line end.
	pos, ok := p.Position(0, 10)
	if !ok || pos != (Position{Line: 0, Offset: 2}) {
		t.Errorf("Position(0,10) = %+v, %v, want end of line 0", pos, ok)
	}
	// Padding rows below the last line are not positions.
	if _, ok := p.Position(1, 0); ok {
		t.Error("padding row should not map to a position")
	}
}

func TestCellRoundTrip(t *testing.T) {
	p := testPane(t, "héllo world\nsecond\n", 3, 2)
	p.WrapWidth = 6
	cases := []Position{
		{Line: 0, Offset: 0},
		{Line: 0, Offset: 1}, // after é (bytes 0-1)
		{Line: 0, Offset: 4},
		{Line: 0, Offset: 11}, // end of line 0
		{Line: 1, Offset: 0},
		{Line: 1, Offset: 6},
	}
	for _, c := range cases {
		row, col, ok := p.Cell(c)
		if !ok {
			t.Errorf("Cell(%+v) not visible", c)
			continue
		}
		back, ok := p.Position(row, col)
		if !ok || back != c {
			t.Errorf("round trip %+v -> (%d,%d) -> %+v (%v)", c, row, col, back, ok)
		}
	}
}

func TestRangeBytesSingleLine(t *testing.T) {
	p := testPane(t, "hello world\n", 1, 0)
	got, ok := p.RangeBytes(Range{Position{0, 0}, Position{0, 5}})
	if !ok || string(got) != "hello" {
		t.Errorf("RangeBytes = %q, %v, want %q", got, ok, "hello")
	}
}

func TestRangeBytesMultiLine(t *testing.T) {
	p := testPane(t, "line one\nline two\nline three\n", 3, 0)
	got, ok := p.RangeBytes(Range{Position{0, 5}, Position{2, 4}})
	if !ok || string(got) != "one\nline two\nline" {
		t.Errorf("RangeBytes = %q, %v, want %q", got, ok, "one\nline two\nline")
	}
	// Reversed ranges are normalized.
	got, _ = p.RangeBytes(Range{Position{2, 4}, Position{0, 5}})
	if string(got) != "one\nline two\nline" {
		t.Errorf("reversed RangeBytes = %q", got)
	}
}

func TestRangeBytesUTF8(t *testing.T) {
	p := testPane(t, "café\nnaïve\n", 2, 0)
	got, ok := p.RangeBytes(Range{Position{0, 3}, Position{1, 6}})
	if !ok || string(got) != "é\nnaïve" {
		t.Errorf("RangeBytes = %q, %v, want %q", got, ok, "é\nnaïve")
	}
}

func TestRangeBytesCRLF(t *testing.T) {
	p := testPane(t, "first\r\nsecond\r\n", 2, 0)
	got, ok := p.RangeBytes(Range{Position{0, 0}, Position{1, 6}})
	if !ok || string(got) != "first\nsecond" {
		t.Errorf("RangeBytes(CRLF) = %q, %v, want %q", got, ok, "first\nsecond")
	}
}

func TestRangeBytesCannotCrossPanes(t *testing.T) {
	p := testPane(t, "only one line\n", 1, 0)
	// A range whose end falls outside the pane's lines is rejected; it can
	// never pull bytes from a neighboring pane.
	if _, ok := p.RangeBytes(Range{Position{0, 0}, Position{5, 0}}); ok {
		t.Error("range ending beyond the pane's lines must be rejected")
	}
	// The selection anchored at the pane's last row and dragged further
	// down maps through Position, which also refuses out-of-pane rows.
	pos, ok := p.Position(0, 5)
	if !ok {
		t.Fatal("expected a position on the last row")
	}
	if _, ok := p.Position(1, 0); ok {
		t.Error("row below the pane must not map to a position")
	}
	_ = pos
}

func TestSelectableKeyboardFallback(t *testing.T) {
	var s Selectable
	if _, ok := s.Range(); ok {
		t.Fatal("fresh selectable must have no selection")
	}
	// Plain movement never selects.
	s.MoveTo(Position{0, 2})
	if _, ok := s.Range(); ok {
		t.Fatal("MoveTo must clear the selection")
	}
	if s.Cursor != (Position{0, 2}) {
		t.Errorf("cursor = %+v, want (0,2)", s.Cursor)
	}
	// Shift-movement anchors at the previous cursor.
	s.SelectTo(Position{0, 5})
	r, ok := s.Range()
	if !ok || r.Start != (Position{0, 2}) || r.End != (Position{0, 5}) {
		t.Errorf("Range = %+v, %v, want (0,2)-(0,5)", r, ok)
	}
	// Extending backward normalizes in RangeBytes.
	s.SelectTo(Position{0, 1})
	got, ok := testPane(t, "abcdef\n", 1, 0).RangeBytes(Range{Position{0, 5}, Position{0, 1}})
	if !ok || string(got) != "bcde" {
		t.Errorf("backward selection bytes = %q, %v, want %q", got, ok, "bcde")
	}
	// Clear drops the anchor.
	s.Clear()
	if _, ok := s.Range(); ok {
		t.Fatal("Clear must drop the selection")
	}
	// A second SelectTo after Clear anchors at the current cursor.
	s.SelectTo(Position{0, 9})
	r, _ = s.Range()
	if r.Start != (Position{0, 1}) || r.End != (Position{0, 9}) {
		t.Errorf("Range after re-anchor = %+v, want (0,1)-(0,9)", r)
	}
}

func TestGutterWithWrapAndScroll(t *testing.T) {
	p := testPane(t, "one\ntwo three four\nfive\n", 6, 4)
	p.WrapWidth = 8
	p.Scroll = 1
	// Line 1 ("two three four", 14 cells) wraps into 2 segments, so it
	// occupies rows 0-1 of the viewport; line 2 starts at row 2.
	pos, ok := p.Position(0, 4)
	if !ok || pos != (Position{Line: 1, Offset: 0}) {
		t.Errorf("Position(0,4) = %+v, %v, want start of line 1", pos, ok)
	}
	pos, ok = p.Position(1, 4)
	if !ok || pos != (Position{Line: 1, Offset: 8}) {
		t.Errorf("Position(1,4) = %+v, %v, want byte 8 of line 1", pos, ok)
	}
	pos, ok = p.Position(2, 4)
	if !ok || pos != (Position{Line: 2, Offset: 0}) {
		t.Errorf("Position(2,4) = %+v, %v, want start of line 2", pos, ok)
	}
	// The scrolled-out first line stays unreachable.
	if _, _, ok := p.Cell(Position{Line: 0, Offset: 0}); ok {
		t.Error("scrolled-out line must stay invisible")
	}
}

func TestCellWidthInjection(t *testing.T) {
	p := testPane(t, "ab界c\n", 1, 0)
	// With a wide-rune width function the line is 5 cells wide; the 界
	// (bytes 2-5) occupies cells 2-3.
	p.CellWidth = func(r rune) int {
		if r >= 0x1100 && (r <= 0x115F || r == 0x2329 || r == 0x232A ||
			(r >= 0x2E80 && r <= 0xA4CF) || (r >= 0xAC00 && r <= 0xD7A3) ||
			(r >= 0xF900 && r <= 0xFAFF) || (r >= 0xFE30 && r <= 0xFE4F) ||
			(r >= 0xFF00 && r <= 0xFF60) || (r >= 0xFFE0 && r <= 0xFFE6)) {
			return 2
		}
		return 1
	}
	pos, ok := p.Position(0, 2)
	if !ok || pos != (Position{Line: 0, Offset: 2}) {
		t.Errorf("Position(0,2) = %+v, %v, want the wide rune start", pos, ok)
	}
	row, col, ok := p.Cell(Position{Line: 0, Offset: 2})
	if !ok || row != 0 || col != 2 {
		t.Errorf("Cell(byte 2) = (%d,%d), %v, want (0,2)", row, col, ok)
	}
	// The end of the line sits at cell 5.
	pos, _ = p.Position(0, 5)
	if pos != (Position{Line: 0, Offset: 6}) {
		t.Errorf("Position(0,5) = %+v, want end of line", pos)
	}
}

func TestSelectionString(t *testing.T) {
	var s Selectable
	if got := s.String(); got != "cursor at line 0 offset 0" {
		t.Errorf("fresh String = %q", got)
	}
	s.SelectTo(Position{1, 2})
	if got := s.String(); got != "selection line 0 offset 0 -> line 1 offset 2" {
		t.Errorf("selected String = %q", got)
	}
}

func TestLineWidthOutOfRange(t *testing.T) {
	p := testPane(t, "ab\ncd\n", 2, 0)
	if w := p.lineWidth(-1); w != 0 {
		t.Errorf("lineWidth(-1) = %d, want 0", w)
	}
	if w := p.lineWidth(2); w != 0 {
		t.Errorf("lineWidth(2) = %d, want 0", w)
	}
}

func TestRowsEmptyWrappedLineIsOneRow(t *testing.T) {
	p := testPane(t, "\nabcd\n", 2, 0)
	p.WrapWidth = 3
	if got := p.Rows(0); got != 1 {
		t.Errorf("Rows(empty wrapped line) = %d, want 1", got)
	}
	if got := p.Rows(1); got != 2 {
		t.Errorf("Rows(abcd with wrap 3) = %d, want 2", got)
	}
}

func TestRowsBeforeBelowScroll(t *testing.T) {
	p := testPane(t, "a\nb\nc\n", 3, 0)
	p.Scroll = 1
	if got := p.rowsBefore(0); got != 0 {
		t.Errorf("rowsBefore(0) below scroll = %d, want 0", got)
	}
	if got := p.rowsBefore(2); got != 1 {
		t.Errorf("rowsBefore(2) = %d, want 1", got)
	}
}

func TestCellToOffsetGuards(t *testing.T) {
	p := testPane(t, "ab\n", 1, 0)
	if got := p.cellToOffset(5, 0); got != 0 {
		t.Errorf("cellToOffset(out-of-range line) = %d, want 0", got)
	}
	if got := p.cellToOffset(0, -4); got != 0 {
		t.Errorf("cellToOffset(negative cell) = %d, want 0", got)
	}
}

func TestCellToOffsetSnapsInsideWideRune(t *testing.T) {
	p := testPane(t, "ab界c\n", 1, 0)
	p.CellWidth = func(r rune) int {
		if r >= 0x2E80 && r <= 0xA4CF {
			return 2
		}
		return 1
	}
	// Cell 3 falls inside the wide rune (cells 2-3); it snaps to the rune's
	// start at byte 2.
	if got := p.cellToOffset(0, 3); got != 2 {
		t.Errorf("cellToOffset(0,3) = %d, want 2", got)
	}
}

func TestOffsetToCellClamps(t *testing.T) {
	p := testPane(t, "ab\n", 1, 0)
	if got := p.offsetToCell(5, 0); got != 0 {
		t.Errorf("offsetToCell(out-of-range line) = %d, want 0", got)
	}
	if got := p.offsetToCell(0, -3); got != 0 {
		t.Errorf("offsetToCell(negative offset) = %d, want 0", got)
	}
	if got := p.offsetToCell(0, 99); got != 2 {
		t.Errorf("offsetToCell(overflow offset) = %d, want 2", got)
	}
}

func TestPositionWrapClampsPastSegmentEnd(t *testing.T) {
	p := testPane(t, "abcdefghijklmno\nx\n", 6, 0)
	p.WrapWidth = 6
	// The last segment holds 3 cells (12,13,14); columns beyond it clamp to
	// the line end.
	pos, ok := p.Position(2, 9)
	if !ok || pos != (Position{Line: 0, Offset: 15}) {
		t.Errorf("Position(2,9) = %+v, %v, want end of line 0", pos, ok)
	}
}

func TestPositionClampsNegativeHorizontalScroll(t *testing.T) {
	p := testPane(t, "abcdef\nx\n", 2, 0)
	p.Offset = -2
	pos, ok := p.Position(0, 0)
	if !ok || pos != (Position{Line: 0, Offset: 0}) {
		t.Errorf("Position(0,0) with offset -2 = %+v, %v, want offset 0", pos, ok)
	}
}

func TestCellRejectsRowsBelowViewport(t *testing.T) {
	p := testPane(t, "a\nb\nc\n", 1, 0)
	if _, _, ok := p.Cell(Position{Line: 1, Offset: 0}); ok {
		t.Error("Cell of a row below the 1-row viewport must be rejected")
	}
}

func TestCellRejectsWrappedSegmentBeyondViewport(t *testing.T) {
	p := testPane(t, "abcdefghijklmno\nx\n", 2, 0)
	p.WrapWidth = 5
	// Line 0 wraps into 3 segments (rows 0-2); the last segment is beyond
	// the 2-row viewport.
	if _, _, ok := p.Cell(Position{Line: 0, Offset: 13}); ok {
		t.Error("Cell in a wrapped segment beyond the viewport must be rejected")
	}
}

func TestRangeBytesClampsOffsets(t *testing.T) {
	p := testPane(t, "hello world\nsecond\n", 2, 0)
	// Negative start offsets clamp to the line start.
	got, ok := p.RangeBytes(Range{Position{0, -3}, Position{0, 5}})
	if !ok || string(got) != "hello" {
		t.Errorf("negative start = %q, %v, want %q", got, ok, "hello")
	}
	// Overlong offsets clamp to their line content.
	got, ok = p.RangeBytes(Range{Position{0, 1}, Position{1, 99}})
	if !ok || string(got) != "ello world\nsecond" {
		t.Errorf("clamped range = %q, %v, want %q", got, ok, "ello world\nsecond")
	}
	// A start offset past the line end clamps before joining the next line.
	got, _ = p.RangeBytes(Range{Position{0, 99}, Position{1, 3}})
	if string(got) != "\nsec" {
		t.Errorf("start past end = %q, want %q", got, "\nsec")
	}
}

func TestCellsInRangeSingleLine(t *testing.T) {
	p := testPane(t, "hello world\n", 2, 3)
	spans := p.CellsInRange(Range{Position{0, 0}, Position{0, 5}})
	want := []CellSpan{{Row: 0, ColStart: 3, ColEnd: 8}}
	if len(spans) != 1 || spans[0] != want[0] {
		t.Errorf("CellsInRange = %+v, want %+v", spans, want)
	}
}

func TestCellsInRangeMultiLine(t *testing.T) {
	p := testPane(t, "one\ntwo three\nfour\n", 3, 0)
	spans := p.CellsInRange(Range{Position{0, 1}, Position{2, 2}})
	want := []CellSpan{
		{Row: 0, ColStart: 1, ColEnd: 3},
		{Row: 1, ColStart: 0, ColEnd: 9},
		{Row: 2, ColStart: 0, ColEnd: 2},
	}
	if len(spans) != len(want) {
		t.Fatalf("CellsInRange = %+v, want %+v", spans, want)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Errorf("span %d = %+v, want %+v", i, spans[i], want[i])
		}
	}
}

func TestCellsInRangeReversedAndZero(t *testing.T) {
	p := testPane(t, "abcdef\n", 1, 0)
	// Reversed ranges are normalized.
	spans := p.CellsInRange(Range{Position{0, 4}, Position{0, 1}})
	want := []CellSpan{{Row: 0, ColStart: 1, ColEnd: 4}}
	if len(spans) != 1 || spans[0] != want[0] {
		t.Errorf("reversed CellsInRange = %+v, want %+v", spans, want)
	}
	// A zero-length range covers nothing.
	if spans := p.CellsInRange(Range{Position{0, 2}, Position{0, 2}}); len(spans) != 0 {
		t.Errorf("zero-length CellsInRange = %+v, want none", spans)
	}
}

func TestCellsInRangeGutterExcluded(t *testing.T) {
	p := testPane(t, "ab\ncd\n", 2, 6)
	// A full-line selection must still start after the gutter.
	spans := p.CellsInRange(Range{Position{0, 0}, Position{1, 2}})
	for _, sp := range spans {
		if sp.ColStart < 6 {
			t.Errorf("span %+v starts inside the gutter", sp)
		}
	}
	if len(spans) != 2 || spans[0] != (CellSpan{Row: 0, ColStart: 6, ColEnd: 8}) {
		t.Errorf("CellsInRange = %+v, want gutter-offset spans", spans)
	}
}

func TestCellsInRangeWrap(t *testing.T) {
	p := testPane(t, "abcdefghijklmno\nx\n", 4, 0)
	p.WrapWidth = 6
	// Selecting bytes 3..12 of line 0 covers cells 3..6 of segment 0 and
	// cells 6..12 of segment 1 (segment 0 occupies line cells 0..5).
	spans := p.CellsInRange(Range{Position{0, 3}, Position{0, 12}})
	want := []CellSpan{
		{Row: 0, ColStart: 3, ColEnd: 6},
		{Row: 1, ColStart: 6, ColEnd: 12},
	}
	if len(spans) != len(want) {
		t.Fatalf("CellsInRange = %+v, want %+v", spans, want)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Errorf("span %d = %+v, want %+v", i, spans[i], want[i])
		}
	}
}

func TestCellsInRangeScrollAndHeight(t *testing.T) {
	p := testPane(t, "a\nb\nc\nd\ne\n", 2, 0)
	p.Scroll = 2
	// Lines 0-1 are scrolled out; line 4 is below the 2-row viewport.
	spans := p.CellsInRange(Range{Position{0, 0}, Position{4, 1}})
	want := []CellSpan{
		{Row: 0, ColStart: 0, ColEnd: 1},
		{Row: 1, ColStart: 0, ColEnd: 1},
	}
	if len(spans) != len(want) {
		t.Fatalf("CellsInRange = %+v, want %+v", spans, want)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Errorf("span %d = %+v, want %+v", i, spans[i], want[i])
		}
	}
}

func TestCellsInRangeEmptyLines(t *testing.T) {
	p := testPane(t, "\nabcd\n\n", 3, 0)
	spans := p.CellsInRange(Range{Position{0, 0}, Position{2, 0}})
	// The middle line is fully selected; empty lines contribute nothing.
	want := []CellSpan{{Row: 1, ColStart: 0, ColEnd: 4}}
	if len(spans) != len(want) {
		t.Fatalf("CellsInRange = %+v, want %+v", spans, want)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Errorf("span %d = %+v, want %+v", i, spans[i], want[i])
		}
	}
}

func TestCellsInRangeSelectedEmptyLineUsesContentWidth(t *testing.T) {
	p := testPane(t, "first\n\nlast\n", 3, 2)
	p.ContentWidth = 8

	spans := p.CellsInRange(Range{Position{0, 0}, Position{2, 0}})
	want := []CellSpan{
		{Row: 0, ColStart: 2, ColEnd: 10},
		{Row: 1, ColStart: 2, ColEnd: 10},
	}
	if len(spans) != len(want) {
		t.Fatalf("CellsInRange = %+v, want %+v", spans, want)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Errorf("span %d = %+v, want %+v", i, spans[i], want[i])
		}
	}

	if spans := p.CellsInRange(Range{Position{1, 0}, Position{1, 0}}); len(spans) != 0 {
		t.Errorf("zero-length empty-line selection = %+v, want none", spans)
	}
}

func TestCellsInRangeOutOfBounds(t *testing.T) {
	p := testPane(t, "ab\n", 1, 0)
	// End beyond the last line clamps to the line end.
	spans := p.CellsInRange(Range{Position{0, 1}, Position{9, 0}})
	want := []CellSpan{{Row: 0, ColStart: 1, ColEnd: 2}}
	if len(spans) != 1 || spans[0] != want[0] {
		t.Errorf("CellsInRange = %+v, want %+v", spans, want)
	}
	// A start beyond the content is clamped too.
	spans = p.CellsInRange(Range{Position{0, 99}, Position{0, 99}})
	if len(spans) != 0 {
		t.Errorf("overlong zero-range CellsInRange = %+v, want none", spans)
	}
}

// foldedPane builds a Pane over a 4-line source with a collapse on lines
// 2-3 (0-based): display rows are [0, 1, -1(indic), 3] and the logical
// lines stay on the full source.
func foldedPane(t *testing.T, height, scroll int) *Pane {
	t.Helper()
	src := "one\ntwo\nthree\nfour\n"
	lines, offsets := splitLines([]byte(src))
	return &Pane{
		Source:       []byte(src),
		Lines:        lines,
		Offsets:      offsets,
		GutterWidth:  0,
		Height:       height,
		ContentWidth: 8,
		Scroll:       scroll,
		RowLines:     []int{0, 1, -1, 3},
	}
}

func TestPositionFoldedRows(t *testing.T) {
	p := foldedPane(t, 4, 0)
	// Visible rows map to their source lines.
	pos, ok := p.Position(0, 0)
	if !ok || pos != (Position{Line: 0, Offset: 0}) {
		t.Errorf("Position(0,0) = %+v, %v, want line 0", pos, ok)
	}
	pos, ok = p.Position(1, 2)
	if !ok || pos != (Position{Line: 1, Offset: 2}) {
		t.Errorf("Position(1,2) = %+v, %v, want line 1 offset 2", pos, ok)
	}
	pos, ok = p.Position(3, 0)
	if !ok || pos != (Position{Line: 3, Offset: 0}) {
		t.Errorf("Position(3,0) = %+v, %v, want line 3", pos, ok)
	}
	// The fold indicator row has no source position.
	if _, ok := p.Position(2, 1); ok {
		t.Error("Position on a fold indicator row must be absent")
	}
	// Rows beyond the content are absent too.
	if _, ok := p.Position(4, 1); ok {
		t.Error("Position in padding must be absent")
	}
}

func TestPositionFoldedScroll(t *testing.T) {
	p := foldedPane(t, 2, 2)
	// Abs row Scroll+0 = the indicator row: absent.
	if _, ok := p.Position(0, 0); ok {
		t.Error("scrolled fold indicator row (abs 2) must be absent")
	}
	pos, ok := p.Position(1, 0)
	if !ok || pos != (Position{Line: 3, Offset: 0}) {
		t.Errorf("scrolled folded Position(1,0) = %+v, %v, want line 3", pos, ok)
	}
}

func TestPositionNearestSkipsFoldRows(t *testing.T) {
	p := foldedPane(t, 4, 0)
	// The indicator row (2) snaps to the closest visible line (1).
	pos, ok := p.PositionNearest(2, 1)
	if !ok || (pos != (Position{Line: 1, Offset: 1}) && pos != (Position{Line: 3, Offset: 1})) {
		t.Errorf("PositionNearest(2,1) = %+v, %v, want line 1 or 3", pos, ok)
	}
	// A row past the content clamps to the last visible line.
	pos, ok = p.PositionNearest(9, 0)
	if !ok || pos.Line != 3 {
		t.Errorf("PositionNearest(9,0) = %+v, %v, want line 3", pos, ok)
	}
	// Without RowLines the plain Position path applies.
	plain := testPane(t, "ab\n", 1, 0)
	if pos, ok := plain.PositionNearest(0, 1); !ok || pos != (Position{Line: 0, Offset: 1}) {
		t.Errorf("plain PositionNearest(0,1) = %+v, %v", pos, ok)
	}
}

func TestCellFolded(t *testing.T) {
	p := foldedPane(t, 4, 0)
	row, col, ok := p.Cell(Position{Line: 1, Offset: 2})
	if !ok || row != 1 || col != 2 {
		t.Errorf("Cell(line 1) = %d,%d,%v, want 1,2,true", row, col, ok)
	}
	if _, _, ok := p.Cell(Position{Line: 2, Offset: 0}); ok {
		t.Error("Cell on a hidden line must be absent")
	}
	if _, _, ok := p.Cell(Position{Line: 0, Offset: 0}); !ok {
		t.Error("Cell on the first visible line must resolve")
	}
}

func TestCellsInRangeSkipsHiddenLines(t *testing.T) {
	p := foldedPane(t, 4, 0)
	// A selection over lines 1-3 (with line 2 hidden by the fold) paints
	// only the visible lines, but the range keeps its exact source span.
	spans := p.CellsInRange(Range{Start: Position{Line: 1, Offset: 1}, End: Position{Line: 3, Offset: 4}})
	var rows []int
	for _, sp := range spans {
		rows = append(rows, sp.Row)
	}
	if len(rows) != 2 || rows[0] != 1 || rows[1] != 3 {
		t.Errorf("CellsInRange rows = %v, want [1 3] (hidden line skipped)", rows)
	}
	// The exact source bytes of the spanned lines are preserved verbatim,
	// including the hidden line: copying a selection across a fold never
	// fabricates or drops bytes.
	bytes, ok := p.RangeBytes(Range{Start: Position{Line: 1, Offset: 0}, End: Position{Line: 2, Offset: 5}})
	if !ok || string(bytes) != "two\nthree" {
		t.Errorf("RangeBytes across a fold = %q, want the exact hidden bytes", bytes)
	}
}

// TestPositionFoldedPaddingAndClamps covers the folded-pane guard paths:
// padding rows below a folded content have no position, and a negative
// horizontal scroll clamps to the line start exactly like the unfolded
// pane does.
func TestPositionFoldedPaddingAndClamps(t *testing.T) {
	// The folded content has 4 rows but the viewport is taller: the
	// padding rows below it must not resolve to a source position.
	p := foldedPane(t, 5, 0)
	if _, ok := p.Position(4, 0); ok {
		t.Error("Position in folded padding must be absent")
	}
	// A negative horizontal scroll clamps back to the line start.
	neg := foldedPane(t, 4, 0)
	neg.Offset = -3
	pos, ok := neg.Position(0, 0)
	if !ok || pos != (Position{Line: 0, Offset: 0}) {
		t.Errorf("Position with negative Offset = %+v, %v, want line 0 offset 0", pos, ok)
	}
}

// TestPositionNearestEdgeCases covers PositionNearest guards: a gutter
// column clamps to the first content column, a negative row clamps to the
// top, a pane with no visible line reports no position, and a negative
// horizontal scroll clamps to the line start.
func TestPositionNearestEdgeCases(t *testing.T) {
	p := foldedPane(t, 4, 0)
	p.GutterWidth = 4
	// A column inside the gutter snaps to the first content column.
	pos, ok := p.PositionNearest(1, 1)
	if !ok || pos != (Position{Line: 1, Offset: 0}) {
		t.Errorf("PositionNearest gutter = %+v, %v, want line 1 offset 0", pos, ok)
	}
	// A negative row snaps to the top row.
	pos, ok = p.PositionNearest(-2, 4)
	if !ok || pos.Line != 0 {
		t.Errorf("PositionNearest(-2) = %+v, %v, want line 0", pos, ok)
	}
	// A pane whose rows are all fold indicators has no selectable position
	// at all: the outward scan exhausts (and skips the out-of-bounds
	// neighbors) and reports absent.
	only := &Pane{RowLines: []int{-1, -1}, Lines: []string{"x"}, Offsets: []int{0},
		Height: 2, GutterWidth: 0, ContentWidth: 8}
	if _, ok := only.PositionNearest(0, 0); ok {
		t.Error("PositionNearest on an all-indicator pane must be absent")
	}
	// A negative horizontal scroll clamps to the line start.
	neg := foldedPane(t, 4, 0)
	neg.Offset = -5
	pos, ok = neg.PositionNearest(2, 0)
	if !ok || pos.Line != 1 || pos.Offset != 0 {
		t.Errorf("PositionNearest negative Offset = %+v, %v, want line 1 offset 0", pos, ok)
	}
}

// TestRowOfLineGuards covers the rowOfLine edge cases: negative lines and
// hidden lines report -1, visible lines resolve to their content row.
func TestRowOfLineGuards(t *testing.T) {
	p := foldedPane(t, 4, 0)
	if got := p.rowOfLine(-1); got != -1 {
		t.Errorf("rowOfLine(-1) = %d, want -1", got)
	}
	if got := p.rowOfLine(2); got != -1 {
		t.Errorf("rowOfLine(hidden line 2) = %d, want -1", got)
	}
	if got := p.rowOfLine(3); got != 3 {
		t.Errorf("rowOfLine(3) = %d, want 3", got)
	}
}

// TestCellFoldedGuards covers the folded Cell guards: out-of-range lines
// are rejected and a position scrolled away horizontally is rejected too.
func TestCellFoldedGuards(t *testing.T) {
	p := foldedPane(t, 4, 0)
	if _, _, ok := p.Cell(Position{Line: -1, Offset: 0}); ok {
		t.Error("Cell with a negative line must be absent")
	}
	if _, _, ok := p.Cell(Position{Line: 9, Offset: 0}); ok {
		t.Error("Cell with an out-of-range line must be absent")
	}
	scrolled := foldedPane(t, 4, 0)
	scrolled.Offset = 10
	if _, _, ok := scrolled.Cell(Position{Line: 0, Offset: 2}); ok {
		t.Error("Cell on a horizontally scrolled-away position must be absent")
	}
}
