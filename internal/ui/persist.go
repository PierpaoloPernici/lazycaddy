package ui

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
	tea "github.com/charmbracelet/bubbletea"
)

func pendingEditVerb(pe *pendingEdit) string {
	if pe != nil {
		switch pe.operation {
		case "add":
			return "add"
		case "new":
			return "create"
		case "reorder":
			return "move after"
		}
	}
	return "save"
}

func pendingEditName(pe *pendingEdit) string {
	if pe != nil {
		switch pe.operation {
		case "add":
			return "add"
		case "new":
			return "new node"
		case "reorder":
			return "move after"
		}
	}
	return "edit"
}

// startSave begins the save workflow. It is gated by the presence of
// a loaded graph, a configured saver, an in-flight save guard, a
// validated working copy and actual changes. When all guards pass it
// opens the save-confirmation modal so the operator can review the
// target path and backup directory before confirming the write. A
// pending editor edit bypasses the root working-copy guards: it was
// already validated by the editor flow and targets its own document,
// so the root state is irrelevant.
func (m *Model) startSave() (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Graph == nil {
		return m, nil
	}
	if m.saver == nil {
		m.statusMessage = "read-only mode — start with --write to enable saving"
		return m, nil
	}
	if m.saving || m.rollingBack {
		return m, nil
	}
	if m.reloading {
		return m, nil
	}
	if m.pendingEdit != nil || m.pendingDelete != nil {
		m.showSaveConfirm = true
		m.clearTextSelection()
		return m, nil
	}
	if m.workingBytes == nil {
		m.statusMessage = "no working copy — press v to format & validate first"
		return m, nil
	}
	if !m.workingValidated {
		m.statusMessage = "✗ validation failed — fix errors before saving"
		return m, nil
	}
	if bytes.Equal(m.workingBytes, m.loadedBytes) {
		m.statusMessage = "no changes to save"
		return m, nil
	}
	m.showSaveConfirm = true
	m.clearTextSelection()
	return m, nil
}

// updateSaveConfirmKey handles keys when the save-confirmation modal
// is open. Enter confirms (closes the modal and starts the async
// save); Esc and q cancel. Cancelling an editor edit also discards the
// pending edit, since the operator declined to apply it.
func (m *Model) updateSaveConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.closeSaveConfirm()
		m.pendingEdit = nil
		m.pendingDelete = nil
		m.statusMessage = "save cancelled"
	case "enter":
		m.closeSaveConfirm()
		m.saving = true
		m.statusMessage = "saving…"
		return m, m.saveCmd()
	}
	return m, nil
}

// closeSaveConfirm dismisses the save-confirmation modal.
func (m *Model) closeSaveConfirm() {
	m.showSaveConfirm = false
}

// saveCmd returns a tea.Cmd that calls the injected saver in a
// goroutine and reports the result as a saveResultMsg. A pending editor
// edit or a pending delete is saved to its own document path (which may
// be an imported file); otherwise the root working copy is saved to the
// config path.
func (m *Model) saveCmd() tea.Cmd {
	saver := m.saver
	path := m.state.Settings.ConfigPath
	original := m.loadedBytes
	working := m.workingBytes
	if m.pendingEdit != nil {
		path = m.pendingEdit.path
		original = m.pendingEdit.original
		working = m.pendingEdit.content
	} else if m.pendingDelete != nil {
		path = m.pendingDelete.path
		original = m.pendingDelete.original
		working = m.pendingDelete.content
	}
	return func() tea.Msg {
		result, err := saver.Save(context.Background(), path, original, working)
		return saveResultMsg{Result: result, Err: err}
	}
}

