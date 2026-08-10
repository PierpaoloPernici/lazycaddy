package browser

import (
	"context"
	"errors"
	"reflect"
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
