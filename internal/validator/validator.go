package validator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// Options configure a Validator. The zero value is not usable: callers must
// supply BinaryPath and may override the other fields.
type Options struct {
	// BinaryPath is the absolute or PATH-relative path to the caddy
	// binary. The validator never resolves or discovers this on its own:
	// discovery lives in the runtime adapter so the validator stays
	// decoupled from the host environment. Required.
	BinaryPath string
	// Runner executes the caddy command. Defaults to ExecRunner{}.
	Runner CommandRunner
	// Redactor scrubs secret-shaped values from command output. Defaults
	// to DefaultRedactor().
	Redactor *Redactor
	// Timeout bounds each individual caddy invocation (Format, Validate).
	// A non-positive value means "use the default of 5s".
	Timeout time.Duration
}

// withDefaults returns a copy of o with missing fields filled in.
func (o Options) withDefaults() Options {
	if o.Runner == nil {
		o.Runner = ExecRunner{}
	}
	if o.Redactor == nil {
		o.Redactor = DefaultRedactor()
	}
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Second
	}
	return o
}

// Validator runs `caddy fmt` and `caddy validate` against temporary working
// copies of a Caddyfile. It does not read or write user files; the caller
// is responsible for handing the formatted output to the persistence
// layer and the validated path to the next workflow step.
//
// Validator is safe for concurrent use as long as the injected
// CommandRunner and Redactor are.
type Validator struct {
	opts Options
}

// New returns a Validator with the supplied options normalised. It is an
// error to call New with an empty BinaryPath; the validator would have
// nothing to invoke.
func New(opts Options) (*Validator, error) {
	if opts.BinaryPath == "" {
		return nil, fmt.Errorf("%w: BinaryPath is required", ErrBinaryMissing)
	}
	return &Validator{opts: opts.withDefaults()}, nil
}

// Format runs `caddy fmt` against a temporary copy of src and returns the
// formatted bytes. The temporary file is removed before Format returns,
// even on failure.
//
// Errors:
//   - ErrBinaryMissing (wrapped) when BinaryPath is empty or the runner
//     reports a missing binary;
//   - ErrTimeout (wrapped) when the context deadline is exceeded;
//   - context.Canceled when ctx is cancelled;
//   - *ExitError when caddy exits non-zero; the Stderr field is redacted;
//   - other I/O errors are wrapped with a short prefix.
func (v *Validator) Format(ctx context.Context, src []byte) ([]byte, error) {
	path, cleanup, err := writeTemp("lazycaddy-fmt-*.caddy", src)
	if err != nil {
		return nil, fmt.Errorf("validator: write temp: %w", err)
	}
	defer cleanup()

	runCtx, cancel := context.WithTimeout(ctx, v.opts.Timeout)
	defer cancel()

	// --overwrite rewrites the file in place; without it, caddy fmt
	// would print the formatted output to stdout and return exit 1 when
	// the source has differences, breaking the in-place workflow.
	_, stderr, exit, err := v.opts.Runner.Run(runCtx, v.opts.BinaryPath, "fmt", "--overwrite", path)
	if err != nil {
		return nil, err
	}
	if exit != 0 {
		return nil, &ExitError{
			Stderr:   []byte(v.opts.Redactor.Redact(string(stderr))),
			ExitCode: exit,
		}
	}
	formatted, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("validator: read formatted file: %w", err)
	}
	return formatted, nil
}

// Validate runs `caddy validate --config <path>` and returns the parsed
// diagnostics. It does not copy the file: the caller passes a path it
// already controls. The path is forwarded to caddy verbatim, so callers
// should resolve it against a trusted root and avoid symlink tricks.
//
// On success Validate returns (nil, nil). On failure the returned error
// is an *ExitError wrapping ErrNonZeroExit and the slice of Diagnostic
// values contains one entry per line of captured stderr.
func (v *Validator) Validate(ctx context.Context, path string) ([]Diagnostic, error) {
	runCtx, cancel := context.WithTimeout(ctx, v.opts.Timeout)
	defer cancel()

	// --adapter caddyfile forces the Caddyfile parser regardless of
	// the file extension. Auto-detection is unreliable for our temp
	// files, which use the .caddy suffix; without this flag caddy
	// would pick a different (or no) adapter and fail to parse.
	_, stderr, exit, err := v.opts.Runner.Run(runCtx, v.opts.BinaryPath, "validate", "--config", path, "--adapter", "caddyfile")
	if err != nil {
		return nil, err
	}
	if exit == 0 {
		return nil, nil
	}
	redacted := v.opts.Redactor.Redact(string(stderr))
	diags := ParseDiagnostics(path, redacted)
	return diags, &ExitError{Stderr: []byte(redacted), ExitCode: exit}
}

// FormatAndValidate runs Format then Validate against a temporary
// working copy. displayPath is the real Caddyfile path to surface in
// the diagnostics: the temporary file path is an internal detail and
// is remapped away before the diagnostics leave the package. If Format
// fails, Validate is not invoked. On Validate failure the formatted
// bytes are still returned alongside the diagnostics so the UI can
// render the diff and the error at once.
func (v *Validator) FormatAndValidate(ctx context.Context, displayPath string, src []byte) (formatted []byte, diags []Diagnostic, err error) {
	formatted, err = v.Format(ctx, src)
	if err != nil {
		return nil, nil, err
	}
	path, cleanup, werr := writeTemp("lazycaddy-validate-*.caddy", formatted)
	if werr != nil {
		return nil, nil, fmt.Errorf("validator: write temp: %w", werr)
	}
	defer cleanup()
	diags, err = v.Validate(ctx, path)
	remapTempPath(diags, path, displayPath)
	if err != nil {
		return formatted, diags, err
	}
	return formatted, diags, nil
}

// remapTempPath replaces the temporary validation file path in each
// diagnostic with the display path (the real Caddyfile path), so the
// UI never surfaces /var/folders/... temp paths. It is a no-op when
// displayPath is empty or equals tempPath. Paths of other documents
// (e.g. imported files, which caddy reports with their real path) are
// left untouched.
func remapTempPath(diags []Diagnostic, tempPath, displayPath string) {
	if displayPath == "" || displayPath == tempPath {
		return
	}
	for i := range diags {
		if diags[i].Path == tempPath {
			diags[i].Path = displayPath
		}
	}
}

// writeTemp writes data to a uniquely named file in the OS temp directory
// and returns its path, a cleanup function that removes the file, and any
// error encountered. The cleanup function is always non-nil on success
// and is safe to call on a missing file.
func writeTemp(name string, data []byte) (string, func(), error) {
	if name == "" {
		return "", nil, errors.New("validator: empty temp name")
	}
	f, err := os.CreateTemp("", name)
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return f.Name(), cleanup, nil
}
