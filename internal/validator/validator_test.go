package validator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeRunner is a programmable CommandRunner for tests. The default
// behavior rejects every command; tests set fn per case.
type fakeRunner struct {
	fn func(ctx context.Context, name string, args []string) (stdout, stderr []byte, exit int, err error)
	// calls records every (name, args) tuple the validator has invoked so
	// tests can assert on the exact caddy invocation.
	calls []fakeCall
}

type fakeCall struct {
	name string
	args []string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, fakeCall{name: name, args: append([]string(nil), args...)})
	if f.fn == nil {
		return nil, nil, 0, errors.New("fakeRunner: no fn set")
	}
	return f.fn(ctx, name, args)
}

func newValidator(t *testing.T, runner *fakeRunner, opts ...func(*Options)) *Validator {
	t.Helper()
	base := Options{
		BinaryPath: "/usr/bin/caddy",
		Runner:     runner,
		Timeout:    2 * time.Second,
	}
	for _, opt := range opts {
		opt(&base)
	}
	v, err := New(base)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return v
}

func TestNew_RequiresBinaryPath(t *testing.T) {
	_, err := New(Options{})
	if err == nil {
		t.Fatal("expected error when BinaryPath is empty, got nil")
	}
	if !errors.Is(err, ErrBinaryMissing) {
		t.Fatalf("expected ErrBinaryMissing, got %v", err)
	}
}

func TestNew_AppliesDefaults(t *testing.T) {
	v, err := New(Options{BinaryPath: "/usr/bin/caddy"})
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	if v.opts.Runner == nil {
		t.Fatal("expected default Runner to be set")
	}
	if v.opts.Redactor == nil {
		t.Fatal("expected default Redactor to be set")
	}
	if v.opts.Timeout != 5*time.Second {
		t.Fatalf("expected default Timeout 5s, got %s", v.opts.Timeout)
	}
}

func TestFormat_RewritesTempFile(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			if name != "/usr/bin/caddy" || len(args) != 3 || args[0] != "fmt" || args[1] != "--overwrite" {
				t.Fatalf("unexpected command: %s %v", name, args)
			}
			// Mimic caddy fmt: rewrite the temp file in place.
			path := args[2]
			if err := os.WriteFile(path, []byte("formatted:"+path), 0o644); err != nil {
				return nil, nil, 1, err
			}
			return nil, nil, 0, nil
		},
	}
	v := newValidator(t, runner)

	out, err := v.Format(context.Background(), []byte("hello"))
	if err != nil {
		t.Fatalf("Format: unexpected error: %v", err)
	}
	if !strings.HasPrefix(string(out), "formatted:") {
		t.Fatalf("expected formatted output to start with 'formatted:', got %q", out)
	}
	if !strings.Contains(string(out), "lazycaddy-fmt-") {
		t.Fatalf("expected formatted output to mention the temp file prefix, got %q", out)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected exactly 1 runner call, got %d", len(runner.calls))
	}
}

func TestFormat_RemovesTempOnExitError(t *testing.T) {
	var leaked string
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			leaked = args[2]
			return []byte("oops"), []byte("parse error"), 1, nil
		},
	}
	v := newValidator(t, runner)

	_, err := v.Format(context.Background(), []byte("garbage"))
	if err == nil {
		t.Fatal("expected error from failing fmt, got nil")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *ExitError, got %T (%v)", err, err)
	}
	if exitErr.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitErr.ExitCode)
	}
	if !errors.Is(err, ErrNonZeroExit) {
		t.Fatalf("expected ErrNonZeroExit in chain, got %v", err)
	}
	if leaked == "" {
		t.Fatal("expected runner to have received a temp path")
	}
	if _, err := os.Stat(leaked); !os.IsNotExist(err) {
		t.Fatalf("expected temp file %q to be removed, stat err = %v", leaked, err)
	}
}

func TestFormat_BinaryMissing(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			return nil, nil, -1, fmt.Errorf("%w: %s", ErrBinaryMissing, name)
		},
	}
	v := newValidator(t, runner)

	_, err := v.Format(context.Background(), []byte("hello"))
	if !errors.Is(err, ErrBinaryMissing) {
		t.Fatalf("expected ErrBinaryMissing, got %v", err)
	}
}

