package caddyfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "fixtures", name, "Caddyfile")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return src
}

// countDirectives returns the number of directives with the given name.
func countDirectives(nodes []Node, name string) int {
	count := 0
	walkNodes(nodes, func(n Node) {
		if n.IsDirective(name) {
			count++
		}
	})
	return count
}

func TestParseHomelabStructure(t *testing.T) {
	src := loadFixture(t, "homelab")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}

	var globals, snippets int
	var siteNames []string
	for _, n := range doc.Nodes {
		switch n.Kind {
		case KindGlobalOptions:
			globals++
		case KindSnippet:
			snippets++
		case KindSite:
			siteNames = append(siteNames, n.Name)
		}
	}

	if globals != 1 {
		t.Errorf("global options blocks = %d, want 1", globals)
	}
	if snippets != 2 {
		t.Errorf("snippets = %d, want 2", snippets)
	}
	wantSites := []string{
		"*.example.test",
		"adguard1.example.test", "adguard2.example.test", "dns.example.test",
		"grafana.example.test", "pbs.example.test", "pxe.example.test",
		"router.example.test", "zb.example.test", "tower.example.test",
		"rock.example.test", "roon.example.test", "couchdb.example.test",
		"crawl.example.test", "mao.example.test", "browse.example.test",
		"search.example.test",
	}
	if len(siteNames) != len(wantSites) {
		t.Fatalf("sites = %d (%v), want %d", len(siteNames), siteNames, len(wantSites))
	}
	for i, want := range wantSites {
		if siteNames[i] != want {
			t.Errorf("site[%d] = %q, want %q", i, siteNames[i], want)
		}
	}

	// Every site block is closed by a real `}` line, so each site must have
	// exactly one child import (except the direct reverse_proxy sites).
	if got := countDirectives(doc.Nodes, "import"); got != 13 {
		t.Errorf("import directives = %d, want 13", got)
	}
	// The health-check options live inside browse.example.test's proxy block.
	if got := countDirectives(doc.Nodes, "health_uri"); got != 1 {
		t.Errorf("health_uri directives = %d, want 1", got)
	}
}

func TestParseHomelabSnippetNames(t *testing.T) {
	src := loadFixture(t, "homelab")
	doc := Parse(src)

	var names []string
	for _, n := range doc.Nodes {
		if n.Kind == KindSnippet {
			names = append(names, n.Name)
		}
	}
	want := []string{"reverse_proxy_http", "reverse_proxy_https"}
	if len(names) != len(want) {
		t.Fatalf("snippet names = %v, want %v", names, want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("snippet[%d] = %q, want %q", i, names[i], w)
		}
	}
}

