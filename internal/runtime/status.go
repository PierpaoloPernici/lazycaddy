package runtime

import (
	"context"
	"errors"
	"time"
)

// Status is the runtime state of Caddy as far as lazycaddy can prove it.
type Status int

const (
	// StatusUnknown is the initial and fallback state: nothing provable
	// (no binary configured and Admin API not reachable, or not probed
	// yet).
	StatusUnknown Status = iota
	// StatusRunning means a Caddy daemon is serving the Admin API.
	StatusRunning
	// StatusStopped means the caddy binary is present and queryable, but
	// the Admin API is unreachable (daemon stopped or admin disabled).
	StatusStopped
	// StatusUnreachable means a probe could not complete (timeout or
	// cancellation) — the state is unknown.
	StatusUnreachable
)

// Capabilities is the set of operations lazycaddy can currently offer.
// Every field must be explicitly proven by a probe or a setting; nothing
// is assumed.
type Capabilities struct {
	// Binary is true when a caddy binary is configured and queryable.
	Binary bool
	// Version is the first field of `caddy version`; empty when Binary
	// is false.
	Version string
	// Validation is true when format/validate is possible, which in v0.1
	// means the binary is present.
	Validation bool
	// AdminAPI is true when the Admin API is reachable.
	AdminAPI bool
	// Readable is true when the running config is readable via the Admin
	// API. It uses the same probe as AdminAPI in v0.1.
	Readable bool
	// Reload is true when Binary AND AdminAPI are both proven.
	Reload bool
	// Writable is true when writable mode is enabled. It comes from
	// settings, not from a probe.
	Writable bool
}

// Report is the result of one probe.
type Report struct {
	// Status is the derived runtime state.
	Status Status
	// Capabilities holds every proven operation.
	Capabilities Capabilities
	// ProbedAt is when the probe ran.
	ProbedAt time.Time
}

// Detector probes the caddy binary and Admin API and always returns a
// Report: probe failures degrade to explicit unknown/stopped states rather
// than errors, so the TUI stays browsable read-only. It is safe for
// concurrent use: it holds no per-probe state.
type Detector struct {
	binary         string
	runner         CommandRunner
	admin          *AdminClient
	writable       bool
	versionTimeout time.Duration
	adminTimeout   time.Duration
}

// Options configures a Detector.
type Options struct {
	// Binary is the configured caddy binary path; empty means none.
	Binary string
	// Runner is used to run `caddy version`; required when Binary is
	// non-empty.
	Runner CommandRunner
	// Admin is the Admin API client; nil disables the AdminAPI, Readable
	// and Reload capabilities and the status falls back to the binary
	// probe alone.
	Admin *AdminClient
	// Writable mirrors settings.ReadOnly == false.
	Writable bool
	// VersionTimeout bounds the version query. A non-positive value
	// defaults to 5s.
	VersionTimeout time.Duration
	// AdminTimeout bounds the Admin API probe. A non-positive value
	// defaults to 5s (the startup probe must not hang on a blackholed
	// host).
	AdminTimeout time.Duration
}

// NewDetector returns a Detector with the given options and the default
// timeouts applied.
func NewDetector(opts Options) *Detector {
	if opts.VersionTimeout <= 0 {
		opts.VersionTimeout = 5 * time.Second
	}
	if opts.AdminTimeout <= 0 {
		opts.AdminTimeout = 5 * time.Second
	}
	return &Detector{
		binary:         opts.Binary,
		runner:         opts.Runner,
		admin:          opts.Admin,
		writable:       opts.Writable,
		versionTimeout: opts.VersionTimeout,
		adminTimeout:   opts.AdminTimeout,
	}
}

// Probe runs every configured probe and derives a Report. It never returns
// an error: probe failures only clear the affected capabilities.
//
//   - the binary version query (when Binary and Runner are configured)
//     sets Binary, Version and Validation on success and clears them on
//     failure; a timed-out query is tracked so it can still steer the
//     status below;
//   - the Admin API probe (when Admin is configured) sets AdminAPI and
//     Readable on success and clears them on failure;
//   - Reload is Binary AND AdminAPI;
//   - Status: AdminAPI -> StatusRunning; Binary without AdminAPI ->
//     StatusStopped; otherwise StatusUnreachable when the version query
//     timed out, the caller context was cancelled or expired, or the
//     admin probe was cancelled or expired, else StatusUnknown.
func (d *Detector) Probe(ctx context.Context) Report {
	rep := Report{
		Status:       StatusUnknown,
		Capabilities: Capabilities{Writable: d.writable},
		ProbedAt:     time.Now(),
	}

	var versionTimedOut bool
	if d.binary != "" && d.runner != nil {
		version, err := QueryVersion(ctx, d.runner, d.binary, d.versionTimeout)
		switch {
		case err == nil:
			rep.Capabilities.Binary = true
			rep.Capabilities.Version = version
			rep.Capabilities.Validation = true
		case errors.Is(err, ErrVersionTimeout):
			versionTimedOut = true
		}
	}

	// The admin probe runs under its own short timeout and its context is
	// retained so a deadline breach can be distinguished from a plain
	// unreachable response when the status is derived below.
	var adminCtx context.Context
	if d.admin != nil {
		var cancel context.CancelFunc
		adminCtx, cancel = context.WithTimeout(ctx, d.adminTimeout)
		defer cancel()
		if _, err := d.admin.Config(adminCtx); err == nil {
			rep.Capabilities.AdminAPI = true
			rep.Capabilities.Readable = true
		}
	}

	rep.Capabilities.Reload = rep.Capabilities.Binary && rep.Capabilities.AdminAPI

	switch {
	case rep.Capabilities.AdminAPI:
		rep.Status = StatusRunning
	case rep.Capabilities.Binary:
		rep.Status = StatusStopped
	case versionTimedOut || ctx.Err() != nil || (adminCtx != nil && adminCtx.Err() != nil):
		rep.Status = StatusUnreachable
	default:
		rep.Status = StatusUnknown
	}
	return rep
}
