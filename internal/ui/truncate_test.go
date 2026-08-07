package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestTruncateToWidth_ShortReturnsUnchanged(t *testing.T) {
	s := "short"
	if got := truncateToWidth(s, 10); got != s {
		t.Errorf("truncateToWidth(%q, 10) = %q, want %q", s, got, s)
	}
}

func TestTruncateToWidth_ExactFitReturnsUnchanged(t *testing.T) {
	s := "exact"
	if got := truncateToWidth(s, 5); got != s {
		t.Errorf("truncateToWidth(%q, 5) = %q, want %q", s, got, s)
	}
}

func TestTruncateToWidth_TooLongTruncatesWithEllipsis(t *testing.T) {
	s := "this is a long message that should be truncated"
	got := truncateToWidth(s, 10)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncateToWidth(%q, 10) = %q, want to end with '…'", s, got)
	}
	if w := lipgloss.Width(got); w > 10 {
		t.Errorf("truncateToWidth(%q, 10) = %q (width %d), want width <= 10", s, got, w)
	}
}

func TestTruncateToWidth_ZeroMaxReturnsEmpty(t *testing.T) {
	if got := truncateToWidth("hello", 0); got != "" {
		t.Errorf("truncateToWidth(\"hello\", 0) = %q, want empty", got)
	}
}

func TestTruncateToWidth_NegativeMaxReturnsEmpty(t *testing.T) {
	if got := truncateToWidth("hello", -1); got != "" {
		t.Errorf("truncateToWidth(\"hello\", -1) = %q, want empty", got)
	}
}

func TestTruncateToWidth_SingleCellMax(t *testing.T) {
	// maxW=1 leaves no room for content + ellipsis: only the
	// ellipsis is returned so the caller's width budget is
	// respected.
	if got := truncateToWidth("hello", 1); got != "…" {
		t.Errorf("truncateToWidth(\"hello\", 1) = %q, want '…'", got)
	}
}

func TestTruncateToWidth_MultibyteSafe(t *testing.T) {
	// "héllo wörld" mixes 1- and 2-cell runes. The function must
	// cut on rune boundaries, never split a multi-byte sequence.
	s := "héllo wörld"
	got := truncateToWidth(s, 5)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncateToWidth(%q, 5) = %q, want to end with '…'", s, got)
	}
	if w := lipgloss.Width(got); w > 5 {
		t.Errorf("truncateToWidth(%q, 5) = %q (width %d), want width <= 5", s, got, w)
	}
}
