package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	tea "github.com/charmbracelet/bubbletea"
)

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case formatAndValidateResultMsg:
		return m.handleFormatAndValidateResult(msg)
	case saveResultMsg:
		return m.handleSaveResult(msg)
	case reloadResultMsg:
		return m.handleReloadResult(msg)
	case runtimeProbeResultMsg:
		return m.handleRuntimeProbeResult(msg)
	case logTailMsg:
		return m.handleLogTail(msg)
	case copyResultMsg:
		if msg.err != nil {
			m.statusMessage = "✗ copy failed: " + msg.err.Error()
			m.recordError("copy", msg.err.Error(), "retry the copy or use another clipboard backend")
		} else {
			m.statusMessage = fmt.Sprintf("✓ copied %d bytes", msg.size)
		}
		return m, nil
	case browserResultMsg:
		return m.handleBrowserResult(msg)
	case externalChangeMsg:
		return m.handleExternalChange(msg)
	case editorReadyMsg:
		return m.handleEditorReady(msg)
	case editorExecMsg:
		return m.handleEditorExec(msg)
	case editorDoneMsg:
		return m.handleEditorDone(msg)
	case editorErrorMsg:
		return m.handleEditorError(msg)
	case deleteValidatedMsg:
		return m.handleDeleteValidated(msg)
	case structuredAddValidatedMsg:
		return m.handleStructuredAddValidated(msg)
	case backupListMsg:
		return m.handleBackupList(msg)
	case backupCompareMsg:
		return m.handleBackupCompare(msg)
	case rollbackResultMsg:
		return m.handleRollbackResult(msg)
	case tea.KeyMsg:
		return m.updateKey(msg)
	case tea.MouseMsg:
		return m.updateMouse(msg)
	}
	return m, nil
}

// logPollCmd returns a one-shot tea.Tick that polls the log source and
// delivers logTailMsg. Returning this command again from the message
// handler keeps the poll alive; returning nil stops it.
func (m *Model) logPollCmd() tea.Cmd {
	src := m.logSource
	return tea.Tick(logPollInterval, func(time.Time) tea.Msg {
		entries, err := src.Next(context.Background())
		return logTailMsg{Entries: entries, Err: err}
	})
}

// handleLogTail is invoked on the main goroutine when a poll completes.
// It appends the new entries (bounded at logMaxLines) and reschedules the
// next poll unless the view is closed or paused. A stale "log poll failed"
// status line is cleared by the next successful poll (only that message:
// status lines set by other actions are left untouched).
func (m *Model) handleLogTail(msg logTailMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.logErr = msg.Err
		m.statusMessage = "✗ log poll failed: " + msg.Err.Error()
	} else {
		m.logErr = nil
		// A recovered poll must not leave the error text visible.
		if strings.HasPrefix(m.statusMessage, "✗ log poll failed") {
			m.statusMessage = ""
		}
	}
	if len(msg.Entries) > 0 {
		// New entries shift the scrollback: a selection anchored in the
		// previous log lines would map to different content.
		m.clearTextSelection()
		m.logLines = append(m.logLines, msg.Entries...)
		dropped := 0
		if len(m.logLines) > logMaxLines {
			dropped = len(m.logLines) - logMaxLines
			m.logLines = m.logLines[len(m.logLines)-logMaxLines:]
		}
		if m.logFollow {
			m.logCursor = len(m.logLines) - 1
		} else {
			// Keep the selection stable across the bounded trim: the
			// dropped front entries shift every index down by `dropped`.
			m.logCursor -= dropped
			if m.logCursor < 0 {
				m.logCursor = 0
			}
			if m.logCursor > len(m.logLines)-1 {
				m.logCursor = len(m.logLines) - 1
			}
		}
	}
	if !m.showLogs || m.logPaused {
		return m, nil // view closed or paused: stop rescheduling
	}
	return m, m.logPollCmd()
}

