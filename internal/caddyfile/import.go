package caddyfile

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ImportKind classifies how an import directive was resolved.
type ImportKind int

const (
	// ImportSnippet means the pattern matched a defined snippet by name.
	ImportSnippet ImportKind = iota
	// ImportFile means the pattern matched one or more files, either an
	// exact path or a glob expansion.
	ImportFile
)

// ImportRef is one resolved import directive inside a source document.
// Imported files stay separate documents with their own byte ranges: nothing
// is spliced into From.Source, so every edit keeps identifying the exact
// file it changes.
type ImportRef struct {
	// From is the document containing the directive.
	From *Document
	// Node is the import directive node (a KindDirective leaf or block).
	Node Node
	// Pattern is the first argument of the import directive as written.
	Pattern string
	// Args are the remaining arguments of the import directive. Snippet
	// argument substitution ({args[...]}, {block}) is a later milestone.
	Args string
	// FromSnippet is the name of the snippet containing the directive, or
	// "" when the directive is not inside a snippet.
	FromSnippet string
	// Kind says whether the import resolved to a snippet or to files.
	Kind ImportKind
	// Snippet is the matched snippet name when Kind is ImportSnippet.
	Snippet string
	// SnippetDoc is the document defining the snippet when Kind is
	// ImportSnippet.
	SnippetDoc *Document
	// Files lists the parsed imported documents, in glob order, when Kind
	// is ImportFile. The same document may appear in several refs.
	Files []*Document
}

// ImportGraph is the result of resolving every import of a root document
// into a graph of separate source documents.
type ImportGraph struct {
	// Root is the parsed root document.
	Root *Document
	// Documents lists the root document first, then every imported file in
	// first-seen depth-first order, without duplicates by path.
	Documents []*Document
	// Imports lists every import directive found, in document order.
	Imports []*ImportRef
	// Warnings are non-fatal resolution notices (empty glob matches, empty
	// imported files).
	Warnings []string
	// Err is the first fatal resolution error, if any: missing files,
	// import cycles, invalid glob patterns or snippet redeclarations.
	Err error
}

// Resolve parses rootSrc as the document at rootPath and resolves every
// import directive transitively, mirroring Caddy's resolution rules:
//
//   - a pattern that matches a defined snippet wins over files; snippets are
//     collected from the root document first (position-independent) and from
//     each imported file when it is loaded;
//   - file patterns are relative to the directory of the importing document;
//   - globs expand deterministically in sorted order; hidden files are
//     skipped when the final glob segment starts with '*'; a glob with more
//     than one wildcard is an error, and a glob with no matches is a warning;
//   - importing a missing specific file is an error; importing an empty file
//     is a warning;
//   - a file importing itself (transitively) and a snippet importing itself
//     are errors, as are cycles among snippets;
//   - a parse error in the root document or in any imported file is recorded
//     in Err; the failing document keeps its own Err as well;
//   - rootPath is normalized with filepath.Clean so that ./Caddyfile and
//     root/../root/Caddyfile identify the same document.
//
// readFile is injected so resolution is testable without touching the real
// filesystem and the caller controls access. Resolution never writes.
func Resolve(rootPath string, rootSrc []byte, readFile func(string) ([]byte, error)) *ImportGraph {
	rootPath = filepath.Clean(rootPath)
	g := &ImportGraph{Root: Parse(rootSrc)}
	g.Root.Path = rootPath
	g.Documents = []*Document{g.Root}
	if g.Root.Err != nil {
		g.setErr("parse error in %s: %v", rootPath, g.Root.Err)
	}

	snippets := map[string]*Document{}
	collectSnippets := func(d *Document) {
		walkNodes(d.Nodes, func(n Node) {
			if n.Kind != KindSnippet {
				return
			}
			if _, dup := snippets[n.Name]; dup {
				g.setErr("redeclaration of previously declared snippet %q", n.Name)
				return
			}
			snippets[n.Name] = d
		})
	}
	collectSnippets(g.Root)

	loaded := map[string]*Document{rootPath: g.Root}

	var resolveDoc func(d *Document, chain []string)
	resolveDoc = func(d *Document, chain []string) {
		handleImport := func(n Node, inSnippet string) {
			pattern, args := importPattern(n)
			if pattern == "" {
				g.setErr("import on line %d of %s requires a non-empty filepath", n.Range.StartLine, d.Path)
				return
			}
			if sd, ok := snippets[pattern]; ok {
				if inSnippet == pattern {
					g.setErr("import cycle detected: snippet %q imports itself", pattern)
				}
				g.Imports = append(g.Imports, &ImportRef{
					From: d, Node: n, Pattern: pattern, Args: args,
					FromSnippet: inSnippet, Kind: ImportSnippet,
					Snippet: pattern, SnippetDoc: sd,
				})
				return
			}
			paths, isGlob, err := resolvePattern(d.Path, pattern)
			if err != nil {
				g.setErr("%v", err)
				return
			}
			ref := &ImportRef{
				From: d, Node: n, Pattern: pattern, Args: args,
				FromSnippet: inSnippet, Kind: ImportFile,
			}
			if len(paths) == 0 && isGlob {
				g.Warnings = append(g.Warnings, fmt.Sprintf("no files matching import glob pattern %q in %s", pattern, d.Path))
				g.Imports = append(g.Imports, ref)
				return
			}
			// Append before resolving children so Imports stays in document
			// order: a parent's ref precedes the refs of the files it loads.
			g.Imports = append(g.Imports, ref)
			for _, p := range paths {
				if containsStr(chain, p) {
					g.setErr("import cycle detected: %s", strings.Join(append(append([]string{}, chain...), p), " → "))
					continue
				}
				doc, ok := loaded[p]
				if !ok {
					src, err := readFile(p)
					if err != nil {
						if isGlob {
							g.setErr("could not import %s: %v", p, err)
						} else {
							g.setErr("file to import not found: %s (imported from %s)", pattern, d.Path)
						}
						return
					}
					if len(strings.TrimSpace(string(src))) == 0 {
						g.Warnings = append(g.Warnings, fmt.Sprintf("import file is empty: %s", p))
					}
					doc = Parse(src)
					doc.Path = p
					if doc.Err != nil {
						g.setErr("parse error in %s: %v", p, doc.Err)
					}
					loaded[p] = doc
					g.Documents = append(g.Documents, doc)
					collectSnippets(doc)
					resolveDoc(doc, append(chain, p))
				}
				ref.Files = append(ref.Files, doc)
			}
		}

		var visit func(nodes []Node, inSnippet string)
		visit = func(nodes []Node, inSnippet string) {
			for _, n := range nodes {
				if n.IsDirective("import") {
					handleImport(n, inSnippet)
					continue // the block after an import is data, not directives
				}
				next := inSnippet
				if n.Kind == KindSnippet {
					next = n.Name
				}
				visit(n.Children, next)
			}
		}
		visit(d.Nodes, "")
	}
	resolveDoc(g.Root, []string{rootPath})

	g.detectSnippetCycles()
	return g
}

