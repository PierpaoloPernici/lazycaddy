package ui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
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

// newLoadedModel builds a Model from loader with an optional
// formatter and saver. The variadic options accept either an
// app.Formatter, an app.Saver, both, or neither.
func newLoadedModel(t *testing.T, loader app.Loader, opts ...any) *Model {
	t.Helper()
	var f app.Formatter
	var s app.Saver
	for _, opt := range opts {
		switch v := opt.(type) {
		case app.Formatter:
			f = v
		case app.Saver:
			s = v
		}
	}
	m := New(loader, f, s)
	if err := m.Load(); err != nil && m.state == nil {
		t.Fatalf("Load: %v", err)
	}
	return m
}

func keyPress(t *testing.T, m *Model, msg tea.Msg) *Model {
	t.Helper()
	updated, _ := m.Update(msg)
	return updated.(*Model)
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
	if !strings.Contains(view, "import sites/a.caddy") {
		t.Errorf("View missing the root source text")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(m.View(), "respond ok") {
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
	// and the malformed region byte-for-byte.
	for _, want := range []string{
		"custom_plugin_directive",
		`"keep this raw"`,
		"example.test {",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("View missing raw source %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "1│") || !strings.Contains(view, "2│") {
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
	if !strings.Contains(view, "custom_plugin_directive \"keep this raw\"") {
		t.Errorf("unknown directive not preserved in the view:\n%s", view)
	}
}

func TestModelQuit(t *testing.T) {
	readFile := func(p string) ([]byte, error) { return []byte("example.test {\n}\n"), nil }
	state := stateFor(t, "config/Caddyfile", readFile)
	m := newLoadedModel(t, fakeLoader{state: state})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = updated.(*Model)
	if !m.quit {
		t.Errorf("quit = false, want true after q")
	}
	if cmd == nil || cmd() == nil {
		t.Errorf("expected tea.Quit command")
	}
}

func TestModelReadErrorShowsMessage(t *testing.T) {
	m := New(fakeLoader{state: nil, err: &noSuchFile{path: "missing/Caddyfile"}}, nil, nil)
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
	if !strings.Contains(view, "pbs.example.test {") {
		t.Errorf("selected block not visible after the reveal:\n%s", view)
	}
	if strings.Contains(view, "respond ok") {
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
	if !strings.Contains(m.View(), "respond ok") {
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
	var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(ansi.ReplaceAllString(line, "")); w > 100 {
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
	m := New(fakeLoader{state: nil, err: &noSuchFile{path: "missing"}}, formatter, nil)
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
	ansi := regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(ansi.ReplaceAllString(line, "")); w > 60 {
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
	ansi := regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(ansi.ReplaceAllString(line, "")); w > 60 {
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
	view := m.View()
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
	view := m.View()
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
	view := m.View()
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
	view := m.View()
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
	ansi := regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(ansi.ReplaceAllString(line, "")); w > 60 {
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
	view := m.View()
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
