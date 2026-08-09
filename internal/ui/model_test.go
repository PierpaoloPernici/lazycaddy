package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// fakeLoader serves a prebuilt state, optionally with an error.
type fakeLoader struct {
	state *app.State
	err   error
}

func (f fakeLoader) LoadState() (*app.State, error) { return f.state, f.err }

// fakeFormatter is a programmable Formatter for tests. The default
// behavior (no fields set) reports a successful call with an empty
// formatted byte slice and no diagnostics.
type fakeFormatter struct {
	formatted   []byte
	diagnostics []validator.Diagnostic
	err         error
	calls       int
	// capturedCtx records the context passed to the last
	// FormatAndValidate call, so tests can verify the outer timeout
	// wiring (e.g. that a zero ValidatorTimeout does not cancel the
	// context before the validator sees it).
	capturedCtx context.Context
	// capturedDisplayPath records the displayPath passed to the last
	// FormatAndValidate call, so tests can verify the real Caddyfile
	// path is surfaced instead of a temp path.
	capturedDisplayPath string
}

func (f *fakeFormatter) FormatAndValidate(ctx context.Context, displayPath string, src []byte) ([]byte, []validator.Diagnostic, error) {
	f.calls++
	f.capturedCtx = ctx
	f.capturedDisplayPath = displayPath
	return f.formatted, f.diagnostics, f.err
}

// fakeSaver is a programmable app.Saver for tests. It records the
// path, original bytes and working bytes passed to Save and returns
// the configured result / error.
type fakeSaver struct {
	result           app.SaveResult
	err              error
	calls            int
	capturedPath     string
	capturedOriginal []byte
	capturedWorking  []byte
}

func (f *fakeSaver) Save(ctx context.Context, path string, original, working []byte) (app.SaveResult, error) {
	f.calls++
	f.capturedPath = path
	f.capturedOriginal = original
	f.capturedWorking = working
	return f.result, f.err
}

// diskSaver is a fakeSaver that also writes the working bytes into the
// fake filesystem on Save, modeling the atomic write so a subsequent loader
// reload picks up the new content.
type diskSaver struct {
	fakeSaver
	fs map[string]string
}

func (s *diskSaver) Save(ctx context.Context, path string, original, working []byte) (app.SaveResult, error) {
	s.fakeSaver.Save(ctx, path, original, working)
	s.fs[path] = string(working)
	return s.fakeSaver.result, s.fakeSaver.err
}

// fakeReloader is a programmable app.Reloader for tests. It records the
// path and saved bytes of the last Reload call.
type fakeReloader struct {
	result        app.ReloadResult
	err           error
	calls         int
	capturedPath  string
	capturedSaved []byte
}

func (f *fakeReloader) Reload(ctx context.Context, path string, saved []byte) (app.ReloadResult, error) {
	f.calls++
	f.capturedPath = path
	f.capturedSaved = saved
	return f.result, f.err
}

// fakeEditor is a programmable app.Editor for tests. Prepare records the
// document and range it was given and returns a session built from that
// document (so the flow tests exercise the real doc path, including
// imported files), or the configured session / error. Complete records
// the exit code and returns the configured result / error.
type fakeEditor struct {
	session          *app.EditSession
	prepareErr       error
	result           app.EditResult
	completeErr      error
	prepareCalls     int
	prepareFullCalls int
	completeCalls    int
	capturedDoc      *caddyfile.Document
	capturedRange    caddyfile.SourceRange
	capturedFullDoc  *caddyfile.Document
	capturedExit     int
}

type fakeClipboard struct {
	content []byte
	err     error
	calls   int
}

func (f *fakeClipboard) Copy(_ context.Context, content []byte) error {
	f.calls++
	f.content = append([]byte(nil), content...)
	return f.err
}

func (f *fakeEditor) Prepare(ctx context.Context, doc *caddyfile.Document, r caddyfile.SourceRange) (*app.EditSession, error) {
	f.prepareCalls++
	f.capturedDoc = doc
	f.capturedRange = r
	if f.prepareErr != nil {
		return nil, f.prepareErr
	}
	if f.session != nil {
		return f.session, nil
	}
	return &app.EditSession{
		Mode:         app.EditNode,
		DocPath:      doc.Path,
		Range:        r,
		Original:     append([]byte(nil), doc.Source...),
		RangeBytes:   append([]byte(nil), doc.Source[r.Start:r.End]...),
		TempFile:     "editor-temp",
		SnapshotPath: "editor-snapshot",
		Cmd:          []string{"vim", "editor-temp"},
	}, nil
}

// PrepareFull implements app.Editor for full-document edits: the editor
// receives the whole source and the session carries EditFull.
func (f *fakeEditor) PrepareFull(ctx context.Context, doc *caddyfile.Document) (*app.EditSession, error) {
	f.prepareFullCalls++
	f.capturedFullDoc = doc
	if f.prepareErr != nil {
		return nil, f.prepareErr
	}
	if f.session != nil {
		return f.session, nil
	}
	return &app.EditSession{
		Mode:         app.EditFull,
		DocPath:      doc.Path,
		Range:        caddyfile.SourceRange{Start: 0, End: len(doc.Source)},
		Original:     append([]byte(nil), doc.Source...),
		RangeBytes:   append([]byte(nil), doc.Source...),
		TempFile:     "editor-temp",
		SnapshotPath: "editor-snapshot",
		Cmd:          []string{"vim", "editor-temp"},
	}, nil
}

func (f *fakeEditor) Complete(ctx context.Context, s *app.EditSession, exitCode int) (app.EditResult, error) {
	f.completeCalls++
	f.capturedExit = exitCode
	if f.completeErr != nil {
		return app.EditResult{}, f.completeErr
	}
	return f.result, nil
}

type noSuchFile struct{ path string }

func (e *noSuchFile) Error() string { return "no such file: " + e.path }

func stateFor(t *testing.T, path string, readFile app.FileReader) *app.State {
	t.Helper()
	loader := app.NewLoader(config.Settings{ConfigPath: path, ReadOnly: true}, readFile)
	state, err := loader.LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return state
}

// writableStateFor is like stateFor but marks the settings writable
// and sets a backup directory, so save-related tests can exercise
// write mode and verify the backup path is surfaced.
func writableStateFor(t *testing.T, path, backupDir string, readFile app.FileReader) *app.State {
	t.Helper()
	loader := app.NewLoader(config.Settings{ConfigPath: path, ReadOnly: false, BackupDir: backupDir}, readFile)
	state, err := loader.LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return state
}

// newLoadedModel builds a Model from loader with optional formatter,
// saver, reloader, runtime probe, log source, editor, searcher, change
// monitor, rollbacker and clipboard. The variadic options accept an
// app.Formatter, an app.Saver, an app.Reloader, an app.RuntimeStatus, an
// app.LogSource, an app.Editor, an app.Searcher, an app.ChangeMonitor, an
// app.Rollbacker, an app.Clipboard, any combination, or none. The
// searcher defaults to app.NewSearcher(); pass a typed nil app.Searcher
// to disable it.
func newLoadedModel(t *testing.T, loader app.Loader, opts ...any) *Model {
	t.Helper()
	var f app.Formatter
	var s app.Saver
	var r app.Reloader
	var rt app.RuntimeStatus
	var ls app.LogSource
	var e app.Editor
	var clip app.Clipboard
	var monitor app.ChangeMonitor
	var rollbacker app.Rollbacker
	var readFile app.FileReader
	searcher := app.NewSearcher()
	for _, opt := range opts {
		switch v := opt.(type) {
		case app.Formatter:
			f = v
		case app.Saver:
			s = v
		case app.Reloader:
			r = v
		case app.RuntimeStatus:
			rt = v
		case app.LogSource:
			ls = v
		case app.Editor:
			e = v
		case app.Clipboard:
			clip = v
		case app.ChangeMonitor:
			monitor = v
		case app.Rollbacker:
			rollbacker = v
		case app.Searcher:
			searcher = v
		case app.FileReader:
			readFile = v
		}
	}
	m := New(loader, f, s, r, rt, ls, e, searcher, testVersion, monitor, rollbacker, readFile, clip)
	if err := m.Load(); err != nil && m.state == nil {
		t.Fatalf("Load: %v", err)
	}
	return m
}

// testVersion is the application version injected by test helpers.
const testVersion = "dev"

func keyPress(t *testing.T, m *Model, msg tea.Msg) *Model {
	t.Helper()
	updated, _ := m.Update(msg)
	return updated.(*Model)
}

// newLoadedModelWithoutSearcher builds a loaded model with the search
// searcher disabled (nil) and every other service nil. The variadic opts of
// newLoadedModel cannot express a nil app.Searcher (a nil interface in a
// ...any slot matches no type-switch case), so this constructs New directly.
func newLoadedModelWithoutSearcher(t *testing.T, loader app.Loader) *Model {
	t.Helper()
	m := New(loader, nil, nil, nil, nil, nil, nil, nil, testVersion, nil, nil, nil)
	if err := m.Load(); err != nil && m.state == nil {
		t.Fatalf("Load: %v", err)
	}
	return m
}

func TestCopyKeyCopiesExactSelectedNodeRange(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n\n# untouched\n"
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte(source), nil
	})
	clip := &fakeClipboard{}
	m := newLoadedModel(t, fakeLoader{state: state}, clip)

	// The root document row is followed by its site node.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("copy command is nil")
	}
	msg := cmd()
	updated, _ := m.Update(msg)
	m = updated.(*Model)

	want := "example.test {\n\trespond / \"hello\"\n}\n"
	if !bytes.Equal(clip.content, []byte(want)) {
		t.Errorf("copied node bytes = %q, want %q", clip.content, want)
	}
	if clip.calls != 1 {
		t.Errorf("clipboard calls = %d, want 1", clip.calls)
	}
	if !strings.Contains(m.statusMessage, "copied") {
		t.Errorf("statusMessage = %q, want copied notification", m.statusMessage)
	}
}

func TestCopyKeyCopiesCompleteDocumentRow(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte(source), nil
	})
	clip := &fakeClipboard{}
	m := newLoadedModel(t, fakeLoader{state: state}, clip)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("copy command is nil")
	}
	_, _ = m.Update(cmd())
	if !bytes.Equal(clip.content, []byte(source)) {
		t.Errorf("copied document bytes = %q, want %q", clip.content, source)
	}
}

func TestCopyKeyReportsUnavailableBackend(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte("example.test {}\n"), nil
	})
	m := newLoadedModel(t, fakeLoader{state: state})

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd != nil {
		t.Fatal("copy command is non-nil without a clipboard backend")
	}
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "copy unavailable") {
		t.Errorf("statusMessage = %q, want unavailable notification", m.statusMessage)
	}
}

func TestCopyKeyReportsBackendError(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte("example.test {}\n"), nil
	})
	clip := &fakeClipboard{err: errors.New("pipe broken")}
	m := newLoadedModel(t, fakeLoader{state: state}, clip)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("copy command is nil")
	}
	updated, _ := m.Update(cmd())
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "copy failed") {
		t.Errorf("statusMessage = %q, want failed notification", m.statusMessage)
	}
}

func resize(m *Model, width, height int) *Model {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(*Model)
}

func fsReader(fs map[string]string) app.FileReader {
	return func(p string) ([]byte, error) {
		src, ok := fs[p]
		if !ok {
			return nil, &noSuchFile{p}
		}
		return []byte(src), nil
	}
}

// fakeMonitor is a programmable app.ChangeMonitor for deterministic tests.
// Next returns the results pre-loaded into nextCh, in order.
type fakeMonitor struct {
	updateCalls int
	targets     []app.ChangeTarget
	updateErr   error
	closeCalls  int
	nextCh      chan monitorResult
}

type monitorResult struct {
	change app.ExternalChange
	err    error
}

func newFakeMonitor() *fakeMonitor {
	return &fakeMonitor{nextCh: make(chan monitorResult, 8)}
}

func (f *fakeMonitor) Update(targets []app.ChangeTarget) error {
	f.updateCalls++
	f.targets = append([]app.ChangeTarget(nil), targets...)
	return f.updateErr
}

func (f *fakeMonitor) Next(ctx context.Context) (app.ExternalChange, error) {
	select {
	case r := <-f.nextCh:
		return r.change, r.err
	case <-ctx.Done():
		return app.ExternalChange{}, ctx.Err()
	}
}

func (f *fakeMonitor) Close() error {
	f.closeCalls++
	return nil
}

// press runs a message through the model and returns the command, so
// tests can assert that the watch was re-armed after a resolution.
func press(m *Model, msg tea.Msg) (*Model, tea.Cmd) {
	updated, cmd := m.Update(msg)
	return updated.(*Model), cmd
}

func TestExternalChange_OpensConflictModalAndReloads(t *testing.T) {
	fs := map[string]string{"Caddyfile": "old {\n\trespond / \"a\"\n}\n"}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)

	// Load targeted the monitor at the resolved document.
	if mon.updateCalls != 1 || len(mon.targets) != 1 || mon.targets[0].Path != "Caddyfile" {
		t.Fatalf("monitor targets = %v (calls %d), want the root document", mon.targets, mon.updateCalls)
	}

	// The file changes on disk while browsing.
	fs["Caddyfile"] = "new {\n\trespond / \"b\"\n}\n"
	m, cmd := press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte(fs["Caddyfile"])}})
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open")
	}
	if cmd != nil {
		t.Fatal("watch must not re-arm while the conflict modal is open")
	}
	if m.hasUnsavedEdits() {
		t.Fatal("a fresh load must have no unsaved edits")
	}

	// No unsaved edits: Enter reloads the graph from disk.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.showChangeConflict {
		t.Fatal("conflict modal stayed open after reload")
	}
	if string(m.state.Graph.Root.Source) != fs["Caddyfile"] {
		t.Errorf("root source = %q, want the reloaded on-disk bytes", m.state.Graph.Root.Source)
	}
	if !strings.Contains(m.statusMessage, "reloaded") {
		t.Errorf("statusMessage = %q, want a reloaded notification", m.statusMessage)
	}
}

func TestExternalChange_KeepRetainsInMemoryVersionAndReArms(t *testing.T) {
	fs := map[string]string{"Caddyfile": "old {\n}\n"}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)

	fs["Caddyfile"] = "new {\n}\n"
	m, _ = press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte(fs["Caddyfile"])}})
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open")
	}

	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.showChangeConflict {
		t.Fatal("keep must close the conflict modal")
	}
	if string(m.state.Graph.Root.Source) != "old {\n}\n" {
		t.Errorf("keep must retain the in-memory version, got %q", m.state.Graph.Root.Source)
	}
	if cmd == nil {
		t.Fatal("keep must re-arm the watch")
	}
	if !strings.Contains(m.statusMessage, "kept") {
		t.Errorf("statusMessage = %q, want a kept notification", m.statusMessage)
	}
}

func TestExternalChange_UnsavedEditsCompareAndKeep(t *testing.T) {
	fs := map[string]string{"Caddyfile": "original {\n\trespond / \"a\"\n}\n"}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)

	// The operator validated a working copy that differs from disk.
	m.workingBytes = []byte("edited {\n\trespond / \"b\"\n}\n")
	m.workingValidated = true

	fs["Caddyfile"] = "original {\n\trespond / \"a\"\n}\n# external\n"
	m, _ = press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte(fs["Caddyfile"])}})
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open")
	}
	if !m.hasUnsavedEdits() {
		t.Fatal("a divergent working copy must count as unsaved edits")
	}

	// c opens the compare diff (in-memory vs on-disk) over the modal.
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if cmd != nil {
		t.Fatal("compare must not re-arm the watch")
	}
	if !m.showDiff || !m.changeCompare {
		t.Fatal("compare diff did not open")
	}
	if !strings.Contains(m.diffTitle, "Compare") {
		t.Errorf("diff title = %q, want a Compare title", m.diffTitle)
	}
	hasChange := false
	for _, l := range m.diffLines {
		if l.Kind == diff.KindAdd || l.Kind == diff.KindRemove {
			hasChange = true
		}
	}
	if !hasChange {
		t.Fatal("compare diff has no +/- lines")
	}

	// Esc from the compare returns to the conflict options.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.showChangeConflict || m.changeCompare || m.showDiff {
		t.Fatal("Esc from compare must return to the conflict options")
	}

	// Esc (keep) closes the flow and retains the working copy.
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showChangeConflict {
		t.Fatal("keep must close the conflict modal")
	}
	if !bytes.Equal(m.workingBytes, []byte("edited {\n\trespond / \"b\"\n}\n")) {
		t.Fatal("keep must retain the working copy")
	}
	if cmd == nil {
		t.Fatal("keep must re-arm the watch")
	}
}

func TestExternalChange_ReloadDiscardsWorkingCopy(t *testing.T) {
	fs := map[string]string{"Caddyfile": "old {\n}\n"}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)

	m.workingBytes = []byte("edited {\n}\n")
	m.workingValidated = true
	fs["Caddyfile"] = "new {\n}\n"
	m, _ = press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte(fs["Caddyfile"])}})

	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.showChangeConflict {
		t.Fatal("reload must close the conflict modal")
	}
	if m.workingBytes != nil || m.workingValidated {
		t.Fatal("reload must discard the in-memory working copy")
	}
	if string(m.state.Graph.Root.Source) != "new {\n}\n" {
		t.Errorf("root source = %q, want the reloaded bytes", m.state.Graph.Root.Source)
	}
}

func TestExternalChange_IgnoredWhenOnDiskMatchesMemory(t *testing.T) {
	const source = "example.test {\n}\n"
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte(source), nil })
	mon := newFakeMonitor()
	m := newLoadedModel(t, fakeLoader{state: state}, mon)

	// The notification races lazycaddy's own atomic save: the on-disk
	// bytes already equal the in-memory document, so it is a non-event.
	m, cmd := press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte(source)}})
	if m.showChangeConflict {
		t.Fatal("a no-op change must not open the conflict modal")
	}
	if cmd == nil {
		t.Fatal("the watch must re-arm after a no-op change")
	}
}

func TestExternalChange_DuringSaveIgnored(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte("old\n"), nil })
	mon := newFakeMonitor()
	m := newLoadedModel(t, fakeLoader{state: state}, mon)

	// A save is in flight: its own ErrConflict guard covers the same
	// conflict, so the notification must not double-report.
	m.saving = true
	m, cmd := press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte("new\n")}})
	if m.showChangeConflict {
		t.Fatal("a change during save must not open the conflict modal")
	}
	if cmd == nil {
		t.Fatal("the watch must re-arm during save")
	}
}

func TestExternalChange_ImportedFileIdentifiedExactly(t *testing.T) {
	fs := map[string]string{
		"Caddyfile":   "example.test {\n\timport common.conf\n}\n",
		"common.conf": "# v1\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)
	if len(mon.targets) != 2 {
		t.Fatalf("monitor targets = %v, want root + imported document", mon.targets)
	}

	fs["common.conf"] = "# v2\n"
	m, _ = press(m, externalChangeMsg{change: app.ExternalChange{Path: "common.conf", OnDisk: []byte("# v2\n")}})
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open for the imported file")
	}
	if m.pendingChange == nil || m.pendingChange.change.Path != "common.conf" {
		t.Fatalf("pending change = %+v, want the imported file", m.pendingChange)
	}
	// The compare diff names the exact file that changed. Compare is
	// offered with unsaved edits; make the imported file's divergence
	// part of the conflict by retaining an unsaved working copy.
	m.workingBytes = []byte("edited root\n")
	m.workingValidated = true
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if !strings.Contains(m.diffTitle, "common.conf") {
		t.Errorf("compare title = %q, want the imported path", m.diffTitle)
	}
}

func TestExternalChange_MissingFileReloadSurfacesGraphError(t *testing.T) {
	fs := map[string]string{
		"Caddyfile":   "example.test {\n\timport common.conf\n}\n",
		"common.conf": "# v1\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)

	// The imported file disappears on disk.
	delete(fs, "common.conf")
	m, _ = press(m, externalChangeMsg{change: app.ExternalChange{Path: "common.conf", Missing: true}})
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open for a missing file")
	}

	// Reload re-reads the graph; the missing import is surfaced in the
	// graph error and the raw view stays available.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.showChangeConflict {
		t.Fatal("reload must close the conflict modal")
	}
	if m.state.Graph == nil || m.state.Graph.Err == nil {
		t.Fatal("reload must surface the missing import error in the graph")
	}
}

func TestExternalChange_InitArmsWatchAndResolveReArms(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte("old\n"), nil })
	mon := newFakeMonitor()
	m := newLoadedModel(t, fakeLoader{state: state}, mon)

	// Init arms the watch; the cmd blocks on the monitor and delivers
	// the pre-loaded change as an externalChangeMsg.
	mon.nextCh <- monitorResult{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte("new\n")}}
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init must arm the watch")
	}
	msg, ok := cmd().(externalChangeMsg)
	if !ok || msg.err != nil || msg.change.Path != "Caddyfile" {
		t.Fatalf("Init watch cmd = %#v, want the queued change", msg)
	}

	// Delivering it opens the modal; keep re-arms the watch, whose cmd
	// then returns the next queued change.
	m, _ = press(m, msg)
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open")
	}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if cmd == nil {
		t.Fatal("keep must re-arm the watch")
	}
	mon.nextCh <- monitorResult{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte("newer\n")}}
	next, ok := cmd().(externalChangeMsg)
	if !ok || next.change.OnDisk == nil || string(next.change.OnDisk) != "newer\n" {
		t.Fatalf("re-armed watch cmd = %#v, want the next queued change", next)
	}
}

func TestExternalChange_MonitorUpdateErrorDisablesFeature(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte("x\n"), nil })
	mon := newFakeMonitor()
	mon.updateErr = errors.New("watch denied")
	m := newLoadedModel(t, fakeLoader{state: state}, mon)
	if m.monitor != nil {
		t.Fatal("monitor must be disabled after an Update error")
	}
	if !strings.Contains(m.statusMessage, "watching unavailable") {
		t.Errorf("statusMessage = %q, want an unavailable notification", m.statusMessage)
	}
}

func TestExternalChange_MonitorErrorShowsStatus(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte("x\n"), nil })
	mon := newFakeMonitor()
	m := newLoadedModel(t, fakeLoader{state: state}, mon)

	m, cmd := press(m, externalChangeMsg{err: errors.New("watcher exploded")})
	if m.monitor != nil {
		t.Fatal("monitor must be detached after a watcher error")
	}
	if cmd != nil {
		t.Fatal("no watch may re-arm after a watcher error")
	}
	if !strings.Contains(m.statusMessage, "watching failed") {
		t.Errorf("statusMessage = %q, want a failure notification", m.statusMessage)
	}
}

