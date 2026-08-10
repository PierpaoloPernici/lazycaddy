package caddyfile

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// NodeSpec describes a new structural node for CreateNode. The spec is
// validated before planning: an empty or multi-line name, or a header that
// would lex or classify differently than the requested kind, is rejected
// instead of producing a node that parses as something else.
type NodeSpec struct {
	// Kind is the kind of node to create: a top-level node (KindSite,
	// KindSnippet, KindNamedRoute, KindGlobalOptions) or a handler block
	// (KindDirective with Name route, handle, handle_path or
	// handle_errors).
	Kind Kind
	// Name is the node header: the site address(es) for KindSite, the
	// snippet name for KindSnippet, the named-route name for
	// KindNamedRoute, or the handler directive name for KindDirective.
	// It must be a single line.
	Name string
	// Args is the raw argument text for a handler directive (for example
	// the path matcher of `handle /path`, or the status codes of
	// `handle_errors 404 5xx`), or "" for none. It must be a single line
	// and must not swallow the opening brace.
	Args string
	// Position selects where the node lands: relative to the document for
	// top-level creation, or relative to the parent block's children for
	// nested creation.
	Position InsertPosition
	// Anchor is the sibling used by InsertBefore and InsertAfter. For
	// top-level creation it must be an existing top-level node.
	Anchor Node
}

// CreateNode plans creating a new structural node. A nil parent targets the
// document top level (site, snippet, named route or global options blocks);
// a non-nil parent must be an existing block that can host a handler node
// (a site, snippet, named route, or a route/handle/handle_path/
// handle_errors block).
//
// The generated block is conservative: a single-line header followed by a
// lone closing brace on its own line, indented like the parent's children
// for nested nodes and rendered with the document's dominant line ending.
// Placement reuses the block insertion machinery, so every existing byte —
// comments, blank lines, unknown directives, CRLF and BOM — is preserved;
// only the exact insertion range is new text. The new node is identified by
// the returned edit's DocID, which matches the target document.
func (p *Planner) CreateNode(parent *Node, spec NodeSpec) (*PlannedEdit, error) {
	if p.doc == nil {
		return nil, errors.New("planner: nil document")
	}
	if p.doc.Err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseError, p.doc.Err)
	}
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Args = strings.TrimSpace(spec.Args)
	if err := validateNodeSpec(spec); err != nil {
		return nil, err
	}
	if parent == nil {
		return p.createTopLevel(spec)
	}
	return p.createChild(parent, spec)
}

// validateNodeSpec rejects specs whose header could not be generated safely:
// empty or multi-line headers, names that would lex or classify as a
// different kind, and argument text that would hide the opening brace (for
// example a comment swallowing it).
func validateNodeSpec(spec NodeSpec) error {
	switch spec.Kind {
	case KindGlobalOptions:
		if spec.Name != "" || spec.Args != "" {
			return fmt.Errorf("%w: a global options block has no name or arguments", ErrInvalidContext)
		}
	case KindSite:
		if err := validateHeader(spec.Name); err != nil {
			return err
		}
		toks, err := lex([]byte(spec.Name + " {"))
		if err != nil {
			return fmt.Errorf("%w: site header cannot be lexed: %v", ErrInvalidContext, err)
		}
		if len(toks) < 2 || toks[len(toks)-1].Kind != tokenOpenBrace {
			return fmt.Errorf("%w: site header %q does not admit a trailing opening brace", ErrInvalidContext, spec.Name)
		}
		header := toks[:len(toks)-1]
		for _, t := range header {
			if t.Kind != tokenWord {
				return fmt.Errorf("%w: site header %q contains a structural or quoted token", ErrInvalidContext, spec.Name)
			}
			if strings.HasSuffix(t.Text, "{") {
				return fmt.Errorf("%w: site address %q cannot end with a curly brace; put a space between the token and the brace", ErrInvalidContext, t.Text)
			}
		}
		if kind, _ := classifyTop(header); kind != KindSite {
			return fmt.Errorf("%w: site header %q would parse as a %s block", ErrInvalidContext, spec.Name, kind)
		}
	case KindSnippet, KindNamedRoute:
		if err := validateHeader(spec.Name); err != nil {
			return err
		}
		if spec.Args != "" {
			return fmt.Errorf("%w: %s nodes take no arguments", ErrInvalidContext, spec.Kind)
		}
		header := "(" + spec.Name + ") {"
		if spec.Kind == KindNamedRoute {
			header = "&(" + spec.Name + ") {"
		}
		toks, err := lex([]byte(header))
		if err != nil || len(toks) != 2 || toks[0].Kind != tokenWord || toks[1].Kind != tokenOpenBrace {
			return fmt.Errorf("%w: %s name must be a single word without whitespace or braces", ErrInvalidContext, spec.Kind)
		}
	case KindDirective:
		if !handlerContainers[spec.Name] {
			return fmt.Errorf("%w: cannot create a %q node block", ErrUnsupported, spec.Name)
		}
		if strings.ContainsAny(spec.Args, "\r\n") {
			return fmt.Errorf("%w: handler arguments must be a single line", ErrInvalidContext)
		}
		header := spec.Name
		if spec.Args != "" {
			header += " " + spec.Args
		}
		toks, err := lex([]byte(header + " {"))
		if err != nil {
			return fmt.Errorf("%w: handler header cannot be lexed: %v", ErrInvalidContext, err)
		}
		if len(toks) < 2 || toks[len(toks)-1].Kind != tokenOpenBrace {
			return fmt.Errorf("%w: handler arguments %q would hide the opening brace", ErrInvalidContext, spec.Args)
		}
	default:
		return fmt.Errorf("%w: unknown node kind %v", ErrUnsupported, spec.Kind)
	}
	return nil
}

