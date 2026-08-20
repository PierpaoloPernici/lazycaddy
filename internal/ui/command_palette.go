package ui

import (
	"fmt"
	"strings"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/cellbuf"
)

// commandID is the stable identity shared by direct keybindings and the
// command palette. Keeping the identity separate from its key lets the
// palette remain useful when a command has no convenient single shortcut.
type commandID string

const (
	commandMoveSelection  commandID = "move-selection"
	commandToggleBranch   commandID = "toggle-branch"
	commandExpandAll      commandID = "expand-all"
	commandCollapseAll    commandID = "collapse-all"
	commandMatcherNext    commandID = "matcher-next"
	commandReviewInline   commandID = "review-inline"
	commandValidate       commandID = "validate"
	commandDiff           commandID = "diff"
	commandSave           commandID = "save"
	commandReload         commandID = "reload"
	commandLogs           commandID = "logs"
	commandRuntime        commandID = "runtime-dashboard"
	commandTLS            commandID = "tls-dashboard"
	commandLogFollow      commandID = "log-follow"
	commandLogFilter      commandID = "log-filter"
	commandLogClearFilter commandID = "log-clear-filter"
	commandLogPause       commandID = "log-pause"
	commandLogDetail      commandID = "log-detail"
	commandEdit           commandID = "edit"
	commandFullEdit       commandID = "full-edit"
	commandAdd            commandID = "add-structured"
	commandNew            commandID = "new-node"
	commandReorder        commandID = "reorder"
	commandEditForm       commandID = "edit-directive-form"
	commandDelete         commandID = "delete"
	commandBackups        commandID = "backups"
	commandErrors         commandID = "errors"
	commandCopy           commandID = "copy"
	commandSelectText     commandID = "select-text"
	commandSearch         commandID = "search"
	commandHelp           commandID = "caddyfile-help"
	commandQuit           commandID = "quit"
	commandPalette        commandID = "command-palette"
)

// uiCommand describes one user action. Keys are the direct shortcuts shown
// in the palette; the first key is the compact label used in command rows.
// Enabled and Reason are evaluated against the current model so unavailable
// capabilities are discoverable without pretending they can run.
type uiCommand struct {
	ID          commandID
	Category    string
	Label       string
	Description string
	Keys        []string
	Enabled     func(*Model) bool
	Reason      func(*Model) string
}

