package app

import (
	"context"

	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

// LogSource supplies structured Caddy log entries to the UI. UI models
// depend on this interface and never open files or tail directly.
type LogSource interface {
	// Next returns entries for complete new lines appended since the last
	// call. A nil error with nil entries means "no new lines".
	Next(ctx context.Context) ([]logs.Entry, error)
	// History returns the current bounded history, for seeding the view
	// when it opens.
	History() []logs.Entry
}

// LogSourceFunc adapts functions to the LogSource interface (mirrors the
// other app *Func adapters; tests use it).
type LogSourceFunc struct {
	NextFn    func(ctx context.Context) ([]logs.Entry, error)
	HistoryFn func() []logs.Entry
}

// Next implements LogSource.
func (f LogSourceFunc) Next(ctx context.Context) ([]logs.Entry, error) { return f.NextFn(ctx) }

// History implements LogSource.
func (f LogSourceFunc) History() []logs.Entry { return f.HistoryFn() }

// tailerSource adapts a *logs.Tailer to the LogSource boundary.
type tailerSource struct{ t *logs.Tailer }

// Next implements LogSource.
func (s tailerSource) Next(ctx context.Context) ([]logs.Entry, error) { return s.t.Next(ctx) }

// History implements LogSource.
func (s tailerSource) History() []logs.Entry { return s.t.Entries() }

// NewLogSource returns a LogSource backed by t.
func NewLogSource(t *logs.Tailer) LogSource { return tailerSource{t: t} }
