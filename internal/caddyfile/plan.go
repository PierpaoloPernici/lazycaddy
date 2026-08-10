package caddyfile

import (
	"errors"
	"fmt"
)

// Sentinel errors reported by the structured edit planner. Every operation
// returns an explicit result: a planned edit, or one of these errors
// explaining why the edit is unsafe, unsupported or ambiguous. The planner
// never guesses.
var (
	// ErrParseError reports that the document has a parse or lexing error,
	// so structure-based edits are unsafe.
	ErrParseError = errors.New("document has a parse error; structured edits are unavailable")
	// ErrNodeNotFound reports that the given node is not present in the
	// document tree (stale selection or a foreign document).
	ErrNodeNotFound = errors.New("node not found in document")
	// ErrUnsupported reports an operation on a directive the planner does
	// not know how to handle.
	ErrUnsupported = errors.New("unsupported directive for structured editing")
	// ErrInvalidContext reports an operation whose context is invalid:
	// wrong node kind, wrong block, a leaf as insertion target, or
	// non-sibling reorder targets.
	ErrInvalidContext = errors.New("invalid context for structured edit")
	// ErrAmbiguous reports that the planner cannot determine a safe edit
	// target without rewriting bytes it must not touch.
	ErrAmbiguous = errors.New("ambiguous structured edit target")
)

// EditOp classifies the operation that produced a PlannedEdit.
type EditOp int

const (
	// EditSetValue replaces a directive's argument list or one argument.
	EditSetValue EditOp = iota
	// EditInsert inserts a new supported directive line into a block.
	EditInsert
	// EditDelete removes a construct's exact source range.
	EditDelete
	// EditReorder swaps two sibling constructs.
	EditReorder
)

func (o EditOp) String() string {
	switch o {
	case EditSetValue:
		return "set-value"
	case EditInsert:
		return "insert"
	case EditDelete:
		return "delete"
	case EditReorder:
		return "reorder"
	default:
		return "unknown"
	}
}

// PlannedEdit is one byte-range replacement against a single source
// document. It is UI-independent and directly consumable by the existing
// diff/save workflow: applying it is a single Patch call, so every byte
// outside Range is preserved verbatim.
type PlannedEdit struct {
	// DocID identifies the source document the edit belongs to. It matches
	// Document.Path when known; "" means the root document.
	DocID string
	// Range is the exact byte range to replace.
	Range SourceRange
	// NewText is the replacement text.
	NewText string
	// Op describes the operation that produced the edit.
	Op EditOp
}

// Apply produces the new document bytes for a single edit.
func (e PlannedEdit) Apply(src []byte) ([]byte, error) {
	return Patch(src, e.Range, []byte(e.NewText))
}

// Plan is an ordered set of edits against one document. Edits must belong
// to the same document, be sorted by position and not overlap.
type Plan []PlannedEdit

// ApplyAll applies the plan to src and returns the new document bytes.
// Edits are applied from last to first so every range stays valid against
// the original offsets; overlapping or unsorted edits are rejected.
func (p Plan) ApplyAll(src []byte) ([]byte, error) {
	out := append([]byte(nil), src...)
	prevEnd := -1
	for _, e := range p {
		if e.Range.Start < prevEnd {
			return nil, fmt.Errorf("plan: edits overlap or are out of order: %+v", e.Range)
		}
		prevEnd = e.Range.End
	}
	for i := len(p) - 1; i >= 0; i-- {
		var err error
		out, err = p[i].Apply(out)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Planner plans byte-preserving structured edits against one parsed
// document. It never renders, never writes files and never runs commands.
// Operations target nodes by identity (kind + name + exact source range),
// so stale copies or nodes from another document are rejected instead of
// being applied to the wrong bytes.
type Planner struct {
	doc *Document
}

// NewPlanner returns a planner for a single document. The planner keeps a
// reference to the parsed tree; callers must not mutate doc.Source between
// planning and applying.
func NewPlanner(doc *Document) *Planner {
	return &Planner{doc: doc}
}

// nodeIdent returns a stable identity for a node based on its kind, name
// and exact source range. Ranges derive from the source, so the identity
// survives tree rebuilds after saves and reloads.
func nodeIdent(n Node) string {
	return fmt.Sprintf("%d\x00%s\x00%d\x00%d", n.Kind, n.Name, n.Range.Start, n.Range.End)
}

// locate finds the node matching the identity of n in the document tree.
func (p *Planner) locate(n Node) (*Node, error) {
	if p.doc == nil {
		return nil, errors.New("planner: nil document")
	}
	if p.doc.Err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseError, p.doc.Err)
	}
	want := nodeIdent(n)
	var found *Node
	walkNodes(p.doc.Nodes, func(cand Node) {
		if found == nil && nodeIdent(cand) == want {
			c := cand
			found = &c
		}
	})
	if found == nil {
		return nil, fmt.Errorf("%w: no node %q with range [%d:%d)", ErrNodeNotFound, n.Name, n.Range.Start, n.Range.End)
	}
	return found, nil
}