func (m *Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The unsaved-changes confirmation modal takes precedence over every
	// other modal and the main keymap: it is the exit guard and can be
	// opened from any context.
	if m.showUnsavedConfirm {
		return m.updateUnsavedConfirmKey(msg)
	}
	// The external-change conflict modal takes precedence over every
	// other modal: it is the most urgent prompt. While its compare diff
	// is open, the diff keys apply (Esc returns to the conflict
	// options).
	if m.showChangeConflict {
		if m.changeCompare {
			return m.updateDiffKey(msg)
		}
		return m.updateChangeConflictKey(msg)
	}
	// The diff modal takes precedence over the main keymap.
	if m.showDiff {
		return m.updateDiffKey(msg)
	}
	// The save-confirmation modal takes precedence over the
	// diagnostics modal and the main keymap.
	if m.showSaveConfirm {
		return m.updateSaveConfirmKey(msg)
	}
	// The reload-confirmation modal takes precedence over the
	// diagnostics modal and the main keymap.
	if m.showReloadConfirm {
		return m.updateReloadConfirmKey(msg)
	}
	// The rollback-confirmation modal takes precedence over the
	// diagnostics modal and the main keymap.
	if m.showRollbackConfirm {
		return m.updateRollbackConfirmKey(msg)
	}
	// The structured-add modal owns its text input until the candidate has
	// been planned and sent through the validation workflow.
	if m.showStructuredAdd {
		return m.updateStructuredAddKey(msg)
	}
	// The command palette takes over ordinary input while it is open. It is
	// intentionally below safety confirmations, so a pending save, reload or
	// quit decision can never be bypassed by a discoverability overlay.
	if m.showCommandPalette {
		return m.updateCommandPaletteKey(msg)
	}
	// The backup-history modal takes precedence over the diagnostics
	// modal and the main keymap.
	if m.showBackups {
		return m.updateBackupsKey(msg)
	}
	// The diagnostics modal takes precedence over every other key.
	if m.showDiagnostics {
		return m.updateDiagnosticsKey(msg)
	}
	// The log detail modal takes precedence over the log view keys.
	if m.logDetailOpen {
		return m.updateLogDetailKey(msg)
	}
	// The log view is a full screen, not a modal: its keys take precedence
	// over the main keymap once it is open.
	if m.showLogs {
		return m.updateLogKey(msg)
	}
	// The search modal is read-only and only opens from the main view. It
	// takes over every key while it is active, so the editor/diff/save/log
	// bindings are inert and resume on close.
	if m.searchActive {
		return m.updateSearchKey(msg)
	}
	// The error-history view opens from the main view and replaces the
	// tree/source panes while it is open.
	if m.showErrorHistory {
		return m.updateErrorHistoryKey(msg)
	}
	switch msg.String() {
	case "shift+up":
		m.shiftTextCursor(0, -1)
	case "shift+down":
		m.shiftTextCursor(0, 1)
	case "shift+left":
		m.shiftTextCursor(-1, 0)
	case "shift+right":
		m.shiftTextCursor(1, 0)
	case "q", "ctrl+c":
		return m.runCommand(commandQuit)
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter", " ":
		return m.runCommand(commandToggleBranch)
	case "left":
		m.collapseOrExpand(false)
	case "right":
		m.collapseOrExpand(true)
	case "+":
		return m.runCommand(commandExpandAll)
	case "-":
		return m.runCommand(commandCollapseAll)
	case "pgup":
		m.viewport.PageUp()
	case "pgdown":
		m.viewport.PageDown()
	case "v":
		return m.runCommand(commandValidate)
	case "D":
		return m.runCommand(commandDiff)
	case "s":
		return m.runCommand(commandSave)
	case "r":
		return m.runCommand(commandReload)
	case "l":
		return m.runCommand(commandLogs)
	case "e":
		return m.runCommand(commandEdit)
	case "E":
		return m.runCommand(commandFullEdit)
	case "a":
		return m.runCommand(commandAdd)
	case "n":
		return m.runCommand(commandNew)
	case "o":
		return m.runCommand(commandReorder)
	case "m":
		return m.runCommand(commandEditReverse)
	case "d":
		return m.runCommand(commandDelete)
	case "B":
		return m.runCommand(commandBackups)
	case "H":
		return m.runCommand(commandErrors)
	case "y":
		return m.runCommand(commandCopy)
	case "/", "ctrl+f":
		return m.runCommand(commandSearch)
	case "ctrl+h":
		return m.runCommand(commandHelp)
	case "?":
		return m.runCommand(commandPalette)
	}
	return m, nil
}

// toggleLogView opens or closes the log view screen. With no configured
// log source it surfaces a status hint instead of opening. Opening seeds
// the scrollback from the source history and starts the polling tick;
// closing stops the poll (no tick is rescheduled).
func (m *Model) toggleLogView() (tea.Model, tea.Cmd) {
	if m.logSource == nil {
		m.statusMessage = "✗ log view unavailable: no log source configured (use --log-file <path> or --log-journal-unit <unit>)"
		return m, nil
	}
	if m.showLogs {
		m.showLogs = false
		m.statusMessage = ""
		m.clearTextSelection()
		return m, nil // stops the poll: no reschedule
	}
	// Open: seed from the bounded history, reset follow state.
	m.clearTextSelection()
	m.logLines = append([]logs.Entry(nil), m.logSource.History()...)
	m.logFollow = true
	m.logPaused = false
	m.logErr = nil
	m.logCursor = len(m.logLines) - 1 // newest, since follow starts on
	m.logDetailOpen = false
	m.showLogs = true
	return m, m.logPollCmd()
}

// startCopy captures the selected bytes before scheduling the adapter. An
// active text selection (mouse or keyboard) in the source, log or diff
// pane wins: it copies the exact unstyled visible content of that
// selection. Without one, the pre-existing behavior applies: a document
// row copies the complete document; a node row copies only its exact
// source range, including the bytes that belong to that range and
// excluding every tree, pane and footer decoration.
func (m *Model) startCopy() (tea.Model, tea.Cmd) {
	if m.clipboard == nil {
		m.statusMessage = "✗ copy unavailable: no clipboard backend"
		return m, nil
	}
	var content []byte
	if r, ok := m.activeTextSelection(); ok {
		content = m.textSelectionBytes(m.textSel.pane, r)
		if content == nil {
			m.statusMessage = "✗ copy failed: selected range is invalid"
			m.recordError("copy", "selected range is invalid", "reselect the text and retry")
			return m, nil
		}
	} else if selected := m.selectedItem(); selected != nil && selected.doc != nil {
		content = selected.doc.Source
		if selected.hasNode {
			if !selected.node.Range.Valid(len(selected.doc.Source)) {
				m.statusMessage = "✗ copy failed: selected source range is invalid"
				m.recordError("copy", "selected source range is invalid", "reselect the block and retry")
				return m, nil
			}
			content = selected.doc.Source[selected.node.Range.Start:selected.node.Range.End]
		}
	} else {
		m.statusMessage = "✗ copy unavailable: no source selected"
		return m, nil
	}
	content = append([]byte(nil), content...)
	clip := m.clipboard
	return m, func() tea.Msg {
		return copyResultMsg{size: len(content), err: clip.Copy(context.Background(), content)}
	}
}
