package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	tea "github.com/charmbracelet/bubbletea"
)

// TestModelLogView_UnavailableWithoutSource verifies that pressing l
// without a configured log source surfaces a hint and opens nothing.
func TestModelLogView_UnavailableWithoutSource(t *testing.T) {
	state := logStateFor(t)
	m := newLoadedModel(t, fakeLoader{state: state}) // no log source
	m = resize(m, 120, 30)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if cmd != nil {
		t.Errorf("expected nil cmd without a log source, got %v", cmd)
	}
	if m.showLogs {
		t.Error("showLogs = true without a log source, want false")
	}
	if !strings.Contains(m.statusMessage, "no log source configured") {
		t.Errorf("statusMessage = %q, want a no-log-source hint", m.statusMessage)
	}
}

// TestModelLogView_OpenSeedsHistory verifies that opening the log view
// seeds the scrollback from the source history, starts the polling tick
// and renders the log pane with the followed path and entries.
func TestModelLogView_OpenSeedsHistory(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{logEntry("handled request"), logEntry("second line")} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("opening the log view must return a poll command")
	}
	if !m.showLogs {
		t.Fatal("showLogs = false after l, want true")
	}
	if len(m.logLines) != 2 {
		t.Fatalf("logLines = %d, want 2 (seeded from history)", len(m.logLines))
	}
	view := m.View()
	if !strings.Contains(view, "Logs · logs/access.log") {
		t.Errorf("View missing the log pane title:\n%s", view)
	}
	visible := stripANSI(view)
	if !strings.Contains(visible, "handled request") || !strings.Contains(visible, "second line") {
		t.Errorf("View missing the seeded entry text:\n%s", visible)
	}
}

// TestModelLogView_JournalUnitTitle verifies that when the configured source
// is a systemd journal unit, the log view title identifies the unit.
func TestModelLogView_JournalUnitTitle(t *testing.T) {
	state := logStateFor(t)
	state.Settings.LogPath = ""
	state.Settings.JournalUnit = "caddy.service"
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("opening the log view must return a poll command")
	}
	if !strings.Contains(m.View(), "Logs · unit caddy.service") {
		t.Errorf("View missing the journal unit title:\n%s", m.View())
	}
}

// TestModelLogView_JournalPollErrorKeepsBrowsing verifies that a failing
// journal source surfaces through the existing poll-error status line while
// the rest of the TUI stays browsable: the error is reported, the log view
// stays open, and tree navigation still works.
func TestModelLogView_JournalPollErrorKeepsBrowsing(t *testing.T) {
	state := logStateFor(t)
	state.Settings.LogPath = ""
	state.Settings.JournalUnit = "caddy.service"
	pollErr := errors.New("journalctl unavailable")
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, pollErr },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)

	// Open the log view: the first poll surfaces the source error.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("opening the log view must return a poll command")
	}
	updated, _ = m.Update(logTailMsg{Err: pollErr})
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "✗ log poll failed") {
		t.Errorf("statusMessage = %q, want the poll-failure message", m.statusMessage)
	}
	if !errors.Is(m.logErr, pollErr) {
		t.Errorf("logErr = %v, want the sentinel poll error", m.logErr)
	}

	// Browsing still works: close the log view (Esc) and navigate the tree.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*Model)
	if m.showLogs {
		t.Error("showLogs = true after closing, want false")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(*Model)
	if m.cursor != 1 {
		t.Errorf("cursor = %d after navigating, want 1 (tree still navigable)", m.cursor)
	}
}

// TestModelLogTail_AppendsAndReschedules verifies that a delivered poll
// result appends entries and reschedules the next poll, and that an empty
// result keeps polling.
func TestModelLogTail_AppendsAndReschedules(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)

	// A non-empty poll appends and reschedules.
	updated, cmd := m.Update(logTailMsg{Entries: []logs.Entry{logEntry("first")}})
	m = updated.(*Model)
	if len(m.logLines) != 1 {
		t.Fatalf("logLines = %d, want 1 after a poll", len(m.logLines))
	}
	if cmd == nil {
		t.Error("poll with new entries must reschedule (non-nil cmd)")
	}
	// An empty poll still reschedules.
	updated, cmd = m.Update(logTailMsg{Entries: nil})
	m = updated.(*Model)
	if cmd == nil {
		t.Error("empty poll must reschedule (non-nil cmd)")
	}
	if m.logErr != nil {
		t.Errorf("logErr = %v, want nil after a clean poll", m.logErr)
	}
}

