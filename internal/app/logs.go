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
	// Close releases the source's underlying resources (a followed file
	// handle or a spawned journalctl process). It is idempotent and must
	// be safe to call more than once.
	Close() error
}

// LogSourceFunc adapts functions to the LogSource interface (mirrors the
// other app *Func adapters; tests use it).
type LogSourceFunc struct {
	NextFn    func(ctx context.Context) ([]logs.Entry, error)
	HistoryFn func() []logs.Entry
	// CloseFn is an optional close hook; when nil, Close is a no-op.
	CloseFn func() error
}

// Next implements LogSource.
func (f LogSourceFunc) Next(ctx context.Context) ([]logs.Entry, error) { return f.NextFn(ctx) }

// History implements LogSource.
func (f LogSourceFunc) History() []logs.Entry { return f.HistoryFn() }

// Close implements LogSource, delegating to CloseFn when set and otherwise
// doing nothing.
func (f LogSourceFunc) Close() error {
	if f.CloseFn != nil {
		return f.CloseFn()
	}
	return nil
}

// tailerSource adapts a *logs.Tailer to the LogSource boundary.
type tailerSource struct{ t *logs.Tailer }

// Next implements LogSource.
func (s tailerSource) Next(ctx context.Context) ([]logs.Entry, error) { return s.t.Next(ctx) }

// History implements LogSource.
func (s tailerSource) History() []logs.Entry { return s.t.Entries() }

// Close implements LogSource, delegating to the Tailer's idempotent Close.
func (s tailerSource) Close() error { return s.t.Close() }

// NewLogSource returns a LogSource backed by t.
func NewLogSource(t *logs.Tailer) LogSource { return tailerSource{t: t} }

// journalSource adapts a *logs.JournalSource to the LogSource boundary.
type journalSource struct{ j *logs.JournalSource }

// Next implements LogSource.
func (s journalSource) Next(ctx context.Context) ([]logs.Entry, error) { return s.j.Next(ctx) }

// History implements LogSource.
func (s journalSource) History() []logs.Entry { return s.j.Entries() }

// Close implements LogSource, delegating to the JournalSource's idempotent
// Close (which kills the current journalctl process and stops the
// supervisor).
func (s journalSource) Close() error { return s.j.Close() }

// NewJournalLogSource returns a LogSource backed by the systemd journal
// source j.
func NewJournalLogSource(j *logs.JournalSource) LogSource { return journalSource{j: j} }
