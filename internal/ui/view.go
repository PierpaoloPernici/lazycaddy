package ui

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	"github.com/charmbracelet/lipgloss"
)

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

	paneH := m.paneContentH(height)

	// Compute the compact footer string now; the pane area above accounts for
	// its height and the command palette carries the full action catalog.
	footerStr := m.footer(width)

	var b strings.Builder
	b.WriteString(m.header(width))
	if m.err != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("✗ %v", m.err)))
		b.WriteString("\n")
	}
	if m.showUnsavedConfirm {
		// The unsaved-changes guard layers above every other modal: it is
		// the exit guard and can be opened from any context.
		b.WriteString(m.unsavedConfirmView(width, paneH))
		b.WriteString("\n")
	} else if m.showChangeConflict {
		// The conflict modal layers above every other modal. While its
		// compare diff is open, the shared diff modal renders instead.
		if m.changeCompare {
			b.WriteString(m.diffView(width, paneH))
		} else {
			b.WriteString(m.changeConflictView(width, paneH))
		}
		b.WriteString("\n")
	} else if m.showDiff {
		b.WriteString(m.diffView(width, paneH))
		b.WriteString("\n")
	} else if m.showSaveConfirm {
		b.WriteString(m.saveConfirmView(width, paneH))
		b.WriteString("\n")
	} else if m.showReloadConfirm {
		// Reload confirmation is composited as a centered modal over the
		// normal panes, matching search and the command palette.
		treeW := width * 2 / 5
		srcW := width - treeW - 2*paneStyle.GetHorizontalBorderSize()
		if srcW < 1 {
			srcW = 1
		}
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
			m.treePane(treeW, paneH),
			m.sourcePane(srcW, paneH)))
		b.WriteString("\n")
	} else if m.showRollbackConfirm {
		b.WriteString(m.rollbackConfirmView(width, paneH))
		b.WriteString("\n")
	} else if m.showBackups {
		b.WriteString(m.backupView(width, paneH))
		b.WriteString("\n")
	} else if m.showDiagnostics {
		if m.showDetail {
			b.WriteString(m.diagnosticDetailView(width, paneH))
		} else {
			b.WriteString(m.diagnosticsView(width, paneH))
		}
		b.WriteString("\n")
	} else if m.showLogs {
		// The log view is a full screen, not a modal: it replaces the
		// tree/source panes but stays below the modal layering above.
		b.WriteString(m.logView(width, paneH))
		b.WriteString("\n")
		if m.logDetailOpen {
			// The detail modal layers over the log view.
			b.WriteString(m.logDetailView(width, paneH))
			b.WriteString("\n")
		}
	} else if m.searchActive {
		// Search is composited as a modal over the normal panes below, just
		// like the command palette. Keep the underlying application chrome
		// visible so closing search returns to the exact same context.
		treeW := width * 2 / 5
		srcW := width - treeW - 2*paneStyle.GetHorizontalBorderSize()
		if srcW < 1 {
			srcW = 1
		}
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
			m.treePane(treeW, paneH),
			m.sourcePane(srcW, paneH)))
		b.WriteString("\n")
	} else if m.showErrorHistory {
		// The error-history view replaces the tree/source panes while it
		// is open, mirroring the log view.
		b.WriteString(m.errorHistoryView(width, paneH))
		b.WriteString("\n")
	} else if m.showInlineReview {
		// The inline-review view replaces the tree/source panes while it is
		// open; v delegates to the authoritative caddy validate workflow.
		b.WriteString(m.inlineReviewView(width, paneH))
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
	if m.statusMessage != "" {
		b.WriteString(m.statusStrip(width))
		b.WriteString("\n")
	}
	b.WriteString(footerStr)
	view := b.String()
	if m.showCommandPalette {
		return m.commandPaletteOverlay(view, width, height)
	}
	if m.searchActive {
		return m.searchOverlay(view, width, height)
	}
	if m.showStructuredAdd {
		return m.structuredAddOverlay(view, width, height)
	}
	if m.showReloadConfirm {
		return m.reloadOverlay(view, width, height)
	}
	return view
}

