// Package watch detects external modifications to the configuration
// documents the TUI has open, without the UI touching fsnotify, timers
// or the filesystem. The Monitor watches the parent directory of every
// resolved document, coalesces event bursts, re-reads the affected
// files and reports only byte differences against the in-memory
// snapshots seeded by Update.
package watch

import (
	"fmt"

	"github.com/fsnotify/fsnotify"
)

// Op is a filesystem operation reported by a Watcher.
type Op uint32

const (
	// OpWrite reports a modification of an existing entry.
	OpWrite Op = 1 << iota
	// OpCreate reports the creation of a new entry.
	OpCreate
	// OpRemove reports the removal of an entry.
	OpRemove
	// OpRename reports the rename of an entry.
	OpRename
)

// Event is one filesystem notification. Path identifies the entry that
// changed: a watched document, or a watched directory (some platforms
// report a single directory-level event for any change inside it).
type Event struct {
	Path string
	Op   Op
}

// Watcher is the filesystem subscription seam. The production
// implementation wraps fsnotify and watches directories; tests inject a
// fake so the monitor logic is deterministic.
type Watcher interface {
	// Add watches the path (a directory) for changes. It is idempotent:
	// adding an already-watched path is a no-op.
	Add(path string) error
	// Remove stops watching the path.
	Remove(path string) error
	// Events delivers filesystem events. The channel is closed by Close.
	Events() <-chan Event
	// Errors delivers watcher failures (for example a watched directory
	// that was deleted). The channel is closed by Close.
	Errors() <-chan error
	// Close releases the watcher and closes both channels. It is
	// idempotent.
	Close() error
}

// NewWatcher returns a production Watcher backed by fsnotify.
func NewWatcher() (Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("fsnotify: %w", err)
	}
	return &fsWatcher{w: fw}, nil
}

// fsWatcher adapts an fsnotify.Watcher to the Watcher interface. It
// forwards events and errors verbatim; the monitor interprets them.
type fsWatcher struct {
	w *fsnotify.Watcher
}

// Add implements Watcher.
func (f *fsWatcher) Add(path string) error {
	if err := f.w.Add(path); err != nil {
		return fmt.Errorf("fsnotify add %s: %w", path, err)
	}
	return nil
}

// Remove implements Watcher.
func (f *fsWatcher) Remove(path string) error { return f.w.Remove(path) }

// Events implements Watcher: fsnotify events are mapped to watch.Op
// bits and forwarded on a dedicated channel so the monitor never
// depends on fsnotify types.
func (f *fsWatcher) Events() <-chan Event {
	out := make(chan Event)
	go func() {
		defer close(out)
		for ev := range f.w.Events {
			out <- mapEvent(ev)
		}
	}()
	return out
}

func mapEvent(ev fsnotify.Event) Event {
	var op Op
	if ev.Has(fsnotify.Write) {
		op |= OpWrite
	}
	if ev.Has(fsnotify.Create) {
		op |= OpCreate
	}
	if ev.Has(fsnotify.Remove) {
		op |= OpRemove
	}
	if ev.Has(fsnotify.Rename) {
		op |= OpRename
	}
	return Event{Path: ev.Name, Op: op}
}

// Errors implements Watcher.
func (f *fsWatcher) Errors() <-chan error { return f.w.Errors }

// Close implements Watcher.
func (f *fsWatcher) Close() error { return f.w.Close() }
