package ui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// structuralFixture is a Caddyfile whose tree must show only structural
// rows: a site with a nested structural block (handle > reverse_proxy),
// an anonymous block, an empty site (a block-kind row without children)
// and several terminal directives that must stay hidden (header_up,
// respond anon, respond "hi").
const structuralFixture = "example.test {\n" +
	"\thandle /api/* {\n" +
	"\t\treverse_proxy api.localhost:8080 {\n" +
	"\t\t\theader_up Host {host}\n" +
	"\t\t}\n" +
	"\t}\n" +
	"\t{\n" +
	"\t\trespond anon\n" +
	"\t}\n" +
	"\trespond \"hi\"\n" +
	"}\n" +
	"empty.example.test {\n" +
	"}\n"

// structuralState builds a loaded model over structuralFixture plus an
// imported document, so imported documents stay top-level rows while the
// root shows the structural skeleton. Rows: Caddyfile, example.test,
// handle /api/*, reverse_proxy api.localhost:8080, anonymous block,
// empty.example.test, a.caddy, top.example.test.
func structuralState(t *testing.T) *app.State {
	t.Helper()
	return stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n" + structuralFixture,
		"config/sites/a.caddy": "top.example.test {\n\trespond top\n}\n",
	}))
}

// expandAll expands every branch so tests can exercise the deep tree
// structure regardless of the startup collapse layout (documents
// expanded, branches below them collapsed).
func expandAll(m *Model) *Model {
	m.collapsed = map[string]bool{}
	m.rebuildTree("")
	return m
}

// TestTree_StructuralNodesVisible verifies buildItems renders document
// rows and every structural node recursively: site blocks, nested
// directive blocks and the anonymous block, with hasChildren only on
// rows that can expand.
func TestTree_StructuralNodesVisible(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = expandAll(m)

	want := []struct {
		label       string
		depth       int
		hasNode     bool
		hasChildren bool
	}{
		{"Caddyfile", 0, false, true},
		{"example.test", 1, true, true},
		{"handle /api/*", 2, true, true},
		{"reverse_proxy api.localhost:8080", 3, true, false},
		{"anonymous block", 2, true, false},
		{"empty.example.test", 1, true, false},
		{"a.caddy", 0, false, true},
		{"top.example.test", 1, true, false},
	}
	if len(m.items) != len(want) {
		t.Fatalf("items = %d, want %d; labels = %v", len(m.items), len(want), itemLabels(m.items))
	}
	for i, w := range want {
		it := m.items[i]
		if it.label != w.label || it.depth != w.depth || it.hasNode != w.hasNode || it.hasChildren != w.hasChildren {
			t.Errorf("item[%d] = {label:%q depth:%d hasNode:%v hasChildren:%v}, want {label:%q depth:%d hasNode:%v hasChildren:%v}",
				i, it.label, it.depth, it.hasNode, it.hasChildren, w.label, w.depth, w.hasNode, w.hasChildren)
		}
		if it.key == "" {
			t.Errorf("item[%d] has an empty key", i)
		}
	}
}

// TestTree_LeafDirectivesHidden verifies terminal directives without
// children (header_up, respond, import) are not tree items while they
// remain in the parse tree.
func TestTree_LeafDirectivesHidden(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	labels := itemLabels(m.items)
	// Terminal directives are never tree rows: nested leaves (header_up,
	// respond) and the top-level import leaf are all hidden, while the
	// parse tree below keeps every one of them.
	for _, hidden := range []string{"header_up", "respond", "import sites/a.caddy"} {
		if strings.Contains(labels, hidden) {
			t.Errorf("tree shows the hidden leaf %q; labels = %v", hidden, labels)
		}
	}

	// The parse tree still holds every leaf node.
	var allNodes []caddyfile.Node
	var walk func(nodes []caddyfile.Node)
	walk = func(nodes []caddyfile.Node) {
		for _, n := range nodes {
			allNodes = append(allNodes, n)
			walk(n.Children)
		}
	}
	for _, doc := range state.Graph.Documents {
		walk(doc.Nodes)
	}
	var names []string
	for _, n := range allNodes {
		if n.Kind == caddyfile.KindDirective && len(n.Children) == 0 {
			names = append(names, n.Name)
		}
	}
	for _, want := range []string{"header_up", "respond", "import"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("parse tree lost the leaf directive %q; leaves = %v", want, names)
		}
	}
}

// TestTree_LeafSourceStillVisible verifies the source pane keeps every
// source line of a structural selection, leaf directives included.
func TestTree_LeafSourceStillVisible(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	// Select the example.test site row; its full source (leaves included)
	// must appear in the source pane.
	m = expandAll(m)
	m.cursor = 2
	view := stripANSI(m.View())
	for _, want := range []string{
		"example.test {",
		"handle /api/* {",
		"header_up Host {host}",
		"respond anon",
		`respond "hi"`,
		"empty.example.test {",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("source view missing %q:\n%s", want, view)
		}
	}
}

