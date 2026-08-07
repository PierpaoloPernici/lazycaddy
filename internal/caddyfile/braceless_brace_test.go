package caddyfile

import (
	"strings"
	"testing"
)

// TestMergeSeparatedBraceSiteHeader pins the fix for a Caddyfile that puts
// the opening `{` on the line after the site header.
func TestMergeSeparatedBraceSiteHeader(t *testing.T) {
	src := []byte("example.test\n{\n\trespond ok\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("top-level nodes = %d, want 1", len(doc.Nodes))
	}
	site := doc.Nodes[0]
	if site.Kind != KindSite || site.Name != "example.test" {
		t.Fatalf("node = kind %v name %q, want KindSite example.test", site.Kind, site.Name)
	}
	if len(site.Children) != 1 || !site.Children[0].IsDirective("respond") {
		t.Fatalf("site children = %v, want one respond", names(site.Children))
	}
	if site.Range.StartLine != 1 || site.Range.EndLine != 4 {
		t.Errorf("site range = %+v, want lines 1-4", site.Range)
	}
}

// TestMergeSeparatedBraceTwoConsecutiveSites pins the pxe regression: a site
// with its brace on the next line must not swallow the following site.
func TestMergeSeparatedBraceTwoConsecutiveSites(t *testing.T) {
	src := []byte("http://pxe.mac\n{\n\timport reverse_proxy_https https://192.168.1.2:8006\n}\nhttp://router.mac {\n\timport reverse_proxy_https https://192.168.1.1\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 2 {
		t.Fatalf("top-level nodes = %d, want 2 sibling sites", len(doc.Nodes))
	}
	pxe, router := doc.Nodes[0], doc.Nodes[1]
	if pxe.Kind != KindSite || pxe.Name != "http://pxe.mac" {
		t.Errorf("node[0] = kind %v name %q, want site http://pxe.mac", pxe.Kind, pxe.Name)
	}
	if router.Kind != KindSite || router.Name != "http://router.mac" {
		t.Errorf("node[1] = kind %v name %q, want site http://router.mac", router.Kind, router.Name)
	}
	if pxe.Range.StartLine != 1 || pxe.Range.EndLine != 4 {
		t.Errorf("pxe range = %+v, want lines 1-4", pxe.Range)
	}
	if router.Range.StartLine != 5 || router.Range.EndLine != 7 {
		t.Errorf("router range = %+v, want lines 5-7", router.Range)
	}
	if len(pxe.Children) != 1 || !pxe.Children[0].IsDirective("import") {
		t.Errorf("pxe children = %v, want one import", names(pxe.Children))
	}
}

// TestMergeSeparatedBraceSnippet pins the fix for a snippet header followed
// by the block opener on the next line.
func TestMergeSeparatedBraceSnippet(t *testing.T) {
	src := []byte("(hello)\n{\n\trespond ok\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("top-level nodes = %d, want 1", len(doc.Nodes))
	}
	snip := doc.Nodes[0]
	if snip.Kind != KindSnippet || snip.Name != "hello" {
		t.Fatalf("node = kind %v name %q, want KindSnippet hello", snip.Kind, snip.Name)
	}
	if len(snip.Children) != 1 || !snip.Children[0].IsDirective("respond") {
		t.Errorf("snippet children = %v, want one respond", names(snip.Children))
	}
	if snip.Range.StartLine != 1 || snip.Range.EndLine != 4 {
		t.Errorf("snippet range = %+v, want lines 1-4", snip.Range)
	}
}

