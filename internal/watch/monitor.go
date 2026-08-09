package watch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrClosed is returned by Next after Close, and by Update on a closed
// monitor.
var ErrClosed = errors.New("watch monitor closed")

// ErrBusy is returned by Next when another Next call is already waiting.
var ErrBusy = errors.New("watch monitor busy")

// defaultQuiet is the debounce window used when Options.Quiet is zero:
// bursts of events produced by atomic saves and editors are coalesced
// into a single flush after the stream goes quiet.
const defaultQuiet = 300 * time.Millisecond

// Change is one detected external modification of a watched document.
// OnDisk holds the bytes read from disk during detection; it is nil when
// the file could not be read (Missing). A change is reported only when
// OnDisk differs from the reference snapshot for that path, so events
// that did not alter the bytes (touches, editor save loops that produce
// identical content) never surface.
type Change struct {
	Path    string
	OnDisk  []byte
	Missing bool
}

// Target is one document to watch: its path and the reference bytes the
// monitor compares against (the in-memory source of the loaded
// document).
type Target struct {
	Path   string
	Source []byte
}

// Clock is the debounce timer seam; the production value uses time.After
// and tests inject a fake so no real timing is involved.
type Clock interface {
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

// After implements Clock.
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// ReadFileFunc reads a file's bytes; the production value is os.ReadFile.
type ReadFileFunc func(path string) ([]byte, error)

// Options configures a Monitor.
type Options struct {
	// Watcher is the filesystem subscription. Required.
	Watcher Watcher
	// ReadFile re-reads a watched document during a flush. Required.
	ReadFile ReadFileFunc
	// Clock drives the debounce timer; nil uses time.After.
	Clock Clock
	// Quiet is the debounce window; zero uses a 300ms default.
	Quiet time.Duration
}

// Monitor detects meaningful external changes to a set of watched
// documents. It subscribes to directory-level events through Watcher,
// coalesces bursts into a quiet-period flush, re-reads the affected
// files, re-registers directory watches (idempotent, restoring watches
// broken by a replacement) and reports only byte differences against the
// reference snapshots seeded by Update.
//
// Lifecycle: NewMonitor, Start, Update (re-target after every graph
// change), Next (at most one call in flight), Close. The event loop is a
// single goroutine; every method is safe for concurrent use.
type Monitor struct {
	watcher Watcher
	read    ReadFileFunc
	clock   Clock
	quiet   time.Duration

	mu      sync.Mutex
	dirs    map[string]struct{} // watched parent directories
	watched map[string]struct{} // cleaned watched file paths
	snap    map[string][]byte   // path -> reference bytes (in-memory source)
	pending map[string]struct{} // paths touched by events in the current window
	timer   <-chan time.Time    // debounce timer armed by handleEvent
	queue   chan result         // buffered, latest-wins delivery to Next
	busy    bool                // a Next call is in flight
	started bool
	done    chan struct{}
	closed  bool
}

type result struct {
	change Change
	err    error
}

// NewMonitor returns a Monitor configured with opts. The event loop is
// started by Start.
func NewMonitor(opts Options) *Monitor {
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}
	quiet := opts.Quiet
	if quiet <= 0 {
		quiet = defaultQuiet
	}
	return &Monitor{
		watcher: opts.Watcher,
		read:    opts.ReadFile,
		clock:   clock,
		quiet:   quiet,
		dirs:    map[string]struct{}{},
		watched: map[string]struct{}{},
		snap:    map[string][]byte{},
		pending: map[string]struct{}{},
		queue:   make(chan result, queueCap),
		done:    make(chan struct{}),
	}
}

// queueCap bounds the pending result batch: a single flush can detect
// several changed files at once (for example a bulk replacement), and the
// UI resolves them one by one.
const queueCap = 8

// Start launches the event loop. It is idempotent; Close stops the loop.
func (m *Monitor) Start() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()
	go m.loop()
}

// loop consumes watcher events and errors, arms and fires the debounce
// timer and exits when the monitor is closed or the watcher's event
// channel closes. The event and error channels are captured once: every
// call to Events() or Errors() would spawn a new forwarding goroutine
// and lose events. The timer channel is copied under the lock because
// flush replaces it.
func (m *Monitor) loop() {
	events := m.watcher.Events()
	errs := m.watcher.Errors()
	for {
		m.mu.Lock()
		timer := m.timer
		m.mu.Unlock()
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			m.handleEvent(ev)
		case err, ok := <-errs:
			if !ok {
				return
			}
			m.queueResult(result{err: err})
		case <-timer:
			m.flush()
		case <-m.done:
			return
		}
	}
}

