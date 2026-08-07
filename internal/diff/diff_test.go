package diff

import (
	"reflect"
	"strings"
	"testing"
)

// Caddyfile fixtures. These mirror the shape of real Caddyfiles: a site
// address block with handler directives. Every fixture ends with a
// newline so gotextdiff never appends a "\ No newline at end of file"
// note, which would pollute the expected counts.
const (
	// site2 is a minimal site block with a single responder.
	site2 = "example.com {\n\trespond \"Hello\"\n}\n"
	// site3 inserts a second responder into site2.
	site3 = "example.com {\n\trespond \"Hello\"\n\trespond \"World\"\n}\n"

	// siteMixedOld and siteMixedNew share the block structure but differ
	// in two handlers: "World" becomes "Goodbye" and the reverse-proxy
	// upstream moves to another port.
	siteMixedOld = "example.com {\n\trespond \"Hello\"\n\trespond \"World\"\n\treverse_proxy localhost:8080\n}\n"
	siteMixedNew = "example.com {\n\trespond \"Hello\"\n\trespond \"Goodbye\"\n\treverse_proxy localhost:9090\n}\n"
)

// countKind returns how many lines carry the given Kind.
func countKind(lines []Line, kind Kind) int {
	n := 0
	for _, l := range lines {
		if l.Kind == kind {
			n++
		}
	}
	return n
}

// findKind returns the first line of the given Kind.
func findKind(lines []Line, kind Kind) (Line, bool) {
	for _, l := range lines {
		if l.Kind == kind {
			return l, true
		}
	}
	return Line{}, false
}

// expectedKind derives the Kind a rendered line should have from its
// leading marker, mirroring the documented classification rules. It is
// used to assert Unified classified every line correctly.
func expectedKind(text string) Kind {
	switch {
	case strings.HasPrefix(text, "--- "), strings.HasPrefix(text, "+++ "):
		return KindFileHeader
	case strings.HasPrefix(text, "@@"):
		return KindHunkHeader
	case strings.HasPrefix(text, "+"):
		return KindAdd
	case strings.HasPrefix(text, "-"):
		return KindRemove
	default:
		return KindContext
	}
}

// unifiedCase is a row in the table-driven Unified tests: the input pair
// and the exact number of each kind of line the diff must contain.
type unifiedCase struct {
	name    string
	old     string
	new     string
	wantAdd int
	wantRm  int
	wantHk  int
}

func TestUnified_Identical(t *testing.T) {
	cases := []unifiedCase{
		{name: "site-block", old: site2, new: site2},
		{name: "empty", old: "", new: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, err := Unified([]byte(tc.old), []byte(tc.new), "Caddyfile", "Caddyfile")
			if err != nil {
				t.Fatalf("Unified: unexpected error: %v", err)
			}
			if countKind(lines, KindAdd)+countKind(lines, KindRemove)+countKind(lines, KindHunkHeader) != 0 {
				t.Fatalf("identical inputs must not produce hunk lines, got %v", lines)
			}
			// The contract guarantees the file-header pair is always
			// present, even when no hunks follow.
			if len(lines) != 2 || lines[0].Kind != KindFileHeader || lines[1].Kind != KindFileHeader {
				t.Fatalf("expected exactly two file-header lines, got %v", lines)
			}
		})
	}
}

func TestUnified_Addition(t *testing.T) {
	cases := []unifiedCase{
		{name: "mid-block", old: site2, new: site3, wantAdd: 1, wantRm: 0, wantHk: 1},
		{name: "into-empty-block", old: "example.com {\n}\n", new: "example.com {\n\trespond \"World\"\n}\n", wantAdd: 1, wantRm: 0, wantHk: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, err := Unified([]byte(tc.old), []byte(tc.new), "Caddyfile", "Caddyfile")
			if err != nil {
				t.Fatalf("Unified: unexpected error: %v", err)
			}
			if got := countKind(lines, KindAdd); got != tc.wantAdd {
				t.Fatalf("expected %d KindAdd lines, got %d (%v)", tc.wantAdd, got, lines)
			}
			if got := countKind(lines, KindRemove); got != tc.wantRm {
				t.Fatalf("expected %d KindRemove lines, got %d (%v)", tc.wantRm, got, lines)
			}
			if got := countKind(lines, KindHunkHeader); got != tc.wantHk {
				t.Fatalf("expected %d hunk header, got %d (%v)", tc.wantHk, got, lines)
			}
			add, ok := findKind(lines, KindAdd)
			if !ok {
				t.Fatalf("expected a KindAdd line, got %v", lines)
			}
			if !strings.HasPrefix(add.Text, "+") {
				t.Fatalf("add line must keep the '+' marker, got %q", add.Text)
			}
		})
	}
}

