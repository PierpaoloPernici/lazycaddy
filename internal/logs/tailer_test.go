package logs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertRaw(t *testing.T, entries []Entry, want []string) {
	t.Helper()
	got := rawStrings(entries)
	if !equalStrings(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

func TestTailer_AppendRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "a\nb\n")

	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	got, err := tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	assertRaw(t, got, []string{"a", "b"})

	appendFile(t, path, "c\n")
	got, err = tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	assertRaw(t, got, []string{"c"})

	// Partial line is carried, not returned.
	appendFile(t, path, "partial")
	got, err = tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Next returned %v, want nothing while line is partial", got)
	}

	// Completing the line yields exactly one entry.
	appendFile(t, path, "-rest\n")
	got, err = tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	assertRaw(t, got, []string{"partial-rest"})

	if err := tt.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := tt.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestTailer_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.log")
	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	got, err := tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next on missing file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Next returned %v, want nothing for a missing file", got)
	}

	// Content written before the file appeared is picked up on the first
	// successful read.
	writeFile(t, path, "a\nb\n")
	got, err = tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next after create: %v", err)
	}
	assertRaw(t, got, []string{"a", "b"})
}

func TestTailer_Rotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "a\nb\n")

	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	got, err := tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	assertRaw(t, got, []string{"a", "b"})

	// Rotate: rename the active file away and create a fresh file at path.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "c\n")
	appendFile(t, path, "d\n")

	got, err = tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next after rotation: %v", err)
	}
	assertRaw(t, got, []string{"c", "d"})

	// The history must contain the old lines exactly once, plus the new.
	assertRaw(t, tt.Entries(), []string{"a", "b", "c", "d"})
}

func TestTailer_RotationDrainsOldHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "a\n")

	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	if _, err := tt.Next(ctx); err != nil {
		t.Fatalf("Next: %v", err)
	}

	// Data lands in the old file, then the file is rotated away.
	appendFile(t, path, "b\n")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "c\n")

	got, err := tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next after rotation: %v", err)
	}
	// "b" must be drained from the old handle before "c" is read fresh.
	assertRaw(t, got, []string{"b", "c"})
	assertRaw(t, tt.Entries(), []string{"a", "b", "c"})
}

func TestTailer_Truncate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "a\nb\n")

	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	if _, err := tt.Next(ctx); err != nil {
		t.Fatalf("Next: %v", err)
	}

	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path, "c\n")

	got, err := tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next after truncate: %v", err)
	}
	assertRaw(t, got, []string{"c"})
	assertRaw(t, tt.Entries(), []string{"a", "b", "c"})
}

func TestTailer_NonJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.log")
	writeFile(t, path, "2026/08/08 12:00:00 INFO something\n")

	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	got, err := tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Next returned %d entries, want 1", len(got))
	}
	e := got[0]
	if want := "2026/08/08 12:00:00 INFO something"; string(e.Raw) != want {
		t.Errorf("Raw = %q, want %q", e.Raw, want)
	}
	if e.Level != "" || e.Logger != "" || e.Msg != "" || e.Method != "" || e.URI != "" || e.Host != "" {
		t.Errorf("expected empty parsed fields, got %+v", e)
	}
	if e.Status != -1 {
		t.Errorf("Status = %d, want -1 for a non-JSON line", e.Status)
	}
}

func TestTailer_MaxLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	tt := NewTailer(Options{Path: path, MaxLines: 3})
	ctx := context.Background()

	writeFile(t, path, "a\nb\nc\n")
	got, err := tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	assertRaw(t, got, []string{"a", "b", "c"})

	appendFile(t, path, "d\ne\n")
	got, err = tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	assertRaw(t, got, []string{"d", "e"})

	// Only the last three lines are retained in history.
	assertRaw(t, tt.Entries(), []string{"c", "d", "e"})
}

func TestTailer_Cancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	tt := NewTailer(Options{Path: path})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := tt.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Next = %v, want context.Canceled", err)
	}
}

func TestTailer_ReadError(t *testing.T) {
	// A directory can be opened but not read for log content; Next must
	// surface the read error instead of swallowing it.
	dir := filepath.Join(t.TempDir(), "adir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tt := NewTailer(Options{Path: dir})
	if _, err := tt.Next(context.Background()); err == nil {
		t.Error("Next on a directory path: expected an error, got nil")
	}
}