func TestParseHomelabSourceRanges(t *testing.T) {
	src := loadFixture(t, "homelab")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}

	if len(doc.Nodes) < 3 {
		t.Fatalf("expected at least 3 top-level nodes, got %d", len(doc.Nodes))
	}

	tests := []struct {
		name       string
		wantStart  int
		wantEnd    int
		wantPrefix string
		wantSuffix string
	}{
		{"global options", 3, 19, "{\n", "}\n"},
		{"snippet reverse_proxy_http", 22, 29, "(reverse_proxy_http) {\n", "}\n"},
		{"snippet reverse_proxy_https", 32, 42, "(reverse_proxy_https) {\n", "}\n"},
		{"site *.example.test", 45, 58, "*.example.test {\n", "}\n"},
		{"site adguard1.example.test", 61, 63, "adguard1.example.test {\n", "}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var n Node
			found := false
			walkNodes(doc.Nodes, func(cand Node) {
				if !found && cand.Range.StartLine == tt.wantStart && cand.Range.EndLine == tt.wantEnd {
					n = cand
					found = true
				}
			})
			if !found {
				t.Fatalf("no node spanning lines %d-%d", tt.wantStart, tt.wantEnd)
			}
			if !n.Range.Valid(len(src)) {
				t.Fatalf("range %+v invalid for %d-byte source", n.Range, len(src))
			}
			text := n.Range.Text(src)
			if !strings.HasPrefix(text, tt.wantPrefix) || !strings.HasSuffix(text, tt.wantSuffix) {
				t.Errorf("range text = %q, want prefix %q and suffix %q", text, tt.wantPrefix, tt.wantSuffix)
			}
			for _, child := range n.Children {
				if !(child.Range.Start >= n.Range.Start && child.Range.End <= n.Range.End) {
					t.Errorf("child %q range %+v escapes parent %q range %+v", child.Name, child.Range, n.Name, n.Range)
				}
			}
		})
	}

	// Top-level nodes must be ordered and non-overlapping.
	for i := 1; i < len(doc.Nodes); i++ {
		prev, cur := doc.Nodes[i-1], doc.Nodes[i]
		if prev.Range.End > cur.Range.Start {
			t.Errorf("top-level nodes overlap: %q [%d:%d) vs %q [%d:%d)",
				prev.Name, prev.Range.Start, prev.Range.End, cur.Name, cur.Range.Start, cur.Range.End)
		}
	}

	// Placeholder braces inside a directive header must not open a block:
	// reverse_proxy {args[0]} { is a sub-block whose first child is header_up.
	var snip Node
	walkNodes(doc.Nodes, func(n Node) {
		if n.Kind == KindSnippet && n.Name == "reverse_proxy_http" {
			snip = n
		}
	})
	if len(snip.Children) != 1 || !snip.Children[0].IsDirective("reverse_proxy") {
		t.Fatalf("snippet reverse_proxy_http children = %+v, want one reverse_proxy block", snip.Children)
	}
	proxy := snip.Children[0]
	if proxy.Args != "{args[0]}" {
		t.Errorf("reverse_proxy args = %q, want {args[0]}", proxy.Args)
	}
	if len(proxy.Children) != 4 {
		t.Errorf("reverse_proxy children = %d, want 4 header_up directives", len(proxy.Children))
	}
	for _, c := range proxy.Children {
		if !c.IsDirective("header_up") {
			t.Errorf("reverse_proxy child = %q, want header_up", c.Name)
		}
	}
}

func TestParseHomelabLosslessPatch(t *testing.T) {
	src := loadFixture(t, "homelab")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}

	// Replace the import in the first site (adguard1.example.test) and
	// verify that exactly that one line changed.
	oldLine := "\timport reverse_proxy_http 192.0.2.4:80\n"
	newLine := "\timport reverse_proxy_http 192.0.2.10:8080\n"
	var importNode *Node
	walkNodes(doc.Nodes, func(n Node) {
		if importNode == nil && n.Range.Text(src) == oldLine {
			importNode = &n
		}
	})
	if importNode == nil {
		t.Fatal("import leaf not found in homelab fixture")
	}
	patched, err := Patch(src, importNode.Range, []byte(newLine))
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	want := strings.Replace(string(src), oldLine, newLine, 1)
	if string(patched) != want {
		t.Errorf("patched source differs from expected\n got: %q\nwant: %q", patched, want)
	}

	// Deleting the browse.example.test block must remove exactly that block
	// and leave every other byte untouched.
	var browse Node
	walkNodes(doc.Nodes, func(n Node) {
		if n.Kind == KindSite && n.Name == "browse.example.test" {
			browse = n
		}
	})
	if browse.Name == "" {
		t.Fatal("browse.example.test site not found")
	}
	blockText := browse.Range.Text(src)
	if !strings.HasPrefix(blockText, "browse.example.test {\n") {
		t.Fatalf("unexpected block text: %q", blockText)
	}
	without, err := Patch(src, browse.Range, nil)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	wantWithout := strings.Replace(string(src), blockText, "", 1)
	if string(without) != wantWithout {
		t.Errorf("deleting block did not remove exactly the block\n got: %q\nwant: %q", without, wantWithout)
	}
	reparsed := Parse(without)
	if reparsed.Err != nil {
		t.Errorf("re-parsed patched source: %v", reparsed.Err)
	}
	sites := 0
	for _, n := range reparsed.Nodes {
		if n.Kind == KindSite {
			sites++
		}
	}
	if sites != 16 {
		t.Errorf("sites after deletion = %d, want 16", sites)
	}
}

