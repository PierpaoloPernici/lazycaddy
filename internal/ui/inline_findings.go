package ui

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	tea "github.com/charmbracelet/bubbletea"
)

// inlineCaddyState captures the last authoritative caddy validate outcome for
// the review view. It is separate from the advisory findings, persists across
// closing/reopening the view, and keeps the source it was computed against so
// the view can flag the result as stale after an edit or reload.
type inlineCaddyState struct {
	// phase is "not run", "result" or "stale".
	phase  string
	errors int
	// summary is a readable one-line description of the first/most relevant
	// error (e.g. "unrecognized matcher name: @phantom").
	summary string
	// details retains the full error diagnostics so the review can reopen the
	// Caddy diagnostics view (with line/column) even after the diagnostics
	// modal has been closed.
	details []validator.Diagnostic
	// source is a copy of the document source the outcome was computed
	// against; when the current document source differs, the result is stale.
	source []byte
}

// syncInlineFindings refreshes the advisory inline findings cache for the
// selected document. Findings are always computed when a reliable document is
// selected (Caddy is the authority on validity; these are advisory only). When
// no reliable document is available, or its source changed, the cache is
// recomputed so stale findings never linger. The recomputation is cheap and
// runs only on a document or source change, never per frame.
func (m *Model) syncInlineFindings(doc *caddyfile.Document) {
	if doc == nil || doc.Err != nil {
		m.inlineFindings = nil
		m.inlineFindingsDoc = doc
		m.inlineFindingsSource = nil
		return
	}
	sourceChanged := m.inlineFindingsDoc != doc || !bytes.Equal(m.inlineFindingsSource, doc.Source)
	if !sourceChanged {
		return // steady selection, cache is valid
	}
	m.inlineFindings = caddyfile.InlineProblems(doc)
	m.inlineFindingsDoc = doc
	m.inlineFindingsSource = append([]byte(nil), doc.Source...)
	m.markInlineCaddyStaleIfNeeded(doc)
}

// markInlineCaddyStaleIfNeeded flags a previously computed caddy validate
// result (phase "result") as stale when the selected document source no longer
// matches the source the result was computed against.
func (m *Model) markInlineCaddyStaleIfNeeded(doc *caddyfile.Document) {
	if m.inlineCaddy == nil || m.inlineCaddy.phase != "result" {
		return
	}
	if !bytes.Equal(m.inlineCaddy.source, doc.Source) {
		m.inlineCaddy.phase = "stale"
	}
}

// caddyDiagsForDoc returns the authoritative caddy validate diagnostics that
// belong to the given document, or nil when the outcome is unavailable
// (never validated, flagged stale after an edit or reload, no reported
// lines, or no diagnostics for this path). Paths are matched with
// filepath.Clean so the display path and the graph document paths agree
// regardless of minor separator differences; a diagnostic whose path cannot
// be matched is never overlaid on another document's lines. Diagnostics
// caddy reported without any position (Line 0, e.g. "unrecognized matcher
// name: @phantom") are pinned onto the token they name in the document
// source as a best-effort presentation mapping; an unpinnable one is
// simply not overlaid. The overlay is driven by the same outcome as the
// review's CADDY VALIDATION section, so the two surfaces never disagree.
func (m *Model) caddyDiagsForDoc(doc *caddyfile.Document) []validator.Diagnostic {
	if doc == nil || m.inlineCaddy == nil || m.inlineCaddy.phase != "result" {
		return nil
	}
	if len(m.inlineCaddy.details) == 0 {
		return nil
	}
	docPath := filepath.Clean(doc.Path)
	var out []validator.Diagnostic
	for _, d := range m.inlineCaddy.details {
		if filepath.Clean(d.Path) != docPath {
			continue
		}
		if d.Line > 0 {
			out = append(out, d)
			continue
		}
		if line, col := pinDiagnostic(doc.Source, d.Message); line > 0 {
			pinned := d
			pinned.Line = line
			pinned.Column = col
			out = append(out, pinned)
		}
	}
	return out
}

// openInlineReview opens the interactive "Review inline findings" view. It
// computes the findings for the current document if not cached yet and starts
// the list cursor at the first row. It never runs caddy validate implicitly.
func (m *Model) openInlineReview() {
	m.syncInlineFindings(m.sourceDoc)
	m.showInlineReview = true
	m.inlineReviewCursor = 0
	if m.inlineCaddy == nil {
		m.inlineCaddy = &inlineCaddyState{phase: "not run"}
	}
}

