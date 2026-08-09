package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PierpaoloPernici/lazycaddy/internal/backup"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// ErrRollbackConflict reports that the target file changed on disk after
// the rollback flow started; the operator must re-inspect before rolling
// back. Nothing is written and no backup is created. It aliases the save
// conflict guard so the two workflows share one conflict semantics.
var ErrRollbackConflict = ErrConflict

// ErrRollbackInvalid reports that the selected backup does not validate
// as a Caddyfile; the rollback is aborted and the target and existing
// backups are left unchanged.
var ErrRollbackInvalid = errors.New("backup does not validate as a Caddyfile")

// RollbackResult reports the outcome of a successful rollback.
type RollbackResult struct {
	// BackupPath is the backup of the pre-rollback file that was created
	// before the restore, so the operator can undo the rollback.
	BackupPath string
	// RestoredFrom is the path of the backup entry that was restored.
	RestoredFrom string
	// RetentionErr reports a retention cleanup failure after the restore
	// succeeded; nil on success or when retention is disabled.
	RetentionErr error
}

// RollbackError is returned when a rollback write fails after the
// pre-rollback backup was created. The target file is unchanged and the
// backup at BackupPath holds the pre-rollback bytes for recovery.
type RollbackError struct {
	BackupPath string
	Err        error
}

// Error implements error.
func (e *RollbackError) Error() string {
	return fmt.Sprintf("rollback failed after backup %s: %v", e.BackupPath, e.Err)
}

// Unwrap returns the underlying write error.
func (e *RollbackError) Unwrap() error { return e.Err }

// RollbackValidator validates candidate backup bytes in the context of
// the full document graph. UI models depend on this interface and never
// import the validator package directly.
type RollbackValidator interface {
	// ValidateConfig validates a configuration graph through `caddy
	// validate`: every document's real directory layout is mirrored into
	// a temporary tree (so relative imports between the documents resolve
	// correctly) and the mirrored root is validated. rootPath is the root
	// document's path; files carries every document, with the target
	// document's Source already replaced by the candidate backup bytes.
	// It returns (nil, nil) when the graph validates.
	ValidateConfig(ctx context.Context, rootPath string, files []validator.File) ([]validator.Diagnostic, error)
}

// Rollbacker lists, compares and restores backups of a source document.
// UI models depend on this interface and never touch the backup
// directory or the filesystem directly.
type Rollbacker interface {
	// ListBackups returns the backups belonging to the exact source file
	// srcPath, newest first. docs is the current resolved document set;
	// it is used to reject identity-less legacy backups whose basename is
	// shared by another document, so a backup is never offered for the
	// wrong same-basename file. Missing or unreadable backup files are
	// never fabricated: their entries still list, but reading them fails
	// later and is surfaced as an error, never as silent wrong content.
	ListBackups(srcPath string, docs []*caddyfile.Document) ([]backup.Entry, error)
	// ReadBackup reads the exact bytes of one backup entry.
	ReadBackup(entry backup.Entry) ([]byte, error)
	// ReadCurrent reads the current on-disk bytes of path, so the UI can
	// compare a backup against the file as it is now (not the in-memory
	// graph, which may lag behind disk).
	ReadCurrent(path string) ([]byte, error)
	// Rollback restores the backup at backupPath over path. It follows
	// the same guard sequence as a save: preflight, re-read path and
	// compare against original (ErrConflict when it changed), then reads
	// the backup content, validates the full document graph with the
	// target's bytes replaced by the backup (ErrRollbackInvalid when it
	// does not validate), creates a backup of the current file, and
	// atomically replaces path with the restored content. docs is the
	// current resolved document set (documents[0] is the root); the
	// graph is what the restored content is validated in context of. A
	// write failure returns a *RollbackError carrying the pre-rollback
	// backup path. A configured retention policy is applied after a
	// successful restore; its failure is reported in
	// RollbackResult.RetentionErr. Exactly one source document is ever
	// affected.
	Rollback(ctx context.Context, path string, original []byte, backupPath string, docs []*caddyfile.Document) (RollbackResult, error)
}

// RollbackerOptions configures a production Rollbacker.
type RollbackerOptions struct {
	// Dir is the backup directory backups are listed from. Required for
	// listing.
	Dir string
	// Creator backs up the current file before a restore. It may be nil
	// in read-only mode; Rollback then errors before touching anything.
	Creator backup.Creator
	// ReadFile reads the target file (conflict check) and backup files.
	// Defaults to os.ReadFile.
	ReadFile FileReader
	// Write atomically replaces the target. Defaults to
	// caddyfile.WriteAtomic. nil means the default.
	Write func(path string, data []byte) error
	// Validator validates the restored content through a temporary file.
	// It may be nil when no caddy binary is configured; Rollback then
	// errors before touching anything.
	Validator RollbackValidator
	// Retention is applied after a successful rollback. Zero (disabled)
	// by default.
	Retention backup.Retention
}

// NewRollbacker returns a Rollbacker with the given options. Dir is
// required; nil hooks fall back to the documented defaults.
func NewRollbacker(opts RollbackerOptions) (Rollbacker, error) {
	if opts.Dir == "" {
		return nil, errors.New("rollback: Dir is required")
	}
	r := &rollbacker{
		dir:       opts.Dir,
		creator:   opts.Creator,
		readFile:  opts.ReadFile,
		validator: opts.Validator,
		retention: opts.Retention,
		write:     opts.Write,
	}
	if r.readFile == nil {
		r.readFile = readFileOS
	}
	if r.write == nil {
		r.write = caddyfile.WriteAtomic
	}
	return r, nil
}

// readFileOS is the production FileReader default for the rollbacker.
func readFileOS(path string) ([]byte, error) { return os.ReadFile(path) }

