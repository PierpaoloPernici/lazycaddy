package ui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

func parseLogFilter(query string) logs.Filter {
	query = strings.TrimSpace(query)
	if query == "" {
		return logs.Filter{}
	}
	var f logs.Filter
	parts := strings.Fields(query)
	var textParts []string
	for _, p := range parts {
		if idx := strings.IndexByte(p, '='); idx > 0 {
			key := strings.ToLower(p[:idx])
			val := p[idx+1:]
			switch key {
			case "host":
				f.Host = val
			case "status":
				if n, err := strconv.Atoi(val); err == nil {
					f.Status = n
				}
			case "class":
				// Allow "2", "2xx", "200" (class is 2..5).
				if strings.HasSuffix(val, "xx") {
					val = val[:len(val)-2]
				}
				if n, err := strconv.Atoi(val); err == nil {
					if n >= 1 && n <= 5 {
						f.Class = n
					} else if n >= 100 {
						f.Class = n / 100
					}
				}
			case "level":
				f.Level = val
			case "text":
				f.Text = val
			default:
				textParts = append(textParts, p)
			}
		} else if strings.Contains(p, ":") {
			// Support host:example.com syntax as well.
			kv := strings.SplitN(p, ":", 2)
			key := strings.ToLower(kv[0])
			val := kv[1]
			switch key {
			case "host":
				f.Host = val
			case "status":
				if n, err := strconv.Atoi(val); err == nil {
					f.Status = n
				}
			case "class":
				if strings.HasSuffix(val, "xx") {
					val = val[:len(val)-2]
				}
				if n, err := strconv.Atoi(val); err == nil {
					if n >= 1 && n <= 5 {
						f.Class = n
					} else if n >= 100 {
						f.Class = n / 100
					}
				}
			case "level":
				f.Level = val
			case "text":
				f.Text = val
			default:
				textParts = append(textParts, p)
			}
		} else {
			textParts = append(textParts, p)
		}
	}
	if f.Text == "" && len(textParts) > 0 {
		f.Text = strings.Join(textParts, " ")
	} else if len(textParts) > 0 && f.Text != "" {
		f.Text = f.Text + " " + strings.Join(textParts, " ")
	}
	return f
}

func (m *Model) filteredLogEntries() []logs.Entry {
	if !m.logFilterActive {
		return m.logLines
	}
	return logs.Apply(m.logLines, m.logFilter)
}

func (m *Model) logStatusCounts() logs.StatusCounts {
	return logs.CountStatusClasses(m.filteredLogEntries())
}

func (m *Model) logLatencyStats() logs.LatencyStats {
	return logs.SummarizeLatency(m.filteredLogEntries())
}

// showLogFilter is true while the filter input modal is open.
func (m *Model) openLogFilter() {
	m.showLogFilter = true
	m.logFilterQuery = []rune(m.logFilterText)
	m.logFilterCursor = len(m.logFilterQuery)
}

func (m *Model) closeLogFilter() {
	m.showLogFilter = false
}

func (m *Model) applyLogFilter() {
	query := strings.TrimSpace(string(m.logFilterQuery))
	m.logFilterText = query
	if query == "" {
		m.logFilterActive = false
		m.logFilter = logs.Filter{}
	} else {
		m.logFilter = parseLogFilter(query)
		m.logFilterActive = true
	}
	m.logCursor = 0
	m.logViewport.GotoTop()
	m.closeLogFilter()
}

func (m *Model) clearLogFilter() {
	m.logFilterActive = false
	m.logFilter = logs.Filter{}
	m.logFilterText = ""
	m.logFilterQuery = nil
	m.closeLogFilter()
	m.logCursor = 0
	m.logViewport.GotoTop()
}

// showLogFilter state is stored on Model (added below via patch).

func (m *Model) updateLogFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.closeLogFilter()
		return m, nil
	case "enter":
		m.applyLogFilter()
		return m, nil
	case "ctrl+c", "q":
		m.closeLogFilter()
		return m, nil
	case "backspace":
		if len(m.logFilterQuery) > 0 {
			m.logFilterQuery = m.logFilterQuery[:len(m.logFilterQuery)-1]
		}
	case "ctrl+u":
		m.logFilterQuery = nil
	default:
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
			m.logFilterQuery = append(m.logFilterQuery, msg.Runes...)
		}
	}
	return m, nil
}

func (m *Model) logFilterView(width, height int) string {
	boxW := width - 8
	if boxW < 44 {
		boxW = width - 2
	}
	if boxW > 78 {
		boxW = 78
	}
	if boxW < 1 {
		boxW = 1
	}
	boxH := 9
	if boxH > height-4 {
		boxH = height - 4
	}
	if boxH < 6 {
		boxH = 6
	}
	contentW := boxW - 4
	if contentW < 1 {
		contentW = 1
	}
	query := string(m.logFilterQuery)
	hint := dimStyle.Render("host=example.com status=200 class=2xx level=error text=foo")
	header := activeTitleStyle.Render("FILTER LOGS") + " " + dimStyle.Render("(Enter apply · Esc cancel · Ctrl-U clear)")
	input := cursorStyle.Render("> " + query + "▌")
	help := dimStyle.Render("examples: error  host:example.com  level:warn  status:500  class:2xx")
	content := strings.Join([]string{
		header,
		dimStyle.Render(strings.Repeat("─", contentW)),
		input,
		"",
		hint,
		help,
	}, "\n")
	return focusedPaneStyle.Width(boxW - 2).Height(boxH - 2).Render(content)
}

func (m *Model) logFilterOverlay(base string, width, height int) string {
	return m.modalOverlay(base, m.logFilterView(width, height), width, height)
}

// renderLogFilterBadge returns the filter badge for the log title.
func (m *Model) renderLogFilterBadge() string {
	if !m.logFilterActive {
		return ""
	}
	return lipgloss.NewStyle().Foreground(warningColor).Render(" · FILTER: " + m.logFilterText)
}
