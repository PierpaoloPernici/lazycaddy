package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// renderWithANSIInline renders through highlightSource with advisory inline
// findings and ANSI output forced on, restoring the terminal-agnostic profile.
func renderWithANSIInline(src []byte, findings []caddyfile.InlineFinding) string {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	return highlightSource(src, 0, 0, findings...)
}

func TestInlineStyleKeyFor(t *testing.T) {
	if got := inlineStyleKeyFor(caddyfile.SeverityAdvisoryHint); got != 100 {
		t.Errorf("hint key = %d, want 100", got)
	}
	if got := inlineStyleKeyFor(caddyfile.SeverityAdvisoryInfo); got != 101 {
		t.Errorf("info key = %d, want 101", got)
	}
	if got := inlineStyleKeyFor(caddyfile.InlineSeverity(99)); got != 0 {
		t.Errorf("unknown severity key = %d, want 0", got)
	}
}

func TestHighlightSource_InlineHintStyled(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy @api localhost:8080\n}\n")
	// @api is referenced but never defined -> hint.
	doc := caddyfile.Parse(src)
	findings := caddyfile.InlineProblems(doc)
	if len(findings) == 0 || findings[0].Severity != caddyfile.SeverityAdvisoryHint {
		t.Fatalf("expected a hint finding, got %+v", findings)
	}
	got := renderWithANSIInline(src, findings)
	assertSourceLossless(t, src, got)
	// The token is split into per-rune styled chunks, so check the style is
	// applied (the losslessness assertion already proves the text survived).
	if !strings.Contains(got, sgrOf(syntaxInlineHintStyle)) {
		t.Errorf("hint token not styled inline:\n%s", got)
	}
	if !strings.Contains(stripANSI(got), "@api") {
		t.Errorf("hint token text missing after styling:\n%s", got)
	}
}

func TestHighlightSource_InlineInfoStyled(t *testing.T) {
	src := []byte("example.test {\n\t@unused path /x/*\n}\n")
	// @unused is defined but never referenced -> info.
	doc := caddyfile.Parse(src)
	findings := caddyfile.InlineProblems(doc)
	if len(findings) == 0 || findings[0].Severity != caddyfile.SeverityAdvisoryInfo {
		t.Fatalf("expected an info finding, got %+v", findings)
	}
	got := renderWithANSIInline(src, findings)
	assertSourceLossless(t, src, got)
	if !strings.Contains(got, sgrOf(syntaxInlineInfoStyle)) {
		t.Errorf("info token not styled inline:\n%s", got)
	}
	if !strings.Contains(stripANSI(got), "@unused") {
		t.Errorf("info token text missing after styling:\n%s", got)
	}
}

func TestHighlightSource_NoFindings_NoInlineStyle(t *testing.T) {
	// A self-consistent document produces no findings, so no inline style.
	src := []byte("example.test {\n\t@api path /api/*\n\treverse_proxy @api localhost\n}\n")
	got := renderWithANSIInline(src, nil)
	assertSourceLossless(t, src, got)
	if strings.Contains(got, sgrOf(syntaxInlineHintStyle)) || strings.Contains(got, sgrOf(syntaxInlineInfoStyle)) {
		t.Errorf("no findings should not apply an inline style:\n%s", got)
	}
}

func TestInlineFindingsByLine(t *testing.T) {
	findings := []caddyfile.InlineFinding{
		{Message: "a", StartLine: 2, Start: 10, End: 14},
		{Message: "b", StartLine: 2, Start: 20, End: 24},
		{Message: "c", StartLine: 5, Start: 5, End: 9},
		{Message: "bad line0", StartLine: 0, Start: 1, End: 2}, // dropped
		{Message: "bad range", StartLine: 3, Start: 8, End: 8}, // dropped (empty)
		{Message: "bad oob", StartLine: 3, Start: -1, End: 3},  // dropped
	}
	src := make([]byte, 30)
	got := inlineFindingsByLine(src, findings)
	if len(got[2]) != 2 {
		t.Errorf("line 2 has %d findings, want 2", len(got[2]))
	}
	if len(got[5]) != 1 {
		t.Errorf("line 5 has %d findings, want 1", len(got[5]))
	}
	for _, line := range []int{0, 3} {
		if len(got[line]) != 0 {
			t.Errorf("line %d should be dropped, got %d", line, len(got[line]))
		}
	}
}

