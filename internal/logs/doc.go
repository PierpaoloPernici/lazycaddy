// Package logs is the log-view engine for a read-only Caddy log viewer. It
// parses one JSON log line into a structured Entry, classifies the tokens of
// a JSON log line into semantic spans for syntax highlighting, keeps a
// bounded in-memory history of parsed entries, and follows a log file with
// tail -F semantics (handling Caddy's default rotation by rename).
//
// The package is strictly read-only: it never modifies log files and never
// logs payloads. A Tailer is the v0.1 log source; it feeds a Buffer that the
// UI renders. ParseEntry and HighlightJSON are pure functions over single
// lines and are safe to use directly.
package logs
