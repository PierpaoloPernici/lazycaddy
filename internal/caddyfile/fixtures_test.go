package caddyfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assertNestedRanges verifies that every child range lies inside its parent
// and that top-level nodes are ordered and non-overlapping.
func assertNestedRanges(t *testing.T, src []byte, nodes []Node) {
	t.Helper()
	for i := 1; i < len(nodes); i++ {
		prev, cur := nodes[i-1], nodes[i]
		if prev.Range.End > cur.Range.Start {
			t.Errorf("top-level nodes overlap: %q [%d:%d) vs %q [%d:%d)",
				prev.Name, prev.Range.Start, prev.Range.End, cur.Name, cur.Range.Start, cur.Range.End)
		}
	}
	var walk func(ns []Node, parent *Node)
	walk = func(ns []Node, parent *Node) {
		for _, n := range ns {
			if !n.Range.Valid(len(src)) {
				t.Errorf("node %q range %+v invalid for %d-byte source", n.Name, n.Range, len(src))
			}
			if parent != nil && !(n.Range.Start >= parent.Range.Start && n.Range.End <= parent.Range.End) {
				t.Errorf("child %q range %+v escapes parent %q range %+v", n.Name, n.Range, parent.Name, parent.Range)
			}
			walk(n.Children, &n)
		}
	}
	walk(nodes, nil)
}

// patchRangeAndCompare patches a node range and asserts the result is
// exactly src with the node's raw text replaced, i.e. every unrelated byte
// is preserved.
func patchRangeAndCompare(t *testing.T, src []byte, n Node, replacement string) []byte {
	t.Helper()
	patched, err := Patch(src, n.Range, []byte(replacement))
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	want := strings.Replace(string(src), n.Range.Text(src), replacement, 1)
	if string(patched) != want {
		t.Fatalf("patched source differs from expected\n got: %q\nwant: %q", patched, want)
	}
	return patched
}

func TestCompatFixtureParses(t *testing.T) {
	src := loadFixture(t, "compat")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 5 {
		t.Fatalf("top-level nodes = %d, want 5 (globals, snippet, named route, 2 sites)", len(doc.Nodes))
	}
	kinds := []Kind{doc.Nodes[0].Kind, doc.Nodes[1].Kind, doc.Nodes[2].Kind, doc.Nodes[3].Kind, doc.Nodes[4].Kind}
	wantKinds := []Kind{KindGlobalOptions, KindSnippet, KindNamedRoute, KindSite, KindSite}
	for i := range kinds {
		if kinds[i] != wantKinds[i] {
			t.Errorf("node[%d].Kind = %v, want %v", i, kinds[i], wantKinds[i])
		}
	}

	snippet := doc.Nodes[1]
	if snippet.Name != "template" {
		t.Errorf("snippet name = %q, want template", snippet.Name)
	}
	if len(snippet.Children) != 2 || !snippet.Children[0].IsDirective("header") || !snippet.Children[1].IsDirective("respond") {
		t.Fatalf("snippet children = %v, want header + respond", names(snippet.Children))
	}
	// The heredoc respond keeps its raw opener, body and trailing argument.
	heredoc := snippet.Children[1]
	if !strings.Contains(heredoc.Args, "<<HTML") || !strings.Contains(heredoc.Args, "200") {
		t.Errorf("heredoc respond args = %q, want <<HTML marker and trailing 200", heredoc.Args)
	}

	route := doc.Nodes[2]
	if route.Kind != KindNamedRoute || route.Name != "app-route" {
		t.Fatalf("node[2] = kind %v name %q, want named route app-route", route.Kind, route.Name)
	}
	if len(route.Children) != 1 || !route.Children[0].IsDirective("reverse_proxy") {
		t.Errorf("named route children = %v, want one reverse_proxy", names(route.Children))
	}

	site := doc.Nodes[3]
	if site.Name != "example.test" {
		t.Fatalf("site name = %q, want example.test", site.Name)
	}
	var matchers, quotedBrace, escaped, fakeHeredoc, plugin, nestedRoute, importLine int
	for _, c := range site.Children {
		switch {
		case c.Name == "@api" || c.Name == "@static":
			matchers++
		case c.IsDirective("respond"):
			quotedBrace++
		case c.IsDirective("header"):
			escaped++
		case c.IsDirective("fake_heredoc"):
			fakeHeredoc++
		case c.IsDirective("custom_plugin_directive"):
			plugin++
		case c.IsDirective("route"):
			nestedRoute++
		case c.IsDirective("import"):
			importLine++
		}
	}
	if matchers != 2 {
		t.Errorf("matcher definitions = %d, want 2", matchers)
	}
	if quotedBrace != 1 {
		t.Errorf("quoted-brace respond = %d, want 1", quotedBrace)
	}
	if escaped != 1 {
		t.Errorf("escaped header = %d, want 1", escaped)
	}
	if fakeHeredoc != 1 {
		t.Errorf("escaped heredoc opener = %d, want 1", fakeHeredoc)
	}
	if plugin != 1 {
		t.Errorf("unknown plugin directive = %d, want 1", plugin)
	}
	if nestedRoute != 1 {
		t.Errorf("nested route = %d, want 1", nestedRoute)
	}
	if importLine != 1 {
		t.Errorf("snippet import = %d, want 1", importLine)
	}
}

