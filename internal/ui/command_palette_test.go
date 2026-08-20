package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestCommandPalette_OpensAndFilters(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\tlog {\n\t\toutput file /tmp/lazycaddy-test.log\n\t}\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 100, 30)

	// Select the log directive so the directive form command is visible.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if sel := m.selectedItem(); !sel.hasNode || sel.node.Name != "log" {
		t.Fatalf("expected the log directive, got %q", sel.label)
	}

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !m.showCommandPalette {
		t.Fatal("showCommandPalette = false after ?")
	}
	view := stripANSI(m.View())
	for _, want := range []string{
		"COMMANDS",
		"NAVIGATION",
		"SOURCE",
		"VALIDATION",
		"Move selection",
		"Goto matcher (next)",
		"Format & validate",
		"Save validated changes",
		"Esc close",
		"Documents",
		"Caddyfile",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("palette missing %q:\n%s", want, view)
		}
	}
	// The command catalog still contains the form command even though it is
	// scrolled below the palette viewport once the matcher command was added.
	if _, ok := m.commandForKey("m"); !ok {
		t.Error("command catalog missing the directive-form command (m)")
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
		{key: "m", id: commandEditForm},
		{key: "n", id: commandNew},
		{key: "o", id: commandReorder},
		{key: "Ctrl-H", id: commandHelp},
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
	if len(copyCommand.Keys) != 1 || copyCommand.Keys[0] != "y" {
		t.Fatalf("copy command keys = %#v, want y", copyCommand.Keys)
	}
	if command, ok := commandDefinition(commandEditForm); !ok || command.Category != "Source" {
		t.Fatalf("directive form command = %+v, want Source category", command)
	}
	if command, ok := commandDefinition(commandValidate); !ok || command.Category != "Validation" {
		t.Fatalf("validate command = %+v, want Validation category", command)
	}
	if command, ok := commandDefinition(commandDiff); !ok || command.Category != "Validation" {
		t.Fatalf("diff command = %+v, want Validation category", command)
	}
	if command, ok := commandDefinition(commandSave); !ok || command.Category != "Validation" {
		t.Fatalf("save command = %+v, want Validation category", command)
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

// TestCommandPalette_FormCommandHiddenWithoutSupportedDirective verifies the
// directive form command is absent from the palette when the selection is
// not a directive with a dedicated form, and visible but disabled in
// read-only mode when it is one.
func TestCommandPalette_FormCommandHiddenWithoutSupportedDirective(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\tlog {\n\t\toutput file /tmp/lazycaddy-test.log\n\t}\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})

	command, ok := commandDefinition(commandEditForm)
	if !ok {
		t.Fatal("directive form command missing from catalog")
	}
	// The initial selection is the document row: the form command is hidden.
	for _, visible := range m.filteredCommands() {
		if visible.ID == commandEditForm {
			t.Fatal("directive form command visible without a supported directive selection")
		}
	}
	if command.Enabled(m) {
		t.Fatal("directive form command unexpectedly enabled without a directive selection")
	}

	// Select the log directive: the command appears, disabled with the
	// read-only reason (the state is read-only).
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if sel := m.selectedItem(); !sel.hasNode || sel.node.Name != "log" {
		t.Fatalf("expected the log directive, got %q", sel.label)
	}
	if got := command.Reason(m); got != "read-only mode" {
		t.Errorf("directive form command reason = %q, want read-only mode", got)
	}
	found := false
	for _, visible := range m.filteredCommands() {
		if visible.ID == commandEditForm {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("directive form command was filtered out on a supported directive")
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

func TestCommandPalette_DisabledReasons(t *testing.T) {
	// A read-only model with no capabilities: every guarded command must
	// report its availability reason instead of crashing.
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	for _, tc := range []struct {
		id     commandID
		reason string
	}{
		{id: commandEdit, reason: "editor unavailable"},
		{id: commandFullEdit, reason: "editor unavailable"},
		{id: commandDelete, reason: "read-only mode"},
	} {
		command, ok := commandDefinition(tc.id)
		if !ok {
			t.Fatalf("commandDefinition(%q) not found", tc.id)
		}
		if command.Enabled(m) {
			t.Errorf("%s: Enabled = true on a bare read-only model", tc.id)
		}
		if got := command.Reason(m); got != tc.reason {
			t.Errorf("%s Reason = %q, want %q", tc.id, got, tc.reason)
		}
	}

	// Writable with a saver but no formatter: delete reports the missing
	// Caddy binary rather than read-only mode.
	writable := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	noFormatter := newLoadedModel(t, fakeLoader{state: writable}, &fakeSaver{})
	deleteCommand, _ := commandDefinition(commandDelete)
	if deleteCommand.Enabled(noFormatter) {
		t.Fatal("delete Enabled = true without a formatter")
	}
	if got := deleteCommand.Reason(noFormatter); got != "Caddy binary unavailable" {
		t.Errorf("delete Reason = %q, want %q", got, "Caddy binary unavailable")
	}

	// Writable with saver and formatter but the cursor on the document
	// row: no node is selected, so delete explains the missing selection.
	withFormatter := newLoadedModel(t, fakeLoader{state: writable}, &fakeSaver{}, &fakeFormatter{})
	if withFormatter.items[0].hasNode {
		t.Fatal("test setup: document row unexpectedly has a node")
	}
	if deleteCommand.Enabled(withFormatter) {
		t.Fatal("delete Enabled = true on the document row")
	}
	if got := deleteCommand.Reason(withFormatter); got != "select a deletable node" {
		t.Errorf("delete Reason = %q, want %q", got, "select a deletable node")
	}

	// Unknown catalog lookups report not-found on both paths.
	if command, ok := commandDefinition(commandID("no-such-command")); ok || command.ID != "" {
		t.Errorf("commandDefinition(unknown) = (%+v, %v), want empty and false", command, ok)
	}
	if id, ok := m.commandForKey("~"); ok || id != "" {
		t.Errorf("commandForKey(unknown) = (%q, %v), want empty and false", id, ok)
	}

	// An editor without writable access reports the writable-mode reasons
	// for both editor commands instead of "editor unavailable".
	readOnlyWithEditor := newLoadedModel(t, fakeLoader{state: state}, &fakeEditor{})
	for _, tc := range []struct {
		id     commandID
		reason string
	}{
		{id: commandEdit, reason: "requires writable mode and a block or comment selection"},
		{id: commandFullEdit, reason: "requires writable mode and a document selection"},
	} {
		command, ok := commandDefinition(tc.id)
		if !ok {
			t.Fatalf("commandDefinition(%q) not found", tc.id)
		}
		if command.Enabled(readOnlyWithEditor) {
			t.Errorf("%s: Enabled = true in read-only mode", tc.id)
		}
		if got := command.Reason(readOnlyWithEditor); got != tc.reason {
			t.Errorf("%s Reason = %q, want %q", tc.id, got, tc.reason)
		}
	}
}

// TestCommandPalette_AddCommandReasons verifies the add command gates and
// reasons across the writable/read-only, formatter and selection states.
func TestCommandPalette_AddCommandReasons(t *testing.T) {
	addCmd, ok := commandDefinition(commandAdd)
	if !ok {
		t.Fatal("commandAdd not found")
	}
	ro := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	writable := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))

	// Read-only mode: disabled with the read-only reason.
	roModel := newLoadedModel(t, fakeLoader{state: ro}, &fakeSaver{}, &fakeFormatter{})
	if addCmd.Enabled(roModel) {
		t.Error("add Enabled = true in read-only mode")
	}
	if got := addCmd.Reason(roModel); got != "read-only mode" {
		t.Errorf("add Reason(ro) = %q, want %q", got, "read-only mode")
	}
	// Writable but no Caddy binary: the formatter reason.
	noFormatter := newLoadedModel(t, fakeLoader{state: writable}, &fakeSaver{})
	if got := addCmd.Reason(noFormatter); got != "Caddy binary unavailable" {
		t.Errorf("add Reason(no formatter) = %q, want %q", got, "Caddy binary unavailable")
	}
	// Writable with everything, on the virtual comments branch: the
	// selection reason (the branch is not a document).
	withComments := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": commentFixture,
	}))
	branchModel := newLoadedModel(t, fakeLoader{state: withComments}, &fakeSaver{}, &fakeFormatter{})
	branchModel = resize(branchModel, 120, 30)
	for i := 0; i < 3; i++ {
		branchModel = keyPress(t, branchModel, tea.KeyMsg{Type: tea.KeyDown})
	}
	if sel := branchModel.selectedItem(); sel.label != "comments (3)" {
		t.Fatalf("expected the comments branch, got %q", sel.label)
	}
	if addCmd.Enabled(branchModel) {
		t.Error("add Enabled = true on the comments branch")
	}
	if got := addCmd.Reason(branchModel); got != "select a supported block, document or comment" {
		t.Errorf("add Reason(branch) = %q, want the selection reason", got)
	}
	// Writable with everything, on a document row: enabled (placement).
	docModel := newLoadedModel(t, fakeLoader{state: writable}, &fakeSaver{}, &fakeFormatter{})
	if !addCmd.Enabled(docModel) {
		t.Error("add Enabled = false on a document row, want true (comment placement)")
	}
}

