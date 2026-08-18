package ui

import (
	"fmt"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// toggleInlineFindings toggles the advisory inline validation overlay in the
// source pane (the i keybinding). When enabling, it recomputes the findings
// for the currently selected document and reports how many were found; when
// disabling, the overlay and cache are cleared. The overlay is advisory only:
// Caddy's own validation remains the authority and this never blocks a save,
// write or reload.
func (m *Model) toggleInlineFindings() {
	m.showInlineFindings = !m.showInlineFindings
	if !m.showInlineFindings {
		m.inlineFindings = nil
		m.inlineFindingsDoc = nil
		m.inlineFindingsSource = nil
		m.statusMessage = "inline findings: off"
		return
	}
	// Force a recompute on the next render for the current document.
	m.inlineFindingsDoc = nil
	doc := m.sourceDoc
	if doc == nil || doc.Err != nil {
		m.inlineFindings = nil
		m.statusMessage = "inline findings: on (no reliable document)"
		return
	}
	m.inlineFindings = caddyfile.InlineProblems(doc)
	m.inlineFindingsSource = append([]byte(nil), doc.Source...)
	if len(m.inlineFindings) == 0 {
		m.statusMessage = "inline findings: on (no suspicious patterns)"
	} else {
		m.statusMessage = fmt.Sprintf("inline findings: on (%d)", len(m.inlineFindings))
	}
}