func commandDefinitions() []uiCommand {
	return []uiCommand{
		{ID: commandMoveSelection, Category: "Navigation", Label: "Move selection", Description: "tree", Keys: []string{"↑↓"}, Enabled: func(*Model) bool {
			return true
		}, Reason: func(*Model) string { return "" }},
		{ID: commandToggleBranch, Category: "Navigation", Label: "Open or toggle branch", Description: "current row", Keys: []string{"Enter", "Space", "←→"}, Enabled: func(m *Model) bool {
			sel := m.selectedItem()
			return sel != nil && sel.hasChildren
		}, Reason: func(*Model) string { return "selected row is a leaf" }},
		{ID: commandExpandAll, Category: "Navigation", Label: "Expand all branches", Description: "tree", Keys: []string{"+"}, Enabled: func(m *Model) bool {
			return m.state != nil && m.state.Graph != nil
		}, Reason: func(*Model) string { return "no configuration loaded" }},
		{ID: commandCollapseAll, Category: "Navigation", Label: "Collapse all branches", Description: "keep documents open", Keys: []string{"-"}, Enabled: func(m *Model) bool {
			return m.state != nil && m.state.Graph != nil
		}, Reason: func(*Model) string { return "no configuration loaded" }},
		{ID: commandMatcherNext, Category: "Navigation", Label: "Goto matcher (next)", Description: "cycle matcher definitions & references", Keys: []string{"g"}, Enabled: func(m *Model) bool {
			return m.sourceDoc != nil
		}, Reason: func(*Model) string { return "no document selected" }},
		{ID: commandSearch, Category: "Navigation", Label: "Search Caddyfile", Description: "read-only search", Keys: []string{"/", "Ctrl-F"}, Enabled: func(m *Model) bool {
			return m.searcher != nil
		}, Reason: func(*Model) string { return "search unavailable" }},
		{ID: commandHelp, Category: "Navigation", Label: "Open Caddyfile help", Description: "official Caddy documentation", Keys: []string{"Ctrl-H"}, Enabled: func(m *Model) bool {
			return m.browser != nil
		}, Reason: func(*Model) string { return "browser help unavailable" }},
		{ID: commandValidate, Category: "Validation", Label: "Format & validate", Description: "Caddy binary", Keys: []string{"v"}, Enabled: func(m *Model) bool {
			return m.formatter != nil
		}, Reason: func(*Model) string { return "Caddy binary unavailable" }},
		{ID: commandReviewInline, Category: "Validation", Label: "Review inline findings", Description: "advisory parse-tree lint in the source pane", Keys: []string{"i"}, Enabled: func(m *Model) bool {
			return m.sourceDoc != nil
		}, Reason: func(*Model) string { return "no document selected" }},
		{ID: commandDiff, Category: "Validation", Label: "Show diff", Description: "working copy or disk", Keys: []string{"D"}, Enabled: func(m *Model) bool {
			return m.state != nil && m.state.Graph != nil
		}, Reason: func(*Model) string { return "no configuration loaded" }},
		{ID: commandSave, Category: "Validation", Label: "Save validated changes", Description: "write to disk", Keys: []string{"s"}, Enabled: func(m *Model) bool {
			return m.saver != nil && m.state != nil && !m.state.Settings.ReadOnly
		}, Reason: func(*Model) string { return "read-only mode" }},
		{ID: commandEdit, Category: "Source", Label: "Edit selected block", Description: "$EDITOR", Keys: []string{"e"}, Enabled: func(m *Model) bool {
			return m.canEditSelection()
		}, Reason: func(m *Model) string {
			if m.editor == nil {
				return "editor unavailable"
			}
			return "requires writable mode and a block or comment selection"
		}},
		{ID: commandFullEdit, Category: "Source", Label: "Edit selected document", Description: "$EDITOR", Keys: []string{"E"}, Enabled: func(m *Model) bool {
			return m.canEditDocument()
		}, Reason: func(m *Model) string {
			if m.editor == nil {
				return "editor unavailable"
			}
			return "requires writable mode and a document selection"
		}},
		{ID: commandAdd, Category: "Source", Label: "Add directive", Description: "context-aware", Keys: []string{"a"}, Enabled: func(m *Model) bool {
			return m.canAddStructured() || m.canAddComment()
		}, Reason: func(m *Model) string {
			if m.state == nil || m.state.Settings.ReadOnly || m.saver == nil {
				return "read-only mode"
			}
			if m.formatter == nil {
				return "Caddy binary unavailable"
			}
			return "select a supported block, document or comment"
		}},
		{ID: commandNew, Category: "Source", Label: "New structural node", Description: "site, snippet or handler", Keys: []string{"n"}, Enabled: func(m *Model) bool {
			return m.canNewNode()
		}, Reason: func(m *Model) string {
			if m.state == nil || m.state.Settings.ReadOnly || m.saver == nil {
				return "read-only mode"
			}
			if m.formatter == nil {
				return "Caddy binary unavailable"
			}
			return "select a document or structural block"
		}},
		{ID: commandReorder, Category: "Source", Label: "Move selected block after sibling", Description: "move after sibling", Keys: []string{"o"}, Enabled: func(m *Model) bool {
			return m.canReorderSelected()
		}, Reason: func(m *Model) string {
			if m.state == nil || m.state.Settings.ReadOnly || m.saver == nil {
				return "read-only mode"
			}
			if m.formatter == nil {
				return "Caddy binary unavailable"
			}
			return "select a block with a reorderable sibling"
		}},
		{ID: commandEditForm, Category: "Source", Label: "Edit directive form", Description: "structured fields for common directives", Keys: []string{"m"}, Enabled: func(m *Model) bool {
			return m.canEditDirectiveForm()
		}, Reason: func(m *Model) string {
			if m.state == nil || m.state.Settings.ReadOnly || m.saver == nil {
				return "read-only mode"
			}
			if m.formatter == nil {
				return "Caddy binary unavailable"
			}
			return "select a supported directive"
		}},
		{ID: commandDelete, Category: "Source", Label: "Delete selected block", Description: "validate then diff", Keys: []string{"d"}, Enabled: func(m *Model) bool {
			return m.canDeleteSelected()
		}, Reason: func(m *Model) string {
			if m.state == nil || m.state.Settings.ReadOnly || m.saver == nil {
				return "read-only mode"
			}
			if m.formatter == nil {
				return "Caddy binary unavailable"
			}
			return "select a deletable node"
		}},
		{ID: commandCopy, Category: "Source", Label: "Copy selected block", Description: "exact bytes (or active text selection)", Keys: []string{"y"}, Enabled: func(m *Model) bool {
			return m.clipboard != nil
		}, Reason: func(*Model) string { return "clipboard unavailable" }},
		{ID: commandSelectText, Category: "Selection", Label: "Select text", Description: "mouse drag or Shift+arrows", Keys: []string{"Shift+↑↓←→"}, Enabled: func(*Model) bool {
			return true
		}, Reason: func(*Model) string { return "" }},
		{ID: commandReload, Category: "Runtime & recovery", Label: "Reload Caddy", Description: "Admin API", Keys: []string{"r"}, Enabled: func(m *Model) bool {
			return m.reloader != nil
		}, Reason: func(*Model) string { return "Caddy reload unavailable" }},
		{ID: commandRuntime, Category: "Runtime & recovery", Label: "Runtime dashboard", Description: "Admin API + upstreams", Keys: []string{"I"}, Enabled: func(*Model) bool {
			return true
		}, Reason: func(*Model) string { return "" }},
		{ID: commandTLS, Category: "Runtime & recovery", Label: "TLS dashboard", Description: "certificates", Keys: []string{"T"}, Enabled: func(*Model) bool {
			return true
		}, Reason: func(*Model) string { return "" }},
		{ID: commandLogs, Category: "Runtime & recovery", Label: "Open logs", Description: "journal / file", Keys: []string{"l"}, Enabled: func(m *Model) bool {
			return m.logSource != nil
		}, Reason: func(*Model) string { return "no log source configured" }},
		{ID: commandBackups, Category: "Runtime & recovery", Label: "Open backups", Description: "recovery", Keys: []string{"B"}, Enabled: func(m *Model) bool {
			return m.rollbacker != nil
		}, Reason: func(*Model) string { return "backup history unavailable" }},
		{ID: commandErrors, Category: "Runtime & recovery", Label: "Open error history", Description: "recent failures", Keys: []string{"H"}, Enabled: func(m *Model) bool {
			return len(m.errorHistory) > 0
		}, Reason: func(*Model) string { return "no recorded failures" }},
		{ID: commandLogFollow, Category: "Logs", Label: "Toggle log follow", Description: "follow on/off", Keys: []string{"f"}, Enabled: func(m *Model) bool {
			return m.showLogs && m.logSource != nil
		}, Reason: func(m *Model) string {
			if !m.showLogs {
				return "open logs first"
			}
			return "no log source configured"
		}},
		{ID: commandLogFilter, Category: "Logs", Label: "Filter logs", Description: "host/status/level/text", Keys: []string{"F"}, Enabled: func(m *Model) bool {
			return m.showLogs && m.logSource != nil
		}, Reason: func(m *Model) string {
			if !m.showLogs {
				return "open logs first"
			}
			return "no log source configured"
		}},
		{ID: commandLogClearFilter, Category: "Logs", Label: "Clear log filter", Description: "show all", Keys: []string{"c"}, Enabled: func(m *Model) bool {
			return m.showLogs && m.logFilterActive
		}, Reason: func(m *Model) string {
			if !m.showLogs {
				return "open logs first"
			}
			return "no active filter"
		}},
		{ID: commandLogPause, Category: "Logs", Label: "Pause/resume log poll", Description: "pause", Keys: []string{"p"}, Enabled: func(m *Model) bool {
			return m.showLogs && m.logSource != nil
		}, Reason: func(m *Model) string {
			if !m.showLogs {
				return "open logs first"
			}
			return "no log source configured"
		}},
		{ID: commandLogDetail, Category: "Logs", Label: "Open log detail", Description: "selected entry", Keys: []string{"Enter"}, Enabled: func(m *Model) bool {
			if !m.showLogs || m.logDetailOpen {
				return false
			}
			entries := m.filteredLogEntries()
			return m.logCursor >= 0 && m.logCursor < len(entries)
		}, Reason: func(m *Model) string {
			if !m.showLogs {
				return "open logs first"
			}
			if m.logDetailOpen {
				return "detail already open"
			}
			return "no log entry selected"
		}},
		{ID: commandQuit, Category: "Application", Label: "Quit lazycaddy", Description: "guarded when unsaved", Keys: []string{"q", "Ctrl-C"}, Enabled: func(*Model) bool {
			return true
		}, Reason: func(*Model) string { return "" }},
		{ID: commandPalette, Category: "Application", Label: "Open command palette", Description: "search commands", Keys: []string{"?"}, Enabled: func(*Model) bool {
			return true
		}, Reason: func(*Model) string { return "" }},
	}
}

