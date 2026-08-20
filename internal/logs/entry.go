package logs

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Entry is one parsed structured log line, or one raw line that was not
// valid JSON. Parsed distinguishes the two so the UI can render the
// structured fields compactly and fall back to showing the raw line
// verbatim for console-encoded output.
type Entry struct {
	// Raw is the original line bytes WITHOUT the trailing newline.
	Raw []byte
	// Parsed is true when the line was valid JSON and ParseEntry
	// succeeded; false for non-JSON lines (which the UI shows verbatim).
	Parsed bool
	// Timestamp parsed from the "ts" field (numeric unix seconds or a
	// string layout); time.Time{} when absent or unparseable.
	Timestamp time.Time
	// Level is the log level ("debug", "info", "warn", "error",
	// "panic", "fatal"); empty when absent.
	Level string
	// Logger is the emitting module (e.g. "http.log.access"); empty when absent.
	Logger string
	// Msg is the human-readable message (e.g. "handled request").
	Msg string
	// Status is the HTTP response status for access logs; -1 when absent.
	Status int
	// Method is request.method for access logs; empty otherwise.
	Method string
	// URI is request.uri (including query string) for access logs.
	URI string
	// Host is request.host for access logs.
	Host string
	// Metadata is an optional set of journald metadata fields (for example
	// PRIORITY, _PID, _SYSTEMD_UNIT) attached by the systemd journal
	// source. It is nil when none of the curated fields were present and
	// is left untouched by ParseEntry and the file Tailer.
	Metadata map[string]string
	// Duration is the request handling time in seconds for access logs;
	// -1 when absent so a zero-duration is distinguishable from "no
	// duration".
	Duration float64
}

// jsonEntry is the tolerant decode target for one log line. Pointer fields
// distinguish an absent key from an explicit zero value; everything else is
// ignored.
type jsonEntry struct {
	TS       json.RawMessage `json:"ts"`
	Level    string          `json:"level"`
	Logger   string          `json:"logger"`
	Msg      string          `json:"msg"`
	Status   *int            `json:"status"`
	Duration *float64        `json:"duration"`
	Request  jsonRequest     `json:"request"`
}

type jsonRequest struct {
	Method string `json:"method"`
	URI    string `json:"uri"`
	Host   string `json:"host"`
}

// tsStringLayouts are accepted timestamp string layouts, tried in order.
var tsStringLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	time.RFC1123Z,
	time.RFC1123,
	"2006/01/02 15:04:05.000",
	"2006/01/02 15:04:05",
}

// ParseEntry parses one JSON log line into an Entry. It returns an error
// only when the line is not valid JSON (console-encoded or garbage lines
// are NOT parse failures the caller must handle — the caller shows them
// raw). Absent/malformed optional fields keep zero values (Status is -1).
func ParseEntry(line []byte) (Entry, error) {
	line = stripTrailingCR(line)
	var je jsonEntry
	if err := json.Unmarshal(line, &je); err != nil {
		return Entry{}, err
	}
	e := Entry{
		Raw:       line,
		Parsed:    true,
		Level:     je.Level,
		Logger:    je.Logger,
		Msg:       je.Msg,
		Status:    -1,
		Method:    je.Request.Method,
		URI:       je.Request.URI,
		Host:      je.Request.Host,
		Timestamp: parseTS(je.TS),
		Duration:  -1,
	}
	if je.Status != nil {
		e.Status = *je.Status
	}
	if je.Duration != nil {
		e.Duration = *je.Duration
	}
	return e, nil
}

// stripTrailingCR removes a single trailing '\r' from Windows-style lines.
func stripTrailingCR(line []byte) []byte {
	if n := len(line); n > 0 && line[n-1] == '\r' {
		return line[:n-1]
	}
	return line
}

// parseTS converts the raw "ts" field value into a time.Time. The value may
// be a numeric unix-seconds float or a string in one of tsStringLayouts.
// time.Time{} is returned when the value is absent, blank, null, or
// unparseable (including non-number, non-string values such as booleans).
func parseTS(raw json.RawMessage) time.Time {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return time.Time{}
	}
	// The literal null (and anything that is not a number or a string) is
	// treated as absent: json.Unmarshal into a float64 would silently
	// succeed for null with value 0, which must not read as Unix epoch.
	if bytes.Equal(trimmed, []byte("null")) {
		return time.Time{}
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return time.Time{}
		}
		for _, layout := range tsStringLayouts {
			if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
				return t
			}
		}
		return time.Time{}
	}
	// Numeric value. The float64 is used for detection and whole seconds;
	// the fractional nanoseconds are recovered from the decimal text so the
	// float64 representation error (up to ~1e-7 for unix-second magnitudes)
	// does not leak into the nanosecond digits.
	if c := trimmed[0]; c != '-' && (c < '0' || c > '9') {
		// Defensive: any value that is neither a string nor null nor a
		// number (e.g. a boolean) cannot be a timestamp.
		return time.Time{}
	}
	var f float64
	if err := json.Unmarshal(trimmed, &f); err != nil {
		return time.Time{}
	}
	sec := int64(f)
	return time.Unix(sec, fracNanos(trimmed, f, sec)).UTC()
}

// fracNanos returns the fractional-seconds part of a numeric timestamp in
// nanoseconds. The exact decimal path (integer part '.' fraction) truncates
// the fraction to nine digits; anything else falls back to float math.
func fracNanos(raw []byte, f float64, sec int64) int64 {
	mant := string(raw)
	if strings.ContainsAny(mant, "eE") {
		return int64((f - float64(sec)) * 1e9)
	}
	if dot := strings.IndexByte(mant, '.'); dot >= 0 {
		frac := mant[dot+1:]
		if len(frac) > 9 {
			frac = frac[:9]
		}
		for len(frac) < 9 {
			frac += "0"
		}
		if ns, err := strconv.ParseInt(frac, 10, 64); err == nil {
			return ns
		}
	}
	return int64((f - float64(sec)) * 1e9)
}
