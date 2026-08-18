package ui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	"github.com/PierpaoloPernici/lazycaddy/internal/selection"
)

// mouseAt sends a mouse message through the model.
func mouseAt(t *testing.T, m *Model, x, y int, action tea.MouseAction, button tea.MouseButton) *Model {
	t.Helper()
	updated, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: action, Button: button})
	return updated.(*Model)
}

// pressCopy presses y and runs the returned clipboard command, returning
// the updated model.
func pressCopy(t *testing.T, m *Model) *Model {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(*Model)
	if cmd == nil {
		return m
	}
	updated, _ = m.Update(cmd())
	return updated.(*Model)
}

// selectionSource returns a model loaded with a small deterministic
// Caddyfile, sized to 80x24 and rendered once so the source pane and its
// viewport are populated.
func selectionSource(t *testing.T, src string, opts ...any) *Model {
	t.Helper()
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte(src), nil
	})
	m := newLoadedModel(t, fakeLoader{state: state}, opts...)
	m = resize(m, 80, 24)
	_ = m.View()
	return m
}

func TestMouseSelectionSourcePressDragRelease(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	m := selectionSource(t, source)
	geo := m.sourcePaneGeometry()

	// Press on the first character of line 1, drag to byte 7 of line 1,
	// release.
	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+7, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+7, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)

	if m.textSel.pane != textPaneSource {
		t.Fatalf("selection pane = %d, want source", m.textSel.pane)
	}
	r, ok := m.textSel.state.Range()
	if !ok {
		t.Fatal("no selection after press/drag/release")
	}
	if r.Start != (selection.Position{Line: 0, Offset: 0}) || r.End != (selection.Position{Line: 0, Offset: 7}) {
		t.Errorf("range = %s, want line 0 offset 0 -> line 0 offset 7", m.textSel.state.String())
	}
}

func TestMouseSelectionCopiesExactSourceBytes(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	clip := &fakeClipboard{}
	m := selectionSource(t, source, clip)
	geo := m.sourcePaneGeometry()

	// Select "example" (bytes 0..7) of line 1.
	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+7, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+7, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)

	m = pressCopy(t, m)
	if !bytes.Equal(clip.content, []byte("example")) {
		t.Errorf("copied = %q, want %q", clip.content, "example")
	}
	if !strings.Contains(m.statusMessage, "copied") {
		t.Errorf("statusMessage = %q, want copied notification", m.statusMessage)
	}
}

func TestRightClickCopiesActiveSourceSelection(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	clip := &fakeClipboard{}
	m := selectionSource(t, source, clip)
	geo := m.sourcePaneGeometry()

	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+7, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+7, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)

	updated, cmd := m.Update(tea.MouseMsg{
		X:      geo.x + 10,
		Y:      geo.y,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonRight,
	})
	if cmd == nil {
		t.Fatal("right-click copy command is nil")
	}
	updated, _ = updated.(*Model).Update(cmd())
	if !bytes.Equal(clip.content, []byte("example")) {
		t.Errorf("copied = %q, want %q", clip.content, "example")
	}
}

func TestRightClickWithoutSelectionDoesNothing(t *testing.T) {
	const source = "example.test {}\n"
	clip := &fakeClipboard{}
	m := selectionSource(t, source, clip)
	geo := m.sourcePaneGeometry()

	updated, cmd := m.Update(tea.MouseMsg{
		X:      geo.x,
		Y:      geo.y,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonRight,
	})
	if cmd != nil {
		t.Fatal("right-click without selection returned a copy command")
	}
	if got := updated.(*Model); got.clipboard == nil || clip.calls != 0 {
		t.Fatal("right-click without selection touched the clipboard")
	}
}

