package ui

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/backup"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
)

// fakeRollbacker is a programmable app.Rollbacker for tests. It serves a
// fixed entry list and per-path bytes and records the Rollback call, so
// the flow tests can assert the exact target, baseline and backup path.
type fakeRollbacker struct {
	entries          []backup.Entry
	listErr          error
	readErr          error
	contents         map[string][]byte // backup path -> bytes
	current          map[string][]byte // document path -> on-disk bytes
	rollbackResult   app.RollbackResult
	rollbackErr      error
	rollbackCalls    int
	rollbackPath     string
	rollbackBaseline []byte
	rollbackBackup   string
	rollbackDocs     []*caddyfile.Document
}

func (f *fakeRollbacker) ListBackups(srcPath string, docs []*caddyfile.Document) ([]backup.Entry, error) {
	return f.entries, f.listErr
}

func (f *fakeRollbacker) ReadBackup(entry backup.Entry) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.contents[entry.Path], nil
}

func (f *fakeRollbacker) ReadCurrent(path string) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.current[path], nil
}

func (f *fakeRollbacker) Rollback(ctx context.Context, path string, original []byte, backupPath string, docs []*caddyfile.Document) (app.RollbackResult, error) {
	f.rollbackCalls++
	f.rollbackPath = path
	f.rollbackBaseline = append([]byte(nil), original...)
	f.rollbackBackup = backupPath
	f.rollbackDocs = append([]*caddyfile.Document(nil), docs...)
	return f.rollbackResult, f.rollbackErr
}

// backupEntry builds a backup.Entry with the given time, sequence and
// source identity for tests.
func backupEntry(t *testing.T, dir, name string, seq int, source string) backup.Entry {
	t.Helper()
	return backup.Entry{
		Path:        dir + "/" + name,
		Time:        time.Date(2026, 8, 1, 20, 10, 0, 0, time.UTC),
		Sequence:    seq,
		Base:        "Caddyfile",
		Source:      source,
		SourceKnown: true,
	}
}

// pressB presses the B keybinding and runs any command it returns, then
// returns the model. The listing is asynchronous, so the command must be
// executed for the modal to open.
func pressB(t *testing.T, m *Model) *Model {
	t.Helper()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("B")})
	if cmd == nil {
		t.Fatal("B returned no command")
	}
	updated, _ := m.Update(cmd())
	return updated.(*Model)
}

// pressEnter presses Enter and executes any asynchronous command it
// returns (the backup compare reads bytes through the rollbacker before
// the diff can open; the rollback runs asynchronously after the
// confirmation), so the flow proceeds exactly as in the real TUI.
func pressEnter(t *testing.T, m *Model) *Model {
	t.Helper()
	updated, cmd := press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		next, _ := updated.Update(cmd())
		return next.(*Model)
	}
	return updated
}

func TestBackups_BOpensList(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	rb := &fakeRollbacker{
		entries: []backup.Entry{
			backupEntry(t, "/backups", "2026-08-01T20-10-00-002-Caddyfile", 2, "config/Caddyfile"),
			backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "config/Caddyfile"),
		},
	}
	m := newLoadedModel(t, fakeLoader{state: state}, rb)
	m = resize(m, 80, 24)
	m = pressB(t, m)

	if !m.showBackups {
		t.Fatal("showBackups = false after B")
	}
	if m.backupDocPath != "config/Caddyfile" {
		t.Errorf("backupDocPath = %q, want the selected document", m.backupDocPath)
	}
	if len(m.backups) != 2 {
		t.Fatalf("backups = %d, want 2", len(m.backups))
	}
	view := m.View()
	if !strings.Contains(view, "Backups") || !strings.Contains(view, "config/Caddyfile") {
		t.Errorf("backup modal view missing title or source path:\n%s", view)
	}
	// Both exact backup paths are shown.
	if !strings.Contains(view, "2026-08-01T20-10-00-002-Caddyfile") || !strings.Contains(view, "2026-08-01T20-10-00-001-Caddyfile") {
		t.Errorf("backup modal view missing backup paths:\n%s", view)
	}
}

func TestBackups_UnavailableWithoutRollbacker(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 80, 24)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("B")})
	if cmd != nil {
		t.Fatalf("B returned a command without a rollbacker, want a status hint only")
	}
	if !strings.Contains(m.statusMessage, "backups unavailable") {
		t.Errorf("statusMessage = %q, want an unavailable hint", m.statusMessage)
	}
}

