package validator

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Severity classifies a validation finding. The validator package only
// produces SeverityError today; the other levels are reserved so the UI
// can render different kinds of findings without a breaking change once
// Caddy adds warning categories.
type Severity int

const (
	// SeverityError marks a finding that prevents the configuration from
	// loading. The default level reported by ParseDiagnostics.
	SeverityError Severity = iota
	// SeverityWarning marks a finding that does not block loading.
	SeverityWarning
	// SeverityInfo marks a non-actionable note.
	SeverityInfo
)

// String returns the lower-case name of the severity. Unknown values map
// to "unknown" so debug output stays stable even if the enum grows.
func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	default:
		return "unknown"
	}
}

// Diagnostic is a structured validation finding parsed from caddy validate
// output. Path is the file the diagnostic refers to; if caddy does not
// embed a path in the line, ParseDiagnostics fills it with the supplied
// defaultPath. Line and Column are 1-based; 0 means "not reported by
// caddy".
type Diagnostic struct {
	Path     string
	Line     int
	Column   int
	Message  string
	Severity Severity
}

// String formats the diagnostic for display, using a
// path:line:col: severity: message layout that matches common toolchain
// conventions. The order of fields is chosen so that copy-pasting the
// result into an editor opens the right line.
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
)

// ParseDiagnostics converts the text output of `caddy validate` into a slice
// of Diagnostic values. The parser is intentionally lenient: it extracts
// path:line:col or path:line prefixes when present, and otherwise records
// the full line as the message associated with defaultPath.
//
// The function does not interpret severity: every finding is tagged
// SeverityError because caddy only prints to stderr on validation failure.
func ParseDiagnostics(defaultPath, output string) []Diagnostic {
	var out []Diagnostic
	for _, raw := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if m := diagLineColRe.FindStringSubmatch(line); m != nil {
			l, _ := strconv.Atoi(m[2])
			c, _ := strconv.Atoi(m[3])
			out = append(out, Diagnostic{
				Path:     m[1],
				Line:     l,
				Column:   c,
				Message:  m[4],
				Severity: SeverityError,
			})
			continue
		}
		if m := diagLineRe.FindStringSubmatch(line); m != nil {
			l, _ := strconv.Atoi(m[2])
			out = append(out, Diagnostic{
				Path:     m[1],
				Line:     l,
				Message:  m[3],
				Severity: SeverityError,
			})
			continue
		}
		out = append(out, Diagnostic{
			Path:     defaultPath,
			Message:  line,
			Severity: SeverityError,
		})
	}
	return out
}