func TestCompatFixtureSourceRanges(t *testing.T) {
	src := loadFixture(t, "compat")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	assertNestedRanges(t, src, doc.Nodes)

	// The quoted-brace respond must keep the quoted braces verbatim inside
	// its range, and the escaped header must span both physical lines.
	var quoted, escaped *Node
	walkNodes(doc.Nodes, func(n Node) {
		if n.IsDirective("respond") && strings.Contains(n.Args, "literal } brace { text") {
			c := n
			quoted = &c
		}
		if n.IsDirective("header") && strings.Contains(n.Args, "X-Chained") {
			c := n
			escaped = &c
		}
	})
	if quoted == nil || escaped == nil {
		t.Fatalf("quoted = %v escaped = %v, want both nodes", quoted, escaped)
	}
	raw := quoted.Range.Text(src)
	if !strings.Contains(raw, `"literal } brace { text"`) {
		t.Errorf("quoted-brace range text = %q, want the quoted braces verbatim", raw)
	}
	escapedRaw := escaped.Range.Text(src)
	if !strings.Contains(escapedRaw, "first \\\n\t\tsecond") {
		t.Errorf("escaped header range text = %q, want both physical lines", escapedRaw)
	}
	if escaped.Range.StartLine+1 != escaped.Range.EndLine {
		t.Errorf("escaped header spans lines %d-%d, want 2 physical lines", escaped.Range.StartLine, escaped.Range.EndLine)
	}
}

