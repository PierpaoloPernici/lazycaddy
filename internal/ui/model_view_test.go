package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

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

func TestModelRendersDocumentTree(t *testing.T) {
	fs := map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": "a.example.test {\n\trespond ok\n}\n",
	}
	state := stateFor(t, "config/Caddyfile", fsReader(fs))
	m := newLoadedModel(t, fakeLoader{state: state})

	view := m.View()
	for _, want := range []string{
		"RO",
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
	m = resize(m, 120, 14) // short window: the source overflows the pane (the two-line footer at 120 cols shrinks the pane)

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

	// Items: root doc, example.test (line 1), pbs.example.test (line 74)
	// and the collapsed comments branch for the 70 padding lines.
	if len(m.items) != 4 {
		t.Fatalf("items = %d, want 4", len(m.items))
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
	// A taller window so the 20-PgUp budget reaches the top with the
	// two-line footer at 120 columns.
	m = resize(m, 120, 14)

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

// TestSourcePaneKeepsAllAvailableRowsAfterTreeExpand verifies that the source
// viewport only reserves rows for its title and separator. paneContentH
// already accounts for the pane frame; subtracting the frame a second time
// drops two source rows, which is especially visible after expanding a large
// tree.
func TestSourcePaneKeepsAllAvailableRowsAfterTreeExpand(t *testing.T) {
	src := "example.test {\n\troute {\n\t\trespond ok\n\t}\n}\n"
	state := stateFor(t, "config/Caddyfile", func(string) ([]byte, error) {
		return []byte(src), nil
	})
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // select the site
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	_ = m.View()

	want := m.paneContentH(m.height) - 2 // source title + separator
	if want < 1 {
		want = 1
	}
	if m.viewport.Height != want {
		t.Fatalf("source viewport height = %d, want %d", m.viewport.Height, want)
	}
}

// TestExpandedTreeFitsItsPane verifies that the title row is included in the
// tree pane height budget. Before this guard, expand-all made a large tree
// render one row too many and the terminal scrolled the application header
// out of view.
func TestExpandedTreeFitsItsPane(t *testing.T) {
	var source strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&source, "site-%d.example.test {\n\troute {\n\trespond ok\n}\n}\n", i)
	}
	state := stateFor(t, "config/Caddyfile", func(string) ([]byte, error) {
		return []byte(source.String()), nil
	})
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 12)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	if len(m.items) <= m.paneContentH(m.height)-1 {
		t.Fatalf("fixture did not overflow the tree before clipping: %d items", len(m.items))
	}

	paneH := m.paneContentH(m.height)
	treeW := m.width * 2 / 5
	want := paneH + paneStyle.GetVerticalFrameSize()
	if got := lipgloss.Height(m.treePane(treeW, paneH)); got != want {
		t.Fatalf("expanded tree pane height = %d, want %d", got, want)
	}
	if got := lipgloss.Height(m.View()); got > m.height {
		t.Fatalf("expanded application view height = %d, exceeds terminal height %d", got, m.height)
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
	for _, want := range []string{"Enter toggle", "? commands"} {
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
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	m = openDiagnosticsModal(m, diags)
	view := stripANSI(m.View())
	if !strings.Contains(view, "Enter/+ or → detail") {
		t.Errorf("list footer should show 'Enter/+ or → detail', got:\n%s", view)
	}
	if strings.Contains(view, "? commands") {
		t.Errorf("modal footer should use only its contextual keys, got:\n%s", view)
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
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	m = openDiagnosticsModal(m, diags)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // open detail
	view := stripANSI(m.View())
	if !strings.Contains(view, "PgUp/PgDown") {
		t.Errorf("detail footer should show PgUp/PgDown, got:\n%s", view)
	}
	if strings.Contains(view, "? commands") {
		t.Errorf("modal footer should use only its contextual keys, got:\n%s", view)
	}
	if strings.Contains(view, "toggle") {
		t.Errorf("detail footer must not show the global toggle key, got:\n%s", view)
	}
}

// TestModelHeader_WriteModeBadge verifies that a writable state shows
// the RW badge instead of RO. The default read-only state
// is covered by TestModelRendersDocumentTree.
func TestModelHeader_WriteModeBadge(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	view := m.View()
	if strings.Contains(view, " RO ") {
		t.Errorf("View should not show RO in write mode:\n%s", view)
	}
	if !strings.Contains(stripANSI(view), "RW") {
		t.Errorf("View should show RW badge in write mode:\n%s", view)
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
	for _, want := range []string{"lazycaddy", testVersion, "Config:", "RO"} {
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
	for _, want := range []string{"lazycaddy", testVersion, "Config:", "RO"} {
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
	if !strings.Contains(view, "? commands") {
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
		if strings.Contains(line, "? commands") {
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

	// The warning text must not be mixed into the navigation footer line.
	lines := strings.Split(view, "\n")
	for _, line := range lines {
		if strings.Contains(line, "? commands") && strings.Contains(line, "warnings") {
			t.Errorf("warning text rendered on the footer line")
		}
	}
}

// TestModelFooter_StaysCompact verifies that the navigation footer remains
// on one line at a normal terminal width.
func TestModelFooter_StaysCompact(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)

	// Flatten whitespace so the assertions remain independent of surrounding
	// layout lines.
	view := strings.Join(strings.Fields(stripANSI(m.View())), " ")
	for _, hint := range []string{"? commands", "Enter toggle"} {
		if !strings.Contains(view, hint) {
			t.Errorf("footer missing critical hint %q:\n%s", hint, view)
		}
	}

	footer := stripANSI(m.footer(80))
	footerLines := lipgloss.Height(footer)
	if footerLines != 1 {
		t.Errorf("footer should stay on one line at 80 cols, got %d line(s):\n%s", footerLines, footer)
	}
	for i, line := range strings.Split(footer, "\n") {
		if w := lipgloss.Width(line); w > 80 {
			t.Errorf("footer line %d is %d columns wide (max 80):\n%s", i+1, w, line)
		}
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
	// The compact footer stays within the normal 80-column budget.
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

// TestRevealRange_CentresBlock verifies the centred reveal: a block that
// fits the viewport is centred on its midpoint, a taller block shows its
// start with a little context above it, and a block near the file start
// clamps naturally to the top.
func TestRevealRange_CentresBlock(t *testing.T) {
	var src strings.Builder
	for i := 0; i < 100; i++ {
		src.WriteString("example.test {\n\trespond ok\n}\n")
	}
	m := matcherModel(t, src.String())
	m = resize(m, 120, 30)
	_ = m.View()
	h := m.viewport.Height
	if h < 10 {
		t.Fatalf("viewport height = %d, want a tall viewport", h)
	}

	// A small block (lines 50-53): centre its midpoint.
	m.revealRange(50, 53)
	want := 49 - (h-4)/2
	if m.viewport.YOffset != want {
		t.Errorf("small block offset = %d, want %d (centred midpoint)", m.viewport.YOffset, want)
	}

	// A tall block (lines 40-90): show its start with about a third of the
	// viewport of context above.
	m.revealRange(40, 90)
	want = 39 - h/3
	if m.viewport.YOffset != want {
		t.Errorf("tall block offset = %d, want %d (start + context)", m.viewport.YOffset, want)
	}

	// Near the file start: the natural clamp keeps the offset at 0.
	m.revealRange(1, 4)
	if m.viewport.YOffset != 0 {
		t.Errorf("block at the file start = %d, want 0", m.viewport.YOffset)
	}

	// Near the file end: the natural clamp pins the viewport to the
	// bottom so the last line stays reachable without blank padding.
	m.revealRange(290, 300)
	if !m.viewport.AtBottom() {
		t.Errorf("block at the file end must clamp to the bottom, YOffset=%d height=%d", m.viewport.YOffset, m.viewport.Height)
	}
}

// TestSourceRevealLine_Centred verifies a one-shot line reveal centres the
// line in the viewport and consumes the flag on render.
func TestSourceRevealLine_Centred(t *testing.T) {
	var src strings.Builder
	for i := 0; i < 100; i++ {
		src.WriteString("example.test {\n\trespond ok\n}\n")
	}
	m := matcherModel(t, src.String())
	m = resize(m, 120, 30)
	_ = m.View()
	h := m.viewport.Height

	m.sourceRevealLine = 150
	_ = m.View()
	want := 149 - h/2
	if m.viewport.YOffset != want {
		t.Errorf("line reveal offset = %d, want %d (centred)", m.viewport.YOffset, want)
	}
	if m.sourceRevealLine != 0 {
		t.Errorf("sourceRevealLine = %d after render, want it consumed", m.sourceRevealLine)
	}

	// A line near the top clamps to 0.
	m.sourceRevealLine = 1
	_ = m.View()
	if m.viewport.YOffset != 0 {
		t.Errorf("line 1 reveal offset = %d, want 0", m.viewport.YOffset)
	}
}