func TestExternalChange_ClosedStopsWatching(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte("x\n"), nil })
	mon := newFakeMonitor()
	m := newLoadedModel(t, fakeLoader{state: state}, mon)

	m, cmd := press(m, externalChangeMsg{err: app.ErrChangeClosed})
	if cmd != nil {
		t.Fatal("no watch may re-arm after the monitor closed")
	}
	if m.monitor != nil {
		t.Fatal("closed monitor must be detached")
	}
	if m.showChangeConflict {
		t.Fatal("a closed monitor must not open the conflict modal")
	}
}

func TestExternalChange_ViewFits(t *testing.T) {
	fs := map[string]string{"Caddyfile": "old {\n}\n"}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)

	m.workingBytes = []byte("edited {\n}\n") // unsaved edits: full options row
	m, _ = press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte("old {\n}\n# extra\n")}})
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open")
	}
	assertFits(t, m, 80, 24)
}

// ansiRe matches ANSI escape sequences emitted by lipgloss styles.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// stripANSI removes ANSI escape sequences so assertions can match the
// visible text of a rendered view. It is a no-op when the environment does
// not emit ANSI (non-TTY test runs).
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func TestModelRendersDocumentTree(t *testing.T) {
	fs := map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": "a.example.test {\n\trespond ok\n}\n",
	}
	state := stateFor(t, "config/Caddyfile", fsReader(fs))
	m := newLoadedModel(t, fakeLoader{state: state})

	view := m.View()
	for _, want := range []string{
		"READ-ONLY",
		"config/Caddyfile", // header path
		"Caddyfile",        // root document row
		"a.caddy",          // imported document row
		"a.example.test",   // site block row
	} {
		if !strings.Contains(view, want) {
			t.Errorf("View missing %q:\n%s", want, view)
		}
	}
	// The source pane shows the selected document: the root at first, and
	// the imported file once the cursor moves to it.
	if !strings.Contains(stripANSI(view), "import sites/a.caddy") {
		t.Errorf("View missing the root source text")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(stripANSI(m.View()), "respond ok") {
		t.Errorf("View missing the imported file's raw source text")
	}
}

func TestModelNavigation(t *testing.T) {
	fs := map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n",
	}
	state := stateFor(t, "config/Caddyfile", fsReader(fs))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30) // wide window so the source pane title fits on one line

	// Items: root doc, a.example.test, b.example.test.
	if len(m.items) != 3 {
		t.Fatalf("items = %d, want 3", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2 after two moves", m.cursor)
	}
	// The selected site is reflected in the source pane header.
	if !strings.Contains(m.View(), "b.example.test (lines 4-6)") {
		t.Errorf("source pane header missing b.example.test selection")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1 after up", m.cursor)
	}
	if !strings.Contains(m.View(), "a.example.test (lines 1-3)") {
		t.Errorf("source pane header missing a.example.test selection")
	}
	// Moving past the ends clamps.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want clamped 0", m.cursor)
	}
}

