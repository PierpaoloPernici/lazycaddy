package logs

import (
	"testing"
)

func checkSpans(t *testing.T, line string, want []Span) {
	t.Helper()
	got := HighlightJSON([]byte(line))
	if len(got) != len(want) {
		t.Fatalf("HighlightJSON(%q) returned %d spans, want %d:\ngot  %v\nwant %v",
			line, len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("span[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestScanString(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		start     int
		want      string
		wantIndex int
		wantOK    bool
	}{
		{name: "plain", line: `"hello" tail`, want: "hello", wantIndex: 7, wantOK: true},
		{name: "escapes", line: `"line\n\t\"quoted\""`, want: "line\n\t\"quoted\"", wantIndex: 20, wantOK: true},
		{name: "unicode", line: `"\u0041\u00e9"`, want: "Aé", wantIndex: 14, wantOK: true},
		{name: "surrogate pair", line: `"\ud83d\ude00"`, want: "😀", wantIndex: 14, wantOK: true},
		{name: "invalid escape", line: `"bad\q"`, wantIndex: 0, wantOK: false},
		{name: "unterminated", line: `"missing`, wantIndex: 0, wantOK: false},
		{name: "offset", line: `x "value"`, start: 2, want: "value", wantIndex: 9, wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := tt.start
			got, ok := scanString([]byte(tt.line), &i)
			if got != tt.want || ok != tt.wantOK || i != tt.wantIndex {
				t.Errorf("scanString(%q, start %d) = (%q, %t), index %d; want (%q, %t), index %d", tt.line, tt.start, got, ok, i, tt.want, tt.wantOK, tt.wantIndex)
			}
		})
	}
}

func TestScanNumber(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		start     int
		wantIndex int
		wantOK    bool
	}{
		{name: "integer", line: "42 tail", wantIndex: 2, wantOK: true},
		{name: "negative fraction exponent", line: "-12.50e+3x", wantIndex: 9, wantOK: true},
		{name: "negative exponent", line: "6E-2", wantIndex: 4, wantOK: true},
		{name: "offset", line: "x42", start: 1, wantIndex: 3, wantOK: true},
		{name: "lone minus", line: "-", wantIndex: 0, wantOK: false},
		{name: "missing fraction digits", line: "1.", wantIndex: 0, wantOK: false},
		{name: "missing exponent digits", line: "2e", wantIndex: 0, wantOK: false},
		{name: "missing integer digits", line: ".5", wantIndex: 0, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := tt.start
			ok := scanNumber([]byte(tt.line), &i)
			if ok != tt.wantOK || i != tt.wantIndex {
				t.Errorf("scanNumber(%q, start %d) = (%t), index %d; want (%t), index %d", tt.line, tt.start, ok, i, tt.wantOK, tt.wantIndex)
			}
		})
	}
}

