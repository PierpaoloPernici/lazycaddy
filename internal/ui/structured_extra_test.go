package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	tea "github.com/charmbracelet/bubbletea"
)

// --- structuredInput unit tests -------------------------------------------

func TestStructuredInputKeyHandling(t *testing.T) {
	t.Run("inserts runes at the cursor", func(t *testing.T) {
		var i structuredInput
		i.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ab")})
		i.update(tea.KeyMsg{Type: tea.KeyLeft})
		i.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
		if got := i.String(); got != "aXb" {
			t.Errorf("String = %q, want aXb", got)
		}
	})
	t.Run("ignores empty runes", func(t *testing.T) {
		var i structuredInput
		i.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: nil})
		if got := i.String(); got != "" {
			t.Errorf("empty runes mutated the input to %q", got)
		}
	})
	t.Run("enforces the rune cap", func(t *testing.T) {
		i := structuredInput{value: make([]rune, maxStructuredInputRunes), cursor: maxStructuredInputRunes}
		i.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
		if got := len(i.value); got != maxStructuredInputRunes {
			t.Errorf("over-cap insert grew the input to %d runes", got)
		}
	})
	t.Run("truncates overflowing pastes", func(t *testing.T) {
		i := structuredInput{value: make([]rune, maxStructuredInputRunes-2), cursor: maxStructuredInputRunes - 2}
		i.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc")})
		if got := len(i.value); got != maxStructuredInputRunes {
			t.Errorf("overflow paste = %d runes, want the cap", got)
		}
	})
	t.Run("backspace, delete and arrows", func(t *testing.T) {
		var i structuredInput
		i.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc")})
		i.update(tea.KeyMsg{Type: tea.KeyLeft})
		i.update(tea.KeyMsg{Type: tea.KeyBackspace})
		if got := i.String(); got != "ac" {
			t.Errorf("backspace = %q, want ac", got)
		}
		i.update(tea.KeyMsg{Type: tea.KeyLeft})
		i.update(tea.KeyMsg{Type: tea.KeyDelete})
		if got := i.String(); got != "c" {
			t.Errorf("delete = %q, want c", got)
		}
		// Guards: backspace at the start and delete at the end are no-ops.
		i.update(tea.KeyMsg{Type: tea.KeyLeft})
		i.update(tea.KeyMsg{Type: tea.KeyBackspace})
		i.update(tea.KeyMsg{Type: tea.KeyEnd})
		i.update(tea.KeyMsg{Type: tea.KeyDelete})
		if got := i.String(); got != "c" {
			t.Errorf("guard edits changed the input to %q", got)
		}
		// Arrow right moves toward the end of the input.
		var j structuredInput
		j.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ab")})
		j.update(tea.KeyMsg{Type: tea.KeyHome})
		j.update(tea.KeyMsg{Type: tea.KeyRight})
		if j.cursor != 1 {
			t.Errorf("right cursor = %d, want 1", j.cursor)
		}
		j.update(tea.KeyMsg{Type: tea.KeyRight})
		if j.cursor != 2 {
			t.Errorf("right at the end cursor = %d, want 2 (guard no-op)", j.cursor)
		}
	})
	t.Run("home, ctrl+a, end and ctrl+e", func(t *testing.T) {
		var i structuredInput
		i.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abcd")})
		i.update(tea.KeyMsg{Type: tea.KeyHome})
		if i.cursor != 0 {
			t.Errorf("home cursor = %d, want 0", i.cursor)
		}
		i.update(tea.KeyMsg{Type: tea.KeyEnd})
		if i.cursor != 4 {
			t.Errorf("end cursor = %d, want 4", i.cursor)
		}
		i.update(tea.KeyMsg{Type: tea.KeyCtrlA})
		if i.cursor != 0 {
			t.Errorf("ctrl+a cursor = %d, want 0", i.cursor)
		}
		i.update(tea.KeyMsg{Type: tea.KeyCtrlE})
		if i.cursor != 4 {
			t.Errorf("ctrl+e cursor = %d, want 4", i.cursor)
		}
	})
}