// TestModelLogTail_PauseStopsPolling verifies that p suspends polling
// (nil reschedule) and a second p resumes it.
func TestModelLogTail_PauseStopsPolling(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = updated.(*Model)
	if !m.logPaused {
		t.Fatal("logPaused = false after p, want true")
	}
	if cmd != nil {
		t.Errorf("p must stop the poll (nil cmd), got %v", cmd)
	}
	// A poll delivered while paused must not reschedule.
	updated, cmd = m.Update(logTailMsg{Entries: []logs.Entry{logEntry("late")}})
	m = updated.(*Model)
	if cmd != nil {
		t.Errorf("paused poll must not reschedule, got %v", cmd)
	}
	if len(m.logLines) != 1 {
		t.Errorf("logLines = %d, want 1 (entries still appended while paused)", len(m.logLines))
	}
	// Resuming restarts the poll.
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = updated.(*Model)
	if m.logPaused {
		t.Error("logPaused = true after second p, want false")
	}
	if cmd == nil {
		t.Error("resume must restart the poll (non-nil cmd)")
	}
}

// TestModelLogTail_FollowToggle verifies that f toggles follow and that
// scrolling up turns follow off (the operator takes control).
func TestModelLogTail_FollowToggle(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if !m.logFollow {
		t.Fatal("logFollow = false on open, want true")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if m.logFollow {
		t.Error("logFollow = true after f, want false")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if !m.logFollow {
		t.Error("logFollow = false after second f, want true")
	}
	// Pressing up hands control back to the operator.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.logFollow {
		t.Error("logFollow = true after up, want false")
	}
	if !strings.Contains(m.statusMessage, "follow off") {
		t.Errorf("statusMessage = %q, want follow-off hint", m.statusMessage)
	}
}

// TestModelLogTail_EscCloses verifies that Esc closes the log view and
// that a poll delivered afterwards is not rescheduled.
func TestModelLogTail_EscCloses(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if !m.showLogs {
		t.Fatal("log view not open")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*Model)
	if m.showLogs {
		t.Error("showLogs = true after Esc, want false")
	}
	if cmd != nil {
		t.Errorf("Esc must stop the poll (nil cmd), got %v", cmd)
	}
	// A late poll result must not reschedule after the view is closed.
	updated, cmd = m.Update(logTailMsg{Entries: []logs.Entry{logEntry("late")}})
	m = updated.(*Model)
	if cmd != nil {
		t.Errorf("poll after close must not reschedule, got %v", cmd)
	}
}

// TestModelLogTail_ClearsStaleError verifies that a successful poll clears
// the "log poll failed" status line left by a previous failed poll, while
// leaving status messages set by other actions untouched.
func TestModelLogTail_ClearsStaleError(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)

	// A failing poll sets the error status line.
	updated, _ = m.Update(logTailMsg{Err: errors.New("boom")})
	m = updated.(*Model)
	if !strings.Contains(m.statusMessage, "log poll failed") {
		t.Fatalf("statusMessage = %q, want a poll-failure message", m.statusMessage)
	}
	if m.logErr == nil {
		t.Fatal("logErr = nil after a failed poll, want the error")
	}

	// A successful poll clears it.
	updated, _ = m.Update(logTailMsg{Entries: []logs.Entry{{Raw: []byte("x"), Status: -1}}})
	m = updated.(*Model)
	if m.statusMessage != "" {
		t.Errorf("statusMessage = %q, want cleared after a successful poll", m.statusMessage)
	}
	if m.logErr != nil {
		t.Errorf("logErr = %v, want nil after a successful poll", m.logErr)
	}

	// Status messages owned by other actions are NOT cleared by a poll.
	m.statusMessage = "log follow on"
	updated, _ = m.Update(logTailMsg{Entries: nil})
	m = updated.(*Model)
	if m.statusMessage != "log follow on" {
		t.Errorf("statusMessage = %q, want it untouched (only poll failures are cleared)", m.statusMessage)
	}
}

// TestModelLogTail_Bounded verifies the UI-side scrollback stays capped at
// logMaxLines and keeps the tail.
func TestModelLogTail_Bounded(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)

	feed := make([]logs.Entry, 0, logMaxLines+50)
	for i := 0; i < logMaxLines+50; i++ {
		feed = append(feed, logEntry(fmt.Sprintf("line-%d", i)))
	}
	updated, _ = m.Update(logTailMsg{Entries: feed})
	m = updated.(*Model)
	if len(m.logLines) != logMaxLines {
		t.Fatalf("logLines = %d, want %d", len(m.logLines), logMaxLines)
	}
	// The tail is preserved: the newest entry survives.
	if m.logLines[len(m.logLines)-1].Msg != "line-1049" {
		t.Errorf("last entry = %q, want line-1049 (tail preserved)", m.logLines[len(m.logLines)-1].Msg)
	}
}