// TestHighlightJSON_AccessLogExample locks the exact span set (kinds and byte
// offsets) for the official Caddy access-log example.
func TestHighlightJSON_AccessLogExample(t *testing.T) {
	checkSpans(t, accessLogExample, []Span{
		{SpanDelimiter, 0, 1},     // {
		{SpanKey, 1, 8},           // "level"
		{SpanDelimiter, 8, 9},     // :
		{SpanLevelInfo, 9, 15},    // "info"
		{SpanDelimiter, 15, 16},   // ,
		{SpanKey, 16, 20},         // "ts"
		{SpanDelimiter, 20, 21},   // :
		{SpanTimestamp, 21, 39},   // 1646861401.5241024
		{SpanDelimiter, 39, 40},   // ,
		{SpanKey, 40, 48},         // "logger"
		{SpanDelimiter, 48, 49},   // :
		{SpanLogger, 49, 66},      // "http.log.access"
		{SpanDelimiter, 66, 67},   // ,
		{SpanKey, 67, 72},         // "msg"
		{SpanDelimiter, 72, 73},   // :
		{SpanMsg, 73, 90},         // "handled request"
		{SpanDelimiter, 90, 91},   // ,
		{SpanKey, 91, 100},        // "request"
		{SpanDelimiter, 100, 101}, // :
		{SpanDelimiter, 101, 102}, // {
		{SpanKey, 102, 113},       // "remote_ip"
		{SpanDelimiter, 113, 114}, // :
		{SpanString, 114, 125},    // "127.0.0.1"
		{SpanDelimiter, 125, 126}, // ,
		{SpanKey, 126, 139},       // "remote_port"
		{SpanDelimiter, 139, 140}, // :
		{SpanString, 140, 147},    // "54468"
		{SpanDelimiter, 147, 148}, // ,
		{SpanKey, 148, 159},       // "client_ip"
		{SpanDelimiter, 159, 160}, // :
		{SpanString, 160, 171},    // "127.0.0.1"
		{SpanDelimiter, 171, 172}, // ,
		{SpanKey, 172, 179},       // "proto"
		{SpanDelimiter, 179, 180}, // :
		{SpanString, 180, 190},    // "HTTP/2.0"
		{SpanDelimiter, 190, 191}, // ,
		{SpanKey, 191, 199},       // "method"
		{SpanDelimiter, 199, 200}, // :
		{SpanMethod, 200, 205},    // "GET"
		{SpanDelimiter, 205, 206}, // ,
		{SpanKey, 206, 212},       // "host"
		{SpanDelimiter, 212, 213}, // :
		{SpanString, 213, 226},    // "example.com"
		{SpanDelimiter, 226, 227}, // ,
		{SpanKey, 227, 232},       // "uri"
		{SpanDelimiter, 232, 233}, // :
		{SpanURI, 233, 236},       // "/"
		{SpanDelimiter, 236, 237}, // ,
		{SpanKey, 237, 246},       // "headers"
		{SpanDelimiter, 246, 247}, // :
		{SpanDelimiter, 247, 248}, // {
		{SpanKey, 248, 260},       // "User-Agent"
		{SpanDelimiter, 260, 261}, // :
		{SpanDelimiter, 261, 262}, // [
		{SpanString, 262, 275},    // "curl/7.82.0"
		{SpanDelimiter, 275, 276}, // ]
		{SpanDelimiter, 276, 277}, // ,
		{SpanKey, 277, 294},       // "Accept-Encoding"
		{SpanDelimiter, 294, 295}, // :
		{SpanDelimiter, 295, 296}, // [
		{SpanString, 296, 302},    // "gzip"
		{SpanDelimiter, 302, 303}, // ]
		{SpanDelimiter, 303, 304}, // ,
		{SpanKey, 304, 312},       // "Accept"
		{SpanDelimiter, 312, 313}, // :
		{SpanDelimiter, 313, 314}, // [
		{SpanString, 314, 319},    // "*/*"
		{SpanDelimiter, 319, 320}, // ]
		{SpanDelimiter, 320, 321}, // }
		{SpanDelimiter, 321, 322}, // ,
		{SpanKey, 322, 327},       // "tls"
		{SpanDelimiter, 327, 328}, // :
		{SpanDelimiter, 328, 329}, // {
		{SpanKey, 329, 338},       // "resumed"
		{SpanDelimiter, 338, 339}, // :
		{SpanBool, 339, 344},      // false
		{SpanDelimiter, 344, 345}, // ,
		{SpanKey, 345, 354},       // "version"
		{SpanDelimiter, 354, 355}, // :
		{SpanNumber, 355, 358},    // 772
		{SpanDelimiter, 358, 359}, // ,
		{SpanKey, 359, 373},       // "cipher_suite"
		{SpanDelimiter, 373, 374}, // :
		{SpanNumber, 374, 378},    // 4865
		{SpanDelimiter, 378, 379}, // ,
		{SpanKey, 379, 386},       // "proto"
		{SpanDelimiter, 386, 387}, // :
		{SpanString, 387, 391},    // "h2"
		{SpanDelimiter, 391, 392}, // ,
		{SpanKey, 392, 405},       // "server_name"
		{SpanDelimiter, 405, 406}, // :
		{SpanString, 406, 419},    // "example.com"
		{SpanDelimiter, 419, 420}, // }
		{SpanDelimiter, 420, 421}, // }
		{SpanDelimiter, 421, 422}, // ,
		{SpanKey, 422, 434},       // "bytes_read"
		{SpanDelimiter, 434, 435}, // :
		{SpanNumber, 435, 436},    // 0
		{SpanDelimiter, 436, 437}, // ,
		{SpanKey, 437, 446},       // "user_id"
		{SpanDelimiter, 446, 447}, // :
		{SpanString, 447, 449},    // ""
		{SpanDelimiter, 449, 450}, // ,
		{SpanKey, 450, 460},       // "duration"
		{SpanDelimiter, 460, 461}, // :
		{SpanNumber, 461, 472},    // 0.000221731
		{SpanDelimiter, 472, 473}, // ,
		{SpanKey, 473, 479},       // "size"
		{SpanDelimiter, 479, 480}, // :
		{SpanNumber, 480, 485},    // 10981
		{SpanDelimiter, 485, 486}, // ,
		{SpanKey, 486, 494},       // "status"
		{SpanDelimiter, 494, 495}, // :
		{SpanStatus2xx, 495, 498}, // 200
		{SpanDelimiter, 498, 499}, // ,
		{SpanKey, 499, 513},       // "resp_headers"
		{SpanDelimiter, 513, 514}, // :
		{SpanDelimiter, 514, 515}, // {
		{SpanKey, 515, 529},       // "Content-Type"
		{SpanDelimiter, 529, 530}, // :
		{SpanDelimiter, 530, 531}, // [
		{SpanString, 531, 557},    // "text/html; charset=utf-8"
		{SpanDelimiter, 557, 558}, // ]
		{SpanDelimiter, 558, 559}, // ,
		{SpanKey, 559, 567},       // "Server"
		{SpanDelimiter, 567, 568}, // :
		{SpanDelimiter, 568, 569}, // [
		{SpanString, 569, 576},    // "Caddy"
		{SpanDelimiter, 576, 577}, // ]
		{SpanDelimiter, 577, 578}, // }
		{SpanDelimiter, 578, 579}, // }
	})
}

