package caddyfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// planDoc parses src and returns the document and planner.
func planDoc(t *testing.T, src string) (*Document, *Planner) {
	t.Helper()
	doc := Parse([]byte(src))
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	return doc, NewPlanner(doc)
}

// findNode walks the tree for the first node matching name (directive name,
// site name, snippet name or named route name) and returns a copy.
func findNode(t *testing.T, doc *Document, name string) Node {
	t.Helper()
	var found *Node
	walkNodes(doc.Nodes, func(n Node) {
		if found == nil && n.Name == name {
			c := n
			found = &c
		}
	})
	if found == nil {
		t.Fatalf("node %q not found", name)
	}
	return *found
}

// findNodes returns every node with the given name, in document order, so
// tests can target repeated directive names (for example several handle
// blocks) by index.
func findNodes(t *testing.T, doc *Document, name string) []Node {
	t.Helper()
	var found []Node
	walkNodes(doc.Nodes, func(n Node) {
		if n.Name == name {
			found = append(found, n)
		}
	})
	if len(found) == 0 {
		t.Fatalf("node %q not found", name)
	}
	return found
}

// applyPlanned applies a planned edit and asserts the result parses cleanly.
func applyPlanned(t *testing.T, doc *Document, e *PlannedEdit) []byte {
	t.Helper()
	if e == nil {
		t.Fatal("planned edit is nil")
	}
	out, err := e.Apply(doc.Source)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if e.Op != EditDelete {
		if re := Parse(out); re.Err != nil {
			t.Fatalf("patched source does not parse: %v\npatched: %q", re.Err, out)
		}
	}
	return out
}

func TestPlanSetArgsLeaf(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\trespond ok\n\tfile_server\n}\n")
	respond := findNode(t, doc, "respond")
	e, err := p.SetArgs(respond, "not found")
	if err != nil {
		t.Fatalf("SetArgs: %v", err)
	}
	if e.Op != EditSetValue || e.DocID != doc.Path {
		t.Errorf("edit = %+v, want set-value on %q", e, doc.Path)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\trespond not found\n\tfile_server\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanSetArgsRemovesValues(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\trespond ok\n\tfile_server\n}\n")
	respond := findNode(t, doc, "respond")
	e, err := p.SetArgs(respond, "")
	if err != nil {
		t.Fatalf("SetArgs: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\trespond\n\tfile_server\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanSetArgsPreservesTrailingWhitespace(t *testing.T) {
	// Trailing whitespace after the last argument is outside the edited
	// span and must survive byte-exactly.
	doc, p := planDoc(t, "example.test {\n\trespond ok  \n}\n")
	respond := findNode(t, doc, "respond")
	e, err := p.SetArgs(respond, "")
	if err != nil {
		t.Fatalf("SetArgs: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\trespond  \n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanSetArgsAddsValues(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\tfile_server\n}\n")
	fs := findNode(t, doc, "file_server")
	e, err := p.SetArgs(fs, "/srv/www")
	if err != nil {
		t.Fatalf("SetArgs: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\tfile_server /srv/www\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanSetArgsBlockDirective(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\treverse_proxy localhost:8080 {\n\t\theader_up Host {host}\n\t}\n\tfile_server\n}\n")
	rp := findNode(t, doc, "reverse_proxy")
	e, err := p.SetArgs(rp, "localhost:9090")
	if err != nil {
		t.Fatalf("SetArgs: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\treverse_proxy localhost:9090 {\n\t\theader_up Host {host}\n\t}\n\tfile_server\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}

	// Removing the upstream keeps the block opener.
	doc2, p2 := planDoc(t, "example.test {\n\treverse_proxy localhost:8080 {\n\t\theader_up Host {host}\n\t}\n}\n")
	rp2 := findNode(t, doc2, "reverse_proxy")
	e2, err := p2.SetArgs(rp2, "")
	if err != nil {
		t.Fatalf("SetArgs(empty): %v", err)
	}
	out2 := applyPlanned(t, doc2, e2)
	want2 := "example.test {\n\treverse_proxy {\n\t\theader_up Host {host}\n\t}\n}\n"
	if string(out2) != want2 {
		t.Errorf("result = %q, want %q", out2, want2)
	}
}

func TestPlanSetArgSingleArgument(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\theader X-Frame-Options \"SAMEORIGIN\"\n}\n")
	h := findNode(t, doc, "header")
	e, err := p.SetArg(h, 1, `"DENY"`)
	if err != nil {
		t.Fatalf("SetArg: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\theader X-Frame-Options \"DENY\"\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanReverseProxyFieldsRoundTrip(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\treverse_proxy @api \"app one:8080\" app-02:8080 {\n\t\theader_up Host {host}\n\t}\n}\n")
	rp := findNode(t, doc, "reverse_proxy")
	fields, err := p.GetReverseProxyFields(rp)
	if err != nil {
		t.Fatalf("GetReverseProxyFields: %v", err)
	}
	if fields.Matcher != "@api" {
		t.Errorf("matcher = %q, want @api", fields.Matcher)
	}
	if got, want := strings.Join(fields.Upstreams, "|"), `"app one:8080"|app-02:8080`; got != want {
		t.Errorf("upstreams = %q, want %q", got, want)
	}

	e, err := p.SetReverseProxyFields(rp, ReverseProxyFields{
		Matcher:   "@backend",
		Upstreams: []string{"app-03:9090", `"app four:9090"`},
	})
	if err != nil {
		t.Fatalf("SetReverseProxyFields: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\treverse_proxy @backend app-03:9090 \"app four:9090\" {\n\t\theader_up Host {host}\n\t}\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanReverseProxyFieldsRejectsOtherDirective(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\trespond ok\n}\n")
	respond := findNode(t, doc, "respond")
	_, err := p.GetReverseProxyFields(respond)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("GetReverseProxyFields error = %v, want ErrUnsupported", err)
	}
	_, err = p.SetReverseProxyFields(respond, ReverseProxyFields{Upstreams: []string{"localhost:8080"}})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetReverseProxyFields error = %v, want ErrUnsupported", err)
	}
}

func TestPlanSetArgOutOfRange(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\theader X-Frame-Options \"SAMEORIGIN\"\n}\n")
	h := findNode(t, doc, "header")
	if _, err := p.SetArg(h, 5, "x"); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("SetArg out of range err = %v, want ErrInvalidContext", err)
	}
	if _, err := p.SetArg(h, -1, "x"); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("SetArg negative err = %v, want ErrInvalidContext", err)
	}
}

func TestPlanSetArgsRejectsBlockNode(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\trespond ok\n}\n")
	site := findNode(t, doc, "example.test")
	if _, err := p.SetArgs(site, "x"); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("SetArgs on a site err = %v, want ErrInvalidContext", err)
	}
}

