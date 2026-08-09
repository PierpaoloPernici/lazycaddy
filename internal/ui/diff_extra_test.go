package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
)

// TestModelDiff_PerDocumentImportedFile verifies that D diffs the
// currently selected imported document (in-memory Source vs on-disk bytes
// read through the injected reader) with a clearly labelled title.
func TestModelDiff_PerDocumentImportedFile(t *testing.T) {
	fs := map[string]string{
		"Caddyfile":   "example.test {\n\timport common.conf\n}\n",
		"common.conf": "# v1\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	m := newLoadedModel(t, loader, fsReader(fs))
	m = resize(m, 80, 24)

	// The tree rows are: root doc, site node, imported doc. Select the
	// imported document.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})

	// The file changed on disk after it was loaded, so the diff has real
	// content.
	fs["common.conf"] = "# changed on disk\n"
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	if !m.showDiff {
		t.Fatal("showDiff = false after D on an imported document")
	}
	if !strings.Contains(m.diffTitle, "Diff current changes") {
		t.Errorf("diffTitle = %q, want the current-changes title", m.diffTitle)
	}
	if !strings.Contains(m.diffTitle, "common.conf") {
		t.Errorf("diffTitle = %q, want the imported document path", m.diffTitle)
	}
	view := m.View()
	if !strings.Contains(view, "changed on disk") || !strings.Contains(view, "# v1") {
		t.Errorf("diff body missing the in-memory vs on-disk change:\n%s", view)
	}
}

// TestModelDiff_RootWithoutWorkingCopyFallsBackToOnDisk verifies that D
// on the root with no working copy falls back to the in-memory-vs-on-disk
// diff instead of erroring, keeping the v hint as a secondary note.
func TestModelDiff_RootWithoutWorkingCopyFallsBackToOnDisk(t *testing.T) {
	fs := map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}
	state := stateFor(t, "config/Caddyfile", fsReader(fs))
	m := newLoadedModel(t, fakeLoader{state: state}, fsReader(fs))
	m = resize(m, 80, 24)

	// The root changed on disk after load; no working copy exists.
	fs["config/Caddyfile"] = "example.test {\n\trespond ok\n}\n"
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	if !m.showDiff {
		t.Fatal("showDiff = false, want the on-disk fallback diff")
	}
	if !strings.Contains(m.diffTitle, "Diff current changes") {
		t.Errorf("diffTitle = %q, want the current-changes title", m.diffTitle)
	}
}

// TestModelDiff_NoReaderImportedShowsHint verifies that D on an imported
// document with no injected reader surfaces a hint instead of opening.
func TestModelDiff_NoReaderImportedShowsHint(t *testing.T) {
	fs := map[string]string{
		"Caddyfile":   "example.test {\n\timport common.conf\n}\n",
		"common.conf": "# v1\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	m := newLoadedModel(t, loader)
	m = resize(m, 80, 24)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	if m.showDiff {
		t.Fatal("showDiff = true without a reader, want a hint")
	}
	if !strings.Contains(m.statusMessage, "diff") {
		t.Errorf("statusMessage = %q, want a diff hint", m.statusMessage)
	}
}

// TestModelDiff_HunkNavigation verifies that n/N jump the diff viewport
// to the next/previous @@ hunk header, wrapping at the ends.
func TestModelDiff_HunkNavigation(t *testing.T) {
	lines := []diff.Line{
		{Kind: diff.KindFileHeader, Text: "--- a"},
		{Kind: diff.KindFileHeader, Text: "+++ b"},
		{Kind: diff.KindHunkHeader, Text: "@@ -1,1 +1,1 @@"}, // index 2
		{Kind: diff.KindAdd, Text: "+a1"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindHunkHeader, Text: "@@ -13,1 +13,1 @@"}, // index 13
		{Kind: diff.KindRemove, Text: "-b1"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindContext, Text: "ctx"},
		{Kind: diff.KindHunkHeader, Text: "@@ -25,1 +25,1 @@"}, // index 24
		{Kind: diff.KindAdd, Text: "+c1"},
	}
	for i := 0; i < 14; i++ {
		lines = append(lines, diff.Line{Kind: diff.KindContext, Text: "tail"})
	}
	m := newLoadedModel(t, fakeLoader{state: stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))})
	// A short window gives the diff viewport a small height (about 4-5
	// rows), so every hunk index stays within the scrollable range of the
	// 40-line content and the reveal scroll is deterministic.
	m = resize(m, 80, 12)
	m.diffLines = lines
	m.diffTitle = "Diff · a"
	m.showDiff = true
	m.syncDiffContent()
	m.diffViewport.GotoTop()

	if m.diffHunkCursor != 0 {
		t.Fatalf("initial diffHunkCursor = %d, want 0", m.diffHunkCursor)
	}
	// The current hunk (the first) is marked in the body.
	if !strings.Contains(m.View(), "> @@ -1,1 +1,1 @@") {
		t.Errorf("first hunk missing the current-hunk marker:\n%s", m.View())
	}
	// n → second hunk.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.diffHunkCursor != 1 || m.diffViewport.YOffset != 13 {
		t.Errorf("after first n: cursor=%d offset=%d, want 1/13", m.diffHunkCursor, m.diffViewport.YOffset)
	}
	// The marker moved to the second hunk.
	if !strings.Contains(m.View(), "> @@ -13,1 +13,1 @@") {
		t.Errorf("second hunk missing the current-hunk marker:\n%s", m.View())
	}
	// n → third hunk.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.diffHunkCursor != 2 || m.diffViewport.YOffset != 24 {
		t.Errorf("after second n: cursor=%d offset=%d, want 2/24", m.diffHunkCursor, m.diffViewport.YOffset)
	}
	// n wraps to the first hunk.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.diffHunkCursor != 0 || m.diffViewport.YOffset != 2 {
		t.Errorf("after wrap n: cursor=%d offset=%d, want 0/2", m.diffHunkCursor, m.diffViewport.YOffset)
	}
	// N wraps back to the last hunk.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	if m.diffHunkCursor != 2 || m.diffViewport.YOffset != 24 {
		t.Errorf("after wrap N: cursor=%d offset=%d, want 2/24", m.diffHunkCursor, m.diffViewport.YOffset)
	}
}

