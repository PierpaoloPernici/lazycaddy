package ui

import (
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	tea "github.com/charmbracelet/bubbletea"
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
	for _, r := range []rune("reverse_proxy localhost:8080") {
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
	if !strings.Contains(string(m.pendingEdit.content), "reverse_proxy localhost:8080") {
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
	for _, r := range []rune("unknown_directive value") {
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
	if !strings.Contains(m.statusMessage, "unsupported") {
		t.Fatalf("statusMessage = %q, want unsupported error", m.statusMessage)
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
	for _, r := range []rune("reverse_proxy localhost:8080") {
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
