package caddyfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findTopLevel returns the first direct child of the document matching name.
func findTopLevel(t *testing.T, doc *Document, name string) Node {
	t.Helper()
	for _, n := range doc.Nodes {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("top-level node %q not found", name)
	return Node{}
}

// nodeSpecFor builds the spec a table case passes to CreateNode, resolving
// the anchor by name against the document. An empty anchor name resolves
// the global options block, whose node name is "".
func nodeSpecFor(t *testing.T, doc *Document, spec NodeSpec, anchor string) NodeSpec {
	t.Helper()
	if spec.Position == InsertBefore || spec.Position == InsertAfter {
		spec.Anchor = findNode(t, doc, anchor)
	}
	return spec
}

func TestPlanCreateNode(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		parent  string // node name hosting the new node; "" targets top level
		spec    NodeSpec
		anchor  string // node name for InsertBefore/InsertAfter
		want    string // exact expected bytes after applying the edit
		wantErr error
	}{
		// --- top-level site blocks --------------------------------------
		{
			name: "site at end",
			src:  "a.test {\n\trespond ok\n}\n",
			spec: NodeSpec{Kind: KindSite, Name: "example.com", Position: InsertAtEnd},
			want: "a.test {\n\trespond ok\n}\nexample.com {\n}\n",
		},
		{
			name: "site at start",
			src:  "a.test {\n\trespond ok\n}\n",
			spec: NodeSpec{Kind: KindSite, Name: "example.com", Position: InsertAtStart},
			want: "example.com {\n}\na.test {\n\trespond ok\n}\n",
		},
		{
			name:   "site before anchor",
			src:    "a.test {\n\trespond ok\n}\nb.test {\n\tfile_server\n}\n",
			spec:   NodeSpec{Kind: KindSite, Name: "example.com", Position: InsertBefore},
			anchor: "b.test",
			want:   "a.test {\n\trespond ok\n}\nexample.com {\n}\nb.test {\n\tfile_server\n}\n",
		},
		{
			name:   "site after anchor",
			src:    "a.test {\n\trespond ok\n}\nb.test {\n\tfile_server\n}\n",
			spec:   NodeSpec{Kind: KindSite, Name: "example.com", Position: InsertAfter},
			anchor: "a.test",
			want:   "a.test {\n\trespond ok\n}\nexample.com {\n}\nb.test {\n\tfile_server\n}\n",
		},
		{
			name: "site at start keeps global options first",
			src:  "{\n\tdebug\n}\na.test {\n\trespond ok\n}\n",
			spec: NodeSpec{Kind: KindSite, Name: "example.com", Position: InsertAtStart},
			want: "{\n\tdebug\n}\nexample.com {\n}\na.test {\n\trespond ok\n}\n",
		},
		{
			name:    "site before global options anchor is invalid",
			src:     "{\n\tdebug\n}\na.test {\n}\n",
			spec:    NodeSpec{Kind: KindSite, Name: "example.com", Position: InsertBefore},
			anchor:  "",
			wantErr: ErrInvalidContext,
		},
		{
			name: "site in empty document",
			src:  "",
			spec: NodeSpec{Kind: KindSite, Name: "example.com", Position: InsertAtEnd},
			want: "example.com {\n}\n",
		},
		{
			name: "site in comment-only document",
			src:  "# leading comment\n",
			spec: NodeSpec{Kind: KindSite, Name: "example.com", Position: InsertAtEnd},
			want: "example.com {\n}\n# leading comment\n",
		},
		// --- snippets ----------------------------------------------------
		{
			name: "snippet at end",
			src:  "a.test {\n}\n",
			spec: NodeSpec{Kind: KindSnippet, Name: "logging", Position: InsertAtEnd},
			want: "a.test {\n}\n(logging) {\n}\n",
		},
		{
			name: "snippet at start",
			src:  "a.test {\n}\n",
			spec: NodeSpec{Kind: KindSnippet, Name: "logging", Position: InsertAtStart},
			want: "(logging) {\n}\na.test {\n}\n",
		},
		{
			name:    "snippet name with space is invalid",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: KindSnippet, Name: "my snippet", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "snippet name with newline is invalid",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: KindSnippet, Name: "log\nx", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "snippet name empty is invalid",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: KindSnippet, Name: "", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		// --- named routes ------------------------------------------------
		{
			name: "named route at start",
			src:  "a.test {\n}\n",
			spec: NodeSpec{Kind: KindNamedRoute, Name: "app-proxy", Position: InsertAtStart},
			want: "&(app-proxy) {\n}\na.test {\n}\n",
		},
		{
			name: "named route at end of empty document",
			src:  "",
			spec: NodeSpec{Kind: KindNamedRoute, Name: "app-proxy", Position: InsertAtEnd},
			want: "&(app-proxy) {\n}\n",
		},
		// --- global options block ----------------------------------------
		{
			name: "global options in empty document",
			src:  "",
			spec: NodeSpec{Kind: KindGlobalOptions, Position: InsertAtStart},
			want: "{\n}\n",
		},
		{
			name:    "duplicate global options is invalid",
			src:     "{\n\tdebug\n}\na.test {\n}\n",
			spec:    NodeSpec{Kind: KindGlobalOptions, Position: InsertAtStart},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "global options after content is invalid",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: KindGlobalOptions, Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		// --- nested handler blocks ---------------------------------------
		{
			name:   "route inside site at start",
			src:    "example.com {\n\trespond ok\n}\n",
			parent: "example.com",
			spec:   NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtStart},
			want:   "example.com {\n\troute {\n\t}\n\trespond ok\n}\n",
		},
		{
			name:   "handle with path inside site at end",
			src:    "example.com {\n\trespond ok\n}\n",
			parent: "example.com",
			spec:   NodeSpec{Kind: KindDirective, Name: "handle", Args: "/api", Position: InsertAtEnd},
			want:   "example.com {\n\trespond ok\n\thandle /api {\n\t}\n}\n",
		},
		{
			name:   "handle_path inside route",
			src:    "example.com {\n\troute {\n\t\tfile_server\n\t}\n}\n",
			parent: "route",
			spec:   NodeSpec{Kind: KindDirective, Name: "handle_path", Args: "/static", Position: InsertAtStart},
			want:   "example.com {\n\troute {\n\t\thandle_path /static {\n\t\t}\n\t\tfile_server\n\t}\n}\n",
		},
		{
			name:   "handle_errors inside empty site",
			src:    "example.com {\n}\n",
			parent: "example.com",
			spec:   NodeSpec{Kind: KindDirective, Name: "handle_errors", Position: InsertAtStart},
			want:   "example.com {\n\thandle_errors {\n\t}\n}\n",
		},
		{
			name:   "handle inside handle",
			src:    "example.com {\n\thandle /api {\n\t\tfile_server\n\t}\n}\n",
			parent: "handle",
			spec:   NodeSpec{Kind: KindDirective, Name: "handle", Args: "/admin", Position: InsertAtEnd},
			want:   "example.com {\n\thandle /api {\n\t\tfile_server\n\t\thandle /admin {\n\t\t}\n\t}\n}\n",
		},
		{
			name:   "route inside snippet",
			src:    "(snip) {\n}\n",
			parent: "snip",
			spec:   NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtEnd},
			want:   "(snip) {\n\troute {\n\t}\n}\n",
		},
		{
			name:   "route inside named route",
			src:    "&(app) {\n}\n",
			parent: "app",
			spec:   NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtEnd},
			want:   "&(app) {\n\troute {\n\t}\n}\n",
		},
		{
			name:   "route after sibling anchor",
			src:    "example.com {\n\tfile_server\n\trespond ok\n}\n",
			parent: "example.com",
			spec:   NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAfter},
			anchor: "file_server",
			want:   "example.com {\n\tfile_server\n\troute {\n\t}\n\trespond ok\n}\n",
		},
		{
			name:   "route before sibling anchor",
			src:    "example.com {\n\tfile_server\n\trespond ok\n}\n",
			parent: "example.com",
			spec:   NodeSpec{Kind: KindDirective, Name: "route", Position: InsertBefore},
			anchor: "respond",
			want:   "example.com {\n\tfile_server\n\troute {\n\t}\n\trespond ok\n}\n",
		},
		{
			name:    "handler inside global options is invalid",
			src:     "{\n\tdebug\n}\n",
			parent:  "",
			spec:    NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtStart},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "handler inside leaf directive is invalid",
			src:     "example.com {\n\trespond ok\n}\n",
			parent:  "respond",
			spec:    NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtStart},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "anchor not a direct child is invalid",
			src:     "a.test {\n\tfile_server\n}\nb.test {\n\trespond ok\n}\n",
			parent:  "a.test",
			spec:    NodeSpec{Kind: KindDirective, Name: "route", Position: InsertBefore},
			anchor:  "respond",
			wantErr: ErrInvalidContext,
		},
		// --- invalid child/parent combinations ---------------------------
		{
			name:    "site inside site is invalid",
			src:     "example.com {\n}\n",
			parent:  "example.com",
			spec:    NodeSpec{Kind: KindSite, Name: "new.test", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "snippet inside site is invalid",
			src:     "example.com {\n}\n",
			parent:  "example.com",
			spec:    NodeSpec{Kind: KindSnippet, Name: "snip", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "handler at top level is invalid",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtStart},
			wantErr: ErrInvalidContext,
		},
		// --- unsupported specs -------------------------------------------
		{
			name:    "non-handler directive node is unsupported",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: KindDirective, Name: "reverse_proxy", Position: InsertAtEnd},
			wantErr: ErrUnsupported,
		},
		{
			name:    "unknown kind is unsupported",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: Kind(99), Position: InsertAtEnd},
			wantErr: ErrUnsupported,
		},
		// --- invalid headers ---------------------------------------------
		{
			name:    "empty site name is invalid",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: KindSite, Name: " ", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "site name with newline is invalid",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: KindSite, Name: "a\nb", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "site name classifying as snippet is invalid",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: KindSite, Name: "(foo)", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "site name classifying as named route is invalid",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: KindSite, Name: "&(foo)", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "site address ending in brace is invalid",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: KindSite, Name: "a{", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "site name with structural token is invalid",
			src:     "a.test {\n}\n",
			spec:    NodeSpec{Kind: KindSite, Name: "{", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "handler arguments with newline are invalid",
			src:     "example.com {\n}\n",
			parent:  "example.com",
			spec:    NodeSpec{Kind: KindDirective, Name: "handle", Args: "/a\n/b", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		{
			name:    "handler arguments hiding the brace are invalid",
			src:     "example.com {\n}\n",
			parent:  "example.com",
			spec:    NodeSpec{Kind: KindDirective, Name: "route", Args: "# comment", Position: InsertAtEnd},
			wantErr: ErrInvalidContext,
		},
		// --- ambiguous placements ----------------------------------------
		{
			name:    "compact single-line parent is ambiguous",
			src:     "example.com { respond ok }\n",
			parent:  "example.com",
			spec:    NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtStart},
			wantErr: ErrAmbiguous,
		},
		// --- byte preservation -------------------------------------------
		{
			name: "comments and blank lines preserved",
			src:  "# header comment\nexample.com {\n\t# site comment\n\tfile_server\n\n\t# another comment\n\trespond ok\n}\n# trailing comment\n",
			spec: NodeSpec{Kind: KindSite, Name: "new.test", Position: InsertAtEnd},
			want: "# header comment\nexample.com {\n\t# site comment\n\tfile_server\n\n\t# another comment\n\trespond ok\n}\nnew.test {\n}\n# trailing comment\n",
		},
		{
			name:   "blank lines inside block preserved",
			src:    "example.com {\n\t# keep\n\n\tfile_server\n\n\t# tail\n}\n",
			parent: "example.com",
			spec:   NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtEnd},
			want:   "example.com {\n\t# keep\n\n\tfile_server\n\troute {\n\t}\n\n\t# tail\n}\n",
		},
		{
			name:   "crlf nested block",
			src:    "example.com {\r\n\tfile_server\r\n}\r\n",
			parent: "example.com",
			spec:   NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtEnd},
			want:   "example.com {\r\n\tfile_server\r\n\troute {\r\n\t}\r\n}\r\n",
		},
		{
			name: "crlf top-level site",
			src:  "example.com {\r\n}\r\n",
			spec: NodeSpec{Kind: KindSite, Name: "new.test", Position: InsertAtEnd},
			want: "example.com {\r\n}\r\nnew.test {\r\n}\r\n",
		},
		{
			name: "bom preserved at start",
			src:  "\xEF\xBB\xBFexample.com {\n}\n",
			spec: NodeSpec{Kind: KindSite, Name: "new.test", Position: InsertAtStart},
			want: "\xEF\xBB\xBFnew.test {\n}\nexample.com {\n}\n",
		},
		{
			name: "bom preserved at end",
			src:  "\xEF\xBB\xBFexample.com {\n}\n",
			spec: NodeSpec{Kind: KindSite, Name: "new.test", Position: InsertAtEnd},
			want: "\xEF\xBB\xBFexample.com {\n}\nnew.test {\n}\n",
		},
		{
			name:   "bom preserved with nested insertion",
			src:    "\xEF\xBB\xBFexample.com {\n}\n",
			parent: "example.com",
			spec:   NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtEnd},
			want:   "\xEF\xBB\xBFexample.com {\n\troute {\n\t}\n}\n",
		},
		{
			name: "crlf and bom combined",
			src:  "\xEF\xBB\xBFexample.com {\r\n}\r\n",
			spec: NodeSpec{Kind: KindSite, Name: "new.test", Position: InsertAtStart},
			want: "\xEF\xBB\xBFnew.test {\r\n}\r\nexample.com {\r\n}\r\n",
		},
		{
			name:   "handler inside empty separated-brace site",
			src:    "example.com\n{\n}\n",
			parent: "example.com",
			spec:   NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtStart},
			want:   "example.com\n{\n\troute {\n\t}\n}\n",
		},
		{
			name:   "handler inside populated separated-brace site",
			src:    "example.com\n{\n\trespond ok\n}\n",
			parent: "example.com",
			spec:   NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtEnd},
			want:   "example.com\n{\n\trespond ok\n\troute {\n\t}\n}\n",
		},
		// --- brace-less sites --------------------------------------------
		{
			name:   "handler inside brace-less site at end",
			src:    "localhost:8080\n\tfile_server\n",
			parent: "localhost:8080",
			spec:   NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtEnd},
			want:   "localhost:8080\n\tfile_server\n\troute {\n\t}\n",
		},
		{
			name:   "handler inside brace-less site at start",
			src:    "localhost:8080\n\tfile_server\n",
			parent: "localhost:8080",
			spec:   NodeSpec{Kind: KindDirective, Name: "handle", Args: "/api", Position: InsertAtStart},
			want:   "localhost:8080\n\thandle /api {\n\t}\n\tfile_server\n",
		},
		{
			name:   "handler inside brace-less site without trailing newline",
			src:    "localhost:8080",
			parent: "localhost:8080",
			spec:   NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtStart},
			want:   "localhost:8080\n\troute {\n\t}\n",
		},
		{
			name:    "top-level site next to brace-less site is ambiguous",
			src:     "localhost:8080\n\tfile_server\n",
			spec:    NodeSpec{Kind: KindSite, Name: "new.test", Position: InsertAtStart},
			wantErr: ErrAmbiguous,
		},
		{
			name:    "top-level snippet next to brace-less site is ambiguous",
			src:     "localhost:8080\n\tfile_server\n",
			spec:    NodeSpec{Kind: KindSnippet, Name: "snip", Position: InsertAtStart},
			wantErr: ErrAmbiguous,
		},
		{
			name:    "top-level global options next to brace-less site is ambiguous",
			src:     "localhost:8080\n\tfile_server\n",
			spec:    NodeSpec{Kind: KindGlobalOptions, Position: InsertAtStart},
			wantErr: ErrAmbiguous,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, p := planDoc(t, tt.src)
			var parent *Node
			if tt.parent != "" {
				n := findNode(t, doc, tt.parent)
				parent = &n
			}
			spec := nodeSpecFor(t, doc, tt.spec, tt.anchor)
			e, err := p.CreateNode(parent, spec)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("CreateNode: expected error %v, got a planned edit %+v", tt.wantErr, e)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("CreateNode error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateNode: %v", err)
			}
			if e.DocID != doc.Path {
				t.Errorf("edit DocID = %q, want %q", e.DocID, doc.Path)
			}
			out := applyPlanned(t, doc, e)
			if string(out) != tt.want {
				t.Errorf("result = %q, want %q", out, tt.want)
			}
		})
	}
}

