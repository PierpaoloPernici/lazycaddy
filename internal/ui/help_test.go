package ui

import (
	"context"
	"errors"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
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
