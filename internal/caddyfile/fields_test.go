package caddyfile

import (
	"errors"
	"strings"
	"testing"
)

// TestFormFields_GetAndSet covers the round trip of every structured form
// field model: Get reads the documented positional grammar, Set plans a
// byte-exact replacement that preserves the matcher, trailing comments,
// nested blocks and every byte outside the argument span.
func TestFormFields_GetAndSet(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		directive string
		get       func(t *testing.T, p *Planner, n Node) any
		set       func(t *testing.T, p *Planner, n Node) (*PlannedEdit, error)
		wantSrc   string
	}{
		{
			name:      "respond status",
			src:       "example.test {\n\trespond 200 # status only\n}\n",
			directive: "respond",
			get: func(t *testing.T, p *Planner, n Node) any {
				f, err := p.GetRespondFields(n)
				if err != nil {
					t.Fatalf("GetRespondFields: %v", err)
				}
				if f.Matcher != "" || f.Status != "200" || f.Body != "" {
					t.Fatalf("fields = %+v, want status 200", f)
				}
				return f
			},
			set: func(t *testing.T, p *Planner, n Node) (*PlannedEdit, error) {
				return p.SetRespondFields(n, RespondFields{Status: "204"})
			},
			wantSrc: "example.test {\n\trespond 204 # status only\n}\n",
		},
		{
			name:      "respond matcher body status",
			src:       "example.test {\n\trespond /health \"ok\" 200\n}\n",
			directive: "respond",
			get: func(t *testing.T, p *Planner, n Node) any {
				f, err := p.GetRespondFields(n)
				if err != nil {
					t.Fatalf("GetRespondFields: %v", err)
				}
				if f.Matcher != "/health" || f.Body != "\"ok\"" || f.Status != "200" {
					t.Fatalf("fields = %+v, want matcher+body+status", f)
				}
				return f
			},
			set: func(t *testing.T, p *Planner, n Node) (*PlannedEdit, error) {
				return p.SetRespondFields(n, RespondFields{Matcher: "/health", Body: "\"pong\"", Status: "201"})
			},
			wantSrc: "example.test {\n\trespond /health \"pong\" 201\n}\n",
		},
		{
			name:      "redir matcher to status",
			src:       "example.test {\n\tredir /old /new permanent\n}\n",
			directive: "redir",
			get: func(t *testing.T, p *Planner, n Node) any {
				f, err := p.GetRedirFields(n)
				if err != nil {
					t.Fatalf("GetRedirFields: %v", err)
				}
				if f.Matcher != "/old" || f.To != "/new" || f.Status != "permanent" {
					t.Fatalf("fields = %+v", f)
				}
				return f
			},
			set: func(t *testing.T, p *Planner, n Node) (*PlannedEdit, error) {
				return p.SetRedirFields(n, RedirFields{Matcher: "/old", To: "/new", Status: "308"})
			},
			wantSrc: "example.test {\n\tredir /old /new 308\n}\n",
		},
		{
			name:      "file_server browse",
			src:       "example.test {\n\tfile_server /static/* browse\n}\n",
			directive: "file_server",
			get: func(t *testing.T, p *Planner, n Node) any {
				f, err := p.GetFileServerFields(n)
				if err != nil {
					t.Fatalf("GetFileServerFields: %v", err)
				}
				if f.Matcher != "/static/*" || !f.Browse {
					t.Fatalf("fields = %+v, want matcher + browse", f)
				}
				return f
			},
			set: func(t *testing.T, p *Planner, n Node) (*PlannedEdit, error) {
				return p.SetFileServerFields(n, FileServerFields{Matcher: "/static/*"})
			},
			wantSrc: "example.test {\n\tfile_server /static/*\n}\n",
		},
		{
			name:      "php_fastcgi unix socket",
			src:       "example.test {\n\tphp_fastcgi unix//run/php/php8.2-fpm.sock\n}\n",
			directive: "php_fastcgi",
			get: func(t *testing.T, p *Planner, n Node) any {
				f, err := p.GetPhpFastcgiFields(n)
				if err != nil {
					t.Fatalf("GetPhpFastcgiFields: %v", err)
				}
				if len(f.Upstreams) != 1 || f.Upstreams[0] != "unix//run/php/php8.2-fpm.sock" {
					t.Fatalf("fields = %+v", f)
				}
				return f
			},
			set: func(t *testing.T, p *Planner, n Node) (*PlannedEdit, error) {
				return p.SetPhpFastcgiFields(n, PhpFastcgiFields{Upstreams: []string{"unix//run/php/php8.3-fpm.sock"}})
			},
			wantSrc: "example.test {\n\tphp_fastcgi unix//run/php/php8.3-fpm.sock\n}\n",
		},
		{
			name:      "encode formats",
			src:       "example.test {\n\tencode zstd gzip\n}\n",
			directive: "encode",
			get: func(t *testing.T, p *Planner, n Node) any {
				f, err := p.GetEncodeFields(n)
				if err != nil {
					t.Fatalf("GetEncodeFields: %v", err)
				}
				if len(f.Formats) != 2 || f.Formats[0] != "zstd" || f.Formats[1] != "gzip" {
					t.Fatalf("fields = %+v", f)
				}
				return f
			},
			set: func(t *testing.T, p *Planner, n Node) (*PlannedEdit, error) {
				return p.SetEncodeFields(n, EncodeFields{Formats: []string{"gzip"}})
			},
			wantSrc: "example.test {\n\tencode gzip\n}\n",
		},
		{
			name:      "header field value replace",
			src:       "example.test {\n\theader Location http:// https://\n}\n",
			directive: "header",
			get: func(t *testing.T, p *Planner, n Node) any {
				f, err := p.GetHeaderFields(n)
				if err != nil {
					t.Fatalf("GetHeaderFields: %v", err)
				}
				if f.Field != "Location" || f.Value != "http://" || f.Replace != "https://" {
					t.Fatalf("fields = %+v", f)
				}
				return f
			},
			set: func(t *testing.T, p *Planner, n Node) (*PlannedEdit, error) {
				return p.SetHeaderFields(n, HeaderFields{Matcher: "/api/*", Field: "-X-Test"})
			},
			wantSrc: "example.test {\n\theader /api/* -X-Test\n}\n",
		},
		{
			name:      "tls cert key pair",
			src:       "example.test {\n\ttls cert.pem key.pem\n}\n",
			directive: "tls",
			get: func(t *testing.T, p *Planner, n Node) any {
				f, err := p.GetTlsFields(n)
				if err != nil {
					t.Fatalf("GetTlsFields: %v", err)
				}
				if f.Email != "" || f.CertFile != "cert.pem" || f.KeyFile != "key.pem" {
					t.Fatalf("fields = %+v", f)
				}
				return f
			},
			set: func(t *testing.T, p *Planner, n Node) (*PlannedEdit, error) {
				return p.SetTlsFields(n, TlsFields{Email: "internal"})
			},
			wantSrc: "example.test {\n\ttls internal\n}\n",
		},
		{
			name:      "log name with block preserved",
			src:       "example.test {\n\tlog access {\n\t\toutput file /var/log/access.log\n\t}\n}\n",
			directive: "log",
			get: func(t *testing.T, p *Planner, n Node) any {
				f, err := p.GetLogFields(n)
				if err != nil {
					t.Fatalf("GetLogFields: %v", err)
				}
				if f.Name != "access" {
					t.Fatalf("fields = %+v", f)
				}
				return f
			},
			set: func(t *testing.T, p *Planner, n Node) (*PlannedEdit, error) {
				return p.SetLogFields(n, LogFields{Name: "audit"})
			},
			wantSrc: "example.test {\n\tlog audit {\n\t\toutput file /var/log/access.log\n\t}\n}\n",
		},
		{
			name:      "import args and block",
			src:       "example.test {\n\timport proxy /api 10.0.0.1 {\n\t\trespond ok\n\t}\n}\n",
			directive: "import",
			get: func(t *testing.T, p *Planner, n Node) any {
				f, err := p.GetImportFields(n)
				if err != nil {
					t.Fatalf("GetImportFields: %v", err)
				}
				if f.Pattern != "proxy" || len(f.Args) != 2 || f.Args[0] != "/api" || f.Args[1] != "10.0.0.1" {
					t.Fatalf("fields = %+v", f)
				}
				return f
			},
			set: func(t *testing.T, p *Planner, n Node) (*PlannedEdit, error) {
				return p.SetImportFields(n, ImportFields{Pattern: "proxy", Args: []string{"/api", "10.0.0.2"}})
			},
			wantSrc: "example.test {\n\timport proxy /api 10.0.0.2 {\n\t\trespond ok\n\t}\n}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, p := planDoc(t, tt.src)
			node := findNode(t, doc, tt.directive)
			tt.get(t, p, node)
			edit, err := tt.set(t, p, node)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := edit.Apply(doc.Source)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if string(got) != tt.wantSrc {
				t.Fatalf("patched = %q, want %q", got, tt.wantSrc)
			}
		})
	}
}