// closeInlineReview dismisses the review view.
func (m *Model) closeInlineReview() {
	m.showInlineReview = false
	m.inlineReviewCursor = 0
}

// returnToInlineReviewIfNeeded restores the review view after a caddy
// validate launched from the review completes. When the validation opened the
// diagnostics view (errors with details) the restore is deferred until that
// view closes; otherwise the review is reopened immediately so the outcome is
// visible.
func (m *Model) returnToInlineReviewIfNeeded() {
	if !m.inlineReviewReturn {
		return
	}
	if m.showDiagnostics {
		// The diagnostics view is open; closing it restores the review.
		return
	}
	m.inlineReviewReturn = false
	m.openInlineReview()
}

// inlineReviewRowCount returns the number of navigable rows in the review:
// one per advisory finding, plus the Caddy validation rows (one per error
// diagnostic, or a single state row for not run / stale / clean).
func (m *Model) inlineReviewRowCount() int {
	return len(m.inlineFindings) + m.caddyReviewRowCount()
}

// caddyReviewRowCount returns the number of CADDY VALIDATION rows: one per
// error diagnostic when an outcome with errors is available, otherwise a
// single state row (not run / stale / clean).
func (m *Model) caddyReviewRowCount() int {
	if m.inlineCaddy == nil {
		return 1
	}
	if m.inlineCaddy.phase == "result" && len(m.inlineCaddy.details) > 0 {
		return len(m.inlineCaddy.details)
	}
	return 1
}

// updateInlineReviewKey handles keys while the review view is open.
func (m *Model) updateInlineReviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "i":
		m.closeInlineReview()
	case "up", "k":
		if m.inlineReviewCursor > 0 {
			m.inlineReviewCursor--
		}
	case "down", "j":
		if m.inlineReviewCursor < m.inlineReviewRowCount()-1 {
			m.inlineReviewCursor++
		}
	case "enter":
		return m.activateInlineReviewRow()
	case "right":
		// → opens the full diagnostic detail for a Caddy row, following the
		// master-detail convention; advisory rows have no deeper detail.
		return m.openCaddyReviewDetail()
	case "v":
		// Reuse the authoritative caddy validate workflow. Mark that the review
		// launched it so the review is restored when the outcome arrives
		// instead of returning to home.
		m.inlineReviewReturn = true
		m.closeInlineReview()
		return m.startFormatAndValidate()
	}
	return m, nil
}

// activateInlineReviewRow handles Enter in the review view: revealing an
// advisory finding line, or selecting the diagnostic's document and
// revealing its line / pinned token for a Caddy row. On a state row (not
// run / stale / clean) it simply closes the review.
func (m *Model) activateInlineReviewRow() (tea.Model, tea.Cmd) {
	findings := len(m.inlineFindings)
	if m.inlineReviewCursor >= findings {
		return m.activateCaddyReviewRow(m.inlineReviewCursor - findings)
	}
	m.revealInlineFinding()
	m.closeInlineReview()
	return m, nil
}

// activateCaddyReviewRow handles Enter on a Caddy validation row: it
// selects the diagnostic's document and reveals the error line or pinned
// token in the source pane, closing the review (mirroring the advisory
// rows). State rows (not run / stale / clean) close without revealing.
func (m *Model) activateCaddyReviewRow(idx int) (tea.Model, tea.Cmd) {
	m.closeInlineReview()
	if m.inlineCaddy == nil || m.inlineCaddy.phase != "result" {
		return m, nil
	}
	if idx < 0 || idx >= len(m.inlineCaddy.details) {
		return m, nil
	}
	m.revealCaddyDiagnostic(m.inlineCaddy.details[idx])
	return m, nil
}

// openCaddyReviewDetail opens the full diagnostic detail for the Caddy row
// under the cursor (→). It reuses the diagnostics modal: the cursor is
// positioned on the selected diagnostic and the detail view opens
// immediately; closing returns to the review list.
func (m *Model) openCaddyReviewDetail() (tea.Model, tea.Cmd) {
	findings := len(m.inlineFindings)
	if m.inlineReviewCursor < findings || m.inlineCaddy == nil {
		return m, nil
	}
	idx := m.inlineReviewCursor - findings
	if m.inlineCaddy.phase != "result" || idx < 0 || idx >= len(m.inlineCaddy.details) {
		return m, nil
	}
	m.diagnostics = append([]validator.Diagnostic(nil), m.inlineCaddy.details...)
	m.diagCursor = idx
	m.showDiagnostics = true
	m.inlineReviewReturn = true
	m.openDetail()
	m.clearTextSelection()
	return m, nil
}

