package logs

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// SpanKind classifies one highlighted region of a log line.
type SpanKind int

const (
	// SpanKey is a JSON object key.
	SpanKey SpanKind = iota
	// SpanString is a JSON string value (not a key).
	SpanString
	// SpanNumber is a JSON number value.
	SpanNumber
	// SpanBool is a JSON boolean value.
	SpanBool
	// SpanNull is a JSON null value.
	SpanNull
	// SpanDelimiter covers braces, brackets, colons and commas.
	SpanDelimiter
	// SpanTimestamp covers the "ts" field VALUE.
	SpanTimestamp
	// SpanMsg covers the "msg" field VALUE.
	SpanMsg
	// SpanLogger covers the "logger" field VALUE.
	SpanLogger
	// SpanLevelDebug/Info/Warn/Error classify the "level" VALUE by level.
	SpanLevelDebug
	SpanLevelInfo
	SpanLevelWarn
	SpanLevelError
	SpanLevelOther
	// SpanStatus1xx..5xx classify the "status" VALUE by HTTP class.
	SpanStatus1xx
	SpanStatus2xx
	SpanStatus3xx
	SpanStatus4xx
	SpanStatus5xx
	SpanStatusOther
	// SpanMethod covers request.method VALUE; SpanURI request.uri VALUE.
	SpanMethod
	SpanURI
)

// Span is a byte range in a log line with a semantic kind. Start/End are
// byte offsets into the line (End exclusive), exactly like caddyfile.Span.
type Span struct {
	Kind  SpanKind
	Start int
	End   int
}

// jsonFrame tracks one open JSON container while tokenizing. For objects,
// key holds the most recently seen key whose value has not yet completed.
type jsonFrame struct {
	array bool
	key   string
}

// HighlightJSON returns semantic spans for ONE JSON log line. It returns
// nil when the line is not valid JSON. Spans must cover the interesting
// tokens (keys, string/number/bool/null values, delimiters, and the
// classified level/status/ts/msg/logger/method/uri values); untokenized
// whitespace is not covered. The span set must be internally consistent:
// sorted by Start, no overlaps, Start < End, and End <= len(line).
func HighlightJSON(line []byte) []Span {
	spans := []Span{}
	stack := []jsonFrame{}
	i := 0
	n := len(line)
	failed := false

	for i < n && !failed {
		c := line[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '{':
			spans = append(spans, Span{Kind: SpanDelimiter, Start: i, End: i + 1})
			stack = append(stack, jsonFrame{})
			i++
		case c == '[':
			spans = append(spans, Span{Kind: SpanDelimiter, Start: i, End: i + 1})
			stack = append(stack, jsonFrame{array: true})
			i++
		case c == '}':
			if len(stack) == 0 || stack[len(stack)-1].array {
				failed = true
				break
			}
			spans = append(spans, Span{Kind: SpanDelimiter, Start: i, End: i + 1})
			stack = stack[:len(stack)-1]
			clearParentKey(&stack)
			i++
		case c == ']':
			if len(stack) == 0 || !stack[len(stack)-1].array {
				failed = true
				break
			}
			spans = append(spans, Span{Kind: SpanDelimiter, Start: i, End: i + 1})
			stack = stack[:len(stack)-1]
			clearParentKey(&stack)
			i++
		case c == ':' || c == ',':
			spans = append(spans, Span{Kind: SpanDelimiter, Start: i, End: i + 1})
			i++
		case c == '"':
			start := i
			name, ok := scanString(line, &i)
			if !ok {
				failed = true
				break
			}
			if isKeyPosition(line, i) {
				spans = append(spans, Span{Kind: SpanKey, Start: start, End: i})
				if len(stack) > 0 && !stack[len(stack)-1].array {
					stack[len(stack)-1].key = name
				}
			} else {
				kind := classifyValue(stack, SpanString, line[start:i])
				spans = append(spans, Span{Kind: kind, Start: start, End: i})
				clearParentKey(&stack)
			}
		case c == '-' || (c >= '0' && c <= '9'):
			start := i
			if !scanNumber(line, &i) {
				failed = true
				break
			}
			kind := classifyValue(stack, SpanNumber, line[start:i])
			spans = append(spans, Span{Kind: kind, Start: start, End: i})
			clearParentKey(&stack)
		case c == 't' || c == 'f' || c == 'n':
			start := i
			kind, ok := scanLiteral(line, &i)
			if !ok {
				failed = true
				break
			}
			kind = classifyValue(stack, kind, line[start:i])
			spans = append(spans, Span{Kind: kind, Start: start, End: i})
			clearParentKey(&stack)
		default:
			failed = true
		}
	}

	if len(spans) == 0 {
		return nil
	}
	return spans
}

// clearParentKey forgets the key on the top object frame once its value has
// been fully consumed (either a scalar token or a closed container).
func clearParentKey(stack *[]jsonFrame) {
	if len(*stack) > 0 && !(*stack)[len(*stack)-1].array {
		(*stack)[len(*stack)-1].key = ""
	}
}

