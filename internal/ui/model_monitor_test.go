package ui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
	tea "github.com/charmbracelet/bubbletea"
)

// fakeMonitor is a programmable app.ChangeMonitor for deterministic tests.
// Next returns the results pre-loaded into nextCh, in order.
type fakeMonitor struct {
	updateCalls int
	targets     []app.ChangeTarget
	updateErr   error
	closeCalls  int
	nextCh      chan monitorResult
}

type monitorResult struct {
	change app.ExternalChange
	err    error
}

func newFakeMonitor() *fakeMonitor {
	return &fakeMonitor{nextCh: make(chan monitorResult, 8)}
}

func (f *fakeMonitor) Update(targets []app.ChangeTarget) error {
	f.updateCalls++
	f.targets = append([]app.ChangeTarget(nil), targets...)
	return f.updateErr
}

func (f *fakeMonitor) Next(ctx context.Context) (app.ExternalChange, error) {
	select {
	case r := <-f.nextCh:
		return r.change, r.err
	case <-ctx.Done():
		return app.ExternalChange{}, ctx.Err()
	}
}

func (f *fakeMonitor) Close() error {
	f.closeCalls++
	return nil
}

func TestExternalChange_OpensConflictModalAndReloads(t *testing.T) {
	fs := map[string]string{"Caddyfile": "old {\n\trespond / \"a\"\n}\n"}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)

	// Load targeted the monitor at the resolved document.
	if mon.updateCalls != 1 || len(mon.targets) != 1 || mon.targets[0].Path != "Caddyfile" {
		t.Fatalf("monitor targets = %v (calls %d), want the root document", mon.targets, mon.updateCalls)
	}

	// The file changes on disk while browsing.
	fs["Caddyfile"] = "new {\n\trespond / \"b\"\n}\n"
	m, cmd := press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte(fs["Caddyfile"])}})
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open")
	}
	if cmd != nil {
		t.Fatal("watch must not re-arm while the conflict modal is open")
	}
	if m.hasUnsavedEdits() {
		t.Fatal("a fresh load must have no unsaved edits")
	}

	// No unsaved edits: Enter reloads the graph from disk.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.showChangeConflict {
		t.Fatal("conflict modal stayed open after reload")
	}
	if string(m.state.Graph.Root.Source) != fs["Caddyfile"] {
		t.Errorf("root source = %q, want the reloaded on-disk bytes", m.state.Graph.Root.Source)
	}
	if !strings.Contains(m.statusMessage, "reloaded") {
		t.Errorf("statusMessage = %q, want a reloaded notification", m.statusMessage)
	}
}

func TestExternalChange_KeepRetainsInMemoryVersionAndReArms(t *testing.T) {
	fs := map[string]string{"Caddyfile": "old {\n}\n"}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)

	fs["Caddyfile"] = "new {\n}\n"
	m, _ = press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte(fs["Caddyfile"])}})
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open")
	}

	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if m.showChangeConflict {
		t.Fatal("keep must close the conflict modal")
	}
	if string(m.state.Graph.Root.Source) != "old {\n}\n" {
		t.Errorf("keep must retain the in-memory version, got %q", m.state.Graph.Root.Source)
	}
	if cmd == nil {
		t.Fatal("keep must re-arm the watch")
	}
	if !strings.Contains(m.statusMessage, "kept") {
		t.Errorf("statusMessage = %q, want a kept notification", m.statusMessage)
	}
}