// headerTokens lexes the raw text of a directive node's range and returns
// the tokens with byte offsets relative to the node's range start, plus the
// index of the first block-opening brace token (-1 when the directive has
// no nested block).
func (p *Planner) headerTokens(n Node) (toks []Token, openBrace int, err error) {
	raw := n.Range.Text(p.doc.Source)
	toks, err = lex([]byte(raw))
	if err != nil {
		return nil, -1, fmt.Errorf("%w: directive %q cannot be lexed: %v", ErrUnsupported, n.Name, err)
	}
	if len(toks) == 0 || toks[0].Kind == tokenOpenBrace || toks[0].Kind == tokenCloseBrace {
		return nil, -1, fmt.Errorf("%w: node %q has no directive name", ErrInvalidContext, n.Name)
	}
	openBrace = -1
	for i, t := range toks {
		if t.Kind == tokenOpenBrace {
			openBrace = i
			break
		}
	}
	return toks, openBrace, nil
}

// argsSpan computes the absolute byte range covering a directive's argument
// list: everything between the name token and the last argument token for a
// leaf, or the block opener for a block directive. The second result
// reports whether the directive currently has any argument.
func (p *Planner) argsSpan(n Node, toks []Token, openBrace int) (SourceRange, bool) {
	src := p.doc.Source
	if openBrace >= 0 {
		return SourceRange{
			Start: n.Range.Start + toks[0].End,
			End:   n.Range.Start + toks[openBrace].Start,
		}, openBrace > 1
	}
	if len(toks) > 1 {
		return SourceRange{
			Start: n.Range.Start + toks[0].End,
			End:   n.Range.Start + toks[len(toks)-1].End,
		}, true
	}
	// No arguments: extend the range over any trailing whitespace so an
	// empty value collapses the line cleanly.
	end := n.Range.End
	if end > n.Range.Start && src[end-1] == '\n' {
		end--
	}
	start := n.Range.Start + toks[0].End
	for end > start && (src[end-1] == ' ' || src[end-1] == '\t') {
		end--
	}
	return SourceRange{Start: start, End: end}, false
}

// SetArgs plans replacing the entire argument list of a directive node.
// newArgs is the raw argument text without leading or trailing whitespace;
// the planner restores the single separating space (and, for block
// directives, the space before the opening brace). An empty newArgs removes
// the arguments. Comments and unknown directives elsewhere in the document
// are never touched.
func (p *Planner) SetArgs(n Node, newArgs string) (*PlannedEdit, error) {
	located, err := p.locate(n)
	if err != nil {
		return nil, err
	}
	if located.Kind != KindDirective {
		return nil, fmt.Errorf("%w: node %q is not a directive", ErrInvalidContext, located.Name)
	}
	toks, ob, err := p.headerTokens(*located)
	if err != nil {
		return nil, err
	}
	span, _ := p.argsSpan(*located, toks, ob)
	var text string
	switch {
	case ob >= 0 && newArgs == "":
		text = " " // keep the single space before the opening brace
	case ob >= 0:
		text = " " + newArgs + " "
	case newArgs == "":
		text = ""
	default:
		text = " " + newArgs
	}
	return &PlannedEdit{DocID: p.doc.Path, Range: span, NewText: text, Op: EditSetValue}, nil
}

// SetArg plans replacing a single argument (0-based) of a directive node.
// newValue is the raw replacement text for that argument token, including
// any enclosing quotes when the original argument is quoted.
func (p *Planner) SetArg(n Node, index int, newValue string) (*PlannedEdit, error) {
	located, err := p.locate(n)
	if err != nil {
		return nil, err
	}
	if located.Kind != KindDirective {
		return nil, fmt.Errorf("%w: node %q is not a directive", ErrInvalidContext, located.Name)
	}
	toks, ob, err := p.headerTokens(*located)
	if err != nil {
		return nil, err
	}
	args := toks[1:]
	if ob >= 0 {
		args = toks[1:ob]
	}
	if index < 0 || index >= len(args) {
		return nil, fmt.Errorf("%w: directive %q has %d arguments, cannot replace argument %d", ErrInvalidContext, located.Name, len(args), index)
	}
	rel := args[index]
	return &PlannedEdit{
		DocID:   p.doc.Path,
		Range:   SourceRange{Start: located.Range.Start + rel.Start, End: located.Range.Start + rel.End},
		NewText: newValue,
		Op:      EditSetValue,
	}, nil
}