// TestModelDiff_ChangeSummary verifies the modal title reports the hunk,
// added and removed line counts derived from the diff kinds.
func TestModelDiff_ChangeSummary(t *testing.T) {
	m := newLoadedModel(t, fakeLoader{state: stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))})
	m = resize(m, 80, 24)
	m.diffLines = []diff.Line{
		{Kind: diff.KindFileHeader, Text: "--- a"},
		{Kind: diff.KindFileHeader, Text: "+++ b"},
		{Kind: diff.KindHunkHeader, Text: "@@ -1,1 +1,1 @@"},
		{Kind: diff.KindAdd, Text: "+a"},
		{Kind: diff.KindHunkHeader, Text: "@@ -5,1 +5,1 @@"},
		{Kind: diff.KindRemove, Text: "-b"},
		{Kind: diff.KindAdd, Text: "+c"},
	}
	m.diffTitle = "Diff · a"
	m.showDiff = true
	m.syncDiffContent()
	view := m.View()
	if !strings.Contains(view, "2 hunk(s)") {
		t.Errorf("view missing the hunk count:\n%s", view)
	}
	if !strings.Contains(view, "+2") || !strings.Contains(view, "−1") {
		t.Errorf("view missing the add/remove counts (+2 −1):\n%s", view)
	}
}

// TestModelDiff_HorizontalScroll verifies that l shifts the horizontal
// offset for long diff lines and h returns it, with the truncated
// indicator rendered while scrolled.
func TestModelDiff_HorizontalScroll(t *testing.T) {
	m := newLoadedModel(t, fakeLoader{state: stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))})
	m = resize(m, 60, 20)
	m.diffLines = []diff.Line{
		{Kind: diff.KindFileHeader, Text: "--- a"},
		{Kind: diff.KindFileHeader, Text: "+++ b"},
		{Kind: diff.KindHunkHeader, Text: "@@ -1,1 +1,1 @@"},
		{Kind: diff.KindAdd, Text: "+" + strings.Repeat("x", 120)},
	}
	m.diffTitle = "Diff · a"
	m.showDiff = true
	m.syncDiffContent()

	if m.diffHOffset != 0 {
		t.Fatalf("initial diffHOffset = %d, want 0", m.diffHOffset)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if m.diffHOffset != 4 {
		t.Fatalf("after l: diffHOffset = %d, want 4", m.diffHOffset)
	}
	view := m.View()
	if !strings.Contains(view, "…") {
		t.Errorf("scrolled diff missing the truncated indicator:\n%s", view)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if m.diffHOffset != 8 {
		t.Fatalf("after second l: diffHOffset = %d, want 8", m.diffHOffset)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	if m.diffHOffset != 0 {
		t.Errorf("after h: diffHOffset = %d, want back to 0", m.diffHOffset)
	}
}

// TestModelDiff_FooterHints verifies the diff modal footer documents the
// hunk and horizontal-scroll keys.
func TestModelDiff_FooterHints(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 120, 30)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	view := stripANSI(m.View())
	if !strings.Contains(view, "n/N hunk") {
		t.Errorf("diff footer missing the hunk keys:\n%s", view)
	}
	if !strings.Contains(view, "h/l scroll") {
		t.Errorf("diff footer missing the horizontal scroll keys:\n%s", view)
	}
}
