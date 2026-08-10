package caddyfile

import (
	"strings"
	"testing"
)

// rolesIn returns the roles covering the given byte span, in emission order.
func rolesIn(t *testing.T, src string, start, end int) []Role {
	t.Helper()
	sr := Classify([]byte(src))
	var roles []Role
	for _, sp := range sr.Spans {
		if sp.Start == start && sp.End == end {
			roles = append(roles, sp.Role)
		}
	}
	return roles
}

// spanOf returns the byte span of the first occurrence of substr.
func spanOf(t *testing.T, src, substr string) (int, int) {
	t.Helper()
	i := strings.Index(src, substr)
	if i < 0 {
		t.Fatalf("%q not found in %q", substr, src)
	}
	return i, i + len(substr)
}

func wantRole(t *testing.T, src, substr string, want Role) {
	t.Helper()
	start, end := spanOf(t, src, substr)
	roles := rolesIn(t, src, start, end)
	for _, r := range roles {
		if r == want {
			return
		}
	}
	t.Errorf("%q in %q: roles at [%d:%d) = %v, want %v", substr, src, start, end, roles, want)
}

func TestClassifySiteAddresses(t *testing.T) {
	src := "example.test {\n\trespond ok\n}\n"
	wantRole(t, src, "example.test", RoleSiteAddress)

	src2 := "localhost:8080 {\n\trespond ok\n}\n"
	wantRole(t, src2, "localhost:8080", RoleSiteAddress)
	wantRole(t, src2, ":8080", RolePort)

	src3 := "*.example.test {\n}\n"
	wantRole(t, src3, "*.example.test", RoleSiteAddress)

	// Brace-less sites classify their header too.
	src4 := "localhost:8080\n\trespond ok\n"
	wantRole(t, src4, "localhost:8080", RoleSiteAddress)
}

func TestClassifyDomainsAndPorts(t *testing.T) {
	src := "example.test {\n\treverse_proxy app-01.example.test:8080\n}\n"
	wantRole(t, src, "app-01.example.test", RoleDomain)
	wantRole(t, src, ":8080", RolePort)
}

func TestClassifyIPAndCIDR(t *testing.T) {
	src := "example.test {\n\treverse_proxy 192.0.2.4:80\n\t@lan ip 192.168.1.0/24\n\trespond ok\n}\n"
	wantRole(t, src, "192.0.2.4", RoleIP)
	wantRole(t, src, ":80", RolePort)
	wantRole(t, src, "192.168.1.0/24", RoleCIDR)

	// IPv6: whole address is an IP, a bracketed address with a port suffix
	// keeps both roles.
	src6 := "example.test {\n\treverse_proxy [::1]:8080\n}\n"
	wantRole(t, src6, "[::1]", RoleIP)
	wantRole(t, src6, ":8080", RolePort)
}

func TestClassifyPaths(t *testing.T) {
	src := "example.test {\n\thandle /api/* {\n\t\trespond ok\n\t}\n}\n"
	wantRole(t, src, "/api/*", RolePath)
}

func TestClassifyMatchers(t *testing.T) {
	src := "example.test {\n\t@api path /api/*\n\thandle @api {\n\t\trespond ok\n\t}\n}\n"
	wantRole(t, src, "@api", RoleMatcherDefinition)
	// The second @api (the reference in handle) must be a reference.
	start, _ := spanOf(t, src, "@api")
	start2, _ := spanOf(t, src[start+1:], "@api")
	refStart := start + 1 + start2
	refEnd := refStart + len("@api")
	roles := rolesIn(t, src, refStart, refEnd)
	for _, r := range roles {
		if r == RoleMatcherReference {
			return
		}
	}
	t.Errorf("second @api at [%d:%d) roles = %v, want RoleMatcherReference", refStart, refEnd, roles)
}

func TestClassifyPlaceholders(t *testing.T) {
	src := "example.test {\n\treverse_proxy {env.BACKEND}:8080\n\theader X-Name {http.request.header.X-Name}\n}\n"
	wantRole(t, src, "{env.BACKEND}", RolePlaceholder)
	wantRole(t, src, "{http.request.header.X-Name}", RolePlaceholder)
}

