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

// watchCmd returns a tea.Cmd that blocks on the injected change monitor
// until an external change is detected or the monitor is closed, then
// delivers an externalChangeMsg. The handler re-arms the watch by
// returning this command again; while the conflict modal is open no new
// watch is armed, and changes detected in the meantime are queued by the
// monitor (latest-wins) for the next arm.
func (m *Model) watchCmd() tea.Cmd {
	mon := m.monitor
	if mon == nil {
		return nil
	}
	return func() tea.Msg {
		change, err := mon.Next(context.Background())
		return externalChangeMsg{change: change, err: err}
	}
}

// syncMonitor re-targets the change monitor at the currently resolved
// documents. An Update failure (for example an unreadable directory)
// disables the feature with an explicit status message; the synchronous
// conflict guards remain active either way.
func (m *Model) syncMonitor() {
	if m.monitor == nil || m.state == nil || m.state.Graph == nil {
		return
	}
	if err := m.monitor.Update(app.ChangeTargets(m.state.Graph.Documents)); err != nil {
		m.monitor = nil
		m.statusMessage = "✗ external change watching unavailable: " + err.Error()
		m.recordError("external change watch", err.Error(), "fix the directory permissions and restart; the synchronous save/reload conflict guards stay active")
	}
}

// handleExternalChange is invoked when the change monitor delivers a
// detection. Monitor failures disable the feature with an explicit
// status line. Detections are ignored when a mutating workflow is in
// flight (save, reload, editor, delete — each has its own synchronous
// conflict guard), when the conflict modal is already open, or when the
// on-disk bytes already match the in-memory document (for example the
// notification produced by our own atomic save). Otherwise the conflict
// modal opens with reload/compare/keep options.
func (m *Model) handleExternalChange(msg externalChangeMsg) (tea.Model, tea.Cmd) {
	if m.monitor == nil {
		// The feature was disabled (or never wired); a stale message
		// cannot open the conflict modal.
		return m, nil
	}
	if errors.Is(msg.err, app.ErrChangeClosed) {
		// The monitor was closed: stop watching, keep browsing.
		m.monitor = nil
		return m, nil
	}
	if msg.err != nil {
		m.monitor = nil
		m.statusMessage = "✗ external change watching failed: " + msg.err.Error()
		m.recordError("external change watch", msg.err.Error(), "restart lazycaddy to re-arm watching; the synchronous conflict guards stay active")
		return m, nil
	}
	if m.inFlightWorkflow() {
		return m, m.watchCmd()
	}
	if m.showChangeConflict {
		return m, m.watchCmd()
	}
	if m.bytesMatchMemory(msg.change) {
		return m, m.watchCmd()
	}

	// Snapshot the affected document's in-memory source so the compare
	// diff is stable for the whole conflict flow.
	var inMem []byte
	if m.state != nil && m.state.Graph != nil {
		cleanPath := filepath.Clean(msg.change.Path)
		for _, doc := range m.state.Graph.Documents {
			if doc != nil && filepath.Clean(doc.Path) == cleanPath {
				inMem = append([]byte(nil), doc.Source...)
				break
			}
		}
	}
	m.pendingChange = &pendingChange{change: msg.change, inMem: inMem}
	m.changeCompare = false
	m.showChangeConflict = true
	m.statusMessage = ""
	return m, nil
}

// inFlightWorkflow reports whether a mutating workflow with its own
// synchronous conflict guard is active. The change monitor defers to
// those guards: reporting the same conflict twice would be noise.
func (m *Model) inFlightWorkflow() bool {
	return m.saving || m.reloading || m.editing || m.deleting || m.rollingBack ||
		m.pendingEdit != nil || m.pendingDelete != nil || m.pendingRollback != nil
}

// bytesMatchMemory reports whether the detected change is already
// reflected in the in-memory graph (the on-disk bytes equal the loaded
// document source), which makes it a non-event: the file on disk and the
// state the UI shows agree. This covers the notification produced by
// lazycaddy's own atomic save, which races the monitor's debounce.
func (m *Model) bytesMatchMemory(change app.ExternalChange) bool {
	if change.Missing || m.state == nil || m.state.Graph == nil {
		return false
	}
	cleanPath := filepath.Clean(change.Path)
	for _, doc := range m.state.Graph.Documents {
		if doc != nil && filepath.Clean(doc.Path) == cleanPath {
			return bytes.Equal(change.OnDisk, doc.Source)
		}
	}
	// A path that is no longer part of the graph is stale: nothing to
	// compare, nothing to reload.
	return true
}

// hasUnsavedEdits reports whether the operator has in-memory changes
// that a graph reload would discard: a pending editor edit or delete, or
// a working copy that differs from the loaded bytes.
func (m *Model) hasUnsavedEdits() bool {
	if m.pendingEdit != nil || m.pendingDelete != nil {
		return true
	}
	return m.workingBytes != nil && !bytes.Equal(m.workingBytes, m.loadedBytes)
}

// updateChangeConflictKey handles keys while the conflict modal is open.
// With no unsaved edits, reload is safe and only keep is offered next to
// it; with unsaved edits, reload explicitly discards them (the modal text
// names that consequence — choosing it is the confirmation), compare
// opens the in-memory vs on-disk diff, and keep retains the in-memory
// version. Esc, q and k always keep; Enter and r always reload; c
// compares when unsaved edits exist.
func (m *Model) updateChangeConflictKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "k":
		return m.resolveChangeKeep()
	case "enter", "r":
		return m.resolveChangeReload()
	case "c":
		if m.hasUnsavedEdits() {
			return m.openChangeCompare()
		}
	case "ctrl+c":
		return m.requestQuit()
	}
	return m, nil
}