func TestMouseSelectionMultiLineCopy(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	clip := &fakeClipboard{}
	m := selectionSource(t, source, clip)
	geo := m.sourcePaneGeometry()

	// Select from line 1 offset 3 to line 2 offset 10 ("mple.test {\n\trespond /").
	m = mouseAt(t, m, geo.x+7+3, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+10, geo.y+1, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+10, geo.y+1, tea.MouseActionRelease, tea.MouseButtonNone)

	m = pressCopy(t, m)
	want := "mple.test {\n\trespond /"
	if !bytes.Equal(clip.content, []byte(want)) {
		t.Errorf("copied = %q, want %q", clip.content, want)
	}
}

func TestMouseSelectionRejectedOutsideSourcePane(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	m := selectionSource(t, source)
	geo := m.sourcePaneGeometry()

	// Seed a valid selection first so we can prove it is dropped when the
	// press lands on a non-text region.
	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+3, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	if m.textSel.pane != textPaneSource {
		t.Fatal("seed selection failed")
	}

	cases := []struct {
		name string
		x, y int
	}{
		{"tree pane", 5, geo.y},
		{"header", 40, 0},
		{"source left border", geo.x - 2, geo.y},
		{"source padding", geo.x - 1, geo.y},
		{"footer", 40, m.height - 1},
		{"above pane", geo.x + 6, geo.y - 1},
		{"below pane", geo.x + 6, geo.y + geo.height},
		{"right of pane", geo.x + geo.width, geo.y},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mm := mouseAt(t, m, tc.x, tc.y, tea.MouseActionPress, tea.MouseButtonLeft)
			if mm.textSel.pane != textPaneNone {
				t.Errorf("press at (%d,%d) kept selection %s", tc.x, tc.y, mm.textSel.state.String())
			}
		})
	}
}

func TestMouseSelectionConfinedToPane(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	clip := &fakeClipboard{}
	m := selectionSource(t, source, clip)
	geo := m.sourcePaneGeometry()

	// Press in the source pane, then drag far beyond the pane into the
	// tree. The cursor must clamp to the pane's content.
	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, 0, m.height-1, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, 0, m.height-1, tea.MouseActionRelease, tea.MouseButtonNone)

	if m.textSel.pane != textPaneSource {
		t.Fatalf("selection pane = %d, want source after clamped drag", m.textSel.pane)
	}
	m = pressCopy(t, m)
	if !bytes.HasPrefix(clip.content, []byte("example.test {")) {
		t.Errorf("clamped copy = %q, want prefix %q", clip.content, "example.test {")
	}
	if bytes.ContainsAny(clip.content, "\x00") {
		t.Errorf("copy contains bytes from outside the source pane: %q", clip.content)
	}
}

