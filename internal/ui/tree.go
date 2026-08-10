package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// Tree vocabulary (canonical):
//
//	ParsedNode — any parser node, including leaves (caddyfile.Node).
//	TreeRow    — a visible row in the UI tree (this struct).
//	Branch     — a visible row with children; only branches expand,
//	             collapse and respond to Enter/Space.
//	Leaf       — a node without children; terminal directives are not visible
//	             tree rows, while visible block-kind leaves carry a · marker.
//	             All leaves stay in the parse tree, source view and search scope.
//	Document   — a root branch (one TreeRow per caddyfile.Document).
//
// Markers: › selected row, - expanded branch, + collapsed branch, · visible
// leaf row. ASCII -/+ are used (not ▾/▸ or −) for terminal readability.
//
// item is a TreeRow: a document row, or a branch of the parse tree
// (site blocks, snippets, named routes, global options, and any node
// with children such as a directive with a nested block), nested
// recursively. Terminal leaves are never rows; visible block-kind leaves
// carry no expansion state. depth is purely a rendering
// indent; expansion logic uses hasChildren and the stable key.
type item struct {
	// key is the stable identity of the row: the document path for a
	// document row, or document path + kind + name + exact source range
	// for a node row. The cursor and the collapsed map anchor on it, so
	// a rebuild after a save, a search jump or a reload re-selects the
	// same row.
	key string
	// label is the concise row text shown in the tree pane.
	label string
	// depth is the nesting level used only for the graphical indent.
	depth int
	doc   *caddyfile.Document
	node  caddyfile.Node
	// hasNode is true for every node row (document rows carry no node).
	hasNode bool
	// hasChildren is true when the row can expand: a document with
	// parsed nodes, or a block node with children. Leaf rows have no
	// expansion marker and cannot be toggled.
	hasChildren bool
	// collapsed is the current expand/collapse state of the row, kept in
	// sync with the model's collapsed map.
	collapsed bool
}

// toggleCursor expands or collapses the row under the cursor, then
// re-anchors the cursor on that row's stable key. Only rows with children
// toggle; on a leaf row it is a no-op — nothing is collapsed and no
// workflow is started, the existing selection keeps showing the source
// pane.
func (m *Model) toggleCursor() {
	cur := m.selectedItem()
	if cur == nil || !cur.hasChildren {
		return
	}
	m.collapsed[cur.key] = !m.collapsed[cur.key]
	m.rebuildTree(cur.key)
}

// collapseOrExpand collapses (expand=false, Left) or expands
// (expand=true, Right) the selected row when it has children. Rows
// already in the requested state and leaf rows are no-ops. The cursor
// stays anchored on the row's key.
func (m *Model) collapseOrExpand(expand bool) {
	cur := m.selectedItem()
	if cur == nil || !cur.hasChildren {
		return
	}
	if expand {
		if !cur.collapsed {
			return
		}
		delete(m.collapsed, cur.key)
	} else {
		if cur.collapsed {
			return
		}
		m.collapsed[cur.key] = true
	}
	m.rebuildTree(cur.key)
}

// rebuildTree re-flattens the visible tree from the graph and re-anchors
// the cursor on the row carrying anchorKey. Every tree rebuild goes
// through here so the selection is always recovered by the item key;
// when the key is gone (for example after a delete removed the node) the
// cursor is clamped to the last row.
func (m *Model) rebuildTree(anchorKey string) {
	if m.state == nil || m.state.Graph == nil {
		return
	}
	m.items = buildItems(m.state.Graph, m.collapsed)
	if anchorKey != "" {
		for i := range m.items {
			if m.items[i].key == anchorKey {
				m.cursor = i
				return
			}
		}
	}
	if len(m.items) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
}