// TestTree_NestedNodeExpandCollapse verifies Enter/Space toggle a
// structural node (hiding and re-showing its structural children), Left
// collapses it, Right expands it, and the cursor stays anchored on the
// row's stable key. Leaf directives stay hidden in every state.
func TestTree_NestedNodeExpandCollapse(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = expandAll(m)

	// Select the handle branch (depth 2).
	m.cursor = 2
	sel := m.selectedItem()
	if sel == nil || sel.label != "handle /api/*" || !sel.hasChildren {
		t.Fatalf("selection = %+v, want the handle branch row", sel)
	}
	handleKey := sel.key
	if len(m.items) != 8 {
		t.Fatalf("items = %d before collapse, want 8", len(m.items))
	}

	// Enter collapses the branch: reverse_proxy (its only structural
	// child) disappears; header_up stays hidden in both states.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.items) != 7 {
		t.Errorf("items = %d after Enter, want 7 (reverse_proxy hidden)", len(m.items))
	}
	if !m.collapsed[handleKey] {
		t.Error("collapsed map does not record the handle row")
	}
	if got := m.selectedItem(); got == nil || got.key != handleKey {
		t.Errorf("cursor re-anchored on %+v, want the handle row key %q", got, handleKey)
	}
	if got := m.selectedItem(); got == nil || !got.collapsed {
		t.Error("selected row not marked collapsed after Enter")
	}

	// Right expands it again.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if len(m.items) != 8 {
		t.Errorf("items = %d after Right, want 8 (reverse_proxy visible again)", len(m.items))
	}
	if m.collapsed[handleKey] {
		t.Error("handle row still collapsed after Right")
	}
	if got := m.selectedItem(); got == nil || got.key != handleKey {
		t.Errorf("cursor re-anchored on %+v, want the handle row key %q", got, handleKey)
	}

	// Left collapses; a second Left is a no-op; Space expands.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if len(m.items) != 7 {
		t.Errorf("items = %d after Left, want 7", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyLeft}) // already collapsed: no-op
	if len(m.items) != 7 {
		t.Errorf("items = %d after second Left, want 7 (no-op)", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if len(m.items) != 8 {
		t.Errorf("items = %d after Space, want 8 (Space expands)", len(m.items))
	}
	// Right on an already-expanded row is a no-op.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if len(m.items) != 8 {
		t.Errorf("items = %d after Right on expanded row, want 8 (no-op)", len(m.items))
	}
	// Leaf directives never appear as rows in any state.
	labels := itemLabels(m.items)
	for _, hidden := range []string{"header_up", "respond"} {
		if strings.Contains(labels, hidden) {
			t.Errorf("hidden leaf %q became a row: %v", hidden, labels)
		}
	}
}

// TestTree_EnterSpaceOnRowsWithoutChildren verifies Enter/Space/←/→ on a
// row without children (an empty site block) collapse nothing and open no
// workflow, while the document row keeps toggling.
func TestTree_EnterSpaceOnRowsWithoutChildren(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = expandAll(m)

	// Select the empty site row (a TreeRow without children, not a Branch).
	m.cursor = 5
	sel := m.selectedItem()
	if sel == nil || sel.label != "empty.example.test" || sel.hasChildren {
		t.Fatalf("selection = %+v, want the empty site row (no children)", sel)
	}
	if len(m.items) != 8 {
		t.Fatalf("precondition: items = %d, want 8", len(m.items))
	}

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeySpace},
		{Type: tea.KeyLeft},
		{Type: tea.KeyRight},
	} {
		m = keyPress(t, m, key)
	}
	if len(m.items) != 8 {
		t.Errorf("items = %d after Enter/Space/←/→ on a row without children, want 8", len(m.items))
	}
	if m.cursor != 5 {
		t.Errorf("cursor = %d after Enter/Space/←/→, want unchanged 5", m.cursor)
	}
	if m.showDiff || m.showDiagnostics || m.searchActive || m.editing || m.showSaveConfirm {
		t.Error("Enter on a row without children opened a workflow")
	}

	// The document row (a root branch with visible children) still toggles.
	m.cursor = 0
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if len(m.items) != 3 {
		t.Errorf("items = %d after Space on the root document, want 3 (root + imported doc + its site)", len(m.items))
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if len(m.items) != 8 {
		t.Errorf("items = %d after Space again, want 8", len(m.items))
	}
}

// TestTree_ImportedDocsRemainTopLevel verifies imported documents stay
// separate top-level rows: the import directive is a hidden leaf inside
// the root document, never a duplicated syntax tree.
func TestTree_ImportedDocsRemainTopLevel(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})

	var topLevel []*item
	for i := range m.items {
		if m.items[i].depth == 0 {
			topLevel = append(topLevel, &m.items[i])
		}
	}
	if len(topLevel) != 2 {
		t.Fatalf("top-level rows = %d, want 2 (root + imported); items = %v", len(topLevel), itemLabels(m.items))
	}
	if topLevel[0].hasNode || topLevel[0].doc == nil || topLevel[0].doc.Path != "config/Caddyfile" {
		t.Errorf("topLevel[0] = %+v, want the root document row", topLevel[0])
	}
	if topLevel[1].hasNode || topLevel[1].doc == nil || topLevel[1].doc.Path != "config/sites/a.caddy" {
		t.Errorf("topLevel[1] = %+v, want the imported document row", topLevel[1])
	}
	if topLevel[1].key == topLevel[0].key {
		t.Error("imported row shares the root row key")
	}
}

