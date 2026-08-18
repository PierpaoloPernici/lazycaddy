package ui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/backup"
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

// copyResultMsg is delivered after a clipboard adapter accepts the exact
// bytes selected in the source tree.
type copyResultMsg struct {
	size int
	err  error
}

// browserResultMsg is delivered after the external help URL opener returns.
type browserResultMsg struct {
	err   error
	label string
}

// externalChangeMsg is delivered when the change monitor detects an
// external modification of a watched document. err is nil exactly when
// change carries a meaningful detection; a non-nil err reports a monitor
// failure (for example ErrChangeClosed or an unreadable directory).
type externalChangeMsg struct {
	change app.ExternalChange
	err    error
}

// backupListMsg is delivered when an asynchronous backup listing
// completes. Path is the document the listing was requested for;
// Entries is empty when no backups exist or the directory is missing.
type backupListMsg struct {
	Path    string
	Entries []backup.Entry
	Err     error
}

// backupCompareMsg is delivered when the bytes for a backup comparison
// have been read: the current on-disk bytes of the target document and
// the selected backup's bytes. Any read failure aborts the comparison.
type backupCompareMsg struct {
	Path       string
	BackupPath string
	Current    []byte
	Backup     []byte
	Err        error
}

// rollbackResultMsg is delivered after an asynchronous rollback through
// the injected app.Rollbacker completes.
type rollbackResultMsg struct {
	Result app.RollbackResult
	Err    error
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

// deleteValidatedMsg is delivered after the delete candidate (the document
// with the selected node removed) has been validated.
type deleteValidatedMsg struct {
	Path        string
	Original    []byte
	Content     []byte
	Diagnostics []validator.Diagnostic
	Err         error
}

// structuredAddValidatedMsg is delivered after a planned structured add or
// edit has passed the same format+validate boundary as other edits.
type structuredAddValidatedMsg struct {
	Path            string
	Original        []byte
	Content         []byte
	Formatted       []byte
	Diagnostics     []validator.Diagnostic
	Name            string
	Operation       string
	Parent          caddyfile.Node
	ItemKey         string
	AnchorStartLine int
	Err             error
}

// pendingEdit holds a validated, recomposed document that came out of an
// edit workflow and is awaiting the diff review and the save
// confirmation. path may be an imported file; it is the exact document the
// edit targets. nodeName and startLine carry the identity of the edited
// node so the tree can re-anchor the selection after a structural save.
// itemKey is the pre-edit stable key of the selected row; the post-save
// re-anchor tries it first (the node survived at the same range) and
// falls back to the node name and then the document row when the edit
// moved or resized the node.
type pendingEdit struct {
	path         string
	original     []byte
	content      []byte
	snapshotPath string
	nodeName     string
	startLine    int
	itemKey      string
	operation    string
	// commentStartLine is the pre-edit start line of a comment-group
	// edit (0 otherwise). Comment groups are source annotations with no
	// node identity, so the post-save re-anchor uses the nearest comment
	// group by start line instead of a node name.
	commentStartLine int
}

// pendingDelete holds a validated document with the selected node removed,
// awaiting the delete-diff confirmation and the normal save pipeline.
type pendingDelete struct {
	path     string
	original []byte
	content  []byte
}

// pendingChange holds a detected external change while the conflict
// modal is open. inMem is a copy of the affected document's in-memory
// source taken at detection time, so the compare diff stays stable even
// if the graph changes underneath.
type pendingChange struct {
	change app.ExternalChange
	inMem  []byte
}

// errorHistoryMax bounds the in-app error history. The log is bounded so
// a long session can never grow the model state without limit.
const errorHistoryMax = 50

// errorEntry is one recorded failure in the bounded error history. Every
// entry names the failed operation, the message and a safe next action so
// the operator always knows how to recover.
type errorEntry struct {
	Op      string
	Message string
	Next    string
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
	// diffHOffset is the horizontal scroll offset (in display columns)
	// applied to long diff lines. h/l shift it in the diff modal.
	diffHOffset int
	// diffHunkCursor is the index of the currently selected hunk header
	// in the diff, for n/N hunk navigation.
	diffHunkCursor int

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

	// version is the lazycaddy application version injected at
	// construction time; it is shown next to the brand label in the
	// header and never changes during the session.
	version string

	// editor launches $EDITOR on the selected node range; nil disables
	// the e keybinding (no editor command and/or no validation binary).
	editor app.Editor
	// editing is true from the moment the editor session starts until the
	// recomposed result is delivered; a second e press is ignored while
	// it is set, mirroring the saving/busy guards.
	editing bool
	// deleting is true while a delete candidate is being validated; a
	// second d press is ignored while it is set, so two concurrent
	// validations can never overwrite each other's pendingDelete.
	deleting bool
	// editorSession is the active app.EditSession between editorReadyMsg
	// and editorDoneMsg. It is cleared once the result is handled.
	editorSession *app.EditSession
	// pendingEdit holds a validated recomposed document awaiting the diff
	// review, which is the single confirmation for saving it. nil means no
	// editor edit is pending.
	pendingEdit *pendingEdit
	// pendingDelete holds a validated document with the selected node
	// removed, awaiting the delete-diff confirmation and the normal save
	// pipeline. nil means no delete is pending.
	pendingDelete *pendingDelete

	// Structured add modal state. The modal first selects a context-aware
	// directive from the advisory catalog, then collects its raw arguments.
	// The caddyfile planner remains authoritative before validation or save.
	showStructuredAdd           bool
	structuredAddInput          structuredInput
	structuredAddDoc            *caddyfile.Document
	structuredAddParent         caddyfile.Node
	structuredAddKey            string
	structuredAddBusy           bool
	structuredAddMode           structuredAddMode
	structuredAddName           string
	structuredAddItems          []string
	structuredAddCursor         int
	structuredAddFields         []structuredInput
	structuredAddFieldLabels    []string
	structuredAddFieldCursor    int
	structuredAddEditing        bool
	structuredAddCreating       bool
	structuredAddNewKind        caddyfile.Kind
	structuredAddNewName        structuredInput
	structuredAddNewArgs        structuredInput
	structuredAddNewField       int
	structuredAddNewTop         bool
	structuredAddReorderTargets []caddyfile.Node

	// commentEditStartLine records the start line of a comment-group
	// edit while the $EDITOR round-trip is in flight (0 otherwise). It
	// lets handleEditorDone enforce the comment-only content rule on the
	// edited range.
	commentEditStartLine int
	// commentInsertActive reports that the current $EDITOR round-trip
	// inserts a new comment at commentInsertPos (a byte offset into the
	// original document). handleEditorDone uses both to enforce the
	// comment-only content rule on the inserted bytes and to re-anchor
	// the selection on the new group.
	commentInsertActive bool
	commentInsertPos    int
	// structuredAddPlacementFromPicker reports that the comment-placement
	// sub-picker was opened from the directive picker (Esc returns to it)
	// rather than directly from a document row (Esc closes the flow).
	structuredAddPlacementFromPicker bool

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

	// matcherNav drives the matcher definition↔reference cycler (the g
	// keybinding): it holds the named matcher occurrences of the document
	// the session was built for and a cursor. A nil session (or a session
	// keyed on a different document) makes the next press rebuild the list
	// and start at-or-after the current selection.
	matcherNav *matcherNav

	// showInlineFindings toggles the advisory inline validation overlay in
	// the source pane (the i keybinding). When active, parse-tree findings
	// for the selected document are highlighted in place; Caddy's own
	// validation (v) remains the authority and this never blocks a save or
	// reload.
	showInlineFindings bool
	// inlineFindings caches the computed findings for the current document,
	// keyed on the document pointer and its source so a reload or edit
	// recomputes them while a steady selection does not.
	inlineFindings       []caddyfile.InlineFinding
	inlineFindingsDoc    *caddyfile.Document
	inlineFindingsSource []byte

	// clipboard copies exact source bytes for the y keybinding. It is nil
	// when the host exposes no clipboard backend.
	clipboard app.Clipboard
	// textSel is the active pane-aware text selection (mouse or keyboard)
	// in the source, log or diff panes. pane identifies the owning pane;
	// state holds the UI-independent anchor/cursor.
	textSel textSelect
	// browser opens the official Caddyfile help page for the Ctrl-H keybinding.
	// It is nil when no platform browser opener is available.
	browser app.Browser

	// monitor detects external changes to the resolved configuration
	// documents (root and imports) and feeds the conflict modal; nil
	// disables the feature. The existing synchronous conflict guards
	// (saver, editor and reloader) remain the final safety net.
	monitor app.ChangeMonitor
	// showChangeConflict is true while the external-change conflict
	// modal is open. It takes precedence over every other modal.
	showChangeConflict bool
	// changeCompare is true while the conflict flow is showing the
	// compare diff (in-memory vs on-disk) inside the shared diff modal.
	changeCompare bool
	// pendingChange holds the change awaiting the operator decision
	// while the conflict modal is open.
	pendingChange *pendingChange

	// rollbacker lists, compares and restores backups through the app
	// boundary; nil disables the B keybinding. UI models never touch the
	// backup directory or the filesystem directly.
	rollbacker app.Rollbacker
	// showBackups is true while the backup-history modal is open for the
	// document selected when B was pressed.
	showBackups bool
	// backupsLoading is true while a backup listing is in flight; a
	// second B press is ignored.
	backupsLoading bool
	// backups holds the listed entries for backupDocPath, newest first.
	backups []backup.Entry
	// backupCursor is the index into backups of the selected entry.
	backupCursor int
	// backupViewport renders the backup list.
	backupViewport viewport.Model
	// backupDocPath is the exact source path the listed backups belong
	// to.
	backupDocPath string
	// backupComparing is true while the shared diff modal shows a backup
	// vs current-on-disk comparison.
	backupComparing bool
	// showRollbackConfirm is true when the rollback-confirmation modal is
	// open.
	showRollbackConfirm bool
	// pendingRollback holds the backup awaiting the rollback
	// confirmation. nil means no rollback is pending.
	pendingRollback *pendingRollback
	// rollingBack is true while an asynchronous rollback is in flight; a
	// second confirmation is ignored, mirroring the saving guard.
	rollingBack bool

	// readFile reads a document's current on-disk bytes for the
	// per-document D diff (and the root fallback when no working copy
	// exists). nil disables the on-disk comparison with a hint. The UI
	// never touches the filesystem directly; main.go injects os.ReadFile.
	readFile app.FileReader

	// showUnsavedConfirm is true while the unsaved-changes confirmation
	// modal is open (a quit was requested with unsaved edits).
	showUnsavedConfirm bool

	// errorHistory is a bounded record of reported failures, opened by
	// the H keybinding.
	errorHistory []errorEntry
	// showErrorHistory is true while the error-history view is open.
	showErrorHistory bool
	// errorHistoryViewport renders the bounded error-history list.
	errorHistoryViewport viewport.Model

	// showCommandPalette is true while the searchable command catalog is
	// open. The palette is a discoverability layer over the same actions
	// invoked by the direct hotkeys; it never replaces them.
	showCommandPalette bool
	commandQuery       []rune
	commandCursor      int
	commandViewport    viewport.Model
	commandLineOffsets []int
}

// pendingRollback holds the state of a backup selected for rollback:
// the target document, the backup to restore, and the on-disk bytes
// captured when the comparison diff was opened (the external-change
// baseline the rollback re-checks before restoring).
type pendingRollback struct {
	path         string
	backupPath   string
	currentBytes []byte
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
// Load before starting the program. version is the lazycaddy application
// version (e.g. "dev" or "v1.2.3") shown in the header brand label. An
// optional clipboard may be supplied; omitting it keeps the model read-only
// with respect to clipboard integration and disables the y keybinding.
// monitor detects external changes to the resolved documents; omitting
// it disables the watch feature (the synchronous conflict guards stay
// active). rollbacker lists and restores backups; omitting it disables
// the B keybinding. readFile reads a document's on-disk bytes for the
// per-document D diff; omitting it disables the on-disk comparison with
// a hint (the root working-copy diff keeps working).
func New(loader app.Loader, formatter app.Formatter, saver app.Saver, reloader app.Reloader, runtimeStatus app.RuntimeStatus, logSource app.LogSource, editor app.Editor, searcher app.Searcher, version string, monitor app.ChangeMonitor, rollbacker app.Rollbacker, readFile app.FileReader, clipboards ...app.Clipboard) *Model {
	var clipboard app.Clipboard
	if len(clipboards) > 0 {
		clipboard = clipboards[0]
	}
	return &Model{
		loader:               loader,
		formatter:            formatter,
		saver:                saver,
		reloader:             reloader,
		runtime:              runtimeStatus,
		logSource:            logSource,
		editor:               editor,
		searcher:             searcher,
		version:              version,
		collapsed:            map[string]bool{},
		viewport:             viewport.New(1, 1),
		detailViewport:       viewport.New(1, 1),
		diffViewport:         viewport.New(1, 1),
		logViewport:          viewport.New(1, 1),
		logDetailViewport:    viewport.New(1, 1),
		searchViewport:       viewport.New(1, 1),
		backupViewport:       viewport.New(1, 1),
		errorHistoryViewport: viewport.New(1, 1),
		commandViewport:      viewport.New(1, 1),
		logFollow:            true,
		clipboard:            clipboard,
		monitor:              monitor,
		rollbacker:           rollbacker,
		readFile:             readFile,
	}
}

// NewWithBrowser is New with an injected external browser opener. Keeping the
// original constructor preserves the small test and plugin surface for
// callers that do not need browser help.
func NewWithBrowser(loader app.Loader, formatter app.Formatter, saver app.Saver, reloader app.Reloader, runtimeStatus app.RuntimeStatus, logSource app.LogSource, editor app.Editor, searcher app.Searcher, version string, monitor app.ChangeMonitor, rollbacker app.Rollbacker, readFile app.FileReader, browser app.Browser, clipboards ...app.Clipboard) *Model {
	m := New(loader, formatter, saver, reloader, runtimeStatus, logSource, editor, searcher, version, monitor, rollbacker, readFile, clipboards...)
	m.browser = browser
	return m
}

// Load resolves the configuration through the injected loader and
// builds the document tree. Parse errors are kept inside the state so
// the raw source view remains available; only a read failure (missing
// file) is returned. The initial tree layout is deterministic: every
// document root is expanded, every visible branch below the document
// roots starts collapsed, and the cursor starts on the first document
// row. Expansion state is derived per session and never persisted across
// sessions unless explicitly configured.
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
		// A fresh session never inherits expansion state: seed the
		// startup layout and start on the first visible document row.
		m.collapsed = map[string]bool{}
		seedCollapsedState(state.Graph, m.collapsed)
		m.items = buildItems(state.Graph, m.collapsed)
		m.cursor = 0
		// A fresh load never inherits a stale loaded claim: whether the
		// running config matches this file is unknown until a reload
		// proves it.
		m.loaded = loadedUnknown
		m.loadedAt = time.Time{}
		// Re-target the change monitor at the freshly resolved documents
		// (root and imports) so it can detect external modifications.
		m.syncMonitor()
	}
	return err
}

// Init implements tea.Model. Loading is done synchronously before the
// program starts, so the only startup commands are the async runtime
// probe (when a probe is configured) and the external-change watch
// (when a monitor is wired and a graph is loaded).
func (m *Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.runtime != nil {
		cmds = append(cmds, m.runtimeProbeCmd())
	}
	if m.monitor != nil && m.state != nil && m.state.Graph != nil {
		cmds = append(cmds, m.watchCmd())
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
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
