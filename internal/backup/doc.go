// Package backup creates atomic, collision-safe backups of configuration
// files and rebuilds the on-disk backup index.
//
// # Naming contract
//
// Every backup file is named <timestamp>-<seq>-<basename>, where
// timestamp is formatted as 2006-01-02T15-04-05, seq is a zero-padded
// three-digit sequence starting at 001 that keeps backups created within
// the same second distinct, and basename is the source file's base name,
// which may itself contain dashes. Example:
//
//	2026-08-01T20-10-00-001-Caddyfile
//
// # Source identity
//
// Because imported documents can share a basename (for example two
// directories each containing a Caddyfile), the filename alone cannot
// resolve a backup to exactly one source file. Every backup therefore
// carries a plain-text identity sidecar next to it — the backup name
// plus ".src" — holding the exact, canonical source path on its first
// line:
//
//	2026-08-01T20-10-00-001-Caddyfile.src
//
//	List recovers Source from the sidecar and refuses to restore a
//	legacy backup (one without a sidecar) to a document whose basename
//	is shared by another document.
//
// Because the full index is embedded in filenames plus the sidecars, it
// can be rebuilt from the files on disk at any time; no sidecar index is
// stored.
//
// # Retention
//
// Retention is a per-source-file cleanup policy (maximum backups kept
// per source file) that is disabled by default and applied only after a
// successful save or rollback. It always preserves the newest backup of
// a source and the backup created for the current operation, and it
// never removes identity-less legacy backups or unrelated files.
package backup
