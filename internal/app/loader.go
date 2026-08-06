// Package app owns the application state and orchestration: loading a
// Caddyfile, resolving its imports and exposing the resulting state to the
// UI through a small interface. The package never writes files, never
// executes commands and never talks to Caddy; read access is injected as a
// plain function so the whole layer is testable without a real filesystem.
package app

import (
	"fmt"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/config"
)

// FileReader reads a file by path. It is the only I/O the app layer performs,
// and it is injected so tests can serve an in-memory filesystem.
type FileReader func(path string) ([]byte, error)

// State is the immutable result of loading a configuration: the resolved
// import graph plus the settings it was loaded with. It is what the UI
// renders; it contains no terminal state.
type State struct {
	Settings config.Settings
	// Graph is always non-nil after a successful read, even when parsing
	// found errors, so the raw source view stays available.
	Graph *caddyfile.ImportGraph
}

// Loader produces a State for the UI. UI models depend on this interface and
// never touch the filesystem or caddyfile package directly.
type Loader interface {
	LoadState() (*State, error)
}

// LoaderFunc adapts a function to the Loader interface.
type LoaderFunc func() (*State, error)

// LoadState implements Loader.
func (f LoaderFunc) LoadState() (*State, error) { return f() }

// NewLoader returns a Loader that reads and resolves the configured
// Caddyfile through readFile. A read error is returned as the LoadState
// error; parse and resolution errors are reported inside State.Graph (and
// its root document) so the raw view remains available.
func NewLoader(settings config.Settings, readFile FileReader) Loader {
	return LoaderFunc(func() (*State, error) {
		src, err := readFile(settings.ConfigPath)
		if err != nil {
			return &State{Settings: settings}, fmt.Errorf("read config %s: %w", settings.ConfigPath, err)
		}
		graph := caddyfile.Resolve(settings.ConfigPath, src, readFile)
		return &State{Settings: settings, Graph: graph}, nil
	})
}