func TestStructuredInputView(t *testing.T) {
	i := structuredInput{value: []rune("ab"), cursor: 2}
	if got := i.View(); got != "ab▌" {
		t.Errorf("end-of-input view = %q, want ab▌", got)
	}
	i = structuredInput{value: []rune("ab"), cursor: 1}
	if got := i.View(); got != "a▌b" {
		t.Errorf("mid-input view = %q, want a▌b", got)
	}
}

// --- new node helper unit tests -------------------------------------------

func TestNewNodeHelperFunctions(t *testing.T) {
	for _, name := range []string{"route", "handle", "handle_path", "handle_errors"} {
		if !isNewNodeHandlerParent(name) {
			t.Errorf("isNewNodeHandlerParent(%q) = false, want true", name)
		}
	}
	if isNewNodeHandlerParent("respond") {
		t.Error("isNewNodeHandlerParent(respond) must be false")
	}

	if got := newNodeKind(true, "site"); got != caddyfile.KindSite {
		t.Errorf("newNodeKind(site) = %v", got)
	}
	if got := newNodeKind(true, "snippet"); got != caddyfile.KindSnippet {
		t.Errorf("newNodeKind(snippet) = %v", got)
	}
	if got := newNodeKind(true, "named route"); got != caddyfile.KindNamedRoute {
		t.Errorf("newNodeKind(named route) = %v", got)
	}
	if got := newNodeKind(true, "global options"); got != caddyfile.KindGlobalOptions {
		t.Errorf("newNodeKind(global options) = %v", got)
	}
	if got := newNodeKind(true, "bogus"); got != caddyfile.Kind(-1) {
		t.Errorf("newNodeKind(bogus) = %v, want -1", got)
	}
	if got := newNodeKind(false, "anything"); got != caddyfile.KindDirective {
		t.Errorf("newNodeKind(nested) = %v, want directive", got)
	}

	if got := newNodeFirstField(caddyfile.KindDirective); got != newNodeArgsField {
		t.Errorf("newNodeFirstField(directive) = %d, want args", got)
	}
	if got := newNodeFirstField(caddyfile.KindGlobalOptions); got != newNodeArgsField {
		t.Errorf("newNodeFirstField(global options) = %d, want args", got)
	}
	if got := newNodeFirstField(caddyfile.KindSite); got != newNodeNameField {
		t.Errorf("newNodeFirstField(site) = %d, want name", got)
	}

	if got := newNodeDisplayName(caddyfile.NodeSpec{Kind: caddyfile.KindGlobalOptions}); got != "global options" {
		t.Errorf("newNodeDisplayName(global options) = %q", got)
	}
	if got := newNodeDisplayName(caddyfile.NodeSpec{Kind: caddyfile.KindSite, Name: "x.test"}); got != "x.test" {
		t.Errorf("newNodeDisplayName(site) = %q", got)
	}

	if got := newNodeOptions(true); len(got) != 4 || got[0] != "site" || got[3] != "global options" {
		t.Errorf("newNodeOptions(top) = %v", got)
	}
	if got := newNodeOptions(false); len(got) != 4 || got[3] != "handle_errors" {
		t.Errorf("newNodeOptions(nested) = %v", got)
	}
}

// --- structured add: availability -----------------------------------------

// TestStructuredAddOnDocumentRowOffersCommentPlacement verifies a on a
// document row opens the comment-placement picker (header/footer), since
// a document hosts no directives.
func TestStructuredAddOnDocumentRowOffersCommentPlacement(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !m.showStructuredAdd {
		t.Fatal("a on a document row must open the comment-placement picker")
	}
	if m.structuredAddMode != structuredAddCommentPlacement {
		t.Fatalf("mode = %v, want structuredAddCommentPlacement", m.structuredAddMode)
	}
	want := []string{commentPlacementTop, commentPlacementBottom}
	if strings.Join(m.structuredAddItems, ",") != strings.Join(want, ",") {
		t.Errorf("placements = %v, want %v", m.structuredAddItems, want)
	}
}

func TestStructuredAddOnHandlerDirective(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\troute {\n\t\trespond ok\n\t}\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})  // site
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight}) // expand
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})  // route
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !m.showStructuredAdd || m.structuredAddParent.Name != "route" {
		t.Fatalf("add on route = show:%v parent:%q, want the handler picker", m.showStructuredAdd, m.structuredAddParent.Name)
	}
	if got := m.structuredAddItems[0]; got != "encode" {
		t.Fatalf("route insertable directives start at %q, want encode", got)
	}
}

