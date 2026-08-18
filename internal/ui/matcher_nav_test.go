package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
)

// matcherModel loads a model whose root document contains named matchers and
// renders it once so the source pane (sourceDoc) is populated.
func matcherModel(t *testing.T, src string) *Model {
	t.Helper()
	fs := fsReader(map[string]string{"config/Caddyfile": src})
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: true}, fs)
	m := newLoadedModel(t, loader)
	_ = m.View()
	if m.sourceDoc == nil {
		t.Fatal("source pane has no document after render")
	}
	return m
}

// TestGotoMatcher_CyclesOccurrences verifies that repeated g presses walk
// through every matcher occurrence (definition first, then references),
// re-anchoring the tree and revealing each line, wrapping at the end.
func TestGotoMatcher_CyclesOccurrences(t *testing.T) {
	src := "example.test {\n\t@api path /api/*\n\treverse_proxy @api localhost:8080\n\thandle @api {\n\t\trespond ok\n\t}\n}\n"
	m := matcherModel(t, src)

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if m.matcherNav == nil {
		t.Fatal("matcherNav not started after g")
	}
	if m.matcherNav.docPath != "config/Caddyfile" {
		t.Errorf("session docPath = %q, want %q", m.matcherNav.docPath, "config/Caddyfile")
	}
	assertMatcher(t, m, "def", 1)
	if m.sourceRevealLine != 2 {
		t.Errorf("sourceRevealLine = %d, want 2 (the definition line)", m.sourceRevealLine)
	}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	assertMatcher(t, m, "ref", 2)
	if m.sourceRevealLine != 3 {
		t.Errorf("sourceRevealLine = %d, want 3 (reverse_proxy line)", m.sourceRevealLine)
	}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	assertMatcher(t, m, "ref", 3)
	if m.sourceRevealLine != 4 {
		t.Errorf("sourceRevealLine = %d, want 4 (handle line)", m.sourceRevealLine)
	}

	// Wrapping returns to the first occurrence.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	assertMatcher(t, m, "def", 1)
}

// assertMatcher checks that the session cursor points at the occurrence index
// and the status message names the matcher and its def/ref kind.
func assertMatcher(t *testing.T, m *Model, kind string, idx int) {
	t.Helper()
	if m.matcherNav == nil {
		t.Fatal("matcherNav was cleared")
	}
	if got := m.matcherNav.cursor; got != idx-1 {
		t.Errorf("cursor = %d, want %d", got, idx-1)
	}
	if !strings.Contains(m.statusMessage, "matcher @api") || !strings.Contains(m.statusMessage, kind) {
		t.Errorf("status = %q, want a status naming matcher @api %s", m.statusMessage, kind)
	}
	if !strings.Contains(m.statusMessage, strconv.Itoa(idx)+"/"+strconv.Itoa(3)) {
		t.Errorf("status = %q, want it to include %s/%d", m.statusMessage, strconv.Itoa(idx), 3)
	}
}

// TestGotoMatcher_NoDocument verifies that g without a source document shows
// a helpful status and leaves the session cleared.
func TestGotoMatcher_NoDocument(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.sourceDoc = nil
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if m.matcherNav != nil {
		t.Fatal("matcherNav should stay nil with no document")
	}
	if !strings.Contains(m.statusMessage, "no document") {
		t.Errorf("status = %q, want a no-document message", m.statusMessage)
	}
}

// TestGotoMatcher_NoMatchers verifies that g on a document without named
// matchers reports it and clears any previous session.
func TestGotoMatcher_NoMatchers(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy localhost:8080\n}\n")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if m.matcherNav != nil {
		t.Fatal("matcherNav should stay nil with no named matchers")
	}
	if !strings.Contains(m.statusMessage, "no named matchers") {
		t.Errorf("status = %q, want a no-matchers message", m.statusMessage)
	}
}

// TestGotoMatcher_SessionRebuildsOnDifferentDocument verifies that a press
// after the source pane was pointed at another document rebuilds the refs
// list instead of indexing into a stale session.
func TestGotoMatcher_SessionRebuildsOnDifferentDocument(t *testing.T) {
	m := matcherModel(t, "example.test {\n\t@api path /api/*\n\treverse_proxy @api localhost:8080\n}\n")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	firstPath := m.matcherNav.docPath

	// Switching the source pane to a different document invalidates the
	// session; the next press must rebuild from the current document.
	m.matcherNav.docPath = "config/other"
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if m.matcherNav == nil {
		t.Fatal("matcherNav was cleared unexpectedly")
	}
	if m.matcherNav.docPath != firstPath {
		t.Errorf("rebuild docPath = %q, want %q (rebuilt from the current doc)", m.matcherNav.docPath, firstPath)
	}
}

