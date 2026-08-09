package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
			return
		case <-deadline:
			t.Fatal("timeout waiting for the write event")
		}
	}
}
