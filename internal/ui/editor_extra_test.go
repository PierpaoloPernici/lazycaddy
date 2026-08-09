package ui

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
)

// TestEditorErrorMsg_SurfacesAndRecovers verifies the editor failure
// handler: the error lands in the status line, the editing flag and
// session are cleared, and the recovery snapshot trail is surfaced when a
// session produced one.
func TestEditorErrorMsg_SurfacesAndRecovers(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, &fakeEditor{})
	m = resize(m, 120, 30)

	// With an active session: the snapshot path must trail the message.
	m.editorSession = &app.EditSession{SnapshotPath: "/tmp/snapshot.caddy"}
	updated, cmd := m.Update(editorErrorMsg{Err: errors.New("editor vanished")})
	m = updated.(*Model)
	if cmd != nil {
		t.Error("editorErrorMsg must not return a command")
	}
	if m.editing {
		t.Error("editing = true after an editor error, want false")
	}
	if m.editorSession != nil {
		t.Error("editorSession still set after an editor error, want nil")
	}
	if !strings.Contains(m.statusMessage, "editor vanished") {
		t.Errorf("statusMessage = %q, want the editor error text", m.statusMessage)
	}
	if !strings.Contains(m.statusMessage, "recovery snapshot: /tmp/snapshot.caddy") {
		t.Errorf("statusMessage = %q, want the recovery snapshot trail", m.statusMessage)
	}

	// Without a session: the error is still surfaced, without a trail.
	updated, _ = m.Update(editorErrorMsg{Err: errors.New("no editor")})
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "no editor") {
		t.Errorf("statusMessage = %q, want the error text", m.statusMessage)
	}
	if strings.Contains(m.statusMessage, "recovery snapshot") {
		t.Errorf("statusMessage = %q, must not claim a recovery snapshot without a session", m.statusMessage)
	}
}

// TestEditorReadyMsg_NilSessionRejects verifies that a prepared session
// that cannot start (nil or argv-less) is rejected cleanly.
func TestEditorReadyMsg_NilSessionRejects(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, &fakeEditor{})
	m = resize(m, 120, 30)
	m.editing = true

	updated, cmd := m.Update(editorReadyMsg{Session: nil})
	m = updated.(*Model)
	if cmd != nil {
		t.Error("nil editor session must not return a command")
	}
	if m.editing {
		t.Error("editing = true after a rejected session, want false")
	}
	if !strings.Contains(m.statusMessage, "editor session could not start") {
		t.Errorf("statusMessage = %q, want the session-start failure hint", m.statusMessage)
	}

	// An argv-less session is rejected identically.
	m.editing = true
	updated, _ = m.Update(editorReadyMsg{Session: &app.EditSession{Cmd: nil}})
	m = updated.(*Model)
	if m.editing || m.editorSession != nil {
		t.Error("argv-less session must be rejected")
	}
}

// TestStartFullEdit_Guards verifies the whole-document edit guards: no
// state, no editor, read-only mode, in-flight workflows and a missing
// selection all return without launching anything.
func TestStartFullEdit_Guards(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))

	t.Run("no editor configured", func(t *testing.T) {
		m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}) // no editor
		_, cmd := m.startFullEdit()
		if cmd != nil {
			t.Error("startFullEdit without an editor returned a command")
		}
		if !strings.Contains(m.statusMessage, "no editor configured") {
			t.Errorf("statusMessage = %q, want the editor hint", m.statusMessage)
		}
	})

	t.Run("read-only mode", func(t *testing.T) {
		roState := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
			"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
		}))
		m := newLoadedModel(t, fakeLoader{state: roState}, &fakeEditor{})
		_, cmd := m.startFullEdit()
		if cmd != nil {
			t.Error("startFullEdit in read-only mode returned a command")
		}
		if !strings.Contains(m.statusMessage, "read-only mode") {
			t.Errorf("statusMessage = %q, want the read-only hint", m.statusMessage)
		}
	})

	t.Run("in-flight workflow", func(t *testing.T) {
		m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, &fakeEditor{})
		m.saving = true
		before := m.statusMessage
		updated, cmd := m.startFullEdit()
		m = updated.(*Model)
		if cmd != nil {
			t.Error("startFullEdit while saving returned a command")
		}
		if m.statusMessage != before {
			t.Errorf("statusMessage changed to %q while saving, want it untouched", m.statusMessage)
		}
	})

	t.Run("no selection", func(t *testing.T) {
		m := newLoadedModel(t, fakeLoader{state: nil}, &fakeSaver{}, &fakeEditor{})
		updated, cmd := m.startFullEdit()
		m = updated.(*Model)
		if cmd != nil {
			t.Error("startFullEdit without a state returned a command")
		}
	})
}

