package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestCreate_WritesIdentitySidecar(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "conf", "Caddyfile")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
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
	sidecar := path + sourceSuffix
	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != filepath.Clean(src) {
		t.Errorf("sidecar = %q, want the exact canonical source path %q", got, filepath.Clean(src))
	}
}

func TestCreate_SidecarFailureAborts(t *testing.T) {
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
	// The next Create in the same tick will take sequence 002. Occupy its
	// sidecar path with a directory so the atomic sidecar rename fails
	// while the backup write itself succeeds.
	next := filepath.Join(dir, "2026-08-01T20-10-00-002-Caddyfile")
	if err := os.Mkdir(next+sourceSuffix, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Create(src); err == nil {
		t.Fatal("Create succeeded despite a sidecar failure")
	}
	// The backup that failed its identity step must not be left behind:
	// an unattributable backup is worse than none.
	if _, err := os.Stat(next); err == nil {
		t.Fatalf("backup %q survived its sidecar failure", next)
	}
	// No sidecar temp files may leak either.
	if leaked, err := filepath.Glob(filepath.Join(dir, ".lazycaddy-*.tmp")); err != nil {
		t.Fatalf("glob temp files: %v", err)
	} else if len(leaked) != 0 {
		t.Errorf("sidecar temp files leaked after the failure: %v", leaked)
	}
	// The original backup is untouched.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("original backup missing: %v", err)
	}
}

func TestCreate_SidecarInheritsBackupMode(t *testing.T) {
	dir := t.TempDir()
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
	backupMode, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	sidecarMode, err := os.Stat(path + sourceSuffix)
	if err != nil {
		t.Fatalf("stat sidecar: %v", err)
	}
	if sidecarMode.Mode().Perm() != backupMode.Mode().Perm() {
		t.Errorf("sidecar mode = %o, want the backup's mode %o", sidecarMode.Mode().Perm(), backupMode.Mode().Perm())
	}
}

func TestCreate_ConcurrentCreatesNeverCollide(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	paths := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths[i], errs[i] = c.Create(src)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			t.Fatalf("concurrent Creates collided on backup path %q", p)
		}
		seen[p] = true
		if _, err := os.Stat(p + sourceSuffix); err != nil {
			t.Errorf("sidecar for %q missing: %v", p, err)
		}
	}
}

func TestList_RecoversExactSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "sites", "example", "Caddyfile")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Dir: dir})
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
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}
	if !entries[0].SourceKnown {
		t.Fatal("SourceKnown = false, want true (identity sidecar present)")
	}
	if entries[0].Source != filepath.Clean(src) {
		t.Errorf("Source = %q, want %q", entries[0].Source, filepath.Clean(src))
	}
}

func TestList_LegacyBackupHasUnknownSource(t *testing.T) {
	dir := t.TempDir()
	// A legacy backup: valid filename, no identity sidecar.
	legacy := filepath.Join(dir, "2026-08-01T20-10-00-007-Caddyfile")
	if err := os.WriteFile(legacy, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}
	if entries[0].SourceKnown {
		t.Fatal("SourceKnown = true, want false for a legacy backup")
	}
	if entries[0].Sequence != 7 {
		t.Errorf("Sequence = %d, want 7", entries[0].Sequence)
	}
}

func TestBelongsTo_ExactSourceIdentity(t *testing.T) {
	a := filepath.Join(t.TempDir(), "sites", "a", "Caddyfile")
	b := filepath.Join(t.TempDir(), "sites", "b", "Caddyfile")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	c, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	pa, err := c.Create(a)
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	pb, err := c.Create(b)
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}
	entries, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(entries))
	}
	for _, e := range entries {
		switch {
		case e.Path == pa:
			if !e.BelongsTo(a) || e.BelongsTo(b) {
				t.Errorf("backup for a: BelongsTo(a)=%v, BelongsTo(b)=%v, want true/false", e.BelongsTo(a), e.BelongsTo(b))
			}
		case e.Path == pb:
			if !e.BelongsTo(b) || e.BelongsTo(a) {
				t.Errorf("backup for b: BelongsTo(b)=%v, BelongsTo(a)=%v, want true/false", e.BelongsTo(b), e.BelongsTo(a))
			}
		}
	}
}

func TestBelongsTo_LegacyFallsBackToBasename(t *testing.T) {
	dir := t.TempDir()
	// One legacy backup and one known-identity backup for the same file.
	legacy := filepath.Join(dir, "2026-08-01T20-10-00-003-Caddyfile")
	if err := os.WriteFile(legacy, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Dir: dir})
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
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(entries))
	}
	for _, e := range entries {
		if !e.BelongsTo(src) {
			t.Errorf("entry %q does not belong to %q", e.Path, src)
		}
	}
}

