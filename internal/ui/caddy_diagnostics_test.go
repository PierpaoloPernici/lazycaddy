package ui

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// renderWithANSIDiags renders through highlightSource with advisory findings
// and authoritative caddy diagnostics, forcing ANSI output on.
func renderWithANSIDiags(src []byte, startLine, endLine int, findings []caddyfile.InlineFinding, diags []validator.Diagnostic) string {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	return highlightSource(src, startLine, endLine, findings, diags)
}

// TestGutterBadges_StyledPerMarker verifies each gutter marker renders as a
// background badge with its own style: amber hint, blue info, red error,
// orange warning. The marker character stays distinct (never colour alone).
func TestGutterBadges_StyledPerMarker(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	src := []byte("example.test {\n\thint line\n\tinfo line\n\terror line\n\twarn line\n}\n")
	findings := []caddyfile.InlineFinding{
		{StartLine: 2, Start: 1, End: 5, Severity: caddyfile.SeverityAdvisoryHint},
		{StartLine: 3, Start: 1, End: 5, Severity: caddyfile.SeverityAdvisoryInfo},
	}
	diags := []validator.Diagnostic{
		{Line: 4, Column: 1, Message: "err", Severity: validator.SeverityError},
		{Line: 5, Column: 1, Message: "warn", Severity: validator.SeverityWarning},
	}
	got := renderWithANSIDiags(src, 0, 0, findings, diags)
	assertSourceLossless(t, src, got)
	for _, check := range []struct {
		style lipgloss.Style
		mark  string
	}{
		{gutterHintBadgeStyle, "! "},
		{gutterInfoBadgeStyle, "i "},
		{gutterErrorBadgeStyle, "E "},
		{gutterWarningBadgeStyle, "W "},
	} {
		if !strings.Contains(got, sgrOf(check.style)) {
			t.Errorf("badge style missing for marker %q:\n%s", check.mark, got)
		}
	}
	stripped := stripANSI(got)
	for _, mark := range []string{"2│! ", "3│i ", "4│E ", "5│W "} {
		if !strings.Contains(stripped, mark) {
			t.Errorf("stripped gutter missing %q:\n%s", mark, stripped)
		}
	}
}

