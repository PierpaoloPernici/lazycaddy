package caddyfile

import (
	"fmt"
	"regexp"
	"strings"
)

// statusTokenRe matches a bare HTTP status code, the discriminator the
// respond directive uses to tell a status argument from a body argument.
var statusTokenRe = regexp.MustCompile(`^\d{3}$`)

// matcherToken reports whether a positional token is Caddy's inline request
// matcher: a named matcher (@name), a bare path matcher (/path/*) or the
// wildcard matcher (*). It mirrors Caddy's own convention so path matchers
// are never mistaken for upstreams, destinations or formats.
func matcherToken(tok string) bool {
	return strings.HasPrefix(tok, "@") || strings.HasPrefix(tok, "/") || tok == "*"
}

// splitMatcher extracts a leading inline matcher token, when present, from
// the positional arguments of a directive that accepts one.
func splitMatcher(args []string) (matcher string, rest []string) {
	if len(args) > 0 && matcherToken(args[0]) {
		return args[0], args[1:]
	}
	return "", args
}

// positionalTokens returns the raw source text of each positional token of
// a directive's header line: every token after the directive name and
// before any block opener. Raw text preserves the exact spelling of each
// token, including enclosing quotes, so round-tripping through a form never
// rewrites bytes the operator did not change.
func (p *Planner) positionalTokens(n Node) ([]string, error) {
	located, err := p.locate(n)
	if err != nil {
		return nil, err
	}
	if located.Kind != KindDirective {
		return nil, fmt.Errorf("%w: node %q is not a directive", ErrInvalidContext, located.Name)
	}
	toks, openBrace, err := p.headerTokens(*located)
	if err != nil {
		return nil, err
	}
	end := len(toks)
	if openBrace >= 0 {
		end = openBrace
	}
	src := p.doc.Source
	out := make([]string, 0, end-1)
	for _, tok := range toks[1:end] {
		out = append(out, string(src[located.Range.Start+tok.Start:located.Range.Start+tok.End]))
	}
	return out, nil
}

// expectDirective locates n and reports whether it is a directive with the
// given name. Every form Get/Set validates the node identity first, so a
// stale or foreign selection is rejected before any edit is planned.
func (p *Planner) expectDirective(n Node, name string) error {
	located, err := p.locate(n)
	if err != nil {
		return err
	}
	if located.Kind != KindDirective || located.Name != name {
		return fmt.Errorf("%w: node %q is not a %s directive", ErrUnsupported, located.Name, name)
	}
	return nil
}

// joinFieldArgs joins non-empty positional parts with single spaces, the
// exact shape the planner emits for a directive header.
func joinFieldArgs(parts ...string) string {
	var out []string
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, " ")
}

// SplitFieldTokens splits a form field's raw text into Caddyfile tokens,
// honoring quotes, heredocs and placeholders, so a value such as
// `"app one:8080"` stays one token. It is the UI-side counterpart of the
// planner's raw positional tokens and is used to turn a typed multi-token
// field (upstreams, formats, import args) into separate raw tokens.
func SplitFieldTokens(text string) []string {
	toks, err := lex([]byte(text))
	if err != nil {
		return strings.Fields(text)
	}
	out := make([]string, 0, len(toks))
	for _, tok := range toks {
		if tok.Kind == tokenOpenBrace || tok.Kind == tokenCloseBrace {
			continue
		}
		out = append(out, text[tok.Start:tok.End])
	}
	return out
}

// RespondFields is the editable positional portion of a respond directive.
// Per the official documentation, the first non-matcher argument is either
// a 3-digit status code or a response body, and when it is a body the next
// argument may be the status code.
type RespondFields struct {
	Matcher string
	Status  string
	Body    string
}

// GetRespondFields reads the optional matcher, status and body from a
// respond directive. Configurations the form cannot represent without
// guessing (for example a status followed by a body, which the documented
// grammar does not allow) return ErrAmbiguous so the raw editor remains
// the only path and no byte is rewritten.
func (p *Planner) GetRespondFields(n Node) (RespondFields, error) {
	if err := p.expectDirective(n, "respond"); err != nil {
		return RespondFields{}, err
	}
	args, err := p.positionalTokens(n)
	if err != nil {
		return RespondFields{}, err
	}
	matcher, rest := splitMatcher(args)
	fields := RespondFields{Matcher: matcher}
	switch len(rest) {
	case 0:
	case 1:
		if statusTokenRe.MatchString(rest[0]) {
			fields.Status = rest[0]
		} else {
			fields.Body = rest[0]
		}
	case 2:
		if statusTokenRe.MatchString(rest[0]) {
			return RespondFields{}, fmt.Errorf("%w: respond has a status followed by %q; the documented grammar is <status>|<body> [<status>]", ErrAmbiguous, rest[1])
		}
		fields.Body, fields.Status = rest[0], rest[1]
	default:
		return RespondFields{}, fmt.Errorf("%w: respond has %d positional arguments; the form supports a matcher, a status and a body", ErrAmbiguous, len(rest))
	}
	return fields, nil
}