func TestPlanSetArgsEscapedNewlineValue(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\theader X-Chained first \\\n\t\tsecond\n}\n")
	h := findNode(t, doc, "header")
	e, err := p.SetArgs(h, "X-B single")
	if err != nil {
		t.Fatalf("SetArgs: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\theader X-B single\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanInsertAtStart(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\tfile_server\n\trespond ok\n}\n")
	site := findNode(t, doc, "example.test")
	e, err := p.Insert(site, DirectiveInsert{Name: "encode", Args: "gzip", Position: InsertAtStart})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\tencode gzip\n\tfile_server\n\trespond ok\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanInsertAtEnd(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\tfile_server\n\trespond ok\n}\n")
	site := findNode(t, doc, "example.test")
	e, err := p.Insert(site, DirectiveInsert{Name: "encode", Args: "zstd", Position: InsertAtEnd})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\tfile_server\n\trespond ok\n\tencode zstd\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanInsertBeforeAndAfter(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\tfile_server\n\trespond ok\n}\n")
	site := findNode(t, doc, "example.test")
	fs := findNode(t, doc, "file_server")

	e, err := p.Insert(site, DirectiveInsert{Name: "redir", Args: "/old /new 302", Position: InsertBefore, Anchor: fs})
	if err != nil {
		t.Fatalf("Insert before: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\tredir /old /new 302\n\tfile_server\n\trespond ok\n}\n"
	if string(out) != want {
		t.Errorf("before result = %q, want %q", out, want)
	}

	e2, err := p.Insert(site, DirectiveInsert{Name: "redir", Args: "/old /new 302", Position: InsertAfter, Anchor: fs})
	if err != nil {
		t.Fatalf("Insert after: %v", err)
	}
	out2 := applyPlanned(t, doc, e2)
	want2 := "example.test {\n\tfile_server\n\tredir /old /new 302\n\trespond ok\n}\n"
	if string(out2) != want2 {
		t.Errorf("after result = %q, want %q", out2, want2)
	}
}

func TestPlanInsertEmptyBlock(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n}\n")
	site := findNode(t, doc, "example.test")
	e, err := p.Insert(site, DirectiveInsert{Name: "respond", Args: "ok", Position: InsertAtStart})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\trespond ok\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanInsertNestedBlockIndentation(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\troute {\n\t\thandle /x {\n\t\t}\n\t}\n}\n")
	var route, handle Node
	walkNodes(doc.Nodes, func(n Node) {
		if route.Name == "" && n.IsDirective("route") {
			route = n
		}
		if handle.Name == "" && n.IsDirective("handle") {
			handle = n
		}
	})

	// Inserting at the start of route places the line at route's child
	// indentation, before its first child.
	e, err := p.Insert(route, DirectiveInsert{Name: "respond", Args: "ok", Position: InsertAtStart})
	if err != nil {
		t.Fatalf("Insert into route: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\troute {\n\t\trespond ok\n\t\thandle /x {\n\t\t}\n\t}\n}\n"
	if string(out) != want {
		t.Errorf("route result = %q, want %q", out, want)
	}

	// Inserting into the nested handle block uses one deeper indentation.
	e2, err := p.Insert(handle, DirectiveInsert{Name: "respond", Args: "ok", Position: InsertAtStart})
	if err != nil {
		t.Fatalf("Insert into handle: %v", err)
	}
	out2 := applyPlanned(t, doc, e2)
	want2 := "example.test {\n\troute {\n\t\thandle /x {\n\t\t\trespond ok\n\t\t}\n\t}\n}\n"
	if string(out2) != want2 {
		t.Errorf("handle result = %q, want %q", out2, want2)
	}
}

func TestPlanInsertBracelessSite(t *testing.T) {
	doc, p := planDoc(t, "localhost:8080\n\tfile_server\n")
	site := findNode(t, doc, "localhost:8080")
	e, err := p.Insert(site, DirectiveInsert{Name: "encode", Args: "gzip", Position: InsertAtStart})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "localhost:8080\n\tencode gzip\n\tfile_server\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanInsertSeparatedBraceSite(t *testing.T) {
	doc, p := planDoc(t, "example.test\n{\n\trespond ok\n}\n")
	site := findNode(t, doc, "example.test")
	e, err := p.Insert(site, DirectiveInsert{Name: "file_server", Position: InsertAtEnd})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test\n{\n\trespond ok\n\tfile_server\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanInsertContextValidation(t *testing.T) {
	t.Run("tls inside route is invalid", func(t *testing.T) {
		doc, p := planDoc(t, "example.test {\n\troute {\n\t}\n}\n")
		var route Node
		walkNodes(doc.Nodes, func(n Node) {
			if route.Name == "" && n.IsDirective("route") {
				route = n
			}
		})
		if _, err := p.Insert(route, DirectiveInsert{Name: "tls", Position: InsertAtStart}); !errors.Is(err, ErrInvalidContext) {
			t.Fatalf("Insert tls into route err = %v, want ErrInvalidContext", err)
		}
	})
	t.Run("respond inside global options is invalid", func(t *testing.T) {
		doc, p := planDoc(t, "{\n\temail a@example.test\n}\n")
		globals := findNode(t, doc, "")
		if _, err := p.Insert(globals, DirectiveInsert{Name: "respond", Args: "ok", Position: InsertAtStart}); !errors.Is(err, ErrInvalidContext) {
			t.Fatalf("Insert respond into globals err = %v, want ErrInvalidContext", err)
		}
	})
	t.Run("log inside global options is valid", func(t *testing.T) {
		doc, p := planDoc(t, "{\n\temail a@example.test\n}\n")
		globals := findNode(t, doc, "")
		e, err := p.Insert(globals, DirectiveInsert{Name: "log", Args: "level DEBUG", Position: InsertAtStart})
		if err != nil {
			t.Fatalf("Insert log: %v", err)
		}
		out := applyPlanned(t, doc, e)
		want := "{\n\tlog level DEBUG\n\temail a@example.test\n}\n"
		if string(out) != want {
			t.Errorf("result = %q, want %q", out, want)
		}
	})
	t.Run("handler directive inside handle block is valid", func(t *testing.T) {
		doc, p := planDoc(t, "example.test {\n\thandle /api {\n\t\treverse_proxy localhost:8080\n\t}\n}\n")
		var handle Node
		walkNodes(doc.Nodes, func(n Node) {
			if handle.Name == "" && n.IsDirective("handle") {
				handle = n
			}
		})
		e, err := p.Insert(handle, DirectiveInsert{Name: "header", Args: "X-API 1", Position: InsertAtEnd})
		if err != nil {
			t.Fatalf("Insert header into handle: %v", err)
		}
		out := applyPlanned(t, doc, e)
		want := "example.test {\n\thandle /api {\n\t\treverse_proxy localhost:8080\n\t\theader X-API 1\n\t}\n}\n"
		if string(out) != want {
			t.Errorf("result = %q, want %q", out, want)
		}
	})
}

func TestInsertableDirectiveNamesAreContextAware(t *testing.T) {
	doc, _ := planDoc(t, "{\n\tdebug\n}\nexample.test {\n\troute {\n\t\trespond ok\n\t}\n}\n")
	globals := findNode(t, doc, "")
	site := findNode(t, doc, "example.test")
	route := findNode(t, doc, "route")
	if got := InsertableDirectiveNames(globals); !reflect.DeepEqual(got, []string{"log"}) {
		t.Fatalf("global insertable directives = %v, want [log]", got)
	}
	if got := InsertableDirectiveNames(site); len(got) != 9 || got[0] != "encode" || got[len(got)-1] != "tls" {
		t.Fatalf("site insertable directives = %v, want sorted common directives", got)
	}
	if got := InsertableDirectiveNames(route); len(got) != 7 {
		t.Fatalf("route insertable directives = %v, want 7 handler directives", got)
	}
}

func TestPlanInsertUnsupportedDirective(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n}\n")
	site := findNode(t, doc, "example.test")
	if _, err := p.Insert(site, DirectiveInsert{Name: "not_a_directive", Position: InsertAtStart}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Insert unsupported err = %v, want ErrUnsupported", err)
	}
}

func TestPlanInsertLeafParentIsInvalid(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\trespond ok\n}\n")
	respond := findNode(t, doc, "respond")
	if _, err := p.Insert(respond, DirectiveInsert{Name: "respond", Args: "x", Position: InsertAtStart}); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Insert into leaf err = %v, want ErrInvalidContext", err)
	}
}

func TestPlanInsertAnchorNotChild(t *testing.T) {
	doc, p := planDoc(t, "a.test {\n\tfile_server\n}\nb.test {\n\trespond ok\n}\n")
	siteA := findNode(t, doc, "a.test")
	respond := findNode(t, doc, "respond") // child of b.test
	if _, err := p.Insert(siteA, DirectiveInsert{Name: "encode", Args: "gzip", Position: InsertBefore, Anchor: respond}); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Insert with foreign anchor err = %v, want ErrInvalidContext", err)
	}
}

func TestPlanInsertCompactBlockIsAmbiguous(t *testing.T) {
	doc, p := planDoc(t, "example.test { respond ok }\n")
	site := findNode(t, doc, "example.test")
	if _, err := p.Insert(site, DirectiveInsert{Name: "respond", Args: "x", Position: InsertAtStart}); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("Insert into compact block err = %v, want ErrAmbiguous", err)
	}
}