func TestCompatFixtureLosslessPatches(t *testing.T) {
	src := loadFixture(t, "compat")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}

	// 1. Replace the unknown plugin directive line.
	var plugin *Node
	walkNodes(doc.Nodes, func(n Node) {
		if plugin == nil && n.IsDirective("custom_plugin_directive") {
			c := n
			plugin = &c
		}
	})
	if plugin == nil {
		t.Fatal("custom_plugin_directive not found")
	}
	if got := plugin.Range.Text(src); got != "\tcustom_plugin_directive \"keep this raw\"\n" {
		t.Fatalf("plugin range text = %q", got)
	}
	patchRangeAndCompare(t, src, *plugin, "\tsome_other_plugin thing\n")

	// 2. Replace the heredoc respond in the snippet.
	var heredoc *Node
	walkNodes(doc.Nodes, func(n Node) {
		if heredoc == nil && n.IsDirective("respond") && strings.Contains(n.Args, "<<HTML") {
			c := n
			heredoc = &c
		}
	})
	if heredoc == nil {
		t.Fatal("heredoc respond not found")
	}
	patchRangeAndCompare(t, src, *heredoc, "\trespond \"replaced\" 200\n")

	// 3. Replace the escaped header node spanning two physical lines.
	var escaped *Node
	walkNodes(doc.Nodes, func(n Node) {
		if escaped == nil && n.IsDirective("header") && strings.Contains(n.Args, "X-Chained") {
			c := n
			escaped = &c
		}
	})
	if escaped == nil {
		t.Fatal("escaped header not found")
	}
	patchRangeAndCompare(t, src, *escaped, "\theader X-Chained single\n")

	// 4. Delete the named route block entirely; every other byte survives.
	var route *Node
	walkNodes(doc.Nodes, func(n Node) {
		if route == nil && n.Kind == KindNamedRoute {
			c := n
			route = &c
		}
	})
	if route == nil {
		t.Fatal("named route not found")
	}
	patched := patchRangeAndCompare(t, src, *route, "")
	reparsed := Parse(patched)
	if reparsed.Err != nil {
		t.Errorf("re-parsed patched source: %v", reparsed.Err)
	}
	sites := 0
	walkNodes(reparsed.Nodes, func(n Node) {
		if n.Kind == KindSite {
			sites++
		}
	})
	if sites != 2 {
		t.Errorf("sites after deletion = %d, want 2", sites)
	}
}

func TestCompatBracelessFixture(t *testing.T) {
	src := loadFixture(t, "compat-braceless")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 1 || doc.Nodes[0].Kind != KindSite {
		t.Fatalf("top-level nodes = %+v, want one brace-less site", doc.Nodes)
	}
	site := doc.Nodes[0]
	if site.Name != "localhost:8080" {
		t.Errorf("site name = %q, want localhost:8080", site.Name)
	}
	if len(site.Children) != 2 || !site.Children[0].IsDirective("respond") || !site.Children[1].IsDirective("file_server") {
		t.Fatalf("site children = %v, want respond + file_server", names(site.Children))
	}
	if site.Range.End != len(src) {
		t.Errorf("brace-less site range end = %d, want %d (spans to EOF)", site.Range.End, len(src))
	}
	assertNestedRanges(t, src, doc.Nodes)

	// The leading comment sits outside the site range; patching the
	// respond line must not touch the comment or the file_server line.
	patched := patchRangeAndCompare(t, src, site.Children[0], "\trespond \"changed\" 200\n")
	if !strings.Contains(string(patched), "# Brace-less site:") {
		t.Errorf("leading comment was lost after patching")
	}
}

