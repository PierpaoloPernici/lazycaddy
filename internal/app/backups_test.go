package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/backup"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// fakeRollbackValidator is a programmable RollbackValidator for tests.
// It records the root path and the full file set so tests can verify the
// graph-context validation: every document is present, and the target
// document's bytes have been replaced by the backup content.
type fakeRollbackValidator struct {
	err      error
	files    []validator.File
	rootPath string
}

func (f *fakeRollbackValidator) ValidateConfig(ctx context.Context, rootPath string, files []validator.File) ([]validator.Diagnostic, error) {
	f.files = append([]validator.File(nil), files...)
	f.rootPath = rootPath
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
}

// newTestRollbacker wires a real backup creator and the caddyfile
// write primitive over a temp dir, returning the concrete *rollbacker
// so tests can override the write step and drive retention.
func newTestRollbacker(t *testing.T, opts ...func(*rollbacker)) *rollbacker {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "backups")
	creator, err := backup.New(backup.Options{Dir: dir})
	if err != nil {
		t.Fatalf("backup.New: %v", err)
	}
	rb, err := NewRollbacker(RollbackerOptions{
		Dir:       dir,
		Creator:   creator,
		ReadFile:  os.ReadFile,
		Validator: &fakeRollbackValidator{},
	})
	if err != nil {
		t.Fatalf("NewRollbacker: %v", err)
	}
	typed := rb.(*rollbacker)
	for _, opt := range opts {
		opt(typed)
	}
	return typed
}

// docPath creates a source file at a nested path and returns the path.
func docPath(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{t.TempDir()}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBackupEntriesFor_SameBasenameImportedFiles(t *testing.T) {
	// The single most important association rule: two imported documents
	// share the basename "Caddyfile", and each backup must resolve to
	// exactly its own source file — never to the sibling.
	rootPath := docPath(t, "root")
	aPath := docPath(t, "sites", "a", "Caddyfile")
	bPath := docPath(t, "sites", "b", "Caddyfile")
	for _, p := range []string{rootPath, aPath, bPath} {
		if err := os.WriteFile(p, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(t.TempDir(), "backups")
	creator, err := backup.New(backup.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	backupA, err := creator.Create(aPath)
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	backupB, err := creator.Create(bPath)
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}

	// The loaded document set: root plus both same-basename imports.
	docs := []*caddyfile.Document{
		{Path: rootPath},
		{Path: aPath},
		{Path: bPath},
	}

	entries, err := backup.List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(entries))
	}

	forA := BackupEntriesFor(entries, aPath, docs)
	if len(forA) != 1 {
		t.Fatalf("backups for %s = %d, want exactly 1", aPath, len(forA))
	}
	if forA[0].Path != backupA {
		t.Fatalf("backups for %s resolved to %q, want %q (its own backup, never the sibling's)", aPath, forA[0].Path, backupA)
	}

	forB := BackupEntriesFor(entries, bPath, docs)
	if len(forB) != 1 {
		t.Fatalf("backups for %s = %d, want exactly 1", bPath, len(forB))
	}
	if forB[0].Path != backupB {
		t.Fatalf("backups for %s resolved to %q, want %q", bPath, forB[0].Path, backupB)
	}
}

func TestBackupEntriesFor_LegacyAmbiguityExcluded(t *testing.T) {
	// A legacy backup (valid filename, no identity sidecar) whose
	// basename is shared by another loaded document must never be offered
	// as a rollback candidate for either document: the source cannot be
	// proven and guessing is forbidden.
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "2026-08-01T20-10-00-001-Caddyfile")
	if err := os.WriteFile(legacy, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	aPath := docPath(t, "sites", "a", "Caddyfile")
	bPath := docPath(t, "sites", "b", "Caddyfile")
	docs := []*caddyfile.Document{{Path: aPath}, {Path: bPath}}

	entries, err := backup.List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}
	if entries[0].SourceKnown {
		t.Fatal("legacy backup has a known source, want unknown")
	}

	for _, p := range []string{aPath, bPath} {
		if got := BackupEntriesFor(entries, p, docs); len(got) != 0 {
			t.Errorf("BackupEntriesFor(%s) offered %v, want none for an ambiguous legacy backup", p, got)
		}
	}
}

