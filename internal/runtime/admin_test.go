package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoadJSON_Success(t *testing.T) {
	config := []byte(`{"apps":{}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/load" {
			t.Errorf("path = %s, want /load", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if cc := r.Header.Get("Cache-Control"); cc != "must-revalidate" {
			t.Errorf("Cache-Control = %q, want must-revalidate", cc)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !bytes.Equal(body, config) {
			t.Errorf("body = %q, want %q", body, config)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Pass a trailing slash on purpose to verify NewAdminClient trims it.
	c := NewAdminClient(srv.URL+"/", time.Second)
	if err := c.LoadJSON(context.Background(), config); err != nil {
		t.Fatalf("LoadJSON: unexpected error: %v", err)
	}
}

func TestLoadJSON_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad config: line 3"}`))
	}))
	defer srv.Close()

	c := NewAdminClient(srv.URL, time.Second)
	err := c.LoadJSON(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrAdminRejected) {
		t.Fatalf("expected ErrAdminRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "bad config") {
		t.Errorf("message = %q, want it to contain the error body field", err.Error())
	}
}

func TestLoadJSON_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := NewAdminClient(url, time.Second)
	err := c.LoadJSON(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrAdminUnreachable) {
		t.Fatalf("expected ErrAdminUnreachable, got %v", err)
	}
}

func TestLoadJSON_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
	}))
	defer srv.Close()

	c := NewAdminClient(srv.URL, 20*time.Millisecond)
	err := c.LoadJSON(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrAdminTimeout) {
		t.Fatalf("expected ErrAdminTimeout, got %v", err)
	}
}

func TestLoadJSON_Cancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := NewAdminClient(srv.URL, time.Second)
	err := c.LoadJSON(ctx, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if errors.Is(err, ErrAdminUnreachable) || errors.Is(err, ErrAdminTimeout) || errors.Is(err, ErrAdminRejected) {
		t.Fatalf("cancellation must not be classified as an admin sentinel, got %v", err)
	}
}

func TestConfig_Success(t *testing.T) {
	body := []byte(`{"apps":{}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/config/" {
			t.Errorf("path = %s, want /config/", r.URL.Path)
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := NewAdminClient(srv.URL, time.Second)
	got, err := c.Config(context.Background())
	if err != nil {
		t.Fatalf("Config: unexpected error: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func TestConfig_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewAdminClient(srv.URL, time.Second)
	_, err := c.Config(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrAdminRejected) {
		t.Fatalf("expected ErrAdminRejected, got %v", err)
	}
}

// TestNewAdminClient_AppliesDefaults verifies the constructor normalises a
// non-positive timeout to 30s and trims a trailing slash from the base URL.
func TestNewAdminClient_AppliesDefaults(t *testing.T) {
	c := NewAdminClient("http://localhost:2019/", 0)
	if c.baseURL != "http://localhost:2019" {
		t.Errorf("baseURL = %q, want the trailing slash trimmed", c.baseURL)
	}
	if c.hc == nil || c.hc.Timeout != 30*time.Second {
		t.Errorf("client timeout = %v, want the 30s default", c.hc.Timeout)
	}

	c2 := NewAdminClient("http://localhost:2019", 5*time.Second)
	if c2.baseURL != "http://localhost:2019" || c2.hc.Timeout != 5*time.Second {
		t.Errorf("explicit values not honoured: %q %v", c2.baseURL, c2.hc.Timeout)
	}
}

// TestLoadJSON_InvalidURLMapsToUnreachable verifies that a request whose
// URL cannot even be constructed surfaces as ErrAdminUnreachable.
func TestLoadJSON_InvalidURLMapsToUnreachable(t *testing.T) {
	c := &AdminClient{baseURL: "http://[::1:bad", hc: &http.Client{}}
	if err := c.LoadJSON(context.Background(), []byte(`{}`)); !errors.Is(err, ErrAdminUnreachable) {
		t.Errorf("LoadJSON with an invalid URL = %v, want ErrAdminUnreachable", err)
	}
	if _, err := c.Config(context.Background()); !errors.Is(err, ErrAdminUnreachable) {
		t.Errorf("Config with an invalid URL = %v, want ErrAdminUnreachable", err)
	}
}

// TestAdminErrorBody_FallsBackToStatusText verifies the final fallback of
// adminErrorBody: an empty non-JSON body yields the HTTP status text.
func TestAdminErrorBody_FallsBackToStatusText(t *testing.T) {
	if got := adminErrorBody(404, nil); got != "Not Found" {
		t.Errorf("adminErrorBody(404, nil) = %q, want the status text", got)
	}
}

// TestLoadJSON_RejectedWithWarnings covers the Admin API contract Caddy 2.11.6
// introduced (#7267): an invalid configuration is rejected with 400 and a
// single JSON body carrying both "error" and "warnings", where older releases
// answered 200 with two concatenated JSON objects. Both fields must reach the
// caller, because the warnings explain why the config was refused.
func TestLoadJSON_RejectedWithWarnings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"loading new config: adapting config using caddyfile: unrecognized directive: nope","warnings":[{"file":"/etc/caddy/Caddyfile","line":12,"directive":"nope","message":"unrecognized directive"}]}`)
	}))
	defer srv.Close()

	c := NewAdminClient(srv.URL, time.Second)
	err := c.LoadJSON(context.Background(), []byte(`{}`))
	if !errors.Is(err, ErrAdminRejected) {
		t.Fatalf("err = %v, want ErrAdminRejected", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"unrecognized directive: nope",
		"/etc/caddy/Caddyfile:12: unrecognized directive (nope)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message = %q, want it to contain %q", msg, want)
		}
	}
}

