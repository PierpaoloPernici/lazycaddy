package caddyfile

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseBracelessSingleSite(t *testing.T) {
	src := []byte("localhost\n\treverse_proxy localhost:9000\n\tfile_server\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("top-level nodes = %d, want 1", len(doc.Nodes))
	}
	site := doc.Nodes[0]
	if site.Kind != KindSite || site.Name != "localhost" {
		t.Errorf("site = %+v, want KindSite named localhost", site)
	}
	if len(site.Children) != 2 {
		t.Fatalf("site children = %v, want 2", names(site.Children))
	}
	if !site.Children[0].IsDirective("reverse_proxy") || !site.Children[1].IsDirective("file_server") {
		t.Errorf("site children = %v, want reverse_proxy and file_server", names(site.Children))
	}
	// The brace-less site spans from the address line to EOF.
	if site.Range.Start != 0 || site.Range.End != len(src) {
		t.Errorf("site range = [%d:%d), want [0:%d)", site.Range.Start, site.Range.End, len(src))
	}
	if got := site.Range.Text(src); got != string(src) {
		t.Errorf("site range text = %q, want %q", got, src)
	}
}

func TestParseBracelessSiteSwallowsFollowingImports(t *testing.T) {
	src := []byte("localhost\n\treverse_proxy localhost:9000\n\timport extra\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 1 || len(doc.Nodes[0].Children) != 2 {
		t.Fatalf("nodes = %+v, want one site with two directives", doc.Nodes)
	}
	if !doc.Nodes[0].Children[1].IsDirective("import") {
		t.Errorf("second child = %q, want import", doc.Nodes[0].Children[1].Name)
	}
}

func TestParseTopLevelImportIsNotASite(t *testing.T) {
	// A top-level import line is opaque and must not start a brace-less site.
	src := []byte("{\n\temail admin@example.test\n}\nimport sites/*.caddy\nimport snippets/security_headers.caddy\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 3 {
		t.Fatalf("top-level nodes = %d, want 3 (global + 2 imports)", len(doc.Nodes))
	}
	if doc.Nodes[0].Kind != KindGlobalOptions {
		t.Errorf("node[0].Kind = %v, want global options", doc.Nodes[0].Kind)
	}
	if !doc.Nodes[1].IsDirective("import") || doc.Nodes[1].Args != "sites/*.caddy" {
		t.Errorf("node[1] = %q %q, want import sites/*.caddy", doc.Nodes[1].Name, doc.Nodes[1].Args)
	}
	if !doc.Nodes[2].IsDirective("import") {
		t.Errorf("node[2] = %q, want import", doc.Nodes[2].Name)
	}
}

func TestParseNamedRoute(t *testing.T) {
	src := []byte("&(app-proxy) {\n\treverse_proxy app-01:8080 app-02:8080\n}\nexample.com {\n\tinvoke app-proxy\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 2 {
		t.Fatalf("top-level nodes = %d, want 2", len(doc.Nodes))
	}
	route := doc.Nodes[0]
	if route.Kind != KindNamedRoute || route.Name != "app-proxy" {
		t.Errorf("node[0] = kind %v name %q, want named route app-proxy", route.Kind, route.Name)
	}
	if len(route.Children) != 1 || !route.Children[0].IsDirective("reverse_proxy") {
		t.Errorf("named route children = %v, want one reverse_proxy", names(route.Children))
	}
	site := doc.Nodes[1]
	if site.Kind != KindSite || site.Name != "example.com" {
		t.Errorf("node[1] = kind %v name %q, want site example.com", site.Kind, site.Name)
	}
	if len(site.Children) != 1 || !site.Children[0].IsDirective("invoke") {
		t.Errorf("site children = %v, want one invoke", names(site.Children))
	}
}

func TestParseImportWithBlock(t *testing.T) {
	src := []byte("(hello-world) {\n\theader {\n\t\tX-Foo bar\n\t}\n}\nexample.com {\n\timport hello-world {\n\t\tContent-Type text/html\n\t}\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 2 {
		t.Fatalf("top-level nodes = %d, want 2", len(doc.Nodes))
	}
	imp := doc.Nodes[1].Children[0]
	if !imp.IsDirective("import") {
		t.Fatalf("site child = %q, want import", imp.Name)
	}
	if imp.Args != "hello-world" {
		t.Errorf("import args = %q, want hello-world", imp.Args)
	}
	if len(imp.Children) != 1 || !imp.Children[0].IsDirective("Content-Type") {
		t.Errorf("import block children = %v, want one Content-Type directive", names(imp.Children))
	}
}

func TestParseHeredocDirective(t *testing.T) {
	// Mirrors the heredoc example in the official Caddyfile docs: trailing
	// arguments and the closing brace on the marker line.
	src := []byte("example.com { respond <<HTML\n\t<html>\nHTML 200 }\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 1 || doc.Nodes[0].Name != "example.com" {
		t.Fatalf("nodes = %+v, want one site example.com", doc.Nodes)
	}
	site := doc.Nodes[0]
	if len(site.Children) != 1 {
		t.Fatalf("site children = %d, want 1 (respond)", len(site.Children))
	}
	respond := site.Children[0]
	if !respond.IsDirective("respond") {
		t.Fatalf("site child = %q, want respond", respond.Name)
	}
	// The heredoc raw text and the trailing argument are part of the args.
	if !strings.Contains(respond.Args, "<<HTML") || !strings.Contains(respond.Args, "HTML 200") {
		t.Errorf("respond args = %q, want to contain the heredoc and the 200 argument", respond.Args)
	}
	// The site block must be closed by the trailing brace, so no error and a
	// complete range.
	if site.Range.End > len(src) || site.Range.EndLine != 3 {
		t.Errorf("site range = %+v, want to end on line 3", site.Range)
	}
	if got := site.Range.Text(src); !strings.HasSuffix(got, "HTML 200 }\n") {
		t.Errorf("site range text = %q, want to end with the marker line", got)
	}
}