func TestBackups_EmptyListShowsHint(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	rb := &fakeRollbacker{}
	m := newLoadedModel(t, fakeLoader{state: state}, rb)
	m = resize(m, 80, 24)
	m = pressB(t, m)

	if !m.showBackups {
		t.Fatal("showBackups = false for an empty list")
	}
	if !strings.Contains(m.View(), "no backups for this document yet") {
		t.Errorf("empty list hint missing:\n%s", m.View())
	}
}

func TestBackups_ListErrorStatus(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	rb := &fakeRollbacker{listErr: errors.New("boom")}
	m := newLoadedModel(t, fakeLoader{state: state}, rb)
	m = resize(m, 80, 24)
	m = pressB(t, m)

	if m.showBackups {
		t.Fatal("showBackups = true after a listing error")
	}
	if !strings.Contains(m.statusMessage, "boom") {
		t.Errorf("statusMessage = %q, want the listing error", m.statusMessage)
	}
}

func TestBackups_CompareDiffOpens(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "config/Caddyfile")
	rb := &fakeRollbacker{
		entries:  []backup.Entry{entry},
		contents: map[string][]byte{entry.Path: []byte("example.test {\n\trespond v1\n}\n")},
		current:  map[string][]byte{"config/Caddyfile": []byte("example.test {\n\trespond ok\n}\n")},
	}
	m := newLoadedModel(t, fakeLoader{state: state}, rb)
	m = resize(m, 80, 24)
	m = pressB(t, m)

	// Enter on the backup opens the compare diff.
	m = pressEnter(t, m)
	if !m.showDiff {
		t.Fatal("showDiff = false after Enter on a backup")
	}
	if !strings.Contains(m.diffTitle, "Compare backup") {
		t.Errorf("diffTitle = %q, want a backup comparison", m.diffTitle)
	}
	if !strings.Contains(m.diffTitle, "config/Caddyfile") {
		t.Errorf("diffTitle = %q, want the document path", m.diffTitle)
	}
	// Read-only mode offers no rollback: the diff is informational.
	if m.pendingRollback != nil {
		t.Fatal("pendingRollback set in read-only mode, want a read-only comparison")
	}
}

func TestBackups_CompareReadFailureStatus(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "config/Caddyfile")
	rb := &fakeRollbacker{
		entries: []backup.Entry{entry},
		readErr: errors.New("unreadable backup"),
	}
	m := newLoadedModel(t, fakeLoader{state: state}, rb)
	m = resize(m, 80, 24)
	m = pressB(t, m)
	m = pressEnter(t, m)

	if m.showDiff {
		t.Fatal("showDiff = true despite the read failure")
	}
	if !strings.Contains(m.statusMessage, "unreadable backup") {
		t.Errorf("statusMessage = %q, want the read error", m.statusMessage)
	}
}

func TestBackups_EscCloses(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	rb := &fakeRollbacker{entries: []backup.Entry{backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "config/Caddyfile")}}
	m := newLoadedModel(t, fakeLoader{state: state}, rb)
	m = resize(m, 80, 24)
	m = pressB(t, m)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showBackups {
		t.Fatal("showBackups = true after Esc")
	}
	if len(m.backups) != 0 {
		t.Errorf("backups retained after close: %v", m.backups)
	}
}