func commandDefinition(id commandID) (uiCommand, bool) {
	for _, command := range commandDefinitions() {
		if command.ID == id {
			return command, true
		}
	}
	return uiCommand{}, false
}

func (m *Model) runCommand(id commandID) (tea.Model, tea.Cmd) {
	switch id {
	case commandMoveSelection:
		// Movement is represented in the catalog for discoverability; arrow
		// keys are handled directly by the main model.
	case commandToggleBranch:
		m.toggleCursor()
	case commandExpandAll:
		m.expandAllBranches()
	case commandCollapseAll:
		m.collapseDescendants()
	case commandMatcherNext:
		m.gotoNextMatcher()
	case commandReviewInline:
		m.openInlineReview()
	case commandValidate:
		return m.startFormatAndValidate()
	case commandDiff:
		return m.startDiff()
	case commandSave:
		return m.startSave()
	case commandReload:
		return m.startReload()
	case commandLogs:
		return m.toggleLogView()
	case commandLogFollow:
		if !m.showLogs {
			return m, nil
		}
		if m.logFollow {
			m.logFollow = false
			m.statusMessage = "log follow off"
		} else {
			m.logFollow = true
			m.logCursor = len(m.filteredLogEntries()) - 1
			m.logViewport.GotoBottom()
			m.statusMessage = "log follow on"
		}
		return m, nil
	case commandLogFilter:
		if m.showLogs {
			m.openLogFilter()
		}
		return m, nil
	case commandLogClearFilter:
		if m.showLogs && m.logFilterActive {
			m.clearLogFilter()
			m.statusMessage = "log filter cleared"
		}
		return m, nil
	case commandLogPause:
		if !m.showLogs {
			return m, nil
		}
		if m.logPaused {
			m.logPaused = false
			m.statusMessage = "log poll resumed"
			return m, m.logPollCmd()
		}
		m.logPaused = true
		m.statusMessage = "log poll paused"
		return m, nil
	case commandLogDetail:
		if !m.showLogs || m.logDetailOpen {
			return m, nil
		}
		entries := m.filteredLogEntries()
		if m.logCursor >= 0 && m.logCursor < len(entries) {
			m.logDetailEntry = entries[m.logCursor]
			m.logDetailOpen = true
			m.clearTextSelection()
			m.syncLogDetailContent(m.width, m.paneHeight())
			m.logDetailViewport.GotoTop()
		}
		return m, nil
	case commandRuntime:
		return m.toggleRuntimeDashboard()
	case commandTLS:
		return m.toggleTLSDashboard()
	case commandEdit:
		return m.startEditor()
	case commandFullEdit:
		return m.startFullEdit()
	case commandAdd:
		return m.startStructuredAdd()
	case commandNew:
		return m.startNewNode()
	case commandReorder:
		return m.startReorder()
	case commandEditForm:
		return m.startDirectiveForm()
	case commandDelete:
		return m.startDelete()
	case commandBackups:
		return m.startBackups()
	case commandErrors:
		return m.startErrorHistory()
	case commandCopy:
		return m.startCopy()
	case commandSearch:
		return m.startSearch()
	case commandHelp:
		return m.startCaddyfileHelp()
	case commandQuit:
		return m.requestQuit()
	case commandPalette:
		return m.startCommandPalette()
	}
	return m, nil
}