func TestPlanInsertPreservesCommentsAndUnknown(t *testing.T) {
	src := "example.test {\n\t# keep this comment\n\tcustom_plugin thing\n\trespond ok\n}\n"
	doc, p := planDoc(t, src)
	site := findNode(t, doc, "example.test")
	e, err := p.Insert(site, DirectiveInsert{Name: "file_server", Position: InsertAtStart})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\tfile_server\n\t# keep this comment\n\tcustom_plugin thing\n\trespond ok\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
	// Every byte of the original still appears in the result.
	if !bytes.Contains(out, []byte("# keep this comment")) || !bytes.Contains(out, []byte("custom_plugin thing")) {
		t.Errorf("comments or unknown directives were lost: %q", out)
	}
}

func TestPlanDeleteDirective(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\trespond ok\n\tfile_server\n}\n")
	respond := findNode(t, doc, "respond")
	e, err := p.Delete(respond)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if e.Op != EditDelete || e.NewText != "" {
		t.Errorf("edit = %+v, want a deletion", e)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\tfile_server\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanDeleteBlock(t *testing.T) {
	doc, p := planDoc(t, "a.test {\n\trespond ok\n}\nb.test {\n\tfile_server\n}\n")
	siteB := findNode(t, doc, "b.test")
	e, err := p.Delete(siteB)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "a.test {\n\trespond ok\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanReorderSiblingDirectives(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\tfile_server\n\trespond ok\n\t# tail comment\n}\n")
	fs := findNode(t, doc, "file_server")
	respond := findNode(t, doc, "respond")
	e, err := p.Reorder(fs, respond)
	if err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	if e.Op != EditReorder {
		t.Errorf("edit op = %v, want reorder", e.Op)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\trespond ok\n\tfile_server\n\t# tail comment\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanReorderPreservesIntermediateBytes(t *testing.T) {
	src := "example.test {\n\tfile_server\n\t# keep between\n\tcustom_plugin thing\n\trespond ok\n}\n"
	doc, p := planDoc(t, src)
	fs := findNode(t, doc, "file_server")
	respond := findNode(t, doc, "respond")
	e, err := p.Reorder(fs, respond)
	if err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\t# keep between\n\tcustom_plugin thing\n\trespond ok\n\tfile_server\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanReorderSiblingBlocks(t *testing.T) {
	doc, p := planDoc(t, "a.test {\n\trespond a\n}\nb.test {\n\trespond b\n}\n")
	siteA := findNode(t, doc, "a.test")
	siteB := findNode(t, doc, "b.test")
	e, err := p.MoveAfter(siteA, siteB)
	if err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "b.test {\n\trespond b\n}\na.test {\n\trespond a\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanReorderNonSiblings(t *testing.T) {
	doc, p := planDoc(t, "a.test {\n\tfile_server\n}\nb.test {\n\trespond ok\n}\n")
	fs := findNode(t, doc, "file_server")
	respond := findNode(t, doc, "respond")
	if _, err := p.Reorder(fs, respond); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Reorder non-siblings err = %v, want ErrInvalidContext", err)
	}
}

