package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

// logSegment is one part of a compact log line. Flexible segments are the
// ones reduced first when the assembled line overflows the width (in order
// msg, uri, logger); the fixed-width segments (timestamp, level, method,
// status) are never dropped.
type logSegment struct {
	text     string
	style    lipgloss.Style
	flexible bool
}

const (
	logTimestampLayout      = "2006-01-02 15:04:05.000"
	logTimestampPlaceholder = "----/--/-- --:--:--.---"
)

// renderCompactLogLine renders one log entry as a compact, human-readable
// line truncated to at most maxW cells. Non-JSON entries (Parsed false)
// are rendered verbatim. The line is built as plain text segments that
// are truncated FIRST and styled SECOND, so a truncation can never split
// an ANSI escape sequence. Level and status are always explicit text, so
// the meaning survives without color.
//
// Layout (segments joined by single spaces, absent segments dropped):
//
//	<ts> <level> <logger> <method> <uri> <status> — <msg>
func renderCompactLogLine(entry logs.Entry, maxW int) string {
	if !entry.Parsed {
		return truncateToWidth(string(entry.Raw), maxW)
	}
	segs := compactLogSegments(entry)
	plain := joinPlainLogSegments(segs)
	if lipgloss.Width(plain) <= maxW {
		return joinStyledLogSegments(segs)
	}
	// Over budget: reduce the flexible segments from the end (msg, then
	// uri, then logger). Each truncateToWidth appends its own ellipsis and
	// shortens the segment to the remaining budget; afterwards re-check.
	over := lipgloss.Width(plain) - maxW
	for over > 0 {
		idx := -1
		for i := len(segs) - 1; i >= 0; i-- {
			if segs[i].flexible && segs[i].text != "" {
				idx = i
				break
			}
		}
		if idx < 0 {
			// All flexible segments are exhausted: degenerate
			// very-narrow-terminal case. Return the whole plain line
			// truncated, unstyled (still ANSI-safe).
			return truncateToWidth(joinPlainLogSegments(segs), maxW)
		}
		budget := lipgloss.Width(segs[idx].text) - over
		if budget < 1 {
			segs[idx].text = ""
			// The msg's companion separator is dropped with it.
			if idx > 0 && segs[idx-1].text == "—" {
				segs[idx-1].text = ""
			}
		} else {
			segs[idx].text = truncateToWidth(segs[idx].text, budget)
		}
		over = lipgloss.Width(joinPlainLogSegments(segs)) - maxW
	}
	return joinStyledLogSegments(segs)
}

// compactLogSegments assembles the plain, per-segment content of a parsed
// entry. The caps (logger 20 cells, uri 36) keep the prefix bounded; the
// fixed-width fields (timestamp 23, level 5, method 6, status 3) keep the
// columns aligned across lines.
func compactLogSegments(entry logs.Entry) []logSegment {
	ts := logTimestampPlaceholder
	if !entry.Timestamp.IsZero() {
		ts = entry.Timestamp.Local().Format(logTimestampLayout)
	}
	level := padOrTruncate(strings.ToUpper(entry.Level), 5)

	segs := []logSegment{
		{text: ts, style: logTimestampStyle},
		{text: level, style: logLevelStyleFor(entry.Level)},
	}
	if entry.Logger != "" {
		segs = append(segs, logSegment{text: truncateToWidth(entry.Logger, 20), style: logLoggerStyle, flexible: true})
	}
	if entry.Method != "" {
		segs = append(segs, logSegment{text: padOrTruncate(strings.ToUpper(entry.Method), 6), style: logMethodStyle})
	}
	if entry.URI != "" {
		segs = append(segs, logSegment{text: truncateToWidth(entry.URI, 36), style: logURIStyle, flexible: true})
	}
	if entry.Status >= 0 {
		segs = append(segs, logSegment{text: fmt.Sprintf("%3d", entry.Status), style: logStatusStyleFor(entry.Status)})
	}
	if entry.Msg != "" {
		segs = append(segs, logSegment{text: "—", style: dimStyle})
		segs = append(segs, logSegment{text: entry.Msg, style: logMsgStyle, flexible: true})
	}
	return segs
}

// joinPlainLogSegments assembles the plain text of the surviving segments,
// joined by single spaces.
func joinPlainLogSegments(segs []logSegment) string {
	var b strings.Builder
	for _, s := range segs {
		if s.text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(s.text)
	}
	return b.String()
}

// joinStyledLogSegments assembles the styled text of the surviving
// segments, joined by plain single spaces so the separators never carry
// color.
func joinStyledLogSegments(segs []logSegment) string {
	var b strings.Builder
	for _, s := range segs {
		if s.text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(s.style.Render(s.text))
	}
	return b.String()
}

// padOrTruncate returns s right-padded with spaces to width cells, or
// truncated to width cells (with an ellipsis) when longer.
func padOrTruncate(s string, width int) string {
	if w := lipgloss.Width(s); w > width {
		return truncateToWidth(s, width)
	}
	return s + strings.Repeat(" ", width-lipgloss.Width(s))
}

// logLevelStyleFor maps a log level string to the level style class. The
// explicit level text always carries the meaning; color is reinforcement.
func logLevelStyleFor(level string) lipgloss.Style {
	switch level {
	case "debug":
		return logLevelDebugStyle
	case "info":
		return logLevelInfoStyle
	case "warn":
		return logLevelWarnStyle
	case "error":
		return logLevelErrorStyle
	default:
		return logLevelOtherStyle
	}
}

// logStatusStyleFor maps an HTTP status to the status style class. The
// explicit status digits always carry the meaning; color is reinforcement.
func logStatusStyleFor(status int) lipgloss.Style {
	switch {
	case status >= 100 && status < 200:
		return logStatus1xxStyle
	case status >= 200 && status < 300:
		return logStatus2xxStyle
	case status >= 300 && status < 400:
		return logStatus3xxStyle
	case status >= 400 && status < 500:
		return logStatus4xxStyle
	case status >= 500 && status < 600:
		return logStatus5xxStyle
	default:
		return logStatusOtherStyle
	}
}
