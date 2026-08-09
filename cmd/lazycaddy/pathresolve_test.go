package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/config"
	"github.com/PierpaoloPernici/lazycaddy/internal/discover"
)

// fileInfo is a minimal os.FileInfo fake carrying only the mode, mirroring
// the fake in internal/discover; it keeps the wiring tests deterministic.
type fileInfo struct{ mode os.FileMode }

func (f fileInfo) Name() string       { return "" }
func (f fileInfo) Size() int64        { return 0 }
func (f fileInfo) Mode() os.FileMode  { return f.mode }
func (f fileInfo) ModTime() time.Time { return time.Time{} }
func (f fileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fileInfo) Sys() any           { return nil }

// fakeDeps returns a discover.Deps with deterministic seams: a local
// Caddyfile exists as a regular file, caddy is not on PATH, the home is
// /home/operator and no environment variables are set. Individual tests
// override the seam they exercise.
func fakeDeps() discover.Deps {
	return discover.Deps{
		Stat: func(path string) (os.FileInfo, error) {
			if path == "Caddyfile" {
				return fileInfo{mode: 0o644}, nil
			}
			return nil, os.ErrNotExist
		},
		LookPath:    func(string) (string, error) { return "", errors.New("not found") },
		UserHomeDir: func() (string, error) { return "/home/operator", nil },
		Getenv:      func(string) string { return "" },
	}
}

// failingDeps returns a discover.Deps where every discovery seam fails, to
// prove explicit flag values win even when discovery cannot succeed.
func failingDeps() discover.Deps {
	return discover.Deps{
		Stat:        func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		LookPath:    func(string) (string, error) { return "", errors.New("no caddy") },
		UserHomeDir: func() (string, error) { return "", errors.New("no home") },
		Getenv:      func(string) string { return "" },
	}
}

func TestResolvePaths_ExplicitFlagsWin(t *testing.T) {
	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)
	if err := cmd.ParseFlags([]string{
		"--config", "/srv/caddy/Caddyfile",
		"--caddy-path", "/usr/bin/caddy",
		"--backup-dir", "/srv/caddy/backups",
	}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := resolvePaths(cmd.Flags(), &settings, failingDeps()); err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	if settings.ConfigPath != "/srv/caddy/Caddyfile" {
		t.Errorf("ConfigPath = %q, want the explicit --config", settings.ConfigPath)
	}
	if settings.BinaryPath != "/usr/bin/caddy" {
		t.Errorf("BinaryPath = %q, want the explicit --caddy-path", settings.BinaryPath)
	}
	if settings.BackupDir != "/srv/caddy/backups" {
		t.Errorf("BackupDir = %q, want the explicit --backup-dir", settings.BackupDir)
	}
}

func TestResolvePaths_ConfigDiscoveryFindsLocalFirst(t *testing.T) {
	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)
	if err := resolvePaths(cmd.Flags(), &settings, fakeDeps()); err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	want, err := filepath.Abs("Caddyfile")
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	if settings.ConfigPath != want {
		t.Errorf("ConfigPath = %q, want %q (local Caddyfile, absolute)", settings.ConfigPath, want)
	}
}

func TestResolvePaths_ConfigDiscoveryFallsBackToSystem(t *testing.T) {
	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)
	deps := fakeDeps()
	deps.Stat = func(path string) (os.FileInfo, error) {
		if path == "/etc/caddy/Caddyfile" {
			return fileInfo{mode: 0o644}, nil
		}
		return nil, os.ErrNotExist
	}
	if err := resolvePaths(cmd.Flags(), &settings, deps); err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	if settings.ConfigPath != "/etc/caddy/Caddyfile" {
		t.Errorf("ConfigPath = %q, want /etc/caddy/Caddyfile", settings.ConfigPath)
	}
}

func TestResolvePaths_ConfigDiscoveryMissingReturnsClearError(t *testing.T) {
	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)
	deps := failingDeps()
	err := resolvePaths(cmd.Flags(), &settings, deps)
	if err == nil {
		t.Fatal("resolvePaths: expected a missing-file error, got nil")
	}
	for _, want := range []string{"no Caddyfile found", "./Caddyfile", "/etc/caddy/Caddyfile"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestResolvePaths_BinaryDiscoveryThroughPATH(t *testing.T) {
	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)
	deps := fakeDeps()
	deps.LookPath = func(file string) (string, error) {
		if file != "caddy" {
			return "", errors.New("unexpected lookup: " + file)
		}
		return "/usr/local/bin/caddy", nil
	}
	if err := resolvePaths(cmd.Flags(), &settings, deps); err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	if settings.BinaryPath != "/usr/local/bin/caddy" {
		t.Errorf("BinaryPath = %q, want the discovered path", settings.BinaryPath)
	}
}

func TestResolvePaths_BinaryDiscoveryUnavailableStaysDisabled(t *testing.T) {
	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)
	if err := resolvePaths(cmd.Flags(), &settings, fakeDeps()); err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	if settings.BinaryPath != "" {
		t.Errorf("BinaryPath = %q, want empty (format/validate/reload disabled)", settings.BinaryPath)
	}
}

func TestResolvePaths_BackupDirDefaultIsUserWritable(t *testing.T) {
	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)
	if err := resolvePaths(cmd.Flags(), &settings, fakeDeps()); err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	want := filepath.Join("/home/operator", ".local", "state", "lazycaddy", "backups")
	if settings.BackupDir != want {
		t.Errorf("BackupDir = %q, want %q (user-writable default)", settings.BackupDir, want)
	}
}

func TestResolvePaths_BackupDirHonorsXDGStateHome(t *testing.T) {
	settings := config.DefaultSettings()
	var write bool
	cmd := newRootCommand(&settings, &write)
	deps := fakeDeps()
	deps.Getenv = func(key string) string {
		if key == "XDG_STATE_HOME" {
			return "/var/lib/state"
		}
		return ""
	}
	if err := resolvePaths(cmd.Flags(), &settings, deps); err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	want := filepath.Join("/var/lib/state", "lazycaddy", "backups")
	if settings.BackupDir != want {
		t.Errorf("BackupDir = %q, want %q", settings.BackupDir, want)
	}
}