func TestBackups_NavigationRevealsCursor(t *testing.T) {
	m := &Model{
		backups: []backup.Entry{{}, {}, {}},
	}
	m.backupViewport.Height = 1
	m.backupViewport.SetContent("a\nb\nc")

	updated, _ := m.updateBackupsKey(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(*Model)
	if m.backupCursor != 1 {
		t.Fatalf("cursor after down = %d, want 1", m.backupCursor)
	}
	if m.backupViewport.YOffset != 1 {
		t.Errorf("viewport after down = %d, want 1", m.backupViewport.YOffset)
	}

	updated, _ = m.updateBackupsKey(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(*Model)
	if m.backupCursor != 0 {
		t.Fatalf("cursor after up = %d, want 0", m.backupCursor)
	}
	if m.backupViewport.YOffset != 0 {
		t.Errorf("viewport after up = %d, want 0", m.backupViewport.YOffset)
	}

	// The cursor stops at both ends of the list.
	m.updateBackupsKey(tea.KeyMsg{Type: tea.KeyUp})
	if m.backupCursor != 0 {
		t.Errorf("cursor moved above first entry: %d", m.backupCursor)
	}
	m.backupCursor = len(m.backups) - 1
	m.updateBackupsKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.backupCursor != len(m.backups)-1 {
		t.Errorf("cursor moved past last entry: %d", m.backupCursor)
	}
}

func TestBackups_FooterUsesCommandPalette(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	rb := &fakeRollbacker{}
	m := newLoadedModel(t, fakeLoader{state: state}, rb)
	m = resize(m, 80, 24)
	view := m.View()
	if !strings.Contains(view, "? commands") {
		t.Errorf("footer missing the command-palette hint:\n%s", view)
	}
	if strings.Contains(view, "B backups") {
		t.Errorf("footer should not list operational commands:\n%s", view)
	}
}

func TestBackups_RollbackFullFlow(t *testing.T) {
	fs := map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: false, BackupDir: "/backups"}, fsReader(fs))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{
		entries: []backup.Entry{entry},
		contents: map[string][]byte{
			entry.Path: []byte("example.test {\n\trespond ok\n}\n"),
		},
		current: map[string][]byte{"Caddyfile": []byte("example.test {\n\trespond ok\n}\n")},
		rollbackResult: app.RollbackResult{
			BackupPath:   "/backups/2026-08-09T12-00-00-001-Caddyfile",
			RestoredFrom: entry.Path,
		},
	}
	mon := newFakeMonitor()
	saver := &fakeSaver{}
	formatter := &fakeFormatter{}
	m := newLoadedModel(t, loader, saver, formatter, rb, mon)
	m = resize(m, 80, 24)

	// Open the backup list, review the diff, confirm, roll back.
	m = pressB(t, m)
	m = pressEnter(t, m) // compare
	if m.pendingRollback == nil {
		t.Fatal("pendingRollback not set in writable mode")
	}
	m = pressEnter(t, m) // confirmation
	if !m.showRollbackConfirm {
		t.Fatal("rollback confirmation modal did not open")
	}
	confirmView := m.View()
	if !strings.Contains(confirmView, "Rollback") || !strings.Contains(confirmView, "Caddyfile") {
		t.Errorf("confirmation view missing target or title:\n%s", confirmView)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("confirmation Enter returned no command")
	}
	updated, _ := m.Update(cmd())
	m = updated.(*Model)

	if rb.rollbackCalls != 1 {
		t.Fatalf("rollback calls = %d, want 1", rb.rollbackCalls)
	}
	if rb.rollbackPath != "Caddyfile" {
		t.Errorf("rollback path = %q, want the exact document", rb.rollbackPath)
	}
	if string(rb.rollbackBaseline) != "example.test {\n\trespond ok\n}\n" {
		t.Errorf("rollback baseline = %q, want the on-disk bytes captured at compare time", rb.rollbackBaseline)
	}
	if rb.rollbackBackup != entry.Path {
		t.Errorf("rollback backup = %q, want the selected backup", rb.rollbackBackup)
	}
	if !strings.Contains(m.statusMessage, "rolled back") {
		t.Errorf("statusMessage = %q, want a rolled-back confirmation", m.statusMessage)
	}
	if m.showBackups || m.showDiff || m.showRollbackConfirm {
		t.Error("backup modals stayed open after a successful rollback")
	}
	if m.loaded != loadedUnknown {
		t.Errorf("loaded = %v, want loadedUnknown after a rollback", m.loaded)
	}
}