func TestModelCollapseDocument(t *testing.T) {
	fs := map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\n",
	}
	state := stateFor(t, "config/Caddyfile", fsReader(fs))
	m := newLoadedModel(t, fakeLoader{state: state})
	if len(m.items) != 2 {
		t.Fatalf("items = %d, want 2 before collapse", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.items) != 1 {
		t.Errorf("items = %d, want 1 after collapsing the root document", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.items) != 2 {
		t.Errorf("items = %d, want 2 after expanding again", len(m.items))
	}
}

func TestModelParseErrorKeepsRawView(t *testing.T) {
	// Malformed Caddyfile: unclosed site block plus an unknown directive.
	src := "example.test {\n\tcustom_plugin_directive \"keep this raw\"\n"
	readFile := func(p string) ([]byte, error) { return []byte(src), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	if state.Graph.Err == nil {
		t.Fatal("fixture must be malformed")
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30) // wide window so source lines are not truncated

	view := m.View()
	if !strings.Contains(view, "PARSE ERROR") {
		t.Errorf("View missing the PARSE ERROR marker")
	}
	// The raw source view stays available, preserving the unknown directive
	// and the malformed region byte-for-byte. ANSI styling is injected
	// between tokens, so match against the stripped visible text.
	visible := stripANSI(view)
	for _, want := range []string{
		"custom_plugin_directive",
		`"keep this raw"`,
		"example.test {",
	} {
		if !strings.Contains(visible, want) {
			t.Errorf("View missing raw source %q:\n%s", want, view)
		}
	}
	if !strings.Contains(visible, "1│") || !strings.Contains(visible, "2│") {
		t.Errorf("View missing line numbers:\n%s", view)
	}
}

func TestModelUnknownDirectivePreserved(t *testing.T) {
	src := "example.test {\n\tcustom_plugin_directive \"keep this raw\"\n}\n"
	readFile := func(p string) ([]byte, error) { return []byte(src), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30) // wide window so source lines are not truncated
	view := m.View()
	if !strings.Contains(stripANSI(view), "custom_plugin_directive \"keep this raw\"") {
		t.Errorf("unknown directive not preserved in the view:\n%s", view)
	}
}

// TestModelQuit verifies that q with no unsaved edits quits immediately
// (the unsaved guard only intercepts genuine exit requests when edits are
// pending — see the quit-guard tests in quit_guard_test.go).
func TestModelQuit(t *testing.T) {
	readFile := func(p string) ([]byte, error) { return []byte("example.test {\n}\n"), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = updated.(*Model)
	if !m.quit {
		t.Errorf("quit = false, want true after q")
	}
	if m.showUnsavedConfirm {
		t.Error("unsaved prompt opened without unsaved edits")
	}
	if cmd == nil || cmd() == nil {
		t.Errorf("expected tea.Quit command")
	}
}

func TestModelReadErrorShowsMessage(t *testing.T) {
	m := New(fakeLoader{state: nil, err: &noSuchFile{path: "missing/Caddyfile"}}, nil, nil, nil, nil, nil, nil, nil, testVersion, nil, nil, nil)
	if err := m.Load(); err == nil {
		t.Fatal("Load must return the read error")
	}
	view := m.View()
	if !strings.Contains(view, "missing/Caddyfile") {
		t.Errorf("View missing the read error:\n%s", view)
	}
	if !strings.Contains(view, "Documents (unavailable)") {
		t.Errorf("View missing the unavailable-documents state:\n%s", view)
	}
}

func TestModelSourceScrollsWithViewport(t *testing.T) {
	var src strings.Builder
	src.WriteString("example.test {\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&src, "\t# line %d\n", i)
	}
	src.WriteString("}\n")
	readFile := func(p string) ([]byte, error) { return []byte(src.String()), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 12) // short window: the source overflows the pane

	if m.viewport.YOffset != 0 {
		t.Errorf("YOffset = %d, want 0 at the top", m.viewport.YOffset)
	}
	view := m.View()
	if !strings.Contains(view, "# line 0") {
		t.Errorf("top of source not visible:\n%s", view)
	}
	if strings.Contains(view, "line 39") {
		t.Errorf("bottom of source visible before scrolling (viewport must truncate)")
	}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.viewport.YOffset == 0 {
		t.Errorf("YOffset = 0 after PgDown, want scroll")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.viewport.YOffset != 0 {
		t.Errorf("YOffset = %d after PgUp, want back to top", m.viewport.YOffset)
	}

	// Scrolling down makes later lines visible.
	for i := 0; i < 20 && !m.viewport.AtBottom(); i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if !strings.Contains(m.View(), "line 39") {
		t.Errorf("bottom of source not reachable after scrolling")
	}

	// Selecting a different item jumps the viewport to that item's start
	// line (applied when the view is rendered, on the next View call).
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.viewport.YOffset == 0 {
		t.Fatalf("precondition: scroll must be active")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // to the site row
	m.View()                                          // render applies the jump
	if m.viewport.YOffset != 0 {
		t.Errorf("YOffset = %d after selection change, want the node start (line 1)", m.viewport.YOffset)
	}
}

func TestModelSelectionJumpsToNodeStartLine(t *testing.T) {
	var src strings.Builder
	src.WriteString("example.test {\n\trespond ok\n}\n")
	for i := 0; i < 70; i++ {
		src.WriteString("# padding\n")
	}
	src.WriteString("pbs.example.test {\n\trespond pbs\n}\n")
	readFile := func(p string) ([]byte, error) { return []byte(src.String()), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 12)

	// Items: root doc, example.test (line 1), pbs.example.test (line 74).
	if len(m.items) != 3 {
		t.Fatalf("items = %d, want 3", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // pbs.example.test
	m.View()

	// Selecting a node below the viewport scrolls just enough to reveal it.
	if m.viewport.YOffset == 0 {
		t.Errorf("YOffset = 0, want a reveal of pbs.example.test (line 74)")
	}
	view := m.View()
	visible := stripANSI(view)
	if !strings.Contains(visible, "pbs.example.test {") {
		t.Errorf("selected block not visible after the reveal:\n%s", view)
	}
	if strings.Contains(visible, "respond ok") {
		t.Errorf("document start still visible after the reveal (viewport must be scrolled down)")
	}
}

func TestModelRevealKeepsPositionWhenVisible(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	readFile := func(p string) ([]byte, error) { return []byte(src), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 20) // viewport is 13 lines tall: both sites are visible

	// Items: root doc, a.example.test (lines 1-3), b.example.test (lines 4-6).
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m.View()
	if m.viewport.YOffset != 0 {
		t.Fatalf("precondition: YOffset = %d, want 0", m.viewport.YOffset)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test, already visible
	m.View()
	if m.viewport.YOffset != 0 {
		t.Errorf("YOffset = %d, want unchanged because the block is already visible", m.viewport.YOffset)
	}
}

func TestModelSourcePaneMarksSelectedNodeRange(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	readFile := func(p string) ([]byte, error) { return []byte(src), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 20)

	// Select b.example.test (lines 4-6).
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test
	view := m.View()

	content := stripANSI(m.viewport.View())
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lineNo := i + 1
		hasBar := strings.Contains(line, "▎")
		switch {
		case lineNo >= 4 && lineNo <= 6:
			if !hasBar {
				t.Errorf("line %d (selected b.example.test) missing the selection bar:\n%s", lineNo, line)
			}
		default:
			if hasBar {
				t.Errorf("line %d (outside selected range) must not contain the selection bar:\n%s", lineNo, line)
			}
		}
	}
	// The full view must still show both site blocks.
	for _, want := range []string{"a.example.test", "b.example.test"} {
		if !strings.Contains(stripANSI(view), want) {
			t.Errorf("source view missing %q", want)
		}
	}
}

func TestModelRevealScrollsBackUpWhenBlockAbove(t *testing.T) {
	var src strings.Builder
	src.WriteString("example.test {\n\trespond ok\n}\n")
	for i := 0; i < 70; i++ {
		src.WriteString("# padding\n")
	}
	src.WriteString("pbs.example.test {\n\trespond pbs\n}\n")
	readFile := func(p string) ([]byte, error) { return []byte(src.String()), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 12)

	// Render once so the viewport content is loaded, then scroll to the
	// bottom of the source and select the first site.
	m.View()
	for i := 0; i < 20 && !m.viewport.AtBottom(); i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if m.viewport.YOffset == 0 {
		t.Fatalf("precondition: viewport must be scrolled down")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // example.test
	m.View()
	if m.viewport.YOffset != 0 {
		t.Errorf("YOffset = %d, want the block above the viewport revealed at the top", m.viewport.YOffset)
	}
	if !strings.Contains(stripANSI(m.View()), "respond ok") {
		t.Errorf("first site not visible after scrolling back up")
	}
}

func TestModelLayoutFitsWindowWidth(t *testing.T) {
	var src strings.Builder
	src.WriteString("example.test {\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&src, "\treverse_proxy upstream-%d.example.test:8080 {\n\t\theader_up Host {host}\n\t}\n", i)
	}
	src.WriteString("}\n")
	readFile := func(p string) ([]byte, error) { return []byte(src.String()), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 100, 20)

	view := m.View()
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(stripANSI(line)); w > 100 {
			t.Errorf("rendered line %d is %d columns wide, exceeds the 100-column window:\n%s", i+1, w, line)
		}
	}
	// The right border of the source pane must be visible on the same row as
	// the tree pane's border (i.e. the two panes fit side by side).
	if !strings.Contains(view, "╮") || !strings.Contains(view, "╯") {
		t.Errorf("missing pane borders in the rendered view")
	}
}

func TestModelManualScrollNotOverriddenByReveal(t *testing.T) {
	var src strings.Builder
	src.WriteString("example.test {\n\trespond ok\n}\n")
	for i := 0; i < 70; i++ {
		src.WriteString("# padding\n")
	}
	src.WriteString("pbs.example.test {\n\trespond pbs\n}\n")
	readFile := func(p string) ([]byte, error) { return []byte(src.String()), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 12)

	// Select pbs.example.test: the reveal scrolls the viewport to it.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // pbs.example.test
	m.View()
	revealed := m.viewport.YOffset
	if revealed == 0 {
		t.Fatalf("precondition: reveal must scroll to pbs.example.test")
	}

	// PgUp must be able to move the viewport above the selected site and all
	// the way back to the top, and a render must not snap it back to the
	// selection.
	for i := 0; i < 20 && !m.viewport.AtTop(); i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
		m.View() // a render must not override the manual scroll
	}
	if m.viewport.YOffset != 0 {
		t.Errorf("YOffset = %d, want 0 after scrolling up past the selection", m.viewport.YOffset)
	}
	if !strings.Contains(m.View(), "respond ok") {
		t.Errorf("top of source not visible after scrolling up")
	}
}

// TestModelShowsGlobalOptions verifies the top-level global options block
// (`{ ... }`) appears in the document tree as a selectable depth-1 row with
// a fixed label, alongside the site blocks.
func TestModelShowsGlobalOptions(t *testing.T) {
	src := "{\n\temail admin@example.test\n}\n\nexample.test {\n\trespond ok\n}\n"
	readFile := func(p string) ([]byte, error) { return []byte(src), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	// Items: root doc, global options, example.test.
	if len(m.items) != 3 {
		t.Fatalf("items = %d, want 3 (root + global options + site)", len(m.items))
	}
	var globalItem, siteItem *item
	for i := range m.items {
		it := &m.items[i]
		if it.label == "global options" && it.depth == 1 && it.hasNode {
			globalItem = it
		}
		if it.label == "example.test" && it.depth == 1 && it.hasNode {
			siteItem = it
		}
	}
	if globalItem == nil {
		t.Error("tree missing the 'global options' depth-1 row")
	}
	if siteItem == nil {
		t.Error("tree missing the example.test site row")
	}

	// Selecting the global-options row reveals its block in the source pane.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // global options
	view := m.View()
	visible := stripANSI(view)
	if !strings.Contains(visible, "global options") {
		t.Errorf("view missing the global options label:\n%s", visible)
	}
	if !strings.Contains(visible, "email admin@example.test") {
		t.Errorf("source pane missing the global options content:\n%s", visible)
	}
}

// TestModelReturnToDocumentRowScrollsHome verifies that moving the cursor
// back up to a document row (depth 0) resets the source viewport to the
// top, instead of keeping the stale reveal of the previously selected node.
func TestModelReturnToDocumentRowScrollsHome(t *testing.T) {
	var src strings.Builder
	src.WriteString("example.test {\n\trespond ok\n}\n")
	for i := 0; i < 70; i++ {
		src.WriteString("# padding\n")
	}
	src.WriteString("pbs.example.test {\n\trespond pbs\n}\n")
	readFile := func(p string) ([]byte, error) { return []byte(src.String()), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 12)

	// Items: root doc, example.test (line 1), pbs.example.test (line 74).
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // pbs.example.test
	m.View()
	if m.viewport.YOffset == 0 {
		t.Fatalf("precondition: reveal must scroll to pbs.example.test")
	}

	// Move back up to the root document row: the source must reset home.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp}) // example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp}) // root doc
	m.View()
	if m.viewport.YOffset != 0 {
		t.Errorf("YOffset = %d after returning to the document row, want 0 (home)", m.viewport.YOffset)
	}
	if !strings.Contains(stripANSI(m.View()), "respond ok") {
		t.Errorf("top of source not visible after returning home:\n%s", m.View())
	}
}

func TestModelPageKeysScrollFullPage(t *testing.T) {
	src := "example.test {\n\trespond ok\n}\n"
	for i := 0; i < 70; i++ {
		src += "# padding\n"
	}
	readFile := func(p string) ([]byte, error) { return []byte(src), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 12)
	m.View() // load the viewport content

	before := m.viewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	// A full page is Height lines (the document row has no reveal target, so
	// the scroll comes only from the key).
	if got := m.viewport.YOffset - before; got != m.viewport.Height {
		t.Errorf("PgDown moved %d lines, want a full page (%d)", got, m.viewport.Height)
	}
}

func TestModelNoWriteOperations(t *testing.T) {
	// The loader is the only I/O path and it only reads: feed a loader that
	// records calls and assert the model never asks for anything but the
	// configured Caddyfile.
	calls := map[string]int{}
	readFile := func(p string) ([]byte, error) {
		calls[p]++
		if p == "config/Caddyfile" {
			return []byte("example.test {\n}\n"), nil
		}
		return nil, &noSuchFile{p}
	}
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	m.View()
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m.View()
	if len(calls) != 1 {
		t.Errorf("file reads = %v, want only the config read", calls)
	}
}

func TestModelFormatAndValidate_DisabledWithoutGraph(t *testing.T) {
	formatter := &fakeFormatter{formatted: []byte("x")}
	m := New(fakeLoader{state: nil, err: &noSuchFile{path: "missing"}}, formatter, nil, nil, nil, nil, nil, nil, testVersion, nil, nil, nil)
	m.Load()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd != nil {
		t.Errorf("expected nil cmd when no state, got %v", cmd)
	}
	if formatter.calls != 0 {
		t.Errorf("formatter.calls = %d, want 0 when no state is loaded", formatter.calls)
	}
}

func TestModelFormatAndValidate_NoFormatterShowsHint(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}) // no formatter
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd != nil {
		t.Errorf("expected nil cmd when formatter is nil, got %v", cmd)
	}
	if !strings.Contains(m.statusMessage, "caddy binary not configured") {
		t.Errorf("statusMessage = %q, want hint about caddy binary", m.statusMessage)
	}
	if m.busy {
		t.Error("busy = true, want false when formatter is nil")
	}
}

func TestModelFormatAndValidate_InvokesFormatter(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("formatted")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd == nil {
		t.Fatal("expected tea.Cmd from v keypress")
	}
	if !m.busy {
		t.Error("busy = false, want true while invocation is in flight")
	}
	msg := cmd()
	result, ok := msg.(formatAndValidateResultMsg)
	if !ok {
		t.Fatalf("got %T, want formatAndValidateResultMsg", msg)
	}
	if string(result.Formatted) != "formatted" {
		t.Errorf("Formatted = %q, want formatted", result.Formatted)
	}
	if formatter.calls != 1 {
		t.Errorf("formatter.calls = %d, want 1", formatter.calls)
	}
	if formatter.capturedDisplayPath != "config/Caddyfile" {
		t.Errorf("displayPath = %q, want config/Caddyfile (real path must be surfaced, not a temp path)", formatter.capturedDisplayPath)
	}
}

func TestModelFormatAndValidate_SuccessStoresWorkingCopy(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("formatted")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	msg := cmd()
	m.Update(msg) // process the result
	if string(m.workingBytes) != "formatted" {
		t.Errorf("workingBytes = %q, want formatted", m.workingBytes)
	}
	if !strings.Contains(m.statusMessage, "validated") {
		t.Errorf("statusMessage = %q, want it to mention 'validated'", m.statusMessage)
	}
	if !strings.HasPrefix(m.statusMessage, "✓") {
		t.Errorf("statusMessage = %q, want it to start with the success glyph", m.statusMessage)
	}
	if m.showDiagnostics {
		t.Error("showDiagnostics = true, want false on success")
	}
	if m.busy {
		t.Error("busy = true, want false after result delivery")
	}
}

func TestModelFormatAndValidate_FailureShowsModal(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{
		formatted:   []byte("formatted working copy"),
		diagnostics: diags,
		err:         errors.New("caddy exit 1"),
	}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	msg := cmd()
	m.Update(msg) // process the result
	if !m.showDiagnostics {
		t.Fatal("showDiagnostics = false, want true on validation failure with diagnostics")
	}
	if len(m.diagnostics) != 1 {
		t.Fatalf("len(diagnostics) = %d, want 1", len(m.diagnostics))
	}
	if m.diagCursor != 0 {
		t.Errorf("diagCursor = %d, want 0 on open", m.diagCursor)
	}
	if string(m.workingBytes) != "formatted working copy" {
		t.Errorf("workingBytes = %q, want formatted working copy", m.workingBytes)
	}
	if !strings.Contains(m.statusMessage, "validation failed") {
		t.Errorf("statusMessage = %q, want validation failure state", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "working copy retained") {
		t.Errorf("statusMessage = %q, want retained working copy state", m.statusMessage)
	}
	view := m.View()
	for _, want := range []string{"Validation", "boom", "config/Caddyfile:1:1"} {
		if !strings.Contains(view, want) {
			t.Errorf("View missing %q:\n%s", want, view)
		}
	}
}

func TestModelFormatAndValidate_FailureEmptyDiags(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	formatter := &fakeFormatter{err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	msg := cmd()
	m.Update(msg)
	if m.showDiagnostics {
		t.Error("showDiagnostics = true, want false when no diagnostics were parsed")
	}
	if !strings.HasPrefix(m.statusMessage, "✗") {
		t.Errorf("statusMessage = %q, want it to start with the error glyph", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "caddy exit 1") {
		t.Errorf("statusMessage = %q, want it to include 'caddy exit 1'", m.statusMessage)
	}
}

func TestModelFormatAndValidate_DiagnosticsModalNavigation(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "p", Line: 1, Message: "first", Severity: validator.SeverityError},
		{Path: "p", Line: 2, Message: "second", Severity: validator.SeverityError},
		{Path: "p", Line: 3, Message: "third", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd()) // open the modal
	if !m.showDiagnostics {
		t.Fatal("modal not open after result delivery")
	}
	if m.diagCursor != 0 {
		t.Fatalf("diagCursor = %d, want 0 on open", m.diagCursor)
	}
	// j moves down
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.diagCursor != 1 {
		t.Errorf("diagCursor = %d, want 1 after j", m.diagCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.diagCursor != 2 {
		t.Errorf("diagCursor = %d, want 2 after second j", m.diagCursor)
	}
	// Clamp at the end
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.diagCursor != 2 {
		t.Errorf("diagCursor = %d, want 2 (clamped at end)", m.diagCursor)
	}
	// k moves up
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.diagCursor != 1 {
		t.Errorf("diagCursor = %d, want 1 after k", m.diagCursor)
	}
	// Arrow keys also work
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.diagCursor != 2 {
		t.Errorf("diagCursor = %d, want 2 after KeyDown", m.diagCursor)
	}
	// Esc closes
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showDiagnostics {
		t.Error("showDiagnostics = true, want false after Esc")
	}
	if len(m.diagnostics) != 0 {
		t.Errorf("diagnostics not cleared after Esc: %v", m.diagnostics)
	}
	if !strings.Contains(m.statusMessage, "validation failed") {
		t.Errorf("statusMessage = %q, want validation failure state after closing modal", m.statusMessage)
	}
}

func TestModelFormatAndValidate_BusyIsIgnored(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("formatted")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	// First v starts the invocation.
	_, cmd1 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if !m.busy {
		t.Error("busy = false after first v, want true")
	}
	if cmd1 == nil {
		t.Fatal("first v must return a tea.Cmd")
	}
	// Second v while busy is a no-op.
	_, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd2 != nil {
		t.Error("second v must return nil cmd while busy")
	}
	if formatter.calls != 0 {
		t.Errorf("formatter.calls = %d before cmd1() executes, want 0", formatter.calls)
	}
	cmd1() // execute the first invocation
	if formatter.calls != 1 {
		t.Errorf("formatter.calls = %d, want exactly 1 (second v must not have triggered a call)", formatter.calls)
	}
}

func TestModelFormatAndValidate_NoExtraReads(t *testing.T) {
	// The v keypress must not touch the filesystem: format+validate is
	// an in-process call against the Formatter, the loader is the only
	// I/O path and it only reads.
	calls := map[string]int{}
	readFile := func(p string) ([]byte, error) {
		calls[p]++
		if p == "config/Caddyfile" {
			return []byte("example.test {\n}\n"), nil
		}
		return nil, &noSuchFile{p}
	}
	state := stateFor(t, "config/Caddyfile", readFile)
	formatter := &fakeFormatter{formatted: []byte("formatted")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m.View()
	beforeReads := calls["config/Caddyfile"]
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.View()
	m.Update(cmd())
	m.View()
	if got := calls["config/Caddyfile"] - beforeReads; got != 0 {
		t.Errorf("file reads triggered by v = %d, want 0 (no-write contract violated)", got)
	}
}

// TestModelFormatAndValidate_ZeroTimeoutDoesNotCancelContext verifies
// that the cmd wraps the formatter call in context.Background() when
// the operator did not pass --validator-timeout. Passing a zero
// duration to context.WithTimeout returns a context that is already
// past its deadline and would cancel the validator immediately,
// preventing its own 5s default from ever firing.
func TestModelFormatAndValidate_ZeroTimeoutDoesNotCancelContext(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("x")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	// m.validatorTimeout is the zero value because
	// Settings.ValidatorTimeout was not set.
	if m.validatorTimeout != 0 {
		t.Fatalf("precondition: m.validatorTimeout = %s, want 0", m.validatorTimeout)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd == nil {
		t.Fatal("expected tea.Cmd from v keypress")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("expected message from cmd execution")
	}
	if formatter.capturedCtx == nil {
		t.Fatal("formatter did not capture context")
	}
	if err := formatter.capturedCtx.Err(); err != nil {
		t.Errorf("captured ctx is canceled (%v); zero ValidatorTimeout must leave the context un-canceled so the validator package can apply its own 5s default", err)
	}
}

// TestModelFormatAndValidate_InfoDiagnosticsFilteredOut verifies that
// the modal only surfaces error-level diagnostics. Caddy's validate
// output includes info-level log lines (e.g. "using config from
// file") that are not actionable and would otherwise clutter the
// modal. The handler filters to SeverityError before opening it.
func TestModelFormatAndValidate_InfoDiagnosticsFilteredOut(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "info noise", Severity: validator.SeverityInfo},
		{Path: "config/Caddyfile", Line: 47, Message: "module not registered", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	if !m.showDiagnostics {
		t.Fatal("expected modal open (error diagnostic present)")
	}
	if len(m.diagnostics) != 1 {
		t.Fatalf("len(m.diagnostics) = %d, want 1 (info must be filtered out)", len(m.diagnostics))
	}
	if m.diagnostics[0].Severity != validator.SeverityError {
		t.Errorf("filtered diagnostic severity = %v, want error", m.diagnostics[0].Severity)
	}
	if m.diagnostics[0].Line != 47 {
		t.Errorf("filtered diagnostic line = %d, want 47", m.diagnostics[0].Line)
	}
	view := m.View()
	if strings.Contains(view, "info noise") {
		t.Errorf("View should not contain info diagnostic, but does:\n%s", view)
	}
	if !strings.Contains(view, "module not registered") {
		t.Errorf("View missing error diagnostic:\n%s", view)
	}
}

// TestModelFormatAndValidate_AllInfoShowsStatusNotModal verifies the
// edge case where every diagnostic is info-level: the modal must not
// open (it would be empty) and the underlying error must surface in
// the status line instead.
func TestModelFormatAndValidate_AllInfoShowsStatusNotModal(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "noise 1", Severity: validator.SeverityInfo},
		{Path: "config/Caddyfile", Line: 2, Message: "noise 2", Severity: validator.SeverityInfo},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	if m.showDiagnostics {
		t.Error("showDiagnostics = true, want false when no errors after filtering")
	}
	if !strings.Contains(m.statusMessage, "✗") {
		t.Errorf("statusMessage = %q, want error status when all diags are info", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "caddy exit 1") {
		t.Errorf("statusMessage = %q, want it to include the underlying error", m.statusMessage)
	}
}

// TestModelDiagnosticsView_LongMessageTruncated verifies that an
// over-long diagnostic message is truncated to fit the modal
// width. Without truncation the body line would push past the
// right border, breaking the layout.
func TestModelDiagnosticsView_LongMessageTruncated(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	longMsg := strings.Repeat("a", 200)
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: longMsg, Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 60, 20) // narrow window
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	view := m.View()
	if !strings.Contains(view, "…") {
		t.Errorf("expected the long message to be truncated with '…', view:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(stripANSI(line)); w > 60 {
			t.Errorf("rendered line %d is %d columns wide, exceeds the 60-column window:\n%s", i+1, w, line)
		}
	}
}

// TestModelDiagnosticsDetail_EnterOpensDetail covers the primary
// keybinding for the detail view: pressing Enter on a diagnostic
// in the list opens its detail, which shows path, line, severity
// and the full message (no truncation).
func TestModelDiagnosticsDetail_EnterOpensDetail(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 47, Column: 1, Message: "module not registered: dns.providers.cloudflare", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	if !m.showDiagnostics {
		t.Fatal("modal must be open before opening detail")
	}
	if m.showDetail {
		t.Fatal("detail must not be open initially")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.showDetail {
		t.Error("showDetail = false after Enter, want true")
	}
	view := m.View()
	for _, want := range []string{
		"config/Caddyfile",
		"47",
		"module not registered: dns.providers.cloudflare",
		"error",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("View missing %q, got:\n%s", want, view)
		}
	}
}

// TestModelDiagnosticsDetail_PlusOpensDetail covers the '+' alias
// for Enter. It must open the detail view from the list and stay a
// no-op outside the diagnostics modal.
func TestModelDiagnosticsDetail_PlusOpensDetail(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	if !m.showDetail {
		t.Error("showDetail = false after '+', want true ('+' is an alias for Enter)")
	}
}

// TestModelDiagnosticsDetail_EscReturnsToList verifies the first
// half of the Esc chain: from the detail view, Esc closes the
// detail but keeps the diagnostics modal open.
func TestModelDiagnosticsDetail_EscReturnsToList(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // open detail
	if !m.showDetail {
		t.Fatal("detail should be open after Enter")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // back to list
	if m.showDetail {
		t.Error("showDetail = true after Esc from detail, want false")
	}
	if !m.showDiagnostics {
		t.Error("showDiagnostics = false after Esc from detail, want true (modal stays open)")
	}
}

// TestModelDiagnosticsDetail_EscClosesModal covers the second
// half of the Esc chain: from the list, Esc closes the modal
// entirely.
func TestModelDiagnosticsDetail_EscClosesModal(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // Esc from list
	if m.showDiagnostics {
		t.Error("showDiagnostics = true after Esc from list, want false")
	}
	if m.showDetail {
		t.Error("showDetail = true after Esc from list, want false")
	}
}

// TestModelDiagnosticsDetail_LongMessageWraps verifies that a long
// diagnostic message is wrapped to the available width in the
// detail view. No rendered line may exceed the window width, and
// the full message must remain visible (not truncated to '…').
func TestModelDiagnosticsDetail_LongMessageWraps(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	longMsg := strings.TrimRight(strings.Repeat("word ", 40), " ")
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: longMsg, Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 60, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	view := m.View()
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(stripANSI(line)); w > 60 {
			t.Errorf("rendered line %d is %d columns wide, exceeds the 60-column window:\n%s", i+1, w, line)
		}
	}
	if !strings.Contains(view, "word") {
		t.Errorf("View missing the message content, got:\n%s", view)
	}
	// The detail must not truncate the message with '…': the full
	// 200-char message should be visible in the body, even if it
	// requires scrolling to read it.
	if !strings.Contains(view, strings.Repeat("word ", 10)) {
		t.Errorf("View should show a long stretch of the message, got:\n%s", view)
	}
}

// TestModelDiagnosticsDetail_PgUpPgDownScroll verifies the page
// keys advance and retreat the detail viewport scroll.
func TestModelDiagnosticsDetail_PgUpPgDownScroll(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	longMsg := strings.TrimRight(strings.Repeat("lorem ipsum dolor sit amet ", 30), " ")
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: longMsg, Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 60, 12) // short window so the body overflows
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	initialY := m.detailViewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.detailViewport.YOffset <= initialY {
		t.Errorf("PgDown did not advance scroll: initial=%d, after=%d", initialY, m.detailViewport.YOffset)
	}
	afterPgDown := m.detailViewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.detailViewport.YOffset >= afterPgDown {
		t.Errorf("PgUp did not retreat scroll: afterPgDown=%d, after=%d", afterPgDown, m.detailViewport.YOffset)
	}
}

// TestModelDiagnosticsDetail_ArrowKeysScroll verifies that the
// arrow keys also scroll the detail viewport (line-by-line,
// independent of PgUp/PgDown).
func TestModelDiagnosticsDetail_ArrowKeysScroll(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	longMsg := strings.TrimRight(strings.Repeat("alpha beta gamma ", 30), " ")
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: longMsg, Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 60, 12)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	initialY := m.detailViewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.detailViewport.YOffset <= initialY {
		t.Errorf("Down arrow did not advance scroll: initial=%d, after=%d", initialY, m.detailViewport.YOffset)
	}
	afterDown := m.detailViewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.detailViewport.YOffset >= afterDown {
		t.Errorf("Up arrow did not retreat scroll: afterDown=%d, after=%d", afterDown, m.detailViewport.YOffset)
	}
}

// TestModelDiagnosticsDetail_ListStillTruncates is a regression
// test for the compact list view: the detail view is additive
// only. The list must still show the truncated message with '…'
// and the detail must show strictly more of the same message.
func TestModelDiagnosticsDetail_ListStillTruncates(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	longMsg := strings.Repeat("a", 200)
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: longMsg, Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 60, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	listView := m.View()
	if !strings.Contains(listView, "…") {
		t.Errorf("list view should still truncate with '…', got:\n%s", listView)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	detailView := m.View()
	for _, want := range []string{"Path", "Line", "Severity"} {
		if !strings.Contains(detailView, want) {
			t.Errorf("detail view should show structured field %q, got:\n%s", want, detailView)
		}
	}
	// The detail body must contain strictly more 'a' characters
	// than the list body, since the list truncates the message
	// and the detail does not.
	listAs := strings.Count(listView, "a")
	detailAs := strings.Count(detailView, "a")
	if detailAs <= listAs {
		t.Errorf("detail view should show more of the message than the list: list=%d 'a's, detail=%d 'a's", listAs, detailAs)
	}
}

// TestWrapText_ShortReturnsUnchanged locks in the no-op behaviour
// for inputs that already fit the width.
func TestWrapText_ShortReturnsUnchanged(t *testing.T) {
	if got := wrapText("hello world", 20); got != "hello world" {
		t.Errorf("wrapText(%q, 20) = %q, want %q", "hello world", got, "hello world")
	}
}

// TestWrapText_WrapsOnWordBoundary verifies that long inputs are
// split at word boundaries and every line fits within the width.
func TestWrapText_WrapsOnWordBoundary(t *testing.T) {
	in := "the quick brown fox jumps over the lazy dog"
	got := wrapText(in, 15)
	for i, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w > 15 {
			t.Errorf("line %d %q is %d cells, exceeds 15", i, line, w)
		}
	}
}

// TestWrapText_HardBreaksLongWord verifies that a single word
// longer than the width is broken on rune boundaries so no line
// exceeds the width.
func TestWrapText_HardBreaksLongWord(t *testing.T) {
	in := "supercalifragilisticexpialidocious"
	got := wrapText(in, 10)
	for i, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w > 10 {
			t.Errorf("line %d %q is %d cells, exceeds 10", i, line, w)
		}
	}
	// All non-newline runes must be preserved across the hard
	// break (newlines are inserted by the wrap, so they are not
	// counted).
	gotStripped := strings.ReplaceAll(got, "\n", "")
	if gotRunes := len([]rune(gotStripped)); gotRunes != len([]rune(in)) {
		t.Errorf("hard break lost runes: got %d, want %d", gotRunes, len([]rune(in)))
	}
}

// TestWrapText_MultiByteSafe verifies that multi-byte runes are
// never split mid-codepoint. A single 30-rune word of 2-byte
// runes forces the hard-break path; if wrapText sliced a rune
// mid-codepoint, the rune count would drop.
func TestWrapText_MultiByteSafe(t *testing.T) {
	const total = 30
	in := strings.Repeat("é", total)
	got := wrapText(in, 10)
	for i, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(line); w > 10 {
			t.Errorf("line %d %q is %d cells, exceeds 10", i, line, w)
		}
	}
	// All non-newline runes must be preserved (newlines are inserted
	// by the wrap, so they are not counted).
	gotStripped := strings.ReplaceAll(got, "\n", "")
	if gotRunes := len([]rune(gotStripped)); gotRunes != total {
		t.Errorf("multi-byte wrap lost runes: got %d, want %d", gotRunes, total)
	}
}

// TestWrapText_ZeroOrNegativeWidthReturnsInput verifies that
// wrapText is a no-op for non-positive widths.
func TestWrapText_ZeroOrNegativeWidthReturnsInput(t *testing.T) {
	if got := wrapText("hello world", 0); got != "hello world" {
		t.Errorf("wrapText(hello world, 0) = %q, want %q", got, "hello world")
	}
	if got := wrapText("hello world", -1); got != "hello world" {
		t.Errorf("wrapText(hello world, -1) = %q, want %q", got, "hello world")
	}
}

// TestModelFooter_GlobalWhenModalClosed verifies that the global
// keymap is unchanged when no diagnostics modal is open.
func TestModelFooter_GlobalWhenModalClosed(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	view := stripANSI(m.View())
	for _, want := range []string{"v format & validate", "Enter toggle"} {
		if !strings.Contains(view, want) {
			t.Errorf("global footer should show %q, got:\n%s", want, view)
		}
	}
}

// TestModelFooter_ListContext verifies that the bottom footer shows
// the list keys (not the global keymap) while the diagnostics modal
// is open in list mode.
func TestModelFooter_ListContext(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	view := stripANSI(m.View())
	if !strings.Contains(view, "Enter/+ detail") {
		t.Errorf("list footer should show 'Enter/+ detail', got:\n%s", view)
	}
	if strings.Contains(view, "v format & validate") {
		t.Errorf("list footer must not show the global 'v format & validate' key, got:\n%s", view)
	}
}

// TestModelFooter_DetailContext verifies that the bottom footer shows
// the detail keys (not the global keymap) while the diagnostic detail
// view is open.
func TestModelFooter_DetailContext(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // open detail
	view := stripANSI(m.View())
	if !strings.Contains(view, "PgUp/PgDown") {
		t.Errorf("detail footer should show PgUp/PgDown, got:\n%s", view)
	}
	if strings.Contains(view, "v format & validate") {
		t.Errorf("detail footer must not show the global 'v format & validate' key, got:\n%s", view)
	}
	if strings.Contains(view, "Enter toggle") {
		t.Errorf("detail footer must not show the global 'Enter toggle' key, got:\n%s", view)
	}
}

// TestModelDiff_NoWorkingCopyShowsHint verifies that pressing D before
// a working copy exists surfaces a status hint instead of opening the
// modal.
func TestModelDiff_NoWorkingCopyShowsHint(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	if m.showDiff {
		t.Error("showDiff = true, want false before validation")
	}
	if !strings.Contains(m.statusMessage, "working copy") {
		t.Errorf("statusMessage = %q, want hint about working copy", m.statusMessage)
	}
}

// TestModelDiff_OpensModalAfterValidation verifies the happy path:
// validate with v, then open the diff with D and see diff markers in
// the rendered view.
func TestModelDiff_OpensModalAfterValidation(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	if !m.showDiff {
		t.Fatal("showDiff = false after D, want true")
	}
	view := m.View()
	hasMarker := strings.Contains(view, "+") || strings.Contains(view, "@@") || strings.Contains(view, "config/Caddyfile (formatted)")
	if !hasMarker {
		t.Errorf("View missing diff markers, got:\n%s", view)
	}
}

// TestModelDiff_IdenticalShowsNoChanges verifies that when the working
// copy matches the source the modal still opens but shows a "no
// changes" message instead of an empty viewport.
func TestModelDiff_IdenticalShowsNoChanges(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	if !m.showDiff {
		t.Fatal("showDiff = false after D, want true")
	}
	view := m.View()
	if !strings.Contains(view, "no changes") {
		t.Errorf("View missing 'no changes', got:\n%s", view)
	}
}

// TestModelDiff_EscCloses verifies that Esc dismisses the diff modal
// and that a second Esc does not quit the application.
func TestModelDiff_EscCloses(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	if !m.showDiff {
		t.Fatal("diff modal not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showDiff {
		t.Error("showDiff = true after Esc, want false")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*Model)
	if m.quit {
		t.Error("quit = true after second Esc")
	}
	if cmd != nil {
		t.Errorf("second Esc returned a command, want nil")
	}
}

// TestModelDiff_ScrollKeys verifies that PgDown advances the diff
// viewport and PgUp retreats it when the diff is taller than the
// short window body.
func TestModelDiff_ScrollKeys(t *testing.T) {
	var src strings.Builder
	var formatted strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&src, "line %d\n", i)
		fmt.Fprintf(&formatted, "changed %d\n", i)
	}
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src.String(),
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted.String())}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 60, 12) // short window so the diff body overflows
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	if !m.showDiff {
		t.Fatal("diff modal not open after D")
	}
	initialY := m.diffViewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.diffViewport.YOffset <= initialY {
		t.Errorf("PgDown did not advance scroll: initial=%d, after=%d", initialY, m.diffViewport.YOffset)
	}
	afterPgDown := m.diffViewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.diffViewport.YOffset >= afterPgDown {
		t.Errorf("PgUp did not retreat scroll: afterPgDown=%d, after=%d", afterPgDown, m.diffViewport.YOffset)
	}
}

// TestModelFooter_DiffContext verifies that the bottom footer shows
// the diff keys (not the global keymap) while the diff modal is open.
func TestModelFooter_DiffContext(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	view := stripANSI(m.View())
	if !strings.Contains(view, "Esc close") {
		t.Errorf("diff footer should show 'Esc close', got:\n%s", view)
	}
	if strings.Contains(view, "v format & validate") {
		t.Errorf("diff footer must not show the global 'v format & validate' key, got:\n%s", view)
	}
}

// TestModelDiff_LongLineTruncated verifies that a diff line wider than
// the modal body is truncated so the rendered view never exceeds the
// window width.
func TestModelDiff_LongLineTruncated(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 60, 20)
	m.diffLines = []diff.Line{
		{Kind: diff.KindFileHeader, Text: "--- config/Caddyfile"},
		{Kind: diff.KindFileHeader, Text: "+++ config/Caddyfile (formatted)"},
		{Kind: diff.KindHunkHeader, Text: "@@ -1,1 +1,1 @@"},
		{Kind: diff.KindAdd, Text: "+" + strings.Repeat("a", 200)},
	}
	m.diffTitle = "Diff · config/Caddyfile"
	m.showDiff = true
	m.syncDiffContent()
	view := m.View()
	if !strings.Contains(view, strings.Repeat("a", 20)) {
		t.Errorf("View missing the long line content, got:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(stripANSI(line)); w > 60 {
			t.Errorf("rendered line %d is %d columns wide, exceeds the 60-column window:\n%s", i+1, w, line)
		}
	}
}

// TestModelSave_NoSaverShowsWriteHint verifies that pressing s without
// a configured saver (read-only mode) surfaces a status hint about
// --write and does not open the confirmation modal.
func TestModelSave_NoSaverShowsWriteHint(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.showSaveConfirm {
		t.Error("showSaveConfirm = true, want false without saver")
	}
	if !strings.Contains(m.statusMessage, "--write") {
		t.Errorf("statusMessage = %q, want --write hint", m.statusMessage)
	}
}

// TestModelSave_NoWorkingCopyShowsHint verifies that pressing s before
// a working copy exists surfaces a status hint instead of opening the
// confirmation modal.
func TestModelSave_NoWorkingCopyShowsHint(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver)
	m = resize(m, 80, 24)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.showSaveConfirm {
		t.Error("showSaveConfirm = true, want false before validation")
	}
	if !strings.Contains(m.statusMessage, "working copy") {
		t.Errorf("statusMessage = %q, want working copy hint", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestModelSave_FailedValidationBlocksSave verifies that a failed
// validation marks the working copy as invalid and prevents the save
// confirmation from opening.
func TestModelSave_FailedValidationBlocksSave(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{formatted: []byte("formatted working copy"), diagnostics: diags, err: errors.New("caddy exit 1")}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.showSaveConfirm {
		t.Error("showSaveConfirm = true, want false after failed validation")
	}
	if m.workingValidated {
		t.Error("workingValidated = true, want false after failed validation")
	}
	if !strings.Contains(m.statusMessage, "validation failed") {
		t.Errorf("statusMessage = %q, want validation failure", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestModelSave_NoChangesShowsHint verifies that pressing s when the
// working copy matches the loaded source surfaces a "no changes"
// status instead of opening the confirmation modal.
func TestModelSave_NoChangesShowsHint(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.showSaveConfirm {
		t.Error("showSaveConfirm = true, want false")
	}
	if !strings.Contains(m.statusMessage, "no changes") {
		t.Errorf("statusMessage = %q, want no changes hint", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestModelSave_OpensConfirmation verifies the happy path: a
// successful validation that changes the working copy opens the save
// confirmation modal, which names the target path and backup dir.
func TestModelSave_OpensConfirmation(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if !m.showSaveConfirm {
		t.Fatal("showSaveConfirm = false, want true")
	}
	view := m.View()
	if !strings.Contains(view, "config/Caddyfile") {
		t.Errorf("View missing config path:\n%s", view)
	}
	if !strings.Contains(view, "Backup dir") {
		t.Errorf("View missing backup dir label:\n%s", view)
	}
	if !strings.Contains(view, "config/backups") {
		t.Errorf("View missing backup dir:\n%s", view)
	}
	if !strings.Contains(view, "Enter save") {
		t.Errorf("View missing Enter save hint:\n%s", view)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0 before confirm", saver.calls)
	}
}

// TestModelSave_EscCancels verifies that Esc closes the save
// confirmation modal without calling the saver.
func TestModelSave_EscCancels(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if !m.showSaveConfirm {
		t.Fatal("confirm modal not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showSaveConfirm {
		t.Error("showSaveConfirm = true after Esc, want false")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0 after cancel", saver.calls)
	}
	if !strings.Contains(m.statusMessage, "cancelled") {
		t.Errorf("statusMessage = %q, want cancelled hint", m.statusMessage)
	}
}

// TestModelSave_EnterTriggersSave verifies that Enter from the
// confirmation modal returns an async save command. Running the
// command invokes the saver with the real path, original bytes and
// working bytes, and delivering the result refreshes the loaded
// snapshot and root source.
func TestModelSave_EnterTriggersSave(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if !m.showSaveConfirm {
		t.Fatal("confirm modal not open")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter must return a tea.Cmd")
	}
	if !m.saving {
		t.Error("saving = false after Enter, want true")
	}
	msg := cmd()
	result, ok := msg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", msg)
	}
	if saver.capturedPath != "config/Caddyfile" {
		t.Errorf("capturedPath = %q, want config/Caddyfile", saver.capturedPath)
	}
	if string(saver.capturedOriginal) != src {
		t.Errorf("capturedOriginal = %q, want %q", saver.capturedOriginal, src)
	}
	if string(saver.capturedWorking) != formatted {
		t.Errorf("capturedWorking = %q, want %q", saver.capturedWorking, formatted)
	}
	updated, _ = m.Update(result)
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "saved") {
		t.Errorf("statusMessage = %q, want saved", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "config/backups/Caddyfile.bak") {
		t.Errorf("statusMessage = %q, want backup path", m.statusMessage)
	}
	if string(m.loadedBytes) != formatted {
		t.Errorf("loadedBytes = %q, want %q", m.loadedBytes, formatted)
	}
	if string(m.state.Graph.Root.Source) != formatted {
		t.Errorf("Root.Source = %q, want %q", m.state.Graph.Root.Source, formatted)
	}
	if m.saving {
		t.Error("saving = true after result, want false")
	}
}

// TestModelSave_ConflictStatus verifies that the saver reporting
// app.ErrConflict surfaces a "changed on disk" status.
func TestModelSave_ConflictStatus(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{err: app.ErrConflict}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "changed on disk") {
		t.Errorf("statusMessage = %q, want conflict message", m.statusMessage)
	}
}

// TestModelSave_SaveErrorShowsBackup verifies that a structured
// app.SaveError surfaces both the backup path and the underlying
// error in the status line.
func TestModelSave_SaveErrorShowsBackup(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{err: &app.SaveError{BackupPath: "config/backups/Caddyfile.bak", Err: errors.New("boom")}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "backup: config/backups/Caddyfile.bak") {
		t.Errorf("statusMessage = %q, want backup path", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "boom") {
		t.Errorf("statusMessage = %q, want boom", m.statusMessage)
	}
}

// TestModelSave_GenericErrorStatus verifies that an unclassified save
// error surfaces a generic "save failed" status.
func TestModelSave_GenericErrorStatus(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{err: errors.New("boom")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(*Model)
	if !strings.HasPrefix(m.statusMessage, "✗ save failed") {
		t.Errorf("statusMessage = %q, want save failed prefix", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "boom") {
		t.Errorf("statusMessage = %q, want boom", m.statusMessage)
	}
}

// TestModelSave_BusyIgnored verifies that a second s press while a
// save is in flight is ignored.
func TestModelSave_BusyIgnored(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{result: app.SaveResult{BackupPath: "x"}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	updated, cmd1 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if !m.saving {
		t.Error("saving = false, want true")
	}
	if cmd1 == nil {
		t.Fatal("Enter must return cmd")
	}
	_, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd2 != nil {
		t.Error("s while saving must return nil cmd")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d before cmd1 executes, want 0", saver.calls)
	}
	cmd1()
	if saver.calls != 1 {
		t.Errorf("saver.calls = %d, want 1 (second s must not trigger)", saver.calls)
	}
}

// TestModelFooter_SaveConfirmContext verifies that the bottom footer
// shows the save-confirmation keys (not the global keymap) while the
// save modal is open.
func TestModelFooter_SaveConfirmContext(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	view := stripANSI(m.View())
	if !strings.Contains(view, "Enter save") {
		t.Errorf("footer should show Enter save, got:\n%s", view)
	}
	if strings.Contains(view, "v format & validate") {
		t.Errorf("footer must not show v format & validate, got:\n%s", view)
	}
}

// TestModelHeader_WriteModeBadge verifies that a writable state shows
// the WRITE badge instead of READ-ONLY. The default read-only state
// is covered by TestModelRendersDocumentTree.
func TestModelHeader_WriteModeBadge(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	view := m.View()
	if strings.Contains(view, "READ-ONLY") {
		t.Errorf("View should not show READ-ONLY in write mode:\n%s", view)
	}
	if !strings.Contains(view, "WRITE") {
		t.Errorf("View should show WRITE badge in write mode:\n%s", view)
	}
}

// TestModelReload_NoReloaderShowsHint verifies that pressing r without
// a configured reloader surfaces a status hint and does not open the
// confirmation modal.
func TestModelReload_NoReloaderShowsHint(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}) // no reloader
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil {
		t.Errorf("expected nil cmd when reloader is nil, got %v", cmd)
	}
	if !strings.Contains(m.statusMessage, "reload unavailable") {
		t.Errorf("statusMessage = %q, want reload unavailable hint", m.statusMessage)
	}
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true, want false without reloader")
	}
}

// TestModelReload_NoWorkingCopyShowsHint verifies that pressing r
// before a working copy exists surfaces a status hint instead of
// opening the confirmation modal.
func TestModelReload_NoWorkingCopyShowsHint(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, reloader)
	m = resize(m, 80, 24)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true, want false before validation")
	}
	if !strings.Contains(m.statusMessage, "working copy") {
		t.Errorf("statusMessage = %q, want working copy hint", m.statusMessage)
	}
	if reloader.calls != 0 {
		t.Errorf("reloader.calls = %d, want 0", reloader.calls)
	}
}

// TestModelReload_NotValidatedBlocks verifies that a failed validation
// marks the working copy as invalid and prevents the reload
// confirmation from opening.
func TestModelReload_NotValidatedBlocks(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("formatted"), err: errors.New("caddy exit 1")}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true, want false after failed validation")
	}
	if !strings.Contains(m.statusMessage, "validation failed") {
		t.Errorf("statusMessage = %q, want validation failure", m.statusMessage)
	}
	if reloader.calls != 0 {
		t.Errorf("reloader.calls = %d, want 0", reloader.calls)
	}
}

// TestModelReload_UnsavedChangesBlock verifies that a working copy that
// differs from the file on disk (not yet saved) blocks reload with a
// "save first" hint.
func TestModelReload_UnsavedChangesBlock(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true, want false when working copy differs from disk")
	}
	if !strings.Contains(m.statusMessage, "save") {
		t.Errorf("statusMessage = %q, want hint about saving first", m.statusMessage)
	}
	if reloader.calls != 0 {
		t.Errorf("reloader.calls = %d, want 0", reloader.calls)
	}
}

// TestModelReload_AlreadyLoadedBlocks verifies that a second r press
// after a successful reload is a no-op with an "already loaded" hint.
func TestModelReload_AlreadyLoadedBlocks(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{result: app.ReloadResult{Endpoint: "http://localhost:2019", LoadedAt: time.Now()}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	m.Update(cmd()) // deliver the successful reload result
	if m.loaded != loadedMatches {
		t.Fatalf("precondition: loaded = %v, want loadedMatches", m.loaded)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true, want false when already loaded")
	}
	if !strings.Contains(m.statusMessage, "already loaded") {
		t.Errorf("statusMessage = %q, want already-loaded hint", m.statusMessage)
	}
	if reloader.calls != 1 {
		t.Errorf("reloader.calls = %d, want 1 (second r must not trigger a reload)", reloader.calls)
	}
}

// TestModelReload_OpensConfirmation verifies the happy path: a
// successful validation that leaves the working copy identical to the
// saved bytes opens the reload-confirmation modal.
func TestModelReload_OpensConfirmation(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !m.showReloadConfirm {
		t.Fatal("showReloadConfirm = false, want true")
	}
	if reloader.calls != 0 {
		t.Errorf("reloader.calls = %d, want 0 before confirm", reloader.calls)
	}
}

// TestModelReload_ConfirmNamesEndpoint verifies that the confirmation
// modal names the Admin API endpoint and the config path, so the
// operator can review the network target before confirming.
func TestModelReload_ConfirmNamesEndpoint(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	// stateFor builds settings with only ConfigPath; set the endpoint
	// explicitly so the modal body renders it.
	state.Settings.AdminEndpoint = "http://localhost:2019"
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !m.showReloadConfirm {
		t.Fatal("showReloadConfirm = false, want true")
	}
	view := m.View()
	if !strings.Contains(view, "http://localhost:2019") {
		t.Errorf("View missing Admin API endpoint:\n%s", view)
	}
	if !strings.Contains(view, "config/Caddyfile") {
		t.Errorf("View missing config path:\n%s", view)
	}
}

// TestModelReload_EscCancels verifies that Esc closes the reload
// confirmation modal without calling the reloader.
func TestModelReload_EscCancels(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !m.showReloadConfirm {
		t.Fatal("confirm modal not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true after Esc, want false")
	}
	if reloader.calls != 0 {
		t.Errorf("reloader.calls = %d, want 0 after cancel", reloader.calls)
	}
	if !strings.Contains(m.statusMessage, "cancelled") {
		t.Errorf("statusMessage = %q, want cancelled hint", m.statusMessage)
	}
}

// TestModelReload_EnterTriggersReload verifies that Enter from the
// confirmation modal returns an async reload command. Running the
// command invokes the reloader with the real path and the loaded
// (on-disk) bytes.
func TestModelReload_EnterTriggersReload(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !m.showReloadConfirm {
		t.Fatal("confirm modal not open")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter must return a tea.Cmd")
	}
	if !m.reloading {
		t.Error("reloading = false after Enter, want true")
	}
	msg := cmd()
	if _, ok := msg.(reloadResultMsg); !ok {
		t.Fatalf("got %T, want reloadResultMsg", msg)
	}
	if reloader.capturedPath != "config/Caddyfile" {
		t.Errorf("capturedPath = %q, want config/Caddyfile", reloader.capturedPath)
	}
	if string(reloader.capturedSaved) != src {
		t.Errorf("capturedSaved = %q, want %q", reloader.capturedSaved, src)
	}
}

// TestModelReload_SuccessSetsLoaded verifies that a successful reload
// marks the configuration as loaded and records the confirmation time.
func TestModelReload_SuccessSetsLoaded(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{result: app.ReloadResult{Endpoint: "http://localhost:2019", LoadedAt: time.Now()}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	m.Update(cmd()) // deliver the successful reload result
	if m.loaded != loadedMatches {
		t.Errorf("loaded = %v, want loadedMatches", m.loaded)
	}
	if m.loadedAt.IsZero() {
		t.Error("loadedAt is zero, want the reload timestamp")
	}
	if !strings.HasPrefix(m.statusMessage, "✓") {
		t.Errorf("statusMessage = %q, want success glyph", m.statusMessage)
	}
	if m.reloading {
		t.Error("reloading = true after result, want false")
	}
}

// TestModelReload_FailureUnreachable verifies that an unreachable Admin
// API maps to the loadedUnreachable state.
func TestModelReload_FailureUnreachable(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	updated, _ := m.Update(reloadResultMsg{Err: &app.ReloadError{
		Endpoint: "http://localhost:2019",
		Err:      fmt.Errorf("%w", app.ErrAdminUnreachable),
	}})
	m = updated.(*Model)
	if m.loaded != loadedUnreachable {
		t.Errorf("loaded = %v, want loadedUnreachable", m.loaded)
	}
	if !strings.HasPrefix(m.statusMessage, "✗") {
		t.Errorf("statusMessage = %q, want error glyph", m.statusMessage)
	}
}

// TestModelReload_FailureRejected verifies that a rejected reload
// (adapt or Admin API rejection) maps to the loadedStale state.
func TestModelReload_FailureRejected(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	updated, _ := m.Update(reloadResultMsg{Err: &app.ReloadError{
		Endpoint: "http://localhost:2019",
		Err:      fmt.Errorf("%w", app.ErrAdminRejected),
	}})
	m = updated.(*Model)
	if m.loaded != loadedStale {
		t.Errorf("loaded = %v, want loadedStale", m.loaded)
	}
	if !strings.HasPrefix(m.statusMessage, "✗") {
		t.Errorf("statusMessage = %q, want error glyph", m.statusMessage)
	}
}

// TestModelReload_BusyIgnored verifies that a second r press while a
// reload is in flight is ignored.
func TestModelReload_BusyIgnored(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 80, 24)
	m.reloading = true
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil {
		t.Error("r while reloading must return nil cmd")
	}
	if m.showReloadConfirm {
		t.Error("showReloadConfirm = true, want false while reloading")
	}
	if reloader.calls != 0 {
		t.Errorf("reloader.calls = %d, want 0", reloader.calls)
	}
}

// TestModelReload_SaveTransitionsToStale verifies that a successful
// save marks the running configuration stale: the file on disk changed,
// so until a reload proves otherwise the running config no longer
// matches it.
func TestModelReload_SaveTransitionsToStale(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	m.Update(cmd()) // deliver the successful save result
	if m.loaded != loadedStale {
		t.Errorf("loaded = %v, want loadedStale after save", m.loaded)
	}
	if !m.loadedAt.IsZero() {
		t.Error("loadedAt must be zero after save (running config no longer matches)")
	}
}

// TestModelReload_FooterShowsKey verifies that the bottom footer shows
// the r reload key only when a reloader is configured.
func TestModelReload_FooterShowsKey(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	// With a reloader the key is listed.
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, reloader)
	m = resize(m, 120, 30)
	if !strings.Contains(stripANSI(m.View()), "r reload") {
		t.Errorf("View missing 'r reload' with reloader configured:\n%s", m.View())
	}
	// Without a reloader the key must be absent.
	m2 := newLoadedModel(t, fakeLoader{state: state})
	m2 = resize(m2, 120, 30)
	if strings.Contains(stripANSI(m2.View()), "r reload") {
		t.Errorf("View should not contain 'r reload' without a reloader:\n%s", m2.View())
	}
}

// TestModelReload_HeaderBadgeLoaded verifies that the header shows the
// LOADED badge after a successful reload.
func TestModelReload_HeaderBadgeLoaded(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{result: app.ReloadResult{Endpoint: "http://localhost:2019", LoadedAt: time.Now()}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 120, 30)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	m.Update(cmd())
	view := m.View()
	if !strings.Contains(view, "LOADED") {
		t.Errorf("View missing LOADED badge:\n%s", view)
	}
}

// TestModelReload_HeaderBadgeStale verifies that the header shows the
// STALE badge after a save that has not been reloaded.
func TestModelReload_HeaderBadgeStale(t *testing.T) {
	src := "example.test {\n}\n"
	formatted := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(formatted)}
	saver := &fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	m.Update(cmd())
	view := m.View()
	if !strings.Contains(view, "STALE") {
		t.Errorf("View missing STALE badge:\n%s", view)
	}
}

// TestModelReload_HeaderBadgeUnknown verifies that the initial loaded
// state is shown as UNKNOWN when reloading is possible, and stays hidden
// in read-only sessions without a reloader (where the state has no
// meaning).
func TestModelReload_HeaderBadgeUnknown(t *testing.T) {
	src := "example.test {\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{formatted: []byte(src)}
	reloader := &fakeReloader{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, reloader)
	m = resize(m, 120, 30)
	if !strings.Contains(m.View(), "UNKNOWN") {
		t.Errorf("View missing UNKNOWN badge in the initial state:\n%s", m.View())
	}
	// Without a reloader the badge must not appear at all.
	m = newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	if strings.Contains(m.View(), "UNKNOWN") {
		t.Errorf("View shows UNKNOWN badge without a reloader:\n%s", m.View())
	}
}

// TestModelInit_RuntimeProbeCmd verifies that Init returns a startup
// command exactly when a runtime probe is configured, and that the
// command delivers a runtimeProbeResultMsg carrying the probe report.
func TestModelInit_RuntimeProbeCmd(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	// Without a probe there is no startup command.
	m := newLoadedModel(t, fakeLoader{state: state})
	if cmd := m.Init(); cmd != nil {
		t.Errorf("Init without a probe returned a command, want nil")
	}
	// With a probe, Init returns a command that reports the probe result.
	report := runtime.Report{Status: runtime.StatusRunning}
	probe := app.RuntimeStatusFunc(func(ctx context.Context) runtime.Report { return report })
	m = newLoadedModel(t, fakeLoader{state: state}, probe)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init with a probe returned nil command")
	}
	msg := cmd()
	result, ok := msg.(runtimeProbeResultMsg)
	if !ok {
		t.Fatalf("got %T, want runtimeProbeResultMsg", msg)
	}
	if result.Report.Status != runtime.StatusRunning {
		t.Errorf("report status = %v, want StatusRunning", result.Report.Status)
	}
}

// TestModelRuntimeProbe_ShowsStatusMessage verifies that delivering a
// runtimeProbeResultMsg stores the report and surfaces the expected
// one-line status text for the running and stopped outcomes.
func TestModelRuntimeProbe_ShowsStatusMessage(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	tests := []struct {
		name   string
		report runtime.Report
		want   string
	}{
		{
			name:   "running",
			report: runtime.Report{Status: runtime.StatusRunning, Capabilities: runtime.Capabilities{Binary: true, Version: "v2.11.4"}},
			want:   "✓ caddy v2.11.4 · running",
		},
		{
			name:   "stopped",
			report: runtime.Report{Status: runtime.StatusStopped, Capabilities: runtime.Capabilities{Binary: true}},
			want:   "✗ caddy binary present but Admin API not reachable (stopped or admin disabled)",
		},
		{
			name:   "unreachable",
			report: runtime.Report{Status: runtime.StatusUnreachable},
			want:   "✗ runtime probe timed out",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLoadedModel(t, fakeLoader{state: state})
			updated, _ := m.Update(runtimeProbeResultMsg{Report: tt.report})
			m = updated.(*Model)
			if !m.runtimeProbed {
				t.Error("runtimeProbed = false, want true after result delivery")
			}
			if m.runtimeReport.Status != tt.report.Status {
				t.Errorf("runtimeReport.Status = %v, want %v", m.runtimeReport.Status, tt.report.Status)
			}
			if m.statusMessage != tt.want {
				t.Errorf("statusMessage = %q, want %q", m.statusMessage, tt.want)
			}
		})
	}
}

// TestModelRuntimeProbe_UnknownStaysQuiet verifies that a fully unknown
// report (no binary, no Admin API) leaves the status line untouched.
func TestModelRuntimeProbe_UnknownStaysQuiet(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.statusMessage = "pre-existing message"
	updated, _ := m.Update(runtimeProbeResultMsg{Report: runtime.Report{Status: runtime.StatusUnknown}})
	m = updated.(*Model)
	if !m.runtimeProbed {
		t.Error("runtimeProbed = false, want true")
	}
	if m.statusMessage != "pre-existing message" {
		t.Errorf("statusMessage = %q, want it untouched by an unknown report", m.statusMessage)
	}
}

// TestModelRuntimeProbe_HeaderBadges verifies that the header renders no
// runtime badge before the probe returns and then shows the RUNNING /
// STOPPED badges plus the version string once the report arrives.
func TestModelRuntimeProbe_HeaderBadges(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	// Before the probe returns no runtime badge is rendered.
	if strings.Contains(m.View(), " RUNNING ") || strings.Contains(m.View(), " STOPPED ") {
		t.Errorf("runtime badge rendered before the probe returned:\n%s", m.View())
	}

	// A running report renders the RUNNING badge and the version.
	updated, _ := m.Update(runtimeProbeResultMsg{Report: runtime.Report{
		Status:       runtime.StatusRunning,
		Capabilities: runtime.Capabilities{Binary: true, Version: "v2.11.4"},
	}})
	m = updated.(*Model)
	view := m.View()
	if !strings.Contains(view, " RUNNING ") {
		t.Errorf("View missing RUNNING badge:\n%s", view)
	}
	if !strings.Contains(view, "caddy v2.11.4") {
		t.Errorf("View missing the version indicator:\n%s", view)
	}

	// A stopped report renders the STOPPED badge.
	updated, _ = m.Update(runtimeProbeResultMsg{Report: runtime.Report{
		Status:       runtime.StatusStopped,
		Capabilities: runtime.Capabilities{Binary: true, Version: "v2.11.4"},
	}})
	m = updated.(*Model)
	if !strings.Contains(m.View(), " STOPPED ") {
		t.Errorf("View missing STOPPED badge:\n%s", m.View())
	}
}

// TestModelRuntimeProbe_UnknownHidesBadge verifies that an unknown probe
// result renders neither a runtime badge nor a version indicator.
func TestModelRuntimeProbe_UnknownHidesBadge(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	updated, _ := m.Update(runtimeProbeResultMsg{Report: runtime.Report{Status: runtime.StatusUnknown}})
	m = updated.(*Model)
	view := m.View()
	if strings.Contains(view, " RUNNING ") || strings.Contains(view, " STOPPED ") || strings.Contains(view, " caddy v") {
		t.Errorf("unknown probe rendered runtime state in the header:\n%s", view)
	}
}

// TestModelHeader_BrandVersion verifies that the header shows the
// application name and the injected version.
func TestModelHeader_BrandVersion(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	view := stripANSI(m.View())
	if !strings.Contains(view, "lazycaddy") {
		t.Errorf("header missing brand label:\n%s", view)
	}
	if !strings.Contains(view, testVersion) {
		t.Errorf("header missing version %q:\n%s", testVersion, view)
	}
	if !strings.Contains(view, "Config:") {
		t.Errorf("header missing explicit configuration label:\n%s", view)
	}
}

// TestModelHeader_ResponsiveNarrow verifies that the header keeps its
// badges on narrow terminals and truncates the path instead of overflowing.
func TestModelHeader_ResponsiveNarrow(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	longPath := "/very/long/path/to/the/caddy/configuration/file/Caddyfile"
	fs := map[string]string{longPath: "example.test {\n}\n"}
	state := stateFor(t, longPath, fsReader(fs))
	m := newLoadedModel(t, fakeLoader{state: state})

	// Wide window: the full path fits and all badges are present.
	m = resize(m, 120, 30)
	wide := stripANSI(m.View())
	if !strings.Contains(wide, longPath) {
		t.Errorf("wide header missing full path:\n%s", wide)
	}
	for _, want := range []string{"lazycaddy", testVersion, "Config:", "READ-ONLY"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide header missing %q:\n%s", want, wide)
		}
	}

	// Narrow window: the raw long path must be gone, but the brand and
	// state badges remain. No rendered line may exceed the window width.
	m = resize(m, 40, 24)
	narrow := stripANSI(m.View())
	if strings.Contains(narrow, longPath) {
		t.Errorf("narrow header should not contain the raw long path:\n%s", narrow)
	}
	for _, want := range []string{"lazycaddy", testVersion, "Config:", "READ-ONLY"} {
		if !strings.Contains(narrow, want) {
			t.Errorf("narrow header missing %q:\n%s", want, narrow)
		}
	}
	for i, line := range strings.Split(narrow, "\n") {
		if w := lipgloss.Width(stripANSI(line)); w > 40 {
			t.Errorf("narrow header line %d is %d columns wide (max 40):\n%s", i+1, w, line)
		}
	}
}

