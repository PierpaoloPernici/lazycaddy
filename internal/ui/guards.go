package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// requestQuit handles a real application-exit request (a binding that
// would otherwise set m.quit and return tea.Quit). When unsaved edits
// exist it opens the unsaved-changes confirmation modal instead of
// quitting; otherwise it quits immediately. Navigation is never routed
// through here — only genuine exit bindings call requestQuit, so the
// guard never fires for cursor movement, search, log toggling or
// document switching.
func (m *Model) requestQuit() (tea.Model, tea.Cmd) {
	if m.hasUnsavedEdits() {
		m.clearTextSelection()
		m.showUnsavedConfirm = true
		m.statusMessage = ""
		return m, nil
	}
	m.quit = true
	return m, tea.Quit
}

// discardUnsaved clears every in-memory edit so the application can quit
// without losing track of what is being abandoned: the pending editor
// edit, the pending delete and the root working copy.
func (m *Model) discardUnsaved() {
	m.pendingEdit = nil
	m.pendingDelete = nil
	m.workingBytes = nil
	m.workingValidated = false
}

// updateUnsavedConfirmKey handles keys while the unsaved-changes modal is
// open. s saves (and stays in the app — save is async), d discards the
// unsaved edits and quits, Esc/q/ctrl+c cancel and return exactly to the
// prior state.
func (m *Model) updateUnsavedConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "s":
		// startSave may return early without opening the save
		// confirmation (read-only mode, another workflow in flight, no
		// working copy). In that case the unsaved-confirm modal stays
		// open so the operator can still discard or cancel instead of
		// being dropped back to the main view.
		m.startSave()
		if m.showSaveConfirm {
			m.closeUnsavedConfirm()
		}
		return m, nil
	case "d":
		m.discardUnsaved()
		m.closeUnsavedConfirm()
		m.quit = true
		return m, tea.Quit
	case "esc", "q", "ctrl+c":
		m.closeUnsavedConfirm()
		return m, nil
	}
	return m, nil
}

// closeUnsavedConfirm dismisses the unsaved-changes confirmation modal.
func (m *Model) closeUnsavedConfirm() {
	m.showUnsavedConfirm = false
}

// unsavedConfirmView renders the unsaved-changes confirmation modal. It
// names the risk (quitting discards the edits) and offers s save (the
// save is async and keeps the user in the app), d discard & quit, and
// Esc cancel.
func (m *Model) unsavedConfirmView(width, height int) string {
	title := "Unsaved changes · s save · d discard & quit · Esc cancel"
	bodyH := height - 3 // border (2) + title (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	var body strings.Builder
	body.WriteString(statusWarningStyle.Render("there are unsaved changes — quitting discards them") + "\n")
	body.WriteString("\n")
	body.WriteString("s  save (stays in the app; the save is async)\n")
	body.WriteString("d  discard & quit\n")
	body.WriteString("Esc  cancel and keep editing")
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(activeTitleStyle.Render(title) + "\n" + body.String())
}

// recordError appends a failure to the bounded error history. Every entry
// names the failed operation, the message and a safe next action. The
// history is capped at errorHistoryMax and is fully deterministic, so the
// H error-history view stays testable.
func (m *Model) recordError(op, message, next string) {
	m.errorHistory = append(m.errorHistory, errorEntry{Op: op, Message: message, Next: next})
	if len(m.errorHistory) > errorHistoryMax {
		m.errorHistory = m.errorHistory[len(m.errorHistory)-errorHistoryMax:]
	}
}

// startErrorHistory opens the error-history view. It is a read-only,
// bounded list of the recorded failures; the keybinding is documented in
// the footer.
func (m *Model) startErrorHistory() (tea.Model, tea.Cmd) {
	m.clearTextSelection()
	m.showErrorHistory = true
	m.errorHistoryViewport.GotoTop()
	return m, nil
}

// updateErrorHistoryKey handles keys while the error-history view is
// open: ↑/↓ and PgUp/PgDn scroll, Esc/q close, ctrl+c is a real exit
// request (and goes through the unsaved guard).
func (m *Model) updateErrorHistoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.closeErrorHistory()
	case "ctrl+c":
		return m.requestQuit()
	case "up", "k":
		m.errorHistoryViewport.LineUp(1)
	case "down", "j":
		m.errorHistoryViewport.LineDown(1)
	case "pgup":
		m.errorHistoryViewport.PageUp()
	case "pgdown":
		m.errorHistoryViewport.PageDown()
	}
	return m, nil
}

// closeErrorHistory dismisses the error-history view.
func (m *Model) closeErrorHistory() {
	m.showErrorHistory = false
	m.errorHistoryViewport.SetContent("")
}

// errorHistoryView renders the bounded error-history list. Each entry
// shows the failed operation and message on the first line and the safe
// next action on an indented second line, so recovery is never hidden.
func (m *Model) errorHistoryView(width, height int) string {
	title := fmt.Sprintf("Error history · %d entr%s · Esc close", len(m.errorHistory), pluralEntr(len(m.errorHistory)))
	bodyH := height - 3 // border (2) + title (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	m.syncErrorHistoryViewport(paneContentW, bodyH)
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(activeTitleStyle.Render(title) + "\n" + m.errorHistoryViewport.View())
}

// syncErrorHistoryViewport sizes the error-history viewport and rebuilds
// its content from the bounded history, preserving the scroll position
// across renders.
func (m *Model) syncErrorHistoryViewport(width, height int) {
	contentW := width - 4 // border (2) + horizontal padding (2)
	if contentW < 1 {
		contentW = 1
	}
	contentH := height
	if contentH < 1 {
		contentH = 1
	}
	m.errorHistoryViewport.Width = contentW
	m.errorHistoryViewport.Height = contentH
	var content strings.Builder
	if len(m.errorHistory) == 0 {
		content.WriteString(dimStyle.Render("no recorded errors"))
	} else {
		textW := contentW - 2 // two-space continuation indent
		if textW < 1 {
			textW = 1
		}
		for _, e := range m.errorHistory {
			content.WriteString(errorStyle.Render("✗ "+truncateToWidth(e.Op, textW)) + "\n")
			content.WriteString(truncateToWidth("  "+e.Message, contentW) + "\n")
			if e.Next != "" {
				content.WriteString(dimStyle.Render(truncateToWidth("  → "+e.Next, contentW)) + "\n")
			}
			content.WriteString("\n")
		}
	}
	m.errorHistoryViewport.SetContent(content.String())
}

// pluralEntr returns "y" or "ies" for the given count ("entry"/"entries").
func pluralEntr(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