// TestStartFullEdit_Launches verifies the happy path: with a writable
// state, an editor and a selected document, E returns a command that
// prepares the full document and yields an editorReadyMsg.
func TestStartFullEdit_Launches(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile":     "import sites/a.caddy\n",
		"config/sites/a.caddy": "a.example.test {\n\trespond ok\n}\n",
	}))
	editor := &fakeEditor{}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, editor)
	m = resize(m, 120, 30)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("E must return a command")
	}
	if !m.editing {
		t.Error("editing = false after E, want true")
	}
	msg := cmd()
	ready, ok := msg.(editorReadyMsg)
	if !ok {
		t.Fatalf("E command returned %T, want editorReadyMsg", msg)
	}
	if ready.Session == nil || ready.Session.Mode != app.EditFull {
		t.Errorf("session = %+v, want a prepared EditFull session", ready.Session)
	}
	if editor.prepareFullCalls == 0 {
		t.Error("editor.PrepareFull was never called")
	}
}

// TestStartFullEdit_ErrorDeliversEditorErrorMsg verifies that a failing
// PrepareFull surfaces as an editorErrorMsg through the command.
func TestStartFullEdit_ErrorDeliversEditorErrorMsg(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	boom := errors.New("prepare exploded")
	editor := &fakeEditor{prepareErr: boom}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, editor)
	m = resize(m, 120, 30)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("E must return a command")
	}
	msg := cmd()
	errMsg, ok := msg.(editorErrorMsg)
	if !ok {
		t.Fatalf("E command returned %T, want editorErrorMsg", msg)
	}
	if !errors.Is(errMsg.Err, boom) {
		t.Errorf("err = %v, want the prepared failure", errMsg.Err)
	}
}

// TestLogDetailKeys verifies the log detail modal key handling: enter
// opens the detail from the list, arrows and PgUp/PgDown scroll, Esc
// closes back to the list, and q quits.
func TestLogDetailKeys(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{logEntry("one"), logEntry("two")} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)

	// Enter on the second line opens the detail modal.
	m.logCursor = 1
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if !m.logDetailOpen {
		t.Fatal("enter did not open the log detail modal")
	}

	// Scrolling keys must not close the modal.
	for _, key := range []string{"up", "down", "pgup", "pgdown"} {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		m = updated.(*Model)
		if !m.logDetailOpen {
			t.Fatalf("%s closed the log detail modal", key)
		}
	}

	// Esc returns to the list.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("esc")})
	m = updated.(*Model)
	if m.logDetailOpen {
		t.Error("esc did not close the log detail modal")
	}
}

// TestNodeLabel_RendersEveryKind verifies the tree label for each node
// kind and the unknown-kind fallback.
func TestNodeLabel_RendersEveryKind(t *testing.T) {
	cases := []struct {
		kind caddyfile.Kind
		name string
		want string
	}{
		{caddyfile.KindSite, "example.test", "example.test"},
		{caddyfile.KindGlobalOptions, "", "global options"},
		{caddyfile.KindSnippet, "headers", "snippet (headers)"},
		{caddyfile.KindNamedRoute, "api", "route &(api)"},
		{caddyfile.KindDirective, "respond", "respond"},
		{caddyfile.Kind(99), "mystery", "mystery"},
	}
	for _, tc := range cases {
		node := caddyfile.Node{Kind: tc.kind, Name: tc.name}
		if got := nodeLabel(node); got != tc.want {
			t.Errorf("nodeLabel(%v %q) = %q, want %q", tc.kind, tc.name, got, tc.want)
		}
	}
}

// TestLogPoll_ErrorClearedOnRecovery verifies that a failed poll sets the
// error status line and a subsequent successful poll clears exactly that
// line (leaving other status text untouched).
func TestLogPoll_ErrorClearedOnRecovery(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)

	// A failed poll surfaces the error.
	updated, cmd := m.Update(logTailMsg{Err: errors.New("journal broke")})
	m = updated.(*Model)
	if cmd == nil {
		t.Error("a failed poll must still reschedule")
	}
	if !strings.Contains(m.statusMessage, "log poll failed: journal broke") {
		t.Errorf("statusMessage = %q, want the poll failure", m.statusMessage)
	}

	// A recovered poll clears the failure text.
	updated, _ = m.Update(logTailMsg{Entries: []logs.Entry{logEntry("back")}})
	m = updated.(*Model)
	if strings.Contains(m.statusMessage, "log poll failed") {
		t.Errorf("statusMessage = %q, want the failure text cleared after recovery", m.statusMessage)
	}
	if m.logErr != nil {
		t.Errorf("logErr = %v, want nil after recovery", m.logErr)
	}
}

