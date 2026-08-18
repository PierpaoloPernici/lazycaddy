package ui

import (
	"errors"
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

// renderWithANSIInline renders through highlightSource with advisory inline
// findings and ANSI output forced on, restoring the terminal-agnostic profile.
func renderWithANSIInline(src []byte, findings []caddyfile.InlineFinding) string {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	return highlightSource(src, 0, 0, findings, nil)
}

// renderWithANSIInlineSelected renders through highlightSource with inline
// findings and an active selected-line range, forcing ANSI output.
func renderWithANSIInlineSelected(src []byte, selStart, selEnd int, findings []caddyfile.InlineFinding) string {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	return highlightSource(src, selStart, selEnd, findings, nil)
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
	findings := caddyfile.InlineProblems(caddyfile.Parse(src))
	if len(findings) == 0 || findings[0].Severity != caddyfile.SeverityAdvisoryHint {
		t.Fatalf("expected a hint finding, got %+v", findings)
	}
	got := renderWithANSIInline(src, findings)
	assertSourceLossless(t, src, got)
	if !strings.Contains(got, sgrOf(syntaxInlineHintStyle)) {
		t.Errorf("hint token not styled inline:\n%s", got)
	}
	if !strings.Contains(stripANSI(got), "@api") {
		t.Errorf("hint token text missing after styling:\n%s", got)
	}
}

func TestHighlightSource_InlineInfoStyled(t *testing.T) {
	src := []byte("example.test {\n\t@unused path /x/*\n}\n")
	findings := caddyfile.InlineProblems(caddyfile.Parse(src))
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
	src := []byte("example.test {\n\t@api path /api/*\n\treverse_proxy @api localhost\n}\n")
	got := renderWithANSIInline(src, nil)
	assertSourceLossless(t, src, got)
	if strings.Contains(got, sgrOf(syntaxInlineHintStyle)) || strings.Contains(got, sgrOf(syntaxInlineInfoStyle)) {
		t.Errorf("no findings should not apply an inline style:\n%s", got)
	}
}

// TestHighlightSource_GutterMarkers verifies the gutter marks a hint line with
// '!' and an info line with 'i', distinct from the default '│', so the markers
// survive even without colour.
func TestHighlightSource_GutterMarkers(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy @api localhost\n\t@unused path /x/*\n}\n")
	findings := caddyfile.InlineProblems(caddyfile.Parse(src))
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
	got := stripANSI(renderWithANSIInline(src, findings))
	assertSourceLossless(t, src, got)
	// The hint (line 2) and info (line 3) lines carry the markers in the gutter.
	if !strings.Contains(got, "2│!") {
		t.Errorf("hint line gutter missing '!':\n%s", got)
	}
	if !strings.Contains(got, "3│i") {
		t.Errorf("info line gutter missing 'i':\n%s", got)
	}
	if strings.Contains(got, "1│!") || strings.Contains(got, "1│i") {
		t.Errorf("clean line 1 should have no gutter marker:\n%s", got)
	}
}

// TestHighlightSource_GutterMarker_Selected verifies the gutter marker also
// appears on a line that is inside the active selection range (the selected
// gutter uses a bar instead of the plain separator).
func TestHighlightSource_GutterMarker_Selected(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy @api localhost\n\t@unused path /x/*\n}\n")
	findings := caddyfile.InlineProblems(caddyfile.Parse(src))
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
	// Select lines 2-3, both of which carry a finding.
	got := stripANSI(renderWithANSIInlineSelected(src, 2, 3, findings))
	assertSourceLossless(t, src, got)
	// The selected gutter bar (▎) plus the marker must both be present.
	if !strings.Contains(got, "2▎!") {
		t.Errorf("selected hint line gutter missing '▎!':\n%s", got)
	}
	if !strings.Contains(got, "3▎i") {
		t.Errorf("selected info line gutter missing '▎i':\n%s", got)
	}
}

func TestInlineGutterMarker(t *testing.T) {
	if got := inlineGutterMarker([]caddyfile.InlineFinding{{Severity: caddyfile.SeverityAdvisoryInfo}}); got != 'i' {
		t.Errorf("info only -> marker %q, want i", got)
	}
	if got := inlineGutterMarker([]caddyfile.InlineFinding{{Severity: caddyfile.SeverityAdvisoryHint}}); got != '!' {
		t.Errorf("hint -> marker %q, want !", got)
	}
	if got := inlineGutterMarker([]caddyfile.InlineFinding{
		{Severity: caddyfile.SeverityAdvisoryInfo},
		{Severity: caddyfile.SeverityAdvisoryHint},
	}); got != '!' {
		t.Errorf("hint+info -> marker %q, want ! (hint wins)", got)
	}
	if got := inlineGutterMarker(nil); got != 0 {
		t.Errorf("no findings -> marker %q, want 0", got)
	}
}

func TestInlineFindingsByLine(t *testing.T) {
	src := make([]byte, 30)
	got := inlineFindingsByLine(src, []caddyfile.InlineFinding{
		{StartLine: 2, Start: 10, End: 14},
		{StartLine: 2, Start: 20, End: 24},
		{StartLine: 5, Start: 5, End: 9},
		{StartLine: 0, Start: 1, End: 2}, // dropped: start line 0
		{StartLine: 3, Start: 8, End: 8}, // dropped: empty range
	})
	if len(got[2]) != 2 {
		t.Errorf("line 2 has %d findings, want 2", len(got[2]))
	}
	if len(got[5]) != 1 {
		t.Errorf("line 5 has %d findings, want 1", len(got[5]))
	}
	if len(got[0]) != 0 || len(got[3]) != 0 {
		t.Errorf("dropped findings still present")
	}
}

func TestSourceTitleWithFindings(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n\t@unused path /x/*\n}\n")
	doc := m.sourceDoc
	m.syncInlineFindings(doc)
	if len(m.inlineFindings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(m.inlineFindings))
	}
	// The new review handover uses the compact per-line count in the title.
	title := m.sourceTitleWithFindings("Source · Caddyfile · example.test (lines 1-4)", doc)
	if !strings.Contains(title, "2 findings") || !strings.Contains(title, "[i] review") {
		t.Errorf("title = %q, want the compact findings/review summary", title)
	}
}

func TestSourceTitleWithFindings_Clean(t *testing.T) {
	m := matcherModel(t, "example.test {\n\t@api path /api/*\n\treverse_proxy @api localhost\n}\n")
	m.syncInlineFindings(m.sourceDoc)
	title := m.sourceTitleWithFindings("Source · Caddyfile", m.sourceDoc)
	if !strings.Contains(title, "advisory: clean") {
		t.Errorf("clean title = %q, want 'advisory: clean'", title)
	}
}

func TestSourceTitleWithFindings_UnavailableDoc(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.syncInlineFindings(nil)
	base := "Source"
	got := m.sourceTitleWithFindings(base, nil)
	if got != base {
		t.Errorf("title with nil doc = %q, want unchanged %q", got, base)
	}
}

func TestSyncInlineFindings_CachesAndRecomputes(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	doc := m.sourceDoc
	m.syncInlineFindings(doc)
	if len(m.inlineFindings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(m.inlineFindings))
	}
	first := m.inlineFindings
	m.syncInlineFindings(doc)
	if &m.inlineFindings[0] != &first[0] {
		t.Error("steady selection should reuse the cached slice")
	}
	// Source change invalidates and recomputes.
	m.sourceDoc = caddyfile.Parse([]byte("example.test {\n\t@x path /x/*\n}\n"))
	m.sourceDoc.Path = doc.Path
	m.syncInlineFindings(m.sourceDoc)
	if len(m.inlineFindings) != 1 || m.inlineFindings[0].Severity != caddyfile.SeverityAdvisoryInfo {
		t.Errorf("source change should recompute, got %+v", m.inlineFindings)
	}
	// Malformed document yields nothing.
	m.sourceDoc = caddyfile.Parse([]byte("example.test {\n"))
	m.syncInlineFindings(m.sourceDoc)
	if m.inlineFindings != nil {
		t.Error("erroneous doc should yield no findings")
	}
}

// TestInlineReview_OpensAndNavigates drives the review view: i opens it, the
// cursor moves, Enter reveals the line and closes it, Esc closes it.
func TestInlineReview_OpensAndNavigates(t *testing.T) {
	src := "example.test {\n\treverse_proxy @api localhost\n\t@unused path /x/*\n}\n"
	m := matcherModel(t, src)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if !m.showInlineReview {
		t.Fatal("i should open the review view")
	}
	if len(m.inlineFindings) != 2 {
		t.Fatalf("review should compute 2 findings, got %d", len(m.inlineFindings))
	}

	// The review renders both findings with hint/info labels.
	view := stripANSI(m.View())
	if !strings.Contains(view, "hint") || !strings.Contains(view, "info") {
		t.Errorf("review view missing severity labels:\n%s", view)
	}

	// Down moves the cursor onto the second finding.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.inlineReviewCursor != 1 {
		t.Errorf("cursor = %d, want 1 after down", m.inlineReviewCursor)
	}
	// Enter reveals the selected line and closes the review.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.showInlineReview {
		t.Error("Enter should close the review view")
	}
	if m.sourceRevealLine == 0 {
		t.Error("Enter should reveal the finding line")
	}

	// Reopen and Esc closes it.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showInlineReview {
		t.Error("Esc should close the review view")
	}
}

func TestInlineReview_EmptyFindings(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if !m.showInlineReview {
		t.Fatal("i should open the review even with no findings")
	}
	if !strings.Contains(stripANSI(m.View()), "no advisory findings") {
		t.Errorf("review with no findings should say so:\n%s", m.View())
	}
	// Up/down are safe no-ops with nothing to move over.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.showInlineReview {
		t.Error("Enter on an empty review should close it")
	}
}

func TestInlineReview_V_LaunchesCaddyValidate(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if !m.showInlineReview {
		t.Fatal("i should open the review")
	}
	// v closes the review and runs the existing workflow. Without a formatter
	// (no Caddy binary) it surfaces the disabled-reason status instead.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	rm := updated.(*Model)
	if rm.showInlineReview {
		t.Error("v should close the review view")
	}
	if rm.formatter == nil {
		if !strings.Contains(rm.statusMessage, "caddy binary not configured") {
			t.Errorf("v without a binary should report the disabled reason, got %q", rm.statusMessage)
		}
	} else if cmd == nil {
		t.Error("v with a binary should return a validation command")
	}
}

func TestInlineReview_V_ReusesExistingWorkflow(t *testing.T) {
	// With a working formatter configured, v must delegate to the existing
	// startFormatAndValidate path (open diagnostics on failure, etc.) and
	// must not itself open the advisory review again.
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	rm := updated.(*Model)
	if rm.showInlineReview {
		t.Error("v should close the review")
	}
	// matcherModel has no Caddy binary, so the authoritative workflow is a
	// no-op that reports the disabled reason rather than returning a cmd.
	if cmd != nil {
		t.Errorf("v without a formatter should not return a cmd, got non-nil")
	}
}

func TestInlineReview_FooterAdvisoryCounts(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m.syncInlineFindings(m.sourceDoc)
	m.showInlineReview = true
	m.inlineCaddy = &inlineCaddyState{phase: "not run"}
	footer := m.inlineReviewFooter()
	if !strings.Contains(footer, "Advisory: 1 hint · 0 info") {
		t.Errorf("footer = %q, want the advisory counts", footer)
	}
}

func TestSetInlineCaddyOutcome(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	src := m.sourceDoc.Source
	m.setInlineCaddyOutcome(0, true, src, "", nil)
	if m.inlineCaddy == nil || m.inlineCaddy.phase != "result" || m.inlineCaddy.errors != 0 || m.inlineCaddy.summary != "" || len(m.inlineCaddy.details) != 0 {
		t.Errorf("clean outcome = %+v", m.inlineCaddy)
	}
	details := []validator.Diagnostic{{Line: 168, Message: "unrecognized matcher name: @phantom"}}
	m.setInlineCaddyOutcome(2, true, src, "unrecognized matcher name: @phantom", details)
	if m.inlineCaddy.errors != 2 || m.inlineCaddy.summary != "unrecognized matcher name: @phantom" || len(m.inlineCaddy.details) != 1 {
		t.Errorf("2-errors outcome = %+v, want retained details", m.inlineCaddy)
	}
	m.setInlineCaddyOutcome(0, false, src, "", nil)
	if m.inlineCaddy.phase != "result" || m.inlineCaddy.summary != "validation failed" {
		t.Errorf("failed outcome = %+v", m.inlineCaddy)
	}
}

// TestInlineReview_CaddyRowsShowRelativePath verifies that a caddy
// diagnostic row shows the diagnostic's line and its path relative to the
// root Caddyfile directory (so imports read e.g. "snippets/auth.caddy").
func TestInlineReview_CaddyRowsShowRelativePath(t *testing.T) {
	fs := fsReader(map[string]string{
		"config/Caddyfile":           "example.test {\n\timport snippets/auth.caddy\n}\n",
		"config/snippets/auth.caddy": "reverse_proxy @phantom localhost:8080\n",
	})
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: true}, fs)
	m := newLoadedModel(t, loader)
	_ = m.View()
	m.inlineCaddy = &inlineCaddyState{
		phase: "result", errors: 1, summary: "boom",
		details: []validator.Diagnostic{{Path: "config/snippets/auth.caddy", Line: 1, Message: "boom"}},
	}
	m.showInlineReview = true
	m.inlineReviewCursor = len(m.inlineFindings)
	block := stripANSI(m.inlineCaddyBlock(5))
	if !strings.Contains(block, "snippets/auth.caddy") {
		t.Errorf("caddy row should show the import path relative to the root dir, got %q", block)
	}
	if !strings.Contains(block, "line 1") {
		t.Errorf("caddy row should show the diagnostic line, got %q", block)
	}
}

// TestCaddyDiagPathLabel verifies the relative-path rendering: imports under
// the root directory are shown relative, the root itself as its base name,
// and paths outside the root keep the full form.
func TestCaddyDiagPathLabel(t *testing.T) {
	m := matcherModel(t, "example.test {\n}\n")
	tests := []struct {
		path string
		want string
	}{
		{"config/snippets/auth.caddy", "snippets/auth.caddy"},
		{"config/Caddyfile", "Caddyfile"},
		{"/etc/other.conf", "/etc/other.conf"},
	}
	for _, tt := range tests {
		if got := m.caddyDiagPathLabel(validator.Diagnostic{Path: tt.path}); got != tt.want {
			t.Errorf("caddyDiagPathLabel(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// TestRevealCaddyDiagnostic_SwitchesToImportDoc verifies that revealing a
// caddy diagnostic for an imported file selects that document and reveals
// the error line in the source pane.
func TestRevealCaddyDiagnostic_SwitchesToImportDoc(t *testing.T) {
	fs := fsReader(map[string]string{
		"config/Caddyfile":           "example.test {\n\timport snippets/auth.caddy\n}\n",
		"config/snippets/auth.caddy": "reverse_proxy @phantom localhost:8080\n",
	})
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: true}, fs)
	m := newLoadedModel(t, loader)
	_ = m.View()
	d := validator.Diagnostic{Path: "config/snippets/auth.caddy", Line: 1, Message: "boom", Severity: validator.SeverityError}
	if !m.revealCaddyDiagnostic(d) {
		t.Fatal("diagnostic should resolve to the imported document")
	}
	sel := m.selectedItem()
	if sel == nil || sel.doc == nil || sel.doc.Path != "config/snippets/auth.caddy" {
		t.Fatalf("selection = %v, want the imported document selected", sel)
	}
	if m.sourceRevealLine != 1 {
		t.Errorf("sourceRevealLine = %d, want 1", m.sourceRevealLine)
	}
}

// TestVFromReview_ReturnsToReviewWithRows verifies the end-to-end v flow
// launched from the review: the review reopens with the caddy outcome
// listed as per-diagnostic rows (the modal is never forced open).
func TestVFromReview_ReturnsToReviewWithRows(t *testing.T) {
	src := "example.test {\n\treverse_proxy @phantom localhost:8080\n}\n"
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 0, Message: "unrecognized matcher name: @phantom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{formatted: []byte(src), diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if !m.showInlineReview {
		t.Fatal("i should open the review")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = keyPress(t, m, cmd())
	if !m.showInlineReview {
		t.Fatal("v from the review should return to the review")
	}
	if m.showDiagnostics {
		t.Error("v from the review must not open the diagnostics modal")
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "E error") || !strings.Contains(view, "line 2") {
		t.Errorf("review should list the caddy diagnostic with its line, got:\n%s", view)
	}
}

// TestCaddyReviewRowCount covers the nil, per-diagnostic and state branches
// of the review row-count helper.
func TestCaddyReviewRowCount(t *testing.T) {
	m := matcherModel(t, "example.test {\n}\n")
	m.inlineCaddy = nil
	if got := m.caddyReviewRowCount(); got != 1 {
		t.Errorf("nil caddy = %d, want 1", got)
	}
	m.inlineCaddy = &inlineCaddyState{
		phase: "result",
		details: []validator.Diagnostic{
			{Line: 1, Message: "a"},
			{Line: 2, Message: "b"},
		},
	}
	if got := m.caddyReviewRowCount(); got != 2 {
		t.Errorf("two diagnostics = %d, want 2", got)
	}
	m.inlineCaddy = &inlineCaddyState{phase: "stale"}
	if got := m.caddyReviewRowCount(); got != 1 {
		t.Errorf("stale state = %d, want 1", got)
	}
}

// TestOpenCaddyReviewDetail_Guards covers the no-op branches of → on a Caddy
// row: advisory cursor, nil caddy, non-result phase and an out-of-range
// cursor never open the detail.
func TestOpenCaddyReviewDetail_Guards(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @phantom localhost:8080\n}\n")
	m.showInlineReview = true
	m.inlineFindings = []caddyfile.InlineFinding{{StartLine: 2, Start: 1, End: 5}}
	m.inlineCaddy = &inlineCaddyState{
		phase:   "result",
		details: []validator.Diagnostic{{Path: "config/Caddyfile", Line: 2, Message: "boom"}},
	}
	// → on an advisory row: no-op.
	m.inlineReviewCursor = 0
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if updated.(*Model).showDiagnostics {
		t.Error("right on an advisory row must be a no-op")
	}
	// nil caddy: no-op.
	m.inlineCaddy = nil
	m.inlineReviewCursor = 1
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if updated.(*Model).showDiagnostics {
		t.Error("right with nil caddy must be a no-op")
	}
	// phase != result: no-op.
	m.inlineCaddy = &inlineCaddyState{phase: "stale", details: []validator.Diagnostic{{Line: 1}}}
	m.inlineReviewCursor = 1
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if updated.(*Model).showDiagnostics {
		t.Error("right on a stale row must be a no-op")
	}
	// cursor beyond the details: no-op.
	m.inlineCaddy = &inlineCaddyState{phase: "result", details: []validator.Diagnostic{{Line: 1}}}
	m.inlineReviewCursor = 2 // beyond the single detail row
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if updated.(*Model).showDiagnostics {
		t.Error("right with an out-of-range cursor must be a no-op")
	}
}

// TestRevealCaddyDiagnostic_Branches covers the fallback paths of the
// reveal: no graph, unknown document, unpinnable line and the tree-row
// selection variants.
func TestRevealCaddyDiagnostic_Branches(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @phantom localhost:8080\n}\n")
	graph := m.state.Graph

	// No graph with a line: reveals without touching the tree.
	m.state.Graph = nil
	if !m.revealCaddyDiagnostic(validator.Diagnostic{Path: "x", Line: 3, Message: "m"}) || m.sourceRevealLine != 3 {
		t.Errorf("no-graph with line: want reveal with line 3")
	}
	// No graph without a line: cannot resolve.
	if m.revealCaddyDiagnostic(validator.Diagnostic{Path: "x", Message: "m"}) {
		t.Error("no-graph without a line must fail")
	}
	m.state.Graph = graph

	// Unknown document: cannot resolve.
	if m.revealCaddyDiagnostic(validator.Diagnostic{Path: "no/such.conf", Line: 1, Message: "m"}) {
		t.Error("unknown document must fail")
	}
	// docAtPath with a nil graph returns nil.
	m.state.Graph = nil
	if m.docAtPath("config/Caddyfile") != nil {
		t.Error("docAtPath must return nil without a graph")
	}
	m.state.Graph = graph
	if m.docAtPath("no/such.conf") != nil {
		t.Error("docAtPath must return nil for an unknown path")
	}

	// Unpinnable token: cannot resolve and nothing is revealed.
	if m.revealCaddyDiagnostic(validator.Diagnostic{Path: "config/Caddyfile", Message: "unknown directive: zzz_absent"}) {
		t.Error("unpinnable diagnostic must fail")
	}

	// A leaf inside the site block resolves through the nearest visible
	// ancestor (the site row); the pin lands on the token's line.
	m.sourceRevealLine = 0
	if !m.revealCaddyDiagnostic(validator.Diagnostic{Path: "config/Caddyfile", Message: "unrecognized matcher name: @phantom"}) {
		t.Fatal("pinnable diagnostic must resolve")
	}
	if m.sourceRevealLine != 2 {
		t.Errorf("pinned reveal line = %d, want 2", m.sourceRevealLine)
	}
	if got := m.caddyDiagDisplayLine(validator.Diagnostic{Path: "no/such.conf", Message: "x"}); got != 0 {
		t.Errorf("display line for an unknown doc = %d, want 0", got)
	}
}

// TestRevealCaddyDiagnostic_TreeRow verifies the reveal selects a structural
// tree row directly when the diagnostic line is a block header.
func TestRevealCaddyDiagnostic_TreeRow(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	// Line 1 is the site block header, a visible tree row.
	if !m.revealCaddyDiagnostic(validator.Diagnostic{Path: "config/Caddyfile", Line: 1, Message: "boom"}) {
		t.Fatal("site-header diagnostic must resolve")
	}
	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.node.Name != "example.test" {
		t.Errorf("selection = %v, want the site block row", sel)
	}
	if m.sourceRevealLine != 1 {
		t.Errorf("reveal line = %d, want 1", m.sourceRevealLine)
	}
}

// TestRevealFirstCaddyError_NoOutcome verifies the empty guards of the
// auto-reveal: nil outcome and no resolvable diagnostic leave the selection
// untouched.
func TestRevealFirstCaddyError_NoOutcome(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.inlineCaddy = nil
	m.revealFirstCaddyError() // must not panic
	m.inlineCaddy = &inlineCaddyState{
		phase: "result",
		details: []validator.Diagnostic{
			{Path: "config/Caddyfile", Message: "no token here"},
			{Path: "no/such.conf", Line: 1, Message: "unknown doc"},
		},
	}
	m.sourceRevealLine = 0
	m.revealFirstCaddyError()
	if m.sourceRevealLine != 0 {
		t.Errorf("unresolvable diagnostics must not reveal, got %d", m.sourceRevealLine)
	}
}

// TestCaddyDiagPathLabel_NoConfig verifies the full-path fallback when the
// root config path is not known.
func TestCaddyDiagPathLabel_NoConfig(t *testing.T) {
	m := matcherModel(t, "example.test {\n}\n")
	m.state.Settings.ConfigPath = ""
	if got := m.caddyDiagPathLabel(validator.Diagnostic{Path: "/etc/caddy/snippets/auth.caddy"}); got != "/etc/caddy/snippets/auth.caddy" {
		t.Errorf("label without a config path = %q, want the full path", got)
	}
}

func TestInlineReviewLabel_Unknown(t *testing.T) {
	if got := inlineReviewLabel(caddyfile.InlineSeverity(99)); got != "? unknown" {
		t.Errorf("unknown severity label = %q, want '? unknown'", got)
	}
}

// TestInlineReviewLabel_UsesGutterBadges verifies the advisory markers in
// the review use the same background badges as the source-pane gutter.
func TestInlineReviewLabel_UsesGutterBadges(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	// Render the labels first, then resolve the badge SGRs: sgrOf restores
	// the Ascii profile after each call.
	hint := inlineReviewLabel(caddyfile.SeverityAdvisoryHint)
	info := inlineReviewLabel(caddyfile.SeverityAdvisoryInfo)
	hintSgr := sgrOf(gutterHintBadgeStyle)
	infoSgr := sgrOf(gutterInfoBadgeStyle)
	if !strings.Contains(hint, hintSgr) || !strings.Contains(stripANSI(hint), "hint") {
		t.Errorf("hint label = %q, want the amber gutter badge + word", hint)
	}
	if !strings.Contains(info, infoSgr) || !strings.Contains(stripANSI(info), "info") {
		t.Errorf("info label = %q, want the blue gutter badge + word", info)
	}
}

func TestInlineReview_Reveal_Fallback(t *testing.T) {
	// revealInlineFinding with no tree graph still reveals the line.
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m.state.Graph = nil
	m.inlineFindings = []caddyfile.InlineFinding{{StartLine: 2, Start: 16, End: 20}}
	m.inlineReviewCursor = 0
	m.revealInlineFinding()
	if m.sourceRevealLine != 2 {
		t.Errorf("sourceRevealLine = %d, want 2 with no graph", m.sourceRevealLine)
	}
}

func TestInlineReview_Reveal_NoStructuralNode(t *testing.T) {
	// A finding line outside every structural node re-anchors the tree on
	// the document row via the reconstruct fallback.
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m.inlineFindings = []caddyfile.InlineFinding{{StartLine: 99, Start: 12, End: 13}}
	m.inlineReviewCursor = 0
	m.revealInlineFinding()
	if m.sourceRevealLine != 99 {
		t.Errorf("sourceRevealLine = %d, want 99", m.sourceRevealLine)
	}
	sel := m.selectedItem()
	if sel == nil || sel.key != itemKey(m.sourceDoc, nil) {
		t.Errorf("fallback should select the document row, got %v", sel)
	}
}

func TestInlineReview_Up_AtTopIsNoop(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n\t@u path /u/*\n}\n")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m.inlineReviewCursor = 0
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp}) // up at 0: no-op
	if m.inlineReviewCursor != 0 {
		t.Errorf("cursor after up-at-top = %d, want 0", m.inlineReviewCursor)
	}
}

func TestInlineReview_Reveal_StructuralNode(t *testing.T) {
	// A finding on the header line of a block node (reverse_proxy with a
	// block, which is a tree row) re-anchors the tree directly on that node.
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost:8080 {\n\t\theader_up Host {host}\n\t}\n}\n")
	m.inlineFindings = []caddyfile.InlineFinding{{StartLine: 2, Start: 16, End: 20}}
	m.inlineReviewCursor = 0
	m.revealInlineFinding()
	if m.sourceRevealLine != 2 {
		t.Errorf("sourceRevealLine = %d, want 2", m.sourceRevealLine)
	}
}

func TestInlineReviewFooter_NotRun(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.showInlineReview = true
	m.inlineCaddy = &inlineCaddyState{phase: "not run"}
	block := m.inlineCaddyBlock(5)
	if !strings.Contains(stripANSI(block), "not run") {
		t.Errorf("caddy block with no run = %q, want 'not run'", block)
	}
}

func TestCommandPalette_ReviewInlineFindings(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !strings.Contains(stripANSI(m.View()), "Review inline findings") {
		t.Errorf("palette should list 'Review inline findings':\n%s", m.View())
	}
	if strings.Contains(stripANSI(m.View()), "Toggle inline findings") {
		t.Errorf("palette must not list the old toggle name")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if !m.showInlineReview {
		t.Error("i keybinding should open the review view")
	}
	cmd, ok := m.commandForKey("i")
	if !ok || cmd != commandReviewInline {
		t.Errorf("commandForKey(i) = %q, %v; want review-inline", cmd, ok)
	}

	// The palette entry is gated on a selected document: without one it
	// reports the disabled reason instead of pretending it can run.
	cmdDef, ok := commandDefinition(commandReviewInline)
	if !ok {
		t.Fatal("review-inline command not found in the catalog")
	}
	m.sourceDoc = nil
	if cmdDef.Enabled(m) {
		t.Errorf("review command should be disabled with no document selected")
	}
	if got := cmdDef.Reason(m); got != "no document selected" {
		t.Errorf("review command Reason = %q, want 'no document selected'", got)
	}
}

// TestInlineReviewView_Sections renders the review and checks the ADVISORY and
// CADDY VALIDATION sections are separate and present.
func TestInlineReviewView_Sections(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m.syncInlineFindings(m.sourceDoc)
	m.showInlineReview = true
	m.inlineReviewCursor = 0
	m.inlineCaddy = &inlineCaddyState{phase: "not run"}
	view := stripANSI(m.inlineReviewView(50, 20))
	if !strings.Contains(view, "Inline findings (1)") {
		t.Errorf("view missing title with count:\n%s", view)
	}
	if !strings.Contains(view, "ADVISORY") || !strings.Contains(view, "CADDY VALIDATION") {
		t.Errorf("view missing advisory/caddy sections:\n%s", view)
	}
	if !strings.Contains(view, "not run") {
		t.Errorf("view should show 'not run' before v:\n%s", view)
	}
	// The header must carry only the view name and count; all commands live in
	// the system footer, so hints like close/move/validate are not duplicated.
	if strings.Contains(view, "[i] close") || strings.Contains(view, "↑/↓ move") || strings.Contains(view, "v validate") {
		t.Errorf("view header must not duplicate footer commands:\n%s", view)
	}
}

// TestInlineReview_CaddyResultPersistsAcrossReopen verifies the Caddy outcome
// survives closing and reopening the review view.
func TestInlineReview_CaddyResultPersistsAcrossReopen(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	// Simulate a failing validate outcome.
	m.inlineCaddy = &inlineCaddyState{
		phase: "result", errors: 1, summary: "unrecognized matcher name: @phantom",
		details: []validator.Diagnostic{{Path: "config/Caddyfile", Line: 2, Message: "unrecognized matcher name: @phantom"}},
		source:  m.sourceDoc.Source,
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if !m.showInlineReview {
		t.Fatal("i should open the review")
	}
	// Close, then reopen: the outcome must still be there.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if m.inlineCaddy == nil || m.inlineCaddy.phase != "result" || m.inlineCaddy.errors != 1 {
		t.Errorf("caddy outcome lost across reopen: %+v", m.inlineCaddy)
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "unrecognized matcher name: @phantom") {
		t.Errorf("review should show the caddy error summary on re-open:\n%s", view)
	}
}

// TestInlineReview_EnterOnCaddyRowReveals verifies Enter on a Caddy
// diagnostic row selects the diagnostic's document and reveals its line in
// the source pane (closing the review), mirroring the advisory rows.
func TestInlineReview_EnterOnCaddyRowReveals(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @phantom localhost:8080\n}\n")
	m.showInlineReview = true
	m.inlineReviewCursor = len(m.inlineFindings) // the first Caddy row
	m.inlineCaddy = &inlineCaddyState{
		phase: "result", errors: 1, summary: "boom",
		details: []validator.Diagnostic{{Path: "config/Caddyfile", Line: 2, Message: "boom"}},
		source:  m.sourceDoc.Source,
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	rm := updated.(*Model)
	if rm.showInlineReview {
		t.Error("Enter on the Caddy row should leave the review view")
	}
	if rm.showDiagnostics {
		t.Error("Enter on the Caddy row must not open the diagnostics modal")
	}
	if rm.sourceRevealLine != 2 {
		t.Errorf("Enter on the Caddy row should reveal the error line, got %d", rm.sourceRevealLine)
	}
}

// TestInlineReview_CaddyStaleAfterSourceChange verifies a validated outcome is
// flagged stale once the selected document source changes.
func TestInlineReview_CaddyStaleAfterSourceChange(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m.inlineCaddy = &inlineCaddyState{phase: "result", errors: 1, summary: "x", source: append([]byte(nil), m.sourceDoc.Source...)}
	m.sourceDoc = caddyfile.Parse([]byte("example.test {\n\trespond ok\n}\n"))
	m.sourceDoc.Path = "config/Caddyfile"
	m.syncInlineFindings(m.sourceDoc)
	if m.inlineCaddy == nil || m.inlineCaddy.phase != "stale" {
		t.Errorf("caddy outcome should be flagged stale after source change, got %+v", m.inlineCaddy)
	}
}

// TestInlineReview_NavigationToCaddyRow verifies down/up movement reaches and
// leaves the Caddy validation row.
func TestInlineReview_NavigationToCaddyRow(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m.syncInlineFindings(m.sourceDoc) // 1 finding
	m.inlineCaddy = &inlineCaddyState{phase: "result", errors: 1, summary: "x"}
	m.showInlineReview = true
	m.inlineReviewCursor = 0
	// Row count = 1 finding + 1 Caddy row.
	if got := m.inlineReviewRowCount(); got != 2 {
		t.Fatalf("row count = %d, want 2", got)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.inlineReviewCursor != 1 {
		t.Errorf("cursor after down = %d, want 1 (the Caddy row)", m.inlineReviewCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.inlineReviewCursor != 1 {
		t.Errorf("cursor should clamp at the Caddy row, got %d", m.inlineReviewCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.inlineReviewCursor != 0 {
		t.Errorf("cursor after up = %d, want 0", m.inlineReviewCursor)
	}
}

// TestInlineCaddyBlock_States renders the Caddy validation section for the
// not-run, result-errors, result-clean and stale states.
func TestInlineCaddyBlock_States(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.showInlineReview = true
	m.inlineFindings = nil
	m.inlineReviewCursor = 0 // select the (sole) Caddy row

	// clean result
	m.inlineCaddy = &inlineCaddyState{phase: "result", errors: 0, summary: ""}
	if got := stripANSI(m.inlineCaddyBlock(5)); !strings.Contains(got, "clean") {
		t.Errorf("clean caddy block = %q, want clean", got)
	}
	// errors result: one row per diagnostic, with its line.
	m.inlineCaddy = &inlineCaddyState{
		phase: "result", errors: 2, summary: "x",
		details: []validator.Diagnostic{
			{Path: "config/Caddyfile", Line: 2, Message: "first"},
			{Path: "config/Caddyfile", Line: 5, Message: "second"},
		},
	}
	if got := stripANSI(m.inlineCaddyBlock(5)); !strings.Contains(got, "E error · line 2") || !strings.Contains(got, "E error · line 5") {
		t.Errorf("errors caddy block = %q, want one row per diagnostic", got)
	}
	// stale result
	m.inlineCaddy = &inlineCaddyState{phase: "stale", errors: 0}
	if got := stripANSI(m.inlineCaddyBlock(5)); !strings.Contains(got, "stale") {
		t.Errorf("stale caddy block = %q, want stale", got)
	}
	// not run
	m.inlineCaddy = &inlineCaddyState{phase: "not run"}
	if got := stripANSI(m.inlineCaddyBlock(5)); !strings.Contains(got, "not run") {
		t.Errorf("not-run caddy block = %q, want not run", got)
	}
}

// TestInlineCaddyBlock_UnselectedNoErrorStyle verifies that the error marker
// stays visually distinct and is not forced onto the advisory text (the Caddy
// error keeps its own error color, separate from advisory hint/info).
func TestInlineCaddyBlock_ErrorMarkerDistinct(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.inlineCaddy = &inlineCaddyState{
		phase: "result", errors: 1, summary: "boom",
		details: []validator.Diagnostic{{Path: "config/Caddyfile", Line: 1, Message: "boom"}},
	}
	m.inlineFindings = nil
	m.inlineReviewCursor = -1 // unselected row
	block := m.inlineCaddyBlock(5)
	if !strings.Contains(block, sgrOf(errorStyle)) {
		t.Errorf("caddy error block should carry the error style marker, got:\n%s", block)
	}
	if strings.Contains(block, sgrOf(syntaxInlineHintStyle)) {
		t.Errorf("caddy error should not reuse the advisory hint style:\n%s", block)
	}
}

// TestInlineReview_EnterOnStateRowCloses verifies Enter on a Caddy state row
// (not run / stale / clean, no diagnostics to reveal) simply closes the
// review without revealing or opening a view.
func TestInlineReview_EnterOnStateRowCloses(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.showInlineReview = true
	m.inlineCaddy = &inlineCaddyState{phase: "result", errors: 1, summary: "x"}
	m.inlineCaddy.details = nil // no retained diagnostics
	m.inlineReviewCursor = len(m.inlineFindings)
	m.diagnostics = nil
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	rm := updated.(*Model)
	if rm.showInlineReview {
		t.Error("Enter on a state row should close the review")
	}
	if rm.showDiagnostics {
		t.Error("a state row must not open the diagnostics view")
	}
	if rm.sourceRevealLine != 0 {
		t.Errorf("a state row must not reveal a line, got %d", rm.sourceRevealLine)
	}
}

// TestFirstErrMessage covers the empty-diagnostics branch.
func TestFirstErrMessage(t *testing.T) {
	if got := firstErrMessage(nil); got != "" {
		t.Errorf("firstErrMessage(nil) = %q, want empty", got)
	}
	if got := firstErrMessage([]validator.Diagnostic{{Message: "boom"}}); got != "boom" {
		t.Errorf("firstErrMessage with one = %q, want boom", got)
	}
}

// TestInlineReviewFooter_Clean verifies the advisory footer with no findings.
func TestInlineReviewFooter_Clean(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.syncInlineFindings(m.sourceDoc)
	m.showInlineReview = true
	footer := m.inlineReviewFooter()
	if !strings.Contains(footer, "Advisory: 0 hint · 0 info") {
		t.Errorf("clean advisory footer = %q", footer)
	}
}

// TestInlineReview_RightOpensDetailLeftReturns verifies the →/← convention on
// a Caddy diagnostic row: → opens the full diagnostic detail (positioned on
// that diagnostic) and ← from the detail returns straight to the review list.
func TestInlineReview_RightOpensDetailLeftReturns(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @phantom localhost:8080\n}\n")
	m = resize(m, 120, 30)
	m.inlineCaddy = &inlineCaddyState{
		phase: "result", errors: 2, summary: "boom",
		details: []validator.Diagnostic{
			{Path: "config/Caddyfile", Line: 2, Message: "first"},
			{Path: "config/Caddyfile", Line: 5, Message: "second"},
		},
	}
	m.showInlineReview = true
	m.inlineReviewCursor = len(m.inlineFindings) + 1 // the second Caddy row

	// → opens the detail for the selected diagnostic.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(*Model)
	if !m.showDiagnostics || !m.showDetail {
		t.Fatal("Right on a Caddy row should open the diagnostic detail")
	}
	if m.diagCursor != 1 {
		t.Errorf("diagCursor = %d, want 1 (the selected diagnostic)", m.diagCursor)
	}
	if m.inlineReviewReturn != true {
		t.Fatal("closing the detail should restore the review")
	}
	if !strings.Contains(stripANSI(m.detailViewport.View()), "second") {
		t.Errorf("detail should show the selected diagnostic, got:\n%s", stripANSI(m.detailViewport.View()))
	}

	// ← from the detail returns directly to the review list.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if !m.showInlineReview {
		t.Error("Left from the detail should return to the review list")
	}
	if m.showDiagnostics || m.showDetail {
		t.Error("the diagnostics modal and detail should be closed")
	}
	if m.inlineReviewReturn {
		t.Error("inlineReviewReturn should be cleared after restoring the review")
	}
}

// TestInlineReview_ReturnAfterValidateClean verifies that a caddy validate
// launched from the review with no errors reopens the review immediately to
// show the clean outcome.
func TestInlineReview_ReturnAfterValidateClean(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	// Simulate a clean validate outcome with no diagnostics opened.
	m.showInlineReview = true
	m.inlineReviewReturn = true
	m.showDiagnostics = false
	m.setInlineCaddyOutcome(0, true, m.sourceDoc.Source, "", nil)
	m.returnToInlineReviewIfNeeded()
	if !m.showInlineReview {
		t.Error("a clean validate from the review should restore the review view")
	}
	if m.inlineReviewReturn {
		t.Error("inlineReviewReturn should be cleared after a clean result")
	}
}

// TestInlineReview_ReturnDeferredWhileDiagnosticsOpen verifies that when the
// validation opens the diagnostics view, the review restore is deferred until
// that view closes.
func TestInlineReview_ReturnDeferredWhileDiagnosticsOpen(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	// v closed the review and the validation opened the diagnostics view.
	m.showInlineReview = false
	m.inlineReviewReturn = true
	m.showDiagnostics = true // diagnostics opened with the errors
	m.returnToInlineReviewIfNeeded()
	if m.showInlineReview {
		t.Error("review must not reopen while the diagnostics view is open")
	}
	if !m.inlineReviewReturn {
		t.Error("return flag must stay set until the diagnostics view closes")
	}
}

// TestInlineReview_Reveal_CursorOutOfRange verifies revealInlineFinding is a
// safe no-op when the review cursor points outside the findings list.
func TestInlineReview_Reveal_CursorOutOfRange(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m.inlineFindings = []caddyfile.InlineFinding{{StartLine: 2, Start: 16, End: 20}}
	before := m.sourceRevealLine
	m.inlineReviewCursor = -1
	m.revealInlineFinding()
	m.inlineReviewCursor = len(m.inlineFindings) // beyond the last index
	m.revealInlineFinding()
	if m.sourceRevealLine != before {
		t.Errorf("sourceRevealLine changed (%d -> %d) for an out-of-range cursor", before, m.sourceRevealLine)
	}
}

// TestInlineReviewView_Clamped verifies the review view degrades gracefully on
// tiny widths/heights (the content width and body height are clamped to 1).
func TestInlineReviewView_Clamped(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m.syncInlineFindings(m.sourceDoc)
	m.showInlineReview = true
	m.inlineReviewCursor = 0
	m.inlineCaddy = &inlineCaddyState{phase: "not run"}
	view := m.inlineReviewView(0, 0)
	if view == "" {
		t.Error("review view returned empty on a zero-sized terminal")
	}
	// A very small height also exercises the bodyH clamp.
	view2 := m.inlineReviewView(30, 2)
	if view2 == "" {
		t.Error("review view returned empty on a 2-row terminal")
	}
}

// TestInlineAdvisoryBlock_CursorBeyondEnd exercises the clamp when the review
// cursor exceeds the findings list length (e.g. after the tree shrank). It
// must not panic; the block may legitimately be empty.
func TestInlineAdvisoryBlock_CursorBeyondEnd(t *testing.T) {
	m := matcherModel(t, "example.test {\n\treverse_proxy @api localhost\n}\n")
	m.syncInlineFindings(m.sourceDoc)
	if len(m.inlineFindings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(m.inlineFindings))
	}
	m.inlineReviewCursor = 10     // beyond the single finding
	_ = m.inlineAdvisoryBlock(10) // must not panic
}

// TestInlineCaddyBlock_NoSummary covers the Caddy error block when the outcome
// carries no readable summary text.
func TestInlineCaddyRow_ShowsMessage(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.inlineFindings = nil
	m.showInlineReview = true
	m.inlineCaddy = &inlineCaddyState{
		phase: "result", errors: 2, summary: "",
		details: []validator.Diagnostic{{Path: "config/Caddyfile", Line: 3, Message: "boom"}},
	}
	block := m.inlineCaddyBlock(5)
	if !strings.Contains(stripANSI(block), "line 3") {
		t.Errorf("caddy block without summary = %q, want the diagnostic line", block)
	}
	if !strings.Contains(stripANSI(block), "boom") {
		t.Errorf("caddy block = %q, want the diagnostic message", block)
	}
}

// TestInlineCaddyBlock_CleanAndStaleSelected covers the selected Caddy clean and
// stale rows.
func TestInlineCaddyBlock_CleanAndStaleSelected(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.inlineFindings = nil
	m.showInlineReview = true

	// Clean result, cursor on the (sole) Caddy row.
	m.inlineCaddy = &inlineCaddyState{phase: "result", errors: 0, summary: ""}
	m.inlineReviewCursor = 0
	if got := stripANSI(m.inlineCaddyBlock(5)); !strings.Contains(got, "clean") {
		t.Errorf("selected clean caddy block = %q, want clean", got)
	}
	// Stale result, cursor on the Caddy row.
	m.inlineCaddy = &inlineCaddyState{phase: "stale", errors: 0}
	if got := stripANSI(m.inlineCaddyBlock(5)); !strings.Contains(got, "stale") {
		t.Errorf("selected stale caddy block = %q, want stale", got)
	}
}

// TestInlineReviewFooter_WithInfo verifies the advisory footer counts an info
// finding as well (covering the info branch of the counting loop).
func TestInlineReviewFooter_WithInfo(t *testing.T) {
	m := matcherModel(t, "example.test {\n\t@unused path /x/*\n}\n")
	m.syncInlineFindings(m.sourceDoc) // 1 info finding
	m.showInlineReview = true
	footer := m.inlineReviewFooter()
	if !strings.Contains(footer, "Advisory: 0 hint · 1 info") {
		t.Errorf("info advisory footer = %q", footer)
	}
}

// TestInlineCaddyBlock_WindowClamp verifies the row window clamps to the
// details length when the cursor sits past the visible window (the end
// clamp; the start can never exceed the list because the cursor is bounded
// by the review row count).
func TestInlineCaddyBlock_WindowClamp(t *testing.T) {
	m := matcherModel(t, "example.test {\n\trespond ok\n}\n")
	m.inlineFindings = nil
	details := make([]validator.Diagnostic, 10)
	for i := range details {
		details[i] = validator.Diagnostic{Path: "config/Caddyfile", Line: i + 1, Message: "m"}
	}
	m.inlineCaddy = &inlineCaddyState{phase: "result", details: details}
	m.showInlineReview = true
	// Cursor on the last row: the window starts inside the list and the end
	// clamps to the details length, rendering the tail rows.
	m.inlineReviewCursor = len(details) - 1
	block := stripANSI(m.inlineCaddyBlock(4))
	if got := strings.Count(block, "E error"); got != 3 {
		t.Errorf("windowed block shows %d rows, want 3 (tail of 10 with a 4-row window):\n%s", got, block)
	}
}