func TestBackupEntriesFor_LegacyUnambiguousIncluded(t *testing.T) {
	// When the basename is unique across the loaded documents, a legacy
	// backup is safe to offer: its basename resolves to exactly one file.
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "2026-08-01T20-10-00-001-common.conf")
	if err := os.WriteFile(legacy, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootPath := docPath(t, "Caddyfile")
	commonPath := docPath(t, "common.conf")
	docs := []*caddyfile.Document{{Path: rootPath}, {Path: commonPath}}

	entries, err := backup.List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := BackupEntriesFor(entries, commonPath, docs)
	if len(got) != 1 {
		t.Fatalf("BackupEntriesFor(common.conf) = %d, want 1 (unique basename)", len(got))
	}
	if got[0].Path != legacy {
		t.Errorf("resolved to %q, want the legacy backup", got[0].Path)
	}
}

func TestRollback_Success(t *testing.T) {
	rb := newTestRollbacker(t)
	path := docPath(t, "Caddyfile")
	const v1 = "example.test {\n\trespond ok\n}\n"
	const v2 = "example.test {\n\trespond ok\n\tencode gzip\n}\n"
	writeFile(t, path, v1)

	// Snapshot v1 into a backup, then advance the file to v2.
	backupPath, err := rb.creator.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeFile(t, path, v2)

	res, err := rb.Rollback(context.Background(), path, []byte(v2), backupPath, []*caddyfile.Document{{Path: path}})
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if res.RestoredFrom != backupPath {
		t.Errorf("RestoredFrom = %q, want %q", res.RestoredFrom, backupPath)
	}
	if res.BackupPath == "" {
		t.Fatal("BackupPath empty, want the pre-rollback backup")
	}
	if got := readFileContent(t, path); got != v1 {
		t.Errorf("file after rollback = %q, want the restored backup %q", got, v1)
	}
	// The pre-rollback backup holds the v2 bytes, so the operation is
	// reversible.
	if got := readFileContent(t, res.BackupPath); got != v2 {
		t.Errorf("pre-rollback backup content = %q, want the pre-rollback file %q", got, v2)
	}
	// The restored content went through graph-context validation: the
	// target document's Source in the validation file set is the backup
	// bytes, and the root path is the document's own path.
	val := rb.validator.(*fakeRollbackValidator)
	if val.rootPath != path {
		t.Errorf("validation root path = %q, want the target path %q", val.rootPath, path)
	}
	if len(val.files) != 1 {
		t.Fatalf("validation files = %d, want 1", len(val.files))
	}
	if string(val.files[0].Source) != v1 {
		t.Errorf("validated target bytes = %q, want the backup content %q", val.files[0].Source, v1)
	}
}

func TestRollback_ValidatesGraphWithImportedFragment(t *testing.T) {
	// An imported fragment (e.g. a snippet-only file) is only valid in
	// the context of the full graph. Rollback must validate the whole
	// graph with the target document's bytes replaced by the backup.
	rb := newTestRollbacker(t)
	root := docPath(t, "Caddyfile")
	imported := docPath(t, "snippets", "common.caddy")
	writeFile(t, root, "example.test {\n\timport snippets/common.caddy\n}\n")
	writeFile(t, imported, "(snippet) {\n\trespond ok\n}\n")
	backupPath, err := rb.creator.Create(imported)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// The current imported file diverged from its backup.
	const currentImport = "# drifted\n"
	writeFile(t, imported, currentImport)

	docs := []*caddyfile.Document{
		{Path: root, Source: []byte("example.test {\n\timport snippets/common.caddy\n}\n")},
		{Path: imported, Source: []byte(currentImport)},
	}
	res, err := rb.Rollback(context.Background(), imported, []byte(currentImport), backupPath, docs)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := readFileContent(t, imported); got != "(snippet) {\n\trespond ok\n}\n" {
		t.Errorf("imported file after rollback = %q, want the restored backup", got)
	}
	if res.BackupPath == "" {
		t.Fatal("BackupPath empty, want the pre-rollback backup")
	}
	val := rb.validator.(*fakeRollbackValidator)
	if val.rootPath != root {
		t.Errorf("validation root = %q, want the root document %q", val.rootPath, root)
	}
	if len(val.files) != 2 {
		t.Fatalf("validation files = %d, want the full graph (2 documents)", len(val.files))
	}
	// The target document carries the backup bytes; the root and sibling
	// carry their graph sources.
	for _, f := range val.files {
		switch f.Path {
		case imported:
			if string(f.Source) != "(snippet) {\n\trespond ok\n}\n" {
				t.Errorf("validated target bytes = %q, want the backup content", f.Source)
			}
		case root:
			if string(f.Source) != "example.test {\n\timport snippets/common.caddy\n}\n" {
				t.Errorf("validated root bytes = %q, want the graph source", f.Source)
			}
		default:
			t.Errorf("unexpected validation file path %q", f.Path)
		}
	}
}