func TestReverseProxyEditUnavailableOnSite(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if m.showStructuredAdd {
		t.Fatal("reverse_proxy edit opened on a site")
	}
	if !strings.Contains(m.statusMessage, "reverse_proxy edit unavailable") {
		t.Fatalf("statusMessage = %q, want reverse_proxy-unavailable error", m.statusMessage)
	}
}

func TestReverseProxyEditRejectsStaleNode(t *testing.T) {
	// A node whose identity no longer matches the document is rejected by
	// the planner instead of being applied to the wrong bytes.
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\treverse_proxy localhost:8080 {\n\t\theader_up Host {host}\n\t}\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})  // site
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight}) // expand
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})  // reverse_proxy
	if !m.canEditReverseProxy() {
		t.Fatal("canEditReverseProxy = false on the reverse_proxy node")
	}
	m.selectedItem().node.Range.Start = 99999
	updated, _ := m.startReverseProxyEdit()
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "reverse_proxy edit unavailable") {
		t.Fatalf("statusMessage = %q, want planner rejection", m.statusMessage)
	}
}

// --- structured add: picker -------------------------------------------------

func TestStructuredAddPickerNavigationAndCancel(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	// Up at the top stays put; down moves.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.structuredAddCursor != 0 {
		t.Errorf("up cursor = %d, want 0", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.structuredAddCursor != 1 {
		t.Errorf("down cursor = %d, want 1", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlK})
	if m.structuredAddCursor != 0 {
		t.Errorf("ctrl+k cursor = %d, want 0", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if m.structuredAddCursor != 1 {
		t.Errorf("ctrl+j cursor = %d, want 1", m.structuredAddCursor)
	}
	// Esc cancels the whole add.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showStructuredAdd || !strings.Contains(m.statusMessage, "add cancelled") {
		t.Fatalf("esc state = show:%v msg:%q, want closed and cancelled", m.showStructuredAdd, m.statusMessage)
	}
}

func TestStructuredAddPickerHelpWithEmptyFilter(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m.browser = app.BrowserFunc(func(context.Context, string) error { return nil })
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("zzzznope") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	m = updated.(*Model)
	if cmd != nil {
		t.Fatal("help with an empty filter returned a browser command")
	}
	if !strings.Contains(m.statusMessage, "no supported directive is selected") {
		t.Fatalf("statusMessage = %q, want no-match help error", m.statusMessage)
	}
}

func TestStructuredAddPickerClampsCursorAfterFilter(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m.browser = app.BrowserFunc(func(context.Context, string) error { return nil })
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.structuredAddCursor != 2 {
		t.Fatalf("cursor = %d, want 2", m.structuredAddCursor)
	}
	// Narrowing the filter clamps the cursor into range.
	for _, r := range []rune("encode") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.structuredAddCursor != 0 {
		t.Errorf("filtered cursor = %d, want clamped to 0", m.structuredAddCursor)
	}
	// Enter with the clamped cursor picks the single match and opens the
	// args form (encode is not reverse_proxy).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if m.structuredAddMode != structuredAddArgs || m.structuredAddName != "encode" {
		t.Fatalf("enter state = mode:%v name:%q, want the encode args form", m.structuredAddMode, m.structuredAddName)
	}
	if !strings.Contains(stripANSI(m.structuredAddView(80, 20)), "args>") {
		t.Fatal("args form does not render the args prompt")
	}
}

func TestStructuredAddPickerHelpClampsCursor(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	var gotURL string
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m.browser = app.BrowserFunc(func(_ context.Context, url string) error {
		gotURL = url
		return nil
	})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	// A cursor beyond the filtered list is clamped to the last item
	// before help opens. With the comment entry sorted alphabetically,
	// the last item on a top-level block is a real directive (tls).
	m.structuredAddCursor = 99
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("ctrl+h did not return a browser command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if gotURL != "https://caddyserver.com/docs/caddyfile/directives/tls" {
		t.Errorf("opened URL = %q, want the tls documentation", gotURL)
	}
}

func TestStructuredAddPickerEnterClampsCursor(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	// A cursor beyond the filtered list is clamped to the last item. With
	// the comment entry sorted alphabetically, the last item on a
	// top-level block is a real directive: Enter selects tls and opens
	// the args form.
	m.structuredAddCursor = 99
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.structuredAddMode != structuredAddArgs || m.structuredAddName != "tls" {
		t.Fatalf("enter clamped to mode %v name %q, want the args form for tls", m.structuredAddMode, m.structuredAddName)
	}
}

// --- structured add: args flow and reverse_proxy form -----------------------

func TestStructuredAddArgsFlow(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	formatter := &fakeFormatter{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("encode") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // select encode → args form
	if m.structuredAddMode != structuredAddArgs {
		t.Fatalf("mode = %v, want args", m.structuredAddMode)
	}
	for _, r := range []rune("gzip") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.structuredAddInput.String(); got != "gzip" {
		t.Fatalf("args input = %q, want gzip", got)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("args submit did not return a validation command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if m.pendingEdit == nil || m.pendingEdit.operation != "add" {
		t.Fatalf("pendingEdit = %+v, want a structured add", m.pendingEdit)
	}
	if !strings.Contains(string(m.pendingEdit.content), "encode gzip") {
		t.Fatalf("candidate missing encode gzip: %q", m.pendingEdit.content)
	}
	if formatter.calls != 1 {
		t.Fatalf("formatter calls = %d, want 1", formatter.calls)
	}
}

func TestStructuredAddArgsEscAndCtrlC(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("encode") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	// Esc in the args form returns to the picker.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.structuredAddMode != structuredAddPicker || m.structuredAddName != "" {
		t.Fatalf("esc state = mode:%v name:%q, want back at the picker", m.structuredAddMode, m.structuredAddName)
	}
	// Esc in the picker closes everything.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showStructuredAdd || !strings.Contains(m.statusMessage, "add cancelled") {
		t.Fatalf("picker esc = show:%v msg:%q, want closed", m.showStructuredAdd, m.statusMessage)
	}

	// Reopen and cancel from the args form with ctrl+c.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("encode") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.showStructuredAdd || !strings.Contains(m.statusMessage, "add cancelled") {
		t.Fatalf("ctrl+c state = show:%v msg:%q, want closed", m.showStructuredAdd, m.statusMessage)
	}
}