// TestModelStatusStrip_AboveFooter verifies that transient status messages
// render in a dedicated strip above the footer and do not push the header
// out of place.
func TestModelStatusStrip_AboveFooter(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	m.statusMessage = "✓ saved (backup: config/backups/Caddyfile.bak)"

	view := stripANSI(m.View())
	if !strings.Contains(view, "✓ saved") {
		t.Errorf("status message missing from view:\n%s", view)
	}
	if !strings.Contains(view, "q quit") {
		t.Errorf("footer missing from view:\n%s", view)
	}

	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[0], "lazycaddy") {
		t.Errorf("header displaced or missing:\n%s", lines[0])
	}

	statusIdx, footerIdx := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "✓ saved") {
			statusIdx = i
		}
		if strings.Contains(line, "q quit") {
			footerIdx = i
		}
	}
	if statusIdx == -1 || footerIdx == -1 {
		t.Fatalf("could not locate status (%d) or footer (%d) lines", statusIdx, footerIdx)
	}
	if statusIdx >= footerIdx {
		t.Errorf("status strip (line %d) must appear above footer (line %d)", statusIdx+1, footerIdx+1)
	}
}

// TestModelStatusStrip_WarningMessage verifies that warning messages are
// surfaced in the status strip and kept out of the footer.
func TestModelStatusStrip_WarningMessage(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	m.statusMessage = "✗ edited document has warnings — not saved"

	view := stripANSI(m.View())
	if !strings.Contains(view, "✗ edited document has warnings") {
		t.Errorf("warning message missing from view:\n%s", view)
	}

	// The warning text must not be mixed into the contextual footer line.
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if strings.Contains(line, "q quit") && i > 0 && strings.Contains(lines[i-1], "warnings") {
			t.Errorf("warning text rendered on the footer line")
		}
	}
}

