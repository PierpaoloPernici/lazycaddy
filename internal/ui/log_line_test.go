package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

// renderCompactANSI renders one compact log line with ANSI output forced
// on, then restores the terminal-agnostic profile, mirroring renderWithANSI.
func renderCompactANSI(entry logs.Entry, maxW int) string {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	return renderCompactLogLine(entry, maxW)
}

// accessEntry builds a parsed access-log entry with the given URI and
// message over a fixed UTC timestamp (the display is LOCAL, so tests
// derive the expected timestamp via entry.Timestamp.Local()).
func accessEntry(uri, msg string) logs.Entry {
	return logs.Entry{
		Raw:       []byte(`{"level":"info","ts":1760000000.035,"logger":"http.log.access","msg":"handled request"}`),
		Parsed:    true,
		Timestamp: time.Date(2026, 8, 8, 15, 42, 26, 35e6, time.UTC),
		Level:     "info",
		Logger:    "http.log.access",
		Msg:       msg,
		Status:    200,
		Method:    "GET",
		URI:       uri,
	}
}

// TestRenderCompactLogLine_AccessLine verifies the compact layout renders
// the local date and timestamp, level, logger, method, URI, status and
// message as explicit text, styled with ANSI codes.
func TestRenderCompactLogLine_AccessLine(t *testing.T) {
	entry := accessEntry("/api/config", "handled request")
	out := renderCompactANSI(entry, 200)
	if out == "" {
		t.Fatal("compact line rendered empty")
	}
	wantTS := entry.Timestamp.Local().Format(logTimestampLayout)
	visible := stripANSI(out)
	for _, want := range []string{wantTS, "INFO", "http.log.access", "GET", "/api/config", "200", "—", "handled request"} {
		if !strings.Contains(visible, want) {
			t.Errorf("stripped compact line missing %q:\n%s", want, visible)
		}
	}
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("compact line must be styled, got no ANSI:\n%s", out)
	}
}

// TestRenderCompactLogLine_LevelAndStatusSemanticWithoutColor verifies the
// explicit ERROR and 500 labels survive color stripping, and the error-red
// style is applied to the raw output.
func TestRenderCompactLogLine_LevelAndStatusSemanticWithoutColor(t *testing.T) {
	entry := accessEntry("/boom", "server error")
	entry.Level = "error"
	entry.Status = 500
	out := renderCompactANSI(entry, 200)
	visible := stripANSI(out)
	if !strings.Contains(visible, "ERROR") {
		t.Errorf("stripped compact line missing the ERROR label:\n%s", visible)
	}
	if !strings.Contains(visible, "500") {
		t.Errorf("stripped compact line missing the 500 label:\n%s", visible)
	}
	if !strings.Contains(out, sgrOf(logLevelErrorStyle)) {
		t.Errorf("error level must use the theme error style:\n%s", out)
	}
}

// TestRenderCompactLogLine_ZeroTimestamp verifies a zero timestamp renders
// the fixed-width placeholder.
func TestRenderCompactLogLine_ZeroTimestamp(t *testing.T) {
	entry := accessEntry("/", "no ts")
	entry.Timestamp = time.Time{}
	out := renderCompactANSI(entry, 200)
	if !strings.Contains(stripANSI(out), logTimestampPlaceholder) {
		t.Errorf("compact line missing the zero-timestamp placeholder:\n%s", stripANSI(out))
	}
}

// TestRenderCompactLogLine_GeneralLogNoStatus verifies a general log
// (no request/status) renders the logger and message without a status
// column.
func TestRenderCompactLogLine_GeneralLogNoStatus(t *testing.T) {
	entry := logs.Entry{
		Raw:       []byte(`{"ts":1760000000.5,"level":"info","logger":"caddy.config","msg":"loading config"}`),
		Parsed:    true,
		Timestamp: time.Date(2026, 8, 8, 15, 42, 26, 5e8, time.UTC),
		Level:     "info",
		Logger:    "caddy.config",
		Msg:       "loading config",
		Status:    -1,
	}
	out := renderCompactANSI(entry, 200)
	visible := stripANSI(out)
	if !strings.Contains(visible, "caddy.config") || !strings.Contains(visible, "loading config") {
		t.Errorf("general log compact line missing logger/msg:\n%s", visible)
	}
	if strings.Contains(visible, " 200 ") {
		t.Errorf("general log must not render a status column, got:\n%s", visible)
	}
}

// TestRenderCompactLogLine_LongLineAnsiSafe verifies a long line is reduced
// before styling, fits the width, and no ANSI sequence is split.
func TestRenderCompactLogLine_LongLineAnsiSafe(t *testing.T) {
	entry := accessEntry("/", strings.Repeat("very long message content ", 10))
	out := renderCompactANSI(entry, 60)
	visible := stripANSI(out)
	if w := lipgloss.Width(visible); w > 60 {
		t.Errorf("stripped output width = %d, exceeds 60", w)
	}
	if strings.Contains(visible, "very long message content") {
		t.Errorf("long message was not reduced:\n%s", visible)
	}
	if out == visible {
		t.Errorf("truncated line must still be styled, got no ANSI:\n%s", out)
	}
}

// TestRenderCompactLogLine_NonJSONVerbatim verifies a non-JSON entry is
// rendered verbatim with no ANSI codes at a width that fits.
func TestRenderCompactLogLine_NonJSONVerbatim(t *testing.T) {
	raw := "2026/08/08 12:00:00 INFO something happened"
	entry := logs.Entry{Raw: []byte(raw), Status: -1} // Parsed false
	out := renderCompactANSI(entry, 200)
	if out != raw {
		t.Errorf("non-JSON line = %q, want verbatim %q", out, raw)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("non-JSON line must not be styled, got:\n%s", out)
	}
}

// TestRenderCompactLogLine_EmptyRaw verifies an empty raw line renders
// empty.
func TestRenderCompactLogLine_EmptyRaw(t *testing.T) {
	if got := renderCompactANSI(logs.Entry{Raw: nil, Status: -1}, 200); got != "" {
		t.Errorf("empty line rendered %q, want empty", got)
	}
}
