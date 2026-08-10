package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestStructuredAdd_PlansValidatesAndOpensDiff(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	formatter := &fakeFormatter{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // select the site block
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !m.showStructuredAdd {
		t.Fatal("showStructuredAdd = false after a")
	}
	for _, r := range []rune("reverse") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	for _, r := range []rune("@api") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	for _, r := range []rune("localhost:8080") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("structured add did not return validation command")
	}
	if m.showStructuredAdd || !m.structuredAddBusy {
		t.Fatalf("structured add state = show:%v busy:%v, want closed and busy", m.showStructuredAdd, m.structuredAddBusy)
	}
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(*Model)
	if m.pendingEdit == nil || m.pendingEdit.operation != "add" {
		t.Fatalf("pendingEdit = %+v, want structured add", m.pendingEdit)
	}
	if !strings.Contains(string(m.pendingEdit.content), "reverse_proxy @api localhost:8080") {
		t.Fatalf("candidate missing reverse_proxy: %q", m.pendingEdit.content)
	}
	if !m.showDiff {
		t.Fatal("showDiff = false after validated structured add")
	}
	if formatter.calls != 1 || formatter.capturedDisplayPath != "config/Caddyfile" {
		t.Fatalf("formatter calls/path = %d/%q, want 1/config/Caddyfile", formatter.calls, formatter.capturedDisplayPath)
	}
}

func TestStructuredAdd_InvalidDirectiveDoesNotValidate(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	formatter := &fakeFormatter{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("unknown_directive") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd != nil {
		t.Fatal("unsupported structured add returned a validation command")
	}
	if m.showStructuredAdd == false || m.structuredAddBusy {
		t.Fatalf("modal state = show:%v busy:%v, want open and idle", m.showStructuredAdd, m.structuredAddBusy)
	}
	if formatter.calls != 0 {
		t.Fatalf("formatter calls = %d, want 0", formatter.calls)
	}
	if !strings.Contains(m.statusMessage, "no supported directives") {
		t.Fatalf("statusMessage = %q, want no-match error", m.statusMessage)
	}
}

func TestStructuredAddPickerSelectsDirectiveBeforeArguments(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !strings.Contains(m.View(), "reverse_proxy") {
		t.Fatal("directive picker does not show reverse_proxy")
	}
	for _, r := range []rune("reverse") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.structuredAddMode != structuredAddReverseProxy || m.structuredAddName != "reverse_proxy" {
		t.Fatalf("picker state = mode:%v name:%q, want reverse_proxy form", m.structuredAddMode, m.structuredAddName)
	}
	if !strings.Contains(m.View(), "upstreams>") || !strings.Contains(m.View(), "matcher>") {
		t.Fatal("directive picker did not switch to reverse_proxy form")
	}
}

func TestStructuredAddPickerOpensDirectiveHelp(t *testing.T) {
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
	for _, r := range []rune("reverse") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("directive help did not return browser command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if gotURL != "https://caddyserver.com/docs/caddyfile/directives/reverse_proxy" {
		t.Errorf("opened URL = %q, want reverse_proxy documentation", gotURL)
	}
	if !m.showStructuredAdd {
		t.Fatal("directive help closed the add picker")
	}
}

func TestStructuredAddPickerRowsDoNotWrap(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})

	view := stripANSI(m.structuredAddView(100, 30))
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "log") && !strings.Contains(line, "Add directive") {
			if lipgloss.Width(line) > 100 {
				t.Fatalf("picker row wraps beyond terminal width: %d cells: %q", lipgloss.Width(line), line)
			}
		}
	}
	if strings.Contains(view, "logging for the site, or global logging as a\n") {
		t.Fatalf("log description wrapped into a second line:\n%s", view)
	}
}

func TestStructuredPickerRowIsBounded(t *testing.T) {
	row := structuredPickerRow("log", "Configures access logging for the site, or global logging as a global option.", true, 40)
	if strings.Contains(row, "\n") {
		t.Fatalf("structured picker row contains a newline: %q", row)
	}
	if got := lipgloss.Width(row); got > 40 {
		t.Fatalf("structured picker row width = %d, want <= 40: %q", got, row)
	}
}

func TestStructuredAddDiffEnterSaves(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile"}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("reverse") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	for _, r := range []rune("localhost:8080") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if !m.showDiff {
		t.Fatal("showDiff = false before confirmation")
	}
	updated, saveCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if saveCmd == nil || !m.saving {
		t.Fatalf("save state = saving:%v cmd:%v, want in-flight save", m.saving, saveCmd != nil)
	}
	updated, _ = m.Update(saveCmd())
	m = updated.(*Model)
	if saver.calls != 1 || saver.capturedPath != "config/Caddyfile" {
		t.Fatalf("save calls/path = %d/%q, want 1/config/Caddyfile", saver.calls, saver.capturedPath)
	}
	if m.pendingEdit != nil || m.showDiff {
		t.Fatalf("pending add state survived save: pending=%v diff=%v", m.pendingEdit != nil, m.showDiff)
	}
}

func TestStructuredReverseProxyEditPlansAndOpensDiff(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\treverse_proxy @api localhost:8080 app-02:8080 {\n\t\theader_up Host {host}\n\t}\n}\n",
	}))
	formatter := &fakeFormatter{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if !m.showStructuredAdd || !m.structuredAddEditing {
		t.Fatalf("reverse_proxy edit state = show:%v editing:%v, want open edit form", m.showStructuredAdd, m.structuredAddEditing)
	}
	if got := m.structuredAddMatcher.String(); got != "@api" {
		t.Fatalf("prefilled matcher = %q, want @api", got)
	}
	if got := m.structuredAddUpstreams.String(); got != "localhost:8080 app-02:8080" {
		t.Fatalf("prefilled upstreams = %q, want both upstreams", got)
	}
	for len(m.structuredAddUpstreams.value) > 0 {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyHome})
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDelete})
	}
	for _, r := range []rune("localhost:9090 app-03:9090") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("reverse_proxy edit did not return validation command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if m.pendingEdit == nil || m.pendingEdit.operation != "edit" {
		t.Fatalf("pendingEdit = %+v, want structured edit", m.pendingEdit)
	}
	content := string(m.pendingEdit.content)
	if !strings.Contains(content, "reverse_proxy @api localhost:9090 app-03:9090 {") {
		t.Fatalf("edited reverse_proxy missing from candidate: %q", content)
	}
	if !strings.Contains(content, "header_up Host {host}") {
		t.Fatalf("nested reverse_proxy options changed: %q", content)
	}
	if !m.showDiff {
		t.Fatal("showDiff = false after validated reverse_proxy edit")
	}
}