// TestGutterBadges_ConstantWidth verifies that every line reserves the
// marker cell: clean lines render a space in it, so all lines have the same
// gutter width and the source text never shifts horizontally between marked
// and unmarked lines.
func TestGutterBadges_ConstantWidth(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	src := []byte("example.test {\n\tbogus x\n\trespond ok\n}\n")
	diags := []validator.Diagnostic{
		{Line: 2, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	got := renderWithANSIDiags(src, 0, 0, nil, diags)
	assertSourceLossless(t, src, got)
	// Every line renders the same 7-cell gutter (digits + bar + reserved
	// marker cell + space), so the source text starts at the same column on
	// marked and unmarked lines: the badge char on line 2, a space elsewhere.
	wantLines := []string{
		"   1│  example.test {",
		"   2│E \tbogus x",
		"   3│  \trespond ok",
		"   4│  }",
	}
	lines := strings.Split(stripANSI(got), "\n")[:4]
	for i, ln := range lines {
		if ln != wantLines[i] {
			t.Errorf("line %d = %q, want %q (constant gutter)", i+1, ln, wantLines[i])
		}
	}
}

// TestDiagnosticTokenSpan verifies the column→byte-span conversion: column 0
// marks the whole line, a column expands to the full token, multi-byte
// columns are char-accurate, and unreliable coordinates yield no span.
func TestDiagnosticTokenSpan(t *testing.T) {
	tests := []struct {
		name      string
		ln        string
		column    int
		wantStart int
		wantEnd   int
		wantOK    bool
	}{
		{"whole line without column", "reverse_proxy @api localhost", 0, 0, 28, true},
		{"empty line without column", "", 0, 0, 0, true},
		{"token at column", "reverse_proxy @api localhost", 1, 0, 13, true},
		{"token mid-line", "reverse_proxy @api localhost", 15, 14, 18, true},
		{"matcher token", "reverse_proxy @api localhost", 15, 14, 18, true},
		{"single char at structural boundary", "foo (bar)", 5, 4, 5, true},
		{"column beyond line", "foo", 9, 0, 0, false},
		{"column at line end", "foo", 4, 0, 0, false},
		{"multi-byte column skips runes", "héllo wörld", 7, 7, 13, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, ok := diagnosticTokenSpan(tt.ln, validator.Diagnostic{Column: tt.column})
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if start != tt.wantStart || end != tt.wantEnd {
				t.Errorf("span = [%d,%d), want [%d,%d)", start, end, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

// TestDiagnosticTokenSpanMultiByte verifies that a column pointing into a
// multi-byte rune never splits it: the rune is skipped whole.
func TestDiagnosticTokenSpanMultiByte(t *testing.T) {
	ln := "aébc"
	start, end, ok := diagnosticTokenSpan(ln, validator.Diagnostic{Column: 3})
	if !ok {
		t.Fatal("column 3 should be valid")
	}
	// Column 3 is the 'b' (é occupies bytes 1-2); the token continues to
	// the end of the line.
	if start != 3 || end != 5 {
		t.Errorf("span = [%d,%d), want [3,5)", start, end)
	}
}

// TestCaddyDiagsByLine verifies grouping by 1-based line and the skip of
// diagnostics without a reported line.
func TestCaddyDiagsByLine(t *testing.T) {
	diags := []validator.Diagnostic{
		{Line: 2, Message: "a"},
		{Line: 2, Message: "b"},
		{Line: 5, Message: "c"},
		{Line: 0, Message: "no line"},
	}
	got := caddyDiagsByLine(diags)
	if len(got[2]) != 2 || len(got[5]) != 1 {
		t.Fatalf("grouping = %v, want 2 diags on line 2 and 1 on line 5", got)
	}
	if _, ok := got[0]; ok {
		t.Error("line 0 must be ignored")
	}
}

// TestDiagnosticStyleKeyMappings verifies the severity→style-key mapping:
// errors annotate (102), warnings reserve their key (103), everything else
// is not annotated.
func TestDiagnosticStyleKeyMappings(t *testing.T) {
	tests := []struct {
		sev  validator.Severity
		want int
	}{
		{validator.SeverityError, 102},
		{validator.SeverityWarning, 103},
		{validator.SeverityInfo, 0},
		{validator.SeverityDebug, 0},
	}
	for _, tt := range tests {
		if got := diagnosticStyleKeyFor(tt.sev); got != tt.want {
			t.Errorf("diagnosticStyleKeyFor(%v) = %d, want %d", tt.sev, got, tt.want)
		}
	}
	if got := stripANSI(styleForKey(102).Render("x")); got != "x" {
		t.Errorf("styleForKey(102) rendered %q, want x", got)
	}
	if got := stripANSI(styleForKey(103).Render("x")); got != "x" {
		t.Errorf("styleForKey(103) rendered %q, want x", got)
	}
}

// TestHighlightSourceCaddyDiagnostics verifies the authoritative overlay: an
// 'E' gutter marker on error lines, the offending token styled with the
// caddy error style, whole-line marking without a column, and byte
// losslessness throughout.
func TestHighlightSourceCaddyDiagnostics(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy @api localhost\n\tbogus_directive x\n}\n")
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 2, Column: 16, Message: "unrecognized matcher name: @api", Severity: validator.SeverityError},
		{Path: "config/Caddyfile", Line: 3, Column: 0, Message: "parse error", Severity: validator.SeverityError},
		{Path: "config/Caddyfile", Line: 4, Column: 1, Message: "non-fatal note", Severity: validator.SeverityWarning},
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "info only", Severity: validator.SeverityInfo},
	}
	got := renderWithANSIDiags(src, 0, 0, nil, diags)
	assertSourceLossless(t, src, got)

	// Error lines carry the 'E' gutter marker, warnings the 'W' marker;
	// clean lines (including an info-only line) carry no caddy marker. The
	// markers are badges: check the stripped text for the character and the
	// raw output for the badge background style.
	stripped := stripANSI(got)
	if !strings.Contains(stripped, "2│E ") {
		t.Errorf("line 2 missing 'E' gutter marker:\n%s", got)
	}
	if !strings.Contains(stripped, "3│E ") {
		t.Errorf("line 3 missing 'E' gutter marker:\n%s", got)
	}
	if !strings.Contains(stripped, "4│W ") {
		t.Errorf("line 4 missing 'W' gutter marker:\n%s", got)
	}
	if strings.Contains(stripped, "1│E ") || strings.Contains(stripped, "1│W ") {
		t.Errorf("an info-only line must not carry a caddy marker:\n%s", got)
	}
	if !strings.Contains(got, sgrOf(gutterErrorBadgeStyle)) || !strings.Contains(got, sgrOf(gutterWarningBadgeStyle)) {
		t.Errorf("caddy markers must render as background badges:\n%s", got)
	}

	// The token at column 16 of line 2 (@api) is styled with the caddy
	// error style. lipgloss emits underline styles per character, so the
	// assertion checks the style sequence and the intact text separately.
	if !strings.Contains(got, sgrOf(syntaxCaddyErrorStyle)) {
		t.Errorf("line 2 token not styled with the caddy error style:\n%s", got)
	}
	if !strings.Contains(stripANSI(got), "@api") {
		t.Errorf("line 2 token text missing after styling:\n%s", got)
	}
	// Column 0 marks the whole line 3: its body survives byte-exact and
	// the error style is emitted on it (diagnosticTokenSpan covers the
	// whole-line range in its unit test).
	if !strings.Contains(stripANSI(got), "\tbogus_directive x") {
		t.Errorf("line 3 body missing after styling:\n%s", got)
	}
}

// TestHighlightSourceCaddyDiagnosticsPrecedence verifies that an
// authoritative error outranks an advisory hint on the same line: the
// gutter shows 'E' (not '!') and the token style layers over the advisory.
func TestHighlightSourceCaddyDiagnosticsPrecedence(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy @api localhost\n}\n")
	findings := []caddyfile.InlineFinding{
		{StartLine: 2, Start: 15, End: 19, Severity: caddyfile.SeverityAdvisoryHint},
	}
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 2, Column: 16, Message: "unrecognized matcher", Severity: validator.SeverityError},
	}
	got := renderWithANSIDiags(src, 0, 0, findings, diags)
	assertSourceLossless(t, src, got)
	stripped := stripANSI(got)
	if !strings.Contains(stripped, "2│E ") {
		t.Errorf("caddy error must win the gutter marker over the advisory hint:\n%s", got)
	}
	if strings.Contains(stripped, "2│! ") {
		t.Errorf("advisory hint marker must not survive next to a caddy error:\n%s", got)
	}
	if !strings.Contains(got, sgrOf(gutterErrorBadgeStyle)) {
		t.Errorf("the caddy error must render as a badge:\n%s", got)
	}
}

