package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewDetector_Defaults(t *testing.T) {
	d := NewDetector(Options{})
	if d.versionTimeout != 5*time.Second {
		t.Errorf("versionTimeout = %s, want 5s default", d.versionTimeout)
	}
	if d.adminTimeout != 5*time.Second {
		t.Errorf("adminTimeout = %s, want 5s default", d.adminTimeout)
	}
	// Negative timeouts also fall back to the defaults.
	d = NewDetector(Options{VersionTimeout: -1, AdminTimeout: -2})
	if d.versionTimeout != 5*time.Second || d.adminTimeout != 5*time.Second {
		t.Errorf("negative timeouts must default to 5s, got %s / %s", d.versionTimeout, d.adminTimeout)
	}
}

func TestProbe_NoBinaryNoAdmin(t *testing.T) {
	d := NewDetector(Options{Binary: "", Admin: nil, Writable: true})
	rep := d.Probe(context.Background())
	if rep.Status != StatusUnknown {
		t.Errorf("Status = %v, want StatusUnknown", rep.Status)
	}
	if rep.Capabilities.Binary || rep.Capabilities.Validation || rep.Capabilities.AdminAPI ||
		rep.Capabilities.Readable || rep.Capabilities.Reload {
		t.Errorf("no capability may be proven without a binary or admin client, got %+v", rep.Capabilities)
	}
	if !rep.Capabilities.Writable {
		t.Error("Writable = false, want true (it comes from settings, not a probe)")
	}
	if rep.ProbedAt.IsZero() {
		t.Error("ProbedAt is zero, want a timestamp")
	}
}

func TestProbe_NoBinaryAdminUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("null"))
	}))
	defer srv.Close()

	d := NewDetector(Options{
		Admin:        NewAdminClient(srv.URL, time.Second),
		AdminTimeout: time.Second,
	})
	rep := d.Probe(context.Background())
	if rep.Status != StatusRunning {
		t.Errorf("Status = %v, want StatusRunning", rep.Status)
	}
	if !rep.Capabilities.AdminAPI || !rep.Capabilities.Readable {
		t.Errorf("AdminAPI/Readable = %v/%v, want true/true", rep.Capabilities.AdminAPI, rep.Capabilities.Readable)
	}
	if rep.Capabilities.Binary || rep.Capabilities.Reload {
		t.Errorf("Binary/Reload = %v/%v, want false/false without a binary", rep.Capabilities.Binary, rep.Capabilities.Reload)
	}
}

func TestProbe_BinaryAdminDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	d := NewDetector(Options{
		Binary:         "caddy",
		Runner:         fakeRunner{stdout: []byte("v2.11.4 h1:q3pe...k=\n")},
		Admin:          NewAdminClient(url, time.Second),
		AdminTimeout:   time.Second,
		VersionTimeout: time.Second,
	})
	rep := d.Probe(context.Background())
	if rep.Status != StatusStopped {
		t.Errorf("Status = %v, want StatusStopped", rep.Status)
	}
	if !rep.Capabilities.Binary || !rep.Capabilities.Validation {
		t.Errorf("Binary/Validation = %v/%v, want true/true", rep.Capabilities.Binary, rep.Capabilities.Validation)
	}
	if rep.Capabilities.Version != "v2.11.4" {
		t.Errorf("Version = %q, want v2.11.4", rep.Capabilities.Version)
	}
	if rep.Capabilities.AdminAPI || rep.Capabilities.Readable || rep.Capabilities.Reload {
		t.Errorf("AdminAPI/Readable/Reload = %v/%v/%v, want all false", rep.Capabilities.AdminAPI, rep.Capabilities.Readable, rep.Capabilities.Reload)
	}
}

func TestProbe_BinaryAdminUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"apps":{}}`))
	}))
	defer srv.Close()

	d := NewDetector(Options{
		Binary:         "/usr/local/bin/caddy",
		Runner:         fakeRunner{stdout: []byte("v2.11.4 h1:q3pe...k=\n")},
		Admin:          NewAdminClient(srv.URL, time.Second),
		AdminTimeout:   time.Second,
		VersionTimeout: time.Second,
	})
	rep := d.Probe(context.Background())
	if rep.Status != StatusRunning {
		t.Errorf("Status = %v, want StatusRunning", rep.Status)
	}
	if !rep.Capabilities.Binary || !rep.Capabilities.Validation || !rep.Capabilities.AdminAPI ||
		!rep.Capabilities.Readable || !rep.Capabilities.Reload {
		t.Errorf("all capabilities should be proven, got %+v", rep.Capabilities)
	}
	if rep.Capabilities.Version != "v2.11.4" {
		t.Errorf("Version = %q, want v2.11.4", rep.Capabilities.Version)
	}
}

// TestProbe_AdminTimeoutUnreachable verifies that an Admin API probe that
// cannot complete within the admin timeout degrades to StatusUnreachable
// rather than hanging the startup.
func TestProbe_AdminTimeoutUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // hold the request until the client gives up
	}))
	defer srv.Close()

	d := NewDetector(Options{
		Admin:        NewAdminClient(srv.URL, time.Second),
		AdminTimeout: 20 * time.Millisecond,
	})
	start := time.Now()
	rep := d.Probe(context.Background())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Probe took %s, want it bounded by the admin timeout", elapsed)
	}
	if rep.Status != StatusUnreachable {
		t.Errorf("Status = %v, want StatusUnreachable", rep.Status)
	}
	if rep.Capabilities.AdminAPI {
		t.Error("AdminAPI = true, want false after a timed-out probe")
	}
}

func TestProbe_CancelledContextUnreachable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := NewDetector(Options{Binary: "", Admin: nil})
	rep := d.Probe(ctx)
	if rep.Status != StatusUnreachable {
		t.Errorf("Status = %v, want StatusUnreachable when the caller context is done", rep.Status)
	}
}
