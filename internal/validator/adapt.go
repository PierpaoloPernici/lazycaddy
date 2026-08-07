package validator

import "context"

// Adapt runs `caddy adapt --config <path> --adapter caddyfile` and returns
// the adapted JSON on stdout. Unlike server-side adaptation through the
// Admin API, passing the real config path makes Caddy resolve relative
// imports from the Caddyfile's own directory. path must be the real,
// on-disk Caddyfile path (the caller already verified it matches the
// bytes it intends to reload).
//
// Errors: ErrBinaryMissing, ErrTimeout, context.Canceled, or *ExitError
// (Stderr redacted) on non-zero exit, following Validator.Format.
func (v *Validator) Adapt(ctx context.Context, path string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, v.opts.Timeout)
	defer cancel()

	// Run against the real path (no temp copy): caddy resolves relative
	// imports from the config file's own directory. --adapter caddyfile is
	// passed explicitly so the file extension does not matter.
	stdout, stderr, exit, err := v.opts.Runner.Run(runCtx, v.opts.BinaryPath, "adapt", "--config", path, "--adapter", "caddyfile")
	if err != nil {
		return nil, err
	}
	if exit != 0 {
		return nil, &ExitError{
			Stderr:   []byte(v.opts.Redactor.Redact(string(stderr))),
			ExitCode: exit,
		}
	}
	return stdout, nil
}
