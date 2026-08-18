package ui

import (
	"strings"

	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	tea "github.com/charmbracelet/bubbletea"
)

// updateLogKey handles keys while the log view is open. The arrow keys
// move the row cursor (up/pgup also turn follow off — the operator takes
// control); Enter opens the detail modal for the selected entry; f toggles
// follow, p pauses/resumes polling, Esc closes the view and q/ctrl+c quits
// the program. y copies the active text selection; shift+arrows extend
// the keyboard selection in the log body.
func (m *Model) updateLogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if dx, dy, ok := shiftSelectionDelta(msg); ok {
		m.shiftTextCursor(dx, dy)
		return m, nil
	}
	switch msg.String() {
	case "y":
		return m.startCopy()
	case "esc":
		m.showLogs = false
		m.statusMessage = ""
		// Closing the log view drops any active text selection in the
		// log pane.
		m.clearTextSelection()
		return m, nil // stops the poll: no reschedule
	case "q", "ctrl+c":
		return m.requestQuit()
	case "up", "k":
		if m.logFollow {
			m.logFollow = false
			m.statusMessage = "log follow off"
			m.logCursor = len(m.logLines) - 1
		}
		if m.logCursor > 0 {
			m.logCursor--
		}
		m.revealLogCursor()
	case "down", "j":
		if m.logCursor < len(m.logLines)-1 {
			m.logCursor++
		}
		m.revealLogCursor()
	case "pgup":
		if m.logFollow {
			m.logFollow = false
			m.statusMessage = "log follow off"
			m.logCursor = len(m.logLines) - 1
		}
		m.logCursor -= m.logViewport.Height
		if m.logCursor < 0 {
			m.logCursor = 0
		}
		m.revealLogCursor()
	case "pgdown":
		m.logCursor += m.logViewport.Height
		if m.logCursor > len(m.logLines)-1 {
			m.logCursor = len(m.logLines) - 1
		}
		m.revealLogCursor()
	case "enter", "right":
		if m.logCursor >= 0 && m.logCursor < len(m.logLines) {
			m.logDetailEntry = m.logLines[m.logCursor] // copy
			m.logDetailOpen = true
			// The detail modal overlays the log pane: the pane's text
			// selection no longer applies.
			m.clearTextSelection()
			m.syncLogDetailContent(m.width, m.paneHeight())
			m.logDetailViewport.GotoTop()
			return m, nil
		}
	case "f":
		if m.logFollow {
			m.logFollow = false
			m.statusMessage = "log follow off"
		} else {
			m.logFollow = true
			m.logCursor = len(m.logLines) - 1
			m.logViewport.GotoBottom()
			m.statusMessage = "log follow on"
		}
	case "p":
		if m.logPaused {
			m.logPaused = false
			m.statusMessage = "log poll resumed"
			return m, m.logPollCmd()
		}
		m.logPaused = true
		m.statusMessage = "log poll paused"
		return m, nil // stops the poll: no reschedule
	}
	return m, nil
}

// updateLogDetailKey handles keys while the log detail modal is open.
// Esc closes it (back to the log list); the arrow keys and PgUp/PgDown
// scroll the wrapped JSON; q/ctrl+c quits.
func (m *Model) updateLogDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "left":
		m.logDetailOpen = false
	case "q", "ctrl+c":
		return m.requestQuit()
	case "up", "k":
		m.logDetailViewport.LineUp(1)
	case "down", "j":
		m.logDetailViewport.LineDown(1)
	case "pgup":
		m.logDetailViewport.PageUp()
	case "pgdown":
		m.logDetailViewport.PageDown()
	}
	return m, nil
}

// revealLogCursor scrolls the log viewport just enough so that the cursor
// row is visible, mirroring revealRange for the source pane.
func (m *Model) revealLogCursor() {
	if m.logCursor < m.logViewport.YOffset {
		m.logViewport.SetYOffset(m.logCursor)
	} else if m.logCursor >= m.logViewport.YOffset+m.logViewport.Height {
		m.logViewport.SetYOffset(m.logCursor - m.logViewport.Height + 1)
	}
}

