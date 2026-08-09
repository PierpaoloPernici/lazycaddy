package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/backup"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// newTestSaver wires a real backup creator over configDir and returns
// the concrete *saver so tests can override the write step.
func newTestSaver(t *testing.T, configDir string) (*saver, string) {
	t.Helper()
	backupDir := filepath.Join(configDir, "backups")
	creator, err := backup.New(backup.Options{Dir: backupDir})
	if err != nil {
		t.Fatalf("backup.New: %v", err)
	}
	s := NewSaver(creator, os.ReadFile)
	typed, ok := s.(*saver)
	if !ok {
		t.Fatalf("NewSaver returned %T, want *saver", s)
	}
	return typed, backupDir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFileContent(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// backupEntries lists the backup directory, treating a missing
// directory as "no backups". Identity sidecars (the ".src" files that
// carry each backup's source path) are part of their backup entry and
// are not counted separately.
func backupEntries(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read backup dir %s: %v", dir, err)
	}
	out := entries[:0]
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".src") {
			continue
		}
		out = append(out, e)
	}
	return out
}

func TestSave_WritesWorkingBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	const original = "example.test {\n\trespond ok\n}\n"
	const working = "example.test {\n\trespond ok\n\tencode gzip\n}\n"
	writeFile(t, path, original)

	s, _ := newTestSaver(t, dir)
	res, err := s.Save(context.Background(), path, []byte(original), []byte(working))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if res.BackupPath == "" {
		t.Error("BackupPath is empty, want a backup path")
	}
	if got := readFileContent(t, path); got != working {
		t.Errorf("file content = %q, want %q", got, working)
	}
	if got := readFileContent(t, res.BackupPath); got != original {
		t.Errorf("backup content = %q, want the original %q", got, original)
	}
	tmp, err := filepath.Glob(filepath.Join(dir, ".lazycaddy-*.tmp"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(tmp) != 0 {
		t.Errorf("leftover temp files in config dir: %v", tmp)
	}
}

func TestSave_ConflictDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	const original = "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, original)

	s, backupDir := newTestSaver(t, dir)

	// Simulate an external edit between load and save.
	edited := original + "\n# edited externally\n"
	writeFile(t, path, edited)

	_, err := s.Save(context.Background(), path, []byte(original), []byte(original+"\n# working\n"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Save err = %v, want ErrConflict", err)
	}
	if got := readFileContent(t, path); got != edited {
		t.Errorf("file was modified despite conflict: %q", got)
	}
	if entries := backupEntries(t, backupDir); len(entries) != 0 {
		t.Errorf("backup created despite conflict: %v", entries)
	}
}

func TestSave_PreflightSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.caddy")
	writeFile(t, target, "example.test {\n\trespond ok\n}\n")
	path := filepath.Join(dir, "Caddyfile")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	s, backupDir := newTestSaver(t, dir)
	_, err := s.Save(context.Background(), path, []byte("stale"), []byte("working"))
	if err == nil {
		t.Fatal("Save succeeded on a symlink, want preflight rejection")
	}
	if errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want a preflight error, not ErrConflict", err)
	}
	if !strings.Contains(err.Error(), "preflight") {
		t.Errorf("err = %v, want a preflight-prefixed error", err)
	}
	if entries := backupEntries(t, backupDir); len(entries) != 0 {
		t.Errorf("backup created despite preflight failure: %v", entries)
	}
}

type failingCreator struct{ err error }

func (c *failingCreator) Create(srcPath string) (string, error) { return "", c.err }

func TestSave_BackupFailureAborts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	const original = "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, original)

	boom := errors.New("backup disk full")
	s := &saver{creator: &failingCreator{err: boom}, readFile: os.ReadFile, write: caddyfile.WriteAtomic}
	_, err := s.Save(context.Background(), path, []byte(original), []byte("working"))
	if err == nil {
		t.Fatal("Save succeeded, want a backup error")
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the creator error wrapped", err)
	}
	if !strings.Contains(err.Error(), "backup:") {
		t.Errorf("err = %v, want a backup-prefixed error", err)
	}
	if got := readFileContent(t, path); got != original {
		t.Errorf("file changed despite backup failure: %q", got)
	}
}

func TestSave_WriteFailureCarriesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	const original = "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, original)

	s, backupDir := newTestSaver(t, dir)
	boom := errors.New("write exploded")
	s.write = func(string, []byte) error { return boom }

	_, err := s.Save(context.Background(), path, []byte(original), []byte("working"))
	var saveErr *SaveError
	if !errors.As(err, &saveErr) {
		t.Fatalf("err = %v, want *SaveError", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the sentinel unwrappable", err)
	}
	if !errors.Is(saveErr.Err, boom) {
		t.Errorf("SaveError.Err = %v, want the sentinel", saveErr.Err)
	}
	if saveErr.BackupPath == "" {
		t.Fatal("SaveError.BackupPath is empty")
	}
	if !strings.HasPrefix(saveErr.BackupPath, backupDir) {
		t.Errorf("backup path %q is outside the backup dir %q", saveErr.BackupPath, backupDir)
	}
	if _, statErr := os.Stat(saveErr.BackupPath); statErr != nil {
		t.Errorf("SaveError.BackupPath %q does not exist: %v", saveErr.BackupPath, statErr)
	}
	if got := readFileContent(t, path); got != original {
		t.Errorf("file changed despite write failure: %q", got)
	}
}

func TestSave_PreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	const original = "example.test {\n\trespond ok\n}\n"
	const working = "example.test {\n\trespond ok\n\tencode gzip\n}\n"
	writeFile(t, path, original)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	s, _ := newTestSaver(t, dir)
	if _, err := s.Save(context.Background(), path, []byte(original), []byte(working)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("mode = %o, want 640", got)
	}
}

func TestSave_ReadConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	const original = "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, original)

	s, backupDir := newTestSaver(t, dir)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	_, err := s.Save(context.Background(), path, []byte(original), []byte("working"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Save err = %v, want ErrConflict", err)
	}
	if entries := backupEntries(t, backupDir); len(entries) != 0 {
		t.Errorf("backup created despite read conflict: %v", entries)
	}
}
