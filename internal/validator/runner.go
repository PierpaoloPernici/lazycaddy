package validator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// CommandRunner abstracts subprocess execution. The validator package
// depends on this interface so tests can substitute a fake runner without
// touching the real filesystem or spawning real processes.
type CommandRunner interface {
	// Run executes name with the given args and returns captured stdout,
	// stderr and the process exit code. A nil error with a non-zero exit
	// code means the command ran to completion but reported failure. A
	// non-nil error means the command could not be started, was killed, or
	// failed to terminate normally. The ctx value may cancel or time out
	// the call.
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

// ExecRunner runs real subprocesses via os/exec. It is the production
// CommandRunner implementation.
type ExecRunner struct{}

// Run implements CommandRunner using exec.CommandContext. It maps the
// common failure modes to the validator's sentinel errors:
//
//   - binary not found (exec.ErrNotFound for PATH lookups, os.ErrNotExist
//     for absolute paths) -> wraps ErrBinaryMissing
//   - context already done before the process started, or context done
//     while the process was running -> wraps ErrTimeout (DeadlineExceeded)
//     or returns context.Canceled as-is
//   - *exec.ExitError with context still healthy -> exit code is set, err
//     is nil (treat as a normal non-zero exit)
//
// Other errors are returned unchanged.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), stderr.Bytes(), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// The process ran. If the context is now done, the kill came from
		// the timeout/cancel rather than from caddy itself.
		if cerr := ctx.Err(); cerr != nil {
			if errors.Is(cerr, context.DeadlineExceeded) {
				return stdout.Bytes(), stderr.Bytes(), -1, fmt.Errorf("%w: %s", ErrTimeout, name)
			}
			if errors.Is(cerr, context.Canceled) {
				return stdout.Bytes(), stderr.Bytes(), -1, cerr
			}
		}
		return stdout.Bytes(), stderr.Bytes(), exitErr.ExitCode(), nil
	}
	// PATH lookup (no slash in name) returns *exec.Error wrapping
	// exec.ErrNotFound; absolute paths return *os.PathError wrapping
	// os.ErrNotExist. Both signal "binary missing".
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return nil, nil, -1, fmt.Errorf("%w: %s", ErrBinaryMissing, name)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, nil, -1, fmt.Errorf("%w: %s", ErrTimeout, name)
	}
	if errors.Is(err, context.Canceled) {
		return nil, nil, -1, err
	}
	return nil, nil, -1, err
}
