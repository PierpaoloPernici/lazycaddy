package caddyfile

import (
	"fmt"
	"strings"
)

// Document is a lossless, parsed view of one Caddyfile source document.
// Source always holds the original bytes; Nodes are projections over it.
type Document struct {
	Source []byte
	Nodes  []Node
	// Err is the first structural or lexing error found, if any. Nodes are
	// still returned so the raw file view stays available and the failing
	// range can be identified.
	Err error
}

// Parse lexes src and builds a lossless node tree. It never drops or rewrites
// source bytes: comments, blank lines, unknown directives, quoted strings and
// heredocs remain inside the byte ranges of their parent nodes.
//
// Recognized structure: a top-level `{ ... }` global options block, top-level
// `(name) { ... }` snippets, top-level `&(name) { ... }` named routes,
// top-level `<addresses> { ... }` site blocks, and directive lines (with or
// without a nested `{ ... }` block) inside any block. A single brace-less
// site (its address line followed by the rest of the file) is grouped into
// one site block, as Caddy does. A top-level line starting with the unquoted
// word `import` is kept as an opaque top-level directive: import resolution
// is a later milestone.
//
// Structural limitations of this milestone, preserved verbatim but not yet
// modeled: environment variable expansion ({$VAR}), the {block}/{blocks.*}
// placeholder substitution for imports with blocks, and the `{}` empty-block
// token. A parse failure leaves the raw view available and identifies the
// failing range in Err.
func Parse(src []byte) *Document {
	d := &Document{Source: src}
	tokens, err := lex(src)
	if err != nil {
		d.Err = err
		return d
	}
	parseTokens(d, tokens)
	return d
}

// lineBound is the byte range of one physical line, with End including the
// trailing newline when present.
type lineBound struct {
	start, end int
}

func lineBounds(src []byte) []lineBound {
	var bounds []lineBound
	start := 0
	for i := 0; i <= len(src); i++ {
		if i == len(src) {
			bounds = append(bounds, lineBound{start: start, end: i})
			break
		}
		if src[i] == '\n' {
			bounds = append(bounds, lineBound{start: start, end: i + 1})
			start = i + 1
		}
	}
	return bounds
}

