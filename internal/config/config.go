// Package config holds CLI flags and application settings.
package config

import "time"

// Settings is the resolved application configuration.
type Settings struct {
	// ConfigPath is the path of the Caddyfile to inspect.
	ConfigPath string
	// ReadOnly is true by default and set from the --write flag: the
	// inspector never writes the Caddyfile unless the operator opts in.
	ReadOnly bool
	// BackupDir is where backups are written. Empty means "derive the
	// default from ConfigPath" (the <config-dir>/.lazycaddy/backups/
	// directory); cmd/lazycaddy resolves that default before wiring.
	BackupDir string
	// BinaryPath is the absolute or PATH-relative path to the caddy
	// binary. Empty means "no binary configured": the TUI starts, but
	// format and validate are disabled until the operator provides a
	// path. The field is opt-in by design: the inspector must remain
	// useful in environments without caddy installed.
	BinaryPath string
	// ValidatorTimeout bounds each individual caddy invocation. A
	// non-positive value means "use the validator package default
	// (5s)". Set this from the CLI when a slower host or a remote
	// filesystem makes the default too tight.
	ValidatorTimeout time.Duration
}

// DefaultConfigPath is the Caddyfile path used when --config is not given.
// It matches Caddy's convention of looking for a Caddyfile in the current
// directory.
func DefaultConfigPath() string {
	return "Caddyfile"
}

// DefaultSettings returns the default application settings: the default
// config path, an explicit read-only mode and empty BinaryPath / zero
// ValidatorTimeout. The operator opts in to format and validate by
// supplying a caddy binary path.
func DefaultSettings() Settings {
	return Settings{
		ConfigPath: DefaultConfigPath(),
		ReadOnly:   true,
	}
}