func TestPlanCreateNodeMalformedDocument(t *testing.T) {
	// A document with a parse error must reject creation for both top-level
	// and nested targets.
	src := []byte("example.com {\n\treverse_proxy localhost:8080\n")
	doc := Parse(src)
	if doc.Err == nil {
		t.Fatal("expected a parse error")
	}
	p := NewPlanner(doc)
	if _, err := p.CreateNode(nil, NodeSpec{Kind: KindSite, Name: "new.test", Position: InsertAtEnd}); !errors.Is(err, ErrParseError) {
		t.Fatalf("top-level CreateNode error = %v, want ErrParseError", err)
	}
	site := findNode(t, doc, "example.com")
	if _, err := p.CreateNode(&site, NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtStart}); !errors.Is(err, ErrParseError) {
		t.Fatalf("nested CreateNode error = %v, want ErrParseError", err)
	}
}

func TestPlanCreateNodeForeignAnchor(t *testing.T) {
	doc, p := planDoc(t, "example.com {\n\tfile_server\n}\n")
	site := findNode(t, doc, "example.com")
	foreign := Node{Kind: KindDirective, Name: "respond", Range: SourceRange{Start: 9999, End: 10000}}
	if _, err := p.CreateNode(&site, NodeSpec{Kind: KindDirective, Name: "route", Position: InsertBefore, Anchor: foreign}); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("CreateNode with foreign anchor error = %v, want ErrNodeNotFound", err)
	}
}