func (m *Model) commandForKey(key string) (commandID, bool) {
	for _, command := range commandDefinitions() {
		for _, commandKey := range command.Keys {
			if commandKey == key {
				return command.ID, true
			}
		}
	}
	return "", false
}

func (m *Model) canEditSelection() bool {
	sel := m.selectedItem()
	return m.editor != nil && m.saver != nil && m.state != nil && !m.state.Settings.ReadOnly && sel != nil && (sel.hasNode || sel.comment != nil)
}

func (m *Model) canEditDocument() bool {
	sel := m.selectedItem()
	return m.editor != nil && m.saver != nil && m.state != nil && !m.state.Settings.ReadOnly && sel != nil && sel.doc != nil
}

func (m *Model) canDeleteSelected() bool {
	sel := m.selectedItem()
	return m.saver != nil && m.formatter != nil && m.state != nil && !m.state.Settings.ReadOnly && sel != nil && sel.hasNode && !(sel.node.Kind == caddyfile.KindDirective && sel.node.Name == "import")
}

func (m *Model) startCommandPalette() (tea.Model, tea.Cmd) {
	m.clearTextSelection()
	m.commandQuery = nil
	m.commandCursor = 0
	m.showCommandPalette = true
	m.commandViewport.GotoTop()
	return m, nil
}

