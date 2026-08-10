package ui

import (
	"context"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	tea "github.com/charmbracelet/bubbletea"
)

// startDelete begins the delete workflow for the selected node: the node's
// exact SourceRange is removed with caddyfile.Patch (every byte outside
// the range is preserved), the candidate is validated, and the result is
// reviewed in a diff before any write. Delete is a node operation only —
// document rows and import directives can never be deleted.
func (m *Model) startDelete() (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Graph == nil {
		return m, nil
	}
	if m.saver == nil || m.state.Settings.ReadOnly {
		m.statusMessage = "read-only mode — start with --write to enable saving"
		return m, nil
	}
	if m.saving || m.editing || m.busy || m.reloading || m.deleting || m.rollingBack {
		return m, nil
	}
	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.doc == nil {
		// Delete removes a node range; a document row has none.
		return m, nil
	}
	if sel.node.Kind == caddyfile.KindDirective && sel.node.Name == "import" {
		m.statusMessage = "✗ import directives cannot be deleted"
		return m, nil
	}
	if m.formatter == nil {
		m.statusMessage = "✗ validation unavailable — caddy binary not configured"
		return m, nil
	}
	working, err := caddyfile.Patch(sel.doc.Source, sel.node.Range, []byte{})
	if err != nil {
		m.statusMessage = "✗ delete failed: " + err.Error()
		return m, nil
	}
	m.deleting = true
	doc := sel.doc
	original := append([]byte(nil), doc.Source...)
	formatter := m.formatter
	return m, func() tea.Msg {
		_, diags, err := formatter.FormatAndValidate(context.Background(), doc.Path, working)
		return deleteValidatedMsg{Path: doc.Path, Original: original, Content: working, Diagnostics: diags, Err: err}
	}
}

// handleDeleteValidated routes the validated delete candidate. Error
// diagnostics have precedence over a companion error: FormatAndValidate
// returns both when Caddy rejects the configuration, and the modal must
// open. The error is an infrastructural failure (missing binary, timeout)
// only when no diagnostics accompany it. A clean candidate opens the
// delete diff, which is the single confirmation before the save.
func (m *Model) handleDeleteValidated(msg deleteValidatedMsg) (tea.Model, tea.Cmd) {
	m.deleting = false
	var errorDiags []validator.Diagnostic
	for _, d := range msg.Diagnostics {
		if d.Severity == validator.SeverityError {
			errorDiags = append(errorDiags, d)
		}
	}
	if len(errorDiags) > 0 {
		m.diagnostics = errorDiags
		m.diagCursor = 0
		m.showDiagnostics = true
		m.statusMessage = "✗ delete did not validate — not applied"
		m.recordError("delete", "delete did not validate", "fix the reported errors and retry the delete")
		return m, nil
	}
	if msg.Err != nil {
		m.statusMessage = "✗ delete validation failed: " + msg.Err.Error()
		m.recordError("delete", msg.Err.Error(), "fix the reported issue and retry the delete")
		return m, nil
	}
	if len(msg.Diagnostics) > 0 {
		m.statusMessage = "✗ delete has warnings — not applied"
		m.recordError("delete", "delete has warnings", "review the warnings and retry the delete")
		return m, nil
	}
	m.pendingDelete = &pendingDelete{path: msg.Path, original: msg.Original, content: msg.Content}
	lines, err := diff.Unified(msg.Original, msg.Content, msg.Path, msg.Path+" (after delete)")
	if err != nil {
		m.statusMessage = "✗ diff failed: " + err.Error()
		m.recordError("delete diff", err.Error(), "retry the delete")
		return m, nil
	}
	m.showDiffModal(lines, "Delete · "+msg.Path)
	return m, nil
}
