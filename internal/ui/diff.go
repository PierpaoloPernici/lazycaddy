package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// showDiffModal opens the shared diff modal with the given lines and
// title, resetting the horizontal scroll offset and the hunk cursor. It
// is the single entry point for every diff the model opens (D, editor,
// delete, backup compare, conflict compare), so a new diff always starts
// at the top-left and on the first hunk.
func (m *Model) showDiffModal(lines []diff.Line, title string) {
	m.diffLines = lines
	m.diffTitle = title
	m.diffHOffset = 0
	m.diffHunkCursor = 0
	m.showDiff = true
	m.syncDiffContent()
	m.diffViewport.GotoTop()
}

// startDiff opens the unified diff modal for the currently selected
// document. The root document keeps the existing working-copy-vs-original
// behavior after `v`; an imported document (and the root without a
// working copy) is diffed against its current on-disk bytes read through
// the injected reader. On error the failure is surfaced in the status
// line. The modal is allowed even when validation previously failed or a
// validation is still in flight, because the working copy is retained in
// both cases.
func (m *Model) startDiff() (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Graph == nil {
		return m, nil
	}
	sel := m.selectedItem()
	if sel == nil || sel.doc == nil {
		m.statusMessage = "✗ diff unavailable: no document selected"
		return m, nil
	}
	doc := sel.doc
	cleanPath := filepath.Clean(doc.Path)
	isRoot := cleanPath == filepath.Clean(m.state.Settings.ConfigPath)

	// Root with a working copy keeps the existing v-based diff.
	if isRoot && m.workingBytes != nil {
		lines, err := diff.Unified(
			m.state.Graph.Root.Source,
			m.workingBytes,
			m.state.Settings.ConfigPath,
			m.state.Settings.ConfigPath+" (formatted)",
		)
		if err != nil {
			m.statusMessage = "✗ diff failed: " + err.Error()
			m.recordError("diff", err.Error(), "retry the diff once the working copy is ready")
			return m, nil
		}
		m.showDiffModal(lines, "Diff · "+m.state.Settings.ConfigPath)
		return m, nil
	}

	// Otherwise compare the document's in-memory source against its
	// current on-disk bytes through the injected reader.
	if m.readFile == nil {
		if isRoot {
			m.statusMessage = "no working copy — press v to format & validate first (no on-disk diff reader configured)"
		} else {
			m.statusMessage = "✗ diff unavailable: no on-disk diff reader configured"
		}
		return m, nil
	}
	onDisk, err := m.readFile(doc.Path)
	if err != nil {
		m.statusMessage = "✗ diff unavailable: " + err.Error()
		m.recordError("diff", "read "+doc.Path+": "+err.Error(), "check the file is readable and retry the diff")
		return m, nil
	}
	lines, err := diff.Unified(doc.Source, onDisk, doc.Path, doc.Path+" (on disk)")
	if err != nil {
		m.statusMessage = "✗ diff failed: " + err.Error()
		m.recordError("diff", err.Error(), "retry the diff")
		return m, nil
	}
	// The title reflects the outcome: an empty diff (in-memory matches
	// on-disk) is labelled "No changes" instead of "Diff current
	// changes", keeping the "no changes" body text as well.
	title := "Diff current changes · " + doc.Path
	if !hasDiffHunks(lines) {
		title = "No changes · " + doc.Path
	}
	m.showDiffModal(lines, title)
	return m, nil
}

// hasDiffHunks reports whether the given diff lines contain any @@ hunk
// header (i.e. whether the two sources actually differ).
func hasDiffHunks(lines []diff.Line) bool {
	for _, l := range lines {
		if l.Kind == diff.KindHunkHeader {
			return true
		}
	}
	return false
}