func TestClassifyDurations(t *testing.T) {
	src := "example.test {\n\treverse_proxy 192.0.2.8:9377 {\n\t\thealth_interval 10s\n\t\thealth_timeout 5s\n\t\tflush_interval -1\n\t}\n}\n"
	wantRole(t, src, "10s", RoleDuration)
	wantRole(t, src, "5s", RoleDuration)
	// -1 is not a duration.
	start, end := spanOf(t, src, "-1")
	if roles := rolesIn(t, src, start, end); len(roles) != 0 {
		t.Errorf("'-1' roles = %v, want none", roles)
	}
	// A Caddy days value classifies as a duration.
	if !isDurationWord("1d") || !isDurationWord("720h") || isDurationWord("10MB") {
		t.Errorf("isDurationWord misclassified 1d/720h/10MB")
	}
}

func TestClassifyStatusCodes(t *testing.T) {
	src := "example.test {\n\trespond 404\n\tredir /old /new 302\n}\n"
	wantRole(t, src, "404", RoleStatusCode)
	wantRole(t, src, "302", RoleStatusCode)
}

func TestClassifyStrings(t *testing.T) {
	src := "example.test {\n\trespond \"hello world\" 200\n\theader X-T `raw`\n}\n"
	wantRole(t, src, "\"hello world\"", RoleString)
	wantRole(t, src, "`raw`", RoleString)
}

func TestClassifyHeredocs(t *testing.T) {
	src := "example.test {\n\trespond <<HTML\n\t\t<html>{p}</html>\n\tHTML 200\n}\n"
	start, end := spanOf(t, src, "<<HTML")
	roles := rolesIn(t, src, start, end)
	for _, r := range roles {
		if r == RoleHeredocMarker {
			return
		}
	}
	t.Errorf("<<HTML roles = %v, want RoleHeredocMarker", roles)

	start, end = spanOf(t, src, "HTML 200")
	closerStart, closerEnd := start, start+4
	roles = rolesIn(t, src, closerStart, closerEnd)
	for _, r := range roles {
		if r == RoleHeredocMarker {
			return
		}
	}
	t.Errorf("closing marker roles = %v, want RoleHeredocMarker", roles)

	// The whole heredoc token (opener through closing marker) has the
	// heredoc role.
	tokStart, _ := spanOf(t, src, "<<HTML")
	heredocEnd := tokStart + len("<<HTML\n\t\t<html>{p}</html>\n\tHTML")
	roles = rolesIn(t, src, tokStart, heredocEnd)
	for _, r := range roles {
		if r == RoleHeredoc {
			return
		}
	}
	t.Errorf("heredoc span roles = %v, want RoleHeredoc", roles)
}

func TestClassifyDirectiveNames(t *testing.T) {
	src := "example.test {\n\trespond ok\n\treverse_proxy localhost:8080 {\n\t\theader_up Host {host}\n\t}\n}\n"
	wantRole(t, src, "respond", RoleDirectiveName)
	wantRole(t, src, "reverse_proxy", RoleDirectiveName)
	wantRole(t, src, "header_up", RoleDirectiveName)
}

func TestClassifyUnknownDirectivesStayVisible(t *testing.T) {
	src := "example.test {\n\tcustom_plugin_directive \"keep this raw\"\n\trespond ok\n}\n"
	wantRole(t, src, "custom_plugin_directive", RoleDirectiveName)
	// The unknown directive's argument keeps only its lexical string role;
	// the directive is not hidden and no validity is implied.
	start, end := spanOf(t, src, "\"keep this raw\"")
	roles := rolesIn(t, src, start, end)
	found := false
	for _, r := range roles {
		if r == RoleString {
			found = true
		}
	}
	if !found {
		t.Errorf("unknown directive string arg roles = %v, want RoleString", roles)
	}
}