func (m *Model) header(width int) string {
	// Keep the brand aligned with the padded content in the panes and footer.
	// Reserve the gutter before calculating the right-side badges so the added
	// space never pushes them off a narrow terminal.
	contentWidth := width - 1
	if contentWidth < 1 {
		contentWidth = 1
	}
	path := ""
	if m.state != nil {
		path = m.state.Settings.ConfigPath
	}
	if path == "" {
		path = "unknown"
	}

	version := m.version
	if version == "" {
		version = "dev"
	}
	left := brandStyle.Render("lazycaddy " + version)
	separator := dimStyle.Render(" · ")

	// The caddy version is secondary metadata: drop it on narrow widths
	// before compressing the path.
	var caddyVersion string
	if m.runtimeProbed && m.runtimeReport.Capabilities.Binary {
		caddyVersion = dimStyle.Render("Caddy " + m.runtimeReport.Capabilities.Version)
	}

	// Important state is conveyed by an explicit compact text label, not
	// color alone. RW/RO stays legible on narrow terminals while preserving
	// the same writable/read-only distinction as the longer labels.
	right := readOnlyBadge.Render(" RO ")
	if m.state != nil && !m.state.Settings.ReadOnly {
		right = writableBadge.Render(" RW ")
	}
	if m.state != nil && m.state.Graph != nil && m.state.Graph.Err != nil {
		right = errorStyle.Render(" PARSE ERROR ") + right
	}
	// The runtime status badge sits at the front of the right stack
	// (before the PARSE ERROR marker) so the most immediate operational
	// state reads first. Nothing is rendered before the probe returns,
	// and an unknown probe result stays quiet.
	if m.runtimeProbed && m.runtimeReport.Status != runtime.StatusUnknown {
		switch m.runtimeReport.Status {
		case runtime.StatusRunning:
			right = runtimeRunningBadge.Render(" RUNNING ") + right
		case runtime.StatusStopped:
			right = runtimeStoppedBadge.Render(" STOPPED ") + right
		case runtime.StatusUnreachable:
			right = runtimeUnreachableBadge.Render(" UNREACHABLE ") + right
		}
	}
	// The loaded-state badge sits between the PARSE ERROR marker and the
	// read/write badge. Explicit text labels carry the state, never color
	// alone, matching the RO convention. The initial state is shown
	// as UNKNOWN (nothing proven yet) only when reloading is possible, so
	// a read-only session without a caddy binary stays quiet.
	if m.reloading {
		right = reloadingBadge.Render(" RELOADING ") + right
	} else if m.loaded == loadedMatches {
		right = loadedBadge.Render(" LOADED ") + right
	} else if m.loaded == loadedStale {
		right = staleBadge.Render(" STALE ") + right
	} else if m.loaded == loadedUnreachable {
		right = unreachableBadge.Render(" UNREACHABLE ") + right
	} else if m.reloader != nil {
		right = unknownBadge.Render(" UNKNOWN ") + right
	}
	// The unsaved badge is the leftmost marker: it is the most immediate
	// state and is an explicit text badge, never color alone.
	if m.hasUnsavedEdits() {
		right = unsavedBadge.Render(" UNSAVED ") + right
	}

	rightW := lipgloss.Width(right)
	leftW := lipgloss.Width(left)
	separatorW := lipgloss.Width(separator)

	// Reserve a minimum path width; drop the caddy version badge first
	// when space gets tight.
	const minPathW = 8
	caddyW := lipgloss.Width(caddyVersion)
	available := contentWidth - leftW - caddyW - rightW - separatorW*2
	if caddyVersion != "" && available < minPathW {
		caddyVersion = ""
		available = contentWidth - leftW - rightW - separatorW
	}
	if caddyVersion != "" {
		left += separator + caddyVersion
	}
	leftW = lipgloss.Width(left)
	available = contentWidth - leftW - rightW - separatorW
	if available < 0 {
		available = 0
	}

	const configLabel = "Config: "
	pathAvailable := available - lipgloss.Width(configLabel)
	if pathAvailable < 0 {
		pathAvailable = 0
	}
	displayedPath := path
	if lipgloss.Width(path) > pathAvailable {
		displayedPath = truncateToWidth(path, pathAvailable)
	}
	if displayedPath == "" {
		displayedPath = "—"
	}
	pathBlock := dimStyle.Render(configLabel + displayedPath)
	pathW := lipgloss.Width(pathBlock)

	pad := contentWidth - leftW - separatorW - pathW - rightW
	if pad < 0 {
		pad = 0
	}

	line := left + separator + pathBlock + strings.Repeat(" ", pad) + right
	return renderLineOnSurface(" "+line, width, chromeBackground) + "\n"
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
		// height is the content height of the pane, and the title consumes
		// one of those rows. Rendering height tree rows in addition to the
		// title makes an expanded tree overflow the pane; the terminal then
		// scrolls the top of the whole application out of view.
		visibleRows := height - 1
		if visibleRows < 1 {
			visibleRows = 1
		}
		start := m.cursor - visibleRows/2
		if start < 0 {
			start = 0
		}
		end := start + visibleRows
		if end > len(m.items) {
			end = len(m.items)
		}
		for i := start; i < end; i++ {
			if i > start {
				body.WriteByte('\n')
			}
			body.WriteString(renderTreeRow(m.items[i], i == m.cursor))
		}
	}
	return focusedPaneStyle.Width(width).Height(height).Render(activeTitleStyle.Render(title) + "\n" + body.String())
}

