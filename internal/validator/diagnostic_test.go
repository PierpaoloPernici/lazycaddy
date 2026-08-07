package validator

import "testing"

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