func TestPlanCreateNodeForeignTopLevelAnchor(t *testing.T) {
	_, p := planDoc(t, "a.test {\n}\n")
	foreign := Node{Kind: KindSnippet, Name: "snip", Range: SourceRange{Start: 9999, End: 10000}}
	if _, err := p.CreateNode(nil, NodeSpec{Kind: KindSite, Name: "new.test", Position: InsertBefore, Anchor: foreign}); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("CreateNode with foreign top-level anchor error = %v, want ErrNodeNotFound", err)
	}
}

func TestPlanCreateNodeNilDocument(t *testing.T) {
	p := NewPlanner(nil)
	if _, err := p.CreateNode(nil, NodeSpec{Kind: KindSite, Name: "x"}); err == nil {
		t.Fatal("CreateNode on a nil document: expected error")
	}
}

func TestPlanCreateNodeHandlerInsideGlobalOptions(t *testing.T) {
	doc, p := planDoc(t, "{\n\tdebug\n}\n")
	globals := findNode(t, doc, "")
	if _, err := p.CreateNode(&globals, NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtStart}); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("handler inside global options error = %v, want ErrInvalidContext", err)
	}
}

func TestPlanCreateNodeSpecRejections(t *testing.T) {
	tests := []struct {
		name string
		src  string
		spec NodeSpec
	}{
		{"global options with name", "", NodeSpec{Kind: KindGlobalOptions, Name: "x"}},
		{"snippet with arguments", "", NodeSpec{Kind: KindSnippet, Name: "snip", Args: "x"}},
		{"site header with unterminated quote", "", NodeSpec{Kind: KindSite, Name: "\"abc"}},
		{"site header with quoted token", "", NodeSpec{Kind: KindSite, Name: "\"a\""}},
		{"handler arguments with unterminated quote", "example.com {\n}\n", NodeSpec{Kind: KindDirective, Name: "handle", Args: "\"abc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, p := planDoc(t, tt.src)
			if _, err := p.CreateNode(nil, tt.spec); !errors.Is(err, ErrInvalidContext) {
				t.Fatalf("CreateNode error = %v, want ErrInvalidContext", err)
			}
		})
	}
}