// TestFormFields_SetPatchesExactBytes verifies Set plans a single byte-range
// edit whose application preserves every byte outside the argument span:
// indentation, the trailing comment, the nested block and the closing
// brace. It re-plans one edit per directive and applies it.
func TestFormFields_SetPatchesExactBytes(t *testing.T) {
	respondDoc, respondP := planDoc(t, "example.test {\n\trespond ok # keep\n}\n")
	respond := findNode(t, respondDoc, "respond")
	edit, err := respondP.SetRespondFields(respond, RespondFields{Status: "202"})
	if err != nil {
		t.Fatalf("SetRespondFields: %v", err)
	}
	if edit.DocID != "" || edit.Op != EditSetValue {
		t.Fatalf("edit = %+v, want root document set-value", edit)
	}
	got, err := edit.Apply(respondDoc.Source)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := "example.test {\n\trespond 202 # keep\n}\n"
	if string(got) != want {
		t.Fatalf("patched = %q, want %q", got, want)
	}

	// The tls block survives an argument replacement.
	tlsDoc, tlsP := planDoc(t, "example.test {\n\ttls {\n\t\tprotocols tls1.2\n\t}\n}\n")
	tls := findNode(t, tlsDoc, "tls")
	edit, err = tlsP.SetTlsFields(tls, TlsFields{Email: "admin@example.test"})
	if err != nil {
		t.Fatalf("SetTlsFields: %v", err)
	}
	got, err = edit.Apply(tlsDoc.Source)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want = "example.test {\n\ttls admin@example.test {\n\t\tprotocols tls1.2\n\t}\n}\n"
	if string(got) != want {
		t.Fatalf("patched = %q, want %q", got, want)
	}
}