func (m *Model) closeCommandPalette() {
	m.showCommandPalette = false
	m.commandQuery = nil
	m.commandCursor = 0
	m.commandLineOffsets = nil
	m.commandViewport.SetContent("")
}

func (m *Model) updateCommandPaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		m.commandQuery = append(m.commandQuery, msg.Runes...)
		m.commandCursor = 0
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeCommandPalette()
	case "ctrl+c":
		m.closeCommandPalette()
		return m.requestQuit()
	case "backspace":
		if len(m.commandQuery) > 0 {
			m.commandQuery = m.commandQuery[:len(m.commandQuery)-1]
			m.commandCursor = 0
		}
	case "up", "k":
		if m.commandCursor > 0 {
			m.commandCursor--
		}
		m.revealCommandCursor()
	case "down", "j":
		commands := m.filteredCommands()
		if m.commandCursor < len(commands)-1 {
			m.commandCursor++
		}
		m.revealCommandCursor()
	case "pgup":
		m.commandViewport.PageUp()
	case "pgdown":
		m.commandViewport.PageDown()
	case "home":
		m.commandCursor = 0
		m.commandViewport.GotoTop()
	case "end":
		commands := m.filteredCommands()
		if len(commands) > 0 {
			m.commandCursor = len(commands) - 1
		}
		m.revealCommandCursor()
	case "enter":
		commands := m.filteredCommands()
		if m.commandCursor >= 0 && m.commandCursor < len(commands) {
			command := commands[m.commandCursor]
			if !command.Enabled(m) {
				m.statusMessage = "✗ " + command.Label + " unavailable: " + command.Reason(m)
				return m, nil
			}
			m.closeCommandPalette()
			return m.runCommand(command.ID)
		}
	}
	return m, nil
}

func (m *Model) filteredCommands() []uiCommand {
	query := strings.ToLower(strings.TrimSpace(string(m.commandQuery)))
	var matches []uiCommand
	for _, command := range commandDefinitions() {
		if !m.isCommandVisible(command) {
			continue
		}
		if command.ID == commandNew && !m.canNewNode() {
			continue
		}
		if command.ID == commandEditForm && !m.formAvailableForSelection() {
			continue
		}
		if command.ID == commandHelp && m.browser == nil {
			continue
		}
		if query == "" {
			matches = append(matches, command)
			continue
		}
		searchable := strings.ToLower(strings.Join([]string{command.Category, command.Label, command.Description, strings.Join(command.Keys, " ")}, " "))
		if strings.Contains(searchable, query) {
			matches = append(matches, command)
		}
	}
	return matches
}