// revealCaddyDiagnostic selects the document of a caddy diagnostic (matched
// by clean path), expands the tree to the row containing the diagnostic's
// line (or the token pinned from its message when caddy reported no
// position) and reveals it in the source pane, where the E marker and the
// authoritative red styling show. It reports whether the diagnostic could
// be resolved; an unresolvable one leaves the selection untouched.
func (m *Model) revealCaddyDiagnostic(d validator.Diagnostic) bool {
	if m.state == nil || m.state.Graph == nil {
		if d.Line > 0 {
			m.sourceRevealLine = d.Line
			return true
		}
		return false
	}
	doc := m.docAtPath(d.Path)
	if doc == nil {
		return false
	}
	line := d.Line
	if line <= 0 {
		line, _ = pinDiagnostic(doc.Source, d.Message)
	}
	if line <= 0 {
		return false
	}
	// structuralNodeAtLine returns the deepest rendered (tree-row) node, so
	// the row always exists in the rebuilt tree; expand the containing
	// ancestors first so it is visible even when collapsed, then select it
	// (the document row when the line falls outside every tree row),
	// mirroring the search activation.
	if n := structuralNodeAtLine(doc, line); n != nil {
		expandNodeAncestors(doc, *n, m.collapsed)
		m.rebuildTree(itemKey(doc, n))
	} else {
		m.rebuildTree(itemKey(doc, nil))
	}
	m.sourceRevealLine = line
	m.clearTextSelection()
	return true
}

// revealFirstCaddyError selects the document of the first authoritative
// error diagnostic that resolves to a source line (positioned or pinned)
// and reveals it, so a failed v from the main view lands the operator on
// the first problem without hunting. The full list stays available in the
// i review.
func (m *Model) revealFirstCaddyError() {
	if m.inlineCaddy == nil {
		return
	}
	for _, d := range m.inlineCaddy.details {
		if m.revealCaddyDiagnostic(d) {
			return
		}
	}
}

// docAtPath returns the graph document whose clean path matches path, or
// nil when the graph is unavailable or the path is not part of it.
func (m *Model) docAtPath(path string) *caddyfile.Document {
	if m.state == nil || m.state.Graph == nil {
		return nil
	}
	clean := filepath.Clean(path)
	for _, d := range m.state.Graph.Documents {
		if filepath.Clean(d.Path) == clean {
			return d
		}
	}
	return nil
}

// caddyDiagDisplayLine returns the 1-based source line of a caddy
// diagnostic for display: the reported line, or the token pinned from the
// message when caddy reported no position (0 when it cannot be pinned).
func (m *Model) caddyDiagDisplayLine(d validator.Diagnostic) int {
	if d.Line > 0 {
		return d.Line
	}
	if doc := m.docAtPath(d.Path); doc != nil {
		line, _ := pinDiagnostic(doc.Source, d.Message)
		return line
	}
	return 0
}

// caddyDiagPathLabel renders the document path of a caddy diagnostic
// relative to the root Caddyfile's directory when the diagnostic lives
// under it (so an import shows e.g. "snippets/auth.caddy"), falling back to
// the full path when the diagnostic refers outside the root directory or
// the relative form would be ambiguous.
func (m *Model) caddyDiagPathLabel(d validator.Diagnostic) string {
	if m.state == nil || m.state.Settings.ConfigPath == "" {
		return d.Path
	}
	rootDir := filepath.Dir(filepath.Clean(m.state.Settings.ConfigPath))
	rel, err := filepath.Rel(rootDir, d.Path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return d.Path
	}
	return rel
}