// TestModelLogView_Footer verifies the footer lists the l key only when a
// log source is configured and shows the log-view keys while it is open.
func TestModelLogView_Footer(t *testing.T) {
	state := logStateFor(t)
	// Without a log source the l key is absent.
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	if strings.Contains(stripANSI(m.View()), "l logs") {
		t.Errorf("footer shows 'l logs' without a log source:\n%s", m.View())
	}
	// The main footer stays navigation-only even with a log source.
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m = newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	if strings.Contains(stripANSI(m.View()), "l logs") {
		t.Errorf("footer should not list operational commands:\n%s", m.View())
	}
	// While the log view is open the footer shows the log-view key hints.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	view := stripANSI(m.View())
	for _, want := range []string{"Enter/→ detail", "f follow", "p pause/resume", "Esc close", "q quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("footer missing %q while the log view is open:\n%s", want, view)
		}
	}
}

// seededLogSource returns a LogSource whose history holds count entries.
func seededLogSource(count int) app.LogSourceFunc {
	return app.LogSourceFunc{
		NextFn: func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry {
			entries := make([]logs.Entry, 0, count)
			for i := 0; i < count; i++ {
				entries = append(entries, logEntry(fmt.Sprintf("e-%d", i)))
			}
			return entries
		},
	}
}

// TestModelLogCursor_MovesAndReveals verifies the row cursor starts on the
// newest entry and moves with up/down, turning follow off on up.
func TestModelLogCursor_MovesAndReveals(t *testing.T) {
	state := logStateFor(t)
	m := newLoadedModel(t, fakeLoader{state: state}, seededLogSource(10))
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if m.logCursor != 9 {
		t.Fatalf("logCursor = %d, want 9 (newest) on open", m.logCursor)
	}
	// Up turns follow off and moves the cursor.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.logFollow {
		t.Error("logFollow = true after up, want false")
	}
	if m.logCursor != 8 {
		t.Errorf("logCursor = %d after up, want 8", m.logCursor)
	}
	// Down moves back toward the newest.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.logCursor != 9 {
		t.Errorf("logCursor = %d after down, want 9", m.logCursor)
	}
}

// TestModelLogCursor_FollowKeepsNewest verifies that new entries keep the
// cursor on the newest line while follow is on.
func TestModelLogCursor_FollowKeepsNewest(t *testing.T) {
	state := logStateFor(t)
	m := newLoadedModel(t, fakeLoader{state: state}, seededLogSource(2))
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	updated, _ = m.Update(logTailMsg{Entries: []logs.Entry{logEntry("a"), logEntry("b")}})
	m = updated.(*Model)
	if !m.logFollow {
		t.Fatal("logFollow = false, want true")
	}
	if m.logCursor != len(m.logLines)-1 {
		t.Errorf("logCursor = %d, want the newest (%d) while following", m.logCursor, len(m.logLines)-1)
	}
}

// TestModelLogCursor_AdjustsAfterTrim verifies the cursor stays valid and
// stable when the bounded trim drops entries from the front.
func TestModelLogCursor_AdjustsAfterTrim(t *testing.T) {
	state := logStateFor(t)
	m := newLoadedModel(t, fakeLoader{state: state}, seededLogSource(logMaxLines))
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	if m.logCursor != logMaxLines-1 {
		t.Fatalf("logCursor = %d, want %d on open", m.logCursor, logMaxLines-1)
	}
	// Deliver enough entries to force a trim while following.
	feed := make([]logs.Entry, 60)
	for i := range feed {
		feed[i] = logEntry(fmt.Sprintf("new-%d", i))
	}
	updated, _ = m.Update(logTailMsg{Entries: feed})
	m = updated.(*Model)
	if len(m.logLines) != logMaxLines {
		t.Fatalf("logLines = %d, want %d after trim", len(m.logLines), logMaxLines)
	}
	if m.logCursor != len(m.logLines)-1 {
		t.Errorf("logCursor = %d, want the newest (%d) while following after trim", m.logCursor, len(m.logLines)-1)
	}
	// Turn follow off, then trim again: the cursor subtracts the dropped
	// count so it keeps pointing at the same entry.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp}) // follow off, cursor 998
	if m.logFollow {
		t.Fatal("logFollow = true after up, want false")
	}
	before := m.logCursor
	updated, _ = m.Update(logTailMsg{Entries: feed})
	m = updated.(*Model)
	want := before - 60
	if want < 0 {
		want = 0
	}
	if m.logCursor != want {
		t.Errorf("logCursor = %d after trim with follow off, want %d", m.logCursor, want)
	}
	if m.logCursor < 0 || m.logCursor >= len(m.logLines) {
		t.Errorf("logCursor = %d out of range for %d entries", m.logCursor, len(m.logLines))
	}
}