func (m *Model) isCommandVisible(cmd uiCommand) bool {
	// Global commands are always visible.
	if cmd.ID == commandQuit || cmd.ID == commandPalette || cmd.ID == commandHelp || cmd.ID == commandCopy || cmd.ID == commandSelectText {
		return true
	}
	// Full-screen dashboards and modals are context-aware: only their
	// relevant commands are shown so the palette stays usable at 80
	// columns and doesn't leak homepage commands into the logs view.
	if m.showLogs {
		if m.logDetailOpen {
			// Detail is a scrollable JSON view: only navigation for the
			// detail and the global commands are relevant.
			return cmd.ID == commandMoveSelection
		}
		// Log list: navigation move, the log operational commands and
		// the toggle to close the view. Runtime & recovery is homepage
		///dashboard-only and would show disabled entries in the log
		// palette, so it is hidden here.
		switch cmd.ID {
		case commandMoveSelection, commandLogs, commandLogFollow, commandLogFilter, commandLogClearFilter, commandLogPause, commandLogDetail:
			return true
		}
		return false
	}
	if m.showRuntime || m.showTLS {
		// Runtime and TLS dashboards are currently display-only; the
		// palette would open but show only disabled entries, so it is
		// hidden for now. Global commands remain reachable via direct
		// hotkeys and the palette is not advertised in the footer.
		return false
	}
	if m.showDiagnostics || m.showDetail {
		switch cmd.ID {
		case commandMoveSelection, commandReviewInline, commandValidate, commandDiff, commandHelp:
			return true
		}
		return false
	}
	if m.searchActive || m.showInlineReview || m.showErrorHistory || m.showBackups || m.showDiff || m.showSaveConfirm || m.showReloadConfirm || m.showRollbackConfirm || m.showStructuredAdd || m.showChangeConflict || m.showUnsavedConfirm {
		// Modals and transient views keep the palette navigation-only to
		// avoid leaking homepage source commands into a focused workflow.
		return cmd.ID == commandMoveSelection
	}
	// Homepage: hide log-only operational commands that require the log
	// view to be open; everything else (Navigation, Validation, Source,
	// Runtime) is relevant for the tree/source layout.
	switch cmd.ID {
	case commandLogFollow, commandLogFilter, commandLogClearFilter, commandLogPause, commandLogDetail:
		return false
	}
	return true
}

func (m *Model) revealCommandCursor() {
	if m.commandCursor < 0 || m.commandCursor >= len(m.commandLineOffsets) || m.commandViewport.Height < 1 {
		return
	}
	line := m.commandLineOffsets[m.commandCursor]
	if line < m.commandViewport.YOffset {
		m.commandViewport.SetYOffset(line)
	} else if line >= m.commandViewport.YOffset+m.commandViewport.Height {
		m.commandViewport.SetYOffset(line - m.commandViewport.Height + 1)
	}
}

// commandPaletteView renders the centered searchable command catalog. The
// caller composites this opaque modal over the normal application view so the
// tree, source pane, status strip and footer remain visible behind it.
func (m *Model) commandPaletteView(width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	boxW := width - 8
	if boxW < 44 {
		boxW = width - 2
	}
	if boxW > 78 {
		boxW = 78
	}
	if boxW < 1 {
		boxW = 1
	}
	boxH := height - 6
	if boxH < 10 {
		boxH = height - 2
	}
	if boxH < 6 {
		boxH = 6
	}
	if boxH > 26 {
		boxH = 26
	}

	commands := m.filteredCommands()
	available := 0
	for _, command := range commands {
		if command.Enabled(m) {
			available++
		}
	}
	contentW := boxW - 4 // border (2) + horizontal padding (2)
	if contentW < 1 {
		contentW = 1
	}
	viewportH := boxH - 6 // header, two separators, footer and border
	if viewportH < 1 {
		viewportH = 1
	}
	m.syncCommandViewport(contentW, viewportH, commands)

	query := truncateToWidth(string(m.commandQuery), max(1, contentW-26))
	header := activeTitleStyle.Render("COMMANDS") + " " +
		cursorStyle.Render("> "+query+"▌") + " " +
		dimStyle.Render(fmt.Sprintf("%d available", available))
	header = commandPaletteSurfaceStyle.Width(contentW).Render(header)
	separator := commandPaletteSurfaceStyle.Width(contentW).Render(dimStyle.Render(strings.Repeat("─", contentW)))
	footer := commandPaletteSurfaceStyle.Width(contentW).Render(renderFooterKeys("↑/↓ navigate · Enter run · Esc close"))
	content := strings.Join([]string{header, separator, m.commandViewport.View(), separator, footer}, "\n")
	return commandPaletteStyle.Width(boxW - 2).Height(boxH - 2).Render(content)
}