func TestPlanReorderItself(t *testing.T) {
	doc, p := planDoc(t, "a.test {\n\trespond ok\n}\n")
	respond := findNode(t, doc, "respond")
	if _, err := p.Reorder(respond, respond); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Reorder itself err = %v, want ErrInvalidContext", err)
	}
}

func TestPlanSiblingNodesReturnsDirectSourceOrder(t *testing.T) {
	doc, p := planDoc(t, "a.test {\n\thandle /x {\n\t\trespond x\n\t}\n\trespond a\n}\nb.test {\n\trespond b\n}\n")
	siteA := findNode(t, doc, "a.test")
	siblings, err := p.SiblingNodes(siteA)
	if err != nil {
		t.Fatalf("SiblingNodes: %v", err)
	}
	if len(siblings) != 2 || siblings[0].Name != "a.test" || siblings[1].Name != "b.test" {
		t.Fatalf("top-level siblings = %#v, want a.test then b.test", siblings)
	}
	handle := findNode(t, doc, "handle")
	siblings, err = p.SiblingNodes(handle)
	if err != nil {
		t.Fatalf("nested SiblingNodes: %v", err)
	}
	if len(siblings) != 2 || siblings[0].Name != "handle" || siblings[1].Name != "respond" {
		t.Fatalf("nested siblings = %#v, want handle then respond", siblings)
	}
}

