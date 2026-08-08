package app

import (
	"context"

	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
)

// RuntimeStatus reports Caddy's version and detected capabilities. UI
// models depend on this interface and never probe the binary or Admin API
// directly.
type RuntimeStatus interface {
	// Probe reports Caddy's runtime state as far as lazycaddy can prove
	// it. It never returns an error: probe failures degrade to explicit
	// unknown/stopped states so the TUI stays browsable read-only.
	Probe(ctx context.Context) runtime.Report
}

// RuntimeStatusFunc adapts a function to the RuntimeStatus interface
// (mirrors LoaderFunc / FormatterFunc / ReloaderFunc).
type RuntimeStatusFunc func(ctx context.Context) runtime.Report

// Probe implements RuntimeStatus.
func (f RuntimeStatusFunc) Probe(ctx context.Context) runtime.Report { return f(ctx) }