// updateDiffKey handles keys when the diff modal is open. Esc and q
// close the modal; the arrow keys and PgUp/PgDown scroll the viewport;
// n/N jump to the next/previous hunk header; h/l shift the horizontal
// scroll offset for long lines. When the diff shows a pending editor
// edit or delete, the diff is the single confirmation: Enter saves
// directly and Esc additionally discards the pending change. When the
// diff shows a backup comparison, Enter opens the rollback confirmation
// (which requires writable mode) and Esc returns to the backup list. The
// read-only D flow keeps its current behavior.
func (m *Model) updateDiffKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		if m.showChangeConflict && m.changeCompare {
			// Esc from the conflict compare returns to the conflict
			// options; the conflict modal stays open.
			m.closeDiff()
			m.changeCompare = false
			return m, nil
		}
		m.closeDiff()
		if m.pendingRollback != nil {
			// Esc from a backup comparison cancels the rollback and
			// returns to the backup list; nothing is changed.
			m.pendingRollback = nil
			m.backupComparing = false
		} else if m.pendingEdit != nil {
			m.pendingEdit = nil
			m.statusMessage = "edit discarded"
		} else if m.pendingDelete != nil {
			m.pendingDelete = nil
			m.statusMessage = "delete cancelled"
		}
	case "enter":
		if m.pendingRollback != nil {
			// Rollback needs writable mode and validation; the
			// confirmation modal names the target and the backup.
			m.closeDiff()
			m.backupComparing = false
			m.showRollbackConfirm = true
			return m, nil
		}
		if m.pendingEdit != nil || m.pendingDelete != nil {
			// The diff is the single confirmation for both an editor
			// edit and a delete: Enter saves directly (already
			// validated), mirroring the save-confirmation Enter branch.
			m.closeDiff()
			m.saving = true
			m.statusMessage = "saving…"
			return m, m.saveCmd()
		}
	case "up", "k":
		m.diffViewport.LineUp(1)
	case "down", "j":
		m.diffViewport.LineDown(1)
	case "pgup":
		m.diffViewport.PageUp()
	case "pgdown":
		m.diffViewport.PageDown()
	case "n":
		m.jumpHunk(1)
	case "N":
		m.jumpHunk(-1)
	case "l":
		m.diffHOffset += 4
		m.syncDiffContent()
	case "h":
		m.diffHOffset -= 4
		if m.diffHOffset < 0 {
			m.diffHOffset = 0
		}
		m.syncDiffContent()
	}
	return m, nil
}

// diffHunkLines returns the line indices of the @@ hunk headers in the
// current diff.
func (m *Model) diffHunkLines() []int {
	var idx []int
	for i, l := range m.diffLines {
		if l.Kind == diff.KindHunkHeader {
			idx = append(idx, i)
		}
	}
	return idx
}

// jumpHunk moves the hunk cursor by delta (wrapping) and scrolls the diff
// viewport so the selected @@ hunk header is visible at the top. It is a
// no-op when the diff has no hunks. The body is re-rendered so the "> "
// marker moves to the newly selected hunk (SetContent preserves the
// scroll position for an unchanged content height). A stale cursor (one
// that no longer matches the current hunk list, e.g. after the diff
// content changed) is re-anchored on the first hunk instead of advancing.
func (m *Model) jumpHunk(delta int) {
	hunks := m.diffHunkLines()
	if len(hunks) == 0 {
		return
	}
	n := len(hunks)
	if m.diffHunkCursor >= n {
		m.diffHunkCursor = 0
	} else {
		m.diffHunkCursor = (m.diffHunkCursor + delta + n) % n
	}
	target := hunks[m.diffHunkCursor]
	m.syncDiffContent()
	if target < m.diffViewport.YOffset || target >= m.diffViewport.YOffset+m.diffViewport.Height {
		m.diffViewport.SetYOffset(target)
	}
}

// diffSummary counts the hunks and the added/removed lines of the current
// diff for the modal title. It returns "" for an unchanged diff (no
// hunks) so the "no changes" message stays the only signal.
func (m *Model) diffSummary() string {
	hunks, adds, removes := 0, 0, 0
	for _, l := range m.diffLines {
		switch l.Kind {
		case diff.KindHunkHeader:
			hunks++
		case diff.KindAdd:
			adds++
		case diff.KindRemove:
			removes++
		}
	}
	if hunks == 0 {
		return ""
	}
	return fmt.Sprintf("%d hunk(s) · +%d −%d", hunks, adds, removes)
}

// closeDiff dismisses the diff modal and clears its state. Called by
// Esc and q from inside the modal.
func (m *Model) closeDiff() {
	m.showDiff = false
	m.diffLines = nil
	m.diffTitle = ""
	m.diffHOffset = 0
	m.diffHunkCursor = 0
	m.diffViewport.SetContent("")
}

