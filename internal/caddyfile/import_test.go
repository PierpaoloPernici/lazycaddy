package caddyfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFS is an in-memory filesystem for exact-path resolution tests.
type fakeFS map[string]string

func (fs fakeFS) readFile(path string) ([]byte, error) {
	if src, ok := fs[path]; ok {
		return []byte(src), nil
	}
	return nil, fmt.Errorf("no such file: %s", path)
}

func resolveStrings(t *testing.T, rootPath, rootSrc string, fs fakeFS) *ImportGraph {
	t.Helper()
	g := Resolve(rootPath, []byte(rootSrc), fs.readFile)
	if g.Err != nil {
		t.Fatalf("Resolve returned error: %v", g.Err)
	}
	return g
}

func TestResolveRealisticFixture(t *testing.T) {
	rootPath := filepath.Join("testdata", "fixtures", "realistic", "Caddyfile")
	src, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	g := Resolve(rootPath, src, os.ReadFile)
	if g.Err != nil {
		t.Fatalf("Resolve returned error: %v", g.Err)
	}
	if len(g.Documents) != 3 {
		t.Fatalf("documents = %d, want 3 (root + snippets + one site)", len(g.Documents))
	}
	if g.Documents[0] != g.Root {
		t.Errorf("Documents[0] is not the root document")
	}
	if len(g.Imports) != 3 {
		t.Fatalf("imports = %d, want 3", len(g.Imports))
	}
	rootImports := g.Imports[:2]
	for i, want := range []string{"snippets/security_headers.caddy", "sites/*.caddy"} {
		imp := rootImports[i]
		if imp.Kind != ImportFile || imp.Pattern != want {
			t.Errorf("import[%d] = kind %v pattern %q, want file %q", i, imp.Kind, imp.Pattern, want)
		}
		if len(imp.Files) != 1 {
			t.Fatalf("import[%d] files = %d, want 1", i, len(imp.Files))
		}
		if imp.Files[0] != g.Documents[i+1] {
			t.Errorf("import[%d] file does not match Documents[%d]", i, i+1)
		}
	}
	// The site file imports the snippet defined in the snippets file.
	snippetImport := g.Imports[2]
	if snippetImport.Kind != ImportSnippet || snippetImport.Snippet != "security_headers" {
		t.Errorf("import[2] = kind %v snippet %q, want snippet security_headers", snippetImport.Kind, snippetImport.Snippet)
	}
	if snippetImport.SnippetDoc != g.Documents[1] {
		t.Errorf("snippet import resolved to the wrong document")
	}
	if snippetImport.From != g.Documents[2] {
		t.Errorf("snippet import has wrong From document")
	}
}

func TestResolveHomelabSnippetImports(t *testing.T) {
	src := loadFixture(t, "homelab")
	noReads := func(string) ([]byte, error) {
		return nil, fmt.Errorf("homelab fixture must not read any files")
	}
	g := Resolve("homelab/Caddyfile", src, noReads)
	if g.Err != nil {
		t.Fatalf("Resolve returned error: %v", g.Err)
	}
	if len(g.Documents) != 1 || g.Documents[0] != g.Root {
		t.Errorf("documents = %d, want only the root", len(g.Documents))
	}
	if len(g.Imports) != 13 {
		t.Fatalf("imports = %d, want 13", len(g.Imports))
	}
	for i, imp := range g.Imports {
		if imp.Kind != ImportSnippet {
			t.Errorf("import[%d] = kind %v, want snippet", i, imp.Kind)
		}
		if imp.Snippet != "reverse_proxy_http" && imp.Snippet != "reverse_proxy_https" {
			t.Errorf("import[%d] snippet = %q, want reverse_proxy_http/https", i, imp.Snippet)
		}
		if imp.SnippetDoc != g.Root {
			t.Errorf("import[%d] resolved to a document other than the root", i)
		}
		if imp.Args == "" {
			t.Errorf("import[%d] has no args, want an upstream address", i)
		}
	}
}

func TestResolveRelativePaths(t *testing.T) {
	fs := fakeFS{
		"root/Caddyfile":   "import dir/a.caddy\n",
		"root/dir/a.caddy": "import b.caddy\n",
		"root/dir/b.caddy": "import c.caddy\n",
		"root/dir/c.caddy": "respond ok\n",
	}
	g := resolveStrings(t, "root/Caddyfile", fs["root/Caddyfile"], fs)
	if len(g.Documents) != 4 {
		t.Fatalf("documents = %d, want 4", len(g.Documents))
	}
	// b.caddy's import (the third ref) must resolve relative to root/dir.
	if got := g.Imports[2].Files[0].Path; got != "root/dir/c.caddy" {
		t.Errorf("nested import resolved to %q, want root/dir/c.caddy", got)
	}
}

