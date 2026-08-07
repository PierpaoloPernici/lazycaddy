package validator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestAdapt_Success locks in the exact caddy adapt invocation and verifies
// that stdout is returned verbatim on exit 0.
func TestAdapt_Success(t *testing.T) {
	json := []byte(`{"apps":{}}`)
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			want := []string{"adapt", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"}
			if len(args) != len(want) {
				t.Fatalf("len(args) = %d, want %d (args = %v)", len(args), len(want), args)
			}
			for i, got := range args {
				if got != want[i] {
					t.Errorf("args[%d] = %q, want %q", i, got, want[i])
				}
			}
			return json, nil, 0, nil
		},
	}
	v := newValidator(t, runner)

	out, err := v.Adapt(context.Background(), "/etc/caddy/Caddyfile")
	if err != nil {
		t.Fatalf("Adapt: unexpected error: %v", err)
	}
	if string(out) != string(json) {
		t.Errorf("output = %q, want %q", out, json)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner.calls = %d, want 1", len(runner.calls))
	}
	call := runner.calls[0]
	if call.name != "/usr/bin/caddy" {
		t.Errorf("name = %q, want /usr/bin/caddy", call.name)
	}
}

// TestAdapt_NonZeroExit verifies that a non-zero exit produces an *ExitError
// carrying the exit code and redacted stderr.
func TestAdapt_NonZeroExit(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			return nil, []byte("adapt failed: password=hunter2"), 3, nil
		},
	}
	v := newValidator(t, runner)

	_, err := v.Adapt(context.Background(), "/etc/caddy/Caddyfile")
	if err == nil {
		t.Fatal("expected error from non-zero exit, got nil")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *ExitError, got %T (%v)", err, err)
	}
	if exitErr.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", exitErr.ExitCode)
	}
	if strings.Contains(string(exitErr.Stderr), "hunter2") {
		t.Errorf("expected redacted stderr to mask hunter2, got %q", exitErr.Stderr)
	}
	if !errors.Is(err, ErrNonZeroExit) {
		t.Errorf("expected ErrNonZeroExit in chain, got %v", err)
	}
}

// TestAdapt_Timeout verifies that a runner-reported timeout propagates as
// ErrTimeout, mirroring the ExecRunner contract.
func TestAdapt_Timeout(t *testing.T) {
	runner := &fakeRunner{
		fn: func(ctx context.Context, name string, args []string) ([]byte, []byte, int, error) {
			<-ctx.Done()
			return nil, nil, -1, fmt.Errorf("%w: %s", ErrTimeout, name)
		},
	}
	v := newValidator(t, runner, func(o *Options) { o.Timeout = 10 * time.Millisecond })

	_, err := v.Adapt(context.Background(), "/etc/caddy/Caddyfile")
	if err == nil {
		t.Fatal("expected error from timeout, got nil")
	}
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}