func TestPlanCreateNodeUnknownPosition(t *testing.T) {
	_, p := planDoc(t, "a.test {\n}\n")
	if _, err := p.CreateNode(nil, NodeSpec{Kind: KindSite, Name: "new.test", Position: InsertPosition(99)}); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("CreateNode unknown position error = %v, want ErrAmbiguous", err)
	}
}

func TestPlanCreateNodeForeignParent(t *testing.T) {
	_, p := planDoc(t, "example.com {\n}\n")
	foreign := Node{Kind: KindSite, Name: "other.test", Range: SourceRange{Start: 9999, End: 10000}}
	if _, err := p.CreateNode(&foreign, NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtStart}); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("CreateNode with foreign parent error = %v, want ErrNodeNotFound", err)
	}
}

func TestPlanCreateNodeImportedDocumentIdentity(t *testing.T) {
	rootPath := filepath.Join("testdata", "fixtures", "compat-imports", "Caddyfile")
	src, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	g := Resolve(rootPath, src, os.ReadFile)
	if g.Err != nil {
		t.Fatalf("Resolve: %v", g.Err)
	}
	appDoc := g.Documents[2] // sites/app.caddy
	if appDoc.Err != nil {
		t.Fatalf("app.caddy parse error: %v", appDoc.Err)
	}
	p := NewPlanner(appDoc)
	site := findTopLevel(t, appDoc, "app.example.test")
	e, err := p.CreateNode(&site, NodeSpec{Kind: KindDirective, Name: "route", Position: InsertAtEnd})
	if err != nil {
		t.Fatalf("CreateNode on imported document: %v", err)
	}
	if e.DocID != appDoc.Path {
		t.Errorf("edit DocID = %q, want %q", e.DocID, appDoc.Path)
	}
	out, err := e.Apply(appDoc.Source)
	if err != nil {
		t.Fatal(err)
	}
	// The new node landed in the imported document; the root Caddyfile is
	// untouched and the imported document's own content stays verbatim.
	if !strings.Contains(string(out), "\troute {\n\t}\n") {
		t.Errorf("patched imported document = %q, want the new route block", out)
	}
	if !bytes.Contains(out, []byte("import ../fragments/upstream.caddy")) || !bytes.Contains(out, []byte("# Nested relative import")) {
		t.Errorf("imported document content was lost: %q", out)
	}
	if bytes.Contains(g.Root.Source, []byte("\troute {\n\t}\n")) {
		t.Errorf("root document was unexpectedly modified: %q", g.Root.Source)
	}
}

