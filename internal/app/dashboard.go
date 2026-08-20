package app

import (
	"context"

	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
	"github.com/PierpaoloPernici/lazycaddy/internal/tls"
)

// ConfigFetcher is the cancellable Admin API boundary for the loaded
// config panel. UI models depend on this interface and never call the
// runtime AdminClient directly.
type ConfigFetcher interface {
	FetchConfig(ctx context.Context) ([]byte, error)
}

// ConfigFetcherFunc adapts a function to the ConfigFetcher interface.
type ConfigFetcherFunc func(ctx context.Context) ([]byte, error)

// FetchConfig implements ConfigFetcher.
func (f ConfigFetcherFunc) FetchConfig(ctx context.Context) ([]byte, error) {
	return f(ctx)
}

// UpstreamFetcher derives upstream health information from the loaded
// config. It is separate from ConfigFetcher so the upstream panel can be
// stale while the config panel is available.
type UpstreamFetcher interface {
	FetchUpstreams(ctx context.Context) ([]runtime.Upstream, error)
}

// UpstreamFetcherFunc adapts a function to the UpstreamFetcher interface.
type UpstreamFetcherFunc func(ctx context.Context) ([]runtime.Upstream, error)

// FetchUpstreams implements UpstreamFetcher.
func (f UpstreamFetcherFunc) FetchUpstreams(ctx context.Context) ([]runtime.Upstream, error) {
	return f(ctx)
}

// TLSFetcher is the certificate source boundary for the TLS dashboard.
// UI models depend on this interface and never touch the filesystem
// directly.
type TLSFetcher interface {
	FetchCertificates(ctx context.Context) ([]tls.Certificate, error)
}

// TLSFetcherFunc adapts a function to the TLSFetcher interface.
type TLSFetcherFunc func(ctx context.Context) ([]tls.Certificate, error)

// FetchCertificates implements TLSFetcher.
func (f TLSFetcherFunc) FetchCertificates(ctx context.Context) ([]tls.Certificate, error) {
	return f(ctx)
}
