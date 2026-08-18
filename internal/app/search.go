package app

import (
	"fmt"
	"strings"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

// maxSearchResults caps the hits returned by a single search so a huge
// document or log history can never stall the UI.
const maxSearchResults = 200

// SearchKind classifies a search hit.
type SearchKind int

const (
	// SearchNode is a hit on a site/node label.
	SearchNode SearchKind = iota
	// SearchDocument is a hit on a document path or a content line.
	SearchDocument
	// SearchLog is a hit on a log-history entry.
	SearchLog
)

// SearchItem is one flattened tree row the UI builds for the search
// scope: a document row (HasNode false) or any node of the parse tree
// (site, snippet, named route, global options, structural directive,
// import, anonymous block, and terminal directives that are hidden from
// the tree pane). Search covers every node; hidden leaves select their
// nearest structural ancestor on activation.
type SearchItem struct {
	Label   string
	Doc     *caddyfile.Document
	Node    caddyfile.Node
	HasNode bool
}

// SearchScope is the read-only search input: tree rows (root and imports)
// plus the already-loaded bounded log history.
type SearchScope struct {
	Items []SearchItem
	Logs  []logs.Entry
}

// SearchResult is a single hit.
type SearchResult struct {
	Kind     SearchKind
	Label    string
	Doc      *caddyfile.Document
	Node     caddyfile.Node
	Line     int // 1-based for content hits; 0 for path/node-only hits
	LogIndex int // index into SearchScope.Logs
}

// Searcher finds read-only, case-insensitive substring matches across node
// labels, document paths, document content lines and the loaded log
// history. It never touches disk and never mutates its input.
type Searcher interface {
	Search(query string, scope SearchScope) []SearchResult
}

// SearcherFunc adapts a plain function to the Searcher interface (mirrors
// the other app *Func adapters; tests use it).
type SearcherFunc func(query string, scope SearchScope) []SearchResult

// Search implements Searcher.
func (f SearcherFunc) Search(query string, scope SearchScope) []SearchResult {
	return f(query, scope)
}

// NewSearcher returns the production Searcher.
func NewSearcher() Searcher { return substringSearcher{} }

// substringSearcher is the concrete, dependency-free search implementation:
// case-insensitive substring matching, no regex, no fuzzy matching, no disk
// access.
type substringSearcher struct{}

// Search implements Searcher. An empty (or whitespace-only) query yields
// no results. Results are occurrence-based: every node label hit carries
// its document and start line, every content hit its exact line, and the
// label always embeds "path:line". Occurrences are deduplicated by
// document and line — a node label hit and a content hit on the same line
// collapse into one result, with the node winning so activating it keeps
// the structural block selection. Node hits come first (in scope order),
// then document path hits and content lines, and the log hits last. The
// result count is capped at maxSearchResults.
func (substringSearcher) Search(query string, scope SearchScope) []SearchResult {
	if strings.TrimSpace(query) == "" {
		return nil
	}
	q := strings.ToLower(query)
	seen := map[string]bool{}
	var out []SearchResult
	// Node label hits first: they carry the structural node, so a node hit
	// wins over a content hit on the same line.
	for _, item := range scope.Items {
		if !item.HasNode || !strings.Contains(strings.ToLower(item.Label), q) {
			continue
		}
		line := item.Node.Range.StartLine
		key := searchHitKey(item.Doc, line)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, SearchResult{
			Kind:  SearchNode,
			Label: searchLineLabel(item.Doc.Path, line, item.Label),
			Doc:   item.Doc,
			Node:  item.Node,
			Line:  line,
		})
		if len(out) >= maxSearchResults {
			return out
		}
	}
	// Document path hits and content lines, skipping lines already claimed
	// by a node hit.
	for _, item := range scope.Items {
		if item.HasNode || item.Doc == nil {
			continue
		}
		if strings.Contains(strings.ToLower(item.Doc.Path), q) {
			out = append(out, SearchResult{Kind: SearchDocument, Label: item.Doc.Path, Doc: item.Doc})
			if len(out) >= maxSearchResults {
				return out
			}
		}
		lines := strings.Split(string(item.Doc.Source), "\n")
		for i, line := range lines {
			if !strings.Contains(strings.ToLower(line), q) {
				continue
			}
			ln := i + 1
			key := searchHitKey(item.Doc, ln)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, SearchResult{
				Kind:  SearchDocument,
				Label: searchLineLabel(item.Doc.Path, ln, strings.TrimSpace(line)),
				Doc:   item.Doc,
				Line:  ln,
			})
			if len(out) >= maxSearchResults {
				return out
			}
		}
	}
	// Log history hits, after every tree hit.
	for i, entry := range scope.Logs {
		if strings.Contains(strings.ToLower(string(entry.Raw)), q) {
			out = append(out, SearchResult{Kind: SearchLog, Label: searchLogLabel(entry), LogIndex: i})
			if len(out) >= maxSearchResults {
				return out
			}
		}
	}
	return out
}

// searchHitKey is the dedup identity of an occurrence: the document path
// plus its 1-based line. Node label hits and content line hits on the same
// line of the same document collapse into one result.
func searchHitKey(doc *caddyfile.Document, line int) string {
	if doc == nil {
		return fmt.Sprintf("?:%d", line)
	}
	return fmt.Sprintf("%s:%d", doc.Path, line)
}

// searchLineLabel builds the occurrence label for a hit: "path:line" plus
// the matched context (the node label or the trimmed content line), so
// every occurrence shows its exact location at a glance.
func searchLineLabel(path string, line int, context string) string {
	return fmt.Sprintf("%s:%d  %s", path, line, context)
}

// maxSearchLogLabel bounds a log search label so a huge entry cannot blow
// up the result list.
const maxSearchLogLabel = 120

// searchLogLabel builds a compact label for a log search hit: the parsed
// timestamp / level / logger / message when available, otherwise the raw
// line, bounded to maxSearchLogLabel.
func searchLogLabel(entry logs.Entry) string {
	raw := string(entry.Raw)
	if !entry.Parsed {
		return truncateSearchLabel(raw)
	}
	parts := make([]string, 0, 4)
	if !entry.Timestamp.IsZero() {
		parts = append(parts, entry.Timestamp.Local().Format("15:04:05.000"))
	}
	if entry.Level != "" {
		parts = append(parts, strings.ToUpper(entry.Level))
	}
	if entry.Logger != "" {
		parts = append(parts, entry.Logger)
	}
	if entry.Msg != "" {
		parts = append(parts, entry.Msg)
	}
	if len(parts) == 0 {
		return truncateSearchLabel(raw)
	}
	return truncateSearchLabel(strings.Join(parts, " "))
}

// truncateSearchLabel shortens s to maxSearchLogLabel runes, appending an
// ellipsis when it had to cut.
func truncateSearchLabel(s string) string {
	r := []rune(s)
	if len(r) <= maxSearchLogLabel {
		return s
	}
	return string(r[:maxSearchLogLabel]) + "…"
}
