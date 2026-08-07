package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
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

// formatAndValidateResultMsg is delivered to the model after a caddy
// fmt + caddy validate invocation completes. Exactly one of Formatted
// and Err is meaningful on a given message; Diagnostics is populated
// whenever the validator could parse any structured finding from the
// captured stderr.
type formatAndValidateResultMsg struct {
	Formatted   []byte
	Diagnostics []validator.Diagnostic
	Err         error
}

// Model is the inspector screen: a document tree on the left, the raw
// source of the selected document on the right (scrollable with
// PgUp/PgDown) and an optional diagnostics modal on top. It performs
// no file writes; data flows from the injected app.Loader and
// app.Formatter.
type Model struct {
	loader    app.Loader
	formatter app.Formatter // nil disables the v keybinding
	state     *app.State
	err       error         // load error (e.g. missing file)

	items     []item
	cursor    int
	collapsed map[string]bool
	width     int
	height    int
	quit      bool

	// viewport shows the source of the selected document, truncated to
	// the pane height instead of overflowing the terminal.
	viewport    viewport.Model
	sourceDoc   *caddyfile.Document
	sourceTitle string
	// lastSel tracks the selection for which revealRange last ran, so a
	// manual scroll (PgUp/PgDown) is not overridden on the next render.
	lastSel selectionKey

	// workingBytes holds the last successful format output. The source
	// pane currently shows the original source; the working copy is
	// stored for the upcoming diff / save workflow. The status line
	// reflects whether a working copy is pending.
	workingBytes []byte
	// busy is true while a format+validate invocation is in flight; a
	// second v press is ignored until the result is delivered.
	busy bool
	// statusMessage is a single line shown below the header. Cleared on
	// the next v press or on quit.
	statusMessage string

	// Diagnostics modal state. The modal is shown when
	// showDiagnostics is true; the cursor and slice are populated only
	// while the modal is open.
	showDiagnostics bool
	diagnostics     []validator.Diagnostic
	diagCursor      int

	// validatorTimeout is read from settings on Load and is the timeout
	// applied to each format+validate invocation. Zero means "no extra
	// timeout on top of the validator package default (5s)".
	validatorTimeout time.Duration
}

// New returns a Model that will load its state through loader and run
// format+validate through formatter. formatter may be nil; the v
// keybinding is disabled in that case and pressing v shows a hint in
// the status line. Call Load before starting the program.
func New(loader app.Loader, formatter app.Formatter) *Model {
	return &Model{
		loader:    loader,
		formatter: formatter,
		collapsed: map[string]bool{},
		viewport:  viewport.New(1, 1),
	}
}

// Load resolves the configuration through the injected loader and
// builds the document tree. Parse errors are kept inside the state so
// the raw source view remains available; only a read failure (missing
// file) is returned.
func (m *Model) Load() error {
	state, err := m.loader.LoadState()
	m.state = state
	m.err = err
	if state != nil {
		m.validatorTimeout = state.Settings.ValidatorTimeout
	}
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
	case formatAndValidateResultMsg:
		return m.handleFormatAndValidateResult(msg)
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The diagnostics modal takes precedence over every other key.
	if m.showDiagnostics {
		return m.updateDiagnosticsKey(msg)
	}
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
	case "v":
		return m.startFormatAndValidate()
	}
	return m, nil
}

func (m *Model) updateDiagnosticsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.closeDiagnostics()
	case "up", "k":
		if m.diagCursor > 0 {
			m.diagCursor--
		}
	case "down", "j":
		if m.diagCursor < len(m.diagnostics)-1 {
			m.diagCursor++
		}
	}
	return m, nil
}

// toggleCursor expands or collapses the document row under the cursor,
// then re-anchors the cursor on that row.
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

// startFormatAndValidate triggers a caddy fmt + caddy validate
// invocation against the root document. It is a no-op (with a status
// hint) when the formatter is not configured, another validation is
// already in flight, or no configuration has been loaded.
func (m *Model) startFormatAndValidate() (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Graph == nil {
		return m, nil
	}
	if m.formatter == nil {
		m.statusMessage = "✗ caddy binary not configured (use --caddy-path)"
		return m, nil
	}
	if m.busy {
		return m, nil
	}
	src := m.state.Graph.Root.Source
	m.busy = true
	m.statusMessage = "validating…"
	return m, m.formatAndValidateCmd(src)
}

// formatAndValidateCmd returns a tea.Cmd that runs the formatter in a
// goroutine and reports the result as a formatAndValidateResultMsg.
// The command applies a context timeout layered on top of the
// validator's own internal timeout, so a slow host cannot pin the
// goroutine forever.
func (m *Model) formatAndValidateCmd(src []byte) tea.Cmd {
	timeout := m.validatorTimeout
	formatter := m.formatter
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		formatted, diags, err := formatter.FormatAndValidate(ctx, src)
		return formatAndValidateResultMsg{
			Formatted:   formatted,
			Diagnostics: diags,
			Err:         err,
		}
	}
}

