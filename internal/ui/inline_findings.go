package ui

import (
	"bytes"
	"fmt"
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
// one per advisory finding, plus the Caddy validation row when an outcome exists.
func (m *Model) inlineReviewRowCount() int {
	n := len(m.inlineFindings)
	if m.inlineCaddy != nil {
		n++
	}
	return n
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
	case "v":
		// Reuse the authoritative caddy validate workflow. Mark that the review
		// launched it so the review is restored when the outcome arrives (or
		// when the diagnostics view closes) instead of returning to home.
		m.inlineReviewReturn = true
		m.closeInlineReview()
		return m.startFormatAndValidate()
	}
	return m, nil
}

// activateInlineReviewRow handles Enter in the review view: revealing an
// advisory finding line, or opening the Caddy diagnostics for a completed
// validation result row. On a "not run" / "stale" Caddy row (no diagnostics
// to show) it simply closes the review.
func (m *Model) activateInlineReviewRow() (tea.Model, tea.Cmd) {
	caddyRow := len(m.inlineFindings)
	if m.inlineReviewCursor == caddyRow && m.inlineCaddy != nil {
		if m.inlineCaddy.phase == "result" {
			// The user opened the details from the review, so restore the
			// review when the diagnostics view closes instead of returning home.
			m.inlineReviewReturn = true
			m.closeInlineReview()
			return m.openCaddyDiagnostics()
		}
		m.closeInlineReview()
		return m, nil
	}
	m.revealInlineFinding()
	m.closeInlineReview()
	return m, nil
}

// openCaddyDiagnostics switches to the existing Caddy diagnostics view when
// validation details are available. It restores the captured diagnostics (so
// Enter on the review's Caddy row works even after the modal was closed) and
// reveals the first error's line in the source pane.
func (m *Model) openCaddyDiagnostics() (tea.Model, tea.Cmd) {
	if m.inlineCaddy == nil {
		m.statusMessage = "no Caddy diagnostics to show — press v to validate"
		return m, nil
	}
	if len(m.inlineCaddy.details) == 0 {
		m.statusMessage = "no Caddy diagnostics to show — press v to validate"
		return m, nil
	}
	m.diagnostics = append([]validator.Diagnostic(nil), m.inlineCaddy.details...)
	m.diagCursor = 0
	m.showDiagnostics = true
	// Reveal the first error's line in the source pane, mirroring how the
	// advisory findings reveal their own line.
	if line := m.inlineCaddy.details[0].Line; line > 0 {
		m.sourceRevealLine = line
	}
	m.clearTextSelection()
	return m, nil
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
	title := activeTitleStyle.Render(fmt.Sprintf("INLINE FINDINGS (%d)", len(m.inlineFindings)))
	header := renderLineOnSurface(title, width-2, chromeBackground)
	// The navigation footer is rendered once by the system footer (View/footer),
	// so the pane carries no hint line; only the title, the two sections and the
	// pane border frame the content.
	bodyH := height - 5 // header (1) + border (2) + separators (2)
	if bodyH < 1 {
		bodyH = 1
	}
	var body strings.Builder
	body.WriteString(dimStyle.Render("ADVISORY") + "\n")
	body.WriteString(m.inlineAdvisoryBlock(bodyH))
	body.WriteString("\n" + dimStyle.Render("CADDY VALIDATION") + "\n")
	body.WriteString(m.inlineCaddyBlock(bodyH))
	return focusedPaneStyle.Width(width - paneStyle.GetVerticalFrameSize()).Height(height).Render(
		header + "\n" + body.String(),
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
			b.WriteString(cursorStyle.Render(" > ") + label + " · line " + strconv.Itoa(f.StartLine) + "\n")
			b.WriteString("    " + dimStyle.Render(f.Message) + "\n")
		} else {
			b.WriteString("  " + label + " · line " + strconv.Itoa(f.StartLine) + "\n")
			b.WriteString("    " + dimStyle.Render(f.Message) + "\n")
		}
	}
	return b.String()
}

// inlineCaddyBlock renders the Caddy validation section: either the outcome
// summary (with the error styled distinctly) or the "not run" placeholder. It
// is a separate navigable row at the end of the review list.
func (m *Model) inlineCaddyBlock(maxRows int) string {
	c := m.inlineCaddy
	if c == nil {
		c = &inlineCaddyState{phase: "not run"}
	}
	caddyRow := len(m.inlineFindings)
	selected := m.inlineReviewCursor == caddyRow
	switch c.phase {
	case "result":
		if c.errors > 0 {
			// A readable error summary with a distinct error marker/colour.
			line := errorStyle.Render("!") + " " + caddyOutcomeSummary(c)
			if c.summary != "" {
				line += "\n    " + dimStyle.Render(c.summary)
			}
			if selected {
				line = cursorStyle.Render(" > ") + line
			} else {
				line = "  " + line
			}
			return line + "\n"
		}
		if selected {
			return cursorStyle.Render(" > ") + statusSuccessStyle.Render("✓") + " caddy validation clean\n"
		}
		return "  " + statusSuccessStyle.Render("✓") + " caddy validation clean\n"
	case "stale":
		line := warningStyleText("Caddy validation: stale") + " — re-run v after the change"
		if selected {
			line = cursorStyle.Render(" > ") + line
		} else {
			line = "  " + line
		}
		return line + "\n"
	default: // not run
		line := dimStyle.Render("Caddy validation: not run")
		if selected {
			line = cursorStyle.Render(" > ") + line
		} else {
			line = "  " + line
		}
		return line + "\n"
	}
}

func warningStyleText(s string) string {
	return errorStyle.Render(s)
}

// caddyOutcomeSummary builds a readable line for a failing Caddy outcome:
// the error count plus the first error's message when available.
func caddyOutcomeSummary(c *inlineCaddyState) string {
	n := c.errors
	if n <= 0 {
		return "validation failed"
	}
	plural := "error"
	if n != 1 {
		plural = "errors"
	}
	return fmt.Sprintf("%d %s · press Enter for details", n, plural)
}

// inlineReviewLabel returns the severity label for the review list, styled to
// match the source-pane marker so hint/info are visually consistent without
// relying on colour alone.
func inlineReviewLabel(sev caddyfile.InlineSeverity) string {
	switch sev {
	case caddyfile.SeverityAdvisoryHint:
		return "! " + syntaxInlineHintStyle.Render("hint")
	case caddyfile.SeverityAdvisoryInfo:
		return "i " + syntaxInlineInfoStyle.Render("info")
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
