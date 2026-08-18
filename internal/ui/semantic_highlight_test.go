package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// TestSemanticRoleKeyMappings verifies the semantic role→style key mapping:
// roles that mirror a lexical kind reuse that key, tree/value roles have
// their own keys, and unclassified roles map to 0 (no semantic style).
func TestSemanticRoleKeyMappings(t *testing.T) {
	tests := []struct {
		name string
		role caddyfile.Role
		want int
	}{
		{"string reuses lexical string", caddyfile.RoleString, 2},
		{"heredoc reuses lexical heredoc", caddyfile.RoleHeredoc, 3},
		{"placeholder reuses lexical placeholder", caddyfile.RolePlaceholder, 4},
		{"site address", caddyfile.RoleSiteAddress, 6},
		{"directive name", caddyfile.RoleDirectiveName, 7},
		{"domain", caddyfile.RoleDomain, 8},
		{"path", caddyfile.RolePath, 9},
		{"port", caddyfile.RolePort, 10},
		{"ip", caddyfile.RoleIP, 11},
		{"cidr", caddyfile.RoleCIDR, 11},
		{"matcher definition", caddyfile.RoleMatcherDefinition, 12},
		{"matcher reference", caddyfile.RoleMatcherReference, 13},
		{"duration", caddyfile.RoleDuration, 14},
		{"status code", caddyfile.RoleStatusCode, 15},
		{"heredoc marker", caddyfile.RoleHeredocMarker, 16},
		{"none", caddyfile.RoleNone, 0},
		{"unknown", caddyfile.Role(99), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := semanticRoleKeyFor(tt.role)
			if key != tt.want {
				t.Fatalf("semanticRoleKeyFor(%v) = %d, want %d", tt.role, key, tt.want)
			}
			if key != 0 {
				if got := stripANSI(styleForKey(key).Render("x")); got != "x" {
					t.Errorf("styleForKey(%d) rendered %q, want x", key, got)
				}
			}
		})
	}
}

// TestHighlightSourceSemanticRoles verifies that a realistic Caddyfile
// renders each advisory semantic role with its dedicated style: site
// address, directive names, domains, ports, status codes, matcher
// references, paths, durations and IP addresses. The stripped output must
// stay byte-lossless.
func TestHighlightSourceSemanticRoles(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy localhost:8080\n\thandle @api {\n\t\tredir /old* /new 301\n\t}\n\trespond \"ok\" 200\n\ttimeout 5m30s\n\tbind 127.0.0.1\n}\n")
	got := renderWithANSI(src)
	assertSourceLossless(t, src, got)

	checks := []struct {
		text  string
		style lipgloss.Style
	}{
		{"example.test", syntaxSiteStyle},
		{"reverse_proxy", syntaxDirectiveStyle},
		{"localhost", syntaxDomainStyle},
		{":8080", syntaxPortStyle},
		{"@api", syntaxMatcherRefStyle},
		{"/old*", syntaxPathStyle},
		{"301", syntaxStatusCodeStyle},
		{"5m30s", syntaxDurationStyle},
		{"127.0.0.1", syntaxAddressStyle},
	}
	for _, c := range checks {
		re := regexp.MustCompile(regexp.QuoteMeta(sgrOf(c.style)) + `[^\x1b]*` + regexp.QuoteMeta(c.text))
		if !re.MatchString(got) {
			t.Errorf("%q not styled with %v:\n%s", c.text, sgrOf(c.style), got)
		}
	}
}

// TestHighlightSourceUnknownDirectiveStaysLexical verifies that an unknown
// or plugin directive name is left unclassified (no directive-name role
// style) and keeps its default lexical base, so lazycaddy never guesses
// about directives it does not know.
func TestHighlightSourceUnknownDirectiveStaysLexical(t *testing.T) {
	src := []byte("totally_unknown_directive foo bar\n")
	got := renderWithANSI(src)
	assertSourceLossless(t, src, got)
	if strings.Contains(got, sgrOf(syntaxDirectiveStyle)) {
		t.Errorf("unknown directive got the known-directive style:\n%s", got)
	}
	if strings.Contains(got, sgrOf(syntaxDomainStyle)) {
		t.Errorf("unknown directive argument was classified as a domain:\n%s", got)
	}
}

// TestHighlightSourceHeredocMarkerStyled verifies that a heredoc marker
// opener is styled distinctly from the heredoc body, and that both remain
// byte-lossless.
func TestHighlightSourceHeredocMarkerStyled(t *testing.T) {
	src := []byte("respond <<HTML\n<html>hi</html>\nHTML\n")
	got := renderWithANSI(src)
	assertSourceLossless(t, src, got)
	if !strings.Contains(got, sgrOf(syntaxHeredocMarkerStyle)) {
		t.Errorf("heredoc marker must be styled, got:\n%s", got)
	}
}

// TestHighlightSourcePartialFileDegrades verifies that semantic highlighting
// still renders (degrading to lexical roles) on a partially parsed or
// unclosed site block, without dropping bytes or panicking.
func TestHighlightSourcePartialFileDegrades(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy localhost:8080\n")
	got := renderWithANSI(src)
	assertSourceLossless(t, src, got)
	if !strings.Contains(got, sgrOf(syntaxDomainStyle)) {
		t.Errorf("partial file should still classify reliable lexical roles (domain), got:\n%s", got)
	}
}
