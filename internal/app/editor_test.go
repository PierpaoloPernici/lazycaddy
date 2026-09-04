package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// fakeEditorFormatter is a programmable Formatter for the editor tests. The
// zero value reports a clean validation (no diagnostics, no error).
type fakeEditorFormatter struct {
	diagnostics []validator.Diagnostic
	err         error
	called      bool
}

func (f *fakeEditorFormatter) FormatAndValidate(ctx context.Context, displayPath string, src []byte) ([]byte, []validator.Diagnostic, error) {
	f.called = true
	return nil, f.diagnostics, f.err
}

// newTestEditor wires an editor over t.TempDir() with a vim command, a
// clean fake formatter, os.ReadFile and the real temp directory so the
// editor temp file can be created and asserted on. A non-empty options
// argument overrides the matching hook.
func newTestEditor(t *testing.T, opts ...EditorOptions) *editor {
	t.Helper()
	dir := t.TempDir()
	tempDir := filepath.Join(dir, "temp")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		t.Fatalf("mkdir temp dir: %v", err)
	}
	o := EditorOptions{
		LookupEnv: func(key string) (string, bool) {
			if key == "VISUAL" {
				return "vim -f", true
			}
			return "", false
		},
		Formatter:   &fakeEditorFormatter{},
		ReadFile:    os.ReadFile,
		SnapshotDir: filepath.Join(dir, "snapshots"),
		TempDir:     tempDir,
	}
	for _, opt := range opts {
		if opt.LookupEnv != nil {
			o.LookupEnv = opt.LookupEnv
		}
		if opt.Formatter != nil {
			o.Formatter = opt.Formatter
		}
		if opt.ReadFile != nil {
			o.ReadFile = opt.ReadFile
		}
		if opt.SnapshotDir != "" {
			o.SnapshotDir = opt.SnapshotDir
		}
		if opt.TempDir != "" {
			o.TempDir = opt.TempDir
		}
	}
	e := NewEditor(o)
	typed, ok := e.(*editor)
	if !ok {
		t.Fatalf("NewEditor returned %T, want *editor", e)
	}
	return typed
}

// sampleDoc parses src as the document at path and fails the test unless it
// parses cleanly.
func sampleDoc(t *testing.T, path, src string) *caddyfile.Document {
	t.Helper()
	doc := caddyfile.Parse([]byte(src))
	doc.Path = path
	if doc.Err != nil {
		t.Fatalf("fixture %s must parse cleanly: %v", path, doc.Err)
	}
	return doc
}

func TestEditorPrepare_WritesRangeAndSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	r := doc.Nodes[0].Range
	session, err := e.Prepare(context.Background(), doc, r)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// The temp file holds exactly the range bytes.
	got := readFileContent(t, session.TempFile)
	if got != src[r.Start:r.End] {
		t.Errorf("temp file = %q, want the exact range bytes %q", got, src[r.Start:r.End])
	}
	// The snapshot holds the full document.
	if got := readFileContent(t, session.SnapshotPath); got != src {
		t.Errorf("snapshot = %q, want the full document %q", got, src)
	}
	// The sidecar is plain text, never JSON: the range line plus the
	// exact document identity.
	sidecar := session.SnapshotPath + ".range"
	wantRange := fmt.Sprintf("%d %d\n%s\n", r.Start, r.End, path)
	if got := readFileContent(t, sidecar); got != wantRange {
		t.Errorf("sidecar = %q, want %q", got, wantRange)
	}
	// The command is the editor argv plus the temp file.
	want := []string{"vim", "-f", session.TempFile}
	if len(session.Cmd) != len(want) {
		t.Fatalf("Cmd = %v, want %v", session.Cmd, want)
	}
	for i := range want {
		if session.Cmd[i] != want[i] {
			t.Errorf("Cmd = %v, want %v", session.Cmd, want)
		}
	}
	if session.DocPath != path {
		t.Errorf("DocPath = %q, want %q", session.DocPath, path)
	}
	if !bytes.Equal(session.Original, []byte(src)) {
		t.Errorf("Original = %q, want %q", session.Original, src)
	}
}

