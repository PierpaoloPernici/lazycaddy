package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/backup"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
)

// startBackups opens the backup-history modal for the currently selected
// document (row or node — both carry a doc). It lists the backups for
// that exact source path through the injected rollbacker and is
// available in read-only mode: comparison is read-only, rollback is
// gated separately. Listing is asynchronous so the model never blocks on
// the backup directory.
func (m *Model) startBackups() (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Graph == nil {
		return m, nil
	}
	if m.rollbacker == nil {
		m.statusMessage = "✗ backups unavailable: no backup directory configured"
		return m, nil
	}
	if m.backupsLoading || m.rollingBack || m.saving || m.reloading || m.editing || m.deleting {
		return m, nil
	}
	sel := m.selectedItem()
	if sel == nil || sel.doc == nil {
		m.statusMessage = "✗ backups unavailable: no document selected"
		return m, nil
	}
	rollbacker := m.rollbacker
	path := sel.doc.Path
	docs := append([]*caddyfile.Document(nil), m.state.Graph.Documents...)
	m.backupsLoading = true
	m.statusMessage = "listing backups…"
	return m, func() tea.Msg {
		entries, err := rollbacker.ListBackups(path, docs)
		return backupListMsg{Path: path, Entries: entries, Err: err}
	}
}

// handleBackupList opens the backup modal with the listing result. A
// listing failure surfaces in the status line; an empty listing opens
// the modal with a hint so the operator sees that no backups exist.
func (m *Model) handleBackupList(msg backupListMsg) (tea.Model, tea.Cmd) {
	m.backupsLoading = false
	if msg.Err != nil {
		m.statusMessage = "✗ backups unavailable: " + msg.Err.Error()
		m.recordError("backups", msg.Err.Error(), "verify the backup directory is readable")
		return m, nil
	}
	m.backups = msg.Entries
	m.backupCursor = 0
	m.backupDocPath = msg.Path
	m.backupComparing = false
	m.showBackups = true
	m.backupViewport.GotoTop()
	m.statusMessage = ""
	return m, nil
}

// updateBackupsKey handles keys while the backup-history modal is open.
// ↑/↓ move the cursor, Enter opens the compare diff (current on-disk vs
// selected backup) and Esc closes the modal. Enter rolls back only when
// the diff is confirmed in the next step; the backup modal itself never
// mutates anything.
func (m *Model) updateBackupsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		// q closes the backup modal; it is navigation, not an exit, so
		// the unsaved guard never fires here.
		m.closeBackups()
	case "ctrl+c":
		return m.requestQuit()
	case "up", "k":
		if m.backupCursor > 0 {
			m.backupCursor--
		}
		m.revealBackupCursor()
	case "down", "j":
		if m.backupCursor < len(m.backups)-1 {
			m.backupCursor++
		}
		m.revealBackupCursor()
	case "pgup":
		m.backupViewport.PageUp()
	case "pgdown":
		m.backupViewport.PageDown()
	case "enter":
		if m.backupCursor >= 0 && m.backupCursor < len(m.backups) {
			return m, m.openBackupCompareCmd(m.backups[m.backupCursor])
		}
	}
	return m, nil
}

// openBackupCompareCmd reads the current on-disk bytes of the target
// document and the selected backup's bytes through the rollbacker, then
// delivers a backupCompareMsg. Any read failure aborts the comparison
// without changing anything.
func (m *Model) openBackupCompareCmd(entry backup.Entry) tea.Cmd {
	rollbacker := m.rollbacker
	path := m.backupDocPath
	backupPath := entry.Path
	return func() tea.Msg {
		current, err := rollbacker.ReadCurrent(path)
		if err != nil {
			return backupCompareMsg{Path: path, BackupPath: backupPath, Err: err}
		}
		content, err := rollbacker.ReadBackup(entry)
		if err != nil {
			return backupCompareMsg{Path: path, BackupPath: backupPath, Current: current, Err: err}
		}
		return backupCompareMsg{Path: path, BackupPath: backupPath, Current: current, Backup: content}
	}
}

