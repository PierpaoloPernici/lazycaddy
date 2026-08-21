package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
)

// runtimeFetchCmd returns a tea.Cmd that fetches the loaded config through
// the injected ConfigFetcher. The fetcher respects the caller's context so
// a dashboard close can cancel the request without blocking the TUI.
func (m *Model) runtimeConfigFetchCmd() tea.Cmd {
	fetcher := m.configFetcher
	gen := m.runtimeConfigGen
	if fetcher == nil {
		return func() tea.Msg {
			return configFetchResultMsg{Err: fmt.Errorf("config fetcher unavailable"), Gen: gen}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		data, err := fetcher.FetchConfig(ctx)
		return configFetchResultMsg{Data: data, Err: err, Gen: gen}
	}
}

func (m *Model) runtimeUpstreamFetchCmd() tea.Cmd {
	fetcher := m.upstreamFetcher
	gen := m.runtimeUpstreamGen
	if fetcher == nil {
		return func() tea.Msg {
			return upstreamFetchResultMsg{Err: fmt.Errorf("upstream fetcher unavailable"), Gen: gen}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ups, err := fetcher.FetchUpstreams(ctx)
		return upstreamFetchResultMsg{Upstreams: ups, Err: err, Gen: gen}
	}
}

func (m *Model) handleConfigFetchResult(msg configFetchResultMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.runtimeConfigGen {
		return m, nil
	}
	m.runtimeConfigLoading = false
	m.runtimeConfigAt = time.Now()
	if msg.Err != nil {
		if m.runtimeConfigHasFetched {
			m.runtimeConfigState = runtime.FetchStale
		} else {
			m.runtimeConfigState = runtime.FetchUnavailable
		}
		m.runtimeConfigErr = msg.Err
		m.recordError("fetch loaded config", msg.Err.Error(), "check Admin API --admin-endpoint and retry with r in the runtime dashboard")
	} else {
		m.runtimeConfigState = runtime.FetchAvailable
		m.runtimeConfigData = msg.Data
		m.runtimeConfigErr = nil
		m.runtimeConfigHasFetched = true
	}
	m.syncRuntimeViewport(m.width, m.paneHeight())
	return m, nil
}

func (m *Model) handleUpstreamFetchResult(msg upstreamFetchResultMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.runtimeUpstreamGen {
		return m, nil
	}
	m.runtimeUpstreamLoading = false
	m.runtimeUpstreamAt = time.Now()
	if msg.Err != nil {
		if m.runtimeUpstreamHasFetched {
			m.runtimeUpstreamState = runtime.FetchStale
		} else {
			m.runtimeUpstreamState = runtime.FetchUnavailable
		}
		m.runtimeUpstreamErr = msg.Err
		m.recordError("fetch upstreams", msg.Err.Error(), "check Admin API and reload")
	} else {
		m.runtimeUpstreamState = runtime.FetchAvailable
		m.runtimeUpstreams = msg.Upstreams
		m.runtimeUpstreamErr = nil
		m.runtimeUpstreamHasFetched = true
	}
	m.syncRuntimeViewport(m.width, m.paneHeight())
	return m, nil
}

// toggleRuntimeDashboard opens or closes the runtime dashboard. Opening
// triggers cancellable fetches for the loaded config and upstreams; each
// panel fetches independently so a failure in one never blocks the
// others. Closing cancels no in-flight fetch (the result is ignored) but
// drops the view immediately so browsing stays responsive.
func (m *Model) toggleRuntimeDashboard() (tea.Model, tea.Cmd) {
	if m.showRuntime {
		m.showRuntime = false
		m.clearTextSelection()
		m.runtimeConfigGen++
		m.runtimeUpstreamGen++
		return m, nil
	}
	m.clearTextSelection()
	m.showRuntime = true
	if m.showTLS {
		m.tlsGen++
	}
	m.showTLS = false
	m.showLogs = false
	m.runtimeCursor = 0
	m.runtimeViewport.GotoTop()

	var cmds []tea.Cmd
	if m.configFetcher != nil {
		m.runtimeConfigGen++
		m.runtimeConfigState = runtime.FetchLoading
		m.runtimeConfigLoading = true
		cmds = append(cmds, m.runtimeConfigFetchCmd())
	} else {
		m.runtimeConfigState = runtime.FetchUnavailable
		m.runtimeConfigErr = errors.New("admin API not configured")
	}
	if m.upstreamFetcher != nil {
		m.runtimeUpstreamGen++
		m.runtimeUpstreamState = runtime.FetchLoading
		m.runtimeUpstreamLoading = true
		cmds = append(cmds, m.runtimeUpstreamFetchCmd())
	} else {
		m.runtimeUpstreamState = runtime.FetchUnavailable
		m.runtimeUpstreamErr = fmt.Errorf("upstream fetcher not configured")
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) refreshRuntimeDashboard() (tea.Model, tea.Cmd) {
	if !m.showRuntime {
		return m, nil
	}
	m.runtimeConfigGen++
	m.runtimeUpstreamGen++
	m.runtimeConfigState = runtime.FetchLoading
	m.runtimeConfigLoading = true
	m.runtimeUpstreamState = runtime.FetchLoading
	m.runtimeUpstreamLoading = true
	return m, tea.Batch(m.runtimeConfigFetchCmd(), m.runtimeUpstreamFetchCmd())
}

func (m *Model) updateRuntimeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "I":
		m.showRuntime = false
		m.clearTextSelection()
		m.runtimeConfigGen++
		m.runtimeUpstreamGen++
		return m, nil
	case "T":
		return m.toggleTLSDashboard()
	case "esc", "q":
		m.showRuntime = false
		m.clearTextSelection()
		m.runtimeConfigGen++
		m.runtimeUpstreamGen++
		return m, nil
	case "ctrl+c":
		return m.requestQuit()
	case "r", "R":
		return m.refreshRuntimeDashboard()
	case "up", "k":
		if m.runtimeCursor > 0 {
			m.runtimeCursor--
		}
		m.revealRuntimeCursor()
	case "down", "j":
		m.runtimeCursor++
		// Clamp to content height; the viewport handles overflow.
		max := m.runtimeLineCount() - 1
		if m.runtimeCursor > max {
			m.runtimeCursor = max
		}
		if m.runtimeCursor < 0 {
			m.runtimeCursor = 0
		}
		m.revealRuntimeCursor()
	case "pgup":
		m.runtimeViewport.PageUp()
	case "pgdown":
		m.runtimeViewport.PageDown()
	case "y":
		return m.startCopy()
	}
	if dx, dy, ok := shiftSelectionDelta(msg); ok {
		m.shiftTextCursor(dx, dy)
	}
	return m, nil
}

func (m *Model) revealRuntimeCursor() {
	if m.runtimeCursor < m.runtimeViewport.YOffset {
		m.runtimeViewport.SetYOffset(m.runtimeCursor)
	} else if m.runtimeCursor >= m.runtimeViewport.YOffset+m.runtimeViewport.Height {
		m.runtimeViewport.SetYOffset(m.runtimeCursor - m.runtimeViewport.Height + 1)
	}
}

func (m *Model) runtimeLineCount() int {
	// Count the selectable upstream rows, not the visible viewport
	// lines. The viewport View() only returns the visible slice, so
	// counting it would make deep lists unreachable.
	if len(m.runtimeUpstreams) == 0 {
		return 1
	}
	return len(m.runtimeUpstreams)
}

func (m *Model) runtimeView(width, height int) string {
	title := "Runtime"
	if m.runtimeProbed {
		switch m.runtimeReport.Status {
		case runtime.StatusRunning:
			title += " · RUNNING"
			if m.runtimeReport.Capabilities.Version != "" {
				title += " " + m.runtimeReport.Capabilities.Version
			}
		case runtime.StatusStopped:
			title += " · STOPPED"
		case runtime.StatusUnreachable:
			title += " · UNREACHABLE"
		case runtime.StatusUnknown:
			title += " · UNKNOWN"
		}
	} else {
		title += " · probing…"
	}
	if len(m.runtimeConfigData) == 0 && m.runtimeConfigState == runtime.FetchLoading {
		title += " · loading config…"
	}
	bodyH := height - 4
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	m.syncRuntimeViewport(width, height)
	content := m.runtimeViewport.View()
	if spans, ok := m.selectionSpans(textPaneRuntime); ok {
		content = renderSelectionOverlay(content, m.runtimeViewport.Width, m.runtimeViewport.Height, spans)
	}
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(activeTitleStyle.Render(title) + "\n\n" + content)
}

func (m *Model) syncRuntimeViewport(width, height int) {
	contentW := width - 4
	if contentW < 1 {
		contentW = 1
	}
	contentH := height - 4
	if contentH < 1 {
		contentH = 1
	}
	m.runtimeViewport.Width = contentW
	m.runtimeViewport.Height = contentH

	var b strings.Builder
	// Section 1: Runtime probe (always available, even when Admin API is down).
	b.WriteString(sectionTitle("Caddy runtime"))
	b.WriteString("\n")
	if !m.runtimeProbed {
		b.WriteString(dimStyle.Render("probing… — the runtime probe runs at startup with a 5s timeout"))
		b.WriteString("\n")
	} else {
		b.WriteString(fmt.Sprintf("Status       %s\n", runtimeStatusLabel(m.runtimeReport.Status)))
		b.WriteString(fmt.Sprintf("Version      %s\n", orDash(m.runtimeReport.Capabilities.Version)))
		b.WriteString(fmt.Sprintf("Binary       %s\n", boolLabel(m.runtimeReport.Capabilities.Binary)))
		b.WriteString(fmt.Sprintf("Admin API    %s\n", boolLabel(m.runtimeReport.Capabilities.AdminAPI)))
		b.WriteString(fmt.Sprintf("Readable     %s\n", boolLabel(m.runtimeReport.Capabilities.Readable)))
		b.WriteString(fmt.Sprintf("Reload       %s\n", boolLabel(m.runtimeReport.Capabilities.Reload)))
		b.WriteString(fmt.Sprintf("Writable     %s\n", boolLabel(m.runtimeReport.Capabilities.Writable)))
	}
	b.WriteString("\n")

	// Section 2: Loaded config (cancellable Admin API GET /config/).
	b.WriteString(sectionTitle("Loaded config  " + fetchBadge(m.runtimeConfigState, m.runtimeConfigLoading)))
	b.WriteString("\n")
	switch m.runtimeConfigState {
	case runtime.FetchLoading:
		b.WriteString(dimStyle.Render("loading… — GET /config/"))
	case runtime.FetchAvailable:
		b.WriteString(fmt.Sprintf("Endpoint     %s\n", orDash(m.state.Settings.AdminEndpoint)))
		b.WriteString(fmt.Sprintf("Fetched      %s\n", m.runtimeConfigAt.Format(time.RFC3339)))
		b.WriteString(fmt.Sprintf("Size         %d bytes\n", len(m.runtimeConfigData)))
		// Show a pretty preview of the top-level keys without dumping the whole JSON.
		if len(m.runtimeConfigData) > 0 {
			var top map[string]json.RawMessage
			if err := json.Unmarshal(m.runtimeConfigData, &top); err == nil {
				b.WriteString("Top-level keys  ")
				keys := make([]string, 0, len(top))
				for k := range top {
					keys = append(keys, k)
				}
				b.WriteString(strings.Join(keys, ", "))
				b.WriteString("\n")
			}
		}
		b.WriteString(dimStyle.Render("(full JSON is available via the Admin API; lazycaddy never regenerates a Caddyfile)"))
	case runtime.FetchStale:
		b.WriteString(errorStyle.Render("stale — last fetch failed, showing previous data"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(m.runtimeConfigErr.Error()))
	case runtime.FetchUnavailable:
		if m.runtimeConfigErr != nil {
			b.WriteString(errorStyle.Render("unavailable — " + m.runtimeConfigErr.Error()))
		} else {
			b.WriteString(dimStyle.Render("unavailable — no loaded config fetched yet"))
		}
		if m.configFetcher == nil {
			b.WriteString("\n")
			b.WriteString(dimStyle.Render("hint: --admin-endpoint http://localhost:2019"))
		}
	}
	b.WriteString("\n\n")

	// Section 3: Upstreams (derived from loaded config).
	b.WriteString(sectionTitle("Upstreams  " + fetchBadge(m.runtimeUpstreamState, m.runtimeUpstreamLoading)))
	b.WriteString("\n")
	switch m.runtimeUpstreamState {
	case runtime.FetchLoading:
		b.WriteString(dimStyle.Render("loading… — parsing upstreams from loaded config"))
	case runtime.FetchAvailable:
		if len(m.runtimeUpstreams) == 0 {
			b.WriteString(dimStyle.Render("no reverse_proxy upstreams in the loaded config"))
		} else {
			for i, u := range m.runtimeUpstreams {
				marker := "  "
				if i == m.runtimeCursor {
					marker = "› "
				}
				line := fmt.Sprintf("%s%s  server=%s", marker, u.Address, u.Server)
				if i == m.runtimeCursor {
					line = cursorStyle.Render(line)
				}
				b.WriteString(line)
				b.WriteString("\n")
				if u.Live != nil {
					b.WriteString(dimStyle.Render("  live: " + liveSummary(u.Live)))
					b.WriteString("\n")
				}
				if u.HealthCheck != nil {
					b.WriteString(dimStyle.Render("  health: " + healthCheckSummary(u.HealthCheck)))
					b.WriteString("\n")
				} else {
					b.WriteString(dimStyle.Render("  health: defaults (no explicit health_checks)"))
					b.WriteString("\n")
				}
			}
			b.WriteString(dimStyle.Render("upstream health is observed runtime state (fails/active/healthy), not a generic ping"))
		}
	case runtime.FetchStale:
		b.WriteString(errorStyle.Render("stale — " + m.runtimeUpstreamErr.Error()))
	case runtime.FetchUnavailable:
		if m.runtimeUpstreamErr != nil {
			b.WriteString(errorStyle.Render("unavailable — " + m.runtimeUpstreamErr.Error()))
		} else {
			b.WriteString(dimStyle.Render("unavailable — no upstream data"))
		}
	}
	m.runtimeViewport.SetContent(b.String())
}

func sectionTitle(s string) string {
	return activeTitleStyle.Render(s)
}

func fetchBadge(s runtime.FetchState, loading bool) string {
	if loading {
		return dimStyle.Render("[loading]")
	}
	switch s {
	case runtime.FetchAvailable:
		return lipgloss.NewStyle().Foreground(successColor).Render("[available]")
	case runtime.FetchStale:
		return lipgloss.NewStyle().Foreground(warningColor).Render("[stale]")
	case runtime.FetchUnavailable:
		return lipgloss.NewStyle().Foreground(errorColor).Render("[unavailable]")
	default:
		return dimStyle.Render("[unknown]")
	}
}

func runtimeStatusLabel(s runtime.Status) string {
	switch s {
	case runtime.StatusRunning:
		return "RUNNING"
	case runtime.StatusStopped:
		return "STOPPED"
	case runtime.StatusUnreachable:
		return "UNREACHABLE"
	default:
		return "UNKNOWN"
	}
}

func boolLabel(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func healthCheckSummary(hc *runtime.HealthCheck) string {
	if hc == nil {
		return "—"
	}
	parts := []string{}
	if len(hc.Active) > 0 {
		parts = append(parts, "active")
	}
	if len(hc.Passive) > 0 {
		parts = append(parts, "passive")
	}
	if len(parts) == 0 {
		return "custom (see raw JSON)"
	}
	return strings.Join(parts, "+") + " health checks"
}

func liveSummary(l *runtime.UpstreamLive) string {
	if l == nil {
		return "—"
	}
	parts := []string{}
	if l.Healthy != nil {
		if *l.Healthy {
			parts = append(parts, "healthy")
		} else {
			parts = append(parts, "unhealthy")
		}
	}
	if l.Fails != nil {
		parts = append(parts, fmt.Sprintf("fails=%d", *l.Fails))
	}
	if l.Active != nil {
		parts = append(parts, fmt.Sprintf("active=%d", *l.Active))
	}
	if l.Available != nil {
		if *l.Available {
			parts = append(parts, "available")
		} else {
			parts = append(parts, "unavailable")
		}
	}
	if len(parts) == 0 {
		return "live"
	}
	return strings.Join(parts, " ")
}