func TestRollback_ValidationFailure(t *testing.T) {
	rb := newTestRollbacker(t)
	path := docPath(t, "Caddyfile")
	const v1 = "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, v1)

	backupPath, err := rb.creator.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Corrupt the backup content so validation must fail.
	writeFile(t, backupPath, "not a valid caddyfile {\n")
	rb.validator.(*fakeRollbackValidator).err = errors.New("validate: parse error")

	_, err = rb.Rollback(context.Background(), path, []byte(v1), backupPath, []*caddyfile.Document{{Path: path}})
	if !errors.Is(err, ErrRollbackInvalid) {
		t.Fatalf("err = %v, want ErrRollbackInvalid", err)
	}
	if got := readFileContent(t, path); got != v1 {
		t.Errorf("file changed despite invalid backup: %q", got)
	}
	entries := backupEntries(t, filepath.Dir(backupPath))
	if len(entries) != 1 {
		t.Errorf("backup count after failed rollback = %d, want 1 (no pre-rollback backup created)", len(entries))
	}
}

func TestRollback_ConflictDetected(t *testing.T) {
	rb := newTestRollbacker(t)
	path := docPath(t, "Caddyfile")
	const v1 = "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, v1)
	backupPath, err := rb.creator.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// The file changes on disk between the flow's capture and the rollback.
	writeFile(t, path, v1+"\n# external edit\n")

	_, err = rb.Rollback(context.Background(), path, []byte(v1), backupPath, []*caddyfile.Document{{Path: path}})
	if !errors.Is(err, ErrRollbackConflict) {
		t.Fatalf("err = %v, want ErrRollbackConflict", err)
	}
	got := readFileContent(t, path)
	if got != v1+"\n# external edit\n" {
		t.Errorf("file was modified despite the conflict: %q", got)
	}
	entries := backupEntries(t, filepath.Dir(backupPath))
	if len(entries) != 1 {
		t.Errorf("backup count after conflict = %d, want 1", len(entries))
	}
}

func TestRollback_BackupBeforeWrite(t *testing.T) {
	rb := newTestRollbacker(t)
	path := docPath(t, "Caddyfile")
	const v1 = "example.test {\n\trespond ok\n}\n"
	const v2 = "example.test {\n\trespond ok\n\tencode gzip\n}\n"
	writeFile(t, path, v1)
	backupPath, err := rb.creator.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeFile(t, path, v2)

	// Count backups before the rollback.
	before := backupEntries(t, filepath.Dir(backupPath))

	res, err := rb.Rollback(context.Background(), path, []byte(v2), backupPath, []*caddyfile.Document{{Path: path}})
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	after := backupEntries(t, filepath.Dir(backupPath))
	if len(after) != len(before)+1 {
		t.Fatalf("backup count = %d, want %d (one pre-rollback backup)", len(after), len(before)+1)
	}
	// The new backup captures the pre-rollback (v2) state.
	if got := readFileContent(t, res.BackupPath); got != v2 {
		t.Errorf("pre-rollback backup = %q, want v2", got)
	}
}

