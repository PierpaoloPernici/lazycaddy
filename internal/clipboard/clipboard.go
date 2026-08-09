// Package clipboard provides terminal and local-platform clipboard support.
package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	osc52 "github.com/aymanbagabas/go-osc52/v2"
)

// ErrUnavailable indicates that neither OSC 52 nor a local clipboard command
// could accept the content.
var ErrUnavailable = errors.New("no clipboard backend available")

// LookPathFunc resolves an executable for a platform clipboard command.
type LookPathFunc func(string) (string, error)

// CommandRunner runs a clipboard command with content on standard input.
// It is injected so fallback behavior can be tested without touching the host
// clipboard or launching a process.
type CommandRunner func(context.Context, string, []string, []byte) error

// Options configures a Clipboard. OSC 52 is enabled by default; set
// DisableOSC52 when the terminal is known not to support it or when a local
// clipboard command should be preferred.
type Options struct {
	// Output receives OSC 52 sequences. When nil and DisableOSC52 is false,
	// os.Stdout is used (the terminal stream in the TUI); set DisableOSC52
	// to force the local fallback commands instead.
	Output io.Writer

	DisableOSC52 bool

	LookPath LookPathFunc
	Run      CommandRunner

	// LookupEnv is used only to select the tmux/screen OSC 52 wrapper. A nil
	// value uses os.LookupEnv.
	LookupEnv func(string) (string, bool)
}

type systemClipboard struct {
	output     io.Writer
	disableOSC bool
	lookPath   LookPathFunc
	run        CommandRunner
	lookupEnv  func(string) (string, bool)
}

// New returns a clipboard implementation using the supplied options. The
// default output is os.Stdout, which is the terminal stream used by the TUI.
func New(options Options) *systemClipboard {
	if options.Output == nil && !options.DisableOSC52 {
		options.Output = os.Stdout
	}
	if options.LookPath == nil {
		options.LookPath = exec.LookPath
	}
	if options.Run == nil {
		options.Run = runCommand
	}
	if options.LookupEnv == nil {
		options.LookupEnv = os.LookupEnv
	}
	return &systemClipboard{
		output:     options.Output,
		disableOSC: options.DisableOSC52,
		lookPath:   options.LookPath,
		run:        options.Run,
		lookupEnv:  options.LookupEnv,
	}
}

// Copy writes content through OSC 52 when possible, then falls back to the
// first available local clipboard command. The bytes passed to a fallback
// command are unchanged; in particular, no UI decorations are added here.
func (c *systemClipboard) Copy(ctx context.Context, content []byte) error {
	if !c.disableOSC && c.output != nil {
		sequence := osc52.New(string(content)).Mode(c.oscMode())
		if _, err := sequence.WriteTo(c.output); err == nil {
			return nil
		} else if fallbackErr := c.copyWithFallback(ctx, content); fallbackErr == nil {
			return nil
		} else {
			return fmt.Errorf("OSC 52 failed: %v; local clipboard fallback failed: %w", err, fallbackErr)
		}
	}
	return c.copyWithFallback(ctx, content)
}

func (c *systemClipboard) oscMode() osc52.Mode {
	if _, ok := c.lookupEnv("TMUX"); ok {
		return osc52.TmuxMode
	}
	if term, ok := c.lookupEnv("TERM"); ok && strings.Contains(term, "screen") {
		return osc52.ScreenMode
	}
	return osc52.DefaultMode
}

type fallbackCommand struct {
	name string
	args []string
}

// The order favors the native macOS and Wayland tools before the broadly
// available X11 tools, with Windows' clip command last. LookPath keeps this
// list harmless on platforms where a command is absent.
var fallbackCommands = []fallbackCommand{
	{name: "pbcopy"},
	{name: "wl-copy"},
	{name: "xclip", args: []string{"-selection", "clipboard"}},
	{name: "xsel", args: []string{"--clipboard", "--input"}},
	{name: "clip"},
}

func (c *systemClipboard) copyWithFallback(ctx context.Context, content []byte) error {
	var errs []error
	for _, candidate := range fallbackCommands {
		path, err := c.lookPath(candidate.name)
		if err != nil {
			continue
		}
		if err := c.run(ctx, path, candidate.args, content); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Errorf("%s: %w", candidate.name, err))
		}
	}
	if len(errs) == 0 {
		return ErrUnavailable
	}
	return errors.Join(errs...)
}

func runCommand(ctx context.Context, path string, args []string, content []byte) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = bytes.NewReader(content)
	return cmd.Run()
}
