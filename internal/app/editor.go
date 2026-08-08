package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// ErrNoEditor reports that neither $VISUAL nor $EDITOR names a command, so
// there is no editor to launch.
var ErrNoEditor = errors.New("no editor configured (set $VISUAL or $EDITOR)")

// EditSession is the prepared state of one $EDITOR round-trip: the exact
// bytes handed to the editor, the snapshot written before launch and the
// argv to run. Prepare creates it; Complete consumes it.
type EditSession struct {
	// DocPath is the path of the real document the range belongs to. It
	// may be an imported file; it is never the root Caddyfile by accident.
	DocPath string
	// Range is the byte range that the edit may replace.
	Range caddyfile.SourceRange
	// Original is a copy of doc.Source at Prepare time: the baseline for
	// the downstream conflict check and the Patch input.
	Original []byte
	// RangeBytes are the exact bytes handed to the editor.
	RangeBytes []byte
	// TempFile is the temporary file holding RangeBytes. Complete always
	// removes it.
	TempFile string
	// SnapshotPath is the snapshot file written before launch. Snapshots
	// are kept for recovery and are never removed by the editor.
	SnapshotPath string
	// Cmd is the ready-to-run argv: the editor command plus TempFile.
	Cmd []string
}

// EditResult reports the outcome of a completed editor session.
type EditResult struct {
	// Original is the baseline document bytes.
	Original []byte
	// Content is the recomposed document. It is non-nil only when the
	// edit changed the document and the recomposed bytes validated; a
	// non-nil Content is the only savable outcome.
	Content []byte
	// Diagnostics describe why a changed edit was not made savable.
	Diagnostics []validator.Diagnostic
	// Cancelled reports that nothing may be applied: the editor exited
	// non-zero, produced an empty result, or the file changed on disk
	// during the edit.
	Cancelled bool
	// Changed reports whether the recomposed document differs from the
	// original.
	Changed bool
	// SnapshotPath repeats the session snapshot for the recovery trail.
	SnapshotPath string
}

// Editor launches the operator's editor on the exact bytes of a selected
// source range and safely recomposes the document from the result. UI
// models depend on this interface; the model never touches the filesystem
// or runs commands itself.
type Editor interface {
	// Prepare snapshots the document, extracts the exact range bytes into
	// a temp file and resolves the editor argv. It returns ErrConflict
	// when the file on disk no longer matches doc.Source, and ErrNoEditor
	// when no editor is configured.
	Prepare(ctx context.Context, doc *caddyfile.Document, r caddyfile.SourceRange) (*EditSession, error)
	// Complete reads the edited temp file, recomposes the document with
	// caddyfile.Patch and validates it. A non-zero exitCode, an empty
	// result or an external change on disk all cancel the edit.
	Complete(ctx context.Context, s *EditSession, exitCode int) (EditResult, error)
}

// EditorOptions configures a production editor. Every hook is injectable
// so tests run against an in-memory filesystem, a fake formatter and a
// fake clock without touching the real environment.
type EditorOptions struct {
	// LookupEnv resolves $VISUAL / $EDITOR. Defaults to os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// Formatter validates the recomposed document. nil disables the
	// editor: Complete errors because an unvalidated document must never
	// become savable.
	Formatter Formatter
	// ReadFile reads the document on disk for the conflict checks.
	// Defaults to os.ReadFile.
	ReadFile FileReader
	// SnapshotDir is where the pre-edit snapshot and its .range sidecar
	// are written. Empty falls back to the system temp directory.
	SnapshotDir string
	// TempDir is where the editor temp file is created. Empty defaults to
	// os.TempDir().
	TempDir string
	// Clock stamps snapshot names. Defaults to time.Now.
	Clock func() time.Time
}

// NewEditor returns an Editor wired from opts, applying the documented
// defaults for nil hooks.
func NewEditor(opts EditorOptions) Editor {
	e := &editor{
		lookupEnv:   opts.LookupEnv,
		formatter:   opts.Formatter,
		readFile:    opts.ReadFile,
		snapshotDir: opts.SnapshotDir,
		tempDir:     opts.TempDir,
		clock:       opts.Clock,
	}
	if e.lookupEnv == nil {
		e.lookupEnv = os.LookupEnv
	}
	if e.readFile == nil {
		e.readFile = os.ReadFile
	}
	if e.tempDir == "" {
		e.tempDir = os.TempDir()
	}
	if e.clock == nil {
		e.clock = time.Now
	}
	return e
}

