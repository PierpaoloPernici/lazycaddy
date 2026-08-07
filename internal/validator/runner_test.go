package validator

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