func TestRetention_DisabledByDefault(t *testing.T) {
	r := Retention{Dir: t.TempDir(), Keep: 0}
	if r.Enabled() {
		t.Fatal("zero Keep must mean retention is disabled")
	}
	removed, err := r.Apply()
	if err != nil {
		t.Fatalf("Apply on a disabled policy: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("disabled retention removed %v", removed)
	}
}

func TestRetention_CleanupOrdering(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	times := []time.Time{
		time.Date(2026, 8, 1, 20, 10, 0, 0, time.UTC), // oldest
		time.Date(2026, 8, 2, 20, 10, 0, 0, time.UTC),
		time.Date(2026, 8, 3, 20, 10, 0, 0, time.UTC),
		time.Date(2026, 8, 4, 20, 10, 0, 0, time.UTC), // newest
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
	// Keep 2 per source: the two oldest backups must be removed, the two
	// newest must survive.
	removed, err := Retention{Dir: dir, Keep: 2}.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %d backups, want 2: %v", len(removed), removed)
	}
	// Deterministic removal order: from the least old to the oldest.
	wantOrder := []string{
		"2026-08-02T20-10-00-001-Caddyfile",
		"2026-08-01T20-10-00-001-Caddyfile",
	}
	for i, name := range wantOrder {
		if got := filepath.Base(removed[i]); got != name {
			t.Errorf("removed[%d] = %q, want %q", i, got, name)
		}
	}
	entries, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries after retention, want 2", len(entries))
	}
	if !entries[0].Time.Equal(times[3]) || !entries[1].Time.Equal(times[2]) {
		t.Errorf("surviving entries = %v, %v; want the two newest backups", entries[0].Time, entries[1].Time)
	}
	// The sidecars of removed backups are gone too.
	if _, err := os.Stat(filepath.Join(dir, "2026-08-02T20-10-00-001-Caddyfile.src")); err == nil {
		t.Error("sidecar of the removed backup survived")
	}
}

func TestRetention_PreservesNewestAndProtected(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	times := []time.Time{
		time.Date(2026, 8, 1, 20, 10, 0, 0, time.UTC),
		time.Date(2026, 8, 2, 20, 10, 0, 0, time.UTC),
		time.Date(2026, 8, 3, 20, 10, 0, 0, time.UTC),
	}
	var newest string
	for _, at := range times {
		c, err := New(Options{Dir: dir, Clock: fixedClock(at)})
		if err != nil {
			t.Fatal(err)
		}
		p, err := c.Create(src)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		newest = p
	}
	// Keep 1: the newest (the just-created backup) must survive even
	// without an explicit protected path.
	removed, err := Retention{Dir: dir, Keep: 1}.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %d backups, want 2", len(removed))
	}
	if _, err := os.Stat(newest); err != nil {
		t.Fatalf("the newest backup was removed: %v", err)
	}

	// An explicitly protected path is never removed even when it is old.
	dir2 := t.TempDir()
	src2 := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(src2, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Dir: dir2, Clock: fixedClock(time.Date(2026, 8, 1, 20, 10, 0, 0, time.UTC))})
	if err != nil {
		t.Fatal(err)
	}
	old, err := c.Create(src2)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	c2, err := New(Options{Dir: dir2, Clock: fixedClock(time.Date(2026, 8, 2, 20, 10, 0, 0, time.UTC))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Create(src2); err != nil {
		t.Fatalf("Create: %v", err)
	}
	removed, err = Retention{Dir: dir2, Keep: 1}.Apply(old)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("the protected backup %q was removed", old)
	}
}

func TestRetention_NeverRemovesLegacyOrUnrelated(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Dir: dir, Clock: fixedClock(time.Date(2026, 8, 1, 20, 10, 0, 0, time.UTC))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Create(src); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A legacy backup (no sidecar) and a stray file that parses as a
	// backup but whose source can never be proven.
	legacy := filepath.Join(dir, "2026-08-02T20-10-00-009-Caddyfile")
	if err := os.WriteFile(legacy, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, "README.md")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := Retention{Dir: dir, Keep: 1}.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("retention removed entries that must be preserved: %v", removed)
	}
	for _, p := range []string{legacy, stray} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("unrelated file %q was removed: %v", p, err)
		}
	}
}

func TestRetention_PermissionFailureReported(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i, at := range []time.Time{
		time.Date(2026, 8, 1, 20, 10, 0, 0, time.UTC),
		time.Date(2026, 8, 2, 20, 10, 0, 0, time.UTC),
		time.Date(2026, 8, 3, 20, 10, 0, 0, time.UTC),
		time.Date(2026, 8, 4, 20, 10, 0, 0, time.UTC),
	} {
		c, err := New(Options{Dir: dir, Clock: fixedClock(at)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Create(src); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	// Block removal by making the directory read-only; removing entries
	// from it then fails on Unix.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("cannot chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	removed, err := Retention{Dir: dir, Keep: 1}.Apply()
	if err == nil {
		t.Fatal("Apply succeeded despite the read-only directory, want a removal failure")
	}
	if len(removed) != 0 {
		t.Fatalf("removed %v despite the read-only directory", removed)
	}
	// All backups must still be present.
	entries, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("List returned %d entries after a failed cleanup, want 4", len(entries))
	}
}