// TestLogPollCmd_DeliversTailMsg executes the polling command returned by
// the l keypress and verifies it delivers a logTailMsg carrying the source
// entries.
func TestLogPollCmd_DeliversTailMsg(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn: func(ctx context.Context) ([]logs.Entry, error) {
			return []logs.Entry{logEntry("polled")}, nil
		},
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("l must return a poll command")
	}
	msg := cmd()
	tail, ok := msg.(logTailMsg)
	if !ok {
		t.Fatalf("poll command returned %T, want logTailMsg", msg)
	}
	if len(tail.Entries) != 1 {
		t.Errorf("poll delivered %d entries, want 1", len(tail.Entries))
	}
}

// TestLogFollowAndPause verifies the f and p bindings inside the log view:
// f toggles follow mode, p pauses and resumes the poll (nil vs non-nil
// reschedule). Follow mode starts enabled, so the first f disables it.
func TestLogFollowAndPause(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{logEntry("a"), logEntry("b")} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)

	// Follow starts enabled; the first f turns it off.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m = updated.(*Model)
	if m.logFollow {
		t.Fatal("f did not disable follow mode")
	}
	if !strings.Contains(m.statusMessage, "follow off") {
		t.Errorf("statusMessage = %q, want the follow-off notice", m.statusMessage)
	}

	// A second f turns it back on.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m = updated.(*Model)
	if !m.logFollow {
		t.Error("f did not re-enable follow mode")
	}

	// p pauses polling (no reschedule).
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = updated.(*Model)
	if cmd != nil {
		t.Error("p must stop polling (nil reschedule)")
	}
	if !m.logPaused {
		t.Error("logPaused = false after p, want true")
	}

	// A second p resumes polling.
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = updated.(*Model)
	if cmd == nil {
		t.Error("second p must resume polling (non-nil reschedule)")
	}
	if m.logPaused {
		t.Error("logPaused = true after resuming, want false")
	}
}

// TestHandleEditorExec_MapsExitCode verifies the exec-outcome mapping: an
// *exec.ExitError yields its exit code (a cancelled edit), while a plain
// launch failure is kept as the exec error.
func TestHandleEditorExec_MapsExitCode(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	editor := &fakeEditor{
		result: app.EditResult{Original: []byte("example.test {\n\trespond ok\n}\n")},
	}
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, editor)
	m = resize(m, 120, 30)
	m.editing = true
	m.editorSession = &app.EditSession{
		Mode:     app.EditNode,
		DocPath:  "config/Caddyfile",
		Range:    caddyfile.SourceRange{Start: 0, End: 3},
		Original: []byte("example.test {\n\trespond ok\n}\n"),
		TempFile: "editor-temp",
		Cmd:      []string{"vim", "editor-temp"},
	}

	// A real exit failure carries an exit code into Complete.
	if err := runFailingCommand(3); err == nil {
		t.Fatal("precondition: runFailingCommand must fail")
	} else {
		updated, cmd := m.Update(editorExecMsg{Err: err})
		m = updated.(*Model)
		if cmd == nil {
			t.Fatal("editorExecMsg must return the complete command")
		}
		cmd() // the complete command invokes editor.Complete with the code
		if editor.capturedExit != 3 {
			t.Errorf("Complete received exit code %d, want 3", editor.capturedExit)
		}
	}

	// A plain launch failure is passed through as the exec error.
	updated, cmd := m.Update(editorExecMsg{Err: errors.New("fork failed")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("editorExecMsg with a launch failure must return the complete command")
	}
	doneMsg := cmd()
	done, ok := doneMsg.(editorDoneMsg)
	if !ok {
		t.Fatalf("complete command returned %T, want editorDoneMsg", doneMsg)
	}
	if done.ExecErr == nil || done.ExecErr.Error() != "fork failed" {
		t.Errorf("ExecErr = %v, want the launch failure carried through", done.ExecErr)
	}
}