func TestSourceCoordinateMappingGutterAndScroll(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 25; i++ {
		sb.WriteString("xxxxxxxx line ")
		sb.WriteString(strings.Repeat("a", i%7))
		sb.WriteByte('\n')
	}
	m := selectionSource(t, sb.String())
	geo := m.sourcePaneGeometry()

	// The gutter occupies the first 7 content cells (including the reserved marker cell): a press there must
	// not select.
	before := mouseAt(t, m, geo.x+3, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	if before.textSel.pane != textPaneNone {
		t.Error("gutter press created a selection")
	}

	// Scroll the source viewport down by one page.
	m = resize(m, 80, 24)
	_ = m.View()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(*Model)
	_ = m.View()
	if m.viewport.YOffset == 0 {
		t.Fatal("pgdown did not scroll the source viewport")
	}
	scroll := m.viewport.YOffset

	// The first visible row now shows the scrolled line.
	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	r, ok := m.textSel.state.Range()
	if !ok || r.Start.Line != scroll || r.Start.Offset != 0 {
		t.Errorf("press after scroll = %+v, want line %d offset 0", r.Start, scroll)
	}
}

func TestSourceCoordinateMappingWideChars(t *testing.T) {
	// 日本語 is 3 wide runes (2 cells each); the line is 9 cells wide.
	const source = "日本語abc\nsecond\n"
	m := selectionSource(t, source)
	geo := m.sourcePaneGeometry()

	// Content cell 2 is 本 (bytes 3..6); content cell 6 is 'a' (byte 9).
	m = mouseAt(t, m, geo.x+7+2, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+6, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+6, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)
	r, ok := m.textSel.state.Range()
	if !ok {
		t.Fatal("no selection on wide-char line")
	}
	if r.Start != (selection.Position{Line: 0, Offset: 3}) || r.End != (selection.Position{Line: 0, Offset: 9}) {
		t.Errorf("wide-char range = %s, want offset 3 -> 9", m.textSel.state.String())
	}
}

func TestLogSelectionCopiesPlainText(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn: func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry {
			return []logs.Entry{
				logEntry("handled request"),
				logEntry("second line"),
			}
		},
	}
	clip := &fakeClipboard{}
	m := newLoadedModel(t, fakeLoader{state: state}, src, clip)
	m = resize(m, 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	m = resize(m, 80, 24)
	_ = m.View()

	geo := m.logPaneGeometry()
	// Select from the first content cell of the first entry to the end of
	// the second entry's line.
	m = mouseAt(t, m, geo.x+2, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	lineW := geo.width - 2
	m = mouseAt(t, m, geo.x+2+lineW, geo.y+1, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+2+lineW, geo.y+1, tea.MouseActionRelease, tea.MouseButtonNone)
	if m.textSel.pane != textPaneLogs {
		t.Fatalf("selection pane = %d, want logs", m.textSel.pane)
	}

	m = pressCopy(t, m)
	if len(clip.content) == 0 {
		t.Fatal("log selection copied nothing")
	}
	got := string(clip.content)
	if strings.Contains(got, "\x1b[") {
		t.Errorf("copied log text contains ANSI: %q", got)
	}
	if strings.Contains(got, "›") || strings.Contains(got, "│") {
		t.Errorf("copied log text contains UI decorations: %q", got)
	}
	// The copy is exactly the plain rendered entries joined by newlines.
	p := m.logTextPane()
	want, ok := p.RangeBytes(selection.Range{
		Start: selection.Position{Line: 0, Offset: 0},
		End:   selection.Position{Line: 1, Offset: len(p.Lines[1])},
	})
	if !ok || string(want) != got {
		t.Errorf("copied = %q, want %q", got, want)
	}
	if !strings.Contains(got, "handled request") || !strings.Contains(got, "second line") {
		t.Errorf("copied log text missing entry messages: %q", got)
	}
}

func TestDiffSelectionCopiesPlainBody(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte("example.test {}\n"), nil
	})
	clip := &fakeClipboard{}
	m := newLoadedModel(t, fakeLoader{state: state}, clip)
	m = resize(m, 80, 24)
	lines, err := diff.Unified([]byte("a\nb\n"), []byte("a\nc\n"), "x", "y")
	if err != nil {
		t.Fatal(err)
	}
	m.showDiffModal(lines, "Diff · x")
	m = resize(m, 80, 24)
	_ = m.View()

	geo := m.diffPaneGeometry()
	// Select the whole first diff line.
	m = mouseAt(t, m, geo.x, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+4, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+4, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)
	if m.textSel.pane != textPaneDiff {
		t.Fatalf("selection pane = %d, want diff", m.textSel.pane)
	}

	m = pressCopy(t, m)
	if string(clip.content) != "--- " {
		t.Errorf("copied = %q, want %q", clip.content, "--- ")
	}
	if strings.Contains(string(clip.content), "\x1b[") {
		t.Errorf("copied diff contains ANSI: %q", clip.content)
	}
}

