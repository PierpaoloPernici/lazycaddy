package tls

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// genCertPEM builds a self-signed certificate PEM with explicit subject and
// validity bounds so parseCertificate branches are reachable deterministically.
func genCertPEM(t *testing.T, cn string, dnsNames []string, notBefore, notAfter time.Time) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		DNSNames:              dnsNames,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pemEncodeCertificate(der)
}

func pemEncodeCertificate(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func pemEncodeText(blockType string, data []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: data})
}

func TestListCertificates_PemValidCertificate(t *testing.T) {
	dir := t.TempDir()
	cert := genCertPEM(t, "pem.example.com", []string{"pem.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	if err := os.WriteFile(filepath.Join(dir, "site.pem"), cert, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site.json"), []byte(`{"ocsp_status":"good"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.lock"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	src := NewFileSource(dir, nil, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("expected 1 cert, got %d: %+v", len(certs), certs)
	}
	c := certs[0]
	if c.Subject != "pem.example.com" {
		t.Errorf("subject = %q", c.Subject)
	}
	if !c.Locked {
		t.Error("expected Locked=true under lock file")
	}
	if c.OCSPState != "good" {
		t.Errorf("sidecar OCSP not applied: %q", c.OCSPState)
	}
	if c.JSONPath == "" {
		t.Error("expected JSONPath recorded")
	}
}

func TestListCertificates_PemHeaderPermission(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "unreadable.pem"), []byte("junk"), 0644); err != nil {
		t.Fatal(err)
	}
	perm := &fs.PathError{Op: "open", Path: "unreadable.pem", Err: fs.ErrPermission}
	src := NewFileSourceWithHeader(dir, nil, func(string) ([]byte, error) {
		return nil, perm
	}, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	if len(certs) != 1 || certs[0].RenewalState != "unknown" {
		t.Errorf("expected unknown placeholder cert, got %+v", certs)
	}
}

func TestListCertificates_PemHeaderOtherError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.pem"), []byte("junk"), 0644); err != nil {
		t.Fatal(err)
	}
	src := NewFileSourceWithHeader(dir, nil, func(string) ([]byte, error) {
		return nil, errors.New("read fault")
	}, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	if len(certs) != 0 {
		t.Errorf("non-permission header error should skip the file, got %+v", certs)
	}
}

func TestListCertificates_LockDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".lock"), 0755); err != nil {
		t.Fatal(err)
	}
	cert := genCertPEM(t, "lockdir.example.com", nil, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	if err := os.WriteFile(filepath.Join(dir, "a.crt"), cert, 0644); err != nil {
		t.Fatal(err)
	}
	src := NewFileSource(dir, nil, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	if len(certs) != 1 || !certs[0].Locked {
		t.Errorf("expected locked cert from .lock directory, got %+v", certs)
	}
}

func TestFileSource_StatPermission(t *testing.T) {
	dir := t.TempDir()
	perm := &fs.PathError{Op: "stat", Path: dir, Err: fs.ErrPermission}
	src := NewFileSource(dir, nil, nil, func(string) (os.FileInfo, error) {
		return nil, perm
	})
	_, err := src.ListCertificates(context.Background())
	if !errors.Is(err, ErrStorageUnavailable) {
		t.Errorf("expected ErrStorageUnavailable, got %v", err)
	}
}

func TestFileSource_StatOtherError(t *testing.T) {
	dir := t.TempDir()
	src := NewFileSource(dir, nil, nil, func(string) (os.FileInfo, error) {
		return nil, errors.New("stat fault")
	})
	_, err := src.ListCertificates(context.Background())
	if err == nil || err.Error() != "stat fault" {
		t.Errorf("expected raw stat error, got %v", err)
	}
}

func TestFileSource_ReadHeaderOpenError(t *testing.T) {
	dir := t.TempDir()
	// Dangling symlink: the walk sees a file, os.Open fails.
	link := filepath.Join(dir, "dangling.pem")
	if err := os.Symlink(filepath.Join(dir, "gone"), link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	src := NewFileSource(dir, nil, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	if len(certs) != 0 {
		t.Errorf("unreadable .pem should be skipped, got %+v", certs)
	}
}

func TestNewFileSourceWithHeader_DefaultHeaderOpenError(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "dangling.pem")
	if err := os.Symlink(filepath.Join(dir, "gone"), link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	src := NewFileSourceWithHeader(dir, nil, nil, nil, nil)
	certs, err := src.ListCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	if len(certs) != 0 {
		t.Errorf("unreadable .pem should be skipped, got %+v", certs)
	}
}

func TestParseCertificate_MultiBlockLoop(t *testing.T) {
	path := "multi.pem"
	// A non-certificate block first, then a valid certificate: the loop
	// must advance through the remaining data.
	junk := pemEncodeText("NOTACERT", []byte("payload"))
	certBlock, _ := pem.Decode(genCertPEM(t, "multi.example.com", nil, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour)))
	if certBlock == nil {
		t.Fatal("expected PEM block")
	}
	data := append(append([]byte{}, junk...), pemEncodeCertificate(certBlock.Bytes)...)
	c := parseCertificate(path, data)
	if c.Subject != "multi.example.com" {
		t.Errorf("subject = %q, want certificate found after junk block", c.Subject)
	}

	// A certificate block with corrupt DER must be skipped, not panic.
	corrupt := pemEncodeText("CERTIFICATE", []byte("not-der"))
	c = parseCertificate(path, corrupt)
	if c.Subject != filepath.Base(path) {
		t.Errorf("corrupt DER should fall back to file name, got %q", c.Subject)
	}
	if c.RenewalState != "unknown" {
		t.Errorf("corrupt DER should leave renewal unknown, got %q", c.RenewalState)
	}
}

func TestParseCertificate_SubjectFallsBackToSAN(t *testing.T) {
	der, _ := pem.Decode(genCertPEM(t, "", []string{"san.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour)))
	if der == nil {
		t.Fatal("expected PEM block")
	}
	c := parseCertificate("san.pem", pemEncodeCertificate(der.Bytes))
	if c.Subject != "san.example.com" {
		t.Errorf("subject = %q, want first SAN", c.Subject)
	}
	if len(c.SANs) != 1 || c.SANs[0] != "san.example.com" {
		t.Errorf("SANs = %v", c.SANs)
	}
}

func TestParseCertificate_ExpiredAndValid(t *testing.T) {
	// Expired: NotAfter in the past.
	der, _ := pem.Decode(genCertPEM(t, "old.example.com", nil, time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour)))
	c := parseCertificate("old.pem", pemEncodeCertificate(der.Bytes))
	if c.RenewalState != "expired" {
		t.Errorf("expired cert state = %q", c.RenewalState)
	}

	// Valid: more than 30 days of remaining lifetime.
	der, _ = pem.Decode(genCertPEM(t, "fine.example.com", nil, time.Now().Add(-time.Hour), time.Now().Add(60*24*time.Hour)))
	c = parseCertificate("fine.pem", pemEncodeCertificate(der.Bytes))
	if c.RenewalState != "valid" {
		t.Errorf("valid cert state = %q", c.RenewalState)
	}
}