// expandAllBranches expands every visible branch recursively, document
// roots included. It is a no-op when no branch is collapsed, and the
// selection is preserved: an expansion never hides a row.
func (m *Model) expandAllBranches() {
	if m.state == nil || m.state.Graph == nil {
		return
	}
	changed := false
	for _, doc := range m.state.Graph.Documents {
		if doc == nil {
			continue
		}
		if key := itemKey(doc, nil); m.collapsed[key] {
			delete(m.collapsed, key)
			changed = true
		}
		var walk func(nodes []caddyfile.Node)
		walk = func(nodes []caddyfile.Node) {
			for i := range nodes {
				n := &nodes[i]
				if len(visibleTreeChildren(n.Children)) > 0 {
					if key := itemKey(doc, n); m.collapsed[key] {
						delete(m.collapsed, key)
						changed = true
					}
				}
				walk(n.Children)
			}
		}
		walk(doc.Nodes)
	}
	if !changed {
		return // no collapsed branch: no-op
	}
	anchor := ""
	if sel := m.selectedItem(); sel != nil {
		anchor = sel.key
	}
	m.rebuildTree(anchor)
}

// collapseDescendants collapses every visible branch below the document
// roots recursively, keeping the document roots expanded. It is a no-op
// when no branch exists below the roots or everything is already
// collapsed. The selection is preserved when its row survives; a hidden
// selection moves to the nearest visible ancestor.
func (m *Model) collapseDescendants() {
	if m.state == nil || m.state.Graph == nil {
		return
	}
	var branchKeys []string
	for _, doc := range m.state.Graph.Documents {
		if doc == nil {
			continue
		}
		var walk func(nodes []caddyfile.Node)
		walk = func(nodes []caddyfile.Node) {
			for i := range nodes {
				n := &nodes[i]
				if len(visibleTreeChildren(n.Children)) > 0 {
					branchKeys = append(branchKeys, itemKey(doc, n))
				}
				walk(n.Children)
			}
		}
		walk(doc.Nodes)
	}
	if len(branchKeys) == 0 {
		return // no expandable branch below the roots: no-op
	}
	changed := false
	for _, key := range branchKeys {
		if !m.collapsed[key] {
			changed = true
		}
		m.collapsed[key] = true
	}
	for _, doc := range m.state.Graph.Documents {
		if doc == nil {
			continue
		}
		// Document roots stay expanded.
		if key := itemKey(doc, nil); m.collapsed[key] {
			delete(m.collapsed, key)
			changed = true
		}
	}
	if !changed {
		return // already collapsed below the roots: no-op
	}
	// Re-anchor the selection: keep the row when it survives the rebuild,
	// otherwise move to the nearest visible ancestor.
	prev := m.selectedItem()
	selKey := ""
	if prev != nil {
		selKey = prev.key
	}
	m.rebuildTree(selKey)
	if prev == nil {
		return
	}
	if cur := m.selectedItem(); cur == nil || cur.key != selKey {
		if prev.hasNode {
			m.rebuildTree(m.nearestVisibleAncestorKey(prev.doc, prev.node))
		} else {
			m.rebuildTree(selKey)
		}
	}
}

// nearestVisibleAncestorKey returns the item key of the deepest ancestor
// row of node that is present in the current tree (m.items), falling back
// to the document row. It is used when a collapse-all rebuild hides the
// selected row: the selection moves to the closest row that is still
// visible.
func (m *Model) nearestVisibleAncestorKey(doc *caddyfile.Document, target caddyfile.Node) string {
	targetKey := nodeKey(&target)
	var chain []caddyfile.Node
	var walk func(nodes []caddyfile.Node, ancestors []caddyfile.Node) bool
	walk = func(nodes []caddyfile.Node, ancestors []caddyfile.Node) bool {
		for i := range nodes {
			n := nodes[i]
			if nodeKey(&n) == targetKey {
				chain = append(chain, ancestors...)
				return true
			}
			if len(n.Children) > 0 {
				if walk(n.Children, append(ancestors, n)) {
					return true
				}
			}
		}
		return false
	}
	walk(doc.Nodes, nil)
	for i := len(chain) - 1; i >= 0; i-- {
		key := itemKey(doc, &chain[i])
		for j := range m.items {
			if m.items[j].key == key {
				return key
			}
		}
	}
	return itemKey(doc, nil)
}

