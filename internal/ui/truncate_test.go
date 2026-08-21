package ui

import (
	"strings"
	"testing"
	"time"

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

func TestTruncateToWidth_WideRunesRespectBudget(t *testing.T) {
	// CJK runes occupy two cells each: the cut must leave room for the
	// ellipsis and never exceed the budget.
	s := strings.Repeat("日本語", 10)
	got := truncateToWidth(s, 7)
	if w := lipgloss.Width(got); w > 7 {
		t.Errorf("truncateToWidth(cjk, 7) = %q (width %d), want width <= 7", got, w)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncateToWidth(cjk, 7) = %q, want to end with '…'", got)
	}
}

func TestTruncateToWidth_ZeroWidthRunesKept(t *testing.T) {
	// A combining mark adds no cells: it belongs to the truncated prefix
	// whenever its base rune fits, exactly like the whole-string width
	// measurement reports.
	s := "e\u0301x\u0301ample text"
	got := truncateToWidth(s, 3)
	if w := lipgloss.Width(got); w > 3 {
		t.Errorf("truncateToWidth(%q, 3) = %q (width %d), want width <= 3", s, got, w)
	}
	if !strings.HasPrefix(got, "e\u0301") {
		t.Errorf("truncateToWidth(%q, 3) = %q, want the combining sequence kept intact", s, got)
	}
}

func TestTruncateToWidth_LongLineIsLinear(t *testing.T) {
	// A regression guard against the quadratic reverse scan: 100k cells
	// must truncate in bounded time. The old implementation needed tens
	// of seconds at this length; the single pass needs milliseconds.
	s := strings.Repeat("x", 100_000)
	start := time.Now()
	got := truncateToWidth(s, 80)
	elapsed := time.Since(start)
	if lipgloss.Width(got) != 80 {
		t.Errorf("truncateToWidth(long, 80) width = %d, want 80", lipgloss.Width(got))
	}
	if elapsed > 2*time.Second {
		t.Errorf("truncateToWidth on a 100k-cell line took %v; the scan is no longer linear", elapsed)
	}
}
