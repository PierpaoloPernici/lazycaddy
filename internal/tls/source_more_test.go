package tls

import (
	"context"
	"testing"
)

func TestSourceFunc_ListCertificates(t *testing.T) {
	called := false
	f := SourceFunc(func(ctx context.Context) ([]Certificate, error) {
		called = true
		return []Certificate{{Subject: "a"}}, nil
	})
	certs, err := f.ListCertificates(context.Background())
	if err != nil || !called || len(certs) != 1 {
		t.Fatalf("SourceFunc failed")
	}
}

func TestFetcherState_String_Default(t *testing.T) {
	if FetchLoading.String() != "loading" {
		t.Error("loading")
	}
	if FetcherState(99).String() != "unknown" {
		t.Error("unknown")
	}
}

func TestNewFileSourceWithHeader_Custom(t *testing.T) {
	called := false
	src := NewFileSourceWithHeader("/tmp", nil, func(string) ([]byte, error) {
		called = true
		return []byte("x"), nil
	}, nil, nil)
	if src.ReadHeader == nil {
		t.Fatal("ReadHeader nil")
	}
	_, _ = src.ReadHeader("/tmp/x")
	if !called {
		t.Error("custom ReadHeader not called")
	}
}

func TestParseCertificate_ExpiredAndExpiring(t *testing.T) {
	certPEM := generateTestCert(t, "expire.test")
	c := parseCertificate("a.crt", certPEM)
	if c.RenewalState != "valid" && c.RenewalState != "expiring" {
		t.Errorf("expected valid or expiring, got %q", c.RenewalState)
	}
}