// renderTreeRow renders one visible tree row: a fixed selector gutter first,
// followed by the hierarchy indent, the expansion/leaf marker and the label.
// The selector remains visible at the left edge while branch and leaf markers
// follow the tree hierarchy.
func renderTreeRow(it item, selected bool) string {
	sel := "  "
	if selected {
		sel = "› "
	}
	exp := "  "
	if it.hasChildren {
		exp = "+ " // collapsed branch
		if !it.collapsed {
			exp = "- " // expanded branch
		}
	} else if it.hasNode {
		exp = "· " // visible leaf row
	}
	row := fmt.Sprintf("%s%s%s%s", sel, strings.Repeat("  ", it.depth), exp, it.label)
	if selected {
		return selectedTreeRowStyle.Render(row)
	}
	return row
}

// sourcePane renders the raw, unmodified source of the selected
// item's document inside a scrollable viewport. Unknown directives,
// comments and malformed regions are all shown exactly as stored; the
// viewport truncates the output to the pane height instead of
// overflowing the terminal.
func (m *Model) sourcePane(srcW, paneH int) string {
	m.syncSource(srcW, paneH)
	content := m.viewport.View()
	if spans, ok := m.selectionSpans(textPaneSource); ok {
		content = renderSelectionOverlay(content, m.viewport.Width, m.viewport.Height, spans)
	}
	return paneStyle.Width(srcW).Height(paneH).Render(dimStyle.Render(m.sourceTitle) + "\n" + content)
}

