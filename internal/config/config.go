// Package config holds CLI flags and application settings.
package config

// Settings is the resolved application configuration.
type Settings struct {
	// ConfigPath is the path of the Caddyfile to inspect.
	ConfigPath string
	// ReadOnly is always true in this milestone: the inspector never writes
	// the Caddyfile. Future milestones add explicit writable modes.
	ReadOnly bool
}

// DefaultConfigPath is the Caddyfile path used when --config is not given.
// It matches Caddy's convention of looking for a Caddyfile in the current
// directory.
func DefaultConfigPath() string {
	return "Caddyfile"
}

// DefaultSettings returns the default application settings: the default
// config path and an explicit read-only mode.
func DefaultSettings() Settings {
	return Settings{
		ConfigPath: DefaultConfigPath(),
		ReadOnly:   true,
	}
}