// TestModelLogDetail_EnterOpensEscCloses verifies Enter opens the detail
// modal for the selected entry and Esc closes it (once to the list, again
// to the main screen).
func TestModelLogDetail_EnterOpensEscCloses(t *testing.T) {
	state := logStateFor(t)
	m := newLoadedModel(t, fakeLoader{state: state}, seededLogSource(3))
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	// Move from index 2 (newest) to index 1.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.logCursor != 1 {
		t.Fatalf("logCursor = %d, want 1", m.logCursor)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if !m.logDetailOpen {
		t.Fatal("logDetailOpen = false after Enter, want true")
	}
	if string(m.logDetailEntry.Raw) != string(m.logLines[1].Raw) {
		t.Errorf("logDetailEntry.Raw = %q, want the selected entry %q", m.logDetailEntry.Raw, m.logLines[1].Raw)
	}
	// Esc closes the detail but keeps the log view open.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.logDetailOpen {
		t.Error("logDetailOpen = true after Esc, want false")
	}
	if !m.showLogs {
		t.Error("showLogs = false after Esc from detail, want true")
	}
	// Esc again closes the log view.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showLogs {
		t.Error("showLogs = true after second Esc, want false")
	}
}

// TestModelLogDetail_ShowsFullJSON verifies the detail modal renders the
// full lossless JSON of the selected entry and the footer shows the detail
// keys.
func TestModelLogDetail_ShowsFullJSON(t *testing.T) {
	state := logStateFor(t)
	raw := `{"level":"info","ts":1760000000.5,"logger":"http.log.access","msg":"handled request","request":{"method":"GET","host":"localhost","uri":"/api/config"},"status":200}`
	entry, err := logs.ParseEntry([]byte(raw))
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{entry} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if !m.logDetailOpen {
		t.Fatal("detail modal not open")
	}
	view := m.View()
	visible := stripANSI(view)
	if !strings.Contains(visible, `"request"`) || !strings.Contains(visible, "/api/config") {
		t.Errorf("detail modal missing the full JSON:\n%s", visible)
	}
	if !strings.Contains(visible, "Esc/← back") {
		t.Errorf("footer missing the detail hint 'Esc/← back':\n%s", visible)
	}
}

func TestModelLogViewsSeparateTitlesFromEntries(t *testing.T) {
	state := logStateFor(t)
	m := newLoadedModel(t, fakeLoader{state: state})
	m.logLines = []logs.Entry{logEntry("handled request")}
	m.logCursor = 0

	view := stripANSI(m.logView(100, 24))
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if strings.Contains(line, "Logs · logs/access.log") {
			if i+1 >= len(lines) || strings.Trim(lines[i+1], "│ ") != "" {
				t.Fatalf("log title is not separated from entries:\n%s", view)
			}
			break
		}
		if i == len(lines)-1 {
			t.Fatalf("log title missing:\n%s", view)
		}
	}

	m.logDetailEntry = logEntry("handled request")
	detail := stripANSI(m.logDetailView(100, 24))
	detailLines := strings.Split(detail, "\n")
	for i, line := range detailLines {
		if strings.Contains(line, "Log detail") {
			if i+1 >= len(detailLines) || strings.Trim(detailLines[i+1], "│ ") != "" {
				t.Fatalf("log detail title is not separated from content:\n%s", detail)
			}
			return
		}
		if i == len(detailLines)-1 {
			t.Fatalf("log detail title missing:\n%s", detail)
		}
	}
}

// TestModelLogDetail_NonJSONEntry verifies the detail modal shows the raw
// line verbatim for a non-JSON entry.
func TestModelLogDetail_NonJSONEntry(t *testing.T) {
	state := logStateFor(t)
	raw := "2026/08/08 12:00:00 INFO something happened"
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{{Raw: []byte(raw), Status: -1}} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if !m.logDetailOpen {
		t.Fatal("detail modal not open")
	}
	if !strings.Contains(stripANSI(m.View()), raw) {
		t.Errorf("detail modal missing the raw line:\n%s", m.View())
	}
}

