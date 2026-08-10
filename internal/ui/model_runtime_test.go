package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
)

// TestModelInit_RuntimeProbeCmd verifies that Init returns a startup
// command exactly when a runtime probe is configured, and that the
// command delivers a runtimeProbeResultMsg carrying the probe report.
func TestModelInit_RuntimeProbeCmd(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	// Without a probe there is no startup command.
	m := newLoadedModel(t, fakeLoader{state: state})
	if cmd := m.Init(); cmd != nil {
		t.Errorf("Init without a probe returned a command, want nil")
	}
	// With a probe, Init returns a command that reports the probe result.
	report := runtime.Report{Status: runtime.StatusRunning}
	probe := app.RuntimeStatusFunc(func(ctx context.Context) runtime.Report { return report })
	m = newLoadedModel(t, fakeLoader{state: state}, probe)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init with a probe returned nil command")
	}
	msg := cmd()
	result, ok := msg.(runtimeProbeResultMsg)
	if !ok {
		t.Fatalf("got %T, want runtimeProbeResultMsg", msg)
	}
	if result.Report.Status != runtime.StatusRunning {
		t.Errorf("report status = %v, want StatusRunning", result.Report.Status)
	}
}

// TestModelRuntimeProbe_ShowsStatusMessage verifies that delivering a
// runtimeProbeResultMsg stores the report and surfaces the expected
// one-line status text for the running and stopped outcomes.
func TestModelRuntimeProbe_ShowsStatusMessage(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	tests := []struct {
		name   string
		report runtime.Report
		want   string
	}{
		{
			name:   "running",
			report: runtime.Report{Status: runtime.StatusRunning, Capabilities: runtime.Capabilities{Binary: true, Version: "v2.11.4"}},
			want:   "✓ caddy v2.11.4 · running",
		},
		{
			name:   "stopped",
			report: runtime.Report{Status: runtime.StatusStopped, Capabilities: runtime.Capabilities{Binary: true}},
			want:   "✗ caddy binary present but Admin API not reachable (stopped or admin disabled)",
		},
		{
			name:   "unreachable",
			report: runtime.Report{Status: runtime.StatusUnreachable},
			want:   "✗ runtime probe timed out",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLoadedModel(t, fakeLoader{state: state})
			updated, _ := m.Update(runtimeProbeResultMsg{Report: tt.report})
			m = updated.(*Model)
			if !m.runtimeProbed {
				t.Error("runtimeProbed = false, want true after result delivery")
			}
			if m.runtimeReport.Status != tt.report.Status {
				t.Errorf("runtimeReport.Status = %v, want %v", m.runtimeReport.Status, tt.report.Status)
			}
			if m.statusMessage != tt.want {
				t.Errorf("statusMessage = %q, want %q", m.statusMessage, tt.want)
			}
		})
	}
}

// TestModelRuntimeProbe_UnknownStaysQuiet verifies that a fully unknown
// report (no binary, no Admin API) leaves the status line untouched.
func TestModelRuntimeProbe_UnknownStaysQuiet(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.statusMessage = "pre-existing message"
	updated, _ := m.Update(runtimeProbeResultMsg{Report: runtime.Report{Status: runtime.StatusUnknown}})
	m = updated.(*Model)
	if !m.runtimeProbed {
		t.Error("runtimeProbed = false, want true")
	}
	if m.statusMessage != "pre-existing message" {
		t.Errorf("statusMessage = %q, want it untouched by an unknown report", m.statusMessage)
	}
}

// TestModelRuntimeProbe_HeaderBadges verifies that the header renders no
// runtime badge before the probe returns and then shows the RUNNING /
// STOPPED badges plus the version string once the report arrives.
func TestModelRuntimeProbe_HeaderBadges(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	// Before the probe returns no runtime badge is rendered.
	if strings.Contains(m.View(), " RUNNING ") || strings.Contains(m.View(), " STOPPED ") {
		t.Errorf("runtime badge rendered before the probe returned:\n%s", m.View())
	}

	// A running report renders the RUNNING badge and the version.
	updated, _ := m.Update(runtimeProbeResultMsg{Report: runtime.Report{
		Status:       runtime.StatusRunning,
		Capabilities: runtime.Capabilities{Binary: true, Version: "v2.11.4"},
	}})
	m = updated.(*Model)
	view := m.View()
	if !strings.Contains(view, " RUNNING ") {
		t.Errorf("View missing RUNNING badge:\n%s", view)
	}
	if !strings.Contains(view, "caddy v2.11.4") {
		t.Errorf("View missing the version indicator:\n%s", view)
	}

	// A stopped report renders the STOPPED badge.
	updated, _ = m.Update(runtimeProbeResultMsg{Report: runtime.Report{
		Status:       runtime.StatusStopped,
		Capabilities: runtime.Capabilities{Binary: true, Version: "v2.11.4"},
	}})
	m = updated.(*Model)
	if !strings.Contains(m.View(), " STOPPED ") {
		t.Errorf("View missing STOPPED badge:\n%s", m.View())
	}
}

// TestModelRuntimeProbe_UnknownHidesBadge verifies that an unknown probe
// result renders neither a runtime badge nor a version indicator.
func TestModelRuntimeProbe_UnknownHidesBadge(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m = resize(m, 120, 30)
	updated, _ := m.Update(runtimeProbeResultMsg{Report: runtime.Report{Status: runtime.StatusUnknown}})
	m = updated.(*Model)
	view := m.View()
	if strings.Contains(view, " RUNNING ") || strings.Contains(view, " STOPPED ") || strings.Contains(view, " caddy v") {
		t.Errorf("unknown probe rendered runtime state in the header:\n%s", view)
	}
}