// editor is the production Editor implementation.
type editor struct {
	lookupEnv   func(string) (string, bool)
	formatter   Formatter
	readFile    FileReader
	snapshotDir string
	tempDir     string
	clock       func() time.Time
}

// Prepare implements Editor. It resolves the editor command, preflights
// against external changes, snapshots the full document plus a plain-text
// range sidecar, extracts the exact range bytes into a temp file and
// assembles the argv ready for exec.Command.
func (e *editor) Prepare(ctx context.Context, doc *caddyfile.Document, r caddyfile.SourceRange) (*EditSession, error) {
	if doc == nil {
		return nil, errors.New("editor: nil document")
	}
	if !r.Valid(len(doc.Source)) {
		return nil, fmt.Errorf("editor: invalid source range [%d:%d) for %d-byte document %s", r.Start, r.End, len(doc.Source), doc.Path)
	}
	command, ok := e.lookupEnv("VISUAL")
	if !ok || strings.TrimSpace(command) == "" {
		command, ok = e.lookupEnv("EDITOR")
		if !ok {
			return nil, ErrNoEditor
		}
	}
	argv, err := splitCommand(command)
	if err != nil {
		return nil, err
	}
	if len(argv) == 0 {
		return nil, ErrNoEditor
	}
	// Preflight: the file on disk must still match the loaded document.
	// Nothing is written and nothing is opened when it does not.
	current, err := e.readFile(doc.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	if !bytes.Equal(current, doc.Source) {
		return nil, fmt.Errorf("%w", ErrConflict)
	}
	snapshotPath, err := e.writeSnapshot(doc.Source, r)
	if err != nil {
		return nil, fmt.Errorf("editor: snapshot: %w", err)
	}
	rangeBytes := doc.Source[r.Start:r.End]
	tempFile, err := os.CreateTemp(e.tempDir, "lazycaddy-editor-*")
	if err != nil {
		return nil, fmt.Errorf("editor: create temp file: %w", err)
	}
	if _, err := tempFile.Write(rangeBytes); err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return nil, fmt.Errorf("editor: write temp file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		os.Remove(tempFile.Name())
		return nil, fmt.Errorf("editor: close temp file: %w", err)
	}
	cmd := make([]string, 0, len(argv)+1)
	cmd = append(cmd, argv...)
	cmd = append(cmd, tempFile.Name())
	return &EditSession{
		DocPath:      doc.Path,
		Range:        r,
		Original:     append([]byte(nil), doc.Source...),
		RangeBytes:   append([]byte(nil), rangeBytes...),
		TempFile:     tempFile.Name(),
		SnapshotPath: snapshotPath,
		Cmd:          cmd,
	}, nil
}

// writeSnapshot writes the full document bytes to a fresh snapshot file in
// the snapshot directory and a plain-text "<start> <end>\n" sidecar next to
// it. The sidecar is deliberately not JSON. Snapshots are never removed.
// The file name embeds the injected clock's timestamp so recovery trails
// are sortable; os.CreateTemp still appends a random suffix, so two
// snapshots taken in the same second never collide.
func (e *editor) writeSnapshot(src []byte, r caddyfile.SourceRange) (string, error) {
	if e.snapshotDir != "" {
		// The snapshot holds the full Caddyfile, which may contain
		// secrets: the directory must be private to the operator.
		if err := os.MkdirAll(e.snapshotDir, 0o700); err != nil {
			return "", err
		}
	}
	pattern := fmt.Sprintf("editor-%s-*.snapshot", e.clock().Format("20060102-150405"))
	tmp, err := os.CreateTemp(e.snapshotDir, pattern)
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	if _, err := tmp.Write(src); err != nil {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	sidecar := name + ".range"
	content := strconv.Itoa(r.Start) + " " + strconv.Itoa(r.End) + "\n"
	if err := os.WriteFile(sidecar, []byte(content), 0o600); err != nil {
		return "", err
	}
	return name, nil
}

// Complete implements Editor. The temp file is removed on every path. A
// non-zero exit, an empty edited result or a file changed on disk since
// Prepare all cancel the edit without recomposing anything.
func (e *editor) Complete(ctx context.Context, s *EditSession, exitCode int) (EditResult, error) {
	if s == nil {
		return EditResult{}, errors.New("editor: nil session")
	}
	result := EditResult{
		Original:     append([]byte(nil), s.Original...),
		SnapshotPath: s.SnapshotPath,
	}
	defer func() {
		// The temp file is always discarded once the session ends; the
		// snapshot stays for recovery.
		os.Remove(s.TempFile)
	}()
	if exitCode != 0 {
		result.Cancelled = true
		return result, nil
	}
	edited, err := os.ReadFile(s.TempFile)
	if err != nil {
		// An unreadable temp file must never apply anything.
		result.Cancelled = true
		return result, nil
	}
	if len(edited) == 0 {
		result.Cancelled = true
		return result, nil
	}
	// Downstream conflict check: the file must still match the baseline
	// the editor was launched against.
	current, err := e.readFile(s.DocPath)
	if err != nil || !bytes.Equal(current, s.Original) {
		result.Cancelled = true
		return result, nil
	}
	composed, err := caddyfile.Patch(s.Original, s.Range, edited)
	if err != nil {
		return EditResult{}, err
	}
	result.Changed = !bytes.Equal(composed, s.Original)
	if !result.Changed {
		return result, nil
	}
	if e.formatter == nil {
		return EditResult{}, errors.New("editor: no formatter configured; cannot validate the edited document")
	}
	_, diags, err := e.formatter.FormatAndValidate(ctx, s.DocPath, composed)
	if err != nil && len(diags) == 0 {
		// A hard failure (missing binary, timeout) with no parse
		// diagnostics surfaces as an error; validation findings always
		// come with diagnostics.
		return EditResult{}, err
	}
	if len(diags) > 0 {
		// Changed but not savable: the diagnostics tell the operator why,
		// even when FormatAndValidate also reported them alongside an
		// error.
		result.Diagnostics = diags
		return result, nil
	}
	result.Content = append([]byte(nil), composed...)
	return result, nil
}

// splitCommand splits an editor command line into argv without invoking a
// shell. Spaces and tabs separate arguments; single quotes group literally,
// double quotes group with backslash escapes (a backslash inside single
// quotes is literal), and an unmatched quote is an error. Empty arguments
// (for example from `""` or `”`) are dropped. An empty or whitespace-only
// command yields ErrNoEditor. Because the result feeds exec.Command
// directly, shell metacharacters never reach a shell.
func splitCommand(command string) ([]string, error) {
	var argv []string
	var cur strings.Builder
	inArg := false
	for i := 0; i < len(command); {
		c := command[i]
		switch {
		case c == ' ' || c == '\t':
			if inArg {
				if cur.Len() > 0 {
					argv = append(argv, cur.String())
				}
				cur.Reset()
				inArg = false
			}
			i++
		case c == '\'':
			inArg = true
			end := i + 1
			for end < len(command) && command[end] != '\'' {
				cur.WriteByte(command[end])
				end++
			}
			if end >= len(command) {
				return nil, fmt.Errorf("editor: unmatched single quote in %q", command)
			}
			i = end + 1
		case c == '"':
			inArg = true
			end := i + 1
			for end < len(command) && command[end] != '"' {
				if command[end] == '\\' && end+1 < len(command) {
					cur.WriteByte(command[end+1])
					end += 2
					continue
				}
				cur.WriteByte(command[end])
				end++
			}
			if end >= len(command) {
				return nil, fmt.Errorf("editor: unmatched double quote in %q", command)
			}
			i = end + 1
		default:
			inArg = true
			cur.WriteByte(c)
			i++
		}
	}
	if inArg && cur.Len() > 0 {
		argv = append(argv, cur.String())
	}
	if len(argv) == 0 {
		return nil, ErrNoEditor
	}
	return argv, nil
}