func (m *Model) syncCommandViewport(width, height int, commands []uiCommand) {
	contentW := width
	if contentW < 1 {
		contentW = 1
	}
	if height < 1 {
		height = 1
	}
	m.commandViewport.Width = contentW
	m.commandViewport.Height = height
	m.commandLineOffsets = make([]int, 0, len(commands))
	var content strings.Builder
	lineNo := 0
	category := ""
	for i, command := range commands {
		if command.Category != category {
			if category != "" {
				content.WriteString("\n")
				lineNo++
			}
			category = command.Category
			group := "  " + commandPaletteGroupStyle.Render(strings.ToUpper(category))
			content.WriteString(commandPaletteSurfaceStyle.Width(contentW).Render(group))
			content.WriteString("\n")
			lineNo++
		}
		m.commandLineOffsets = append(m.commandLineOffsets, lineNo)
		paletteKeys := command.Keys
		if command.ID == commandToggleBranch {
			// Space remains a valid direct hotkey, but Enter and the arrow
			// keys are the clearer compact label in the palette.
			paletteKeys = []string{"Enter", "←→"}
		}
		key := strings.Join(paletteKeys, " · ")
		line := renderCommandPaletteRow(key, command.Label, contentW, i == m.commandCursor, command.Enabled(m))
		content.WriteString(line)
		content.WriteString("\n")
		lineNo++
	}
	if len(commands) == 0 {
		content.WriteString(commandPaletteSurfaceStyle.Width(contentW).Render(dimStyle.Render("no matching commands")))
	}
	m.commandViewport.SetContent(content.String())
	m.revealCommandCursor()
}

func renderCommandPaletteRow(key, label string, width int, selected, enabled bool) string {
	if width < 8 {
		width = 8
	}
	keyW := 12
	labelW := width - 2 - keyW
	if labelW < 1 {
		labelW = 1
	}
	keyText := truncateToWidth(key, keyW-1)
	labelText := truncateToWidth(label, labelW-1)
	keyStyle := keyHintStyle
	labelStyle := lipgloss.NewStyle()
	if !enabled {
		keyStyle = commandPaletteDisabledStyle.Copy().Bold(true)
		labelStyle = commandPaletteDisabledStyle
	}
	keyPart := keyStyle.Width(keyW).Render(keyText)
	labelPart := labelStyle.Width(labelW).Render(labelText)
	content := lipgloss.JoinHorizontal(lipgloss.Top, keyPart, labelPart)
	line := commandPaletteSurfaceStyle.Width(width).Render("  " + content)
	if selected {
		line = commandPaletteSelectedStyle.Width(width).Render(cursorStyle.Render("› ") + content)
	}
	return line
}

// commandPaletteOverlay composites the opaque palette over the existing view.
// cellbuf understands the ANSI styles emitted by Lip Gloss, so replacing the
// modal rectangle does not strip the colors from the underlying TUI.
func (m *Model) commandPaletteOverlay(base string, width, height int) string {
	return m.modalOverlay(base, m.commandPaletteView(width, height), width, height)
}

// searchOverlay composites the centered search catalog over the normal TUI.
func (m *Model) searchOverlay(base string, width, height int) string {
	return m.modalOverlay(base, m.searchView(width, height), width, height)
}

// reloadOverlay composites the reload confirmation over the normal TUI.
func (m *Model) reloadOverlay(base string, width, height int) string {
	return m.modalOverlay(base, m.reloadConfirmView(width, height), width, height)
}

