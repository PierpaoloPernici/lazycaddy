package tls

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FetcherState mirrors runtime.FetchState so the TLS panel can report
// the same explicit lifecycle without importing the runtime package.
// The values are intentionally aligned.
type FetcherState int

const (
	FetchLoading FetcherState = iota
	FetchAvailable
	FetchStale
	FetchUnavailable
)

func (s FetcherState) String() string {
	switch s {
	case FetchLoading:
		return "loading"
	case FetchAvailable:
		return "available"
	case FetchStale:
		return "stale"
	case FetchUnavailable:
		return "unavailable"
	default:
		return "unknown"
	}
}

// Snapshot is the per-panel state for the TLS dashboard.
type Snapshot struct {
	State        FetcherState
	Certificates []Certificate
	Err          error
	FetchedAt    time.Time
	StorageDir   string
}

// Certificate is one certificate as surfaced in the TLS panel.
// Storage location, renewal state and OCSP state are distinct values so
// the panel never conflates them; private key material is never read.
type Certificate struct {
	// Subject is the certificate subject (CommonName or first SAN).
	Subject string
	// Issuer is the issuer.
	Issuer string
	// SANs are the Subject Alternative Names.
	SANs []string
	// NotBefore and NotAfter bound the validity period.
	NotBefore time.Time
	NotAfter  time.Time
	// StoragePath is the file that holds the certificate (e.g.
	// certificates/acme-v02.api.letsencrypt.org-directory/example.com/example.com.crt).
	StoragePath string
	// JSONPath is the sidecar JSON that holds CertMagic metadata
	// (certificates/.../*.json) when present; empty when absent.
	JSONPath string
	// RenewalState is "valid", "expiring", "expired" or "unknown".
	RenewalState string
	// OCSPState is "good", "revoked", "unknown" or the raw status when
	// the sidecar reports it.
	OCSPState string
	// Locked reports whether the storage indicates a lock file is present.
	Locked bool
}

// Source lists certificates behind a narrow filesystem boundary so the UI
// can be tested without touching the real CertMagic layout.
type Source interface {
	ListCertificates(ctx context.Context) ([]Certificate, error)
}

// SourceFunc adapts a function to the Source interface.
type SourceFunc func(ctx context.Context) ([]Certificate, error)

// ListCertificates implements Source.
func (f SourceFunc) ListCertificates(ctx context.Context) ([]Certificate, error) {
	return f(ctx)
}

// Sentinel errors for TLS storage failures.
var (
	ErrStorageUnavailable = errors.New("TLS storage unavailable")
	ErrStorageLocked      = errors.New("TLS storage locked")
)

// FileSource reads certificates from a directory on disk. It does not
// assume a private CertMagic layout: it walks the storage dir and tries
// to parse every *.crt/*.pem/*.cer file it finds, and it tolerates
// missing sidecars, permission errors and partial parses by surfacing
// them as unavailable states rather than blocking the TUI.
type FileSource struct {
	Dir      string
	ReadFile func(string) ([]byte, error)
	ReadDir  func(string) ([]os.DirEntry, error)
	Stat     func(string) (os.FileInfo, error)
}

// NewFileSource returns a FileSource for dir. Nil callbacks default to
// os.ReadFile/os.ReadDir/os.Stat so tests can inject a fake filesystem.
func NewFileSource(dir string, readFile func(string) ([]byte, error), readDir func(string) ([]os.DirEntry, error), stat func(string) (os.FileInfo, error)) *FileSource {
	if readFile == nil {
		readFile = os.ReadFile
	}
	if readDir == nil {
		readDir = os.ReadDir
	}
	if stat == nil {
		stat = os.Stat
	}
	return &FileSource{Dir: dir, ReadFile: readFile, ReadDir: readDir, Stat: stat}
}

// ListCertificates implements Source.
func (s *FileSource) ListCertificates(ctx context.Context) ([]Certificate, error) {
	if s.Dir == "" {
		return nil, ErrStorageUnavailable
	}
	if _, err := s.Stat(s.Dir); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrStorageUnavailable
		}
		if os.IsPermission(err) {
			return nil, ErrStorageUnavailable
		}
		return nil, err
	}
	var out []Certificate
	err := filepath.WalkDir(s.Dir, func(path string, d os.DirEntry, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Surface a lock marker without reading private material.
			if d.Name() == ".lock" || strings.HasSuffix(d.Name(), ".lock") {
				// Record a synthetic locked entry? For now just note
				// that storage is locked; the caller can surface it.
				// We do not abort the walk.
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".crt" && ext != ".pem" && ext != ".cer" {
			return nil
		}
		b, err := s.ReadFile(path)
		if err != nil {
			if os.IsPermission(err) {
				out = append(out, Certificate{StoragePath: path, RenewalState: "unknown", OCSPState: "unknown"})
				return nil
			}
			return nil
		}
		cert := parseCertificate(path, b)
		// Try to enrich from sidecar JSON (same base name, .json).
		jsonPath := strings.TrimSuffix(path, ext) + ".json"
		if jb, err := s.ReadFile(jsonPath); err == nil {
			enrichFromSidecar(&cert, jb, jsonPath)
		}
		out = append(out, cert)
		return nil
	})
	if err != nil {
		return out, err
	}
	return out, nil
}

func parseCertificate(path string, data []byte) Certificate {
	c := Certificate{StoragePath: path, RenewalState: "unknown", OCSPState: "unknown"}
	// Try PEM first.
	var der *x509.Certificate
	for {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
				der = cert
				break
			}
		}
		data = rest
		if len(data) == 0 {
			break
		}
	}
	if der != nil {
		c.Subject = der.Subject.CommonName
		if c.Subject == "" && len(der.DNSNames) > 0 {
			c.Subject = der.DNSNames[0]
		}
		c.Issuer = der.Issuer.CommonName
		c.SANs = der.DNSNames
		c.NotBefore = der.NotBefore
		c.NotAfter = der.NotAfter
		now := time.Now()
		switch {
		case now.After(der.NotAfter):
			c.RenewalState = "expired"
		case der.NotAfter.Sub(now) < 30*24*time.Hour:
			c.RenewalState = "expiring"
		default:
			c.RenewalState = "valid"
		}
	} else {
		c.Subject = filepath.Base(path)
	}
	return c
}

func enrichFromSidecar(c *Certificate, data []byte, jsonPath string) {
	var sidecar struct {
		OCSP string `json:"ocsp_status"`
	}
	if err := json.Unmarshal(data, &sidecar); err == nil {
		if sidecar.OCSP != "" {
			c.OCSPState = sidecar.OCSP
		}
	}
	c.JSONPath = jsonPath
	// Preservation: the JSON sidecar is the source of OCSP/renewal hints;
	// we never infer renewal from a single .crt alone beyond NotAfter.
}