// TestModelFooter_TruncatesOnNarrow verifies that the footer wraps onto
// additional lines when the hints do not fit, instead of truncating away
// critical keys like q quit.
func TestModelFooter_TruncatesOnNarrow(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)

	view := stripANSI(m.View())
	for _, hint := range []string{"q quit", "v format & validate", "Enter toggle"} {
		if !strings.Contains(view, hint) {
			t.Errorf("wrapped footer missing critical hint %q:\n%s", hint, view)
		}
	}

	footer := stripANSI(m.footer(80))
	footerLines := lipgloss.Height(footer)
	if footerLines < 2 {
		t.Errorf("footer should wrap to multiple lines at 80 cols, got %d line(s):\n%s", footerLines, footer)
	}
	for i, line := range strings.Split(footer, "\n") {
		if w := lipgloss.Width(line); w > 80 {
			t.Errorf("footer line %d is %d columns wide (max 80):\n%s", i+1, w, line)
		}
	}
}

// assertFits verifies that the rendered view fits the terminal budget,
// that the header is the first line, and that the footer is the last
// visible section. It is used by the layout regression tests.
func assertFits(t *testing.T, m *Model, width, height int) {
	t.Helper()
	m = resize(m, width, height)
	lines := strings.Split(stripANSI(m.View()), "\n")
	n := len(lines)
	if n > 0 && lines[n-1] == "" {
		n--
	}
	if n > height {
		t.Fatalf("view renders %d lines on a %d-line budget:\n%s", n, height, m.View())
	}
	if !strings.Contains(lines[0], "lazycaddy") {
		t.Errorf("header is not the first line: %q", lines[0])
	}
	if !strings.Contains(lines[n-1], "quit") && !strings.Contains(lines[n-1], "Esc") {
		t.Errorf("footer is not the last section: %q", lines[n-1])
	}
}

func TestModelViewFits_Normal(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	assertFits(t, m, 80, 24)
}

func TestModelViewFits_StatusMessage(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.statusMessage = "✓ caddy v2.11.4 · running"
	assertFits(t, m, 80, 24)
}

func TestModelViewFits_ErrorAndStatus(t *testing.T) {
	m := New(fakeLoader{state: nil, err: &noSuchFile{path: "missing/Caddyfile"}}, nil, nil, nil, nil, nil, nil, nil, testVersion, nil, nil, nil)
	m.Load()
	m.statusMessage = "✓ caddy v2.11.4 · running"
	assertFits(t, m, 80, 24)
}

func TestModelViewFits_WrappedFooter(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	// The default 80-column global footer wraps onto two lines; this must
	// not push the header off the screen.
	assertFits(t, m, 80, 24)
}

func TestModelViewFits_LogViewWithStatus(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		HistoryFn: func() []logs.Entry { return nil },
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m.showLogs = true
	m.statusMessage = "log poll resumed"
	assertFits(t, m, 80, 24)
}

func TestModelViewFits_ModalWithStatus(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.showSaveConfirm = true
	m.statusMessage = "save cancelled"
	assertFits(t, m, 80, 24)
}

// logEntry builds a structured entry with the given message for tests.
func logEntry(msg string) logs.Entry {
	return logs.Entry{
		Raw:    []byte(`{"level":"info","msg":"` + msg + `"}`),
		Parsed: true,
		Level:  "info",
		Msg:    msg,
		Status: -1,
	}
}

// logStateFor returns a loaded state whose settings carry a log path, so
// the log view title and footer assertions can exercise the path text.
func logStateFor(t *testing.T) *app.State {
	t.Helper()
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	state.Settings.LogPath = "logs/access.log"
	return state
}

// TestModelLogView_UnavailableWithoutSource verifies that pressing l
// without a configured log source surfaces a hint and opens nothing.
func TestModelLogView_UnavailableWithoutSource(t *testing.T) {
	state := logStateFor(t)
	m := newLoadedModel(t, fakeLoader{state: state}) // no log source
	m = resize(m, 120, 30)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if cmd != nil {
		t.Errorf("expected nil cmd without a log source, got %v", cmd)
	}
	if m.showLogs {
		t.Error("showLogs = true without a log source, want false")
	}
	if !strings.Contains(m.statusMessage, "no log source configured") {
		t.Errorf("statusMessage = %q, want a no-log-source hint", m.statusMessage)
	}
}

// TestModelLogView_OpenSeedsHistory verifies that opening the log view
// seeds the scrollback from the source history, starts the polling tick
// and renders the log pane with the followed path and entries.
func TestModelLogView_OpenSeedsHistory(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{logEntry("handled request"), logEntry("second line")} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("opening the log view must return a poll command")
	}
	if !m.showLogs {
		t.Fatal("showLogs = false after l, want true")
	}
	if len(m.logLines) != 2 {
		t.Fatalf("logLines = %d, want 2 (seeded from history)", len(m.logLines))
	}
	view := m.View()
	if !strings.Contains(view, "Logs · logs/access.log") {
		t.Errorf("View missing the log pane title:\n%s", view)
	}
	visible := stripANSI(view)
	if !strings.Contains(visible, "handled request") || !strings.Contains(visible, "second line") {
		t.Errorf("View missing the seeded entry text:\n%s", visible)
	}
}

// TestModelLogView_JournalUnitTitle verifies that when the configured source
// is a systemd journal unit, the log view title identifies the unit.
func TestModelLogView_JournalUnitTitle(t *testing.T) {
	state := logStateFor(t)
	state.Settings.LogPath = ""
	state.Settings.JournalUnit = "caddy.service"
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("opening the log view must return a poll command")
	}
	if !strings.Contains(m.View(), "Logs · unit caddy.service") {
		t.Errorf("View missing the journal unit title:\n%s", m.View())
	}
}

// TestModelLogView_JournalPollErrorKeepsBrowsing verifies that a failing
// journal source surfaces through the existing poll-error status line while
// the rest of the TUI stays browsable: the error is reported, the log view
// stays open, and tree navigation still works.
func TestModelLogView_JournalPollErrorKeepsBrowsing(t *testing.T) {
	state := logStateFor(t)
	state.Settings.LogPath = ""
	state.Settings.JournalUnit = "caddy.service"
	pollErr := errors.New("journalctl unavailable")
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, pollErr },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)

	// Open the log view: the first poll surfaces the source error.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("opening the log view must return a poll command")
	}
	updated, _ = m.Update(logTailMsg{Err: pollErr})
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "✗ log poll failed") {
		t.Errorf("statusMessage = %q, want the poll-failure message", m.statusMessage)
	}
	if !errors.Is(m.logErr, pollErr) {
		t.Errorf("logErr = %v, want the sentinel poll error", m.logErr)
	}

	// Browsing still works: close the log view (Esc) and navigate the tree.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*Model)
	if m.showLogs {
		t.Error("showLogs = true after closing, want false")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(*Model)
	if m.cursor != 1 {
		t.Errorf("cursor = %d after navigating, want 1 (tree still navigable)", m.cursor)
	}
}

// TestModelLogTail_AppendsAndReschedules verifies that a delivered poll
// result appends entries and reschedules the next poll, and that an empty
// result keeps polling.
func TestModelLogTail_AppendsAndReschedules(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)

	// A non-empty poll appends and reschedules.
	updated, cmd := m.Update(logTailMsg{Entries: []logs.Entry{logEntry("first")}})
	m = updated.(*Model)
	if len(m.logLines) != 1 {
		t.Fatalf("logLines = %d, want 1 after a poll", len(m.logLines))
	}
	if cmd == nil {
		t.Error("poll with new entries must reschedule (non-nil cmd)")
	}
	// An empty poll still reschedules.
	updated, cmd = m.Update(logTailMsg{Entries: nil})
	m = updated.(*Model)
	if cmd == nil {
		t.Error("empty poll must reschedule (non-nil cmd)")
	}
	if m.logErr != nil {
		t.Errorf("logErr = %v, want nil after a clean poll", m.logErr)
	}
}

// TestModelLogTail_PauseStopsPolling verifies that p suspends polling
// (nil reschedule) and a second p resumes it.
func TestModelLogTail_PauseStopsPolling(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = updated.(*Model)
	if !m.logPaused {
		t.Fatal("logPaused = false after p, want true")
	}
	if cmd != nil {
		t.Errorf("p must stop the poll (nil cmd), got %v", cmd)
	}
	// A poll delivered while paused must not reschedule.
	updated, cmd = m.Update(logTailMsg{Entries: []logs.Entry{logEntry("late")}})
	m = updated.(*Model)
	if cmd != nil {
		t.Errorf("paused poll must not reschedule, got %v", cmd)
	}
	if len(m.logLines) != 1 {
		t.Errorf("logLines = %d, want 1 (entries still appended while paused)", len(m.logLines))
	}
	// Resuming restarts the poll.
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = updated.(*Model)
	if m.logPaused {
		t.Error("logPaused = true after second p, want false")
	}
	if cmd == nil {
		t.Error("resume must restart the poll (non-nil cmd)")
	}
}

// TestModelLogTail_FollowToggle verifies that f toggles follow and that
// scrolling up turns follow off (the operator takes control).
func TestModelLogTail_FollowToggle(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if !m.logFollow {
		t.Fatal("logFollow = false on open, want true")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if m.logFollow {
		t.Error("logFollow = true after f, want false")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if !m.logFollow {
		t.Error("logFollow = false after second f, want true")
	}
	// Pressing up hands control back to the operator.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.logFollow {
		t.Error("logFollow = true after up, want false")
	}
	if !strings.Contains(m.statusMessage, "follow off") {
		t.Errorf("statusMessage = %q, want follow-off hint", m.statusMessage)
	}
}

// TestModelLogTail_EscCloses verifies that Esc closes the log view and
// that a poll delivered afterwards is not rescheduled.
func TestModelLogTail_EscCloses(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if !m.showLogs {
		t.Fatal("log view not open")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*Model)
	if m.showLogs {
		t.Error("showLogs = true after Esc, want false")
	}
	if cmd != nil {
		t.Errorf("Esc must stop the poll (nil cmd), got %v", cmd)
	}
	// A late poll result must not reschedule after the view is closed.
	updated, cmd = m.Update(logTailMsg{Entries: []logs.Entry{logEntry("late")}})
	m = updated.(*Model)
	if cmd != nil {
		t.Errorf("poll after close must not reschedule, got %v", cmd)
	}
}

// TestModelLogTail_ClearsStaleError verifies that a successful poll clears
// the "log poll failed" status line left by a previous failed poll, while
// leaving status messages set by other actions untouched.
func TestModelLogTail_ClearsStaleError(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)

	// A failing poll sets the error status line.
	updated, _ = m.Update(logTailMsg{Err: errors.New("boom")})
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "log poll failed") {
		t.Fatalf("statusMessage = %q, want a poll-failure message", m.statusMessage)
	}
	if m.logErr == nil {
		t.Fatal("logErr = nil after a failed poll, want the error")
	}

	// A successful poll clears it.
	updated, _ = m.Update(logTailMsg{Entries: []logs.Entry{{Raw: []byte("x"), Status: -1}}})
	m = updated.(*Model)
	if m.statusMessage != "" {
		t.Errorf("statusMessage = %q, want cleared after a successful poll", m.statusMessage)
	}
	if m.logErr != nil {
		t.Errorf("logErr = %v, want nil after a successful poll", m.logErr)
	}

	// Status messages owned by other actions are NOT cleared by a poll.
	m.statusMessage = "log follow on"
	updated, _ = m.Update(logTailMsg{Entries: nil})
	m = updated.(*Model)
	if m.statusMessage != "log follow on" {
		t.Errorf("statusMessage = %q, want it untouched (only poll failures are cleared)", m.statusMessage)
	}
}

// TestModelLogTail_Bounded verifies the UI-side scrollback stays capped at
// logMaxLines and keeps the tail.
func TestModelLogTail_Bounded(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)

	feed := make([]logs.Entry, 0, logMaxLines+50)
	for i := 0; i < logMaxLines+50; i++ {
		feed = append(feed, logEntry(fmt.Sprintf("line-%d", i)))
	}
	updated, _ = m.Update(logTailMsg{Entries: feed})
	m = updated.(*Model)
	if len(m.logLines) != logMaxLines {
		t.Fatalf("logLines = %d, want %d", len(m.logLines), logMaxLines)
	}
	// The tail is preserved: the newest entry survives.
	if m.logLines[len(m.logLines)-1].Msg != "line-1049" {
		t.Errorf("last entry = %q, want line-1049 (tail preserved)", m.logLines[len(m.logLines)-1].Msg)
	}
}

// TestModelLogView_Footer verifies the footer lists the l key only when a
// log source is configured and shows the log-view keys while it is open.
func TestModelLogView_Footer(t *testing.T) {
	state := logStateFor(t)
	// Without a log source the l key is absent.
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	if strings.Contains(stripANSI(m.View()), "l logs") {
		t.Errorf("footer shows 'l logs' without a log source:\n%s", m.View())
	}
	// With a log source the l key appears.
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m = newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	if !strings.Contains(stripANSI(m.View()), "l logs") {
		t.Errorf("footer missing 'l logs' with a log source:\n%s", m.View())
	}
	// While the log view is open the footer shows the log-view key hints.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	view := stripANSI(m.View())
	for _, want := range []string{"Enter detail", "f follow", "p pause/resume", "Esc close", "q quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("footer missing %q while the log view is open:\n%s", want, view)
		}
	}
}

// seededLogSource returns a LogSource whose history holds count entries.
func seededLogSource(count int) app.LogSourceFunc {
	return app.LogSourceFunc{
		NextFn: func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry {
			entries := make([]logs.Entry, 0, count)
			for i := 0; i < count; i++ {
				entries = append(entries, logEntry(fmt.Sprintf("e-%d", i)))
			}
			return entries
		},
	}
}

// TestModelLogCursor_MovesAndReveals verifies the row cursor starts on the
// newest entry and moves with up/down, turning follow off on up.
func TestModelLogCursor_MovesAndReveals(t *testing.T) {
	state := logStateFor(t)
	m := newLoadedModel(t, fakeLoader{state: state}, seededLogSource(10))
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if m.logCursor != 9 {
		t.Fatalf("logCursor = %d, want 9 (newest) on open", m.logCursor)
	}
	// Up turns follow off and moves the cursor.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.logFollow {
		t.Error("logFollow = true after up, want false")
	}
	if m.logCursor != 8 {
		t.Errorf("logCursor = %d after up, want 8", m.logCursor)
	}
	// Down moves back toward the newest.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.logCursor != 9 {
		t.Errorf("logCursor = %d after down, want 9", m.logCursor)
	}
}

// TestModelLogCursor_FollowKeepsNewest verifies that new entries keep the
// cursor on the newest line while follow is on.
func TestModelLogCursor_FollowKeepsNewest(t *testing.T) {
	state := logStateFor(t)
	m := newLoadedModel(t, fakeLoader{state: state}, seededLogSource(2))
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	updated, _ = m.Update(logTailMsg{Entries: []logs.Entry{logEntry("a"), logEntry("b")}})
	m = updated.(*Model)
	if !m.logFollow {
		t.Fatal("logFollow = false, want true")
	}
	if m.logCursor != len(m.logLines)-1 {
		t.Errorf("logCursor = %d, want the newest (%d) while following", m.logCursor, len(m.logLines)-1)
	}
}

// TestModelLogCursor_AdjustsAfterTrim verifies the cursor stays valid and
// stable when the bounded trim drops entries from the front.
func TestModelLogCursor_AdjustsAfterTrim(t *testing.T) {
	state := logStateFor(t)
	m := newLoadedModel(t, fakeLoader{state: state}, seededLogSource(logMaxLines))
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if m.logCursor != logMaxLines-1 {
		t.Fatalf("logCursor = %d, want %d on open", m.logCursor, logMaxLines-1)
	}
	// Deliver enough entries to force a trim while following.
	feed := make([]logs.Entry, 60)
	for i := range feed {
		feed[i] = logEntry(fmt.Sprintf("new-%d", i))
	}
	updated, _ = m.Update(logTailMsg{Entries: feed})
	m = updated.(*Model)
	if len(m.logLines) != logMaxLines {
		t.Fatalf("logLines = %d, want %d after trim", len(m.logLines), logMaxLines)
	}
	if m.logCursor != len(m.logLines)-1 {
		t.Errorf("logCursor = %d, want the newest (%d) while following after trim", m.logCursor, len(m.logLines)-1)
	}
	// Turn follow off, then trim again: the cursor subtracts the dropped
	// count so it keeps pointing at the same entry.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp}) // follow off, cursor 998
	if m.logFollow {
		t.Fatal("logFollow = true after up, want false")
	}
	before := m.logCursor
	updated, _ = m.Update(logTailMsg{Entries: feed})
	m = updated.(*Model)
	want := before - 60
	if want < 0 {
		want = 0
	}
	if m.logCursor != want {
		t.Errorf("logCursor = %d after trim with follow off, want %d", m.logCursor, want)
	}
	if m.logCursor < 0 || m.logCursor >= len(m.logLines) {
		t.Errorf("logCursor = %d out of range for %d entries", m.logCursor, len(m.logLines))
	}
}

// TestModelLogDetail_EnterOpensEscCloses verifies Enter opens the detail
// modal for the selected entry and Esc closes it (once to the list, again
// to the main screen).
func TestModelLogDetail_EnterOpensEscCloses(t *testing.T) {
	state := logStateFor(t)
	m := newLoadedModel(t, fakeLoader{state: state}, seededLogSource(3))
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	// Move from index 2 (newest) to index 1.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.logCursor != 1 {
		t.Fatalf("logCursor = %d, want 1", m.logCursor)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if !m.logDetailOpen {
		t.Fatal("logDetailOpen = false after Enter, want true")
	}
	if string(m.logDetailEntry.Raw) != string(m.logLines[1].Raw) {
		t.Errorf("logDetailEntry.Raw = %q, want the selected entry %q", m.logDetailEntry.Raw, m.logLines[1].Raw)
	}
	// Esc closes the detail but keeps the log view open.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.logDetailOpen {
		t.Error("logDetailOpen = true after Esc, want false")
	}
	if !m.showLogs {
		t.Error("showLogs = false after Esc from detail, want true")
	}
	// Esc again closes the log view.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showLogs {
		t.Error("showLogs = true after second Esc, want false")
	}
}