// TestTree_SelectionStableAfterRebuild verifies the cursor re-anchors on
// the item key after every rebuild, including after a graph reload that
// rebuilds the tree.
func TestTree_SelectionStableAfterRebuild(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = expandAll(m)

	m.cursor = 4 // anonymous block row
	anchor := m.selectedItem().key

	// A rebuild (as a reload would trigger) re-anchors on the same key.
	m.items = buildItems(state.Graph, m.collapsed)
	m.rebuildTree(anchor)
	if got := m.selectedItem(); got == nil || got.key != anchor {
		t.Errorf("selection after rebuild = %+v, want key %q", got, anchor)
	}
	// A key that no longer exists falls back to clamping the cursor.
	m.rebuildTree("node:999:gone:0:1@config/Caddyfile")
	if m.selectedItem() == nil || m.cursor >= len(m.items) {
		t.Error("cursor out of range after a rebuild with a missing key")
	}
}

// TestSearch_LeafInCollapsedDoc verifies a global search covers the leaf
// directives of a collapsed document and that activating such a hit
// expands the ancestors, selects the nearest visible structural ancestor
// and reveals the exact source line without creating a tree row.
func TestSearch_LeafInCollapsedDoc(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	// A short window so the leaf line (4) sits below the first screen and
	// the line reveal actually scrolls the viewport.
	m = resize(m, 120, 12)

	// Collapse the root document: the whole example.test chain hides (the
	// imported document stays expanded). header_up is a leaf directive
	// inside the collapsed root.
	m.collapsed[itemKey(state.Graph.Documents[0], nil)] = true
	m.rebuildTree("")
	if len(m.items) != 3 {
		t.Fatalf("items = %d after collapsing the root, want 3 (root + imported doc + its site)", len(m.items))
	}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("header_up") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	nodeIdx := -1
	for i, r := range m.searchResults {
		if r.Kind == app.SearchNode && r.Node.Name == "header_up" {
			nodeIdx = i
		}
	}
	if nodeIdx < 0 {
		t.Fatalf("results = %+v, want the hidden header_up node hit", m.searchResults)
	}
	for m.searchCursor != nodeIdx {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	// The ancestors were expanded and the nearest visible structural
	// ancestor (the enclosing reverse_proxy block) is selected.
	if m.collapsed[itemKey(state.Graph.Documents[0], nil)] {
		t.Error("document still collapsed after activating the hidden leaf hit")
	}
	sel := m.selectedItem()
	if sel == nil || sel.label != "reverse_proxy api.localhost:8080" {
		t.Errorf("selection = %+v, want the reverse_proxy structural row", sel)
	}
	// The exact source line of the leaf is revealed (header_up is line 5
	// of the root document).
	m.View()
	if m.viewport.YOffset != 4 {
		t.Errorf("viewport YOffset = %d after the leaf hit, want 4 (header_up line)", m.viewport.YOffset)
	}
	if !strings.Contains(stripANSI(m.viewport.View()), "header_up Host {host}") {
		t.Errorf("source pane does not show the revealed leaf line:\n%s", m.viewport.View())
	}
	// No tree row was created for the leaf itself.
	if strings.Contains(itemLabels(m.items), "header_up") {
		t.Errorf("a tree row appeared for the hidden leaf: %v", itemLabels(m.items))
	}
}

// TestSearch_LeafExpandsAncestors verifies activating a leaf hit expands
// every collapsed ancestor (document, site, intermediate blocks) and
// selects the nearest visible structural ancestor at its correct depth.
func TestSearch_LeafExpandsAncestors(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	// Collapse the root document, the example.test site and the handle
	// block: header_up is four levels deep.
	root := state.Graph.Documents[0]
	site := root.Nodes[1]
	handle := site.Children[0]
	m.collapsed[itemKey(root, nil)] = true
	m.collapsed[itemKey(root, &site)] = true
	m.collapsed[itemKey(root, &handle)] = true
	m.rebuildTree("")
	// Rows: collapsed root doc, a.caddy doc, top.example.test.
	if len(m.items) != 3 {
		t.Fatalf("items = %d after collapsing the chain, want 3", len(m.items))
	}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("header_up") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	nodeIdx := -1
	for i, r := range m.searchResults {
		if r.Kind == app.SearchNode && r.Node.Name == "header_up" {
			nodeIdx = i
		}
	}
	if nodeIdx < 0 {
		t.Fatalf("results = %+v, want the header_up hit", m.searchResults)
	}
	for m.searchCursor != nodeIdx {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	// Every ancestor is expanded and the nearest visible structural
	// ancestor (the enclosing reverse_proxy block) is selected.
	if m.collapsed[itemKey(root, nil)] || m.collapsed[itemKey(root, &site)] || m.collapsed[itemKey(root, &handle)] {
		t.Error("an ancestor stayed collapsed after activating the hidden leaf hit")
	}
	sel := m.selectedItem()
	if sel == nil || sel.label != "reverse_proxy api.localhost:8080" {
		t.Fatalf("selection = %+v, want the reverse_proxy structural row", sel)
	}
	if sel.depth != 3 {
		t.Errorf("selected row depth = %d, want 3 (doc > site > handle > reverse_proxy)", sel.depth)
	}
}

// TestSearch_TopLevelLeafSelectsDocumentRow verifies a top-level leaf
// directive (an import) has no tree row of its own: activating its
// search hit selects the document row and reveals its source line.
func TestSearch_TopLevelLeafSelectsDocumentRow(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("sites/a.caddy") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	nodeIdx := -1
	for i, r := range m.searchResults {
		if r.Kind == app.SearchNode && r.Node.Name == "import" {
			nodeIdx = i
		}
	}
	if nodeIdx < 0 {
		t.Fatalf("results = %+v, want the import node hit", m.searchResults)
	}
	for m.searchCursor != nodeIdx {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	// The document row is selected (the import leaf is not a tree row)
	// and line 1 (the import line) is revealed.
	sel := m.selectedItem()
	if sel == nil || sel.hasNode || sel.doc == nil || sel.doc.Path != "config/Caddyfile" {
		t.Errorf("selection = %+v, want the root document row", sel)
	}
	m.View()
	if m.viewport.YOffset != 0 {
		t.Errorf("viewport YOffset = %d after the import hit, want 0 (line 1)", m.viewport.YOffset)
	}
	if !strings.Contains(stripANSI(m.viewport.View()), "import sites/a.caddy") {
		t.Errorf("source pane does not show the revealed import line:\n%s", m.viewport.View())
	}
	if strings.Contains(itemLabels(m.items), "import sites/a.caddy") {
		t.Errorf("a tree row appeared for the import leaf: %v", itemLabels(m.items))
	}
}

// TestSearch_LineHitSelectsContainingNode verifies that activating a
// source-line hit selects the deepest structural node containing the line
// instead of jumping to the document row, while still revealing the exact
// line. This is the fix for the reported behavior where only node hits
// positioned the tree cursor at the right point.
func TestSearch_LineHitSelectsContainingNode(t *testing.T) {
	src := "(snippet) {\n\trespond \"tranquillo\"\n}\n"
	state := stateFor(t, "Caddyfile", func(p string) ([]byte, error) { return []byte(src), nil })
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 12)

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("tranquillo") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Find the content-line hit (the snippet's respond directive is a
	// hidden nested leaf, so its label hit is the second result).
	lineIdx := -1
	for i, r := range m.searchResults {
		if r.Kind == app.SearchDocument && r.Line > 0 {
			lineIdx = i
		}
	}
	if lineIdx < 0 {
		t.Fatalf("results = %+v, want the content-line hit", m.searchResults)
	}
	for m.searchCursor != lineIdx {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	// The cursor lands on the snippet row (the deepest structural node
	// containing line 2), not on the document row.
	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.label != "snippet (snippet)" {
		t.Errorf("selection = %+v, want the snippet row containing the line", sel)
	}
	m.View()
	if m.viewport.YOffset != 1 {
		t.Errorf("viewport YOffset = %d after the line hit, want 1 (line 2)", m.viewport.YOffset)
	}
	if !strings.Contains(stripANSI(m.viewport.View()), "tranquillo") {
		t.Errorf("source pane does not show the revealed line:\n%s", m.viewport.View())
	}
}

// TestTree_VisibleChildrenPolicy verifies that a row's expandable state
// depends on its visible children only, never on raw parser children:
//   - a site whose only child is a hidden import renders as a leaf row;
//   - a header containing only hidden terminal directives renders as a
//     leaf row;
//   - a site containing a visible structural header block stays a branch;
//   - a document with visible snippets/sites stays a branch.
func TestTree_VisibleChildrenPolicy(t *testing.T) {
	src := "(shared_snippet) {\n\trespond ok\n}\n" +
		"import-only.test {\n\timport sites/extra.conf\n}\n" +
		"header-leaves.test {\n\theader {\n\t\tX-Frame-Options \"SAMEORIGIN\"\n\t}\n}\n" +
		"header-branch.test {\n\theaders {\n\t\thandle /api/* {\n\t\t\trespond hi\n\t\t}\n\t}\n}\n"
	state := stateFor(t, "Caddyfile", func(p string) ([]byte, error) {
		if p == "Caddyfile" {
			return []byte(src), nil
		}
		return nil, &noSuchFile{p}
	})
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = expandAll(m)

	byLabel := map[string]*item{}
	for i := range m.items {
		byLabel[m.items[i].label] = &m.items[i]
	}
	// A document with visible snippets/sites remains a branch.
	doc := byLabel["Caddyfile"]
	if doc == nil || !doc.hasChildren {
		t.Fatalf("document row = %+v, want a branch (visible snippets/sites)", doc)
	}
	// A site whose only child is a hidden import leaf renders as a leaf.
	if s := byLabel["import-only.test"]; s == nil || s.hasChildren {
		t.Errorf("import-only.test = %+v, want a leaf row (only a hidden import)", s)
	}
	// A header with only hidden terminal directives renders as a leaf.
	if h := byLabel["header"]; h == nil || h.hasChildren {
		t.Errorf("header = %+v, want a leaf row (only hidden terminal directives)", h)
	}
	// A site containing a visible structural header block stays a branch,
	// and that header is itself a branch (it has a visible child).
	if s := byLabel["header-branch.test"]; s == nil || !s.hasChildren {
		t.Errorf("header-branch.test = %+v, want a branch", s)
	}
	if h := byLabel["headers"]; h == nil || !h.hasChildren {
		t.Errorf("headers = %+v, want a branch (visible handle child)", h)
	}

	// The rendered markers match the policy: leaf rows (import-only.test
	// at depth 1) show no expansion marker; branch rows show the ASCII -
	// marker.
	view := stripANSI(m.View())
	if !strings.Contains(view, "      import-only.test") {
		t.Errorf("import-only.test must render as a leaf row without markers:\n%s", view)
	}
	if strings.Contains(view, "  +   import-only.test") || strings.Contains(view, "  -   import-only.test") {
		t.Errorf("import-only.test must not render an expansion marker:\n%s", view)
	}
	// Branch rows render with the - marker in the expansion column.
	for _, want := range []string{
		"  -   header-branch.test",
		"  -     headers",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("branch rows must show the - marker (%q):\n%s", want, view)
		}
	}
}

// TestTree_ExpandAllBranches verifies + expands every visible branch
// recursively (document roots included), preserves the selection, and is
// a no-op when everything is already expanded.
func TestTree_ExpandAllBranches(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	// Startup hides the deep structure behind the collapsed example.test.
	if strings.Contains(itemLabels(m.items), "handle /api/*") {
		t.Fatal("precondition: the deep structure must be hidden at startup")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	for _, want := range []string{"handle /api/*", "reverse_proxy api.localhost:8080", "anonymous block"} {
		if !strings.Contains(itemLabels(m.items), want) {
			t.Errorf("after + the tree must show %q; labels = %v", want, itemLabels(m.items))
		}
	}
	// The selection survives (the cursor was on the root document row).
	if m.cursor != 0 || m.selectedItem().hasNode {
		t.Errorf("cursor = %d (sel %+v) after +, want the preserved document row", m.cursor, m.selectedItem())
	}
	if len(m.collapsed) != 0 {
		t.Errorf("collapsed = %v after +, want everything expanded", m.collapsed)
	}
	// A second + is a no-op: nothing changes.
	before := itemLabels(m.items)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	if itemLabels(m.items) != before || m.cursor != 0 {
		t.Error("second + must be a no-op when the tree is already fully expanded")
	}
}

// TestTree_CollapseAll_NestedBranches verifies - collapses every branch
// below the document roots recursively, keeps the document roots
// expanded, preserves a surviving selection and moves a hidden selection
// to the nearest visible ancestor.
func TestTree_CollapseAll_NestedBranches(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")}) // expand all

	// Select a deep row (handle, depth 2) and collapse all: the deep rows
	// disappear and the selection moves to the nearest visible ancestor
	// (example.test, its depth-1 parent).
	m.cursor = 2 // handle /api/*
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-")})
	for _, hidden := range []string{"handle /api/*", "reverse_proxy api.localhost:8080", "anonymous block"} {
		if strings.Contains(itemLabels(m.items), hidden) {
			t.Errorf("after - the tree must hide %q; labels = %v", hidden, itemLabels(m.items))
		}
	}
	if got := m.selectedItem(); got == nil || got.label != "example.test" {
		t.Errorf("selection = %+v after -, want the nearest visible ancestor example.test", m.selectedItem())
	}
	// Document roots stay expanded.
	for _, doc := range state.Graph.Documents {
		if m.collapsed[itemKey(doc, nil)] {
			t.Errorf("document root %s must stay expanded after -", doc.Path)
		}
	}
	// A second - is a no-op: everything below the roots is already
	// collapsed.
	before := itemLabels(m.items)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-")})
	if itemLabels(m.items) != before {
		t.Error("second - must be a no-op when everything below the roots is collapsed")
	}
	// + restores the full tree and the selection stays anchored.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	if !strings.Contains(itemLabels(m.items), "handle /api/*") {
		t.Errorf("+ must re-expand the tree; labels = %v", itemLabels(m.items))
	}
	if got := m.selectedItem(); got == nil || got.label != "example.test" {
		t.Errorf("selection = %+v after +, want the still-visible example.test row", m.selectedItem())
	}
}

// TestTree_CollapseAll_MultipleDocuments verifies - collapses branches in
// every document while keeping each document root expanded, and + expands
// them again.
func TestTree_CollapseAll_MultipleDocuments(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": "top.example.test {\n\thandle /x {\n\t\trespond ok\n\t}\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})

	// Select a deep row of the imported document and collapse all.
	m.cursor = 3 // handle /x, inside the imported document
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-")})
	if got := m.selectedItem(); got == nil || got.label != "top.example.test" {
		t.Errorf("selection = %+v after -, want top.example.test (its depth-1 ancestor)", m.selectedItem())
	}
	// The imported document root stays expanded; its branch is collapsed.
	imported := state.Graph.Documents[1]
	if m.collapsed[itemKey(imported, nil)] {
		t.Error("imported document root must stay expanded after -")
	}
	if !m.collapsed[itemKey(imported, &imported.Nodes[0])] {
		t.Error("top.example.test must be collapsed after -")
	}
	if strings.Contains(itemLabels(m.items), "handle /x") {
		t.Errorf("deep rows of the imported document must be hidden after -; labels = %v", itemLabels(m.items))
	}
	// + expands it again.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	if !strings.Contains(itemLabels(m.items), "handle /x") {
		t.Errorf("+ must reveal handle /x again; labels = %v", itemLabels(m.items))
	}
}

// TestTree_CollapseAll_NoOpWithoutBranches verifies - and + are no-ops
// when no expandable branch exists below the document roots.
func TestTree_CollapseAll_NoOpWithoutBranches(t *testing.T) {
	src := "example.test {\n}\nimport-only.test {\n\timport x.conf\n}\n"
	state := stateFor(t, "Caddyfile", func(p string) ([]byte, error) {
		if p == "Caddyfile" {
			return []byte(src), nil
		}
		return nil, &noSuchFile{p}
	})
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	before := itemLabels(m.items)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-")})
	if itemLabels(m.items) != before || m.cursor != 0 {
		t.Error("- must be a no-op when no branch exists below the roots")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	if itemLabels(m.items) != before {
		t.Error("+ must be a no-op when nothing is collapsed")
	}
}

// TestTree_CollapseAll_StartupStateIsNoOp verifies - on the startup
// layout (branches below the roots already collapsed) is a no-op, and +
// produces the fully expanded tree.
func TestTree_CollapseAll_StartupStateIsNoOp(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	if !m.items[1].collapsed || m.items[1].label != "example.test" {
		t.Fatalf("precondition: example.test must start collapsed")
	}
	before := itemLabels(m.items)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-")})
	if itemLabels(m.items) != before {
		t.Error("- on the startup layout must be a no-op (already collapsed below the roots)")
	}
	// + expands everything from the startup layout.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	if !strings.Contains(itemLabels(m.items), "handle /api/*") {
		t.Errorf("+ must expand the startup layout; labels = %v", itemLabels(m.items))
	}
}

// TestTree_ExpandCollapseAll_FooterHints verifies the footer advertises
// the + expand all and - collapse all keys in the main view.
func TestTree_ExpandCollapseAll_FooterHints(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	// The footer wraps onto additional lines at 120 cols; flatten the
	// whitespace so a hint split across a line break is still found.
	view := strings.Join(strings.Fields(stripANSI(m.View())), " ")
	for _, hint := range []string{"+ expand all", "- collapse all"} {
		if !strings.Contains(view, hint) {
			t.Errorf("footer missing %q:\n%s", hint, m.View())
		}
	}
}

// TestSearch_LineHitInCollapsedImportedDoc is a regression test for the
// SearchDocument activation: a content-line hit inside a collapsed
// imported document must expand the document and its structural
// ancestors, select the correct structural row and reveal the exact
// source line. Previously the branch did not expand ancestors before
// rebuilding, so the selection was lost when the document or a containing
// branch was collapsed.
func TestSearch_LineHitInCollapsedImportedDoc(t *testing.T) {
	importedSrc := "top.example.test {\n" +
		"\thandle /api/* {\n" +
		"\t\trespond target-hit\n" +
		"\t}\n" +
		"}\n" +
		strings.Repeat("# padding\n", 50)
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	// Collapse the imported document: its rows disappear from the tree.
	imported := state.Graph.Documents[1]
	m.collapsed[itemKey(imported, nil)] = true
	m.rebuildTree("")
	if len(m.items) != 2 {
		t.Fatalf("items = %d after collapsing the imported doc, want 2", len(m.items))
	}

	// Search for a content line inside the collapsed document (line 3,
	// inside handle /api/* which is inside top.example.test).
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("target-hit") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	lineIdx := -1
	for i, r := range m.searchResults {
		if r.Kind == app.SearchDocument && r.Line > 0 {
			lineIdx = i
		}
	}
	if lineIdx < 0 {
		t.Fatalf("results = %+v, want the imported-file content hit", m.searchResults)
	}
	for m.searchCursor != lineIdx {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	// The document and its structural ancestors were expanded and the
	// deepest structural row containing the line is selected.
	if m.collapsed[itemKey(imported, nil)] {
		t.Error("imported document still collapsed after the line hit")
	}
	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.label != "handle /api/*" {
		t.Fatalf("selection = %+v, want the handle /api/* structural row", m.selectedItem())
	}
	if sel.doc == nil || sel.doc.Path != "config/sites/a.caddy" {
		t.Errorf("selection doc = %v, want the imported file", sel.doc)
	}
	// The exact source line (1-based 3) is revealed below the fold.
	m.View()
	if m.viewport.YOffset != 2 {
		t.Errorf("viewport YOffset = %d after the line hit, want 2 (line 3)", m.viewport.YOffset)
	}
	if !strings.Contains(stripANSI(m.viewport.View()), "respond target-hit") {
		t.Errorf("source pane does not show the revealed line:\n%s", m.viewport.View())
	}
}

// TestSearch_LineHitInTopLevelLeafExpandsDocumentOnly verifies a line hit
// inside a hidden top-level leaf (an import directive) expands the
// document root only and selects the document row.
func TestSearch_LineHitInTopLevelLeafExpandsDocumentOnly(t *testing.T) {
	src := "import sites/a.caddy\nexample.test {\n\trespond ok\n}\n"
	state := stateFor(t, "Caddyfile", func(p string) ([]byte, error) {
		if p == "Caddyfile" {
			return []byte(src), nil
		}
		return nil, &noSuchFile{p}
	})
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	// Collapse the root document: the site row hides.
	root := state.Graph.Documents[0]
	m.collapsed[itemKey(root, nil)] = true
	m.rebuildTree("")
	if len(m.items) != 1 {
		t.Fatalf("items = %d after collapsing the root, want 1", len(m.items))
	}

	// Search for the import content line (line 1, inside the hidden
	// top-level import leaf).
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range []rune("sites/a.caddy") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	lineIdx := -1
	for i, r := range m.searchResults {
		if r.Kind == app.SearchDocument && r.Line > 0 {
			lineIdx = i
		}
	}
	if lineIdx < 0 {
		t.Fatalf("results = %+v, want the import content hit", m.searchResults)
	}
	for m.searchCursor != lineIdx {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	// The document root is expanded and the document row is selected;
	// the import line is revealed (line 1, the top of the source).
	if m.collapsed[itemKey(root, nil)] {
		t.Error("document root still collapsed after the line hit")
	}
	sel := m.selectedItem()
	if sel == nil || sel.hasNode || sel.doc == nil || sel.doc.Path != "Caddyfile" {
		t.Fatalf("selection = %+v, want the document row", m.selectedItem())
	}
	m.View()
	if !strings.Contains(stripANSI(m.viewport.View()), "import sites/a.caddy") {
		t.Errorf("source pane does not show the import line:\n%s", m.viewport.View())
	}
}

// TestTree_FooterAndMarkers verifies the canonical markers: the selection
// marker (›) and the expansion markers (- expanded, + collapsed) live in
// separate columns, Unicode glyphs are never used, rows without visible
// children carry no expansion marker, and the footer advertises toggle
// only when the selected row is a branch.
func TestTree_FooterAndMarkers(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = expandAll(m)

	// An expanded branch shows -, a collapsed branch +, in the expansion
	// column (the second column, after the selection marker).
	block := m.items[2] // handle /api/*, expanded
	if got := renderTreeRow(block, true); got != "› - "+strings.Repeat("  ", 2)+"handle /api/*" {
		t.Errorf("selected expanded branch render = %q, want the › and - markers", got)
	}
	collapsedBlock := block
	collapsedBlock.collapsed = true
	if got := renderTreeRow(collapsedBlock, false); got != "  + "+strings.Repeat("  ", 2)+"handle /api/*" {
		t.Errorf("unselected collapsed branch render = %q, want the + marker", got)
	}
	// A row without children (empty site) carries no expansion marker.
	noChildren := m.items[5] // empty.example.test
	if got := renderTreeRow(noChildren, true); got != "›   "+strings.Repeat("  ", 1)+"empty.example.test" {
		t.Errorf("selected row without children render = %q, want only the › marker", got)
	}
	// The expansion column uses ASCII -/+; the Unicode ▾/▸ and − are
	// never used.
	for _, row := range []item{block, collapsedBlock, noChildren} {
		got := renderTreeRow(row, true)
		if strings.ContainsAny(got, "▾▸−") {
			t.Errorf("render %q must not use Unicode expansion markers", got)
		}
	}

	// Footer: a row with children advertises toggle; a row without
	// children does not.
	if !strings.Contains(stripANSI(m.footer(120)), "toggle") {
		t.Errorf("footer on a branch must advertise toggle:\n%s", m.footer(120))
	}
	m.cursor = 5 // empty site row
	if strings.Contains(stripANSI(m.footer(120)), "toggle") {
		t.Errorf("footer on a row without children must not advertise toggle:\n%s", m.footer(120))
	}
}

// TestTree_MarkerColumnsInPane verifies the rendered tree pane keeps the
// selection and expansion markers in separate fixed columns: › for the
// selected row, -/+ for the branch state, and no marker on rows without
// visible children. Unicode expansion glyphs never appear. The pane is
// rendered with the startup layout (branches below the document roots
// collapsed), so example.test shows the + marker.
func TestTree_MarkerColumnsInPane(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	view := stripANSI(m.View())
	for _, want := range []string{
		"› - Caddyfile",            // selected, expanded document row
		"  +   example.test",       // unselected, collapsed branch
		"      empty.example.test", // row without children: no markers
	} {
		if !strings.Contains(view, want) {
			t.Errorf("tree pane missing %q:\n%s", want, view)
		}
	}
	for _, bad := range []string{"▾ Caddyfile", "▸ Caddyfile", "− Caddyfile"} {
		if strings.Contains(view, bad) {
			t.Errorf("tree pane must not use Unicode expansion markers (found %q):\n%s", bad, view)
		}
	}
}

// TestTree_StartupLayout verifies the initial tree state on a fresh
// session: every document root is expanded, every visible branch below
// the document roots starts collapsed, visible leaves carry no marker,
// the cursor starts deterministically on the first document row, and a
// second fresh model never inherits the previous session's expansion
// state.
func TestTree_StartupLayout(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)

	initialCollapsed := map[string]bool{}
	for k, v := range m.collapsed {
		initialCollapsed[k] = v
	}

	// Rows on startup: the two document roots (expanded) plus their
	// top-level branches (collapsed) and visible leaf rows (no marker).
	want := []struct {
		label       string
		hasChildren bool
		collapsed   bool
	}{
		{"Caddyfile", true, false},           // document root: expanded
		{"example.test", true, true},         // branch below the root: collapsed
		{"empty.example.test", false, false}, // visible leaf row
		{"a.caddy", true, false},             // imported document root: expanded
		{"top.example.test", false, false},   // leaf row (only child is hidden)
	}
	if len(m.items) != len(want) {
		t.Fatalf("items = %d, want %d; labels = %v", len(m.items), len(want), itemLabels(m.items))
	}
	for i, w := range want {
		it := m.items[i]
		if it.label != w.label || it.hasChildren != w.hasChildren || it.collapsed != w.collapsed {
			t.Errorf("item[%d] = {label:%q hasChildren:%v collapsed:%v}, want {label:%q hasChildren:%v collapsed:%v}",
				i, it.label, it.hasChildren, it.collapsed, w.label, w.hasChildren, w.collapsed)
		}
	}
	// The cursor starts deterministically on the first visible document
	// row.
	if m.cursor != 0 || m.selectedItem() == nil || m.selectedItem().hasNode {
		t.Errorf("initial cursor = %d (sel %+v), want the first document row", m.cursor, m.selectedItem())
	}
	// The deep structure is hidden until a branch is expanded.
	labels := itemLabels(m.items)
	for _, hidden := range []string{"handle /api/*", "reverse_proxy api.localhost:8080", "anonymous block"} {
		if strings.Contains(labels, hidden) {
			t.Errorf("startup tree must hide %q behind the collapsed example.test; labels = %v", hidden, labels)
		}
	}
	// Rendered markers: - for expanded document roots, + for collapsed
	// branches, blank for visible leaves.
	view := stripANSI(m.View())
	for _, want := range []string{
		"› - Caddyfile",
		"  +   example.test",
		"      empty.example.test",
		"  - a.caddy",
		"      top.example.test",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("startup pane missing %q:\n%s", want, view)
		}
	}
	// Expanding a collapsed branch reveals its children, with the next
	// level still collapsed.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})  // example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // expand it
	labels = itemLabels(m.items)
	for _, want := range []string{"handle /api/*", "anonymous block"} {
		if !strings.Contains(labels, want) {
			t.Errorf("expanded example.test must show %q; labels = %v", want, labels)
		}
	}
	if strings.Contains(labels, "reverse_proxy api.localhost:8080") {
		t.Errorf("handle stays collapsed on expand: reverse_proxy must be hidden; labels = %v", labels)
	}

	// A fresh session never inherits the previous session's expansion
	// state: a second model over the same graph starts with the exact
	// same startup layout.
	m2 := newLoadedModel(t, fakeLoader{state: state})
	if !reflect.DeepEqual(m2.collapsed, initialCollapsed) {
		t.Errorf("fresh session collapse state = %v, want the startup layout %v", m2.collapsed, initialCollapsed)
	}
	if len(m2.items) != len(want) || m2.cursor != 0 {
		t.Errorf("fresh session items/cursor = %d/%d, want %d/0", len(m2.items), m2.cursor, len(want))
	}
}

// TestTree_AnonymousBlockLabel verifies an anonymous `{ ... }` block
// renders with an explicit label. In structuralFixture its only child is
// a hidden respond leaf, so the anonymous block renders as a leaf row:
// no expansion marker, Enter/Space do nothing and nothing collapses.
func TestTree_AnonymousBlockLabel(t *testing.T) {
	state := structuralState(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	m = expandAll(m)

	m.cursor = 4
	sel := m.selectedItem()
	if sel == nil || sel.label != "anonymous block" {
		t.Fatalf("selection = %+v, want the anonymous block row", sel)
	}
	if sel.hasChildren {
		t.Fatal("anonymous block must be a leaf row: its only child is a hidden respond leaf")
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "anonymous block") {
		t.Errorf("view missing the anonymous block label:\n%s", view)
	}
	// It is a leaf row: Enter/Space/←/→ do nothing and no state changes.
	// The render carries only the selection marker, never an expansion
	// marker.
	if got := renderTreeRow(*sel, true); got != "›       anonymous block" {
		t.Errorf("leaf row render = %q, want only the selection marker", got)
	}
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeySpace},
		{Type: tea.KeyLeft},
		{Type: tea.KeyRight},
	} {
		m = keyPress(t, m, key)
	}
	if got := m.selectedItem(); got == nil || got.label != "anonymous block" || got.collapsed {
		t.Errorf("cursor/state = %+v after leaf keys, want the untouched anonymous block row", got)
	}
}
