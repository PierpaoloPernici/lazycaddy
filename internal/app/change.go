package app

import (
	"context"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/watch"
)

// ExternalChange is a detected external modification of one configuration
// document. OnDisk holds the bytes read from disk during detection and is
// nil when the file could not be read (Missing).
type ExternalChange = watch.Change

// ChangeTarget pairs a document path with its in-memory source: the
// reference bytes the change monitor compares against on-disk content.
type ChangeTarget = watch.Target

// ErrChangeClosed reports that the change monitor was closed.
var ErrChangeClosed = watch.ErrClosed

// ErrChangeBusy reports that another Next call is already waiting.
var ErrChangeBusy = watch.ErrBusy

// ChangeMonitor detects external changes to the configuration documents
// the UI has open. UI models depend on this interface and never touch
// fsnotify, timers or the filesystem; the implementation lives behind it.
// The existing synchronous conflict guards (the saver's ErrConflict, the
// editor preflight and the reloader preflight) remain the final safety
// net: this monitor is an early notification, never a write gate.
type ChangeMonitor interface {
	// Update re-targets the monitor at the current documents: the
	// watched file set, the reference bytes (each document's in-memory
	// source), the directory watches and the pending state. An error
	// (for example an unreadable directory) leaves the previous target
	// set intact and callers should treat the monitor as unavailable.
	Update(targets []ChangeTarget) error
	// Next blocks until a meaningful external change is detected, the
	// monitor is closed (ErrChangeClosed) or ctx is done. At most one
	// Next may be in flight; concurrent calls return ErrChangeBusy.
	Next(ctx context.Context) (ExternalChange, error)
	// Close releases the monitor and unblocks a pending Next with
	// ErrChangeClosed. It is idempotent.
	Close() error
}

// ChangeTargets maps the resolved documents of a graph to change-monitor
// targets: the root document first, then every imported file, each with
// its own path and in-memory source, so the monitor can identify exactly
// which file changed.
func ChangeTargets(docs []*caddyfile.Document) []ChangeTarget {
	var targets []ChangeTarget
	for _, doc := range docs {
		if doc == nil {
			continue
		}
		targets = append(targets, ChangeTarget{Path: doc.Path, Source: doc.Source})
	}
	return targets
}
