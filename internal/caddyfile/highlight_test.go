package caddyfile

import (
	"fmt"
	"strings"
	"testing"
)

// sameSpans reports whether two span groups are exactly equal, including
// empty trailing lines.
func sameSpans(a, b [][]Span) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

// dumpSpans renders spans for readable test failures.
func dumpSpans(src []byte, lines [][]Span) string {
	var b strings.Builder
	for i, spans := range lines {
		fmt.Fprintf(&b, "line %d:", i)
		for _, s := range spans {
			fmt.Fprintf(&b, " [%s %d:%d %q]", s.Kind, s.Start, s.End, src[s.Start:s.End])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// checkHighlight asserts exact per-line spans and that the number of lines
// aligns 1:1 with strings.Split(string(src), "\n").
func checkHighlight(t *testing.T, src []byte, want [][]Span) {
	t.Helper()
	got := Highlight(src)
	if !sameSpans(got, want) {
		t.Errorf("Highlight(%q) =\n%s\nwant\n%s", src, dumpSpans(src, got), dumpSpans(src, want))
	}
	if n := strings.Count(string(src), "\n") + 1; len(got) != n {
		t.Errorf("Highlight(%q) returned %d lines, want %d (strings.Split count)", src, len(got), n)
	}
}

// TestHighlightBraceSite covers a simple braced site: word + brace on the
// header line, directive on the next, close brace on the last.
func TestHighlightBraceSite(t *testing.T) {
	checkHighlight(t, []byte("example.test {\n\trespond ok\n}\n"), [][]Span{
		{{SpanWord, 0, 12}, {SpanOpenBrace, 13, 14}},
		{{SpanWord, 16, 23}, {SpanWord, 24, 26}},
		{{SpanCloseBrace, 27, 28}},
		{},
	})
}

// TestHighlightComments covers a full-line comment and a trailing comment
// that runs to the end of its line.
func TestHighlightComments(t *testing.T) {
	checkHighlight(t, []byte("# comment\nexample.test # note\n"), [][]Span{
		{{SpanComment, 0, 9}},
		{{SpanWord, 10, 22}, {SpanComment, 23, 29}},
		{},
	})
}

// TestHighlightHashInsideStringIsNotComment pins that a '#' inside a quoted
// string is covered by the string token and never re-derived as a comment.
func TestHighlightHashInsideStringIsNotComment(t *testing.T) {
	checkHighlight(t, []byte("respond \"# not a comment\"\n"), [][]Span{
		{{SpanWord, 0, 7}, {SpanString, 8, 25}},
		{},
	})
}

// TestHighlightPlaceholders covers a whole-word placeholder that coincides
// with the word span, a mid-word placeholder, and the no-nesting rule.
func TestHighlightPlaceholders(t *testing.T) {
	t.Run("whole word", func(t *testing.T) {
		checkHighlight(t, []byte("respond {$MSG}\n"), [][]Span{
			{{SpanWord, 0, 7}, {SpanWord, 8, 14}, {SpanPlaceholder, 8, 14}},
			{},
		})
	})
	t.Run("mid word", func(t *testing.T) {
		checkHighlight(t, []byte("foo{bar}baz\n"), [][]Span{
			{{SpanWord, 0, 11}, {SpanPlaceholder, 3, 8}},
			{},
		})
	})
	t.Run("no nesting", func(t *testing.T) {
		// Scanning resumes after the first '}' following a '{'.
		checkHighlight(t, []byte("{{a}}\n"), [][]Span{
			{{SpanWord, 0, 5}, {SpanPlaceholder, 0, 4}},
			{},
		})
	})
}

// TestHighlightPlaceholderInsideString pins a placeholder nested inside a
// SpanString: the placeholder span overlaps its parent string span.
func TestHighlightPlaceholderInsideString(t *testing.T) {
	checkHighlight(t, []byte("args \"{args[0]}\"\n"), [][]Span{
		{{SpanWord, 0, 4}, {SpanString, 5, 16}, {SpanPlaceholder, 6, 15}},
		{},
	})
}

// TestHighlightMultilineString pins that a quoted string spanning lines is
// reported on every covered line, clipped to each line's byte range.
func TestHighlightMultilineString(t *testing.T) {
	checkHighlight(t, []byte("example.test {\n\trespond \"line one\nline two\"\n}\n"), [][]Span{
		{{SpanWord, 0, 12}, {SpanOpenBrace, 13, 14}},
		{{SpanWord, 16, 23}, {SpanString, 24, 33}},
		{{SpanString, 34, 43}},
		{{SpanCloseBrace, 44, 45}},
		{},
	})
}

// TestHighlightHeredoc pins that a heredoc (including its <<MARKER opener and
// the closing marker line) is reported on every covered line, clipped to each
// line's byte range.
func TestHighlightHeredoc(t *testing.T) {
	checkHighlight(t, []byte("example.test {\n\trespond <<EOT\nbody one\nbody two\nbody three\nEOT\n}\n"), [][]Span{
		{{SpanWord, 0, 12}, {SpanOpenBrace, 13, 14}},
		{{SpanWord, 16, 23}, {SpanHeredoc, 24, 29}},
		{{SpanHeredoc, 30, 38}},
		{{SpanHeredoc, 39, 47}},
		{{SpanHeredoc, 48, 58}},
		{{SpanHeredoc, 59, 62}},
		{{SpanCloseBrace, 63, 64}},
		{},
	})
}

// TestHighlightCRLF pins correct clipping and comment detection on CRLF
// input: the trailing '\r' stays part of the line's content and the comment
// runs to the '\n'.
func TestHighlightCRLF(t *testing.T) {
	checkHighlight(t, []byte("example.test {\r\n\trespond ok # note\r\n}\r\n"), [][]Span{
		{{SpanWord, 0, 12}, {SpanOpenBrace, 13, 14}},
		{{SpanWord, 17, 24}, {SpanWord, 25, 27}, {SpanComment, 28, 35}},
		{{SpanCloseBrace, 36, 37}},
		{},
	})
}

// TestHighlightEmptySource pins that empty input returns nil and that bare
// newlines produce one empty line group each.
func TestHighlightEmptySource(t *testing.T) {
	if got := Highlight(nil); got != nil {
		t.Errorf("Highlight(nil) = %v, want nil", got)
	}
	if got := Highlight([]byte{}); got != nil {
		t.Errorf("Highlight(empty) = %v, want nil", got)
	}
	checkHighlight(t, []byte("\n"), [][]Span{{}, {}})
	checkHighlight(t, []byte("\n\n"), [][]Span{{}, {}, {}})
}

// TestHighlightHashInsideHeredocIsNotComment pins that a '#' preceded by
// content inside a heredoc body is covered by the heredoc token and never
// re-derived as a comment.
func TestHighlightHashInsideHeredocIsNotComment(t *testing.T) {
	checkHighlight(t, []byte("example.test {\n\trespond <<EOT\nbody # one\nEOT\n}\n"), [][]Span{
		{{SpanWord, 0, 12}, {SpanOpenBrace, 13, 14}},
		{{SpanWord, 16, 23}, {SpanHeredoc, 24, 29}},
		{{SpanHeredoc, 30, 40}},
		{{SpanHeredoc, 41, 44}},
		{{SpanCloseBrace, 45, 46}},
		{},
	})
}

// TestHighlightNoTrailingNewline pins that input without a final newline
// produces one line group for the unterminated line.
func TestHighlightNoTrailingNewline(t *testing.T) {
	checkHighlight(t, []byte("example.test {\n\trespond ok"), [][]Span{
		{{SpanWord, 0, 12}, {SpanOpenBrace, 13, 14}},
		{{SpanWord, 16, 23}, {SpanWord, 24, 26}},
	})
}

// TestHighlightBOM pins that a leading UTF-8 BOM is skipped by the lexer
// without shifting byte offsets, so spans start at the first real token.
func TestHighlightBOM(t *testing.T) {
	src := []byte("\xEF\xBB\xBFexample.test {\n\trespond ok\n}\n")
	checkHighlight(t, src, [][]Span{
		{{SpanWord, 3, 15}, {SpanOpenBrace, 16, 17}},
		{{SpanWord, 19, 26}, {SpanWord, 27, 29}},
		{{SpanCloseBrace, 30, 31}},
		{},
	})
}