// TestFormFields_GetRefusesAmbiguous verifies that constructs the form
// cannot represent without guessing are rejected with ErrAmbiguous, so the
// UI disables the form and the raw editor remains the only path.
func TestFormFields_GetRefusesAmbiguous(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		directive string
		get       func(*Planner, Node) error
	}{
		{
			name:      "respond status then body",
			src:       "example.test {\n\trespond 200 \"ok\"\n}\n",
			directive: "respond",
			get:       func(p *Planner, n Node) error { _, err := p.GetRespondFields(n); return err },
		},
		{
			name:      "respond too many args",
			src:       "example.test {\n\trespond a b c d\n}\n",
			directive: "respond",
			get:       func(p *Planner, n Node) error { _, err := p.GetRespondFields(n); return err },
		},
		{
			name:      "redir too many args",
			src:       "example.test {\n\tredir /a /b 308 extra\n}\n",
			directive: "redir",
			get:       func(p *Planner, n Node) error { _, err := p.GetRedirFields(n); return err },
		},
		{
			name:      "file_server unknown mode",
			src:       "example.test {\n\tfile_server browse custom.html\n}\n",
			directive: "file_server",
			get:       func(p *Planner, n Node) error { _, err := p.GetFileServerFields(n); return err },
		},
		{
			name:      "header too many tokens",
			src:       "example.test {\n\theader X a b c\n}\n",
			directive: "header",
			get:       func(p *Planner, n Node) error { _, err := p.GetHeaderFields(n); return err },
		},
		{
			name:      "tls too many args",
			src:       "example.test {\n\ttls a b c d\n}\n",
			directive: "tls",
			get:       func(p *Planner, n Node) error { _, err := p.GetTlsFields(n); return err },
		},
		{
			name:      "log multiple names",
			src:       "example.test {\n\tlog one two\n}\n",
			directive: "log",
			get:       func(p *Planner, n Node) error { _, err := p.GetLogFields(n); return err },
		},
		{
			name:      "import without pattern",
			src:       "example.test {\n\timport\n}\n",
			directive: "import",
			get:       func(p *Planner, n Node) error { _, err := p.GetImportFields(n); return err },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, p := planDoc(t, tt.src)
			node := findNode(t, doc, tt.directive)
			err := tt.get(p, node)
			if !errors.Is(err, ErrAmbiguous) {
				t.Fatalf("err = %v, want ErrAmbiguous", err)
			}
		})
	}
}

