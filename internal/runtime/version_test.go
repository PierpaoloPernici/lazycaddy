package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// fakeRunner is a programmable runtime.CommandRunner. When runFn is set it
// takes precedence over the static fields, so tests can block on the
// context to exercise timeouts.
type fakeRunner struct {
	stdout   []byte
	stderr   []byte
	exitCode int
	err      error
	runFn    func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error)
}

func (f fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
	if f.runFn != nil {
		return f.runFn(ctx, name, args...)
	}
	return f.stdout, f.stderr, f.exitCode, f.err
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		want    string
		wantErr bool
	}{
		{name: "release build", out: "v2.11.4 h1:q3pe...k=", want: "v2.11.4"},
		{name: "beta release", out: "v2.9.0-beta.1 h1:abcdef123456=", want: "v2.9.0-beta.1"},
		{name: "git sha build", out: "a1b2c3d4e5f67890 (08 Aug 26 14:30 UTC)", want: "a1b2c3d4e5f67890"},
		{name: "pathological build", out: "unknown", want: "unknown"},
		{name: "trailing newline", out: "v2.11.4 h1:abc\n", want: "v2.11.4"},
		{name: "multi line", out: "v2.11.4 h1:abc\nextra line", want: "v2.11.4"},
		{name: "blank", out: "", wantErr: true},
		{name: "whitespace only", out: "  \n\t ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVersion([]byte(tt.out))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseVersion(%q) = %q, want error", tt.out, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVersion(%q): unexpected error: %v", tt.out, err)
			}
			if got != tt.want {
				t.Errorf("parseVersion(%q) = %q, want %q", tt.out, got, tt.want)
			}
		})
	}
}

func TestQueryVersion_Success(t *testing.T) {
	runner := fakeRunner{stdout: []byte("v2.11.4 h1:q3pe...k=\n")}
	got, err := QueryVersion(context.Background(), runner, "caddy", time.Second)
	if err != nil {
		t.Fatalf("QueryVersion: unexpected error: %v", err)
	}
	if got != "v2.11.4" {
		t.Errorf("version = %q, want v2.11.4", got)
	}
}

func TestQueryVersion_InvokesVersionCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	runner := fakeRunner{runFn: func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		gotName = name
		gotArgs = args
		return []byte("v2.11.4 h1:x\n"), nil, 0, nil
	}}
	if _, err := QueryVersion(context.Background(), runner, "/usr/local/bin/caddy", time.Second); err != nil {
		t.Fatalf("QueryVersion: unexpected error: %v", err)
	}
	if gotName != "/usr/local/bin/caddy" {
		t.Errorf("binary = %q, want /usr/local/bin/caddy", gotName)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "version" {
		t.Errorf("args = %v, want [version] (no flags may be passed to caddy version)", gotArgs)
	}
}

func TestQueryVersion_EmptyStdout(t *testing.T) {
	runner := fakeRunner{stdout: nil}
	if _, err := QueryVersion(context.Background(), runner, "caddy", time.Second); err == nil {
		t.Fatal("expected error for empty stdout, got nil")
	}
}

func TestQueryVersion_NonZeroExit(t *testing.T) {
	runner := fakeRunner{stderr: []byte("fatal: something broke\n"), exitCode: 2}
	_, err := QueryVersion(context.Background(), runner, "caddy", time.Second)
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
	if errors.Is(err, ErrBinaryMissing) || errors.Is(err, ErrVersionTimeout) {
		t.Errorf("non-zero exit must not map to a sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "fatal: something broke") {
		t.Errorf("error = %q, want it to include the captured stderr", err.Error())
	}
}

func TestQueryVersion_NonZeroExitNoStderr(t *testing.T) {
	runner := fakeRunner{exitCode: 1}
	_, err := QueryVersion(context.Background(), runner, "caddy", time.Second)
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
	if !strings.Contains(err.Error(), "exit") {
		t.Errorf("error = %q, want it to mention the exit", err.Error())
	}
}

func TestQueryVersion_ErrNotFound(t *testing.T) {
	runner := fakeRunner{err: exec.ErrNotFound}
	_, err := QueryVersion(context.Background(), runner, "caddy", time.Second)
	if !errors.Is(err, ErrBinaryMissing) {
		t.Fatalf("expected ErrBinaryMissing, got %v", err)
	}
}

func TestQueryVersion_ErrNotExist(t *testing.T) {
	runner := fakeRunner{err: os.ErrNotExist}
	_, err := QueryVersion(context.Background(), runner, "/missing/caddy", time.Second)
	if !errors.Is(err, ErrBinaryMissing) {
		t.Fatalf("expected ErrBinaryMissing, got %v", err)
	}
}

func TestQueryVersion_DeadlineExceeded(t *testing.T) {
	runner := fakeRunner{err: context.DeadlineExceeded}
	_, err := QueryVersion(context.Background(), runner, "caddy", time.Second)
	if !errors.Is(err, ErrVersionTimeout) {
		t.Fatalf("expected ErrVersionTimeout, got %v", err)
	}
}

func TestQueryVersion_CanceledPassthrough(t *testing.T) {
	runner := fakeRunner{err: context.Canceled}
	_, err := QueryVersion(context.Background(), runner, "caddy", time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if errors.Is(err, ErrBinaryMissing) || errors.Is(err, ErrVersionTimeout) {
		t.Fatalf("cancellation must not map to a sentinel, got %v", err)
	}
}

// TestQueryVersion_ContextDeadlineExpiry verifies that a runner blocking
// on its context until the caller deadline fires maps to ErrVersionTimeout.
func TestQueryVersion_ContextDeadlineExpiry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	runner := fakeRunner{runFn: func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		<-ctx.Done()
		return nil, nil, -1, ctx.Err()
	}}
	_, err := QueryVersion(ctx, runner, "caddy", 0)
	if !errors.Is(err, ErrVersionTimeout) {
		t.Fatalf("expected ErrVersionTimeout, got %v", err)
	}
}