func TestFormat_TimeoutCleansUp(t *testing.T) {
	var leaked string
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			leaked = args[2]
			<-ctx.Done()
			// Mirror ExecRunner's behavior: wrap ErrTimeout so callers
			// can detect the high-level failure with errors.Is.
			return nil, nil, -1, fmt.Errorf("%w: %s", ErrTimeout, name)
		},
	}
	v := newValidator(t, runner, func(o *Options) { o.Timeout = 10 * time.Millisecond })

	_, err := v.Format(context.Background(), []byte("hello"))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
	if leaked == "" {
		t.Fatal("expected runner to have received a temp path")
	}
	if _, err := os.Stat(leaked); !os.IsNotExist(err) {
		t.Fatalf("expected temp file %q to be removed, stat err = %v", leaked, err)
	}
}

func TestFormat_CancellationCleansUp(t *testing.T) {
	var leaked string
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			leaked = args[2]
			return nil, nil, -1, context.Canceled
		},
	}
	v := newValidator(t, runner)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := v.Format(ctx, []byte("hello"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if leaked == "" {
		t.Fatal("expected runner to have received a temp path")
	}
	if _, err := os.Stat(leaked); !os.IsNotExist(err) {
		t.Fatalf("expected temp file %q to be removed, stat err = %v", leaked, err)
	}
}

func TestFormat_RedactsStderrOnFailure(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			return nil, []byte("password=hunter2 token=abc123"), 1, nil
		},
	}
	v := newValidator(t, runner)

	_, err := v.Format(context.Background(), []byte("garbage"))
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *ExitError, got %T", err)
	}
	if strings.Contains(string(exitErr.Stderr), "hunter2") {
		t.Fatalf("expected redacted stderr to mask hunter2, got %q", exitErr.Stderr)
	}
	if strings.Contains(string(exitErr.Stderr), "abc123") {
		t.Fatalf("expected redacted stderr to mask abc123, got %q", exitErr.Stderr)
	}
}

func TestFormat_ExitErrorMessage(t *testing.T) {
	e := &ExitError{Stderr: []byte("  boom  \n"), ExitCode: 3}
	if got, want := e.Error(), "caddy exit 3: boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(e, ErrNonZeroExit) {
		t.Errorf("expected ExitError to wrap ErrNonZeroExit")
	}
}

func TestFormat_ExitErrorMessageEmptyStderr(t *testing.T) {
	e := &ExitError{ExitCode: 1}
	if got, want := e.Error(), "caddy exit 1"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestValidate_OKReturnsNoDiagnostics(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			if args[0] != "validate" || args[1] != "--config" {
				t.Fatalf("expected 'validate --config <path>', got %v", args)
			}
			if args[2] != "/tmp/Caddyfile" {
				t.Fatalf("expected /tmp/Caddyfile as config path, got %q", args[2])
			}
			if len(args) != 5 || args[3] != "--adapter" || args[4] != "caddyfile" {
				t.Fatalf("expected --adapter caddyfile, got %v", args)
			}
			return nil, nil, 0, nil
		},
	}
	v := newValidator(t, runner)
	diags, err := v.Validate(context.Background(), "/tmp/Caddyfile")
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %d", len(diags))
	}
}

func TestValidate_ParseErrorReturnsLineColDiagnostic(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			return nil, []byte("/etc/caddy/Caddyfile:7:12: unexpected token: ident\n"), 1, nil
		},
	}
	v := newValidator(t, runner)

	diags, err := v.Validate(context.Background(), "/etc/caddy/Caddyfile")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *ExitError, got %T", err)
	}
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	d := diags[0]
	if d.Path != "/etc/caddy/Caddyfile" {
		t.Errorf("unexpected path: %q", d.Path)
	}
	if d.Line != 7 {
		t.Errorf("unexpected line: %d", d.Line)
	}
	if d.Column != 12 {
		t.Errorf("unexpected column: %d", d.Column)
	}
	if d.Severity != SeverityError {
		t.Errorf("unexpected severity: %v", d.Severity)
	}
}

func TestValidate_ParseErrorLineOnly(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			return nil, []byte("/etc/caddy/Caddyfile:42: something broke\n"), 1, nil
		},
	}
	v := newValidator(t, runner)

	diags, err := v.Validate(context.Background(), "/etc/caddy/Caddyfile")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	d := diags[0]
	if d.Line != 42 {
		t.Errorf("unexpected line: %d", d.Line)
	}
	if d.Column != 0 {
		t.Errorf("expected column 0 for line-only output, got %d", d.Column)
	}
}

