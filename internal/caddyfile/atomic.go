package caddyfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Injectable filesystem operations. Production code calls through these
// vars; tests swap them (with t.Cleanup restore) to force each error
// branch deterministically instead of relying on permission-dependent
// failures.
var (
	createTemp = os.CreateTemp
	removePath = os.Remove
	fileWrite  = (*os.File).Write
	fileChmod  = (*os.File).Chmod
	fileSync   = (*os.File).Sync
	fileClose  = (*os.File).Close
)

// CheckWritePreflight inspects path and returns nil when an atomic
// replacement is safe to attempt. It reports an error when path
// resolves through a symbolic link, when the target exists but is not
// a regular file, or when the containing directory is not writable.
// The check must not modify the target.
func CheckWritePreflight(path string) error {
	fi, err := os.Lstat(path)
	switch {
	case err == nil:
		// Lstat (not Stat) so a symlink is rejected outright: renaming
		// over it would replace the link itself, silently breaking the
		// relationship the user set up.
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to replace symlink %q", path)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular file %q", path)
		}
	case os.IsNotExist(err):
		// A missing target is fine as long as the directory accepts a
		// new file; that is probed below.
	default:
		return fmt.Errorf("checking %q: %w", path, err)
	}

	// Probe the containing directory by creating a unique temp file and
	// removing it immediately. This mirrors what WriteAtomic will do and
	// fails fast on an unwritable or missing directory, without touching
	// the target.
	probe, err := createTemp(filepath.Dir(path), ".lazycaddy-*.tmp")
	if err != nil {
		return fmt.Errorf("directory %q not writable: %w", filepath.Dir(path), err)
	}
	name := probe.Name()
	if err := fileClose(probe); err != nil {
		_ = removePath(name)
		return fmt.Errorf("closing write probe %q: %w", name, err)
	}
	if err := removePath(name); err != nil {
		return fmt.Errorf("removing write probe %q: %w", name, err)
	}
	return nil
}

// WriteAtomic atomically replaces path with data: it writes a
// temporary file in the same directory, fsyncs it and renames it over
// path. The temporary file is removed on any failure (including the
// fsync and rename steps). When path exists, its permission bits are
// preserved on the new file; a non-existent path is created with the
// temporary file's default mode.
func WriteAtomic(path string, data []byte) error {
	// Capture the existing permissions up front so a save never
	// silently rewrites mode bits. A non-existent path keeps the
	// CreateTemp default mode.
	var mode os.FileMode
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat: %w", err)
	}

	// CreateTemp places the file in the same directory as path, which is
	// what makes the final rename atomic (a rename across filesystems is
	// a copy, not an atomic swap).
	f, err := createTemp(filepath.Dir(path), ".lazycaddy-*.tmp")
	if err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	tempName := f.Name()
	// From here on every failure path must remove the temporary file so
	// a crash or error does not leak partial content next to the real
	// Caddyfile.
	defer func() { _ = removePath(tempName) }()

	if _, err := fileWrite(f, data); err != nil {
		fileClose(f)
		return fmt.Errorf("write temp: %w", err)
	}
	if mode != 0 {
		if err := fileChmod(f, mode); err != nil {
			fileClose(f)
			return fmt.Errorf("chmod temp: %w", err)
		}
	}
	// fsync before rename so the data is durable before it is published
	// under path; otherwise a crash could leave a renamed file with
	// missing or partial content.
	if err := fileSync(f); err != nil {
		fileClose(f)
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := fileClose(f); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace: %w", err)
	}
	return nil
}