// handleFormatAndValidateResult is invoked on the main goroutine when
// a format+validate invocation completes. It clears the busy flag,
// stores the working copy on success and either opens the diagnostics
// modal or surfaces a status line on failure.
func (m *Model) handleFormatAndValidateResult(msg formatAndValidateResultMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	if msg.Err != nil {
		if len(msg.Diagnostics) > 0 {
			m.diagnostics = msg.Diagnostics
			m.diagCursor = 0
			m.showDiagnostics = true
			m.statusMessage = ""
			return m, nil
		}
		m.statusMessage = "✗ " + msg.Err.Error()
		return m, nil
	}
	m.workingBytes = msg.Formatted
	m.diagnostics = nil
	m.showDiagnostics = false
	m.statusMessage = "✓ validated (working copy updated, not saved)"
	return m, nil
}

// closeDiagnostics dismisses the diagnostics modal and clears its
// state. Called by Esc and q from inside the modal.
func (m *Model) closeDiagnostics() {
	m.showDiagnostics = false
	m.diagnostics = nil
	m.diagCursor = 0
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

	paneH := height - 3
	if m.err != nil {
		paneH--
	}
	if m.statusMessage != "" {
		paneH--
	}
	if paneH < 1 {
		paneH = 1
	}

	var b strings.Builder
	b.WriteString(m.header(width))
	if m.err != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("✗ %v", m.err)))
		b.WriteString("\n")
	}
	if m.statusMessage != "" {
		b.WriteString(m.statusLine(width))
		b.WriteString("\n")
	}
	if m.showDiagnostics {
		b.WriteString(m.diagnosticsView(width, paneH))
		b.WriteString("\n")
	} else {
		treeW := width * 2 / 5
		// Both panes carry a left and right border; subtract the full
		// horizontal border width of both so the source pane's right
		// border stays on screen.
		srcW := width - treeW - 2*paneStyle.GetHorizontalBorderSize()
		if srcW < 1 {
			srcW = 1
		}
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
			m.treePane(treeW, paneH),
			m.sourcePane(srcW, paneH)))
		b.WriteString("\n")
	}
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

// sourcePane renders the raw, unmodified source of the selected
// item's document inside a scrollable viewport. Unknown directives,
// comments and malformed regions are all shown exactly as stored; the
// viewport truncates the output to the pane height instead of
// overflowing the terminal.
func (m *Model) sourcePane(srcW, paneH int) string {
	m.syncSource(srcW, paneH)
	return paneStyle.Width(srcW).Height(paneH).Render(m.sourceTitle + "\n" + m.viewport.View())
}

// syncSource keeps the source viewport sized to the pane and refreshes
// its content whenever the selection or the pane dimensions change.
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
	// Reveal-if-needed, but only when the selection changed: after a
	// manual scroll the viewport must stay where the user left it.
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

// selectionKey identifies the tree item the source pane is bound to.
// It is deliberately comparable so revealRange only runs on actual
// selection changes.
type selectionKey struct {
	doc     *caddyfile.Document
	hasNode bool
	node    string
	start   int
	end     int
}

// revealRange scrolls the viewport just enough so that the 1-based
// source lines [startLine, endLine] are visible: when the range starts
// above the viewport it is brought to the top, when it ends below the
// viewport it is brought to the bottom, and otherwise the position is
// left unchanged.
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

// statusLine renders the current statusMessage in a style chosen by
// the leading glyph: ✓ for success, ✗ for error, anything else is
// shown in the dim info style.
func (m *Model) statusLine(width int) string {
	msg := m.statusMessage
	switch {
	case strings.HasPrefix(msg, "✓"):
		return statusSuccessStyle.Width(width).Render(msg)
	case strings.HasPrefix(msg, "✗"):
		return errorStyle.Width(width).Render(msg)
	default:
		return statusInfoStyle.Width(width).Render(msg)
	}
}

func (m *Model) footer(width int) string {
	keys := "↑/↓ move · Enter toggle · PgUp/PgDown scroll · v format & validate · q quit"
	if m.state != nil && m.state.Graph != nil {
		keys = fmt.Sprintf("↑/↓ move · Enter toggle · PgUp/PgDown scroll · v format & validate · q quit · %d items", len(m.items))
	}
	return statusLineStyle.Width(width).Render(keys)
}

// diagnosticsView renders the validation results modal. It lists the
// diagnostics with a movable cursor and a hint footer; the caller
// is responsible for closing the modal through closeDiagnostics.
func (m *Model) diagnosticsView(width, height int) string {
	title := fmt.Sprintf("Validation · %d diagnostic(s) · Esc close", len(m.diagnostics))
	bodyH := height - 4 // border (2) + title (1) + hint (1)
	if bodyH < 1 {
		bodyH = 1
	}
	var body strings.Builder
	if len(m.diagnostics) == 0 {
		body.WriteString(dimStyle.Render("no diagnostics — close with Esc"))
	} else {
		start := m.diagCursor - bodyH/2
		if start < 0 {
			start = 0
		}
		end := start + bodyH
		if end > len(m.diagnostics) {
			end = len(m.diagnostics)
		}
		for i := start; i < end; i++ {
			d := m.diagnostics[i]
			line := d.String()
			if i == m.diagCursor {
				line = cursorStyle.Render("▸ " + line)
			} else {
				line = "  " + line
			}
			body.WriteString(line + "\n")
		}
	}
	hint := "↑/↓ navigate"
	return paneStyle.Width(width).Height(height).Render(title + "\n" + body.String() + "\n" + dimStyle.Render(hint))
}

// buildItems flattens the graph into the visible tree: one row per
// document (root first, then imported files in resolution order),
// with site blocks, snippets and named routes nested under their
// document.
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