func TestResolveDiamondImportSharesDocument(t *testing.T) {
	fs := fakeFS{
		"root/Caddyfile":    "import a.caddy\nimport b.caddy\n",
		"root/a.caddy":      "import shared.caddy\n",
		"root/b.caddy":      "import shared.caddy\n",
		"root/shared.caddy": "respond ok\n",
	}
	g := resolveStrings(t, "root/Caddyfile", fs["root/Caddyfile"], fs)
	shared := 0
	for _, d := range g.Documents {
		if filepath.Base(d.Path) == "shared.caddy" {
			shared++
		}
	}
	if shared != 1 {
		t.Errorf("shared.caddy loaded %d times, want 1", shared)
	}
	if len(g.Imports) != 4 {
		t.Fatalf("imports = %d, want 4", len(g.Imports))
	}
	// Both branches must reference the same shared document.
	var sharedRefs []*ImportRef
	for _, imp := range g.Imports {
		if imp.Pattern == "shared.caddy" {
			sharedRefs = append(sharedRefs, imp)
		}
	}
	if len(sharedRefs) != 2 || sharedRefs[0].Files[0] != sharedRefs[1].Files[0] {
		t.Errorf("diamond branches must reference the same shared document, got %+v", sharedRefs)
	}
}

func TestResolveCycle(t *testing.T) {
	fs := fakeFS{
		"root/Caddyfile": "import a.caddy\n",
		"root/a.caddy":   "import b.caddy\n",
		"root/b.caddy":   "import a.caddy\n",
	}
	g := Resolve("root/Caddyfile", []byte(fs["root/Caddyfile"]), fs.readFile)
	if g.Err == nil || !strings.Contains(g.Err.Error(), "cycle") {
		t.Fatalf("Err = %v, want an import cycle error", g.Err)
	}
}

func TestResolveSelfImportingSnippet(t *testing.T) {
	src := "(A) {\n\timport A\n}\n:80 {\n\timport A\n}\n"
	g := Resolve("root/Caddyfile", []byte(src), fakeFS{}.readFile)
	if g.Err == nil || !strings.Contains(g.Err.Error(), "cycle") {
		t.Fatalf("Err = %v, want a snippet self-import cycle error", g.Err)
	}
}

func TestResolveCrossSnippetCycle(t *testing.T) {
	src := "(A) {\n\timport B\n}\n(B) {\n\timport A\n}\n:80 {\n\timport A\n}\n"
	g := Resolve("root/Caddyfile", []byte(src), fakeFS{}.readFile)
	if g.Err == nil || !strings.Contains(g.Err.Error(), "cycle") {
		t.Fatalf("Err = %v, want a cross-snippet cycle error", g.Err)
	}
}

func TestResolveSnippetAfterUse(t *testing.T) {
	// Snippets may be defined after their first use in the same document.
	src := "example.com {\n\timport common\n}\n(common) {\n\tgzip foo\n}\n"
	g := resolveStrings(t, "root/Caddyfile", src, fakeFS{})
	if len(g.Imports) != 1 || g.Imports[0].Kind != ImportSnippet || g.Imports[0].Snippet != "common" {
		t.Fatalf("imports = %+v, want one snippet import of common", g.Imports)
	}
}

func TestResolveSnippetPrecedenceOverFile(t *testing.T) {
	fs := fakeFS{
		"root/Caddyfile": "(foo) {\n\trespond ok\n}\nimport foo\n",
		"root/foo":       "not a caddyfile\n",
	}
	g := resolveStrings(t, "root/Caddyfile", fs["root/Caddyfile"], fs)
	if len(g.Imports) != 1 || g.Imports[0].Kind != ImportSnippet {
		t.Fatalf("imports = %+v, want the snippet to win over the file", g.Imports)
	}
}

func TestResolveMissingFileIsError(t *testing.T) {
	fs := fakeFS{"root/Caddyfile": "import missing.caddy\n"}
	g := Resolve("root/Caddyfile", []byte(fs["root/Caddyfile"]), fs.readFile)
	if g.Err == nil || !strings.Contains(g.Err.Error(), "not found") {
		t.Fatalf("Err = %v, want a not-found error", g.Err)
	}
}