// TestAdminErrorBody_WarningsOnly covers a rejected body that carries warnings
// but no error message: the status text is still the leading message.
func TestAdminErrorBody_WarningsOnly(t *testing.T) {
	got := adminErrorBody(http.StatusBadRequest, []byte(`{"warnings":[{"line":7,"message":"deprecated directive"}]}`))
	want := "Bad Request (warnings: line 7: deprecated directive)"
	if got != want {
		t.Errorf("adminErrorBody = %q, want %q", got, want)
	}
}

// TestFormatAdminWarnings pins the rendering of every field combination Caddy
// emits, including entries that must be skipped entirely.
func TestFormatAdminWarnings(t *testing.T) {
	tests := []struct {
		name     string
		warnings []adminWarning
		want     string
	}{
		{
			name:     "empty slice",
			warnings: nil,
			want:     "",
		},
		{
			name:     "file line directive and message",
			warnings: []adminWarning{{File: "Caddyfile", Line: 3, Directive: "encode", Message: "unknown format"}},
			want:     "Caddyfile:3: unknown format (encode)",
		},
		{
			name:     "file without line",
			warnings: []adminWarning{{File: "Caddyfile", Message: "server block is empty"}},
			want:     "Caddyfile: server block is empty",
		},
		{
			name:     "line without file",
			warnings: []adminWarning{{Line: 9, Message: "placeholder is unknown"}},
			want:     "line 9: placeholder is unknown",
		},
		{
			name:     "message only",
			warnings: []adminWarning{{Message: "no automatic HTTPS"}},
			want:     "no automatic HTTPS",
		},
		{
			name: "blank entries are skipped and survivors joined",
			warnings: []adminWarning{
				{File: "Caddyfile", Line: 1, Message: "   "},
				{File: "Caddyfile", Line: 2, Message: "first"},
				{Message: "second"},
			},
			want: "Caddyfile:2: first; second",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatAdminWarnings(tt.warnings); got != tt.want {
				t.Errorf("formatAdminWarnings = %q, want %q", got, tt.want)
			}
		})
	}
}

// errRoundTripper fails every request at the transport layer.
type errRoundTripper struct{}

func (errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("transport boom")
}

func TestLoadJSON_InvalidURL(t *testing.T) {
	// A base URL that cannot be parsed makes request construction fail,
	// which maps to the unreachable sentinel.
	c := NewAdminClient("://bad", time.Second)
	err := c.LoadJSON(context.Background(), []byte("{}"))
	if !errors.Is(err, ErrAdminUnreachable) {
		t.Fatalf("err = %v, want ErrAdminUnreachable", err)
	}
}

func TestConfig_TransportError(t *testing.T) {
	c := NewAdminClient("http://localhost:2019", time.Second)
	c.hc = &http.Client{Transport: errRoundTripper{}}
	if _, err := c.Config(context.Background()); !errors.Is(err, ErrAdminUnreachable) {
		t.Fatalf("err = %v, want ErrAdminUnreachable", err)
	}
}

func TestLoadJSON_RawBodyFallback(t *testing.T) {
	// A non-2xx response with a non-JSON body surfaces the raw body text.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "boom")
	}))
	defer srv.Close()
	c := NewAdminClient(srv.URL, time.Second)
	err := c.LoadJSON(context.Background(), []byte("{}"))
	if !errors.Is(err, ErrAdminRejected) {
		t.Fatalf("err = %v, want ErrAdminRejected", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want the raw body in the message", err)
	}
}

// errReadCloser is a response body whose Read always fails.
type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, errors.New("body boom") }
func (errReadCloser) Close() error             { return nil }

// bodyErrorRoundTripper returns a 200 response whose body cannot be read.
type bodyErrorRoundTripper struct{}

func (bodyErrorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: errReadCloser{}}, nil
}

func TestLoadJSON_BodyReadError(t *testing.T) {
	c := NewAdminClient("http://localhost:2019", time.Second)
	c.hc = &http.Client{Transport: bodyErrorRoundTripper{}}
	if err := c.LoadJSON(context.Background(), []byte("{}")); !errors.Is(err, ErrAdminUnreachable) {
		t.Fatalf("err = %v, want ErrAdminUnreachable", err)
	}
}

func TestConfig_BodyReadError(t *testing.T) {
	c := NewAdminClient("http://localhost:2019", time.Second)
	c.hc = &http.Client{Transport: bodyErrorRoundTripper{}}
	if _, err := c.Config(context.Background()); !errors.Is(err, ErrAdminUnreachable) {
		t.Fatalf("err = %v, want ErrAdminUnreachable", err)
	}
}