// validateHeader rejects empty and multi-line node headers.
func validateHeader(s string) error {
	if s == "" {
		return fmt.Errorf("%w: node header must not be empty", ErrInvalidContext)
	}
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("%w: node header must be a single line", ErrInvalidContext)
	}
	return nil
}

// createTopLevel plans a new top-level node. Top-level insertion next to a
// brace-less site is rejected: a brace-less site consumes the rest of the
// file, so no unambiguous top-level point exists.
func (p *Planner) createTopLevel(spec NodeSpec) (*PlannedEdit, error) {
	src := p.doc.Source
	if spec.Kind == KindDirective {
		return nil, fmt.Errorf("%w: handler nodes cannot be created at the document top level", ErrInvalidContext)
	}
	if p.hasBracelessSite() {
		return nil, fmt.Errorf("%w: cannot create a top-level node in a document with a brace-less site", ErrAmbiguous)
	}
	if spec.Kind == KindGlobalOptions {
		if p.hasGlobalOptions() {
			return nil, fmt.Errorf("%w: the document already has a global options block", ErrInvalidContext)
		}
		if len(p.doc.Nodes) > 0 && spec.Position != InsertAtStart {
			return nil, fmt.Errorf("%w: a global options block must be the first block in the file", ErrInvalidContext)
		}
	}
	pos, err := p.topLevelPoint(spec)
	if err != nil {
		return nil, err
	}
	return &PlannedEdit{
		DocID:   p.doc.Path,
		Range:   SourceRange{Start: pos, End: pos},
		NewText: nodeText(spec, "", newlineFor(src, 0, len(src))),
		Op:      EditInsert,
	}, nil
}

// createChild plans a new handler node inside an existing block parent.
func (p *Planner) createChild(parent *Node, spec NodeSpec) (*PlannedEdit, error) {
	located, err := p.locate(*parent)
	if err != nil {
		return nil, err
	}
	if isTopLevelNodeKind(spec.Kind) {
		return nil, fmt.Errorf("%w: %s nodes are top-level only", ErrInvalidContext, spec.Kind)
	}
	pctx, err := p.parentCtx(*located)
	if err != nil {
		return nil, err
	}
	if pctx == ctxGlobal {
		return nil, fmt.Errorf("%w: handler nodes cannot be created inside the global options block", ErrInvalidContext)
	}
	geo, err := p.blockGeometry(*located)
	if err != nil {
		return nil, err
	}
	pos, err := p.insertPoint(*located, spec.Position, spec.Anchor, geo)
	if err != nil {
		return nil, err
	}
	nl := newlineFor(p.doc.Source, located.Range.Start, located.Range.End)
	text := nodeText(spec, geo.childIndent, nl)
	if pos > 0 && p.doc.Source[pos-1] != '\n' && p.doc.Source[pos-1] != '\r' {
		// The insertion point is not at the start of a line (a brace-less
		// site whose address line has no trailing newline): start the new
		// block on its own line instead of gluing it to the previous text.
		text = nl + text
	}
	return &PlannedEdit{DocID: p.doc.Path, Range: SourceRange{Start: pos, End: pos}, NewText: text, Op: EditInsert}, nil
}