func TestEditorPrepare_UsesDocumentPath(t *testing.T) {
	dir := t.TempDir()
	// The document belongs to an imported file; the editor must target
	// that path, never the root Caddyfile.
	rootPath := filepath.Join(dir, "Caddyfile")
	imported := filepath.Join(dir, "sites", "a.caddy")
	src := "a.example.test {\n\trespond ok\n}\n"
	writeFile(t, rootPath, "import sites/a.caddy\n")
	if err := os.MkdirAll(filepath.Dir(imported), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, imported, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, imported, src)
	session, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if session.DocPath != imported {
		t.Errorf("DocPath = %q, want the imported path %q", session.DocPath, imported)
	}
	if got := readFileContent(t, session.SnapshotPath); got != src {
		t.Errorf("snapshot = %q, want the imported document bytes", got)
	}
}

func TestEditorPrepare_SingleSlotPerDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	session, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// The slot name is a stable 16-hex SHA-256 prefix of the document
	// path: no timestamp and no random suffix.
	sum := sha256.Sum256([]byte(path))
	want := filepath.Join(e.snapshotDir, "editor-"+hex.EncodeToString(sum[:])[:16]+".snapshot")
	if session.SnapshotPath != want {
		t.Fatalf("SnapshotPath = %q, want the stable slot %q", session.SnapshotPath, want)
	}
	// The slot holds the full Caddyfile, which may contain secrets: it
	// must be private to the operator.
	fi, err := os.Stat(session.SnapshotPath)
	if err != nil {
		t.Fatalf("stat slot: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("slot mode = %o, want 0600", got)
	}
	// A second round-trip of the same document overwrites the same slot
	// instead of accumulating a new file: stale slot content is replaced.
	stale := append([]byte(nil), src...)
	stale[0] = 'x'
	writeFile(t, session.SnapshotPath, string(stale))
	session2, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range)
	if err != nil {
		t.Fatalf("second Prepare: %v", err)
	}
	if session2.SnapshotPath != session.SnapshotPath {
		t.Errorf("second Prepare = %q, want the same slot %q", session2.SnapshotPath, session.SnapshotPath)
	}
	if got := readFileContent(t, session.SnapshotPath); got != src {
		t.Errorf("slot = %q, want the document bytes after the overwrite", got)
	}
	// A second document gets its own slot, separate from the first.
	imported := filepath.Join(dir, "site.caddy")
	importedSrc := "site.test {\n\trespond ok\n}\n"
	writeFile(t, imported, importedSrc)
	importedDoc := sampleDoc(t, imported, importedSrc)
	session3, err := e.Prepare(context.Background(), importedDoc, importedDoc.Nodes[0].Range)
	if err != nil {
		t.Fatalf("imported Prepare: %v", err)
	}
	if session3.SnapshotPath == session.SnapshotPath {
		t.Errorf("imported slot = %q, want a slot separate from %q", session3.SnapshotPath, session.SnapshotPath)
	}
}

func TestEditorPrepare_NoEditorConfigured(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)

	e := newTestEditor(t, EditorOptions{
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	doc := sampleDoc(t, path, src)
	_, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range)
	if !errors.Is(err, ErrNoEditor) {
		t.Fatalf("err = %v, want ErrNoEditor", err)
	}
}

func TestEditorPrepare_ExternalChangeBefore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	// The file on disk diverged from the loaded document.
	writeFile(t, path, src+"\n# external edit\n")

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	_, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	// Nothing was prepared: no snapshot dir, no temp file.
	if _, statErr := os.Stat(e.snapshotDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("snapshot dir exists despite preflight conflict: %v", statErr)
	}
}

func TestEditorComplete_NonZeroExitCancels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	session, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	result, err := e.Complete(context.Background(), session, 1)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !result.Cancelled {
		t.Error("Cancelled = false, want true for a non-zero exit")
	}
	if result.Content != nil || result.Diagnostics != nil {
		t.Errorf("non-zero exit must yield no Content/Diagnostics: %+v", result)
	}
	if _, statErr := os.Stat(session.TempFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("temp file still exists after cancellation: %v", statErr)
	}
}

func TestEditorComplete_EmptyResultCancels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	session, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// The editor cleared the file.
	writeFile(t, session.TempFile, "")

	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !result.Cancelled {
		t.Error("Cancelled = false, want true for an empty result")
	}
	if _, statErr := os.Stat(session.TempFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("temp file still exists after cancellation: %v", statErr)
	}
}