func TestUnified_Removal(t *testing.T) {
	cases := []unifiedCase{
		{name: "mid-block", old: site3, new: site2, wantAdd: 0, wantRm: 1, wantHk: 1},
		{name: "emptied-block", old: "example.com {\n\trespond \"World\"\n}\n", new: "example.com {\n}\n", wantAdd: 0, wantRm: 1, wantHk: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, err := Unified([]byte(tc.old), []byte(tc.new), "Caddyfile", "Caddyfile")
			if err != nil {
				t.Fatalf("Unified: unexpected error: %v", err)
			}
			if got := countKind(lines, KindAdd); got != tc.wantAdd {
				t.Fatalf("expected %d KindAdd lines, got %d (%v)", tc.wantAdd, got, lines)
			}
			if got := countKind(lines, KindRemove); got != tc.wantRm {
				t.Fatalf("expected %d KindRemove lines, got %d (%v)", tc.wantRm, got, lines)
			}
			rm, ok := findKind(lines, KindRemove)
			if !ok {
				t.Fatalf("expected a KindRemove line, got %v", lines)
			}
			if !strings.HasPrefix(rm.Text, "-") {
				t.Fatalf("remove line must keep the '-' marker, got %q", rm.Text)
			}
		})
	}
}

func TestUnified_Mixed(t *testing.T) {
	cases := []unifiedCase{
		{name: "handlers-and-upstream", old: siteMixedOld, new: siteMixedNew, wantAdd: 2, wantRm: 2, wantHk: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, err := Unified([]byte(tc.old), []byte(tc.new), "Caddyfile", "Caddyfile")
			if err != nil {
				t.Fatalf("Unified: unexpected error: %v", err)
			}
			if got := countKind(lines, KindAdd); got != tc.wantAdd {
				t.Fatalf("expected %d KindAdd lines, got %d (%v)", tc.wantAdd, got, lines)
			}
			if got := countKind(lines, KindRemove); got != tc.wantRm {
				t.Fatalf("expected %d KindRemove lines, got %d (%v)", tc.wantRm, got, lines)
			}
			if got := countKind(lines, KindHunkHeader); got != tc.wantHk {
				t.Fatalf("expected %d hunk header, got %d (%v)", tc.wantHk, got, lines)
			}
			if got := countKind(lines, KindContext); got < 1 {
				t.Fatalf("expected at least one unchanged context line, got %d (%v)", got, lines)
			}
		})
	}
}

func TestUnified_LabelsInFileHeaders(t *testing.T) {
	cases := []struct {
		name   string
		oldLbl string
		newLbl string
	}{
		{name: "same-label", oldLbl: "Caddyfile", newLbl: "Caddyfile"},
		{name: "renamed-paths", oldLbl: "site-a/Caddyfile", newLbl: "site-b/Caddyfile"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, err := Unified([]byte(site2), []byte(site3), tc.oldLbl, tc.newLbl)
			if err != nil {
				t.Fatalf("Unified: unexpected error: %v", err)
			}
			// The header lines precede any hunk, so the first two lines
			// are always the ---/+++ pair when a diff exists.
			if len(lines) < 2 {
				t.Fatalf("expected file-header lines, got %v", lines)
			}
			wantOld := "--- " + tc.oldLbl
			wantNew := "+++ " + tc.newLbl
			if lines[0].Kind != KindFileHeader || lines[0].Text != wantOld {
				t.Errorf("expected first line %q, got %q (kind %v)", wantOld, lines[0].Text, lines[0].Kind)
			}
			if lines[1].Kind != KindFileHeader || lines[1].Text != wantNew {
				t.Errorf("expected second line %q, got %q (kind %v)", wantNew, lines[1].Text, lines[1].Kind)
			}
		})
	}
}

func TestUnified_EmptyOld(t *testing.T) {
	// An empty old source describes a brand-new file: gotextdiff has no
	// lines to reuse as context, so every content line is an addition.
	lines, err := Unified(nil, []byte(site2), "Caddyfile", "Caddyfile")
	if err != nil {
		t.Fatalf("Unified: unexpected error: %v", err)
	}
	if got := countKind(lines, KindRemove); got != 0 {
		t.Fatalf("expected no KindRemove lines for a new file, got %d (%v)", got, lines)
	}
	for _, l := range lines {
		switch l.Kind {
		case KindFileHeader, KindHunkHeader:
		case KindAdd:
		default:
			t.Fatalf("expected every content line of a new file to be KindAdd, got %v", l)
		}
	}
	if got := countKind(lines, KindHunkHeader); got != 1 {
		t.Fatalf("expected one hunk header, got %d (%v)", got, lines)
	}
}