// SetRespondFields plans replacing the positional fields of a respond
// directive, preserving the optional matcher, the nested block and every
// byte outside the argument span.
func (p *Planner) SetRespondFields(n Node, fields RespondFields) (*PlannedEdit, error) {
	if err := p.expectDirective(n, "respond"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(fields.Status) == "" && strings.TrimSpace(fields.Body) == "" {
		return nil, fmt.Errorf("%w: respond requires a status code or a body", ErrInvalidContext)
	}
	return p.SetArgs(n, joinFieldArgs(strings.TrimSpace(fields.Matcher), strings.TrimSpace(fields.Body), strings.TrimSpace(fields.Status)))
}

// RedirFields is the editable positional portion of a redir directive:
// an optional matcher, the destination and an optional status code.
type RedirFields struct {
	Matcher string
	To      string
	Status  string
}

// GetRedirFields reads the matcher, destination and status from a redir
// directive. The status is a free-form token (3xx code, 401, temporary,
// permanent, html or a placeholder), so it is never validated here.
func (p *Planner) GetRedirFields(n Node) (RedirFields, error) {
	if err := p.expectDirective(n, "redir"); err != nil {
		return RedirFields{}, err
	}
	args, err := p.positionalTokens(n)
	if err != nil {
		return RedirFields{}, err
	}
	matcher, rest := splitMatcher(args)
	fields := RedirFields{Matcher: matcher}
	switch len(rest) {
	case 0:
	case 1:
		fields.To = rest[0]
	case 2:
		fields.To, fields.Status = rest[0], rest[1]
	default:
		return RedirFields{}, fmt.Errorf("%w: redir has %d positional arguments; the form supports a matcher, a destination and a status", ErrAmbiguous, len(rest))
	}
	return fields, nil
}

// SetRedirFields plans replacing the positional fields of a redir directive.
func (p *Planner) SetRedirFields(n Node, fields RedirFields) (*PlannedEdit, error) {
	if err := p.expectDirective(n, "redir"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(fields.To) == "" {
		return nil, fmt.Errorf("%w: redir requires a destination", ErrInvalidContext)
	}
	return p.SetArgs(n, joinFieldArgs(strings.TrimSpace(fields.Matcher), strings.TrimSpace(fields.To), strings.TrimSpace(fields.Status)))
}

// FileServerFields is the editable positional portion of a file_server
// directive: an optional matcher and the optional browse mode.
type FileServerFields struct {
	Matcher string
	Browse  bool
}

// GetFileServerFields reads the matcher and browse mode from a file_server
// directive. Any positional argument other than the documented browse mode
// (for example a browse template file) makes the form refuse the construct.
func (p *Planner) GetFileServerFields(n Node) (FileServerFields, error) {
	if err := p.expectDirective(n, "file_server"); err != nil {
		return FileServerFields{}, err
	}
	args, err := p.positionalTokens(n)
	if err != nil {
		return FileServerFields{}, err
	}
	matcher, rest := splitMatcher(args)
	fields := FileServerFields{Matcher: matcher}
	switch len(rest) {
	case 0:
	case 1:
		if rest[0] != "browse" {
			return FileServerFields{}, fmt.Errorf("%w: file_server argument %q is not the browse mode", ErrAmbiguous, rest[0])
		}
		fields.Browse = true
	default:
		return FileServerFields{}, fmt.Errorf("%w: file_server has %d positional arguments; the form supports a matcher and the browse mode", ErrAmbiguous, len(rest))
	}
	return fields, nil
}

// SetFileServerFields plans replacing the positional fields of a
// file_server directive.
func (p *Planner) SetFileServerFields(n Node, fields FileServerFields) (*PlannedEdit, error) {
	if err := p.expectDirective(n, "file_server"); err != nil {
		return nil, err
	}
	args := []string{strings.TrimSpace(fields.Matcher)}
	if fields.Browse {
		args = append(args, "browse")
	}
	return p.SetArgs(n, joinFieldArgs(args...))
}

// PhpFastcgiFields is the editable positional portion of a php_fastcgi
// directive: an optional matcher and the FastCGI gateway addresses.
type PhpFastcgiFields struct {
	Matcher   string
	Upstreams []string
}

// GetPhpFastcgiFields reads the matcher and gateway addresses from a
// php_fastcgi directive.
func (p *Planner) GetPhpFastcgiFields(n Node) (PhpFastcgiFields, error) {
	if err := p.expectDirective(n, "php_fastcgi"); err != nil {
		return PhpFastcgiFields{}, err
	}
	args, err := p.positionalTokens(n)
	if err != nil {
		return PhpFastcgiFields{}, err
	}
	matcher, rest := splitMatcher(args)
	return PhpFastcgiFields{Matcher: matcher, Upstreams: append([]string(nil), rest...)}, nil
}

// SetPhpFastcgiFields plans replacing the positional fields of a
// php_fastcgi directive. At least one FastCGI gateway is required.
func (p *Planner) SetPhpFastcgiFields(n Node, fields PhpFastcgiFields) (*PlannedEdit, error) {
	if err := p.expectDirective(n, "php_fastcgi"); err != nil {
		return nil, err
	}
	if len(fields.Upstreams) == 0 {
		return nil, fmt.Errorf("%w: php_fastcgi requires at least one gateway", ErrInvalidContext)
	}
	args := make([]string, 0, 1+len(fields.Upstreams))
	if m := strings.TrimSpace(fields.Matcher); m != "" {
		args = append(args, m)
	}
	for _, upstream := range fields.Upstreams {
		if u := strings.TrimSpace(upstream); u != "" {
			args = append(args, u)
		}
	}
	return p.SetArgs(n, strings.Join(args, " "))
}

// EncodeFields is the editable positional portion of an encode directive:
// an optional matcher and the enabled encoding formats. An empty format
// list is valid (Caddy then enables zstd and gzip by default).
type EncodeFields struct {
	Matcher string
	Formats []string
}

// GetEncodeFields reads the matcher and formats from an encode directive.
func (p *Planner) GetEncodeFields(n Node) (EncodeFields, error) {
	if err := p.expectDirective(n, "encode"); err != nil {
		return EncodeFields{}, err
	}
	args, err := p.positionalTokens(n)
	if err != nil {
		return EncodeFields{}, err
	}
	matcher, rest := splitMatcher(args)
	return EncodeFields{Matcher: matcher, Formats: append([]string(nil), rest...)}, nil
}

// SetEncodeFields plans replacing the positional fields of an encode
// directive.
func (p *Planner) SetEncodeFields(n Node, fields EncodeFields) (*PlannedEdit, error) {
	if err := p.expectDirective(n, "encode"); err != nil {
		return nil, err
	}
	args := make([]string, 0, 1+len(fields.Formats))
	if m := strings.TrimSpace(fields.Matcher); m != "" {
		args = append(args, m)
	}
	for _, format := range fields.Formats {
		if f := strings.TrimSpace(format); f != "" {
			args = append(args, f)
		}
	}
	return p.SetArgs(n, strings.Join(args, " "))
}

// HeaderFields is the editable positional portion of a header directive:
// an optional matcher, the field (optionally prefixed with +, -, ? or >)
// and the value or search pattern, plus the optional replacement of a
// search-and-replace operation.
type HeaderFields struct {
	Matcher string
	Field   string
	Value   string
	Replace string
}

// GetHeaderFields reads the matcher, field, value and optional replacement
// from a header directive. More than three positional tokens (a value
// spanning several tokens) make the form refuse the construct so the raw
// editor stays the only path.
func (p *Planner) GetHeaderFields(n Node) (HeaderFields, error) {
	if err := p.expectDirective(n, "header"); err != nil {
		return HeaderFields{}, err
	}
	args, err := p.positionalTokens(n)
	if err != nil {
		return HeaderFields{}, err
	}
	matcher, rest := splitMatcher(args)
	fields := HeaderFields{Matcher: matcher}
	switch len(rest) {
	case 0:
	case 1:
		fields.Field = rest[0]
	case 2:
		fields.Field, fields.Value = rest[0], rest[1]
	case 3:
		fields.Field, fields.Value, fields.Replace = rest[0], rest[1], rest[2]
	default:
		return HeaderFields{}, fmt.Errorf("%w: header has %d positional arguments; the form supports a matcher, a field, a value and a replacement", ErrAmbiguous, len(rest))
	}
	return fields, nil
}

// SetHeaderFields plans replacing the positional fields of a header
// directive. A value or replacement without a field is rejected.
func (p *Planner) SetHeaderFields(n Node, fields HeaderFields) (*PlannedEdit, error) {
	if err := p.expectDirective(n, "header"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(fields.Field) == "" && (strings.TrimSpace(fields.Value) != "" || strings.TrimSpace(fields.Replace) != "") {
		return nil, fmt.Errorf("%w: header value and replacement require a field", ErrInvalidContext)
	}
	return p.SetArgs(n, joinFieldArgs(strings.TrimSpace(fields.Matcher), strings.TrimSpace(fields.Field), strings.TrimSpace(fields.Value), strings.TrimSpace(fields.Replace)))
}

// TlsFields is the editable positional portion of a tls directive: either
// the ACME email / internal / force_automate marker, or a certificate and
// key file pair, or both.
type TlsFields struct {
	Email    string
	CertFile string
	KeyFile  string
}

// GetTlsFields reads the email marker and the certificate/key pair from a
// tls directive. The documented grammar allows one marker argument, one
// cert/key pair, or marker plus pair; anything else is ambiguous.
func (p *Planner) GetTlsFields(n Node) (TlsFields, error) {
	if err := p.expectDirective(n, "tls"); err != nil {
		return TlsFields{}, err
	}
	args, err := p.positionalTokens(n)
	if err != nil {
		return TlsFields{}, err
	}
	fields := TlsFields{}
	switch len(args) {
	case 0:
	case 1:
		fields.Email = args[0]
	case 2:
		fields.CertFile, fields.KeyFile = args[0], args[1]
	case 3:
		fields.Email, fields.CertFile, fields.KeyFile = args[0], args[1], args[2]
	default:
		return TlsFields{}, fmt.Errorf("%w: tls has %d positional arguments; the form supports an email or internal marker plus an optional certificate/key pair", ErrAmbiguous, len(args))
	}
	return fields, nil
}

// SetTlsFields plans replacing the positional fields of a tls directive.
// A certificate without a key (or vice versa) is rejected.
func (p *Planner) SetTlsFields(n Node, fields TlsFields) (*PlannedEdit, error) {
	if err := p.expectDirective(n, "tls"); err != nil {
		return nil, err
	}
	cert := strings.TrimSpace(fields.CertFile)
	key := strings.TrimSpace(fields.KeyFile)
	if (cert == "") != (key == "") {
		return nil, fmt.Errorf("%w: tls requires both a certificate file and a key file, or neither", ErrInvalidContext)
	}
	return p.SetArgs(n, joinFieldArgs(strings.TrimSpace(fields.Email), cert, key))
}

// LogFields is the editable positional portion of a log directive: the
// optional logger name. Site logs and global-options logs both follow the
// documented log [<logger_name>] grammar.
type LogFields struct {
	Name string
}

// GetLogFields reads the logger name from a log directive.
func (p *Planner) GetLogFields(n Node) (LogFields, error) {
	if err := p.expectDirective(n, "log"); err != nil {
		return LogFields{}, err
	}
	args, err := p.positionalTokens(n)
	if err != nil {
		return LogFields{}, err
	}
	if len(args) > 1 {
		return LogFields{}, fmt.Errorf("%w: log has %d positional arguments; the form supports a single logger name", ErrAmbiguous, len(args))
	}
	name := ""
	if len(args) == 1 {
		name = args[0]
	}
	return LogFields{Name: name}, nil
}

// SetLogFields plans replacing the logger name of a log directive.
func (p *Planner) SetLogFields(n Node, fields LogFields) (*PlannedEdit, error) {
	if err := p.expectDirective(n, "log"); err != nil {
		return nil, err
	}
	return p.SetArgs(n, strings.TrimSpace(fields.Name))
}

// ImportFields is the editable positional portion of an import directive:
// the pattern and the optional argument list. The optional {block} is a
// nested block and is preserved verbatim by SetArgs.
type ImportFields struct {
	Pattern string
	Args    []string
}

// GetImportFields reads the pattern and arguments from an import directive.
// A bare import (no pattern) is ambiguous and refuses the form.
func (p *Planner) GetImportFields(n Node) (ImportFields, error) {
	if err := p.expectDirective(n, "import"); err != nil {
		return ImportFields{}, err
	}
	args, err := p.positionalTokens(n)
	if err != nil {
		return ImportFields{}, err
	}
	if len(args) == 0 {
		return ImportFields{}, fmt.Errorf("%w: import has no pattern", ErrAmbiguous)
	}
	return ImportFields{Pattern: args[0], Args: append([]string(nil), args[1:]...)}, nil
}

// SetImportFields plans replacing the pattern and arguments of an import
// directive. The optional nested block is preserved.
func (p *Planner) SetImportFields(n Node, fields ImportFields) (*PlannedEdit, error) {
	if err := p.expectDirective(n, "import"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(fields.Pattern) == "" {
		return nil, fmt.Errorf("%w: import requires a pattern", ErrInvalidContext)
	}
	args := make([]string, 0, 1+len(fields.Args))
	if pattern := strings.TrimSpace(fields.Pattern); pattern != "" {
		args = append(args, pattern)
	}
	for _, arg := range fields.Args {
		if a := strings.TrimSpace(arg); a != "" {
			args = append(args, a)
		}
	}
	return p.SetArgs(n, strings.Join(args, " "))
}
