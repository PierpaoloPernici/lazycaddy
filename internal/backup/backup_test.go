package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixedClock returns a Clock that always reports at, giving deterministic
// backup names.
func fixedClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

func TestNew_EmptyDirErrors(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New with an empty Dir succeeded")
	}
}

func TestCreate_NameFormat(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 8, 1, 20, 10, 0, 0, time.UTC)
	c, err := New(Options{Dir: dir, Clock: fixedClock(at)})
	if err != nil {
		t.Fatal(err)
	}
	path, err := c.Create(src)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if filepath.Base(path) != "2026-08-01T20-10-00-001-Caddyfile" {
		t.Fatalf("backup name = %q, want 2026-08-01T20-10-00-001-Caddyfile", filepath.Base(path))
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("backup dir = %q, want %q", filepath.Dir(path), dir)
	}
}

func TestCreate_ContentPreserved(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "Caddyfile")
	// Multi-byte runes and CRLF must round-trip byte-for-byte so a
	// backup is a faithful snapshot of the source.
	source := []byte("# caf\u00e9\r\nlocalhost {\r\n\trespond \"h\u00e9llo w\u00f6rld\"\r\n}\r\n")
	if err := os.WriteFile(src, source, 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	path, err := c.Create(src)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !bytes.Equal(got, source) {
		t.Fatalf("backup content differs:\n got %q\nwant %q", got, source)
	}
}

func TestCreate_CollisionSequence(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 8, 1, 20, 10, 0, 0, time.UTC)
	c, err := New(Options{Dir: dir, Clock: fixedClock(at)})
	if err != nil {
		t.Fatal(err)
	}
	// Two creates within the same clock tick must not overwrite each
	// other: the sequence keeps them distinct.
	first, err := c.Create(src)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second, err := c.Create(src)
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}

	if filepath.Base(first) != "2026-08-01T20-10-00-001-Caddyfile" {
		t.Fatalf("first backup = %q, want ...-001-Caddyfile", filepath.Base(first))
	}
	if filepath.Base(second) != "2026-08-01T20-10-00-002-Caddyfile" {
		t.Fatalf("second backup = %q, want ...-002-Caddyfile", filepath.Base(second))
	}
	if first == second {
		t.Fatal("two Creates in the same second returned the same path")
	}
}

func TestCreate_CreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "backups") // does not exist yet
	src := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	path, err := c.Create(src)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("backup dir missing after Create (err = %v)", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("backup dir = %q, want %q", filepath.Dir(path), dir)
	}
}

func TestCreate_MissingSourceErrors(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "nope.txt")
	c, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	path, err := c.Create(missing)
	if err == nil {
		t.Fatal("Create on a missing source succeeded")
	}
	if path != "" {
		t.Fatalf("Create returned path %q on error", path)
	}
	// No backup file may be produced from a failed read.
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(des) != 0 {
		t.Fatalf("backup files created despite read failure: %v", des)
	}
}

func TestList_RebuildsIndex(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	times := []time.Time{
		time.Date(2026, 8, 1, 20, 10, 0, 0, time.UTC), // oldest; reused below
		time.Date(2026, 8, 3, 9, 30, 15, 0, time.UTC), // newest
		time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),  // middle
	}
	for _, at := range times {
		c, err := New(Options{Dir: dir, Clock: fixedClock(at)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Create(src); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	// A second backup sharing the oldest timestamp exercises the sequence
	// tie-break in the sort.
	c, err := New(Options{Dir: dir, Clock: fixedClock(times[0])})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Create(src); err != nil {
		t.Fatalf("Create: %v", err)
	}

	entries, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("List returned %d entries, want 4", len(entries))
	}

	want := []struct {
		time time.Time
		seq  int
	}{
		{times[1], 1}, // newest timestamp first
		{times[2], 1},
		{times[0], 2}, // higher sequence wins within the same second
		{times[0], 1},
	}
	for i, w := range want {
		if !entries[i].Time.Equal(w.time) {
			t.Errorf("entries[%d].Time = %v, want %v", i, entries[i].Time, w.time)
		}
		if entries[i].Sequence != w.seq {
			t.Errorf("entries[%d].Sequence = %d, want %d", i, entries[i].Sequence, w.seq)
		}
	}
}

func TestList_MissingDirEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	entries, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("List returned %d entries for a missing dir", len(entries))
	}
}

func TestList_IgnoresNonBackupFiles(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 8, 1, 20, 10, 0, 0, time.UTC)
	c, err := New(Options{Dir: dir, Clock: fixedClock(at)})
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Create(src); err != nil {
		t.Fatal(err)
	}

	// Stray files that must not be treated as backups: wrong structure,
	// wrong sequence width, non-numeric sequence, or a garbage prefix.
	strays := []string{
		"README.md",
		"2026-08-01T20-10-00-Caddyfile",           // missing sequence
		"2026-08-01T20-10-00-01-Caddyfile",        // sequence not 3 digits
		"2026-08-01T20-10-00-abc-Caddyfile",       // non-numeric sequence
		"2026-08-01T20-10-00-123",                 // basename-less
		"not-a-2026-08-01T20-10-00-005-Caddyfile", // garbage prefix
	}
	for _, name := range strays {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A directory named like a backup is not a backup file either.
	if err := os.Mkdir(filepath.Join(dir, "2026-08-01T20-10-00-009-Caddyfile"), 0o755); err != nil {
		t.Fatal(err)
	}

	entries, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1: %+v", len(entries), entries)
	}
	if entries[0].Path != filepath.Join(dir, "2026-08-01T20-10-00-001-Caddyfile") {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
}

func TestList_ParsesBasenameWithDashes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "my-caddyfile-a.b.caddy")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 1, 20, 10, 0, 0, time.UTC)
	c, err := New(Options{Dir: dir, Clock: fixedClock(at)})
	if err != nil {
		t.Fatal(err)
	}
	path, err := c.Create(src)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantName := "2026-08-01T20-10-00-001-my-caddyfile-a.b.caddy"
	if filepath.Base(path) != wantName {
		t.Fatalf("backup name = %q, want %q", filepath.Base(path), wantName)
	}

	entries, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}
	if !entries[0].Time.Equal(at) {
		t.Errorf("Time = %v, want %v", entries[0].Time, at)
	}
	if entries[0].Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", entries[0].Sequence)
	}
	if filepath.Base(entries[0].Path) != wantName {
		t.Errorf("entry path = %q, want basename %q", entries[0].Path, wantName)
	}
}
