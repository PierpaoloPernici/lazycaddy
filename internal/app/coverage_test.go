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
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

// TestErrorTypes_SurfaceAndUnwrap covers the concrete error types that
// wrap an underlying error with a recovery trail (SaveError, ReloadError,
// RollbackError): Error must render the context and Unwrap must expose the
// cause for errors.Is / errors.As.
func TestErrorTypes_SurfaceAndUnwrap(t *testing.T) {
	cause := errors.New("disk full")
	cases := []struct {
		name   string
		got    error
		want   string
		unwrap error
	}{
		{
			name:   "save",
			got:    &SaveError{BackupPath: "/backups/x", Err: cause},
			want:   "save failed after backup /backups/x: disk full",
			unwrap: cause,
		},
		{
			name:   "reload",
			got:    &ReloadError{Endpoint: "http://localhost:2019", Err: cause},
			want:   "reload via http://localhost:2019 failed: disk full",
			unwrap: cause,
		},
		{
			name:   "rollback",
			got:    &RollbackError{BackupPath: "/backups/x", Err: cause},
			want:   "rollback failed after backup /backups/x: disk full",
			unwrap: cause,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.got.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
			if !errors.Is(tc.got, cause) {
				t.Errorf("errors.Is(%v, cause) = false, want true", tc.got)
			}
		})
	}
}

// TestReadFileOS is the production FileReader default used by the
// rollbacker; it must read real file bytes.
func TestReadFileOS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readFileOS(path)
	if err != nil {
		t.Fatalf("readFileOS: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("readFileOS = %q, want %q", got, "hello")
	}
	if _, err := readFileOS(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("readFileOS on a missing file: expected an error, got nil")
	}
}

// TestLogSourceFunc_NextAndHistory covers the adapter methods that
// delegate straight to the injected functions.
func TestLogSourceFunc_NextAndHistory(t *testing.T) {
	entry := logs.Entry{Raw: []byte("line")}
	src := LogSourceFunc{
		NextFn:    func(ctx context.Context) ([]logs.Entry, error) { return []logs.Entry{entry}, nil },
		HistoryFn: func() []logs.Entry { return []logs.Entry{entry} },
	}
	got, err := src.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(got) != 1 || string(got[0].Raw) != "line" {
		t.Errorf("Next = %v, want one entry", got)
	}
	if h := src.History(); len(h) != 1 {
		t.Errorf("History = %v, want one entry", h)
	}
}

// TestSearcherFunc_Delegates covers the Searcher adapter.
func TestSearcherFunc_Delegates(t *testing.T) {
	want := []SearchResult{{Kind: SearchNode, Label: "hit"}}
	f := SearcherFunc(func(query string, scope SearchScope) []SearchResult {
		if query != "q" {
			t.Errorf("query = %q, want q", query)
		}
		return want
	})
	got := f.Search("q", SearchScope{})
	if len(got) != 1 || got[0].Label != "hit" {
		t.Errorf("Search = %v, want the delegated result", got)
	}
}

// TestEditor_NewEditorDefaults verifies NewEditor fills in the documented
// defaults (os.LookupEnv, os.ReadFile, os.TempDir, time.Now) when the
// hooks are left nil.
func TestEditor_NewEditorDefaults(t *testing.T) {
	e := NewEditor(EditorOptions{})
	typed, ok := e.(*editor)
	if !ok {
		t.Fatalf("NewEditor returned %T, want *editor", e)
	}
	if typed.lookupEnv == nil || typed.readFile == nil || typed.clock == nil {
		t.Fatal("NewEditor must default nil lookupEnv/readFile/clock hooks")
	}
	if typed.tempDir == "" {
		t.Fatal("NewEditor must default tempDir to the OS temp directory")
	}
}