// syncDiffContent sets the diff viewport size and content from the
// current state. The body is rebuilt only when the size changes:
// rebuilding resets the viewport scroll, so doing it on every render
// would make PgUp / PgDown unusable.
func (m *Model) syncDiffContent() {
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
	m.diffViewport.Width = bodyW
	m.diffViewport.Height = bodyH
	m.diffViewport.SetContent(m.diffBody(bodyW))
}

// diffBody returns the colored, width-truncated content for the diff
// viewport. When the diff contains no additions, removals or hunk
// headers, it returns a dim "no changes" message instead.
func (m *Model) diffBody(bodyW int) string {
	hasChanges := false
	for _, line := range m.diffLines {
		switch line.Kind {
		case diff.KindAdd, diff.KindRemove, diff.KindHunkHeader:
			hasChanges = true
		}
	}
	if !hasChanges {
		return dimStyle.Render("no changes — the working copy matches the source")
	}
	// The current hunk (the one at diffHunkCursor) is marked with a "> "
	// prefix so the operator always knows which @@ header n/N selected.
	currentHunk := -1
	if hunks := m.diffHunkLines(); len(hunks) > 0 && m.diffHunkCursor >= 0 && m.diffHunkCursor < len(hunks) {
		currentHunk = hunks[m.diffHunkCursor]
	}
	var b strings.Builder
	for i, line := range m.diffLines {
		switch line.Kind {
		case diff.KindAdd:
			text := m.renderDiffLine(line.Text, bodyW)
			b.WriteString(diffAddStyle.Render(text))
		case diff.KindRemove:
			text := m.renderDiffLine(line.Text, bodyW)
			b.WriteString(diffRemoveStyle.Render(text))
		case diff.KindHunkHeader:
			raw := line.Text
			if i == currentHunk {
				// The marker is part of the line so horizontal scrolling
				// and truncation account for it.
				raw = "> " + raw
			}
			text := m.renderDiffLine(raw, bodyW)
			b.WriteString(diffHunkStyle.Render(text))
		case diff.KindFileHeader:
			text := m.renderDiffLine(line.Text, bodyW)
			b.WriteString(diffFileStyle.Render(text))
		default:
			text := m.renderDiffLine(line.Text, bodyW)
			b.WriteString(text)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderDiffLine applies the diff viewport's horizontal scroll offset to
// one plain-text diff line and truncates it to fit bodyW. With a positive
// offset the line is prefixed with an ellipsis so the operator can see
// content is scrolled horizontally; tail truncation keeps the "…" suffix.
// The offset is measured in display columns and is rune-aware.
func (m *Model) renderDiffLine(text string, bodyW int) string {
	if m.diffHOffset <= 0 {
		return truncateToWidth(text, bodyW)
	}
	runes := []rune(text)
	seen := 0
	i := 0
	for ; i < len(runes); i++ {
		if seen >= m.diffHOffset {
			break
		}
		seen += lipgloss.Width(string(runes[i]))
	}
	if bodyW <= 1 {
		return "…"
	}
	return "…" + truncateToWidth(string(runes[i:]), bodyW-1)
}

// diffView renders the unified diff modal. The content is only rebuilt
// when the body height changes, so scrolling with PgUp/PgDown is
// preserved across renders. A diff reviewing an editor edit offers
// Enter to save and Esc to discard; a backup comparison offers Enter to
// roll back and Esc to cancel.
func (m *Model) diffView(width, height int) string {
	title := m.diffTitle
	if summary := m.diffSummary(); summary != "" {
		title += " · " + summary
	}
	switch {
	case m.pendingRollback != nil:
		title += " · Enter rollback · Esc cancel"
	case m.pendingDelete != nil:
		title += " · Enter delete · Esc cancel"
	case m.pendingEdit != nil:
		title += " · Enter save · Esc discard"
	default:
		title += " · Esc close"
	}
	bodyH := height - 3 // border (2) + title (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	// Re-sync only when the size has changed since the last set
	// (e.g. the terminal was resized while the diff modal was open).
	// Re-syncing resets the scroll; doing it on every render would
	// make PgUp / PgDown unusable.
	if m.diffViewport.Height != bodyH {
		m.syncDiffContent()
	}
	// The title (path + summary + action hints) is truncated so a long
	// path or summary can never overflow the modal border.
	title = truncateToWidth(title, paneContentW-2)
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(activeTitleStyle.Render(title) + "\n" + m.diffViewport.View())
}