func TestStructuredReverseProxyFormNavigation(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("reverse_proxy") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.structuredAddMode != structuredAddReverseProxy {
		t.Fatalf("mode = %v, want reverse_proxy form", m.structuredAddMode)
	}
	// Enter on the matcher field moves to upstreams.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.structuredAddRPField != structuredReverseProxyMatcher {
		t.Fatalf("shift+tab field = %v, want matcher", m.structuredAddRPField)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd != nil || m.structuredAddRPField != structuredReverseProxyUpstreams {
		t.Fatalf("enter on matcher = cmd:%v field:%v, want move to upstreams", cmd != nil, m.structuredAddRPField)
	}
	// Typing goes to the active field.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@api")})
	if got := m.structuredAddMatcher.String(); got != "@api" {
		t.Fatalf("matcher input = %q, want @api", got)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("localhost:8080")})
	if got := m.structuredAddUpstreams.String(); got != "localhost:8080" {
		t.Fatalf("upstreams input = %q, want localhost:8080", got)
	}
	// Esc returns to the picker; ctrl+c cancels.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.structuredAddMode != structuredAddPicker {
		t.Fatalf("esc mode = %v, want picker", m.structuredAddMode)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.showStructuredAdd || !strings.Contains(m.statusMessage, "add cancelled") {
		t.Fatalf("ctrl+c = show:%v msg:%q, want cancelled", m.showStructuredAdd, m.statusMessage)
	}

	// Reopen and cancel directly from the reverse_proxy form.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("reverse_proxy") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.structuredAddMode != structuredAddReverseProxy {
		t.Fatalf("mode = %v, want reverse_proxy form", m.structuredAddMode)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.showStructuredAdd || !strings.Contains(m.statusMessage, "add cancelled") {
		t.Fatalf("form ctrl+c = show:%v msg:%q, want cancelled", m.showStructuredAdd, m.statusMessage)
	}
}

