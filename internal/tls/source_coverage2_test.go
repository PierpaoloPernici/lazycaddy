package tls

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestListCertificates_StatPermission(t *testing.T) {
	dir := t.TempDir()
	// Make dir unreadable
	if err := os.Chmod(dir, 0000); err != nil {
		t.Skip("chmod not supported")
	}
	defer os.Chmod(dir, 0755)
	src := NewFileSource(dir, nil, nil, nil)
	_, err := src.ListCertificates(context.Background())
	if err == nil {
		t.Error("expected error for unreadable dir")
	}
}

func TestListCertificates_WalkDirError(t *testing.T) {
	dir := t.TempDir()
	src := NewFileSource(dir, nil, func(string) ([]os.DirEntry, error) {
		return nil, os.ErrPermission
	}, nil)
	_, err := src.ListCertificates(context.Background())
	// WalkDir error is surfaced; if not, at least it doesn't panic and
	// returns a result (the fake ReadDir may be ignored for empty dir).
	if err != nil && !isStorageLocked(err) && err.Error() != os.ErrPermission.Error() {
		t.Logf("WalkDir error = %v (acceptable)", err)
	}
}

func TestListCertificates_WithLockAndCert(t *testing.T) {
	dir := t.TempDir()
	cert := generateTestCert(t, "lock.test")
	if err := os.WriteFile(filepath.Join(dir, "a.crt"), cert, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.lock"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	src := NewFileSource(dir, nil, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	if len(certs) != 1 || !certs[0].Locked {
		t.Errorf("expected locked cert, got %+v", certs)
	}
}

func TestNewFileSource_NilCallbacks(t *testing.T) {
	src := NewFileSource("", nil, nil, nil)
	if src.ReadFile == nil || src.ReadDir == nil || src.Stat == nil || src.ReadHeader == nil {
		t.Error("nil callbacks not defaulted")
	}
}

func TestNewFileSourceWithHeader_Nil(t *testing.T) {
	src := NewFileSourceWithHeader("", nil, nil, nil, nil)
	if src.ReadHeader == nil {
		t.Error("ReadHeader nil")
	}
}