func TestDiffSelectionCopiesAllLinesWithPrefixes(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte("example.test {}\n"), nil
	})
	clip := &fakeClipboard{}
	m := newLoadedModel(t, fakeLoader{state: state}, clip)
	m = resize(m, 80, 24)
	lines, err := diff.Unified([]byte("a\nb\n"), []byte("a\nc\n"), "x", "y")
	if err != nil {
		t.Fatal(err)
	}
	m.showDiffModal(lines, "Diff · x")
	m = resize(m, 80, 24)
	_ = m.View()

	geo := m.diffPaneGeometry()
	p := m.diffTextPane()
	// Select every diff line by dragging from the top-left to the end of
	// the last line (the pane has no gutter, so col 0 is content).
	m = mouseAt(t, m, geo.x, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	lastRow := len(p.Lines) - 1
	m = mouseAt(t, m, geo.x+len(p.Lines[lastRow]), geo.y+lastRow, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+len(p.Lines[lastRow]), geo.y+lastRow, tea.MouseActionRelease, tea.MouseButtonNone)

	m = pressCopy(t, m)
	want := "--- x\n+++ y\n@@ -1,2 +1,2 @@\n a\n-b\n+c"
	if string(clip.content) != want {
		t.Errorf("copied = %q, want %q", clip.content, want)
	}
	if strings.Contains(string(clip.content), "\x1b[") {
		t.Errorf("copied diff contains ANSI: %q", clip.content)
	}
}

func TestCopyYPrefersActiveTextSelection(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	clip := &fakeClipboard{}
	m := selectionSource(t, source, clip)
	geo := m.sourcePaneGeometry()

	// Move the tree cursor to the site node (so the fallback would copy
	// the node range), then make a text selection.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(*Model)
	_ = m.View()
	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+7, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+7, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)

	m = pressCopy(t, m)
	if !bytes.Equal(clip.content, []byte("example")) {
		t.Errorf("copied = %q, want the text selection %q", clip.content, "example")
	}
}

func TestCopyYFallsBackToNodeWhenNoTextSelection(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	clip := &fakeClipboard{}
	m := selectionSource(t, source, clip)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(*Model)
	_ = m.View()

	m = pressCopy(t, m)
	want := "example.test {\n\trespond / \"hello\"\n}\n"
	if !bytes.Equal(clip.content, []byte(want)) {
		t.Errorf("copied = %q, want node range %q", clip.content, want)
	}
}

func TestCopyYUnavailableWithoutSelectionOrSource(t *testing.T) {
	// A fresh model with no clipboard: y must report the backend missing.
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte("example.test {}\n"), nil
	})
	m := newLoadedModel(t, fakeLoader{state: state})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd != nil {
		t.Fatal("copy command non-nil without a clipboard backend")
	}
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "copy unavailable") {
		t.Errorf("statusMessage = %q, want unavailable", m.statusMessage)
	}
}

func TestCopyYReportsBackendErrorForSelection(t *testing.T) {
	const source = "example.test {\n}\n"
	clip := &fakeClipboard{err: errors.New("pipe broken")}
	m := selectionSource(t, source, clip)
	geo := m.sourcePaneGeometry()
	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+4, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+4, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)

	m = pressCopy(t, m)
	if !strings.Contains(m.statusMessage, "copy failed") {
		t.Errorf("statusMessage = %q, want failed", m.statusMessage)
	}
	if len(m.errorHistory) == 0 || m.errorHistory[len(m.errorHistory)-1].Op != "copy" {
		t.Errorf("copy failure not recorded in error history")
	}
}

func TestSelectionClearedOnViewSwitch(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{logEntry("handled request")} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 80, 24)
	_ = m.View()

	// Select in the source pane.
	geo := m.sourcePaneGeometry()
	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+3, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	if m.textSel.pane != textPaneSource {
		t.Fatal("seed source selection failed")
	}

	// Opening the log view is a view switch: the selection is dropped.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if m.textSel.pane != textPaneNone {
		t.Error("source selection survived opening the log view")
	}
	m = resize(m, 80, 24)
	_ = m.View()
	// Select in the log pane.
	lgeo := m.logPaneGeometry()
	m = mouseAt(t, m, lgeo.x+2, lgeo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, lgeo.x+2+3, lgeo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	if m.textSel.pane != textPaneLogs {
		t.Fatal("seed log selection failed")
	}
	// Closing the log view drops it.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*Model)
	if m.textSel.pane != textPaneNone {
		t.Error("log selection survived closing the log view")
	}
}

