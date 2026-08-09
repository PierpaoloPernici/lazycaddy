package logs

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// TestExecProcessFactory_Lifecycle drives a real subprocess through the
// production Process implementation: Stdout pipes the output, Wait
// reports a clean exit and Kill terminates a running process.
func TestExecProcessFactory_Lifecycle(t *testing.T) {
	factory := ExecProcessFactory{}

	// Clean exit: stdout is readable and Wait returns nil.
	p, err := factory.Start(context.Background(), "echo", "hello")
	if err != nil {
		t.Fatalf("Start(echo): %v", err)
	}
	out, err := io.ReadAll(p.Stdout())
	if err != nil {
		t.Fatalf("Stdout: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("stdout = %q, want hello", out)
	}
	if err := p.Wait(); err != nil {
		t.Errorf("Wait on a clean exit = %v, want nil", err)
	}

	// Kill: a sleeping process is terminated and Wait reports it.
	p, err = factory.Start(context.Background(), "sleep", "30")
	if err != nil {
		t.Fatalf("Start(sleep): %v", err)
	}
	if err := p.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := p.Wait(); err == nil {
		t.Error("Wait after Kill = nil, want an exit error")
	}
}

// TestExecProcessFactory_NonZeroExit verifies Wait maps a failing exit to
// a JournalExitError carrying the exit code and the captured stderr.
func TestExecProcessFactory_NonZeroExit(t *testing.T) {
	factory := ExecProcessFactory{}
	p, err := factory.Start(context.Background(), "sh", "-c", "echo oops >&2; exit 3")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	io.ReadAll(p.Stdout()) // drain so the process can exit
	err = p.Wait()
	var jee *JournalExitError
	if !errors.As(err, &jee) {
		t.Fatalf("Wait = %v, want *JournalExitError", err)
	}
	if jee.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", jee.ExitCode)
	}
	if !strings.Contains(jee.Stderr, "oops") {
		t.Errorf("Stderr = %q, want the captured oops line", jee.Stderr)
	}
	if !errors.Is(err, ErrJournalctlExit) {
		t.Error("JournalExitError must unwrap to ErrJournalctlExit")
	}
}

// TestExecProcessFactory_CancelledContext verifies that a cancelled
// context surfaces as context.Canceled instead of a binary-missing error.
func TestExecProcessFactory_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// An existing binary: the lookup succeeds and Start itself reports the
	// cancelled context, exercising the ctx.Err() branch of the factory.
	_, err := (ExecProcessFactory{}).Start(ctx, "echo", "hi")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Start with a cancelled context = %v, want context.Canceled", err)
	}
}

// TestExecProcess_KillWithoutProcess verifies Kill tolerates a command
// that never started (nil Process).
func TestExecProcess_KillWithoutProcess(t *testing.T) {
	p := &execProcess{cmd: &exec.Cmd{}}
	if err := p.Kill(); err != nil {
		t.Errorf("Kill without a process = %v, want nil", err)
	}
}

// TestJournalExitError_Error renders every branch: with and without a
// unit, with and without redactable stderr.
func TestJournalExitError_Error(t *testing.T) {
	if got := (&JournalExitError{ExitCode: 1, Unit: "caddy.service", Stderr: "boom"}).Error(); got != "journalctl exited with code 1 (unit caddy.service): boom" {
		t.Errorf("Error() = %q, want the full message", got)
	}
	if got := (&JournalExitError{ExitCode: 2, Stderr: "secret=abc"}).Error(); !strings.Contains(got, "<unknown>") {
		t.Errorf("Error() = %q, want the <unknown> unit placeholder", got)
	}
	if got := (&JournalExitError{ExitCode: 3}).Error(); got != "journalctl exited with code 3 (unit <unknown>)" {
		t.Errorf("Error() = %q, want the stderr-less message", got)
	}
}

// TestJournalHelpers covers the small formatting helpers: rawJSONString
// handles absent/non-string values, mapStartError maps lookup failures and
// leaves other errors alone, excerpt bounds snippets and stripNewline
// removes a trailing newline.
func TestJournalHelpers(t *testing.T) {
	if got := rawJSONString(nil); got != "" {
		t.Errorf("rawJSONString(nil) = %q, want empty", got)
	}
	if got := rawJSONString([]byte(`123`)); got != "" {
		t.Errorf("rawJSONString(number) = %q, want empty", got)
	}
	if got := rawJSONString([]byte(`"value"`)); got != "value" {
		t.Errorf("rawJSONString(string) = %q, want value", got)
	}

	want := errors.New("sentinel")
	if got := mapStartError(want, "x"); got != want {
		t.Errorf("mapStartError(sentinel) = %v, want the original error", got)
	}
	if got := mapStartError(exec.ErrNotFound, "x"); !errors.Is(got, ErrBinaryMissing) {
		t.Errorf("mapStartError(ErrNotFound) = %v, want ErrBinaryMissing", got)
	}

	long := strings.Repeat("x", 200)
	if got := excerpt([]byte(long)); !strings.HasSuffix(got, "…") || len(got) != 123 { // 120 bytes + 3-byte ellipsis
		t.Errorf("excerpt = %q (%d bytes), want 120 bytes + ellipsis", got, len(got))
	}
	if got := excerpt([]byte("  short  ")); got != "short" {
		t.Errorf("excerpt = %q, want the trimmed text", got)
	}

	if got := stripNewline([]byte("line\n")); string(got) != "line" {
		t.Errorf("stripNewline = %q, want line", got)
	}
	if got := stripNewline([]byte("line")); string(got) != "line" {
		t.Errorf("stripNewline without newline = %q, want unchanged", got)
	}
}
