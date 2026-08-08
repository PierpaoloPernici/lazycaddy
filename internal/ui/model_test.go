package ui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
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
// saver, reloader, runtime probe and log source. The variadic options
// accept an app.Formatter, an app.Saver, an app.Reloader, an
// app.RuntimeStatus, an app.LogSource, any combination, or none.
func newLoadedModel(t *testing.T, loader app.Loader, opts ...any) *Model {
	t.Helper()
	var f app.Formatter
	var s app.Saver
	var r app.Reloader
	var rt app.RuntimeStatus
	var ls app.LogSource
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
		}
	}
	m := New(loader, f, s, r, rt, ls)
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
	m := New(fakeLoader{state: nil, err: &noSuchFile{path: "missing/Caddyfile"}}, nil, nil, nil, nil, nil)
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
	m := New(fakeLoader{state: nil, err: &noSuchFile{path: "missing"}}, formatter, nil, nil, nil, nil)
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
	if !strings.Contains(m.View(), "r reload") {
		t.Errorf("View missing 'r reload' with reloader configured:\n%s", m.View())
	}
	// Without a reloader the key must be absent.
	m2 := newLoadedModel(t, fakeLoader{state: state})
	m2 = resize(m2, 120, 30)
	if strings.Contains(m2.View(), "r reload") {
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
	if strings.Contains(m.View(), "l logs") {
		t.Errorf("footer shows 'l logs' without a log source:\n%s", m.View())
	}
	// With a log source the l key appears.
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m = newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	if !strings.Contains(m.View(), "l logs") {
		t.Errorf("footer missing 'l logs' with a log source:\n%s", m.View())
	}
	// While the log view is open the footer shows the log-view key hints.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	view := m.View()
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
	if !strings.Contains(view, "Esc back") {
		t.Errorf("footer missing the detail hint 'Esc back':\n%s", view)
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