func TestSelectionClearedOnDiffClose(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte("example.test {}\n"), nil
	})
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	lines, err := diff.Unified([]byte("a\n"), []byte("b\n"), "x", "y")
	if err != nil {
		t.Fatal(err)
	}
	m.showDiffModal(lines, "Diff · x")
	m = resize(m, 80, 24)
	_ = m.View()

	geo := m.diffPaneGeometry()
	m = mouseAt(t, m, geo.x, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+2, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	if m.textSel.pane != textPaneDiff {
		t.Fatal("seed diff selection failed")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*Model)
	if m.textSel.pane != textPaneNone {
		t.Error("diff selection survived closing the modal")
	}
}

func TestSelectionClearedOnTreeNavigation(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	m := selectionSource(t, source)
	geo := m.sourcePaneGeometry()

	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+3, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	if m.textSel.pane != textPaneSource {
		t.Fatal("seed source selection failed")
	}
	// Moving the tree cursor rebuilds the source content.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(*Model)
	_ = m.View()
	if m.textSel.pane != textPaneNone {
		t.Error("source selection survived tree navigation")
	}
}

func TestSelectionClearedOnModalOpen(t *testing.T) {
	const source = "example.test {\n}\n"
	m := selectionSource(t, source)
	geo := m.sourcePaneGeometry()

	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+3, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	if m.textSel.pane != textPaneSource {
		t.Fatal("seed source selection failed")
	}
	// The command palette is an unrelated workflow.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = updated.(*Model)
	if m.textSel.pane != textPaneNone {
		t.Error("source selection survived opening the command palette")
	}
	if !m.showCommandPalette {
		t.Fatal("command palette did not open")
	}
}

func TestLogSelectionClearedWhenEntriesArrive(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{logEntry("first")} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	m = resize(m, 80, 24)
	_ = m.View()

	geo := m.logPaneGeometry()
	m = mouseAt(t, m, geo.x+2, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+2+2, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	if m.textSel.pane != textPaneLogs {
		t.Fatal("seed log selection failed")
	}
	// New entries shift the scrollback: the selection is dropped.
	updated, cmd := m.Update(logTailMsg{Entries: []logs.Entry{logEntry("second")}})
	m = updated.(*Model)
	_ = cmd
	if m.textSel.pane != textPaneNone {
		t.Error("log selection survived new entries")
	}
}

func TestDiffSelectionClearedOnHorizontalScroll(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte("example.test {}\n"), nil
	})
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	lines, err := diff.Unified([]byte("a\n"), []byte("b\n"), "x", "y")
	if err != nil {
		t.Fatal(err)
	}
	m.showDiffModal(lines, "Diff · x")
	m = resize(m, 80, 24)
	_ = m.View()

	geo := m.diffPaneGeometry()
	m = mouseAt(t, m, geo.x, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+2, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	if m.textSel.pane != textPaneDiff {
		t.Fatal("seed diff selection failed")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if m.textSel.pane != textPaneNone {
		t.Error("diff selection survived horizontal scroll")
	}
}

func TestKeyboardSelectionShiftArrows(t *testing.T) {
	const source = "line one\nline two\nline three\n"
	clip := &fakeClipboard{}
	m := selectionSource(t, source, clip)

	// shift+down twice selects lines 0-1 (bytes 0..16).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = updated.(*Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = updated.(*Model)

	r, ok := m.textSel.state.Range()
	if !ok {
		t.Fatal("no keyboard selection after shift+down")
	}
	if r.Start != (selection.Position{Line: 0, Offset: 0}) || r.End != (selection.Position{Line: 2, Offset: 0}) {
		t.Errorf("keyboard range = %s, want line 0 -> line 2 offset 0", m.textSel.state.String())
	}

	m = pressCopy(t, m)
	want := "line one\nline two\n"
	if !bytes.Equal(clip.content, []byte(want)) {
		t.Errorf("keyboard copy = %q, want %q", clip.content, want)
	}
}

func TestKeyboardSelectionShiftLeftRight(t *testing.T) {
	const source = "abcdef\n"
	clip := &fakeClipboard{}
	m := selectionSource(t, source, clip)

	// shift+right x4 then shift+left once: bytes 0..3.
	mm := m
	for i := 0; i < 4; i++ {
		updated, _ := mm.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
		mm = updated.(*Model)
	}
	updated, _ := mm.Update(tea.KeyMsg{Type: tea.KeyShiftLeft})
	mm = updated.(*Model)
	mm = pressCopy(t, mm)
	if !bytes.Equal(clip.content, []byte("abc")) {
		t.Errorf("keyboard copy = %q, want %q", clip.content, "abc")
	}
}

func TestSelectionRenderingPreservesText(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	m := selectionSource(t, source)
	geo := m.sourcePaneGeometry()

	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+14, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+14, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)

	view := m.View()
	plain := stripANSI(view)
	for _, want := range []string{"example.test {", "respond /", "Caddyfile"} {
		if !strings.Contains(plain, want) {
			t.Errorf("rendered view lost %q:\n%s", want, plain)
		}
	}
	// The overlay adds background sequences over the styled viewport.
	if strings.Count(view, "\x1b[") == 0 {
		t.Errorf("selection overlay did not render: %q", view)
	}
}