// rollbacker is the production Rollbacker implementation.
type rollbacker struct {
	dir       string
	creator   backup.Creator
	readFile  FileReader
	write     func(path string, data []byte) error
	validator RollbackValidator
	retention backup.Retention
}

// ListBackups implements Rollbacker.
func (r *rollbacker) ListBackups(srcPath string, docs []*caddyfile.Document) ([]backup.Entry, error) {
	entries, err := backup.List(r.dir)
	if err != nil {
		return nil, fmt.Errorf("rollback: list backups: %w", err)
	}
	return BackupEntriesFor(entries, srcPath, docs), nil
}

// ReadBackup implements Rollbacker.
func (r *rollbacker) ReadBackup(entry backup.Entry) ([]byte, error) {
	data, err := r.readFile(entry.Path)
	if err != nil {
		return nil, fmt.Errorf("rollback: read backup %s: %w", entry.Path, err)
	}
	return data, nil
}

// ReadCurrent implements Rollbacker.
func (r *rollbacker) ReadCurrent(path string) ([]byte, error) {
	data, err := r.readFile(path)
	if err != nil {
		return nil, fmt.Errorf("rollback: read current %s: %w", path, err)
	}
	return data, nil
}

// Rollback implements Rollbacker.
func (r *rollbacker) Rollback(ctx context.Context, path string, original []byte, backupPath string, docs []*caddyfile.Document) (RollbackResult, error) {
	// The same external-change guard a save uses: the target must still
	// match the bytes the operator was shown.
	if _, err := verifyUnchangedTarget(r.readFile, path, original); err != nil {
		return RollbackResult{}, err
	}
	content, err := r.readFile(backupPath)
	if err != nil {
		return RollbackResult{}, fmt.Errorf("rollback: read backup: %w", err)
	}
	if r.validator == nil {
		return RollbackResult{}, errors.New("rollback: validation unavailable (no caddy binary configured)")
	}
	files, rootPath, err := rollbackFiles(path, content, docs)
	if err != nil {
		return RollbackResult{}, err
	}
	// Validate the restored content in the context of the full graph:
	// imported fragment files (snippets, partial blocks) are only valid
	// next to their siblings, so the mirror lets caddy resolve them.
	if _, err := r.validator.ValidateConfig(ctx, rootPath, files); err != nil {
		return RollbackResult{}, fmt.Errorf("%w: %v", ErrRollbackInvalid, err)
	}
	if r.creator == nil {
		return RollbackResult{}, errors.New("rollback: backup creation unavailable (read-only mode)")
	}
	// Backup the current (pre-rollback) file so the operation is
	// reversible, then restore atomically.
	preBackup, err := r.creator.Create(path)
	if err != nil {
		return RollbackResult{}, fmt.Errorf("rollback: backup before restore: %w", err)
	}
	if err := r.write(path, content); err != nil {
		return RollbackResult{}, &RollbackError{BackupPath: preBackup, Err: err}
	}
	res := RollbackResult{BackupPath: preBackup, RestoredFrom: backupPath}
	if r.retention.Enabled() {
		if _, err := r.retention.Apply(preBackup); err != nil {
			res.RetentionErr = err
		}
	}
	return res, nil
}

// rollbackFiles builds the validation file set for a rollback: every
// document of the graph with its current Source, except the target
// document (matched by canonical path), which carries the backup bytes
// instead. The root path is the root document's path (the graph's
// Documents[0], per the ImportGraph contract). A graph without documents
// is an error: there is nothing to validate the restored content in
// context of.
func rollbackFiles(path string, content []byte, docs []*caddyfile.Document) ([]validator.File, string, error) {
	if len(docs) == 0 {
		return nil, "", errors.New("rollback: no document graph to validate against")
	}
	cleanPath := filepath.Clean(path)
	files := make([]validator.File, 0, len(docs))
	for _, d := range docs {
		if d == nil {
			continue
		}
		src := d.Source
		if filepath.Clean(d.Path) == cleanPath {
			src = content
		}
		files = append(files, validator.File{Path: d.Path, Source: src})
	}
	if docs[0] == nil {
		return nil, "", errors.New("rollback: root document is nil")
	}
	return files, docs[0].Path, nil
}

// BackupEntriesFor filters the backup index to the entries belonging to
// srcPath, preserving the index order (newest first). It is the exact
// source-association decision point for rollback candidates:
//
//   - an entry with a known identity (an identity sidecar written by
//     Create) belongs to srcPath only when its canonical source equals
//     srcPath, so two same-basename documents never share candidates;
//   - a legacy entry (no sidecar) falls back to a basename match, but
//     only when srcPath's basename is unambiguous across the loaded
//     documents. When another document shares the basename, legacy
//     entries are excluded entirely: guessing between two same-basename
//     files is never allowed.
//
// A legacy backup is therefore never offered as a rollback candidate for
// a same-basename imported file, no matter how tempting the match.
func BackupEntriesFor(entries []backup.Entry, srcPath string, docs []*caddyfile.Document) []backup.Entry {
	cleanSrc := filepath.Clean(srcPath)
	sameBase := 0
	for _, d := range docs {
		if d == nil {
			continue
		}
		if filepath.Clean(d.Path) != cleanSrc && filepath.Base(d.Path) == filepath.Base(srcPath) {
			sameBase++
		}
	}
	var out []backup.Entry
	for _, e := range entries {
		if !e.BelongsTo(srcPath) {
			continue
		}
		if !e.SourceKnown && sameBase > 0 {
			// The basename is shared: a legacy backup without identity
			// cannot be proven to belong to this exact file.
			continue
		}
		out = append(out, e)
	}
	return out
}
