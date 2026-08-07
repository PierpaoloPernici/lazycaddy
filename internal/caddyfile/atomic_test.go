package caddyfile

import (
	"os"
	"path/filepath"
	"testing"
)

// assertNoTempFiles fails the test when the directory still contains a
// .lazycaddy-* temp file, which WriteAtomic guarantees must never survive.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".lazycaddy-*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temp files left behind: %v", matches)
	}
}

func TestWriteAtomic_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")

	want := []byte("# comment\nlocalhost {\n\treverse_proxy :9000\n}\n")
	if err := WriteAtomic(path, want); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWriteAtomic_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(path, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	replacement := []byte("new content, same path")
	if err := WriteAtomic(path, replacement); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(replacement) {
		t.Fatalf("content = %q, want %q", got, replacement)
	}
	assertNoTempFiles(t, dir)
}

func TestWriteAtomic_PreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	// 0o640 is a distinctive mode that a fresh CreateTemp would not
	// produce, so it proves the mode is copied from the target.
	if err := os.WriteFile(path, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtomic(path, []byte("replacement")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %o, want 640", got)
	}
}

func TestWriteAtomic_NoTempLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")

	// Success path leaves no temp file.
	if err := WriteAtomic(path, []byte("first")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	assertNoTempFiles(t, dir)

	// An induced failure (unwritable directory) must not leak a temp file
	// either.
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission checks")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := WriteAtomic(path, []byte("second")); err == nil {
		t.Fatal("WriteAtomic succeeded in an unwritable directory")
	}
	assertNoTempFiles(t, dir)
}

func TestWriteAtomic_BytePreserving(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")

	// CRLF line endings and multi-byte runes must survive byte-for-byte:
	// a Caddyfile is the source of truth and a save must not normalize it.
	want := []byte("# caf\u00e9\r\nlocalhost {\r\n\trespond \"h\u00e9llo w\u00f6rld\"\r\n}\r\n")
	if err := WriteAtomic(path, want); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("bytes changed:\n got %q\nwant %q", got, want)
	}
}

func TestCheckWritePreflight_RegularFileOK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CheckWritePreflight(path); err != nil {
		t.Fatalf("CheckWritePreflight: %v", err)
	}
	// The check must not modify the target.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "content" {
		t.Fatalf("target modified by preflight: %q", got)
	}
}

func TestCheckWritePreflight_MissingTargetOK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-yet.txt")

	if err := CheckWritePreflight(path); err != nil {
		t.Fatalf("CheckWritePreflight: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("preflight created the target (stat err = %v)", err)
	}
}

func TestCheckWritePreflight_SymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.WriteFile(target, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := CheckWritePreflight(link); err == nil {
		t.Fatal("CheckWritePreflight accepted a symlink")
	}
}

func TestCheckWritePreflight_DirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sites")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := CheckWritePreflight(sub); err == nil {
		t.Fatal("CheckWritePreflight accepted a directory as a write target")
	}
}

func TestCheckWritePreflight_UnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission checks")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	path := filepath.Join(dir, "Caddyfile")
	if err := CheckWritePreflight(path); err == nil {
		t.Fatal("CheckWritePreflight succeeded in an unwritable directory")
	}
}
