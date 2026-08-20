package runtime

import (
	"context"
	"errors"
	"time"
)

// FetchState is the explicit lifecycle of a single dashboard panel.
// Every panel — runtime, loaded config, upstreams — carries its own
// state so a failure in one never blocks the others.
type FetchState int

const (
	// FetchLoading is the initial state while the first fetch is in
	// flight or a manual refresh is running. The panel shows a spinner
	// and preserves the previous data when possible.
	FetchLoading FetchState = iota
	// FetchAvailable means the last fetch succeeded and the data is fresh.
	FetchAvailable
	// FetchStale means the data was available but a subsequent refresh
	// failed; the previous data is still shown with a stale badge.
	FetchStale
	// FetchUnavailable means no data has ever been fetched successfully
	// and the last attempt failed (or the capability is missing).
	FetchUnavailable
)

func (s FetchState) String() string {
	switch s {
	case FetchLoading:
		return "loading"
	case FetchAvailable:
		return "available"
	case FetchStale:
		return "stale"
	case FetchUnavailable:
		return "unavailable"
	default:
		return "unknown"
	}
}

// ConfigSnapshot is the per-panel state for the loaded-config inspection
// (GET /config/). It is owned by the UI and updated on every fetch
// result so refresh never blocks the TUI.
type ConfigSnapshot struct {
	State     FetchState
	Data      []byte
	Err       error
	FetchedAt time.Time
	Endpoint  string
}

// UpstreamSnapshot is the per-panel state for the upstream health view.
// Upstreams are derived from the loaded config, so a config fetch failure
// propagates as an unavailable upstream panel without blocking the runtime
// panel.
type UpstreamSnapshot struct {
	State     FetchState
	Upstreams []Upstream
	Err       error
	FetchedAt time.Time
}

// ConfigFetcher is the cancellable Admin API boundary for the loaded
// config panel. UI models depend on this interface and never call the
// AdminClient directly.
type ConfigFetcher interface {
	FetchConfig(ctx context.Context) ([]byte, error)
}

// ConfigFetcherFunc adapts a function to the ConfigFetcher interface.
type ConfigFetcherFunc func(ctx context.Context) ([]byte, error)

// FetchConfig implements ConfigFetcher.
func (f ConfigFetcherFunc) FetchConfig(ctx context.Context) ([]byte, error) {
	return f(ctx)
}

// AdminConfigFetcher is the production ConfigFetcher backed by an
// AdminClient. It delegates to AdminClient.Config and is safe for
// concurrent use.
type AdminConfigFetcher struct {
	client *AdminClient
}

// NewAdminConfigFetcher returns a fetcher for the given client. A nil
// client makes every fetch fail with ErrAdminUnreachable so the panel
// degrades to an explicit unavailable state rather than a panic.
func NewAdminConfigFetcher(client *AdminClient) *AdminConfigFetcher {
	return &AdminConfigFetcher{client: client}
}

// FetchConfig implements ConfigFetcher.
func (f *AdminConfigFetcher) FetchConfig(ctx context.Context) ([]byte, error) {
	if f.client == nil {
		return nil, ErrAdminUnreachable
	}
	return f.client.Config(ctx)
}

// UpstreamFetcher derives upstream health information from the loaded
// config. It is a separate fetcher so the upstream panel can be stale
// while the config panel is available.
type UpstreamFetcher interface {
	FetchUpstreams(ctx context.Context) ([]Upstream, error)
}

// UpstreamFetcherFunc adapts a function to the UpstreamFetcher interface.
type UpstreamFetcherFunc func(ctx context.Context) ([]Upstream, error)

// FetchUpstreams implements UpstreamFetcher.
func (f UpstreamFetcherFunc) FetchUpstreams(ctx context.Context) ([]Upstream, error) {
	return f(ctx)
}

// AdminUpstreamFetcher fetches upstreams by first fetching the loaded
// config and then parsing it with ParseUpstreams. It reuses the same
// AdminClient and respects the caller's context for cancellation.
type AdminUpstreamFetcher struct {
	client *AdminClient
}

// NewAdminUpstreamFetcher returns a fetcher for the given client.
func NewAdminUpstreamFetcher(client *AdminClient) *AdminUpstreamFetcher {
	return &AdminUpstreamFetcher{client: client}
}

// FetchUpstreams implements UpstreamFetcher. It first parses the
// static upstreams from GET /config/ and then, when the Admin API
// exposes it, enriches them with live health (fails, active, healthy,
// available) from GET /reverse_proxy/upstreams. A missing live
// endpoint is not a failure: the static view stays available.
func (f *AdminUpstreamFetcher) FetchUpstreams(ctx context.Context) ([]Upstream, error) {
	if f.client == nil {
		return nil, ErrAdminUnreachable
	}
	cfg, err := f.client.Config(ctx)
	if err != nil {
		return nil, err
	}
	static, err := ParseUpstreams(cfg)
	if err != nil {
		return nil, err
	}
	live, err := f.client.Upstreams(ctx)
	if err != nil {
		if errors.Is(err, ErrAdminNotFound) {
			return static, nil
		}
		return nil, err
	}
	return EnrichUpstreamsWithLive(static, live), nil
}
