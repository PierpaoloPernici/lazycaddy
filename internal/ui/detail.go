package ui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// updateDetailKey handles keys when the diagnostics detail view is
// open. Esc / ← return to the list (the modal stays open), except when the
// detail was opened from the inline review (→): there they return straight
// to the review list. PgUp / PgDown and the arrow keys scroll the wrapped
// message.
func (m *Model) updateDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "left":
		if m.inlineReviewReturn {
			// Detail opened from the inline review: closing returns
			// directly to the review list, skipping the intermediate
			// diagnostics list.
			m.showDetail = false
			m.closeDiagnostics()
		} else {
			m.closeDetail()
		}
	case "up", "k":
		m.detailViewport.LineUp(1)
	case "down", "j":
		m.detailViewport.LineDown(1)
	case "pgup":
		m.detailViewport.PageUp()
	case "pgdown":
		m.detailViewport.PageDown()
	}
	return m, nil
}

// openDetail transitions from the diagnostics list to the detail
// view for the diagnostic under the cursor. It is a no-op when the
// cursor is out of range. The content is loaded into the viewport
// immediately so the user can scroll (PgUp / PgDown / arrows) before
// the next render; the view function refreshes the size when the
// terminal is resized while the detail view is open.
func (m *Model) openDetail() {
	if m.diagCursor < 0 || m.diagCursor >= len(m.diagnostics) {
		return
	}
	m.showDetail = true
	m.syncDetailContent()
	m.detailViewport.GotoTop()
}

// syncDetailContent sets the detail viewport size and content from
// the current state. The body is rebuilt only when the size or the
// cursor changes: rebuilding resets the viewport scroll, so calling
// this on every render would make PgUp / PgDown unusable.
func (m *Model) syncDetailContent() {
	paneContentW := m.width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	bodyW := paneContentW - 2 // padding (1 each side)
	if bodyW < 1 {
		bodyW = 1
	}
	bodyH := m.paneHeight() - 3 // border (2) + title (1)
	if bodyH < 1 {
		bodyH = 1
	}
	m.detailViewport.Width = bodyW
	m.detailViewport.Height = bodyH
	m.detailViewport.SetContent(m.buildDetailBody(bodyW))
}

// buildDetailBody formats the diagnostic at the current cursor for
// the detail view. The path / line / column / severity are listed on
// fixed labels, then a blank line, then the message word-wrapped to
// bodyW. Missing line or column is omitted so we do not print
// "Line 0" or "Column 0" for diagnostics that did not report a
// position.
func (m *Model) buildDetailBody(bodyW int) string {
	var body strings.Builder
	if m.diagCursor < 0 || m.diagCursor >= len(m.diagnostics) {
		body.WriteString(dimStyle.Render("no diagnostic selected"))
		return body.String()
	}
	d := m.diagnostics[m.diagCursor]
	body.WriteString(dimStyle.Render("Path     ") + d.Path + "\n")
	if d.Line > 0 {
		body.WriteString(dimStyle.Render("Line     ") + strconv.Itoa(d.Line) + "\n")
	}
	if d.Column > 0 {
		body.WriteString(dimStyle.Render("Column   ") + strconv.Itoa(d.Column) + "\n")
	}
	body.WriteString(dimStyle.Render("Severity ") + d.Severity.String() + "\n")
	body.WriteString("\n")
	body.WriteString(wrapText(d.Message, bodyW))
	return body.String()
}

// paneContentH returns the content height handed to pane/modal renderers
// so the complete view (header + optional error line + pane/modal +
// optional status strip + compact footer) fits the given total height.
// Pane renderers draw their border around the content, so the vertical
// frame size must be subtracted here, mirroring the horizontal handling
// in View(). All pane/modal styles are copies of paneStyle with the same
// border and padding, so paneStyle.GetVerticalFrameSize() is correct for
// every renderer.
func (m *Model) paneContentH(height int) int {
	width := m.width
	if width == 0 {
		width = 80
	}
	footerH := lipgloss.Height(m.footer(width))
	if footerH < 1 {
		footerH = 1
	}
	h := height - 1 /*header*/ - paneStyle.GetVerticalFrameSize() - footerH
	if m.err != nil {
		h--
	}
	if m.statusMessage != "" {
		h--
	}
	if h < 1 {
		h = 1
	}
	return h
}

// paneHeight returns the height of the main pane area (the modal or
// the tree+source panes), matching the computation in View. It is
// extracted so the detail view can size its viewport to match the
// pane that will contain it, without re-deriving the layout.
func (m *Model) paneHeight() int {
	return m.paneContentH(m.height)
}

// closeDetail returns from the detail view to the diagnostics list.
// The list keeps the cursor at the same position; the modal stays
// open until the user presses Esc again or another keybinding.
func (m *Model) closeDetail() {
	m.showDetail = false
}