func TestCompatImportsResolve(t *testing.T) {
	rootPath := filepath.Join("testdata", "fixtures", "compat-imports", "Caddyfile")
	src, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	g := Resolve(rootPath, src, os.ReadFile)
	if g.Err != nil {
		t.Fatalf("Resolve returned error: %v", g.Err)
	}
	if len(g.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", g.Warnings)
	}
	if len(g.Documents) != 6 {
		t.Fatalf("documents = %d, want 6 (root + base + app + upstream + blog + leaf)", len(g.Documents))
	}
	if g.Documents[0] != g.Root {
		t.Error("Documents[0] is not the root document")
	}
	basenames := make([]string, len(g.Documents))
	for i, d := range g.Documents {
		basenames[i] = filepath.Base(d.Path)
	}
	// Depth-first resolution appends app.caddy's nested upstream import
	// before the next glob match (blog.caddy) is loaded.
	wantDocs := []string{"Caddyfile", "base.caddy", "app.caddy", "upstream.caddy", "blog.caddy", "leaf.caddy"}
	for i, want := range wantDocs {
		if basenames[i] != want {
			t.Errorf("Documents[%d] = %s, want %s", i, basenames[i], want)
		}
	}

	// Root: base import, sites glob, nested glob; then app's nested refs,
	// blog's snippet ref and finally the nested glob's leaf.
	if len(g.Imports) != 6 {
		t.Fatalf("imports = %d, want 6", len(g.Imports))
	}
	baseRef := g.Imports[0]
	if baseRef.Kind != ImportFile || baseRef.Pattern != "fragments/base.caddy" || len(baseRef.Files) != 1 {
		t.Errorf("import[0] = %+v, want one base.caddy file import", baseRef)
	}
	globRef := g.Imports[1]
	if globRef.Kind != ImportFile || globRef.Pattern != "sites/*.caddy" || len(globRef.Files) != 2 {
		t.Fatalf("import[1] = %+v, want the sites glob with 2 files", globRef)
	}
	if filepath.Base(globRef.Files[0].Path) != "app.caddy" || filepath.Base(globRef.Files[1].Path) != "blog.caddy" {
		t.Errorf("sites glob order = %s, %s, want app.caddy blog.caddy",
			filepath.Base(globRef.Files[0].Path), filepath.Base(globRef.Files[1].Path))
	}

	// app.caddy's nested relative import resolves to fragments/upstream.caddy.
	appRef := g.Imports[2]
	if appRef.Pattern != "../fragments/upstream.caddy" || len(appRef.Files) != 1 {
		t.Fatalf("import[2] = %+v, want the nested relative upstream import", appRef)
	}
	if !strings.HasSuffix(filepath.ToSlash(appRef.Files[0].Path), "fragments/upstream.caddy") {
		t.Errorf("nested relative import resolved to %q, want .../fragments/upstream.caddy", appRef.Files[0].Path)
	}

	// Both site files reference the common-headers snippet from base.caddy.
	var snippetRefs []*ImportRef
	for _, imp := range g.Imports[3:5] {
		if imp.Kind == ImportSnippet && imp.Snippet == "common-headers" {
			snippetRefs = append(snippetRefs, imp)
		}
	}
	if len(snippetRefs) != 2 {
		t.Fatalf("common-headers snippet refs = %d, want 2", len(snippetRefs))
	}
	for _, ref := range snippetRefs {
		if ref.SnippetDoc != g.Documents[1] {
			t.Errorf("snippet ref %+v resolved to the wrong document", ref)
		}
	}

	// The nested glob is the last ref and yields leaf.caddy.
	nestedRef := g.Imports[5]
	if nestedRef.Pattern != "fragments/nested/*.caddy" || len(nestedRef.Files) != 1 ||
		filepath.Base(nestedRef.Files[0].Path) != "leaf.caddy" {
		t.Errorf("import[5] = %+v, want the nested glob with leaf.caddy", nestedRef)
	}

	// Patching an imported document keeps every unrelated byte.
	appDoc := g.Documents[2]
	var importNode *Node
	walkNodes(appDoc.Nodes, func(n Node) {
		if importNode == nil && n.IsDirective("import") && strings.Contains(n.Args, "upstream") {
			c := n
			importNode = &c
		}
	})
	if importNode == nil {
		t.Fatal("upstream import node not found in app.caddy")
	}
	patchRangeAndCompare(t, appDoc.Source, *importNode, "\timport ../fragments/other.caddy\n")
}

func TestCompatImportCycle(t *testing.T) {
	rootPath := filepath.Join("testdata", "fixtures", "compat-cycles", "Caddyfile")
	src, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	g := Resolve(rootPath, src, os.ReadFile)
	if g.Err == nil || !strings.Contains(g.Err.Error(), "cycle") {
		t.Fatalf("Err = %v, want an import cycle error", g.Err)
	}
}

