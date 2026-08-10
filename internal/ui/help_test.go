package ui

import (
	"context"
	"errors"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	tea "github.com/charmbracelet/bubbletea"
)

func TestCaddyfileHelpOpensOfficialPage(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	var gotURL string
	m := newLoadedModel(t, fakeLoader{state: state})
	m.browser = app.BrowserFunc(func(_ context.Context, url string) error {
		gotURL = url
		return nil
	})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("help key did not return browser command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if gotURL != caddyfileHelpURL {
		t.Errorf("opened URL = %q, want %q", gotURL, caddyfileHelpURL)
	}
	if m.statusMessage != "✓ opened Caddyfile help in the browser" {
		t.Errorf("statusMessage = %q, want success", m.statusMessage)
	}
}

func TestCaddyfileHelpReportsBrowserFailure(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.browser = app.BrowserFunc(func(context.Context, string) error { return errors.New("opener failed") })
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	m = updated.(*Model)
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if m.statusMessage != "✗ could not open Caddyfile help: opener failed" {
		t.Errorf("statusMessage = %q, want browser error", m.statusMessage)
	}
}

func TestCaddyfileDirectiveHelpURLForGlobalOptions(t *testing.T) {
	if got := caddyfileDirectiveHelpURL(caddyfile.Node{Kind: caddyfile.KindGlobalOptions}, "log"); got != caddyfileOptionsHelpURL {
		t.Errorf("global options URL = %q, want %q", got, caddyfileOptionsHelpURL)
	}
	if got := caddyfileDirectiveHelpURL(caddyfile.Node{Kind: caddyfile.KindSite}, "reverse_proxy"); got != caddyfileHelpURL+"/directives/reverse_proxy" {
		t.Errorf("directive URL = %q, want the directives page", got)
	}
}

func TestStartHelpURLWithoutBrowser(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}) // m.browser stays nil
	updated, cmd := m.startHelpURL(caddyfileHelpURL, "Caddyfile help")
	if cmd != nil {
		t.Fatal("startHelpURL without a browser must not return a command")
	}
	m = updated.(*Model)
	if m.statusMessage != "✗ browser help unavailable on this host" {
		t.Errorf("statusMessage = %q, want the browser-unavailable message", m.statusMessage)
	}
}

func TestHandleBrowserResultDefaultsLabel(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	updated, _ := m.handleBrowserResult(browserResultMsg{err: nil, label: ""})
	m = updated.(*Model)
	if m.statusMessage != "✓ opened Caddyfile help in the browser" {
		t.Errorf("statusMessage = %q, want the default success message", m.statusMessage)
	}
}