// TestModelLogDetail_ShowsFullJSON verifies the detail modal renders the
// full lossless JSON of the selected entry and the footer shows the detail
// keys.
func TestModelLogDetail_ShowsFullJSON(t *testing.T) {
	state := logStateFor(t)
	raw := `{"level":"info","ts":1760000000.5,"logger":"http.log.access","msg":"handled request","request":{"method":"GET","host":"localhost","uri":"/api/config"},"status":200}`
	entry, err := logs.ParseEntry([]byte(raw))
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{entry} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if !m.logDetailOpen {
		t.Fatal("detail modal not open")
	}
	view := m.View()
	visible := stripANSI(view)
	if !strings.Contains(visible, `"request"`) || !strings.Contains(visible, "/api/config") {
		t.Errorf("detail modal missing the full JSON:\n%s", visible)
	}
	if !strings.Contains(visible, "Esc back") {
		t.Errorf("footer missing the detail hint 'Esc back':\n%s", visible)
	}
}

// TestModelLogDetail_NonJSONEntry verifies the detail modal shows the raw
// line verbatim for a non-JSON entry.
func TestModelLogDetail_NonJSONEntry(t *testing.T) {
	state := logStateFor(t)
	raw := "2026/08/08 12:00:00 INFO something happened"
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{{Raw: []byte(raw), Status: -1}} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if !m.logDetailOpen {
		t.Fatal("detail modal not open")
	}
	if !strings.Contains(stripANSI(m.View()), raw) {
		t.Errorf("detail modal missing the raw line:\n%s", m.View())
	}
}

// TestModelLogView_CompactLines verifies the log list renders the compact
// human-readable layout (not the raw JSON blob).
func TestModelLogView_CompactLines(t *testing.T) {
	state := logStateFor(t)
	raw := `{"level":"info","ts":1760000000.5,"logger":"http.log.access","msg":"handled request","request":{"method":"GET","host":"localhost","uri":"/api/config"},"status":200}`
	entry, err := logs.ParseEntry([]byte(raw))
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{entry} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	visible := stripANSI(m.View())
	for _, want := range []string{"—", "GET", "/api/config", "200", "handled request"} {
		if !strings.Contains(visible, want) {
			t.Errorf("compact log view missing %q:\n%s", want, visible)
		}
	}
	// The raw JSON structure must be gone from the list view.
	if strings.Contains(visible, `"request":{`) {
		t.Errorf("compact log view still shows the raw JSON blob:\n%s", visible)
	}
}

// editorStateFor returns a writable state whose root imports one site
// file, so the editor flow exercises a document that is not the root.
func editorStateFor(t *testing.T) *app.State {
	t.Helper()
	return writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": "a.example.test {\n\trespond ok\n}\n",
	}))
}

// pressEditorKey drives a full $EDITOR round-trip from the e keypress
// through the delivered editorDoneMsg, assuming a clean editor exit with
// code 0. It mutates m in place and returns the done message for the
// caller to deliver.
func pressEditorKey(t *testing.T, m *Model) editorDoneMsg {
	t.Helper()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd == nil {
		t.Fatal("e must return a command")
	}
	msg := cmd()
	ready, ok := msg.(editorReadyMsg)
	if !ok {
		t.Fatalf("got %T, want editorReadyMsg", msg)
	}
	m.Update(ready)
	updated, cmd := m.Update(editorExecMsg{Err: nil})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("editorExecMsg must return the complete command")
	}
	doneMsg := cmd()
	done, ok := doneMsg.(editorDoneMsg)
	if !ok {
		t.Fatalf("got %T, want editorDoneMsg", doneMsg)
	}
	return done
}

// TestEditorKey_DisabledOnDocumentRow verifies the explicit decision: with
// a document row selected (depth 0, no node) the e command is disabled and
// never falls back to opening the whole file.
func TestEditorKey_DisabledOnDocumentRow(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	editor := &fakeEditor{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	if m.selectedItem().hasNode {
		t.Fatal("precondition: the root document row must have no node")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd != nil {
		t.Errorf("e on a document row returned a command, want none")
	}
	if editor.prepareCalls != 0 {
		t.Errorf("Prepare called %d times on a document row, want 0", editor.prepareCalls)
	}
	if m.editing {
		t.Error("editing = true, want false")
	}
	if m.editorSession != nil {
		t.Error("editorSession set, want nil")
	}
}

// TestEditorKey_DisabledInReadOnly verifies that the e command is ignored
// in read-only mode, mirroring the save flow.
func TestEditorKey_DisabledInReadOnly(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	editor := &fakeEditor{}
	m := newLoadedModel(t, fakeLoader{state: state}, editor)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // select the node row
	if !m.selectedItem().hasNode {
		t.Fatal("precondition: a node row must be selected")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd != nil {
		t.Errorf("e in read-only mode returned a command, want none")
	}
	if editor.prepareCalls != 0 {
		t.Errorf("Prepare called %d times in read-only mode, want 0", editor.prepareCalls)
	}
	if m.editing {
		t.Error("editing = true, want false")
	}
}

// TestEditorKey_DisabledWithoutEditor verifies that the e command is
// ignored when no app.Editor is wired, surfacing a hint.
func TestEditorKey_DisabledWithoutEditor(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver) // no editor
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})       // select the node row
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd != nil {
		t.Errorf("e without an editor returned a command, want none")
	}
	if m.editing {
		t.Error("editing = true, want false")
	}
	if !strings.Contains(m.statusMessage, "no editor configured") {
		t.Errorf("statusMessage = %q, want the editor hint", m.statusMessage)
	}
}

// TestEditorFlow_ValidAppliesToDocument walks the happy path against an
// imported document: Prepare targets the imported file (never the root),
// the diff opens with the document path, Enter confirms through the save
// modal and the saver writes the imported path.
func TestEditorFlow_ValidAppliesToDocument(t *testing.T) {
	importedSrc := "a.example.test {\n\trespond ok\n}\n"
	editedSrc := "a.example.test {\n\trespond ok\n\tencode gzip\n}\n"
	fs := map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}
	// The loader reads through the mutable fs so the post-save structural
	// refresh picks up the content the diskSaver wrote.
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(fs))
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(importedSrc),
		Content:      []byte(editedSrc),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/a.caddy.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	// items: root doc, a.caddy doc, a.example.test node.
	if len(m.items) != 3 {
		t.Fatalf("items = %d, want 3", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	if !m.selectedItem().hasNode {
		t.Fatal("precondition: the node row must be selected")
	}

	// Press e: Prepare must target the imported document, never the root.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd == nil {
		t.Fatal("e must return a command")
	}
	if !m.editing {
		t.Error("editing = false after e, want true")
	}
	msg := cmd()
	ready, ok := msg.(editorReadyMsg)
	if !ok {
		t.Fatalf("got %T, want editorReadyMsg", msg)
	}
	if editor.prepareCalls != 1 {
		t.Errorf("Prepare calls = %d, want 1", editor.prepareCalls)
	}
	if editor.capturedDoc == nil || editor.capturedDoc.Path != "config/sites/a.caddy" {
		t.Errorf("Prepare doc path = %v, want the imported config/sites/a.caddy (never the root)", editor.capturedDoc)
	}
	wantRange := state.Graph.Documents[1].Nodes[0].Range
	if editor.capturedRange != wantRange {
		t.Errorf("Prepare range = %+v, want the node range %+v", editor.capturedRange, wantRange)
	}

	// The ready message stores the session and returns the exec command.
	updated, cmd := m.Update(ready)
	m = updated.(*Model)
	if m.editorSession == nil {
		t.Fatal("editorSession not stored on editorReadyMsg")
	}
	if cmd == nil {
		t.Fatal("editorReadyMsg must return the exec command")
	}

	// Simulate the editor exiting cleanly.
	updated, cmd = m.Update(editorExecMsg{Err: nil})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("editorExecMsg must return the complete command")
	}
	doneMsg := cmd()
	done, ok := doneMsg.(editorDoneMsg)
	if !ok {
		t.Fatalf("got %T, want editorDoneMsg", doneMsg)
	}
	if editor.capturedExit != 0 {
		t.Errorf("Complete exit = %d, want 0", editor.capturedExit)
	}

	// Deliver the result: the edit diff opens with the document path.
	updated, _ = m.Update(done)
	m = updated.(*Model)
	if m.editing {
		t.Error("editing = true after the result, want false")
	}
	if !m.showDiff {
		t.Fatal("showDiff = false after a valid edit, want true")
	}
	if m.pendingEdit == nil {
		t.Fatal("pendingEdit not set after a valid edit")
	}
	if m.pendingEdit.path != "config/sites/a.caddy" {
		t.Errorf("pendingEdit.path = %q, want the imported path", m.pendingEdit.path)
	}
	if string(m.pendingEdit.content) != editedSrc {
		t.Errorf("pendingEdit.content = %q, want %q", m.pendingEdit.content, editedSrc)
	}
	if m.diffTitle != "Diff · config/sites/a.caddy" {
		t.Errorf("diffTitle = %q, want the imported document path", m.diffTitle)
	}

	// Enter in the edit diff saves directly: the diff is the single
	// confirmation for an editor edit, and the saver targets the
	// imported document.
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if m.showDiff {
		t.Error("showDiff = true after Enter, want false")
	}
	if m.showSaveConfirm {
		t.Error("showSaveConfirm = true after Enter in the edit diff, want false (the diff is the only confirmation)")
	}
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	if saver.capturedPath != "config/sites/a.caddy" {
		t.Errorf("saver path = %q, want the imported document, never the root", saver.capturedPath)
	}
	if string(saver.capturedOriginal) != importedSrc {
		t.Errorf("saver original = %q, want %q", saver.capturedOriginal, importedSrc)
	}
	if string(saver.capturedWorking) != editedSrc {
		t.Errorf("saver working = %q, want %q", saver.capturedWorking, editedSrc)
	}

	// The delivered save refreshes the imported document in memory and
	// leaves the root untouched.
	updated, _ = m.Update(res)
	m = updated.(*Model)
	if m.pendingEdit != nil {
		t.Error("pendingEdit not cleared after a successful save")
	}
	var importedDoc *caddyfile.Document
	for _, d := range m.state.Graph.Documents {
		if d.Path == "config/sites/a.caddy" {
			importedDoc = d
		}
	}
	if importedDoc == nil {
		t.Fatal("imported document not found in the graph")
	}
	if string(importedDoc.Source) != editedSrc {
		t.Errorf("imported doc Source = %q, want %q", importedDoc.Source, editedSrc)
	}
	if string(m.state.Graph.Root.Source) != "import sites/a.caddy\n" {
		t.Errorf("root Source = %q, want it untouched", m.state.Graph.Root.Source)
	}
	if string(m.loadedBytes) != "import sites/a.caddy\n" {
		t.Errorf("loadedBytes = %q, want the root unchanged after an imported-document save", m.loadedBytes)
	}
}

