package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// logMaxLines bounds the log view's in-memory scrollback.
const logMaxLines = 1000

// logPollInterval is the period between log source polls while the log
// view is open and following.
const logPollInterval = 500 * time.Millisecond

// item is one row of the document tree: a document row (depth 0) or one of
// its site blocks, snippets or named routes (depth 1).
type item struct {
	label     string
	depth     int
	doc       *caddyfile.Document
	node      caddyfile.Node
	hasNode   bool
	collapsed bool
}

// loadedState is the relationship between the running Caddy
// configuration and the saved file on disk.
type loadedState int

const (
	// loadedUnknown is the initial state: lazycaddy never claims the
	// running config matches the file unless it proved it by reloading.
	loadedUnknown loadedState = iota
	// loadedMatches means a reload of the exact saved bytes succeeded,
	// so the running config provably matches the file.
	loadedMatches
	// loadedStale means the file on disk is newer than the running
	// config (saved but not reloaded).
	loadedStale
	// loadedUnreachable means the last reload attempt could not reach
	// the Admin API.
	loadedUnreachable
)

// formatAndValidateResultMsg is delivered to the model after a caddy
// fmt + caddy validate invocation completes. Exactly one of Formatted
// and Err is meaningful on a given message; Diagnostics is populated
// whenever the validator could parse any structured finding from the
// captured stderr.
type formatAndValidateResultMsg struct {
	Formatted   []byte
	Diagnostics []validator.Diagnostic
	Err         error
}

// saveResultMsg is delivered to the model after an asynchronous save
// through the injected app.Saver completes.
type saveResultMsg struct {
	Result app.SaveResult
	Err    error
}

// reloadResultMsg is delivered to the model after an asynchronous
// reload through the injected app.Reloader completes.
type reloadResultMsg struct {
	Result app.ReloadResult
	Err    error
}

// runtimeProbeResultMsg is delivered to the model when the startup
// runtime probe completes.
type runtimeProbeResultMsg struct {
	Report runtime.Report
}

// logTailMsg is delivered by the log polling tick when new entries
// arrive or the poll fails.
type logTailMsg struct {
	Entries []logs.Entry
	Err     error
}

// editorReadyMsg is delivered when the injected app.Editor finishes
// Prepare: the session holds the snapshot, the range bytes temp file and
// the argv to run, and the model hands it to the Bubble Tea exec pipeline.
type editorReadyMsg struct {
	Session *app.EditSession
}

// editorExecMsg is delivered by the Bubble Tea exec pipeline when the
// external editor process exits. Err is what exec.Command.Run returned:
// nil for a clean exit, an *exec.ExitError for a non-zero exit, or any
// other launch failure.
type editorExecMsg struct {
	Err error
}

// editorDoneMsg is delivered when the injected app.Editor finishes
// Complete. ExecErr, when set, means the editor could not even start.
type editorDoneMsg struct {
	Result  app.EditResult
	ExecErr error
}

// editorErrorMsg is delivered when the injected app.Editor returns an
// error from Prepare or Complete (for example ErrConflict or ErrNoEditor).
type editorErrorMsg struct {
	Err error
}

// pendingEdit holds a validated, recomposed document that came out of an
// $EDITOR round-trip and is awaiting the diff review and the save
// confirmation. path may be an imported file; it is the exact document the
// edit targets. nodeName and startLine carry the identity of the edited
// node so the tree can re-anchor the selection after a structural save.
type pendingEdit struct {
	path         string
	original     []byte
	content      []byte
	snapshotPath string
	nodeName     string
	startLine    int
}

// Model is the inspector screen: a document tree on the left, the raw
// source of the selected document on the right (scrollable with
// PgUp/PgDown) and optional diagnostics / diff / save-confirmation
// modals on top. File writes are delegated to the injected
// app.Saver; the model itself never touches the filesystem.
type Model struct {
	loader    app.Loader
	formatter app.Formatter // nil disables the v keybinding
	saver     app.Saver     // nil disables the s keybinding (read-only mode)
	state     *app.State
	err       error // load error (e.g. missing file)

	items     []item
	cursor    int
	collapsed map[string]bool
	width     int
	height    int
	quit      bool

	// viewport shows the source of the selected document, truncated to
	// the pane height instead of overflowing the terminal.
	viewport    viewport.Model
	sourceDoc   *caddyfile.Document
	sourceTitle string
	// lastSel tracks the selection for which revealRange last ran, so a
	// manual scroll (PgUp/PgDown) is not overridden on the next render.
	lastSel selectionKey
	// sourceRefresh forces the source pane to reload its content and
	// re-reveal the selected node on the next render. It is set after a
	// save, which replaces the document bytes in place, so the previous
	// reveal decision (based on an unchanged selection key) must not
	// suppress the reveal.
	sourceRefresh bool

	// detailViewport shows the body of the diagnostic detail view.
	// It is independent of the source viewport: the source is hidden
	// while the diagnostics modal is open, so the two cannot collide.
	detailViewport viewport.Model
	// showDetail is true when the diagnostics modal is showing the
	// detail view for the diagnostic under diagCursor. Esc returns to
	// the list view; another Esc closes the modal.
	showDetail bool

	// loadedBytes is a snapshot of the root document source taken when
	// the configuration is loaded. It is the conflict-detection
	// reference: if the on-disk file has changed since load, the saver
	// reports app.ErrConflict.
	loadedBytes []byte
	// workingBytes holds the last format output. The source pane shows
	// the original source; the working copy is stored for the diff /
	// save workflow. The status line reflects whether a working copy is
	// pending.
	workingBytes []byte
	// workingValidated is true only when the working copy in
	// workingBytes has passed format+validate. A failed validation must
	// never be savable, even though the working copy is retained for
	// inspection.
	workingValidated bool
	// busy is true while a format+validate invocation is in flight; a
	// second v press is ignored until the result is delivered.
	busy bool
	// saving is true while an asynchronous save is in flight; a second
	// s press is ignored until the result is delivered.
	saving bool
	// statusMessage is a single line shown below the header. Cleared on
	// the next v press or on quit.
	statusMessage string

	// Diagnostics modal state. The modal is shown when
	// showDiagnostics is true; the cursor and slice are populated only
	// while the modal is open.
	showDiagnostics bool
	diagnostics     []validator.Diagnostic
	diagCursor      int

	// Diff modal state. The modal is shown when showDiff is true;
	// diffLines and diffTitle are populated while the modal is open.
	showDiff     bool
	diffViewport viewport.Model
	diffLines    []diff.Line
	diffTitle    string

	// Save-confirmation modal state. The modal is shown when
	// showSaveConfirm is true; it names the target path and backup
	// directory before the operator confirms the write.
	showSaveConfirm bool

	// reloader is nil in read-only or binary-less mode and disables the
	// r keybinding, mirroring how a nil saver disables s.
	reloader app.Reloader
	// reloading is true while a reload is in flight; a second r press is
	// ignored until the result is delivered.
	reloading bool
	// showReloadConfirm is true when the reload-confirmation modal is
	// open.
	showReloadConfirm bool
	// loaded tracks the proven relationship between the running Caddy
	// configuration and the file on disk. It is distinct from
	// workingValidated, which is about the in-memory working copy.
	loaded loadedState
	// loadedAt is when the last successful reload was confirmed; zero
	// unless loaded == loadedMatches.
	loadedAt time.Time

	// validatorTimeout is read from settings on Load and is the timeout
	// applied to each format+validate invocation. Zero means "no extra
	// timeout on top of the validator package default (5s)".
	validatorTimeout time.Duration

	// runtime is the startup capability probe; nil disables it (the
	// header then renders no runtime badges).
	runtime app.RuntimeStatus
	// runtimeReport holds the latest probe result. It is rendered only
	// once runtimeProbed is true, so the header never flashes an
	// unproven state.
	runtimeReport runtime.Report
	// runtimeProbed is true once the startup probe has delivered its
	// report.
	runtimeProbed bool

	// logSource supplies structured log entries for the log view; nil
	// disables the l keybinding.
	logSource app.LogSource
	// showLogs is true when the log view screen is open.
	showLogs bool
	// logViewport renders the bounded log scrollback.
	logViewport viewport.Model
	// logLines is the UI-side bounded scrollback (capped at logMaxLines).
	logLines []logs.Entry
	// logFollow is true when new entries auto-scroll to the bottom.
	logFollow bool
	// logPaused is true when polling is suspended (the p keybinding).
	logPaused bool
	// logErr is the last polling error, shown in the log view header.
	logErr error
	// logCursor is the index into logLines of the selected log entry.
	logCursor int
	// logDetailOpen is true when the log detail modal is shown.
	logDetailOpen bool
	// logDetailEntry is a copy of the entry shown in the detail modal.
	logDetailEntry logs.Entry
	// logDetailViewport renders the detail modal body.
	logDetailViewport viewport.Model

	// editor launches $EDITOR on the selected node range; nil disables
	// the e keybinding (no editor command and/or no validation binary).
	editor app.Editor
	// editing is true from the moment the editor session starts until the
	// recomposed result is delivered; a second e press is ignored while
	// it is set, mirroring the saving/busy guards.
	editing bool
	// editorSession is the active app.EditSession between editorReadyMsg
	// and editorDoneMsg. It is cleared once the result is handled.
	editorSession *app.EditSession
	// pendingEdit holds a validated recomposed document awaiting the diff
	// review, which is the single confirmation for saving it. nil means no
	// editor edit is pending.
	pendingEdit *pendingEdit

	// searcher runs read-only substring search across node labels,
	// document paths/content and the loaded log history; nil disables the
	// / keybinding.
	searcher app.Searcher
	// searchActive is true while the search modal is open. While it is
	// active every other keybinding is inert and resumes on close.
	searchActive bool
	// searchQuery accumulates the typed query as a rune buffer (the model
	// does not use bubbles.textinput).
	searchQuery []rune
	// searchResults holds the current hits and searchCursor moves over
	// them.
	searchResults []app.SearchResult
	searchCursor  int
	// searchViewport renders the result list.
	searchViewport viewport.Model
	// sourceRevealLine is a one-shot 1-based line to reveal in the source
	// pane when a search activates a document content hit; 0 means no
	// reveal. It is consumed by syncSource on the next render.
	sourceRevealLine int
}