// insertCtx is the bitmask of block kinds a directive may be inserted into.
type insertCtx uint8

const (
	ctxSite insertCtx = 1 << iota
	ctxSnippet
	ctxNamedRoute
	ctxGlobal
	ctxHandler // route / handle / handle_path / handle_errors blocks
)

// handlerContainers are directive blocks that accept handler directives.
var handlerContainers = map[string]bool{
	"route":         true,
	"handle":        true,
	"handle_path":   true,
	"handle_errors": true,
}

// insertSpecs lists the directives the planner can insert and the block
// kinds that accept them. Conservative by design: tls is site-level only,
// log is site/global-level only, and handler directives nest only inside
// handler containers.
var insertSpecs = map[string]struct {
	ctx insertCtx
}{
	"reverse_proxy": {ctxSite | ctxSnippet | ctxNamedRoute | ctxHandler},
	"encode":        {ctxSite | ctxSnippet | ctxNamedRoute | ctxHandler},
	"file_server":   {ctxSite | ctxSnippet | ctxNamedRoute | ctxHandler},
	"php_fastcgi":   {ctxSite | ctxSnippet | ctxNamedRoute | ctxHandler},
	"header":        {ctxSite | ctxSnippet | ctxNamedRoute | ctxHandler},
	"redir":         {ctxSite | ctxSnippet | ctxNamedRoute | ctxHandler},
	"respond":       {ctxSite | ctxSnippet | ctxNamedRoute | ctxHandler},
	"tls":           {ctxSite | ctxSnippet},
	"log":           {ctxSite | ctxSnippet | ctxGlobal},
}

// parentCtx classifies the block kind of a node as an insertion context.
func (p *Planner) parentCtx(n Node) (insertCtx, error) {
	switch n.Kind {
	case KindSite:
		return ctxSite, nil
	case KindSnippet:
		return ctxSnippet, nil
	case KindNamedRoute:
		return ctxNamedRoute, nil
	case KindGlobalOptions:
		return ctxGlobal, nil
	case KindDirective:
		if handlerContainers[n.Name] {
			return ctxHandler, nil
		}
		return 0, fmt.Errorf("%w: %q is not a block that accepts inserted directives", ErrInvalidContext, n.Name)
	default:
		return 0, fmt.Errorf("%w: unsupported parent kind %v", ErrInvalidContext, n.Kind)
	}
}

// blockGeometry describes where insertions may land inside a block node.
type blockGeometry struct {
	interiorStart   int    // first byte of the first potential child line
	interiorEnd     int    // byte offset of the closing brace
	childIndent     string // indentation for inserted lines
	braceLineIndent string // leading whitespace of the closing brace line
}

// lineEnd returns the byte offset one past the newline of the line
// containing off, or len(src) when the line has no newline.
func lineEnd(src []byte, off int) int {
	for off < len(src) && src[off] != '\n' {
		off++
	}
	if off < len(src) {
		off++
	}
	return off
}

// leadingIndent returns the whitespace prefix of the line containing off.
func leadingIndent(src []byte, off int) string {
	start := off
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	end := start
	for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
		end++
	}
	return string(src[start:end])
}