func TestHighlightJSON_LevelValues(t *testing.T) {
	tests := []struct {
		level string
		want  SpanKind
	}{
		{"debug", SpanLevelDebug},
		{"info", SpanLevelInfo},
		{"warn", SpanLevelWarn},
		{"error", SpanLevelError},
		{"panic", SpanLevelOther},
		{"unknown", SpanLevelOther},
	}
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			line := `{"level":"` + tt.level + `"}`
			vStart := 9
			vEnd := vStart + len(tt.level) + 2
			checkSpans(t, line, []Span{
				{SpanDelimiter, 0, 1},
				{SpanKey, 1, 8},
				{SpanDelimiter, 8, 9},
				{tt.want, vStart, vEnd},
				{SpanDelimiter, vEnd, vEnd + 1},
			})
		})
	}
}

func TestHighlightJSON_LevelNonString(t *testing.T) {
	// A non-string value under "level" stays generic (SpanNumber).
	checkSpans(t, `{"level":5}`, []Span{
		{SpanDelimiter, 0, 1},
		{SpanKey, 1, 8},
		{SpanDelimiter, 8, 9},
		{SpanNumber, 9, 10},
		{SpanDelimiter, 10, 11},
	})
}

func TestHighlightJSON_StatusValues(t *testing.T) {
	tests := []struct {
		status string
		want   SpanKind
	}{
		{"100", SpanStatus1xx},
		{"204", SpanStatus2xx},
		{"304", SpanStatus3xx},
		{"404", SpanStatus4xx},
		{"599", SpanStatus5xx},
		{"600", SpanStatusOther},
		{"99", SpanStatusOther},
		{"0", SpanStatusOther},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			line := `{"status":` + tt.status + `}`
			spans := HighlightJSON([]byte(line))
			for _, s := range spans {
				if line[s.Start:s.End] == tt.status {
					if s.Kind != tt.want {
						t.Errorf("status %s classified as %v, want %v", tt.status, s.Kind, tt.want)
					}
					return
				}
			}
			t.Errorf("no span found for status %s in %q", tt.status, line)
		})
	}
}

func TestHighlightJSON_StatusNonNumeric(t *testing.T) {
	// A string under the "status" key is classified SpanStatusOther.
	checkSpans(t, `{"status":"ok"}`, []Span{
		{SpanDelimiter, 0, 1},
		{SpanKey, 1, 9},
		{SpanDelimiter, 9, 10},
		{SpanStatusOther, 10, 14},
		{SpanDelimiter, 14, 15},
	})
}

func TestHighlightJSON_NestedStatusIsGeneric(t *testing.T) {
	// "status" is only classified at the top level.
	checkSpans(t, `{"nested":{"status":200}}`, []Span{
		{SpanDelimiter, 0, 1},
		{SpanKey, 1, 9},
		{SpanDelimiter, 9, 10},
		{SpanDelimiter, 10, 11},
		{SpanKey, 11, 19},
		{SpanDelimiter, 19, 20},
		{SpanNumber, 20, 23},
		{SpanDelimiter, 23, 24},
		{SpanDelimiter, 24, 25},
	})
}