func TestClassifyDegradesOnParseError(t *testing.T) {
	// Lexical roles still appear in a partially parsed document (an
	// unclosed block: the tree is incomplete but the string token is
	// intact).
	src := "example.test {\n\trespond \"ok\"\n"
	sr := Classify([]byte(src))
	if sr == nil {
		t.Fatal("Classify returned nil for malformed input")
	}
	foundString := false
	for _, sp := range sr.Spans {
		if sp.Role == RoleString {
			foundString = true
		}
	}
	if !foundString {
		t.Errorf("spans = %v, want at least one RoleString on malformed input", sr.Spans)
	}

	// Heredoc markers and site addresses survive a lex-level error too.
	src2 := "example.test {\n\trespond <<EOF\nbody\nEOF\n"
	sr2 := Classify([]byte(src2))
	markers := 0
	for _, sp := range sr2.Spans {
		if sp.Role == RoleHeredocMarker {
			markers++
		}
	}
	if markers < 2 {
		t.Errorf("heredoc markers = %d, want 2 on malformed input", markers)
	}
}

func TestClassifyCRLFAndBOM(t *testing.T) {
	src := "\xEF\xBB\xBFexample.test {\r\n\trespond ok\r\n}\r\n"
	sr := Classify([]byte(src))
	siteAddr, ports := 0, 0
	for _, sp := range sr.Spans {
		switch sp.Role {
		case RoleSiteAddress:
			siteAddr++
		case RolePort:
			ports++
		}
	}
	if siteAddr != 1 {
		t.Errorf("site address spans = %d, want 1 (BOM/CRLF source)", siteAddr)
	}
	// A bare :8080 site address inside the fixture would add a port; with
	// example.test there is none.
	if ports != 0 {
		t.Errorf("port spans = %d, want 0", ports)
	}
}

func TestClassifyEmptyAndTrivial(t *testing.T) {
	if sr := Classify(nil); sr == nil || len(sr.Spans) != 0 {
		t.Errorf("Classify(nil) = %+v, want empty spans", sr)
	}
	sr := Classify([]byte("# comment only\n"))
	if len(sr.Spans) != 0 {
		t.Errorf("comment-only spans = %v, want none", sr.Spans)
	}
}

func TestRoleString(t *testing.T) {
	tests := []struct {
		role Role
		want string
	}{
		{RoleNone, "none"},
		{RoleSiteAddress, "site-address"},
		{RoleDomain, "domain"},
		{RolePath, "path"},
		{RolePort, "port"},
		{RoleIP, "ip"},
		{RoleCIDR, "cidr"},
		{RoleMatcherDefinition, "matcher-definition"},
		{RoleMatcherReference, "matcher-reference"},
		{RolePlaceholder, "placeholder"},
		{RoleDuration, "duration"},
		{RoleStatusCode, "status-code"},
		{RoleString, "string"},
		{RoleHeredoc, "heredoc"},
		{RoleHeredocMarker, "heredoc-marker"},
		{RoleDirectiveName, "directive-name"},
	}
	for _, tt := range tests {
		if got := tt.role.String(); got != tt.want {
			t.Errorf("Role(%d).String() = %q, want %q", tt.role, got, tt.want)
		}
	}
}

// TestClassifyCompatFixture sweeps the Phase 2 compat corpus: classification
// must not panic and must produce the expected roles on a rich file.
func TestClassifyCompatFixture(t *testing.T) {
	src := loadFixture(t, "compat")
	sr := Classify(src)
	siteAddr, matcherDefs, placeholders := 0, 0, 0
	for _, sp := range sr.Spans {
		switch sp.Role {
		case RoleSiteAddress:
			siteAddr++
		case RoleMatcherDefinition:
			matcherDefs++
		case RolePlaceholder:
			placeholders++
		}
	}
	if siteAddr < 2 {
		t.Errorf("site address spans = %d, want >= 2", siteAddr)
	}
	if matcherDefs != 2 {
		t.Errorf("matcher definition spans = %d, want 2", matcherDefs)
	}
	if placeholders < 3 {
		t.Errorf("placeholder spans = %d, want >= 3", placeholders)
	}
}

