package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestModelFormatAndValidate_DisabledWithoutGraph(t *testing.T) {
	formatter := &fakeFormatter{formatted: []byte("x")}
	m := New(fakeLoader{state: nil, err: &noSuchFile{path: "missing"}}, formatter, nil, nil, nil, nil, nil, nil, testVersion, nil, nil, nil)
	m.Load()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd != nil {
		t.Errorf("expected nil cmd when no state, got %v", cmd)
	}
	if formatter.calls != 0 {
		t.Errorf("formatter.calls = %d, want 0 when no state is loaded", formatter.calls)
	}
}

func TestModelFormatAndValidate_NoFormatterShowsHint(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}) // no formatter
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd != nil {
		t.Errorf("expected nil cmd when formatter is nil, got %v", cmd)
	}
	if !strings.Contains(m.statusMessage, "caddy binary not configured") {
		t.Errorf("statusMessage = %q, want hint about caddy binary", m.statusMessage)
	}
	if m.busy {
		t.Error("busy = true, want false when formatter is nil")
	}
}

func TestModelFormatAndValidate_InvokesFormatter(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("formatted")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd == nil {
		t.Fatal("expected tea.Cmd from v keypress")
	}
	if !m.busy {
		t.Error("busy = false, want true while invocation is in flight")
	}
	msg := cmd()
	result, ok := msg.(formatAndValidateResultMsg)
	if !ok {
		t.Fatalf("got %T, want formatAndValidateResultMsg", msg)
	}
	if string(result.Formatted) != "formatted" {
		t.Errorf("Formatted = %q, want formatted", result.Formatted)
	}
	if formatter.calls != 1 {
		t.Errorf("formatter.calls = %d, want 1", formatter.calls)
	}
	if formatter.capturedDisplayPath != "config/Caddyfile" {
		t.Errorf("displayPath = %q, want config/Caddyfile (real path must be surfaced, not a temp path)", formatter.capturedDisplayPath)
	}
}

func TestModelFormatAndValidate_SuccessStoresWorkingCopy(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("formatted")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	msg := cmd()
	m.Update(msg) // process the result
	if string(m.workingBytes) != "formatted" {
		t.Errorf("workingBytes = %q, want formatted", m.workingBytes)
	}
	if !strings.Contains(m.statusMessage, "validated") {
		t.Errorf("statusMessage = %q, want it to mention 'validated'", m.statusMessage)
	}
	if !strings.HasPrefix(m.statusMessage, "✓") {
		t.Errorf("statusMessage = %q, want it to start with the success glyph", m.statusMessage)
	}
	if m.showDiagnostics {
		t.Error("showDiagnostics = true, want false on success")
	}
	if m.busy {
		t.Error("busy = true, want false after result delivery")
	}
}

