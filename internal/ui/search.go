package ui

import (
	"fmt"
	"strings"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	tea "github.com/charmbracelet/bubbletea"
)

// startSearch opens the read-only search modal. It is gated on a
// configured searcher; there is no read-only gate (search never writes).
// Opening seeds the log scope from the configured source when it was never
// loaded, so search covers the bounded history even before the log view
// was opened: every recompute passes m.logLines as scope.Logs, so a
// LogIndex always refers to that exact slice.
func (m *Model) startSearch() (tea.Model, tea.Cmd) {
	if m.searcher == nil {
		m.statusMessage = "✗ search unavailable"
		return m, nil
	}
	if m.logSource != nil && len(m.logLines) == 0 {
		m.logLines = append([]logs.Entry(nil), m.logSource.History()...)
	}
	m.searchQuery = nil
	m.searchResults = nil
	m.searchCursor = 0
	m.searchActive = true
	m.statusMessage = ""
	m.searchViewport.SetContent("")
	return m, nil
}

// updateSearchKey handles keys while the search modal is open. Runes
// always accumulate into the query before any named-key handling, so
// ordinary characters (q, j, k, space, …) are always searchable; only the
// actual navigation keys move the result cursor, PgUp/PgDown page the
// result viewport, Enter activates the result under the cursor, backspace
// trims the query and Esc closes without side effects.
func (m *Model) updateSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// An input buffer must always win over named-key navigation: a typed
	// character is text, never a shortcut.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		m.searchQuery = append(m.searchQuery, msg.Runes...)
		m.recomputeSearch()
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeSearch()
	case "ctrl+c":
		return m.requestQuit()
	case "backspace":
		if len(m.searchQuery) > 0 {
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
			m.recomputeSearch()
		}
	case "up":
		if m.searchCursor > 0 {
			m.searchCursor--
		}
		m.revealSearchCursor()
	case "down":
		if m.searchCursor < len(m.searchResults)-1 {
			m.searchCursor++
		}
		m.revealSearchCursor()
	case "pgup":
		m.searchViewport.PageUp()
	case "pgdown":
		m.searchViewport.PageDown()
	case "enter":
		if m.searchCursor >= 0 && m.searchCursor < len(m.searchResults) {
			m.activateSearchResult(m.searchResults[m.searchCursor])
			m.closeSearch()
		}
	}
	return m, nil
}

// recomputeSearch rebuilds the results for the current query against the
// whole resolved graph (every document, node and content line, imported
// files included, independent of the collapsed UI state) plus the loaded
// log lines, resets the cursor to the top and re-anchors the viewport
// scroll (the content itself is rebuilt on the next render).
func (m *Model) recomputeSearch() {
	if m.searcher == nil {
		m.searchResults = nil
		m.searchCursor = 0
		return
	}
	scope := app.SearchScope{Logs: m.logLines}
	if m.state != nil && m.state.Graph != nil {
		for _, doc := range m.state.Graph.Documents {
			if doc == nil {
				continue
			}
			// Document row: path and content matches.
			scope.Items = append(scope.Items, app.SearchItem{Label: doc.Path, Doc: doc})
			// Every node, recursively, independent of the collapsed UI
			// state: a global search must cover collapsed documents and
			// collapsed blocks too, including leaf directives, imports
			// and anonymous blocks.
			appendSearchItems(&scope.Items, doc, doc.Nodes)
		}
	}
	m.searchResults = m.searcher.Search(string(m.searchQuery), scope)
	m.searchCursor = 0
	m.searchViewport.GotoTop()
}

// appendSearchItems adds one SearchItem per node in nodes, recursing into
// children, so the global search scope covers the whole parse tree of
// every document with the same labels the tree would show.
func appendSearchItems(items *[]app.SearchItem, doc *caddyfile.Document, nodes []caddyfile.Node) {
	for _, n := range nodes {
		*items = append(*items, app.SearchItem{Label: nodeLabel(n), Doc: doc, Node: n, HasNode: true})
		appendSearchItems(items, doc, n.Children)
	}
}

