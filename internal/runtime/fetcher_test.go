package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchState_String(t *testing.T) {
	tests := []struct {
		state FetchState
		want  string
	}{
		{FetchLoading, "loading"},
		{FetchAvailable, "available"},
		{FetchStale, "stale"},
		{FetchUnavailable, "unavailable"},
		{FetchState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("FetchState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestAdminConfigFetcher_Success(t *testing.T) {
	body := []byte(`{"apps":{}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	f := NewAdminConfigFetcher(NewAdminClient(srv.URL, time.Second))
	got, err := f.FetchConfig(context.Background())
	if err != nil {
		t.Fatalf("FetchConfig: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("got %q, want %q", got, body)
	}
}

func TestAdminConfigFetcher_NilClient(t *testing.T) {
	f := NewAdminConfigFetcher(nil)
	_, err := f.FetchConfig(context.Background())
	if !errors.Is(err, ErrAdminUnreachable) {
		t.Fatalf("err = %v, want ErrAdminUnreachable", err)
	}
}

func TestAdminUpstreamFetcher_Success(t *testing.T) {
	cfg := []byte(`{"apps":{"http":{"servers":{"srv0":{"routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"backend:8080"}],"health_checks":{"active":{"uri":"/health"}}},{"handler":"static_response"}]}]}}}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(cfg)
	}))
	defer srv.Close()
	f := NewAdminUpstreamFetcher(NewAdminClient(srv.URL, time.Second))
	ups, err := f.FetchUpstreams(context.Background())
	if err != nil {
		t.Fatalf("FetchUpstreams: %v", err)
	}
	if len(ups) != 1 || ups[0].Address != "backend:8080" || ups[0].Server != "srv0" {
		t.Fatalf("upstreams = %+v, want one backend:8080 on srv0", ups)
	}
	if ups[0].HealthCheck == nil || ups[0].HealthCheck.Active == nil {
		t.Error("HealthCheck not parsed")
	}
}

func TestAdminUpstreamFetcher_ConfigError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	f := NewAdminUpstreamFetcher(NewAdminClient(srv.URL, time.Second))
	_, err := f.FetchUpstreams(context.Background())
	if !errors.Is(err, ErrAdminRejected) {
		t.Fatalf("err = %v, want ErrAdminRejected", err)
	}
}

func TestConfigFetcherFunc(t *testing.T) {
	called := false
	f := ConfigFetcherFunc(func(ctx context.Context) ([]byte, error) {
		called = true
		return []byte("ok"), nil
	})
	if _, err := f.FetchConfig(context.Background()); err != nil || !called {
		t.Fatalf("ConfigFetcherFunc failed")
	}
}

func TestUpstreamFetcherFunc(t *testing.T) {
	f := UpstreamFetcherFunc(func(ctx context.Context) ([]Upstream, error) {
		return []Upstream{{Address: "a"}}, nil
	})
	ups, _ := f.FetchUpstreams(context.Background())
	if len(ups) != 1 {
		t.Error("UpstreamFetcherFunc failed")
	}
}