// TestFormFields_GetRejectsWrongDirective verifies each Get refuses a node
// of a different directive with ErrUnsupported.
func TestFormFields_GetRejectsWrongDirective(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\trespond ok\n}\n")
	respond := findNode(t, doc, "respond")
	gets := []func(Node) error{
		func(n Node) error { _, err := p.GetFileServerFields(n); return err },
		func(n Node) error { _, err := p.GetPhpFastcgiFields(n); return err },
		func(n Node) error { _, err := p.GetEncodeFields(n); return err },
		func(n Node) error { _, err := p.GetHeaderFields(n); return err },
		func(n Node) error { _, err := p.GetRedirFields(n); return err },
		func(n Node) error { _, err := p.GetTlsFields(n); return err },
		func(n Node) error { _, err := p.GetImportFields(n); return err },
		func(n Node) error { _, err := p.GetLogFields(n); return err },
	}
	for _, get := range gets {
		if err := get(respond); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("err = %v, want ErrUnsupported", err)
		}
	}
}

// TestFormFields_SetRejectsInvalidValues verifies each Set refuses values
// that would produce an invalid or ambiguous directive, before any edit is
// planned.
func TestFormFields_SetRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		directive string
		set       func(p *Planner, n Node) error
	}{
		{
			name:      "respond without status or body",
			src:       "example.test {\n\trespond 200\n}\n",
			directive: "respond",
			set:       func(p *Planner, n Node) error { _, err := p.SetRespondFields(n, RespondFields{}); return err },
		},
		{
			name:      "redir without destination",
			src:       "example.test {\n\tredir /a /b\n}\n",
			directive: "redir",
			set:       func(p *Planner, n Node) error { _, err := p.SetRedirFields(n, RedirFields{Matcher: "/a"}); return err },
		},
		{
			name:      "php_fastcgi without gateway",
			src:       "example.test {\n\tphp_fastcgi localhost:9000\n}\n",
			directive: "php_fastcgi",
			set:       func(p *Planner, n Node) error { _, err := p.SetPhpFastcgiFields(n, PhpFastcgiFields{}); return err },
		},
		{
			name:      "header value without field",
			src:       "example.test {\n\theader X-Value 1\n}\n",
			directive: "header",
			set:       func(p *Planner, n Node) error { _, err := p.SetHeaderFields(n, HeaderFields{Value: "1"}); return err },
		},
		{
			name:      "tls certificate without key",
			src:       "example.test {\n\ttls cert.pem key.pem\n}\n",
			directive: "tls",
			set: func(p *Planner, n Node) error {
				_, err := p.SetTlsFields(n, TlsFields{CertFile: "cert.pem"})
				return err
			},
		},
		{
			name:      "import without pattern",
			src:       "example.test {\n\timport fragments/*.caddy\n}\n",
			directive: "import",
			set:       func(p *Planner, n Node) error { _, err := p.SetImportFields(n, ImportFields{}); return err },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, p := planDoc(t, tt.src)
			node := findNode(t, doc, tt.directive)
			if err := tt.set(p, node); err == nil {
				t.Fatal("Set accepted invalid values")
			}
			// The source is untouched: no edit was planned.
			if !strings.Contains(string(doc.Source), tt.directive) {
				t.Fatalf("source changed unexpectedly: %q", doc.Source)
			}
		})
	}
}

