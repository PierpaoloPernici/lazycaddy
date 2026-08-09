package discover

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fileInfo is a minimal os.FileInfo fake carrying only the mode, which is
// all ConfigPath inspects (regular-file vs directory vs other).
type fileInfo struct {
	mode os.FileMode
}

func (f fileInfo) Name() string       { return "" }
func (f fileInfo) Size() int64        { return 0 }
func (f fileInfo) Mode() os.FileMode  { return f.mode }
func (f fileInfo) ModTime() time.Time { return time.Time{} }
func (f fileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fileInfo) Sys() any           { return nil }

func TestConfigPath_ExplicitTakesPrecedence(t *testing.T) {
	// Stat must not be consulted at all for an explicit --config.
	r := Resolver{Deps: Deps{Stat: func(string) (os.FileInfo, error) {
		t.Fatal("Stat must not be called for an explicit config")
		return nil, nil
	}}}
	got, err := r.ConfigPath(true, "/custom/Caddyfile")
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if got != "/custom/Caddyfile" {
		t.Errorf("ConfigPath = %q, want the explicit value", got)
	}
}

func TestConfigPath_LocalWinsOverSystem(t *testing.T) {
	r := Resolver{Deps: Deps{Stat: func(p string) (os.FileInfo, error) {
		switch p {
		case "Caddyfile", "/etc/caddy/Caddyfile":
			return fileInfo{mode: 0o644}, nil
		default:
			return nil, os.ErrNotExist
		}
	}}}
	got, err := r.ConfigPath(false, "Caddyfile")
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	want, err := filepath.Abs("Caddyfile")
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	if got != want {
		t.Errorf("ConfigPath = %q, want %q (local Caddyfile, absolute)", got, want)
	}
}

func TestConfigPath_LocalMissingFallsBackToSystem(t *testing.T) {
	r := Resolver{Deps: Deps{Stat: func(p string) (os.FileInfo, error) {
		if p == "/etc/caddy/Caddyfile" {
			return fileInfo{mode: 0o644}, nil
		}
		return nil, os.ErrNotExist
	}}}
	got, err := r.ConfigPath(false, "Caddyfile")
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if got != "/etc/caddy/Caddyfile" {
		t.Errorf("ConfigPath = %q, want /etc/caddy/Caddyfile", got)
	}
}

func TestConfigPath_UnreadableLocalDoesNotFallBack(t *testing.T) {
	r := Resolver{Deps: Deps{Stat: func(p string) (os.FileInfo, error) {
		if p == "Caddyfile" {
			return nil, os.ErrPermission
		}
		if p == "/etc/caddy/Caddyfile" {
			return fileInfo{mode: 0o644}, nil
		}
		return nil, os.ErrNotExist
	}}}
	_, err := r.ConfigPath(false, "Caddyfile")
	if err == nil {
		t.Fatal("ConfigPath: expected an error for an unreadable local Caddyfile, got nil")
	}
	if !strings.Contains(err.Error(), "Caddyfile") {
		t.Errorf("error %q does not name the unreadable local candidate", err)
	}
}

func TestConfigPath_LocalDirectoryIsError(t *testing.T) {
	r := Resolver{Deps: Deps{Stat: func(p string) (os.FileInfo, error) {
		if p == "Caddyfile" {
			return fileInfo{mode: os.ModeDir | 0o755}, nil
		}
		return nil, os.ErrNotExist
	}}}
	_, err := r.ConfigPath(false, "Caddyfile")
	if err == nil {
		t.Fatal("ConfigPath: expected an error for a directory candidate, got nil")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error %q does not say the candidate is not a regular file", err)
	}
}

func TestConfigPath_SystemCandidateNonRegularIsError(t *testing.T) {
	r := Resolver{Deps: Deps{Stat: func(p string) (os.FileInfo, error) {
		if p == "/etc/caddy/Caddyfile" {
			return fileInfo{mode: os.ModeSocket}, nil
		}
		return nil, os.ErrNotExist
	}}}
	_, err := r.ConfigPath(false, "Caddyfile")
	if err == nil {
		t.Fatal("ConfigPath: expected an error for a non-regular system candidate, got nil")
	}
	if !strings.Contains(err.Error(), "/etc/caddy/Caddyfile") {
		t.Errorf("error %q does not name the non-regular system candidate", err)
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error %q does not say the candidate is not a regular file", err)
	}
}