func TestExternalChange_UnsavedEditsCompareAndKeep(t *testing.T) {
	fs := map[string]string{"Caddyfile": "original {\n\trespond / \"a\"\n}\n"}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)

	// The operator validated a working copy that differs from disk.
	m.workingBytes = []byte("edited {\n\trespond / \"b\"\n}\n")
	m.workingValidated = true

	fs["Caddyfile"] = "original {\n\trespond / \"a\"\n}\n# external\n"
	m, _ = press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte(fs["Caddyfile"])}})
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open")
	}
	if !m.hasUnsavedEdits() {
		t.Fatal("a divergent working copy must count as unsaved edits")
	}

	// c opens the compare diff (in-memory vs on-disk) over the modal.
	m, cmd := press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if cmd != nil {
		t.Fatal("compare must not re-arm the watch")
	}
	if !m.showDiff || !m.changeCompare {
		t.Fatal("compare diff did not open")
	}
	if !strings.Contains(m.diffTitle, "Compare") {
		t.Errorf("diff title = %q, want a Compare title", m.diffTitle)
	}
	hasChange := false
	for _, l := range m.diffLines {
		if l.Kind == diff.KindAdd || l.Kind == diff.KindRemove {
			hasChange = true
		}
	}
	if !hasChange {
		t.Fatal("compare diff has no +/- lines")
	}

	// Esc from the compare returns to the conflict options.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.showChangeConflict || m.changeCompare || m.showDiff {
		t.Fatal("Esc from compare must return to the conflict options")
	}

	// Esc (keep) closes the flow and retains the working copy.
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showChangeConflict {
		t.Fatal("keep must close the conflict modal")
	}
	if !bytes.Equal(m.workingBytes, []byte("edited {\n\trespond / \"b\"\n}\n")) {
		t.Fatal("keep must retain the working copy")
	}
	if cmd == nil {
		t.Fatal("keep must re-arm the watch")
	}
}

func TestExternalChange_ReloadDiscardsWorkingCopy(t *testing.T) {
	fs := map[string]string{"Caddyfile": "old {\n}\n"}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)

	m.workingBytes = []byte("edited {\n}\n")
	m.workingValidated = true
	fs["Caddyfile"] = "new {\n}\n"
	m, _ = press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte(fs["Caddyfile"])}})

	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.showChangeConflict {
		t.Fatal("reload must close the conflict modal")
	}
	if m.workingBytes != nil || m.workingValidated {
		t.Fatal("reload must discard the in-memory working copy")
	}
	if string(m.state.Graph.Root.Source) != "new {\n}\n" {
		t.Errorf("root source = %q, want the reloaded bytes", m.state.Graph.Root.Source)
	}
}

func TestExternalChange_IgnoredWhenOnDiskMatchesMemory(t *testing.T) {
	const source = "example.test {\n}\n"
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte(source), nil })
	mon := newFakeMonitor()
	m := newLoadedModel(t, fakeLoader{state: state}, mon)

	// The notification races lazycaddy's own atomic save: the on-disk
	// bytes already equal the in-memory document, so it is a non-event.
	m, cmd := press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte(source)}})
	if m.showChangeConflict {
		t.Fatal("a no-op change must not open the conflict modal")
	}
	if cmd == nil {
		t.Fatal("the watch must re-arm after a no-op change")
	}
}

func TestExternalChange_DuringSaveIgnored(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte("old\n"), nil })
	mon := newFakeMonitor()
	m := newLoadedModel(t, fakeLoader{state: state}, mon)

	// A save is in flight: its own ErrConflict guard covers the same
	// conflict, so the notification must not double-report.
	m.saving = true
	m, cmd := press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte("new\n")}})
	if m.showChangeConflict {
		t.Fatal("a change during save must not open the conflict modal")
	}
	if cmd == nil {
		t.Fatal("the watch must re-arm during save")
	}
}

func TestExternalChange_ImportedFileIdentifiedExactly(t *testing.T) {
	fs := map[string]string{
		"Caddyfile":   "example.test {\n\timport common.conf\n}\n",
		"common.conf": "# v1\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)
	if len(mon.targets) != 2 {
		t.Fatalf("monitor targets = %v, want root + imported document", mon.targets)
	}

	fs["common.conf"] = "# v2\n"
	m, _ = press(m, externalChangeMsg{change: app.ExternalChange{Path: "common.conf", OnDisk: []byte("# v2\n")}})
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open for the imported file")
	}
	if m.pendingChange == nil || m.pendingChange.change.Path != "common.conf" {
		t.Fatalf("pending change = %+v, want the imported file", m.pendingChange)
	}
	// The compare diff names the exact file that changed. Compare is
	// offered with unsaved edits; make the imported file's divergence
	// part of the conflict by retaining an unsaved working copy.
	m.workingBytes = []byte("edited root\n")
	m.workingValidated = true
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if !strings.Contains(m.diffTitle, "common.conf") {
		t.Errorf("compare title = %q, want the imported path", m.diffTitle)
	}
}