func TestValidate_UnparseableLine(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			return nil, []byte("caddy: assertion failed\n"), 1, nil
		},
	}
	v := newValidator(t, runner)

	diags, err := v.Validate(context.Background(), "/etc/caddy/Caddyfile")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Path != "/etc/caddy/Caddyfile" {
		t.Errorf("expected default path in unparseable diagnostic, got %q", diags[0].Path)
	}
	if !strings.Contains(diags[0].Message, "assertion failed") {
		t.Errorf("expected message to contain 'assertion failed', got %q", diags[0].Message)
	}
}

func TestValidate_RedactsStderr(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			return nil, []byte("/etc/caddy/Caddyfile:1:1: api_key=topsecret"), 1, nil
		},
	}
	v := newValidator(t, runner)

	_, err := v.Validate(context.Background(), "/etc/caddy/Caddyfile")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *ExitError, got %T", err)
	}
	if strings.Contains(string(exitErr.Stderr), "topsecret") {
		t.Fatalf("expected redacted stderr to mask topsecret, got %q", exitErr.Stderr)
	}
	if !strings.Contains(string(exitErr.Stderr), "api_key=<redacted>") {
		t.Errorf("expected api_key label to be preserved, got %q", exitErr.Stderr)
	}
}

func TestValidate_BinaryMissing(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			return nil, nil, -1, fmt.Errorf("%w: %s", ErrBinaryMissing, name)
		},
	}
	v := newValidator(t, runner)

	_, err := v.Validate(context.Background(), "/etc/caddy/Caddyfile")
	if !errors.Is(err, ErrBinaryMissing) {
		t.Fatalf("expected ErrBinaryMissing, got %v", err)
	}
}

func TestFormatAndValidate_HappyPath(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			switch args[0] {
			case "fmt":
				if args[1] != "--overwrite" {
					t.Fatalf("expected fmt --overwrite, got %v", args)
				}
				_ = os.WriteFile(args[2], []byte("formatted"), 0o644)
				return nil, nil, 0, nil
			case "validate":
				if args[1] != "--config" {
					t.Fatalf("expected --config, got %v", args)
				}
				if args[3] != "--adapter" || args[4] != "caddyfile" {
					t.Fatalf("expected --adapter caddyfile, got %v", args)
				}
				return nil, nil, 0, nil
			default:
				t.Fatalf("unexpected command: %v", args)
				return nil, nil, 1, errors.New("unexpected")
			}
		},
	}
	v := newValidator(t, runner)
	out, diags, err := v.FormatAndValidate(context.Background(), "/etc/caddy/Caddyfile", []byte("raw"))
	if err != nil {
		t.Fatalf("FormatAndValidate: unexpected error: %v", err)
	}
	if string(out) != "formatted" {
		t.Errorf("expected formatted bytes, got %q", out)
	}
	if len(diags) != 0 {
		t.Errorf("expected no diagnostics, got %d", len(diags))
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 runner calls (fmt + validate), got %d", len(runner.calls))
	}
}

func TestFormatAndValidate_FormatFailureSkipsValidate(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			if args[0] != "fmt" {
				t.Fatalf("expected only fmt to be called, got %v", args)
			}
			return nil, []byte("boom"), 2, nil
		},
	}
	v := newValidator(t, runner)
	_, _, err := v.FormatAndValidate(context.Background(), "/etc/caddy/Caddyfile", []byte("raw"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(runner.calls))
	}
}

func TestFormatAndValidate_ReturnsDiagnosticsOnError(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			switch args[0] {
			case "fmt":
				if args[1] != "--overwrite" {
					t.Fatalf("expected fmt --overwrite, got %v", args)
				}
				_ = os.WriteFile(args[2], []byte("formatted"), 0o644)
				return nil, nil, 0, nil
			case "validate":
				return nil, []byte("/etc/caddy/Caddyfile:3:1: parse error"), 1, nil
			default:
				t.Fatalf("unexpected command: %v", args)
				return nil, nil, 1, errors.New("unexpected")
			}
		},
	}
	v := newValidator(t, runner)
	out, diags, err := v.FormatAndValidate(context.Background(), "/etc/caddy/Caddyfile", []byte("raw"))
	if err == nil {
		t.Fatal("expected error from failing validate, got nil")
	}
	if string(out) != "formatted" {
		t.Errorf("expected formatted bytes still to be returned, got %q", out)
	}
	if len(diags) != 1 || diags[0].Line != 3 {
		t.Fatalf("expected 1 diagnostic at line 3, got %+v", diags)
	}
}

