package ui

import "github.com/charmbracelet/lipgloss"

// truncateToWidth returns s truncated to fit within maxW cells, with
// an ellipsis appended when truncation occurs. The input is treated
// as plain text: the caller is responsible for applying any ANSI
// styles after the truncation, so a leading color code can never be
// split by a mid-string cut. The scan is rune-aware so multi-byte
// characters (and emoji) are not split.
//
// A non-positive maxW returns an empty string. A maxW of 1 returns
// only the ellipsis (no room for content). A string that already
// fits is returned unchanged.
func truncateToWidth(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	target := maxW - 1
	if target <= 0 {
		return "…"
	}
	runes := []rune(s)
	for i := len(runes); i > 0; i-- {
		if lipgloss.Width(string(runes[:i])) <= target {
			return string(runes[:i]) + "…"
		}
	}
	return "…"
}