// isTopLevelNodeKind reports whether the kind can only be created at the
// document top level.
func isTopLevelNodeKind(k Kind) bool {
	switch k {
	case KindSite, KindSnippet, KindNamedRoute, KindGlobalOptions:
		return true
	}
	return false
}

// topLevelPoint resolves the byte offset where a top-level insertion lands.
// Every resolved point is clamped past a leading byte order mark so the BOM
// keeps its position even though the first node's range starts at line 0.
func (p *Planner) topLevelPoint(spec NodeSpec) (int, error) {
	src := p.doc.Source
	nodes := p.doc.Nodes
	start := docStartOffset(src)
	switch spec.Position {
	case InsertAtStart:
		if len(nodes) == 0 {
			return start, nil
		}
		if nodes[0].Kind == KindGlobalOptions {
			// The global options block must stay first; the new node lands
			// right after it.
			return max(nodes[0].Range.End, start), nil
		}
		return max(nodes[0].Range.Start, start), nil
	case InsertAtEnd:
		if len(nodes) == 0 {
			return start, nil
		}
		return nodes[len(nodes)-1].Range.End, nil
	case InsertBefore, InsertAfter:
		idx := p.topLevelIndex(spec.Anchor)
		if idx < 0 {
			if _, err := p.locate(spec.Anchor); err != nil {
				return -1, err
			}
			return -1, fmt.Errorf("%w: anchor is not a top-level node", ErrInvalidContext)
		}
		if spec.Position == InsertBefore {
			if nodes[idx].Kind == KindGlobalOptions {
				return -1, fmt.Errorf("%w: nothing can be inserted before the global options block", ErrInvalidContext)
			}
			return max(nodes[idx].Range.Start, start), nil
		}
		return nodes[idx].Range.End, nil
	}
	return -1, fmt.Errorf("%w: unknown insertion position %d", ErrAmbiguous, spec.Position)
}

// topLevelIndex returns the index of the top-level node matching anchor's
// identity, or -1.
func (p *Planner) topLevelIndex(anchor Node) int {
	want := nodeIdent(anchor)
	for i, c := range p.doc.Nodes {
		if nodeIdent(c) == want {
			return i
		}
	}
	return -1
}

// hasGlobalOptions reports whether the document has a global options block.
func (p *Planner) hasGlobalOptions() bool {
	for _, n := range p.doc.Nodes {
		if n.Kind == KindGlobalOptions {
			return true
		}
	}
	return false
}

// hasBracelessSite reports whether the document has a brace-less site,
// which consumes the rest of the file after its address line.
func (p *Planner) hasBracelessSite() bool {
	for _, n := range p.doc.Nodes {
		if n.Kind == KindSite && p.isBracelessSite(n) {
			return true
		}
	}
	return false
}

// isBracelessSite reports whether n is a site block without an unquoted
// opening brace in its source range.
func (p *Planner) isBracelessSite(n Node) bool {
	toks, err := lex([]byte(n.Range.Text(p.doc.Source)))
	if err != nil {
		return false
	}
	for _, t := range toks {
		if t.Kind == tokenOpenBrace {
			return false
		}
	}
	return true
}

// docStartOffset returns the first safe insertion offset of a document: 0,
// or right after a leading byte order mark so the BOM keeps its position.
func docStartOffset(src []byte) int {
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		return 3
	}
	return 0
}

// newlineFor returns the dominant line ending of a source region, used to
// render newly generated node text: CRLF when the region contains a CRLF
// sequence, otherwise LF. Existing bytes are never rewritten; the choice
// only affects the generated block.
func newlineFor(src []byte, regionStart, regionEnd int) string {
	if bytes.Contains(src[regionStart:regionEnd], []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// nodeText renders the canonical braced block for a validated spec: a
// single-line header and a lone closing brace on its own line. indent
// prefixes every generated line (the parent's child indentation for nested
// nodes, "" for top-level nodes); nl is the document's line ending.
func nodeText(spec NodeSpec, indent, nl string) string {
	header := spec.Name
	switch spec.Kind {
	case KindSnippet:
		header = "(" + spec.Name + ")"
	case KindNamedRoute:
		header = "&(" + spec.Name + ")"
	case KindGlobalOptions:
		header = ""
	case KindDirective:
		if spec.Args != "" {
			header = spec.Name + " " + spec.Args
		}
	}
	if header == "" {
		return indent + "{" + nl + indent + "}" + nl
	}
	return indent + header + " {" + nl + indent + "}" + nl
}