// revealInlineFinding re-anchors the tree on the node containing the finding
// under the cursor and reveals its source line.
func (m *Model) revealInlineFinding() {
	if m.inlineReviewCursor < 0 || m.inlineReviewCursor >= len(m.inlineFindings) {
		return
	}
	f := m.inlineFindings[m.inlineReviewCursor]
	if m.sourceDoc == nil || m.state == nil || m.state.Graph == nil {
		m.sourceRevealLine = f.StartLine
		return
	}
	if n := structuralNodeAtLine(m.sourceDoc, f.StartLine); n != nil {
		expandNodeAncestors(m.sourceDoc, *n, m.collapsed)
		if nodeIsTreeRow(n) {
			m.rebuildTree(itemKey(m.sourceDoc, n))
		} else if parent := nearestVisibleAncestor(m.sourceDoc, *n); parent != nil {
			m.rebuildTree(itemKey(m.sourceDoc, parent))
		} else {
			m.rebuildTree(itemKey(m.sourceDoc, nil))
		}
	} else {
		m.rebuildTree(itemKey(m.sourceDoc, nil))
	}
	m.sourceRevealLine = f.StartLine
}

// inlineReviewView renders the interactive review of advisory findings and the
// separate Caddy validation outcome. It is byte-lossless (it never modifies the
// document source) and does not reuse the temporary status strip for the main
// summary.
func (m *Model) inlineReviewView(width, height int) string {
	paneContentW := width - paneStyle.GetVerticalFrameSize()
	if paneContentW < 1 {
		paneContentW = 1
	}
	title := activeTitleStyle.Render(fmt.Sprintf("Inline findings (%d)", len(m.inlineFindings)))
	// The title carries only the view name and count (no command hints, which
	// live once in the system footer), styled like every other view title (accent
	// foreground, no background) with a blank line before the content.
	bodyH := height - 6 // title (1) + blank (1) + border (2) + separators (2)
	if bodyH < 1 {
		bodyH = 1
	}
	var body strings.Builder
	body.WriteString(dimStyle.Render("ADVISORY") + "\n")
	body.WriteString(m.inlineAdvisoryBlock(bodyH))
	body.WriteString("\n" + dimStyle.Render("CADDY VALIDATION") + "\n")
	body.WriteString(m.inlineCaddyBlock(bodyH))
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(
		title + "\n\n" + body.String(),
	)
}

// inlineAdvisoryBlock renders the list of advisory findings (or an empty
// state) for the current cursor window.
func (m *Model) inlineAdvisoryBlock(maxRows int) string {
	var b strings.Builder
	if len(m.inlineFindings) == 0 {
		b.WriteString("  " + dimStyle.Render("no advisory findings") + "\n")
		return b.String()
	}
	start := m.inlineReviewCursor - maxRows/2
	if start < 0 {
		start = 0
	}
	if start > len(m.inlineFindings) {
		start = len(m.inlineFindings)
	}
	end := start + maxRows
	if end > len(m.inlineFindings) {
		end = len(m.inlineFindings)
	}
	for i := start; i < end; i++ {
		f := m.inlineFindings[i]
		label := inlineReviewLabel(f.Severity)
		if i == m.inlineReviewCursor {
			b.WriteString(cursorStyle.Render("> ") + label + " · line " + strconv.Itoa(f.StartLine) + "\n")
			b.WriteString("    " + dimStyle.Render(f.Message) + "\n")
		} else {
			b.WriteString("  " + label + " · line " + strconv.Itoa(f.StartLine) + "\n")
			b.WriteString("    " + dimStyle.Render(f.Message) + "\n")
		}
	}
	return b.String()
}

// inlineCaddyBlock renders the CADDY VALIDATION rows: one row per error
// diagnostic (with its line and relative path) when an outcome with errors
// is available, otherwise a single state row (not run / stale / clean).
func (m *Model) inlineCaddyBlock(maxRows int) string {
	c := m.inlineCaddy
	if c == nil {
		c = &inlineCaddyState{phase: "not run"}
	}
	findings := len(m.inlineFindings)
	var b strings.Builder
	if c.phase == "result" && len(c.details) > 0 {
		// The cursor is bounded by the review row count, so start never
		// exceeds the details length; only the window end needs clamping.
		start := m.inlineReviewCursor - findings - maxRows/2
		if start < 0 {
			start = 0
		}
		end := start + maxRows
		if end > len(c.details) {
			end = len(c.details)
		}
		for i := start; i < end; i++ {
			b.WriteString(m.inlineCaddyRow(c.details[i], m.inlineReviewCursor == findings+i))
		}
		return b.String()
	}
	b.WriteString(m.inlineCaddyStateRow(c, m.inlineReviewCursor == findings))
	return b.String()
}

