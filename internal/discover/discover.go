// Package discover implements v0.2 path discovery and sensible defaults.
// When the operator does not pass an explicit --config, --caddy-path or
// --backup-dir, the resolver finds a Caddyfile, locates the caddy binary
// and picks a user-writable backup location. Every external lookup flows
// through injectable seams (Deps) so the resolution rules are deterministic
// under test; production wiring uses DefaultDeps.
package discover

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Deps are the injectable seams the resolver uses to probe the environment.
// Production wiring passes DefaultDeps; tests substitute fakes per seam and
// leave the others nil, which the resolver fills from the defaults.
type Deps struct {
	// Stat returns the file info for a path, or the error from the
	// underlying filesystem call (typically os.Stat). The resolver
	// inspects both the info and the error: only os.ErrNotExist counts
	// as "candidate absent", permission and other errors are surfaced.
	Stat func(path string) (os.FileInfo, error)
	// LookPath resolves an executable name through PATH
	// (typically exec.LookPath).
	LookPath func(file string) (string, error)
	// UserHomeDir returns the current user's home directory
	// (typically os.UserHomeDir).
	UserHomeDir func() (string, error)
	// Getenv reads an environment variable (typically os.Getenv).
	Getenv func(key string) string
}

// DefaultDeps returns the production seams backed by the real OS.
func DefaultDeps() Deps {
	return Deps{
		Stat:        os.Stat,
		LookPath:    exec.LookPath,
		UserHomeDir: os.UserHomeDir,
		Getenv:      os.Getenv,
	}
}

// fill returns d with every nil seam replaced by its production default, so
// a partially populated Deps (as in tests) behaves sensibly.
func (d Deps) fill() Deps {
	def := DefaultDeps()
	if d.Stat == nil {
		d.Stat = def.Stat
	}
	if d.LookPath == nil {
		d.LookPath = def.LookPath
	}
	if d.UserHomeDir == nil {
		d.UserHomeDir = def.UserHomeDir
	}
	if d.Getenv == nil {
		d.Getenv = def.Getenv
	}
	return d
}

// Resolver computes effective paths and defaults. The zero value uses the
// production seams.
type Resolver struct {
	Deps Deps
}

// ConfigCandidates are the Caddyfile locations tried in order when --config
// is not supplied: a local Caddyfile first (Caddy's own convention), then
// the system location used by packaged Caddy installs.
var ConfigCandidates = []string{
	"Caddyfile",
	"/etc/caddy/Caddyfile",
}

// configCandidatesDisplay is the human-readable form of ConfigCandidates,
// used in the missing-file error and flag help text.
var configCandidatesDisplay = []string{"./Caddyfile", "/etc/caddy/Caddyfile"}

// ConfigPath returns the effective Caddyfile path. When explicit is true
// (the operator passed --config) value is returned unchanged and takes
// precedence over discovery. Otherwise the candidates are tried in order
// (./Caddyfile, then /etc/caddy/Caddyfile) and the first existing regular
// file is returned as an absolute path. Only os.ErrNotExist means
// "candidate absent": any other stat error (for example a permission
// failure on ./Caddyfile) is surfaced instead of silently falling back, a
// candidate that exists but is not a regular file (directory, device,
// socket, ...) is an error rather than a selection, and when no candidate
// exists a clear missing-file error names every location tried.
func (r Resolver) ConfigPath(explicit bool, value string) (string, error) {
	if explicit {
		return value, nil
	}
	d := r.Deps.fill()
	for _, candidate := range ConfigCandidates {
		info, err := d.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("cannot inspect config candidate %s: %w", candidate, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("config candidate %s is a %s, not a regular file",
				candidate, modeKind(info.Mode()))
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return "", fmt.Errorf("cannot resolve %s to an absolute path: %w", candidate, err)
		}
		return abs, nil
	}
	return "", fmt.Errorf("no Caddyfile found: tried %s (pass --config to point at an existing file)",
		strings.Join(configCandidatesDisplay, " and "))
}

// modeKind names a non-regular mode for error messages.
func modeKind(m os.FileMode) string {
	if m.IsDir() {
		return "directory"
	}
	return "non-regular file"
}

// BinaryPath returns the effective caddy binary path. When explicit is
// true, value is returned unchanged. Otherwise "caddy" is resolved through
// PATH; an empty result (not found) keeps formatting, validation and reload
// disabled without failing startup.
func (r Resolver) BinaryPath(explicit bool, value string) string {
	if explicit {
		return value
	}
	d := r.Deps.fill()
	path, err := d.LookPath("caddy")
	if err != nil {
		return ""
	}
	return path
}

// BackupDir returns the effective backup directory. When explicit is true,
// value is returned unchanged and takes precedence. Otherwise a
// user-writable default location is used: $XDG_STATE_HOME/lazycaddy/backups,
// or ~/.local/state/lazycaddy/backups when XDG_STATE_HOME is unset. The
// default never derives from the config directory, because system
// Caddyfiles under /etc/caddy live in directories the operator usually
// cannot write to. This is the default location, not a writability
// guarantee: an arbitrary XDG_STATE_HOME may point somewhere the operator
// cannot write, and the save/backup pipeline is responsible for reporting
// any writability failure.
func (r Resolver) BackupDir(explicit bool, value string) (string, error) {
	if explicit {
		return value, nil
	}
	d := r.Deps.fill()
	home, err := d.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine the default backup directory: %w", err)
	}
	stateHome := d.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "lazycaddy", "backups"), nil
}
