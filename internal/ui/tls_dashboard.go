package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/tls"
)

func (m *Model) tlsFetchCmd() tea.Cmd {
	fetcher := m.tlsFetcher
	if fetcher == nil {
		return func() tea.Msg {
			return tlsFetchResultMsg{Err: fmt.Errorf("TLS source not configured (use --tls-storage-dir)")}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		certs, err := fetcher.FetchCertificates(ctx)
		return tlsFetchResultMsg{Certs: certs, Err: err}
	}
}

func (m *Model) handleTLSFetchResult(msg tlsFetchResultMsg) (tea.Model, tea.Cmd) {
	m.tlsLoading = false
	m.tlsAt = time.Now()
	if msg.Err != nil {
		if m.tlsState == tls.FetchAvailable {
			m.tlsState = tls.FetchStale
		} else {
			m.tlsState = tls.FetchUnavailable
		}
		m.tlsErr = msg.Err
		m.recordError("fetch TLS certificates", msg.Err.Error(), "check --tls-storage-dir permissions and retry with r in the TLS dashboard")
	} else {
		m.tlsState = tls.FetchAvailable
		m.tlsCerts = msg.Certs
		m.tlsErr = nil
	}
	m.syncTLSViewport(m.width, m.paneHeight())
	return m, nil
}

func (m *Model) toggleTLSDashboard() (tea.Model, tea.Cmd) {
	if m.showTLS {
		m.showTLS = false
		m.clearTextSelection()
		return m, nil
	}
	m.clearTextSelection()
	m.showTLS = true
	m.showRuntime = false
	m.showLogs = false
	m.tlsCursor = 0
	m.tlsViewport.GotoTop()
	if m.tlsFetcher == nil {
		m.tlsState = tls.FetchUnavailable
		m.tlsErr = fmt.Errorf("TLS storage not configured")
		return m, nil
	}
	m.tlsState = tls.FetchLoading
	m.tlsLoading = true
	return m, m.tlsFetchCmd()
}

func (m *Model) refreshTLSDashboard() (tea.Model, tea.Cmd) {
	if !m.showTLS || m.tlsFetcher == nil {
		return m, nil
	}
	m.tlsState = tls.FetchLoading
	m.tlsLoading = true
	return m, m.tlsFetchCmd()
}

func (m *Model) updateTLSKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.showTLS = false
		m.clearTextSelection()
		return m, nil
	case "ctrl+c":
		return m.requestQuit()
	case "r", "R":
		return m.refreshTLSDashboard()
	case "up", "k":
		if m.tlsCursor > 0 {
			m.tlsCursor--
		}
		m.revealTLSCursor()
	case "down", "j":
		if m.tlsCursor < len(m.tlsCerts)-1 {
			m.tlsCursor++
		}
		m.revealTLSCursor()
	case "pgup":
		m.tlsViewport.PageUp()
	case "pgdown":
		m.tlsViewport.PageDown()
	case "y":
		return m.startCopy()
	}
	if dx, dy, ok := shiftSelectionDelta(msg); ok {
		m.shiftTextCursor(dx, dy)
	}
	return m, nil
}

func (m *Model) revealTLSCursor() {
	if m.tlsCursor < m.tlsViewport.YOffset {
		m.tlsViewport.SetYOffset(m.tlsCursor)
	} else if m.tlsCursor >= m.tlsViewport.YOffset+m.tlsViewport.Height {
		m.tlsViewport.SetYOffset(m.tlsCursor - m.tlsViewport.Height + 1)
	}
}

func (m *Model) tlsView(width, height int) string {
	title := "TLS"
	switch m.tlsState {
	case tls.FetchLoading:
		title += " · loading…"
	case tls.FetchAvailable:
		title += fmt.Sprintf(" · %d certificates", len(m.tlsCerts))
		if !m.tlsAt.IsZero() {
			title += " · " + m.tlsAt.Format(time.RFC3339)
		}
	case tls.FetchStale:
		title += " · STALE"
	case tls.FetchUnavailable:
		title += " · UNAVAILABLE"
	}
	if m.state != nil && m.state.Settings.TLSStorageDir != "" {
		title += " · " + m.state.Settings.TLSStorageDir
	}
	bodyH := height - 4
	if bodyH < 1 {
		bodyH = 1
	}
	paneContentW := width - 2
	if paneContentW < 1 {
		paneContentW = 1
	}
	m.syncTLSViewport(width, height)
	content := m.tlsViewport.View()
	if spans, ok := m.selectionSpans(textPaneTLS); ok {
		content = renderSelectionOverlay(content, m.tlsViewport.Width, m.tlsViewport.Height, spans)
	}
	return focusedPaneStyle.Width(paneContentW).Height(height).Render(activeTitleStyle.Render(title) + "\n\n" + content)
}

func (m *Model) syncTLSViewport(width, height int) {
	contentW := width - 4
	if contentW < 1 {
		contentW = 1
	}
	contentH := height - 4
	if contentH < 1 {
		contentH = 1
	}
	m.tlsViewport.Width = contentW
	m.tlsViewport.Height = contentH

	var b strings.Builder
	switch m.tlsState {
	case tls.FetchLoading:
		b.WriteString(dimStyle.Render("loading… — reading TLS storage"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("the panel stays responsive; browse the Caddyfile while it loads"))
	case tls.FetchAvailable:
		if len(m.tlsCerts) == 0 {
			b.WriteString(dimStyle.Render("no certificates found in TLS storage"))
			b.WriteString("\n")
			b.WriteString(dimStyle.Render("check the storage directory and cert issuer configuration"))
		} else {
			for i, c := range m.tlsCerts {
				marker := "  "
				if i == m.tlsCursor {
					marker = "› "
				}
				line := fmt.Sprintf("%s%s  issuer=%s  %s → %s  renewal=%s  ocsp=%s", marker, c.Subject, c.Issuer, c.NotBefore.Format("2006-01-02"), c.NotAfter.Format("2006-01-02"), c.RenewalState, c.OCSPState)
				if i == m.tlsCursor {
					line = cursorStyle.Render(line)
				}
				b.WriteString(line)
				b.WriteString("\n")
				b.WriteString(dimStyle.Render("  storage: " + c.StoragePath))
				if c.Locked {
					b.WriteString("  " + lipgloss.NewStyle().Foreground(warningColor).Render("[locked]"))
				}
				b.WriteString("\n")
			}
		}
	case tls.FetchStale:
		b.WriteString(errorStyle.Render("stale — " + m.tlsErr.Error()))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("previous certificates still shown; press r to retry"))
		if len(m.tlsCerts) > 0 {
			b.WriteString("\n")
			for _, c := range m.tlsCerts {
				b.WriteString(dimStyle.Render("  " + c.Subject + " → " + c.StoragePath))
				b.WriteString("\n")
			}
		}
	case tls.FetchUnavailable:
		if m.tlsErr != nil {
			b.WriteString(errorStyle.Render("unavailable — " + m.tlsErr.Error()))
		} else {
			b.WriteString(dimStyle.Render("unavailable — TLS storage not configured"))
		}
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("hint: --tls-storage-dir <path> (the CertMagic storage directory)"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("the panel never reads private key material; only .crt/.pem/.json are inspected"))
	default:
		b.WriteString(dimStyle.Render("unknown TLS state"))
	}
	m.tlsViewport.SetContent(b.String())
}
