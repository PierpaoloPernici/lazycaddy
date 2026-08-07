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
}

func (f *fakeFormatter) FormatAndValidate(ctx context.Context, src []byte) ([]byte, []validator.Diagnostic, error) {
	f.calls++
	f.capturedCtx = ctx
	return f.formatted, f.diagnostics, f.err
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

func newLoadedModel(t *testing.T, loader app.Loader, formatter ...app.Formatter) *Model {
	t.Helper()
	var f app.Formatter
	if len(formatter) > 0 {
		f = formatter[0]
	}
	m := New(loader, f)
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
	m := New(fakeLoader{state: nil, err: &noSuchFile{path: "missing/Caddyfile"}}, nil)
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
	m := New(fakeLoader{state: nil, err: &noSuchFile{path: "missing"}}, formatter)
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
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
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