// handleSaveResult is invoked on the main goroutine when the saver
// returns. On success it refreshes the saved document in memory (the
// root or an imported file) and, for a root save, the loaded snapshot,
// so the source viewport and the diff command reflect the new state.
// On failure it surfaces the specific error in the status line,
// including the backup path when one was created.
func (m *Model) handleSaveResult(msg saveResultMsg) (tea.Model, tea.Cmd) {
	m.saving = false
	if msg.Err == nil {
		path := m.state.Settings.ConfigPath
		content := m.workingBytes
		if m.pendingEdit != nil {
			path = m.pendingEdit.path
			content = m.pendingEdit.content
		} else if m.pendingDelete != nil {
			path = m.pendingDelete.path
			content = m.pendingDelete.content
		}
		// Refresh the exact document the save targeted: imported files
		// keep their own Source, the root keeps its own as well.
		if m.state != nil && m.state.Graph != nil {
			cleanPath := filepath.Clean(path)
			for _, doc := range m.state.Graph.Documents {
				if doc != nil && filepath.Clean(doc.Path) == cleanPath {
					doc.Source = append([]byte(nil), content...)
				}
			}
		}
		if filepath.Clean(path) == filepath.Clean(m.state.Settings.ConfigPath) {
			m.loadedBytes = append([]byte(nil), content...)
			m.workingBytes = append([]byte(nil), content...)
		}
		// The saved document bytes replaced the in-memory source, so the
		// source pane must reload its content AND re-reveal the selected
		// node on the next render. A plain selection-key comparison would
		// suppress the reveal because the selection did not change.
		m.sourceRefresh = true
		// The file on disk changed: until a reload proves otherwise,
		// the running config no longer matches it.
		m.loaded = loadedStale
		m.loadedAt = time.Time{}
		status := "✓ saved (backup: " + msg.Result.BackupPath + ")"
		if msg.Result.RetentionErr != nil {
			// Cleanup failures are surfaced exactly like rollback does:
			// the save itself completed, the failure is reported.
			status += " · ✗ retention cleanup failed: " + msg.Result.RetentionErr.Error()
			m.recordError("save retention", msg.Result.RetentionErr.Error(), "remove old backups manually or lower --backup-retention")
		}
		if m.pendingEdit != nil || m.pendingDelete != nil {
			// An editor edit or a delete can change the document
			// structure: rebuild the tree from the freshly written file so
			// the new structure, the selection range and the source pane
			// stay in sync. For a delete, pendingDelete carries no node
			// identity, so the selection falls back to the document row —
			// the stable anchor requested for deletes.
			if !m.refreshAfterStructuralSave(path) {
				status += " · tree refresh failed"
			}
		}
		// The saved document is now the graph state: re-seed the change
		// monitor so it compares against the freshly written bytes.
		m.syncMonitor()
		m.pendingEdit = nil
		m.pendingDelete = nil
		m.statusMessage = status
		return m, nil
	}
	if errors.Is(msg.Err, app.ErrConflict) {
		m.statusMessage = "✗ file changed on disk — reload before saving"
		m.recordError("save", "file changed on disk since it was loaded", "reload the file from disk before saving")
		m.reopenEditDiff()
		return m, nil
	}
	var saveErr *app.SaveError
	if errors.As(msg.Err, &saveErr) {
		m.statusMessage = "✗ save failed (backup: " + saveErr.BackupPath + "): " + saveErr.Err.Error() + " · press B to compare the recovery backup"
		m.recordError("save", saveErr.Err.Error(), "press B on the document to inspect the recovery backup: "+saveErr.BackupPath)
		m.reopenEditDiff()
		return m, nil
	}
	m.statusMessage = "✗ save failed: " + msg.Err.Error()
	m.recordError("save", msg.Err.Error(), "check the file and retry the save")
	m.reopenEditDiff()
	return m, nil
}