func TestBackups_RollbackCancellationLeavesNothingChanged(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "/backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{
		entries:  []backup.Entry{entry},
		contents: map[string][]byte{entry.Path: []byte("example.test {\n\trespond v1\n}\n")},
		current:  map[string][]byte{"Caddyfile": []byte("example.test {\n\trespond ok\n}\n")},
	}
	saver := &fakeSaver{}
	formatter := &fakeFormatter{}
	m := newLoadedModel(t, fakeLoader{state: state}, saver, formatter, rb)
	m = resize(m, 80, 24)

	m = pressB(t, m)
	m = pressEnter(t, m) // compare
	m = pressEnter(t, m) // confirmation
	if !m.showRollbackConfirm {
		t.Fatal("rollback confirmation modal did not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc}) // cancel

	if rb.rollbackCalls != 0 {
		t.Fatalf("rollback called %d times despite cancellation", rb.rollbackCalls)
	}
	if m.rollingBack {
		t.Fatal("rollingBack = true after cancellation")
	}
	if !strings.Contains(m.statusMessage, "rollback cancelled") {
		t.Errorf("statusMessage = %q, want a cancellation notice", m.statusMessage)
	}
	// The backup list is still open so the operator can retry.
	if !m.showBackups {
		t.Error("backup list closed after cancellation, want it to stay open")
	}
}

func TestBackups_RollbackReadOnlyNotOffered(t *testing.T) {
	// Read-only mode: the confirmation step is never reachable because
	// no pendingRollback is created, so Enter in the diff is a no-op.
	state := stateFor(t, "Caddyfile", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{
		entries:  []backup.Entry{entry},
		contents: map[string][]byte{entry.Path: []byte("v1")},
		current:  map[string][]byte{"Caddyfile": []byte("v2")},
	}
	m := newLoadedModel(t, fakeLoader{state: state}, rb)
	m = resize(m, 80, 24)

	m = pressB(t, m)
	if !strings.Contains(m.View(), "rollback unavailable") {
		t.Errorf("read-only rollback hint missing:\n%s", m.View())
	}
	m = pressEnter(t, m) // compare only
	if m.pendingRollback != nil {
		t.Fatal("pendingRollback set in read-only mode")
	}
	m = pressEnter(t, m) // must be a no-op
	if m.showRollbackConfirm {
		t.Fatal("rollback confirmation opened in read-only mode")
	}
	if rb.rollbackCalls != 0 {
		t.Fatal("rollback called in read-only mode")
	}
}

func TestRollback_ConflictStatus(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "/backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n}\n",
	}))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{
		entries:     []backup.Entry{entry},
		contents:    map[string][]byte{entry.Path: []byte("v1")},
		current:     map[string][]byte{"Caddyfile": []byte("v2")},
		rollbackErr: app.ErrRollbackConflict,
	}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, &fakeFormatter{}, rb)
	m = resize(m, 80, 24)
	m = pressB(t, m)
	m = pressEnter(t, m)
	m = pressEnter(t, m)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ := m.Update(cmd())
	m = updated.(*Model)

	if !strings.Contains(m.statusMessage, "changed on disk") {
		t.Errorf("statusMessage = %q, want a conflict notice", m.statusMessage)
	}
	if m.loaded != loadedUnknown && m.loaded != loadedStale {
		t.Errorf("loaded = %v, want unknown/stale after a failed rollback", m.loaded)
	}
	// The backup list stays open after the failure so the operator can
	// retry.
	if !m.showBackups {
		t.Error("backup list closed after a failed rollback")
	}
}

func TestRollback_ValidationFailureStatus(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "/backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n}\n",
	}))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{
		entries:     []backup.Entry{entry},
		contents:    map[string][]byte{entry.Path: []byte("garbage")},
		current:     map[string][]byte{"Caddyfile": []byte("v2")},
		rollbackErr: app.ErrRollbackInvalid,
	}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, &fakeFormatter{}, rb)
	m = resize(m, 80, 24)
	m = pressB(t, m)
	m = pressEnter(t, m)
	m = pressEnter(t, m)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ := m.Update(cmd())
	m = updated.(*Model)

	if !strings.Contains(m.statusMessage, "does not validate") {
		t.Errorf("statusMessage = %q, want an invalid-backup notice", m.statusMessage)
	}
}

func TestRollback_RestoreFailureStatus(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "/backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n}\n",
	}))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{
		entries:  []backup.Entry{entry},
		contents: map[string][]byte{entry.Path: []byte("v1")},
		current:  map[string][]byte{"Caddyfile": []byte("v2")},
		rollbackErr: &app.RollbackError{
			BackupPath: "/backups/pre-backup",
			Err:        errors.New("write exploded"),
		},
	}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, &fakeFormatter{}, rb)
	m = resize(m, 80, 24)
	m = pressB(t, m)
	m = pressEnter(t, m)
	m = pressEnter(t, m)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ := m.Update(cmd())
	m = updated.(*Model)

	if !strings.Contains(m.statusMessage, "pre-backup") {
		t.Errorf("statusMessage = %q, want the recovery backup path", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "write exploded") {
		t.Errorf("statusMessage = %q, want the underlying error", m.statusMessage)
	}
	// The recovery backup path is actionable: the status line points the
	// operator at B to inspect the pre-rollback backup.
	if !strings.Contains(m.statusMessage, "press B") {
		t.Errorf("statusMessage = %q, want the press-B recovery hint", m.statusMessage)
	}
	// The failure is recorded with a safe next action.
	if len(m.errorHistory) == 0 || m.errorHistory[len(m.errorHistory)-1].Op != "rollback" {
		t.Errorf("error history missing the rollback failure: %+v", m.errorHistory)
	}
}

