package ui

import (
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/tls"
)

func TestTLSGeometries(t *testing.T) {
	m := &Model{}
	m.width = 120
	m.height = 30
	geo := m.tlsPaneGeometry()
	if geo.width < 1 || geo.height < 1 {
		t.Error("tlsPaneGeometry invalid")
	}
	pane := m.tlsTextPane()
	if pane == nil {
		t.Error("tlsTextPane nil")
	}
	// Also test runtime pane geometry
	geo2 := m.runtimePaneGeometry()
	if geo2.width < 1 {
		t.Error("runtimePaneGeometry invalid")
	}
	pane2 := m.runtimeTextPane()
	if pane2 == nil {
		t.Error("runtimeTextPane nil")
	}
}

func TestUpdateTLSKey_Extra(t *testing.T) {
	m := &Model{tlsCerts: []tls.Certificate{{Subject: "a"}, {Subject: "b"}}, tlsState: tls.FetchAvailable}
	m.tlsViewport.Height = 10
	m.tlsViewport.Width = 10
	m.tlsViewport.SetContent("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\n")
	m.tlsCursor = 5
	m.revealTLSCursor()
	m.tlsCursor = 0
	m.revealTLSCursor()
	m.tlsCursor = 2
	m.revealTLSCursor()
}
