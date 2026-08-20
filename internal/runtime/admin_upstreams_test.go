package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdminClient_Upstreams_Success(t *testing.T) {
	body := []byte(`[{"address":"a:80","fails":1}]`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reverse_proxy/upstreams" {
			t.Errorf("path = %s, want /reverse_proxy/upstreams", r.URL.Path)
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c := NewAdminClient(srv.URL, time.Second)
	got, err := c.Upstreams(context.Background())
	if err != nil {
		t.Fatalf("Upstreams: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("got %q, want %q", got, body)
	}
}

func TestAdminClient_Upstreams_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`not found`))
	}))
	defer srv.Close()
	c := NewAdminClient(srv.URL, time.Second)
	_, err := c.Upstreams(context.Background())
	if !errors.Is(err, ErrAdminNotFound) {
		t.Fatalf("err = %v, want ErrAdminNotFound", err)
	}
}

func TestAdminClient_Upstreams_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewAdminClient(srv.URL, time.Second)
	_, err := c.Upstreams(context.Background())
	if !errors.Is(err, ErrAdminRejected) {
		t.Fatalf("err = %v, want ErrAdminRejected", err)
	}
}

func TestAdminUpstreamFetcher_Live404Fallback(t *testing.T) {
	// Config returns one static, live returns 404 -> should return static
	cfg := []byte(`{"apps":{"http":{"servers":{"srv0":{"routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"a:80"}]}]}]}}}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config/" {
			_, _ = w.Write(cfg)
			return
		}
		if r.URL.Path == "/reverse_proxy/upstreams" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
	}))
	defer srv.Close()
	f := NewAdminUpstreamFetcher(NewAdminClient(srv.URL, time.Second))
	ups, err := f.FetchUpstreams(context.Background())
	if err != nil {
		t.Fatalf("FetchUpstreams: %v", err)
	}
	if len(ups) != 1 || ups[0].Address != "a:80" {
		t.Fatalf("got %+v, want a:80", ups)
	}
}

func TestAdminUpstreamFetcher_LiveTimeoutPropagated(t *testing.T) {
	cfg := []byte(`{"apps":{"http":{"servers":{"srv0":{"routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"a:80"}]}]}]}}}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config/" {
			_, _ = w.Write(cfg)
			return
		}
		// Hold the live request until context times out
		<-r.Context().Done()
	}))
	defer srv.Close()
	f := NewAdminUpstreamFetcher(NewAdminClient(srv.URL, 20*time.Millisecond))
	_, err := f.FetchUpstreams(context.Background())
	if !errors.Is(err, ErrAdminTimeout) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want timeout", err)
	}
}

func TestAdminUpstreamFetcher_NilClient(t *testing.T) {
	f := NewAdminUpstreamFetcher(nil)
	_, err := f.FetchUpstreams(context.Background())
	if !errors.Is(err, ErrAdminUnreachable) {
		t.Fatalf("err = %v, want ErrAdminUnreachable", err)
	}
}
