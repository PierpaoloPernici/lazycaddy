package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCommandPalette_OpensAndFilters(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 100, 30)

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !m.showCommandPalette {
		t.Fatal("showCommandPalette = false after ?")
	}
	view := stripANSI(m.View())
	for _, want := range []string{
		"COMMANDS",
		"NAVIGATION",
		"SOURCE & VALIDATION",
		"RUNTIME & RECOVERY",
		"Move selection",
		"Format & validate",
		"Save validated changes",
		"Esc close",
		"Documents",
		"Source · config/Caddyfile",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("palette missing %q:\n%s", want, view)
		}
	}

	for _, r := range []rune("save") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	view = stripANSI(m.View())
	if !strings.Contains(view, "Save validated changes") {
		t.Errorf("filtered palette missing save command:\n%s", view)
	}
	if strings.Contains(view, "read-only mode") {
		t.Errorf("filtered palette should not render disabled-command help:\n%s", view)
	}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.showCommandPalette {
		t.Fatal("disabled command unexpectedly closed the palette")
	}
	if !strings.Contains(m.statusMessage, "read-only mode") {
		t.Errorf("statusMessage = %q, want the disabled-command reason", m.statusMessage)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showCommandPalette {
		t.Fatal("showCommandPalette = true after Esc")
	}
}

func TestCommandPalette_ExecutesSharedCommand(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	formatter := &fakeFormatter{formatted: []byte("example.test {\n\trespond ok\n}\n")}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	for _, r := range []rune("validate") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if m.showCommandPalette {
		t.Fatal("showCommandPalette = true after executing validate")
	}
	if !m.busy {
		t.Fatal("busy = false after executing validate from the palette")
	}
	if cmd == nil {
		t.Fatal("palette validate did not return the formatter command")
	}
}

func TestCommandPalette_KeyCatalogKeepsDirectHotkeys(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	for _, tc := range []struct {
		key string
		id  commandID
	}{
		{key: "v", id: commandValidate},
		{key: "D", id: commandDiff},
		{key: "s", id: commandSave},
		{key: "?", id: commandPalette},
		{key: "q", id: commandQuit},
	} {
		id, ok := m.commandForKey(tc.key)
		if !ok || id != tc.id {
			t.Errorf("commandForKey(%q) = %q, %v; want %q", tc.key, id, ok, tc.id)
		}
	}
}

func TestCommandPalette_CategoriesAndCompactKeys(t *testing.T) {
	commands := commandDefinitions()
	if len(commands) == 0 {
		t.Fatal("commandDefinitions returned no commands")
	}

	seen := map[string]bool{}
	lastCategory := ""
	for _, command := range commands {
		if command.Category != lastCategory {
			if seen[command.Category] {
				t.Fatalf("category %q is split into multiple palette sections", command.Category)
			}
			seen[command.Category] = true
			lastCategory = command.Category
		}
	}

	copyCommand, ok := commandDefinition(commandCopy)
	if !ok || copyCommand.Label != "Copy selected block" {
		t.Fatalf("copy command = %+v, want label %q", copyCommand, "Copy selected block")
	}
	toggleCommand, ok := commandDefinition(commandToggleBranch)
	if !ok {
		t.Fatal("toggle command missing from catalog")
	}
	if !containsString(toggleCommand.Keys, "Space") {
		t.Fatalf("toggle hotkeys = %v, want Space to remain a direct hotkey", toggleCommand.Keys)
	}

	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	view := stripANSI(m.View())
	if !strings.Contains(view, "Enter · ←→") {
		t.Errorf("toggle row should use the compact Enter/arrow label:\n%s", view)
	}
	if strings.Contains(view, "Space") {
		t.Errorf("palette should hide Space from the toggle label while keeping the hotkey:\n%s", view)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