func TestPlanReorderKeepsGlobalOptionsFirst(t *testing.T) {
	doc, p := planDoc(t, "{\n\tdebug\n}\nexample.test {\n\trespond ok\n}\n")
	globals := findNode(t, doc, "")
	site := findNode(t, doc, "example.test")
	if _, err := p.Reorder(globals, site); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Reorder global options err = %v, want ErrInvalidContext", err)
	}

	doc, p = planDoc(t, "{\n\tdebug\n}\na.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n")
	globals = findNode(t, doc, "")
	site = findNode(t, doc, "b.example.test")
	e, err := p.MoveAfter(site, globals)
	if err != nil {
		t.Fatalf("MoveAfter site after global options: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "{\n\tdebug\n}\nb.example.test {\n\trespond b\n}\na.example.test {\n\trespond a\n}\n"
	if string(out) != want {
		t.Errorf("global-options move result = %q, want %q", out, want)
	}
}

func TestPlanRejectsParseErrorDocument(t *testing.T) {
	doc := Parse([]byte("example.test {\n\treverse_proxy localhost:8080\n"))
	if doc.Err == nil {
		t.Fatal("expected a parse error")
	}
	p := NewPlanner(doc)
	site := findNode(t, doc, "example.test")
	rp := findNode(t, doc, "reverse_proxy")
	checks := []struct {
		name string
		err  error
	}{
		{"SetArgs", func() error {
			_, err := p.SetArgs(rp, "x")
			return err
		}()},
		{"SetArg", func() error {
			_, err := p.SetArg(rp, 0, "x")
			return err
		}()},
		{"Insert", func() error {
			_, err := p.Insert(site, DirectiveInsert{Name: "respond", Args: "ok"})
			return err
		}()},
		{"Delete", func() error {
			_, err := p.Delete(rp)
			return err
		}()},
		{"Reorder", func() error {
			_, err := p.Reorder(rp, rp)
			return err
		}()},
	}
	for _, c := range checks {
		if c.err == nil {
			t.Errorf("%s on a parse-error document: expected error", c.name)
		} else if !errors.Is(c.err, ErrParseError) {
			t.Errorf("%s on a parse-error document: err = %v, want ErrParseError", c.name, c.err)
		}
	}
}

func TestPlanRejectsUnknownNode(t *testing.T) {
	_, p := planDoc(t, "example.test {\n\trespond ok\n}\n")
	foreign := Node{Kind: KindDirective, Name: "respond", Range: SourceRange{Start: 9999, End: 10000}}
	if _, err := p.SetArgs(foreign, "x"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("SetArgs on foreign node err = %v, want ErrNodeNotFound", err)
	}
}

func TestPlanApplyAll(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\trespond ok\n\tfile_server\n}\n")
	respond := findNode(t, doc, "respond")
	fs := findNode(t, doc, "file_server")
	e1, err := p.SetArgs(respond, "changed")
	if err != nil {
		t.Fatal(err)
	}
	e2, err := p.Delete(fs)
	if err != nil {
		t.Fatal(err)
	}
	out, err := (Plan{*e1, *e2}).ApplyAll(doc.Source)
	if err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	want := "example.test {\n\trespond changed\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanApplyAllRejectsOverlap(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\trespond ok\n}\n")
	respond := findNode(t, doc, "respond")
	e, err := p.SetArgs(respond, "x")
	if err != nil {
		t.Fatal(err)
	}
	overlap := PlannedEdit{Range: SourceRange{Start: e.Range.Start, End: e.Range.End}}
	if _, err := (Plan{*e, overlap}).ApplyAll(doc.Source); err == nil {
		t.Fatal("ApplyAll with overlapping edits: expected error")
	}
}

func TestPlanImportedDocumentIdentity(t *testing.T) {
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
	importNode := findNode(t, appDoc, "import")
	e, err := p.SetArgs(importNode, "fragments/base.caddy")
	if err != nil {
		t.Fatalf("SetArgs on imported document: %v", err)
	}
	if e.DocID != appDoc.Path {
		t.Errorf("edit DocID = %q, want %q", e.DocID, appDoc.Path)
	}
	out, err := e.Apply(appDoc.Source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "import fragments/base.caddy") {
		t.Errorf("patched imported document = %q, want the new import line", out)
	}
	if strings.Contains(string(out), "import ../fragments/upstream.caddy") {
		t.Errorf("patched imported document still contains the old import: %q", out)
	}
}

