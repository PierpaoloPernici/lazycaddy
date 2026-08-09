// Package config holds CLI flags and application settings.
package config

import "time"

// Settings is the resolved application configuration.
type Settings struct {
	// ConfigPath is the path of the Caddyfile to inspect. When --config
	// is not given, cmd/lazycaddy discovers it: ./Caddyfile when present,
	// else /etc/caddy/Caddyfile.
	ConfigPath string
	// ReadOnly is true by default and set from the --write flag: the
	// inspector never writes the Caddyfile unless the operator opts in.
	ReadOnly bool
	// BackupDir is where backups are written. Empty means "use the
	// user-state default location" (~/.local/state/lazycaddy/backups,
	// honoring $XDG_STATE_HOME); cmd/lazycaddy resolves that default
	// before wiring. The default never derives from the config directory,
	// so system Caddyfiles do not require writing next to the config. It
	// is the default location, not a writability guarantee: the
	// save/backup pipeline reports any writability failure at save time.
	BackupDir string
	// BinaryPath is the absolute or PATH-relative path to the caddy
	// binary. Empty means "no binary available": the TUI starts, but
	// format and validate are disabled. When --caddy-path is not given,
	// cmd/lazycaddy discovers caddy through PATH and leaves this empty
	// when it is unavailable, so the inspector stays useful in
	// environments without caddy installed.
	BinaryPath string
	// ValidatorTimeout bounds each individual caddy invocation. A
	// non-positive value means "use the validator package default
	// (5s)". Set this from the CLI when a slower host or a remote
	// filesystem makes the default too tight.
	ValidatorTimeout time.Duration
	// AdminEndpoint is the base URL of the local Caddy Admin API used for
	// reloads. Defaults to Caddy's localhost:2019.
	AdminEndpoint string
	// AdminTimeout bounds a single Admin API request (e.g. a reload, which
	// can block until active connections drain). A non-positive value means
	// "use the runtime client default (30s)".
	AdminTimeout time.Duration
	// LogPath is the path of a Caddy log file to follow in the log view.
	// Empty means "no log source configured": the l keybinding is disabled
	// and the TUI stays fully browsable. Mutually exclusive with
	// JournalUnit.
	LogPath string
	// JournalUnit is the systemd journal unit to follow in the log view
	// (for example "caddy.service"). Empty means "no log source
	// configured": the l keybinding is disabled and the TUI stays fully
	// browsable. Mutually exclusive with LogPath.
	JournalUnit string
}

// DefaultConfigPath is the Caddyfile path used as the --config flag
// default. It matches Caddy's convention of looking for a Caddyfile in the
// current directory and is the first path-discovery candidate; when the
// flag is not given explicitly, discovery resolves the effective path.
func DefaultConfigPath() string {
	return "Caddyfile"
}

// DefaultSettings returns the default application settings: the default
// config path, an explicit read-only mode, empty BinaryPath / zero
// ValidatorTimeout, and the standard local Admin API endpoint and request
// timeout. The operator opts in to format and validate by supplying a caddy
// binary path.
func DefaultSettings() Settings {
	return Settings{
		ConfigPath:    DefaultConfigPath(),
		ReadOnly:      true,
		AdminEndpoint: "http://localhost:2019",
		AdminTimeout:  30 * time.Second,
	}
}