func TestRollback_StateRefreshAndWatcherRearm(t *testing.T) {
	fs := map[string]string{
		"Caddyfile":   "example.test {\n\timport common.conf\n}\n",
		"common.conf": "# v2\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: false, BackupDir: "/backups"}, fsReader(fs))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{
		entries:  []backup.Entry{entry},
		contents: map[string][]byte{entry.Path: []byte("example.test {\n\timport common.conf\n}\n")},
		current:  map[string][]byte{"Caddyfile": []byte("example.test {\n\timport common.conf\n}\n")},
		rollbackResult: app.RollbackResult{
			BackupPath:   "/backups/pre",
			RestoredFrom: entry.Path,
		},
	}
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, &fakeSaver{}, &fakeFormatter{}, rb, mon)
	m = resize(m, 80, 24)
	before := mon.updateCalls

	m = pressB(t, m)
	m = pressEnter(t, m)
	m = pressEnter(t, m)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ := m.Update(cmd())
	m = updated.(*Model)

	if m.loaded != loadedUnknown {
		t.Errorf("loaded = %v, want loadedUnknown (no implicit reload)", m.loaded)
	}
	if mon.updateCalls <= before {
		t.Errorf("monitor update calls = %d, want a re-target after rollback (> %d)", mon.updateCalls, before)
	}
	// The in-flight guard is released once the rollback and the monitor
	// re-arm have fully settled.
	if m.rollingBack {
		t.Error("rollingBack = true after the rollback and re-arm completed")
	}
	// The graph was reloaded from the loader, so the tree and monitor
	// snapshots reflect the restored file.
	if m.state == nil || m.state.Graph == nil {
		t.Fatal("graph is nil after the rollback refresh")
	}
	if m.workingValidated {
		t.Error("workingValidated survived the rollback refresh")
	}
}

func TestBackups_MonitorIgnoresChangeDuringRollback(t *testing.T) {
	fs := map[string]string{
		"Caddyfile": "example.test {\n}\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: false, BackupDir: "/backups"}, fsReader(fs))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{
		entries:  []backup.Entry{entry},
		contents: map[string][]byte{entry.Path: []byte("v1")},
		current:  map[string][]byte{"Caddyfile": []byte("v2")},
	}
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, &fakeSaver{}, &fakeFormatter{}, rb, mon)
	m = resize(m, 80, 24)
	m = pressB(t, m)
	m = pressEnter(t, m)
	m = pressEnter(t, m)
	if !m.showRollbackConfirm {
		t.Fatal("rollback confirmation modal did not open")
	}
	// An external change delivered while the rollback is being confirmed
	// must not interrupt the flow (the rollback has its own conflict
	// guard).
	m, cmd := press(m, externalChangeMsg{change: app.ExternalChange{Path: "Caddyfile", OnDisk: []byte("changed")}})
	if cmd == nil {
		t.Fatal("change during rollback flow did not re-arm the watch")
	}
	if m.showChangeConflict {
		t.Fatal("conflict modal opened during the rollback flow")
	}
	if !m.showRollbackConfirm {
		t.Fatal("rollback confirmation was closed by the change")
	}
}

// failingAfterFirstLoader is a loader that succeeds once (the initial
// load) and then fails every subsequent LoadState, simulating a graph
// reload failure after a rollback.
type failingAfterFirstLoader struct {
	loader app.Loader
	calls  int
}

func (f *failingAfterFirstLoader) LoadState() (*app.State, error) {
	f.calls++
	if f.calls > 1 {
		return nil, errors.New("graph reload failed")
	}
	return f.loader.LoadState()
}

// TestRollback_GraphReloadFailureSeedsMonitorWithRestoredBytes covers the
// false-conflict hazard: after a rollback, when the graph reload fails,
// the change monitor must be re-seeded with the true on-disk bytes of the
// restored file (read through the rollbacker), so it never fires a
// spurious "file changed on disk" conflict for the file that was just
// restored.
func TestRollback_GraphReloadFailureSeedsMonitorWithRestoredBytes(t *testing.T) {
	fs := map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}
	loader := &failingAfterFirstLoader{
		loader: app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: false, BackupDir: "/backups"}, fsReader(fs)),
	}
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	restored := []byte("example.test {\n\trespond restored\n}\n")
	rb := &fakeRollbacker{
		entries:  []backup.Entry{entry},
		contents: map[string][]byte{entry.Path: restored},
		current:  map[string][]byte{"Caddyfile": restored},
		rollbackResult: app.RollbackResult{
			BackupPath:   "/backups/pre",
			RestoredFrom: entry.Path,
		},
	}
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, &fakeSaver{}, &fakeFormatter{}, rb, mon)
	m = resize(m, 80, 24)
	before := mon.updateCalls

	m = pressB(t, m)
	m = pressEnter(t, m)
	m = pressEnter(t, m)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ := m.Update(cmd())
	m = updated.(*Model)

	if !strings.Contains(m.statusMessage, "tree refresh failed") {
		t.Errorf("statusMessage = %q, want a tree refresh failure notice", m.statusMessage)
	}
	if mon.updateCalls <= before {
		t.Fatalf("monitor update calls = %d, want a re-seed (> %d)", mon.updateCalls, before)
	}
	var found bool
	for _, tg := range mon.targets {
		if filepath.Clean(tg.Path) == filepath.Clean("Caddyfile") {
			found = true
			if !bytes.Equal(tg.Source, restored) {
				t.Errorf("monitor reference = %q, want the restored bytes %q", tg.Source, restored)
			}
		}
	}
	if !found {
		t.Fatalf("monitor targets missing the restored document: %+v", mon.targets)
	}
	// The in-flight guard is released only after the re-seed completes.
	if m.rollingBack {
		t.Error("rollingBack = true after the rollback and re-seed completed")
	}
}