// TestEditorPrepare_DefensiveErrors covers the pre-flight error paths that
// return before anything is written: nil documents, invalid ranges, no
// configured editor and malformed editor commands.
func TestEditorPrepare_DefensiveErrors(t *testing.T) {
	doc := sampleDoc(t, "Caddyfile", "example.test {\n\trespond ok\n}\n")
	valid := caddyfile.SourceRange{Start: 0, End: len(doc.Source)}

	t.Run("nil document", func(t *testing.T) {
		e := newTestEditor(t)
		if _, err := e.Prepare(context.Background(), nil, valid); err == nil {
			t.Error("Prepare(nil doc): expected an error, got nil")
		}
		if _, err := e.PrepareFull(context.Background(), nil); err == nil {
			t.Error("PrepareFull(nil doc): expected an error, got nil")
		}
	})

	t.Run("invalid range", func(t *testing.T) {
		e := newTestEditor(t)
		bad := caddyfile.SourceRange{Start: 0, End: len(doc.Source) + 1}
		if _, err := e.Prepare(context.Background(), doc, bad); err == nil {
			t.Error("Prepare with an out-of-bounds range: expected an error, got nil")
		}
	})

	t.Run("no editor configured", func(t *testing.T) {
		e := newTestEditor(t, EditorOptions{
			LookupEnv: func(string) (string, bool) { return "", false },
		})
		_, err := e.Prepare(context.Background(), doc, valid)
		if !errors.Is(err, ErrNoEditor) {
			t.Errorf("Prepare without $VISUAL/$EDITOR = %v, want ErrNoEditor", err)
		}
	})

	t.Run("unmatched quote", func(t *testing.T) {
		e := newTestEditor(t, EditorOptions{
			LookupEnv: func(key string) (string, bool) {
				if key == "VISUAL" {
					return `vim "unterminated`, true
				}
				return "", false
			},
		})
		if _, err := e.Prepare(context.Background(), doc, valid); err == nil {
			t.Error("Prepare with an unterminated quote: expected an error, got nil")
		}
	})

	t.Run("empty command", func(t *testing.T) {
		e := newTestEditor(t, EditorOptions{
			LookupEnv: func(key string) (string, bool) {
				if key == "VISUAL" {
					return "", true
				}
				return "", false
			},
		})
		_, err := e.Prepare(context.Background(), doc, valid)
		if !errors.Is(err, ErrNoEditor) {
			t.Errorf("Prepare with an empty VISUAL = %v, want ErrNoEditor", err)
		}
	})
}

// TestEditorPrepare_ExternalChangeDetection verifies the preflight guards:
// an unreadable document and a document whose on-disk bytes differ from
// the loaded source both yield ErrConflict and write nothing.
func TestEditorPrepare_ExternalChangeDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)
	doc := sampleDoc(t, path, src)
	valid := caddyfile.SourceRange{Start: 0, End: len(doc.Source)}

	t.Run("unreadable document", func(t *testing.T) {
		e := newTestEditor(t, EditorOptions{
			ReadFile: func(string) ([]byte, error) { return nil, errors.New("permission denied") },
		})
		_, err := e.Prepare(context.Background(), doc, valid)
		if !errors.Is(err, ErrConflict) {
			t.Errorf("Prepare with an unreadable doc = %v, want ErrConflict", err)
		}
	})

	t.Run("bytes differ from loaded source", func(t *testing.T) {
		e := newTestEditor(t, EditorOptions{
			ReadFile: func(string) ([]byte, error) { return []byte("changed on disk"), nil },
		})
		_, err := e.Prepare(context.Background(), doc, valid)
		if !errors.Is(err, ErrConflict) {
			t.Errorf("Prepare with diverged bytes = %v, want ErrConflict", err)
		}
	})
}

