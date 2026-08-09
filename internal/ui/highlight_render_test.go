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
	return renderWithANSISelected(src, 0, 0)
}

func renderWithANSISelected(src []byte, startLine, endLine int) string {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	return highlightSource(src, startLine, endLine)
}

var gutterRe = regexp.MustCompile(`^\s*\d+[│▎] `)

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
	if !strings.Contains(got, "\x1b[") || !strings.Contains(got, sgrOf(syntaxCommentStyle)) {
		t.Errorf("comment must be styled with the theme comment style, got:\n%s", got)
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
	if !strings.Contains(got, sgrOf(syntaxPlaceholderStyle)) {
		t.Errorf("placeholder must use the theme accent style, got:\n%s", got)
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

// TestHighlightSourceSelectedRange verifies that the gutter for lines inside
// the selected range carries the selection marker and styling, while the
// rest of the gutter stays plain.
func TestHighlightSourceSelectedRange(t *testing.T) {
	src := []byte("a.example.test {\n\trespond a\n}\nb.example.test {\n\trespond b\n}\n")
	got := renderWithANSISelected(src, 4, 6)
	assertSourceLossless(t, src, got)

	lines := strings.Split(got, "\n")
	// Drop the trailing empty line produced by strings.Split on a final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lineNo := i + 1
		stripped := stripANSI(line)
		hasBar := strings.Contains(stripped, "▎")
		if lineNo >= 4 && lineNo <= 6 {
			if !hasBar {
				t.Errorf("line %d inside the selected range missing the selection bar:\n%s", lineNo, line)
			}
			if !strings.Contains(line, "\x1b[") {
				t.Errorf("line %d inside the selected range must be styled, got plain:\n%s", lineNo, line)
			}
		} else {
			if hasBar {
				t.Errorf("line %d outside the selected range must not contain the selection bar:\n%s", lineNo, line)
			}
		}
	}
}

// TestHighlightSourceSelectedRangeClamped verifies ranges that extend past
// the end of the file only mark the lines that actually exist.
func TestHighlightSourceSelectedRangeClamped(t *testing.T) {
	src := []byte("example.test {\n\trespond ok\n}\n")
	got := renderWithANSISelected(src, 2, 100)
	assertSourceLossless(t, src, got)

	lines := strings.Split(stripANSI(got), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lineNo := i + 1
		hasBar := strings.Contains(line, "▎")
		switch {
		case lineNo >= 2 && lineNo <= len(lines):
			if !hasBar {
				t.Errorf("line %d should carry the selection bar:\n%s", lineNo, line)
			}
		case lineNo == 1:
			if hasBar {
				t.Errorf("line %d must not carry the selection bar:\n%s", lineNo, line)
			}
		}
	}
}

// TestHighlightSourceNoSelection verifies that a zero range leaves every
// gutter plain, matching the original behavior.
func TestHighlightSourceNoSelection(t *testing.T) {
	src := []byte("example.test {\n\trespond ok\n}\n")
	got := renderWithANSISelected(src, 0, 0)
	assertSourceLossless(t, src, got)
	if strings.Contains(stripANSI(got), "▎") {
		t.Errorf("no-selection render must not contain the selection bar:\n%s", got)
	}
}

// renderLogDetailANSI renders one entry's detail with ANSI output forced
// on, then restores the terminal-agnostic profile, mirroring renderWithANSI.
func renderLogDetailANSI(entry logs.Entry, maxW int) []string {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	return renderLogDetail(entry, maxW)
}

// accessLogSample is a realistic Caddy access log JSON line used by the
// detail renderer tests.
const accessLogSample = `{"level":"info","ts":1592833155.86084,"logger":"http.log.access","msg":"handled request","request":{"remote_ip":"127.0.0.1","remote_port":"50786","proto":"HTTP/2.0","method":"GET","host":"localhost","uri":"/api/config"},"bytes_read":0,"user_id":"","duration":0.000571055,"size":1259,"status":200}`

// TestRenderLogDetail_LosslessJSON verifies that wrapping a narrow JSON
// line into multiple visual lines loses no bytes: concatenating the
// rendered lines reproduces the raw line exactly (modulo ANSI).
func TestRenderLogDetail_LosslessJSON(t *testing.T) {
	entry := logs.Entry{Raw: []byte(accessLogSample), Parsed: true, Status: 200}
	lines := renderLogDetailANSI(entry, 30)
	if len(lines) <= 1 {
		t.Fatalf("got %d lines at width 30, want the line wrapped into several", len(lines))
	}
	if got := stripANSI(strings.Join(lines, "")); got != accessLogSample {
		t.Errorf("concatenated detail is not lossless:\n got %q\nwant %q", got, accessLogSample)
	}
}

// TestRenderLogDetail_Highlighted verifies that a line within the width is
// returned as one highlighted line with ANSI styling and no data loss.
func TestRenderLogDetail_Highlighted(t *testing.T) {
	entry := logs.Entry{Raw: []byte(accessLogSample), Parsed: true, Status: 200}
	lines := renderLogDetailANSI(entry, 400)
	if len(lines) != 1 {
		t.Fatalf("got %d lines at width 400, want 1", len(lines))
	}
	got := lines[0]
	if got == stripANSI(got) {
		t.Errorf("fitted detail must be styled, got no ANSI:\n%s", got)
	}
	if stripANSI(got) != accessLogSample {
		t.Errorf("stripped detail = %q, want the raw line", stripANSI(got))
	}
}

// TestRenderLogDetail_NonJSONVerbatim verifies a non-JSON entry is wrapped
// and returned without any ANSI styling, byte-for-byte.
func TestRenderLogDetail_NonJSONVerbatim(t *testing.T) {
	raw := "2026/08/08 12:00:00 INFO something happened in the access log"
	entry := logs.Entry{Raw: []byte(raw), Status: -1} // Parsed false
	lines := renderLogDetailANSI(entry, 20)
	if len(lines) <= 1 {
		t.Fatalf("got %d lines at width 20, want the line wrapped", len(lines))
	}
	if got := stripANSI(strings.Join(lines, "")); got != raw {
		t.Errorf("concatenated detail = %q, want %q", got, raw)
	}
	for _, line := range lines {
		if strings.Contains(line, "\x1b[") {
			t.Errorf("non-JSON detail line must not be styled, got:\n%s", line)
		}
	}
}

// TestRenderLogDetail_Empty verifies an empty raw line renders nil.
func TestRenderLogDetail_Empty(t *testing.T) {
	if got := renderLogDetailANSI(logs.Entry{Raw: nil}, 200); got != nil {
		t.Errorf("empty detail rendered %v, want nil", got)
	}
}
