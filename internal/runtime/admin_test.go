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