// logView renders the full-screen log scrollback inside a bordered pane.
// The title names the log source (the followed log file path or the
// followed systemd journal unit) and the current follow/pause state, so
// those modes never rely on color alone.
func (m *Model) logView(width, height int) string {
	title := "Logs"
	if m.state != nil {
		switch {
		case m.state.Settings.LogPath != "":
			title += " · " + m.state.Settings.LogPath
		case m.state.Settings.JournalUnit != "":
			title += " · unit " + m.state.Settings.JournalUnit
		}
	}
	if m.logFollow {
		title += " · FOLLOW"
	}
	if m.logPaused {
		title += " · PAUSED"
	}
	if m.logErr != nil {
		title += " · poll error"
	}
	bodyH := height - 4 // border (2) + title (1) + blank separator (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	m.syncLogViewport(paneContentW, height)
	content := m.logViewport.View()
	if spans, ok := m.selectionSpans(textPaneLogs); ok {
		content = renderSelectionOverlay(content, m.logViewport.Width, m.logViewport.Height, spans)
	}
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(activeTitleStyle.Render(title) + "\n\n" + content)
}

// syncLogViewport sizes the log viewport to the pane and refreshes its
// content. The scroll is bottom-anchored in follow mode: new entries keep
// the newest line visible. When follow is off the manual scroll position
// is preserved (clamped to the new content), matching the "don't clobber
// manual scroll" rule from syncSource, inverted for follow mode.
func (m *Model) syncLogViewport(width, height int) {
	contentW := width - 4 // border (2) + horizontal padding (2)
	if contentW < 1 {
		contentW = 1
	}
	contentH := height - 4 // border (2) + title (1) + blank separator (1)
	if contentH < 1 {
		contentH = 1
	}
	m.logViewport.Width = contentW
	m.logViewport.Height = contentH

	var content strings.Builder
	if len(m.logLines) == 0 {
		content.WriteString(dimStyle.Render("no log entries yet — waiting for the first poll"))
	} else {
		for i, entry := range m.logLines {
			gutter := "  "
			if i == m.logCursor {
				gutter = cursorStyle.Render("› ")
			}
			// renderCompactLogLine truncates the plain text before
			// styling, so a long line can never cut an ANSI escape
			// sequence in half.
			content.WriteString(gutter + renderCompactLogLine(entry, contentW-2))
			content.WriteString("\n")
		}
	}

	wasAtBottom := m.logViewport.AtBottom()
	offset := m.logViewport.YOffset
	m.logViewport.SetContent(content.String())
	if m.logFollow && (wasAtBottom || len(m.logLines) > 0) {
		m.logViewport.GotoBottom()
	} else if !m.logFollow {
		// Restore the manual position, clamped to the new content.
		m.logViewport.SetYOffset(offset)
	}
}

// logDetailView renders the full lossless JSON of the selected entry as a
// modal layered over the log view. The content is only rebuilt when the
// body size changes (SetContent resets the viewport scroll), mirroring the
// diagnostics detail view.
func (m *Model) logDetailView(width, height int) string {
	title := "Log detail"
	summaryWidth := width - 18 // title label, border and padding
	if summaryWidth < 30 {
		summaryWidth = 30
	}
	if summaryWidth > 80 {
		summaryWidth = 80
	}
	if summary := logDetailSummary(m.logDetailEntry, summaryWidth); summary != "" {
		title += " · " + summary
	}
	bodyH := height - 4 // border (2) + title (1) + blank separator (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	if m.logDetailViewport.Height != bodyH {
		m.syncLogDetailContent(width, height)
	}
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(activeTitleStyle.Render(title) + "\n\n" + m.logDetailViewport.View())
}

// syncLogDetailContent sizes the log detail viewport and rebuilds its
// content from the current entry. width/height are the window pane
// dimensions (the same values logDetailView receives).
func (m *Model) syncLogDetailContent(width, height int) {
	contentW := width - 4 // border (2) + horizontal padding (2)
	if contentW < 1 {
		contentW = 1
	}
	contentH := height - 4 // border (2) + title (1) + blank separator (1)
	if contentH < 1 {
		contentH = 1
	}
	m.logDetailViewport.Width = contentW
	m.logDetailViewport.Height = contentH
	m.logDetailViewport.SetContent(strings.Join(renderLogDetail(m.logDetailEntry, contentW), "\n"))
}

// logDetailSummary builds a short descriptor for the detail modal title:
// timestamp + logger + message truncated to maxWidth cells, or "raw line"
// for non-JSON entries.
func logDetailSummary(entry logs.Entry, maxWidth int) string {
	if !entry.Parsed {
		return "raw line"
	}
	ts := logTimestampPlaceholder
	if !entry.Timestamp.IsZero() {
		ts = entry.Timestamp.Local().Format(logTimestampLayout)
	}
	parts := []string{ts}
	if entry.Logger != "" {
		parts = append(parts, entry.Logger)
	}
	if entry.Msg != "" {
		parts = append(parts, entry.Msg)
	}
	return truncateToWidth(strings.Join(parts, " "), maxWidth)
}