// TestEditorPrepare_FilesystemFailures verifies that snapshot and temp-file
// failures surface as errors without leaving a session behind.
func TestEditorPrepare_FilesystemFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)
	doc := sampleDoc(t, path, src)
	valid := caddyfile.SourceRange{Start: 0, End: len(doc.Source)}

	t.Run("snapshot dir is a file", func(t *testing.T) {
		blocker := filepath.Join(dir, "blocker")
		writeFile(t, blocker, "not a directory")
		e := newTestEditor(t, EditorOptions{
			SnapshotDir: filepath.Join(blocker, "snapshots"),
		})
		if _, err := e.Prepare(context.Background(), doc, valid); err == nil {
			t.Error("Prepare with an unusable snapshot dir: expected an error, got nil")
		}
	})

	t.Run("temp dir does not exist", func(t *testing.T) {
		e := newTestEditor(t, EditorOptions{
			TempDir: filepath.Join(dir, "no-such-temp"),
		})
		if _, err := e.Prepare(context.Background(), doc, valid); err == nil {
			t.Error("Prepare with a missing temp dir: expected an error, got nil")
		}
	})
}

// TestEditorComplete_DefensivePaths covers Complete's cancellation and
// error branches: nil sessions, nil formatters and documents that changed
// on disk during the edit.
func TestEditorComplete_DefensivePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)
	doc := sampleDoc(t, path, src)

	t.Run("nil session", func(t *testing.T) {
		e := newTestEditor(t)
		if _, err := e.Complete(context.Background(), nil, 0); err == nil {
			t.Error("Complete(nil session): expected an error, got nil")
		}
	})

	t.Run("read failure cancels", func(t *testing.T) {
		// A stateful reader: the first call (Prepare's preflight) returns
		// the real bytes; the second call (Complete's downstream conflict
		// check) fails.
		calls := 0
		e := newTestEditor(t, EditorOptions{
			ReadFile: func(p string) ([]byte, error) {
				calls++
				if calls > 1 {
					return nil, errors.New("gone")
				}
				return os.ReadFile(p)
			},
		})
		valid := caddyfile.SourceRange{Start: 0, End: len(doc.Source)}
		session, err := e.Prepare(context.Background(), doc, valid)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		result, err := e.Complete(context.Background(), session, 0)
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if !result.Cancelled {
			t.Error("Complete with an unreadable doc: Cancelled = false, want true")
		}
		if result.Content != nil {
			t.Error("Complete with an unreadable doc must not produce content")
		}
	})

	t.Run("no formatter rejects change", func(t *testing.T) {
		// newTestEditor cannot express a nil formatter (its options merge
		// skips nil hooks), so the editor is built directly.
		e := NewEditor(EditorOptions{
			LookupEnv: func(key string) (string, bool) {
				if key == "VISUAL" {
					return "vim -f", true
				}
				return "", false
			},
			ReadFile: os.ReadFile,
		})
		valid := caddyfile.SourceRange{Start: 0, End: len(doc.Source)}
		session, err := e.Prepare(context.Background(), doc, valid)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		// Overwrite the temp file with a changed result so Complete must
		// validate it.
		if err := os.WriteFile(session.TempFile, []byte("example.test {\n\trespond changed\n}\n"), 0o600); err != nil {
			t.Fatalf("write temp: %v", err)
		}
		if _, err := e.Complete(context.Background(), session, 0); err == nil {
			t.Error("Complete without a formatter on a changed edit: expected an error, got nil")
		}
	})

	t.Run("bytes diverged during edit cancels", func(t *testing.T) {
		calls := 0
		e := newTestEditor(t, EditorOptions{
			ReadFile: func(p string) ([]byte, error) {
				calls++
				if calls > 1 {
					return []byte("someone else wrote"), nil
				}
				return os.ReadFile(p)
			},
		})
		valid := caddyfile.SourceRange{Start: 0, End: len(doc.Source)}
		session, err := e.Prepare(context.Background(), doc, valid)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		result, err := e.Complete(context.Background(), session, 0)
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if !result.Cancelled {
			t.Error("Complete with diverged bytes: Cancelled = false, want true")
		}
	})
}