func TestClassifyDocDirectEntry(t *testing.T) {
	doc := Parse([]byte("example.test {\n\trespond ok\n}\n"))
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	sr := ClassifyDoc(doc)
	if sr == nil || len(sr.Spans) == 0 {
		t.Fatalf("ClassifyDoc spans = %+v, want site-address and directive-name spans", sr)
	}
}

func TestClassifyToleratesEmptyAndNonWordDirectives(t *testing.T) {
	// A directive node with an empty name is skipped during tree-context
	// collection; the neutral source produces no lexical roles either.
	empty := &Document{Source: []byte("\n"), Nodes: []Node{{Kind: KindDirective, Name: ""}}}
	if sr := ClassifyDoc(empty); sr == nil || len(sr.Spans) != 0 {
		t.Errorf("empty-name directive spans = %+v, want none", sr)
	}
	// A directive whose first token is not a word (here an open brace)
	// contributes no tree context.
	nonWord := &Document{Source: []byte("{\n}\n"), Nodes: []Node{{Kind: KindDirective, Name: "x", Range: SourceRange{Start: 0, End: 1}}}}
	if sr := ClassifyDoc(nonWord); sr == nil || len(sr.Spans) != 0 {
		t.Errorf("non-word header directive spans = %+v, want none", sr)
	}
}

func TestSiteHeaderTokensDegrades(t *testing.T) {
	src := []byte("example.test {\n\trespond \"oops\n}\n")
	// An un-lexable site header yields no tokens.
	if toks := siteHeaderTokens(src, Node{Kind: KindSite, Name: "example.test", Range: SourceRange{Start: 0, End: 29}}); toks != nil {
		t.Errorf("siteHeaderTokens(unlexable) = %v, want nil", toks)
	}
	// An empty site range yields no tokens either.
	if toks := siteHeaderTokens(src, Node{Kind: KindSite, Name: "x", Range: SourceRange{Start: 5, End: 5}}); toks != nil {
		t.Errorf("siteHeaderTokens(empty) = %v, want nil", toks)
	}
}

func TestHeredocMarkersWithoutNewline(t *testing.T) {
	opener, closer := heredocMarkers([]byte("abc"), Token{Start: 0, End: 3})
	if opener != (Classified{}) || closer != (Classified{}) {
		t.Errorf("heredocMarkers(single-line) = %+v, %+v, want zero markers", opener, closer)
	}
}

func TestClassifyWordDirectRoles(t *testing.T) {
	cases := []struct {
		text string
		role Role
	}{
		{"https://example.test", RoleDomain}, // scheme stripped before classification
		{"192.0.2.4", RoleIP},
		{":8080", RolePort},
	}
	for _, c := range cases {
		out := &SemanticRoles{}
		classifyWord([]byte(c.text), Token{Text: c.text, Start: 0, End: len(c.text)}, out)
		if len(out.Spans) != 1 || out.Spans[0].Role != c.role {
			t.Errorf("classifyWord(%q) spans = %+v, want a single %v", c.text, out.Spans, c.role)
		}
	}
}

func TestClassifySiteAddressSubspans(t *testing.T) {
	// A scheme-bearing site address strips the scheme before sub-span
	// classification; the port keeps its role.
	wantRole(t, "https://example.test:8443 {\n}\n", ":8443", RolePort)
	// A path-prefixed address classifies the path sub-span directly.
	out := &SemanticRoles{}
	classifyWordSubspans([]byte("/foo"), Token{Text: "/foo", Start: 0, End: 4}, out)
	if len(out.Spans) != 1 || out.Spans[0].Role != RolePath {
		t.Errorf("classifyWordSubspans(/foo) = %+v, want a single RolePath", out.Spans)
	}
}

func TestIsAllDigits(t *testing.T) {
	if isAllDigits("") {
		t.Error("isAllDigits(\"\") must be false")
	}
	if isAllDigits("12a") {
		t.Error("isAllDigits(\"12a\") must be false")
	}
	if !isAllDigits("8080") {
		t.Error("isAllDigits(\"8080\") must be true")
	}
}
