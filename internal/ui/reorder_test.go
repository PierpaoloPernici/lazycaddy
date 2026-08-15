package ui

import (
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
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

// runReorderAndSave drives the full reorder flow on a loaded model: select a
// tree row, open the picker, confirm the first target, validate, review the
// diff and save. It returns the model after the save message has been handled,
// with the tree reloaded and the selection re-anchored.
func runReorderAndSave(t *testing.T, m *Model, row int) *Model {
	t.Helper()
	if row < 0 || row >= len(m.items) {
		t.Fatalf("row %d out of range (items: %d)", row, len(m.items))
	}
	m.cursor = row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if !m.showStructuredAdd || m.structuredAddMode != structuredAddReorder {
		t.Fatalf("reorder picker did not open: show:%v mode:%v", m.showStructuredAdd, m.structuredAddMode)
	}
	if len(m.structuredAddReorderTargets) == 0 {
		t.Fatal("reorder picker has no targets")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("reorder submit did not return a validation command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if m.pendingEdit == nil || !m.showDiff {
		t.Fatalf("reorder did not reach the diff review: pendingEdit:%v showDiff:%v", m.pendingEdit != nil, m.showDiff)
	}
	updated, saveCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if saveCmd == nil {
		t.Fatal("reorder diff did not return a save command")
	}
	updated, _ = m.Update(saveCmd())
	return updated.(*Model)
}

func TestReorderSaveReanchorsMovedBlockRepeatedNames(t *testing.T) {
	t.Run("backward across structural sibling", func(t *testing.T) {
		fs := map[string]string{
			"config/Caddyfile": "example.test {\n\thandle /a {\n\t\trespond a\n\t}\n\thandle /b {\n\t\trespond b\n\t}\n\thandle /c {\n\t\trespond c\n\t}\n}\n",
		}
		loader := app.NewLoader(config.Settings{
			ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups",
		}, fsReader(fs))
		saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile"}}}
		m := newLoadedModel(t, loader, &fakeFormatter{}, saver)
		m = resize(m, 120, 30)

		// Expand the site row so the nested handle rows appear.
		m.cursor = 1 // example.test
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight})

		// rows: doc(0), example.test(1), handle /a(2), handle /b(3), handle /c(4)
		m = runReorderAndSave(t, m, 4) // move handle /c after handle /a

		selected := m.selectedItem()
		if selected == nil || !selected.hasNode || selected.node.Name != "handle" {
			t.Fatalf("selection after backward reorder = %+v, want a handle row", selected)
		}
		if got := selected.node.Range.StartLine; got != 5 {
			t.Errorf("selected handle starts at line %d, want 5 (the moved handle /c)", got)
		}
		want := "example.test {\n\thandle /a {\n\t\trespond a\n\t}\n\thandle /c {\n\t\trespond c\n\t}\n\thandle /b {\n\t\trespond b\n\t}\n}\n"
		if got := string(m.state.Graph.Root.Source); got != want {
			t.Errorf("root source after backward reorder = %q, want %q", got, want)
		}
	})

	t.Run("forward unequal heights", func(t *testing.T) {
		fs := map[string]string{
			"config/Caddyfile": "example.test {\n\thandle {\n\t\trespond nf 404\n\t}\n\thandle /api/* {\n\t\treverse_proxy localhost:8080\n\t\t# pad\n\t\t# pad\n\t\t# pad\n\t\t# pad\n\t\t# pad\n\t\t# pad\n\t\t# pad\n\t\t# pad\n\t\t# pad\n\t\t# pad\n\t}\n}\n",
		}
		loader := app.NewLoader(config.Settings{
			ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups",
		}, fsReader(fs))
		saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile"}}}
		m := newLoadedModel(t, loader, &fakeFormatter{}, saver)
		m = resize(m, 120, 30)

		// Expand the site row so the nested handle rows appear.
		m.cursor = 1 // example.test
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight})

		// rows: doc(0), example.test(1), small handle(2), big handle /api/*(3)
		m = runReorderAndSave(t, m, 2) // move the small catch-all after handle /api/*

		selected := m.selectedItem()
		if selected == nil || !selected.hasNode || selected.node.Name != "handle" {
			t.Fatalf("selection after forward reorder = %+v, want a handle row", selected)
		}
		// the small handle lands right after the 13-line /api block
		if got := selected.node.Range.StartLine; got != 15 {
			t.Errorf("selected handle starts at line %d, want 15 (the moved catch-all)", got)
		}
	})
}