func TestLogSelectionRenderingPreservesEntries(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{logEntry("handled request")} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	m = resize(m, 80, 24)
	_ = m.View()

	geo := m.logPaneGeometry()
	m = mouseAt(t, m, geo.x+2, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+2+5, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+2+5, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)

	view := stripANSI(m.View())
	if !strings.Contains(view, "handled request") || !strings.Contains(view, "Logs ·") {
		t.Errorf("log view lost content with selection:\n%s", view)
	}
}

func TestSourceTextPaneGeometry(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	m := selectionSource(t, source)
	_ = m.View()
	p := m.sourceTextPane()

	if p.GutterWidth != 7 {
		t.Errorf("GutterWidth = %d, want 7", p.GutterWidth)
	}
	if p.Scroll != m.viewport.YOffset {
		t.Errorf("Scroll = %d, want viewport YOffset %d", p.Scroll, m.viewport.YOffset)
	}
	if p.Height != m.viewport.Height {
		t.Errorf("Height = %d, want viewport height %d", p.Height, m.viewport.Height)
	}
	wantLines := []string{"example.test {", "\trespond / \"hello\"", "}", ""}
	if len(p.Lines) != len(wantLines) {
		t.Fatalf("Lines = %v, want %v", p.Lines, wantLines)
	}
	for i := range wantLines {
		if p.Lines[i] != wantLines[i] {
			t.Errorf("Lines[%d] = %q, want %q", i, p.Lines[i], wantLines[i])
		}
	}
	if string(p.Source) != source {
		t.Errorf("Source = %q, want the exact document bytes", p.Source)
	}
}

func TestLogTextPanePlainLines(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{logEntry("handled request")} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	m = resize(m, 80, 24)
	_ = m.View()

	p := m.logTextPane()
	if p.GutterWidth != 2 {
		t.Errorf("GutterWidth = %d, want 2", p.GutterWidth)
	}
	if len(p.Lines) != 1 {
		t.Fatalf("Lines = %v, want one entry", p.Lines)
	}
	if strings.Contains(p.Lines[0], "\x1b[") {
		t.Errorf("log pane line contains ANSI: %q", p.Lines[0])
	}
	if !strings.Contains(p.Lines[0], "handled request") {
		t.Errorf("log pane line missing message: %q", p.Lines[0])
	}
	// The pane line equals the plain rendering of the entry.
	want := compactLogPlainLine(logEntry("handled request"), 72)
	if p.Lines[0] != want {
		t.Errorf("log pane line = %q, want %q", p.Lines[0], want)
	}
}