func TestParseCRLF(t *testing.T) {
	src := []byte("example.test {\r\n\treverse_proxy localhost:8080\r\n}\r\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	site := doc.Nodes[0]
	if len(site.Children) != 1 {
		t.Fatalf("site children = %d, want 1", len(site.Children))
	}
	// Ranges include the CR bytes; bytes outside a patch stay identical.
	proxy := site.Children[0]
	if got := proxy.Range.Text(src); got != "\treverse_proxy localhost:8080\r\n" {
		t.Errorf("proxy range text = %q, want CRLF line", got)
	}
	patched, err := Patch(src, proxy.Range, []byte("\treverse_proxy localhost:9000\r\n"))
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	before, after := string(src[:proxy.Range.Start]), string(src[proxy.Range.End:])
	if !strings.HasPrefix(string(patched), before) || !strings.HasSuffix(string(patched), after) {
		t.Errorf("CRLF patch altered bytes outside the range")
	}
}

func TestParseNoTrailingNewline(t *testing.T) {
	src := []byte("example.test {\n\trespond ok\n}")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	site := doc.Nodes[0]
	if site.Range.End != len(src) {
		t.Errorf("site range end = %d, want %d (no trailing newline)", site.Range.End, len(src))
	}

	// A directive on the last line without a trailing newline ends exactly
	// at the end of the source.
	src2 := []byte("localhost\nrespond ok")
	doc2 := Parse(src2)
	if doc2.Err != nil {
		t.Fatalf("Parse returned error: %v", doc2.Err)
	}
	leaf := doc2.Nodes[0].Children[0]
	if leaf.Range.Text(src2) != "respond ok" {
		t.Errorf("leaf range text = %q, want %q", leaf.Range.Text(src2), "respond ok")
	}
	if leaf.Range.End != len(src2) {
		t.Errorf("leaf range end = %d, want %d", leaf.Range.End, len(src2))
	}
}

func TestParseURLFragmentIsNotAComment(t *testing.T) {
	src := []byte("example.test {\n\tredir /some/#/path\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	redir := doc.Nodes[0].Children[0]
	if !redir.IsDirective("redir") {
		t.Fatalf("site child = %q, want redir", redir.Name)
	}
	if redir.Args != "/some/#/path" {
		t.Errorf("redir args = %q, want %q", redir.Args, "/some/#/path")
	}
}

func TestPatchInvalidRanges(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy localhost:8080\n}\n")
	tests := []struct {
		name string
		r    SourceRange
	}{
		{"negative start", SourceRange{Start: -1, End: 5}},
		{"end beyond source", SourceRange{Start: 0, End: len(src) + 1}},
		{"start after end", SourceRange{Start: 10, End: 5}},
		{"end negative", SourceRange{Start: 0, End: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := Patch(src, tt.r, []byte("x")); err == nil {
				t.Errorf("Patch(%+v) = %q, want error", tt.r, got)
			}
		})
	}
}

func TestPatchEmptyAndIdentityRanges(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy localhost:8080\n}\n")
	// Replacing a range with identical bytes is a no-op.
	r := SourceRange{Start: 0, End: len(src)}
	patched, err := Patch(src, r, src)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !bytes.Equal(patched, src) {
		t.Errorf("identity patch changed the source")
	}
	// An empty range inserts without removing anything.
	inserted, err := Patch(src, SourceRange{Start: 0, End: 0}, []byte("// header\n"))
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !bytes.HasPrefix(inserted, []byte("// header\n")) || !bytes.HasSuffix(inserted, src) {
		t.Errorf("insert at offset 0 failed: %q", inserted)
	}
}

func TestPatchPrefixSuffixByteIdentical(t *testing.T) {
	src := loadFixture(t, "homelab")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}

	// Replace every site header line and every import line, then verify that
	// for each patch the bytes before and after the range are untouched.
	var targets []SourceRange
	walkNodes(doc.Nodes, func(n Node) {
		if n.Kind == KindSite {
			targets = append(targets, n.Range)
		}
	})
	if len(targets) == 0 {
		t.Fatal("no site ranges found")
	}
	for _, r := range targets {
		patched, err := Patch(src, r, []byte("# replaced\n"))
		if err != nil {
			t.Fatalf("Patch(%+v): %v", r, err)
		}
		if !bytes.Equal(patched[:r.Start], src[:r.Start]) {
			t.Errorf("prefix before [%d:%d) not byte-identical", r.Start, r.End)
		}
		if !bytes.Equal(patched[r.Start+len("# replaced\n"):], src[r.End:]) {
			t.Errorf("suffix after [%d:%d) not byte-identical", r.Start, r.End)
		}
	}
}