// lineOf returns the 0-based index of the line containing offset.
func lineOf(bounds []lineBound, offset int) int {
	if len(bounds) == 0 {
		return 0
	}
	lo, hi := 0, len(bounds)-1
	for lo < hi {
		mid := (lo + hi) / 2
		if offset < bounds[mid].end {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo >= len(bounds) {
		lo = len(bounds) - 1
	}
	return lo
}

// spanArgs returns the exact source text of every token in g except the
// first, or "" when g has fewer than two tokens.
func spanArgs(src []byte, g []Token) string {
	if len(g) < 2 {
		return ""
	}
	return string(src[g[1].Start:g[len(g)-1].End])
}

// joinNames joins the token texts of a header with single spaces.
func joinNames(g []Token) string {
	parts := make([]string, len(g))
	for i, t := range g {
		parts[i] = t.Text
	}
	return strings.Join(parts, " ")
}

// classifyTop classifies a top-level block header: a single (name) token is a
// snippet, a single &(name) token is a named route, an import header is kept
// opaque, and anything else is a site address list.
func classifyTop(header []Token) (Kind, string) {
	if len(header) == 1 {
		t := header[0]
		if t.Kind == tokenWord {
			if strings.HasPrefix(t.Text, "(") && strings.HasSuffix(t.Text, ")") {
				return KindSnippet, t.Text[1 : len(t.Text)-1]
			}
			if len(t.Text) >= 3 && strings.HasPrefix(t.Text, "&(") && strings.HasSuffix(t.Text, ")") {
				return KindNamedRoute, t.Text[2 : len(t.Text)-1]
			}
		}
	}
	if len(header) > 0 && header[0].Kind == tokenWord && header[0].Text == "import" {
		return KindDirective, "import"
	}
	return KindSite, joinNames(header)
}

func parseTokens(d *Document, tokens []Token) {
	src := d.Source
	bounds := lineBounds(src)
	lineIdx := func(offset int) int { return lineOf(bounds, offset) }

	setErr := func(format string, args ...any) {
		if d.Err == nil {
			d.Err = fmt.Errorf(format, args...)
		}
	}

	// newLeaf builds a directive node for a full group, spanning from the
	// start of the first token's line to the end of the last token's line.
	newLeaf := func(g []Token) *Node {
		startIdx, endIdx := lineIdx(g[0].Start), lineIdx(g[len(g)-1].End-1)
		return &Node{
			Kind: KindDirective,
			Name: g[0].Text,
			Args: spanArgs(src, g),
			Range: SourceRange{
				Start:     bounds[startIdx].start,
				End:       bounds[endIdx].end,
				StartLine: startIdx + 1,
				EndLine:   endIdx + 1,
			},
		}
	}

	// newBlock opens a block node whose range is filled in when it closes.
	newBlock := func(kind Kind, name, args string, first Token) *Node {
		return &Node{
			Kind:  kind,
			Name:  name,
			Args:  args,
			Range: SourceRange{Start: bounds[lineIdx(first.Start)].start, StartLine: lineIdx(first.Start) + 1},
		}
	}

	var stack []*Node
	var braceLess *Node

	addNode := func(n *Node) {
		if len(stack) > 0 {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, *n)
		} else if braceLess != nil {
			braceLess.Children = append(braceLess.Children, *n)
		} else {
			d.Nodes = append(d.Nodes, *n)
		}
	}

	closeBlock := func(t Token) {
		if len(stack) == 0 {
			setErr("unexpected closing brace on line %d", t.Line)
			return
		}
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		idx := lineIdx(t.Start)
		n.Range.End = bounds[idx].end
		n.Range.EndLine = idx + 1
		addNode(n)
	}

	// processGroup walks one logical line's tokens in order. An unquoted `{`
	// token opens a block with the tokens before it as its header; an
	// unquoted `}` token closes the innermost block. Tokens without braces
	// form a directive line. Processing tokens in order keeps arguments
	// after a heredoc marker and a closing brace on the same line (as in the
	// heredoc example in the official Caddyfile docs) attached to the right
	// blocks.
	processGroup := func(g []Token) {
		tokens := g
		for len(tokens) > 0 {
			first := tokens[0]
			if first.Kind == tokenCloseBrace {
				closeBlock(first)
				tokens = tokens[1:]
				continue
			}
			end := 0
			for end < len(tokens) && tokens[end].Kind != tokenOpenBrace && tokens[end].Kind != tokenCloseBrace {
				end++
			}
			header := tokens[:end]
			top := len(stack) == 0 && braceLess == nil
			if end < len(tokens) && tokens[end].Kind == tokenOpenBrace {
				if top {
					var kind Kind
					var name string
					if len(header) == 0 {
						kind = KindGlobalOptions
					} else {
						kind, name = classifyTop(header)
					}
					stack = append(stack, newBlock(kind, name, spanArgs(src, header), first))
				} else if len(header) == 0 {
					stack = append(stack, newBlock(KindDirective, "", "", first))
				} else {
					stack = append(stack, newBlock(KindDirective, header[0].Text, spanArgs(src, header), first))
				}
				tokens = tokens[end+1:]
				continue
			}
			if len(header) == 0 {
				tokens = tokens[end:]
				continue
			}
			if top {
				if header[0].Kind == tokenWord && header[0].Text == "import" {
					addNode(newLeaf(header))
					tokens = tokens[end:]
					continue
				}
				// A non-import line without a block opener starts the single
				// brace-less site, which consumes the rest of the file.
				if header[0].Kind == tokenWord && strings.HasSuffix(header[0].Text, "{") {
					setErr("site addresses cannot end with a curly brace: %q on line %d; put a space between the token and the brace", header[0].Text, first.Line)
				}
				braceLess = newBlock(KindSite, joinNames(header), "", first)
				if len(header) > 1 {
					// Rare single-line form: remaining tokens on the address
					// line are the site's first directives.
					addNode(newLeaf(header[1:]))
				}
				tokens = tokens[end:]
				continue
			}
			addNode(newLeaf(header))
			tokens = tokens[end:]
		}
	}

	for _, g := range groupLines(tokens) {
		processGroup(g)
	}

	// Close any blocks left open at EOF so the raw view remains usable and
	// the failing range is identifiable.
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		idx := lineIdx(len(src))
		n.Range.End = len(src)
		n.Range.EndLine = idx + 1
		addNode(n)
		if d.Err == nil {
			d.Err = fmt.Errorf("unclosed %s block %q starting at line %d", n.Kind, n.Name, n.Range.StartLine)
		}
	}
	if braceLess != nil {
		idx := lineIdx(len(src))
		braceLess.Range.End = len(src)
		braceLess.Range.EndLine = idx + 1
		d.Nodes = append(d.Nodes, *braceLess)
	}
}