func TestCommandPalette_KeyHandling(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})

	// Navigation before any render: revealCommandCursor's guard is a no-op.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.commandCursor != 1 {
		t.Fatalf("cursor = %d after down, want 1", m.commandCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.commandCursor != 0 {
		t.Fatalf("cursor = %d after up, want 0", m.commandCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.commandCursor != 0 {
		t.Fatalf("cursor = %d after up at the top, want 0", m.commandCursor)
	}

	// Pager and edge keys.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyHome})
	if m.commandCursor != 0 {
		t.Fatalf("cursor = %d after home, want 0", m.commandCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.commandCursor != len(m.filteredCommands())-1 {
		t.Fatalf("cursor = %d after end, want %d", m.commandCursor, len(m.filteredCommands())-1)
	}

	// Backspace edits the query; backspacing an empty query is a no-op.
	for _, r := range []rune("save") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := string(m.commandQuery); got != "save" {
		t.Fatalf("query = %q, want %q", got, "save")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if got := string(m.commandQuery); got != "sav" {
		t.Fatalf("query = %q after backspace, want %q", got, "sav")
	}
	for len(m.commandQuery) > 0 {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if len(m.commandQuery) != 0 {
		t.Fatalf("query = %q after backspacing an empty query", m.commandQuery)
	}

	// ctrl+c closes the palette and requests quit.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(*Model)
	if m.showCommandPalette {
		t.Fatal("showCommandPalette = true after ctrl+c")
	}
	if cmd == nil {
		t.Fatal("ctrl+c from the palette did not request quit")
	}
}

func TestCommandPalette_RevealCursorScrolls(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 40, 10) // short palette: the catalog overflows the viewport
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m.View() // renders the palette and seeds commandLineOffsets

	// An out-of-range cursor makes reveal a no-op.
	m.commandCursor = 999
	m.revealCommandCursor()

	// end jumps to the last row and scrolls the viewport down; moving up
	// scrolls back once the cursor leaves the visible window.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	afterEnd := m.commandViewport.YOffset
	if afterEnd == 0 {
		t.Fatalf("end did not scroll the palette down (YOffset = 0)")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.commandViewport.YOffset >= afterEnd {
		t.Errorf("moving up from the last row did not scroll back (YOffset %d -> %d)", afterEnd, m.commandViewport.YOffset)
	}
}

func TestCommandPalette_NoMatchingCommands(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	for _, r := range []rune("zzzzz") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "no matching commands") {
		t.Errorf("filtered-out palette should show the empty state:\n%s", view)
	}
}

func TestCommandPalette_ViewSizing(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})

	// Direct calls exercise the size clamps without needing a window.
	for _, size := range []struct{ w, h int }{{0, 10}, {10, 0}, {2, 2}, {5, 3}, {30, 8}, {120, 60}} {
		if got := m.commandPaletteView(size.w, size.h); got == "" {
			t.Errorf("commandPaletteView(%d, %d) rendered empty", size.w, size.h)
		}
	}
	m.syncCommandViewport(0, 5, commandDefinitions())
	m.syncCommandViewport(5, 0, commandDefinitions())
	if got := renderCommandPaletteRow("↑↓", "label", 3, false, false); got == "" {
		t.Error("renderCommandPaletteRow with a tiny width rendered empty")
	}
	if got := renderCommandPaletteRow("↑↓", "label", 20, true, false); got == "" {
		t.Error("selected row rendered empty")
	}

	// Full renders at small windows keep the modal inside the terminal.
	m = resize(m, 20, 8)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if view := stripANSI(m.View()); view == "" {
		t.Fatal("palette view at 20x8 rendered empty")
	}
	m = resize(m, 10, 5)
	if view := stripANSI(m.View()); view == "" {
		t.Fatal("palette view at 10x5 rendered empty")
	}

	// renderLineOnSurface clamps a non-positive width to one column.
	if got := renderLineOnSurface("x", 0, lipgloss.AdaptiveColor{}); got == "" {
		t.Error("renderLineOnSurface at width 0 rendered empty")
	}

	// modalOverlay clamps an oversized modal into the terminal, centres it
	// vertically, and fills leftover blank cells with the modal surface.
	for _, tc := range []struct {
		text string
		w, h int
	}{
		{text: strings.Repeat("x", 20), w: 5, h: 5}, // wider than the terminal
		{text: "aaa\nbb", w: 5, h: 5},               // shorter line leaves blank cells
		{text: "x", w: 5, h: 1},                     // taller than the available space
	} {
		if got := m.modalOverlay("base", tc.text, tc.w, tc.h); got == "" {
			t.Errorf("modalOverlay(%q, %d, %d) rendered empty", tc.text, tc.w, tc.h)
		}
	}
}