// TestMatcherStartingRef picks the first occurrence at-or-after the line and
// falls back to 0 when every occurrence precedes it.
func TestMatcherStartingRef(t *testing.T) {
	refs := []caddyfile.MatcherRef{
		{Node: caddyfile.Node{Range: caddyfile.SourceRange{StartLine: 2}}},
		{Node: caddyfile.Node{Range: caddyfile.SourceRange{StartLine: 5}}},
		{Node: caddyfile.Node{Range: caddyfile.SourceRange{StartLine: 8}}},
	}
	if got := matcherStartingRef(refs, 1); got != 0 {
		t.Errorf("curLine 1 -> %d, want 0", got)
	}
	if got := matcherStartingRef(refs, 5); got != 1 {
		t.Errorf("curLine 5 -> %d, want 1", got)
	}
	if got := matcherStartingRef(refs, 9); got != 0 {
		t.Errorf("curLine 9 (past every occurrence) -> %d, want 0", got)
	}
}

// TestMatcherStatusFeedback verifies the status wording for a definition and
// a reference occurrence.
func TestMatcherStatusFeedback(t *testing.T) {
	def := caddyfile.MatcherRef{Name: "api", Definition: true, Node: caddyfile.Node{Name: "@api", Range: caddyfile.SourceRange{StartLine: 2}}}
	if got := matcherStatus(def, 0, 3); !strings.Contains(got, "matcher @api") || !strings.Contains(got, "def 1/3") {
		t.Errorf("definition status = %q", got)
	}
	ref := caddyfile.MatcherRef{Name: "api", Node: caddyfile.Node{Name: "reverse_proxy", Range: caddyfile.SourceRange{StartLine: 3}}}
	if got := matcherStatus(ref, 1, 3); !strings.Contains(got, "ref 2/3") || !strings.Contains(got, "reverse_proxy") {
		t.Errorf("reference status = %q", got)
	}
}

// TestCommandPalette_GotoMatcher verifies the g command is discoverable in
// the palette and gated on a selected document.
func TestCommandPalette_GotoMatcher(t *testing.T) {
	m := matcherModel(t, "example.test {\n\t@api path /api/*\n}\n")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	view := stripANSI(m.View())
	if !strings.Contains(view, "Goto matcher (next)") {
		t.Errorf("palette should list the matcher command, got:\n%s", view)
	}
	cmd, ok := m.commandForKey("g")
	if !ok || cmd != commandMatcherNext {
		t.Errorf("commandForKey(g) = %q, %v; want matcher-next, true", cmd, ok)
	}

	cmdDef, ok := commandDefinition(commandMatcherNext)
	if !ok {
		t.Fatal("matcher-next command not found in the catalog")
	}
	if !cmdDef.Enabled(m) {
		t.Errorf("matcher command should be enabled with a document present")
	}
	m.sourceDoc = nil
	if cmdDef.Enabled(m) {
		t.Errorf("matcher command should be disabled with no document present")
	}
	if got := cmdDef.Reason(m); got != "no document selected" {
		t.Errorf("matcher command Reason = %q, want %q", got, "no document selected")
	}
}

// TestGotoMatcher_RevealsLeafRef verifies that cycling to a matcher reference
// inside a leaf directive (which has no tree row of its own) re-anchors the
// tree on the nearest visible ancestor instead of dropping the selection.
func TestGotoMatcher_RevealsLeafRef(t *testing.T) {
	// The @api reference inside the nested respond leaf has no tree row of
	// its own, so revealMatcher falls back to the enclosing handle branch.
	src := "example.test {\n\t@api path /api/*\n\thandle @api {\n\t\trespond @api ok\n\t}\n}\n"
	m := matcherModel(t, src)
	// Advance through occurrences (def @api, handle @api refs, respond @api
	// ref) until the source reveal lands on the respond leaf line.
	for i := 0; i < 10; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
		if m.sourceRevealLine == 4 {
			break
		}
	}
	sel := m.selectedItem()
	if sel == nil {
		t.Fatal("no selection after reaching the leaf reference")
	}
	// The respond leaf has no tree row of its own, so the selectable row
	// re-anchors on its nearest visible ancestor, the handle block.
	if !strings.Contains(sel.label, "handle") {
		t.Errorf("leaf ref should re-anchor onto the handle ancestor, got row %q", sel.label)
	}
	if m.sourceRevealLine != 4 {
		t.Errorf("sourceRevealLine = %d, want 4 (the respond leaf line)", m.sourceRevealLine)
	}
}

