package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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
