package caddyfile

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func parseGroups(t *testing.T, src string) []CommentGroup {
	t.Helper()
	doc := Parse([]byte(src))
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	return CommentGroups(doc)
}

func groupSummary(g CommentGroup) string {
	after := "nil"
	if g.After != nil {
		after = g.After.Name
	}
	return fmt.Sprintf("%d|%d|%s|after:%s", g.StartLine, g.EndLine, g.Preview, after)
}

// TestCommentGroups_TopLevelGroups verifies header, between-block and
// footer comments become one group each, in source order, with exact
// line ranges and previews, and that each group identifies the block it
// documents when one follows it.
func TestCommentGroups_TopLevelGroups(t *testing.T) {
	src := "# header one\n# header two\nexample.test {\n\trespond ok\n}\n\n# between\nexample.net {\n}\n# footer\n"
	groups := parseGroups(t, src)
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3: %+v", len(groups), groups)
	}
	want := []struct {
		start, end, lines int
		preview           string
		after             string
	}{
		{1, 2, 2, "header one", "example.test"},
		{7, 7, 1, "between", "example.net"},
		{10, 10, 1, "footer", ""},
	}
	for i, w := range want {
		g := groups[i]
		if g.StartLine != w.start || g.EndLine != w.end || g.Lines != w.lines {
			t.Errorf("group[%d] = lines %d-%d (%d lines), want %d-%d (%d)", i, g.StartLine, g.EndLine, g.Lines, w.start, w.end, w.lines)
		}
		if g.Preview != w.preview {
			t.Errorf("group[%d] preview = %q, want %q", i, g.Preview, w.preview)
		}
		after := ""
		if g.After != nil {
			after = g.After.Name
		}
		if after != w.after {
			t.Errorf("group[%d] after = %q, want %q", i, after, w.after)
		}
	}
	// The byte range must cover exactly the group text.
	if got := groups[0].Range.Text([]byte(src)); got != "# header one\n# header two\n" {
		t.Errorf("group[0] text = %q", got)
	}
	if got := groups[2].Range.Text([]byte(src)); got != "# footer\n" {
		t.Errorf("group[2] text = %q", got)
	}
}

// TestCommentGroups_ExcludesCommentsInsideBlocks verifies that full-line
// comments inside site blocks, nested blocks and global options never
// become groups, while a trailing file comment still does.
func TestCommentGroups_ExcludesCommentsInsideBlocks(t *testing.T) {
	src := "{\n\tlog {\n\t\t# inside nested\n\t}\n\t# inside global\n}\nexample.test {\n\t# inside site\n\trespond ok\n}\n# footer\n"
	groups := parseGroups(t, src)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1 (only the footer): %+v", len(groups), groups)
	}
	if groups[0].StartLine != 11 {
		t.Errorf("group start line = %d, want 11", groups[0].StartLine)
	}
}

// TestCommentGroups_ExcludesBracelessSiteComments verifies that comments
// inside a brace-less site (whose range spans to EOF) are not exposed as
// groups, even though no brace token changes the depth.
func TestCommentGroups_ExcludesBracelessSiteComments(t *testing.T) {
	src := "localhost\n# not a group: inside the brace-less site\nrespond ok\n"
	groups := parseGroups(t, src)
	if len(groups) != 0 {
		t.Fatalf("groups = %d, want 0: %+v", len(groups), groups)
	}
}

// TestCommentGroups_BlankLinesSeparateGroups verifies that a blank line
// between comment runs produces distinct groups.
func TestCommentGroups_BlankLinesSeparateGroups(t *testing.T) {
	src := "# first\n\n# second\nexample.test {\n}\n"
	groups := parseGroups(t, src)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2: %+v", len(groups), groups)
	}
	if groups[0].StartLine != 1 || groups[0].EndLine != 1 {
		t.Errorf("group[0] = lines %d-%d, want 1-1", groups[0].StartLine, groups[0].EndLine)
	}
	if groups[1].StartLine != 3 || groups[1].EndLine != 3 {
		t.Errorf("group[1] = lines %d-%d, want 3-3", groups[1].StartLine, groups[1].EndLine)
	}
}