func TestEditorComplete_ExternalChangeDuring(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	session, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// The file changed on disk while the editor was open.
	writeFile(t, path, src+"\n# external edit\n")
	writeFile(t, session.TempFile, "example.test {\n\trespond changed\n}\n")

	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !result.Cancelled {
		t.Error("Cancelled = false, want true when the file changed during the edit")
	}
	if _, statErr := os.Stat(session.TempFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("temp file still exists after cancellation: %v", statErr)
	}
}

func TestEditorComplete_ValidResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	edited := "example.test {\n\trespond ok\n\tencode gzip\n}\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	r := doc.Nodes[0].Range
	session, err := e.Prepare(context.Background(), doc, r)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	writeFile(t, session.TempFile, edited)

	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Cancelled {
		t.Error("Cancelled = true, want false for a valid edit")
	}
	if !result.Changed {
		t.Error("Changed = false, want true")
	}
	want, err := caddyfile.Patch([]byte(src), r, []byte(edited))
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !bytes.Equal(result.Content, want) {
		t.Errorf("Content = %q, want the patched document %q", result.Content, want)
	}
	if len(result.Diagnostics) != 0 {
		t.Errorf("Diagnostics = %v, want none", result.Diagnostics)
	}
	if _, statErr := os.Stat(session.TempFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("temp file still exists after completion: %v", statErr)
	}
	// The snapshot is a recovery artifact and must survive the session.
	if _, statErr := os.Stat(session.SnapshotPath); statErr != nil {
		t.Errorf("snapshot removed after completion: %v", statErr)
	}
}

func TestEditorComplete_InvalidResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	edited := "example.test {\n\tbroken\n}\n"
	writeFile(t, path, src)

	diags := []validator.Diagnostic{
		{Path: path, Line: 2, Column: 1, Message: "unknown directive", Severity: validator.SeverityError},
	}
	e := newTestEditor(t, EditorOptions{Formatter: &fakeEditorFormatter{diagnostics: diags}})
	doc := sampleDoc(t, path, src)
	session, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	writeFile(t, session.TempFile, edited)

	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Cancelled {
		t.Error("Cancelled = true, want false: an invalid edit is not a cancellation")
	}
	if !result.Changed {
		t.Error("Changed = false, want true")
	}
	if result.Content != nil {
		t.Errorf("Content = %q, want nil: an invalid document must not be savable", result.Content)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("Diagnostics = %v, want the formatter diagnostics", result.Diagnostics)
	}
	if _, statErr := os.Stat(session.TempFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("temp file still exists after an invalid result: %v", statErr)
	}
}

// TestEditorComplete_ErrWithDiagnosticsKeepsDiags verifies that a
// validation failure that carries both an error (the *validator.ExitError)
// and parse diagnostics preserves the diagnostics in the result instead of
// dropping them: the UI must open the diagnostics modal, not just show a
// status line. Only a hard failure with no diagnostics at all (missing
// binary, timeout) surfaces as an error.
func TestEditorComplete_ErrWithDiagnosticsKeepsDiags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	edited := "example.test {\n\tbroken\n}\n"
	writeFile(t, path, src)

	diags := []validator.Diagnostic{
		{Path: path, Line: 2, Column: 1, Message: "unknown directive", Severity: validator.SeverityError},
	}
	e := newTestEditor(t, EditorOptions{Formatter: &fakeEditorFormatter{
		diagnostics: diags,
		err:         errors.New("caddy exit 1"),
	}})
	doc := sampleDoc(t, path, src)
	session, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	writeFile(t, session.TempFile, edited)

	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v, diagnostics must travel in the result", err)
	}
	if result.Cancelled {
		t.Error("Cancelled = true, want false: an invalid edit is not a cancellation")
	}
	if !result.Changed {
		t.Error("Changed = false, want true")
	}
	if result.Content != nil {
		t.Errorf("Content = %q, want nil: an invalid document must not be savable", result.Content)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("Diagnostics = %v, want the formatter diagnostics preserved", result.Diagnostics)
	}
	if result.Diagnostics[0].Message != "unknown directive" {
		t.Errorf("Diagnostics[0].Message = %q, want %q", result.Diagnostics[0].Message, "unknown directive")
	}
}

func TestEditorComplete_NoChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	r := doc.Nodes[0].Range
	session, err := e.Prepare(context.Background(), doc, r)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// The editor left the range bytes untouched.
	writeFile(t, session.TempFile, src[r.Start:r.End])

	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Changed {
		t.Error("Changed = true, want false when the range was untouched")
	}
	if result.Content != nil {
		t.Errorf("Content = %q, want nil for a no-change result", result.Content)
	}
	if result.Cancelled {
		t.Error("Cancelled = true, want false for a no-change result")
	}
}

