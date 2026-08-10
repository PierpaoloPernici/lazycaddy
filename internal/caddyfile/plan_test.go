package caddyfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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

func TestPlanReorderSiblingBlocks(t *testing.T) {
	doc, p := planDoc(t, "a.test {\n\trespond a\n}\nb.test {\n\trespond b\n}\n")
	siteA := findNode(t, doc, "a.test")
	siteB := findNode(t, doc, "b.test")
	e, err := p.Reorder(siteA, siteB)
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
