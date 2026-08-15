package ui

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	tea "github.com/charmbracelet/bubbletea"
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
	session            *app.EditSession
	prepareErr         error
	result             app.EditResult
	completeErr        error
	prepareCalls       int
	prepareFullCalls   int
	prepareInsertCalls int
	completeCalls      int
	capturedDoc        *caddyfile.Document
	capturedRange      caddyfile.SourceRange
	capturedFullDoc    *caddyfile.Document
	capturedInsertPos  int
	capturedTemplate   string
	capturedExit       int
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

// PrepareInsert implements app.Editor for insertions: the session carries
// the zero-length range at pos and the template as its range bytes.
func (f *fakeEditor) PrepareInsert(ctx context.Context, doc *caddyfile.Document, pos int, template string) (*app.EditSession, error) {
	f.prepareInsertCalls++
	f.capturedDoc = doc
	f.capturedInsertPos = pos
	f.capturedTemplate = template
	if f.prepareErr != nil {
		return nil, f.prepareErr
	}
	if f.session != nil {
		return f.session, nil
	}
	return &app.EditSession{
		Mode:         app.EditNode,
		DocPath:      doc.Path,
		Range:        caddyfile.SourceRange{Start: pos, End: pos, StartLine: 1, EndLine: 1},
		Original:     append([]byte(nil), doc.Source...),
		RangeBytes:   []byte(template),
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

// press runs a message through the model and returns the command, so
// tests can assert that the watch was re-armed after a resolution.
func press(m *Model, msg tea.Msg) (*Model, tea.Cmd) {
	updated, cmd := m.Update(msg)
	return updated.(*Model), cmd
}

// ansiRe matches ANSI escape sequences emitted by lipgloss styles.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// stripANSI removes ANSI escape sequences so assertions can match the
// visible text of a rendered view. It is a no-op when the environment does
// not emit ANSI (non-TTY test runs).
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
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
	tail := strings.Join(lines[max(0, n-2):n], "\n")
	if !strings.Contains(tail, "quit") && !strings.Contains(tail, "Esc") && !strings.Contains(tail, "commands") && !strings.Contains(tail, "detail") && !strings.Contains(tail, "follow") {
		t.Errorf("footer is not the last section: %q", tail)
	}
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

// itemLabels returns the labels of all items joined, for readable test
// failures.
func itemLabels(items []item) string {
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.label
	}
	return strings.Join(labels, ", ")
}