// TestModelLogView_CompactLines verifies the log list renders the compact
// human-readable layout (not the raw JSON blob).
func TestModelLogView_CompactLines(t *testing.T) {
	state := logStateFor(t)
	raw := `{"level":"info","ts":1760000000.5,"logger":"http.log.access","msg":"handled request","request":{"method":"GET","host":"localhost","uri":"/api/config"},"status":200}`
	entry, err := logs.ParseEntry([]byte(raw))
	if err != nil {
		t.Fatalf("ParseEntry: %v", err)
	}
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{entry} },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = updated.(*Model)
	visible := stripANSI(m.View())
	for _, want := range []string{"—", "GET", "/api/config", "200", "handled request"} {
		if !strings.Contains(visible, want) {
			t.Errorf("compact log view missing %q:\n%s", want, visible)
		}
	}
	// The raw JSON structure must be gone from the list view.
	if strings.Contains(visible, `"request":{`) {
		t.Errorf("compact log view still shows the raw JSON blob:\n%s", visible)
	}
}

func TestModelLogView_QuitKeys(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	for _, key := range []tea.KeyType{tea.KeyRunes, tea.KeyCtrlC} {
		msg := tea.KeyMsg{Type: key}
		if key == tea.KeyRunes {
			msg.Runes = []rune("q")
		}
		updated, cmd := m.Update(msg)
		m = updated.(*Model)
		if cmd == nil {
			t.Errorf("%s in the log view did not request quit", msg.String())
		}
	}

	// The same keys quit from the log detail modal.
	m.logLines = []logs.Entry{logEntry("handled request")}
	m.logCursor = 0
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // open detail
	if !m.logDetailOpen {
		t.Fatal("log detail did not open on Enter")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = updated.(*Model)
	if cmd == nil {
		t.Error("q in the log detail did not request quit")
	}
}

func TestModelLogTail_PageKeysTurnFollowOff(t *testing.T) {
	state := logStateFor(t)
	entries := []logs.Entry{logEntry("one"), logEntry("two"), logEntry("three")}
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return entries },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m.View() // sizes the viewport

	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.logFollow {
		t.Error("PgUp left follow enabled")
	}
	if !strings.Contains(m.statusMessage, "follow off") {
		t.Errorf("statusMessage = %q after PgUp, want the follow-off hint", m.statusMessage)
	}
	if m.logCursor > len(m.logLines)-1 {
		t.Errorf("logCursor = %d out of range after PgUp", m.logCursor)
	}

	// PgDown moves the cursor towards the end and clamps there.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.logCursor > len(m.logLines)-1 {
		t.Errorf("logCursor = %d out of range after PgDown", m.logCursor)
	}
}

func TestModelLogView_TitleStates(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})

	m.logPaused = true
	if view := stripANSI(m.View()); !strings.Contains(view, "PAUSED") {
		t.Errorf("paused log view missing the PAUSED marker:\n%s", view)
	}
	m.logPaused = false
	m.logErr = errors.New("journal poll failed")
	if view := stripANSI(m.View()); !strings.Contains(view, "poll error") {
		t.Errorf("log view missing the poll-error marker:\n%s", view)
	}
}

func TestModelLogView_TinySizes(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})

	// Size clamps never panic and always render something.
	for _, size := range []struct{ w, h int }{{1, 2}, {40, 2}, {0, 0}} {
		if got := m.logView(size.w, size.h); got == "" {
			t.Errorf("logView(%d, %d) rendered empty", size.w, size.h)
		}
		if got := m.logDetailView(size.w, size.h); got == "" {
			t.Errorf("logDetailView(%d, %d) rendered empty", size.w, size.h)
		}
	}
	m.syncLogViewport(1, 1)
	m.syncLogViewport(5, 0)
	m.syncLogDetailContent(1, 1)
	m.syncLogDetailContent(5, 0)

	// With follow off, a manual offset survives a re-sync (clamped).
	m.logFollow = false
	m.logViewport.SetYOffset(999)
	m.syncLogViewport(60, 10)
	if m.logViewport.YOffset > 0 {
		t.Errorf("manual offset was not clamped into the restored position: %d", m.logViewport.YOffset)
	}
}

func TestModelLogDetail_TinyTitle(t *testing.T) {
	state := logStateFor(t)
	src := app.LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return nil, nil },
		HistoryFn: func() []logs.Entry { return nil },
	}
	m := newLoadedModel(t, fakeLoader{state: state}, src)
	m = resize(m, 120, 30)
	m.logDetailEntry = logEntry("a long message")
	// A narrow window clamps the summary width to the 30-cell floor.
	if got := m.logDetailView(20, 10); got == "" {
		t.Error("logDetailView(20, 10) rendered empty")
	}
}