// handleBackupCompare opens the shared diff modal with the current
// on-disk bytes vs the selected backup. When rollback is available the
// diff is the review step before the confirmation: Enter opens the
// rollback confirmation. In read-only mode the diff is strictly
// informational and Enter does nothing.
func (m *Model) handleBackupCompare(msg backupCompareMsg) (tea.Model, tea.Cmd) {
	// A stale compare message — the operator navigated away or reopened
	// the backup modal for a different document while the async read was
	// in flight — must never open a diff for the wrong document.
	if msg.Path != m.backupDocPath {
		return m, nil
	}
	if msg.Err != nil {
		m.statusMessage = "✗ backup comparison failed: " + msg.Err.Error()
		m.recordError("backup comparison", msg.Err.Error(), "verify the backup file is readable and retry")
		return m, nil
	}
	lines, err := diff.Unified(msg.Current, msg.Backup, msg.Path+" (on disk)", msg.Path+" ← "+filepath.Base(msg.BackupPath))
	if err != nil {
		m.statusMessage = "✗ diff failed: " + err.Error()
		m.recordError("backup comparison", err.Error(), "retry the comparison")
		return m, nil
	}
	if m.canRollback() {
		m.pendingRollback = &pendingRollback{
			path:         msg.Path,
			backupPath:   msg.BackupPath,
			currentBytes: append([]byte(nil), msg.Current...),
		}
	}
	m.showDiffModal(lines, "Compare backup · "+msg.Path)
	m.backupComparing = true
	return m, nil
}

// closeBackups dismisses the backup-history modal and clears its state.
func (m *Model) closeBackups() {
	m.showBackups = false
	m.backups = nil
	m.backupCursor = 0
	m.backupDocPath = ""
	m.backupComparing = false
	m.backupViewport.SetContent("")
}

// revealBackupCursor scrolls the backup viewport just enough so the row
// under backupCursor is visible, mirroring revealLogCursor.
func (m *Model) revealBackupCursor() {
	if m.backupCursor < m.backupViewport.YOffset {
		m.backupViewport.SetYOffset(m.backupCursor)
	} else if m.backupCursor >= m.backupViewport.YOffset+m.backupViewport.Height {
		m.backupViewport.SetYOffset(m.backupCursor - m.backupViewport.Height + 1)
	}
}

// updateRollbackConfirmKey handles keys while the rollback-confirmation
// modal is open. Enter confirms and starts the async rollback; Esc and q
// cancel. A cancellation leaves the target and every backup unchanged.
func (m *Model) updateRollbackConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		// q cancels the rollback; it is not an exit, so the unsaved
		// guard never fires here.
		m.closeRollbackConfirm()
		m.pendingRollback = nil
		m.statusMessage = "rollback cancelled"
	case "ctrl+c":
		return m.requestQuit()
	case "enter":
		if m.pendingRollback == nil {
			return m, nil
		}
		m.closeRollbackConfirm()
		m.rollingBack = true
		m.statusMessage = "rolling back…"
		return m, m.rollbackCmd()
	}
	return m, nil
}

// closeRollbackConfirm dismisses the rollback-confirmation modal. The
// pending backup survives so Esc from the confirmation returns the
// operator to the compare diff.
func (m *Model) closeRollbackConfirm() {
	m.showRollbackConfirm = false
}

// rollbackCmd returns a tea.Cmd that calls the injected rollbacker in a
// goroutine and reports the result as a rollbackResultMsg. The conflict
// baseline is the on-disk bytes captured when the compare diff opened,
// and the document set is snapshotted so the validation runs in the
// context of the exact graph the operator was looking at.
func (m *Model) rollbackCmd() tea.Cmd {
	rollbacker := m.rollbacker
	pr := m.pendingRollback
	path := pr.path
	original := pr.currentBytes
	backupPath := pr.backupPath
	var docs []*caddyfile.Document
	if m.state != nil && m.state.Graph != nil {
		docs = append([]*caddyfile.Document(nil), m.state.Graph.Documents...)
	}
	return func() tea.Msg {
		result, err := rollbacker.Rollback(context.Background(), path, original, backupPath, docs)
		return rollbackResultMsg{Result: result, Err: err}
	}
}

