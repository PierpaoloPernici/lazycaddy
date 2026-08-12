package ui

import (
	"context"
	"errors"
	"os/exec"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	tea "github.com/charmbracelet/bubbletea"
)

// startEditor begins the $EDITOR round-trip for the selected node. It is
// gated on a configured editor, writable mode, a free busy state and a
// selected node. A document row (no node) has no range to edit, so the
// command is disabled there by design: there is no fallback to opening
// the whole file.
func (m *Model) startEditor() (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Graph == nil {
		return m, nil
	}
	if m.editor == nil {
		m.statusMessage = "✗ no editor configured (set $VISUAL or $EDITOR)"
		return m, nil
	}
	if m.state.Settings.ReadOnly || m.saver == nil {
		m.statusMessage = "read-only mode — start with --write to enable saving"
		return m, nil
	}
	if m.saving || m.editing || m.busy || m.reloading || m.deleting || m.rollingBack {
		return m, nil
	}
	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.doc == nil {
		return m, nil
	}
	m.editing = true
	m.statusMessage = "launching editor…"
	editor := m.editor
	doc := sel.doc
	r := sel.node.Range
	return m, func() tea.Msg {
		session, err := editor.Prepare(context.Background(), doc, r)
		if err != nil {
			return editorErrorMsg{Err: err}
		}
		return editorReadyMsg{Session: session}
	}
}

// startFullEdit begins the $EDITOR round-trip for the whole selected
// document (root or imported), mirroring startEditor's guards but with no
// node requirement: both a document row and a node row carry a doc. There
// is no fallback between the two commands — e is always the node edit and
// E is always the full-document edit.
func (m *Model) startFullEdit() (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Graph == nil {
		return m, nil
	}
	if m.editor == nil {
		m.statusMessage = "✗ no editor configured (set $VISUAL or $EDITOR)"
		return m, nil
	}
	if m.state.Settings.ReadOnly || m.saver == nil {
		m.statusMessage = "read-only mode — start with --write to enable saving"
		return m, nil
	}
	if m.saving || m.editing || m.busy || m.reloading || m.deleting || m.rollingBack {
		return m, nil
	}
	sel := m.selectedItem()
	if sel == nil || sel.doc == nil {
		return m, nil
	}
	m.editing = true
	m.statusMessage = "launching editor…"
	editor := m.editor
	doc := sel.doc
	return m, func() tea.Msg {
		session, err := editor.PrepareFull(context.Background(), doc)
		if err != nil {
			return editorErrorMsg{Err: err}
		}
		return editorReadyMsg{Session: session}
	}
}

// handleEditorReady stores the prepared session and returns a
// tea.ExecProcess command that runs the editor argv directly — never
// through a shell. Bubble Tea suspends the TUI while the editor runs and
// resumes it when the process exits.
func (m *Model) handleEditorReady(msg editorReadyMsg) (tea.Model, tea.Cmd) {
	if msg.Session == nil || len(msg.Session.Cmd) == 0 {
		m.editing = false
		m.editorSession = nil
		m.statusMessage = "✗ editor session could not start"
		return m, nil
	}
	m.editorSession = msg.Session
	cmd := exec.Command(msg.Session.Cmd[0], msg.Session.Cmd[1:]...)
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorExecMsg{Err: err}
	})
}

// handleEditorExec maps the exec outcome to an exit code and hands it to
// app.Editor.Complete. A non-zero exit (an *exec.ExitError) is treated as
// a cancellation by Complete; any other launch failure is kept for the
// status line.
func (m *Model) handleEditorExec(msg editorExecMsg) (tea.Model, tea.Cmd) {
	if m.editorSession == nil {
		m.editing = false
		return m, nil
	}
	exitCode := 0
	var execErr error
	if msg.Err != nil {
		var exitErr *exec.ExitError
		if errors.As(msg.Err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			execErr = msg.Err
			exitCode = -1
		}
	}
	editor := m.editor
	session := m.editorSession
	return m, func() tea.Msg {
		result, err := editor.Complete(context.Background(), session, exitCode)
		if err != nil {
			return editorErrorMsg{Err: err}
		}
		return editorDoneMsg{Result: result, ExecErr: execErr}
	}
}