// New returns a Model that will load its state through loader, run
// format+validate through formatter, write changes through saver and
// reload the running configuration through reloader. formatter may be
// nil; the v keybinding is disabled in that case. saver may be nil; the
// s keybinding is disabled in that case (read-only mode). reloader may
// be nil; the r keybinding is disabled in that case. runtimeStatus may
// be nil; the startup runtime probe is disabled in that case. logSource
// may be nil; the l log-view keybinding is disabled in that case. editor
// may be nil; the e editor keybinding is disabled in that case. searcher
// may be nil; the / search keybinding is disabled in that case. Call
// Load before starting the program.
func New(loader app.Loader, formatter app.Formatter, saver app.Saver, reloader app.Reloader, runtimeStatus app.RuntimeStatus, logSource app.LogSource, editor app.Editor, searcher app.Searcher) *Model {
	return &Model{
		loader:            loader,
		formatter:         formatter,
		saver:             saver,
		reloader:          reloader,
		runtime:           runtimeStatus,
		logSource:         logSource,
		editor:            editor,
		searcher:          searcher,
		collapsed:         map[string]bool{},
		viewport:          viewport.New(1, 1),
		detailViewport:    viewport.New(1, 1),
		diffViewport:      viewport.New(1, 1),
		logViewport:       viewport.New(1, 1),
		logDetailViewport: viewport.New(1, 1),
		searchViewport:    viewport.New(1, 1),
		logFollow:         true,
	}
}

// Load resolves the configuration through the injected loader and
// builds the document tree. Parse errors are kept inside the state so
// the raw source view remains available; only a read failure (missing
// file) is returned.
func (m *Model) Load() error {
	state, err := m.loader.LoadState()
	m.state = state
	m.err = err
	if state != nil {
		m.validatorTimeout = state.Settings.ValidatorTimeout
	}
	if state != nil && state.Graph != nil {
		// Copy the root source so later disk changes can be detected
		// by comparing against this snapshot.
		m.loadedBytes = append([]byte(nil), state.Graph.Root.Source...)
		m.items = buildItems(state.Graph, m.collapsed)
		m.cursor = 0
		// A fresh load never inherits a stale loaded claim: whether the
		// running config matches this file is unknown until a reload
		// proves it.
		m.loaded = loadedUnknown
		m.loadedAt = time.Time{}
	}
	return err
}

// Init implements tea.Model. Loading is done synchronously before the
// program starts, so the only startup command is the async runtime
// probe (when a probe is configured).
func (m *Model) Init() tea.Cmd {
	if m.runtime == nil {
		return nil
	}
	return m.runtimeProbeCmd()
}

// runtimeProbeCmd returns a tea.Cmd that runs the startup probe in a
// goroutine and delivers the report as a runtimeProbeResultMsg. The
// detector applies its own per-step timeouts, so the probe context is
// deliberately unbounded.
func (m *Model) runtimeProbeCmd() tea.Cmd {
	probe := m.runtime
	return func() tea.Msg {
		return runtimeProbeResultMsg{Report: probe.Probe(context.Background())}
	}
}

// handleRuntimeProbeResult stores the probe report on the main goroutine
// and, on the first delivery only, surfaces a concise status line so the
// probe outcome is visible without a dedicated modal.
func (m *Model) handleRuntimeProbeResult(msg runtimeProbeResultMsg) (tea.Model, tea.Cmd) {
	m.runtimeReport = msg.Report
	if !m.runtimeProbed {
		if text := runtimeStatusMessage(msg.Report); text != "" {
			m.statusMessage = text
		}
	}
	m.runtimeProbed = true
	return m, nil
}

// runtimeStatusMessage builds the concise one-line status text for a
// probe report. Outcomes use the explicit ✓/✗ glyphs so the state never
// relies on color alone; an empty string means "keep the status line
// as-is".
func runtimeStatusMessage(rep runtime.Report) string {
	switch rep.Status {
	case runtime.StatusRunning:
		if rep.Capabilities.Version != "" {
			return "✓ caddy " + rep.Capabilities.Version + " · running"
		}
		return "✓ caddy running"
	case runtime.StatusStopped:
		return "✗ caddy binary present but Admin API not reachable (stopped or admin disabled)"
	case runtime.StatusUnreachable:
		return "✗ runtime probe timed out"
	case runtime.StatusUnknown:
		if rep.Capabilities.Binary {
			return "caddy " + rep.Capabilities.Version + " detected (no Admin API)"
		}
	}
	return ""
}

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
	case editorReadyMsg:
		return m.handleEditorReady(msg)
	case editorExecMsg:
		return m.handleEditorExec(msg)
	case editorDoneMsg:
		return m.handleEditorDone(msg)
	case editorErrorMsg:
		return m.handleEditorError(msg)
	case tea.KeyMsg:
		return m.updateKey(msg)
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
	switch msg.String() {
	case "q", "ctrl+c":
		m.quit = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter", " ":
		m.toggleCursor()
	case "pgup":
		m.viewport.PageUp()
	case "pgdown":
		m.viewport.PageDown()
	case "v":
		return m.startFormatAndValidate()
	case "D":
		return m.startDiff()
	case "s":
		return m.startSave()
	case "r":
		return m.startReload()
	case "l":
		return m.toggleLogView()
	case "e":
		return m.startEditor()
	case "/", "ctrl+f":
		return m.startSearch()
	}
	return m, nil
}

