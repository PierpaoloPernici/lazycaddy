package caddyfile

import "strconv"

// Fold describes one foldable range in a document. Folds are derived from
// the parse tree's source ranges, so folding never rewrites bytes and the
// ranges stay valid for patching.
type Fold struct {
	// Kind and Name mirror the folded node for stable selection.
	Kind Kind
	Name string
	// Range is the exact byte range of the folded block.
	Range SourceRange
	// StartLine and EndLine are the 1-based fold boundaries (the block's
	// line span).
	StartLine, EndLine int
	// CloseBraceLine is the 1-based line of the block's closing brace, or
	// 0 for a brace-less site or an unclosed (partially parsed) block. A
	// collapsed fold keeps this line visible so the block boundary always
	// reads.
	CloseBraceLine int
	// Depth is the nesting depth: top-level blocks are 0.
	Depth int
	// Foldable is always true for the blocks Folds returns; leaf
	// directives are never emitted.
	Foldable bool
}

// Folds returns the foldable ranges of every block node in the document:
// site blocks, snippets, named routes, the global options block and nested
// handler/directive blocks. Partially parsed files remain navigable: the
// node tree is walked even when the document carries a parse error, and
// unknown directives are opaque leaves that never fold. A fold's Range can
// be handed to Patch directly.
func Folds(doc *Document) []Fold {
	src := doc.Source
	var out []Fold
	var walk func(ns []Node, depth int)
	walk = func(ns []Node, depth int) {
		for _, n := range ns {
			if isBlockNode(src, n) {
				out = append(out, Fold{
					Kind:           n.Kind,
					Name:           n.Name,
					Range:          n.Range,
					StartLine:      n.Range.StartLine,
					EndLine:        n.Range.EndLine,
					CloseBraceLine: blockCloseBraceLine(src, n),
					Depth:          depth,
					Foldable:       true,
				})
				walk(n.Children, depth+1)
				continue
			}
			walk(n.Children, depth+1)
		}
	}
	walk(doc.Nodes, 0)
	return out
}

// isBlockNode reports whether a node is a structural block (as opposed to a
// leaf directive). Quoted braces are lexed as string tokens, so they never
// turn a leaf into a block.
func isBlockNode(src []byte, n Node) bool {
	switch n.Kind {
	case KindSite, KindSnippet, KindNamedRoute, KindGlobalOptions:
		return true
	case KindDirective:
		if len(n.Children) > 0 {
			return true
		}
		toks, err := lex([]byte(n.Range.Text(src)))
		if err != nil {
			return false
		}
		for _, t := range toks {
			if t.Kind == tokenOpenBrace {
				return true
			}
		}
	}
	return false
}

// blockCloseBraceLine returns the 1-based line of the last closing brace
// token inside n's range, or 0 when the range has none: a brace-less site,
// an unclosed block, or an un-lexable directive. Quoted braces are string
// tokens, so they never count.
func blockCloseBraceLine(src []byte, n Node) int {
	toks, err := lex([]byte(n.Range.Text(src)))
	if err != nil {
		return 0
	}
	bounds := lineBounds(src)
	line := 0
	for _, t := range toks {
		if t.Kind == tokenCloseBrace {
			line = lineOf(bounds, n.Range.Start+t.Start) + 1
		}
	}
	return line
}

// FoldRange is one collapsed fold in a folded source view: the structural
// block replaced by a single indicator row. It is the display counterpart
// of Fold: the UI derives it from Folds plus CloseBraceLine and hands it to
// FoldLayoutFor, which never rewrites the source.
type FoldRange struct {
	// Kind and Name mirror the folded block for stable selection.
	Kind Kind
	Name string
	// Range is the exact source range of the folded block.
	Range SourceRange
	// StartLine and EndLine are the inclusive 1-based block boundaries.
	StartLine, EndLine int
	// CloseBraceLine is the 1-based line of the closing brace, or 0 for
	// a brace-less or unclosed block.
	CloseBraceLine int
	// Hidden is the number of source lines the indicator row replaces.
	Hidden int
}

// FoldHidden returns the number of source lines a collapsed fold replaces
// with its indicator row: every line strictly between the header and the
// closing brace, or every line after the header for a brace-less or
// unclosed block. A block with nothing hidden (a single-line block, or a
// header immediately followed by its closing brace) is never foldable.
func FoldHidden(f FoldRange) int {
	if f.CloseBraceLine > f.StartLine {
		return f.CloseBraceLine - f.StartLine - 1
	}
	return f.EndLine - f.StartLine
}

