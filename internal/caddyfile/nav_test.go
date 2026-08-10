package caddyfile

import (
	"strings"
	"testing"
)

// findFold returns the fold whose range matches the given start/end.
func findFold(t *testing.T, folds []Fold, start, end int) *Fold {
	t.Helper()
	for i := range folds {
		if folds[i].Range.Start == start && folds[i].Range.End == end {
			return &folds[i]
		}
	}
	t.Fatalf("no fold with range [%d:%d) in %+v", start, end, folds)
	return nil
}

func TestFoldsSiteSnippetAndRoute(t *testing.T) {
	src := []byte("(snip) {\n\trespond ok\n}\n&(route) {\n\treverse_proxy localhost:8080\n}\nexample.test {\n\tfile_server\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	folds := Folds(doc)
	if len(folds) != 3 {
		t.Fatalf("folds = %d, want 3 (%+v)", len(folds), folds)
	}
	if folds[0].Kind != KindSnippet || folds[0].StartLine != 1 || folds[0].EndLine != 3 || folds[0].Depth != 0 {
		t.Errorf("fold[0] = %+v, want the snippet on lines 1-3 at depth 0", folds[0])
	}
	if folds[1].Kind != KindNamedRoute || folds[1].Name != "route" {
		t.Errorf("fold[1] = %+v, want the named route", folds[1])
	}
	if folds[2].Kind != KindSite || folds[2].Name != "example.test" {
		t.Errorf("fold[2] = %+v, want the site", folds[2])
	}
}

func TestFoldsNestedHandlers(t *testing.T) {
	src := []byte("example.test {\n\troute {\n\t\thandle /api {\n\t\t\trespond ok\n\t\t}\n\t}\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	folds := Folds(doc)
	if len(folds) != 3 {
		t.Fatalf("folds = %d, want 3 (site, route, handle)", len(folds))
	}
	site := findFold(t, folds, 0, len(src))
	if site.Depth != 0 {
		t.Errorf("site depth = %d, want 0", site.Depth)
	}
	if folds[1].Name != "route" || folds[1].Depth != 1 {
		t.Errorf("route fold = %+v, want depth 1", folds[1])
	}
	if folds[2].Name != "handle" || folds[2].Depth != 2 {
		t.Errorf("handle fold = %+v, want depth 2", folds[2])
	}
	// Fold ranges are patchable: deleting the handle fold preserves the rest.
	patched, err := Patch(src, folds[2].Range, nil)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	want := "example.test {\n\troute {\n\t}\n}\n"
	if string(patched) != want {
		t.Errorf("patched = %q, want %q", patched, want)
	}
}

func TestFoldsBracelessSite(t *testing.T) {
	src := []byte("localhost:8080\n\trespond ok\n\tfile_server\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	folds := Folds(doc)
	if len(folds) != 1 {
		t.Fatalf("folds = %d, want 1", len(folds))
	}
	if folds[0].Kind != KindSite || folds[0].Range.End != len(src) {
		t.Errorf("fold = %+v, want the brace-less site spanning to EOF", folds[0])
	}
}

func TestFoldsQuotedBracesAreNotFolds(t *testing.T) {
	src := []byte("example.test {\n\trespond \"literal } brace { text\" 200\n\tfile_server\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	folds := Folds(doc)
	if len(folds) != 1 {
		t.Fatalf("folds = %d, want 1 (quoted braces must not create folds)", len(folds))
	}
	if folds[0].Name != "example.test" {
		t.Errorf("fold = %+v, want only the site", folds[0])
	}
}

func TestFoldsPartiallyParsed(t *testing.T) {
	src := []byte("example.test {\n\troute {\n\t\trespond ok\n\t}\n")
	doc := Parse(src)
	if doc.Err == nil {
		t.Fatal("expected a parse error")
	}
	folds := Folds(doc)
	if len(folds) < 2 {
		t.Fatalf("folds = %d, want >= 2 on partially parsed input", len(folds))
	}
	if folds[1].Name != "route" {
		t.Errorf("fold[1] = %+v, want the route block", folds[1])
	}
}

func TestLandmarksNormalBlock(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy localhost:8080\n\tfile_server\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	site := doc.Nodes[0]
	l := LandmarksOf(src, site)
	if l.HeaderLine != 1 || l.OpenBraceLine != 1 || l.CloseBraceLine != 4 {
		t.Errorf("landmarks = %+v, want header 1 open 1 close 4", l)
	}
	if l.FirstChildLine != 2 || l.LastChildLine != 3 {
		t.Errorf("landmarks = %+v, want children on lines 2-3", l)
	}
	if l.Indent != "" || l.ChildIndent != "\t" {
		t.Errorf("landmarks = %+v, want indent \"\" child indent \"\\t\"", l)
	}
}

func TestLandmarksSeparatedBraceAndNested(t *testing.T) {
	src := []byte("example.test\n{\n\troute {\n\t\thandle /x {\n\t\t\trespond ok\n\t\t}\n\t}\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	site := doc.Nodes[0]
	l := LandmarksOf(src, site)
	if l.HeaderLine != 1 || l.OpenBraceLine != 2 || l.CloseBraceLine != 8 {
		t.Errorf("site landmarks = %+v, want header 1 open 2 close 8", l)
	}
	route := site.Children[0]
	rl := LandmarksOf(src, route)
	if rl.OpenBraceLine != 3 || rl.ChildIndent != "\t\t" {
		t.Errorf("route landmarks = %+v, want open 3 child indent \\t\\t", rl)
	}
}

func TestLandmarksBracelessAndEmpty(t *testing.T) {
	src := []byte("localhost:8080\n\trespond ok\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	site := doc.Nodes[0]
	l := LandmarksOf(src, site)
	if l.OpenBraceLine != 0 || l.CloseBraceLine != 0 {
		t.Errorf("braceless landmarks = %+v, want no brace lines", l)
	}
	if l.FirstChildLine != 2 || l.ChildIndent != "\t" {
		t.Errorf("braceless landmarks = %+v, want first child 2 child indent \\t", l)
	}

	empty := Parse([]byte("example.test {\n}\n"))
	el := LandmarksOf(empty.Source, empty.Nodes[0])
	if el.FirstChildLine != 0 || el.LastChildLine != 0 {
		t.Errorf("empty block landmarks = %+v, want no children", el)
	}
	if el.ChildIndent != "\t" {
		t.Errorf("empty block child indent = %q, want \\t", el.ChildIndent)
	}
}

func TestMatchersDefinitionsAndReferences(t *testing.T) {
	src := []byte("example.test {\n\t@api path /api/*\n\thandle @api {\n\t\trespond ok\n\t}\n\treverse_proxy @api localhost:8080\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	refs := Matchers(doc)
	var defs, uses int
	for _, r := range refs {
		if r.Name != "api" {
			t.Errorf("matcher name = %q, want api", r.Name)
		}
		if r.Definition {
			defs++
			if !strings.HasPrefix(string(src[r.Start:r.End]), "@api") {
				t.Errorf("definition span = %q, want @api", src[r.Start:r.End])
			}
		} else {
			uses++
		}
	}
	if defs != 1 {
		t.Errorf("definitions = %d, want 1", defs)
	}
	if uses != 2 {
		t.Errorf("references = %d, want 2", uses)
	}
}

func TestMatchersDistinctNames(t *testing.T) {
	src := []byte("example.test {\n\t@api path /api/*\n\t@static path /static/*\n\thandle @api {\n\t\trespond ok\n\t}\n\thandle @static {\n\t\tfile_server\n\t}\n}\n")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	refs := Matchers(doc)
	defs := map[string]int{}
	uses := map[string]int{}
	for _, r := range refs {
		if r.Definition {
			defs[r.Name]++
		} else {
			uses[r.Name]++
		}
	}
	if defs["api"] != 1 || defs["static"] != 1 {
		t.Errorf("definitions = %v, want api and static once each", defs)
	}
	if uses["api"] != 1 || uses["static"] != 1 {
		t.Errorf("references = %v, want api and static once each", uses)
	}
}

func TestMatchersPartiallyParsed(t *testing.T) {
	src := []byte("example.test {\n\t@api path /api/*\n\thandle @api {\n\t\trespond ok\n")
	doc := Parse(src)
	if doc.Err == nil {
		t.Fatal("expected a parse error")
	}
	refs := Matchers(doc)
	defs, uses := 0, 0
	for _, r := range refs {
		if r.Definition {
			defs++
		} else {
			uses++
		}
	}
	if defs != 1 || uses != 1 {
		t.Errorf("matchers on partial input = %d defs %d uses, want 1/1", defs, uses)
	}
}

func TestNodeKeyStable(t *testing.T) {
	src := []byte("example.test {\n\trespond ok\n}\n")
	doc := Parse(src)
	site := doc.Nodes[0]
	key1 := NodeKey(site)

	// The same node found after a re-parse (tree rebuild) has the same key.
	doc2 := Parse(src)
	site2 := doc2.Nodes[0]
	if NodeKey(site2) != key1 {
		t.Errorf("node key changed across rebuilds: %q vs %q", key1, NodeKey(site2))
	}

	// A different range yields a different key.
	mutated := site
	mutated.Range.Start++
	if NodeKey(mutated) == key1 {
		t.Errorf("node key did not change with the range")
	}
}

// TestFoldsCompatFixture sweeps the Phase 2 compat corpus: every structural
// block folds, leaves do not, and partially parsed fixtures degrade.
func TestFoldsCompatFixture(t *testing.T) {
	src := loadFixture(t, "compat")
	doc := Parse(src)
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	folds := Folds(doc)
	// globals, snippet, named route, 2 sites + nested route/handle blocks
	if len(folds) < 8 {
		t.Fatalf("folds = %d, want >= 8", len(folds))
	}
	names := map[string]int{}
	for _, f := range folds {
		names[f.Name]++
		if f.StartLine < 1 || f.EndLine < f.StartLine {
			t.Errorf("fold %+v has invalid line span", f)
		}
		if !f.Foldable {
			t.Errorf("fold %+v is not marked foldable", f)
		}
	}
	// The compat corpus folds: globals, the snippet, the named route (with
	// its reverse_proxy), the two sites, their handler blocks and the
	// nested route/file_server/log blocks.
	if names["example.test"] != 1 || names["route"] != 1 || names["handle"] != 3 {
		t.Errorf("fold names = %v, want the compat structure", names)
	}
	if names["reverse_proxy"] != 1 || names["file_server"] != 1 || names["log"] != 1 {
		t.Errorf("fold names = %v, want the nested directive blocks too", names)
	}
}

func TestIsBlockNodeUnlexableDirectiveIsNotBlock(t *testing.T) {
	src := []byte("x \"unterminated")
	n := Node{Kind: KindDirective, Name: "x", Range: SourceRange{Start: 0, End: len(src)}}
	if isBlockNode(src, n) {
		t.Error("an un-lexable directive must not fold as a block")
	}
}

func TestFoldsStrayBraceInLeafDirective(t *testing.T) {
	// A partially parsed file keeps the tree: the respond leaf contains an
	// unquoted brace, so it folds as a block even without children.
	src := []byte("example.test {\n\trespond ok {\n}\n")
	doc := Parse(src)
	folds := Folds(doc)
	if len(folds) != 2 {
		t.Fatalf("folds = %d, want 2 (%+v)", len(folds), folds)
	}
	findFold(t, folds, 15, 31)
}

func TestLandmarksUnclosedBlock(t *testing.T) {
	doc := Parse([]byte("example.test {"))
	if len(doc.Nodes) == 0 {
		t.Fatal("expected a site node for an unclosed block")
	}
	l := LandmarksOf(doc.Source, doc.Nodes[0])
	if l.CloseBraceLine != 0 {
		t.Errorf("unclosed block close brace line = %d, want 0", l.CloseBraceLine)
	}
	if l.ChildIndent != "\t" {
		t.Errorf("unclosed block child indent = %q, want \\t", l.ChildIndent)
	}
}

func TestMatchersSkipsUnlexableDirective(t *testing.T) {
	raw := "respond \"unterminated"
	doc := &Document{Source: []byte(raw + "\n"), Nodes: []Node{
		{Kind: KindDirective, Name: "respond", Range: SourceRange{Start: 0, End: len(raw)}},
	}}
	if refs := Matchers(doc); len(refs) != 0 {
		t.Fatalf("Matchers = %v, want none for an un-lexable directive", refs)
	}
}
