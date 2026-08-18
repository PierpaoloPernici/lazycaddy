package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) updateDiagnosticsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The detail view takes precedence over the list view.
	if m.showDetail {
		return m.updateDetailKey(msg)
	}
	switch msg.String() {
	case "esc", "q", "left":
		m.closeDiagnostics()
	case "up", "k":
		if m.diagCursor > 0 {
			m.diagCursor--
		}
	case "down", "j":
		if m.diagCursor < len(m.diagnostics)-1 {
			m.diagCursor++
		}
	// Enter, '+' and Right open the detail view for the diagnostic under
	// the cursor. '+' is a Vim-style alias and Right follows the
	// master-detail convention (→ opens a deeper detail); both are
	// intentionally no-ops outside the diagnostics modal.
	case "enter", "+", "right":
		m.openDetail()
	}
	return m, nil
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
	// The diagnostics must surface the real Caddyfile path, not the
	// temporary working file the validator runs against. m.state is
	// guaranteed non-nil by the caller (startFormatAndValidate checks
	// it), so ConfigPath is safe to capture here.
	displayPath := m.state.Settings.ConfigPath
	return func() tea.Msg {
		// Only apply a context timeout when the operator asked for one.
		// context.WithTimeout(parent, 0) returns a context that is
		// already past its deadline, which would cancel the validator
		// immediately and let its own 5s default never fire.
		ctx := context.Background()
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		formatted, diags, err := formatter.FormatAndValidate(ctx, displayPath, src)
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
	// FormatAndValidate returns the formatted working copy even when
	// validation fails. Retain it so the next diff/edit workflow can show
	// the candidate that produced the diagnostic without touching disk.
	if msg.Formatted != nil {
		m.workingBytes = msg.Formatted
	}
	if msg.Err != nil {
		// A failed validation must never be savable.
		m.workingValidated = false
		// Caddy emits info-level log lines alongside parse errors
		// (e.g. "INFO  using config from file"). Only error-level
		// diagnostics are actionable, so the review lists just those; if no
		// errors remain the status line surfaces the underlying error.
		var errors []validator.Diagnostic
		for _, d := range msg.Diagnostics {
			if d.Severity == validator.SeverityError {
				errors = append(errors, d)
			}
		}
		if len(errors) > 0 {
			// The operator must not hunt for the first problem: a failed v
			// from the main view selects the first authoritative error's
			// document and reveals its line / pinned token in the source
			// pane (the E marker and red token show there). The full list
			// stays available in the i review; the diagnostics modal is
			// still used by the delete/edit and review-detail flows.
			m.setInlineCaddyOutcome(len(errors), true, m.state.Graph.Root.Source, firstErrMessage(errors), errors)
			m.statusMessage = "✗ validation failed (working copy not saved)"
			if msg.Formatted != nil {
				m.statusMessage = "✗ validation failed (working copy retained, not saved)"
			}
			m.recordError("format & validate", "validation failed", "fix the reported errors and re-run v")
			m.returnToInlineReviewIfNeeded()
			if !m.showInlineReview {
				m.revealFirstCaddyError()
			}
			return m, nil
		}
		m.setInlineCaddyOutcome(0, false, m.state.Graph.Root.Source, "", nil)
		m.returnToInlineReviewIfNeeded()
		m.statusMessage = "✗ validation failed (working copy not saved): " + msg.Err.Error()
		m.recordError("format & validate", msg.Err.Error(), "fix the reported issue and re-run v")
		return m, nil
	}
	m.diagnostics = nil
	m.showDiagnostics = false
	m.workingValidated = true
	m.setInlineCaddyOutcome(0, true, m.state.Graph.Root.Source, "", nil)
	m.returnToInlineReviewIfNeeded()
	m.statusMessage = "✓ validated (working copy updated, not saved)"
	return m, nil
}

// closeDiagnostics dismisses the diagnostics modal and clears its
// state. Called by Esc and q from inside the modal. When a caddy validate (or
// diagnostics detail) was opened from the inline review, closing returns to the
// review instead of the home view.
func (m *Model) closeDiagnostics() {
	m.showDiagnostics = false
	m.diagnostics = nil
	m.diagCursor = 0
	if m.inlineReviewReturn {
		m.inlineReviewReturn = false
		m.openInlineReview()
	}
}

// diagnosticsView renders the validation results modal. It lists the
// diagnostics with a movable cursor; the caller is responsible for
// closing the modal through closeDiagnostics. The bottom footer shows
// the context-aware keys, so the pane itself carries no hint line.
func (m *Model) diagnosticsView(width, height int) string {
	title := fmt.Sprintf("Validation · %d diagnostic(s)", len(m.diagnostics))
	bodyH := height - 4 // border (2) + title (1) + blank line (1)
	if bodyH < 1 {
		bodyH = 1
	}
	// paneStyle has Border(RoundedBorder()) and Padding(0, 1):
	// Width(N) sets the *content* width to N, and the rendered total
	// is N + 2 (borders). To make the modal fit the window exactly
	// (matching the tree+source pane math elsewhere), pass
	// width - 2 here so the total comes out to width.
	//
	// Within the pane, the cursor prefix ("› " or "  ") eats 2 more
	// cells, so the available text width is width - 6. Truncate
	// each diagnostic string to that width to keep long messages
	// from pushing the pane past its right border.
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	textW := paneContentW - 2 - 2 // border (2) + padding (2) + cursor prefix (2)
	if textW < 1 {
		textW = 1
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
			line := truncateToWidth(d.String(), textW)
			if i == m.diagCursor {
				line = cursorStyle.Render("› " + line)
			} else {
				line = "  " + line
			}
			body.WriteString(line + "\n")
		}
	}
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(activeTitleStyle.Render(title) + "\n\n" + body.String())
}

// firstErrMessage returns the message of the first diagnostic (for the
// advisory review's Caddy summary), or "" when there is none.
func firstErrMessage(diags []validator.Diagnostic) string {
	if len(diags) == 0 {
		return ""
	}
	return diags[0].Message
}

// diagnosticDetailView renders the full diagnostic for the entry
// under the cursor. The path / line / column / severity are listed
// on fixed labels, then a blank line, then the message word-wrapped
// to the available body width via detailViewport. The viewport
// scroll position is preserved across renders because the body is
// only rebuilt when the cursor or the body size changes: SetContent
// resets the viewport scroll, so calling it on every render would
// make PgUp / PgDown unusable.
func (m *Model) diagnosticDetailView(width, height int) string {
	title := "Diagnostic detail · Esc back"
	// The pane has no hint line of its own: the bottom footer shows
	// the context-aware keys. The title keeps the "Esc back" hint so
	// the escape affordance is always visible inside the pane.

	// paneStyle has Border(RoundedBorder()) and Padding(0, 1):
	// Width(N) sets the *content* width to N, and the rendered total
	// is N + 2 (borders). To make the modal fit the window exactly
	// (matching the tree+source pane math and the diagnosticsView
	// fix from the previous milestone), pass width - 2 here so the
	// total is width.
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	bodyH := height - 4 // border (2) + title (1) + blank line (1)
	if bodyH < 1 {
		bodyH = 1
	}

	// Re-sync only when the size has changed since the last set
	// (e.g. the terminal was resized while the detail view was
	// open). Re-syncing resets the scroll; doing it on every
	// render would make PgUp / PgDown unusable.
	if m.detailViewport.Height != bodyH {
		m.syncDetailContent()
	}

	return focusedPaneStyle.Width(paneContentW).Height(height).Render(
		activeTitleStyle.Render(title) + "\n\n" + m.detailViewport.View(),
	)
}