// handleRollbackResult is invoked on the main goroutine when the
// rollbacker returns. On success the document graph is reloaded from
// disk, the change monitor is re-targeted at the freshly written bytes
// and the runtime loaded state is marked unknown until an explicit
// reload proves it again. The pre-rollback backup path is surfaced so
// the operator can undo the rollback. On failure the backup-history
// modal stays open (it was never closed) so the operator can retry or
// close, and the target and existing backups are untouched.
func (m *Model) handleRollbackResult(msg rollbackResultMsg) (tea.Model, tea.Cmd) {
	m.pendingRollback = nil
	targetPath := m.backupDocPath
	if msg.Err == nil {
		status := "✓ rolled back " + targetPath + " to " + msg.Result.RestoredFrom + " (pre-rollback backup: " + msg.Result.BackupPath + ")"
		if msg.Result.RetentionErr != nil {
			status += " · ✗ retention cleanup failed: " + msg.Result.RetentionErr.Error()
			m.recordError("rollback retention", msg.Result.RetentionErr.Error(), "remove old backups manually or lower --backup-retention")
		}
		m.loaded = loadedUnknown
		m.loadedAt = time.Time{}
		if !m.refreshAfterRollback(targetPath) {
			status += " · tree refresh failed"
		}
		// The in-flight guard must cover the whole re-arm window above:
		// only once the graph (or the monitor re-seed) has settled is the
		// rollback fully complete. Cleared after refreshAfterRollback so
		// no external-change notification can interrupt the re-seed.
		m.rollingBack = false
		m.statusMessage = status
		return m, nil
	}
	// Failure: the rollback is over; the guard can be released. The
	// target and existing backups are untouched.
	m.rollingBack = false
	switch {
	case errors.Is(msg.Err, app.ErrRollbackConflict):
		m.statusMessage = "✗ file changed on disk — rollback aborted, nothing changed"
		m.recordError("rollback", "file changed on disk", "reload the file from disk before retrying the rollback")
	case errors.Is(msg.Err, app.ErrRollbackInvalid):
		m.statusMessage = "✗ backup does not validate as a Caddyfile — rollback aborted"
		m.recordError("rollback", "backup does not validate as a Caddyfile", "choose a different backup or fix the configuration")
	default:
		var rbErr *app.RollbackError
		if errors.As(msg.Err, &rbErr) {
			m.statusMessage = "✗ rollback failed (pre-rollback backup: " + rbErr.BackupPath + "): " + rbErr.Err.Error() + " · press B to compare the pre-rollback backup"
			m.recordError("rollback", rbErr.Err.Error(), "press B on the document to compare the pre-rollback backup: "+rbErr.BackupPath)
		} else {
			m.statusMessage = "✗ rollback failed: " + msg.Err.Error()
			m.recordError("rollback", msg.Err.Error(), "inspect the backup list with B and retry")
		}
	}
	// The backup-history modal stayed open underneath; leave it open so
	// the operator can inspect and retry. Its compare baseline is stale,
	// so clear any half-open diff state.
	m.showDiff = false
	m.backupComparing = false
	return m, nil
}

// refreshAfterRollback reloads the graph through the loader after a
// successful rollback and rebuilds the tree, because the restored
// content can change the document structure. It also re-aligns the root
// snapshots and re-seeds the change monitor so its reference bytes match
// the restored file. It reports whether the reload succeeded; the caller
// keeps the rollback status message either way.
func (m *Model) refreshAfterRollback(targetPath string) bool {
	m.closeBackups()
	if m.loader == nil {
		m.seedMonitorWithRestoredBytes(targetPath)
		return false
	}
	state, err := m.loader.LoadState()
	if err != nil || state == nil || state.Graph == nil {
		// The file is restored but the graph reload failed. Re-seed the
		// monitor with the true on-disk bytes only when they can be read;
		// when they cannot, the monitor keeps its existing reference so
		// the natural "disk changed vs what the app knows" detection
		// surfaces a genuine reload prompt (the graph is stale).
		m.seedMonitorWithRestoredBytes(targetPath)
		return false
	}
	m.state.Graph = state.Graph
	// Re-anchor the cursor on the previously selected row's stable key.
	prevKey := ""
	if sel := m.selectedItem(); sel != nil {
		prevKey = sel.key
	}
	m.rebuildTree(prevKey)
	// The restored file is now the graph state: re-align the root
	// snapshots. loadedBytes/workingBytes deliberately track only the
	// root document (the diff/save/reload guards compare against them);
	// imported documents keep their own Source in the graph.
	if filepath.Clean(targetPath) == filepath.Clean(m.state.Settings.ConfigPath) {
		m.loadedBytes = append([]byte(nil), state.Graph.Root.Source...)
		m.workingBytes = append([]byte(nil), state.Graph.Root.Source...)
	}
	m.workingValidated = false
	m.pendingEdit = nil
	m.pendingDelete = nil
	m.sourceRefresh = true
	m.syncMonitor()
	return true
}