// TestEditorPrepareFull_SessionCoversWholeDocument verifies PrepareFull
// hands the entire document to the editor and keeps EditFull mode so an
// empty result stays validatable.
func TestEditorPrepareFull_SessionCoversWholeDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)
	doc := sampleDoc(t, path, src)

	e := newTestEditor(t)
	session, err := e.PrepareFull(context.Background(), doc)
	if err != nil {
		t.Fatalf("PrepareFull: %v", err)
	}
	if session.Mode != EditFull {
		t.Errorf("Mode = %v, want EditFull", session.Mode)
	}
	if session.Range.Start != 0 || session.Range.End != len(doc.Source) {
		t.Errorf("Range = [%d:%d), want the whole document [0:%d)", session.Range.Start, session.Range.End, len(doc.Source))
	}
	if !strings.EqualFold(session.DocPath, path) {
		t.Errorf("DocPath = %q, want %q", session.DocPath, path)
	}
}

// TestEditorComplete_TempFileGoneCancels verifies that an unreadable
// editor temp file cancels the edit without applying anything.
func TestEditorComplete_TempFileGoneCancels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)
	doc := sampleDoc(t, path, src)

	e := newTestEditor(t)
	valid := caddyfile.SourceRange{Start: 0, End: len(doc.Source)}
	session, err := e.Prepare(context.Background(), doc, valid)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.Remove(session.TempFile); err != nil {
		t.Fatalf("remove temp file: %v", err)
	}
	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !result.Cancelled {
		t.Error("Complete with a missing temp file: Cancelled = false, want true")
	}
}

