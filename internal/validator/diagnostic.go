package validator

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Severity classifies a validation finding. The validator package tags
// each Diagnostic with the level reported by caddy's log output (text
// / logfmt / JSON); the UI filters out non-error levels because they
// are not actionable in the validate-before-save flow.
type Severity int

const (
	// SeverityError marks a finding that prevents the configuration
	// from loading. Surfaced as a hard error to the user and the
	// default for any line without an explicit level marker.
	SeverityError Severity = iota
	// SeverityWarning marks a finding that does not block loading.
	SeverityWarning
	// SeverityInfo marks a non-actionable note. Caddy emits these
	// for "using config from file" and similar progress lines; the
	// UI hides them by default.
	SeverityInfo
	// SeverityDebug marks a verbose trace line. The UI hides these
	// by default; they are reserved for future verbose modes.
	SeverityDebug
)

// String returns the lower-case name of the severity. Unknown values
// map to "unknown" so debug output stays stable even if the enum grows.
func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	case SeverityDebug:
		return "debug"
	default:
		return "unknown"
	}
}

// Diagnostic is a structured validation finding parsed from caddy
// validate output. Path is the file the diagnostic refers to; if
// caddy does not embed a path in the line, ParseDiagnostics fills it
// with the supplied defaultPath. Line and Column are 1-based; 0
// means "not reported by caddy".
type Diagnostic struct {
	Path     string
	Line     int
	Column   int
	Message  string
	Severity Severity
}

// String formats the diagnostic for display, using a
// path:line:col: severity: message layout that matches common
// toolchain conventions. The order of fields is chosen so that
// copy-pasting the result into an editor opens the right line.
func (d Diagnostic) String() string {
	switch {
	case d.Line > 0 && d.Column > 0:
		return fmt.Sprintf("%s:%d:%d: %s: %s", d.Path, d.Line, d.Column, d.Severity, d.Message)
	case d.Line > 0:
		return fmt.Sprintf("%s:%d: %s: %s", d.Path, d.Line, d.Severity, d.Message)
	default:
		return fmt.Sprintf("%s: %s: %s", d.Path, d.Severity, d.Message)
	}
}

var (
	// diagLineColRe captures "path:line:col: message" tokens.
	diagLineColRe = regexp.MustCompile(`^(.+?):(\d+):(\d+):\s*(.+)$`)
	// diagLineRe captures "path:line: message" tokens without a column.
	diagLineRe = regexp.MustCompile(`^(.+?):(\d+):\s*(.+)$`)

	// Level detection. Caddy emits validate output in one of three
	// formats depending on the encoder (text, logfmt, JSON). All
	// three embed a level marker that the parser uses to tag each
	// Diagnostic with the right Severity. The regexes match the
	// marker in any position so the order of fields (e.g. logfmt's
	// "msg=" before "level=") does not matter.
	levelLogfmtRe = regexp.MustCompile(`\blevel=(error|info|warn|warning|debug|trace)\b`)
	levelJSONRe   = regexp.MustCompile(`"level"\s*:\s*"(error|info|warn|warning|debug|trace)"`)
	levelTextRe   = regexp.MustCompile(`^(ERROR|INFO|WARN|WARNING|DEBUG|TRACE)\b`)
	errorPrefixRe = regexp.MustCompile(`(?i)^error:\s*`)
	// levelTextStripRe matches a leading level prefix and the
	// following whitespace, so the path:line:col regex can match
	// the rest of a "ERROR /etc/caddy/Caddyfile:47:1: msg" line.
	levelTextStripRe = regexp.MustCompile(`^(ERROR|INFO|WARN|WARNING|DEBUG|TRACE)\s+`)
)

// parseLogLevel extracts the severity from a Caddy log line. It
// recognises the text, logfmt and JSON formats and returns
// SeverityError when no level marker is present. The Error default
// preserves the historical behaviour for lines that are not log
// output at all (e.g. raw caddy validate text without the logger
// prefix): they are surfaced in the modal so the user can see them.
func parseLogLevel(line string) Severity {
	if m := levelLogfmtRe.FindStringSubmatch(line); m != nil {
		return severityFromString(m[1])
	}
	if m := levelJSONRe.FindStringSubmatch(line); m != nil {
		return severityFromString(m[1])
	}
	if m := levelTextRe.FindStringSubmatch(line); m != nil {
		return severityFromString(m[1])
	}
	return SeverityError
}

// severityFromString maps a Caddy log level name to the matching
// Severity. Unknown names fall back to SeverityError so the parser
// never silently downgrades a finding.
func severityFromString(s string) Severity {
	switch strings.ToLower(s) {
	case "error", "err":
		return SeverityError
	case "warn", "warning":
		return SeverityWarning
	case "info":
		return SeverityInfo
	case "debug", "trace":
		return SeverityDebug
	default:
		return SeverityError
	}
}

// stripTextLevelPrefix removes a leading "ERROR " or "INFO  " style
// prefix from a line so the path:line:col regex can match the rest.
// JSON and logfmt levels are embedded in the line and cannot be
// stripped, but they do not block the regex match either (the regex
// simply does not fire on those lines and the parser falls back to
// the default path with the full line as the message).
func stripTextLevelPrefix(line string) string {
	return levelTextStripRe.ReplaceAllString(line, "")
}

// stripErrorPrefix removes the top-level "Error: " prefix emitted by caddy
// for adaptation failures. The Diagnostic.String method supplies the
// structured severity label, so retaining this prefix would render as
// "error: Error: ...".
func stripErrorPrefix(line string) string {
	return errorPrefixRe.ReplaceAllString(line, "")
}

// ParseDiagnostics converts the text output of `caddy validate` into
// a slice of Diagnostic values. The parser is intentionally lenient:
// it extracts path:line:col or path:line prefixes when present, and
// otherwise records the full line as the message associated with
// defaultPath.
//
// Each line is tagged with the Severity that caddy attached to it
// (text / logfmt / JSON). The default is SeverityError for
// historical compatibility, so the parser never downgrades a
// finding unless a level marker explicitly says so. The UI can then
// filter the result by severity to hide non-actionable info / debug
// / warning lines.
func ParseDiagnostics(defaultPath, output string) []Diagnostic {
	var out []Diagnostic
	for _, raw := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		severity := parseLogLevel(line)
		// Strip a text-style level prefix so the path:line:col regex
		// can match "ERROR /etc/caddy/Caddyfile:47:1: msg" as a real
		// finding instead of falling back to the unparseable branch.
		matchLine := stripErrorPrefix(stripTextLevelPrefix(line))
		if m := diagLineColRe.FindStringSubmatch(matchLine); m != nil {
			l, _ := strconv.Atoi(m[2])
			c, _ := strconv.Atoi(m[3])
			out = append(out, Diagnostic{
				Path:     m[1],
				Line:     l,
				Column:   c,
				Message:  m[4],
				Severity: severity,
			})
			continue
		}
		if m := diagLineRe.FindStringSubmatch(matchLine); m != nil {
			l, _ := strconv.Atoi(m[2])
			out = append(out, Diagnostic{
				Path:     m[1],
				Line:     l,
				Message:  m[3],
				Severity: severity,
			})
			continue
		}
		out = append(out, Diagnostic{
			Path:     defaultPath,
			Message:  matchLine,
			Severity: severity,
		})
	}
	return out
}