// seedMonitorWithRestoredBytes re-seeds the change monitor's reference
// for the restored document with the true current on-disk bytes when the
// post-rollback graph reload failed. Without this the monitor would
// compare the on-disk file against the stale pre-rollback in-memory bytes
// and fire a spurious "file changed on disk" conflict for the file that
// was just restored. The on-disk bytes are read through the injected
// rollbacker (the UI never touches the filesystem directly). When the
// read fails the monitor is left untouched: its existing reference then
// surfaces a genuine "disk changed" reload prompt, which is correct
// because the graph is stale.
func (m *Model) seedMonitorWithRestoredBytes(targetPath string) {
	if m.monitor == nil || m.rollbacker == nil || m.state == nil || m.state.Graph == nil {
		return
	}
	onDisk, err := m.rollbacker.ReadCurrent(targetPath)
	if err != nil {
		return // keep the monitor's existing reference
	}
	cleanPath := filepath.Clean(targetPath)
	for _, doc := range m.state.Graph.Documents {
		if doc != nil && filepath.Clean(doc.Path) == cleanPath {
			doc.Source = append([]byte(nil), onDisk...)
		}
	}
	m.syncMonitor()
}

// backupView renders the backup-history modal: the exact source path the
// backups belong to, then one row per backup (newest first) with its
// timestamp, sequence and exact backup path, and an explicit note about
// read-only rollback availability. The content is only rebuilt when the
// size changes, so the cursor reveal keeps working.
func (m *Model) backupView(width, height int) string {
	title := "Backups · " + truncateToWidth(m.backupDocPath, width-20)
	bodyH := height - 3 // border (2) + title (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	m.syncBackupViewport(paneContentW, bodyH)
	var body strings.Builder
	body.WriteString(dimStyle.Render("Source ") + truncateToWidth(m.backupDocPath, paneContentW-14) + "\n")
	if !m.canRollback() {
		body.WriteString(dimStyle.Render("read-only — rollback unavailable (needs --write and a caddy binary)") + "\n")
	}
	body.WriteString("\n")
	body.WriteString(m.backupViewport.View())
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(activeTitleStyle.Render(title) + "\n" + body.String())
}

// syncBackupViewport sizes the backup viewport and rebuilds its content
// from the current entries, highlighting the row under backupCursor. The
// content is only rebuilt when the size changes: rebuilding resets the
// scroll, so doing it on every render would make PgUp/PgDown unusable.
func (m *Model) syncBackupViewport(width, height int) {
	contentW := width - 4 // border (2) + horizontal padding (2)
	if contentW < 1 {
		contentW = 1
	}
	contentH := height
	if contentH < 1 {
		contentH = 1
	}
	m.backupViewport.Width = contentW
	m.backupViewport.Height = contentH
	var content strings.Builder
	if len(m.backups) == 0 {
		content.WriteString(dimStyle.Render("no backups for this document yet"))
	} else {
		textW := contentW - 2 // cursor prefix ("› ")
		if textW < 1 {
			textW = 1
		}
		for i, e := range m.backups {
			line := backupEntryLine(e, textW)
			if i == m.backupCursor {
				line = cursorStyle.Render("› " + line)
			} else {
				line = "  " + line
			}
			content.WriteString(line + "\n")
		}
	}
	m.backupViewport.SetContent(content.String())
}

// backupEntryLine renders one backup row: the timestamp, the sequence
// and the exact backup path (truncated to the row width).
func backupEntryLine(e backup.Entry, width int) string {
	ts := e.Time.Local().Format("2006-01-02 15:04:05")
	label := fmt.Sprintf("%s · %s", ts, e.Path)
	return truncateToWidth(label, width)
}

// rollbackConfirmView renders the rollback-confirmation modal. It names
// the exact target path and the backup that will be restored, and warns
// that a new backup of the current file is created before the restore.
func (m *Model) rollbackConfirmView(width, height int) string {
	title := "Rollback · Enter restore · Esc cancel"
	bodyH := height - 3 // border (2) + title (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	var body strings.Builder
	body.WriteString(dimStyle.Render("Restore ") + truncateToWidth(m.pendingRollback.path, paneContentW-16) + "\n")
	body.WriteString(dimStyle.Render("Backup  ") + truncateToWidth(m.pendingRollback.backupPath, paneContentW-16) + "\n")
	body.WriteString("\n")
	body.WriteString(dimStyle.Render("the current file is validated and backed up before the restore") + "\n")
	body.WriteString(dimStyle.Render("Caddy is never reloaded implicitly by a rollback"))
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(activeTitleStyle.Render(title) + "\n" + body.String())
}
