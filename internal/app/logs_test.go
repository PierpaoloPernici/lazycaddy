package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

// TestNewLogSource_WrapsTailer verifies the adapter end-to-end against a
// real temp file (no network, no Caddy): entries written to the file are
// surfaced through LogSource.Next and the bounded history through
// LogSource.History.
func TestNewLogSource_WrapsTailer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	if err := os.WriteFile(path, []byte(`{"level":"info","msg":"first"}
{"level":"error","msg":"second"}
`), 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	src := NewLogSource(logs.NewTailer(logs.Options{Path: path}))
	entries, err := src.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Next returned %d entries, want 2", len(entries))
	}
	if entries[0].Msg != "first" || entries[1].Msg != "second" {
		t.Errorf("entries = %+v, want msgs first/second", entries)
	}
	if entries[0].Level != "info" || entries[1].Level != "error" {
		t.Errorf("levels = %q/%q, want info/error", entries[0].Level, entries[1].Level)
	}
	// History reflects the same tail.
	hist := src.History()
	if len(hist) != 2 {
		t.Fatalf("History returned %d entries, want 2", len(hist))
	}
	// A follow-up poll finds nothing new (no error, no entries).
	again, err := src.Next(context.Background())
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second Next returned %d entries, want 0", len(again))
	}
}

// TestNewLogSource_MissingFileKeepsPolling verifies that a log file that
// does not exist yet is not a failure: Next returns no entries and no
// error, so the UI keeps polling until the file appears.
func TestNewLogSource_MissingFileKeepsPolling(t *testing.T) {
	dir := t.TempDir()
	src := NewLogSource(logs.NewTailer(logs.Options{Path: filepath.Join(dir, "missing.log")}))
	entries, err := src.Next(context.Background())
	if err != nil {
		t.Fatalf("Next for a missing file: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("Next returned %d entries for a missing file, want 0", len(entries))
	}
}