// TestRollback_GraphReloadAndReadCurrentFailKeepMonitorReference covers
// the second half of the guard: when the restored on-disk bytes cannot be
// read either, the monitor must NOT be re-seeded from the stale graph —
// its existing reference then surfaces a genuine reload prompt.
func TestRollback_GraphReloadAndReadCurrentFailKeepMonitorReference(t *testing.T) {
	fs := map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}
	loader := &failingAfterFirstLoader{
		loader: app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: false, BackupDir: "/backups"}, fsReader(fs)),
	}
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	restored := []byte("example.test {\n\trespond restored\n}\n")
	rb := &fakeRollbacker{
		entries:  []backup.Entry{entry},
		contents: map[string][]byte{entry.Path: restored},
		current:  map[string][]byte{"Caddyfile": restored},
		rollbackResult: app.RollbackResult{
			BackupPath:   "/backups/pre",
			RestoredFrom: entry.Path,
		},
	}
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, &fakeSaver{}, &fakeFormatter{}, rb, mon)
	m = resize(m, 80, 24)
	before := mon.updateCalls

	m = pressB(t, m)
	m = pressEnter(t, m) // compare: ReadCurrent still succeeds
	m = pressEnter(t, m) // confirmation
	// The restored file becomes unreadable before the rollback re-seed.
	rb.readErr = errors.New("restored file unreadable")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ := m.Update(cmd())
	m = updated.(*Model)

	if mon.updateCalls != before {
		t.Errorf("monitor update calls = %d, want %d (no re-seed when the read fails)", mon.updateCalls, before)
	}
	if !strings.Contains(m.statusMessage, "tree refresh failed") {
		t.Errorf("statusMessage = %q, want a tree refresh failure notice", m.statusMessage)
	}
}

func TestBackups_StaleCompareMessageDiscarded(t *testing.T) {
	fs := map[string]string{
		"Caddyfile":   "example.test {\n\timport common.conf\n}\n",
		"common.conf": "# v1\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: true}, fsReader(fs))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{entries: []backup.Entry{entry}}
	m := newLoadedModel(t, loader, rb)
	m = resize(m, 80, 24)

	// Open the backup list for the root document.
	m = pressB(t, m)
	if m.backupDocPath != "Caddyfile" {
		t.Fatalf("backupDocPath = %q, want the root document", m.backupDocPath)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	// Move the selection to the imported document and reopen for it.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // site node
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown}) // common.conf document row
	m = pressB(t, m)
	if m.backupDocPath != "common.conf" {
		t.Fatalf("backupDocPath = %q, want the imported document", m.backupDocPath)
	}

	// A stale compare message for the previous document arrives.
	m, _ = press(m, backupCompareMsg{
		Path:       "Caddyfile",
		BackupPath: "/backups/stale",
		Current:    []byte("a"),
		Backup:     []byte("b"),
	})
	if m.showDiff {
		t.Fatal("stale compare message opened a diff for the wrong document")
	}
}

func TestBackups_RollbackPassesSnapshotDocumentSet(t *testing.T) {
	fs := map[string]string{
		"Caddyfile":   "example.test {\n\timport common.conf\n}\n",
		"common.conf": "# v1\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: false, BackupDir: "/backups"}, fsReader(fs))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{
		entries:  []backup.Entry{entry},
		contents: map[string][]byte{entry.Path: []byte("restored")},
		current:  map[string][]byte{"Caddyfile": []byte("current")},
		rollbackResult: app.RollbackResult{
			BackupPath:   "/backups/pre",
			RestoredFrom: entry.Path,
		},
	}
	m := newLoadedModel(t, loader, &fakeSaver{}, &fakeFormatter{}, rb)
	m = resize(m, 80, 24)

	m = pressB(t, m)
	m = pressEnter(t, m)
	m = pressEnter(t, m)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ := m.Update(cmd())
	m = updated.(*Model)

	if len(rb.rollbackDocs) != 2 {
		t.Fatalf("rollback docs = %d, want the full graph snapshot (root + import)", len(rb.rollbackDocs))
	}
	if rb.rollbackDocs[0] == nil || rb.rollbackDocs[0].Path != "Caddyfile" {
		t.Errorf("rollback docs[0] = %+v, want the root document", rb.rollbackDocs[0])
	}
	if rb.rollbackDocs[1] == nil || rb.rollbackDocs[1].Path != "common.conf" {
		t.Errorf("rollback docs[1] = %+v, want the imported document", rb.rollbackDocs[1])
	}
}