func TestExternalChange_MissingFileReloadSurfacesGraphError(t *testing.T) {
	fs := map[string]string{
		"Caddyfile":   "example.test {\n\timport common.conf\n}\n",
		"common.conf": "# v1\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)

	// The imported file disappears on disk.
	delete(fs, "common.conf")
	m, _ = press(m, externalChangeMsg{change: app.ExternalChange{Path: "common.conf", Missing: true}})
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open for a missing file")
	}

	// Reload re-reads the graph; the missing import is surfaced in the
	// graph error and the raw view stays available.
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.showChangeConflict {
		t.Fatal("reload must close the conflict modal")
	}
	if m.state.Graph == nil || m.state.Graph.Err == nil {
		t.Fatal("reload must surface the missing import error in the graph")
	}
}

func TestExternalChange_InitArmsWatchAndResolveReArms(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte("old\n"), nil })
	mon := newFakeMonitor()
	m := newLoadedModel(t, fakeLoader{state: state}, mon)

	// Init arms the watch; the cmd blocks on the monitor and delivers
	// the pre-loaded change as an externalChangeMsg.
	mon.nextCh <- monitorResult{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte("new\n")}}
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init must arm the watch")
	}
	msg, ok := cmd().(externalChangeMsg)
	if !ok || msg.err != nil || msg.change.Path != "Caddyfile" {
		t.Fatalf("Init watch cmd = %#v, want the queued change", msg)
	}

	// Delivering it opens the modal; keep re-arms the watch, whose cmd
	// then returns the next queued change.
	m, _ = press(m, msg)
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open")
	}
	m, cmd = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if cmd == nil {
		t.Fatal("keep must re-arm the watch")
	}
	mon.nextCh <- monitorResult{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte("newer\n")}}
	next, ok := cmd().(externalChangeMsg)
	if !ok || next.change.OnDisk == nil || string(next.change.OnDisk) != "newer\n" {
		t.Fatalf("re-armed watch cmd = %#v, want the next queued change", next)
	}
}

func TestExternalChange_MonitorUpdateErrorDisablesFeature(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte("x\n"), nil })
	mon := newFakeMonitor()
	mon.updateErr = errors.New("watch denied")
	m := newLoadedModel(t, fakeLoader{state: state}, mon)
	if m.monitor != nil {
		t.Fatal("monitor must be disabled after an Update error")
	}
	if !strings.Contains(m.statusMessage, "watching unavailable") {
		t.Errorf("statusMessage = %q, want an unavailable notification", m.statusMessage)
	}
}

func TestExternalChange_MonitorErrorShowsStatus(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte("x\n"), nil })
	mon := newFakeMonitor()
	m := newLoadedModel(t, fakeLoader{state: state}, mon)

	m, cmd := press(m, externalChangeMsg{err: errors.New("watcher exploded")})
	if m.monitor != nil {
		t.Fatal("monitor must be detached after a watcher error")
	}
	if cmd != nil {
		t.Fatal("no watch may re-arm after a watcher error")
	}
	if !strings.Contains(m.statusMessage, "watching failed") {
		t.Errorf("statusMessage = %q, want a failure notification", m.statusMessage)
	}
}

func TestExternalChange_ClosedStopsWatching(t *testing.T) {
	state := stateFor(t, "Caddyfile", func(string) ([]byte, error) { return []byte("x\n"), nil })
	mon := newFakeMonitor()
	m := newLoadedModel(t, fakeLoader{state: state}, mon)

	m, cmd := press(m, externalChangeMsg{err: app.ErrChangeClosed})
	if cmd != nil {
		t.Fatal("no watch may re-arm after the monitor closed")
	}
	if m.monitor != nil {
		t.Fatal("closed monitor must be detached")
	}
	if m.showChangeConflict {
		t.Fatal("a closed monitor must not open the conflict modal")
	}
}

func TestExternalChange_ViewFits(t *testing.T) {
	fs := map[string]string{"Caddyfile": "old {\n}\n"}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, mon)

	m.workingBytes = []byte("edited {\n}\n") // unsaved edits: full options row
	m, _ = press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte("old {\n}\n# extra\n")}})
	if !m.showChangeConflict {
		t.Fatal("conflict modal did not open")
	}
	assertFits(t, m, 80, 24)
}
