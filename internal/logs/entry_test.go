package logs

import (
	"testing"
	"time"
)

const accessLogExample = `{"level":"info","ts":1646861401.5241024,"logger":"http.log.access","msg":"handled request","request":{"remote_ip":"127.0.0.1","remote_port":"54468","client_ip":"127.0.0.1","proto":"HTTP/2.0","method":"GET","host":"example.com","uri":"/","headers":{"User-Agent":["curl/7.82.0"],"Accept-Encoding":["gzip"],"Accept":["*/*"]},"tls":{"resumed":false,"version":772,"cipher_suite":4865,"proto":"h2","server_name":"example.com"}},"bytes_read":0,"user_id":"","duration":0.000221731,"size":10981,"status":200,"resp_headers":{"Content-Type":["text/html; charset=utf-8"],"Server":["Caddy"]}}`

func TestParseEntry_AccessLogExample(t *testing.T) {
	e, err := ParseEntry([]byte(accessLogExample))
	if err != nil {
		t.Fatalf("ParseEntry returned error: %v", err)
	}
	if got, want := string(e.Raw), accessLogExample; got != want {
		t.Errorf("Raw = %q, want %q", got, want)
	}
	wantTS := time.Unix(1646861401, 524102400).UTC()
	if !e.Timestamp.Equal(wantTS) {
		t.Errorf("Timestamp = %v, want %v", e.Timestamp, wantTS)
	}
	if e.Level != "info" {
		t.Errorf("Level = %q, want %q", e.Level, "info")
	}
	if e.Logger != "http.log.access" {
		t.Errorf("Logger = %q, want %q", e.Logger, "http.log.access")
	}
	if e.Msg != "handled request" {
		t.Errorf("Msg = %q, want %q", e.Msg, "handled request")
	}
	if e.Status != 200 {
		t.Errorf("Status = %d, want 200", e.Status)
	}
	if e.Method != "GET" {
		t.Errorf("Method = %q, want %q", e.Method, "GET")
	}
	if e.URI != "/" {
		t.Errorf("URI = %q, want %q", e.URI, "/")
	}
	if e.Host != "example.com" {
		t.Errorf("Host = %q, want %q", e.Host, "example.com")
	}
}

func TestParseEntry_NumericTSAccuracy(t *testing.T) {
	e, err := ParseEntry([]byte(`{"ts":1646861401.5241024}`))
	if err != nil {
		t.Fatalf("ParseEntry returned error: %v", err)
	}
	want := time.Unix(1646861401, 524102400).UTC()
	if !e.Timestamp.Equal(want) {
		t.Errorf("Timestamp = %v, want %v", e.Timestamp, want)
	}
	if e.Timestamp.Location() != time.UTC {
		t.Errorf("Timestamp location = %v, want UTC", e.Timestamp.Location())
	}
}

func TestParseEntry_GeneralLog(t *testing.T) {
	e, err := ParseEntry([]byte(`{"ts":1646861401.5,"level":"info","logger":"caddy.config","msg":"loading config"}`))
	if err != nil {
		t.Fatalf("ParseEntry returned error: %v", err)
	}
	if e.Status != -1 {
		t.Errorf("Status = %d, want -1 (absent)", e.Status)
	}
	if e.Method != "" || e.URI != "" || e.Host != "" {
		t.Errorf("request fields = %q/%q/%q, want empty", e.Method, e.URI, e.Host)
	}
	if e.Level != "info" || e.Logger != "caddy.config" || e.Msg != "loading config" {
		t.Errorf("got %q/%q/%q, want info/caddy.config/loading config", e.Level, e.Logger, e.Msg)
	}
	want := time.Unix(1646861401, 500000000).UTC()
	if !e.Timestamp.Equal(want) {
		t.Errorf("Timestamp = %v, want %v", e.Timestamp, want)
	}
}

func TestParseEntry_AbsentTS(t *testing.T) {
	e, err := ParseEntry([]byte(`{"level":"warn","msg":"no timestamp here"}`))
	if err != nil {
		t.Fatalf("ParseEntry returned error: %v", err)
	}
	if !e.Timestamp.IsZero() {
		t.Errorf("Timestamp = %v, want zero", e.Timestamp)
	}
}