// importPattern extracts the first argument (the import pattern) and the
// remaining arguments from an import directive's Args.
func importPattern(n Node) (pattern, args string) {
	toks, err := lex([]byte(n.Args))
	if err != nil || len(toks) == 0 {
		return "", ""
	}
	return toks[0].Text, strings.TrimSpace(n.Args[toks[0].End:])
}

// resolvePattern turns an import pattern into the candidate file paths,
// relative to the directory of the importing document, expanding globs like
// Caddy does.
func resolvePattern(fromPath, pattern string) (paths []string, isGlob bool, err error) {
	full := pattern
	if !filepath.IsAbs(pattern) {
		full = filepath.Join(filepath.Dir(fromPath), pattern)
	}
	full = filepath.Clean(full)

	if !strings.ContainsAny(full, "*?[") {
		return []string{full}, false, nil
	}
	if strings.Count(full, "*") > 1 || strings.Count(full, "?") > 1 ||
		(strings.Contains(full, "[") && strings.Contains(full, "]")) {
		return nil, true, fmt.Errorf("glob pattern may only contain one wildcard (*), but has others: %s", full)
	}
	matches, err := filepath.Glob(full)
	if err != nil {
		return nil, true, err
	}
	// Skip hidden files when the final path segment starts with a wildcard,
	// matching Caddy (importing `.*` explicitly includes them).
	segments := strings.Split(full, string(filepath.Separator))
	if strings.HasPrefix(segments[len(segments)-1], "*") {
		kept := matches[:0]
		for _, m := range matches {
			if !strings.HasPrefix(filepath.Base(m), ".") {
				kept = append(kept, m)
			}
		}
		matches = kept
	}
	return matches, true, nil
}

// detectSnippetCycles reports cycles among snippet imports, such as
// (A) importing B and (B) importing A. Self-imports are already reported
// during resolution.
func (g *ImportGraph) detectSnippetCycles() {
	adj := map[string][]string{}
	for _, ref := range g.Imports {
		if ref.Kind != ImportSnippet || ref.FromSnippet == "" {
			continue
		}
		adj[ref.FromSnippet] = append(adj[ref.FromSnippet], ref.Snippet)
	}

	const (
		white = iota
		gray
		black
	)
	colors := map[string]int{}
	var visit func(name string) bool
	visit = func(name string) bool {
		colors[name] = gray
		for _, next := range adj[name] {
			switch colors[next] { // missing key reads as white
			case gray:
				g.setErr("import cycle detected among snippets: %s → %s", name, next)
				return true
			case white:
				if visit(next) {
					return true
				}
			}
		}
		colors[name] = black
		return false
	}
	for name := range adj {
		if colors[name] == white && visit(name) {
			return
		}
	}
}

func (g *ImportGraph) setErr(format string, args ...any) {
	if g.Err == nil {
		g.Err = fmt.Errorf(format, args...)
	}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