// refreshAfterStructuralSave reloads the graph through the loader after an
// editor save and rebuilds the tree, because an edit can add or remove
// nodes: updating doc.Source in place alone leaves the tree, the selection
// range and the source pane on the pre-edit structure. The file was
// already written atomically by the saver, so the loader re-reads the new
// content and resolves the imports again. It reports whether the reload
// succeeded; the caller keeps the saved-state message either way. The
// runtime loaded state stays stale (file saved but not reloaded in Caddy).
func (m *Model) refreshAfterStructuralSave(path string) bool {
	if m.loader == nil {
		return false
	}
	state, err := m.loader.LoadState()
	if err != nil || state == nil || state.Graph == nil {
		// The file is saved and the raw source view still shows the new
		// bytes via the in-place update; only the tree is stale. Surface
		// it and keep the saved state.
		return false
	}
	m.state.Graph = state.Graph
	m.items = buildItems(state.Graph, m.collapsed)

	pe := m.pendingEdit
	cleanPath := filepath.Clean(path)
	idx := -1
	if pe != nil && pe.itemKey != "" && (pe.operation != "reorder" || pe.startLine <= 0) {
		// Prefer the exact pre-edit row identity: the node survived at
		// the same range, so its stable key still matches. A reorder with
		// a recorded destination line skips this: the moved block vacates
		// its old range, which another same-named sibling may now occupy,
		// so the key match would land on the wrong row.
		for i := range m.items {
			if m.items[i].key == pe.itemKey {
				idx = i
				break
			}
		}
	}
	if idx < 0 && pe != nil && pe.nodeName != "" {
		// The edit moved or resized the node: fall back to the same name
		// in the saved document. A reorder records the moved block's exact
		// post-edit line, so the nearest same-named candidate is the moved
		// node itself even when siblings repeat (for example multiple
		// handle blocks). Non-reorder edits keep the first same-name hit.
		bestDistance := int(^uint(0) >> 1)
		for i := range m.items {
			it := &m.items[i]
			if it.doc != nil && filepath.Clean(it.doc.Path) == cleanPath && it.hasNode && it.node.Name == pe.nodeName {
				if pe.operation != "reorder" || pe.startLine <= 0 {
					idx = i
					break
				}
				distance := it.node.Range.StartLine - pe.startLine
				if distance < 0 {
					distance = -distance
				}
				if distance < bestDistance {
					bestDistance = distance
					idx = i
				}
			}
		}
	}
	if idx < 0 && pe != nil && pe.commentStartLine > 0 {
		// A comment edit can add or remove lines inside the group, so its
		// range (and its stable key) may change. Comment groups carry no
		// node identity: re-anchor on the nearest comment group by its
		// start line in the saved document.
		bestDistance := int(^uint(0) >> 1)
		for i := range m.items {
			it := &m.items[i]
			if it.doc != nil && filepath.Clean(it.doc.Path) == cleanPath && it.comment != nil {
				distance := it.comment.StartLine - pe.commentStartLine
				if distance < 0 {
					distance = -distance
				}
				if distance < bestDistance {
					bestDistance = distance
					idx = i
				}
			}
		}
	}
	if idx < 0 {
		// Fall back to the document row of the saved document.
		for i := range m.items {
			it := &m.items[i]
			if it.doc != nil && !it.hasNode && filepath.Clean(it.doc.Path) == cleanPath {
				idx = i
				break
			}
		}
	}
	if idx >= 0 {
		m.cursor = idx
	}
	if pe != nil && pe.startLine > 0 {
		m.sourceRevealLine = pe.startLine
	}
	// Keep the root snapshots aligned with what is now on disk.
	if filepath.Clean(path) == filepath.Clean(m.state.Settings.ConfigPath) {
		m.loadedBytes = append([]byte(nil), state.Graph.Root.Source...)
		m.workingBytes = append([]byte(nil), state.Graph.Root.Source...)
	}
	m.sourceRefresh = true
	return true
}