// TestRevealMatcher_NoGraph verifies revealMatcher still reports and reveals
// the source line when the tree graph is unavailable, instead of anchoring.
func TestRevealMatcher_NoGraph(t *testing.T) {
	m := matcherModel(t, "example.test {\n\t@api path /api/*\n}\n")
	m.state.Graph = nil
	m.matcherNav = &matcherNav{docPath: "x", refs: []caddyfile.MatcherRef{
		{Name: "api", Definition: true, Node: caddyfile.Node{Name: "@api", Range: caddyfile.SourceRange{StartLine: 2}}},
	}}
	m.revealMatcher(m.matcherNav.refs[0], 1)
	if m.sourceRevealLine != 2 {
		t.Errorf("sourceRevealLine = %d, want 2 without a graph", m.sourceRevealLine)
	}
	if m.statusMessage == "" {
		t.Error("revealMatcher without a graph should still publish a status")
	}
}

// TestRevealMatcher_NoAncestor verifies the final fallback of revealMatcher:
// when the matcher's node is a leaf with no visible ancestor (a top-level
// directive), the tree re-anchors on the document row instead.
func TestRevealMatcher_NoAncestor(t *testing.T) {
	m := matcherModel(t, "example.test {\n\t@api path /api/*\n}\n")
	// A top-level leaf directive has no rendered ancestor, so
	// nearestVisibleAncestor returns nil and revealMatcher re-anchors on
	// the document row.
	topLevelLeaf := caddyfile.Node{
		Kind:  caddyfile.KindDirective,
		Name:  "totally_unknown_leaf",
		Range: caddyfile.SourceRange{StartLine: 1, EndLine: 1},
	}
	m.matcherNav = &matcherNav{docPath: m.sourceDoc.Path, refs: []caddyfile.MatcherRef{
		{Name: "api", Node: topLevelLeaf},
	}}
	m.revealMatcher(m.matcherNav.refs[0], 1)
	sel := m.selectedItem()
	if sel == nil || sel.key != itemKey(m.sourceDoc, nil) {
		t.Errorf("no-ancestor fallback should select the document row, got %v", sel)
	}
	if m.sourceRevealLine != 1 {
		t.Errorf("sourceRevealLine = %d, want 1", m.sourceRevealLine)
	}
}

// TestMatcherStatus_EmptyDirective verifies the status fallback that names a
// non-directive node when the containing directive name is empty.
func TestMatcherStatus_EmptyDirective(t *testing.T) {
	r := caddyfile.MatcherRef{Name: "api", Node: caddyfile.Node{Kind: caddyfile.KindSite, Range: caddyfile.SourceRange{StartLine: 2}}}
	if got := matcherStatus(r, 0, 1); !strings.Contains(got, "site") {
		t.Errorf("empty-directive status = %q, want it to name the node kind", got)
	}
}

// TestGotoMatcher_InvalidatesOnSamePathReload verifies that a structurally
// edited reload replacing a document (same path, different matchers) rebuilds
// the matcher session on the next press instead of reusing the stale refs.
func TestGotoMatcher_InvalidatesOnSamePathReload(t *testing.T) {
	srcBefore := "example.test {\n\t@old path /old/*\n\trespond @old ok\n}\n"
	srcAfter := "example.test {\n\t@new path /new/*\n\trespond @new ok\n}\n"
	m := matcherModel(t, srcBefore)
	_ = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if m.matcherNav == nil || len(m.matcherNav.refs) == 0 {
		t.Fatal("session not initialized on the first press")
	}
	if m.matcherNav.refs[0].Name != "old" {
		t.Fatalf("initial session matcher = %q, want old", m.matcherNav.refs[0].Name)
	}

	// Simulate a reload that replaces the document with new matchers while
	// keeping the same path. The reloaded document carries its own parsed
	// nodes, like a real load.
	reloaded := caddyfile.Parse([]byte(srcAfter))
	reloaded.Path = m.matcherNav.docPath
	m.sourceDoc = reloaded

	_ = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if m.matcherNav == nil {
		t.Fatal("session cleared after the reload press")
	}
	if m.matcherNav.docPath != "config/Caddyfile" {
		t.Errorf("session docPath = %q, want config/Caddyfile", m.matcherNav.docPath)
	}
	for _, ref := range m.matcherNav.refs {
		if ref.Name != "new" {
			t.Errorf("reloaded session still references matcher @%s, want only @new", ref.Name)
		}
	}
}