func TestDiffTextPaneLines(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte("example.test {}\n"), nil
	})
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	lines, err := diff.Unified([]byte("a\nb\n"), []byte("a\nc\n"), "x", "y")
	if err != nil {
		t.Fatal(err)
	}
	m.showDiffModal(lines, "Diff · x")
	m = resize(m, 80, 24)
	_ = m.View()

	p := m.diffTextPane()
	want := []string{"--- x", "+++ y", "@@ -1,2 +1,2 @@", " a", "-b", "+c"}
	if len(p.Lines) != len(want) {
		t.Fatalf("Lines = %v, want %v", p.Lines, want)
	}
	for i := range want {
		if p.Lines[i] != want[i] {
			t.Errorf("Lines[%d] = %q, want %q", i, p.Lines[i], want[i])
		}
	}
}

func TestMouseSelectionIgnoredWhileModalOpen(t *testing.T) {
	const source = "example.test {\n}\n"
	m := selectionSource(t, source)
	// Open the command palette (an opaque overlay over the main panes).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = updated.(*Model)

	geo := m.sourcePaneGeometry()
	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	if m.textSel.pane != textPaneNone {
		t.Error("mouse press through a modal created a selection")
	}
}

func TestMousePressOnEmptyLogView(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	m = resize(m, 80, 24)
	_ = m.View()

	geo := m.logPaneGeometry()
	m = mouseAt(t, m, geo.x+2, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	if m.textSel.pane != textPaneLogs {
		t.Fatalf("empty log pane did not accept a selection (pane=%d)", m.textSel.pane)
	}
}

func TestSourceSelectionSurvivesResize(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	clip := &fakeClipboard{}
	m := selectionSource(t, source, clip)
	geo := m.sourcePaneGeometry()

	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+7, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+7, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)

	// A resize re-lays-out the panes but does not change the content: the
	// selection (anchored in source bytes) stays valid.
	m = resize(m, 100, 30)
	_ = m.View()
	m = pressCopy(t, m)
	if !bytes.Equal(clip.content, []byte("example")) {
		t.Errorf("copied after resize = %q, want %q", clip.content, "example")
	}
}

