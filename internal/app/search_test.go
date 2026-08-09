package app

import (
	"strings"
	"testing"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

// searchDoc returns a bare document with the given path, source and nodes.
// The search layer never parses: it reads Path and Source only, and the UI
// supplies the flattened labels.
func searchDoc(path, src string, nodes ...caddyfile.Node) *caddyfile.Document {
	return &caddyfile.Document{Path: path, Source: []byte(src), Nodes: nodes}
}

// searchNode returns a node suitable for a SearchItem.
func searchNode(kind caddyfile.Kind, name string, start, end int) caddyfile.Node {
	return caddyfile.Node{
		Kind:  kind,
		Name:  name,
		Range: caddyfile.SourceRange{Start: start, End: end, StartLine: 1, EndLine: 1},
	}
}

func TestSearch_EmptyQueryNoResults(t *testing.T) {
	doc := searchDoc("config/Caddyfile", "example.test {\n\trespond ok\n}\n")
	scope := SearchScope{Items: []SearchItem{{Label: doc.Path, Doc: doc}}}
	s := NewSearcher()
	for _, q := range []string{"", "   ", "\t"} {
		if got := s.Search(q, scope); got != nil {
			t.Errorf("Search(%q) = %v, want nil", q, got)
		}
	}
}

func TestSearch_CaseInsensitive(t *testing.T) {
	doc := searchDoc("config/Caddyfile", "example.test {\n\trespond ok\n}\n")
	scope := SearchScope{Items: []SearchItem{{Label: doc.Path, Doc: doc}}}
	s := NewSearcher()
	got := s.Search("CONFIG/caddy", scope)
	if len(got) != 1 {
		t.Fatalf("Search(CONFIG/caddy) = %d results, want 1 (case-insensitive path match)", len(got))
	}
	if got[0].Kind != SearchDocument {
		t.Errorf("kind = %v, want SearchDocument", got[0].Kind)
	}
	if got[0].Label != doc.Path {
		t.Errorf("Label = %q, want the path %q", got[0].Label, doc.Path)
	}
}

func TestSearch_NodeLabelMatch(t *testing.T) {
	doc := searchDoc("config/Caddyfile", "example.test {\n}\n(snippet) {\n}\n&(route) {\n}\n",
		searchNode(caddyfile.KindSite, "example.test", 0, 16),
		searchNode(caddyfile.KindSnippet, "snippet", 17, 28),
		searchNode(caddyfile.KindNamedRoute, "route", 29, 40),
	)
	// The UI supplies the flattened labels, including the snippet/named
	// route prefixes.
	scope := SearchScope{Items: []SearchItem{
		{Label: "example.test", Doc: doc, Node: doc.Nodes[0], HasNode: true},
		{Label: "snippet (snippet)", Doc: doc, Node: doc.Nodes[1], HasNode: true},
		{Label: "route &(route)", Doc: doc, Node: doc.Nodes[2], HasNode: true},
	}}
	s := NewSearcher()

	if got := s.Search("EXAMPLE", scope); len(got) != 1 {
		t.Fatalf("Search(EXAMPLE) = %d results, want 1 node hit", len(got))
	}

	got := s.Search("snippet", scope)
	if len(got) != 1 {
		t.Fatalf("Search(snippet) = %d results, want 1 snippet hit", len(got))
	}
	if got[0].Kind != SearchNode || got[0].Node.Name != "snippet" {
		t.Errorf("hit = %+v, want a SearchNode on the snippet node", got[0])
	}

	got = s.Search("ROUTE", scope)
	if len(got) != 1 {
		t.Fatalf("Search(ROUTE) = %d results, want 1 named-route hit", len(got))
	}
	if got[0].Kind != SearchNode || got[0].Node.Name != "route" {
		t.Errorf("hit = %+v, want a SearchNode on the named-route node", got[0])
	}
}

func TestSearch_DocumentPathMatch(t *testing.T) {
	root := searchDoc("config/Caddyfile", "import sites/a.caddy\n")
	imported := searchDoc("config/sites/a.caddy", "a.example.test {\n\trespond ok\n}\n")
	scope := SearchScope{Items: []SearchItem{
		{Label: root.Path, Doc: root},
		{Label: imported.Path, Doc: imported},
	}}
	s := NewSearcher()

	got := s.Search("config/sites", scope)
	if len(got) != 1 {
		t.Fatalf("Search(config/sites) = %d results, want 1 path hit", len(got))
	}
	if got[0].Kind != SearchDocument {
		t.Errorf("kind = %v, want SearchDocument", got[0].Kind)
	}
	if got[0].Doc != imported {
		t.Errorf("hit points to %v, want the imported document", got[0].Doc)
	}
	if got[0].Line != 0 {
		t.Errorf("Line = %d, want 0 for a path-only hit", got[0].Line)
	}
}

func TestSearch_DocumentContentLine(t *testing.T) {
	src := "example.test {\n\trespond ok\n\tencode gzip\n}\n"
	doc := searchDoc("config/Caddyfile", src)
	scope := SearchScope{Items: []SearchItem{{Label: doc.Path, Doc: doc}}}
	s := NewSearcher()

	got := s.Search("encode gzip", scope)
	if len(got) != 1 {
		t.Fatalf("Search(encode gzip) = %d results, want 1 content hit", len(got))
	}
	if got[0].Kind != SearchDocument {
		t.Errorf("kind = %v, want SearchDocument", got[0].Kind)
	}
	if got[0].Line != 3 {
		t.Errorf("Line = %d, want 3 (1-based line of the match)", got[0].Line)
	}
	if !strings.Contains(got[0].Label, "config/Caddyfile:3") {
		t.Errorf("Label = %q, want it to embed path:line", got[0].Label)
	}
	if !strings.Contains(got[0].Label, "encode gzip") {
		t.Errorf("Label = %q, want it to embed the trimmed line", got[0].Label)
	}
}

func TestSearch_ImportedFileContent(t *testing.T) {
	root := searchDoc("config/Caddyfile", "import sites/a.caddy\n")
	imported := searchDoc("config/sites/a.caddy", "a.example.test {\n\trespond from-import\n}\n")
	scope := SearchScope{Items: []SearchItem{
		{Label: root.Path, Doc: root},
		{Label: imported.Path, Doc: imported},
	}}
	s := NewSearcher()

	got := s.Search("from-import", scope)
	if len(got) != 1 {
		t.Fatalf("Search(from-import) = %d results, want 1", len(got))
	}
	if got[0].Kind != SearchDocument {
		t.Errorf("kind = %v, want SearchDocument", got[0].Kind)
	}
	if got[0].Doc != imported {
		t.Errorf("hit Doc = %v, want the imported file", got[0].Doc)
	}
	if got[0].Line != 2 {
		t.Errorf("Line = %d, want 2", got[0].Line)
	}
}

func TestSearch_LogMatch(t *testing.T) {
	entries := []logs.Entry{
		{Raw: []byte(`{"level":"info","msg":"handled request"}`), Parsed: true, Level: "info", Msg: "handled request", Status: -1},
		{Raw: []byte("2026/08/08 12:00:00 ERROR something happened"), Status: -1},
	}
	scope := SearchScope{Logs: entries}
	s := NewSearcher()

	got := s.Search("handled", scope)
	if len(got) != 1 {
		t.Fatalf("Search(handled) = %d results, want 1 log hit", len(got))
	}
	if got[0].Kind != SearchLog {
		t.Errorf("kind = %v, want SearchLog", got[0].Kind)
	}
	if got[0].LogIndex != 0 {
		t.Errorf("LogIndex = %d, want 0", got[0].LogIndex)
	}
	// The parsed label is compact (timestamp/level/msg), not the raw JSON.
	if !strings.Contains(got[0].Label, "handled request") {
		t.Errorf("Label = %q, want it to embed the message", got[0].Label)
	}

	got = s.Search("ERROR something", scope)
	if len(got) != 1 {
		t.Fatalf("Search(ERROR something) = %d results, want 1 raw-line log hit", len(got))
	}
	if got[0].Kind != SearchLog || got[0].LogIndex != 1 {
		t.Errorf("hit = %+v, want a SearchLog on entry 1", got[0])
	}
}

func TestSearch_NoMatches(t *testing.T) {
	doc := searchDoc("config/Caddyfile", "example.test {\n\trespond ok\n}\n")
	scope := SearchScope{
		Items: []SearchItem{{Label: doc.Path, Doc: doc}},
		Logs:  []logs.Entry{{Raw: []byte("unrelated"), Status: -1}},
	}
	s := NewSearcher()
	if got := s.Search("zzz-no-such-token", scope); got != nil {
		t.Errorf("Search = %v, want nil when nothing matches", got)
	}
}

func TestSearch_CappedResults(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 300; i++ {
		content.WriteString("\trepeat me\n")
	}
	doc := searchDoc("config/Caddyfile", content.String())
	scope := SearchScope{Items: []SearchItem{{Label: doc.Path, Doc: doc}}}
	s := NewSearcher()

	got := s.Search("repeat me", scope)
	if len(got) != maxSearchResults {
		t.Fatalf("Search = %d results, want the cap %d", len(got), maxSearchResults)
	}
}

// TestSearch_EveryCapBranch verifies the result cap applies to each of the
// three tree branches (node label, document path, document content line)
// and to the log branch, so a huge scope can never grow the result list
// past maxSearchResults.
func TestSearch_EveryCapBranch(t *testing.T) {
	s := NewSearcher()

	// Node-label hits: 220 matching node rows cap at maxSearchResults.
	nodes := make([]SearchItem, 0, 220)
	for i := 0; i < 220; i++ {
		nodes = append(nodes, SearchItem{Label: "node hit", HasNode: true})
	}
	if got := s.Search("node hit", SearchScope{Items: nodes}); len(got) != maxSearchResults {
		t.Errorf("node hits = %d results, want the cap %d", len(got), maxSearchResults)
	}

	// Document-path hits: 220 matching paths cap at maxSearchResults.
	docs := make([]SearchItem, 0, 220)
	for i := 0; i < 220; i++ {
		docs = append(docs, SearchItem{Label: "path hit", Doc: searchDoc("path hit", "no content")})
	}
	if got := s.Search("path hit", SearchScope{Items: docs}); len(got) != maxSearchResults {
		t.Errorf("path hits = %d results, want the cap %d", len(got), maxSearchResults)
	}

	// Log hits: 220 matching entries cap at maxSearchResults.
	entries := make([]logs.Entry, 0, 220)
	for i := 0; i < 220; i++ {
		entries = append(entries, logs.Entry{Raw: []byte("log hit")})
	}
	if got := s.Search("log hit", SearchScope{Logs: entries}); len(got) != maxSearchResults {
		t.Errorf("log hits = %d results, want the cap %d", len(got), maxSearchResults)
	}
}

// TestSearch_SkipsNilDocument verifies that a document row without a
// document (defensive: the UI never builds one) is skipped without
// touching its label.
func TestSearch_SkipsNilDocument(t *testing.T) {
	s := NewSearcher()
	got := s.Search("anything", SearchScope{Items: []SearchItem{{Label: "no doc", Doc: nil}}})
	if len(got) != 0 {
		t.Errorf("Search over a nil document row = %v, want no hits", got)
	}
}

// TestSearchLogLabel_Variants covers every searchLogLabel branch: a fully
// populated parsed entry, a parsed entry with no fields (falls back to the
// raw line) and a long label that must be truncated.
func TestSearchLogLabel_Variants(t *testing.T) {
	full := logs.Entry{
		Raw:       []byte(`{"level":"warn","msg":"boot","logger":"http"}`),
		Parsed:    true,
		Timestamp: time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC),
		Level:     "warn",
		Logger:    "http",
		Msg:       "boot",
	}
	if label := searchLogLabel(full); !strings.Contains(label, full.Timestamp.Local().Format("15:04:05.000")) ||
		!strings.Contains(label, "WARN") ||
		!strings.Contains(label, "http") ||
		!strings.Contains(label, "boot") {
		t.Errorf("searchLogLabel(full) = %q, want timestamp/level/logger/msg", label)
	}

	bare := logs.Entry{Raw: []byte("raw line"), Parsed: true}
	if label := searchLogLabel(bare); label != "raw line" {
		t.Errorf("searchLogLabel(bare) = %q, want the raw line", label)
	}

	long := strings.Repeat("a", maxSearchLogLabel+10)
	if label := truncateSearchLabel(long); len([]rune(label)) != maxSearchLogLabel+1 {
		t.Errorf("truncateSearchLabel = %d runes, want %d (label + ellipsis)", len([]rune(label)), maxSearchLogLabel+1)
	}
}