// TestFormatAndValidate_RemapsTempPathToDisplayPath verifies that the
// temporary working file path is remapped to the real Caddyfile path
// in the diagnostics. The UI must never surface /var/folders/... temp
// paths; the temp file is an internal implementation detail.
func TestFormatAndValidate_RemapsTempPathToDisplayPath(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			switch args[0] {
			case "fmt":
				if args[1] != "--overwrite" {
					t.Fatalf("expected fmt --overwrite, got %v", args)
				}
				_ = os.WriteFile(args[2], []byte("formatted"), 0o644)
				return nil, nil, 0, nil
			case "validate":
				// The validate temp path is args[2]. Emit a parse
				// error that embeds it, as caddy does.
				return nil, []byte(args[2] + ":47:1: module not registered"), 1, nil
			default:
				t.Fatalf("unexpected command: %v", args)
				return nil, nil, 1, errors.New("unexpected")
			}
		},
	}
	v := newValidator(t, runner)
	_, diags, err := v.FormatAndValidate(context.Background(), "/etc/caddy/Caddyfile", []byte("raw"))
	if err == nil {
		t.Fatal("expected error from failing validate, got nil")
	}
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1", len(diags))
	}
	if diags[0].Path != "/etc/caddy/Caddyfile" {
		t.Errorf("diag path = %q, want /etc/caddy/Caddyfile (temp path must be remapped)", diags[0].Path)
	}
	if diags[0].Line != 47 || diags[0].Column != 1 {
		t.Errorf("diag line/col = %d/%d, want 47/1", diags[0].Line, diags[0].Column)
	}
}

// TestFormat_InvokesFmtWithOverwrite locks in the --overwrite flag
// passed to caddy fmt. Without it, caddy prints the formatted output
// to stdout and returns exit 1 when differences exist, breaking the
// in-place workflow that the validator package relies on.
func TestFormat_InvokesFmtWithOverwrite(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			// Rewrite the file in place, mirroring caddy fmt --overwrite.
			_ = os.WriteFile(args[2], []byte("formatted"), 0o644)
			return nil, nil, 0, nil
		},
	}
	v := newValidator(t, runner)

	if _, err := v.Format(context.Background(), []byte("hello")); err != nil {
		t.Fatalf("Format: unexpected error: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner.calls = %d, want 1", len(runner.calls))
	}
	call := runner.calls[0]
	if call.name != "/usr/bin/caddy" {
		t.Errorf("name = %q, want /usr/bin/caddy", call.name)
	}
	if len(call.args) != 3 || call.args[0] != "fmt" || call.args[1] != "--overwrite" {
		t.Errorf("args = %v, want [\"fmt\", \"--overwrite\", <path>]", call.args)
	}
}

// TestValidate_InvokesValidateWithAdapterCaddyfile locks in the
// --adapter caddyfile flag passed to caddy validate. Without it, the
// auto-detection might not pick the Caddyfile adapter for our .caddy
// temp files and validation would fail with a confusing error.
func TestValidate_InvokesValidateWithAdapterCaddyfile(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			return nil, nil, 0, nil
		},
	}
	v := newValidator(t, runner)

	if _, err := v.Validate(context.Background(), "/tmp/lazycaddy-test.caddy"); err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner.calls = %d, want 1", len(runner.calls))
	}
	call := runner.calls[0]
	if call.name != "/usr/bin/caddy" {
		t.Errorf("name = %q, want /usr/bin/caddy", call.name)
	}
	want := []string{"validate", "--config", "/tmp/lazycaddy-test.caddy", "--adapter", "caddyfile"}
	if len(call.args) != len(want) {
		t.Fatalf("len(args) = %d, want %d (args = %v)", len(call.args), len(want), call.args)
	}
	for i, got := range call.args {
		if got != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, got, want[i])
		}
	}
}
