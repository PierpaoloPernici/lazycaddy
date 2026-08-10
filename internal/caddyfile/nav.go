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
					Kind:      n.Kind,
					Name:      n.Name,
					Range:     n.Range,
					StartLine: n.Range.StartLine,
					EndLine:   n.Range.EndLine,
					Depth:     depth,
					Foldable:  true,
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