// modalOverlay dims the application behind an opaque centered modal while
// preserving the ANSI styles in both layers.
func (m *Model) modalOverlay(base, modalText string, width, height int) string {
	modalW := lipgloss.Width(modalText)
	modalH := lipgloss.Height(modalText)
	if modalW > width {
		modalW = width
	}
	if modalH > height {
		modalH = height
	}
	x := (width - modalW) / 2
	y := (height - modalH) / 2
	if y < 1 {
		y = 1
	}
	if y+modalH > height {
		y = max(0, height-modalH)
	}

	background := cellbuf.NewBuffer(width, height)
	cellbuf.SetContent(background, base)
	fillBlankCells(background)
	dimCommandPaletteBackground(background, x, y, modalW, modalH)
	modal := cellbuf.NewBuffer(modalW, modalH)
	cellbuf.SetContent(modal, modalText)
	fillPaletteCells(modal)
	for row := 0; row < modalH; row++ {
		for col := 0; col < modalW; col++ {
			cell := modal.Cell(col, row)
			if cell != nil {
				background.SetCell(x+col, y+row, cell)
			}
		}
	}
	return strings.ReplaceAll(cellbuf.Render(background), "\r\n", "\n")
}

// fillBlankCells keeps the cell grid's leading and trailing spaces explicit.
// cellbuf normally trims unstyled blank cells when rendering; that would make
// an overlay jump left on rows where the underlying TUI has no glyph before
// the modal rectangle.
func fillBlankCells(buffer *cellbuf.Buffer) {
	blank := &cellbuf.Cell{Rune: ' ', Width: 1, Style: cellbuf.Style{Attrs: cellbuf.FaintAttr}}
	for _, line := range buffer.Lines {
		for i, cell := range line {
			if cell == nil {
				line[i] = blank.Clone()
			}
		}
	}
}

// fillPaletteCells gives every modal cell the same dark surface. Lip Gloss
// styles do not reliably reach blank cells produced by the viewport, so
// leaving those cells unstyled allows the composited application underneath
// to show through as lighter bands.
func fillPaletteCells(buffer *cellbuf.Buffer) {
	surface := cellbuf.NewBuffer(1, 1)
	cellbuf.SetContent(surface, commandPaletteSurfaceStyle.Render(" "))
	background := surface.Cell(0, 0).Style.Bg
	blank := &cellbuf.Cell{
		Rune:  ' ',
		Width: 1,
		Style: cellbuf.Style{Attrs: cellbuf.FaintAttr, Bg: background},
	}
	for _, line := range buffer.Lines {
		for i, cell := range line {
			if cell == nil {
				line[i] = blank.Clone()
				continue
			}
			if cell.Style.Bg == nil {
				cell.Style.Bg = background
			}
		}
	}
}

// renderLineOnSurface renders one full-width UI band after its nested text
// styles have been applied. Applying the background at cell level prevents
// foreground-only Lip Gloss styles from resetting it in the middle of a line.
func renderLineOnSurface(text string, width int, background lipgloss.AdaptiveColor) string {
	if width < 1 {
		width = 1
	}
	buffer := cellbuf.NewBuffer(width, 1)
	cellbuf.SetContent(buffer, text)

	backgroundBuffer := cellbuf.NewBuffer(1, 1)
	cellbuf.SetContent(backgroundBuffer, lipgloss.NewStyle().Background(background).Render(" "))
	backgroundColor := backgroundBuffer.Cell(0, 0).Style.Bg
	blank := &cellbuf.Cell{
		Rune:  ' ',
		Width: 1,
		Style: cellbuf.Style{Bg: backgroundColor},
	}
	for _, line := range buffer.Lines {
		for i, cell := range line {
			if cell == nil {
				line[i] = blank.Clone()
				continue
			}
			if cell.Style.Bg == nil {
				cell.Style.Bg = backgroundColor
			}
		}
	}
	return strings.ReplaceAll(cellbuf.Render(buffer), "\r\n", "\n")
}

func dimCommandPaletteBackground(buffer *cellbuf.Buffer, x, y, width, height int) {
	for row, line := range buffer.Lines {
		for col, cell := range line {
			if cell == nil || (col >= x && col < x+width && row >= y && row < y+height) {
				continue
			}
			faint := cell.Clone()
			faint.Style.Attrs |= cellbuf.FaintAttr
			line[col] = faint
		}
	}
}