// inlineCaddyRow renders one caddy diagnostic row: an "E error · line N ·
// path" headline (the marker as a gutter badge like the source pane, the
// path relative to the root Caddyfile directory) with the diagnostic
// message indented below, mirroring the advisory rows.
func (m *Model) inlineCaddyRow(d validator.Diagnostic, selected bool) string {
	label := caddySeverityLabel(d.Severity)
	line := m.caddyDiagDisplayLine(d)
	pathLabel := m.caddyDiagPathLabel(d)
	head := label
	if line > 0 {
		head += " · line " + strconv.Itoa(line)
	}
	if pathLabel != "" {
		head += " · " + pathLabel
	}
	if selected {
		return cursorStyle.Render("> ") + head + "\n    " + dimStyle.Render(d.Message) + "\n"
	}
	return "  " + head + "\n    " + dimStyle.Render(d.Message) + "\n"
}

// caddySeverityLabel renders the caddy severity marker as the same gutter
// badge used in the source pane, followed by the severity word (trimmed:
// the word styles carry horizontal padding).
func caddySeverityLabel(sev validator.Severity) string {
	switch sev {
	case validator.SeverityWarning:
		return gutterMarkerBadge('W') + " " + strings.TrimSpace(syntaxCaddyWarningStyle.Render("warning"))
	default:
		return gutterMarkerBadge('E') + " " + strings.TrimSpace(errorStyle.Render("error"))
	}
}

// inlineCaddyStateRow renders the single state row of the CADDY VALIDATION
// section when no error diagnostics are listed: not run, clean or stale.
func (m *Model) inlineCaddyStateRow(c *inlineCaddyState, selected bool) string {
	var line string
	switch c.phase {
	case "result":
		line = statusSuccessStyle.Render("✓") + " caddy validation clean"
	case "stale":
		line = warningStyleText("Caddy validation: stale") + " — re-run v after the change"
	default: // not run
		line = dimStyle.Render("Caddy validation: not run")
	}
	if selected {
		return cursorStyle.Render("> ") + line + "\n"
	}
	return "  " + line + "\n"
}

func warningStyleText(s string) string {
	return errorStyle.Render(s)
}

// inlineReviewLabel returns the severity label for the review list: the
// marker as the same background badge used in the source-pane gutter,
// followed by the severity word, so the review and the source agree at a
// glance without relying on colour alone.
func inlineReviewLabel(sev caddyfile.InlineSeverity) string {
	switch sev {
	case caddyfile.SeverityAdvisoryHint:
		return gutterMarkerBadge('!') + " " + syntaxInlineHintStyle.Render("hint")
	case caddyfile.SeverityAdvisoryInfo:
		return gutterMarkerBadge('i') + " " + syntaxInlineInfoStyle.Render("info")
	default:
		return "? unknown"
	}
}

// inlineReviewFooter composes the review footer line: the advisory counts and
// the separate Caddy validation outcome.
func (m *Model) inlineReviewFooter() string {
	hints, infos := 0, 0
	for _, f := range m.inlineFindings {
		switch f.Severity {
		case caddyfile.SeverityAdvisoryHint:
			hints++
		case caddyfile.SeverityAdvisoryInfo:
			infos++
		}
	}
	advisory := fmt.Sprintf("Advisory: %d hint · %d info", hints, infos)
	return dimStyle.Render(advisory)
}

// setInlineCaddyOutcome records the authoritative caddy validate outcome so
// the review Caddy section can reflect it separately from the advisory
// findings. summary is a readable one-line description of the first error (may
// be empty when validation was clean). It stores the source the outcome was
// computed against so a later edit marks it stale, and retains the full error
// diagnostics so the review can reopen the Caddy diagnostics view with
// line/column details.
func (m *Model) setInlineCaddyOutcome(errors int, ok bool, src []byte, summary string, details []validator.Diagnostic) {
	if m.inlineCaddy == nil {
		m.inlineCaddy = &inlineCaddyState{}
	}
	m.inlineCaddy.phase = "result"
	if !ok {
		m.inlineCaddy.errors = 0
		m.inlineCaddy.summary = "validation failed"
		m.inlineCaddy.details = nil
	} else {
		m.inlineCaddy.errors = errors
		m.inlineCaddy.summary = summary
		m.inlineCaddy.details = append([]validator.Diagnostic(nil), details...)
	}
	m.inlineCaddy.source = append([]byte(nil), src...)
}
