package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

// renderWithANSI renders through highlightSource with ANSI output forced on,
// then restores the terminal-agnostic profile. Without the forced profile
// lipgloss emits no escape sequences when stdout is not a TTY, so the style
// assertions below would silently test nothing.
func renderWithANSI(src []byte) string {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	return highlightSource(src)
}

var gutterRe = regexp.MustCompile(`^\s*\d+│ `)

// assertSourceLossless verifies that every source line appears in the
// stripped rendered output exactly once, with the gutter removed: byte
// losslessness of the rendered view.
func assertSourceLossless(t *testing.T, src []byte, got string) {
	t.Helper()
	rendered := strings.Split(stripANSI(got), "\n")
	if len(rendered) > 0 && rendered[len(rendered)-1] == "" {
		rendered = rendered[:len(rendered)-1]
	}
	want := strings.Split(string(src), "\n")
	if len(rendered) != len(want) {
		t.Fatalf("rendered %d lines, want %d:\n%s", len(rendered), len(want), got)
	}
	for i := range want {
		body := gutterRe.ReplaceAllString(rendered[i], "")
		if body != want[i] {
			t.Errorf("rendered line %d body = %q, want %q", i+1, body, want[i])
		}
	}
}

// TestHighlightSourceBraceSite verifies the basic rendering of a braced
// site: gutters, token text, and ANSI styling between the unstyled header
// word and the styled brace token.
func TestHighlightSourceBraceSite(t *testing.T) {
	src := []byte("example.test {\n\trespond ok\n}\n")
	got := renderWithANSI(src)
	if !strings.Contains(got, "1│") || !strings.Contains(got, "3│") {
		t.Errorf("gutter line numbers missing:\n%s", got)
	}
	stripped := stripANSI(got)
	if !strings.Contains(stripped, "example.test") || !strings.Contains(stripped, "respond") {
		t.Errorf("token text missing from the rendered view:\n%s", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("expected ANSI styling for the brace token, got none:\n%s", got)
	}
	// The brace is styled, so the raw header must no longer be contiguous.
	if strings.Contains(got, "example.test {") {
		t.Errorf("header word and brace must be separated by ANSI codes, got:\n%s", got)
	}
	assertSourceLossless(t, src, got)
}

// TestHighlightSourceCommentStyled verifies a full-line comment is wrapped
// in the comment style and survives byte-for-byte.
func TestHighlightSourceCommentStyled(t *testing.T) {
	src := []byte("# hello\n")
	got := renderWithANSI(src)
	stripped := stripANSI(got)
	line := strings.Split(stripped, "\n")[0]
	if line != "   1│ # hello" {
		t.Errorf("stripped line = %q, want %q", line, "   1│ # hello")
	}
	if !strings.Contains(got, "\x1b[") || !strings.Contains(got, "38;5;245") {
		t.Errorf("comment must be styled with the dim foreground, got:\n%s", got)
	}
}

// TestHighlightSourcePlaceholderStyled verifies the placeholder sub-token is
// styled inside its bareword, and that the stripped line still equals the
// source cell-for-cell.
func TestHighlightSourcePlaceholderStyled(t *testing.T) {
	src := []byte("respond {$MSG}\n")
	got := renderWithANSI(src)
	stripped := stripANSI(got)
	line := strings.Split(stripped, "\n")[0]
	if line != "   1│ respond {$MSG}" {
		t.Errorf("stripped line = %q, want %q", line, "   1│ respond {$MSG}")
	}
	if w := lipgloss.Width(line); w != 20 {
		t.Errorf("stripped line width = %d, want 20", w)
	}
	if !strings.Contains(got, "{$MSG}") {
		t.Errorf("placeholder text missing from the rendered view:\n%s", got)
	}
	if !strings.Contains(got, "38;5;212") {
		t.Errorf("placeholder must use the cursor accent, got:\n%s", got)
	}
}

// TestHighlightSourceStringStyled verifies a quoted string token is wrapped
// in the string style while the surrounding directive stays plain.
func TestHighlightSourceStringStyled(t *testing.T) {
	src := []byte("custom_plugin_directive \"keep this raw\"\n")
	got := renderWithANSI(src)
	stripped := stripANSI(got)
	if !strings.Contains(stripped, `custom_plugin_directive "keep this raw"`) {
		t.Errorf("stripped view lost the directive text:\n%s", got)
	}
	if !strings.Contains(got, "38;5;114") {
		t.Errorf("string token must use the soft-green string style, got:\n%s", got)
	}
	assertSourceLossless(t, src, got)
}

// TestHighlightSourceWideChars verifies that wide (CJK) runes are never
// split or dropped: every byte of the source appears exactly once.
func TestHighlightSourceWideChars(t *testing.T) {
	src := []byte("example.test {\n\trespond 日本語ok\n}\n")
	got := renderWithANSI(src)
	if !strings.Contains(stripANSI(got), "日本語") {
		t.Errorf("wide runes missing from the rendered view:\n%s", got)
	}
	assertSourceLossless(t, src, got)
}

// TestHighlightSourceTabs verifies a tab character renders as one cell and
// is preserved byte-for-byte.
func TestHighlightSourceTabs(t *testing.T) {
	src := []byte("example.test {\n\treverse_proxy localhost:8080\n}\n")
	got := renderWithANSI(src)
	if !strings.Contains(stripANSI(got), "\treverse_proxy") {
		t.Errorf("tab character lost in the rendered view:\n%s", got)
	}
	assertSourceLossless(t, src, got)
}

// TestHighlightSourceEmpty verifies the empty-source message is returned,
// dim-styled exactly like the previous numberedSource.
func TestHighlightSourceEmpty(t *testing.T) {
	got := renderWithANSI(nil)
	if !strings.Contains(stripANSI(got), "(empty source — raw view still available)") {
		t.Errorf("empty source message missing:\n%s", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("empty source message must be dim-styled, got:\n%s", got)
	}
}

// TestHighlightSourceANSIChunksSelfClose verifies every rendered line keeps
// its ANSI escape sequences balanced (each styled chunk emits its own reset).
func TestHighlightSourceANSIChunksSelfClose(t *testing.T) {
	src := []byte("# c\nexample.test {\n\trespond \"s\" {p}\n}\n")
	got := renderWithANSI(src)
	re := regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	for i, line := range strings.Split(got, "\n") {
		if n := len(re.FindAllString(line, -1)); n%2 != 0 {
			t.Errorf("line %d has %d ANSI sequences, want an even number (balanced chunks):\n%s", i+1, n, line)
		}
	}
}

// renderLogLineANSI renders one log line with ANSI output forced on, then
// restores the terminal-agnostic profile, mirroring renderWithANSI. maxW
// is passed straight through to renderLogLine so each test controls the
// truncation width.
func renderLogLineANSI(entry logs.Entry, maxW int) string {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	return renderLogLine(entry, maxW)
}

// TestRenderLogLine_HighlightsJSON renders a realistic Caddy access log
// line (the official docs example) and verifies it is both lossless
// (every byte of the original survives the styling) and styled (the
// rendered output differs from the stripped text and carries the level
// and status colors).
func TestRenderLogLine_HighlightsJSON(t *testing.T) {
	line := `{"level":"info","ts":1592833155.86084,"logger":"http.log.access","msg":"handled request","request":{"remote_ip":"127.0.0.1","remote_port":"50786","proto":"HTTP/2.0","method":"GET","host":"localhost","uri":"/"},"bytes_read":0,"user_id":"","duration":0.000571055,"size":1259,"status":200}`
	got := renderLogLineANSI(logs.Entry{Raw: []byte(line)}, 400)
	stripped := stripANSI(got)
	if stripped != line {
		t.Errorf("rendered line is not lossless:\n got %q\nwant %q", stripped, line)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected ANSI styling for a JSON line, got none:\n%s", got)
	}
	// info level (blue 33) and 2xx status (green 42) must be styled.
	if !strings.Contains(got, "38;5;33") {
		t.Errorf("info level must use the blue style (38;5;33):\n%s", got)
	}
	if !strings.Contains(got, "38;5;42") {
		t.Errorf("2xx status must use the green style (38;5;42):\n%s", got)
	}
}

// TestRenderLogLine_NonJSONVerbatim verifies a console-encoded line is
// rendered byte-for-byte with no ANSI codes.
func TestRenderLogLine_NonJSONVerbatim(t *testing.T) {
	line := "2026/08/08 12:00:00 INFO something happened"
	got := renderLogLineANSI(logs.Entry{Raw: []byte(line), Status: -1}, 200)
	if got != line {
		t.Errorf("non-JSON line = %q, want verbatim %q", got, line)
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("non-JSON line must not be styled, got:\n%s", got)
	}
}

// TestRenderLogLine_LevelAndStatusColors verifies the error level and 5xx
// status tokens are rendered in the error-red style.
func TestRenderLogLine_LevelAndStatusColors(t *testing.T) {
	errorLine := logs.Entry{Raw: []byte(`{"level":"error","msg":"boom"}`)}
	got := renderLogLineANSI(errorLine, 200)
	if !strings.Contains(got, "38;5;203") {
		t.Errorf("error level must use the red style (38;5;203):\n%s", got)
	}
	fivexxLine := logs.Entry{Raw: []byte(`{"level":"info","status":503}`)}
	got = renderLogLineANSI(fivexxLine, 200)
	if !strings.Contains(got, "38;5;203") {
		t.Errorf("5xx status must use the red style (38;5;203):\n%s", got)
	}
}

// TestRenderLogLine_Empty verifies an empty raw line renders empty.
func TestRenderLogLine_Empty(t *testing.T) {
	if got := renderLogLineANSI(logs.Entry{Raw: nil}, 200); got != "" {
		t.Errorf("empty line rendered %q, want empty", got)
	}
}

// TestRenderLogLine_TruncatesBeforeStyling verifies that a long JSON line
// is truncated on the PLAIN text before any ANSI styling: the visible
// (stripped) output must equal the plain truncated text, proving no escape
// sequence was split by the cut and no bytes were lost.
func TestRenderLogLine_TruncatesBeforeStyling(t *testing.T) {
	uri := strings.Repeat("a", 120)
	line := `{"level":"error","msg":"long request","request":{"method":"GET","uri":"/` + uri + `"},"status":503}`
	got := renderLogLineANSI(logs.Entry{Raw: []byte(line)}, 40)
	want := truncateToWidth(line, 40)
	if stripANSI(got) != want {
		t.Errorf("stripped output = %q, want the plain truncated text %q", stripANSI(got), want)
	}
}

// TestRenderLogLine_FitsStillHighlights verifies that a line within the
// width is returned highlighted: the output carries ANSI codes and the
// stripped text equals the untruncated raw line.
func TestRenderLogLine_FitsStillHighlights(t *testing.T) {
	line := `{"level":"warn","msg":"still styled"}`
	got := renderLogLineANSI(logs.Entry{Raw: []byte(line)}, 200)
	if got == stripANSI(got) {
		t.Errorf("fitted line must be styled, got no ANSI:\n%s", got)
	}
	if stripANSI(got) != line {
		t.Errorf("stripped output = %q, want %q", stripANSI(got), line)
	}
}