// toggleLogView opens or closes the log view screen. With no configured
// log source it surfaces a status hint instead of opening. Opening seeds
// the scrollback from the source history and starts the polling tick;
// closing stops the poll (no tick is rescheduled).
func (m *Model) toggleLogView() (tea.Model, tea.Cmd) {
	if m.logSource == nil {
		m.statusMessage = "✗ log view unavailable: no log source configured (use --log-file)"
		return m, nil
	}
	if m.showLogs {
		m.showLogs = false
		m.statusMessage = ""
		return m, nil // stops the poll: no reschedule
	}
	// Open: seed from the bounded history, reset follow state.
	m.logLines = append([]logs.Entry(nil), m.logSource.History()...)
	m.logFollow = true
	m.logPaused = false
	m.logErr = nil
	m.logCursor = len(m.logLines) - 1 // newest, since follow starts on
	m.logDetailOpen = false
	m.showLogs = true
	return m, m.logPollCmd()
}

// startSearch opens the read-only search modal. It is gated on a
// configured searcher; there is no read-only gate (search never writes).
// Opening seeds the log scope from the configured source when it was never
// loaded, so search covers the bounded history even before the log view
// was opened: every recompute passes m.logLines as scope.Logs, so a
// LogIndex always refers to that exact slice.
func (m *Model) startSearch() (tea.Model, tea.Cmd) {
	if m.searcher == nil {
		m.statusMessage = "✗ search unavailable"
		return m, nil
	}
	if m.logSource != nil && len(m.logLines) == 0 {
		m.logLines = append([]logs.Entry(nil), m.logSource.History()...)
	}
	m.searchQuery = nil
	m.searchResults = nil
	m.searchCursor = 0
	m.searchActive = true
	m.statusMessage = ""
	m.searchViewport.SetContent("")
	return m, nil
}

// updateSearchKey handles keys while the search modal is open. Runes
// always accumulate into the query before any named-key handling, so
// ordinary characters (q, j, k, space, …) are always searchable; only the
// actual navigation keys move the result cursor, PgUp/PgDown page the
// result viewport, Enter activates the result under the cursor, backspace
// trims the query and Esc closes without side effects.
func (m *Model) updateSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// An input buffer must always win over named-key navigation: a typed
	// character is text, never a shortcut.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		m.searchQuery = append(m.searchQuery, msg.Runes...)
		m.recomputeSearch()
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeSearch()
	case "ctrl+c":
		m.quit = true
		return m, tea.Quit
	case "backspace":
		if len(m.searchQuery) > 0 {
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
			m.recomputeSearch()
		}
	case "up":
		if m.searchCursor > 0 {
			m.searchCursor--
		}
		m.revealSearchCursor()
	case "down":
		if m.searchCursor < len(m.searchResults)-1 {
			m.searchCursor++
		}
		m.revealSearchCursor()
	case "pgup":
		m.searchViewport.PageUp()
	case "pgdown":
		m.searchViewport.PageDown()
	case "enter":
		if m.searchCursor >= 0 && m.searchCursor < len(m.searchResults) {
			m.activateSearchResult(m.searchResults[m.searchCursor])
			m.closeSearch()
		}
	}
	return m, nil
}

// recomputeSearch rebuilds the results for the current query against the
// whole resolved graph (every document, node and content line, imported
// files included, independent of the collapsed UI state) plus the loaded
// log lines, resets the cursor to the top and re-anchors the viewport
// scroll (the content itself is rebuilt on the next render).
func (m *Model) recomputeSearch() {
	if m.searcher == nil {
		m.searchResults = nil
		m.searchCursor = 0
		return
	}
	scope := app.SearchScope{Logs: m.logLines}
	if m.state != nil && m.state.Graph != nil {
		for _, doc := range m.state.Graph.Documents {
			if doc == nil {
				continue
			}
			// Document row: path and content matches.
			scope.Items = append(scope.Items, app.SearchItem{Label: doc.Path, Doc: doc})
			// Every node, independent of the collapsed UI state: a global
			// search must cover collapsed documents too.
			for _, n := range doc.Nodes {
				scope.Items = append(scope.Items, app.SearchItem{Label: nodeLabel(n), Doc: doc, Node: n, HasNode: true})
			}
		}
	}
	m.searchResults = m.searcher.Search(string(m.searchQuery), scope)
	m.searchCursor = 0
	m.searchViewport.GotoTop()
}

// closeSearch dismisses the search modal and clears its state. It never
// touches the selection, the log view, the diff, the editor or the save
// workflow; sourceRevealLine survives so a just-activated result can still
// be revealed by the next render.
func (m *Model) closeSearch() {
	m.searchActive = false
	m.searchQuery = nil
	m.searchResults = nil
	m.searchCursor = 0
	m.searchViewport.SetContent("")
}

// activateSearchResult jumps to the target of a search hit: a node hit
// re-anchors the tree cursor on the node row, a document hit selects the
// document row (optionally revealing a content line), and a log hit opens
// the log view with the entry's detail.
func (m *Model) activateSearchResult(r app.SearchResult) {
	m.sourceRevealLine = 0
	switch r.Kind {
	case app.SearchNode:
		// A search covers collapsed documents too; expand the containing
		// document first so its node row exists in the tree.
		if r.Doc != nil && m.collapsed[r.Doc.Path] {
			delete(m.collapsed, r.Doc.Path)
			if m.state != nil && m.state.Graph != nil {
				m.items = buildItems(m.state.Graph, m.collapsed)
			}
		}
		for i := range m.items {
			it := &m.items[i]
			if it.doc == r.Doc && it.hasNode && it.node.Range == r.Node.Range {
				m.cursor = i
				return
			}
		}
	case app.SearchDocument:
		for i := range m.items {
			it := &m.items[i]
			if it.doc != nil && !it.hasNode && it.doc.Path == r.Doc.Path {
				m.cursor = i
				break
			}
		}
		if r.Line > 0 {
			m.sourceRevealLine = r.Line
		}
	case app.SearchLog:
		if r.LogIndex < 0 || r.LogIndex >= len(m.logLines) {
			return
		}
		m.logCursor = r.LogIndex
		m.logDetailEntry = m.logLines[r.LogIndex]
		m.logDetailOpen = true
		m.logFollow = false
		m.showLogs = true
	}
}

// revealSearchCursor scrolls the search viewport just enough so that the
// row under searchCursor is visible, mirroring revealLogCursor.
func (m *Model) revealSearchCursor() {
	if m.searchCursor < m.searchViewport.YOffset {
		m.searchViewport.SetYOffset(m.searchCursor)
	} else if m.searchCursor >= m.searchViewport.YOffset+m.searchViewport.Height {
		m.searchViewport.SetYOffset(m.searchCursor - m.searchViewport.Height + 1)
	}
}

// startEditor begins the $EDITOR round-trip for the selected node. It is
// gated on a configured editor, writable mode, a free busy state and a
// selected node. A document row (depth 0, no node) has no range to edit,
// so the command is disabled there by design: there is no fallback to
// opening the whole file.
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
	if m.saving || m.editing || m.busy || m.reloading {
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
		return m, nil
	case !result.Changed:
		m.statusMessage = "no changes"
		return m, nil
	}
	if session == nil || len(result.Content) == 0 {
		m.statusMessage = "✗ editor session missing a result"
		return m, nil
	}
	// During the editor flow the tree selection is still the edited node;
	// capture its identity so the post-save tree refresh can re-anchor the
	// selection even when the edit added or removed sections above it.
	nodeName := ""
	startLine := 0
	if sel := m.selectedItem(); sel != nil && sel.hasNode {
		nodeName = sel.node.Name
		startLine = sel.node.Range.StartLine
	}
	m.pendingEdit = &pendingEdit{
		path:         session.DocPath,
		original:     result.Original,
		content:      result.Content,
		snapshotPath: result.SnapshotPath,
		nodeName:     nodeName,
		startLine:    startLine,
	}
	lines, err := diff.Unified(result.Original, result.Content, session.DocPath, session.DocPath+" (edited)")
	if err != nil {
		m.statusMessage = "✗ diff failed: " + err.Error()
		return m, nil
	}
	m.diffLines = lines
	m.diffTitle = "Diff · " + session.DocPath
	m.showDiff = true
	m.syncDiffContent()
	m.diffViewport.GotoTop()
	return m, nil
}

