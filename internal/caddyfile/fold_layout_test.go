package caddyfile

import (
	"strconv"
	"strings"
	"testing"
)

// foldRangeFor builds the FoldRange for the fold whose range matches
// start/end, so tests can drive FoldLayoutFor through Folds exactly as the
// UI does.
func foldRangeFor(t *testing.T, doc *Document, start, end int) FoldRange {
	t.Helper()
	for _, f := range Folds(doc) {
		if f.Range.Start == start && f.Range.End == end {
			return FoldRange{
				Kind:           f.Kind,
				Name:           f.Name,
				Range:          f.Range,
				StartLine:      f.StartLine,
				EndLine:        f.EndLine,
				CloseBraceLine: f.CloseBraceLine,
			}
		}
	}
	t.Fatalf("no fold with range [%d:%d) in %+v", start, end, Folds(doc))
	return FoldRange{}
}

// rowsOf renders the display rows of a layout as compact tokens for
// assertions: the line number for a source-line row, "•" for a fold
// indicator row.
func rowsOf(l *FoldLayout) string {
	parts := make([]string, len(l.Rows))
	for i, r := range l.Rows {
		if r > 0 {
			parts[i] = strconv.Itoa(r)
		} else {
			parts[i] = "•"
		}
	}
	return strings.Join(parts, " ")
}

func TestFoldHiddenBraced(t *testing.T) {
	f := FoldRange{StartLine: 1, EndLine: 5, CloseBraceLine: 5}
	if got := FoldHidden(f); got != 3 {
		t.Errorf("FoldHidden(1..5 close 5) = %d, want 3", got)
	}
	f = FoldRange{StartLine: 1, EndLine: 2, CloseBraceLine: 2}
	if got := FoldHidden(f); got != 0 {
		t.Errorf("FoldHidden(empty block) = %d, want 0", got)
	}
}

func TestFoldHiddenBraceless(t *testing.T) {
	f := FoldRange{StartLine: 1, EndLine: 3, CloseBraceLine: 0}
	if got := FoldHidden(f); got != 2 {
		t.Errorf("FoldHidden(braceless 1..3) = %d, want 2", got)
	}
	f = FoldRange{StartLine: 2, EndLine: 2, CloseBraceLine: 0}
	if got := FoldHidden(f); got != 0 {
		t.Errorf("FoldHidden(single-line braceless) = %d, want 0", got)
	}
}

