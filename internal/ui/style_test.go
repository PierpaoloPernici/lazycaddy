package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestHeaderBadgeStylesUseThemeForegrounds(t *testing.T) {
	if got, want := sgrOf(writableBadge), sgrOf(lipgloss.NewStyle().Foreground(badgeSuccessForeground).Background(successColor).Bold(true).Padding(0, 1)); got != want {
		t.Fatalf("writable badge foreground differs from the semantic success theme: got %q want %q", got, want)
	}
	if got, want := sgrOf(loadedBadge), sgrOf(lipgloss.NewStyle().Foreground(badgeSuccessForeground).Background(successColor).Bold(true).Padding(0, 1)); got != want {
		t.Fatalf("loaded badge foreground differs from the semantic success theme: got %q want %q", got, want)
	}
	if strings.Contains(sgrOf(writableBadge), "38;5;15") {
		t.Fatal("writable badge still uses the low-contrast fixed ANSI-15 foreground")
	}
}

func TestPersistentChromeSharesPaletteSurface(t *testing.T) {
	if chromeBackground != paletteBackground {
		t.Fatalf("chrome background = %#v, want the command-palette surface %#v", chromeBackground, paletteBackground)
	}
	if statusBackground == paletteBackground {
		t.Fatal("status background should remain a distinct transient surface")
	}
}

// sgrOf returns the first SGR escape sequence that style emits for a probe
// character, so tests can assert against the current theme instead of magic
// ANSI codes. It forces the same ANSI-256 profile used by the other render
// helpers and restores the terminal-agnostic profile afterwards.
func sgrOf(style lipgloss.Style) string {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	seq := style.Render("x")
	i := strings.Index(seq, "\x1b[")
	if i < 0 {
		return ""
	}
	j := strings.Index(seq[i:], "m") + i
	if j < i {
		return ""
	}
	return seq[i : j+1]
}