func TestCompatResolveSnippetImportInFixture(t *testing.T) {
	src := loadFixture(t, "compat")
	noReads := func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	g := Resolve("compat/Caddyfile", src, noReads)
	if g.Err != nil {
		t.Fatalf("Resolve returned error: %v", g.Err)
	}
	if len(g.Documents) != 1 {
		t.Fatalf("documents = %d, want only the root", len(g.Documents))
	}
	if len(g.Imports) != 1 || g.Imports[0].Kind != ImportSnippet || g.Imports[0].Snippet != "template" {
		t.Fatalf("imports = %+v, want one snippet import of template", g.Imports)
	}
	if g.Imports[0].Args != "frontpage" {
		t.Errorf("snippet import args = %q, want frontpage", g.Imports[0].Args)
	}
}

func TestCompatMalformedFixtures(t *testing.T) {
	t.Run("unclosed block", func(t *testing.T) {
		src := loadFixture(t, "compat-malformed-unclosed")
		doc := Parse(src)
		if doc.Err == nil || !strings.Contains(doc.Err.Error(), "unclosed") {
			t.Fatalf("Err = %v, want an unclosed-block error", doc.Err)
		}
		if len(doc.Nodes) != 1 || doc.Nodes[0].Kind != KindSite || doc.Nodes[0].Name != "example.test" {
			t.Fatalf("nodes = %+v, want the site to remain available", doc.Nodes)
		}
		site := doc.Nodes[0]
		if site.Range.End != len(src) {
			t.Errorf("site range end = %d, want %d (block extends to EOF)", site.Range.End, len(src))
		}
		if len(site.Children) != 1 || !site.Children[0].IsDirective("reverse_proxy") {
			t.Fatalf("site children = %v, want one reverse_proxy", names(site.Children))
		}
		// The malformed tree still supports byte-preserving patches.
		patched := patchRangeAndCompare(t, src, site.Children[0], "\treverse_proxy localhost:9090\n")
		if !strings.Contains(string(patched), "never closed") {
			t.Errorf("comment outside the patched range was lost")
		}
	})

	t.Run("unterminated string", func(t *testing.T) {
		src := loadFixture(t, "compat-malformed-string")
		doc := Parse(src)
		if doc.Err == nil || !strings.Contains(doc.Err.Error(), "unterminated") {
			t.Fatalf("Err = %v, want an unterminated-string error", doc.Err)
		}
		if len(doc.Nodes) != 0 {
			t.Errorf("nodes = %+v, want none when lexing fails", doc.Nodes)
		}
	})

	t.Run("address ending in brace", func(t *testing.T) {
		src := loadFixture(t, "compat-malformed-brace")
		doc := Parse(src)
		if doc.Err == nil || !strings.Contains(doc.Err.Error(), "curly brace") {
			t.Fatalf("Err = %v, want a curly-brace error", doc.Err)
		}
		// The site node still exists and its range stays valid.
		if len(doc.Nodes) != 1 || doc.Nodes[0].Kind != KindSite {
			t.Fatalf("nodes = %+v, want one site node", doc.Nodes)
		}
		if !doc.Nodes[0].Range.Valid(len(src)) {
			t.Errorf("site range %+v invalid", doc.Nodes[0].Range)
		}
	})
}

// TestCompatEscapedInputNodeRange pins that a directive continued by an
// escaped newline keeps both physical lines inside one patchable range.
func TestCompatEscapedInputNodeRange(t *testing.T) {
	src := []byte("example.test {\n\trespond ok\n\theader X-A \\\n\t\tcontinued\n\tfile_server\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	site := doc.Nodes[0]
	if len(site.Children) != 3 {
		t.Fatalf("site children = %v, want 3", names(site.Children))
	}
	escaped := site.Children[1]
	if !escaped.IsDirective("header") || escaped.Args != "X-A \\\n\t\tcontinued" {
		t.Fatalf("escaped directive = %+v, want header X-A with continuation", escaped)
	}
	raw := escaped.Range.Text(src)
	if !strings.HasPrefix(raw, "\theader X-A \\\n") || !strings.HasSuffix(raw, "\t\tcontinued\n") {
		t.Errorf("escaped range text = %q, want both lines", raw)
	}
	patchRangeAndCompare(t, src, escaped, "\theader X-B single\n")
}