func TestPlanValueEditPreservesPlaceholders(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\treverse_proxy {env.BACKEND}:8080\n}\n")
	rp := findNode(t, doc, "reverse_proxy")
	e, err := p.SetArgs(rp, "{env.BACKEND}:9090")
	if err != nil {
		t.Fatalf("SetArgs: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\treverse_proxy {env.BACKEND}:9090\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestEditOpString(t *testing.T) {
	tests := []struct {
		op   EditOp
		want string
	}{
		{EditSetValue, "set-value"},
		{EditInsert, "insert"},
		{EditDelete, "delete"},
		{EditReorder, "reorder"},
		{EditOp(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.op.String(); got != tt.want {
			t.Errorf("EditOp(%d).String() = %q, want %q", tt.op, got, tt.want)
		}
	}
}

func TestPlanApplyAllPropagatesApplyError(t *testing.T) {
	// The first edit is in range, so ordering passes; the second edit is
	// out of bounds and must fail while being applied from the end.
	plan := Plan{
		PlannedEdit{Range: SourceRange{Start: 0, End: 5}, NewText: "x"},
		PlannedEdit{Range: SourceRange{Start: 100, End: 101}, NewText: "y"},
	}
	if _, err := plan.ApplyAll([]byte("hello world")); err == nil {
		t.Fatal("ApplyAll with an out-of-bounds edit must fail")
	}
}

func TestPlanNilDocument(t *testing.T) {
	p := NewPlanner(nil)
	checks := []struct {
		name string
		err  error
	}{
		{"SetArgs", func() error {
			_, err := p.SetArgs(Node{Kind: KindDirective, Name: "respond"}, "x")
			return err
		}()},
		{"SetArg", func() error {
			_, err := p.SetArg(Node{Kind: KindDirective, Name: "respond"}, 0, "x")
			return err
		}()},
		{"Insert", func() error {
			_, err := p.Insert(Node{Kind: KindSite, Name: "a.test"}, DirectiveInsert{Name: "respond"})
			return err
		}()},
		{"Delete", func() error {
			_, err := p.Delete(Node{Kind: KindDirective, Name: "respond"})
			return err
		}()},
		{"Reorder", func() error {
			_, err := p.Reorder(Node{Kind: KindDirective, Name: "a"}, Node{Kind: KindDirective, Name: "b"})
			return err
		}()},
		{"GetReverseProxyFields", func() error {
			_, err := p.GetReverseProxyFields(Node{Kind: KindDirective, Name: "reverse_proxy"})
			return err
		}()},
		{"SetReverseProxyFields", func() error {
			_, err := p.SetReverseProxyFields(Node{Kind: KindDirective, Name: "reverse_proxy"}, ReverseProxyFields{})
			return err
		}()},
	}
	for _, c := range checks {
		if c.err == nil || !strings.Contains(c.err.Error(), "nil document") {
			t.Errorf("%s on a nil planner document: err = %v, want a nil-document error", c.name, c.err)
		}
	}
}

// craftedDoc builds a parser-free document for guard-condition tests that
// need nodes the tolerant parser would never produce (un-lexable directive
// headers, overlapping top-level ranges).
func craftedDoc(src string, nodes ...Node) *Document {
	return &Document{Source: []byte(src), Nodes: nodes}
}

func TestPlanHeaderTokensRejectsUnlexableHeader(t *testing.T) {
	p := NewPlanner(craftedDoc("x\n\trespond \"oops\n"))
	n := Node{Kind: KindDirective, Name: "respond", Range: SourceRange{Start: 3, End: 16}}
	if _, _, err := p.headerTokens(n); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("headerTokens err = %v, want ErrUnsupported", err)
	}
}

func TestPlanHeaderTokensRejectsMissingName(t *testing.T) {
	p := NewPlanner(craftedDoc("{\n}\n"))
	n := Node{Kind: KindDirective, Name: "", Range: SourceRange{Start: 0, End: 1}}
	if _, _, err := p.headerTokens(n); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("headerTokens err = %v, want ErrInvalidContext", err)
	}
}

func TestPlanSetArgsSurvivesHeaderTokensError(t *testing.T) {
	n := Node{Kind: KindDirective, Name: "respond", Range: SourceRange{Start: 3, End: 16}}
	p := NewPlanner(craftedDoc("x\n\trespond \"oops\n", n))
	if _, err := p.SetArgs(n, "changed"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetArgs err = %v, want ErrUnsupported", err)
	}
}

func TestPlanSetArgSurvivesHeaderTokensError(t *testing.T) {
	n := Node{Kind: KindDirective, Name: "respond", Range: SourceRange{Start: 3, End: 16}}
	p := NewPlanner(craftedDoc("x\n\trespond \"oops\n", n))
	if _, err := p.SetArg(n, 0, "changed"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetArg err = %v, want ErrUnsupported", err)
	}
}

func TestPlanReverseProxyFieldsSurviveHeaderTokensError(t *testing.T) {
	n := Node{Kind: KindDirective, Name: "reverse_proxy", Range: SourceRange{Start: 3, End: 18}}
	p := NewPlanner(craftedDoc("x\n\treverse_proxy \"oops\n", n))
	if _, err := p.GetReverseProxyFields(n); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("GetReverseProxyFields err = %v, want ErrUnsupported", err)
	}
}

func TestPlanSetArgsTrimsTrailingWhitespaceOnBareDirective(t *testing.T) {
	// A directive with no arguments keeps its trailing whitespace: the
	// planned span collapses it so the new value lands before it.
	doc, p := planDoc(t, "example.test {\n\tfile_server  \n}\n")
	fs := findNode(t, doc, "file_server")
	e, err := p.SetArgs(fs, "/srv/www")
	if err != nil {
		t.Fatalf("SetArgs: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\tfile_server /srv/www  \n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanSetArgRejectsBlockNode(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\trespond ok\n}\n")
	site := findNode(t, doc, "example.test")
	if _, err := p.SetArg(site, 0, "x"); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("SetArg on a site err = %v, want ErrInvalidContext", err)
	}
}

func TestPlanSetArgReplacesBlockDirectiveArgument(t *testing.T) {
	// The argument index is computed inside the header, excluding the
	// nested block options.
	doc, p := planDoc(t, "example.test {\n\treverse_proxy localhost:8080 localhost:9090 {\n\t\theader_up Host {host}\n\t}\n}\n")
	rp := findNode(t, doc, "reverse_proxy")
	e, err := p.SetArg(rp, 1, "localhost:7070")
	if err != nil {
		t.Fatalf("SetArg: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\treverse_proxy localhost:8080 localhost:7070 {\n\t\theader_up Host {host}\n\t}\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanReverseProxyFieldsRejectForeignNode(t *testing.T) {
	_, p := planDoc(t, "example.test {\n\treverse_proxy localhost:8080\n}\n")
	foreign := Node{Kind: KindDirective, Name: "reverse_proxy", Range: SourceRange{Start: 999, End: 1000}}
	if _, err := p.GetReverseProxyFields(foreign); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("GetReverseProxyFields foreign err = %v, want ErrNodeNotFound", err)
	}
	if _, err := p.SetReverseProxyFields(foreign, ReverseProxyFields{Upstreams: []string{"localhost:8080"}}); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("SetReverseProxyFields foreign err = %v, want ErrNodeNotFound", err)
	}
}

