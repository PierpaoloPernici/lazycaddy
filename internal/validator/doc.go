// Package validator wraps Caddy's formatter and validator commands.
//
// The package is designed to be used without a running Caddy daemon: every
// command runs against a temporary working file, the process is bounded by
// a context, and the binary path is injected by the caller. The package
// never reads or writes the user's source file directly.
//
// The Validator relies on three injected boundaries so tests can run without
// a real caddy binary and so the package stays decoupled from the host:
//
//   - a CommandRunner for subprocess execution;
//   - a Redactor for scrubbing secret-shaped values from command output
//     (defence in depth: the validator does not log secrets from the
//     environment or from caddy output);
//   - a Timeout for bounding each caddy invocation.
//
// Validate failures are returned as *ExitError (wrapping ErrNonZeroExit)
// together with a slice of structured Diagnostic values; the Stderr field
// of the error is redacted.
//
// Typical usage:
//
//	v, err := validator.New(validator.Options{BinaryPath: path})
//	if err != nil {
//	    return err
//	}
//	formatted, err := v.Format(ctx, workingBytes)
//	if err != nil {
//	    return err
//	}
//	diags, err := v.Validate(ctx, tempPath)
//	if err != nil {
//	    var exitErr *validator.ExitError
//	    if errors.As(err, &exitErr) {
//	        // render exitErr.Stderr and diags in the UI
//	    }
//	    return err
//	}
//
// Validator is safe for concurrent use as long as the injected CommandRunner
// and Redactor are. ExecRunner and DefaultRedactor are stateless.
package validator
