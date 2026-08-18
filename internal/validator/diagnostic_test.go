package validator

import (
	"strings"
	"testing"
)

func TestDiagnostic_String(t *testing.T) {
	cases := []struct {
		name string
		d    Diagnostic
		want string
	}{
		{
			name: "with line and column",
			d:    Diagnostic{Path: "/etc/caddy/Caddyfile", Line: 7, Column: 12, Message: "unexpected token", Severity: SeverityError},
			want: "/etc/caddy/Caddyfile:7:12: error: unexpected token",
		},
		{
			name: "line only",
			d:    Diagnostic{Path: "/etc/caddy/Caddyfile", Line: 42, Message: "broken", Severity: SeverityError},
			want: "/etc/caddy/Caddyfile:42: error: broken",
		},
		{
			name: "no location",
			d:    Diagnostic{Path: "/etc/caddy/Caddyfile", Message: "assertion failed", Severity: SeverityError},
			want: "/etc/caddy/Caddyfile: error: assertion failed",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.d.String(); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSeverity_String(t *testing.T) {
	cases := []struct {
		s    Severity
		want string
	}{
		{SeverityError, "error"},
		{SeverityWarning, "warning"},
		{SeverityInfo, "info"},
		{SeverityDebug, "debug"},
		{Severity(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("Severity(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestParseDiagnostics(t *testing.T) {
	cases := []struct {
		name, in string
		want     []Diagnostic
	}{
		{
			name: "line and column",
			in:   "/etc/caddy/Caddyfile:7:12: unexpected token",
			want: []Diagnostic{
				{Path: "/etc/caddy/Caddyfile", Line: 7, Column: 12, Message: "unexpected token", Severity: SeverityError},
			},
		},
		{
			name: "line only",
			in:   "/etc/caddy/Caddyfile:42: something broke",
			want: []Diagnostic{
				{Path: "/etc/caddy/Caddyfile", Line: 42, Message: "something broke", Severity: SeverityError},
			},
		},
		{
			name: "plain line uses default path",
			in:   "caddy: assertion failed",
			want: []Diagnostic{
				{Path: "/default/Caddyfile", Message: "caddy: assertion failed", Severity: SeverityError},
			},
		},
		{
			name: "adapting wrapper with position strips the wrapper",
			in:   "Error: adapting config using caddyfile: /var/folders/x/lazycaddy-validate-123.caddy:2: unrecognized directive: bogus_directive",
			want: []Diagnostic{
				{Path: "/var/folders/x/lazycaddy-validate-123.caddy", Line: 2, Message: "unrecognized directive: bogus_directive", Severity: SeverityError},
			},
		},
		{
			name: "adapting wrapper with line and column",
			in:   "Error: adapting config using caddyfile: /etc/caddy/Caddyfile:7:12: unexpected token",
			want: []Diagnostic{
				{Path: "/etc/caddy/Caddyfile", Line: 7, Column: 12, Message: "unexpected token", Severity: SeverityError},
			},
		},
		{
			name: "trailing at path:line form",
			in:   "Error: adapting config using caddyfile: unexpected EOF, at /var/folders/x/lazycaddy-validate-123.caddy:3",
			want: []Diagnostic{
				{Path: "/var/folders/x/lazycaddy-validate-123.caddy", Line: 3, Message: "unexpected EOF", Severity: SeverityError},
			},
		},
		{
			name: "trailing at path:line with import chain suffix",
			in:   "Error: adapting config using caddyfile: ', expecting '}', at /var/folders/x/lazycaddy-validate-123.caddy:2 import chain: ['']",
			want: []Diagnostic{
				{Path: "/var/folders/x/lazycaddy-validate-123.caddy", Line: 2, Message: "', expecting '}'", Severity: SeverityError},
			},
		},
		{
			name: "unpositioned matcher error keeps the wrapped message",
			in:   "Error: adapting config using caddyfile: parsing caddyfile tokens for 'reverse_proxy': unrecognized matcher name: @phantom",
			want: []Diagnostic{
				{Path: "/default/Caddyfile", Message: "adapting config using caddyfile: parsing caddyfile tokens for 'reverse_proxy': unrecognized matcher name: @phantom", Severity: SeverityError},
			},
		},
		{
			name: "caddy top-level error prefix",
			in:   "Error: adapting config using caddyfile: invalid server block",
			want: []Diagnostic{
				{Path: "/default/Caddyfile", Message: "adapting config using caddyfile: invalid server block", Severity: SeverityError},
			},
		},
		{
			name: "multiple lines",
			in:   "/etc/caddy/Caddyfile:1:1: first\n/etc/caddy/Caddyfile:2:2: second",
			want: []Diagnostic{
				{Path: "/etc/caddy/Caddyfile", Line: 1, Column: 1, Message: "first", Severity: SeverityError},
				{Path: "/etc/caddy/Caddyfile", Line: 2, Column: 2, Message: "second", Severity: SeverityError},
			},
		},
		{
			name: "empty",
			in:   "",
			want: nil,
		},
		{
			name: "blank lines skipped",
			in:   "/etc/caddy/Caddyfile:1:1: x\n\n\n/etc/caddy/Caddyfile:2:2: y",
			want: []Diagnostic{
				{Path: "/etc/caddy/Caddyfile", Line: 1, Column: 1, Message: "x", Severity: SeverityError},
				{Path: "/etc/caddy/Caddyfile", Line: 2, Column: 2, Message: "y", Severity: SeverityError},
			},
		},
		{
			name: "trailing newline trimmed",
			in:   "/etc/caddy/Caddyfile:1:1: x\n",
			want: []Diagnostic{
				{Path: "/etc/caddy/Caddyfile", Line: 1, Column: 1, Message: "x", Severity: SeverityError},
			},
		},
		{
			name: "whitespace-only lines skipped",
			in:   "/etc/caddy/Caddyfile:1:1: x\n   \n\t\n",
			want: []Diagnostic{
				{Path: "/etc/caddy/Caddyfile", Line: 1, Column: 1, Message: "x", Severity: SeverityError},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseDiagnostics("/default/Caddyfile", c.in)
			if len(got) != len(c.want) {
				t.Fatalf("len(diags) = %d, want %d (got=%+v)", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("diag[%d] = %+v, want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestParseLogLevel_Text(t *testing.T) {
	cases := []struct {
		line string
		want Severity
	}{
		{"ERROR /etc/caddy/Caddyfile:47:1: module not registered", SeverityError},
		{"INFO  using config from file", SeverityInfo},
		{"WARN  deprecated directive", SeverityWarning},
		{"DEBUG verbose", SeverityDebug},
		{"plain line with no level", SeverityError},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			if got := parseLogLevel(c.line); got != c.want {
				t.Errorf("parseLogLevel(%q) = %v, want %v", c.line, got, c.want)
			}
		})
	}
}

func TestParseLogLevel_Logfmt(t *testing.T) {
	cases := []struct {
		line string
		want Severity
	}{
		{`level=error msg="module not registered"`, SeverityError},
		{`level=info msg="using config from file"`, SeverityInfo},
		{`level=warning msg="deprecated"`, SeverityWarning},
		{`level=debug msg="trace"`, SeverityDebug},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			if got := parseLogLevel(c.line); got != c.want {
				t.Errorf("parseLogLevel(%q) = %v, want %v", c.line, got, c.want)
			}
		})
	}
}

func TestParseLogLevel_JSON(t *testing.T) {
	cases := []struct {
		line string
		want Severity
	}{
		{`{"level":"error","msg":"module not registered"}`, SeverityError},
		{`{"level":"info","msg":"using config from file"}`, SeverityInfo},
		{`{"level": "warning", "msg":"deprecated"}`, SeverityWarning},
		{`{"level":"debug","msg":"trace"}`, SeverityDebug},
		{`{"msg":"no level field"}`, SeverityError},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			if got := parseLogLevel(c.line); got != c.want {
				t.Errorf("parseLogLevel(%q) = %v, want %v", c.line, got, c.want)
			}
		})
	}
}

func TestParseDiagnostics_MixedLevels(t *testing.T) {
	// Caddy typically emits an info line before the error:
	//   INFO  using config from file
	//   ERROR /etc/caddy/Caddyfile:47:1: module not registered
	in := "INFO  using config from file\n" +
		"ERROR /etc/caddy/Caddyfile:47:1: module not registered"
	got := ParseDiagnostics("/default/Caddyfile", in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (got = %+v)", len(got), got)
	}
	// The info line falls back to the default path with the full
	// text as the message and is tagged SeverityInfo.
	if got[0].Severity != SeverityInfo {
		t.Errorf("info diagnostic severity = %v, want SeverityInfo", got[0].Severity)
	}
	if !strings.Contains(got[0].Message, "using config from file") {
		t.Errorf("info diagnostic message = %q, want to contain 'using config from file'", got[0].Message)
	}
	// The error line is matched against the path:line:col regex and
	// is tagged SeverityError.
	if got[1].Path != "/etc/caddy/Caddyfile" {
		t.Errorf("error diagnostic path = %q, want /etc/caddy/Caddyfile", got[1].Path)
	}
	if got[1].Line != 47 {
		t.Errorf("error diagnostic line = %d, want 47", got[1].Line)
	}
	if got[1].Column != 1 {
		t.Errorf("error diagnostic column = %d, want 1", got[1].Column)
	}
	if got[1].Severity != SeverityError {
		t.Errorf("error diagnostic severity = %v, want SeverityError", got[1].Severity)
	}
}

func TestParseDiagnostics_JSONWithLevel(t *testing.T) {
	in := `{"level":"info","msg":"using config from file"}` + "\n" +
		`{"level":"error","msg":"module not registered","file":"/etc/caddy/Caddyfile","line":47}`
	got := ParseDiagnostics("/default/Caddyfile", in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Severity != SeverityInfo {
		t.Errorf("got[0].Severity = %v, want SeverityInfo", got[0].Severity)
	}
	if got[1].Severity != SeverityError {
		t.Errorf("got[1].Severity = %v, want SeverityError", got[1].Severity)
	}
}