// FoldLayout maps the display rows of a folded source view back to the
// source lines they show. It is the presentation contract between the
// parser and the source pane: rendering, scroll reveal, search/diagnostic
// jumps and mouse selection all translate through it, while the underlying
// source stays byte-exact and lossless.
type FoldLayout struct {
	// Rows[i] is the 1-based source line displayed by row i, or 0 when
	// row i is a fold indicator row.
	Rows []int
	// LineRow is indexed by 1-based source line; LineRow[line] is the
	// display row of that line, or -1 when the line is hidden by a
	// collapsed fold. LineRow[0] is always -1; entries beyond the last
	// source line are -1 too, so out-of-range lookups never wrap.
	LineRow []int
	// FoldAt[i] is the index into Folds of the fold collapsed by display
	// row i, or -1 when row i shows a source line.
	FoldAt []int
	// Folds lists the collapsed folds in display order (one indicator row
	// each).
	Folds []FoldRange
}

// FoldLayoutFor builds the folded display layout of src with the given
// collapsed folds. Folds with nothing hidden are ignored (a single-line
// block or a header followed immediately by its closing brace cannot be
// collapsed usefully), so is the last fold when two active folds share a
// header line (defensive: Folds never produces that). A collapsed fold
// keeps its header and, when present, its closing brace line visible; the
// indicator row replaces the lines strictly between them. Brace-less and
// unclosed blocks replace every line after the header. For an unclosed
// block the closest closing brace token inside its range (the deepest
// unclosed child's brace) anchors the visible tail line, so the folded
// view always keeps the block's last physical line readable. Partially
// parsed files stay navigable. It returns nil when no fold hides any line.
// The trailing empty line of a source ending in a newline is part of the
// layout (mirroring strings.Split), so the folded and unfolded views never
// disagree on row counts.
func FoldLayoutFor(src []byte, folds []FoldRange) *FoldLayout {
	lineCount := len(lineBounds(src))
	byStart := map[int]int{}
	norm := make([]FoldRange, 0, len(folds))
	for _, f := range folds {
		f.Hidden = FoldHidden(f)
		if f.Hidden <= 0 {
			continue
		}
		byStart[f.StartLine] = len(norm)
		norm = append(norm, f)
	}
	if len(norm) == 0 {
		return nil
	}
	rows := make([]int, 0, lineCount)
	foldAt := make([]int, 0, lineCount)
	displayFolds := make([]FoldRange, 0, len(norm))
	line := 1
	for line <= lineCount {
		if idx, ok := byStart[line]; ok {
			f := norm[idx]
			// Header row, then the indicator row that replaces the hidden
			// lines. displayFolds keeps only the folds that actually get an
			// indicator row: an inner fold subsumed by a collapsed parent
			// stays active underneath but never renders.
			rows = append(rows, f.StartLine)
			foldAt = append(foldAt, -1)
			rows = append(rows, 0)
			foldAt = append(foldAt, len(displayFolds))
			displayFolds = append(displayFolds, f)
			if f.CloseBraceLine > f.StartLine {
				// Keep the closing brace visible; the next iteration
				// renders it as a plain source line.
				line = f.CloseBraceLine
			} else {
				// Brace-less or unclosed: the block ends here.
				line = f.EndLine + 1
			}
			continue
		}
		rows = append(rows, line)
		foldAt = append(foldAt, -1)
		line++
	}
	lineRow := make([]int, lineCount+1)
	for i := range lineRow {
		lineRow[i] = -1
	}
	for r, ln := range rows {
		if ln > 0 && ln <= lineCount {
			lineRow[ln] = r
		}
	}
	return &FoldLayout{Rows: rows, LineRow: lineRow, FoldAt: foldAt, Folds: displayFolds}
}

// Landmarks describes the byte and line positions a user can jump to while
// navigating a block: its header line, the opening and closing braces, its
// first and last child lines, and the indentation used by the block and its
// children.
type Landmarks struct {
	// HeaderLine is the 1-based line of the block's first token.
	HeaderLine int
	// OpenBraceLine is the 1-based line of the opening brace, or 0 for a
	// brace-less site.
	OpenBraceLine int
	// CloseBraceLine is the 1-based line of the closing brace, or 0 when
	// the block is unclosed (partially parsed input).
	CloseBraceLine int
	// FirstChildLine and LastChildLine are the 1-based lines of the
	// block's first and last child, or 0 when the block has no children.
	FirstChildLine, LastChildLine int
	// Indent is the block's own leading indentation.
	Indent string
	// ChildIndent is the indentation used by the block's children, used
	// for brace-aware movement and insertion previews.
	ChildIndent string
}

