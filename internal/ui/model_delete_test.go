package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	tea "github.com/charmbracelet/bubbletea"
)

// TestDeleteKey_DisabledOnDocumentRow verifies that d on a document row is
// a no-op: delete is a node operation only.
func TestDeleteKey_DisabledOnDocumentRow(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\n",
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	if m.selectedItem().hasNode {
		t.Fatal("precondition: the document row must be selected")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd != nil {
		t.Errorf("d on a document row returned a command, want none")
	}
	if m.pendingDelete != nil || m.showDiff {
		t.Error("delete state set on a document row, want untouched")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestDelete_ImportDirectiveRejected verifies the defensive guard: an
// import directive can never be deleted. Import directives are leaves
// (not visible tree rows), so the selection is set directly to exercise
// the guard.
func TestDelete_ImportDirectiveRejected(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "import sites/a.caddy\n",
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	importNode := caddyfile.Node{
		Kind:  caddyfile.KindDirective,
		Name:  "import",
		Range: caddyfile.SourceRange{Start: 0, End: 18, StartLine: 1, EndLine: 1},
	}
	m.items = []item{
		{key: itemKey(m.state.Graph.Root, nil), label: "Caddyfile", doc: m.state.Graph.Root, hasChildren: true},
		{key: itemKey(m.state.Graph.Root, &importNode), label: "import sites/a.caddy", depth: 1, doc: m.state.Graph.Root, node: importNode, hasNode: true},
	}
	m.cursor = 1
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd != nil {
		t.Errorf("d on an import directive returned a command, want none")
	}
	if !strings.Contains(m.statusMessage, "import directives cannot be deleted") {
		t.Errorf("statusMessage = %q, want the import guard hint", m.statusMessage)
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestDelete_ValidShowsDiffAndSaves verifies the happy path: d removes the
// node range, the delete diff opens with an explicit title, and Enter
// saves the exact Patch(original, range, empty) result.
func TestDelete_ValidShowsDiffAndSaves(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test
	if !m.selectedItem().hasNode || m.selectedItem().node.Name != "b.example.test" {
		t.Fatalf("precondition: b.example.test node must be selected")
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd == nil {
		t.Fatal("d must return a command")
	}
	msg := cmd()
	vmsg, ok := msg.(deleteValidatedMsg)
	if !ok {
		t.Fatalf("got %T, want deleteValidatedMsg", msg)
	}
	if vmsg.Err != nil {
		t.Fatalf("delete validation error: %v", vmsg.Err)
	}
	m.Update(vmsg)
	if !m.showDiff {
		t.Fatal("diff not open after a valid delete")
	}
	if m.diffTitle != "Delete · config/Caddyfile" {
		t.Errorf("diffTitle = %q, want the explicit delete title", m.diffTitle)
	}
	if m.pendingDelete == nil {
		t.Fatal("pendingDelete not set")
	}
	// The diff footer advertises the delete confirmation.
	if !strings.Contains(stripANSI(m.View()), "Enter delete") {
		t.Errorf("delete diff footer missing 'Enter delete':\n%s", m.View())
	}
	expected, err := caddyfile.Patch([]byte(src), state.Graph.Root.Nodes[1].Range, []byte{})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	// Enter saves the deletion through the normal pipeline.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter in the delete diff must return a command")
	}
	resMsg := cmd()
	if _, ok := resMsg.(saveResultMsg); !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	if saver.capturedPath != "config/Caddyfile" {
		t.Errorf("saver path = %q, want the root", saver.capturedPath)
	}
	if string(saver.capturedOriginal) != src {
		t.Errorf("saver original = %q, want %q", saver.capturedOriginal, src)
	}
	if !bytes.Equal(saver.capturedWorking, expected) {
		t.Errorf("saver working = %q, want the node removed: %q", saver.capturedWorking, expected)
	}
}

// TestDelete_PreservesCommentsOutsideNode verifies that deleting a node
// preserves every byte outside its range, including surrounding comments.
func TestDelete_PreservesCommentsOutsideNode(t *testing.T) {
	src := "# header comment\n\nexample.test {\n\trespond ok\n}\n\n# footer comment\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // example.test node

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	msg := cmd()
	vmsg := msg.(deleteValidatedMsg)
	m.Update(vmsg)
	if !m.showDiff {
		t.Fatal("diff not open")
	}
	expected, err := caddyfile.Patch([]byte(src), state.Graph.Root.Nodes[0].Range, []byte{})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter must return the save command")
	}
	cmd()
	if !bytes.Equal(saver.capturedWorking, expected) {
		t.Errorf("saver working = %q, want comments preserved byte-for-byte: %q", saver.capturedWorking, expected)
	}
	if !strings.Contains(string(saver.capturedWorking), "# header comment") ||
		!strings.Contains(string(saver.capturedWorking), "# footer comment") {
		t.Errorf("saved content lost a surrounding comment: %q", saver.capturedWorking)
	}
}

// TestDelete_InvalidShowsDiagnostics verifies that a delete candidate that
// fails validation opens the diagnostics modal and never reaches the saver.
func TestDelete_InvalidShowsDiagnostics(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	msg := cmd()
	vmsg := msg.(deleteValidatedMsg)
	m.Update(vmsg)
	if !m.showDiagnostics {
		t.Fatal("showDiagnostics = false, want true for an invalid delete")
	}
	if m.showDiff {
		t.Error("showDiff = true for an invalid delete, want false")
	}
	if m.pendingDelete != nil {
		t.Error("pendingDelete set for an invalid delete, want nil")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestDelete_EscCancels verifies that Esc from the delete diff cancels the
// deletion without touching the saver.
func TestDelete_EscCancels(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	msg := cmd()
	m.Update(msg.(deleteValidatedMsg))
	if !m.showDiff {
		t.Fatal("delete diff not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showDiff {
		t.Error("showDiff = true after Esc, want false")
	}
	if m.pendingDelete != nil {
		t.Error("pendingDelete not discarded after Esc, want nil")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
	if !strings.Contains(m.statusMessage, "delete cancelled") {
		t.Errorf("statusMessage = %q, want a delete-cancelled hint", m.statusMessage)
	}
}

// TestDelete_ImportedNode verifies that deleting a node of an imported
// file saves to that file and leaves the root intact.
func TestDelete_ImportedNode(t *testing.T) {
	importedSrc := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	fs := map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": importedSrc,
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	formatter := &fakeFormatter{}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/a.caddy.bak"}}}
	m := newLoadedModel(t, loader, formatter, saver)
	m = resize(m, 120, 30)
	// items: root doc, a.caddy doc, a.example.test, b.example.test.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.caddy doc
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test
	if !m.selectedItem().hasNode || m.selectedItem().node.Name != "b.example.test" {
		t.Fatalf("precondition: b.example.test of the imported file must be selected")
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	msg := cmd()
	m.Update(msg.(deleteValidatedMsg))
	if !m.showDiff {
		t.Fatal("delete diff not open")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("Enter must return the save command")
	}
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	if saver.capturedPath != "config/sites/a.caddy" {
		t.Errorf("saver path = %q, want the imported file", saver.capturedPath)
	}
	updated, _ = m.Update(res)
	m = updated.(*Model)
	var importedDoc *caddyfile.Document
	for _, d := range m.state.Graph.Documents {
		if d.Path == "config/sites/a.caddy" {
			importedDoc = d
		}
	}
	if importedDoc == nil || strings.Contains(string(importedDoc.Source), "b.example.test") {
		t.Errorf("imported doc Source = %q, want b.example.test removed", importedDoc.Source)
	}
	if string(m.state.Graph.Root.Source) != "import sites/a.caddy\n" {
		t.Errorf("root Source = %q, want it untouched", m.state.Graph.Root.Source)
	}
}

// TestDelete_AfterSaveTreeRebuilt verifies that a successful delete
// rebuilds the tree (one fewer node row), re-anchors the selection on the
// stable document row and updates the source pane.
func TestDelete_AfterSaveTreeRebuilt(t *testing.T) {
	fs := map[string]string{"config/Caddyfile": "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"}
	loader := app.NewLoader(config.Settings{ConfigPath: "config/Caddyfile", ReadOnly: false, BackupDir: "config/backups"}, fsReader(fs))
	formatter := &fakeFormatter{}
	saver := &diskSaver{fs: fs, fakeSaver: fakeSaver{result: app.SaveResult{BackupPath: "config/backups/Caddyfile.bak"}}}
	m := newLoadedModel(t, loader, formatter, saver)
	m = resize(m, 120, 30)
	before := len(m.items)
	if before != 3 {
		t.Fatalf("items = %d before delete, want 3", before)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	msg := cmd()
	m.Update(msg.(deleteValidatedMsg))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = updated.(*Model)
	resMsg := cmd()
	res, ok := resMsg.(saveResultMsg)
	if !ok {
		t.Fatalf("got %T, want saveResultMsg", resMsg)
	}
	updated, _ = m.Update(res)
	m = updated.(*Model)

	if len(m.items) != before-1 {
		t.Errorf("items = %d after delete, want %d; items = %v", len(m.items), before-1, itemLabels(m.items))
	}
	sel := m.selectedItem()
	if sel == nil || sel.hasNode || sel.doc == nil || sel.doc.Path != "config/Caddyfile" {
		t.Errorf("selection = %+v, want the stable document row after delete", sel)
	}
	m.View()
	if strings.Contains(stripANSI(m.viewport.View()), "b.example.test") {
		t.Errorf("source pane still shows the deleted node:\n%s", m.viewport.View())
	}
}

// TestDelete_FooterShowsKey verifies that the footer lists d delete only on
// node rows in writable mode.
func TestDelete_FooterShowsKey(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\n",
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	// The document row exposes only navigation and the palette.
	if strings.Contains(stripANSI(m.View()), "d delete") || !strings.Contains(stripANSI(m.View()), "? commands") {
		t.Errorf("footer should stay navigation-only:\n%s", m.View())
	}
	// On a node row it remains compact as well.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(stripANSI(m.View()), "d delete") {
		t.Errorf("footer should not list delete on a node row:\n%s", m.View())
	}
	// Read-only: hidden even on a node row.
	readOnly := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "a.example.test {\n\trespond a\n}\n",
	}))
	m2 := newLoadedModel(t, fakeLoader{state: readOnly}, formatter)
	m2 = resize(m2, 120, 30)
	m2 = keyPress(t, m2, tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(stripANSI(m2.View()), "d delete") {
		t.Errorf("footer shows 'd delete' in read-only mode:\n%s", m2.View())
	}
	// Without a formatter the key is hidden even on a writable node row:
	// the delete flow validates before the diff, so the key would fail at
	// the first press.
	saverOnly := &fakeSaver{}
	m3 := newLoadedModel(t, fakeLoader{state: state}, saverOnly)
	m3 = resize(m3, 120, 30)
	m3 = keyPress(t, m3, tea.KeyMsg{Type: tea.KeyDown})
	if strings.Contains(stripANSI(m3.View()), "d delete") {
		t.Errorf("footer shows 'd delete' without a formatter:\n%s", m3.View())
	}
}

// TestDelete_DiagnosticsWithErrorOpensModal verifies that a delete whose
// validation returns both diagnostics and an error (the real
// FormatAndValidate contract when Caddy rejects the configuration) opens
// the diagnostics modal instead of only surfacing a status line.
func TestDelete_DiagnosticsWithErrorOpensModal(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags, err: errors.New("caddy exit 1")}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	msg := cmd()
	vmsg := msg.(deleteValidatedMsg)
	m.Update(vmsg)
	if !m.showDiagnostics {
		t.Fatal("showDiagnostics = false, want true when validation returns diagnostics alongside an error")
	}
	if m.showDiff {
		t.Error("showDiff = true, want false (invalid delete, no diff)")
	}
	if m.pendingDelete != nil {
		t.Error("pendingDelete set for an invalid delete, want nil")
	}
	if saver.calls != 0 {
		t.Errorf("saver.calls = %d, want 0", saver.calls)
	}
}

// TestDelete_SecondPressIgnoredWhileValidating verifies that a second d
// press while the first delete is still validating is ignored, so two
// concurrent validations cannot overwrite each other.
func TestDelete_SecondPressIgnoredWhileValidating(t *testing.T) {
	src := "a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n"
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // a.example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // b.example.test

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd == nil {
		t.Fatal("first d must return a command")
	}
	if !m.deleting {
		t.Error("deleting = false after the first d, want true")
	}
	// A second d while validating is a no-op: no command, no extra call.
	_, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd2 != nil {
		t.Error("second d returned a command, want none")
	}
	msg := cmd() // runs FormatAndValidate exactly once
	if formatter.calls != 1 {
		t.Errorf("formatter.calls = %d, want 1 (the second d must not trigger a call)", formatter.calls)
	}
	vmsg, ok := msg.(deleteValidatedMsg)
	if !ok {
		t.Fatalf("got %T, want deleteValidatedMsg", msg)
	}
	updated, _ := m.Update(vmsg)
	m = updated.(*Model)
	if m.deleting {
		t.Error("deleting = true after the result, want false")
	}
	if !m.showDiff {
		t.Fatal("delete diff not open after the validated result")
	}
}

func TestDelete_NoGraphIsNoOp(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, &fakeFormatter{})
	m.state = nil
	updated, cmd := m.startDelete()
	if updated != m || cmd != nil {
		t.Fatalf("startDelete without a graph returned (%v, %v), want no-op", updated != m, cmd != nil)
	}
}

func TestDelete_ReadOnlyShowsHint(t *testing.T) {
	state := stateFor(t, "Caddyfile", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // select the node row
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if !strings.Contains(m.statusMessage, "read-only mode — start with --write") {
		t.Errorf("statusMessage = %q, want the read-only hint", m.statusMessage)
	}
}

func TestDelete_NoFormatterShowsHint(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{})
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if !strings.Contains(m.statusMessage, "validation unavailable") {
		t.Errorf("statusMessage = %q, want the missing-binary hint", m.statusMessage)
	}
}

func TestDelete_InvalidRangeSurfacesError(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, &fakeFormatter{})
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	// Corrupt the selected node's range so caddyfile.Patch rejects it.
	for i := range m.items {
		if m.items[i].hasNode {
			m.items[i].node.Range = caddyfile.SourceRange{Start: 5000, End: 6000}
			break
		}
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if !strings.Contains(m.statusMessage, "✗ delete failed") {
		t.Errorf("statusMessage = %q, want the patch failure", m.statusMessage)
	}
}

func TestDelete_WarningsNotApplied(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	diags := []validator.Diagnostic{
		{Severity: validator.SeverityWarning, Message: "deprecated directive", Path: "Caddyfile", Line: 1},
	}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, &fakeFormatter{diagnostics: diags})
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if !m.deleting {
		t.Fatal("deleting = false after starting the delete")
	}
	updated, _ := m.Update(deleteValidatedMsg{
		Path:        "Caddyfile",
		Original:    []byte("example.test {\n\trespond ok\n}\n"),
		Content:     []byte("example.test {\n\trespond ok\n}\n"),
		Diagnostics: diags,
	})
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "✗ delete has warnings — not applied") {
		t.Errorf("statusMessage = %q, want the warnings message", m.statusMessage)
	}
	if m.showDiff || m.pendingDelete != nil {
		t.Error("warning-only delete was applied anyway")
	}
}

func TestDelete_ValidationErrorIsInfrastructure(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, &fakeFormatter{err: errors.New("caddy missing")})
	m = resize(m, 100, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if !m.deleting {
		t.Fatal("deleting = false after starting the delete")
	}
	updated, _ := m.Update(deleteValidatedMsg{
		Path:     "Caddyfile",
		Original: []byte("example.test {\n\trespond ok\n}\n"),
		Content:  []byte("example.test {\n}\n"),
		Err:      errors.New("caddy missing"),
	})
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "✗ delete validation failed: caddy missing") {
		t.Errorf("statusMessage = %q, want the validation failure", m.statusMessage)
	}
}