func TestPlanParentCtxRejectsUnknownKind(t *testing.T) {
	p := NewPlanner(craftedDoc(""))
	if _, err := p.parentCtx(Node{Kind: Kind(99), Name: "x"}); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("parentCtx unknown kind err = %v, want ErrInvalidContext", err)
	}
	// insertionContext reports false for unknown kinds and plain leaf
	// directives.
	if ctx, ok := insertionContext(Node{Kind: Kind(99)}); ok || ctx != 0 {
		t.Errorf("insertionContext(unknown kind) = %d, %v, want 0, false", ctx, ok)
	}
	if _, ok := insertionContext(Node{Kind: KindDirective, Name: "respond"}); ok {
		t.Error("insertionContext(leaf directive) must be false")
	}
}

func TestInsertableDirectiveNamesForLeafParent(t *testing.T) {
	if got := InsertableDirectiveNames(Node{Kind: KindDirective, Name: "respond"}); got != nil {
		t.Fatalf("InsertableDirectiveNames(leaf) = %v, want nil", got)
	}
}

func TestPlanBlockGeometryGuardBranches(t *testing.T) {
	t.Run("unlexable block", func(t *testing.T) {
		p := NewPlanner(craftedDoc("x\n\trespond \"oops\n"))
		n := Node{Kind: KindSite, Name: "x", Range: SourceRange{Start: 0, End: 16}}
		if _, err := p.blockGeometry(n); !errors.Is(err, ErrInvalidContext) {
			t.Fatalf("blockGeometry err = %v, want ErrInvalidContext", err)
		}
	})
	t.Run("leaf directive cannot host insertions", func(t *testing.T) {
		doc, p := planDoc(t, "example.test {\n\trespond ok\n}\n")
		respond := findNode(t, doc, "respond")
		if _, err := p.blockGeometry(respond); !errors.Is(err, ErrInvalidContext) {
			t.Fatalf("blockGeometry err = %v, want ErrInvalidContext", err)
		}
	})
	t.Run("block without closing brace", func(t *testing.T) {
		p := NewPlanner(craftedDoc("route {\n"))
		n := Node{Kind: KindDirective, Name: "route", Range: SourceRange{Start: 0, End: 8}}
		if _, err := p.blockGeometry(n); !errors.Is(err, ErrInvalidContext) {
			t.Fatalf("blockGeometry err = %v, want ErrInvalidContext", err)
		}
	})
}

func TestPlanInsertHeaderLineCommentIsNotCompact(t *testing.T) {
	// A comment on the header line is not compact content: the block has a
	// safe interior point after the header line.
	doc, p := planDoc(t, "example.test { # keep\n\trespond ok\n}\n")
	site := findNode(t, doc, "example.test")
	e, err := p.Insert(site, DirectiveInsert{Name: "file_server", Position: InsertAtStart})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test { # keep\n\tfile_server\n\trespond ok\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanInsertPointGuardBranches(t *testing.T) {
	geo := blockGeometry{interiorStart: 5, interiorEnd: 20, childIndent: "\t", braceLineIndent: ""}
	p := NewPlanner(craftedDoc("x"))
	t.Run("last child extends past the closing brace", func(t *testing.T) {
		parent := Node{Kind: KindSite, Name: "s", Children: []Node{
			{Kind: KindDirective, Name: "a", Range: SourceRange{Start: 0, End: 50}},
		}}
		if _, err := p.insertPoint(parent, InsertAtEnd, Node{}, geo); !errors.Is(err, ErrAmbiguous) {
			t.Fatalf("insertPoint err = %v, want ErrAmbiguous", err)
		}
	})
	t.Run("anchor extends past the closing brace", func(t *testing.T) {
		anchor := Node{Kind: KindDirective, Name: "a", Range: SourceRange{Start: 0, End: 50}}
		parent := Node{Kind: KindSite, Name: "s", Children: []Node{anchor}}
		if _, err := p.insertPoint(parent, InsertAfter, anchor, geo); !errors.Is(err, ErrAmbiguous) {
			t.Fatalf("insertPoint err = %v, want ErrAmbiguous", err)
		}
	})
	t.Run("unknown position", func(t *testing.T) {
		if _, err := p.insertPoint(Node{}, InsertPosition(99), Node{}, geo); !errors.Is(err, ErrAmbiguous) {
			t.Fatalf("insertPoint err = %v, want ErrAmbiguous", err)
		}
	})
}

func TestPlanReorderNestedSiblings(t *testing.T) {
	// Reordering two directives deep inside a handler block exercises the
	// common-parent search across nested subtrees.
	doc, p := planDoc(t, "a.test {\n\thandle /x {\n\t\trespond a\n\t\tfile_server\n\t}\n\tencode gzip\n}\nb.test {\n\trespond b\n}\n")
	respond := findNode(t, doc, "respond")
	fs := findNode(t, doc, "file_server")
	e, err := p.MoveAfter(respond, fs)
	if err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "a.test {\n\thandle /x {\n\t\tfile_server\n\t\trespond a\n\t}\n\tencode gzip\n}\nb.test {\n\trespond b\n}\n"
	if string(out) != want {
		t.Errorf("result = %q, want %q", out, want)
	}
}

func TestPlanReorderForeignNode(t *testing.T) {
	doc, p := planDoc(t, "a.test {\n\trespond a\n}\n")
	respond := findNode(t, doc, "respond")
	foreign := Node{Kind: KindDirective, Name: "respond", Range: SourceRange{Start: 999, End: 1000}}
	if _, err := p.Reorder(respond, foreign); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("Reorder foreign err = %v, want ErrNodeNotFound", err)
	}
}