// reopenEditDiff reopens the diff modal for the pending editor edit or
// pending delete after a failed save, so the operator can retry with Enter
// or discard with Esc. It is a no-op when neither is pending. The status
// message set by the caller stays visible above the reopened modal.
func (m *Model) reopenEditDiff() {
	if pe := m.pendingEdit; pe != nil {
		lines, err := diff.Unified(pe.original, pe.content, pe.path, pe.path+" (edited)")
		if err != nil {
			return
		}
		m.showDiffModal(lines, "Diff · "+pe.path)
		return
	}
	if pd := m.pendingDelete; pd != nil {
		lines, err := diff.Unified(pd.original, pd.content, pd.path, pd.path+" (after delete)")
		if err != nil {
			return
		}
		m.showDiffModal(lines, "Delete · "+pd.path)
	}
}

// startReload begins the reload workflow. It is gated by a loaded
// graph, a configured reloader, an in-flight guard (reload and save
// share the one shot at the file on disk), a working copy that
// validated, no unsaved changes and a reload that is actually needed.
// When all guards pass it opens the reload-confirmation modal so the
// operator can review the Admin API target before confirming.
func (m *Model) startReload() (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Graph == nil {
		return m, nil
	}
	if m.reloader == nil {
		m.statusMessage = "reload unavailable — needs --caddy-path and a running Admin API"
		return m, nil
	}
	if m.reloading || m.saving || m.rollingBack {
		return m, nil
	}
	if m.workingBytes == nil {
		m.statusMessage = "no working copy — press v to format & validate first"
		return m, nil
	}
	if !m.workingValidated {
		m.statusMessage = "✗ validation failed — fix errors before reloading"
		return m, nil
	}
	if !bytes.Equal(m.workingBytes, m.loadedBytes) {
		m.statusMessage = "save changes before reloading"
		return m, nil
	}
	if m.loaded == loadedMatches {
		m.statusMessage = "configuration already loaded — no reload needed"
		return m, nil
	}
	m.showReloadConfirm = true
	m.clearTextSelection()
	return m, nil
}

// updateReloadConfirmKey handles keys when the reload-confirmation
// modal is open. Enter confirms (closes the modal and starts the async
// reload); Esc and q cancel.
func (m *Model) updateReloadConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.closeReloadConfirm()
		m.statusMessage = "reload cancelled"
	case "enter":
		m.closeReloadConfirm()
		m.reloading = true
		m.statusMessage = "reloading…"
		return m, m.reloadCmd()
	}
	return m, nil
}

// closeReloadConfirm dismisses the reload-confirmation modal.
func (m *Model) closeReloadConfirm() {
	m.showReloadConfirm = false
}

// reloadCmd returns a tea.Cmd that calls the injected reloader in a
// goroutine and reports the result as a reloadResultMsg.
func (m *Model) reloadCmd() tea.Cmd {
	reloader := m.reloader
	path := m.state.Settings.ConfigPath
	// loadedBytes is what is on disk now: after a successful save it is
	// the working copy, and the r guard already ensured workingBytes ==
	// loadedBytes.
	saved := m.loadedBytes
	return func() tea.Msg {
		result, err := reloader.Reload(context.Background(), path, saved)
		return reloadResultMsg{Result: result, Err: err}
	}
}

// handleReloadResult is invoked on the main goroutine when the reloader
// returns. On success the loaded state provably matches the file on
// disk. On failure the specific failure mode is mapped to a loaded
// state so the header badge and the status line agree about what
// happened.
func (m *Model) handleReloadResult(msg reloadResultMsg) (tea.Model, tea.Cmd) {
	m.reloading = false
	if msg.Err == nil {
		m.loaded = loadedMatches
		m.loadedAt = msg.Result.LoadedAt
		m.statusMessage = "✓ reloaded via " + msg.Result.Endpoint
		return m, nil
	}
	var reloadErr *app.ReloadError
	if !errors.As(msg.Err, &reloadErr) {
		m.statusMessage = "✗ reload failed: " + msg.Err.Error()
		m.recordError("reload", msg.Err.Error(), "check the Admin API endpoint and the saved file, then retry")
		return m, nil
	}
	switch {
	case errors.Is(msg.Err, app.ErrConflict):
		m.loaded = loadedUnknown
		m.statusMessage = "✗ file changed on disk since save — reload aborted"
		m.recordError("reload", "file changed on disk since save", "reload the file from disk before retrying the reload")
	case errors.Is(msg.Err, app.ErrAdminUnreachable), errors.Is(msg.Err, app.ErrAdminTimeout):
		m.loaded = loadedUnreachable
		m.statusMessage = "✗ reload failed (file saved, backup intact): " + msg.Err.Error()
		m.recordError("reload", msg.Err.Error(), "verify the Admin API endpoint and that Caddy is running, then retry")
	default:
		m.loaded = loadedStale
		m.statusMessage = "✗ reload failed (file saved, backup intact): " + msg.Err.Error()
		m.recordError("reload", msg.Err.Error(), "check the reported rejection and retry the reload")
	}
	return m, nil
}