func TestPlanCreateNodePreservesUnrelatedBytesAcrossPlans(t *testing.T) {
	// Applying several creations in one plan must keep every unrelated byte
	// identical and land each node exactly once.
	src := "example.com {\n\t# keep me\n\tfile_server\n}\n"
	doc, p := planDoc(t, src)
	site := findNode(t, doc, "example.com")
	fs := findNode(t, doc, "file_server")

	topEdit, err := p.CreateNode(nil, NodeSpec{Kind: KindSite, Name: "new.test", Position: InsertAfter, Anchor: site})
	if err != nil {
		t.Fatalf("CreateNode top-level: %v", err)
	}
	childEdit, err := p.CreateNode(&site, NodeSpec{Kind: KindDirective, Name: "route", Position: InsertBefore, Anchor: fs})
	if err != nil {
		t.Fatalf("CreateNode nested: %v", err)
	}
	out, err := (Plan{*childEdit, *topEdit}).ApplyAll(doc.Source)
	if err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	want := "example.com {\n\t# keep me\n\troute {\n\t}\n\tfile_server\n}\nnew.test {\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
	if !bytes.Contains(out, []byte("# keep me")) {
		t.Errorf("comment was lost: %q", out)
	}
}

func TestPlanCreateNodeRejectsHeaderWithoutTrailingBrace(t *testing.T) {
	// A trailing comment swallows the generated opening brace, so the spec
	// is rejected instead of producing a brace-less site.
	_, p := planDoc(t, "")
	if _, err := p.CreateNode(nil, NodeSpec{Kind: KindSite, Name: "foo # comment", Position: InsertAtEnd}); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("CreateNode error = %v, want ErrInvalidContext", err)
	}
}

func TestPlanCreateNodeRejectsNestedAnchorAtTopLevel(t *testing.T) {
	doc, p := planDoc(t, "a.test {\n\trespond ok\n}\n")
	respond := findNode(t, doc, "respond")
	if _, err := p.CreateNode(nil, NodeSpec{Kind: KindSite, Name: "new.test", Position: InsertBefore, Anchor: respond}); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("CreateNode nested anchor error = %v, want ErrInvalidContext", err)
	}
}

func TestPlanIsBracelessSiteDegradesOnLexError(t *testing.T) {
	raw := "x \"unterminated"
	p := NewPlanner(craftedDoc(raw))
	n := Node{Kind: KindSite, Name: "x", Range: SourceRange{Start: 0, End: len(raw)}}
	if p.isBracelessSite(n) {
		t.Error("an un-lexable site must not be treated as brace-less")
	}
}