func TestReorderPickerFiltersUnusableTargets(t *testing.T) {
	t.Run("last block only offers valid backward target", func(t *testing.T) {
		src := "example.test {\n\thandle /a {\n\t\trespond a\n\t}\n\thandle /b {\n\t\trespond b\n\t}\n\thandle /c {\n\t\trespond c\n\t}\n}\n"
		doc := caddyfile.Parse([]byte(src))
		if doc.Err != nil {
			t.Fatalf("Parse: %v", doc.Err)
		}
		m := newLoadedModel(t, fakeLoader{state: writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
			"config/Caddyfile": src,
		}))}, &fakeFormatter{}, &fakeSaver{})
		parent := doc.Nodes[0]
		handles := []caddyfile.Node{parent.Children[0], parent.Children[1], parent.Children[2]}
		targets := m.reorderTargets(doc, handles[2])
		if len(targets) != 1 || nodeKey(&targets[0]) != nodeKey(&handles[0]) {
			t.Fatalf("reorder targets for last handle = %#v, want only handle /a (backward move)", targets)
		}
	})

	t.Run("leaf gap leaves picker without targets", func(t *testing.T) {
		src := "a.example.test {\n\thandle /x {\n\t\trespond x\n\t}\n\trespond a\n\thandle /y {\n\t\trespond y\n\t}\n}\n"
		doc := caddyfile.Parse([]byte(src))
		if doc.Err != nil {
			t.Fatalf("Parse: %v", doc.Err)
		}
		state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
			"config/Caddyfile": src,
		}))
		m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
		parent := doc.Nodes[0]
		last := parent.Children[len(parent.Children)-1]
		if got := len(m.reorderTargets(doc, last)); got != 0 {
			t.Fatalf("reorder targets for last handle across a leaf = %d, want 0 (backward move is refused)", got)
		}
		// Selecting the last handle makes the reorder command unavailable:
		// the only visible sibling is behind a leaf directive.
		m.items = []item{
			{key: itemKey(doc, nil), label: "Caddyfile", doc: doc, hasChildren: true},
			{key: itemKey(doc, &parent), label: parent.Name, doc: doc, node: parent, hasNode: true, hasChildren: true},
			{key: itemKey(doc, &last), label: last.Name, doc: doc, node: last, hasNode: true},
		}
		m.cursor = 2
		if m.canReorderSelected() {
			t.Fatal("canReorderSelected = true for the last handle behind a leaf, want false")
		}
	})
}

func TestReorderPickerNavigation(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\nc.example.test {\n\trespond c\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if len(m.structuredAddReorderTargets) != 2 {
		t.Fatalf("reorder targets = %d, want 2", len(m.structuredAddReorderTargets))
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "Move block after") {
		t.Errorf("reorder picker view missing title:\n%s", view)
	}
	if m.structuredAddCursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.structuredAddCursor != 1 {
		t.Fatalf("cursor after down = %d, want 1", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // clamp at the end
	if m.structuredAddCursor != 1 {
		t.Fatalf("cursor after clamped down = %d, want 1", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.structuredAddCursor != 0 {
		t.Fatalf("cursor after up = %d, want 0", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp}) // clamp at the start
	if m.structuredAddCursor != 0 {
		t.Fatalf("cursor after clamped up = %d, want 0", m.structuredAddCursor)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.showStructuredAdd || !strings.Contains(m.statusMessage, "reorder cancelled") {
		t.Fatalf("ctrl+c state = show:%v status:%q, want closed and cancelled", m.showStructuredAdd, m.statusMessage)
	}
}

func TestReorderCommandPaletteInvocation(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	for _, r := range []rune("after sibling") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.showCommandPalette {
		t.Fatal("showCommandPalette = true after executing reorder from the palette")
	}
	if !m.showStructuredAdd || m.structuredAddMode != structuredAddReorder {
		t.Fatalf("palette reorder state = show:%v mode:%v, want open reorder picker", m.showStructuredAdd, m.structuredAddMode)
	}
	if len(m.structuredAddReorderTargets) != 1 || m.structuredAddReorderTargets[0].Name != "b.example.test" {
		t.Fatalf("palette reorder targets = %#v, want b.example.test", m.structuredAddReorderTargets)
	}
}

func TestReorderValidationErrorBlocksDiff(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{diagnostics: []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("reorder submit did not return a validation command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if m.pendingEdit != nil || m.showDiff {
		t.Fatalf("reorder with validation errors reached the diff: pendingEdit:%v showDiff:%v", m.pendingEdit != nil, m.showDiff)
	}
	if !strings.Contains(m.statusMessage, "did not validate") {
		t.Errorf("statusMessage = %q, want validation refusal", m.statusMessage)
	}
}
func TestReorderSubmitDefensiveBranches(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if len(m.structuredAddReorderTargets) != 1 {
		t.Fatalf("reorder targets = %d, want 1", len(m.structuredAddReorderTargets))
	}

	// Nil document: submit is a defensive no-op.
	m.structuredAddDoc = nil
	updated, cmd := m.submitReorder()
	if updated != m || cmd != nil {
		t.Fatalf("submit with nil doc returned cmd:%v model change, want no-op", cmd != nil)
	}

	// Empty targets: submit is a defensive no-op.
	m = newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m.structuredAddReorderTargets = nil
	updated, cmd = m.submitReorder()
	if updated != m || cmd != nil {
		t.Fatalf("submit with empty targets returned cmd:%v model change, want no-op", cmd != nil)
	}

	// Out-of-range cursor: submit is a defensive no-op.
	m = newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m.structuredAddCursor = 99
	updated, cmd = m.submitReorder()
	if updated != m || cmd != nil {
		t.Fatalf("submit with out-of-range cursor returned cmd:%v model change, want no-op", cmd != nil)
	}

	// A planner rejection surfaces as a status message.
	m = newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m.structuredAddParent = caddyfile.Node{Kind: caddyfile.KindGlobalOptions, Name: "", Range: caddyfile.SourceRange{Start: 0, End: 1, StartLine: 1}}
	updated, cmd = m.submitReorder()
	if cmd != nil {
		t.Fatalf("submit with rejected plan returned a command, want status message")
	}
	if !strings.Contains(m.statusMessage, "✗ reorder unavailable") {
		t.Errorf("statusMessage = %q, want the planner rejection", m.statusMessage)
	}
}
