package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/PierpaoloPernici/lazycaddy/internal/backup"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// SaveResult reports the outcome of a successful save.
type SaveResult struct {
	// BackupPath is the path of the backup created before the write.
	BackupPath string
	// RetentionErr reports a retention cleanup failure after the write
	// succeeded; nil on success or when retention is disabled. The save
	// itself completed; the error is reported so cleanup failures are
	// never silent.
	RetentionErr error
}

// Saver persists a validated working copy of the Caddyfile. UI models
// depend on this interface and never touch the filesystem directly.
type Saver interface {
	// Save preflights path, verifies the file on disk still matches
	// original (no external change since it was loaded), creates a
	// backup of the current file, then atomically replaces it with
	// working. It returns ErrConflict when the file on disk no longer
	// matches original — in that case nothing is written and no backup
	// is created. When the write fails after a backup was created, the
	// returned error is a *SaveError carrying the recovery backup path.
	Save(ctx context.Context, path string, original, working []byte) (SaveResult, error)
}

// ErrConflict reports that the file on disk changed after it was
// loaded; the operator must reload before saving.
var ErrConflict = errors.New("file changed on disk since it was loaded")

// SaveError is returned when a save fails after a backup was created,
// so the caller can point the operator at the recovery copy.
type SaveError struct {
	BackupPath string
	Err        error
}

// Error implements error.
func (e *SaveError) Error() string {
	return fmt.Sprintf("save failed after backup %s: %v", e.BackupPath, e.Err)
}

// Unwrap returns the underlying write error.
func (e *SaveError) Unwrap() error { return e.Err }

// saver is the production Saver implementation. It is deliberately a
// small struct with an unexported write hook so package-internal tests
// can force a post-backup write failure; NewSaver wires the caddyfile
// primitives and the backup creator. A configured retention policy is
// applied after a successful write.
type saver struct {
	creator   backup.Creator
	readFile  FileReader
	write     func(path string, data []byte) error
	retention backup.Retention
}

// verifyUnchangedTarget is the shared external-change guard used by both
// saves and rollbacks: it preflights path, re-reads it and reports
// ErrConflict when the file on disk no longer matches original. On
// success it returns the freshly read current bytes. Keeping one helper
// for both workflows means the guards can never drift apart.
func verifyUnchangedTarget(readFile FileReader, path string, original []byte) ([]byte, error) {
	if err := caddyfile.CheckWritePreflight(path); err != nil {
		return nil, fmt.Errorf("preflight: %w", err)
	}
	current, err := readFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	if !bytes.Equal(current, original) {
		return nil, fmt.Errorf("%w", ErrConflict)
	}
	return current, nil
}

// Save implements Saver.
func (s *saver) Save(ctx context.Context, path string, original, working []byte) (SaveResult, error) {
	if _, err := verifyUnchangedTarget(s.readFile, path, original); err != nil {
		return SaveResult{}, err
	}
	backupPath, err := s.creator.Create(path)
	if err != nil {
		return SaveResult{}, fmt.Errorf("backup: %w", err)
	}
	if err := s.write(path, working); err != nil {
		return SaveResult{}, &SaveError{BackupPath: backupPath, Err: err}
	}
	res := SaveResult{BackupPath: backupPath}
	if s.retention.Enabled() {
		// Cleanup runs only after a successful write. A cleanup failure
		// never undoes the save; it is reported so the operator can
		// reclaim the space.
		if _, err := s.retention.Apply(backupPath); err != nil {
			res.RetentionErr = err
		}
	}
	return res, nil
}

// NewSaver returns a Saver that reads through readFile (conflict
// check), preflights and writes through the caddyfile primitives and
// backs up through creator. readFile may be os.ReadFile. Retention is
// disabled.
func NewSaver(creator backup.Creator, readFile FileReader) Saver {
	return NewSaverWithRetention(creator, readFile, backup.Retention{})
}

// NewSaverWithRetention returns a Saver like NewSaver that additionally
// applies the retention policy after a successful write. A non-positive
// retention.Keep disables cleanup.
func NewSaverWithRetention(creator backup.Creator, readFile FileReader, retention backup.Retention) Saver {
	return &saver{
		creator:   creator,
		readFile:  readFile,
		write:     caddyfile.WriteAtomic,
		retention: retention,
	}
}