// saveConfirmView renders the save-confirmation modal. It names the
// target path and the backup directory (safety requirement for any
// replacing action) and offers Enter to confirm or Esc to cancel. An
// editor edit names its own document path, which may be an imported
// file.
func (m *Model) saveConfirmView(width, height int) string {
	title := "Save config · Enter save · Esc cancel"
	path := m.state.Settings.ConfigPath
	hint := dimStyle.Render("review the diff with D before confirming")
	if m.pendingEdit != nil {
		title = "Save " + pendingEditName(m.pendingEdit) + " · Enter save · Esc cancel"
		path = m.pendingEdit.path
		hint = dimStyle.Render("the edit applies only to the selected node range")
	}
	bodyH := height - 4 // border (2) + title (1) + blank separator (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	var body strings.Builder
	body.WriteString(dimStyle.Render("Path       ") + path + "\n")
	body.WriteString(dimStyle.Render("Backup dir ") + m.state.Settings.BackupDir + "\n")
	body.WriteString("\n")
	body.WriteString(dimStyle.Render("a backup is created before the file is replaced") + "\n")
	body.WriteString(hint)
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(activeTitleStyle.Render(title) + "\n" + body.String())
}

// reloadConfirmView renders the centered reload-confirmation modal. It names
// the target path and Admin API endpoint while keeping the confirmation keys
// in the modal footer, matching the command palette and search surfaces.
func (m *Model) reloadConfirmView(width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	boxW := width - 8
	if boxW < 56 {
		boxW = width - 2
	}
	if boxW > 78 {
		boxW = 78
	}
	if boxW < 1 {
		boxW = 1
	}
	boxH := 12
	if height-6 < boxH {
		boxH = height - 6
	}
	if boxH < 8 {
		boxH = 8
	}

	contentW := boxW - 4
	if contentW < 1 {
		contentW = 1
	}
	header := activeTitleStyle.Render("RELOAD CONFIG") + " " + dimStyle.Render("confirm")
	separator := dimStyle.Render(strings.Repeat("─", contentW))
	path := "—"
	adminEndpoint := "—"
	if m.state != nil {
		if m.state.Settings.ConfigPath != "" {
			path = m.state.Settings.ConfigPath
		}
		if m.state.Settings.AdminEndpoint != "" {
			adminEndpoint = m.state.Settings.AdminEndpoint
		}
	}
	lines := []string{
		dimStyle.Render("Path      ") + truncateToWidth(path, max(1, contentW-10)),
		dimStyle.Render("Admin API ") + truncateToWidth(adminEndpoint, max(1, contentW-10)),
		"",
		dimStyle.Render("the saved file and its backup stay intact if the reload fails"),
		dimStyle.Render("reloads through the local Admin API after a confirmed save"),
	}
	for i, line := range lines {
		lines[i] = truncateToWidth(line, contentW)
	}
	body := strings.Join(lines, "\n")
	footer := renderFooterKeys("Enter reload · Esc cancel")
	content := strings.Join([]string{header, separator, body, separator, footer}, "\n")
	return commandPaletteStyle.Width(boxW - 2).Height(boxH - 2).Render(content)
}
