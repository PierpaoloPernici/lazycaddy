package validator

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors returned by Validator methods. They are wrapped by
// concrete errors; callers should branch with errors.Is.
var (
	// ErrBinaryMissing is returned when the caddy binary path is empty or
	// the runner reports that the binary could not be located.
	ErrBinaryMissing = errors.New("caddy binary not available")
	// ErrTimeout is returned when a caddy invocation exceeds the configured
	// Timeout. The underlying process is killed and the temporary working
	// file is removed.
	ErrTimeout = errors.New("caddy command timed out")
	// ErrNonZeroExit is wrapped by ExitError when caddy returns a non-zero
	// status. Use errors.As to recover the captured stderr.
	ErrNonZeroExit = errors.New("caddy command exited non-zero")
)

// ExitError records a non-zero exit code along with the redacted stderr
// captured from the caddy command. It wraps ErrNonZeroExit so callers can
// branch on the high-level reason with errors.Is and still inspect the
// captured output through errors.As.
type ExitError struct {
	// Stderr holds the redacted stderr produced by caddy.
	Stderr []byte
	// ExitCode is the process exit code reported by caddy.
	ExitCode int
}

// Error implements the error interface. The captured stderr is trimmed
// before being included so single-line error messages stay readable.
func (e *ExitError) Error() string {
	msg := strings.TrimSpace(string(e.Stderr))
	if msg == "" {
		return fmt.Sprintf("caddy exit %d", e.ExitCode)
	}
	return fmt.Sprintf("caddy exit %d: %s", e.ExitCode, msg)
}

// Unwrap returns ErrNonZeroExit so errors.Is can identify the high-level
// failure mode without exposing the wrapper type.
func (e *ExitError) Unwrap() error { return ErrNonZeroExit }