// resolveChangeKeep keeps the in-memory version and resumes watching.
// The monitor already advanced its snapshot to the on-disk bytes when it
// reported the change, so only genuinely new changes surface again.
func (m *Model) resolveChangeKeep() (tea.Model, tea.Cmd) {
	if pc := m.pendingChange; pc != nil {
		if pc.change.Missing {
			m.statusMessage = "kept in-memory version — " + pc.change.Path + " is missing on disk"
		} else {
			m.statusMessage = "kept in-memory version — " + pc.change.Path + " differs on disk"
		}
	}
	m.closeChangeConflict()
	return m, m.watchCmd()
}

// resolveChangeReload discards the in-memory changes (the modal text
// names that consequence, so choosing reload is the confirmation) and
// reloads the whole graph from disk through the injected loader. It
// never touches the running Caddy configuration: the loaded state is set
// to unknown until an explicit r reload proves it again.
func (m *Model) resolveChangeReload() (tea.Model, tea.Cmd) {
	path := ""
	if m.pendingChange != nil {
		path = m.pendingChange.change.Path
	}
	m.closeChangeConflict()
	if m.loader == nil || m.state == nil || m.state.Graph == nil {
		m.statusMessage = "✗ reload failed: no configuration loaded"
		return m, m.watchCmd()
	}
	state, err := m.loader.LoadState()
	if err != nil || state == nil || state.Graph == nil {
		if err != nil {
			m.statusMessage = "✗ reload failed: " + err.Error()
		} else {
			m.statusMessage = "✗ reload failed"
		}
		return m, m.watchCmd()
	}
	m.state.Graph = state.Graph
	// Re-anchor the cursor on the previously selected row's stable key; a
	// rebuild must never lose the selection, and the key survives a graph
	// reload as long as the row still exists.
	prevKey := ""
	if sel := m.selectedItem(); sel != nil {
		prevKey = sel.key
	}
	m.rebuildTree(prevKey)
	// Discard the in-memory working state: the graph now reflects disk.
	m.loadedBytes = append([]byte(nil), state.Graph.Root.Source...)
	m.workingBytes = nil
	m.workingValidated = false
	m.pendingEdit = nil
	m.pendingDelete = nil
	m.showSaveConfirm = false
	m.showReloadConfirm = false
	m.sourceRefresh = true
	// The file changed: the relationship to the running config is
	// unknown until an explicit reload proves it.
	m.loaded = loadedUnknown
	m.loadedAt = time.Time{}
	m.syncMonitor()
	if path == "" {
		m.statusMessage = "✓ reloaded from disk"
	} else {
		m.statusMessage = "✓ reloaded " + path + " from disk"
	}
	return m, m.watchCmd()
}

// openChangeCompare opens the shared diff modal with the in-memory vs
// on-disk diff of the affected document. Esc returns to the conflict
// options; the conflict modal stays open underneath.
func (m *Model) openChangeCompare() (tea.Model, tea.Cmd) {
	pc := m.pendingChange
	if pc == nil {
		return m, nil
	}
	var lines []diff.Line
	var err error
	if pc.change.Missing {
		lines, err = diff.Unified(pc.inMem, nil, pc.change.Path+" (in memory)", pc.change.Path+" (deleted on disk)")
	} else {
		lines, err = diff.Unified(pc.inMem, pc.change.OnDisk, pc.change.Path+" (in memory)", pc.change.Path+" (on disk)")
	}
	if err != nil {
		m.statusMessage = "✗ diff failed: " + err.Error()
		return m, nil
	}
	m.showDiffModal(lines, "Compare · "+pc.change.Path)
	m.changeCompare = true
	return m, nil
}

// closeChangeConflict closes the conflict modal and its compare diff.
// Callers set the status message afterwards.
func (m *Model) closeChangeConflict() {
	m.showChangeConflict = false
	m.changeCompare = false
	m.pendingChange = nil
	m.showDiff = false
	m.diffLines = nil
	m.diffTitle = ""
	m.diffViewport.SetContent("")
}

// changeConflictView renders the external-change conflict modal. It
// names the exact file that changed (root or imported) and explains the
// state, so the operator can decide between reload (discarding any
// unsaved edits — the text names that consequence, choosing reload is
// the confirmation), compare (diff in-memory vs on-disk) and keep
// (retain the in-memory version). Without unsaved edits, compare is not
// offered and reload is safe.
func (m *Model) changeConflictView(width, height int) string {
	path := ""
	missing := false
	if pc := m.pendingChange; pc != nil {
		path = pc.change.Path
		missing = pc.change.Missing
	}
	if path == "" {
		path = "unknown"
	}
	title := "External change · " + truncateToWidth(path, width-20)
	bodyH := height - 4 // border (2) + title (1) + blank separator (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	var body strings.Builder
	body.WriteString(dimStyle.Render("File   ") + truncateToWidth(path, paneContentW-14) + "\n")
	if missing {
		body.WriteString(errorStyle.Render("✗ the file was removed or moved on disk") + "\n")
	} else {
		body.WriteString(dimStyle.Render("State  ") + "the file changed on disk\n")
	}
	body.WriteString("\n")
	if m.hasUnsavedEdits() {
		body.WriteString(statusWarningStyle.Render("unsaved edits exist — reload discards them") + "\n")
		body.WriteString("r reload (discards edits) · c compare · k keep\n")
	} else {
		body.WriteString(dimStyle.Render("no unsaved edits — reloading is safe") + "\n")
		body.WriteString("r reload · Esc keep\n")
	}
	body.WriteString(dimStyle.Render("the running Caddy configuration is never reloaded implicitly"))
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(activeTitleStyle.Render(title) + "\n" + body.String())
}