func TestModelFormatAndValidate_FailureRevealsFirstError(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{
		formatted:   []byte("formatted working copy"),
		diagnostics: diags,
		err:         errors.New("caddy exit 1"),
	}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	msg := cmd()
	m.Update(msg) // process the result
	// A failed v no longer forces the diagnostics modal open: it reveals the
	// first authoritative error in the source pane instead, so the operator
	// does not have to hunt. The list stays available in the i review.
	if m.showDiagnostics {
		t.Fatal("showDiagnostics = true, want false: v must not force the modal")
	}
	if m.sourceRevealLine != 1 {
		t.Errorf("sourceRevealLine = %d, want 1 (first error line revealed)", m.sourceRevealLine)
	}
	if string(m.workingBytes) != "formatted working copy" {
		t.Errorf("workingBytes = %q, want formatted working copy", m.workingBytes)
	}
	if !strings.Contains(m.statusMessage, "validation failed") {
		t.Errorf("statusMessage = %q, want validation failure state", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "working copy retained") {
		t.Errorf("statusMessage = %q, want retained working copy state", m.statusMessage)
	}
	// The outcome is retained for the i review and the source overlay.
	if m.inlineCaddy == nil || m.inlineCaddy.phase != "result" || len(m.inlineCaddy.details) != 1 {
		t.Errorf("inline outcome not retained after v failure: %+v", m.inlineCaddy)
	}
	if got := m.caddyDiagsForDoc(m.selectedItem().doc); len(got) != 1 {
		t.Errorf("source overlay diags = %d, want 1 for the root document", len(got))
	}
}

func TestModelFormatAndValidate_FailureEmptyDiags(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	formatter := &fakeFormatter{err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	msg := cmd()
	m.Update(msg)
	if m.showDiagnostics {
		t.Error("showDiagnostics = true, want false when no diagnostics were parsed")
	}
	if !strings.HasPrefix(m.statusMessage, "✗") {
		t.Errorf("statusMessage = %q, want it to start with the error glyph", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "caddy exit 1") {
		t.Errorf("statusMessage = %q, want it to include 'caddy exit 1'", m.statusMessage)
	}
}

func TestModelDiagnosticsList_Navigation(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "p", Line: 1, Message: "first", Severity: validator.SeverityError},
		{Path: "p", Line: 2, Message: "second", Severity: validator.SeverityError},
		{Path: "p", Line: 3, Message: "third", Severity: validator.SeverityError},
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	// The diagnostics modal is now reachable from the delete/edit flows and
	// the review detail (→), not from a plain v: drive it directly.
	m.diagnostics = diags
	m.diagCursor = 0
	m.showDiagnostics = true
	if !m.showDiagnostics {
		t.Fatal("modal not open")
	}
	if m.diagCursor != 0 {
		t.Fatalf("diagCursor = %d, want 0 on open", m.diagCursor)
	}
	// j moves down
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.diagCursor != 1 {
		t.Errorf("diagCursor = %d, want 1 after j", m.diagCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.diagCursor != 2 {
		t.Errorf("diagCursor = %d, want 2 after second j", m.diagCursor)
	}
	// Clamp at the end
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if m.diagCursor != 2 {
		t.Errorf("diagCursor = %d, want 2 (clamped at end)", m.diagCursor)
	}
	// k moves up
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.diagCursor != 1 {
		t.Errorf("diagCursor = %d, want 1 after k", m.diagCursor)
	}
	// Arrow keys also work
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.diagCursor != 2 {
		t.Errorf("diagCursor = %d, want 2 after KeyDown", m.diagCursor)
	}
	// Esc closes
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showDiagnostics {
		t.Error("showDiagnostics = true, want false after Esc")
	}
	if len(m.diagnostics) != 0 {
		t.Errorf("diagnostics not cleared after Esc: %v", m.diagnostics)
	}
}

func TestModelFormatAndValidate_BusyIsIgnored(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("formatted")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	// First v starts the invocation.
	_, cmd1 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if !m.busy {
		t.Error("busy = false after first v, want true")
	}
	if cmd1 == nil {
		t.Fatal("first v must return a tea.Cmd")
	}
	// Second v while busy is a no-op.
	_, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd2 != nil {
		t.Error("second v must return nil cmd while busy")
	}
	if formatter.calls != 0 {
		t.Errorf("formatter.calls = %d before cmd1() executes, want 0", formatter.calls)
	}
	cmd1() // execute the first invocation
	if formatter.calls != 1 {
		t.Errorf("formatter.calls = %d, want exactly 1 (second v must not have triggered a call)", formatter.calls)
	}
}

func TestModelFormatAndValidate_NoExtraReads(t *testing.T) {
	// The v keypress must not touch the filesystem: format+validate is
	// an in-process call against the Formatter, the loader is the only
	// I/O path and it only reads.
	calls := map[string]int{}
	readFile := func(p string) ([]byte, error) {
		calls[p]++
		if p == "config/Caddyfile" {
			return []byte("example.test {\n}\n"), nil
		}
		return nil, &noSuchFile{p}
	}
	state := stateFor(t, "config/Caddyfile", readFile)
	formatter := &fakeFormatter{formatted: []byte("formatted")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m.View()
	beforeReads := calls["config/Caddyfile"]
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.View()
	m.Update(cmd())
	m.View()
	if got := calls["config/Caddyfile"] - beforeReads; got != 0 {
		t.Errorf("file reads triggered by v = %d, want 0 (no-write contract violated)", got)
	}
}

// TestModelFormatAndValidate_ZeroTimeoutDoesNotCancelContext verifies
// that the cmd wraps the formatter call in context.Background() when
// the operator did not pass --validator-timeout. Passing a zero
// duration to context.WithTimeout returns a context that is already
// past its deadline and would cancel the validator immediately,
// preventing its own 5s default from ever firing.
func TestModelFormatAndValidate_ZeroTimeoutDoesNotCancelContext(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("x")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	// m.validatorTimeout is the zero value because
	// Settings.ValidatorTimeout was not set.
	if m.validatorTimeout != 0 {
		t.Fatalf("precondition: m.validatorTimeout = %s, want 0", m.validatorTimeout)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if cmd == nil {
		t.Fatal("expected tea.Cmd from v keypress")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("expected message from cmd execution")
	}
	if formatter.capturedCtx == nil {
		t.Fatal("formatter did not capture context")
	}
	if err := formatter.capturedCtx.Err(); err != nil {
		t.Errorf("captured ctx is canceled (%v); zero ValidatorTimeout must leave the context un-canceled so the validator package can apply its own 5s default", err)
	}
}

// TestModelFormatAndValidate_InfoDiagnosticsFilteredOut verifies that
// the modal only surfaces error-level diagnostics. Caddy's validate
// output includes info-level log lines (e.g. "using config from
// file") that are not actionable and would otherwise clutter the
// modal. The handler filters to SeverityError before opening it.
func TestModelFormatAndValidate_InfoDiagnosticsFilteredOut(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "info noise", Severity: validator.SeverityInfo},
		{Path: "config/Caddyfile", Line: 47, Message: "module not registered", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	// A failed v does not open the modal; the outcome retains only the
	// error-level diagnostic for the review and the source overlay.
	if m.showDiagnostics {
		t.Fatal("v must not open the diagnostics modal")
	}
	if m.inlineCaddy == nil || len(m.inlineCaddy.details) != 1 {
		t.Fatalf("outcome details = %+v, want 1 (info must be filtered out)", m.inlineCaddy)
	}
	if m.inlineCaddy.details[0].Severity != validator.SeverityError {
		t.Errorf("filtered diagnostic severity = %v, want error", m.inlineCaddy.details[0].Severity)
	}
	if m.inlineCaddy.details[0].Line != 47 {
		t.Errorf("filtered diagnostic line = %d, want 47", m.inlineCaddy.details[0].Line)
	}
	if m.sourceRevealLine != 47 {
		t.Errorf("first error line not revealed, got %d", m.sourceRevealLine)
	}
	view := m.View()
	if strings.Contains(view, "info noise") {
		t.Errorf("View should not contain info diagnostic, but does:\n%s", view)
	}
	// The filtered error is listed in the i review, not in the source view.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	review := stripANSI(m.View())
	if !strings.Contains(review, "module not registered") {
		t.Errorf("review missing the error diagnostic:\n%s", review)
	}
	if strings.Contains(review, "info noise") {
		t.Errorf("review should not contain the info diagnostic:\n%s", review)
	}
}

// TestModelFormatAndValidate_AllInfoShowsStatusNotModal verifies the
// edge case where every diagnostic is info-level: the modal must not
// open (it would be empty) and the underlying error must surface in
// the status line instead.
func TestModelFormatAndValidate_AllInfoShowsStatusNotModal(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "noise 1", Severity: validator.SeverityInfo},
		{Path: "config/Caddyfile", Line: 2, Message: "noise 2", Severity: validator.SeverityInfo},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m.Update(cmd())
	if m.showDiagnostics {
		t.Error("showDiagnostics = true, want false when no errors after filtering")
	}
	if !strings.Contains(m.statusMessage, "✗") {
		t.Errorf("statusMessage = %q, want error status when all diags are info", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "caddy exit 1") {
		t.Errorf("statusMessage = %q, want it to include the underlying error", m.statusMessage)
	}
}

// TestModelDiagnosticsView_LongMessageTruncated verifies that an
// over-long diagnostic message is truncated to fit the modal
// width. Without truncation the body line would push past the
// right border, breaking the layout.
func TestModelDiagnosticsView_LongMessageTruncated(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	longMsg := strings.Repeat("a", 200)
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: longMsg, Severity: validator.SeverityError},
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 60, 20) // narrow window
	m = openDiagnosticsModal(m, diags)
	view := m.View()
	if !strings.Contains(view, "…") {
		t.Errorf("expected the long message to be truncated with '…', view:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(stripANSI(line)); w > 60 {
			t.Errorf("rendered line %d is %d columns wide, exceeds the 60-column window:\n%s", i+1, w, line)
		}
	}
}

// TestModelDiagnosticsDetail_EnterOpensDetail covers the primary
// keybinding for the detail view: pressing Enter on a diagnostic
// in the list opens its detail, which shows path, line, severity
// and the full message (no truncation).
func TestModelDiagnosticsDetail_EnterOpensDetail(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 47, Column: 1, Message: "module not registered: dns.providers.cloudflare", Severity: validator.SeverityError},
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	m = openDiagnosticsModal(m, diags)
	if !m.showDiagnostics {
		t.Fatal("modal must be open before opening detail")
	}
	if m.showDetail {
		t.Fatal("detail must not be open initially")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.showDetail {
		t.Error("showDetail = false after Enter, want true")
	}
	view := m.View()
	for _, want := range []string{
		"config/Caddyfile",
		"47",
		"module not registered: dns.providers.cloudflare",
		"error",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("View missing %q, got:\n%s", want, view)
		}
	}
}

// TestModelDiagnosticsDetail_PlusOpensDetail covers the '+' alias
// for Enter. It must open the detail view from the list and stay a
// no-op outside the diagnostics modal.
func TestModelDiagnosticsDetail_PlusOpensDetail(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "boom", Severity: validator.SeverityError},
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	m = openDiagnosticsModal(m, diags)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	if !m.showDetail {
		t.Error("showDetail = false after '+', want true ('+' is an alias for Enter)")
	}
}

// TestModelDiagnosticsDetail_EscReturnsToList verifies the first
// half of the Esc chain: from the detail view, Esc closes the
// detail but keeps the diagnostics modal open.
func TestModelDiagnosticsDetail_EscReturnsToList(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "boom", Severity: validator.SeverityError},
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	m = openDiagnosticsModal(m, diags)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // open detail
	if !m.showDetail {
		t.Fatal("detail should be open after Enter")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // back to list
	if m.showDetail {
		t.Error("showDetail = true after Esc from detail, want false")
	}
	if !m.showDiagnostics {
		t.Error("showDiagnostics = false after Esc from detail, want true (modal stays open)")
	}
}

// TestModelDiagnosticsDetail_EscClosesModal covers the second
// half of the Esc chain: from the list, Esc closes the modal
// entirely.
func TestModelDiagnosticsDetail_EscClosesModal(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: "boom", Severity: validator.SeverityError},
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	m = openDiagnosticsModal(m, diags)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // Esc from list
	if m.showDiagnostics {
		t.Error("showDiagnostics = true after Esc from list, want false")
	}
	if m.showDetail {
		t.Error("showDetail = true after Esc from list, want false")
	}
}

// TestModelDiagnosticsDetail_LongMessageWraps verifies that a long
// diagnostic message is wrapped to the available width in the
// detail view. No rendered line may exceed the window width, and
// the full message must remain visible (not truncated to '…').
func TestModelDiagnosticsDetail_LongMessageWraps(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	longMsg := strings.TrimRight(strings.Repeat("word ", 40), " ")
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: longMsg, Severity: validator.SeverityError},
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 60, 24)
	m = openDiagnosticsModal(m, diags)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	view := m.View()
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(stripANSI(line)); w > 60 {
			t.Errorf("rendered line %d is %d columns wide, exceeds the 60-column window:\n%s", i+1, w, line)
		}
	}
	if !strings.Contains(view, "word") {
		t.Errorf("View missing the message content, got:\n%s", view)
	}
	// The detail must not truncate the message with '…': the full
	// 200-char message should be visible in the body, even if it
	// requires scrolling to read it.
	if !strings.Contains(view, strings.Repeat("word ", 10)) {
		t.Errorf("View should show a long stretch of the message, got:\n%s", view)
	}
}

