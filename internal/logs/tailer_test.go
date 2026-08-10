package logs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
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

// TestTailer_TruncateDropsCarry verifies that truncating a file while a
// partial line is carried does not recombine the pre-truncation carry with
// the first post-truncation chunk: the carried bytes belong to a record
// that no longer exists.
func TestTailer_TruncateDropsCarry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, path, "partial")

	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	// First poll carries "partial" (no trailing newline): no entries yet.
	got, err := tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Next returned %d entries, want 0 (partial line carried)", len(got))
	}

	// Truncate the file and write a fresh complete line.
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path, "fresh\n")

	got, err = tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next after truncate: %v", err)
	}
	assertRaw(t, got, []string{"fresh"})
	assertRaw(t, tt.Entries(), []string{"fresh"})
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
	if e.Parsed {
		t.Error("Parsed = true, want false for a non-JSON line")
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

// swap temporarily replaces an injectable package var and restores it when
// the test finishes.
func swap[T any](t *testing.T, dst *T, val T) {
	t.Helper()
	orig := *dst
	*dst = val
	t.Cleanup(func() { *dst = orig })
}

func TestTailer_OpenErrorSurfaced(t *testing.T) {
	// A file in the path (not a directory) makes os.Open fail with a
	// non-IsNotExist error, which Next must surface.
	dir := t.TempDir()
	file := filepath.Join(dir, "notadir")
	writeFile(t, file, "x")
	tt := NewTailer(Options{Path: filepath.Join(file, "access.log")})
	if _, err := tt.Next(context.Background()); err == nil {
		t.Error("Next on a path through a file: expected an error, got nil")
	}
}

func TestTailer_FileRemovedWhileFollowing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	writeFile(t, path, "a\n")
	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	if _, err := tt.Next(ctx); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// The file is temporarily gone: keep the handle and keep polling.
	if got, err := tt.Next(ctx); err != nil || len(got) != 0 {
		t.Errorf("Next after removal = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestTailer_StatErrorSurfaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	writeFile(t, path, "a\n")
	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	if _, err := tt.Next(ctx); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	swap(t, &statPath, func(string) (os.FileInfo, error) { return nil, errors.New("stat boom") })
	if _, err := tt.Next(ctx); err == nil {
		t.Error("Next with a failing stat: expected an error, got nil")
	}
}

func TestTailer_OpenStatErrorSurfaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	writeFile(t, path, "a\n")
	tt := NewTailer(Options{Path: path})
	swap(t, &statFile, func(*os.File) (os.FileInfo, error) { return nil, errors.New("stat boom") })
	if _, err := tt.Next(context.Background()); err == nil {
		t.Error("Next with a failing file stat: expected an error, got nil")
	}
}

func TestTailer_RotationReadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	writeFile(t, path, "a\n")
	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	if _, err := tt.Next(ctx); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "c\n")

	swap(t, &readAt, func(*os.File, []byte, int64) (int, error) { return 0, errors.New("read boom") })
	if _, err := tt.Next(ctx); err == nil {
		t.Error("Next during rotation with a failing read: expected an error, got nil")
	}
}

func TestTailer_RotationCloseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	writeFile(t, path, "a\n")
	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	if _, err := tt.Next(ctx); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "c\n")

	swap(t, &closeFile, func(*os.File) error { return errors.New("close boom") })
	if _, err := tt.Next(ctx); err == nil {
		t.Error("Next during rotation with a failing close: expected an error, got nil")
	}
}

func TestTailer_RotationReopenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	writeFile(t, path, "a\n")
	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	if _, err := tt.Next(ctx); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path+".1", "b\n") // unread lines on the old handle
	writeFile(t, path, "c\n")

	// The new file disappears before the reopen: drained lines survive.
	swap(t, &openFile, func(string) (*os.File, error) { return nil, os.ErrNotExist })
	got, err := tt.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	assertRaw(t, got, []string{"b"})
}

func TestTailer_RotationReopenError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	writeFile(t, path, "a\n")
	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	if _, err := tt.Next(ctx); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "c\n")

	swap(t, &openFile, func(string) (*os.File, error) { return nil, errors.New("open boom") })
	if _, err := tt.Next(ctx); err == nil {
		t.Error("Next during rotation with a failing reopen: expected an error, got nil")
	}
}

func TestTailer_CloseErrorSurfaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	writeFile(t, path, "a\n")
	tt := NewTailer(Options{Path: path})
	if _, err := tt.Next(context.Background()); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	swap(t, &closeFile, func(*os.File) error { return errors.New("close boom") })
	if err := tt.Close(); err == nil {
		t.Error("Close with a failing close: expected an error, got nil")
	}
}

func TestTailer_CRLFAndEmptyLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	// CRLF line, an empty line, a parsed JSON line.
	writeFile(t, path, "plain\r\n\n{\"msg\":\"hello\"}\n")
	tt := NewTailer(Options{Path: path})
	got, err := tt.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2 (CRLF raw line + parsed JSON)", len(got))
	}
	if string(got[0].Raw) != "plain" {
		t.Errorf("entries[0].Raw = %q, want %q (CR stripped)", got[0].Raw, "plain")
	}
	if got[1].Msg != "hello" {
		t.Errorf("entries[1].Msg = %q, want %q", got[1].Msg, "hello")
	}
}

// flipCtx reports no error for the first Err() call (Next's upfront check)
// and context.Canceled afterwards, so the mid-read cancellation branch in
// readAll is reachable deterministically.
type flipCtx struct {
	context.Context
	mu    sync.Mutex
	calls int
}

func (c *flipCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls > 1 {
		return context.Canceled
	}
	return nil
}

func TestTailer_MidReadCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	writeFile(t, path, "a\n")
	tt := NewTailer(Options{Path: path})
	ctx := &flipCtx{Context: context.Background()}
	if _, err := tt.Next(ctx); err == nil {
		t.Error("Next with a cancellation mid-read: expected an error, got nil")
	}
}

func TestTailer_RotationSecondReadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	writeFile(t, path, "a\n")
	tt := NewTailer(Options{Path: path})
	ctx := context.Background()

	if _, err := tt.Next(ctx); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path+".1", "b\n")
	writeFile(t, path, "c\n")

	// The drain of the old handle succeeds; the read of the freshly
	// opened file fails.
	calls := 0
	swap(t, &readAt, func(f *os.File, buf []byte, off int64) (int, error) {
		calls++
		if calls >= 2 {
			return 0, errors.New("read boom")
		}
		return f.ReadAt(buf, off)
	})
	if _, err := tt.Next(ctx); err == nil {
		t.Error("Next with a failing read of the new file: expected an error, got nil")
	}
}