func TestStructuredReverseProxyRequiresUpstream(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("reverse_proxy") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd != nil {
		t.Fatal("empty-upstream submit returned a validation command")
	}
	if !strings.Contains(m.statusMessage, "requires at least one upstream") {
		t.Fatalf("statusMessage = %q, want upstream error", m.statusMessage)
	}
}

// --- structured add: direct submit guards -----------------------------------

func TestStructuredAddSubmitGuards(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})

	t.Run("busy", func(t *testing.T) {
		m.structuredAddBusy = true
		if _, cmd := m.submitStructuredAdd(); cmd != nil {
			t.Fatal("busy submit returned a command")
		}
		m.structuredAddBusy = false
	})
	t.Run("missing name", func(t *testing.T) {
		m.structuredAddMode = structuredAddArgs
		m.structuredAddName = ""
		if _, cmd := m.submitStructuredAdd(); cmd != nil {
			t.Fatal("missing-name submit returned a command")
		}
		if !strings.Contains(m.statusMessage, "requires a directive selection") {
			t.Fatalf("statusMessage = %q, want selection error", m.statusMessage)
		}
	})
	t.Run("missing document", func(t *testing.T) {
		m.structuredAddMode = structuredAddArgs
		m.structuredAddName = "respond"
		m.structuredAddDoc = nil
		m.showStructuredAdd = true
		if _, cmd := m.submitStructuredAdd(); cmd != nil {
			t.Fatal("missing-doc submit returned a command")
		}
		if m.showStructuredAdd || !strings.Contains(m.statusMessage, "source document is unavailable") {
			t.Fatalf("state = show:%v msg:%q, want closed with doc error", m.showStructuredAdd, m.statusMessage)
		}
	})
	t.Run("planner rejection", func(t *testing.T) {
		doc := caddyfile.Parse([]byte("example.test {\n\trespond ok\n}\n"))
		foreign := caddyfile.Node{Kind: caddyfile.KindDirective, Name: "respond", Range: caddyfile.SourceRange{Start: 999, End: 1000}}
		m.structuredAddDoc = doc
		m.structuredAddParent = foreign
		m.structuredAddMode = structuredAddArgs
		m.structuredAddName = "respond"
		if _, cmd := m.submitStructuredAdd(); cmd != nil {
			t.Fatal("planner-rejected submit returned a command")
		}
		if !strings.Contains(m.statusMessage, "add unavailable") {
			t.Fatalf("statusMessage = %q, want planner rejection", m.statusMessage)
		}
	})
}

func TestStructuredReverseProxySubmitGuards(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})

	t.Run("missing document", func(t *testing.T) {
		m.structuredAddMode = structuredAddReverseProxy
		m.structuredAddUpstreams = structuredInput{value: []rune("localhost:8080")}
		m.structuredAddDoc = nil
		m.showStructuredAdd = true
		if _, cmd := m.submitStructuredReverseProxy(); cmd != nil {
			t.Fatal("missing-doc submit returned a command")
		}
		if m.showStructuredAdd || !strings.Contains(m.statusMessage, "source document is unavailable") {
			t.Fatalf("state = show:%v msg:%q, want closed with doc error", m.showStructuredAdd, m.statusMessage)
		}
	})
	t.Run("planner rejection", func(t *testing.T) {
		doc := caddyfile.Parse([]byte("example.test {\n\trespond ok\n}\n"))
		foreign := caddyfile.Node{Kind: caddyfile.KindDirective, Name: "reverse_proxy", Range: caddyfile.SourceRange{Start: 999, End: 1000}}
		m.structuredAddDoc = doc
		m.structuredAddParent = foreign
		m.structuredAddMode = structuredAddReverseProxy
		m.structuredAddUpstreams = structuredInput{value: []rune("localhost:8080")}
		m.structuredAddEditing = false
		if _, cmd := m.submitStructuredReverseProxy(); cmd != nil {
			t.Fatal("planner-rejected submit returned a command")
		}
		if !strings.Contains(m.statusMessage, "reverse_proxy unavailable") {
			t.Fatalf("statusMessage = %q, want planner rejection", m.statusMessage)
		}
	})
}

