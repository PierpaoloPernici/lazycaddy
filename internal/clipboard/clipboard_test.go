package clipboard

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
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