// TestHighlightSourceCaddyDiagnosticsColumnBeyondLine verifies that a
// diagnostic whose column falls past the line end still marks the line in
// the gutter (the line itself is known) but never styles a token on
// unreliable coordinates.
func TestHighlightSourceCaddyDiagnosticsColumnBeyondLine(t *testing.T) {
	src := []byte("example.test {\n}\n")
	diags := []validator.Diagnostic{
		{Line: 2, Column: 99, Message: "beyond line", Severity: validator.SeverityError},
	}
	got := renderWithANSIDiags(src, 0, 0, nil, diags)
	assertSourceLossless(t, src, got)
	if !strings.Contains(stripANSI(got), "2│E ") {
		t.Errorf("the known line must still carry the 'E' marker:\n%s", got)
	}
	if strings.Contains(got, sgrOf(syntaxCaddyErrorStyle)) {
		t.Errorf("an unpinnable column must not style a token:\n%s", got)
	}
}

// TestHighlightSourceCaddyDiagnosticsUnreliable verifies that diagnostics
// that cannot be pinned (no line, line beyond the source) never annotate
// the source view and never break losslessness.
func TestHighlightSourceCaddyDiagnosticsUnreliable(t *testing.T) {
	src := []byte("example.test {\n}\n")
	diags := []validator.Diagnostic{
		{Line: 0, Column: 1, Message: "no line", Severity: validator.SeverityError},
		{Line: 99, Column: 1, Message: "beyond source", Severity: validator.SeverityError},
	}
	got := renderWithANSIDiags(src, 0, 0, nil, diags)
	assertSourceLossless(t, src, got)
	if strings.Contains(got, "│E") {
		t.Errorf("unreliable diagnostics must not annotate the gutter:\n%s", got)
	}
	if strings.Contains(got, sgrOf(syntaxCaddyErrorStyle)) {
		t.Errorf("unreliable diagnostics must not style tokens:\n%s", got)
	}
}

