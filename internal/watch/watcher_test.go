package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// TestFSWatcherReportsWrite is a real-filesystem smoke test for the
// fsnotify wrapper: it verifies the event mapping and the directory-watch
// wiring end to end. It is the only watch test that uses real timing; the
// monitor logic itself is covered deterministically with fake watchers.
func TestFSWatcherReportsWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	w, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Add(dir); err != nil {
		t.Fatalf("Add(%s): %v", dir, err)
	}

	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("write change: %v", err)
	}

	deadline := time.After(5 * time.Second)
	events := w.Events()
	for {
		select {
		case ev := <-events:
			// Some platforms deliver a spurious event (for example a
			// chmod from the initial directory scan) before the write;
			// skip events without the write bit.
			if ev.Op&OpWrite == 0 {
				continue
			}
			// Linux reports the file path; kqueue may report the
			// directory (re-scan signal). Both must reach the monitor.
			if ev.Path != path && ev.Path != dir {
				t.Errorf("event path = %q, want %q or %q", ev.Path, path, dir)
			}
			if err := w.Remove(dir); err != nil {
				t.Fatalf("Remove(%s): %v", dir, err)
			}
			if w.Errors() == nil {
				t.Fatal("Errors() returned a nil channel")
			}
			return
		case <-deadline:
			t.Fatal("timeout waiting for the write event")
		}
	}
}

func TestMapEvent_MapsAllOperations(t *testing.T) {
	got := mapEvent(fsnotify.Event{
		Name: "Caddyfile",
		Op:   fsnotify.Write | fsnotify.Create | fsnotify.Remove | fsnotify.Rename,
	})
	if got.Path != "Caddyfile" {
		t.Fatalf("path = %q, want Caddyfile", got.Path)
	}
	want := OpWrite | OpCreate | OpRemove | OpRename
	if got.Op != want {
		t.Fatalf("op = %d, want %d", got.Op, want)
	}
}

// TestFSWatcher_AddMissingPath verifies the real fsnotify-backed watcher
// surfaces an add failure for a path that cannot be watched.
func TestFSWatcher_AddMissingPath(t *testing.T) {
	w, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()
	if err := w.Add(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("Add on a missing path: expected an error, got nil")
	}
}