// handleEditorDone routes the completed round-trip. Launch failures,
// cancellations and invalid results never reach the diff; a validated,
// changed result opens the diff modal, which is the single confirmation
// for saving the pending edit.
func (m *Model) handleEditorDone(msg editorDoneMsg) (tea.Model, tea.Cmd) {
	m.editing = false
	session := m.editorSession
	m.editorSession = nil
	if msg.ExecErr != nil {
		m.statusMessage = "✗ could not start editor: " + msg.ExecErr.Error()
		return m, nil
	}
	result := msg.Result
	switch {
	case result.Cancelled:
		m.statusMessage = "editor cancelled or empty result — nothing applied"
		if result.SnapshotPath != "" {
			m.statusMessage += " (recovery snapshot: " + result.SnapshotPath + ")"
		}
		m.recordError("editor", "cancelled or empty result — nothing applied", recoverySnapshotNext(result.SnapshotPath))
		return m, nil
	case len(result.Diagnostics) > 0:
		// The modal is for actionable findings, so non-error diagnostics
		// are filtered out, mirroring the format+validate flow.
		var errorDiags []validator.Diagnostic
		for _, d := range result.Diagnostics {
			if d.Severity == validator.SeverityError {
				errorDiags = append(errorDiags, d)
			}
		}
		if len(errorDiags) == 0 {
			// Only warnings or info-level findings survived the filter:
			// there is nothing actionable to list, so surface a status
			// line instead of an empty modal, mirroring the
			// format+validate flow.
			m.statusMessage = "✗ edited document has warnings — not saved"
			return m, nil
		}
		m.diagnostics = errorDiags
		m.diagCursor = 0
		m.showDiagnostics = true
		m.statusMessage = "✗ edited document did not validate — not saved"
		m.clearTextSelection()
		return m, nil
	case !result.Changed:
		m.statusMessage = "no changes"
		return m, nil
	}
	if session == nil || (len(result.Content) == 0 && session.Mode == app.EditNode) {
		m.statusMessage = "✗ editor session missing a result"
		return m, nil
	}
	// During the editor flow the tree selection is still the edited node;
	// capture its identity so the post-save tree refresh can re-anchor the
	// selection even when the edit added or removed sections above it. A
	// full-document edit replaces the whole doc, so its re-anchor falls on
	// the document row instead.
	nodeName := ""
	startLine := 0
	itemKey := ""
	if session.Mode == app.EditNode {
		if sel := m.selectedItem(); sel != nil && sel.hasNode {
			nodeName = sel.node.Name
			startLine = sel.node.Range.StartLine
			itemKey = sel.key
		}
	}
	m.pendingEdit = &pendingEdit{
		path:         session.DocPath,
		original:     result.Original,
		content:      result.Content,
		snapshotPath: result.SnapshotPath,
		nodeName:     nodeName,
		startLine:    startLine,
		itemKey:      itemKey,
		operation:    "edit",
	}
	lines, err := diff.Unified(result.Original, result.Content, session.DocPath, session.DocPath+" (edited)")
	if err != nil {
		m.statusMessage = "✗ diff failed: " + err.Error()
		return m, nil
	}
	m.showDiffModal(lines, "Diff · "+session.DocPath)
	return m, nil
}

// handleEditorError surfaces a Prepare or Complete failure (an
// external-change conflict, a missing editor command or a patch error) in
// the status line. When the session already produced a snapshot, the
// snapshot path is surfaced as the recovery trail.
func (m *Model) handleEditorError(msg editorErrorMsg) (tea.Model, tea.Cmd) {
	snapshot := ""
	if m.editorSession != nil {
		snapshot = m.editorSession.SnapshotPath
	}
	m.editing = false
	m.editorSession = nil
	m.statusMessage = "✗ editor: " + msg.Err.Error()
	if snapshot != "" {
		m.statusMessage += " (recovery snapshot: " + snapshot + ")"
	}
	m.recordError("editor", msg.Err.Error(), recoverySnapshotNext(snapshot))
	return m, nil
}

// recoverySnapshotNext builds the safe next action for an editor failure,
// pointing at the pre-edit snapshot when one exists.
func recoverySnapshotNext(snapshotPath string) string {
	if snapshotPath != "" {
		return "the pre-edit snapshot survives for recovery: " + snapshotPath
	}
	return "retry the edit once the editor is available"
}