func TestPlanReorderReversedArguments(t *testing.T) {
	// A node that is already immediately after the target is a no-op.
	doc, p := planDoc(t, "example.test {\n\tfile_server\n\trespond ok\n}\n")
	fs := findNode(t, doc, "file_server")
	respond := findNode(t, doc, "respond")
	if _, err := p.MoveAfter(respond, fs); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("MoveAfter already-after err = %v, want ErrInvalidContext", err)
	}
}

func TestPlanMoveAfterRejectsBackwardCommentCrossing(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\ta\n\t# keep with b\n\tb\n\tc\n}\n")
	a := findNode(t, doc, "a")
	c := findNode(t, doc, "c")
	if _, err := p.MoveAfter(c, a); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("MoveAfter across comment err = %v, want ErrInvalidContext", err)
	}
}

func TestPlanMoveAfterRejectsBackwardLeafCrossing(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\thandle /a {\n\t\trespond a\n\t}\n\trespond ok\n\thandle /b {\n\t\trespond b\n\t}\n}\n")
	handles := findNodes(t, doc, "handle")
	if _, err := p.MoveAfter(handles[1], handles[0]); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("MoveAfter across leaf err = %v, want ErrInvalidContext", err)
	}
}

func TestPlanMoveAfterBackwardAcrossStructuralBlock(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\thandle /a {\n\t\trespond a\n\t}\n\thandle /b {\n\t\trespond b\n\t}\n\thandle /c {\n\t\trespond c\n\t}\n}\n")
	handles := findNodes(t, doc, "handle")
	e, err := p.MoveAfter(handles[2], handles[0])
	if err != nil {
		t.Fatalf("MoveAfter handle /c after handle /a: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\thandle /a {\n\t\trespond a\n\t}\n\thandle /c {\n\t\trespond c\n\t}\n\thandle /b {\n\t\trespond b\n\t}\n}\n"
	if string(out) != want {
		t.Errorf("backward structural move result = %q, want %q", out, want)
	}
}

func TestPlanMoveAfterBackwardAcrossBlankLines(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\thandle /a {\n\t\trespond a\n\t}\n\n\thandle /b {\n\t\trespond b\n\t}\n\n\thandle /c {\n\t\trespond c\n\t}\n}\n")
	handles := findNodes(t, doc, "handle")
	e, err := p.MoveAfter(handles[2], handles[0])
	if err != nil {
		t.Fatalf("MoveAfter across blank lines: %v", err)
	}
	out := applyPlanned(t, doc, e)
	want := "example.test {\n\thandle /a {\n\t\trespond a\n\t}\n\thandle /c {\n\t\trespond c\n\t}\n\n\thandle /b {\n\t\trespond b\n\t}\n\n}\n"
	if string(out) != want {
		t.Errorf("blank-line move result = %q, want %q", out, want)
	}
}

func TestPlanMoveAfterRecordsNewStartLine(t *testing.T) {
	// Forward move: the source lands immediately after the target's block.
	doc, p := planDoc(t, "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n")
	a := findNode(t, doc, "a.example.test")
	b := findNode(t, doc, "b.example.test")
	e, err := p.MoveAfter(a, b)
	if err != nil {
		t.Fatalf("MoveAfter forward: %v", err)
	}
	if e.NewStartLine != 4 {
		t.Errorf("forward NewStartLine = %d, want 4 (moved a.example.test starts on line 4)", e.NewStartLine)
	}

	// Backward move across a structural sibling.
	doc, p = planDoc(t, "example.test {\n\thandle /a {\n\t\trespond a\n\t}\n\thandle /b {\n\t\trespond b\n\t}\n\thandle /c {\n\t\trespond c\n\t}\n}\n")
	handles := findNodes(t, doc, "handle")
	e, err = p.MoveAfter(handles[2], handles[0])
	if err != nil {
		t.Fatalf("MoveAfter backward: %v", err)
	}
	if e.NewStartLine != 5 {
		t.Errorf("backward NewStartLine = %d, want 5 (moved handle /c starts on line 5)", e.NewStartLine)
	}

	// Non-reorder operations leave the field at its zero value.
	edit, err := p.SetArgs(handles[0], "")
	if err != nil {
		t.Fatalf("SetArgs: %v", err)
	}
	if edit.NewStartLine != 0 {
		t.Errorf("SetArgs NewStartLine = %d, want 0", edit.NewStartLine)
	}
}

func TestPlanReorderOverlappingRanges(t *testing.T) {
	// Overlapping top-level ranges can never be moved safely.
	a := Node{Kind: KindSite, Name: "a.test", Range: SourceRange{Start: 0, End: 10}}
	b := Node{Kind: KindSite, Name: "b.test", Range: SourceRange{Start: 5, End: 7}}
	p := NewPlanner(craftedDoc("a.test {\n}\nb.test {\n}\n", a, b))
	if _, err := p.Reorder(a, b); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Reorder overlapping err = %v, want ErrInvalidContext", err)
	}
}
