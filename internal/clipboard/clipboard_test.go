package clipboard

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/aymanbagabas/go-osc52/v2"
)

func TestCopyUsesOSC52WithExactBytes(t *testing.T) {
	var output bytes.Buffer
	called := false
	c := New(Options{
		Output: &output,
		// A fixed environment keeps the expected sequence hermetic even
		// when the test host itself runs inside tmux or screen.
		LookupEnv: func(string) (string, bool) { return "", false },
		LookPath: func(string) (string, error) {
			called = true
			return "", errors.New("not expected")
		},
	})

	content := []byte("site.example.test {\n\trespond / \"café\"\n}\n")
	if err := c.Copy(context.Background(), content); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if called {
		t.Fatal("fallback lookup called after successful OSC 52 copy")
	}
	want := "\x1b]52;c;" + "c2l0ZS5leGFtcGxlLnRlc3QgewoJcmVzcG9uZCAvICJjYWbDqSIKfQo=" + "\x07"
	if output.String() != want {
		t.Errorf("OSC 52 output = %q, want %q", output.String(), want)
	}
}

func TestCopyFallsBackWhenOSC52IsDisabled(t *testing.T) {
	var gotPath string
	var gotArgs []string
	var gotContent []byte
	c := New(Options{
		DisableOSC52: true,
		LookPath: func(name string) (string, error) {
			if name == "wl-copy" {
				return "/usr/bin/wl-copy", nil
			}
			return "", errors.New("not installed")
		},
		Run: func(_ context.Context, path string, args []string, content []byte) error {
			gotPath = path
			gotArgs = append([]string(nil), args...)
			gotContent = append([]byte(nil), content...)
			return nil
		},
	})

	content := []byte("raw\x00bytes\n")
	if err := c.Copy(context.Background(), content); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if gotPath != "/usr/bin/wl-copy" {
		t.Errorf("fallback path = %q, want /usr/bin/wl-copy", gotPath)
	}
	if gotArgs != nil {
		t.Errorf("wl-copy args = %#v, want none", gotArgs)
	}
	if !bytes.Equal(gotContent, content) {
		t.Errorf("fallback content = %q, want %q", gotContent, content)
	}
}

func TestCopyFallsBackAfterOSC52WriteError(t *testing.T) {
	var output failingWriter
	var runCalls int
	c := New(Options{
		Output: &output,
		LookPath: func(name string) (string, error) {
			if name == "pbcopy" {
				return name, nil
			}
			return "", errors.New("not installed")
		},
		Run: func(_ context.Context, path string, args []string, content []byte) error {
			runCalls++
			if path != "pbcopy" || len(args) != 0 || !bytes.Equal(content, []byte("fallback")) {
				t.Errorf("fallback call = (%q, %#v, %q)", path, args, content)
			}
			return nil
		},
	})

	if err := c.Copy(context.Background(), []byte("fallback")); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if runCalls != 1 {
		t.Errorf("fallback calls = %d, want 1", runCalls)
	}
}

func TestCopyReportsUnavailable(t *testing.T) {
	c := New(Options{
		DisableOSC52: true,
		LookPath:     func(string) (string, error) { return "", errors.New("not installed") },
	})

	err := c.Copy(context.Background(), []byte("nothing"))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Copy error = %v, want ErrUnavailable", err)
	}
}

func TestCopyUsesTmuxOSC52Mode(t *testing.T) {
	var output bytes.Buffer
	c := New(Options{
		Output: &output,
		LookupEnv: func(name string) (string, bool) {
			return map[string]string{"TMUX": "/tmp/tmux"}[name], name == "TMUX"
		},
	})

	if err := c.Copy(context.Background(), []byte("hello")); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if !strings.HasPrefix(output.String(), "\x1bPtmux;\x1b\x1b]52;c;") {
		t.Errorf("OSC 52 tmux output = %q, want tmux wrapper", output.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("terminal output closed") }

// TestRunCommand_ExecutesRealProcess verifies the production command
// runner end to end: content is piped to the command's stdin, a zero exit
// succeeds and a non-zero exit surfaces as an error.
func TestRunCommand_ExecutesRealProcess(t *testing.T) {
	ctx := context.Background()

	// Success: `cat` echoes stdin back, so the exit code is zero.
	err := runCommand(ctx, "cat", nil, []byte("payload"))
	if err != nil {
		t.Fatalf("runCommand(cat) = %v, want nil", err)
	}

	// Failure: a real failing command returns an *exec.ExitError. The
	// helper test is re-executed through the test binary; the marker
	// environment is set only for the duration of this test, and the
	// helper's own skip guard keeps it inert during the normal pass.
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	err = runCommand(ctx, os.Args[0], []string{"-test.run=TestRunCommandHelper"}, nil)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("runCommand(failing helper) = %v, want *exec.ExitError", err)
	}
	if exitErr.ExitCode() != 42 {
		t.Errorf("helper exited %d, want 42", exitErr.ExitCode())
	}
}

// TestRunCommandHelper is re-executed by the test binary to exercise the
// failure path of runCommand with a real non-zero exit; the marker
// environment prevents it from running during the normal test pass.
func TestRunCommandHelper(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		t.Skip("helper process only")
	}
	os.Exit(42)
}

// TestNew_DefaultsOutputToStdout verifies that New wires os.Stdout as the
// OSC 52 output when none is supplied and OSC 52 stays enabled.
func TestNew_DefaultsOutputToStdout(t *testing.T) {
	c := New(Options{})
	if c.output == nil {
		t.Fatal("output is nil, want the os.Stdout default")
	}
	if c.output != os.Stdout {
		t.Errorf("output = %T, want os.Stdout", c.output)
	}
}

// TestOscMode_ScreenTerminal verifies the screen(1) OSC 52 wrapper is
// selected through TERM without TMUX.
func TestOscMode_ScreenTerminal(t *testing.T) {
	c := New(Options{
		LookupEnv: func(name string) (string, bool) {
			if name == "TERM" {
				return "screen-256color", true
			}
			return "", false
		},
	})
	if mode := c.oscMode(); mode != osc52.ScreenMode {
		t.Errorf("oscMode = %v, want ScreenMode", mode)
	}
}

// TestCopy_ReportsCombinedFailure verifies the error surfaced when both
// OSC 52 and every local fallback fail: the message carries both halves
// and the accumulated per-command errors.
func TestCopy_ReportsCombinedFailure(t *testing.T) {
	c := New(Options{
		Output: failingWriter{},
		LookPath: func(name string) (string, error) {
			if name == "pbcopy" {
				return "/usr/bin/pbcopy", nil
			}
			return "", errors.New("not installed")
		},
		Run: func(_ context.Context, path string, args []string, content []byte) error {
			return errors.New("clipboard refused")
		},
	})
	err := c.Copy(context.Background(), []byte("x"))
	if err == nil {
		t.Fatal("Copy: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "OSC 52 failed") {
		t.Errorf("error = %q, want the OSC 52 failure half", err.Error())
	}
	if !strings.Contains(err.Error(), "local clipboard fallback failed") {
		t.Errorf("error = %q, want the fallback failure half", err.Error())
	}
	if !strings.Contains(err.Error(), "pbcopy: clipboard refused") {
		t.Errorf("error = %q, want the per-command error joined", err.Error())
	}
}