func TestFoldLayoutBasic(t *testing.T) {
	src := []byte("example.test {\n\troute {\n\t\thandle /api {\n\t\t\trespond ok\n\t\t}\n\t}\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	site := foldRangeFor(t, doc, 0, len(src))
	layout := FoldLayoutFor(src, []FoldRange{site})
	if layout == nil {
		t.Fatal("FoldLayoutFor returned nil for a collapseable site")
	}
	// site header (1), indicator, close brace (7), trailing empty line (8)
	wantRows := []int{1, 0, 7, 8}
	if len(layout.Rows) != len(wantRows) {
		t.Fatalf("rows = %v, want %v", layout.Rows, wantRows)
	}
	for i, want := range wantRows {
		if layout.Rows[i] != want {
			t.Errorf("row %d = %d, want %d", i, layout.Rows[i], want)
		}
	}
	if layout.Folds[0].Hidden != 5 {
		t.Errorf("site hidden = %d, want 5", layout.Folds[0].Hidden)
	}
	// Visible lines map to their display row; hidden lines map to -1.
	for line, row := range map[int]int{1: 0, 7: 2, 8: 3} {
		if layout.LineRow[line] != row {
			t.Errorf("LineRow[%d] = %d, want %d", line, layout.LineRow[line], row)
		}
	}
	for _, line := range []int{2, 3, 4, 5, 6} {
		if layout.LineRow[line] != -1 {
			t.Errorf("LineRow[%d] = %d, want -1 (hidden)", line, layout.LineRow[line])
		}
	}
}

func TestFoldLayoutNestedIndependent(t *testing.T) {
	src := []byte("example.test {\n\troute {\n\t\thandle /api {\n\t\t\trespond ok\n\t\t}\n\t}\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	folds := Folds(doc)
	site := foldRangeFor(t, doc, 0, len(src))
	route := foldRangeFor(t, doc, folds[1].Range.Start, folds[1].Range.End)
	handle := foldRangeFor(t, doc, folds[2].Range.Start, folds[2].Range.End)

	// Only the route fold collapsed: site header(1), route header(2),
	// indicator, route close(6), site close(7), trailing line(8).
	layout := FoldLayoutFor(src, []FoldRange{route})
	if got := rowsOf(layout); got != "1 2 • 6 7 8" {
		t.Errorf("route-only rows = %q, want \"1 2 • 6 7 8\"", got)
	}
	if layout.Folds[0].Hidden != 3 {
		t.Errorf("route hidden = %d, want 3", layout.Folds[0].Hidden)
	}

	// Only the handle fold collapsed: the outer blocks stay open.
	layout = FoldLayoutFor(src, []FoldRange{handle})
	if got := rowsOf(layout); got != "1 2 3 • 5 6 7 8" {
		t.Errorf("handle-only rows = %q, want \"1 2 3 • 5 6 7 8\"", got)
	}

	// All three collapsed: the outer fold subsumes the inner ones; the
	// inner fold states are preserved in the layout only by their absence
	// from the display (they remain active underneath).
	layout = FoldLayoutFor(src, []FoldRange{site, route, handle})
	if got := rowsOf(layout); got != "1 • 7 8" {
		t.Errorf("all-collapsed rows = %q, want \"1 • 7 8\"", got)
	}
	if len(layout.Folds) != 1 || layout.Folds[0].Name != "example.test" {
		t.Errorf("all-collapsed folds = %+v, want only the site", layout.Folds)
	}
}

func TestFoldLayoutEmptyAndSingleLineBlocksIgnored(t *testing.T) {
	src := []byte("empty.example.test {\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	empty := foldRangeFor(t, doc, 0, len(src))
	if layout := FoldLayoutFor(src, []FoldRange{empty}); layout != nil {
		t.Errorf("FoldLayoutFor on an empty block = %+v, want nil", layout)
	}
	// Single-line blocks are simply not foldable.
	one := FoldRange{StartLine: 2, EndLine: 2, CloseBraceLine: 0}
	if layout := FoldLayoutFor(src, []FoldRange{one}); layout != nil {
		t.Errorf("FoldLayoutFor on a single-line block = %+v, want nil", layout)
	}
}

func TestFoldLayoutBracelessSite(t *testing.T) {
	src := []byte("localhost:8080\n\trespond ok\n\tfile_server\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	site := foldRangeFor(t, doc, 0, len(src))
	if site.CloseBraceLine != 0 {
		t.Fatalf("braceless site close brace = %d, want 0", site.CloseBraceLine)
	}
	layout := FoldLayoutFor(src, []FoldRange{site})
	if got := rowsOf(layout); got != "1 •" {
		t.Errorf("braceless rows = %q, want \"1 •\"", got)
	}
	// The braceless site's range covers every line through the trailing
	// empty line, so the indicator replaces them all.
	if layout.Folds[0].Hidden != 3 {
		t.Errorf("braceless hidden = %d, want 3", layout.Folds[0].Hidden)
	}
	if layout.LineRow[1] != 0 || layout.LineRow[2] != -1 || layout.LineRow[3] != -1 || layout.LineRow[4] != -1 {
		t.Errorf("braceless LineRow = %v, want 1->0 and 2,3,4 hidden", layout.LineRow)
	}
}

func TestFoldLayoutSeparatedBrace(t *testing.T) {
	src := []byte("example.test\n{\n\trespond ok\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	site := foldRangeFor(t, doc, 0, len(src))
	if site.CloseBraceLine != 4 {
		t.Fatalf("separated-brace close = %d, want 4", site.CloseBraceLine)
	}
	layout := FoldLayoutFor(src, []FoldRange{site})
	if got := rowsOf(layout); got != "1 • 4 5" {
		t.Errorf("separated-brace rows = %q, want \"1 • 4 5\"", got)
	}
	if layout.Folds[0].Hidden != 2 {
		t.Errorf("separated-brace hidden = %d, want 2 (open brace and body)", layout.Folds[0].Hidden)
	}
}

func TestFoldLayoutUnclosedBlock(t *testing.T) {
	src := []byte("example.test {\n\troute {\n\t\trespond ok\n\t}\n")
	doc := Parse(src)
	if doc.Err == nil {
		t.Fatal("expected a parse error for an unclosed block")
	}
	folds := Folds(doc)
	if len(folds) != 2 {
		t.Fatalf("folds = %+v, want the site and the route", folds)
	}
	site := foldRangeFor(t, doc, 0, len(src))
	// The site is unclosed, so the close-brace token inside its range is
	// the route's brace: it anchors the visible tail line of the folded
	// site (graceful degradation on partially parsed input).
	if site.CloseBraceLine == 0 {
		t.Fatal("unclosed site should keep the deepest child's close brace as its tail anchor")
	}
	layout := FoldLayoutFor(src, []FoldRange{site})
	if got := rowsOf(layout); got != "1 • 4 5" {
		t.Errorf("unclosed rows = %q, want \"1 • 4 5\"", got)
	}
	if layout.Folds[0].Hidden != 2 {
		t.Errorf("unclosed hidden = %d, want 2", layout.Folds[0].Hidden)
	}
}

func TestFoldLayoutLineRowFullMapping(t *testing.T) {
	src := []byte("example.test {\n\troute {\n\t\trespond ok\n\t}\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	site := foldRangeFor(t, doc, 0, len(src))
	layout := FoldLayoutFor(src, []FoldRange{site})
	// Every source line maps to exactly one display row, except hidden
	// ones which map to -1.
	seen := map[int]bool{}
	for line := 1; line < len(layout.LineRow); line++ {
		row := layout.LineRow[line]
		if row >= 0 {
			if seen[row] {
				t.Errorf("display row %d mapped by multiple lines", row)
			}
			seen[row] = true
			if layout.Rows[row] != line {
				t.Errorf("Rows[%d] = %d, want %d", row, layout.Rows[row], line)
			}
		}
	}
}

// TestFoldLayoutCloseBraceDetectedInFolds mirrors the UI contract: Folds
// reports CloseBraceLine so the fold layout always knows whether the
// closing brace line stays visible.
func TestFoldLayoutCloseBraceDetectedInFolds(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy localhost:8080\n\tfile_server\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	site := Folds(doc)[0]
	if site.CloseBraceLine != 4 {
		t.Errorf("site CloseBraceLine = %d, want 4", site.CloseBraceLine)
	}
}
