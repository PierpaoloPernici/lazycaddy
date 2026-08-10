package browser

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestOpenURLUsesPlatformOpener(t *testing.T) {
	var gotPath string
	var gotArgs []string
	b := New(Options{
		GOOS:     "darwin",
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		Run: func(_ context.Context, path string, args []string) error {
			gotPath, gotArgs = path, args
			return nil
		},
	})
	if err := b.OpenURL(context.Background(), "https://caddyserver.com/docs/caddyfile"); err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	if gotPath != "/usr/bin/open" {
		t.Errorf("path = %q, want /usr/bin/open", gotPath)
	}
	if want := []string{"https://caddyserver.com/docs/caddyfile"}; !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}

func TestOpenURLRejectsNonWebURL(t *testing.T) {
	b := New(Options{LookPath: func(string) (string, error) { t.Fatal("LookPath called"); return "", nil }})
	if err := b.OpenURL(context.Background(), "file:///tmp/help.html"); err == nil {
		t.Fatal("OpenURL accepted a non-web URL")
	}
}

func TestOpenURLReportsUnavailableOpener(t *testing.T) {
	b := New(Options{
		GOOS:     "linux",
		LookPath: func(string) (string, error) { return "", errors.New("missing") },
	})
	if err := b.OpenURL(context.Background(), "https://caddyserver.com/docs/caddyfile"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("OpenURL error = %v, want ErrUnavailable", err)
	}
}

func TestNewDefaultsLookPathToRealLookup(t *testing.T) {
	// A nil LookPath falls back to exec.LookPath; the window opener is
	// selected by GOOS and never executes anything here.
	b := New(Options{
		GOOS: "windows",
		Run:  func(context.Context, string, []string) error { return nil },
	})
	if b.lookPath == nil {
		t.Fatal("New with a nil LookPath must default to exec.LookPath")
	}
	if b.goos != "windows" {
		t.Errorf("goos = %q, want windows", b.goos)
	}
}

func TestOpenURLWindowsOpener(t *testing.T) {
	var gotArgs []string
	b := New(Options{
		GOOS:     "windows",
		LookPath: func(name string) (string, error) { return "C:\\bin\\" + name, nil },
		Run: func(_ context.Context, path string, args []string) error {
			if path != "C:\\bin\\rundll32" {
				t.Errorf("path = %q, want rundll32", path)
			}
			gotArgs = args
			return nil
		},
	})
	if err := b.OpenURL(context.Background(), "https://caddyserver.com"); err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	want := []string{"url.dll,FileProtocolHandler", "https://caddyserver.com"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}

func TestOpenURLReportsRunFailure(t *testing.T) {
	b := New(Options{
		GOOS:     "linux",
		LookPath: func(string) (string, error) { return "/usr/bin/xdg-open", nil },
		Run:      func(context.Context, string, []string) error { return errors.New("spawn failed") },
	})
	err := b.OpenURL(context.Background(), "https://caddyserver.com")
	if err == nil || !strings.Contains(err.Error(), "open URL with xdg-open") {
		t.Fatalf("OpenURL error = %v, want a run failure wrapping xdg-open", err)
	}
}

func TestRunCommandFailsForMissingBinary(t *testing.T) {
	// The real command runner must fail deterministically for a binary that
	// cannot exist, with no network or daemon dependency.
	if err := runCommand(context.Background(), "/nonexistent/lazycaddy-opener", nil); err == nil {
		t.Fatal("runCommand of a missing binary must fail")
	}
}