// TestCommentGroups_TrailingCommentsNotFullLine verifies that comments at
// the end of a directive or header line are never full-line groups.
func TestCommentGroups_TrailingCommentsNotFullLine(t *testing.T) {
	src := "example.test { # header comment\n\trespond ok # trailing\n}\n"
	groups := parseGroups(t, src)
	if len(groups) != 0 {
		t.Fatalf("groups = %d, want 0: %+v", len(groups), groups)
	}
}

// TestCommentGroups_IndentedTopLevelComments verifies that a top-level
// comment with leading whitespace is still a group and its range starts
// at the '#' (preserving the indentation bytes outside the range).
func TestCommentGroups_IndentedTopLevelComments(t *testing.T) {
	src := "    # indented\nexample.test {\n}\n"
	groups := parseGroups(t, src)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1: %+v", len(groups), groups)
	}
	if got := groups[0].Range.Text([]byte(src)); got != "# indented\n" {
		t.Errorf("range text = %q, want %q (leading whitespace preserved outside)", got, "# indented\n")
	}
}

// TestCommentGroups_BOMPreserved verifies that a byte order mark before
// the first comment stays outside the group range.
func TestCommentGroups_BOMPreserved(t *testing.T) {
	src := "\xEF\xBB\xBF# header\nexample.test {\n}\n"
	groups := parseGroups(t, src)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1: %+v", len(groups), groups)
	}
	if groups[0].Range.Start != 3 {
		t.Errorf("range start = %d, want 3 (after the BOM)", groups[0].Range.Start)
	}
	if got := groups[0].Range.Text([]byte(src)); got != "# header\n" {
		t.Errorf("range text = %q, want %q", got, "# header\n")
	}
}

// TestCommentGroups_ParseErrorStillDetects verifies that top-level
// comments on a document with a parse error (an unclosed block) are
// still detected when they fall outside the failing block's range.
func TestCommentGroups_ParseErrorStillDetects(t *testing.T) {
	src := "# header\nexample.test {\n\trespond ok\n"
	doc := Parse([]byte(src))
	if doc.Err == nil {
		t.Fatal("want a parse error for an unclosed block")
	}
	groups := CommentGroups(doc)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1 (the header, before the unclosed block): %+v", len(groups), groups)
	}
	if groups[0].StartLine != 1 {
		t.Errorf("group start line = %d, want 1", groups[0].StartLine)
	}
}

// TestCommentGroups_NilDocument verifies the defensive nil handling.
func TestCommentGroups_NilDocument(t *testing.T) {
	if groups := CommentGroups(nil); len(groups) != 0 {
		t.Fatalf("groups for nil doc = %+v, want none", groups)
	}
}

// TestCommentGroups_NoComments verifies an empty result for a comment-free
// document.
func TestCommentGroups_NoComments(t *testing.T) {
	if groups := parseGroups(t, "example.test {\n\trespond ok\n}\n"); len(groups) != 0 {
		t.Fatalf("groups = %+v, want none", groups)
	}
}

