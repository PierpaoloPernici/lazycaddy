package tls

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestListCertificates_Coverage(t *testing.T) {
	dir := t.TempDir()
	// Create a valid cert .crt
	cert := generateTestCert(t, "a.example.com")
	if err := os.WriteFile(filepath.Join(dir, "a.crt"), cert, 0644); err != nil {
		t.Fatal(err)
	}
	// Create a .pem private key that should be skipped (ReadHeader will detect PRIVATE KEY)
	keyPEM := []byte("-----BEGIN PRIVATE KEY-----\nMIIBVAIBADANBgkqhkiG9w0BAQEFAASCAT4A...\n-----END PRIVATE KEY-----\n")
	if err := os.WriteFile(filepath.Join(dir, "priv.pem"), keyPEM, 0644); err != nil {
		t.Fatal(err)
	}
	// Create a .key file that should be skipped
	if err := os.WriteFile(filepath.Join(dir, "b.key"), keyPEM, 0644); err != nil {
		t.Fatal(err)
	}
	// Create a .cer file
	if err := os.WriteFile(filepath.Join(dir, "c.cer"), cert, 0644); err != nil {
		t.Fatal(err)
	}
	src := NewFileSource(dir, nil, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	// Should have 2 certs (a.crt and c.cer), not the private keys
	if len(certs) != 2 {
		t.Errorf("expected 2 certs, got %d: %+v", len(certs), certs)
	}
	// Test with sidecar
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(`{"ocsp_status":"revoked"}`), 0644); err != nil {
		t.Fatal(err)
	}
	certs2, _ := src.ListCertificates(context.Background())
	found := false
	for _, c := range certs2 {
		if c.Subject == "a.example.com" && c.OCSPState == "revoked" {
			found = true
		}
	}
	if !found {
		t.Error("sidecar not enriched")
	}
	// Test String for all states
	for _, s := range []FetcherState{FetchLoading, FetchAvailable, FetchStale, FetchUnavailable, FetcherState(99)} {
		_ = s.String()
	}
	// Test NewFileSourceWithHeader
	src2 := NewFileSourceWithHeader(dir, nil, nil, nil, nil)
	if src2.ReadHeader == nil {
		t.Error("ReadHeader nil")
	}
	_, _ = src2.ListCertificates(context.Background())
}
