package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// Sentinel errors returned by QueryVersion. They are wrapped by concrete
// errors; callers should branch with errors.Is.
var (
	// ErrBinaryMissing is returned when the caddy binary could not be
	// started: a PATH lookup miss (exec.ErrNotFound) or a missing file
	// (os.ErrNotExist).
	ErrBinaryMissing = errors.New("caddy binary not available")
	// ErrVersionTimeout is returned when the `caddy version` query
	// exceeded its timeout.
	ErrVersionTimeout = errors.New("caddy version query timed out")
)

// QueryVersion runs "caddy version" through runner and returns the first
// whitespace-separated field of stdout. That field is the version for
// release and xcaddy builds (e.g. "v2.11.4"), a commit SHA for git-built
// binaries, or the literal string "unknown" for pathological builds. The
// leading "v" is deliberately preserved: it is display text, not a
// formatting artifact.
//
// Error mapping:
//   - the query's own context has expired -> ErrVersionTimeout (wrapped).
//     This check runs first and is authoritative: a runner (e.g.
//     validator.ExecRunner) may report the deadline wrapped in its own
//     sentinel instead of context.DeadlineExceeded, so the expired
//     context is the reliable timeout signal.
//   - runner error wrapping context.DeadlineExceeded -> ErrVersionTimeout (wrapped)
//   - runner error wrapping exec.ErrNotFound or os.ErrNotExist -> ErrBinaryMissing (wrapped)
//   - runner error wrapping context.Canceled -> passed through as-is (NOT a sentinel)
//   - runner err == nil but exitCode != 0 -> a plain error carrying the
//     exit code and, when present, the captured stderr
//   - success -> parseVersion(stdout); empty or blank stdout -> error
//
// A timeout <= 0 means no explicit timeout and the caller's context is used
// as-is; otherwise the query runs under context.WithTimeout.
func QueryVersion(ctx context.Context, runner validator.CommandRunner, binary string, timeout time.Duration) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	stdout, stderr, exitCode, err := runner.Run(ctx, binary, "version")
	// The query's own context is the authoritative timeout signal: a
	// runner that obeys its context (killing the process on deadline)
	// may surface the failure as its own sentinel that does not wrap
	// context.DeadlineExceeded. When the deadline we applied (or the
	// caller's) has fired, the query could not complete in time no
	// matter how the runner reported it.
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("%w: %v", ErrVersionTimeout, ctx.Err())
	}
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return "", fmt.Errorf("%w: %v", ErrVersionTimeout, err)
		case errors.Is(err, exec.ErrNotFound), errors.Is(err, os.ErrNotExist):
			return "", fmt.Errorf("%w: %s", ErrBinaryMissing, binary)
		case errors.Is(err, context.Canceled):
			return "", err
		default:
			return "", err
		}
	}
	if exitCode != 0 {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return "", fmt.Errorf("caddy version exited %d: %s", exitCode, msg)
		}
		return "", fmt.Errorf("caddy version exited %d", exitCode)
	}
	return parseVersion(stdout)
}

// parseVersion extracts the first whitespace-separated field of the
// `caddy version` output. The field is returned raw: release builds keep
// their leading "v", git builds keep their commit SHA. Blank output is an
// error because no version is provable from it.
func parseVersion(out []byte) (string, error) {
	fields := strings.Fields(string(out))
	if len(fields) == 0 || fields[0] == "" {
		return "", errors.New("empty caddy version output")
	}
	return fields[0], nil
}