func TestRollback_AtomicRestoreFailure(t *testing.T) {
	rb := newTestRollbacker(t)
	path := docPath(t, "Caddyfile")
	const v1 = "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, v1)
	backupPath, err := rb.creator.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := backupEntries(t, filepath.Dir(backupPath))

	boom := errors.New("restore exploded")
	rb.write = func(string, []byte) error { return boom }

	_, err = rb.Rollback(context.Background(), path, []byte(v1), backupPath, []*caddyfile.Document{{Path: path}})
	var rbErr *RollbackError
	if !errors.As(err, &rbErr) {
		t.Fatalf("err = %v, want *RollbackError", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the sentinel unwrappable", err)
	}
	if rbErr.BackupPath == "" {
		t.Fatal("RollbackError.BackupPath is empty")
	}
	if got := readFileContent(t, path); got != v1 {
		t.Errorf("file changed despite restore failure: %q", got)
	}
	// The pre-rollback backup exists and holds the pre-restore bytes, and
	// the existing backups are untouched.
	if _, statErr := os.Stat(rbErr.BackupPath); statErr != nil {
		t.Errorf("pre-rollback backup %q missing: %v", rbErr.BackupPath, statErr)
	}
	if got := readFileContent(t, rbErr.BackupPath); got != v1 {
		t.Errorf("pre-rollback backup content = %q, want v1", got)
	}
	after := backupEntries(t, filepath.Dir(backupPath))
	if len(after) != len(before)+1 {
		t.Errorf("backup count after failed restore = %d, want %d (only the pre-rollback backup added)", len(after), len(before)+1)
	}
}

func TestRollback_ReadOnlyGating(t *testing.T) {
	// A rollbacker without a creator (read-only wiring) must fail before
	// touching anything, after validation, with a clear error.
	dir := filepath.Join(t.TempDir(), "backups")
	rb, err := NewRollbacker(RollbackerOptions{
		Dir:       dir,
		ReadFile:  os.ReadFile,
		Validator: &fakeRollbackValidator{},
	})
	if err != nil {
		t.Fatalf("NewRollbacker: %v", err)
	}
	path := docPath(t, "Caddyfile")
	writeFile(t, path, "example.test {\n\trespond ok\n}\n")

	_, err = rb.Rollback(context.Background(), path, []byte("example.test {\n\trespond ok\n}\n"), "/no/such/backup", []*caddyfile.Document{{Path: path}})
	if err == nil {
		t.Fatal("Rollback succeeded without a creator")
	}
	if errors.Is(err, ErrRollbackConflict) || errors.Is(err, ErrRollbackInvalid) {
		t.Errorf("err = %v, want a missing-capability error", err)
	}
	if got := readFileContent(t, path); got != "example.test {\n\trespond ok\n}\n" {
		t.Errorf("file changed in read-only mode: %q", got)
	}
}

func TestRollback_ValidationUnavailable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	creator, err := backup.New(backup.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	rb, err := NewRollbacker(RollbackerOptions{Dir: dir, Creator: creator, ReadFile: os.ReadFile})
	if err != nil {
		t.Fatalf("NewRollbacker: %v", err)
	}
	path := docPath(t, "Caddyfile")
	writeFile(t, path, "example.test {\n\trespond ok\n}\n")
	backupPath, err := creator.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = rb.Rollback(context.Background(), path, []byte("example.test {\n\trespond ok\n}\n"), backupPath, []*caddyfile.Document{{Path: path}})
	if err == nil {
		t.Fatal("Rollback succeeded without a validator")
	}
	if got := readFileContent(t, path); got != "example.test {\n\trespond ok\n}\n" {
		t.Errorf("file changed without validation: %q", got)
	}
}

func TestRollback_RetentionAppliedOnSuccess(t *testing.T) {
	rb := newTestRollbacker(t, func(r *rollbacker) {
		r.retention = backup.Retention{Dir: r.dir, Keep: 2}
	})
	path := docPath(t, "Caddyfile")
	const v1 = "example.test {\n\trespond ok\n}\n"
	const v2 = "example.test {\n\trespond ok\n\tencode gzip\n}\n"
	const v3 = "example.test {\n\trespond ok\n\tencode zstd\n}\n"
	writeFile(t, path, v1)
	b1, err := rb.creator.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeFile(t, path, v2)
	b2, err := rb.creator.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeFile(t, path, v3)

	res, err := rb.Rollback(context.Background(), path, []byte(v3), b2, []*caddyfile.Document{{Path: path}})
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if res.RetentionErr != nil {
		t.Fatalf("RetentionErr = %v, want nil", res.RetentionErr)
	}
	entries, err := backup.List(rb.dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// The oldest backup (b1) is beyond Keep=2 and must be removed; the
	// pre-rollback backup and the restored-from backup survive.
	if _, err := os.Stat(b1); err == nil {
		t.Errorf("oldest backup %q survived retention (Keep=2)", b1)
	}
	if _, err := os.Stat(b2); err != nil {
		t.Errorf("restored-from backup %q was removed: %v", b2, err)
	}
	if _, err := os.Stat(res.BackupPath); err != nil {
		t.Errorf("pre-rollback backup %q was removed: %v", res.BackupPath, err)
	}
	if len(entries) != 2 {
		t.Errorf("List returned %d entries after retention, want 2", len(entries))
	}
}

func TestRollback_RetentionFailureReported(t *testing.T) {
	// A retention directory that is actually a file makes the cleanup
	// listing fail, so the failure is reported instead of being silent.
	rb := newTestRollbacker(t, func(r *rollbacker) {
		fake := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(fake, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		r.retention = backup.Retention{Dir: fake, Keep: 1}
	})
	path := docPath(t, "Caddyfile")
	const v1 = "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, v1)
	backupPath, err := rb.creator.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := rb.Rollback(context.Background(), path, []byte(v1), backupPath, []*caddyfile.Document{{Path: path}})
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if res.RetentionErr == nil {
		t.Fatal("RetentionErr = nil, want a reported cleanup failure")
	}
	// The rollback itself completed despite the cleanup failure.
	if got := readFileContent(t, path); got != v1 {
		t.Errorf("file after rollback = %q, want v1", got)
	}
}

func TestSaver_RetentionDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	writeFile(t, path, "v1\n")
	creator, err := backup.New(backup.Options{Dir: filepath.Join(dir, "backups")})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSaver(creator, os.ReadFile)
	res, err := s.Save(context.Background(), path, []byte("v1\n"), []byte("v2\n"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if res.RetentionErr != nil {
		t.Errorf("RetentionErr = %v, want nil when retention is disabled", res.RetentionErr)
	}
	entries, err := backup.List(filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("backup count = %d, want 1 (no cleanup when disabled)", len(entries))
	}
}

func TestSaver_RetentionAppliedAfterSave(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	path := filepath.Join(dir, "Caddyfile")
	writeFile(t, path, "v1\n")
	creator, err := backup.New(backup.Options{Dir: backupDir})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSaverWithRetention(creator, os.ReadFile, backup.Retention{Dir: backupDir, Keep: 1})
	current := []byte("v1\n")
	for i := 0; i < 3; i++ {
		next := []byte("v2\n")
		res, err := s.Save(context.Background(), path, current, next)
		if err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
		if res.RetentionErr != nil {
			t.Fatalf("Save %d RetentionErr = %v", i, res.RetentionErr)
		}
		current = append([]byte(nil), next...)
	}
	entries, err := backup.List(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("backup count after Keep=1 saves = %d, want 1", len(entries))
	}
}

func TestBackupListOrdering_NewestFirst(t *testing.T) {
	// The rollbacker's ListBackups preserves the index order (newest
	// first) after filtering to one source.
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	creator, err := backup.New(backup.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	path := docPath(t, "Caddyfile")
	writeFile(t, path, "v1\n")
	b1, err := creator.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "v2\n")
	b2, err := creator.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := NewRollbacker(RollbackerOptions{Dir: dir, Creator: creator, ReadFile: os.ReadFile})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := rb.ListBackups(path, []*caddyfile.Document{{Path: path}})
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ListBackups returned %d entries, want 2", len(entries))
	}
	if entries[0].Path != b2 {
		t.Errorf("entries[0] = %q, want the newest %q", entries[0].Path, b2)
	}
	if entries[1].Path != b1 {
		t.Errorf("entries[1] = %q, want the oldest %q", entries[1].Path, b1)
	}
}

func TestRollback_ReadBackupAndCurrent(t *testing.T) {
	rb := newTestRollbacker(t)
	path := docPath(t, "Caddyfile")
	writeFile(t, path, "on-disk\n")
	backupPath, err := rb.creator.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "changed\n")

	current, err := rb.ReadCurrent(path)
	if err != nil {
		t.Fatalf("ReadCurrent: %v", err)
	}
	if string(current) != "changed\n" {
		t.Errorf("ReadCurrent = %q, want the on-disk bytes", current)
	}
	entries, err := rb.ListBackups(path, []*caddyfile.Document{{Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("ListBackups returned %d entries, want 1", len(entries))
	}
	content, err := rb.ReadBackup(entries[0])
	if err != nil {
		t.Fatalf("ReadBackup: %v", err)
	}
	if string(content) != "on-disk\n" {
		t.Errorf("ReadBackup = %q, want the backed-up bytes", content)
	}
	if entries[0].Path != backupPath {
		t.Errorf("entry path = %q, want %q", entries[0].Path, backupPath)
	}
}

func TestRollback_DefensiveGraphErrors(t *testing.T) {
	// A rollback whose document set cannot be used for graph-context
	// validation must fail before anything is written or backed up:
	// nil docs, an empty docs slice, and a slice whose root (first
	// element) is nil.
	cases := []struct {
		name string
		docs []*caddyfile.Document
	}{
		{"nil docs", nil},
		{"empty docs", []*caddyfile.Document{}},
		{"nil root", []*caddyfile.Document{nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rb := newTestRollbacker(t)
			path := docPath(t, "Caddyfile")
			const content = "example.test {\n\trespond ok\n}\n"
			writeFile(t, path, content)
			backupPath, err := rb.creator.Create(path)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			before := backupEntries(t, filepath.Dir(backupPath))

			_, err = rb.Rollback(context.Background(), path, []byte(content), backupPath, tc.docs)
			if err == nil {
				t.Fatal("Rollback succeeded for a defensive graph error")
			}
			// The target is untouched and no new backup was created.
			if got := readFileContent(t, path); got != content {
				t.Errorf("file was modified despite the graph error: %q", got)
			}
			after := backupEntries(t, filepath.Dir(backupPath))
			if len(after) != len(before) {
				t.Errorf("backup count changed: %d -> %d", len(before), len(after))
			}
		})
	}
}