// buildItems flattens the graph into the visible tree: one TreeRow per
// document (root first, then imported files in resolution order), with
// every branch of every document nested recursively underneath it (site
// blocks, global options, snippets, named routes and nodes with
// children). Leaves are never rows: they stay in the parse tree, the
// source view and the search scope. Document rows and branches can
// expand and collapse; rows without children cannot. The cursor and the
// collapsed state anchor on each row's stable key, so a rebuild never
// loses the selection or the expand/collapse state. Imported files stay
// separate top-level rows: the import graph is never duplicated into a
// synthetic syntax tree.
func buildItems(g *caddyfile.ImportGraph, collapsed map[string]bool) []item {
	var items []item
	for _, doc := range g.Documents {
		if doc == nil {
			continue
		}
		docKey := itemKey(doc, nil)
		docCollapsed := collapsed[docKey]
		items = append(items, item{
			key:         docKey,
			label:       filepath.Base(doc.Path),
			doc:         doc,
			hasChildren: len(visibleTreeChildren(doc.Nodes)) > 0,
			collapsed:   docCollapsed,
		})
		if docCollapsed {
			continue
		}
		appendNodeItems(&items, doc, doc.Nodes, 1, collapsed)
	}
	return items
}

// visibleTreeChildren filters children through the tree visibility
// policy, returning the ones that themselves become tree rows. Hidden
// leaf directives are dropped, so a row's expandable state depends only
// on children that are actually visible: the rows shown underneath it.
func visibleTreeChildren(nodes []caddyfile.Node) []caddyfile.Node {
	var out []caddyfile.Node
	for i := range nodes {
		if renderedNode(nodes[i]) {
			out = append(out, nodes[i])
		}
	}
	return out
}

// seedCollapsedState initializes the startup expand/collapse layout for
// a fresh session: every document root is expanded and every visible
// branch below the document roots starts collapsed. Visible leaf rows carry
// no expansion state and a · marker. The layout is derived from the graph and never
// persisted across sessions unless explicitly configured.
func seedCollapsedState(g *caddyfile.ImportGraph, collapsed map[string]bool) {
	for _, doc := range g.Documents {
		if doc == nil {
			continue
		}
		var walk func(nodes []caddyfile.Node)
		walk = func(nodes []caddyfile.Node) {
			for i := range nodes {
				n := &nodes[i]
				if len(visibleTreeChildren(n.Children)) > 0 {
					collapsed[itemKey(doc, n)] = true
				}
				walk(n.Children)
			}
		}
		walk(doc.Nodes)
	}
}

// appendNodeItems appends one visible tree row per branch in nodes,
// recursing into the visible children. A ParsedNode is a TreeRow when it
// can carry children: the top-level block kinds (sites, global options,
// snippets, named routes) always render, and any other node renders only
// when it has children. Its expandable state (hasChildren) depends on
// the visible children only: a site whose only children are hidden
// leaves (for example a lone import directive) renders as a leaf row
// with a · marker. Terminal leaves are never TreeRows: they stay in
// the parse tree, the source view and the search scope. depth is only
// the rendering indent, and a row is collapsible exactly when it has
// visible children (they are hidden while it is collapsed).
func appendNodeItems(items *[]item, doc *caddyfile.Document, nodes []caddyfile.Node, depth int, collapsed map[string]bool) {
	for i := range nodes {
		n := &nodes[i]
		if !renderedNode(*n) {
			continue
		}
		key := itemKey(doc, n)
		visible := visibleTreeChildren(n.Children)
		*items = append(*items, item{
			key:         key,
			label:       nodeLabel(*n),
			depth:       depth,
			doc:         doc,
			node:        *n,
			hasNode:     true,
			hasChildren: len(visible) > 0,
			collapsed:   collapsed[key],
		})
		if len(visible) > 0 && !collapsed[key] {
			appendNodeItems(items, doc, visible, depth+1, collapsed)
		}
	}
}