// backupReadErrorRollbacker serves the current bytes but fails to read the
// selected backup, exercising the second error branch of the comparison.
type backupReadErrorRollbacker struct {
	current []byte
}

func (r *backupReadErrorRollbacker) ListBackups(string, []*caddyfile.Document) ([]backup.Entry, error) {
	return nil, nil
}

func (r *backupReadErrorRollbacker) ReadCurrent(string) ([]byte, error) {
	return r.current, nil
}

func (r *backupReadErrorRollbacker) ReadBackup(backup.Entry) ([]byte, error) {
	return nil, errors.New("backup unreadable")
}

func (r *backupReadErrorRollbacker) Rollback(context.Context, string, []byte, string, []*caddyfile.Document) (app.RollbackResult, error) {
	return app.RollbackResult{}, nil
}

func TestBackups_NoGraphIsNoOp(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeRollbacker{})
	m.state = nil
	updated, cmd := m.startBackups()
	if updated != m || cmd != nil {
		t.Fatalf("startBackups without a graph returned (%v, %v), want no-op", updated != m, cmd != nil)
	}
}

func TestBackups_BusyGuardIsNoOp(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeRollbacker{})
	m.rollingBack = true
	m.startBackups()
	if m.backupsLoading {
		t.Error("startBackups while rolling back started a listing")
	}
	if m.statusMessage != "" {
		t.Errorf("startBackups while rolling back set a status: %q", m.statusMessage)
	}
}

func TestBackups_NoDocumentShowsHint(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeRollbacker{})
	m.items = nil
	m.startBackups()
	if !strings.Contains(m.statusMessage, "no document selected") {
		t.Errorf("statusMessage = %q, want the no-document hint", m.statusMessage)
	}
}

func TestBackups_QuitKey(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeRollbacker{})
	m = resize(m, 80, 24)
	m = pressB(t, m)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("ctrl+c in the backup modal did not request quit")
	}
}

func TestBackups_PageKeys(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeRollbacker{entries: []backup.Entry{entry}})
	m = resize(m, 80, 24)
	m = pressB(t, m)
	m.View() // sizes the backup viewport
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if !m.showBackups {
		t.Fatal("pager keys closed the backup modal")
	}
}

func TestBackups_CompareBackupReadError(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &backupReadErrorRollbacker{current: []byte("example.test {\n\trespond ok\n}\n")}
	m := newLoadedModel(t, fakeLoader{state: state}, rb)
	m = resize(m, 80, 24)
	m = pressB(t, m)
	m.backups = []backup.Entry{entry}
	m.backupCursor = 0
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter in the backup modal returned no compare command")
	}
	msg := cmd().(backupCompareMsg)
	if msg.Err == nil {
		t.Fatal("backup compare command did not report the read failure")
	}
	updated, _ := m.Update(msg)
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "✗ backup comparison failed") {
		t.Errorf("statusMessage = %q, want the comparison failure", m.statusMessage)
	}
}

func TestRollbackConfirm_QuitAndNoPending(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeRollbacker{})
	m.showRollbackConfirm = true

	// ctrl+c quits from the rollback confirmation.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c in the rollback confirmation did not request quit")
	}

	// Enter without a pending rollback is a no-op.
	m = updated.(*Model)
	m.pendingRollback = nil
	m.showRollbackConfirm = true
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd != nil || m.rollingBack {
		t.Errorf("Enter without a pending rollback returned (%v, %v)", cmd != nil, m.rollingBack)
	}
}

func TestRollback_RetentionError(t *testing.T) {
	fs := map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: false, BackupDir: "/backups"}, fsReader(fs))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{
		entries:  []backup.Entry{entry},
		contents: map[string][]byte{entry.Path: []byte("example.test {\n\trespond ok\n}\n")},
		current:  map[string][]byte{"Caddyfile": []byte("example.test {\n\trespond ok\n}\n")},
		rollbackResult: app.RollbackResult{
			BackupPath:   "/backups/pre",
			RestoredFrom: entry.Path,
			RetentionErr: errors.New("old backups could not be removed"),
		},
	}
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, &fakeSaver{}, &fakeFormatter{}, rb, mon)
	m = resize(m, 80, 24)
	m = pressB(t, m)
	m = pressEnter(t, m) // compare
	m = pressEnter(t, m) // confirmation
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ := m.Update(cmd())
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "retention cleanup failed") {
		t.Errorf("statusMessage = %q, want the retention failure", m.statusMessage)
	}
	if len(m.errorHistory) == 0 {
		t.Error("retention failure did not record an error")
	}
}