// TestMergeSeparatedBraceNamedRoute pins the fix for a named-route header
// followed by the block opener on the next line.
func TestMergeSeparatedBraceNamedRoute(t *testing.T) {
	src := []byte("&(myname)\n{\n\trespond ok\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 1 {
		t.Fatalf("top-level nodes = %d, want 1", len(doc.Nodes))
	}
	route := doc.Nodes[0]
	if route.Kind != KindNamedRoute || route.Name != "myname" {
		t.Fatalf("node = kind %v name %q, want KindNamedRoute myname", route.Kind, route.Name)
	}
	if len(route.Children) != 1 || !route.Children[0].IsDirective("respond") {
		t.Errorf("named route children = %v, want one respond", names(route.Children))
	}
	if route.Range.StartLine != 1 || route.Range.EndLine != 4 {
		t.Errorf("named route range = %+v, want lines 1-4", route.Range)
	}
}

// TestMergeSeparatedBraceCommentBetween pins that comments and blank lines
// between the header and the `{` (lexed out) still merge.
func TestMergeSeparatedBraceCommentBetween(t *testing.T) {
	src := []byte("example.test\n# comment\n\n{\n\trespond ok\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 1 || doc.Nodes[0].Kind != KindSite {
		t.Fatalf("top-level nodes = %+v, want one site", doc.Nodes)
	}
	site := doc.Nodes[0]
	if site.Name != "example.test" {
		t.Errorf("site name = %q, want example.test", site.Name)
	}
	if len(site.Children) != 1 || !site.Children[0].IsDirective("respond") {
		t.Errorf("site children = %v, want one respond", names(site.Children))
	}
	if site.Range.StartLine != 1 || site.Range.EndLine != 6 {
		t.Errorf("site range = %+v, want lines 1-6", site.Range)
	}
	// The comment must be preserved inside the site's range.
	if !strings.Contains(site.Range.Text(src), "# comment") {
		t.Errorf("site range text = %q, want it to contain the comment", site.Range.Text(src))
	}
}

// TestMergeSeparatedBraceBracelessSiteUnchanged pins that a true brace-less
// site (header followed by a directive line, no brace) is untouched.
func TestMergeSeparatedBraceBracelessSiteUnchanged(t *testing.T) {
	src := []byte("localhost\n\treverse_proxy localhost:9000\n\tfile_server\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 1 || doc.Nodes[0].Kind != KindSite {
		t.Fatalf("top-level nodes = %+v, want one site", doc.Nodes)
	}
	site := doc.Nodes[0]
	if len(site.Children) != 2 {
		t.Fatalf("site children = %v, want 2", names(site.Children))
	}
	if site.Range.Start != 0 || site.Range.End != len(src) {
		t.Errorf("site range = [%d:%d), want [0:%d)", site.Range.Start, site.Range.End, len(src))
	}
	// EndLine resolves past the trailing newline to the virtual next line, as
	// the existing brace-less site handling does.
	if site.Range.StartLine != 1 || site.Range.EndLine != 4 {
		t.Errorf("site range = %+v, want start line 1 end line 4", site.Range)
	}
}

// TestMergeSeparatedBraceUnclosedAtEOF pins that a header with `{` on the
// next line and no closing brace still errors and stays lossless.
func TestMergeSeparatedBraceUnclosedAtEOF(t *testing.T) {
	src := []byte("example.test\n{\n")
	doc := Parse(src)
	if doc.Err == nil {
		t.Fatal("expected unclosed block error, got nil")
	}
	if !strings.Contains(doc.Err.Error(), "unclosed") {
		t.Errorf("error = %q, want it to mention unclosed block", doc.Err)
	}
	if len(doc.Nodes) != 1 || doc.Nodes[0].Kind != KindSite {
		t.Fatalf("top-level nodes = %+v, want one site", doc.Nodes)
	}
	site := doc.Nodes[0]
	if site.Range.End != len(src) {
		t.Errorf("site range end = %d, want %d (block extends to EOF)", site.Range.End, len(src))
	}
	// EndLine resolves past the trailing newline to the virtual next line, as
	// the existing unclosed-block handling does.
	if site.Range.StartLine != 1 || site.Range.EndLine != 3 {
		t.Errorf("site range = %+v, want start line 1 end line 3", site.Range)
	}
	// The tree stays lossless: the site covers every byte of the source.
	if got := site.Range.Text(src); got != string(src) {
		t.Errorf("site range text = %q, want %q", got, src)
	}
}