// isKeyPosition reports whether a string token ending at afterQuote is an
// object key, i.e. the next non-whitespace byte is a colon.
func isKeyPosition(line []byte, afterQuote int) bool {
	j := afterQuote
	for j < len(line) && isSpace(line[j]) {
		j++
	}
	return j < len(line) && line[j] == ':'
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// scanString consumes a JSON string starting at the opening quote at *i,
// leaving *i past the closing quote. It reports ok=false on an unterminated
// string or an invalid escape. The unescaped contents are returned for key
// path matching.
func scanString(line []byte, i *int) (string, bool) {
	j := *i + 1 // skip opening quote
	var sb strings.Builder
	for j < len(line) {
		c := line[j]
		if c == '\\' {
			if j+1 >= len(line) {
				return "", false
			}
			esc := line[j+1]
			switch esc {
			case '"', '\\', '/':
				sb.WriteByte(esc)
				j += 2
			case 'b':
				sb.WriteByte('\b')
				j += 2
			case 'f':
				sb.WriteByte('\f')
				j += 2
			case 'n':
				sb.WriteByte('\n')
				j += 2
			case 'r':
				sb.WriteByte('\r')
				j += 2
			case 't':
				sb.WriteByte('\t')
				j += 2
			case 'u':
				if j+6 > len(line) {
					return "", false
				}
				v, err := strconv.ParseUint(string(line[j+2:j+6]), 16, 32)
				if err != nil {
					return "", false
				}
				r := rune(v)
				consumed := 6
				if utf16.IsSurrogate(r) {
					if j+12 <= len(line) && line[j+6] == '\\' && line[j+7] == 'u' {
						if v2, err := strconv.ParseUint(string(line[j+8:j+12]), 16, 32); err == nil {
							if dec := utf16.DecodeRune(r, rune(v2)); dec != unicode.ReplacementChar {
								r = dec
								consumed = 12
							}
						}
					}
				}
				sb.WriteRune(r)
				j += consumed
			default:
				return "", false
			}
			continue
		}
		if c == '"' {
			*i = j + 1
			return sb.String(), true
		}
		sb.WriteByte(c)
		j++
	}
	return "", false
}

// scanNumber consumes a JSON number starting at *i, leaving *i one past the
// last byte of the number. It reports ok=false when the token is not a
// well-formed number (e.g. a lone '-' or '.' without digits).
func scanNumber(line []byte, i *int) bool {
	j := *i
	if j < len(line) && line[j] == '-' {
		j++
	}
	intDigits := 0
	for j < len(line) && line[j] >= '0' && line[j] <= '9' {
		intDigits++
		j++
	}
	if intDigits == 0 {
		return false
	}
	if j < len(line) && line[j] == '.' {
		j++
		fracDigits := 0
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			fracDigits++
			j++
		}
		if fracDigits == 0 {
			return false
		}
	}
	if j < len(line) && (line[j] == 'e' || line[j] == 'E') {
		j++
		if j < len(line) && (line[j] == '+' || line[j] == '-') {
			j++
		}
		expDigits := 0
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			expDigits++
			j++
		}
		if expDigits == 0 {
			return false
		}
	}
	*i = j
	return true
}

// scanLiteral consumes true/false/null. It returns the generic kind and
// ok=false when the bytes do not match the expected literal.
func scanLiteral(line []byte, i *int) (SpanKind, bool) {
	lit := ""
	kind := SpanBool
	switch line[*i] {
	case 't':
		lit = "true"
	case 'f':
		lit = "false"
	default:
		lit = "null"
		kind = SpanNull
	}
	if len(line)-*i < len(lit) || string(line[*i:*i+len(lit)]) != lit {
		return 0, false
	}
	*i += len(lit)
	return kind, true
}

// classifyValue maps a value token to its semantic span kind based on the
// current key path. generic is the fallback kind for unclassified values.
func classifyValue(stack []jsonFrame, generic SpanKind, raw []byte) SpanKind {
	path := make([]string, 0, len(stack))
	for _, f := range stack {
		if !f.array && f.key != "" {
			path = append(path, f.key)
		}
	}
	switch strings.Join(path, ".") {
	case "ts":
		return SpanTimestamp
	case "msg":
		return SpanMsg
	case "logger":
		return SpanLogger
	case "level":
		if generic != SpanString {
			return generic
		}
		switch string(raw) {
		case `"debug"`:
			return SpanLevelDebug
		case `"info"`:
			return SpanLevelInfo
		case `"warn"`:
			return SpanLevelWarn
		case `"error"`:
			return SpanLevelError
		default:
			return SpanLevelOther
		}
	case "status":
		if generic != SpanNumber {
			return SpanStatusOther
		}
		if v, err := strconv.ParseFloat(string(raw), 64); err == nil {
			switch {
			case v >= 100 && v < 200:
				return SpanStatus1xx
			case v >= 200 && v < 300:
				return SpanStatus2xx
			case v >= 300 && v < 400:
				return SpanStatus3xx
			case v >= 400 && v < 500:
				return SpanStatus4xx
			case v >= 500 && v < 600:
				return SpanStatus5xx
			}
		}
		return SpanStatusOther
	case "request.method":
		return SpanMethod
	case "request.uri":
		return SpanURI
	}
	return generic
}
