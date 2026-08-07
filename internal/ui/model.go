package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

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

	// validatorTimeout is read from settings on Load and is the timeout
	// applied to each format+validate invocation. Zero means "no extra
	// timeout on top of the validator package default (5s)".
	validatorTimeout time.Duration
}

// New returns a Model that will load its state through loader, run
// format+validate through formatter and write changes through saver.
// formatter may be nil; the v keybinding is disabled in that case.
// saver may be nil; the s keybinding is disabled in that case
// (read-only mode). Call Load before starting the program.
func New(loader app.Loader, formatter app.Formatter, saver app.Saver) *Model {
	return &Model{
		loader:         loader,
		formatter:      formatter,
		saver:          saver,
		collapsed:      map[string]bool{},
		viewport:       viewport.New(1, 1),
		detailViewport: viewport.New(1, 1),
		diffViewport:   viewport.New(1, 1),
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
	}
	return err
}

// Init implements tea.Model. Loading is done synchronously before the
// program starts, so there is no startup command.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case formatAndValidateResultMsg:
		return m.handleFormatAndValidateResult(msg)
	case saveResultMsg:
		return m.handleSaveResult(msg)
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
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
	// The diagnostics modal takes precedence over every other key.
	if m.showDiagnostics {
		return m.updateDiagnosticsKey(msg)
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
	}
	return m, nil
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
func (m *Model) updateDiffKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.closeDiff()
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
// target path and backup directory before confirming the write.
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
// save); Esc and q cancel.
func (m *Model) updateSaveConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.closeSaveConfirm()
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
// goroutine and reports the result as a saveResultMsg.
func (m *Model) saveCmd() tea.Cmd {
	saver := m.saver
	path := m.state.Settings.ConfigPath
	original := m.loadedBytes
	working := m.workingBytes
	return func() tea.Msg {
		result, err := saver.Save(context.Background(), path, original, working)
		return saveResultMsg{Result: result, Err: err}
	}
}

// handleSaveResult is invoked on the main goroutine when the saver
// returns. On success it refreshes the loaded snapshot and the root
// source so the source viewport and the diff command reflect the new
// state. On failure it surfaces the specific error in the status
// line, including the backup path when one was created.
func (m *Model) handleSaveResult(msg saveResultMsg) (tea.Model, tea.Cmd) {
	m.saving = false
	if msg.Err == nil {
		m.loadedBytes = append([]byte(nil), m.workingBytes...)
		m.state.Graph.Root.Source = append([]byte(nil), m.workingBytes...)
		// Force the source viewport to reload on next render.
		m.sourceDoc = nil
		m.statusMessage = "✓ saved (backup: " + msg.Result.BackupPath + ")"
		return m, nil
	}
	if errors.Is(msg.Err, app.ErrConflict) {
		m.statusMessage = "✗ file changed on disk — reload before saving"
		return m, nil
	}
	var saveErr *app.SaveError
	if errors.As(msg.Err, &saveErr) {
		m.statusMessage = "✗ save failed (backup: " + saveErr.BackupPath + "): " + saveErr.Err.Error()
		return m, nil
	}
	m.statusMessage = "✗ save failed: " + msg.Err.Error()
	return m, nil
}

// saveConfirmView renders the save-confirmation modal. It names the
// target path and the backup directory (safety requirement for any
// replacing action) and offers Enter to confirm or Esc to cancel.
func (m *Model) saveConfirmView(width, height int) string {
	title := "Save config · Enter save · Esc cancel"
	bodyH := height - 3 // border (2) + title (1)
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	var body strings.Builder
	body.WriteString(dimStyle.Render("Path       ") + m.state.Settings.ConfigPath + "\n")
	body.WriteString(dimStyle.Render("Backup dir ") + m.state.Settings.BackupDir + "\n")
	body.WriteString("\n")
	body.WriteString(dimStyle.Render("a backup is created before the file is replaced") + "\n")
	body.WriteString(dimStyle.Render("review the diff with D before confirming"))
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
	} else if m.showDiagnostics {
		if m.showDetail {
			b.WriteString(m.diagnosticDetailView(width, paneH))
		} else {
			b.WriteString(m.diagnosticsView(width, paneH))
		}
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
	// Important state is conveyed by an explicit text label, not
	// color alone, matching the existing READ-ONLY pattern.
	right := readOnlyBadge.Render(" READ-ONLY ")
	if m.state != nil && !m.state.Settings.ReadOnly {
		right = writableBadge.Render(" WRITE ")
	}
	if m.state != nil && m.state.Graph != nil && m.state.Graph.Err != nil {
		right = errorStyle.Render(" PARSE ERROR ") + right
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
	if doc != m.sourceDoc {
		// New document: load its full source and start at the top. The
		// reveal below then scrolls just enough for the selected node.
		m.sourceDoc = doc
		m.sourceTitle = title
		var src []byte
		if doc != nil {
			src = doc.Source
		}
		m.viewport.SetContent(numberedSource(src))
		m.viewport.GotoTop()
	} else if title != m.sourceTitle {
		m.sourceTitle = title
	}
	// Reveal-if-needed, but only when the selection changed: after a
	// manual scroll the viewport must stay where the user left it.
	key := selectionKey{doc: doc}
	if selected != nil && selected.hasNode {
		key.hasNode = true
		key.node = selected.node.Name
		key.start = selected.node.Range.StartLine
		key.end = selected.node.Range.EndLine
	}
	if key != m.lastSel {
		m.lastSel = key
		if key.hasNode {
			m.revealRange(key.start, key.end)
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

func (m *Model) footer(width int) string {
	// Modals replace the global keymap with context-aware keys, so the
	// bottom footer never shows keys that are not active in the current
	// context.
	var keys string
	switch {
	case m.showDiff:
		keys = "↑/↓ scroll · PgUp/PgDown page · Esc close"
	case m.showSaveConfirm:
		keys = "Enter save · Esc cancel"
	case m.showDetail:
		keys = "↑/↓ scroll · PgUp/PgDown page · Esc back"
	case m.showDiagnostics:
		keys = "↑/↓ navigate · Enter/+ detail · Esc close"
	case m.state != nil && m.state.Graph != nil:
		keys = fmt.Sprintf("↑/↓ move · Enter toggle · PgUp/PgDown scroll · v format & validate · D diff · s save · q quit · %d items", len(m.items))
	default:
		keys = "↑/↓ move · Enter toggle · PgUp/PgDown scroll · v format & validate · D diff · s save · q quit"
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
// preserved across renders.
func (m *Model) diffView(width, height int) string {
	title := m.diffTitle + " · Esc close"
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
// with site blocks, snippets and named routes nested under their
// document.
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
			switch n.Kind {
			case caddyfile.KindSite:
				items = append(items, item{label: n.Name, depth: 1, doc: doc, node: n, hasNode: true})
			case caddyfile.KindSnippet:
				items = append(items, item{label: "snippet (" + n.Name + ")", depth: 1, doc: doc, node: n, hasNode: true})
			case caddyfile.KindNamedRoute:
				items = append(items, item{label: "route &(" + n.Name + ")", depth: 1, doc: doc, node: n, hasNode: true})
			}
		}
	}
	return items
}

func numberedSource(src []byte) string {
	if len(src) == 0 {
		return dimStyle.Render("(empty source — raw view still available)")
	}
	lines := strings.Split(string(src), "\n")
	var b strings.Builder
	for i, ln := range lines {
		fmt.Fprintf(&b, "%4d│ %s\n", i+1, ln)
	}
	return b.String()
}
