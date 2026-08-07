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
// Because the full index is embedded in filenames, it can be rebuilt from
// the files on disk at any time; no sidecar index is stored. Rollback and
// retention are future milestones and will be layered on top of this
// contract.
package backup