// handleEditorError surfaces a Prepare or Complete failure (an
// external-change conflict, a missing editor command or a patch error) in
// the status line.
func (m *Model) handleEditorError(msg editorErrorMsg) (tea.Model, tea.Cmd) {
	m.editing = false
	m.editorSession = nil
	m.statusMessage = "✗ editor: " + msg.Err.Error()
	return m, nil
}

// updateLogKey handles keys while the log view is open. The arrow keys
// move the row cursor (up/pgup also turn follow off — the operator takes
// control); Enter opens the detail modal for the selected entry; f toggles
// follow, p pauses/resumes polling, Esc closes the view and q/ctrl+c quits
// the program.
func (m *Model) updateLogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.showLogs = false
		m.statusMessage = ""
		return m, nil // stops the poll: no reschedule
	case "q", "ctrl+c":
		m.quit = true
		return m, tea.Quit
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
	case "enter":
		if m.logCursor >= 0 && m.logCursor < len(m.logLines) {
			m.logDetailEntry = m.logLines[m.logCursor] // copy
			m.logDetailOpen = true
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
	case "esc":
		m.logDetailOpen = false
	case "q", "ctrl+c":
		m.quit = true
		return m, tea.Quit
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

func (m *Model) updateDiagnosticsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The detail view takes precedence over the list view.
	if m.showDetail {
		return m.updateDetailKey(msg)
	}
	switch msg.String() {
	case "esc", "q":
		m.closeDiagnostics()
	case "up", "k":
		if m.diagCursor > 0 {
			m.diagCursor--
		}
	case "down", "j":
		if m.diagCursor < len(m.diagnostics)-1 {
			m.diagCursor++
		}
	// Enter and '+' open the detail view for the diagnostic under the
	// cursor. '+' is a Vim-style alias and is intentionally a no-op
	// outside the diagnostics modal.
	case "enter", "+":
		m.openDetail()
	}
	return m, nil
}

// updateDetailKey handles keys when the diagnostics detail view is
// open. Esc returns to the list (the modal stays open). PgUp /
// PgDown and the arrow keys scroll the wrapped message.
func (m *Model) updateDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.closeDetail()
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

// paneHeight returns the height of the main pane area (the modal or
// the tree+source panes), matching the computation in View. It is
// extracted so the detail view can size its viewport to match the
// pane that will contain it, without re-deriving the layout.
func (m *Model) paneHeight() int {
	paneH := m.height - 3
	if m.err != nil {
		paneH--
	}
	if m.statusMessage != "" {
		paneH--
	}
	if paneH < 1 {
		paneH = 1
	}
	return paneH
}

// closeDetail returns from the detail view to the diagnostics list.
// The list keeps the cursor at the same position; the modal stays
// open until the user presses Esc again or another keybinding.
func (m *Model) closeDetail() {
	m.showDetail = false
}

// toggleCursor expands or collapses the document row under the cursor,
// then re-anchors the cursor on that row.
func (m *Model) toggleCursor() {
	if m.cursor >= len(m.items) || m.state == nil || m.state.Graph == nil {
		return
	}
	cur := m.items[m.cursor]
	if cur.depth != 0 || cur.doc == nil {
		return
	}
	path := cur.doc.Path
	m.collapsed[path] = !m.collapsed[path]
	m.items = buildItems(m.state.Graph, m.collapsed)
	for i, it := range m.items {
		if it.doc != nil && it.doc.Path == path && it.depth == 0 {
			m.cursor = i
			break
		}
	}
}

// startFormatAndValidate triggers a caddy fmt + caddy validate
// invocation against the root document. It is a no-op (with a status
// hint) when the formatter is not configured, another validation is
// already in flight, or no configuration has been loaded.
func (m *Model) startFormatAndValidate() (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Graph == nil {
		return m, nil
	}
	if m.formatter == nil {
		m.statusMessage = "✗ caddy binary not configured (use --caddy-path)"
		return m, nil
	}
	if m.busy {
		return m, nil
	}
	src := m.state.Graph.Root.Source
	m.busy = true
	m.statusMessage = "validating…"
	return m, m.formatAndValidateCmd(src)
}

// formatAndValidateCmd returns a tea.Cmd that runs the formatter in a
// goroutine and reports the result as a formatAndValidateResultMsg.
// The command applies a context timeout layered on top of the
// validator's own internal timeout, so a slow host cannot pin the
// goroutine forever.
func (m *Model) formatAndValidateCmd(src []byte) tea.Cmd {
	timeout := m.validatorTimeout
	formatter := m.formatter
	// The diagnostics must surface the real Caddyfile path, not the
	// temporary working file the validator runs against. m.state is
	// guaranteed non-nil by the caller (startFormatAndValidate checks
	// it), so ConfigPath is safe to capture here.
	displayPath := m.state.Settings.ConfigPath
	return func() tea.Msg {
		// Only apply a context timeout when the operator asked for one.
		// context.WithTimeout(parent, 0) returns a context that is
		// already past its deadline, which would cancel the validator
		// immediately and let its own 5s default never fire.
		ctx := context.Background()
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		formatted, diags, err := formatter.FormatAndValidate(ctx, displayPath, src)
		return formatAndValidateResultMsg{
			Formatted:   formatted,
			Diagnostics: diags,
			Err:         err,
		}
	}
}

// handleFormatAndValidateResult is invoked on the main goroutine when
// a format+validate invocation completes. It clears the busy flag,
// stores the working copy on success and either opens the diagnostics
// modal or surfaces a status line on failure.
func (m *Model) handleFormatAndValidateResult(msg formatAndValidateResultMsg) (tea.Model, tea.Cmd) {
	m.busy = false
	// FormatAndValidate returns the formatted working copy even when
	// validation fails. Retain it so the next diff/edit workflow can show
	// the candidate that produced the diagnostic without touching disk.
	if msg.Formatted != nil {
		m.workingBytes = msg.Formatted
	}
	if msg.Err != nil {
		// A failed validation must never be savable.
		m.workingValidated = false
		// Caddy emits info-level log lines alongside parse errors
		// (e.g. "INFO  using config from file"). The modal is for
		// actionable findings, so filter to error-level diagnostics
		// before opening it. If no errors remain after the filter,
		// fall back to the status line so the underlying error is
		// still visible.
		var errors []validator.Diagnostic
		for _, d := range msg.Diagnostics {
			if d.Severity == validator.SeverityError {
				errors = append(errors, d)
			}
		}
		if len(errors) > 0 {
			m.diagnostics = errors
			m.diagCursor = 0
			m.showDiagnostics = true
			m.statusMessage = "✗ validation failed (working copy not saved)"
			if msg.Formatted != nil {
				m.statusMessage = "✗ validation failed (working copy retained, not saved)"
			}
			return m, nil
		}
		m.statusMessage = "✗ validation failed (working copy not saved): " + msg.Err.Error()
		return m, nil
	}
	m.diagnostics = nil
	m.showDiagnostics = false
	m.workingValidated = true
	m.statusMessage = "✓ validated (working copy updated, not saved)"
	return m, nil
}

// closeDiagnostics dismisses the diagnostics modal and clears its
// state. Called by Esc and q from inside the modal.
func (m *Model) closeDiagnostics() {
	m.showDiagnostics = false
	m.diagnostics = nil
	m.diagCursor = 0
}

// startDiff opens the unified diff modal comparing the original root
// source with the formatted working copy. It is a no-op when no
// configuration is loaded or no working copy exists yet; on error the
// failure is surfaced in the status line. The modal is allowed even
// when validation previously failed or a validation is still in
// flight, because the working copy is retained in both cases.
func (m *Model) startDiff() (tea.Model, tea.Cmd) {
	if m.state == nil || m.state.Graph == nil {
		return m, nil
	}
	if m.workingBytes == nil {
		m.statusMessage = "no working copy — press v to format & validate first"
		return m, nil
	}
	lines, err := diff.Unified(
		m.state.Graph.Root.Source,
		m.workingBytes,
		m.state.Settings.ConfigPath,
		m.state.Settings.ConfigPath+" (formatted)",
	)
	if err != nil {
		m.statusMessage = "✗ diff failed: " + err.Error()
		return m, nil
	}
	m.diffLines = lines
	m.diffTitle = "Diff · " + m.state.Settings.ConfigPath
	m.showDiff = true
	m.syncDiffContent()
	m.diffViewport.GotoTop()
	return m, nil
}