// Update re-targets the monitor at targets: the watched file set, the
// reference bytes (each target's in-memory source), the directory
// watches and the pending state. In-flight events and queued results are
// discarded: a resync supersedes everything detected before it, so the
// monitor never reports a change that predates the current graph. The
// new directory watches are added before the stale ones are removed, so
// a failed Add leaves the previous target set intact.
func (m *Monitor) Update(targets []Target) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrClosed
	}
	oldDirs := m.dirs
	newWatched := make(map[string]struct{}, len(targets))
	newSnap := make(map[string][]byte, len(targets))
	newDirs := make(map[string]struct{}, len(targets))
	for _, t := range targets {
		p := filepath.Clean(t.Path)
		newWatched[p] = struct{}{}
		newSnap[p] = append([]byte(nil), t.Source...)
		newDirs[filepath.Dir(p)] = struct{}{}
	}
	m.watched = newWatched
	m.snap = newSnap
	m.dirs = newDirs
	m.pending = map[string]struct{}{}
	m.timer = nil
	m.mu.Unlock()
	m.drain()

	for d := range newDirs {
		if _, ok := oldDirs[d]; ok {
			continue
		}
		if err := m.watcher.Add(d); err != nil {
			return fmt.Errorf("watch %s: %w", d, err)
		}
	}
	for d := range oldDirs {
		if _, ok := newDirs[d]; ok {
			continue
		}
		_ = m.watcher.Remove(d)
	}
	return nil
}

// Next blocks until a meaningful external change is detected, the
// monitor is closed (ErrClosed) or ctx is done. At most one Next may be
// in flight; a concurrent call returns ErrBusy immediately.
func (m *Monitor) Next(ctx context.Context) (Change, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Change{}, ErrClosed
	}
	if m.busy {
		m.mu.Unlock()
		return Change{}, ErrBusy
	}
	m.busy = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.busy = false
		m.mu.Unlock()
	}()

	select {
	case r := <-m.queue:
		if r.err != nil {
			return Change{}, r.err
		}
		return r.change, nil
	case <-m.done:
		return Change{}, ErrClosed
	case <-ctx.Done():
		return Change{}, ctx.Err()
	}
}

// Close stops the event loop, releases the underlying watcher and
// unblocks any pending Next with ErrClosed. It is idempotent.
func (m *Monitor) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	close(m.done)
	m.mu.Unlock()
	return m.watcher.Close()
}

// handleEvent records an event for a watched document, or for every
// watched document under a watched directory when the event targets the
// directory itself (the directory-level signal some platforms emit), and
// (re)arms the debounce timer. Events for unrelated entries in a shared
// directory are ignored.
func (m *Monitor) handleEvent(ev Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	p := filepath.Clean(ev.Path)
	if _, ok := m.watched[p]; ok {
		m.pending[p] = struct{}{}
	} else if _, ok := m.dirs[p]; ok {
		prefix := p + string(filepath.Separator)
		for w := range m.watched {
			if strings.HasPrefix(w, prefix) {
				m.pending[w] = struct{}{}
			}
		}
	} else {
		return
	}
	m.timer = m.clock.After(m.quiet)
}

// flush re-reads every pending document and reports the meaningful
// differences. It also re-registers the directory watches: Add is
// idempotent, so a directory whose watch was broken by a replacement is
// restored cheaply.
func (m *Monitor) flush() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	paths := m.pending
	m.pending = map[string]struct{}{}
	m.timer = nil
	m.mu.Unlock()
	if len(paths) == 0 {
		return
	}
	m.reRegister()
	for p := range paths {
		m.checkFile(p)
	}
}

// reRegister re-adds every watched directory, restoring watches that a
// rename or replacement may have invalidated.
func (m *Monitor) reRegister() {
	m.mu.Lock()
	dirs := make([]string, 0, len(m.dirs))
	for d := range m.dirs {
		dirs = append(dirs, d)
	}
	m.mu.Unlock()
	for _, d := range dirs {
		if err := m.watcher.Add(d); err != nil {
			m.queueResult(result{err: fmt.Errorf("re-watch %s: %w", d, err)})
		}
	}
}

// checkFile re-reads one watched document and reports it when its bytes
// differ from the reference snapshot, or when the file can no longer be
// read. Reporting advances the snapshot to the on-disk bytes, so a
// subsequent event for the same state (for example a touch) is silent:
// the operator is asked about the divergence once.
func (m *Monitor) checkFile(p string) {
	onDisk, err := m.read(p)
	if err != nil {
		m.mu.Lock()
		known := false
		if _, ok := m.watched[p]; ok {
			known = true
		}
		m.mu.Unlock()
		if known {
			m.queueResult(result{change: Change{Path: p, Missing: true}})
		}
		return
	}
	m.mu.Lock()
	ref := m.snap[p]
	m.mu.Unlock()
	if bytes.Equal(onDisk, ref) {
		return
	}
	m.mu.Lock()
	m.snap[p] = onDisk
	m.mu.Unlock()
	m.queueResult(result{change: Change{Path: p, OnDisk: onDisk}})
}

// queueResult delivers a result to the Next subscriber. The queue holds a
// bounded batch; when it is full the oldest result is dropped to make room
// for the newest, so a burst of changes while no Next is waiting never
// blocks the event loop and never loses the most recent detection.
func (m *Monitor) queueResult(r result) {
	select {
	case m.queue <- r:
		return
	default:
	}
	// The batch is full; drain one slot non-blockingly (the subscriber
	// may have consumed it in the meantime) and try again.
	select {
	case <-m.queue:
	default:
	}
	select {
	case m.queue <- r:
	default:
	}
}

// drain discards any result that has not been consumed yet. Called by
// Update so a resync supersedes everything detected before it.
func (m *Monitor) drain() {
	select {
	case <-m.queue:
	default:
	}
}
