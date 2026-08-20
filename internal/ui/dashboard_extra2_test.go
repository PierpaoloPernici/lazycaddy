package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
	"github.com/PierpaoloPernici/lazycaddy/internal/tls"
)

func TestHealthCheckSummary_All(t *testing.T) {
	if got := healthCheckSummary(nil); got != "—" {
		t.Errorf("nil: got %q", got)
	}
	hc := &runtime.HealthCheck{Active: map[string]any{"uri": "/health"}}
	if got := healthCheckSummary(hc); got != "active health checks" {
		t.Errorf("active: got %q", got)
	}
	hc2 := &runtime.HealthCheck{Passive: map[string]any{"max_fails": 3}}
	if got := healthCheckSummary(hc2); got != "passive health checks" {
		t.Errorf("passive: got %q", got)
	}
	hc3 := &runtime.HealthCheck{Active: map[string]any{"a": 1}, Passive: map[string]any{"b": 2}}
	if got := healthCheckSummary(hc3); got != "active+passive health checks" {
		t.Errorf("both: got %q", got)
	}
	hc4 := &runtime.HealthCheck{}
	if got := healthCheckSummary(hc4); got != "custom (see raw JSON)" {
		t.Errorf("empty: got %q", got)
	}
}

func TestRuntimeStatusLabelAndBool(t *testing.T) {
	if got := runtimeStatusLabel(runtime.StatusRunning); got != "RUNNING" {
		t.Errorf("running: got %q", got)
	}
	if got := runtimeStatusLabel(runtime.StatusUnknown); got != "UNKNOWN" {
		t.Errorf("unknown: got %q", got)
	}
	if boolLabel(true) != "yes" || boolLabel(false) != "no" {
		t.Error("boolLabel")
	}
	if orDash("") != "—" || orDash("x") != "x" {
		t.Error("orDash")
	}
}

func TestRuntimeView_Title(t *testing.T) {
	m := &Model{runtimeProbed: true, runtimeReport: runtime.Report{Status: runtime.StatusRunning, Capabilities: runtime.Capabilities{Version: "v2.11.4"}}}
	view := m.runtimeView(120, 30)
	if view == "" {
		t.Error("runtimeView empty")
	}
	m2 := &Model{runtimeProbed: false}
	view2 := m2.runtimeView(120, 30)
	if view2 == "" {
		t.Error("runtimeView probing empty")
	}
	// Test with loading
	m3 := &Model{runtimeProbed: true, runtimeReport: runtime.Report{Status: runtime.StatusRunning}, runtimeConfigState: runtime.FetchLoading, runtimeConfigData: []byte{}}
	view3 := m3.runtimeView(120, 30)
	if view3 == "" {
		t.Error("runtimeView loading empty")
	}
}

func TestRevealRuntimeCursor(t *testing.T) {
	m := &Model{}
	m.runtimeViewport.Height = 10
	m.runtimeViewport.Width = 10
	m.runtimeViewport.SetContent("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n")
	m.runtimeCursor = 5
	m.revealRuntimeCursor()
	m.runtimeCursor = 0
	m.revealRuntimeCursor()
	m.runtimeCursor = 11
	m.revealRuntimeCursor()
}

func TestTLSView_Title(t *testing.T) {
	m := &Model{tlsState: tls.FetchLoading}
	view := m.tlsView(120, 30)
	if view == "" {
		t.Error("tlsView loading empty")
	}
	m2 := &Model{tlsState: tls.FetchAvailable, tlsCerts: []tls.Certificate{{Subject: "a", Issuer: "b"}}}
	view2 := m2.tlsView(120, 30)
	if view2 == "" {
		t.Error("tlsView available empty")
	}
	m3 := &Model{tlsState: tls.FetchUnavailable, tlsErr: nil}
	view3 := m3.tlsView(120, 30)
	if view3 == "" {
		t.Error("tlsView unavailable empty")
	}
}

func TestUpdateTLSKey_Navigation(t *testing.T) {
	m := &Model{tlsCerts: []tls.Certificate{{Subject: "a"}, {Subject: "b"}}, tlsState: tls.FetchAvailable}
	m.tlsViewport.Height = 10
	m.tlsViewport.Width = 10
	m.tlsViewport.SetContent("a\nb\nc\n")
	m.tlsCursor = 0
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
}