// --- structured add: validation handling ------------------------------------

func TestStructuredAddValidationRejectedByErrorDiagnostics(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("encode") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if m.pendingEdit != nil || m.showDiff {
		t.Fatalf("error diagnostics applied: pending=%v diff=%v", m.pendingEdit != nil, m.showDiff)
	}
	if !m.showDiagnostics || len(m.diagnostics) != 1 {
		t.Fatalf("diagnostics state = show:%v count:%d, want one error shown", m.showDiagnostics, len(m.diagnostics))
	}
	if !strings.Contains(m.statusMessage, "did not validate") {
		t.Fatalf("statusMessage = %q, want not-validated error", m.statusMessage)
	}
}

func TestStructuredAddValidationFailureAndWarnings(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))

	t.Run("validator error", func(t *testing.T) {
		formatter := &fakeFormatter{err: context.DeadlineExceeded}
		m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		for _, r := range []rune("encode") {
			m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		updated, _ = updated.(*Model).Update(cmd())
		m = updated.(*Model)
		if m.pendingEdit != nil || !strings.Contains(m.statusMessage, "validation failed") {
			t.Fatalf("validator error state = pending:%v msg:%q", m.pendingEdit != nil, m.statusMessage)
		}
	})

	t.Run("warnings", func(t *testing.T) {
		warn := []validator.Diagnostic{
			{Path: "config/Caddyfile", Line: 2, Message: "advice", Severity: validator.SeverityWarning},
		}
		formatter := &fakeFormatter{diagnostics: warn}
		m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		for _, r := range []rune("encode") {
			m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		updated, _ = updated.(*Model).Update(cmd())
		m = updated.(*Model)
		if m.pendingEdit != nil || !strings.Contains(m.statusMessage, "has warnings") {
			t.Fatalf("warnings state = pending:%v msg:%q", m.pendingEdit != nil, m.statusMessage)
		}
	})
}

func TestStructuredAddUsesFormattedOutput(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("formatted candidate\n")}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("encode") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ = updated.(*Model).Update(cmd())
	m = updated.(*Model)
	if m.pendingEdit == nil || string(m.pendingEdit.content) != "formatted candidate\n" {
		t.Fatalf("pendingEdit content = %q, want the formatted output", m.pendingEdit.content)
	}
}