// renderedNode reports whether a nested node becomes a visible tree row
// (a Branch or an empty block-kind row). The top-level block kinds
// (sites, global options, snippets, named routes) always render, and any
// other node renders only when it has children (a structural block such
// as a directive with a nested block). Terminal directives without
// children (header_up, tls_insecure_skip_verify, protocols, respond,
// import, …) are Leaves: they stay in the parse tree, the source view and
// the search scope, but never become tree rows.
func renderedNode(n caddyfile.Node) bool {
	switch n.Kind {
	case caddyfile.KindGlobalOptions, caddyfile.KindSite, caddyfile.KindSnippet, caddyfile.KindNamedRoute:
		return true
	}
	return len(n.Children) > 0
}

// nodeIsTreeRow reports whether a node has a visible tree row of its
// own: block kinds and any node with children. Leaves never render, so a
// leaf search hit selects its nearest visible ancestor instead.
func nodeIsTreeRow(n *caddyfile.Node) bool {
	return n != nil && renderedNode(*n)
}

// structuralNodeAtLine returns the deepest tree row whose source range
// contains the 1-based line, or nil when the line falls outside every
// tree row (the caller then selects the document row). It is used when a
// search hit activates a source line: the tree cursor lands on the
// containing branch instead of jumping to the document row.
func structuralNodeAtLine(doc *caddyfile.Document, line int) *caddyfile.Node {
	var best *caddyfile.Node
	var walk func(nodes []caddyfile.Node)
	walk = func(nodes []caddyfile.Node) {
		for i := range nodes {
			n := &nodes[i]
			if line < n.Range.StartLine || line > n.Range.EndLine {
				continue
			}
			if renderedNode(*n) {
				best = n
			}
			walk(n.Children)
		}
	}
	walk(doc.Nodes)
	return best
}

// itemKey returns the stable identity of a tree row: the document path
// for a document row, or the document path plus kind, name and exact
// source range for a node row. The two spaces cannot collide (node keys
// are prefixed), so a document and a node never share an anchor.
func itemKey(doc *caddyfile.Document, n *caddyfile.Node) string {
	path := ""
	if doc != nil {
		path = doc.Path
	}
	if n == nil {
		return "doc:" + path
	}
	return nodeKey(n) + "@" + path
}

// nodeKey identifies a node within its document by kind, name and exact
// source range. It is the per-node part of itemKey and is shared by the
// search activation, which expands every collapsed ancestor of a hit.
func nodeKey(n *caddyfile.Node) string {
	return fmt.Sprintf("node:%d:%s:%d:%d", n.Kind, n.Name, n.Range.Start, n.Range.End)
}

// nodeLabel renders the tree label for a node. buildItems and the search
// scope share it, so a collapsed document still contributes its nodes to
// global search with the same labels the tree would show. Directive rows
// carry a concise label made of the directive name plus its arguments,
// truncated so a long argument list never overflows the tree pane; the
// source pane always shows the exact bytes.
func nodeLabel(n caddyfile.Node) string {
	switch n.Kind {
	case caddyfile.KindGlobalOptions:
		// The global options block has no header, so it renders under a
		// fixed label rather than a name.
		return "global options"
	case caddyfile.KindSnippet:
		return "snippet (" + n.Name + ")"
	case caddyfile.KindNamedRoute:
		return "route &(" + n.Name + ")"
	case caddyfile.KindDirective:
		if n.Name == "" {
			// An anonymous `{ ... }` block inside a block: it has no
			// header to name it, so it needs an explicit label.
			return "anonymous block"
		}
		label := n.Name
		if args := strings.TrimSpace(n.Args); args != "" {
			label += " " + args
		}
		return truncateToWidth(label, maxDirectiveLabel)
	default:
		return n.Name // KindSite and unknown kinds
	}
}

// maxDirectiveLabel bounds the tree label of a directive row so a long
// argument list (or an import path) never overflows the tree pane.
const maxDirectiveLabel = 40