func TestParseEdgeCasesUnknownDirective(t *testing.T) {
	src := loadFixture(t, "edge-cases")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}

	if len(doc.Nodes) != 1 || doc.Nodes[0].Name != "example.test" {
		t.Fatalf("top-level nodes = %+v, want one site example.test", doc.Nodes)
	}
	site := doc.Nodes[0]

	// The comment line is preserved inside the site range but is not a node.
	comment := "# Preserve comments and unusual spacing."
	if !strings.Contains(site.Range.Text(src), comment) {
		t.Errorf("site range does not contain comment %q", comment)
	}

	if len(site.Children) != 2 {
		t.Fatalf("site children = %d (%v), want 2 (unknown directive + route)", len(site.Children), names(site.Children))
	}
	unknown := site.Children[0]
	if !unknown.IsDirective("custom_plugin_directive") {
		t.Errorf("child[0] = %q, want custom_plugin_directive", unknown.Name)
	}
	if unknown.Args != `"keep this raw"` {
		t.Errorf("unknown directive args = %q, want %q", unknown.Args, `"keep this raw"`)
	}
	if got := unknown.Range.Text(src); got != "\tcustom_plugin_directive \"keep this raw\"\n" {
		t.Errorf("unknown directive range text = %q", got)
	}

	route := site.Children[1]
	if !route.IsDirective("route") || len(route.Children) != 1 {
		t.Fatalf("route = %+v, want a route block with one child", route)
	}
	if !route.Children[0].IsDirective("reverse_proxy") {
		t.Errorf("route child = %q, want reverse_proxy", route.Children[0].Name)
	}

	// Patching the unknown directive must leave every other byte identical.
	patched, err := Patch(src, unknown.Range, []byte("\tsome_other_plugin thing\n"))
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	want := strings.Replace(string(src), "\tcustom_plugin_directive \"keep this raw\"\n", "\tsome_other_plugin thing\n", 1)
	if string(patched) != want {
		t.Errorf("patched source differs from expected\n got: %q\nwant: %q", patched, want)
	}
}

func TestParseRealisticTopLevelImports(t *testing.T) {
	src := loadFixture(t, "realistic")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}

	if len(doc.Nodes) != 3 {
		t.Fatalf("top-level nodes = %d, want 3 (global options + 2 imports)", len(doc.Nodes))
	}
	if doc.Nodes[0].Kind != KindGlobalOptions {
		t.Errorf("node[0].Kind = %v, want global options", doc.Nodes[0].Kind)
	}
	if !doc.Nodes[1].IsDirective("import") || doc.Nodes[1].Args != "snippets/security_headers.caddy" {
		t.Errorf("node[1] = %q args %q, want import snippets/security_headers.caddy", doc.Nodes[1].Name, doc.Nodes[1].Args)
	}
	if !doc.Nodes[2].IsDirective("import") || doc.Nodes[2].Args != "sites/*.caddy" {
		t.Errorf("node[2] = %q args %q, want import sites/*.caddy", doc.Nodes[2].Name, doc.Nodes[2].Args)
	}
}

func TestParseUnclosedBlock(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy localhost:8080\n")
	doc := Parse(src)
	if doc.Err == nil {
		t.Fatal("expected error for unclosed block, got nil")
	}
	if !strings.Contains(doc.Err.Error(), "unclosed") {
		t.Errorf("error = %q, want it to mention unclosed block", doc.Err)
	}
	if len(doc.Nodes) != 1 || doc.Nodes[0].Name != "example.test" {
		t.Fatalf("nodes = %+v, want the site block to remain available", doc.Nodes)
	}
	site := doc.Nodes[0]
	if !site.Range.Valid(len(src)) {
		t.Fatalf("site range %+v invalid", site.Range)
	}
	if site.Range.End != len(src) {
		t.Errorf("site range end = %d, want %d (block extends to EOF)", site.Range.End, len(src))
	}
	if len(site.Children) != 1 || !site.Children[0].IsDirective("reverse_proxy") {
		t.Errorf("site children = %+v, want reverse_proxy leaf", site.Children)
	}
}

func TestParseEmptyInput(t *testing.T) {
	doc := Parse(nil)
	if doc.Err != nil {
		t.Errorf("Parse(nil) error = %v", doc.Err)
	}
	if len(doc.Nodes) != 0 {
		t.Errorf("Parse(nil) nodes = %d, want 0", len(doc.Nodes))
	}
}

func names(nodes []Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Name
	}
	return out
}
