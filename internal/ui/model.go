package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// item is one row of the document tree: a document row (depth 0) or one of
// its site blocks, snippets or named routes (depth 1).
type item struct {
	label     string
	depth     int
	doc       *caddyfile.Document
	node      caddyfile.Node
	hasNode   bool
	collapsed bool
}

// Model is the first read-only inspector screen: a document tree on the left
// and the raw source of the selected document on the right, scrollable with
// PgUp/PgDown. It performs no writes; the only data source is the injected
// app.Loader.
type Model struct {
	loader app.Loader
	state  *app.State
	err    error // load error (e.g. missing file), independent of parse errors

	items     []item
	cursor    int
	collapsed map[string]bool
	width     int
	height    int
	quit      bool

	// viewport shows the source of the selected document, truncated to the
	// pane height instead of overflowing the terminal.
	viewport    viewport.Model
	sourceDoc   *caddyfile.Document
	sourceTitle string
	// lastSel tracks the selection for which revealRange last ran, so a
	// manual scroll (PgUp/PgDown) is not overridden on the next render.
	lastSel selectionKey
}

// New returns a Model that will load its state through loader. Call Load
// before starting the program.
func New(loader app.Loader) *Model {
	return &Model{
		loader:    loader,
		collapsed: map[string]bool{},
		viewport:  viewport.New(1, 1),
	}
}

// Load resolves the configuration through the injected loader and builds the
// document tree. Parse errors are kept inside the state so the raw source
// view remains available; only a read failure (missing file) is returned.
func (m *Model) Load() error {
	state, err := m.loader.LoadState()
	m.state = state
	m.err = err
	if state != nil && state.Graph != nil {
		m.items = buildItems(state.Graph, m.collapsed)
		m.cursor = 0
	}
	return err
}

// Init implements tea.Model. Loading is done synchronously before the
// program starts, so there is no startup command.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quit = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter", " ":
		m.toggleCursor()
	case "pgup":
		m.viewport.PageUp()
	case "pgdown":
		m.viewport.PageDown()
	}
	return m, nil
}

// toggleCursor expands or collapses the document row under the cursor, then
// re-anchors the cursor on that row.
func (m *Model) toggleCursor() {
	if m.cursor >= len(m.items) || m.state == nil || m.state.Graph == nil {
		return
	}
	cur := m.items[m.cursor]
	if cur.depth != 0 || cur.doc == nil {
		return
	}
	path := cur.doc.Path
	m.collapsed[path] = !m.collapsed[path]
	m.items = buildItems(m.state.Graph, m.collapsed)
	for i, it := range m.items {
		if it.doc != nil && it.doc.Path == path && it.depth == 0 {
			m.cursor = i
			break
		}
	}
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.state == nil && m.err == nil {
		return "loading…"
	}
	width, height := m.width, m.height
	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}

	var b strings.Builder
	b.WriteString(m.header(width))
	if m.err != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("✗ %v", m.err)))
		b.WriteString("\n")
	}
	treeW := width * 2 / 5
	// Both panes carry a left and right border; subtract the full horizontal
	// border width of both so the source pane's right border stays on
	// screen.
	srcW := width - treeW - 2*paneStyle.GetHorizontalBorderSize()
	if srcW < 1 {
		srcW = 1
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
		m.treePane(treeW, height-3),
		m.sourcePane(srcW, height-3)))
	b.WriteString("\n")
	b.WriteString(m.footer(width))
	return b.String()
}

func (m *Model) header(width int) string {
	path := ""
	if m.state != nil {
		path = m.state.Settings.ConfigPath
	}
	if path == "" {
		path = "unknown"
	}
	left := headerStyle.Render(" lazycaddy ")
	right := readOnlyBadge.Render(" READ-ONLY ")
	if m.state != nil && m.state.Graph != nil && m.state.Graph.Err != nil {
		right = errorStyle.Render(" PARSE ERROR ") + right
	}
	pad := width - lipgloss.Width(left) - lipgloss.Width(right) - len(path) - 3
	if pad < 1 {
		pad = 1
	}
	return left + dimStyle.Render(path) + strings.Repeat(" ", pad) + right + "\n"
}

func (m *Model) treePane(width, height int) string {
	title := "Documents"
	if m.err != nil {
		title = "Documents (unavailable)"
	}
	var body strings.Builder
	if len(m.items) == 0 {
		body.WriteString(dimStyle.Render("no documents loaded — raw source view is on the right"))
	} else {
		start := m.cursor - height/2
		if start < 0 {
			start = 0
		}
		end := start + height
		if end > len(m.items) {
			end = len(m.items)
		}
		for i := start; i < end; i++ {
			it := m.items[i]
			line := renderItem(it)
			if i == m.cursor {
				line = cursorStyle.Render("▸ " + line)
			} else {
				line = "  " + line
			}
			body.WriteString(line + "\n")
		}
	}
	return paneStyle.Width(width).Height(height).Render(title + "\n" + body.String())
}

func renderItem(it item) string {
	indent := strings.Repeat("  ", it.depth)
	if it.depth == 0 {
		marker := "−" // expanded
		if it.collapsed {
			marker = "+" // collapsed
		}
		return fmt.Sprintf("%s%s %s", indent, marker, filepath.Base(it.doc.Path))
	}
	if it.hasNode {
		return fmt.Sprintf("%s%s", indent, it.label)
	}
	return indent + it.label
}