// TestMergeSeparatedBraceNestedDirectiveNotMerged pins that a lone `{` on a
// new line inside a block is NOT merged with the preceding directive: it
// stays an anonymous directive block with an empty name.
func TestMergeSeparatedBraceNestedDirectiveNotMerged(t *testing.T) {
	src := []byte("example.test {\n\thandle\n\t{\n\t\trespond ok\n\t}\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 1 || doc.Nodes[0].Kind != KindSite {
		t.Fatalf("top-level nodes = %+v, want one site", doc.Nodes)
	}
	site := doc.Nodes[0]
	if len(site.Children) != 2 {
		t.Fatalf("site children = %v, want handle and the anonymous block", names(site.Children))
	}
	if !site.Children[0].IsDirective("handle") {
		t.Errorf("child[0] = %q, want handle", site.Children[0].Name)
	}
	anon := site.Children[1]
	if anon.Kind != KindDirective || anon.Name != "" {
		t.Errorf("child[1] = kind %v name %q, want anonymous directive block", anon.Kind, anon.Name)
	}
	if len(anon.Children) != 1 || !anon.Children[0].IsDirective("respond") {
		t.Errorf("anonymous block children = %v, want one respond", names(anon.Children))
	}
}

// TestMergeSeparatedBraceImportNotMerged pins that a top-level import stays
// an opaque directive; a following lone `{` opens the global options block.
func TestMergeSeparatedBraceImportNotMerged(t *testing.T) {
	src := []byte("import foo\n{\n\temail admin@example.test\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 2 {
		t.Fatalf("top-level nodes = %d, want 2 (import + global options)", len(doc.Nodes))
	}
	imp := doc.Nodes[0]
	if !imp.IsDirective("import") || imp.Args != "foo" {
		t.Errorf("node[0] = %q args %q, want import foo", imp.Name, imp.Args)
	}
	globals := doc.Nodes[1]
	if globals.Kind != KindGlobalOptions {
		t.Errorf("node[1].Kind = %v, want global options", globals.Kind)
	}
	if len(globals.Children) != 1 || !globals.Children[0].IsDirective("email") {
		t.Errorf("global options children = %v, want one email", names(globals.Children))
	}
}

// TestMergeSeparatedBraceGlobalOptionsLeadingBrace pins that a file starting
// with a lone `{` stays a global options block and is not merged.
func TestMergeSeparatedBraceGlobalOptionsLeadingBrace(t *testing.T) {
	src := []byte("{\n\temail admin@example.test\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse returned error: %v", doc.Err)
	}
	if len(doc.Nodes) != 1 || doc.Nodes[0].Kind != KindGlobalOptions {
		t.Fatalf("top-level nodes = %+v, want one global options block", doc.Nodes)
	}
	globals := doc.Nodes[0]
	if len(globals.Children) != 1 || !globals.Children[0].IsDirective("email") {
		t.Errorf("global options children = %v, want one email", names(globals.Children))
	}
	if globals.Range.StartLine != 1 || globals.Range.EndLine != 3 {
		t.Errorf("global options range = %+v, want lines 1-3", globals.Range)
	}
}

// TestMergeSeparatedBraceHeaderEndingCurlyBrace pins that `http://a.test{`
// followed by `{` on the next line must not be merged: the existing
// suffix-`{` error path must still fire.
func TestMergeSeparatedBraceHeaderEndingCurlyBrace(t *testing.T) {
	src := []byte("http://a.test{\n{\n")
	doc := Parse(src)
	if doc.Err == nil {
		t.Fatal("expected error for site address ending in curly brace, got nil")
	}
	if !strings.Contains(doc.Err.Error(), "curly brace") {
		t.Errorf("error = %q, want it to mention curly brace", doc.Err)
	}
}
