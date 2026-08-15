package ui

import (
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	tea "github.com/charmbracelet/bubbletea"
)

func TestReorderPlansValidatesAndOpensDiff(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = updated.(*Model)
	if cmd != nil {
		t.Fatal("reorder picker returned a command, want none")
	}
	if !m.showStructuredAdd || m.structuredAddMode != structuredAddReorder {
		t.Fatalf("reorder modal state = show:%v mode:%v, want open reorder picker", m.showStructuredAdd, m.structuredAddMode)
	}
	if len(m.structuredAddReorderTargets) != 1 || m.structuredAddReorderTargets[0].Name != "b.example.test" {
		t.Fatalf("reorder targets = %#v, want b.example.test", m.structuredAddReorderTargets)
	}

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("reorder submit did not return validation command")
	}
	if m.showStructuredAdd || !m.structuredAddBusy {
		t.Fatalf("reorder submit state = show:%v busy:%v, want closed and busy", m.showStructuredAdd, m.structuredAddBusy)
	}

	rawMsg := cmd()
	msg, ok := rawMsg.(structuredAddValidatedMsg)
	if !ok {
		t.Fatalf("validation message = %T, want structuredAddValidatedMsg", cmd())
	}
	if msg.Operation != "reorder" {
		t.Fatalf("validation operation = %q, want reorder", msg.Operation)
	}
	updated, _ = m.Update(msg)
	m = updated.(*Model)
	if m.pendingEdit == nil || m.pendingEdit.operation != "reorder" {
		t.Fatalf("pendingEdit = %+v, want reorder", m.pendingEdit)
	}
	want := "b.example.test {\n\trespond b\n}\na.example.test {\n\trespond a\n}\n"
	if string(m.pendingEdit.content) != want {
		t.Errorf("reordered content = %q, want %q", m.pendingEdit.content, want)
	}
	if !m.showDiff || !strings.Contains(m.diffTitle, "Move a.example.test after b.example.test") {
		t.Fatalf("diff state = show:%v title:%q, want reorder diff", m.showDiff, m.diffTitle)
	}
	if !strings.Contains(stripANSI(m.View()), "Enter move after") {
		t.Error("reorder diff footer does not advertise the reorder confirmation")
	}
	if formatter.calls != 1 {
		t.Errorf("formatter calls = %d, want 1", formatter.calls)
	}
}

func TestReorderDisabledOnDocumentRowAndSingleBlock(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{name: "document row", src: "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"},
		{name: "single block", src: "a.example.test {\n\trespond a\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
				"config/Caddyfile": tc.src,
			}))
			m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
			m = resize(m, 120, 30)
			if tc.name == "single block" {
				m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
			}
			updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
			m = updated.(*Model)
			if cmd != nil || m.showStructuredAdd {
				t.Fatalf("reorder state = cmd:%v show:%v, want no-op", cmd != nil, m.showStructuredAdd)
			}
		})
	}
}

func TestReorderCancelClosesPicker(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if !m.showStructuredAdd {
		t.Fatal("reorder picker did not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showStructuredAdd || !strings.Contains(m.statusMessage, "reorder cancelled") {
		t.Fatalf("cancel state = show:%v status:%q, want closed and cancelled", m.showStructuredAdd, m.statusMessage)
	}
}

func TestReorderSaveReanchorsMovedSourceNode(t *testing.T) {
	fs := map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n",
	}
	loader := app.NewLoader(config.Settings{
		ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups",
	}, fsReader(fs))
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile"}}}
	m := newLoadedModel(t, loader, &fakeFormatter{}, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("reorder submit did not return validation command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	updated, saveCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if saveCmd == nil {
		t.Fatal("reorder diff did not return save command")
	}
	updated, _ = m.Update(saveCmd())
	m = updated.(*Model)
	selected := m.selectedItem()
	if selected == nil || !selected.hasNode || selected.node.Name != "a.example.test" {
		t.Fatalf("selection after reorder save = %+v, want moved a.example.test node", selected)
	}
	if got := string(m.state.Graph.Root.Source); got != fs["config/Caddyfile"] {
		t.Fatalf("root source after reorder save = %q, want saved bytes %q", got, fs["config/Caddyfile"])
	}
}

func TestReorderSkipsHiddenLeafTargets(t *testing.T) {
	src := "a.example.test {\n\thandle /x {\n\t\trespond x\n\t}\n\trespond a\n\thandle /y {\n\t\trespond y\n\t}\n}\n"
	doc := caddyfile.Parse([]byte(src))
	if doc.Err != nil {
		t.Fatalf("Parse: %v", doc.Err)
	}
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m.items = []item{{key: itemKey(doc, nil), label: "Caddyfile", doc: doc, hasChildren: true}}
	m.state.Graph.Root = doc
	parent := doc.Nodes[0]
	selected := parent.Children[0]
	if got := len(m.reorderTargets(doc, selected)); got != 1 {
		t.Fatalf("reorder targets = %d, want one visible sibling", got)
	}
}