// TestQueryVersion_ExplicitTimeoutExpiry verifies that a non-positive
// caller deadline with an explicit timeout param still maps to
// ErrVersionTimeout when the query overruns.
func TestQueryVersion_ExplicitTimeoutExpiry(t *testing.T) {
	runner := fakeRunner{runFn: func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		<-ctx.Done()
		return nil, nil, -1, ctx.Err()
	}}
	_, err := QueryVersion(context.Background(), runner, "caddy", 30*time.Millisecond)
	if !errors.Is(err, ErrVersionTimeout) {
		t.Fatalf("expected ErrVersionTimeout, got %v", err)
	}
}

// TestQueryVersion_TimeoutKillNonDeadlineError verifies that a runner
// which blocks until the deadline and then reports the kill as a plain
// error that does NOT wrap context.DeadlineExceeded still maps to
// ErrVersionTimeout. This simulates validator.ExecRunner, whose timeout
// path returns an error wrapping its own ErrTimeout sentinel.
func TestQueryVersion_TimeoutKillNonDeadlineError(t *testing.T) {
	runner := fakeRunner{runFn: func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		<-ctx.Done()
		return nil, nil, -1, errors.New("signal: killed")
	}}
	_, err := QueryVersion(context.Background(), runner, "caddy", 30*time.Millisecond)
	if !errors.Is(err, ErrVersionTimeout) {
		t.Fatalf("expected ErrVersionTimeout, got %v", err)
	}
}

// TestQueryVersion_TimeoutOwnSentinelError verifies the same contract
// when the runner wraps the deadline in its own sentinel (the shape
// validator.ExecRunner uses with validator.ErrTimeout): the expired
// query context, not the runner's error, is the authoritative timeout
// signal.
func TestQueryVersion_TimeoutOwnSentinelError(t *testing.T) {
	errTimeout := errors.New("caddy command timed out")
	runner := fakeRunner{runFn: func(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
		<-ctx.Done()
		return nil, nil, -1, fmt.Errorf("%w: %s", errTimeout, name)
	}}
	_, err := QueryVersion(context.Background(), runner, "caddy", 30*time.Millisecond)
	if !errors.Is(err, ErrVersionTimeout) {
		t.Fatalf("expected ErrVersionTimeout, got %v", err)
	}
}

// TestQueryVersion_WithValidatorExecRunner is the interop test with the
// real subprocess runner used by main.go. QueryVersion hardcodes the
// "version" argument, so the binary must be a real command that ignores
// its arguments and blocks until killed; `yes` fits (present on macOS
// and Linux). `sleep` is not usable here because `sleep version` fails
// immediately instead of blocking. The deadline kills the process and
// ExecRunner reports validator.ErrTimeout, which must still surface as
// ErrVersionTimeout via the expired query context.
func TestQueryVersion_WithValidatorExecRunner(t *testing.T) {
	start := time.Now()
	_, err := QueryVersion(context.Background(), validator.ExecRunner{}, "yes", 50*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("QueryVersion took %s, want it bounded by the timeout", elapsed)
	}
	if !errors.Is(err, ErrVersionTimeout) {
		t.Fatalf("expected ErrVersionTimeout, got %v", err)
	}
}