func TestSelectionClearedOnSourceRefresh(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	m := selectionSource(t, source)
	geo := m.sourcePaneGeometry()

	m = mouseAt(t, m, geo.x+7, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+7+3, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	if m.textSel.pane != textPaneSource {
		t.Fatal("seed source selection failed")
	}
	// A save sets sourceRefresh: the next render rebuilds the source
	// content, which drops the selection anchored in the old bytes.
	m.sourceRefresh = true
	_ = m.View()
	if m.textSel.pane != textPaneNone {
		t.Error("source selection survived a content refresh")
	}
}

func TestCopyYStaleSelectionGuard(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	m := selectionSource(t, source)

	// Force a stale source selection while a modal hides the source pane
	// (the state transitions normally clear it, but the guard must also
	// hold as a safety net).
	m.textSel.pane = textPaneSource
	m.textSel.state.SelectTo(selection.Position{Line: 0, Offset: 5})
	m.showCommandPalette = true

	if r, ok := m.activeTextSelection(); ok {
		t.Errorf("stale selection reported as active: %+v", r)
	}
	if m.textSel.pane != textPaneNone {
		t.Error("stale selection was not cleared by the guard")
	}
	// With the guard satisfied, y copies the document fallback.
	m.showCommandPalette = false
	clip := &fakeClipboard{}
	m.clipboard = clip
	m = pressCopy(t, m)
	if !bytes.Equal(clip.content, []byte(source)) {
		t.Errorf("y after stale guard copied %q, want the document fallback %q", clip.content, source)
	}
}

func TestSelectionGutterDragClampsToLineStart(t *testing.T) {
	const source = "example.test {\n\trespond / \"hello\"\n}\n"
	clip := &fakeClipboard{}
	m := selectionSource(t, source, clip)
	geo := m.sourcePaneGeometry()

	// Press on the first character, drag far left into the gutter: the
	// selection clamps to the line start.
	m = mouseAt(t, m, geo.x+7+3, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)

	m = pressCopy(t, m)
	if !bytes.Equal(clip.content, []byte("exa")) {
		t.Errorf("gutter-clamped copy = %q, want %q", clip.content, "exa")
	}
}

func TestKeyboardSelectionInLogView(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn: func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry {
			return []logs.Entry{logEntry("first entry"), logEntry("second entry")}
		},
	}
	clip := &fakeClipboard{}
	m := newLoadedModel(t, fakeLoader{state: state}, src, clip)
	m = resize(m, 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	m = resize(m, 80, 24)
	_ = m.View()

	// shift+down twice selects both entries; y copies the two plain log
	// lines.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = updated.(*Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	m = updated.(*Model)
	if m.textSel.pane != textPaneLogs {
		t.Fatalf("keyboard selection pane = %d, want logs", m.textSel.pane)
	}
	m = pressCopy(t, m)
	p := m.logTextPane()
	// shift+down twice reaches the end of the second (last) entry, so the
	// copy is both plain lines.
	want := p.Lines[0] + "\n" + p.Lines[1]
	if !bytes.Equal(clip.content, []byte(want)) {
		t.Errorf("log keyboard copy = %q, want %q", clip.content, want)
	}
	if strings.Contains(string(clip.content), "\x1b[") {
		t.Errorf("log keyboard copy contains ANSI: %q", clip.content)
	}
}

func TestKeyboardSelectionInDiffModal(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte("example.test {}\n"), nil
	})
	clip := &fakeClipboard{}
	m := newLoadedModel(t, fakeLoader{state: state}, clip)
	m = resize(m, 80, 24)
	lines, err := diff.Unified([]byte("a\nb\n"), []byte("a\nc\n"), "x", "y")
	if err != nil {
		t.Fatal(err)
	}
	m.showDiffModal(lines, "Diff · x")
	m = resize(m, 80, 24)
	_ = m.View()

	// The first diff line is "--- x". shift+right x4 selects its first
	// four bytes.
	for i := 0; i < 4; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
		m = updated.(*Model)
	}
	m = pressCopy(t, m)
	if string(clip.content) != "--- " {
		t.Errorf("diff keyboard copy = %q, want %q", clip.content, "--- ")
	}
}

func TestCopyYWorksInLogView(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{logEntry("handled request")} },
	}
	clip := &fakeClipboard{}
	m := newLoadedModel(t, fakeLoader{state: state}, src, clip)
	m = resize(m, 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	m = resize(m, 80, 24)
	_ = m.View()

	geo := m.logPaneGeometry()
	m = mouseAt(t, m, geo.x+2, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+2+70, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+2+70, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)

	m = pressCopy(t, m)
	if len(clip.content) == 0 {
		t.Fatal("y in the log view copied nothing")
	}
	if !strings.Contains(string(clip.content), "handled request") {
		t.Errorf("log copy = %q, want the entry message", clip.content)
	}
}

func TestCopyYWorksInDiffModal(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
		return []byte("example.test {}\n"), nil
	})
	clip := &fakeClipboard{}
	m := newLoadedModel(t, fakeLoader{state: state}, clip)
	m = resize(m, 80, 24)
	lines, err := diff.Unified([]byte("a\n"), []byte("b\n"), "x", "y")
	if err != nil {
		t.Fatal(err)
	}
	m.showDiffModal(lines, "Diff · x")
	m = resize(m, 80, 24)
	_ = m.View()

	geo := m.diffPaneGeometry()
	m = mouseAt(t, m, geo.x, geo.y, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+5, geo.y, tea.MouseActionMotion, tea.MouseButtonLeft)
	m = mouseAt(t, m, geo.x+5, geo.y, tea.MouseActionRelease, tea.MouseButtonNone)

	m = pressCopy(t, m)
	if len(clip.content) == 0 {
		t.Fatal("y in the diff modal copied nothing")
	}
	if string(clip.content) != "--- x" {
		t.Errorf("diff copy = %q, want %q", clip.content, "--- x")
	}
}
