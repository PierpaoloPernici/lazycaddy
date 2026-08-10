package ui

import (
	"context"
	"net/url"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	tea "github.com/charmbracelet/bubbletea"
)

const caddyfileHelpURL = "https://caddyserver.com/docs/caddyfile"
const caddyfileOptionsHelpURL = caddyfileHelpURL + "/options"

func caddyfileDirectiveHelpURL(parent caddyfile.Node, name string) string {
	if parent.Kind == caddyfile.KindGlobalOptions {
		return caddyfileOptionsHelpURL
	}
	return caddyfileHelpURL + "/directives/" + url.PathEscape(name)
}

func (m *Model) startCaddyfileHelp() (tea.Model, tea.Cmd) {
	return m.startHelpURL(caddyfileHelpURL, "Caddyfile help")
}

func (m *Model) startHelpURL(rawURL, label string) (tea.Model, tea.Cmd) {
	if m.browser == nil {
		m.statusMessage = "✗ browser help unavailable on this host"
		return m, nil
	}
	browser := m.browser
	m.statusMessage = "opening " + label + "…"
	return m, func() tea.Msg {
		return browserResultMsg{err: browser.OpenURL(context.Background(), rawURL), label: label}
	}
}

func (m *Model) handleBrowserResult(msg browserResultMsg) (tea.Model, tea.Cmd) {
	label := msg.label
	if label == "" {
		label = "Caddyfile help"
	}
	if msg.err != nil {
		m.statusMessage = "✗ could not open " + label + ": " + msg.err.Error()
		m.recordError("browser help", msg.err.Error(), "open the Caddy documentation manually")
		return m, nil
	}
	m.statusMessage = "✓ opened " + label + " in the browser"
	return m, nil
}