func TestHighlightJSON_StringEscapes(t *testing.T) {
	// The value span must cover the raw escaped bytes including quotes.
	line := `{"msg":"say \"hi\" and \\done"}`
	checkSpans(t, line, []Span{
		{SpanDelimiter, 0, 1},
		{SpanKey, 1, 6},
		{SpanDelimiter, 6, 7},
		{SpanMsg, 7, 30},
		{SpanDelimiter, 30, 31},
	})
	if got := line[7:30]; got != `"say \"hi\" and \\done"` {
		t.Errorf("value span text = %q, want the raw escaped bytes", got)
	}
}

func TestHighlightJSON_TopLevelValuesByKind(t *testing.T) {
	line := `{"a":1,"b":true,"c":null,"d":"x"}`
	checkSpans(t, line, []Span{
		{SpanDelimiter, 0, 1},
		{SpanKey, 1, 4},
		{SpanDelimiter, 4, 5},
		{SpanNumber, 5, 6},
		{SpanDelimiter, 6, 7},
		{SpanKey, 7, 10},
		{SpanDelimiter, 10, 11},
		{SpanBool, 11, 15},
		{SpanDelimiter, 15, 16},
		{SpanKey, 16, 19},
		{SpanDelimiter, 19, 20},
		{SpanNull, 20, 24},
		{SpanDelimiter, 24, 25},
		{SpanKey, 25, 28},
		{SpanDelimiter, 28, 29},
		{SpanString, 29, 32},
		{SpanDelimiter, 32, 33},
	})
}

func TestHighlightJSON_StringTSIsTimestamp(t *testing.T) {
	line := `{"ts":"2022-03-09T20:10:01.524Z"}`
	checkSpans(t, line, []Span{
		{SpanDelimiter, 0, 1},
		{SpanKey, 1, 5},
		{SpanDelimiter, 5, 6},
		{SpanTimestamp, 6, 32},
		{SpanDelimiter, 32, 33},
	})
}

func TestHighlightJSON_GarbageLines(t *testing.T) {
	// Lines that fail at the very first token produce no spans at all and
	// yield nil. Lines that begin as JSON but break later return the spans
	// produced so far (the UI trusts spans only when ParseEntry succeeds).
	for _, line := range []string{
		"hello world",
		"tru",
		"",
		"   ",
	} {
		if got := HighlightJSON([]byte(line)); got != nil {
			t.Errorf("HighlightJSON(%q) = %v, want nil", line, got)
		}
	}
}

func TestHighlightJSON_PartialMalformed(t *testing.T) {
	// Structurally broken but partially valid lines return the spans
	// produced before the error instead of nil.
	checkSpans(t, `{"a": tru}`, []Span{
		{SpanDelimiter, 0, 1},
		{SpanKey, 1, 4},
		{SpanDelimiter, 4, 5},
	})

	got := HighlightJSON([]byte("2026/08/08 12:00:00 INFO something"))
	if len(got) != 1 || got[0].Kind != SpanNumber || got[0].Start != 0 || got[0].End != 4 {
		t.Errorf("HighlightJSON(console line) = %v, want a single number span [0,4)", got)
	}
}

// TestHighlightJSON_Consistency asserts the span-set invariants for a variety
// of lines: sorted by Start, no overlaps, Start < End, End <= len(line).
func TestHighlightJSON_Consistency(t *testing.T) {
	samples := []string{
		accessLogExample,
		`{"level":"debug","msg":"x"}`,
		`{"status":599}`,
		`{"a":1,"b":true,"c":null,"d":"x"}`,
		`[1,2,3]`,
		`{"msg":"say \"hi\" and \\done"}`,
		`{"request":{"method":"POST","uri":"/api","headers":{"X":["y"]}}}`,
		`{"ts":"2022-03-09T20:10:01.524Z","level":"error","status":500}`,
		`{"bytes_read":0,"user_id":"","duration":0.000221731,"size":10981}`,
		"2026/08/08 12:00:00 INFO something",
		`{"a": tru}`,
		`{"broken": [1,2`,
		"",
	}
	for _, line := range samples {
		spans := HighlightJSON([]byte(line))
		prevEnd := 0
		for _, s := range spans {
			if s.Start < 0 || s.End <= s.Start {
				t.Errorf("line %q: invalid span %+v (Start<End violated)", line, s)
			}
			if s.End > len(line) {
				t.Errorf("line %q: span %+v exceeds line length %d", line, s, len(line))
			}
			if s.Start < prevEnd {
				t.Errorf("line %q: span %+v overlaps previous span ending at %d", line, s, prevEnd)
			}
			prevEnd = s.End
		}
	}
}