// sourcePane renders the raw, unmodified source of the selected item's
// document inside a scrollable viewport. Unknown directives, comments and
// malformed regions are all shown exactly as stored; the viewport truncates
// the output to the pane height instead of overflowing the terminal.
func (m *Model) sourcePane(srcW, paneH int) string {
	m.syncSource(srcW, paneH)
	return paneStyle.Width(srcW).Height(paneH).Render(m.sourceTitle + "\n" + m.viewport.View())
}

// syncSource keeps the source viewport sized to the pane and refreshes its
// content whenever the selection or the pane dimensions change.
func (m *Model) syncSource(srcW, paneH int) {
	contentW := srcW - 4 // border (2) + horizontal padding (2)
	if contentW < 1 {
		contentW = 1
	}
	contentH := paneH - 4 // border (2) + title and blank line (2)
	if contentH < 1 {
		contentH = 1
	}
	m.viewport.Width = contentW
	m.viewport.Height = contentH

	selected := m.selectedItem()
	title := "Source"
	var doc *caddyfile.Document
	if selected != nil && selected.doc != nil {
		doc = selected.doc
		title = "Source · " + selected.doc.Path
		if selected.hasNode {
			title += fmt.Sprintf(" · %s (lines %d-%d)", selected.node.Name, selected.node.Range.StartLine, selected.node.Range.EndLine)
		}
	}
	if doc != m.sourceDoc {
		// New document: load its full source and start at the top. The
		// reveal below then scrolls just enough for the selected node.
		m.sourceDoc = doc
		m.sourceTitle = title
		var src []byte
		if doc != nil {
			src = doc.Source
		}
		m.viewport.SetContent(numberedSource(src))
		m.viewport.GotoTop()
	} else if title != m.sourceTitle {
		m.sourceTitle = title
	}
	// Reveal-if-needed, but only when the selection changed: after a manual
	// scroll the viewport must stay where the user left it.
	key := selectionKey{doc: doc}
	if selected != nil && selected.hasNode {
		key.hasNode = true
		key.node = selected.node.Name
		key.start = selected.node.Range.StartLine
		key.end = selected.node.Range.EndLine
	}
	if key != m.lastSel {
		m.lastSel = key
		if key.hasNode {
			m.revealRange(key.start, key.end)
		}
	}
}

// selectionKey identifies the tree item the source pane is bound to. It is
// deliberately comparable so revealRange only runs on actual selection
// changes.
type selectionKey struct {
	doc     *caddyfile.Document
	hasNode bool
	node    string
	start   int
	end     int
}

// revealRange scrolls the viewport just enough so that the 1-based source
// lines [startLine, endLine] are visible: when the range starts above the
// viewport it is brought to the top, when it ends below the viewport it is
// brought to the bottom, and otherwise the position is left unchanged.
func (m *Model) revealRange(startLine, endLine int) {
	firstVisible := m.viewport.YOffset + 1
	lastVisible := m.viewport.YOffset + m.viewport.Height
	switch {
	case startLine < firstVisible:
		m.viewport.SetYOffset(startLine - 1)
	case endLine > lastVisible:
		m.viewport.SetYOffset(endLine - m.viewport.Height)
	}
}

// selectedItem returns the item under the cursor, or nil.
func (m *Model) selectedItem() *item {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	return &m.items[m.cursor]
}

func (m *Model) footer(width int) string {
	keys := "↑/↓ move · Enter toggle · PgUp/PgDown scroll source · q quit"
	if m.state != nil && m.state.Graph != nil {
		keys = fmt.Sprintf("↑/↓ move · Enter toggle · PgUp/PgDown scroll source · q quit · %d items", len(m.items))
	}
	return statusLineStyle.Width(width).Render(keys)
}

// buildItems flattens the graph into the visible tree: one row per document
// (root first, then imported files in resolution order), with site blocks,
// snippets and named routes nested under their document.
func buildItems(g *caddyfile.ImportGraph, collapsed map[string]bool) []item {
	var items []item
	for _, doc := range g.Documents {
		items = append(items, item{
			depth:     0,
			doc:       doc,
			collapsed: collapsed[doc.Path],
		})
		if collapsed[doc.Path] {
			continue
		}
		for _, n := range doc.Nodes {
			switch n.Kind {
			case caddyfile.KindSite:
				items = append(items, item{label: n.Name, depth: 1, doc: doc, node: n, hasNode: true})
			case caddyfile.KindSnippet:
				items = append(items, item{label: "snippet (" + n.Name + ")", depth: 1, doc: doc, node: n, hasNode: true})
			case caddyfile.KindNamedRoute:
				items = append(items, item{label: "route &(" + n.Name + ")", depth: 1, doc: doc, node: n, hasNode: true})
			}
		}
	}
	return items
}

func numberedSource(src []byte) string {
	if len(src) == 0 {
		return dimStyle.Render("(empty source — raw view still available)")
	}
	lines := strings.Split(string(src), "\n")
	var b strings.Builder
	for i, ln := range lines {
		fmt.Fprintf(&b, "%4d│ %s\n", i+1, ln)
	}
	return b.String()
}