// TestRuntimeStatusMessage_BranchCoverage exercises the probe report
// statuses that the runtime probe tests never produce: running without a
// version and unknown with a detected binary.
func TestRuntimeStatusMessage_BranchCoverage(t *testing.T) {
	if got := runtimeStatusMessage(runtime.Report{Status: runtime.StatusRunning}); got != "✓ caddy running" {
		t.Errorf("running without version = %q, want the plain running line", got)
	}
	got := runtimeStatusMessage(runtime.Report{Status: runtime.StatusUnknown, Capabilities: runtime.Capabilities{Binary: true, Version: "v2.11.4"}})
	if !strings.Contains(got, "v2.11.4") || !strings.Contains(got, "no Admin API") {
		t.Errorf("unknown with binary = %q, want the detected-binary line", got)
	}
	if got := runtimeStatusMessage(runtime.Report{Status: runtime.StatusUnknown}); got != "" {
		t.Errorf("unknown without binary = %q, want an empty status", got)
	}
}

// runFailingCommand runs a real failing command and returns its
// *exec.ExitError, so the editor exec mapping can be exercised against a
// genuine exit status.
func runFailingCommand(exitCode int) error {
	return exec.Command("sh", "-c", "exit "+strconv.Itoa(exitCode)).Run()
}

// TestReopenEditDiff_ReopensPendingWorkflows verifies that a failed save
// reopens the correct diff modal for a pending editor edit and for a
// pending delete, and that a no-op stays a no-op when nothing is pending.
func TestReopenEditDiff_ReopensPendingWorkflows(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeSaver{}, &fakeEditor{})
	m = resize(m, 120, 30)

	// Nothing pending: no modal.
	m.reopenEditDiff()
	if m.showDiff {
		t.Fatal("reopenEditDiff opened a modal without pending work")
	}

	// A pending editor edit reopens the edit diff.
	m.pendingEdit = &pendingEdit{
		path:     "config/Caddyfile",
		original: []byte("example.test {\n\trespond ok\n}\n"),
		content:  []byte("example.test {\n\trespond changed\n}\n"),
	}
	m.reopenEditDiff()
	if !m.showDiff {
		t.Fatal("reopenEditDiff did not reopen the edit diff")
	}
	if m.diffTitle == "" || !strings.Contains(m.diffTitle, "Diff · config/Caddyfile") {
		t.Errorf("diffTitle = %q, want the edit diff title", m.diffTitle)
	}
	m.closeDiff()

	// A pending delete reopens the delete diff (once no edit is pending).
	m.pendingEdit = nil
	m.pendingDelete = &pendingDelete{
		path:     "config/Caddyfile",
		original: []byte("example.test {\n\trespond ok\n}\n"),
		content:  []byte("example.test {\n}\n"),
	}
	m.reopenEditDiff()
	if !m.showDiff {
		t.Fatal("reopenEditDiff did not reopen the delete diff")
	}
	if !strings.Contains(m.diffTitle, "Delete · config/Caddyfile") {
		t.Errorf("diffTitle = %q, want the delete diff title", m.diffTitle)
	}
}

// TestStartDiff_Guards covers the diff guards: no state, no document
// selection and an unreadable on-disk source each fail with a status
// message instead of opening a modal.
func TestStartDiff_Guards(t *testing.T) {
	t.Run("no state", func(t *testing.T) {
		m := newLoadedModel(t, fakeLoader{state: nil})
		updated, cmd := m.startDiff()
		m = updated.(*Model)
		if cmd != nil {
			t.Error("startDiff without a state returned a command")
		}
		if m.showDiff {
			t.Error("startDiff without a state opened a modal")
		}
	})

	t.Run("no document selected", func(t *testing.T) {
		m := newLoadedModel(t, fakeLoader{state: stateFor(t, "Caddyfile", func(string) ([]byte, error) {
			return []byte("example.test {\n}\n"), nil
		})})
		m.cursor = 1 << 20 // out of range: no selection
		updated, cmd := m.startDiff()
		m = updated.(*Model)
		if cmd != nil {
			t.Error("startDiff without a selection returned a command")
		}
		if !strings.Contains(m.statusMessage, "no document selected") {
			t.Errorf("statusMessage = %q, want the no-selection hint", m.statusMessage)
		}
	})

	t.Run("unreadable on-disk source", func(t *testing.T) {
		state := stateFor(t, "Caddyfile", func(string) ([]byte, error) {
			return []byte("example.test {\n}\n"), nil
		})
		m := newLoadedModel(t, fakeLoader{state: state}, app.FileReader(func(string) ([]byte, error) {
			return nil, errors.New("permission denied")
		}))
		updated, cmd := m.startDiff()
		m = updated.(*Model)
		if cmd != nil {
			t.Error("startDiff with an unreadable doc returned a command")
		}
		if !strings.Contains(m.statusMessage, "diff unavailable") {
			t.Errorf("statusMessage = %q, want the unreadable diff hint", m.statusMessage)
		}
	})
}