func TestConfigPath_MissingBothReturnsClearError(t *testing.T) {
	r := Resolver{Deps: Deps{Stat: func(string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}}}
	_, err := r.ConfigPath(false, "Caddyfile")
	if err == nil {
		t.Fatal("ConfigPath: expected an error, got nil")
	}
	for _, want := range []string{"no Caddyfile found", "./Caddyfile", "/etc/caddy/Caddyfile"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestConfigPath_OtherStatErrorIsSurfaced(t *testing.T) {
	// A non-ErrNotExist stat failure that is not a permission error must
	// still abort discovery instead of being treated as "absent".
	other := errors.New("filesystem exploded")
	r := Resolver{Deps: Deps{Stat: func(string) (os.FileInfo, error) {
		return nil, other
	}}}
	_, err := r.ConfigPath(false, "Caddyfile")
	if err == nil {
		t.Fatal("ConfigPath: expected an error, got nil")
	}
	if !errors.Is(err, other) {
		t.Errorf("error %v does not wrap the stat failure %v", err, other)
	}
}

func TestBinaryPath_ExplicitTakesPrecedence(t *testing.T) {
	r := Resolver{Deps: Deps{LookPath: func(string) (string, error) {
		return "", errors.New("must not be called")
	}}}
	if got := r.BinaryPath(true, "/usr/bin/caddy"); got != "/usr/bin/caddy" {
		t.Errorf("BinaryPath = %q, want the explicit value", got)
	}
}

func TestBinaryPath_DiscoveredThroughPATH(t *testing.T) {
	r := Resolver{Deps: Deps{LookPath: func(file string) (string, error) {
		if file != "caddy" {
			t.Errorf("LookPath file = %q, want caddy", file)
		}
		return "/usr/local/bin/caddy", nil
	}}}
	if got := r.BinaryPath(false, ""); got != "/usr/local/bin/caddy" {
		t.Errorf("BinaryPath = %q, want the discovered path", got)
	}
}

func TestBinaryPath_UnavailableStaysEmpty(t *testing.T) {
	r := Resolver{Deps: Deps{LookPath: func(string) (string, error) {
		return "", os.ErrNotExist
	}}}
	if got := r.BinaryPath(false, ""); got != "" {
		t.Errorf("BinaryPath = %q, want empty (format/validate/reload disabled)", got)
	}
}

func TestBackupDir_ExplicitTakesPrecedence(t *testing.T) {
	r := Resolver{Deps: Deps{UserHomeDir: func() (string, error) {
		return "", errors.New("must not be called")
	}}}
	got, err := r.BackupDir(true, "/custom/backups")
	if err != nil {
		t.Fatalf("BackupDir: %v", err)
	}
	if got != "/custom/backups" {
		t.Errorf("BackupDir = %q, want the explicit value", got)
	}
}

func TestBackupDir_DefaultUnderHome(t *testing.T) {
	r := Resolver{Deps: Deps{
		UserHomeDir: func() (string, error) { return "/home/op", nil },
		Getenv:      func(string) string { return "" },
	}}
	got, err := r.BackupDir(false, "")
	if err != nil {
		t.Fatalf("BackupDir: %v", err)
	}
	want := filepath.Join("/home/op", ".local", "state", "lazycaddy", "backups")
	if got != want {
		t.Errorf("BackupDir = %q, want %q", got, want)
	}
}

func TestBackupDir_HonorsXDGStateHome(t *testing.T) {
	r := Resolver{Deps: Deps{
		UserHomeDir: func() (string, error) { return "/home/op", nil },
		Getenv: func(key string) string {
			if key == "XDG_STATE_HOME" {
				return "/state"
			}
			return ""
		},
	}}
	got, err := r.BackupDir(false, "")
	if err != nil {
		t.Fatalf("BackupDir: %v", err)
	}
	if got != filepath.Join("/state", "lazycaddy", "backups") {
		t.Errorf("BackupDir = %q, want /state/lazycaddy/backups", got)
	}
}

func TestBackupDir_HomeErrorIsSurfaced(t *testing.T) {
	r := Resolver{Deps: Deps{UserHomeDir: func() (string, error) {
		return "", errors.New("no home")
	}}}
	if _, err := r.BackupDir(false, ""); err == nil {
		t.Fatal("BackupDir: expected an error, got nil")
	}
}