// TestModelDiagnosticsDetail_PgUpPgDownScroll verifies the page
// keys advance and retreat the detail viewport scroll.
func TestModelDiagnosticsDetail_PgUpPgDownScroll(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	longMsg := strings.TrimRight(strings.Repeat("lorem ipsum dolor sit amet ", 30), " ")
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: longMsg, Severity: validator.SeverityError},
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 60, 12) // short window so the body overflows
	m = openDiagnosticsModal(m, diags)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	initialY := m.detailViewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.detailViewport.YOffset <= initialY {
		t.Errorf("PgDown did not advance scroll: initial=%d, after=%d", initialY, m.detailViewport.YOffset)
	}
	afterPgDown := m.detailViewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.detailViewport.YOffset >= afterPgDown {
		t.Errorf("PgUp did not retreat scroll: afterPgDown=%d, after=%d", afterPgDown, m.detailViewport.YOffset)
	}
}

// TestModelDiagnosticsDetail_ArrowKeysScroll verifies that the
// arrow keys also scroll the detail viewport (line-by-line,
// independent of PgUp/PgDown).
func TestModelDiagnosticsDetail_ArrowKeysScroll(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	longMsg := strings.TrimRight(strings.Repeat("alpha beta gamma ", 30), " ")
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: longMsg, Severity: validator.SeverityError},
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 60, 12)
	m = openDiagnosticsModal(m, diags)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	initialY := m.detailViewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.detailViewport.YOffset <= initialY {
		t.Errorf("Down arrow did not advance scroll: initial=%d, after=%d", initialY, m.detailViewport.YOffset)
	}
	afterDown := m.detailViewport.YOffset
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.detailViewport.YOffset >= afterDown {
		t.Errorf("Up arrow did not retreat scroll: afterDown=%d, after=%d", afterDown, m.detailViewport.YOffset)
	}
}

// TestModelDiagnosticsDetail_ListStillTruncates is a regression
// test for the compact list view: the detail view is additive
// only. The list must still show the truncated message with '…'
// and the detail must show strictly more of the same message.
func TestModelDiagnosticsDetail_ListStillTruncates(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "garbage\n",
	}))
	longMsg := strings.Repeat("a", 200)
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Message: longMsg, Severity: validator.SeverityError},
	}
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 60, 24)
	m = openDiagnosticsModal(m, diags)
	listView := m.View()
	if !strings.Contains(listView, "…") {
		t.Errorf("list view should still truncate with '…', got:\n%s", listView)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	detailView := m.View()
	for _, want := range []string{"Path", "Line", "Severity"} {
		if !strings.Contains(detailView, want) {
			t.Errorf("detail view should show structured field %q, got:\n%s", want, detailView)
		}
	}
	// The detail body must contain strictly more 'a' characters
	// than the list body, since the list truncates the message
	// and the detail does not.
	listAs := strings.Count(listView, "a")
	detailAs := strings.Count(detailView, "a")
	if detailAs <= listAs {
		t.Errorf("detail view should show more of the message than the list: list=%d 'a's, detail=%d 'a's", listAs, detailAs)
	}
}