func TestRollback_RefreshWithoutLoader(t *testing.T) {
	fs := map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: false, BackupDir: "/backups"}, fsReader(fs))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{
		entries:  []backup.Entry{entry},
		contents: map[string][]byte{entry.Path: []byte("example.test {\n\trespond restored\n}\n")},
		current:  map[string][]byte{"Caddyfile": []byte("example.test {\n\trespond restored\n}\n")},
		rollbackResult: app.RollbackResult{
			BackupPath:   "/backups/pre",
			RestoredFrom: entry.Path,
		},
	}
	mon := newFakeMonitor()
	m := newLoadedModel(t, loader, &fakeSaver{}, &fakeFormatter{}, rb, mon)
	m.loader = nil // the refresh after the restore has no loader
	m = resize(m, 80, 24)
	before := mon.updateCalls

	m = pressB(t, m)
	m = pressEnter(t, m)
	m = pressEnter(t, m)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ := m.Update(cmd())
	m = updated.(*Model)

	if !strings.Contains(m.statusMessage, "tree refresh failed") {
		t.Errorf("statusMessage = %q, want the tree refresh failure", m.statusMessage)
	}
	if mon.updateCalls == before {
		t.Error("change monitor was not re-seeded with the restored bytes")
	}
}

func TestRollback_SeedWithoutMonitor(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeRollbacker{})
	m.monitor = nil
	m.seedMonitorWithRestoredBytes("Caddyfile") // no-op, no panic
}

func TestRollback_GenericError(t *testing.T) {
	fs := map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: false, BackupDir: "/backups"}, fsReader(fs))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{
		entries:     []backup.Entry{entry},
		contents:    map[string][]byte{entry.Path: []byte("example.test {\n\trespond ok\n}\n")},
		current:     map[string][]byte{"Caddyfile": []byte("example.test {\n\trespond ok\n}\n")},
		rollbackErr: errors.New("permission denied"),
	}
	m := newLoadedModel(t, loader, &fakeSaver{}, &fakeFormatter{}, rb)
	m = resize(m, 80, 24)
	m = pressB(t, m)
	m = pressEnter(t, m)
	m = pressEnter(t, m)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ := m.Update(cmd())
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "✗ rollback failed: permission denied") {
		t.Errorf("statusMessage = %q, want the generic rollback failure", m.statusMessage)
	}
}

func TestBackups_TinySizes(t *testing.T) {
	state := writableStateFor(t, "Caddyfile", "backups", fsReader(map[string]string{
		"Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeRollbacker{entries: []backup.Entry{entry}})
	m.backups = []backup.Entry{entry}
	m.pendingRollback = &pendingRollback{path: "Caddyfile", backupPath: entry.Path}
	for _, size := range []struct{ w, h int }{{1, 2}, {40, 2}, {0, 0}} {
		if got := m.backupView(size.w, size.h); got == "" {
			t.Errorf("backupView(%d, %d) rendered empty", size.w, size.h)
		}
		if got := m.rollbackConfirmView(size.w, size.h); got == "" {
			t.Errorf("rollbackConfirmView(%d, %d) rendered empty", size.w, size.h)
		}
	}
	m.syncBackupViewport(1, 1)
	m.syncBackupViewport(5, 0)
}

func TestBackups_FooterRollbackHint(t *testing.T) {
	// With writable settings plus a saver, formatter and rollbacker, the
	// backup-list footer advertises the rollback path (Enter/→ compare &
	// rollback).
	loader := app.NewLoader(config.Settings{ConfigPath: "Caddyfile", ReadOnly: false, BackupDir: "/backups"}, fsReader(map[string]string{
		"Caddyfile": "example.test {\n}\n",
	}))
	entry := backupEntry(t, "/backups", "2026-08-01T20-10-00-001-Caddyfile", 1, "Caddyfile")
	rb := &fakeRollbacker{entries: []backup.Entry{entry}}
	m := newLoadedModel(t, loader, rb, &fakeSaver{}, &fakeFormatter{})
	m = resize(m, 80, 24)
	m = pressB(t, m)
	if !m.showBackups {
		t.Fatal("showBackups = false after B")
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "Enter/→ compare & rollback") {
		t.Errorf("rollback-capable backups footer missing the hint, got:\n%s", view)
	}
}