// LandmarksOf computes navigation landmarks for a block node. It degrades
// gracefully on partially parsed files: missing braces or children report 0
// and the remaining fields stay usable.
func LandmarksOf(src []byte, n Node) Landmarks {
	l := Landmarks{HeaderLine: n.Range.StartLine, Indent: leadingIndent(src, n.Range.Start)}
	bounds := lineBounds(src)
	lineAt := func(off int) int {
		return lineOf(bounds, off) + 1
	}
	closeAbs := -1
	toks, err := lex([]byte(n.Range.Text(src)))
	if err == nil {
		for _, t := range toks {
			abs := n.Range.Start + t.Start
			if t.Kind == tokenOpenBrace && l.OpenBraceLine == 0 {
				l.OpenBraceLine = lineAt(abs)
			}
			if t.Kind == tokenCloseBrace {
				l.CloseBraceLine = lineAt(abs)
				closeAbs = abs
			}
		}
	}
	if len(n.Children) > 0 {
		l.FirstChildLine = n.Children[0].Range.StartLine
		l.LastChildLine = n.Children[len(n.Children)-1].Range.EndLine
		l.ChildIndent = leadingIndent(src, n.Children[0].Range.Start)
	} else if closeAbs >= 0 {
		l.ChildIndent = leadingIndent(src, closeAbs) + "\t"
	} else {
		l.ChildIndent = l.Indent + "\t"
	}
	return l
}

// MatcherRef is one named matcher occurrence: a definition or a reference.
type MatcherRef struct {
	// Name is the matcher name without the leading '@'.
	Name string
	// Node is the directive node containing the occurrence.
	Node Node
	// Definition is true when the line defines the matcher (an @name
	// directive such as `@api path /api/*`), false when it references it.
	Definition bool
	// Start and End locate the @name token in the source.
	Start, End int
}

// Matchers returns every named matcher definition and reference in the
// document with its exact byte span, so navigation can jump between a
// definition and its references. Definition lines are @name directives;
// every other @name token is a reference. Partially parsed files still
// report the occurrences found so far, and unknown directives are never
// modified or hidden.
func Matchers(doc *Document) []MatcherRef {
	src := doc.Source
	defSpans := map[string]bool{}
	key := func(a, b int) string {
		return strconv.Itoa(a) + ":" + strconv.Itoa(b)
	}
	var refs []MatcherRef

	var walk func(ns []Node)
	walk = func(ns []Node) {
		for _, n := range ns {
			if n.Kind != KindDirective || n.Name == "" {
				walk(n.Children)
				continue
			}
			raw := n.Range.Text(src)
			toks, err := lex([]byte(raw))
			if err != nil || len(toks) == 0 {
				walk(n.Children)
				continue
			}
			for _, t := range toks {
				if t.Kind != tokenWord || !isMatcherName(t.Text) {
					continue
				}
				absStart := n.Range.Start + t.Start
				absEnd := n.Range.Start + t.End
				isDef := absStart == n.Range.Start+toks[0].Start && n.Name == t.Text
				if isDef {
					defSpans[key(absStart, absEnd)] = true
				}
			}
			walk(n.Children)
		}
	}
	walk(doc.Nodes)

	// Second pass: every @name token that is not a definition is a
	// reference.
	var walkRefs func(ns []Node)
	walkRefs = func(ns []Node) {
		for _, n := range ns {
			if n.Kind == KindDirective && n.Name != "" {
				raw := n.Range.Text(src)
				toks, err := lex([]byte(raw))
				if err == nil {
					for _, t := range toks {
						if t.Kind != tokenWord || !isMatcherName(t.Text) {
							continue
						}
						absStart := n.Range.Start + t.Start
						absEnd := n.Range.Start + t.End
						if defSpans[key(absStart, absEnd)] {
							refs = append(refs, MatcherRef{
								Name: t.Text[1:], Node: n, Definition: true,
								Start: absStart, End: absEnd,
							})
							continue
						}
						refs = append(refs, MatcherRef{
							Name: t.Text[1:], Node: n,
							Start: absStart, End: absEnd,
						})
					}
				}
			}
			walkRefs(n.Children)
		}
	}
	walkRefs(doc.Nodes)
	return refs
}

// isMatcherName reports whether a word is a named matcher token (@name).
func isMatcherName(word string) bool {
	return len(word) > 1 && word[0] == '@' && word != "@"
}

// NodeKey returns a stable identity string for a node: kind, name and exact
// source range. Ranges derive from the source, so the key survives tree
// rebuilds after saves, reloads and rollbacks, and selections keyed by it
// stay anchored.
func NodeKey(n Node) string {
	return nodeIdent(n)
}