func TestEditor_CRLFPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "a.example.test {\r\n\trespond a\r\n}\r\nb.example.test {\r\n\trespond b\r\n}\r\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	r := doc.Nodes[0].Range // the a.example.test block
	editedRange := "a.example.test {\r\n\trespond a\r\n\tencode gzip\r\n}\r\n"
	session, err := e.Prepare(context.Background(), doc, r)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	writeFile(t, session.TempFile, editedRange)

	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	want := editedRange + src[r.End:]
	if !bytes.Equal(result.Content, []byte(want)) {
		t.Errorf("Content = %q, want %q (CRLF must survive byte-for-byte outside the range)", result.Content, want)
	}
	// The untouched tail is byte-identical to the original.
	if !bytes.Equal(result.Content[len(result.Content)-len(src[r.End:]):], []byte(src[r.End:])) {
		t.Errorf("bytes outside the edited range were modified")
	}
}

func TestEditor_ByteForByteOutsideRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "# header comment\n\nexample.test {\n\t# inner comment\n\trespond ok\n}\n\n# trailing comment\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	r := doc.Nodes[0].Range
	editedRange := "example.test {\n\t# inner comment\n\trespond ok\n\tencode gzip\n}\n"
	session, err := e.Prepare(context.Background(), doc, r)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	writeFile(t, session.TempFile, editedRange)

	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// The recomposed document equals the untouched prefix, the edited range
	// and the untouched suffix: comments, blank lines and indentation
	// outside the range are preserved byte-for-byte.
	want := string(src[:r.Start]) + editedRange + string(src[r.End:])
	if string(result.Content) != want {
		t.Errorf("Content = %q, want %q (comments and indentation outside the range must be preserved)", result.Content, want)
	}
	// The untouched suffix is byte-identical to the original.
	if !bytes.Equal(result.Content[len(result.Content)-len(src[r.End:]):], []byte(src[r.End:])) {
		t.Errorf("bytes outside the edited range were modified")
	}
}

func TestSplitCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []string
		wantErr error
	}{
		{
			name:    "code with wait flag",
			command: "code --wait",
			want:    []string{"code", "--wait"},
		},
		{
			name:    "vim foreground flag",
			command: "vim -f",
			want:    []string{"vim", "-f"},
		},
		{
			name:    "absolute binary path",
			command: "/usr/bin/nano",
			want:    []string{"/usr/bin/nano"},
		},
		{
			name:    "quoted path with spaces",
			command: `"/Applications/Visual Studio Code.app/Contents/MacOS/Code" --wait`,
			want:    []string{"/Applications/Visual Studio Code.app/Contents/MacOS/Code", "--wait"},
		},
		{
			name:    "double-quoted group",
			command: `"vim -f"`,
			want:    []string{"vim -f"},
		},
		{
			name:    "single-quoted group",
			command: `'code --wait'`,
			want:    []string{"code --wait"},
		},
		{
			name:    "backslash escape inside double quotes",
			command: `"a\"b"`,
			want:    []string{`a"b`},
		},
		{
			name:    "backslash literal inside single quotes",
			command: `'a\b'`,
			want:    []string{`a\b`},
		},
		{
			name:    "quoted shell injection stays one argument",
			command: `"vim; rm -rf /"`,
			want:    []string{`vim; rm -rf /`},
		},
		{
			name:    "unquoted semicolons stay literal arguments",
			command: "vim; rm -rf /",
			want:    []string{"vim;", "rm", "-rf", "/"},
		},
		{
			name:    "empty double-quoted argument dropped",
			command: `code "" --wait`,
			want:    []string{"code", "--wait"},
		},
		{
			name:    "empty single-quoted argument dropped",
			command: `code ''`,
			want:    []string{"code"},
		},
		{
			name:    "empty quoted command yields no editor",
			command: `""`,
			wantErr: ErrNoEditor,
		},
		{
			name:    "empty string",
			command: "",
			wantErr: ErrNoEditor,
		},
		{
			name:    "whitespace only",
			command: "   \t  ",
			wantErr: ErrNoEditor,
		},
		{
			name:    "unmatched single quote",
			command: "'unclosed",
			wantErr: errors.New("unmatched"),
		},
		{
			name:    "unmatched double quote",
			command: `"unclosed`,
			wantErr: errors.New("unmatched"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitCommand(tt.command)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("splitCommand(%q) = %v, want error %v", tt.command, got, tt.wantErr)
				}
				if tt.wantErr != ErrNoEditor && errors.Is(err, ErrNoEditor) {
					t.Errorf("splitCommand(%q) err = %v, want a quote error, not ErrNoEditor", tt.command, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitCommand(%q): %v", tt.command, err)
			}
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Errorf("splitCommand(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestEditorPrepareFull_WritesWholeDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	session, err := e.PrepareFull(context.Background(), doc)
	if err != nil {
		t.Fatalf("PrepareFull: %v", err)
	}
	if session.Mode != EditFull {
		t.Errorf("Mode = %v, want EditFull", session.Mode)
	}
	if session.Range.Start != 0 || session.Range.End != len(src) {
		t.Errorf("Range = %+v, want [0,%d)", session.Range, len(src))
	}
	if got := readFileContent(t, session.TempFile); got != src {
		t.Errorf("temp file = %q, want the whole document %q", got, src)
	}
	if got := readFileContent(t, session.SnapshotPath); got != src {
		t.Errorf("snapshot = %q, want the whole document", got)
	}
	if !bytes.Equal(session.RangeBytes, []byte(src)) {
		t.Errorf("RangeBytes = %q, want the whole document", session.RangeBytes)
	}
}

func TestEditorPrepareFull_UsesDocumentPath(t *testing.T) {
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "Caddyfile")
	imported := filepath.Join(dir, "sites", "a.caddy")
	src := "a.example.test {\n\trespond ok\n}\n"
	writeFile(t, rootPath, "import sites/a.caddy\n")
	if err := os.MkdirAll(filepath.Dir(imported), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, imported, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, imported, src)
	session, err := e.PrepareFull(context.Background(), doc)
	if err != nil {
		t.Fatalf("PrepareFull: %v", err)
	}
	if session.DocPath != imported {
		t.Errorf("DocPath = %q, want the imported path %q", session.DocPath, imported)
	}
	if got := readFileContent(t, session.TempFile); got != src {
		t.Errorf("temp file = %q, want the imported document bytes", got)
	}
}

func TestEditorPrepareFull_EmptyResultValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	session, err := e.PrepareFull(context.Background(), doc)
	if err != nil {
		t.Fatalf("PrepareFull: %v", err)
	}
	// The editor emptied the file: for a full edit that is a legitimate
	// (if unlikely valid) document, not a cancellation.
	writeFile(t, session.TempFile, "")

	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Cancelled {
		t.Error("Cancelled = true, want false: an empty full edit goes through validation")
	}
	if !result.Changed {
		t.Error("Changed = false, want true (the document was emptied)")
	}
	if result.Content == nil || !bytes.Equal(result.Content, []byte{}) {
		t.Errorf("Content = %q, want an empty (but non-nil) validated document", result.Content)
	}
	if len(result.Diagnostics) != 0 {
		t.Errorf("Diagnostics = %v, want none", result.Diagnostics)
	}
}

func TestEditorPrepareFull_EmptyResultInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)

	diags := []validator.Diagnostic{
		{Path: path, Line: 1, Column: 1, Message: "empty document", Severity: validator.SeverityError},
	}
	e := newTestEditor(t, EditorOptions{Formatter: &fakeEditorFormatter{diagnostics: diags}})
	doc := sampleDoc(t, path, src)
	session, err := e.PrepareFull(context.Background(), doc)
	if err != nil {
		t.Fatalf("PrepareFull: %v", err)
	}
	writeFile(t, session.TempFile, "")

	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Cancelled {
		t.Error("Cancelled = true, want false: an empty full edit is validated, not cancelled")
	}
	if !result.Changed {
		t.Error("Changed = false, want true")
	}
	if result.Content != nil {
		t.Errorf("Content = %q, want nil: the empty document did not validate", result.Content)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("Diagnostics = %v, want the formatter diagnostics", result.Diagnostics)
	}
}

