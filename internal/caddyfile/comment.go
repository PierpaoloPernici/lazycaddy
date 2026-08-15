package caddyfile

import "strings"

// CommentGroup describes one contiguous run of full-line comments at the
// top level of a document (brace depth 0 and outside every top-level node
// range). Comment groups are source annotations, not parser nodes: they
// never appear in the Node tree and never affect parsing, folding,
// deletion or reordering. The exact byte range is the only identity an
// edit needs, and an edit that replaces it preserves every byte outside.
type CommentGroup struct {
	// Range covers the group from the '#' that starts its first line to
	// the end of its last line, trailing newline included. Leading
	// whitespace of the first line and every byte outside the group stay
	// outside the range, so replacing the group never rewrites unrelated
	// source (including a byte order mark that precedes it).
	Range SourceRange
	// StartLine and EndLine are the 1-based line numbers of the group.
	StartLine, EndLine int
	// Lines is the number of comment lines in the group.
	Lines int
	// Text is the exact source bytes of the group.
	Text string
	// Preview is a short single-line rendering of the first comment, for
	// tree labels and pickers. It is empty for a bare "#" line.
	Preview string
	// After is the top-level node immediately following the group, or nil
	// when the group is the last annotation in the document. It is
	// advisory context ("documents handle /a") and never affects edits.
	After *Node
}

// commentSpan is one full-line comment with its exact byte offsets.
type commentSpan struct {
	hash      int // offset of the '#' that starts the comment
	lineStart int // offset of the first byte of the physical line
	lineEnd   int // offset one past the line, trailing newline included
}

// scanComments mirrors the top-level lexer loop and records every
// full-line comment: a physical line whose first non-whitespace byte is
// '#'. Token lexing is delegated to lexToken, so a '#' inside a quoted
// string, a heredoc body or an escaped-newline continuation is consumed
// with its token and never reaches this loop, exactly as the lexer never
// treats it as a comment. Logical line, column and skipped-newline
// accounting mirror lex so multi-line tokens advance the state
// identically.
func scanComments(src []byte) ([]commentSpan, error) {
	var spans []commentSpan
	i := 0
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		i = 3 // skip byte order mark without shifting offsets
	}
	line, col, skipped := 1, 0, 0
	lineStart := 0
	atLineStart := true
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\r':
			i++
			col++
		case '\n':
			line += 1 + skipped
			skipped = 0
			col = 0
			i++
			lineStart = i
			atLineStart = true
		case '#':
			start := i
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if atLineStart {
				end := i
				if i < len(src) {
					end++ // include the trailing newline
				}
				spans = append(spans, commentSpan{hash: start, lineStart: lineStart, lineEnd: end})
			}
			// The comment runs to the end of its line; the loop continues
			// with the newline case above.
		default:
			atLineStart = false
			if _, err := lexToken(src, &i, &line, &skipped, &col, ""); err != nil {
				return spans, err
			}
		}
	}
	return spans, nil
}

// braceDepthAtLineStart returns, for every 1-based line, the brace depth
// at the start of that line: the number of unclosed '{' brace tokens on
// earlier lines minus the closing ones. Brace tokens are always
// single-line, so a per-line count prefix-summed over lines yields the
// exact depth at each line start. The last element of the result holds
// the depth after the final token line, for lines beyond it.
func braceDepthAtLineStart(tokens []Token) []int {
	perLine := make(map[int]int)
	maxLine := 0
	for _, t := range tokens {
		switch t.Kind {
		case tokenOpenBrace:
			perLine[t.Line]++
		case tokenCloseBrace:
			perLine[t.Line]--
		}
		if t.Line > maxLine {
			maxLine = t.Line
		}
	}
	depth := make([]int, maxLine+2)
	d := 0
	for line := 1; line <= maxLine+1; line++ {
		depth[line] = d
		d += perLine[line]
	}
	return depth
}

// depthAtLineStart returns the brace depth at the start of a 1-based
// line, clamping lines beyond the last token line to the final depth.
func depthAtLineStart(line int, depth []int) int {
	if line >= len(depth) {
		return depth[len(depth)-1]
	}
	return depth[line]
}

// commentPreview renders the first comment line of a group for tree
// labels: the '#' marker and surrounding whitespace are stripped and the
// result is truncated to a bounded width.
func commentPreview(firstLine []byte) string {
	line := strings.TrimSpace(string(firstLine))
	line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
	if line == "" {
		return ""
	}
	runes := []rune(line)
	const maxPreviewRunes = 40
	if len(runes) > maxPreviewRunes {
		runes = runes[:maxPreviewRunes-3]
		return string(runes) + "..."
	}
	return string(runes)
}

// CommentGroups returns the top-level full-line comment groups of a
// document in source order. A group is a maximal run of consecutive
// full-line comments whose lines sit at brace depth 0 and outside every
// top-level node range, so comments inside a block, inside a brace-less
// site or inside an unclosed block on a partial document are never
// exposed as groups. The result is advisory: groups are source
// annotations with exact ranges and never affect parsing. When the
// document cannot be lexed no groups are returned.
func CommentGroups(doc *Document) []CommentGroup {
	if doc == nil {
		return nil
	}
	spans, err := scanComments(doc.Source)
	if err != nil || len(spans) == 0 {
		return nil
	}
	tokens, err := lex(doc.Source)
	if err != nil {
		return nil
	}
	depth := braceDepthAtLineStart(tokens)
	bounds := lineBounds(doc.Source)
	lineOf := func(offset int) int { return lineOf(bounds, offset) }
	outsideAllNodes := func(start, end int) bool {
		for i := range doc.Nodes {
			n := &doc.Nodes[i]
			if end <= n.Range.Start || start >= n.Range.End {
				continue
			}
			return false
		}
		return true
	}

	var groups []CommentGroup
	var cur []commentSpan
	flush := func() {
		if len(cur) == 0 {
			return
		}
		first, last := cur[0], cur[len(cur)-1]
		startLine := lineOf(first.hash) + 1
		endLine := lineOf(last.lineEnd-1) + 1
		groups = append(groups, CommentGroup{
			Range:     SourceRange{Start: first.hash, End: last.lineEnd, StartLine: startLine, EndLine: endLine},
			StartLine: startLine,
			EndLine:   endLine,
			Lines:     len(cur),
			Text:      string(doc.Source[first.hash:last.lineEnd]),
			Preview:   commentPreview(doc.Source[first.hash : first.lineEnd-1]),
		})
		cur = nil
	}
	for _, sp := range spans {
		line := lineOf(sp.hash) + 1 // 1-based
		if depthAtLineStart(line, depth) != 0 || !outsideAllNodes(sp.hash, sp.lineEnd) {
			flush()
			continue
		}
		if len(cur) > 0 && line != lineOf(cur[len(cur)-1].hash)+2 {
			flush()
		}
		cur = append(cur, sp)
	}
	flush()

	for i := range groups {
		g := &groups[i]
		for j := range doc.Nodes {
			n := &doc.Nodes[j]
			if n.Range.Start >= g.Range.End {
				g.After = n
				break
			}
		}
	}
	return groups
}