func TestStructuredAddValidateCmdAppliesTimeout(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	formatter := &fakeFormatter{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
	m.validatorTimeout = time.Second
	cmd := m.structuredAddValidateCmd("config/Caddyfile", []byte("a"), []byte("b"), "encode", "add", caddyfile.Node{}, "")
	msg := cmd()
	_ = msg
	if formatter.calls != 1 {
		t.Fatalf("formatter calls = %d, want 1", formatter.calls)
	}
	if _, ok := formatter.capturedCtx.Deadline(); !ok {
		t.Fatal("validator context has no deadline despite a positive timeout")
	}
}

func TestQueueStructuredAddValidationApplyError(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	doc := caddyfile.Parse([]byte("abc"))
	m.structuredAddDoc = doc
	edit := &caddyfile.PlannedEdit{Range: caddyfile.SourceRange{Start: 99, End: 100}}
	if _, cmd := m.queueStructuredAddValidation("x", "add", edit); cmd != nil {
		t.Fatal("apply-failed validation returned a command")
	}
	if !strings.Contains(m.statusMessage, "add failed") {
		t.Fatalf("statusMessage = %q, want apply error", m.statusMessage)
	}
}

// --- structured add: view geometry ------------------------------------------

func TestStructuredAddViewSmallTerminal(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, dims := range [][2]int{{30, 8}, {2, 5}, {80, 5}} {
		view := stripANSI(m.structuredAddView(dims[0], dims[1]))
		if view == "" {
			t.Fatalf("picker view empty at %dx%d", dims[0], dims[1])
		}
	}
}

func TestStructuredAddViewEmptyFilter(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("zzzznope") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	view := stripANSI(m.structuredAddView(80, 20))
	if !strings.Contains(view, "no supported directives match this filter") {
		t.Fatalf("empty-filter view does not explain the empty list:\n%s", view)
	}
}

// --- new node: picker and form ----------------------------------------------

func TestNewNodePickerNavigationAndCancel(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	opened := false
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m.browser = app.BrowserFunc(func(context.Context, string) error {
		opened = true
		return nil
	})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	// Up at the top stays put; down moves; plain letters are ignored.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.structuredAddCursor != 0 {
		t.Errorf("up cursor = %d, want 0", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.structuredAddCursor != 0 || m.structuredAddInput.String() != "" {
		t.Fatalf("letter key moved/typed: cursor=%d input=%q", m.structuredAddCursor, m.structuredAddInput.String())
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.structuredAddCursor != 1 {
		t.Errorf("down cursor = %d, want 1", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlK})
	if m.structuredAddCursor != 0 {
		t.Errorf("ctrl+k cursor = %d, want 0", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if m.structuredAddCursor != 1 {
		t.Errorf("ctrl+j cursor = %d, want 1", m.structuredAddCursor)
	}
	// Ctrl-H opens the Caddyfile help.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("new node ctrl+h did not return a help command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if !opened {
		t.Fatal("new node help did not open the browser")
	}
	// Esc cancels.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showStructuredAdd || !strings.Contains(m.statusMessage, "new node cancelled") {
		t.Fatalf("esc = show:%v msg:%q, want cancelled", m.showStructuredAdd, m.statusMessage)
	}
}

func TestNewNodeUnavailableInReadOnlyMode(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.showStructuredAdd {
		t.Fatal("new node opened in read-only mode")
	}
	if !strings.Contains(m.statusMessage, "new node unavailable") {
		t.Fatalf("statusMessage = %q, want new-node-unavailable error", m.statusMessage)
	}
}

func TestNewNodePickerOnHandlerDirective(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\thandle /api {\n\t\trespond ok\n\t}\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})  // site
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight}) // expand
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})  // handle
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if !m.showStructuredAdd || m.structuredAddNewTop {
		t.Fatalf("new node on handler = show:%v top:%v, want nested creation", m.showStructuredAdd, m.structuredAddNewTop)
	}
	if got := m.structuredAddItems[0]; got != "route" {
		t.Fatalf("nested picker starts at %q, want route", got)
	}
}

func TestNewNodeCreatesSnippetAndNamedRoute(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))

	t.Run("snippet", func(t *testing.T) {
		formatter := &fakeFormatter{}
		m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // snippet
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		for _, r := range []rune("head") {
			m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(*Model)
		if cmd == nil {
			t.Fatal("snippet submit did not return a validation command")
		}
		updated, _ = m.Update(cmd())
		m = updated.(*Model)
		if m.pendingEdit == nil || !strings.Contains(string(m.pendingEdit.content), "(head) {\n}\n") {
			t.Fatalf("snippet candidate = %+v, want (head) { }", m.pendingEdit)
		}
	})

	t.Run("named route", func(t *testing.T) {
		formatter := &fakeFormatter{}
		m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // named route
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
		for _, r := range []rune("api") {
			m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(*Model)
		if cmd == nil {
			t.Fatal("named route submit did not return a validation command")
		}
		updated, _ = m.Update(cmd())
		m = updated.(*Model)
		if m.pendingEdit == nil || !strings.Contains(string(m.pendingEdit.content), "&(api) {\n}\n") {
			t.Fatalf("named route candidate = %+v, want &(api) { }", m.pendingEdit)
		}
	})
}

func TestNewNodeCreatesGlobalOptionsOnEmptyDocument(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "",
	}))
	formatter := &fakeFormatter{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // global options
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	view := stripANSI(m.newNodeView(80, 20))
	if !strings.Contains(view, "Creates an empty global options block") {
		t.Fatalf("global options form view missing explanation:\n%s", view)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("global options submit did not return a validation command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if m.pendingEdit == nil || string(m.pendingEdit.content) != "{\n}\n" {
		t.Fatalf("global options candidate = %+v, want an empty block", m.pendingEdit)
	}
}

func TestNewNodeCreatesNestedHandlerWithArgs(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	formatter := &fakeFormatter{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // site
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // handle
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.structuredAddNewField != newNodeArgsField {
		t.Fatalf("handler form starts at field %d, want args", m.structuredAddNewField)
	}
	for _, r := range []rune("/api") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.structuredAddNewArgs.String(); got != "/api" {
		t.Fatalf("handler args = %q, want /api", got)
	}
	view := stripANSI(m.newNodeView(80, 20))
	if !strings.Contains(view, "args>") {
		t.Fatalf("handler form view missing args prompt:\n%s", view)
	}
	// Tab keeps the single-field form on args.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.structuredAddNewField != newNodeArgsField {
		t.Fatalf("tab field = %d, want args", m.structuredAddNewField)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("nested handler submit did not return a validation command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if m.pendingEdit == nil || !strings.Contains(string(m.pendingEdit.content), "\thandle /api {\n\t}\n") {
		t.Fatalf("nested handler candidate = %+v, want the indented block", m.pendingEdit)
	}
}

func TestNewNodeFormEscAndCtrlC(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m.browser = app.BrowserFunc(func(context.Context, string) error { return nil })
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // site → form
	if m.structuredAddMode != structuredAddNewForm {
		t.Fatalf("mode = %v, want form", m.structuredAddMode)
	}
	// Ctrl-H in the form opens help.
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH}); cmd == nil {
		t.Fatal("form ctrl+h did not return a help command")
	}
	// Esc returns to the picker.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.structuredAddMode != structuredAddNewPicker {
		t.Fatalf("esc mode = %v, want picker", m.structuredAddMode)
	}
	// Re-enter the form and cancel with ctrl+c.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.showStructuredAdd || !strings.Contains(m.statusMessage, "new node cancelled") {
		t.Fatalf("ctrl+c = show:%v msg:%q, want cancelled", m.showStructuredAdd, m.statusMessage)
	}
}

