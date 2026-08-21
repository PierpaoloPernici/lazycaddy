package ui

import (
	"context"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
	"github.com/PierpaoloPernici/lazycaddy/internal/tls"
)

func TestModel_WithFetchers(t *testing.T) {
	m := &Model{}
	cf := app.ConfigFetcherFunc(func(ctx context.Context) ([]byte, error) { return nil, nil })
	uf := app.UpstreamFetcherFunc(func(ctx context.Context) ([]runtime.Upstream, error) { return nil, nil })
	tf := app.TLSFetcherFunc(func(ctx context.Context) ([]tls.Certificate, error) { return nil, nil })
	m.WithConfigFetcher(cf)
	if m.configFetcher == nil {
		t.Error("WithConfigFetcher not set")
	}
	m.WithUpstreamFetcher(uf)
	if m.upstreamFetcher == nil {
		t.Error("WithUpstreamFetcher not set")
	}
	m.WithTLSFetcher(tf)
	if m.tlsFetcher == nil {
		t.Error("WithTLSFetcher not set")
	}
	// Test NewWithBrowser
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{"config/Caddyfile": "x {}\n"}))
	loader := fakeLoader{state: state}
	browser := app.BrowserFunc(func(ctx context.Context, url string) error { return nil })
	m2 := NewWithBrowser(loader, nil, nil, nil, nil, nil, nil, nil, "dev", nil, nil, nil, browser)
	if m2.browser == nil {
		t.Error("NewWithBrowser not set")
	}
	_ = caddyfile.Plan{}
	_ = config.DefaultSettings()
}

func TestModel_NewWithBrowser_AndFetchers(t *testing.T) {
	// Ensure the 0% functions are covered
	m := NewWithBrowser(nil, nil, nil, nil, nil, nil, nil, nil, "v", nil, nil, nil, nil)
	if m == nil {
		t.Error("NewWithBrowser nil")
	}
}