// TestCommentGroups_ScannerHeredoc verifies at the scanner level that a
// '#' line inside a heredoc body is never a comment, while a following
// top-level comment still is.
func TestCommentGroups_ScannerHeredoc(t *testing.T) {
	src := "import sites <<EOF\n# heredoc body, not a comment\nEOF\n# real comment\n"
	spans, err := scanComments([]byte(src))
	if err != nil {
		t.Fatalf("scanComments: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("spans = %+v, want exactly the trailing comment", spans)
	}
	if got := string(src[spans[0].hash:spans[0].lineEnd]); got != "# real comment\n" {
		t.Errorf("span text = %q, want %q", got, "# real comment\n")
	}
}

// TestCommentGroups_ScannerQuotedString verifies that a '#' line inside a
// multi-line quoted string is never a comment.
func TestCommentGroups_ScannerQuotedString(t *testing.T) {
	src := "respond \"first\n# string body, not a comment\nlast\"\n# real comment\n"
	spans, err := scanComments([]byte(src))
	if err != nil {
		t.Fatalf("scanComments: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("spans = %+v, want exactly the trailing comment", spans)
	}
	if got := string(src[spans[0].hash:spans[0].lineEnd]); got != "# real comment\n" {
		t.Errorf("span text = %q, want %q", got, "# real comment\n")
	}
}

// TestCommentGroups_ScannerEscapedNewline verifies that a '#' at the
// start of an escaped-newline continuation line is consumed with the
// token and never becomes a comment.
func TestCommentGroups_ScannerEscapedNewline(t *testing.T) {
	src := "handle /a \\\n# continuation, not a comment\nrespond ok\n"
	spans, err := scanComments([]byte(src))
	if err != nil {
		t.Fatalf("scanComments: %v", err)
	}
	if len(spans) != 0 {
		t.Fatalf("spans = %+v, want none (the # line is a token continuation)", spans)
	}
}

// TestCommentGroups_PreviewTruncates verifies long first lines are
// bounded in the preview.
func TestCommentGroups_PreviewTruncates(t *testing.T) {
	long := strings.Repeat("x", 80)
	src := "# " + long + "\nexample.test {\n}\n"
	groups := parseGroups(t, src)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if len([]rune(groups[0].Preview)) != 40 {
		t.Errorf("preview length = %d, want 40", len([]rune(groups[0].Preview)))
	}
	if !strings.HasSuffix(groups[0].Preview, "...") {
		t.Errorf("preview %q should end with an ellipsis", groups[0].Preview)
	}
}

// TestCommentGroups_BareHashLine verifies a bare '#' line yields an empty
// preview but still forms a group.
func TestCommentGroups_BareHashLine(t *testing.T) {
	groups := parseGroups(t, "#\nexample.test {\n}\n")
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if groups[0].Preview != "" {
		t.Errorf("preview = %q, want empty", groups[0].Preview)
	}
	if groups[0].Lines != 1 {
		t.Errorf("lines = %d, want 1", groups[0].Lines)
	}
}

// TestCommentGroups_RangeMatchesSource verifies the group text is exactly
// the source bytes between Range.Start and Range.End for a multi-line
// group with CRLF line endings.
func TestCommentGroups_RangeMatchesSource(t *testing.T) {
	src := "# one\r\n# two\r\nexample.test {\r\n}\r\n"
	groups := parseGroups(t, src)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1: %+v", len(groups), groups)
	}
	if got := groups[0].Range.Text([]byte(src)); got != "# one\r\n# two\r\n" {
		t.Errorf("range text = %q, want %q", got, "# one\r\n# two\r\n")
	}
	if groups[0].StartLine != 1 || groups[0].EndLine != 2 {
		t.Errorf("group lines = %d-%d, want 1-2", groups[0].StartLine, groups[0].EndLine)
	}
}

// TestCommentGroups_UnclosedBlockFooterExcluded verifies that a comment
// after the header of an unclosed block is inside the block range and is
// never a group, while one before it is.
func TestCommentGroups_UnclosedBlockFooterExcluded(t *testing.T) {
	src := "example.test {\n# inside the unclosed block\n"
	doc := Parse([]byte(src))
	if doc.Err == nil {
		t.Fatal("want a parse error for an unclosed block")
	}
	groups := CommentGroups(doc)
	if len(groups) != 0 {
		t.Fatalf("groups = %+v, want none (inside the unclosed block)", groups)
	}
}

// TestCommentGroups_GroupSummaries verifies the full grouped output of a
// document that mixes top-level, nested and trailing comments, exercising
// the After linkage and source order.
func TestCommentGroups_GroupSummaries(t *testing.T) {
	src := "# a\nexample.test {\n\t# inner\n\trespond ok\n}\n# b\n# c\nexample.net {\n}\n# d\n"
	groups := parseGroups(t, src)
	var got []string
	for _, g := range groups {
		got = append(got, groupSummary(g))
	}
	want := []string{
		"1|1|a|after:example.test",
		"6|7|b|after:example.net",
		"10|10|d|after:nil",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("summaries = %v, want %v", got, want)
	}
}
