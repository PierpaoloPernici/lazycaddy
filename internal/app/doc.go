// Package app owns the application state and orchestration: loading a
// Caddyfile, resolving its imports, formatting and validating a working
// copy and saving it back. File I/O and command execution happen only
// through injected adapters — a FileReader function, a backup.Creator
// and the caddyfile primitives — so the whole layer stays testable
// without a real filesystem or a Caddy daemon.
package app
