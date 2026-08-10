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