// --- new node: direct submit guards -----------------------------------------

func TestNewNodeSubmitGuards(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})

	t.Run("no selection", func(t *testing.T) {
		m.cursor = -1
		if m.canNewNode() {
			t.Error("canNewNode = true with no selected row")
		}
	})
	t.Run("busy or missing document", func(t *testing.T) {
		m.cursor = 0
		m.structuredAddBusy = true
		if _, cmd := m.submitNewNode(); cmd != nil {
			t.Fatal("busy submit returned a command")
		}
		m.structuredAddBusy = false
		m.structuredAddDoc = nil
		if _, cmd := m.submitNewNode(); cmd != nil {
			t.Fatal("missing-doc submit returned a command")
		}
		m.structuredAddDoc = caddyfile.Parse([]byte(""))
	})
	t.Run("unknown kind", func(t *testing.T) {
		m.structuredAddNewKind = caddyfile.Kind(-1)
		if _, cmd := m.submitNewNode(); cmd != nil {
			t.Fatal("unknown-kind submit returned a command")
		}
		if !strings.Contains(m.statusMessage, "no supported type") {
			t.Fatalf("statusMessage = %q, want unknown-type error", m.statusMessage)
		}
	})
	t.Run("planner rejection", func(t *testing.T) {
		m.structuredAddNewKind = caddyfile.KindSite
		m.structuredAddNewTop = true
		m.structuredAddNewName = structuredInput{value: []rune("foo # comment")}
		if _, cmd := m.submitNewNode(); cmd != nil {
			t.Fatal("planner-rejected submit returned a command")
		}
		if !strings.Contains(m.statusMessage, "new node unavailable") {
			t.Fatalf("statusMessage = %q, want planner rejection", m.statusMessage)
		}
	})
}

func TestNewNodePickerEnterWithEmptyItems(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m.structuredAddItems = nil
	if _, cmd := m.updateNewNodePickerKey(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatal("enter on an empty picker returned a command")
	}
	if m.structuredAddMode != structuredAddNewPicker {
		t.Fatalf("empty-picker enter changed the mode to %v", m.structuredAddMode)
	}
}

func TestNewNodeViewSmallTerminal(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	for _, dims := range [][2]int{{30, 8}, {2, 5}, {80, 5}, {120, 30}} {
		view := stripANSI(m.newNodeView(dims[0], dims[1]))
		if view == "" {
			t.Fatalf("new node view empty at %dx%d", dims[0], dims[1])
		}
	}
}