// TestFormFields_MatcherToken verifies the inline-matcher convention shared
// by the forms: named, path and wildcard matchers are recognized only in
// the leading position.
func TestFormFields_MatcherToken(t *testing.T) {
	for _, tok := range []string{"@api", "/api/*", "*"} {
		if !matcherToken(tok) {
			t.Errorf("matcherToken(%q) = false, want true", tok)
		}
	}
	for _, tok := range []string{"localhost:8080", "\"/api/*\"", "200", "unix//run/x.sock", "https://example.com"} {
		if matcherToken(tok) {
			t.Errorf("matcherToken(%q) = true, want false", tok)
		}
	}
}

// TestSplitFieldTokens verifies the UI-side token splitter keeps quoted
// tokens and placeholders intact.
func TestSplitFieldTokens(t *testing.T) {
	got := SplitFieldTokens(`localhost:8080 "app one:8080" {env.UPSTREAM}`)
	want := []string{"localhost:8080", "\"app one:8080\"", "{env.UPSTREAM}"}
	if len(got) != len(want) {
		t.Fatalf("tokens = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokens = %q, want %q", got, want)
		}
	}
	if got := SplitFieldTokens(""); len(got) != 0 {
		t.Fatalf("empty tokens = %q, want none", got)
	}
}

// TestFormFields_EdgeShapes covers the remaining positional shapes of each
// grammar: bare directives, single-token variants, matcher preservation in
// Set and the documented three-argument tls form.
func TestFormFields_EdgeShapes(t *testing.T) {
	doc, p := planDoc(t, `example.test {
	respond
	redir
	file_server
	header
	tls
	log
	encode
	php_fastcgi
	import snippets/common
	respond /x "hello"
	file_server browse
	tls internal
	tls admin@example.test cert.pem key.pem
	header -X-Remove
	log named
	encode /api/* zstd
	php_fastcgi /php/* localhost:9000
	header X-Field value
	header X-Field value replacement
}
`)

	respond := findNode(t, doc, "respond")
	// The first respond node is bare.
	f, err := p.GetRespondFields(respond)
	if err != nil {
		t.Fatalf("bare GetRespondFields: %v", err)
	}
	if f.Matcher != "" || f.Status != "" || f.Body != "" {
		t.Fatalf("bare respond fields = %+v, want all empty", f)
	}

	redir := findNode(t, doc, "redir")
	rf, err := p.GetRedirFields(redir)
	if err != nil {
		t.Fatalf("bare GetRedirFields: %v", err)
	}
	if rf.To != "" || rf.Status != "" {
		t.Fatalf("bare redir fields = %+v, want empty", rf)
	}

	fileServer := findNode(t, doc, "file_server")
	ff, err := p.GetFileServerFields(fileServer)
	if err != nil {
		t.Fatalf("bare GetFileServerFields: %v", err)
	}
	if ff.Browse {
		t.Fatal("bare file_server unexpectedly has browse")
	}
	// file_server browse sets the mode; a browse removal clears it.
	browseNode := findNodes(t, doc, "file_server")[1]
	bf, err := p.GetFileServerFields(browseNode)
	if err != nil || !bf.Browse {
		t.Fatalf("browse file_server fields = %+v err=%v, want browse", bf, err)
	}
	if _, err := p.SetFileServerFields(browseNode, FileServerFields{Browse: true}); err != nil {
		t.Fatalf("SetFileServerFields browse: %v", err)
	}

	// header -X-Remove is a single-token field.
	hc := findNodes(t, doc, "header")[1]
	hf, err := p.GetHeaderFields(hc)
	if err != nil || hf.Field != "-X-Remove" || hf.Value != "" {
		t.Fatalf("single-token header fields = %+v err=%v", hf, err)
	}

	tc := findNodes(t, doc, "tls")[1]
	tf, err := p.GetTlsFields(tc)
	if err != nil || tf.Email != "internal" {
		t.Fatalf("tls internal fields = %+v err=%v", tf, err)
	}
	three := findNodes(t, doc, "tls")[2]
	t3, err := p.GetTlsFields(three)
	if err != nil || t3.Email != "admin@example.test" || t3.CertFile != "cert.pem" || t3.KeyFile != "key.pem" {
		t.Fatalf("three-token tls fields = %+v err=%v", t3, err)
	}

	logNode := findNodes(t, doc, "log")[1]
	lf, err := p.GetLogFields(logNode)
	if err != nil || lf.Name != "named" {
		t.Fatalf("named log fields = %+v err=%v", lf, err)
	}

	encodeNode := findNodes(t, doc, "encode")[1]
	ef, err := p.GetEncodeFields(encodeNode)
	if err != nil || ef.Matcher != "/api/*" || len(ef.Formats) != 1 || ef.Formats[0] != "zstd" {
		t.Fatalf("matcher encode fields = %+v err=%v", ef, err)
	}
	if _, err := p.SetEncodeFields(encodeNode, EncodeFields{Matcher: "/api/*", Formats: []string{"gzip"}}); err != nil {
		t.Fatalf("SetEncodeFields with matcher: %v", err)
	}

	php := findNodes(t, doc, "php_fastcgi")[1]
	pf, err := p.GetPhpFastcgiFields(php)
	if err != nil || pf.Matcher != "/php/*" || len(pf.Upstreams) != 1 {
		t.Fatalf("matcher php_fastcgi fields = %+v err=%v", pf, err)
	}
	if _, err := p.SetPhpFastcgiFields(php, PhpFastcgiFields{Matcher: "/php/*", Upstreams: []string{"localhost:9001"}}); err != nil {
		t.Fatalf("SetPhpFastcgiFields with matcher: %v", err)
	}

	importNode := findNode(t, doc, "import")
	imp, err := p.GetImportFields(importNode)
	if err != nil || imp.Pattern != "snippets/common" || len(imp.Args) != 0 {
		t.Fatalf("pattern-only import fields = %+v err=%v", imp, err)
	}

	// A body followed by a status is the documented respond two-argument
	// form.
	bodyStatus := findNodes(t, doc, "respond")[1]
	bs, err := p.GetRespondFields(bodyStatus)
	if err != nil || bs.Matcher != "/x" || bs.Body != "\"hello\"" || bs.Status != "" {
		t.Fatalf("body-only respond fields = %+v err=%v", bs, err)
	}

	// A single-token header with a value; a three-token header with a
	// replacement.
	hv := findNodes(t, doc, "header")[2]
	hvFields, err := p.GetHeaderFields(hv)
	if err != nil || hvFields.Field != "X-Field" || hvFields.Value != "value" || hvFields.Replace != "" {
		t.Fatalf("two-token header fields = %+v err=%v", hvFields, err)
	}
	hr := findNodes(t, doc, "header")[3]
	hrFields, err := p.GetHeaderFields(hr)
	if err != nil || hrFields.Replace != "replacement" {
		t.Fatalf("three-token header fields = %+v err=%v", hrFields, err)
	}
}

