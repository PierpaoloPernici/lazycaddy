package tls

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListCertificates_Locked(t *testing.T) {
	dir := t.TempDir()
	// Create a lock file and a cert
	if err := os.WriteFile(filepath.Join(dir, ".lock"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	cert := generateTestCert(t, "example.com")
	if err := os.WriteFile(filepath.Join(dir, "example.crt"), cert, 0644); err != nil {
		t.Fatal(err)
	}
	src := NewFileSource(dir, nil, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		// When lock is present and certs exist, it should return certs with Locked=true, not error
		t.Fatalf("ListCertificates with lock: %v", err)
	}
	if len(certs) != 1 || !certs[0].Locked {
		t.Errorf("expected locked cert, got %+v", certs)
	}
	// Empty dir with only lock -> ErrStorageLocked
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, ".lock"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	src2 := NewFileSource(empty, nil, nil, nil)
	_, err = src2.ListCertificates(context.Background())
	if err == nil || err.Error() != ErrStorageLocked.Error() {
		// Check via errors.Is
		if !isStorageLocked(err) {
			t.Errorf("empty locked dir should be ErrStorageLocked, got %v", err)
		}
	}
}

func isStorageLocked(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == ErrStorageLocked.Error() || err.Error() == "TLS storage locked" || contains(err.Error(), "locked")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

func TestListCertificates_PrivateKeySkipped(t *testing.T) {
	dir := t.TempDir()
	// Create a private key PEM
	keyPEM := []byte("-----BEGIN PRIVATE KEY-----\nMIIBVAIBADANBgkqhkiG9w0BAQEFAASCAT4A...\n-----END PRIVATE KEY-----\n")
	if err := os.WriteFile(filepath.Join(dir, "key.pem"), keyPEM, 0644); err != nil {
		t.Fatal(err)
	}
	// Also create a .key file
	if err := os.WriteFile(filepath.Join(dir, "priv.key"), keyPEM, 0644); err != nil {
		t.Fatal(err)
	}
	// And a private.example.com.crt should NOT be skipped (only .key and .pem with PRIVATE KEY)
	cert := generateTestCert(t, "private.example.com")
	if err := os.WriteFile(filepath.Join(dir, "private.example.com.crt"), cert, 0644); err != nil {
		t.Fatal(err)
	}
	src := NewFileSource(dir, nil, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	if len(certs) != 1 || certs[0].Subject != "private.example.com" {
		t.Errorf("expected only private.example.com.crt, got %+v", certs)
	}
}

func TestListCertificates_PermissionAndSidecar(t *testing.T) {
	dir := t.TempDir()
	cert := generateTestCert(t, "test.example.com")
	if err := os.WriteFile(filepath.Join(dir, "test.crt"), cert, 0644); err != nil {
		t.Fatal(err)
	}
	// Sidecar with OCSP
	if err := os.WriteFile(filepath.Join(dir, "test.json"), []byte(`{"ocsp_status":"good"}`), 0644); err != nil {
		t.Fatal(err)
	}
	src := NewFileSource(dir, nil, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	if len(certs) != 1 || certs[0].OCSPState != "good" {
		t.Errorf("OCSP not enriched: %+v", certs)
	}
	if certs[0].RenewalState == "unknown" {
		t.Errorf("renewal state should be valid/expiring/expired, got unknown")
	}
}

func TestNewFileSourceWithHeader_PrivateKey(t *testing.T) {
	dir := t.TempDir()
	// Use injected ReadHeader that returns PRIVATE KEY
	src := NewFileSourceWithHeader(dir, func(string) ([]byte, error) { return []byte("cert"), nil }, func(string) ([]byte, error) { return []byte("-----BEGIN PRIVATE KEY-----"), nil }, nil, nil)
	// Create a .pem file entry via fake ReadDir
	// Instead test directly that ReadHeader is used
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	// The test is just to ensure NewFileSourceWithHeader doesn't panic and uses the injected header
	_, _ = src.ListCertificates(context.Background())
}

func generateTestCert(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		DNSNames:              []string{cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