// TestEditorFlow_CancelledNoSave verifies that a cancelled result never
// reaches the diff and never touches the saver.
func TestEditorFlow_CancelledNoSave(t *testing.T) {
	state := editorStateFor(t)
	editor := &fakeEditor{result: app.EditResult{
		Original:  []byte("a.example.test {\n\trespond ok\n}\n"),
		Cancelled: true,
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	done := pressEditorKey(t, m)
	m.Update(done)
	if m.showDiff {
		t.Error("showDiff = true for a cancelled edit, want false")
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit set for a cancelled edit, want nil")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
	if m.editing {
		t.Error("editing = true, want false")
	}
}

// TestEditorFlow_InvalidShowsDiagnostics verifies that an edit that fails
// validation opens the diagnostics modal and is never savable.
func TestEditorFlow_InvalidShowsDiagnostics(t *testing.T) {
	state := editorStateFor(t)
	diags := []validator.Diagnostic{
		{Path: "config/sites/a.caddy", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	editor := &fakeEditor{result: app.EditResult{
		Original:    []byte("a.example.test {\n\trespond ok\n}\n"),
		Diagnostics: diags,
		Changed:     true,
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	done := pressEditorKey(t, m)
	m.Update(done)
	if !m.showDiagnostics {
		t.Fatal("showDiagnostics = false, want true for an invalid edit")
	}
	if len(m.diagnostics) != 1 {
		t.Errorf("diagnostics = %v, want the editor diagnostics", m.diagnostics)
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit set for an invalid edit, want nil (not savable)")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestEditorFlow_NoChanges verifies that an edit leaving the range intact
// surfaces a no-changes status and opens nothing.
func TestEditorFlow_NoChanges(t *testing.T) {
	state := editorStateFor(t)
	editor := &fakeEditor{result: app.EditResult{
		Original: []byte("a.example.test {\n\trespond ok\n}\n"),
		Changed:  false,
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	done := pressEditorKey(t, m)
	m.Update(done)
	if m.showDiff {
		t.Error("showDiff = true for a no-change edit, want false")
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit set for a no-change edit, want nil")
	}
	if !strings.Contains(m.statusMessage, "no changes") {
		t.Errorf("statusMessage = %q, want a no-changes hint", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestEditorFlow_DiscardViaEsc verifies that Esc from the edit diff closes
// it and discards the pending edit without saving.
func TestEditorFlow_DiscardViaEsc(t *testing.T) {
	state := editorStateFor(t)
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte("a.example.test {\n\trespond ok\n}\n"),
		Content:      []byte("a.example.test {\n\trespond ok\n\tencode gzip\n}\n"),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	done := pressEditorKey(t, m)
	m.Update(done)
	if !m.showDiff {
		t.Fatal("showDiff = false, want true before discarding")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showDiff {
		t.Error("showDiff = true after Esc, want false")
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit not discarded after Esc, want nil")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestEditorFlow_CouldNotStart verifies that a launch failure (as opposed
// to a non-zero editor exit) surfaces a status line and applies nothing.
func TestEditorFlow_CouldNotStart(t *testing.T) {
	state := editorStateFor(t)
	editor := &fakeEditor{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	msg := cmd()
	ready, ok := msg.(editorReadyMsg)
	if !ok {
		t.Fatalf("got %T, want editorReadyMsg", msg)
	}
	m.Update(ready)
	updated, cmd := m.Update(editorExecMsg{Err: errors.New("exec: no such file")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("editorExecMsg must return the complete command")
	}
	doneMsg := cmd()
	done, ok := doneMsg.(editorDoneMsg)
	if !ok {
		t.Fatalf("got %T, want editorDoneMsg", doneMsg)
	}
	m.Update(done)
	if !strings.Contains(m.statusMessage, "could not start editor") {
		t.Errorf("statusMessage = %q, want a launch failure hint", m.statusMessage)
	}
	if m.editing {
		t.Error("editing = true, want false")
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit set despite the launch failure")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestEditorFlow_FooterShowsKey verifies that the footer lists the e edit
// key only when an editor is wired, writable mode is active and a node row
// is selected.
func TestEditorFlow_FooterShowsKey(t *testing.T) {
	state := editorStateFor(t)
	editor := &fakeEditor{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	// On a document row the key is hidden (no node range to edit).
	if strings.Contains(stripANSI(m.View()), "e edit") {
		t.Errorf("footer shows 'e edit' on a document row:\n%s", m.View())
	}
	// On a node row the key appears.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(stripANSI(m.View()), "e edit") {
		t.Errorf("footer missing 'e edit' on a node row:\n%s", m.View())
	}
	// In read-only mode the key is hidden even on a node row.
	readOnly := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m2 := newLoadedModel(t, fakeLoader{state: readOnly}, editor)
	m2 = resize(m2, 120, 30)
	m2 = keyPress(t, m2, tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(stripANSI(m2.View()), "e edit") {
		t.Errorf("footer shows 'e edit' in read-only mode:\n%s", m2.View())
	}
}

// TestEditorFlow_FailedSaveReopensDiff verifies that a failed save of a
// pending editor edit reopens the diff modal — so the operator can retry
// with Enter or discard with Esc — and keeps the pendingEdit intact
// alongside the error status message.
func TestEditorFlow_FailedSaveReopensDiff(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{name: "conflict", err: app.ErrConflict, wantMsg: "changed on disk"},
		{name: "save error with backup", err: &app.SaveError{BackupPath: "config/backups/a.caddy.bak", Err: errors.New("boom")}, wantMsg: "save failed"},
		{name: "generic error", err: errors.New("boom"), wantMsg: "save failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := editorStateFor(t)
			editor := &fakeEditor{result: app.EditResult{
				Original:     []byte("a.example.test {\n\trespond ok\n}\n"),
				Content:      []byte("a.example.test {\n\trespond ok\n\tencode gzip\n}\n"),
				Changed:      true,
				SnapshotPath: "snap-1",
			}}
			saver := &fakeSaver{err: tt.err}
			m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
			m = resize(m, 120, 30)
			m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
			m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
			done := pressEditorKey(t, m)
			m.Update(done)
			if !m.showDiff {
				t.Fatal("precondition: diff must be open")
			}
			// Enter in the edit diff saves directly (the diff is the only
			// confirmation for an editor edit).
			updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
			m = updated.(*Model)
			if m.showDiff {
				t.Error("showDiff = true after Enter, want false")
			}
			if cmd == nil {
				t.Fatal("Enter must return the save command")
			}
			msg := cmd() // saveResultMsg carrying tt.err
			updated, _ = m.Update(msg)
			m = updated.(*Model)
			if !m.showDiff {
				t.Fatalf("diff not reopened after a failed save (statusMessage = %q)", m.statusMessage)
			}
			if m.pendingEdit == nil {
				t.Error("pendingEdit cleared after a failed save, want retained")
			}
			if !strings.Contains(m.statusMessage, tt.wantMsg) {
				t.Errorf("statusMessage = %q, want it to contain %q", m.statusMessage, tt.wantMsg)
			}
			// Enter retries the save from the reopened diff.
			updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
			m = updated.(*Model)
			if cmd == nil {
				t.Fatal("retry Enter must return the save command")
			}
			retryMsg := cmd()
			if saver.calls != 2 {
				t.Errorf("saver.calls = %d, want 2 (Enter retries the save)", saver.calls)
			}
			updated, _ = m.Update(retryMsg)
			m = updated.(*Model)
			if !m.showDiff {
				t.Fatal("diff must be reopened after the failed retry")
			}
			// Esc discards the pending edit.
			m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
			if m.pendingEdit != nil {
				t.Error("pendingEdit not discarded after Esc from the reopened diff")
			}
		})
	}
}

// TestEditorFlow_SaveShortcutOpensConfirmForPendingEdit verifies that the
// s keybinding opens the save confirmation for a pending editor edit even
// when no root working copy exists: the pending edit was already validated
// by the editor flow and targets its own document.
func TestEditorFlow_SaveShortcutOpensConfirmForPendingEdit(t *testing.T) {
	state := editorStateFor(t)
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver)
	m = resize(m, 120, 30)
	// No working copy exists; only the pending edit is set.
	m.pendingEdit = &pendingEdit{
		path:         "config/sites/a.caddy",
		original:     []byte("a.example.test {\n\trespond ok\n}\n"),
		content:      []byte("a.example.test {\n\trespond ok\n\tencode gzip\n}\n"),
		snapshotPath: "snap-1",
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd != nil {
		t.Errorf("s returned a command, want none (confirmation is a modal, not a cmd)")
	}
	if !m.showSaveConfirm {
		t.Fatal("showSaveConfirm = false, want true: a pending edit saves regardless of the root working copy")
	}
}

// TestEditorKey_DisabledWhileReloading verifies that the e command is
// ignored while a reload is in flight, mirroring the save/reload mutual
// exclusion.
func TestEditorKey_DisabledWhileReloading(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	editor := &fakeEditor{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // select the node row
	m.reloading = true
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd != nil {
		t.Error("e while reloading returned a command, want none")
	}
	if editor.prepareCalls != 0 {
		t.Errorf("Prepare called %d times while reloading, want 0", editor.prepareCalls)
	}
	if m.editing {
		t.Error("editing = true, want false")
	}
}

// TestEditorFlow_WarningsOnlyNotSaved verifies that an edit whose only
// diagnostics are warnings (no errors) is not savable and surfaces a
// warnings status instead of an empty modal, mirroring the
// format+validate flow.
func TestEditorFlow_WarningsOnlyNotSaved(t *testing.T) {
	state := editorStateFor(t)
	diags := []validator.Diagnostic{
		{Path: "config/sites/a.caddy", Line: 1, Message: "deprecated directive", Severity: validator.SeverityWarning},
	}
	editor := &fakeEditor{result: app.EditResult{
		Original:    []byte("a.example.test {\n\trespond ok\n}\n"),
		Diagnostics: diags,
		Changed:     true,
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	done := pressEditorKey(t, m)
	m.Update(done)
	if m.showDiagnostics {
		t.Error("showDiagnostics = true for warning-only findings, want false")
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit set for a warnings-only edit, want nil (not savable)")
	}
	if !strings.Contains(m.statusMessage, "warnings") {
		t.Errorf("statusMessage = %q, want a warnings hint", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestModelSave_KeepsSourceRevealed is a regression test for the source
// pane jumping back to the top after a save: handleSaveResult used to
// reset the source pane via sourceDoc = nil, which reloaded the content
// but never re-revealed the still-selected node, so the viewport stayed
// pinned at the top. The fix sets a one-shot sourceRefresh flag that
// forces a content reload plus a re-reveal on the next render.
func TestModelSave_KeepsSourceRevealed(t *testing.T) {
	importedSrc := "top.example.test {\n\trespond top\n}\n" +
		strings.Repeat("# padding\n", 70) +
		"target.example.test {\n\trespond target\n}\n"
	editedSrc := "top.example.test {\n\trespond top\n}\n" +
		strings.Repeat("# padding\n", 70) +
		"target.example.test {\n\trespond edited\n}\n"
	fs := map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}
	// The loader reads through the mutable fs so the post-save structural
	// refresh picks up the content the diskSaver wrote.
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(importedSrc),
		Content:      []byte(editedSrc),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/a.caddy.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	// A short window so the target node (line 74) starts below the first
	// screen: a correct reveal produces YOffset > 0, while the bug leaves
	// the viewport pinned at the top.
	m = resize(m, 120, 30)

	// items: root doc, a.caddy doc, top.example.test, target.example.test.
	if len(m.items) != 4 {
		t.Fatalf("items = %d, want 4", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // top.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // target.example.test
	if !m.selectedItem().hasNode {
		t.Fatal("precondition: the target node must be selected")
	}
	// Render: the reveal scrolls the source pane onto the selected node.
	m.View()
	if m.viewport.YOffset == 0 {
		t.Fatalf("precondition: reveal must scroll to the target node, YOffset = %d", m.viewport.YOffset)
	}

	// Complete the editor round-trip and save it. Enter in the edit diff
	// saves directly — the diff is the single confirmation for an editor
	// edit.
	done := pressEditorKey(t, m)
	m.Update(done) // the edit diff opens
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	updated, _ = m.Update(res)
	m = updated.(*Model)

	// After the save the source pane must stay on the edited section: the
	// document bytes were replaced in place, but the selected node is
	// re-revealed instead of the viewport being reset to the top.
	m.View()
	if m.viewport.YOffset == 0 {
		t.Errorf("viewport YOffset = 0 after save, want it to stay revealed on the edited section")
	}
	if string(m.state.Graph.Documents[1].Source) != editedSrc {
		t.Errorf("imported doc Source = %q, want the saved bytes", m.state.Graph.Documents[1].Source)
	}
}

// TestSearchKey_OpensWithSlashAndCtrlF verifies that both / and Ctrl-F
// open the read-only search modal with an empty query and no results.
func TestSearchKey_OpensWithSlashAndCtrlF(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}) // default searcher
	m = resize(m, 120, 30)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated.(*Model)
	if cmd != nil {
		t.Errorf("/ returned a command, want none")
	}
	if !m.searchActive {
		t.Fatal("searchActive = false after /, want true")
	}
	if len(m.searchQuery) != 0 || len(m.searchResults) != 0 || m.searchCursor != 0 {
		t.Errorf("search state not reset on open: query=%q results=%d cursor=%d", m.searchQuery, len(m.searchResults), m.searchCursor)
	}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.searchActive {
		t.Fatal("searchActive = true after Esc, want false")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	m = updated.(*Model)
	if !m.searchActive {
		t.Fatal("searchActive = false after ctrl+f, want true")
	}
}

// TestSearch_DisabledWithoutSearcher verifies that a nil searcher surfaces
// a status hint and never activates the modal.
func TestSearch_DisabledWithoutSearcher(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModelWithoutSearcher(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if cmd != nil {
		t.Errorf("/ returned a command without a searcher, want none")
	}
	if m.searchActive {
		t.Error("searchActive = true without a searcher, want false")
	}
	if !strings.Contains(m.statusMessage, "search unavailable") {
		t.Errorf("statusMessage = %q, want a search-unavailable hint", m.statusMessage)
	}
}

// TestSearch_TypingFilters verifies that typing runes recomputes the
// results, backspace recomputes with the shortened query, and an empty
// query yields no results.
func TestSearch_TypingFilters(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if len(m.searchResults) == 0 {
		t.Fatal("typing 'e' produced no results, want the example.test hits")
	}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if string(m.searchQuery) != "ex" {
		t.Errorf("searchQuery = %q, want %q", m.searchQuery, "ex")
	}
	if len(m.searchResults) == 0 {
		t.Fatal("query 'ex' produced no results")
	}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if string(m.searchQuery) != "e" {
		t.Errorf("searchQuery = %q after backspace, want %q", m.searchQuery, "e")
	}
	if len(m.searchResults) == 0 {
		t.Fatal("backspace to 'e' produced no results")
	}

	// Backspace down to an empty query: no results, no crash.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if len(m.searchQuery) != 0 {
		t.Errorf("searchQuery = %q, want empty after the final backspace", m.searchQuery)
	}
	if len(m.searchResults) != 0 {
		t.Errorf("searchResults = %d with an empty query, want 0", len(m.searchResults))
	}
}

// TestSearch_Navigation verifies that up/down/k/j move the result cursor
// (clamped) and PgUp/PgDown page the result viewport.
func TestSearch_Navigation(t *testing.T) {
	src := "example.test {\n" + strings.Repeat("\trespond hit\n", 40) + "}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("hit") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.searchResults) < 10 {
		t.Fatalf("precondition: %d results, want enough to scroll", len(m.searchResults))
	}
	m.View() // render sizes the search viewport and loads the content

	// Arrow keys move the cursor (vim j/k are ordinary runes now and
	// would be typed into the query, not navigated).
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.searchCursor != 1 {
		t.Errorf("searchCursor = %d after down, want 1", m.searchCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.searchCursor != 2 {
		t.Errorf("searchCursor = %d after second down, want 2", m.searchCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.searchCursor != 1 {
		t.Errorf("searchCursor = %d after up, want 1", m.searchCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.searchCursor != 0 {
		t.Errorf("searchCursor = %d after second up, want 0", m.searchCursor)
	}
	// Clamp at the top.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.searchCursor != 0 {
		t.Errorf("searchCursor = %d after clamping at the top, want 0", m.searchCursor)
	}

	// PgDown advances the viewport scroll, PgUp retreats it.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	afterPgDown := m.searchViewport.YOffset
	if afterPgDown <= 0 {
		t.Errorf("PgDown did not scroll the search viewport: YOffset = %d", afterPgDown)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.searchViewport.YOffset >= afterPgDown {
		t.Errorf("PgUp did not retreat the search viewport: %d -> %d", afterPgDown, m.searchViewport.YOffset)
	}
}

// TestSearch_EnterNodeSelectsAndReveals verifies that activating a node hit
// re-anchors the tree cursor on that node and the source pane reveals it.
func TestSearch_EnterNodeSelectsAndReveals(t *testing.T) {
	src := "top.example.test {\n\trespond top\n}\n" +
		strings.Repeat("# padding\n", 70) +
		"target.example.test {\n\trespond target\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("target") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Results: root content lines 74 and 76 (SearchDocument) then the
	// target.example.test node label (SearchNode). Move to the node hit.
	if len(m.searchResults) < 3 {
		t.Fatalf("precondition: %d results, want the node hit present", len(m.searchResults))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // second content hit
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // the SearchNode result
	if m.searchResults[m.searchCursor].Kind != app.SearchNode {
		t.Fatalf("cursor result kind = %v, want SearchNode", m.searchResults[m.searchCursor].Kind)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.searchActive {
		t.Error("searchActive = true after Enter, want false")
	}
	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.node.Name != "target.example.test" {
		t.Errorf("selection = %+v, want the target.example.test node row", sel)
	}
	// The render reveals the node range (line 74, below the first screen).
	m.View()
	if m.viewport.YOffset == 0 {
		t.Errorf("viewport YOffset = 0, want the node reveal scrolled past the top")
	}
	if !strings.Contains(stripANSI(m.viewport.View()), "target.example.test {") {
		t.Errorf("source pane does not show the revealed node:\n%s", m.viewport.View())
	}
}

// TestSearch_EnterDocumentSelectsAndRevealsLine verifies that activating a
// document content hit selects the document row and reveals the exact
// 1-based line in the source pane.
func TestSearch_EnterDocumentSelectsAndRevealsLine(t *testing.T) {
	importedSrc := "top.example.test {\n\trespond top\n}\n" +
		strings.Repeat("# padding\n", 70) +
		"target.example.test {\n\trespond target\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("respond target") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.searchResults) != 1 {
		t.Fatalf("results = %d, want exactly the imported-file content hit", len(m.searchResults))
	}
	if m.searchResults[0].Doc == nil || m.searchResults[0].Doc.Path != "config/sites/a.caddy" {
		t.Fatalf("hit Doc = %v, want the imported file", m.searchResults[0].Doc)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	sel := m.selectedItem()
	if sel == nil || sel.hasNode || sel.doc == nil || sel.doc.Path != "config/sites/a.caddy" {
		t.Errorf("selection = %+v, want the imported document row", sel)
	}
	if m.sourceRevealLine == 0 {
		t.Error("sourceRevealLine = 0, want the hit line pending reveal")
	}
	// The render consumes the one-shot reveal and positions the viewport
	// at the clamped line offset.
	m.View()
	if m.sourceRevealLine != 0 {
		t.Errorf("sourceRevealLine = %d after render, want it consumed", m.sourceRevealLine)
	}
	if m.viewport.YOffset == 0 {
		t.Errorf("viewport YOffset = 0, want the hit line revealed")
	}
	if !strings.Contains(stripANSI(m.viewport.View()), "respond target") {
		t.Errorf("source pane does not show the revealed line:\n%s", m.viewport.View())
	}
}

// TestSearch_EnterLogOpensDetail verifies that activating a log hit opens
// the log view with the detail modal for the matching entry.
func TestSearch_EnterLogOpensDetail(t *testing.T) {
	state := logStateFor(t)
	entry := logEntry("handled request from search")
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{entry} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("from search") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.searchResults) != 1 {
		t.Fatalf("results = %d, want the log hit", len(m.searchResults))
	}
	if m.searchResults[0].Kind != app.SearchLog || m.searchResults[0].LogIndex != 0 {
		t.Fatalf("hit = %+v, want a SearchLog on entry 0", m.searchResults[0])
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.showLogs {
		t.Fatal("showLogs = false after activating a log hit, want true")
	}
	if m.logCursor != 0 {
		t.Errorf("logCursor = %d, want 0", m.logCursor)
	}
	if !m.logDetailOpen {
		t.Error("logDetailOpen = false, want true")
	}
	if string(m.logDetailEntry.Raw) != string(entry.Raw) {
		t.Errorf("logDetailEntry.Raw = %q, want %q", m.logDetailEntry.Raw, entry.Raw)
	}
	if m.logFollow {
		t.Error("logFollow = true after a search jump, want false")
	}
}

// TestSearch_EscClosesWithoutChanges verifies that closing the search modal
// leaves the selection and the log state exactly as they were before.
func TestSearch_EscClosesWithoutChanges(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}) // no log source: no seeding
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test node
	beforeCursor := m.cursor

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("example") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.searchActive {
		t.Error("searchActive = true after Esc, want false")
	}
	if len(m.searchQuery) != 0 || len(m.searchResults) != 0 {
		t.Errorf("search state not cleared after Esc: query=%q results=%d", m.searchQuery, len(m.searchResults))
	}
	if m.cursor != beforeCursor {
		t.Errorf("cursor = %d after Esc, want the untouched %d", m.cursor, beforeCursor)
	}
	if m.showLogs || m.showDiff || m.showSaveConfirm || m.showDiagnostics || m.editing {
		t.Error("a modal or the log view opened while searching/closed search, want nothing changed")
	}
}

// TestSearch_AvailableReadOnly verifies that search works in read-only
// mode (unlike e/s which are gated on writable mode).
func TestSearch_AvailableReadOnly(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	if !state.Settings.ReadOnly {
		t.Fatal("precondition: fixture must be read-only")
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated.(*Model)
	if cmd != nil {
		t.Errorf("/ returned a command, want none")
	}
	if !m.searchActive {
		t.Fatal("searchActive = false in read-only mode, want true (search is read-only)")
	}
}

// TestSearch_DoesNotInterfere verifies that while the search modal is
// active the v/s/D/r/l/e bindings are inert, and that they resume once the
// search closes.
func TestSearch_DoesNotInterfere(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("x")}
	saver := &fakeSaver{}
	logSrc := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver, logSrc)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})

	for _, key := range []rune{'v', 's', 'D', 'r', 'l', 'e'} {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
	}
	if formatter.calls != 0 {
		t.Errorf("formatter.calls = %d while searching, want 0 (v must be inert)", formatter.calls)
	}
	if m.busy || m.saving || m.editing {
		t.Error("busy/saving/editing set while searching, want all false")
	}
	if m.showDiff || m.showSaveConfirm || m.showReloadConfirm || m.showLogs {
		t.Error("a workflow opened while searching, want none")
	}

	// After Esc the bindings resume. v first (the log view opened by l
	// would otherwise swallow the next key).
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("v must return a command after search closes")
	}
	if !m.busy {
		t.Error("busy = false after v, want true")
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if !m.showLogs {
		t.Fatal("l must open the log view after search closes")
	}
	if cmd == nil {
		t.Error("l returned no poll command after search closes")
	}
}

// TestSearch_FooterShowsKey verifies that the main footer lists / search
// only when a searcher is wired and that the search-active footer shows
// the search keys.
func TestSearch_FooterShowsKey(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))

	// With the default searcher the key is listed.
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	if !strings.Contains(stripANSI(m.View()), "/ search") {
		t.Errorf("footer missing '/ search' with a searcher:\n%s", m.View())
	}

	// Without a searcher the key is absent.
	m2 := newLoadedModelWithoutSearcher(t, fakeLoader{state: state})
	m2 = resize(m2, 120, 30)
	if strings.Contains(stripANSI(m2.View()), "/ search") {
		t.Errorf("footer shows '/ search' without a searcher:\n%s", m2.View())
	}

	// While the search modal is open the footer shows the search keys.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	view := stripANSI(m.View())
	for _, want := range []string{"type to search", "Enter open", "Esc close"} {
		if !strings.Contains(view, want) {
			t.Errorf("search footer missing %q:\n%s", want, view)
		}
	}
}

// TestSearch_QueryAcceptsOrdinaryRunes verifies that ordinary characters —
// including q, j, k and space, which used to be named keys — always
// accumulate into the query instead of closing the modal or moving the
// cursor, so words containing them are searchable.
func TestSearch_QueryAcceptsOrdinaryRunes(t *testing.T) {
	src := "query.example.test {\n\trespond ok\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})

	// Type q, j, k and a space: all must land in the query and the modal
	// must stay open.
	for _, r := range []rune{'q', 'j', 'k', ' '} {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if !m.searchActive {
			t.Fatalf("search modal closed while typing %q", r)
		}
	}
	if got := string(m.searchQuery); got != "qjk " {
		t.Errorf("searchQuery = %q, want %q", got, "qjk ")
	}

	// Clear the buffer, then run a real search containing q/j/k: the node
	// hit must be found (without the fix the leading q would have closed
	// the modal).
	for i := 0; i < len([]rune("qjk ")); i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if len(m.searchQuery) != 0 {
		t.Fatalf("searchQuery = %q, want empty after backspaces", m.searchQuery)
	}
	for _, r := range []rune("query.example") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	found := false
	for _, r := range m.searchResults {
		if r.Kind == app.SearchNode && r.Node.Name == "query.example.test" {
			found = true
		}
	}
	if !found {
		t.Errorf("results = %+v, want the query.example.test node hit", m.searchResults)
	}
}

// TestSearch_CollapsedDocumentStillSearched verifies that a global search
// covers the nodes of a collapsed document and that activating such a hit
// expands the document, rebuilds the tree and reveals the node.
func TestSearch_CollapsedDocumentStillSearched(t *testing.T) {
	importedSrc := "top.example.test {\n\trespond top\n}\n" +
		strings.Repeat("# padding\n", 70) +
		"target.example.test {\n\trespond target\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	// Collapse the imported document: its node rows disappear from the
	// visible tree.
	m.collapsed["config/sites/a.caddy"] = true
	m.items = buildItems(m.state.Graph, m.collapsed)
	if len(m.items) != 2 {
		t.Fatalf("items = %d, want 2 (both document rows, no node rows)", len(m.items))
	}

	// A global search must still find the site inside the collapsed file.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("target") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	nodeIdx := -1
	for i, r := range m.searchResults {
		if r.Kind == app.SearchNode && r.Node.Name == "target.example.test" {
			nodeIdx = i
		}
	}
	if nodeIdx < 0 {
		t.Fatalf("results = %+v, want the node hit of the collapsed document", m.searchResults)
	}
	for m.searchCursor != nodeIdx {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	// The document was expanded so the node row exists and is selected.
	if m.collapsed["config/sites/a.caddy"] {
		t.Error("document still collapsed after activating its node hit")
	}
	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.node.Name != "target.example.test" {
		t.Errorf("selection = %+v, want the target.example.test node row", sel)
	}
	// The render reveals the node range (line 74, below the first screen).
	m.View()
	if m.viewport.YOffset == 0 {
		t.Errorf("viewport YOffset = 0, want the node reveal")
	}
	if !strings.Contains(stripANSI(m.viewport.View()), "target.example.test {") {
		t.Errorf("source pane does not show the revealed node:\n%s", m.viewport.View())
	}
}

// itemLabels returns the labels of all items joined, for readable test
// failures.
func itemLabels(items []item) string {
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.label
	}
	return strings.Join(labels, ", ")
}

// runEditorSave drives the full editor round-trip to a delivered saveResultMsg
// against the model under test and returns it for the caller to deliver.
func runEditorSave(t *testing.T, m *Model) saveResultMsg {
	t.Helper()
	done := pressEditorKey(t, m)
	m.Update(done) // the edit diff opens
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	return res
}

// TestEditorSave_AddsSiteToRoot verifies that after a successful editor
// save that adds a site to the root document, the tree is rebuilt with the
// new node row, the graph source carries the new site, the selection is
// re-anchored and the source pane shows the new content. Without the fix
// the new node never appears in m.items.
func TestEditorSave_AddsSiteToRoot(t *testing.T) {
	fs := map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond ok\n}\n",
	}
	edited := "a.example.test {\n\trespond ok\n}\nnew.example.test {\n\trespond new\n}\n"
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(fs["config/Caddyfile"]),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	// items: root doc, a.example.test node.
	if len(m.items) != 2 {
		t.Fatalf("items = %d, want 2", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node
	if !m.selectedItem().hasNode {
		t.Fatal("precondition: the node row must be selected")
	}

	res := runEditorSave(t, m)
	updated, _ := m.Update(res)
	m = updated.(*Model)

	// The tree now contains the new site row.
	if len(m.items) != 3 {
		t.Errorf("items = %d after save, want 3 (root + a + new); items = %v", len(m.items), itemLabels(m.items))
	}
	foundNew := false
	for _, it := range m.items {
		if it.hasNode && it.node.Name == "new.example.test" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Errorf("tree missing the new site row; items = %v", itemLabels(m.items))
	}
	// The graph source carries the new site.
	if !strings.Contains(string(m.state.Graph.Root.Source), "new.example.test") {
		t.Errorf("root Source = %q, want it to contain the new site", m.state.Graph.Root.Source)
	}
	// The selection is re-anchored on the edited node.
	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.node.Name != "a.example.test" {
		t.Errorf("selection = %+v, want the edited node row", sel)
	}
	// After a render the source pane shows the new structure.
	m.View()
	if !strings.Contains(stripANSI(m.viewport.View()), "new.example.test") {
		t.Errorf("source pane does not show the new site:\n%s", m.viewport.View())
	}
}

// TestEditorSave_AddsSectionToImported verifies that a successful editor
// save that adds a section to an imported file rebuilds the tree with the
// new node of the imported document, keeps the root intact and shows the
// new content in the source pane.
func TestEditorSave_AddsSectionToImported(t *testing.T) {
	importedSrc := "a.example.test {\n\trespond ok\n}\n"
	edited := "a.example.test {\n\trespond ok\n}\nb.example.test {\n\trespond b\n}\n"
	fs := map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(importedSrc),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/a.caddy.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	// items: root doc, a.caddy doc, a.example.test node.
	if len(m.items) != 3 {
		t.Fatalf("items = %d, want 3", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy document row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node

	res := runEditorSave(t, m)
	updated, _ := m.Update(res)
	m = updated.(*Model)

	// The tree contains the new site of the imported file.
	if len(m.items) != 4 {
		t.Errorf("items = %d after save, want 4 (root + a.caddy + a + b); items = %v", len(m.items), itemLabels(m.items))
	}
	foundNew := false
	for _, it := range m.items {
		if it.hasNode && it.node.Name == "b.example.test" && it.doc != nil && it.doc.Path == "config/sites/a.caddy" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Errorf("tree missing the new imported site; items = %v", itemLabels(m.items))
	}
	// The imported document carries the new content and the root is intact.
	var importedDoc *caddyfile.Document
	for _, d := range m.state.Graph.Documents {
		if d.Path == "config/sites/a.caddy" {
			importedDoc = d
		}
	}
	if importedDoc == nil {
		t.Fatal("imported document not found after the reload")
	}
	if string(importedDoc.Source) != edited {
		t.Errorf("imported doc Source = %q, want %q", importedDoc.Source, edited)
	}
	if string(m.state.Graph.Root.Source) != "import sites/a.caddy\n" {
		t.Errorf("root Source = %q, want it untouched", m.state.Graph.Root.Source)
	}
	// After a render the source pane shows the new structure.
	m.View()
	if !strings.Contains(stripANSI(m.viewport.View()), "b.example.test") {
		t.Errorf("source pane does not show the new site:\n%s", m.viewport.View())
	}
}

// TestEditorSave_TreeAndSourceUpdated pins the post-save structural sync
// explicitly: both the item count and the rendered source pane must reflect
// the new tree after an editor save that adds a section to the root.
func TestEditorSave_TreeAndSourceUpdated(t *testing.T) {
	fs := map[string]string{
		"config/Caddyfile": "one.example.test {\n\trespond one\n}\n",
	}
	edited := "one.example.test {\n\trespond one\n}\ntwo.example.test {\n\trespond two\n}\n"
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(fs["config/Caddyfile"]),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // one.example.test node

	before := len(m.items)
	if before != 2 {
		t.Fatalf("items = %d before save, want 2", before)
	}
	res := runEditorSave(t, m)
	updated, _ := m.Update(res)
	m = updated.(*Model)

	if len(m.items) != before+1 {
		t.Errorf("items = %d after save, want %d (one more node row)", len(m.items), before+1)
	}
	m.View()
	visible := stripANSI(m.viewport.View())
	if !strings.Contains(visible, "one.example.test") || !strings.Contains(visible, "two.example.test") {
		t.Errorf("source pane does not show the full new structure:\n%s", visible)
	}
}

// pressFullEditorKey drives the full-document editor round-trip from the E
// keypress through the delivered editorDoneMsg, assuming a clean editor
// exit with code 0. It mutates m in place.
func pressFullEditorKey(t *testing.T, m *Model) editorDoneMsg {
	t.Helper()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	if cmd == nil {
		t.Fatal("E must return a command")
	}
	msg := cmd()
	ready, ok := msg.(editorReadyMsg)
	if !ok {
		t.Fatalf("got %T, want editorReadyMsg", msg)
	}
	m.Update(ready)
	updated, cmd := m.Update(editorExecMsg{Err: nil})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("editorExecMsg must return the complete command")
	}
	doneMsg := cmd()
	done, ok := doneMsg.(editorDoneMsg)
	if !ok {
		t.Fatalf("got %T, want editorDoneMsg", doneMsg)
	}
	return done
}

// runFullEditorSave drives the full-document editor round-trip to a
// delivered saveResultMsg against the model under test and returns it for
// the caller to deliver.
func runFullEditorSave(t *testing.T, m *Model) saveResultMsg {
	t.Helper()
	done := pressFullEditorKey(t, m)
	m.Update(done) // the edit diff opens
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	return res
}

// TestEditorFullKey_EditsRoot verifies that E on the root document row
// prepares a full-document edit and saves it through the normal pipeline.
func TestEditorFullKey_EditsRoot(t *testing.T) {
	src := "example.test {\n\trespond ok\n}\n"
	edited := "example.test {\n\trespond ok\n\tencode gzip\n}\n"
	fs := map[string]string{"config/Caddyfile": src}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(src),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	done := pressFullEditorKey(t, m)
	m.Update(done) // the edit diff opens
	if editor.prepareFullCalls != 1 {
		t.Errorf("PrepareFull calls = %d, want 1", editor.prepareFullCalls)
	}
	if editor.capturedFullDoc == nil || editor.capturedFullDoc.Path != "config/Caddyfile" {
		t.Errorf("PrepareFull doc = %v, want the root document", editor.capturedFullDoc)
	}
	if !m.showDiff {
		t.Fatal("showDiff = false after a valid full edit, want true")
	}
	if m.pendingEdit == nil || m.pendingEdit.path != "config/Caddyfile" {
		t.Fatalf("pendingEdit = %+v, want the root path", m.pendingEdit)
	}

	// Enter saves directly to the root document.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	if saver.capturedPath != "config/Caddyfile" {
		t.Errorf("saver path = %q, want the root", saver.capturedPath)
	}
	if string(saver.capturedOriginal) != src {
		t.Errorf("saver original = %q, want %q", saver.capturedOriginal, src)
	}
	if string(saver.capturedWorking) != edited {
		t.Errorf("saver working = %q, want %q", saver.capturedWorking, edited)
	}
	updated, _ = m.Update(res)
	m = updated.(*Model)
	if string(m.state.Graph.Root.Source) != edited {
		t.Errorf("root Source = %q, want %q", m.state.Graph.Root.Source, edited)
	}
}

// TestEditorFullKey_EditsImportedFile verifies that E on an imported
// document row edits that file, leaving the root untouched.
func TestEditorFullKey_EditsImportedFile(t *testing.T) {
	importedSrc := "a.example.test {\n\trespond ok\n}\n"
	edited := "a.example.test {\n\trespond ok\n\tencode gzip\n}\n"
	fs := map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(importedSrc),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/a.caddy.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	// items: root doc, a.caddy doc, a.example.test node. Select the
	// imported document row.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	sel := m.selectedItem()
	if sel == nil || sel.hasNode || sel.doc == nil || sel.doc.Path != "config/sites/a.caddy" {
		t.Fatalf("selection = %+v, want the imported document row", sel)
	}

	done := pressFullEditorKey(t, m)
	m.Update(done)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	if saver.capturedPath != "config/sites/a.caddy" {
		t.Errorf("saver path = %q, want the imported file", saver.capturedPath)
	}
	updated, _ = m.Update(res)
	m = updated.(*Model)
	var importedDoc *caddyfile.Document
	for _, d := range m.state.Graph.Documents {
		if d.Path == "config/sites/a.caddy" {
			importedDoc = d
		}
	}
	if importedDoc == nil || string(importedDoc.Source) != edited {
		t.Errorf("imported doc Source = %q, want %q", importedDoc.Source, edited)
	}
	if string(m.state.Graph.Root.Source) != "import sites/a.caddy\n" {
		t.Errorf("root Source = %q, want it untouched", m.state.Graph.Root.Source)
	}
}

// TestEditorFull_EditsCommentsOutsideBlocks verifies that a full-document
// edit can change comments that live outside any block.
func TestEditorFull_EditsCommentsOutsideBlocks(t *testing.T) {
	src := "# header comment\n\nexample.test {\n\trespond ok\n}\n\n# footer comment\n"
	edited := "# edited header comment\n\nexample.test {\n\trespond ok\n}\n\n# footer comment\n"
	fs := map[string]string{"config/Caddyfile": src}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(src),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	res := runFullEditorSave(t, m)
	updated, _ := m.Update(res)
	m = updated.(*Model)
	if string(m.state.Graph.Root.Source) != edited {
		t.Errorf("root Source = %q, want the edited comment content %q", m.state.Graph.Root.Source, edited)
	}
}

// TestEditorFull_EmptyResultGoesThrough verifies that an empty full-file
// edit is NOT a cancellation: it reaches the diff and is savable.
func TestEditorFull_EmptyResultGoesThrough(t *testing.T) {
	src := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(src),
		Content:      []byte{},
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)

	done := pressFullEditorKey(t, m)
	m.Update(done)
	if m.showDiff == false {
		t.Fatal("empty full edit must reach the diff, not be cancelled")
	}
	if m.pendingEdit == nil {
		t.Fatal("pendingEdit not set for an empty full edit")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter must return the save command")
	}
	cmd()
	if len(saver.capturedWorking) != 0 {
		t.Errorf("saver working = %q, want the empty document", saver.capturedWorking)
	}
}

// TestEditorFull_InvalidShowsDiagnostics verifies that an invalid full
// edit opens the diagnostics modal and is never savable.
func TestEditorFull_InvalidShowsDiagnostics(t *testing.T) {
	src := "example.test {\n\trespond ok\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	editor := &fakeEditor{result: app.EditResult{
		Original:    []byte(src),
		Diagnostics: diags,
		Changed:     true,
	}}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)

	done := pressFullEditorKey(t, m)
	m.Update(done)
	if !m.showDiagnostics {
		t.Fatal("showDiagnostics = false, want true for an invalid full edit")
	}
	if m.showDiff {
		t.Error("showDiff = true for an invalid full edit, want false")
	}
	if m.pendingEdit != nil {
		t.Error("pendingEdit set for an invalid full edit, want nil")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestEditorFull_AfterSaveRebuildsTree verifies that after a structural
// full-document save the tree is rebuilt with the new site and the source
// pane shows it.
func TestEditorFull_AfterSaveRebuildsTree(t *testing.T) {
	fs := map[string]string{"config/Caddyfile": "a.example.test {\n\trespond a\n}\n"}
	edited := "a.example.test {\n\trespond a\n}\nnew.example.test {\n\trespond new\n}\n"
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(fs["config/Caddyfile"]),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)

	res := runFullEditorSave(t, m)
	updated, _ := m.Update(res)
	m = updated.(*Model)

	if len(m.items) != 3 {
		t.Errorf("items = %d after save, want 3 (root + a + new); items = %v", len(m.items), itemLabels(m.items))
	}
	foundNew := false
	for _, it := range m.items {
		if it.hasNode && it.node.Name == "new.example.test" {
			foundNew = true
		}
	}
	if !foundNew {
		t.Errorf("tree missing the new site; items = %v", itemLabels(m.items))
	}
	m.View()
	if !strings.Contains(stripANSI(m.viewport.View()), "new.example.test") {
		t.Errorf("source pane does not show the new site:\n%s", m.viewport.View())
	}
}

// TestDeleteKey_DisabledOnDocumentRow verifies that d on a document row is
// a no-op: delete is a node operation only.
func TestDeleteKey_DisabledOnDocumentRow(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\n",
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	if m.selectedItem().hasNode {
		t.Fatal("precondition: the document row must be selected")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd != nil {
		t.Errorf("d on a document row returned a command, want none")
	}
	if m.pendingDelete != nil || m.showDiff {
		t.Error("delete state set on a document row, want untouched")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestDelete_ImportDirectiveRejected verifies the defensive guard: an
// import directive can never be deleted. The tree never renders import
// rows, so the selection is set directly to exercise the guard.
func TestDelete_ImportDirectiveRejected(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "import sites/a.caddy\n",
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	importNode := caddyfile.Node{
		Kind:  caddyfile.KindDirective,
		Name:  "import",
		Range: caddyfile.SourceRange{Start: 0, End: 18, StartLine: 1, EndLine: 1},
	}
	m.items = []item{
		{depth: 0, doc: m.state.Graph.Root},
		{depth: 1, doc: m.state.Graph.Root, node: importNode, hasNode: true},
	}
	m.cursor = 1
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd != nil {
		t.Errorf("d on an import directive returned a command, want none")
	}
	if !strings.Contains(m.statusMessage, "import directives cannot be deleted") {
		t.Errorf("statusMessage = %q, want the import guard hint", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestDelete_ValidShowsDiffAndSaves verifies the happy path: d removes the
// node range, the delete diff opens with an explicit title, and Enter
// saves the exact Patch(original, range, empty) result.
func TestDelete_ValidShowsDiffAndSaves(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test
	if !m.selectedItem().hasNode || m.selectedItem().node.Name != "b.example.test" {
		t.Fatalf("precondition: b.example.test node must be selected")
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd == nil {
		t.Fatal("d must return a command")
	}
	msg := cmd()
	vmsg, ok := msg.(deleteValidatedMsg)
	if !ok {
		t.Fatalf("got %T, want deleteValidatedMsg", msg)
	}
	if vmsg.Err != nil {
		t.Fatalf("delete validation error: %v", vmsg.Err)
	}
	m.Update(vmsg)
	if !m.showDiff {
		t.Fatal("diff not open after a valid delete")
	}
	if m.diffTitle != "Delete · config/Caddyfile" {
		t.Errorf("diffTitle = %q, want the explicit delete title", m.diffTitle)
	}
	if m.pendingDelete == nil {
		t.Fatal("pendingDelete not set")
	}
	// The diff footer advertises the delete confirmation.
	if !strings.Contains(stripANSI(m.View()), "Enter delete") {
		t.Errorf("delete diff footer missing 'Enter delete':\n%s", m.View())
	}
	expected, err := caddyfile.Patch([]byte(src), state.Graph.Root.Nodes[1].Range, []byte{})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	// Enter saves the deletion through the normal pipeline.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the delete diff must return a command")
	}
	resMsg := cmd()
	if _, ok := resMsg.(saveResultMsg); !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	if saver.capturedPath != "config/Caddyfile" {
		t.Errorf("saver path = %q, want the root", saver.capturedPath)
	}
	if string(saver.capturedOriginal) != src {
		t.Errorf("saver original = %q, want %q", saver.capturedOriginal, src)
	}
	if !bytes.Equal(saver.capturedWorking, expected) {
		t.Errorf("saver working = %q, want the node removed: %q", saver.capturedWorking, expected)
	}
}

// TestDelete_PreservesCommentsOutsideNode verifies that deleting a node
// preserves every byte outside its range, including surrounding comments.
func TestDelete_PreservesCommentsOutsideNode(t *testing.T) {
	src := "# header comment\n\nexample.test {\n\trespond ok\n}\n\n# footer comment\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // example.test node

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	msg := cmd()
	vmsg := msg.(deleteValidatedMsg)
	m.Update(vmsg)
	if !m.showDiff {
		t.Fatal("diff not open")
	}
	expected, err := caddyfile.Patch([]byte(src), state.Graph.Root.Nodes[0].Range, []byte{})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter must return the save command")
	}
	cmd()
	if !bytes.Equal(saver.capturedWorking, expected) {
		t.Errorf("saver working = %q, want comments preserved byte-for-byte: %q", saver.capturedWorking, expected)
	}
	if !strings.Contains(string(saver.capturedWorking), "# header comment") ||
		!strings.Contains(string(saver.capturedWorking), "# footer comment") {
		t.Errorf("saved content lost a surrounding comment: %q", saver.capturedWorking)
	}
}

// TestDelete_InvalidShowsDiagnostics verifies that a delete candidate that
// fails validation opens the diagnostics modal and never reaches the saver.
func TestDelete_InvalidShowsDiagnostics(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	msg := cmd()
	vmsg := msg.(deleteValidatedMsg)
	m.Update(vmsg)
	if !m.showDiagnostics {
		t.Fatal("showDiagnostics = false, want true for an invalid delete")
	}
	if m.showDiff {
		t.Error("showDiff = true for an invalid delete, want false")
	}
	if m.pendingDelete != nil {
		t.Error("pendingDelete set for an invalid delete, want nil")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestDelete_EscCancels verifies that Esc from the delete diff cancels the
// deletion without touching the saver.
func TestDelete_EscCancels(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	msg := cmd()
	m.Update(msg.(deleteValidatedMsg))
	if !m.showDiff {
		t.Fatal("delete diff not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showDiff {
		t.Error("showDiff = true after Esc, want false")
	}
	if m.pendingDelete != nil {
		t.Error("pendingDelete not discarded after Esc, want nil")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
	if !strings.Contains(m.statusMessage, "delete cancelled") {
		t.Errorf("statusMessage = %q, want a delete-cancelled hint", m.statusMessage)
	}
}

// TestDelete_ImportedNode verifies that deleting a node of an imported
// file saves to that file and leaves the root intact.
func TestDelete_ImportedNode(t *testing.T) {
	importedSrc := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	fs := map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	formatter := &fakeFormatter{}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/a.caddy.bak"}}}
	m := newLoadedModel(t, loader, formatter, saver)
	m = resize(m, 120, 30)
	// items: root doc, a.caddy doc, a.example.test, b.example.test.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy doc
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test
	if !m.selectedItem().hasNode || m.selectedItem().node.Name != "b.example.test" {
		t.Fatalf("precondition: b.example.test of the imported file must be selected")
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	msg := cmd()
	m.Update(msg.(deleteValidatedMsg))
	if !m.showDiff {
		t.Fatal("delete diff not open")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter must return the save command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	if saver.capturedPath != "config/sites/a.caddy" {
		t.Errorf("saver path = %q, want the imported file", saver.capturedPath)
	}
	updated, _ = m.Update(res)
	m = updated.(*Model)
	var importedDoc *caddyfile.Document
	for _, d := range m.state.Graph.Documents {
		if d.Path == "config/sites/a.caddy" {
			importedDoc = d
		}
	}
	if importedDoc == nil || strings.Contains(string(importedDoc.Source), "b.example.test") {
		t.Errorf("imported doc Source = %q, want b.example.test removed", importedDoc.Source)
	}
	if string(m.state.Graph.Root.Source) != "import sites/a.caddy\n" {
		t.Errorf("root Source = %q, want it untouched", m.state.Graph.Root.Source)
	}
}

// TestDelete_AfterSaveTreeRebuilt verifies that a successful delete
// rebuilds the tree (one fewer node row), re-anchors the selection on the
// stable document row and updates the source pane.
func TestDelete_AfterSaveTreeRebuilt(t *testing.T) {
	fs := map[string]string{"config/Caddyfile": "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	formatter := &fakeFormatter{}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, formatter, saver)
	m = resize(m, 120, 30)
	before := len(m.items)
	if before != 3 {
		t.Fatalf("items = %d before delete, want 3", before)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	msg := cmd()
	m.Update(msg.(deleteValidatedMsg))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	updated, _ = m.Update(res)
	m = updated.(*Model)

	if len(m.items) != before-1 {
		t.Errorf("items = %d after delete, want %d; items = %v", len(m.items), before-1, itemLabels(m.items))
	}
	sel := m.selectedItem()
	if sel == nil || sel.hasNode || sel.doc == nil || sel.doc.Path != "config/Caddyfile" {
		t.Errorf("selection = %+v, want the stable document row after delete", sel)
	}
	m.View()
	if strings.Contains(stripANSI(m.viewport.View()), "b.example.test") {
		t.Errorf("source pane still shows the deleted node:\n%s", m.viewport.View())
	}
}

// TestSecondNodeEditAfterStructuralAdd is a stale-range regression: after a
// structural full-document save rebuilds the tree, a subsequent node edit
// must target the freshly reloaded range, not the pre-edit one.
func TestSecondNodeEditAfterStructuralAdd(t *testing.T) {
	fs := map[string]string{"config/Caddyfile": "a.example.test {\n\trespond a\n}\n"}
	edited := "a.example.test {\n\trespond a\n}\nnew.example.test {\n\trespond new\n}\n"
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	editor := &fakeEditor{result: app.EditResult{
		Original:     []byte(fs["config/Caddyfile"]),
		Content:      []byte(edited),
		Changed:      true,
		SnapshotPath: "snap-1",
	}}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, saver, editor)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test node

	// First: E adds a site and saves; the refresh rebuilds the tree.
	res := runFullEditorSave(t, m)
	updated, _ := m.Update(res)
	m = updated.(*Model)
	if len(m.items) != 3 {
		t.Fatalf("items = %d after E save, want 3; items = %v", len(m.items), itemLabels(m.items))
	}

	// Select the newly added node and edit it with e: the captured range
	// must come from the reloaded graph.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // new.example.test
	freshRange := m.state.Graph.Root.Nodes[1].Range
	editor.result = app.EditResult{
		Original:     []byte(edited),
		Content:      []byte(edited + "third.example.test {\n\trespond third\n}\n"),
		Changed:      true,
		SnapshotPath: "snap-2",
	}
	done := pressEditorKey(t, m)
	m.Update(done) // the edit diff opens
	if editor.capturedRange != freshRange {
		t.Errorf("capturedRange = %+v, want the fresh range %+v (stale ranges would fail)", editor.capturedRange, freshRange)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the edit diff must return a command")
	}
	resMsg := cmd()
	res2, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	updated, _ = m.Update(res2)
	m = updated.(*Model)
	if !strings.Contains(string(m.state.Graph.Root.Source), "third.example.test") {
		t.Errorf("root Source = %q, want it to contain the third site", m.state.Graph.Root.Source)
	}
}

// TestEditorFull_FooterShowsKey verifies that the footer lists E full edit
// on document and node rows (when writable with an editor) and hides it in
// read-only mode; e edit stays node-only.
func TestEditorFull_FooterShowsKey(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\n",
	}))
	editor := &fakeEditor{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, editor)
	m = resize(m, 120, 30)
	// On the document row: E is shown, e is not.
	if !strings.Contains(stripANSI(m.View()), "E full edit") {
		t.Errorf("footer missing 'E full edit' on a document row:\n%s", m.View())
	}
	if strings.Contains(stripANSI(m.View()), "e edit") {
		t.Errorf("footer shows 'e edit' on a document row:\n%s", m.View())
	}
	// On a node row: both are shown.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(stripANSI(m.View()), "E full edit") || !strings.Contains(stripANSI(m.View()), "e edit") {
		t.Errorf("footer missing E/e edit on a node row:\n%s", m.View())
	}
	// Read-only: neither.
	readOnly := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\n",
	}))
	m2 := newLoadedModel(t, fakeLoader{state: readOnly}, editor)
	m2 = resize(m2, 120, 30)
	if strings.Contains(stripANSI(m2.View()), "E full edit") || strings.Contains(stripANSI(m2.View()), "e edit") {
		t.Errorf("footer shows edit keys in read-only mode:\n%s", m2.View())
	}
}

// TestDelete_FooterShowsKey verifies that the footer lists d delete only on
// node rows in writable mode.
func TestDelete_FooterShowsKey(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\n",
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	// On the document row the key is hidden.
	if strings.Contains(stripANSI(m.View()), "d delete") {
		t.Errorf("footer shows 'd delete' on a document row:\n%s", m.View())
	}
	// On a node row the key appears.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(stripANSI(m.View()), "d delete") {
		t.Errorf("footer missing 'd delete' on a node row:\n%s", m.View())
	}
	// Read-only: hidden even on a node row.
	readOnly := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\n",
	}))
	m2 := newLoadedModel(t, fakeLoader{state: readOnly}, formatter)
	m2 = resize(m2, 120, 30)
	m2 = keyPress(t, m2, tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(stripANSI(m2.View()), "d delete") {
		t.Errorf("footer shows 'd delete' in read-only mode:\n%s", m2.View())
	}
	// Without a formatter the key is hidden even on a writable node row:
	// the delete flow validates before the diff, so the key would fail at
	// the first press.
	saverOnly := &fakeSaver{}
	m3 := newLoadedModel(t, fakeLoader{state: state}, saverOnly)
	m3 = resize(m3, 120, 30)
	m3 = keyPress(t, m3, tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(stripANSI(m3.View()), "d delete") {
		t.Errorf("footer shows 'd delete' without a formatter:\n%s", m3.View())
	}
}

// TestDelete_DiagnosticsWithErrorOpensModal verifies that a delete whose
// validation returns both diagnostics and an error (the real
// FormatAndValidate contract when Caddy rejects the configuration) opens
// the diagnostics modal instead of only surfacing a status line.
func TestDelete_DiagnosticsWithErrorOpensModal(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	msg := cmd()
	vmsg := msg.(deleteValidatedMsg)
	m.Update(vmsg)
	if !m.showDiagnostics {
		t.Fatal("showDiagnostics = false, want true when validation returns diagnostics alongside an error")
	}
	if m.showDiff {
		t.Error("showDiff = true, want false (invalid delete, no diff)")
	}
	if m.pendingDelete != nil {
		t.Error("pendingDelete set for an invalid delete, want nil")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestDelete_SecondPressIgnoredWhileValidating verifies that a second d
// press while the first delete is still validating is ignored, so two
// concurrent validations cannot overwrite each other.
func TestDelete_SecondPressIgnoredWhileValidating(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd == nil {
		t.Fatal("first d must return a command")
	}
	if !m.deleting {
		t.Error("deleting = false after the first d, want true")
	}
	// A second d while validating is a no-op: no command, no extra call.
	_, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd2 != nil {
		t.Error("second d returned a command, want none")
	}
	msg := cmd() // runs FormatAndValidate exactly once
	if formatter.calls != 1 {
		t.Errorf("formatter.calls = %d, want 1 (the second d must not trigger a call)", formatter.calls)
	}
	vmsg, ok := msg.(deleteValidatedMsg)
	if !ok {
		t.Fatalf("got %T, want deleteValidatedMsg", msg)
	}
	updated, _ := m.Update(vmsg)
	m = updated.(*Model)
	if m.deleting {
		t.Error("deleting = true after the result, want false")
	}
	if !m.showDiff {
		t.Fatal("delete diff not open after the validated result")
	}
}
