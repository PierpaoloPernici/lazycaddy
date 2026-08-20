package tls

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSource_EmptyDir(t *testing.T) {
	_, err := NewFileSource("", nil, nil, nil).ListCertificates(context.Background())
	if err == nil {
		t.Error("expected error for empty dir")
	}
}

func TestFileSource_MissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	src := NewFileSource(dir, nil, nil, nil)
	_, err := src.ListCertificates(context.Background())
	if err == nil {
		t.Error("expected error for missing dir")
	}
}

func TestFileSource_PermissionError(t *testing.T) {
	// Inject a failing ReadFile to simulate permission.
	dir := t.TempDir()
	// Create a fake .crt so the walk finds it.
	if err := os.WriteFile(filepath.Join(dir, "test.crt"), []byte("not a cert"), 0644); err != nil {
		t.Fatal(err)
	}
	src := NewFileSource(dir, func(string) ([]byte, error) {
		return nil, os.ErrPermission
	}, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(certs) != 1 || certs[0].StoragePath == "" {
		t.Errorf("expected one cert with permission fallback, got %+v", certs)
	}
}

func TestFileSource_CancelledContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "x.crt"), []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"), 0644); err != nil {
		t.Fatal(err)
	}
	src := NewFileSource(dir, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := src.ListCertificates(ctx)
	if err == nil {
		t.Error("expected context cancellation")
	}
}

func TestFileSource_InvalidCertFallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.crt")
	if err := os.WriteFile(path, []byte("not pem"), 0644); err != nil {
		t.Fatal(err)
	}
	src := NewFileSource(dir, nil, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(certs) != 1 || certs[0].Subject == "" {
		t.Errorf("expected fallback cert, got %+v", certs)
	}
}

func TestFetcherState_String(t *testing.T) {
	if FetchLoading.String() != "loading" || FetchUnavailable.String() != "unavailable" {
		t.Error("FetcherState String mismatch")
	}
	if FetcherState(99).String() != "unknown" {
		t.Error("unknown state should be unknown")
	}
}