// TestFormFields_PlannerErrors verifies the shared helpers report their
// error paths: a stale node, a non-directive node and an unlexable header.
func TestFormFields_PlannerErrors(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\trespond ok\n}\n")
	respond := findNode(t, doc, "respond")
	// A stale identity is rejected by every Get before any edit is planned.
	stale := respond
	stale.Range.Start = 99999
	if _, err := p.GetRespondFields(stale); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("stale Get err = %v, want ErrNodeNotFound", err)
	}
	// A site node is not a directive.
	site := findNode(t, doc, "example.test")
	if _, err := p.GetLogFields(site); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("site Get err = %v, want ErrUnsupported", err)
	}
}

// TestSplitFieldTokens_UnlexableInput verifies the fallback keeps the raw
// text when the field cannot be lexed as a token stream.
func TestSplitFieldTokens_UnlexableInput(t *testing.T) {
	got := SplitFieldTokens(`"unclosed`)
	if len(got) != 1 || got[0] != `"unclosed` {
		t.Fatalf("tokens = %q, want the raw fallback", got)
	}
	// Structural braces are never treated as tokens of a field.
	if got := SplitFieldTokens("a { b"); len(got) != 2 {
		t.Fatalf("tokens = %q, want 2 tokens", got)
	}
}

// TestFormFields_SetsRejectWrongDirective verifies every Set refuses a node
// of a different directive with ErrUnsupported before planning an edit.
func TestFormFields_SetsRejectWrongDirective(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\trespond ok\n}\n")
	// A site node is never a directive, so every Set must refuse it.
	site := findNode(t, doc, "example.test")
	sets := []func(Node) error{
		func(n Node) error {
			_, err := p.SetReverseProxyFields(n, ReverseProxyFields{Upstreams: []string{"localhost:8080"}})
			return err
		},
		func(n Node) error { _, err := p.SetRespondFields(n, RespondFields{Status: "200"}); return err },
		func(n Node) error { _, err := p.SetRedirFields(n, RedirFields{To: "/x"}); return err },
		func(n Node) error { _, err := p.SetFileServerFields(n, FileServerFields{}); return err },
		func(n Node) error {
			_, err := p.SetPhpFastcgiFields(n, PhpFastcgiFields{Upstreams: []string{"x"}})
			return err
		},
		func(n Node) error { _, err := p.SetEncodeFields(n, EncodeFields{}); return err },
		func(n Node) error { _, err := p.SetHeaderFields(n, HeaderFields{}); return err },
		func(n Node) error { _, err := p.SetTlsFields(n, TlsFields{}); return err },
		func(n Node) error { _, err := p.SetLogFields(n, LogFields{}); return err },
		func(n Node) error { _, err := p.SetImportFields(n, ImportFields{Pattern: "x"}); return err },
	}
	for _, set := range sets {
		if err := set(site); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("Set err = %v, want ErrUnsupported", err)
		}
	}
}

