package ui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

const caddyfileHelpURL = "https://caddyserver.com/docs/caddyfile"

func (m *Model) startCaddyfileHelp() (tea.Model, tea.Cmd) {
	if m.browser == nil {
		m.statusMessage = "✗ browser help unavailable on this host"
		return m, nil
	}
	browser := m.browser
	m.statusMessage = "opening Caddyfile help…"
	return m, func() tea.Msg {
		return browserResultMsg{err: browser.OpenURL(context.Background(), caddyfileHelpURL)}
	}
}

func (m *Model) handleBrowserResult(msg browserResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.statusMessage = "✗ could not open Caddyfile help: " + msg.err.Error()
		m.recordError("browser help", msg.err.Error(), "open "+caddyfileHelpURL+" manually")
		return m, nil
	}
	m.statusMessage = "✓ opened Caddyfile help in the browser"
	return m, nil
}