// blockGeometry computes the insertion geometry of a block node. Blocks
// whose layout does not admit a safe interior point (compact single-line
// blocks) return ErrAmbiguous instead of guessing.
func (p *Planner) blockGeometry(n Node) (blockGeometry, error) {
	src := p.doc.Source
	raw := n.Range.Text(src)
	toks, err := lex([]byte(raw))
	if err != nil {
		return blockGeometry{}, fmt.Errorf("%w: block %q cannot be lexed: %v", ErrInvalidContext, n.Name, err)
	}
	ob := -1
	for i, t := range toks {
		if t.Kind == tokenOpenBrace {
			ob = i
			break
		}
	}
	if ob < 0 {
		// A brace-less site spans to EOF; any other brace-less node is a
		// leaf and cannot host insertions.
		if n.Kind != KindSite {
			return blockGeometry{}, fmt.Errorf("%w: cannot insert into a leaf directive", ErrInvalidContext)
		}
		indent := leadingIndent(src, n.Range.Start) + "\t"
		if len(n.Children) > 0 {
			indent = leadingIndent(src, n.Children[0].Range.Start)
		}
		return blockGeometry{
			interiorStart:   lineEnd(src, n.Range.Start),
			interiorEnd:     n.Range.End,
			childIndent:     indent,
			braceLineIndent: leadingIndent(src, n.Range.Start),
		}, nil
	}

	openAbs := n.Range.Start + toks[ob].Start
	headerEnd := lineEnd(src, openAbs+1)
	// A compact single-line block has content after the brace on the
	// header line; a comment there is fine. Without a safe interior point
	// the planner refuses instead of rewriting the line.
	compact := false
	for i := openAbs + 1; i < len(src) && src[i] != '\n'; i++ {
		if src[i] == '#' {
			break // a comment runs to the end of the line
		}
		if src[i] != ' ' && src[i] != '\t' && src[i] != '\r' {
			compact = true
			break
		}
	}
	if compact {
		return blockGeometry{}, fmt.Errorf("%w: cannot determine a safe insertion point in a compact single-line block", ErrAmbiguous)
	}

	cb := -1
	for i, t := range toks {
		if t.Kind == tokenCloseBrace {
			cb = i
		}
	}
	if cb < 0 {
		return blockGeometry{}, fmt.Errorf("%w: block %q has no closing brace", ErrInvalidContext, n.Name)
	}
	closeAbs := n.Range.Start + toks[cb].Start

	indent := leadingIndent(src, closeAbs) + "\t"
	if len(n.Children) > 0 {
		indent = leadingIndent(src, n.Children[0].Range.Start)
	}
	return blockGeometry{
		interiorStart:   headerEnd,
		interiorEnd:     closeAbs,
		childIndent:     indent,
		braceLineIndent: leadingIndent(src, closeAbs),
	}, nil
}

// indexOfChild returns the index of the child matching target's identity,
// or -1.
func indexOfChild(parent Node, target Node) int {
	want := nodeIdent(target)
	for i, c := range parent.Children {
		if nodeIdent(c) == want {
			return i
		}
	}
	return -1
}

// InsertPosition selects where a new directive line lands inside a block.
type InsertPosition int

const (
	// InsertAtStart inserts before the block's first child, or right
	// after the header line when the block is empty.
	InsertAtStart InsertPosition = iota
	// InsertAtEnd inserts after the block's last child, or right after
	// the header line when the block is empty.
	InsertAtEnd
	// InsertBefore inserts immediately before an anchor child.
	InsertBefore
	// InsertAfter inserts immediately after an anchor child.
	InsertAfter
)

// DirectiveInsert describes a new directive line to insert into a block.
type DirectiveInsert struct {
	// Name is a supported directive name (see insertSpecs).
	Name string
	// Args is the raw argument text, "" for none.
	Args string
	// Position selects the insertion point.
	Position InsertPosition
	// Anchor is the sibling used by InsertBefore/InsertAfter. It is
	// matched against the parent's children by identity.
	Anchor Node
}

// insertPoint resolves the byte offset where an insertion lands.
func (p *Planner) insertPoint(parent Node, ins DirectiveInsert, geo blockGeometry) (int, error) {
	switch ins.Position {
	case InsertAtStart:
		return geo.interiorStart, nil
	case InsertAtEnd:
		if len(parent.Children) == 0 {
			return geo.interiorStart, nil
		}
		last := parent.Children[len(parent.Children)-1]
		if last.Range.End > geo.interiorEnd {
			return -1, fmt.Errorf("%w: the last child extends past the closing brace", ErrAmbiguous)
		}
		return last.Range.End, nil
	case InsertBefore, InsertAfter:
		idx := indexOfChild(parent, ins.Anchor)
		if idx < 0 {
			if _, err := p.locate(ins.Anchor); err != nil {
				return -1, err
			}
			return -1, fmt.Errorf("%w: anchor is not a direct child of the target block", ErrInvalidContext)
		}
		if ins.Position == InsertBefore {
			return parent.Children[idx].Range.Start, nil
		}
		if parent.Children[idx].Range.End > geo.interiorEnd {
			return -1, fmt.Errorf("%w: the anchor extends past the closing brace", ErrAmbiguous)
		}
		return parent.Children[idx].Range.End, nil
	}
	return -1, fmt.Errorf("%w: unknown insertion position %d", ErrAmbiguous, ins.Position)
}

