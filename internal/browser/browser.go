// Package browser provides the platform adapter for opening web URLs.
package browser

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
)

var ErrUnavailable = errors.New("no browser opener available")

type LookPathFunc func(string) (string, error)
type CommandRunner func(context.Context, string, []string) error

type Options struct {
	LookPath LookPathFunc
	Run      CommandRunner
	GOOS     string
}

type systemBrowser struct {
	lookPath LookPathFunc
	run      CommandRunner
	goos     string
}

func New(options Options) *systemBrowser {
	if options.LookPath == nil {
		options.LookPath = exec.LookPath
	}
	if options.Run == nil {
		options.Run = runCommand
	}
	if options.GOOS == "" {
		options.GOOS = runtime.GOOS
	}
	return &systemBrowser{lookPath: options.LookPath, run: options.Run, goos: options.GOOS}
}

func (b *systemBrowser) OpenURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid browser URL %q", rawURL)
	}
	name, args := opener(b.goos, rawURL)
	path, err := b.lookPath(name)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, name)
	}
	if err := b.run(ctx, path, append(args, rawURL)); err != nil {
		return fmt.Errorf("open URL with %s: %w", name, err)
	}
	return nil
}

func opener(goos, rawURL string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		return "xdg-open", nil
	}
}

func runCommand(ctx context.Context, path string, args []string) error {
	return exec.CommandContext(ctx, path, args...).Run()
}
