package app

import (
	"context"

	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// Formatter runs the caddy fmt and caddy validate pipeline on a working
// copy of a Caddyfile. UI models depend on this interface and never
// import the validator package directly: the concrete implementation
// is injected at construction time so tests can substitute a fake.
type Formatter interface {
	// FormatAndValidate formats src with `caddy fmt` and then validates
	// the result with `caddy validate`. displayPath is the real
	// Caddyfile path; the validator reports it in the diagnostics
	// instead of the temporary working file it validated against. On
	// success the returned formatted bytes are the new working copy and
	// diagnostics is nil. On failure the returned diagnostics describe
	// every parse error Caddy reported and err is a
	// *validator.ExitError that carries the redacted stderr.
	FormatAndValidate(ctx context.Context, displayPath string, src []byte) (formatted []byte, diagnostics []validator.Diagnostic, err error)
}

// FormatterFunc adapts a plain function to the Formatter interface. It
// mirrors the LoaderFunc pattern: callers that want to bypass the
// production constructor can wire a closure in one line, which is what
// the UI tests do.
type FormatterFunc func(ctx context.Context, displayPath string, src []byte) (formatted []byte, diagnostics []validator.Diagnostic, err error)

// FormatAndValidate implements Formatter.
func (f FormatterFunc) FormatAndValidate(ctx context.Context, displayPath string, src []byte) ([]byte, []validator.Diagnostic, error) {
	return f(ctx, displayPath, src)
}

// NewFormatter wraps a configured *validator.Validator in a Formatter.
// The caller is responsible for constructing the validator with the
// right BinaryPath and Timeout; this constructor does not perform
// discovery or fall back to defaults. A nil v is a programmer error
// and surfaces as a nil-pointer panic at the first FormatAndValidate
// call, which is acceptable because the panic has a clear stack at
// the construction site.
func NewFormatter(v *validator.Validator) Formatter {
	return FormatterFunc(v.FormatAndValidate)
}
