package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/PierpaoloPernici/lazycaddy/internal/runtime"
)

// Sentinel errors returned by Reload implementations, so the UI can
// distinguish failure modes. Wrap the underlying error for detail.
var (
	// ErrAdminUnreachable reports that the Admin API could not be reached
	// during a reload.
	ErrAdminUnreachable = errors.New("admin API unreachable")
	// ErrAdminTimeout reports that an Admin API request exceeded its
	// timeout during a reload.
	ErrAdminTimeout = errors.New("admin API timeout")
	// ErrAdminRejected reports that the Admin API refused the posted
	// configuration.
	ErrAdminRejected = errors.New("admin API rejected the configuration")
)

// ReloadResult reports the outcome of a successful reload.
type ReloadResult struct {
	// Endpoint is the Admin API base URL that was called.
	Endpoint string
	// LoadedAt is when the Admin API confirmed the reload.
	LoadedAt time.Time
}

// ReloadError is returned when a reload fails. The file on disk and its
// backup are untouched by a reload; the error only reports the failure.
type ReloadError struct {
	Endpoint string
	Err      error
}

// Error implements error.
func (e *ReloadError) Error() string {
	return fmt.Sprintf("reload via %s failed: %v", e.Endpoint, e.Err)
}

// Unwrap returns the underlying error.
func (e *ReloadError) Unwrap() error { return e.Err }

// Reloader reloads the Caddy configuration through the local Admin API.
// UI models depend on this interface and never talk HTTP directly.
type Reloader interface {
	// Reload verifies that the file at path still matches saved (no
	// external change since it was written), adapts it with the local
	// caddy binary so relative imports resolve against the real config
	// path, and POSTs the adapted JSON to the Admin API /load endpoint.
	// A nil error means the Admin API accepted the configuration and
	// the loaded state now provably matches saved. On failure the error
	// is a *ReloadError wrapping ErrConflict, ErrAdminUnreachable,
	// ErrAdminTimeout or ErrAdminRejected (or the raw adapt error).
	Reload(ctx context.Context, path string, saved []byte) (ReloadResult, error)
}

// ReloaderFunc adapts a function to the Reloader interface (mirrors
// LoaderFunc / FormatterFunc).
type ReloaderFunc func(ctx context.Context, path string, saved []byte) (ReloadResult, error)

// Reload implements Reloader.
func (f ReloaderFunc) Reload(ctx context.Context, path string, saved []byte) (ReloadResult, error) {
	return f(ctx, path, saved)
}

// adminLoader is the slice of the runtime AdminClient the reloader needs
// so tests can fake the HTTP boundary without a live Admin API.
type adminLoader interface {
	LoadJSON(ctx context.Context, config []byte) error
}

// caddyAdapter is the slice of the validator.Validator the reloader needs
// to produce adapted JSON from the real config path; tests fake it.
type caddyAdapter interface {
	Adapt(ctx context.Context, path string) ([]byte, error)
}

// NewReloader returns a Reloader that performs the safe reload sequence:
// readFile(path) must still equal saved (else ErrConflict, nothing is
// sent), then adapt.Adapt(ctx, path) produces the JSON config, then
// admin.LoadJSON posts it. Errors from admin are mapped to the app
// sentinels (runtime.ErrAdminUnreachable -> ErrAdminUnreachable,
// runtime.ErrAdminTimeout -> ErrAdminTimeout, context.Canceled passed
// through unwrapped, everything else -> ErrAdminRejected). All failures
// are wrapped in *ReloadError. Adapt failures are wrapped in *ReloadError
// as-is (no sentinel). On success returns ReloadResult{Endpoint: endpoint,
// LoadedAt: time.Now()}.
func NewReloader(endpoint string, admin adminLoader, adapt caddyAdapter, readFile FileReader) Reloader {
	return ReloaderFunc(func(ctx context.Context, path string, saved []byte) (ReloadResult, error) {
		// Conflict check first: reloading would clobber an external edit
		// with stale config, so an unchanged file is a precondition.
		current, err := readFile(path)
		if err != nil {
			return ReloadResult{}, &ReloadError{Endpoint: endpoint, Err: fmt.Errorf("%w: %v", ErrConflict, err)}
		}
		if !bytes.Equal(current, saved) {
			return ReloadResult{}, &ReloadError{Endpoint: endpoint, Err: fmt.Errorf("%w", ErrConflict)}
		}

		// Adapt against the real config path so relative imports resolve
		// from the Caddyfile's own directory.
		jsonBytes, err := adapt.Adapt(ctx, path)
		if err != nil {
			return ReloadResult{}, &ReloadError{Endpoint: endpoint, Err: err}
		}

		if err := admin.LoadJSON(ctx, jsonBytes); err != nil {
			return ReloadResult{}, &ReloadError{Endpoint: endpoint, Err: mapAdminError(err)}
		}
		return ReloadResult{Endpoint: endpoint, LoadedAt: time.Now()}, nil
	})
}

// mapAdminError converts an error from the runtime AdminClient into the
// app-level sentinels. context.Canceled is passed through untouched so an
// operator cancel never reads as a configuration rejection; every other
// error that is not a recognised runtime sentinel is treated as a rejected
// configuration.
func mapAdminError(err error) error {
	switch {
	case errors.Is(err, runtime.ErrAdminUnreachable):
		return fmt.Errorf("%w: %v", ErrAdminUnreachable, err)
	case errors.Is(err, runtime.ErrAdminTimeout):
		return fmt.Errorf("%w: %v", ErrAdminTimeout, err)
	case errors.Is(err, context.Canceled):
		return err
	default:
		return fmt.Errorf("%w: %v", ErrAdminRejected, err)
	}
}