func TestUnified_EmptyNew(t *testing.T) {
	// An empty new source describes a deleted file: every content line is
	// a removal and there is nothing left to add.
	lines, err := Unified([]byte(site2), nil, "Caddyfile", "Caddyfile")
	if err != nil {
		t.Fatalf("Unified: unexpected error: %v", err)
	}
	if got := countKind(lines, KindAdd); got != 0 {
		t.Fatalf("expected no KindAdd lines for a deleted file, got %d (%v)", got, lines)
	}
	for _, l := range lines {
		switch l.Kind {
		case KindFileHeader, KindHunkHeader:
		case KindRemove:
		default:
			t.Fatalf("expected every content line of a deleted file to be KindRemove, got %v", l)
		}
	}
	if got := countKind(lines, KindHunkHeader); got != 1 {
		t.Fatalf("expected one hunk header, got %d (%v)", got, lines)
	}
}

func TestUnified_CRLF(t *testing.T) {
	cases := []struct {
		name string
		old  string
		new  string
		want string
	}{
		{
			name: "addition",
			old:  "example.com {\r\n\trespond \"Hello\"\r\n}\r\n",
			new:  "example.com {\r\n\trespond \"Hello\"\r\n\trespond \"World\"\r\n}\r\n",
			want: "+\trespond \"World\"\r",
		},
		{
			name: "removal",
			old:  "example.com {\r\n\trespond \"A\"\r\n\trespond \"B\"\r\n}\r\n",
			new:  "example.com {\r\n\trespond \"A\"\r\n}\r\n",
			want: "-\trespond \"B\"\r",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, err := Unified([]byte(tc.old), []byte(tc.new), "Caddyfile", "Caddyfile")
			if err != nil {
				t.Fatalf("Unified: unexpected error: %v", err)
			}
			var marked []string
			for _, l := range lines {
				if l.Kind == KindAdd || l.Kind == KindRemove {
					marked = append(marked, l.Text)
				}
			}
			if !slicesContains(marked, tc.want) {
				t.Fatalf("expected a marked line %q, got %v", tc.want, marked)
			}
			// CRLF input must round-trip: every content line keeps the
			// trailing '\r' so the UI can render it verbatim.
			for _, l := range lines {
				switch l.Kind {
				case KindAdd, KindRemove, KindContext:
					if !strings.HasSuffix(l.Text, "\r") {
						t.Fatalf("CRLF input lost its '\\r' in line %q", l.Text)
					}
				}
			}
		})
	}
}

// slicesContains reports whether want is present in got.
func slicesContains(got []string, want string) bool {
	for _, s := range got {
		if s == want {
			return true
		}
	}
	return false
}

func TestKindClassification_AllKinds(t *testing.T) {
	// The old source's final line has no trailing newline, which makes
	// gotextdiff append a "\ No newline at end of file" note. Combined
	// with the file headers, hunk header, additions, removals and context
	// lines this fixture exercises every Kind at least once.
	old := "example.com {\n\trespond \"Hello\"\n\trespond \"World\"\n}"
	new := "example.com {\n\trespond \"Hello\"\n\trespond \"Goodbye\"\n\treverse_proxy localhost:9090\n}\n"

	got, err := Unified([]byte(old), []byte(new), "old", "new")
	if err != nil {
		t.Fatalf("Unified: unexpected error: %v", err)
	}

	// The exact rendered output is deterministic because gotextdiff is
	// pinned; asserting every line verbatim doubles as a regression test
	// for the classification of each kind.
	want := []Line{
		{Kind: KindFileHeader, Text: "--- old"},
		{Kind: KindFileHeader, Text: "+++ new"},
		{Kind: KindHunkHeader, Text: "@@ -1,4 +1,5 @@"},
		{Kind: KindContext, Text: " example.com {"},
		{Kind: KindContext, Text: " \trespond \"Hello\""},
		{Kind: KindRemove, Text: "-\trespond \"World\""},
		{Kind: KindRemove, Text: "-}"},
		{Kind: KindContext, Text: "\\ No newline at end of file"},
		{Kind: KindAdd, Text: "+\trespond \"Goodbye\""},
		{Kind: KindAdd, Text: "+\treverse_proxy localhost:9090"},
		{Kind: KindAdd, Text: "+}"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unified mismatch:\n got: %v\nwant: %v", got, want)
	}

	// Independent of the exact rendering, the documented classification
	// rules must hold for every line: the marker decides the Kind.
	for _, l := range got {
		if exp := expectedKind(l.Text); l.Kind != exp {
			t.Errorf("line %q: got Kind %v, want %v", l.Text, l.Kind, exp)
		}
	}
	for _, kind := range []Kind{KindContext, KindAdd, KindRemove, KindHunkHeader, KindFileHeader} {
		if countKind(got, kind) < 1 {
			t.Errorf("expected at least one %v line in %v", kind, got)
		}
	}
}