// closeSearch dismisses the search modal and clears its state. It never
// touches the selection, the log view, the diff, the editor or the save
// workflow; sourceRevealLine survives so a just-activated result can still
// be revealed by the next render.
func (m *Model) closeSearch() {
	m.searchActive = false
	m.searchQuery = nil
	m.searchResults = nil
	m.searchCursor = 0
	m.searchViewport.SetContent("")
}

// activateSearchResult jumps to the target of a search hit: a node hit
// re-anchors the tree cursor on its visible row, a document hit selects the
// deepest containing row (optionally revealing a content line), and a log
// hit opens the log view with the entry's detail.
func (m *Model) activateSearchResult(r app.SearchResult) {
	m.sourceRevealLine = 0
	switch r.Kind {
	case app.SearchNode:
		// A search covers every node, including collapsed rows and hidden
		// leaf directives. Expand every ancestor of the node (the document
		// row first, then each parent block) so the containing rows exist.
		if r.Doc == nil || m.state == nil || m.state.Graph == nil {
			return
		}
		expandNodeAncestors(r.Doc, r.Node, m.collapsed)
		if nodeIsTreeRow(&r.Node) {
			// A structural node: its own row is visible, select it by its
			// stable key.
			m.rebuildTree(itemKey(r.Doc, &r.Node))
			return
		}
		// A leaf directive has no tree row of its own: select the nearest
		// visible ancestor (the deepest enclosing branch, or the document
		// row for a top-level leaf such as an import) and reveal the exact
		// source line of the hit without creating a new row.
		parent := nearestVisibleAncestor(r.Doc, r.Node)
		if parent != nil {
			m.rebuildTree(itemKey(r.Doc, parent))
		} else {
			m.rebuildTree(itemKey(r.Doc, nil))
		}
		m.sourceRevealLine = r.Node.Range.StartLine
		return
	case app.SearchDocument:
		// A path hit (no line) selects the document row. A line hit
		// selects the deepest tree row containing the line, so the cursor
		// lands on the structural node instead of the document row, and
		// reveals the exact source line. The target document and every
		// containing ancestors are expanded first (mirroring the
		// SearchNode branch), so the row exists in the rebuilt tree even
		// when the document or a containing branch is collapsed; a line
		// outside every tree row (for example inside a hidden top-level
		// leaf such as an import) expands the document root only.
		var node *caddyfile.Node
		if r.Line > 0 {
			node = structuralNodeAtLine(r.Doc, r.Line)
			if node != nil {
				expandNodeAncestors(r.Doc, *node, m.collapsed)
			} else {
				delete(m.collapsed, itemKey(r.Doc, nil))
			}
		}
		m.rebuildTree(itemKey(r.Doc, node))
		if r.Line > 0 {
			m.sourceRevealLine = r.Line
		}
	case app.SearchLog:
		if r.LogIndex < 0 || r.LogIndex >= len(m.logLines) {
			return
		}
		m.logCursor = r.LogIndex
		m.logDetailEntry = m.logLines[r.LogIndex]
		m.logDetailOpen = true
		m.logFollow = false
		m.showLogs = true
	}
}

// expandNodeAncestors removes the collapsed state of every row on the
// path from the document row down to target, so the target row exists in
// the visible tree after the next rebuild. Search covers collapsed rows,
// so activating a nested hit must reveal the whole ancestor chain. It is
// a no-op when doc is nil or the node is not found.
func expandNodeAncestors(doc *caddyfile.Document, target caddyfile.Node, collapsed map[string]bool) {
	if doc == nil {
		return
	}
	delete(collapsed, itemKey(doc, nil))
	targetKey := nodeKey(&target)
	var walk func(nodes []caddyfile.Node, chain []caddyfile.Node) bool
	walk = func(nodes []caddyfile.Node, chain []caddyfile.Node) bool {
		for i := range nodes {
			n := nodes[i]
			if nodeKey(&n) == targetKey {
				for _, a := range chain {
					delete(collapsed, itemKey(doc, &a))
				}
				return true
			}
			if len(n.Children) > 0 {
				if walk(n.Children, append(chain, n)) {
					return true
				}
			}
		}
		return false
	}
	walk(doc.Nodes, nil)
}