// TestFormFields_GetSingleTokenShapes covers the remaining single-token
// shapes of the grammars: a redir destination without a matcher, a bare
// header, a bare tls and a file_server argument that is not browse.
func TestFormFields_GetSingleTokenShapes(t *testing.T) {
	doc, p := planDoc(t, "example.test {\n\tredir https://example.com\n\theader\n\ttls\n\tfile_server exotic\n}\n")

	redir := findNode(t, doc, "redir")
	rf, err := p.GetRedirFields(redir)
	if err != nil || rf.To != "https://example.com" || rf.Status != "" || rf.Matcher != "" {
		t.Fatalf("single-token redir = %+v err=%v", rf, err)
	}

	header := findNode(t, doc, "header")
	hf, err := p.GetHeaderFields(header)
	if err != nil || hf.Field != "" || hf.Value != "" {
		t.Fatalf("bare header = %+v err=%v", hf, err)
	}

	tls := findNode(t, doc, "tls")
	tf, err := p.GetTlsFields(tls)
	if err != nil || tf.Email != "" || tf.CertFile != "" || tf.KeyFile != "" {
		t.Fatalf("bare tls = %+v err=%v", tf, err)
	}

	fileServer := findNode(t, doc, "file_server")
	if _, err := p.GetFileServerFields(fileServer); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("exotic file_server err = %v, want ErrAmbiguous", err)
	}
}