// updateDiffKey handles keys when the diff modal is open. Esc and q
// close the modal; the arrow keys and PgUp/PgDown scroll the viewport.
// When the diff shows a pending editor edit, the diff is the single
// confirmation: Enter saves directly and Esc additionally discards the
// pending edit (nothing is saved). The read-only D flow keeps its
// current behavior.
func (m *Model) updateDiffKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.closeDiff()
		if m.pendingEdit != nil {
			m.pendingEdit = nil
			m.statusMessage = "edit discarded"
		}
	case "enter":
		if m.pendingEdit != nil {
			// For an editor edit the diff is the single confirmation:
			// Enter saves directly (the edit was already validated),
			// mirroring the save-confirmation Enter branch.
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
	}
	return m, nil
}

// closeDiff dismisses the diff modal and clears its state. Called by
// Esc and q from inside the modal.
func (m *Model) closeDiff() {
	m.showDiff = false
	m.diffLines = nil
	m.diffTitle = ""
	m.diffViewport.SetContent("")
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
	if m.saving {
		return m, nil
	}
	if m.reloading {
		return m, nil
	}
	if m.pendingEdit != nil {
		m.showSaveConfirm = true
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
// edit is saved to its own document path (which may be an imported
// file); otherwise the root working copy is saved to the config path.
func (m *Model) saveCmd() tea.Cmd {
	saver := m.saver
	path := m.state.Settings.ConfigPath
	original := m.loadedBytes
	working := m.workingBytes
	if m.pendingEdit != nil {
		path = m.pendingEdit.path
		original = m.pendingEdit.original
		working = m.pendingEdit.content
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
		if m.pendingEdit != nil {
			// An editor edit can add or remove sections: rebuild the tree
			// from the freshly written file so the new structure, the
			// selection range and the source pane stay in sync.
			if !m.refreshAfterStructuralSave(path) {
				status += " · tree refresh failed"
			}
		}
		m.pendingEdit = nil
		m.statusMessage = status
		return m, nil
	}
	if errors.Is(msg.Err, app.ErrConflict) {
		m.statusMessage = "✗ file changed on disk — reload before saving"
		m.reopenEditDiff()
		return m, nil
	}
	var saveErr *app.SaveError
	if errors.As(msg.Err, &saveErr) {
		m.statusMessage = "✗ save failed (backup: " + saveErr.BackupPath + "): " + saveErr.Err.Error()
		m.reopenEditDiff()
		return m, nil
	}
	m.statusMessage = "✗ save failed: " + msg.Err.Error()
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
	if pe != nil && pe.nodeName != "" {
		// Prefer the edited node: same name in the saved document.
		for i := range m.items {
			it := &m.items[i]
			if it.doc != nil && filepath.Clean(it.doc.Path) == cleanPath && it.hasNode && it.node.Name == pe.nodeName {
				idx = i
				break
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

// reopenEditDiff reopens the diff modal for the pending editor edit after
// a failed save, so the operator can retry with Enter or discard with Esc.
// It is a no-op when no edit is pending. The status message set by the
// caller stays visible above the reopened modal.
func (m *Model) reopenEditDiff() {
	pe := m.pendingEdit
	if pe == nil {
		return
	}
	lines, err := diff.Unified(pe.original, pe.content, pe.path, pe.path+" (edited)")
	if err != nil {
		return
	}
	m.diffLines = lines
	m.diffTitle = "Diff · " + pe.path
	m.showDiff = true
	m.syncDiffContent()
	m.diffViewport.GotoTop()
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
	if m.reloading || m.saving {
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
		return m, nil
	}
	switch {
	case errors.Is(msg.Err, app.ErrConflict):
		m.loaded = loadedUnknown
		m.statusMessage = "✗ file changed on disk since save — reload aborted"
	case errors.Is(msg.Err, app.ErrAdminUnreachable), errors.Is(msg.Err, app.ErrAdminTimeout):
		m.loaded = loadedUnreachable
		m.statusMessage = "✗ reload failed (file saved, backup intact): " + msg.Err.Error()
	default:
		m.loaded = loadedStale
		m.statusMessage = "✗ reload failed (file saved, backup intact): " + msg.Err.Error()
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
		title = "Save edit · Enter save · Esc cancel"
		path = m.pendingEdit.path
		hint = dimStyle.Render("the edit applies only to the selected node range")
	}
	bodyH := height - 3 // border (2) + title (1)
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
	return paneStyle.Width(paneContentW).Height(height).Render(title + "\n" + body.String())
}

// reloadConfirmView renders the reload-confirmation modal. It names the
// target path and the Admin API endpoint (the action is network-visible
// and irreversible once accepted) and offers Enter to confirm or Esc to
// cancel.
func (m *Model) reloadConfirmView(width, height int) string {
	title := "Reload config · Enter reload · Esc cancel"
	bodyH := height - 3 // border (2) + title (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	var body strings.Builder
	body.WriteString(dimStyle.Render("Path      ") + m.state.Settings.ConfigPath + "\n")
	body.WriteString(dimStyle.Render("Admin API ") + m.state.Settings.AdminEndpoint + "\n")
	body.WriteString("\n")
	body.WriteString(dimStyle.Render("the saved file and its backup stay intact if the reload fails") + "\n")
	body.WriteString(dimStyle.Render("reloads through the local Admin API after a confirmed save"))
	return paneStyle.Width(paneContentW).Height(height).Render(title + "\n" + body.String())
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
	var b strings.Builder
	for _, line := range m.diffLines {
		text := truncateToWidth(line.Text, bodyW)
		switch line.Kind {
		case diff.KindAdd:
			b.WriteString(diffAddStyle.Render(text))
		case diff.KindRemove:
			b.WriteString(diffRemoveStyle.Render(text))
		case diff.KindHunkHeader:
			b.WriteString(diffHunkStyle.Render(text))
		case diff.KindFileHeader:
			b.WriteString(diffFileStyle.Render(text))
		default:
			b.WriteString(text)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.state == nil && m.err == nil {
		return "loading…"
	}
	width, height := m.width, m.height
	if width == 0 {
		width = 80
	}
	if height == 0 {
		height = 24
	}

	paneH := height - 3
	if m.err != nil {
		paneH--
	}
	if m.statusMessage != "" {
		paneH--
	}
	if paneH < 1 {
		paneH = 1
	}

	var b strings.Builder
	b.WriteString(m.header(width))
	if m.err != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("✗ %v", m.err)))
		b.WriteString("\n")
	}
	if m.statusMessage != "" {
		b.WriteString(m.statusLine(width))
		b.WriteString("\n")
	}
	if m.showDiff {
		b.WriteString(m.diffView(width, paneH))
		b.WriteString("\n")
	} else if m.showSaveConfirm {
		b.WriteString(m.saveConfirmView(width, paneH))
		b.WriteString("\n")
	} else if m.showReloadConfirm {
		b.WriteString(m.reloadConfirmView(width, paneH))
		b.WriteString("\n")
	} else if m.showDiagnostics {
		if m.showDetail {
			b.WriteString(m.diagnosticDetailView(width, paneH))
		} else {
			b.WriteString(m.diagnosticsView(width, paneH))
		}
		b.WriteString("\n")
	} else if m.showLogs {
		// The log view is a full screen, not a modal: it replaces the
		// tree/source panes but stays below the modal layering above.
		b.WriteString(m.logView(width, paneH))
		b.WriteString("\n")
		if m.logDetailOpen {
			// The detail modal layers over the log view.
			b.WriteString(m.logDetailView(width, paneH))
			b.WriteString("\n")
		}
	} else if m.searchActive {
		// The search modal is read-only and only opens from the main view.
		b.WriteString(m.searchView(width, paneH))
		b.WriteString("\n")
	} else {
		treeW := width * 2 / 5
		// Both panes carry a left and right border; subtract the full
		// horizontal border width of both so the source pane's right
		// border stays on screen.
		srcW := width - treeW - 2*paneStyle.GetHorizontalBorderSize()
		if srcW < 1 {
			srcW = 1
		}
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
			m.treePane(treeW, paneH),
			m.sourcePane(srcW, paneH)))
		b.WriteString("\n")
	}
	b.WriteString(m.footer(width))
	return b.String()
}

func (m *Model) header(width int) string {
	path := ""
	if m.state != nil {
		path = m.state.Settings.ConfigPath
	}
	if path == "" {
		path = "unknown"
	}
	left := headerStyle.Render(" lazycaddy ")
	// A compact version indicator joins the left cluster once the startup
	// probe has proven the binary exists. The version is shown exactly as
	// reported (leading "v" included).
	if m.runtimeProbed && m.runtimeReport.Capabilities.Binary {
		left += dimStyle.Render(" caddy " + m.runtimeReport.Capabilities.Version)
	}
	// Important state is conveyed by an explicit text label, not
	// color alone, matching the existing READ-ONLY pattern.
	right := readOnlyBadge.Render(" READ-ONLY ")
	if m.state != nil && !m.state.Settings.ReadOnly {
		right = writableBadge.Render(" WRITE ")
	}
	if m.state != nil && m.state.Graph != nil && m.state.Graph.Err != nil {
		right = errorStyle.Render(" PARSE ERROR ") + right
	}
	// The runtime status badge sits at the front of the right stack
	// (before the PARSE ERROR marker) so the most immediate operational
	// state reads first. Nothing is rendered before the probe returns,
	// and an unknown probe result stays quiet.
	if m.runtimeProbed && m.runtimeReport.Status != runtime.StatusUnknown {
		switch m.runtimeReport.Status {
		case runtime.StatusRunning:
			right = runtimeRunningBadge.Render(" RUNNING ") + right
		case runtime.StatusStopped:
			right = runtimeStoppedBadge.Render(" STOPPED ") + right
		case runtime.StatusUnreachable:
			right = runtimeUnreachableBadge.Render(" UNREACHABLE ") + right
		}
	}
	// The loaded-state badge sits between the PARSE ERROR marker and the
	// read/write badge. Explicit text labels carry the state, never color
	// alone, matching the READ-ONLY convention. The initial state is shown
	// as UNKNOWN (nothing proven yet) only when reloading is possible, so
	// a read-only session without a caddy binary stays quiet.
	if m.reloading {
		right = reloadingBadge.Render(" RELOADING ") + right
	} else if m.loaded == loadedMatches {
		right = loadedBadge.Render(" LOADED ") + right
	} else if m.loaded == loadedStale {
		right = staleBadge.Render(" STALE ") + right
	} else if m.loaded == loadedUnreachable {
		right = unreachableBadge.Render(" UNREACHABLE ") + right
	} else if m.reloader != nil {
		right = unknownBadge.Render(" UNKNOWN ") + right
	}
	pad := width - lipgloss.Width(left) - lipgloss.Width(right) - len(path) - 3
	if pad < 1 {
		pad = 1
	}
	return left + dimStyle.Render(path) + strings.Repeat(" ", pad) + right + "\n"
}

func (m *Model) treePane(width, height int) string {
	title := "Documents"
	if m.err != nil {
		title = "Documents (unavailable)"
	}
	var body strings.Builder
	if len(m.items) == 0 {
		body.WriteString(dimStyle.Render("no documents loaded — raw source view is on the right"))
	} else {
		start := m.cursor - height/2
		if start < 0 {
			start = 0
		}
		end := start + height
		if end > len(m.items) {
			end = len(m.items)
		}
		for i := start; i < end; i++ {
			it := m.items[i]
			line := renderItem(it)
			if i == m.cursor {
				line = cursorStyle.Render("▸ " + line)
			} else {
				line = "  " + line
			}
			body.WriteString(line + "\n")
		}
	}
	return paneStyle.Width(width).Height(height).Render(title + "\n" + body.String())
}

func renderItem(it item) string {
	indent := strings.Repeat("  ", it.depth)
	if it.depth == 0 {
		marker := "−" // expanded
		if it.collapsed {
			marker = "+" // collapsed
		}
		return fmt.Sprintf("%s%s %s", indent, marker, filepath.Base(it.doc.Path))
	}
	if it.hasNode {
		return fmt.Sprintf("%s%s", indent, it.label)
	}
	return indent + it.label
}

// sourcePane renders the raw, unmodified source of the selected
// item's document inside a scrollable viewport. Unknown directives,
// comments and malformed regions are all shown exactly as stored; the
// viewport truncates the output to the pane height instead of
// overflowing the terminal.
func (m *Model) sourcePane(srcW, paneH int) string {
	m.syncSource(srcW, paneH)
	return paneStyle.Width(srcW).Height(paneH).Render(m.sourceTitle + "\n" + m.viewport.View())
}

// syncSource keeps the source viewport sized to the pane and refreshes
// its content whenever the selection or the pane dimensions change.
func (m *Model) syncSource(srcW, paneH int) {
	contentW := srcW - 4 // border (2) + horizontal padding (2)
	if contentW < 1 {
		contentW = 1
	}
	contentH := paneH - 4 // border (2) + title and blank line (2)
	if contentH < 1 {
		contentH = 1
	}
	m.viewport.Width = contentW
	m.viewport.Height = contentH

	selected := m.selectedItem()
	title := "Source"
	var doc *caddyfile.Document
	if selected != nil && selected.doc != nil {
		doc = selected.doc
		title = "Source · " + selected.doc.Path
		if selected.hasNode {
			title += fmt.Sprintf(" · %s (lines %d-%d)", selected.node.Name, selected.node.Range.StartLine, selected.node.Range.EndLine)
		}
	}

	// Build the selection key first: it carries the 1-based range used
	// both for highlighting the source gutter and for revealing the node.
	key := selectionKey{doc: doc}
	if selected != nil && selected.hasNode {
		key.hasNode = true
		key.node = selected.node.Name
		key.start = selected.node.Range.StartLine
		key.end = selected.node.Range.EndLine
	}

	// A refresh (set by a save) forces the content reload and the reveal
	// even when the selection key is unchanged; it is consumed here so it
	// applies to exactly one render.
	refresh := m.sourceRefresh
	m.sourceRefresh = false
	prevDoc := m.sourceDoc
	prevSel := m.lastSel
	needsContent := refresh || doc != m.sourceDoc || key != m.lastSel
	if needsContent {
		m.sourceDoc = doc
		m.lastSel = key
		m.sourceTitle = title
		var src []byte
		if doc != nil {
			src = doc.Source
		}
		m.viewport.SetContent(numberedSource(src, key.start, key.end))
		if doc != prevDoc && !refresh {
			// New document: start at the top; revealRange then scrolls
			// just enough for the selected node. A save refresh stays put
			// and re-reveals the selection below instead.
			m.viewport.GotoTop()
		}
	} else if title != m.sourceTitle {
		m.sourceTitle = title
	}

	// Reveal-if-needed, but only when the selection changed, the source
	// was refreshed, or a search activated a document line: after a manual
	// scroll the viewport must stay where the user left it, while a save
	// or a search activation must re-position the viewport on the
	// selected node / line.
	if key != prevSel || refresh || m.sourceRevealLine > 0 {
		if key.hasNode {
			m.sourceRevealLine = 0
			m.revealRange(key.start, key.end)
		} else if m.sourceRevealLine > 0 {
			// A search result activated a document content line: reveal
			// it (one-shot) instead of resetting to the top.
			offset := m.sourceRevealLine - 1 // 1-based line → 0-based offset
			if offset < 0 {
				offset = 0
			}
			m.viewport.SetYOffset(offset)
			m.sourceRevealLine = 0
		} else {
			// Returning to a document row: reset the source view to the top
			// (the "home" position) instead of keeping a stale node reveal.
			m.viewport.GotoTop()
		}
	}
}

// selectionKey identifies the tree item the source pane is bound to.
// It is deliberately comparable so revealRange only runs on actual
// selection changes.
type selectionKey struct {
	doc     *caddyfile.Document
	hasNode bool
	node    string
	start   int
	end     int
}

// revealRange scrolls the viewport just enough so that the 1-based
// source lines [startLine, endLine] are visible: when the range starts
// above the viewport it is brought to the top, when it ends below the
// viewport it is brought to the bottom, and otherwise the position is
// left unchanged.
func (m *Model) revealRange(startLine, endLine int) {
	firstVisible := m.viewport.YOffset + 1
	lastVisible := m.viewport.YOffset + m.viewport.Height
	switch {
	case startLine < firstVisible:
		m.viewport.SetYOffset(startLine - 1)
	case endLine > lastVisible:
		m.viewport.SetYOffset(endLine - m.viewport.Height)
	}
}

// selectedItem returns the item under the cursor, or nil.
func (m *Model) selectedItem() *item {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	return &m.items[m.cursor]
}

// statusLine renders the current statusMessage in a style chosen by
// the leading glyph: ✓ for success, ✗ for error, anything else is
// shown in the dim info style.
func (m *Model) statusLine(width int) string {
	msg := m.statusMessage
	switch {
	case strings.HasPrefix(msg, "✓"):
		return statusSuccessStyle.Width(width).Render(msg)
	case strings.HasPrefix(msg, "✗"):
		return errorStyle.Width(width).Render(msg)
	default:
		return statusInfoStyle.Width(width).Render(msg)
	}
}

// logView renders the full-screen log scrollback inside a bordered pane.
// The title names the followed log file and the current follow/pause
// state, so those modes never rely on color alone.
func (m *Model) logView(width, height int) string {
	title := "Logs"
	if m.state != nil && m.state.Settings.LogPath != "" {
		title += " · " + m.state.Settings.LogPath
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
	bodyH := height - 3 // border (2) + title (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	m.syncLogViewport(paneContentW, bodyH)
	return paneStyle.Width(paneContentW).Height(height).Render(title + "\n" + m.logViewport.View())
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
	contentH := height - 3 // border (2) + title (1)
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
				gutter = cursorStyle.Render("▸ ")
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
	if summary := logDetailSummary(m.logDetailEntry); summary != "" {
		title += " · " + summary
	}
	title += " · Esc back"
	bodyH := height - 3 // border (2) + title (1)
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
	return paneStyle.Width(paneContentW).Height(height).Render(title + "\n" + m.logDetailViewport.View())
}

// syncLogDetailContent sizes the log detail viewport and rebuilds its
// content from the current entry. width/height are the window pane
// dimensions (the same values logDetailView receives).
func (m *Model) syncLogDetailContent(width, height int) {
	contentW := width - 4 // border (2) + horizontal padding (2)
	if contentW < 1 {
		contentW = 1
	}
	contentH := height - 3 // border (2) + title (1)
	if contentH < 1 {
		contentH = 1
	}
	m.logDetailViewport.Width = contentW
	m.logDetailViewport.Height = contentH
	m.logDetailViewport.SetContent(strings.Join(renderLogDetail(m.logDetailEntry, contentW), "\n"))
}

// logDetailSummary builds a short descriptor for the detail modal title:
// timestamp + logger + message truncated to 30 cells, or "raw line" for
// non-JSON entries.
func logDetailSummary(entry logs.Entry) string {
	if !entry.Parsed {
		return "raw line"
	}
	ts := "--:--:--.---"
	if !entry.Timestamp.IsZero() {
		ts = entry.Timestamp.Local().Format("15:04:05.000")
	}
	parts := []string{ts}
	if entry.Logger != "" {
		parts = append(parts, entry.Logger)
	}
	if entry.Msg != "" {
		parts = append(parts, entry.Msg)
	}
	return truncateToWidth(strings.Join(parts, " "), 30)
}

func (m *Model) footer(width int) string {
	// Modals replace the global keymap with context-aware keys, so the
	// bottom footer never shows keys that are not active in the current
	// context.
	// The r key is only shown when a reloader is configured, mirroring
	// how the s key presence depends on the saver. The l key is only
	// shown when a log source is configured. The e key is only shown when
	// an editor is configured, writable mode is active and a node (not a
	// document row) is selected.
	reloadSuffix := ""
	if m.reloader != nil {
		reloadSuffix = " · r reload"
	}
	logSuffix := ""
	if m.logSource != nil {
		logSuffix = " · l logs"
	}
	editSuffix := ""
	if m.editor != nil && m.state != nil && !m.state.Settings.ReadOnly && m.saver != nil {
		sel := m.selectedItem()
		if sel != nil && sel.hasNode {
			editSuffix = " · e edit"
		}
	}
	searchSuffix := ""
	if m.searcher != nil {
		searchSuffix = " · / search"
	}
	var keys string
	switch {
	case m.showDiff:
		if m.pendingEdit != nil {
			keys = "↑/↓ scroll · PgUp/PgDown page · Enter save · Esc discard"
		} else {
			keys = "↑/↓ scroll · PgUp/PgDown page · Esc close"
		}
	case m.showSaveConfirm:
		keys = "Enter save · Esc cancel"
	case m.showReloadConfirm:
		keys = "Enter reload · Esc cancel"
	case m.showDetail:
		keys = "↑/↓ scroll · PgUp/PgDown page · Esc back"
	case m.showDiagnostics:
		keys = "↑/↓ navigate · Enter/+ detail · Esc close"
	case m.logDetailOpen:
		keys = "↑/↓ scroll · PgUp/PgDown page · Esc back · q quit"
	case m.showLogs:
		keys = "↑/↓ move · PgUp/PgDown page · Enter detail · f follow (on/off) · p pause/resume · Esc close · q quit"
	case m.searchActive:
		keys = "type to search · ↑/↓ move · PgUp/PgDown page · Enter open · Esc close"
	case m.state != nil && m.state.Graph != nil:
		keys = fmt.Sprintf("↑/↓ move · Enter toggle · PgUp/PgDown scroll · v format & validate · D diff%s%s · s save%s%s · q quit · %d items", reloadSuffix, editSuffix, logSuffix, searchSuffix, len(m.items))
	default:
		keys = "↑/↓ move · Enter toggle · PgUp/PgDown scroll · v format & validate · D diff" + reloadSuffix + " · s save" + logSuffix + searchSuffix + " · q quit"
	}
	return statusLineStyle.Width(width).Render(keys)
}

// diagnosticsView renders the validation results modal. It lists the
// diagnostics with a movable cursor; the caller is responsible for
// closing the modal through closeDiagnostics. The bottom footer shows
// the context-aware keys, so the pane itself carries no hint line.
func (m *Model) diagnosticsView(width, height int) string {
	title := fmt.Sprintf("Validation · %d diagnostic(s) · Esc close", len(m.diagnostics))
	bodyH := height - 3 // border (2) + title (1)
	if bodyH < 1 {
		bodyH = 1
	}
	// paneStyle has Border(RoundedBorder()) and Padding(0, 1):
	// Width(N) sets the *content* width to N, and the rendered total
	// is N + 2 (borders). To make the modal fit the window exactly
	// (matching the tree+source pane math elsewhere), pass
	// width - 2 here so the total comes out to width.
	//
	// Within the pane, the cursor prefix ("▸ " or "  ") eats 2 more
	// cells, so the available text width is width - 6. Truncate
	// each diagnostic string to that width to keep long messages
	// from pushing the pane past its right border.
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	textW := paneContentW - 2 - 2 // border (2) + padding (2) + cursor prefix (2)
	if textW < 1 {
		textW = 1
	}
	var body strings.Builder
	if len(m.diagnostics) == 0 {
		body.WriteString(dimStyle.Render("no diagnostics — close with Esc"))
	} else {
		start := m.diagCursor - bodyH/2
		if start < 0 {
			start = 0
		}
		end := start + bodyH
		if end > len(m.diagnostics) {
			end = len(m.diagnostics)
		}
		for i := start; i < end; i++ {
			d := m.diagnostics[i]
			line := truncateToWidth(d.String(), textW)
			if i == m.diagCursor {
				line = cursorStyle.Render("▸ " + line)
			} else {
				line = "  " + line
			}
			body.WriteString(line + "\n")
		}
	}
	return paneStyle.Width(paneContentW).Height(height).Render(title + "\n" + body.String())
}

// diagnosticDetailView renders the full diagnostic for the entry
// under the cursor. The path / line / column / severity are listed
// on fixed labels, then a blank line, then the message word-wrapped
// to the available body width via detailViewport. The viewport
// scroll position is preserved across renders because the body is
// only rebuilt when the cursor or the body size changes: SetContent
// resets the viewport scroll, so calling it on every render would
// make PgUp / PgDown unusable.
func (m *Model) diagnosticDetailView(width, height int) string {
	title := "Diagnostic detail · Esc back"
	// The pane has no hint line of its own: the bottom footer shows
	// the context-aware keys. The title keeps the "Esc back" hint so
	// the escape affordance is always visible inside the pane.

	// paneStyle has Border(RoundedBorder()) and Padding(0, 1):
	// Width(N) sets the *content* width to N, and the rendered total
	// is N + 2 (borders). To make the modal fit the window exactly
	// (matching the tree+source pane math and the diagnosticsView
	// fix from the previous milestone), pass width - 2 here so the
	// total is width.
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	bodyH := height - 3 // border (2) + title (1)
	if bodyH < 1 {
		bodyH = 1
	}

	// Re-sync only when the size has changed since the last set
	// (e.g. the terminal was resized while the detail view was
	// open). Re-syncing resets the scroll; doing it on every
	// render would make PgUp / PgDown unusable.
	if m.detailViewport.Height != bodyH {
		m.syncDetailContent()
	}

	return paneStyle.Width(paneContentW).Height(height).Render(
		title + "\n" + m.detailViewport.View(),
	)
}

// diffView renders the unified diff modal. The content is only rebuilt
// when the body height changes, so scrolling with PgUp/PgDown is
// preserved across renders. A diff reviewing an editor edit offers
// Enter to save and Esc to discard.
func (m *Model) diffView(width, height int) string {
	title := m.diffTitle
	if m.pendingEdit != nil {
		title += " · Enter save · Esc discard"
	} else {
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
	return paneStyle.Width(paneContentW).Height(height).Render(title + "\n" + m.diffViewport.View())
}

// searchView renders the read-only search modal: the current query on an
// input row and the scrollable result list below it. The content is only
// rebuilt when the size changes; the offset survives across renders so
// PgUp/PgDown and the cursor reveal keep working.
func (m *Model) searchView(width, height int) string {
	query := string(m.searchQuery)
	title := "Search"
	if query != "" {
		title += " · " + truncateToWidth(query, 30)
	}
	title += fmt.Sprintf(" · %d result(s)", len(m.searchResults))
	bodyH := height - 3 // border (2) + title (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	// The query input row takes one line above the results.
	viewportH := bodyH - 1
	if viewportH < 1 {
		viewportH = 1
	}
	m.syncSearchViewport(paneContentW, viewportH)
	input := cursorStyle.Render("> ") + query
	inputW := paneContentW - 4 // border (2) + padding (2)
	if inputW < 1 {
		inputW = 1
	}
	input = truncateToWidth(input, inputW)
	return paneStyle.Width(paneContentW).Height(height).Render(title + "\n" + input + "\n" + m.searchViewport.View())
}

// syncSearchViewport sizes the search viewport and refreshes its content
// from the current results, highlighting the row under searchCursor. The
// current offset is preserved by SetContent (it clamps only when the
// result set shrank), matching the "don't clobber manual scroll" rule used
// elsewhere.
func (m *Model) syncSearchViewport(width, height int) {
	contentW := width - 4 // border (2) + horizontal padding (2)
	if contentW < 1 {
		contentW = 1
	}
	contentH := height
	if contentH < 1 {
		contentH = 1
	}
	m.searchViewport.Width = contentW
	m.searchViewport.Height = contentH

	var content strings.Builder
	if len(m.searchResults) == 0 {
		if string(m.searchQuery) != "" {
			content.WriteString(dimStyle.Render("no matches"))
		} else {
			content.WriteString(dimStyle.Render("type to search across sites, files and logs"))
		}
	} else {
		textW := contentW - 2 // cursor prefix ("▸ ")
		if textW < 1 {
			textW = 1
		}
		for i, r := range m.searchResults {
			line := truncateToWidth(r.Label, textW)
			if i == m.searchCursor {
				line = cursorStyle.Render("▸ " + line)
			} else {
				line = "  " + line
			}
			content.WriteString(line + "\n")
		}
	}
	m.searchViewport.SetContent(content.String())
}

// wrapText wraps text to fit within the given cell width, breaking on
// word boundaries when possible. A single word longer than the
// width is hard-broken on rune boundaries so multi-byte characters
// are never split. Short lines are not padded to the width: the
// result is suitable for a scrolling viewport where trailing
// spaces would be visible on the right.
func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	var b strings.Builder
	lineW := 0
	for _, word := range strings.Fields(text) {
		wW := lipgloss.Width(word)
		if wW > width {
			// Word longer than the width: hard-break on rune
			// boundaries.
			if lineW > 0 {
				b.WriteString("\n")
				lineW = 0
			}
			for _, r := range word {
				if lineW >= width {
					b.WriteString("\n")
					lineW = 0
				}
				b.WriteRune(r)
				lineW += lipgloss.Width(string(r))
			}
			continue
		}
		if lineW == 0 {
			b.WriteString(word)
			lineW = wW
		} else if lineW+1+wW <= width {
			b.WriteString(" ")
			b.WriteString(word)
			lineW += 1 + wW
		} else {
			b.WriteString("\n")
			b.WriteString(word)
			lineW = wW
		}
	}
	return b.String()
}

// buildItems flattens the graph into the visible tree: one row per
// document (root first, then imported files in resolution order),
// with site blocks, global options, snippets and named routes nested
// under their document.
func buildItems(g *caddyfile.ImportGraph, collapsed map[string]bool) []item {
	var items []item
	for _, doc := range g.Documents {
		items = append(items, item{
			depth:     0,
			doc:       doc,
			collapsed: collapsed[doc.Path],
		})
		if collapsed[doc.Path] {
			continue
		}
		for _, n := range doc.Nodes {
			// Only the rendered block kinds appear in the tree; opaque
			// directive rows (e.g. top-level import) stay out, exactly as
			// before. nodeLabel still labels them for global search.
			switch n.Kind {
			case caddyfile.KindGlobalOptions, caddyfile.KindSite, caddyfile.KindSnippet, caddyfile.KindNamedRoute:
				items = append(items, item{label: nodeLabel(n), depth: 1, doc: doc, node: n, hasNode: true})
			}
		}
	}
	return items
}

// nodeLabel renders the tree label for a node. buildItems and the search
// scope share it, so a collapsed document still contributes its nodes to
// global search with the same labels the tree would show.
func nodeLabel(n caddyfile.Node) string {
	switch n.Kind {
	case caddyfile.KindGlobalOptions:
		// The global options block has no header, so it renders under a
		// fixed label rather than a name.
		return "global options"
	case caddyfile.KindSnippet:
		return "snippet (" + n.Name + ")"
	case caddyfile.KindNamedRoute:
		return "route &(" + n.Name + ")"
	default:
		return n.Name // KindSite and unknown kinds
	}
}

// numberedSource renders the source pane content: line numbers, the exact
// source bytes and syntax highlighting. It is a thin wrapper around
// highlightSource that accepts the same selected-line range so syncSource can
// keep the gutter highlight in sync with the tree selection.
func numberedSource(src []byte, selStartLine, selEndLine int) string {
	return highlightSource(src, selStartLine, selEndLine)
}