// nearestVisibleAncestor returns the deepest ancestor of target that is
// rendered as a visible tree row: for a nested leaf directive the
// enclosing block (the immediate parent always has children, so it always
// renders). Top-level leaves are caught by nodeIsTreeRow before this
// helper is reached; only nested leaves land here. It is used when a
// search hit activates a leaf directive, which has no tree row of its own.
func nearestVisibleAncestor(doc *caddyfile.Document, target caddyfile.Node) *caddyfile.Node {
	targetKey := nodeKey(&target)
	var found *caddyfile.Node
	var walk func(nodes []caddyfile.Node, chain []caddyfile.Node) bool
	walk = func(nodes []caddyfile.Node, chain []caddyfile.Node) bool {
		for i := range nodes {
			n := nodes[i]
			if nodeKey(&n) == targetKey {
				if len(chain) > 0 {
					found = &chain[len(chain)-1]
				}
				return true
			}
			if len(n.Children) > 0 {
				if walk(n.Children, append(chain, n)) {
					return true
				}
			}
		}
		return false
	}
	walk(doc.Nodes, nil)
	return found
}

// revealSearchCursor scrolls the search viewport just enough so that the
// row under searchCursor is visible, mirroring revealLogCursor.
func (m *Model) revealSearchCursor() {
	if m.searchCursor < m.searchViewport.YOffset {
		m.searchViewport.SetYOffset(m.searchCursor)
	} else if m.searchCursor >= m.searchViewport.YOffset+m.searchViewport.Height {
		m.searchViewport.SetYOffset(m.searchCursor - m.searchViewport.Height + 1)
	}
}

// searchView renders the centered read-only search catalog. The query shares
// the command palette header treatment, including a visible block cursor and
// result count, while the viewport keeps the selected result under a blue
// selector.
func (m *Model) searchView(width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	boxW := width - 8
	if boxW < 56 {
		boxW = width - 2
	}
	if boxW > 110 {
		boxW = 110
	}
	if boxW < 1 {
		boxW = 1
	}
	boxH := height - 6
	if boxH < 10 {
		boxH = height - 2
	}
	if boxH < 6 {
		boxH = 6
	}
	if boxH > 30 {
		boxH = 30
	}

	contentW := boxW - 4 // border (2) + horizontal padding (2)
	if contentW < 1 {
		contentW = 1
	}
	viewportH := boxH - 6 // header, two separators, footer and border
	if viewportH < 1 {
		viewportH = 1
	}
	m.syncSearchViewport(contentW, viewportH)

	query := truncateToWidth(string(m.searchQuery), max(1, contentW-24))
	header := activeTitleStyle.Render("SEARCH") + " " +
		cursorStyle.Render("> "+query+"▌") + " " +
		dimStyle.Render(fmt.Sprintf("%d result(s)", len(m.searchResults)))
	separator := dimStyle.Render(strings.Repeat("─", contentW))
	footer := renderFooterKeys("↑/↓ navigate · PgUp/PgDown scroll · Enter open · Esc close")
	content := strings.Join([]string{header, separator, m.searchViewport.View(), separator, footer}, "\n")
	return commandPaletteStyle.Width(boxW - 2).Height(boxH - 2).Render(content)
}

// syncSearchViewport sizes the search viewport and refreshes its content
// from the current results, highlighting the row under searchCursor. The
// current offset is preserved by SetContent (it clamps only when the
// result set shrank), matching the "don't clobber manual scroll" rule used
// elsewhere.
func (m *Model) syncSearchViewport(width, height int) {
	contentW := width
	if contentW < 1 {
		contentW = 1
	}
	contentH := height
	if contentH < 1 {
		contentH = 1
	}
	m.searchViewport.Width = contentW
	m.searchViewport.Height = contentH

	var content strings.Builder
	if len(m.searchResults) == 0 {
		if string(m.searchQuery) != "" {
			content.WriteString(dimStyle.Render("no matches"))
		} else {
			content.WriteString(dimStyle.Render("type to search across sites, files and logs"))
		}
	} else {
		textW := contentW - 2 // cursor prefix ("› ")
		if textW < 1 {
			textW = 1
		}
		for i, r := range m.searchResults {
			line := truncateToWidth(r.Label, textW)
			if i == m.searchCursor {
				line = cursorStyle.Render("› " + line)
			} else {
				line = "  " + line
			}
			content.WriteString(line + "\n")
		}
	}
	m.searchViewport.SetContent(content.String())
}
