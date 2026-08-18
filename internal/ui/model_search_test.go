package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	tea "github.com/charmbracelet/bubbletea"
)

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
	// Results are occurrence-based: the target.example.test site node and
	// the nested respond leaf each carry their line, deduplicated against
	// the matching content lines. The site node is the first result.
	if len(m.searchResults) != 2 {
		t.Fatalf("precondition: %d results, want the two node hits", len(m.searchResults))
	}
	if m.searchResults[0].Kind != app.SearchNode || m.searchResults[0].Node.Name != "target.example.test" {
		t.Fatalf("hit[0] = %+v, want the target site node hit", m.searchResults[0])
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
	for _, r := range []rune("padding") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// The padding lines match no node label, so every result is a plain
	// content hit carrying its exact line; the first is line 4.
	if len(m.searchResults) < 2 {
		t.Fatalf("results = %d, want the padding content hits", len(m.searchResults))
	}
	if m.searchResults[0].Kind != app.SearchDocument || m.searchResults[0].Doc == nil || m.searchResults[0].Doc.Path != "config/sites/a.caddy" || m.searchResults[0].Line != 4 {
		t.Fatalf("hit[0] = %+v, want the first padding content hit on line 4", m.searchResults[0])
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	sel := m.selectedItem()
	// The line sits outside every tree row (top-level padding), so the
	// imported file's document row is selected.
	if sel == nil || sel.hasNode || sel.doc == nil || sel.doc.Path != "config/sites/a.caddy" {
		t.Errorf("selection = %+v, want the imported document row", sel)
	}
	if m.sourceRevealLine != 4 {
		t.Errorf("sourceRevealLine = %d, want 4 (the first padding line)", m.sourceRevealLine)
	}
	// The render consumes the one-shot reveal and positions the viewport
	// (the centred reveal clamps to the top for a line this close to the
	// file start, so the line is visible at the first row).
	m.View()
	if m.sourceRevealLine != 0 {
		t.Errorf("sourceRevealLine = %d after render, want it consumed", m.sourceRevealLine)
	}
	if !strings.Contains(stripANSI(m.viewport.View()), "padding") {
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

// TestSearch_FooterUsesCommandPalette verifies that search stays available
// through its direct hotkey without expanding the normal footer.
func TestSearch_FooterUsesCommandPalette(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))

	// With the default searcher the normal footer remains compact.
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	if strings.Contains(stripANSI(m.View()), "/ search") || !strings.Contains(stripANSI(m.View()), "? commands") {
		t.Errorf("footer should not list operational search:\n%s", m.View())
	}

	// Without a searcher the key is absent.
	m2 := newLoadedModelWithoutSearcher(t, fakeLoader{state: state})
	m2 = resize(m2, 120, 30)
	if !strings.Contains(stripANSI(m2.View()), "? commands") {
		t.Errorf("footer missing command-palette hint without a searcher:\n%s", m2.View())
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

func TestSearch_ViewUsesPaletteInputTreatment(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("respond") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	view := stripANSI(m.View())
	for _, want := range []string{"SEARCH > respond▌", "result(s)", "Enter open", "Esc close"} {
		if !strings.Contains(view, want) {
			t.Errorf("search modal missing %q:\n%s", want, view)
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
	m.collapsed[itemKey(m.state.Graph.Documents[1], nil)] = true
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
	if m.collapsed[itemKey(m.state.Graph.Documents[1], nil)] {
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

func TestSearch_QuitKey(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("ctrl+c in search did not request quit")
	}
}

func TestSearch_RecomputeWithoutSearcher(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModelWithoutSearcher(t, fakeLoader{state: state})
	m.searchResults = []app.SearchResult{{Kind: app.SearchNode}}
	m.recomputeSearch()
	if m.searchResults != nil || m.searchCursor != 0 {
		t.Errorf("recomputeSearch without a searcher left (%v, %d)", m.searchResults, m.searchCursor)
	}
}

func TestSearch_NilDocumentSkipped(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.state.Graph.Documents = append(m.state.Graph.Documents, nil)
	m.searchQuery = []rune("example")
	m.recomputeSearch() // must not panic on the nil document
	if len(m.searchResults) == 0 {
		t.Error("recomputeSearch skipped the healthy documents")
	}
}

func TestSearch_NodeHitWithoutDocument(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.activateSearchResult(app.SearchResult{Kind: app.SearchNode}) // Doc == nil
	expandNodeAncestors(nil, caddyfile.Node{}, m.collapsed)        // direct: doc == nil no-op
	// No panic; the tree stays untouched.
}

func TestSearch_DocumentHitOutsideRows(t *testing.T) {
	fs := map[string]string{"config/Caddyfile": "example.test {\n\trespond ok\n}\n"}
	state := stateFor(t, "config/Caddyfile", fsReader(fs))
	m := newLoadedModel(t, fakeLoader{state: state})
	doc := m.state.Graph.Root
	m.collapsed[itemKey(doc, nil)] = true
	// A line inside the hidden top-level leaf is outside every tree row:
	// the collapsed document entry is dropped instead of expanded.
	m.activateSearchResult(app.SearchResult{Kind: app.SearchDocument, Doc: doc, Line: 2})
	if m.collapsed[itemKey(doc, nil)] {
		t.Error("document hit outside tree rows left the document collapsed")
	}
}

func TestSearch_LogHitOutOfRange(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.activateSearchResult(app.SearchResult{Kind: app.SearchLog, LogIndex: 42}) // no log lines loaded
	if m.showLogs || m.logDetailOpen {
		t.Error("out-of-range log hit opened the log view")
	}
}

func TestSearch_NodeHitMissingFromTree(t *testing.T) {
	fs := map[string]string{"config/Caddyfile": "example.test {\n\trespond ok\n}\n"}
	state := stateFor(t, "config/Caddyfile", fsReader(fs))
	m := newLoadedModel(t, fakeLoader{state: state})
	doc := m.state.Graph.Root
	// A leaf directive that is not part of doc's tree: the ancestor walk
	// finds nothing and the hit falls back to the document row.
	m.activateSearchResult(app.SearchResult{Kind: app.SearchNode, Doc: doc, Node: caddyfile.Node{Kind: caddyfile.KindDirective, Name: "ghost"}})
	if len(m.items) == 0 || m.items[m.cursor].hasNode {
		t.Errorf("cursor %d should land on the document row for a missing node", m.cursor)
	}
}

func TestSearch_ViewSizing(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})

	for _, size := range []struct{ w, h int }{{0, 10}, {10, 0}, {2, 2}, {30, 8}, {120, 60}} {
		if got := m.searchView(size.w, size.h); got == "" {
			t.Errorf("searchView(%d, %d) rendered empty", size.w, size.h)
		}
	}
	m.syncSearchViewport(0, 5)
	m.syncSearchViewport(5, 0)

	// A query with no hits renders the empty state; the reveal scrolls
	// the selected row into view once the cursor leaves the window.
	m.searchQuery = []rune("zzzz")
	if view := stripANSI(m.searchView(100, 30)); !strings.Contains(view, "no matches") {
		t.Errorf("no-hit search missing the empty state:\n%s", view)
	}
	m.searchResults = []app.SearchResult{{Label: "a"}, {Label: "b"}, {Label: "c"}, {Label: "d"}}
	m.syncSearchViewport(60, 2)
	m.searchCursor = 3
	m.revealSearchCursor()
	if m.searchViewport.YOffset == 0 {
		t.Error("revealSearchCursor did not scroll a cursor past the viewport")
	}
	down := m.searchViewport.YOffset
	m.searchCursor = 0
	m.revealSearchCursor()
	if m.searchViewport.YOffset >= down {
		t.Errorf("revealSearchCursor did not scroll a cursor above the viewport (%d -> %d)", down, m.searchViewport.YOffset)
	}

	// A one-column viewport clamps the row text width.
	m.syncSearchViewport(1, 5)
	if m.searchViewport.Width != 1 {
		t.Errorf("syncSearchViewport(1, 5) width = %d, want 1", m.searchViewport.Width)
	}
}

// TestSearch_CommentHitSelectsCommentRow verifies that activating a search
// hit inside a top-level comment selects the comment's row in the comments
// branch (expanding it) and reveals the exact line, instead of jumping back
// to the document row at the top of the tree.
func TestSearch_CommentHitSelectsCommentRow(t *testing.T) {
	src := "# leading comment about grafana\n" +
		"example.test {\n\trespond ok\n}\n" +
		"# trailing comment about grafana\n" +
		"other.test {\n\trespond ok\n}\n"
	state := stateFor(t, "Caddyfile", fsReader(map[string]string{"Caddyfile": src}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	_ = m.View()

	// Move the cursor deep (the second site) so the jump is observable.
	for m.selectedItem() == nil || m.selectedItem().label != "other.test" {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	before := m.cursor

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("grafana") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Both comment hits are content hits (comments are not nodes).
	if len(m.searchResults) != 2 {
		t.Fatalf("results = %d, want the two comment line hits", len(m.searchResults))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	sel := m.selectedItem()
	if sel == nil || sel.comment == nil || sel.label != "lines 1–1" {
		t.Fatalf("selection = %+v (cursor %d, before %d), want the comment leaf row", sel, m.cursor, before)
	}
	if m.cursor == 0 {
		t.Error("cursor = 0, want the comment row in the comments branch, not the document row")
	}
	if m.sourceRevealLine != 1 {
		t.Errorf("sourceRevealLine = %d, want 1 (the comment line)", m.sourceRevealLine)
	}
	// The source pane identifies the comment selection.
	_ = m.View()
	if !strings.Contains(m.sourceTitle, "comment (lines 1-1)") {
		t.Errorf("source title = %q, want the comment line span", m.sourceTitle)
	}
}

// TestSearch_CommentInsideBlockSelectsBlock verifies a comment inside a
// structural block (not a top-level comment group) still selects the
// containing block, never the document row.
func TestSearch_CommentInsideBlockSelectsBlock(t *testing.T) {
	src := "example.test {\n\t# inner note\n\trespond ok\n}\n"
	state := stateFor(t, "Caddyfile", fsReader(map[string]string{"Caddyfile": src}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	_ = m.View()

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("inner note") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.searchResults) != 1 {
		t.Fatalf("results = %d, want the inner comment hit", len(m.searchResults))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.node.Name != "example.test" {
		t.Errorf("selection = %+v, want the containing site block", sel)
	}
	if m.sourceRevealLine != 2 {
		t.Errorf("sourceRevealLine = %d, want 2 (the comment line)", m.sourceRevealLine)
	}
}
