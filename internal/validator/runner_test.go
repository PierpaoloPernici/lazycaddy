package validator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExecRunner_SuccessCapturesOutput(t *testing.T) {
	stdout, stderr, exit, err := (ExecRunner{}).Run(context.Background(), "sh", "-c", "printf stdout; printf stderr >&2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0", exit)
	}
	if string(stdout) != "stdout" || string(stderr) != "stderr" {
		t.Fatalf("output = (%q, %q), want (stdout, stderr)", stdout, stderr)
	}
}

func TestExecRunner_NonZeroExitIsReturnedWithoutError(t *testing.T) {
	stdout, stderr, exit, err := (ExecRunner{}).Run(context.Background(), "sh", "-c", "printf output; printf failure >&2; exit 7")
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if exit != 7 {
		t.Fatalf("exit code = %d, want 7", exit)
	}
	if !strings.EqualFold(string(stdout), "output") || !strings.EqualFold(string(stderr), "failure") {
		t.Fatalf("output = (%q, %q), want (output, failure)", stdout, stderr)
	}
}

// TestExecRunner_NotFoundAbsolutePath verifies that ExecRunner surfaces
// the ErrBinaryMissing sentinel for an absolute path that does not exist.
// On absolute paths os/exec skips LookPath and returns *os.PathError
// wrapping os.ErrNotExist.
func TestExecRunner_NotFoundAbsolutePath(t *testing.T) {
	_, _, _, err := ExecRunner{}.Run(context.Background(), "/no/such/binary-1234", "fmt")
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	if !errors.Is(err, ErrBinaryMissing) {
		t.Fatalf("expected ErrBinaryMissing, got %v", err)
	}
}

// TestExecRunner_NotFoundBareName verifies that ExecRunner surfaces the
// ErrBinaryMissing sentinel for a bare name that PATH lookup cannot
// resolve. On bare names os/exec uses LookPath which returns an
// *exec.Error wrapping exec.ErrNotFound.
func TestExecRunner_NotFoundBareName(t *testing.T) {
	_, _, _, err := ExecRunner{}.Run(context.Background(), "definitely-not-a-real-binary-xyz", "fmt")
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	if !errors.Is(err, ErrBinaryMissing) {
		t.Fatalf("expected ErrBinaryMissing, got %v", err)
	}
}

// TestExecRunner_DeadlineExceeded uses a context whose deadline is
// already in the past. The race between fork/exec and the deadline
// timer is avoided by making the deadline deterministic, so the test
// does not depend on machine speed.
func TestExecRunner_DeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, _, _, err := ExecRunner{}.Run(ctx, "sleep", "1")
	if err == nil {
		t.Fatal("expected error from deadline, got nil")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

// TestExecRunner_Cancellation uses an already-canceled context to
// verify that cancellation propagates without being misclassified as
// ErrTimeout.
func TestExecRunner_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := ExecRunner{}.Run(ctx, "sleep", "1")
	if err == nil {
		t.Fatal("expected error from cancellation, got nil")
	}
	if errors.Is(err, ErrTimeout) {
		t.Fatalf("cancellation should not be classified as ErrTimeout, got %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
