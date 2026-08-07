package validator

import "regexp"

// Redactor scrubs secret-shaped values from command output. It is a
// defence in depth: the validator must not log secrets in the first place,
// but when caddy surfaces a value that matches a known pattern we do not
// want to forward it verbatim to the UI or to disk.
//
// A Redactor is constructed once and shared across calls; it is safe for
// concurrent use because the underlying regexp patterns are read-only.
type Redactor struct {
	patterns []*regexp.Regexp
}

// DefaultRedactor returns a Redactor configured for the well-known secret
// keys emitted by common Caddyfile directives. The pattern set is
// deliberately conservative: only the value following a recognised key is
// masked, and the regexes stop at whitespace, quotes or punctuation.
func DefaultRedactor() *Redactor {
	keys := []string{
		"password", "passwd", "secret", "token",
		"api_key", "apikey",
		"private_key", "privatekey",
		"access_key", "accesskey",
		"client_secret", "clientsecret",
		"authorization",
	}
	rs := make([]*regexp.Regexp, 0, len(keys))
	for _, k := range keys {
		rs = append(rs, regexp.MustCompile(
			`(?i)(\b`+regexp.QuoteMeta(k)+`\s*=\s*)("[^"]*"|'[^']*'|[^\s"',;]+)`,
		))
	}
	return &Redactor{patterns: rs}
}

// NewRedactor builds a Redactor from the given secret keys. Matching is
// case-insensitive and looks for KEY=VALUE where the value is double
// quoted, single quoted, or a bare token terminated by whitespace or
// punctuation.
func NewRedactor(keys ...string) *Redactor {
	rs := make([]*regexp.Regexp, 0, len(keys))
	for _, k := range keys {
		rs = append(rs, regexp.MustCompile(
			`(?i)(\b`+regexp.QuoteMeta(k)+`\s*=\s*)("[^"]*"|'[^']*'|[^\s"',;]+)`,
		))
	}
	return &Redactor{patterns: rs}
}

// Redact returns s with every recognised secret value replaced by
// "<redacted>". The matching key is preserved so the user can still see
// which field was masked. A nil receiver is a valid no-op so callers
// that build a Redactor conditionally do not need to nil-check.
func (r *Redactor) Redact(s string) string {
	if r == nil {
		return s
	}
	for _, p := range r.patterns {
		s = p.ReplaceAllString(s, "${1}<redacted>")
	}
	return s
}