// Insert plans inserting a new supported directive line into a block. The
// new line inherits the block's child indentation; every existing byte —
// including comments and unknown directives outside the edited line — is
// preserved.
func (p *Planner) Insert(parent Node, ins DirectiveInsert) (*PlannedEdit, error) {
	plocated, err := p.locate(parent)
	if err != nil {
		return nil, err
	}
	spec, ok := insertSpecs[ins.Name]
	if !ok {
		return nil, fmt.Errorf("%w: %q is not in the supported directive set", ErrUnsupported, ins.Name)
	}
	pctx, err := p.parentCtx(*plocated)
	if err != nil {
		return nil, err
	}
	if spec.ctx&pctx == 0 {
		return nil, fmt.Errorf("%w: %q cannot be inserted into a %s block", ErrInvalidContext, ins.Name, plocated.Kind)
	}
	geo, err := p.blockGeometry(*plocated)
	if err != nil {
		return nil, err
	}
	pos, err := p.insertPoint(*plocated, ins, geo)
	if err != nil {
		return nil, err
	}
	line := geo.childIndent + ins.Name
	if ins.Args != "" {
		line += " " + ins.Args
	}
	line += "\n"
	return &PlannedEdit{DocID: p.doc.Path, Range: SourceRange{Start: pos, End: pos}, NewText: line, Op: EditInsert}, nil
}

// Delete plans removing the exact source range of a construct (a directive
// line or a whole block). Every byte outside the range is preserved.
func (p *Planner) Delete(n Node) (*PlannedEdit, error) {
	located, err := p.locate(n)
	if err != nil {
		return nil, err
	}
	return &PlannedEdit{DocID: p.doc.Path, Range: located.Range, NewText: "", Op: EditDelete}, nil
}

// findCommonParent returns the first node whose children contain both a and
// b as direct children, or nil when there is none.
func (p *Planner) findCommonParent(a, b Node) *Node {
	var parent *Node
	var walk func(ns []Node)
	walk = func(ns []Node) {
		if parent != nil {
			return
		}
		for _, n := range ns {
			if parent != nil {
				return
			}
			if indexOfChild(n, a) >= 0 && indexOfChild(n, b) >= 0 {
				c := n
				parent = &c
				return
			}
			walk(n.Children)
		}
	}
	walk(p.doc.Nodes)
	return parent
}

// isTopLevel reports whether n is a direct child of the document.
func (p *Planner) isTopLevel(n Node) bool {
	want := nodeIdent(n)
	for _, c := range p.doc.Nodes {
		if nodeIdent(c) == want {
			return true
		}
	}
	return false
}

// Reorder plans swapping two sibling constructs. Both nodes must be direct
// children of the same block (or both top-level nodes); the swap replaces
// the combined span, so comments and unknown directives between the two
// keep their bytes.
func (p *Planner) Reorder(a, b Node) (*PlannedEdit, error) {
	la, err := p.locate(a)
	if err != nil {
		return nil, err
	}
	lb, err := p.locate(b)
	if err != nil {
		return nil, err
	}
	if nodeIdent(*la) == nodeIdent(*lb) {
		return nil, fmt.Errorf("%w: cannot reorder a node with itself", ErrInvalidContext)
	}
	parent := p.findCommonParent(*la, *lb)
	if parent == nil && (!p.isTopLevel(*la) || !p.isTopLevel(*lb)) {
		return nil, fmt.Errorf("%w: %q and %q are not siblings", ErrInvalidContext, la.Name, lb.Name)
	}
	earlier, later := *la, *lb
	if later.Range.Start < earlier.Range.Start {
		earlier, later = later, earlier
	}
	if later.Range.End < earlier.Range.End {
		return nil, fmt.Errorf("%w: node ranges overlap; cannot reorder safely", ErrInvalidContext)
	}
	span := SourceRange{Start: earlier.Range.Start, End: later.Range.End}
	// Keep the original bytes between the two nodes in place relative to
	// the reordered constructs. This preserves comments, blank lines and
	// unknown directives instead of silently dropping the gap.
	text := later.Range.Text(p.doc.Source) +
		string(p.doc.Source[earlier.Range.End:later.Range.Start]) +
		earlier.Range.Text(p.doc.Source)
	return &PlannedEdit{DocID: p.doc.Path, Range: span, NewText: text, Op: EditReorder}, nil
}
