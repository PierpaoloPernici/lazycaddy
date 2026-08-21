package watch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeWatcher is a programmable Watcher for deterministic tests.
type fakeWatcher struct {
	adds    []string
	removes []string
	events  chan Event
	errors  chan error
	addErr  error
	closed  bool
}

func newFakeWatcher() *fakeWatcher {
	return &fakeWatcher{
		events: make(chan Event, 100),
		errors: make(chan error, 10),
	}
}

func (f *fakeWatcher) Add(path string) error {
	f.adds = append(f.adds, path)
	if f.addErr != nil {
		return f.addErr
	}
	return nil
}

func (f *fakeWatcher) Remove(path string) error {
	f.removes = append(f.removes, path)
	return nil
}

func (f *fakeWatcher) Events() <-chan Event { return f.events }

func (f *fakeWatcher) Errors() <-chan error { return f.errors }

func (f *fakeWatcher) Close() error {
	f.closed = true
	close(f.events)
	close(f.errors)
	return nil
}

// fakeClock arms debounce timers that the test fires manually. It is
// mutex-protected because the monitor loop and a test goroutine may arm
// timers concurrently.
type fakeClock struct {
	mu     sync.Mutex
	timers []chan time.Time
}

func (c *fakeClock) After(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.timers = append(c.timers, ch)
	c.mu.Unlock()
	return ch
}

// fire delivers on the most recent timer (the only one the loop listens
// to; earlier timers were replaced by newer events).
func (c *fakeClock) fire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.timers) == 0 {
		return
	}
	c.timers[len(c.timers)-1] <- time.Now()
}

// memFS is an in-memory file system for the ReadFile seam.
type memFS map[string]string

