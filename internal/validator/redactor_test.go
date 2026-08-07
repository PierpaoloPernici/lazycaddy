package validator

import (
	"strings"
	"testing"
)

func TestDefaultRedactor_MasksKnownKeys(t *testing.T) {
	r := DefaultRedactor()
	cases := []struct {
		name, in, want string
	}{
		{"equals", "password=hunter2", "password=<redacted>"},
		{"double quoted", `password="hunter 2"`, "password=<redacted>"},
		{"single quoted", "password='hunter 2'", "password=<redacted>"},
		{"token", "token=abc.def.ghi", "token=<redacted>"},
		{"api_key", "api_key=sk-12345", "api_key=<redacted>"},
		{"case insensitive", "PASSWORD=hunter2", "PASSWORD=<redacted>"},
		{"authorization quoted", `Authorization="Bearer abcdef"`, "Authorization=<redacted>"},
		{"unrelated key preserved", "id=42", "id=42"},
		{"no match without equals", "password hunter2", "password hunter2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.Redact(c.in)
			if got != c.want {
				t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestDefaultRedactor_StopsAtBoundary(t *testing.T) {
	r := DefaultRedactor()
	in := `password=hunter2,mode=strict`
	got := r.Redact(in)
	if !strings.Contains(got, "password=<redacted>") {
		t.Errorf("expected redacted password, got %q", got)
	}
	if !strings.Contains(got, "mode=strict") {
		t.Errorf("expected mode=strict to be preserved, got %q", got)
	}
}

func TestDefaultRedactor_LeavesSuffixAlone(t *testing.T) {
	r := DefaultRedactor()
	// "mypassword=" should not be matched: \b requires a word boundary,
	// and the '_'/'char' before 'p' is a word character.
	got := r.Redact("mypassword=hunter2")
	if got != "mypassword=hunter2" {
		t.Errorf("expected mypassword=hunter2 to be preserved, got %q", got)
	}
}

func TestNilRedactor_IsNoOp(t *testing.T) {
	var r *Redactor
	if got := r.Redact("password=hunter2"); got != "password=hunter2" {
		t.Errorf("nil redactor should be a no-op, got %q", got)
	}
}

func TestNewRedactor_OnlyConfiguredKeys(t *testing.T) {
	r := NewRedactor("custom_key")
	in := "custom_key=secret password=hunter2"
	got := r.Redact(in)
	if !strings.Contains(got, "custom_key=<redacted>") {
		t.Errorf("expected custom_key to be redacted, got %q", got)
	}
	if !strings.Contains(got, "password=hunter2") {
		t.Errorf("expected password to be left alone (not in custom set), got %q", got)
	}
}
