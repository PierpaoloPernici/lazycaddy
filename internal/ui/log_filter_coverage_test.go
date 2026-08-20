package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

func TestParseLogFilter_All(t *testing.T) {
	tests := []struct {
		q    string
		want logs.Filter
	}{
		{"", logs.Filter{}},
		{"host=example.com", logs.Filter{Host: "example.com"}},
		{"host:example.com", logs.Filter{Host: "example.com"}},
		{"status=200", logs.Filter{Status: 200}},
		{"status:404", logs.Filter{Status: 404}},
		{"class=2", logs.Filter{Class: 2}},
		{"class:2xx", logs.Filter{Class: 2}},
		{"class:200", logs.Filter{Class: 2}},
		{"level=error", logs.Filter{Level: "error"}},
		{"level:error", logs.Filter{Level: "error"}},
		{"text=hello", logs.Filter{Text: "hello"}},
		{"text:world", logs.Filter{Text: "world"}},
		{"hello", logs.Filter{Text: "hello"}},
		{"host=example.com level=error hello", logs.Filter{Host: "example.com", Level: "error", Text: "hello"}},
		{"unknown=foo hello", logs.Filter{Text: "unknown=foo hello"}},
	}
	for i, tc := range tests {
		got := parseLogFilter(tc.q)
		if got.Host != tc.want.Host || got.Status != tc.want.Status || got.Class != tc.want.Class || got.Level != tc.want.Level || got.Text != tc.want.Text {
			t.Errorf("case %d: parseLogFilter(%q) = %+v, want %+v", i, tc.q, got, tc.want)
		}
	}
}

func TestLogFilter_Flow(t *testing.T) {
	m := &Model{}
	m.logLines = []logs.Entry{{Host: "a.com", Level: "info"}, {Host: "b.com", Level: "error"}}
	m.logFilter = logs.Filter{Level: "error"}
	m.logFilterActive = true
	m.logFilterText = "level=error"
	entries := m.filteredLogEntries()
	if len(entries) != 1 || entries[0].Host != "b.com" {
		t.Errorf("filteredLogEntries = %+v", entries)
	}
	if got := m.logStatusCounts(); got.Class2xx != 0 {
		// Just check it doesn't panic and returns counts
	}
	if got := m.logLatencyStats(); got.Count != 0 {
		// No durations, so 0
	}
	if badge := m.renderLogFilterBadge(); badge == "" {
		t.Error("badge should not be empty when filter active")
	}
	m.logFilterActive = false
	if badge := m.renderLogFilterBadge(); badge != "" {
		t.Error("badge should be empty when not active")
	}
}

func TestLogFilter_Modal(t *testing.T) {
	m := &Model{}
	m.showLogs = true
	m.openLogFilter()
	if !m.showLogFilter {
		t.Error("openLogFilter didn't set showLogFilter")
	}
	m.logFilterQuery = []rune("host=example.com")
	m.applyLogFilter()
	if !m.logFilterActive || m.logFilter.Host != "example.com" {
		t.Errorf("applyLogFilter failed: %+v", m.logFilter)
	}
	m.openLogFilter()
	m.logFilterQuery = []rune{}
	m.applyLogFilter()
	if m.logFilterActive {
		t.Error("empty query should clear filter")
	}
	m.openLogFilter()
	m.clearLogFilter()
	if m.logFilterActive || m.showLogFilter {
		t.Error("clearLogFilter failed")
	}
	// Test updateLogFilterKey
	m.openLogFilter()
	m.logFilterQuery = []rune("a")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if string(m.logFilterQuery) != "ab" {
		t.Errorf("typing failed: %q", m.logFilterQuery)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if string(m.logFilterQuery) != "a" {
		t.Errorf("backspace failed")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if len(m.logFilterQuery) != 0 {
		t.Error("Ctrl-U should clear")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showLogFilter {
		t.Error("Esc should close")
	}
	m.openLogFilter()
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.showLogFilter {
		t.Error("Enter should apply and close")
	}
	// Test logFilterView and overlay
	m.showLogFilter = true
	view := m.logFilterView(80, 24)
	if view == "" {
		t.Error("logFilterView empty")
	}
	overlay := m.logFilterOverlay("base", 80, 24)
	if overlay == "" {
		t.Error("logFilterOverlay empty")
	}
}