// syncSource keeps the source viewport sized to the pane and refreshes
// its content whenever the selection or the pane dimensions change.
func (m *Model) syncSource(srcW, paneH int) {
	contentW := srcW - 4 // border (2) + horizontal padding (2)
	if contentW < 1 {
		contentW = 1
	}
	// paneContentH already removes the pane's border and padding. Only
	// the source title and its separator consume rows inside that content
	// area; subtracting the frame here again loses two visible source rows.
	contentH := paneH - 2 // title (1) + blank separator (1)
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
		} else if selected.comment != nil {
			title += fmt.Sprintf(" · comment (lines %d-%d)", selected.comment.StartLine, selected.comment.EndLine)
		}
	}
	// Refresh the advisory inline-findings cache whenever the line count is
	// shown below, recomputing only on a document or source change (a steady
	// selection reuses the cache so this is not per-frame work).
	m.syncInlineFindings(doc)

	// The authoritative caddy diagnostics for this document, when the last
	// validate outcome covers it and is not stale. The overlay is derived
	// from the same outcome the review's CADDY VALIDATION section shows, so
	// the two surfaces never disagree.
	diags := m.caddyDiagsForDoc(doc)

	// The finding summary lives in the pane title, not the temporary status
	// strip: it is scoped to the selected document and survives transient
	// status messages. On narrow terminals it degrades to a short form.
	title = m.sourceTitleWithFindings(title, doc)

	// Build the selection key first: it carries the 1-based range used
	// both for highlighting the source gutter and for revealing the node.
	key := selectionKey{doc: doc}
	if selected != nil && selected.hasNode {
		key.hasNode = true
		key.node = selected.node.Name
		key.start = selected.node.Range.StartLine
		key.end = selected.node.Range.EndLine
	} else if selected != nil && selected.comment != nil {
		key.hasNode = true
		key.node = "comment"
		key.start = selected.comment.StartLine
		key.end = selected.comment.EndLine
	}

	// A refresh (set by a save) forces the content reload and the reveal
	// even when the selection key is unchanged; it is consumed here so it
	// applies to exactly one render. A changed caddy diagnostic overlay (a
	// fresh validate outcome, or a result flagged stale after an edit)
	// rebuilds the content too, so the 'E' markers and token highlights
	// appear or disappear without a selection change.
	refresh := m.sourceRefresh
	m.sourceRefresh = false
	prevDoc := m.sourceDoc
	prevSel := m.lastSel
	prevFoldVer := m.lastFoldLayoutVersion
	diagsChanged := !slices.Equal(m.lastCaddyDiags, diags)

	// A pending reveal (a selection change, a search hit, a diagnostic
	// or matcher jump, a save re-anchor) auto-expands every fold that
	// hides it before the content decision, so the rebuilt layout and
	// content reflect the expansion and the reveal lands on visible rows.
	// Folds that do not cover the target stay untouched, so fold state
	// remains stable across unrelated selections.
	if key != prevSel || refresh || m.sourceRevealLine > 0 {
		m.expandFoldsForReveal(doc, m.sourceRevealLine, key)
	}
	layout := m.foldLayoutFor(doc)

	// The folded view state is part of the title so a collapsed document
	// is never mistaken for an empty one: closing folds reports how many
	// blocks are hidden. It is computed after the reveal expansion above,
	// so the title always matches the content about to be rendered.
	if n := m.foldCount(doc); n > 0 {
		title += fmt.Sprintf(" · %d fold(s)", n)
	}

	needsContent := refresh || doc != m.sourceDoc || key != m.lastSel || diagsChanged ||
		m.foldVersion != prevFoldVer
	if needsContent {
		// The source pane is about to render different content: any text
		// selection anchored in the previous document or node is stale.
		if m.textSel.pane == textPaneSource {
			m.clearTextSelection()
		}
		m.lastFoldLayoutVersion = m.foldVersion
		m.sourceDoc = doc
		m.lastSel = key
		m.sourceTitle = title
		var src []byte
		if doc != nil {
			src = doc.Source
		}
		m.viewport.SetContent(numberedSource(src, key.start, key.end, m.inlineFindings, diags, layout))
		m.lastCaddyDiags = diags
		if doc != prevDoc && !refresh {
			// New document: start at the top; revealRange then scrolls
			// just enough for the selected node. A save refresh stays put
			// and re-reveals the selection below instead.
			m.viewport.GotoTop()
		}
	} else if title != m.sourceTitle {
		m.sourceTitle = title
	}

	// Reveal-if-needed, but only when the selection changed, the source
	// was refreshed, or a search activated a line: after a manual scroll
	// the viewport must stay where the user left it, while a save or a
	// search activation must re-position the viewport on the selected
	// node / line. A one-shot search-activated line (a document content
	// hit or an import-directive hit, which selects its document row) takes
	// precedence over the node reveal so
	// the exact hit line is always shown.
	if key != prevSel || refresh || m.sourceRevealLine > 0 {
		if m.sourceRevealLine > 0 {
			// Centre the single reveal line in the viewport (the natural
			// clamp keeps it on screen near the file start or end), matching
			// every other reveal. The row conversion follows the folded
			// layout, and the covering folds were expanded above.
			row := m.rowForLine(m.sourceRevealLine)
			m.viewport.SetYOffset(row - m.viewport.Height/2)
			m.sourceRevealLine = 0
		} else if key.hasNode {
			m.revealRange(key.start, key.end)
		} else {
			// Returning to a document row: reset the source view to the top
			// (the "home" position) instead of keeping a stale node reveal.
			m.viewport.GotoTop()
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

// revealRange scrolls the source viewport so the 1-based source lines
// [startLine, endLine] are shown centred: a range that fits the viewport
// is centred on its midpoint, a taller range shows its start with a
// little context above it, and both clamp naturally to the file bounds
// (SetYOffset clamps to the content height). The line-to-row conversion
// follows the folded display layout, so a selection inside a folded
// region scrolls to the region's visible rows (the covering folds were
// expanded by the caller). It runs only when the selection changes or a
// reveal is requested, never during a manual scroll, so the operator
// keeps control of the viewport while browsing.
func (m *Model) revealRange(startLine, endLine int) {
	startRow := m.rowForLine(startLine)
	endRow := m.rowForLine(endLine)
	if endRow < startRow {
		endRow = startRow
	}
	rangeLen := endRow - startRow + 1
	var target int
	if rangeLen <= m.viewport.Height {
		// The range fits: centre its midpoint in the viewport.
		target = startRow - (m.viewport.Height-rangeLen)/2
	} else {
		// Too tall to centre: show the start with a little context above.
		target = startRow - m.viewport.Height/3
	}
	m.viewport.SetYOffset(target)
}

// selectedItem returns the item under the cursor, or nil.
func (m *Model) selectedItem() *item {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	return &m.items[m.cursor]
}

// statusStrip renders the current statusMessage in a dedicated strip
// above the footer. The style is chosen by the leading glyph: ✓ for
// success, ✗ for error (or warning when the message mentions warnings),
// and anything else is shown in the dim info style.
func (m *Model) statusStrip(width int) string {
	msg := m.statusMessage
	switch {
	case strings.HasPrefix(msg, "✓"):
		return renderLineOnSurface(statusSuccessStyle.Render(msg), width, statusBackground)
	case strings.HasPrefix(msg, "✗") && strings.Contains(msg, "warnings"):
		return renderLineOnSurface(statusWarningStyle.Render(msg), width, statusBackground)
	case strings.HasPrefix(msg, "✗"):
		return renderLineOnSurface(statusErrorStyle.Render(msg), width, statusBackground)
	default:
		return renderLineOnSurface(statusInfoStyle.Render(msg), width, statusBackground)
	}
}

func (m *Model) footer(width int) string {
	var keys string
	switch {
	case m.showUnsavedConfirm:
		keys = "s save · d discard & quit · Esc cancel"
	case m.showChangeConflict:
		if m.changeCompare {
			keys = "↑/↓ scroll · PgUp/PgDown page · n/N hunk · h/l scroll · Esc back"
		} else if m.hasUnsavedEdits() {
			keys = "r reload (discards unsaved edits) · c compare · k/Esc keep"
		} else {
			keys = "r reload · Esc keep"
		}
	case m.showDiff:
		if m.pendingRollback != nil {
			keys = "↑/↓ scroll · PgUp/PgDown page · n/N hunk · h/l scroll · Enter rollback · Esc cancel"
		} else if m.pendingDelete != nil {
			keys = "↑/↓ scroll · PgUp/PgDown page · n/N hunk · h/l scroll · Enter delete · Esc cancel"
		} else if m.pendingEdit != nil {
			keys = "↑/↓ scroll · PgUp/PgDown page · n/N hunk · h/l scroll · Enter " + pendingEditVerb(m.pendingEdit) + " · Esc discard"
		} else {
			keys = "↑/↓ scroll · PgUp/PgDown page · n/N hunk · h/l scroll · Esc close"
		}
	case m.showSaveConfirm:
		keys = "Enter save · Esc cancel"
	case m.showReloadConfirm:
		keys = "Enter reload · Esc cancel"
	case m.showStructuredAdd:
		if m.structuredAddMode == structuredAddReorder {
			keys = "↑/↓ choose sibling · Enter move after & validate · Esc cancel"
		} else if m.structuredAddMode == structuredAddCommentPlacement {
			keys = "↑/↓ choose placement · Enter open editor · Esc cancel"
		} else {
			keys = "type directive · Enter plan & validate · Esc cancel"
		}
	case m.showRollbackConfirm:
		keys = "Enter rollback · Esc cancel"
	case m.showCommandPalette:
		keys = "↑/↓ navigate · PgUp/PgDown scroll · Enter run · Esc close"
	case m.showBackups:
		if m.canRollback() {
			keys = "↑/↓ move · Enter/→ compare & rollback · Esc close"
		} else {
			keys = "↑/↓ move · Enter/→ compare · Esc close"
		}
	case m.showDetail:
		keys = "↑/↓ scroll · PgUp/PgDown page · Esc/← back"
	case m.showDiagnostics:
		keys = "↑/↓ navigate · Enter/+ or → detail · Esc/← close"
	case m.logDetailOpen:
		keys = "↑/↓ scroll · PgUp/PgDown page · Esc/← back · q quit"
	case m.showLogs:
		keys = "↑/↓ move · PgUp/PgDown page · Enter/→ detail · f follow (on/off) · p pause/resume · Esc close · q quit"
	case m.searchActive:
		keys = "type to search · ↑/↓ move · PgUp/PgDown page · Enter open · Esc close"
	case m.showErrorHistory:
		keys = "↑/↓ scroll · PgUp/PgDown page · Esc close"
	case m.showInlineReview:
		keys = "↑/↓ move · Enter reveal · → detail · v validate · Esc close"
	case m.state != nil && m.state.Graph != nil:
		// The normal footer is deliberately navigation-only. Operational
		// actions remain available through their direct hotkeys and the
		// searchable command palette opened by ?. Expand/collapse is
		// advertised only when the selected row has children.
		navKeys := "↑/↓ move"
		if sel := m.selectedItem(); sel != nil && sel.hasChildren {
			navKeys = "↑/↓ move · Enter toggle"
		}
		keys = navKeys + " · PgUp/PgDown · +/- all · ? commands"
	default:
		keys = "↑/↓ move · PgUp/PgDown · ? commands"
	}
	return renderLineOnSurface(footerStyle.Render(renderFooterKeys(keys)), width, chromeBackground)
}

// canRollback reports whether the backup-history modal may offer
// rollback for the selected backup: writable mode (a saver) plus a
// validation binary (a formatter). Listing and comparison stay available
// read-only.
func (m *Model) canRollback() bool {
	return m.rollbacker != nil && m.saver != nil && m.formatter != nil &&
		m.state != nil && !m.state.Settings.ReadOnly
}

// renderFooterKeys highlights key names with the accent color while
// keeping descriptions dim. The compact footer carries only navigation
// hints; the full action catalog lives in the command palette.
func renderFooterKeys(keys string) string {
	segments := strings.Split(keys, " · ")
	var parts []string
	for _, seg := range segments {
		idx := strings.IndexByte(seg, ' ')
		if idx <= 0 {
			parts = append(parts, keyHintStyle.Render(seg))
			continue
		}
		parts = append(parts, keyHintStyle.Render(seg[:idx])+dimStyle.Render(seg[idx:]))
	}
	return strings.Join(parts, dimStyle.Render(" · "))
}

// wrapText wraps text to fit within the given cell width, breaking on
// word boundaries when possible. A single word longer than the
// width is hard-broken on rune boundaries so multi-byte characters
// are never split. Short lines are not padded to the width: the
// result is suitable for a scrolling viewport where trailing
// spaces would be visible on the right.
func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	var b strings.Builder
	lineW := 0
	for _, word := range strings.Fields(text) {
		wW := lipgloss.Width(word)
		if wW > width {
			// Word longer than the width: hard-break on rune
			// boundaries.
			if lineW > 0 {
				b.WriteString("\n")
				lineW = 0
			}
			for _, r := range word {
				if lineW >= width {
					b.WriteString("\n")
					lineW = 0
				}
				b.WriteRune(r)
				lineW += lipgloss.Width(string(r))
			}
			continue
		}
		if lineW == 0 {
			b.WriteString(word)
			lineW = wW
		} else if lineW+1+wW <= width {
			b.WriteString(" ")
			b.WriteString(word)
			lineW += 1 + wW
		} else {
			b.WriteString("\n")
			b.WriteString(word)
			lineW = wW
		}
	}
	return b.String()
}

// numberedSource renders the source pane content: line numbers, the exact
// source bytes and syntax highlighting, with the given folded display
// layout applied (nil renders the full source). Optional advisory inline
// findings and authoritative caddy diagnostics are layered over the source
// when supplied, so suspicious parse-tree patterns and real validation
// errors stand out without changing any byte.
func numberedSource(src []byte, selStartLine, selEndLine int, findings []caddyfile.InlineFinding, diags []validator.Diagnostic, layout *caddyfile.FoldLayout) string {
	return renderHighlightedSource(src, selStartLine, selEndLine, findings, diags, layout)
}

// sourceTitleWithFindings appends the advisory finding summary to the source
// pane title, scoped to the selected document. With findings it reads
// e.g. "; 2 findings · [i] review" (as per the review view handover); without
// findings it reads "; advisory: clean". When the authoritative caddy
// outcome has errors on this document, the error count is appended as well
// (e.g. "; 2 caddy error(s)"). The summary lives in the title so transient
// status messages never overwrite it.
func (m *Model) sourceTitleWithFindings(base string, doc *caddyfile.Document) string {
	if doc == nil || doc.Err != nil || !m.inlineFindingsReady(doc) {
		if n := len(m.caddyDiagsForDoc(doc)); n > 0 {
			return base + fmt.Sprintf(" · %d caddy error(s)", n)
		}
		return base
	}
	if len(m.inlineFindings) == 0 {
		base += " · advisory: clean"
	} else {
		base += fmt.Sprintf(" · %d findings · [i] review", len(m.inlineFindings))
	}
	if n := len(m.caddyDiagsForDoc(doc)); n > 0 {
		base += fmt.Sprintf(" · %d caddy error(s)", n)
	}
	return base
}

// inlineFindingsReady reports whether the cached findings belong to the given
// document (same pointer and source), so the title never shows counts for a
// stale document.
func (m *Model) inlineFindingsReady(doc *caddyfile.Document) bool {
	return m.inlineFindingsDoc == doc && bytes.Equal(m.inlineFindingsSource, doc.Source)
}