func (fs memFS) read(path string) ([]byte, error) {
	v, ok := fs[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(v), nil
}

// newMonitor builds a started monitor over a fake watcher and an
// in-memory file system, with the given initial documents.
func newMonitor(t *testing.T, w *fakeWatcher, fs memFS, targets ...Target) *Monitor {
	t.Helper()
	m := NewMonitor(Options{Watcher: w, ReadFile: fs.read, Clock: &fakeClock{}})
	if err := m.Update(targets); err != nil {
		t.Fatalf("Update: %v", err)
	}
	m.Start()
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// next is a helper that blocks on Next with a bounded context so a
// regression cannot hang the suite.
func next(t *testing.T, m *Monitor) (Change, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.Next(ctx)
}

func TestMonitorWriteEventReportsChange(t *testing.T) {
	w := newFakeWatcher()
	path := filepath.Join("etc", "caddy", "Caddyfile")
	fs := memFS{path: "new content"}
	m := newMonitor(t, w, fs, Target{Path: path, Source: []byte("old content")})

	// The parent directory must be watched, not the file itself, so
	// atomic replacements are visible on platforms where file watches
	// die with the inode.
	if len(w.adds) != 1 || filepath.Clean(w.adds[0]) != filepath.Clean(filepath.Dir(path)) {
		t.Fatalf("watched dirs = %v, want %q", w.adds, filepath.Dir(path))
	}

	// Drive the handlers directly (deterministic, no timer goroutine).
	m.handleEvent(Event{Path: path, Op: OpWrite})
	m.flush()
	ch, err := next(t, m)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ch.Path != path || string(ch.OnDisk) != "new content" || ch.Missing {
		t.Errorf("change = %+v, want write change for %s", ch, path)
	}
}

func TestMonitorAtomicRenameReplacementReportsChange(t *testing.T) {
	w := newFakeWatcher()
	path := filepath.Join("etc", "caddy", "Caddyfile")
	fs := memFS{path: "replaced"}
	m := newMonitor(t, w, fs, Target{Path: path, Source: []byte("original")})

	// An atomic save is a create of a new inode (often reported as a
	// rename of the temp file, then a create of the target).
	m.handleEvent(Event{Path: path, Op: OpRename | OpCreate})
	m.flush()
	ch, err := next(t, m)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if string(ch.OnDisk) != "replaced" {
		t.Errorf("change OnDisk = %q, want %q", ch.OnDisk, "replaced")
	}
}

func TestMonitorRemoveReportsMissingFile(t *testing.T) {
	w := newFakeWatcher()
	path := filepath.Join("etc", "caddy", "Caddyfile")
	m := newMonitor(t, w, memFS{}, Target{Path: path, Source: []byte("old")})

	m.handleEvent(Event{Path: path, Op: OpRemove})
	m.flush()
	ch, err := next(t, m)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !ch.Missing || ch.Path != path || ch.OnDisk != nil {
		t.Errorf("change = %+v, want missing change for %s", ch, path)
	}
}

func TestMonitorRecreateAfterRemoveReportsNewChange(t *testing.T) {
	w := newFakeWatcher()
	path := filepath.Join("etc", "caddy", "Caddyfile")
	fs := memFS{}
	m := newMonitor(t, w, fs, Target{Path: path, Source: []byte("old")})

	m.handleEvent(Event{Path: path, Op: OpRemove})
	m.flush()
	if ch, err := next(t, m); err != nil || !ch.Missing {
		t.Fatalf("first Next = (%+v, %v), want missing change", ch, err)
	}

	fs[path] = "recreated"
	m.handleEvent(Event{Path: path, Op: OpCreate})
	m.flush()
	ch, err := next(t, m)
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	if ch.Missing || string(ch.OnDisk) != "recreated" {
		t.Errorf("second change = %+v, want recreated content", ch)
	}
}

func TestMonitorImportedFileChangeIdentifiesExactPath(t *testing.T) {
	w := newFakeWatcher()
	root := filepath.Join("etc", "caddy", "Caddyfile")
	imported := filepath.Join("srv", "sites", "a.conf")
	fs := memFS{imported: "changed"}
	m := newMonitor(t, w, fs,
		Target{Path: root, Source: []byte("root")},
		Target{Path: imported, Source: []byte("original")},
	)

	m.handleEvent(Event{Path: imported, Op: OpWrite})
	m.flush()
	ch, err := next(t, m)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ch.Path != imported {
		t.Errorf("change path = %q, want %q (the imported file)", ch.Path, imported)
	}
}

func TestMonitorDebouncesBurstIntoOneChange(t *testing.T) {
	w := newFakeWatcher()
	path := filepath.Join("etc", "caddy", "Caddyfile")
	fs := memFS{path: "burst"}
	m := newMonitor(t, w, fs, Target{Path: path, Source: []byte("old")})

	// A save burst: several events land inside one quiet window; only
	// the pending set (one entry per path) matters, so a single flush
	// delivers a single change.
	for i := 0; i < 5; i++ {
		m.handleEvent(Event{Path: path, Op: OpWrite})
	}
	m.flush()
	if ch, err := next(t, m); err != nil || string(ch.OnDisk) != "burst" {
		t.Fatalf("Next = (%+v, %v), want the coalesced change", ch, err)
	}
	select {
	case r := <-m.queue:
		t.Fatalf("second queued change after a debounced burst: %+v", r)
	default:
	}
}

func TestMonitorIgnoresEventsForUnchangedBytes(t *testing.T) {
	w := newFakeWatcher()
	path := filepath.Join("etc", "caddy", "Caddyfile")
	fs := memFS{path: "same"} // on disk equals the snapshot
	m := newMonitor(t, w, fs, Target{Path: path, Source: []byte("same")})

	// Drive the handlers directly so the absence of a queued result is
	// observable without real timing.
	m.handleEvent(Event{Path: path, Op: OpWrite})
	m.flush()
	select {
	case r := <-m.queue:
		t.Fatalf("unchanged bytes reported a change: %+v", r)
	default:
	}
}

func TestMonitorIgnoresSiblingEventsInSharedDirectory(t *testing.T) {
	w := newFakeWatcher()
	path := filepath.Join("etc", "caddy", "Caddyfile")
	other := filepath.Join("etc", "caddy", "unrelated.tmp")
	fs := memFS{path: "same"}
	m := newMonitor(t, w, fs, Target{Path: path, Source: []byte("same")})

	m.handleEvent(Event{Path: other, Op: OpWrite})
	if len(m.pending) != 0 {
		t.Fatalf("sibling event marked %v as pending", m.pending)
	}
}

func TestMonitorDirLevelEventRescansWatchedFiles(t *testing.T) {
	w := newFakeWatcher()
	dir := filepath.Join("etc", "caddy")
	root := filepath.Join(dir, "Caddyfile")
	imported := filepath.Join(dir, "common.conf")
	fs := memFS{root: "r-new", imported: "i-new"}
	m := newMonitor(t, w, fs,
		Target{Path: root, Source: []byte("r-old")},
		Target{Path: imported, Source: []byte("i-old")},
	)

	// Some platforms report a single directory-level event for any
	// change inside the watched directory; the monitor must re-read
	// every watched file under it and report both differences.
	m.handleEvent(Event{Path: dir, Op: OpWrite})
	m.flush()
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		ch, err := next(t, m)
		if err != nil {
			t.Fatalf("change %d: %v", i, err)
		}
		seen[ch.Path] = true
	}
	if !seen[root] || !seen[imported] {
		t.Errorf("reported changes = %v, want both %q and %q", seen, root, imported)
	}
}

func TestMonitorUpdateResyncsAfterSave(t *testing.T) {
	w := newFakeWatcher()
	path := filepath.Join("etc", "caddy", "Caddyfile")
	fs := memFS{path: "saved"}
	m := newMonitor(t, w, fs, Target{Path: path, Source: []byte("old")})

	// A save write event lands, then the graph is re-synced to the
	// freshly saved bytes before the debounce fires: the resync
	// supersedes the event and nothing is reported.
	m.handleEvent(Event{Path: path, Op: OpWrite})
	if err := m.Update([]Target{{Path: path, Source: []byte("saved")}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	m.flush()
	select {
	case r := <-m.queue:
		t.Fatalf("own-save resync reported a change: %+v", r)
	default:
	}
}

func TestMonitorUpdateAddsNewDirsAndRemovesStaleOnes(t *testing.T) {
	w := newFakeWatcher()
	dir1 := filepath.Join("etc", "caddy")
	dir2 := filepath.Join("srv", "sites")
	root := filepath.Join(dir1, "Caddyfile")
	imported := filepath.Join(dir2, "a.conf")
	m := newMonitor(t, w, memFS{}, Target{Path: root, Source: []byte("root")})
	if len(w.adds) != 1 || filepath.Clean(w.adds[0]) != filepath.Clean(dir1) {
		t.Fatalf("adds = %v, want %q", w.adds, dir1)
	}
	// Re-target at a single document in another directory: the stale
	// directory watch must be released.
	if err := m.Update([]Target{{Path: imported, Source: []byte("import")}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	has := func(list []string, s string) bool {
		for _, v := range list {
			if filepath.Clean(v) == filepath.Clean(s) {
				return true
			}
		}
		return false
	}
	if !has(w.adds, dir2) {
		t.Errorf("adds = %v, want %q added", w.adds, dir2)
	}
	if !has(w.removes, dir1) {
		t.Errorf("removes = %v, want %q removed", w.removes, dir1)
	}
}

func TestMonitorReRegistersDirsOnFlush(t *testing.T) {
	w := newFakeWatcher()
	dir := filepath.Join("etc", "caddy")
	path := filepath.Join(dir, "Caddyfile")
	fs := memFS{path: "new"}
	m := newMonitor(t, w, fs, Target{Path: path, Source: []byte("old")})
	before := len(w.adds)

	m.handleEvent(Event{Path: path, Op: OpWrite})
	m.flush()
	if len(w.adds) != before+1 {
		t.Fatalf("adds = %v, want one re-registration on flush", w.adds)
	}
}

func TestMonitorReRegisterErrorIsDelivered(t *testing.T) {
	w := newFakeWatcher()
	path := filepath.Join("etc", "caddy", "Caddyfile")
	fs := memFS{path: "new"}
	m := newMonitor(t, w, fs, Target{Path: path, Source: []byte("old")})
	w.addErr = errors.New("watch lost")

	m.handleEvent(Event{Path: path, Op: OpWrite})
	m.flush()
	if _, err := next(t, m); !strings.Contains(err.Error(), "watch lost") {
		t.Fatalf("re-register error = %v, want watch lost", err)
	}
}

func TestMonitorWatcherErrorIsDelivered(t *testing.T) {
	w := newFakeWatcher()
	m := newMonitor(t, w, memFS{}, Target{Path: "Caddyfile", Source: []byte("x")})
	boom := errors.New("watcher exploded")
	w.errors <- boom
	if _, err := next(t, m); !errors.Is(err, boom) {
		t.Fatalf("Next error = %v, want the watcher error", err)
	}
}

func TestMonitorCloseUnblocksNextWithErrClosed(t *testing.T) {
	w := newFakeWatcher()
	m := newMonitor(t, w, memFS{}, Target{Path: "Caddyfile", Source: []byte("x")})
	done := make(chan error, 1)
	go func() {
		_, err := m.Next(context.Background())
		done <- err
	}()
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatalf("Next after Close = %v, want ErrClosed", err)
	}
	// A second Close is a no-op.
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestMonitorNextRejectsConcurrentWaiter(t *testing.T) {
	w := newFakeWatcher()
	m := newMonitor(t, w, memFS{}, Target{Path: "Caddyfile", Source: []byte("x")})
	m.mu.Lock()
	m.busy = true
	m.mu.Unlock()
	if _, err := m.Next(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent Next error = %v, want ErrBusy", err)
	}
}

func TestMonitorUpdateAfterCloseReturnsErrClosed(t *testing.T) {
	w := newFakeWatcher()
	m := newMonitor(t, w, memFS{})
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := m.Update([]Target{{Path: "Caddyfile", Source: []byte("x")}}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Update after Close = %v, want ErrClosed", err)
	}
}

func TestMonitorSnapshotDoesNotAliasTargetSource(t *testing.T) {
	w := newFakeWatcher()
	src := []byte("original")
	m := newMonitor(t, w, memFS{"Caddyfile": "original"}, Target{Path: "Caddyfile", Source: src})
	src[0] = 'X'
	m.mu.Lock()
	got := string(m.snap["Caddyfile"])
	m.mu.Unlock()
	if got != "original" {
		t.Fatalf("snapshot aliases the target source: %q", got)
	}
}

func TestMonitorReportedChangeAdvancesSnapshot(t *testing.T) {
	w := newFakeWatcher()
	path := filepath.Join("etc", "caddy", "Caddyfile")
	fs := memFS{path: "v1"}
	m := newMonitor(t, w, fs, Target{Path: path, Source: []byte("v0")})

	m.handleEvent(Event{Path: path, Op: OpWrite})
	m.flush()
	if ch, err := next(t, m); err != nil || string(ch.OnDisk) != "v1" {
		t.Fatalf("first change = (%+v, %v)", ch, err)
	}
	m.mu.Lock()
	snap := m.snap[path]
	m.mu.Unlock()
	if !bytes.Equal(snap, []byte("v1")) {
		t.Fatalf("snapshot after report = %q, want %q (acknowledge)", snap, "v1")
	}

	// A touch that does not change the bytes is now silent.
	m.handleEvent(Event{Path: path, Op: OpWrite})
	m.flush()
	select {
	case r := <-m.queue:
		t.Fatalf("touch after acknowledge reported a change: %+v", r)
	default:
	}
}

func TestMonitorQueueDropsOldestWhenFull(t *testing.T) {
	m := NewMonitor(Options{Watcher: newFakeWatcher(), ReadFile: memFS{}.read})
	for i := 0; i < queueCap+2; i++ {
		m.queueResult(result{change: Change{Path: filepath.Join("file", string(rune('0'+i)))}})
	}

	if got := len(m.queue); got != queueCap {
		t.Fatalf("queue length = %d, want %d", got, queueCap)
	}
	first := (<-m.queue).change.Path
	last := first
	for len(m.queue) > 0 {
		last = (<-m.queue).change.Path
	}
	if first != filepath.Join("file", "2") {
		t.Errorf("oldest queued result = %q, want file/2", first)
	}
	if last != filepath.Join("file", "9") {
		t.Errorf("newest queued result = %q, want file/9", last)
	}
}

// TestMonitorUpdateDiscardsAllQueuedResults verifies that a resync
// supersedes every result queued before it: a full batch of stale
// detections must never surface through Next after Update.
func TestMonitorUpdateDiscardsAllQueuedResults(t *testing.T) {
	m := NewMonitor(Options{Watcher: newFakeWatcher(), ReadFile: memFS{}.read})
	for i := 0; i < queueCap; i++ {
		m.queueResult(result{change: Change{Path: "stale"}})
	}
	if got := len(m.queue); got != queueCap {
		t.Fatalf("queue length before Update = %d, want %d", got, queueCap)
	}
	if err := m.Update([]Target{{Path: "Caddyfile", Source: []byte("x")}}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := len(m.queue); got != 0 {
		t.Fatalf("queue length after Update = %d, want 0", got)
	}
}

// TestRealClockAfter verifies the production Clock seam fires the returned
// channel after the requested duration.
func TestRealClockAfter(t *testing.T) {
	start := time.Now()
	ch := (realClock{}).After(10 * time.Millisecond)
	select {
	case <-ch:
		if elapsed := time.Since(start); elapsed < 5*time.Millisecond {
			t.Errorf("timer fired after %v, want at least ~10ms", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("realClock.After never fired")
	}
}

// TestMonitorStart_IsIdempotent verifies Start can be called more than
// once without double-launching the event loop.
func TestMonitorStart_IsIdempotent(t *testing.T) {
	w := newFakeWatcher()
	m := NewMonitor(Options{Watcher: w, ReadFile: memFS{}.read, Clock: &fakeClock{}})
	m.Start()
	m.Start() // second call must be a no-op
	if !m.started {
		t.Error("started = false after Start")
	}
	_ = m.Close()
}