// TestParseEntry_TSNull verifies that an explicit null (and a non-number,
// non-string value such as a boolean) is treated as an absent timestamp:
// json.Unmarshal into a float64 would otherwise succeed for null with
// value 0 and read as the Unix epoch.
func TestParseEntry_TSNull(t *testing.T) {
	tests := []struct {
		name string
		ts   string
	}{
		{name: "null", ts: "null"},
		{name: "boolean", ts: "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := ParseEntry([]byte(`{"ts":` + tt.ts + `,"level":"info"}`))
			if err != nil {
				t.Fatalf("ParseEntry returned error: %v", err)
			}
			if !e.Timestamp.IsZero() {
				t.Errorf("Timestamp = %v, want zero for ts=%s", e.Timestamp, tt.ts)
			}
			if e.Level != "info" {
				t.Errorf("Level = %q, want info (other fields must still parse)", e.Level)
			}
		})
	}
}

func TestParseEntry_TSSringLayouts(t *testing.T) {
	tests := []struct {
		name string
		ts   string
		want time.Time
	}{
		{
			name: "rfc3339nano",
			ts:   `"2022-03-09T20:10:01.524Z"`,
			want: time.Date(2022, 3, 9, 20, 10, 1, 524000000, time.UTC),
		},
		{
			name: "rfc3339",
			ts:   `"2022-03-09T20:10:01Z"`,
			want: time.Date(2022, 3, 9, 20, 10, 1, 0, time.UTC),
		},
		{
			name: "caddy wall layout with millis",
			ts:   `"2022/03/09 20:10:01.524"`,
			want: time.Date(2022, 3, 9, 20, 10, 1, 524000000, time.UTC),
		},
		{
			name: "caddy wall layout seconds",
			ts:   `"2022/03/09 20:10:01"`,
			want: time.Date(2022, 3, 9, 20, 10, 1, 0, time.UTC),
		},
		{
			name: "unparseable string falls back to zero",
			ts:   `"not-a-timestamp"`,
			want: time.Time{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := ParseEntry([]byte(`{"ts":` + tt.ts + `}`))
			if err != nil {
				t.Fatalf("ParseEntry returned error: %v", err)
			}
			if !e.Timestamp.Equal(tt.want) {
				t.Errorf("Timestamp = %v, want %v", e.Timestamp, tt.want)
			}
		})
	}
}

func TestParseEntry_ExplicitStatusZero(t *testing.T) {
	e, err := ParseEntry([]byte(`{"status":0,"msg":"no response written"}`))
	if err != nil {
		t.Fatalf("ParseEntry returned error: %v", err)
	}
	if e.Status != 0 {
		t.Errorf("Status = %d, want 0 (explicit)", e.Status)
	}
}

func TestParseEntry_EmptyUserID(t *testing.T) {
	line := `{"level":"info","logger":"http.log.access","msg":"handled request","request":{"method":"GET","host":"example.com","uri":"/"},"user_id":"","status":200}`
	e, err := ParseEntry([]byte(line))
	if err != nil {
		t.Fatalf("ParseEntry returned error: %v", err)
	}
	if e.Status != 200 || e.Method != "GET" {
		t.Errorf("got status=%d method=%q, want 200/GET", e.Status, e.Method)
	}
}

func TestParseEntry_NonJSON(t *testing.T) {
	if _, err := ParseEntry([]byte("2026/08/08 12:00:00 INFO something")); err == nil {
		t.Error("ParseEntry accepted a non-JSON line")
	}
	if _, err := ParseEntry([]byte("hello world")); err == nil {
		t.Error("ParseEntry accepted a garbage line")
	}
}

func TestParseEntry_CRLF(t *testing.T) {
	line := `{"level":"info","logger":"http.log.access","msg":"handled request","status":200}`
	e, err := ParseEntry([]byte(line + "\r"))
	if err != nil {
		t.Fatalf("ParseEntry returned error for CRLF line: %v", err)
	}
	if got, want := string(e.Raw), line; got != want {
		t.Errorf("Raw = %q, want %q (trailing \\r stripped)", got, want)
	}
	if e.Status != 200 {
		t.Errorf("Status = %d, want 200", e.Status)
	}
}
