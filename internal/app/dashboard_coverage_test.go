package app

import (
	"context"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
	"github.com/PierpaoloPernici/lazycaddy/internal/tls"
)

func TestDashboard_FetchConfigFunc(t *testing.T) {
	f := ConfigFetcherFunc(func(ctx context.Context) ([]byte, error) {
		return []byte("ok"), nil
	})
	b, err := f.FetchConfig(context.Background())
	if err != nil || string(b) != "ok" {
		t.Errorf("FetchConfig failed")
	}
}

func TestDashboard_FetchUpstreamsFunc(t *testing.T) {
	f := UpstreamFetcherFunc(func(ctx context.Context) ([]runtime.Upstream, error) {
		return []runtime.Upstream{{Address: "a:80"}}, nil
	})
	ups, err := f.FetchUpstreams(context.Background())
	if err != nil || len(ups) != 1 {
		t.Errorf("FetchUpstreams failed")
	}
}

func TestDashboard_FetchCertificatesFunc(t *testing.T) {
	f := TLSFetcherFunc(func(ctx context.Context) ([]tls.Certificate, error) {
		return []tls.Certificate{{Subject: "s"}}, nil
	})
	certs, err := f.FetchCertificates(context.Background())
	if err != nil || len(certs) != 1 {
		t.Errorf("FetchCertificates failed")
	}
}