// TestEditorComplete_PatchErrorSurfaces verifies that a malformed session
// range (which can never come from Prepare, but guards the boundary)
// surfaces as an error instead of silently recomposing nothing.
func TestEditorComplete_PatchErrorSurfaces(t *testing.T) {
	temp := filepath.Join(t.TempDir(), "edited")
	if err := os.WriteFile(temp, []byte("content"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	session := &EditSession{
		Mode:         EditFull,
		DocPath:      "Caddyfile",
		Range:        caddyfile.SourceRange{Start: 5, End: 2}, // invalid: end < start
		Original:     []byte("abc"),
		RangeBytes:   []byte("abc"),
		TempFile:     temp,
		SnapshotPath: "",
		Cmd:          []string{"vim", temp},
	}
	e := newTestEditor(t, EditorOptions{
		ReadFile: func(string) ([]byte, error) { return []byte("abc"), nil },
	})
	if _, err := e.Complete(context.Background(), session, 0); err == nil {
		t.Error("Complete with an invalid session range: expected an error, got nil")
	}
}

// TestEditorComplete_HardFormatterError verifies that a validation
// infrastructure failure (no diagnostics attached) surfaces as an error
// rather than a cancelled or silently accepted edit.
func TestEditorComplete_HardFormatterError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)
	doc := sampleDoc(t, path, src)

	hardErr := errors.New("caddy binary missing")
	e := newTestEditor(t, EditorOptions{
		Formatter: &fakeEditorFormatter{err: hardErr},
	})
	valid := caddyfile.SourceRange{Start: 0, End: len(doc.Source)}
	session, err := e.Prepare(context.Background(), doc, valid)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.WriteFile(session.TempFile, []byte("example.test {\n\trespond changed\n}\n"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if _, err := e.Complete(context.Background(), session, 0); !errors.Is(err, hardErr) {
		t.Errorf("Complete = %v, want the formatter's hard error", err)
	}
}

// TestRollbacker_ConstructorDefaults covers the NewRollbacker boundary:
// an empty Dir is rejected and a nil ReadFile falls back to readFileOS.
func TestRollbacker_ConstructorDefaults(t *testing.T) {
	if _, err := NewRollbacker(RollbackerOptions{}); err == nil {
		t.Error("NewRollbacker without a Dir: expected an error, got nil")
	}
	rb, err := NewRollbacker(RollbackerOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewRollbacker: %v", err)
	}
	r, ok := rb.(*rollbacker)
	if !ok {
		t.Fatalf("NewRollbacker returned %T, want *rollbacker", rb)
	}
	if r.readFile == nil {
		t.Error("readFile is nil, want the readFileOS default")
	}
	if r.write == nil {
		t.Error("write is nil, want the caddyfile.WriteAtomic default")
	}
}

// TestRollbacker_ReadErrors covers the error paths of ListBackups,
// ReadBackup and ReadCurrent: failures are wrapped with context, never
// swallowed.
func TestRollbacker_ReadErrors(t *testing.T) {
	dir := t.TempDir()
	rb, err := NewRollbacker(RollbackerOptions{
		Dir:      dir,
		ReadFile: func(string) ([]byte, error) { return nil, errors.New("boom") },
	})
	if err != nil {
		t.Fatalf("NewRollbacker: %v", err)
	}

	// backup.List fails when the backup directory is not a directory.
	notADir := filepath.Join(dir, "f")
	writeFile(t, notADir, "x")
	rbBad, err := NewRollbacker(RollbackerOptions{Dir: notADir})
	if err != nil {
		t.Fatalf("NewRollbacker: %v", err)
	}
	if _, err := rbBad.ListBackups("Caddyfile", nil); err == nil {
		t.Error("ListBackups over a non-directory: expected an error, got nil")
	}

	if _, err := rb.ReadBackup(backup.Entry{Path: "x"}); err == nil {
		t.Error("ReadBackup with a failing reader: expected an error, got nil")
	}
	if _, err := rb.ReadCurrent("x"); err == nil {
		t.Error("ReadCurrent with a failing reader: expected an error, got nil")
	}
}

// TestRollback_CreatorMissingAndFailing covers the two post-validation
// gates of Rollback: a missing creator (read-only wiring) aborts before
// touching anything, and a creator whose backup fails is surfaced as an
// error.
func TestRollback_CreatorMissingAndFailing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	writeFile(t, path, "example.test {\n\trespond ok\n}\n")
	src := []byte("example.test {\n\trespond ok\n}\n")
	docs := []*caddyfile.Document{{Path: path}}

	backupPath := filepath.Join(dir, "backup")
	writeFile(t, backupPath, string(src))

	// Missing creator: validation passes (fake validator), then the
	// read-only gate rejects the restore.
	rb, err := NewRollbacker(RollbackerOptions{
		Dir:       dir,
		ReadFile:  os.ReadFile,
		Validator: &fakeRollbackValidator{},
	})
	if err != nil {
		t.Fatalf("NewRollbacker: %v", err)
	}
	if _, err := rb.Rollback(context.Background(), path, src, backupPath, docs); err == nil {
		t.Error("Rollback without a creator: expected an error, got nil")
	}

	// Failing creator: the pre-restore backup cannot be created.
	failCreator := &failingCreator{err: errors.New("backup dir unwritable")}
	rbFail, err := NewRollbacker(RollbackerOptions{
		Dir:       dir,
		Creator:   failCreator,
		ReadFile:  os.ReadFile,
		Validator: &fakeRollbackValidator{},
	})
	if err != nil {
		t.Fatalf("NewRollbacker: %v", err)
	}
	if _, err := rbFail.Rollback(context.Background(), path, src, backupPath, docs); err == nil {
		t.Error("Rollback with a failing creator: expected an error, got nil")
	}
}

// TestBackupEntriesFor_SkipsNilDocs covers the defensive skip of nil
// document entries while counting same-basename ambiguity.
func TestBackupEntriesFor_SkipsNilDocs(t *testing.T) {
	entries := []backup.Entry{{Path: "/b/a", Base: "a", SourceKnown: true, Source: "/srv/a.caddy"}}
	got := BackupEntriesFor(entries, "/srv/a.caddy", []*caddyfile.Document{nil})
	if len(got) != 1 || got[0].Path != "/b/a" {
		t.Errorf("BackupEntriesFor with a nil doc = %v, want the known-source entry kept", got)
	}
}