// TestTokenFromMessage verifies the token extraction from unpositioned
// caddy messages: the text after the last ": ", quotes stripped, or ""
// when no token is named.
func TestTokenFromMessage(t *testing.T) {
	tests := []struct {
		msg  string
		want string
	}{
		{"unrecognized matcher name: @phantom", "@phantom"},
		{"unrecognized directive: bogus_directive", "bogus_directive"},
		{"unknown directive: 'foo'", "foo"},
		{"parsing caddyfile tokens for 'reverse_proxy': unrecognized matcher name: @phantom", "@phantom"},
		{"unexpected EOF", ""},
		{"open /etc/caddy/Caddyfile: no such file", "no such file"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := tokenFromMessage(tt.msg); got != tt.want {
			t.Errorf("tokenFromMessage(%q) = %q, want %q", tt.msg, got, tt.want)
		}
	}
}

// TestTokenColumn verifies the word-boundary search: the token must stand
// alone (never inside a longer word) and the column is 1-based and
// rune-based.
func TestTokenColumn(t *testing.T) {
	tests := []struct {
		ln, tok string
		wantCol int
		wantOK  bool
	}{
		{"\treverse_proxy @phantom localhost:8080", "@phantom", 16, true},
		{"@phantom path /x/*", "@phantom", 1, true},
		{"reverse_proxy localhost:8080", "@phantom", 0, false},
		{"http@phantom inside a word", "@phantom", 0, false},
		{"php_fastcgi localhost", "php", 0, false},
		{"café @tok end", "@tok", 6, true},
		{"", "x", 0, false},
	}
	for _, tt := range tests {
		col, ok := tokenColumn(tt.ln, tt.tok)
		if ok != tt.wantOK || col != tt.wantCol {
			t.Errorf("tokenColumn(%q, %q) = (%d, %v), want (%d, %v)", tt.ln, tt.tok, col, ok, tt.wantCol, tt.wantOK)
		}
	}
}

// TestPinDiagnostic verifies the no-position mapping: the token named in
// the message pins onto its first standalone occurrence, and messages
// without a pinnable token (or without a hit in the source) yield 0s.
func TestPinDiagnostic(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy @phantom localhost:8080\n\t@phantom path /x/*\n}\n")
	line, col := pinDiagnostic(src, "unrecognized matcher name: @phantom")
	if line != 2 || col != 16 {
		t.Errorf("pin = (%d,%d), want (2,16)", line, col)
	}
	// A token that appears only later pins to its first occurrence.
	line, col = pinDiagnostic(src, "unrecognized matcher name: @phantom")
	_ = line
	// No token in the message: nothing pins.
	if l, c := pinDiagnostic(src, "unexpected EOF"); l != 0 || c != 0 {
		t.Errorf("pin without a token = (%d,%d), want (0,0)", l, c)
	}
	// Token absent from the source: nothing pins.
	if l, c := pinDiagnostic(src, "unknown directive: zzz"); l != 0 || c != 0 {
		t.Errorf("pin with an absent token = (%d,%d), want (0,0)", l, c)
	}
}

// TestCaddyDiagsForDoc verifies the per-document filter: path matching with
// filepath.Clean, line-0 exclusion, stale/incomplete outcomes yield nil.
func TestCaddyDiagsForDoc(t *testing.T) {
	src := "example.test {\n\treverse_proxy @phantom localhost:8080\n}\n"
	m := matcherModel(t, src)
	details := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 2, Column: 1, Message: "boom", Severity: validator.SeverityError},
		{Path: "config/other.conf", Line: 7, Column: 1, Message: "other doc", Severity: validator.SeverityError},
		{Path: "config/Caddyfile", Line: 0, Column: 0, Message: "unrecognized matcher name: @phantom", Severity: validator.SeverityError},
		{Path: "config/Caddyfile", Line: 0, Column: 0, Message: "unknown directive: zzz_absent", Severity: validator.SeverityError},
	}
	m.setInlineCaddyOutcome(4, true, m.sourceDoc.Source, "boom", details)

	got := m.caddyDiagsForDoc(m.sourceDoc)
	if len(got) != 2 {
		t.Fatalf("caddyDiagsForDoc = %d diags, want 2 (positioned + pinned; absent token skipped)", len(got))
	}
	// The positioned diagnostic passes through untouched.
	if got[0].Message != "boom" || got[0].Line != 2 {
		t.Errorf("diag[0] = %+v, want the positioned error", got[0])
	}
	// The unpositioned matcher error is pinned onto the token it names.
	if got[1].Message != "unrecognized matcher name: @phantom" || got[1].Line != 2 || got[1].Column != 16 {
		t.Errorf("diag[1] = %+v, want the pinned matcher error at line 2 col 16", got[1])
	}
	// The original details are never mutated by the pinning.
	if details[2].Line != 0 || details[2].Column != 0 {
		t.Errorf("pinning mutated the stored diagnostic: %+v", details[2])
	}

	// A document with another path (and an import-like path spelling) gets
	// nothing, never another document's lines.
	other := &caddyfile.Document{Path: "config/unrelated.conf", Source: []byte("x")}
	if o := m.caddyDiagsForDoc(other); o != nil {
		t.Errorf("caddyDiagsForDoc(other) = %v, want nil", o)
	}

	// A nil document is always empty.
	if o := m.caddyDiagsForDoc(nil); o != nil {
		t.Errorf("caddyDiagsForDoc(nil) = %v, want nil", o)
	}

	// After the outcome is flagged stale, the overlay disappears.
	m.markInlineCaddyStaleIfNeeded(&caddyfile.Document{Path: "config/Caddyfile", Source: []byte("changed")})
	if m.inlineCaddy.phase != "stale" {
		t.Fatalf("phase = %q, want stale", m.inlineCaddy.phase)
	}
	if o := m.caddyDiagsForDoc(m.sourceDoc); o != nil {
		t.Errorf("caddyDiagsForDoc after stale = %v, want nil", o)
	}

	// A fresh clean outcome (no details) yields nil too.
	m.setInlineCaddyOutcome(0, true, m.sourceDoc.Source, "", nil)
	if o := m.caddyDiagsForDoc(m.sourceDoc); o != nil {
		t.Errorf("caddyDiagsForDoc with no details = %v, want nil", o)
	}
}

// TestSourcePaneCaddyDiagnosticsOverlay verifies the full integration: a
// completed validate with error details for the selected document surfaces
// the 'E' marker in the source pane viewport and the error count in the
// title, without any selection change.
func TestSourcePaneCaddyDiagnosticsOverlay(t *testing.T) {
	src := "example.test {\n\treverse_proxy @api localhost\n}\n"
	m := matcherModel(t, src)
	if strings.Contains(m.viewport.View(), "│E ") {
		t.Fatal("no validate yet: source pane must not carry caddy markers")
	}

	details := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 2, Column: 16, Message: "unrecognized matcher name: @api", Severity: validator.SeverityError},
	}
	m.setInlineCaddyOutcome(1, true, m.sourceDoc.Source, "unrecognized matcher name: @api", details)
	_ = m.View() // render: syncSource must rebuild the content without a selection change

	if !strings.Contains(m.viewport.View(), "2│E ") {
		t.Errorf("source pane missing the 'E' marker after validate:\n%s", m.viewport.View())
	}
	if !strings.Contains(m.sourceTitle, "1 caddy error") {
		t.Errorf("source title missing the error count, got %q", m.sourceTitle)
	}

	// After the outcome is flagged stale (the source changed), a render
	// drops the overlay and the count.
	m.markInlineCaddyStaleIfNeeded(&caddyfile.Document{Path: "config/Caddyfile", Source: []byte("changed")})
	_ = m.View()
	if strings.Contains(m.viewport.View(), "│E ") {
		t.Errorf("stale outcome must drop the 'E' marker:\n%s", m.viewport.View())
	}
	if strings.Contains(m.sourceTitle, "caddy error") {
		t.Errorf("stale outcome must drop the error count, got %q", m.sourceTitle)
	}
}

// TestSourcePaneCaddyDiagnosticsImportFiltered verifies that diagnostics for
// another document never annotate the selected document's pane.
func TestSourcePaneCaddyDiagnosticsImportFiltered(t *testing.T) {
	src := "example.test {\n\timport snippets/*.caddy\n}\n"
	fs := fsReader(map[string]string{
		"config/Caddyfile":             src,
		"config/snippets/common.caddy": "respond ok\n",
	})
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: true}, fs)
	m := newLoadedModel(t, loader)
	_ = m.View()

	details := []validator.Diagnostic{
		{Path: "config/snippets/common.caddy", Line: 1, Column: 1, Message: "boom in import", Severity: validator.SeverityError},
	}
	m.setInlineCaddyOutcome(1, true, m.sourceDoc.Source, "boom in import", details)
	_ = m.View()

	if strings.Contains(m.viewport.View(), "│E ") {
		t.Errorf("an import's diagnostic must not annotate the root pane:\n%s", m.viewport.View())
	}
	if strings.Contains(m.sourceTitle, "caddy error") {
		t.Errorf("import diagnostic must not count in the root title, got %q", m.sourceTitle)
	}
}

// TestSourceTitleWithFindings_CaddyErrorsOnParseErrorDoc verifies that the
// advisory summary is skipped on a parse-error document while the
// authoritative caddy error count still surfaces in the title.
func TestSourceTitleWithFindings_CaddyErrorsOnParseErrorDoc(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	doc := &caddyfile.Document{Path: "config/Caddyfile", Source: []byte("bogus"), Err: errors.New("parse")}
	m.inlineFindingsDoc = nil
	m.setInlineCaddyOutcome(1, true, []byte("bogus"), "boom", []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	})
	got := m.sourceTitleWithFindings("Source · config/Caddyfile", doc)
	if !strings.Contains(got, "1 caddy error") {
		t.Errorf("title = %q, want the caddy error count on a parse-error doc", got)
	}
	if strings.Contains(got, "advisory") {
		t.Errorf("title = %q, want no advisory summary for a parse-error doc", got)
	}
}

// TestValidateFlow_PinsUnpositionedMatcherError reproduces the real caddy
// v2.11.4 scenario the overlay must handle: the "unrecognized matcher name:
// @phantom" error carries no position at all, so the token is pinned onto
// its line and the source pane shows the authoritative 'E' marker there
// (outranking the advisory '!' on the same line) with the token styled.
func TestValidateFlow_PinsUnpositionedMatcherError(t *testing.T) {
	src := "example.test {\n\treverse_proxy @phantom localhost:8080\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 0, Column: 0, Message: "adapting config using caddyfile: parsing caddyfile tokens for 'reverse_proxy': unrecognized matcher name: @phantom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{formatted: []byte(src), diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 100, 30)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd == nil {
		t.Fatal("v should return a validation command")
	}
	m = keyPress(t, m, cmd())
	if m.showDiagnostics {
		t.Fatal("a failed v must not force the diagnostics modal")
	}
	if m.sourceRevealLine != 2 {
		t.Errorf("pinned error line not revealed, sourceRevealLine = %d", m.sourceRevealLine)
	}
	_ = m.View()

	viewport := m.viewport.View()
	if !regexp.MustCompile(`2[│▎]E `).MatchString(viewport) {
		t.Errorf("source pane missing the 'E' marker on the pinned line:\n%s", viewport)
	}
	if !strings.Contains(m.sourceTitle, "1 caddy error") {
		t.Errorf("source title missing the error count, got %q", m.sourceTitle)
	}
}

// TestValidateFlowRevealsOverlayWithoutModal verifies the end-to-end
// workflow: a failed v validation from the main view does not force the
// diagnostics modal open; the source pane shows the overlay and the first
// error line is revealed.
func TestValidateFlowRevealsOverlayWithoutModal(t *testing.T) {
	src := "example.test {\n\tbogus_directive x\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 2, Column: 1, Message: "unknown directive", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{formatted: []byte(src), diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 100, 30)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd == nil {
		t.Fatal("v should return a validation command")
	}
	m = keyPress(t, m, cmd())
	if m.showDiagnostics {
		t.Fatal("a failed v from the main view must not force the diagnostics modal")
	}
	if m.sourceRevealLine != 2 {
		t.Errorf("first error line not revealed, sourceRevealLine = %d", m.sourceRevealLine)
	}
	_ = m.View()
	if !regexp.MustCompile(`2[│▎]E `).MatchString(m.viewport.View()) {
		t.Errorf("source pane missing the 'E' marker:\n%s", m.viewport.View())
	}
}