func TestSyncInlineFindings_CachesAndRecomputes(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m.showInlineFindings = true
	doc := m.sourceDoc

	// First call recomputes and populates the cache.
	m.syncInlineFindings(doc)
	if len(m.inlineFindings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(m.inlineFindings))
	}
	first := m.inlineFindings

	// Steady document: the cache is reused (no panic, same result).
	m.syncInlineFindings(doc)
	if &m.inlineFindings[0] != &first[0] {
		t.Error("steady selection should reuse the cached slice")
	}

	// Changing the source invalidates the cache and recomputes.
	m.sourceDoc = caddyfile.Parse([]byte("example.test {\n\t@x path /x/*\n}\n"))
	m.sourceDoc.Path = doc.Path
	m.syncInlineFindings(m.sourceDoc)
	if len(m.inlineFindings) != 1 || m.inlineFindings[0].Severity != caddyfile.SeverityAdvisoryInfo {
		t.Errorf("source change should recompute, got %+v", m.inlineFindings)
	}

	// Toggled off clears the cache.
	m.showInlineFindings = false
	m.syncInlineFindings(m.sourceDoc)
	if m.inlineFindings != nil || m.inlineFindingsDoc != nil {
		t.Error("toggle off should clear the findings cache")
	}
}

func TestToggleInlineFindings(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m.toggleInlineFindings()
	if !m.showInlineFindings {
		t.Fatal("toggle should enable inline findings")
	}
	if len(m.inlineFindings) != 1 {
		t.Errorf("expected 1 finding after enable, got %d", len(m.inlineFindings))
	}
	if !strings.Contains(m.statusMessage, "inline findings: on") {
		t.Errorf("status = %q after enable", m.statusMessage)
	}

	// Second press disables and clears.
	m.toggleInlineFindings()
	if m.showInlineFindings {
		t.Fatal("toggle should disable inline findings")
	}
	if m.inlineFindings != nil {
		t.Error("disable should clear findings")
	}
	if !strings.Contains(m.statusMessage, "off") {
		t.Errorf("status = %q after disable", m.statusMessage)
	}
}

func TestToggleInlineFindings_NoReliableDoc(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.sourceDoc = nil
	m.toggleInlineFindings()
	if !m.showInlineFindings {
		t.Fatal("toggle should still enable the flag")
	}
	if !strings.Contains(m.statusMessage, "no reliable document") {
		t.Errorf("status = %q, want a no-reliable-document message", m.statusMessage)
	}
}

func TestHighlightSource_Inline_UnknownSeverity(t *testing.T) {
	// A finding with an unrecognized severity maps to key 0 and must be
	// ignored by the renderer without error or byte loss. Calling
	// renderHighlightedLine directly with an inline finding of unknown
	// severity exercises the skip guard deterministically.
	got := renderHighlightedLine("example.test {", 0, nil, nil, []caddyfile.InlineFinding{{
		Message: "x", Severity: caddyfile.InlineSeverity(99), Start: 2, End: 6, StartLine: 1,
	}})
	if got != "example.test {" {
		t.Errorf("unknown-severity finding should leave the line verbatim, got %q", got)
	}
}

func TestToggleInlineFindings_ErrDoc(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.sourceDoc = caddyfile.Parse([]byte("example.test {\n")) // unclosed -> Err != nil
	m.toggleInlineFindings()
	if !strings.Contains(m.statusMessage, "no reliable document") {
		t.Errorf("status = %q, want no-reliable-document for an erroneous doc", m.statusMessage)
	}
}

func TestSyncInlineFindings_ErrOrNilDoc(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.showInlineFindings = true
	// nil document clears the cache without a recompute.
	m.inlineFindings = []caddyfile.InlineFinding{{Message: "stale"}}
	m.syncInlineFindings(nil)
	if m.inlineFindings != nil || m.inlineFindingsDoc != nil {
		t.Error("nil doc should clear the findings cache")
	}
	// A document with a parse error also yields no findings.
	errDoc := caddyfile.Parse([]byte("example.test {\n"))
	m.syncInlineFindings(errDoc)
	if m.inlineFindings != nil {
		t.Error("erroneous doc should yield no findings")
	}
}

func TestCommandPalette_ToggleInlineFindings(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !strings.Contains(stripANSI(m.View()), "Toggle inline findings") {
		t.Errorf("palette should list the inline-findings command:\n%s", m.View())
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // close palette

	// The i keybinding toggles the overlay.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if !m.showInlineFindings {
		t.Error("i should enable inline findings")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if m.showInlineFindings {
		t.Error("second i should disable inline findings")
	}
}