// TestEditorComplete_NodeEmptyStillCancels locks the node-edit policy: an
// empty result from a node-range edit stays a cancellation, unlike a full
// edit where the empty document goes through validation.
func TestEditorComplete_NodeEmptyStillCancels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)

	e := newTestEditor(t)
	doc := sampleDoc(t, path, src)
	session, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if session.Mode != EditNode {
		t.Fatalf("Mode = %v, want EditNode", session.Mode)
	}
	writeFile(t, session.TempFile, "")

	result, err := e.Complete(context.Background(), session, 0)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !result.Cancelled {
		t.Error("Cancelled = false, want true: an empty node edit is a cancellation")
	}
	if result.Content != nil || result.Diagnostics != nil {
		t.Errorf("empty node edit must yield no Content/Diagnostics: %+v", result)
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

func TestEditorPrepare_TempFileErrorBranches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)
	boom := errors.New("boom")

	t.Run("create temp", func(t *testing.T) {
		e := newTestEditor(t)
		doc := sampleDoc(t, path, src)
		swap(t, &createTemp, func(dir, pattern string) (*os.File, error) {
			if strings.Contains(pattern, "lazycaddy-editor-") {
				return nil, boom
			}
			return os.CreateTemp(dir, pattern)
		})
		if _, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range); !strings.Contains(err.Error(), "create temp file") {
			t.Errorf("err = %v, want the create-temp failure", err)
		}
	})
	t.Run("write", func(t *testing.T) {
		e := newTestEditor(t)
		doc := sampleDoc(t, path, src)
		swap(t, &fileWrite, func(f *os.File, b []byte) (int, error) {
			if strings.Contains(f.Name(), "lazycaddy-editor-") {
				return 0, boom
			}
			return f.Write(b)
		})
		if _, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range); !strings.Contains(err.Error(), "write temp file") {
			t.Errorf("err = %v, want the write-temp failure", err)
		}
	})
	t.Run("close", func(t *testing.T) {
		e := newTestEditor(t)
		doc := sampleDoc(t, path, src)
		swap(t, &fileClose, func(f *os.File) error {
			if strings.Contains(f.Name(), "lazycaddy-editor-") {
				return boom
			}
			return f.Close()
		})
		if _, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range); !strings.Contains(err.Error(), "close temp file") {
			t.Errorf("err = %v, want the close-temp failure", err)
		}
	})
	t.Run("whitespace command", func(t *testing.T) {
		e := newTestEditor(t, EditorOptions{
			LookupEnv: func(key string) (string, bool) {
				switch key {
				case "VISUAL":
					return "", true // set but empty: treated as unset
				case "EDITOR":
					return "   ", true // splits to an empty argv
				}
				return "", false
			},
		})
		doc := sampleDoc(t, path, src)
		if _, err := e.Prepare(context.Background(), doc, doc.Nodes[0].Range); !errors.Is(err, ErrNoEditor) {
			t.Fatalf("err = %v, want ErrNoEditor for a whitespace-only command", err)
		}
	})
}

func TestEditorWriteSnapshot_ErrorBranches(t *testing.T) {
	boom := errors.New("boom")
	dir := t.TempDir()
	path := filepath.Join(dir, "Caddyfile")
	src := "example.test {\n\trespond ok\n}\n"
	writeFile(t, path, src)
	doc := sampleDoc(t, path, src)
	r := caddyfile.SourceRange{Start: 0, End: len(src)}

	t.Run("mkdir", func(t *testing.T) {
		e := newTestEditor(t, EditorOptions{SnapshotDir: filepath.Join(t.TempDir(), "snap")})
		swap(t, &mkdirAll, func(string, os.FileMode) error { return boom })
		if _, err := e.writeSnapshot(doc, r); !errors.Is(err, boom) {
			t.Errorf("err = %v, want the mkdir failure", err)
		}
	})
	t.Run("slot write", func(t *testing.T) {
		e := newTestEditor(t)
		swap(t, &atomicWrite, func(string, []byte) error { return boom })
		if _, err := e.writeSnapshot(doc, r); !errors.Is(err, boom) {
			t.Errorf("err = %v, want the slot write failure", err)
		}
	})
	t.Run("sidecar write", func(t *testing.T) {
		e := newTestEditor(t)
		calls := 0
		swap(t, &atomicWrite, func(string, []byte) error {
			calls++
			if calls == 2 {
				return boom
			}
			return nil
		})
		if _, err := e.writeSnapshot(doc, r); !errors.Is(err, boom) {
			t.Errorf("err = %v, want the sidecar write failure", err)
		}
	})
}