func TestResolveEmptyGlobIsWarning(t *testing.T) {
	g := Resolve("root/Caddyfile", []byte("import missing/*.caddy\n"), fakeFS{}.readFile)
	if g.Err != nil {
		t.Fatalf("Err = %v, want nil for an empty glob match", g.Err)
	}
	if len(g.Warnings) != 1 || !strings.Contains(g.Warnings[0], "no files matching") {
		t.Fatalf("warnings = %v, want a no-match warning", g.Warnings)
	}
}

func TestResolveMultipleWildcardsIsError(t *testing.T) {
	g := Resolve("root/Caddyfile", []byte("import a/*/b/*\n"), fakeFS{}.readFile)
	if g.Err == nil || !strings.Contains(g.Err.Error(), "one wildcard") {
		t.Fatalf("Err = %v, want a single-wildcard error", g.Err)
	}
}

func TestResolveEmptyImportFileIsWarning(t *testing.T) {
	fs := fakeFS{
		"root/Caddyfile":   "import empty.caddy\n",
		"root/empty.caddy": "  \n",
	}
	g := resolveStrings(t, "root/Caddyfile", fs["root/Caddyfile"], fs)
	if len(g.Warnings) != 1 || !strings.Contains(g.Warnings[0], "empty") {
		t.Fatalf("warnings = %v, want an empty-file warning", g.Warnings)
	}
}

func TestResolveSnippetRedeclarationIsError(t *testing.T) {
	src := "(dup) {\n\trespond a\n}\n(dup) {\n\trespond b\n}\n:80 {\n\timport dup\n}\n"
	g := Resolve("root/Caddyfile", []byte(src), fakeFS{}.readFile)
	if g.Err == nil || !strings.Contains(g.Err.Error(), "redeclaration") {
		t.Fatalf("Err = %v, want a snippet redeclaration error", g.Err)
	}
}

func TestResolveImportWithBlockArgs(t *testing.T) {
	// The block after import is data, not directives to resolve.
	src := "example.com {\n\timport hello-world {\n\t\tContent-Type text/html\n\t}\n}\n(hello-world) {\n\t{block}\n}\n"
	g := resolveStrings(t, "root/Caddyfile", src, fakeFS{})
	if len(g.Imports) != 1 {
		t.Fatalf("imports = %d, want 1", len(g.Imports))
	}
	if g.Imports[0].Kind != ImportSnippet || g.Imports[0].Snippet != "hello-world" {
		t.Errorf("import = %+v, want the hello-world snippet", g.Imports[0])
	}
}

func TestResolveGlobSkipsHiddenFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.caddy", "a.example.test {\n}\n")
	write("b.caddy", "b.example.test {\n}\n")
	write(".hidden.caddy", "hidden.example.test {\n}\n")

	rootPath := filepath.Join(dir, "Caddyfile")
	rootSrc := "import *.caddy\n"

	g := Resolve(rootPath, []byte(rootSrc), os.ReadFile)
	if g.Err != nil {
		t.Fatalf("Resolve returned error: %v", g.Err)
	}
	if len(g.Imports) != 1 || len(g.Imports[0].Files) != 2 {
		t.Fatalf("imports = %+v, want one import of 2 files (hidden skipped)", g.Imports)
	}
	names := []string{
		filepath.Base(g.Imports[0].Files[0].Path),
		filepath.Base(g.Imports[0].Files[1].Path),
	}
	if names[0] != "a.caddy" || names[1] != "b.caddy" {
		t.Errorf("glob files = %v, want sorted a.caddy b.caddy", names)
	}

	// A final segment of .* includes hidden files (the hidden-file filter is
	// only applied when the segment starts with a wildcard); the glob itself
	// matches only dotfiles.
	g2 := Resolve(rootPath, []byte("import .*\n"), os.ReadFile)
	if g2.Err != nil {
		t.Fatalf("Resolve returned error: %v", g2.Err)
	}
	if len(g2.Imports[0].Files) != 1 || filepath.Base(g2.Imports[0].Files[0].Path) != ".hidden.caddy" {
		t.Errorf(".* glob files = %v, want only .hidden.caddy", g2.Imports[0].Files)
	}
}
