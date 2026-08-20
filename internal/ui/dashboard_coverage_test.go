package ui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
	"github.com/PierpaoloPernici/lazycaddy/internal/tls"
)

func TestRuntimeDashboard_StaleVsUnavailable(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{"config/Caddyfile": "example.test {\n}\n"}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.configFetcher = app.ConfigFetcherFunc(func(ctx context.Context) ([]byte, error) { return []byte(`{"apps":{}}`), nil })
	m.upstreamFetcher = app.UpstreamFetcherFunc(func(ctx context.Context) ([]runtime.Upstream, error) { return nil, nil })
	m.tlsFetcher = app.TLSFetcherFunc(func(ctx context.Context) ([]tls.Certificate, error) { return nil, nil })
	m = resize(m, 120, 30)
	// Open runtime dashboard
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	if !m.showRuntime {
		t.Fatal("showRuntime not open after I")
	}
	// Simulate successful fetch
	m.handleConfigFetchResult(configFetchResultMsg{Data: []byte(`{"apps":{}}`), Err: nil, Gen: m.runtimeConfigGen})
	if m.runtimeConfigState != runtime.FetchAvailable {
		t.Errorf("state = %v, want available", m.runtimeConfigState)
	}
	// Now fail refresh - should become stale, not unavailable
	m.handleConfigFetchResult(configFetchResultMsg{Err: errors.New("boom"), Gen: m.runtimeConfigGen})
	if m.runtimeConfigState != runtime.FetchStale {
		t.Errorf("after success+error, state = %v, want stale", m.runtimeConfigState)
	}
	// Empty case: no previous success, error -> unavailable
	m2 := newLoadedModel(t, fakeLoader{state: state})
	m2.configFetcher = app.ConfigFetcherFunc(func(ctx context.Context) ([]byte, error) { return nil, errors.New("fail") })
	m2 = resize(m2, 120, 30)
	m2 = keyPress(t, m2, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	m2.handleConfigFetchResult(configFetchResultMsg{Err: errors.New("fail"), Gen: m2.runtimeConfigGen})
	if m2.runtimeConfigState != runtime.FetchUnavailable {
		t.Errorf("empty+error should be unavailable, got %v", m2.runtimeConfigState)
	}
}

func TestRuntimeDashboard_GenerationIgnored(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{"config/Caddyfile": "x {}\n"}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.configFetcher = app.ConfigFetcherFunc(func(ctx context.Context) ([]byte, error) { return []byte(`{}`), nil })
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	oldGen := m.runtimeConfigGen
	// Send result with old gen (simulate slow fetch)
	m.runtimeConfigGen++
	m.handleConfigFetchResult(configFetchResultMsg{Data: []byte(`new`), Gen: oldGen})
	if len(m.runtimeConfigData) != 0 && string(m.runtimeConfigData) == "new" {
		t.Error("stale Gen should be ignored")
	}
}

func TestRuntimeDashboard_ToggleInvalidates(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{"config/Caddyfile": "x {}\n"}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.configFetcher = app.ConfigFetcherFunc(func(ctx context.Context) ([]byte, error) { return []byte(`{}`), nil })
	m.upstreamFetcher = app.UpstreamFetcherFunc(func(ctx context.Context) ([]runtime.Upstream, error) { return nil, nil })
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	gen1 := m.runtimeConfigGen
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showRuntime {
		t.Error("should close")
	}
	if m.runtimeConfigGen != gen1+1 {
		t.Errorf("Gen not incremented on close")
	}
	// Switching to TLS should invalidate runtime
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	genBefore := m.runtimeConfigGen
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")})
	if m.runtimeConfigGen != genBefore+1 {
		t.Errorf("switching to TLS should invalidate runtime Gen")
	}
}

func TestRuntimeDashboard_UpstreamStaleWithEmptySuccess(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{"config/Caddyfile": "x {}\n"}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.upstreamFetcher = app.UpstreamFetcherFunc(func(ctx context.Context) ([]runtime.Upstream, error) { return []runtime.Upstream{}, nil })
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	m.handleUpstreamFetchResult(upstreamFetchResultMsg{Upstreams: []runtime.Upstream{}, Err: nil, Gen: m.runtimeUpstreamGen})
	if !m.runtimeUpstreamHasFetched {
		t.Error("empty success should set hasFetched")
	}
	m.handleUpstreamFetchResult(upstreamFetchResultMsg{Err: errors.New("fail"), Gen: m.runtimeUpstreamGen})
	if m.runtimeUpstreamState != runtime.FetchStale {
		t.Errorf("empty success + fail should be stale, got %v", m.runtimeUpstreamState)
	}
}

func TestTLSDashboard_StaleAndGeneration(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{"config/Caddyfile": "x {}\n"}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.tlsFetcher = app.TLSFetcherFunc(func(ctx context.Context) ([]tls.Certificate, error) {
		return []tls.Certificate{{Subject: "a"}}, nil
	})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")})
	m.handleTLSFetchResult(tlsFetchResultMsg{Certs: []tls.Certificate{{Subject: "a"}}, Gen: m.tlsGen})
	if m.tlsState != tls.FetchAvailable || !m.tlsHasFetched {
		t.Errorf("TLS should be available")
	}
	m.handleTLSFetchResult(tlsFetchResultMsg{Err: errors.New("boom"), Gen: m.tlsGen})
	if m.tlsState != tls.FetchStale {
		t.Errorf("TLS stale after success+error, got %v", m.tlsState)
	}
	// Gen mismatch ignored
	oldGen := m.tlsGen
	m.tlsGen++
	m.handleTLSFetchResult(tlsFetchResultMsg{Err: errors.New("ignored"), Gen: oldGen})
	if m.tlsErr.Error() != "boom" {
		t.Error("old Gen should be ignored")
	}
}

func TestRuntimeDashboard_NavigationWithManyUpstreams(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{"config/Caddyfile": "x {}\n"}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.configFetcher = app.ConfigFetcherFunc(func(ctx context.Context) ([]byte, error) { return []byte(`{}`), nil })
	m.upstreamFetcher = app.UpstreamFetcherFunc(func(ctx context.Context) ([]runtime.Upstream, error) {
		var ups []runtime.Upstream
		for i := 0; i < 50; i++ {
			ups = append(ups, runtime.Upstream{Address: "a:80"})
		}
		return ups, nil
	})
	m = resize(m, 120, 30)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	m.handleUpstreamFetchResult(upstreamFetchResultMsg{Upstreams: make([]runtime.Upstream, 50), Gen: m.runtimeUpstreamGen})
	if m.runtimeLineCount() != 50 {
		t.Errorf("runtimeLineCount = %d, want 50", m.runtimeLineCount())
	}
	// Navigate down should be able to reach the last element
	for i := 0; i < 60; i++ {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.runtimeCursor != 49 {
		t.Errorf("cursor = %d, want 49", m.runtimeCursor)
	}
}

func TestDashboard_FetchCmds(t *testing.T) {
	m := &Model{}
	// No fetcher -> unavailable error
	_, _ = m.handleConfigFetchResult(configFetchResultMsg{Err: errors.New("x"), Gen: 0})
	// With fetcher, test the Cmd creation
	m.configFetcher = app.ConfigFetcherFunc(func(ctx context.Context) ([]byte, error) { return []byte("ok"), nil })
	cmd := m.runtimeConfigFetchCmd()
	msg := cmd().(configFetchResultMsg)
	if msg.Err != nil || string(msg.Data) != "ok" {
		t.Errorf("runtimeConfigFetchCmd failed: %+v", msg)
	}
	m.tlsFetcher = app.TLSFetcherFunc(func(ctx context.Context) ([]tls.Certificate, error) { return []tls.Certificate{{Subject: "s"}}, nil })
	cmd2 := m.tlsFetchCmd()
	msg2 := cmd2().(tlsFetchResultMsg)
	if msg2.Err != nil || len(msg2.Certs) != 1 {
		t.Errorf("tlsFetchCmd failed")
	}
	// Test with nil fetcher
	m2 := &Model{}
	cmd3 := m2.runtimeConfigFetchCmd()
	if m3 := cmd3().(configFetchResultMsg); m3.Err == nil {
		t.Error("nil configFetcher should error")
	}
	cmd4 := m2.tlsFetchCmd()
	if m4 := cmd4().(tlsFetchResultMsg); m4.Err == nil {
		t.Error("nil tlsFetcher should error")
	}
}

func TestRuntimeDashboard_RefreshAndToggle(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{"config/Caddyfile": "x {}\n"}))
	m := newLoadedModel(t, fakeLoader{state: state})
	m.configFetcher = app.ConfigFetcherFunc(func(ctx context.Context) ([]byte, error) { return []byte(`{}`), nil })
	m.upstreamFetcher = app.UpstreamFetcherFunc(func(ctx context.Context) ([]runtime.Upstream, error) { return nil, nil })
	m = resize(m, 120, 30)
	// Refresh when not open should be no-op
	if _, cmd := m.refreshRuntimeDashboard(); cmd != nil {
		t.Error("refresh when not open should be nil")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	_, cmd := m.refreshRuntimeDashboard()
	if cmd == nil {
		t.Error("refresh when open should return cmd")
	}
	// Toggle close
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	if m.showRuntime {
		t.Error("should close")
	}
	// Test updateRuntimeKey navigation
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("I")})
	m.handleUpstreamFetchResult(upstreamFetchResultMsg{Upstreams: []runtime.Upstream{{Address: "a"}, {Address: "b"}}, Gen: m.runtimeUpstreamGen})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyUp})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	// Test TLS refresh
	m2 := newLoadedModel(t, fakeLoader{state: state})
	m2.tlsFetcher = app.TLSFetcherFunc(func(ctx context.Context) ([]tls.Certificate, error) { return nil, nil })
	m2 = resize(m2, 120, 30)
	m2 = keyPress(t, m2, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")})
	if _, cmd := m2.refreshTLSDashboard(); cmd == nil {
		t.Error("TLS refresh should return cmd")
	}
	m2 = keyPress(t, m2, tea.KeyMsg{Type: tea.KeyDown})
	m2 = keyPress(t, m2, tea.KeyMsg{Type: tea.KeyUp})
}

func TestLiveSummary(t *testing.T) {
	healthy := true
	fails := 2
	active := 5
	available := true
	l := &runtime.UpstreamLive{Healthy: &healthy, Fails: &fails, Active: &active, Available: &available}
	s := liveSummary(l)
	if !contains(s, "healthy") || !contains(s, "fails=2") {
		t.Errorf("liveSummary = %q", s)
	}
	if liveSummary(nil) != "—" {
		t.Error("nil live should be —")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

// Helper to create a model with many sites for navigation tests
func manySitesState(t *testing.T) *app.State {
	t.Helper()
	src := ""
	for i := 0; i < 20; i++ {
		src += caddyfileSite(i)
	}
	return stateFor(t, "config/Caddyfile", fsReader(map[string]string{"config/Caddyfile": src}))
}

func caddyfileSite(i int) string {
	return "site" + string(rune('a'+i)) + ".test {\n\trespond ok\n}\n"
}

// Ensure imports are used
var _ = caddyfile.KindSite
var _ = config.DefaultSettings
