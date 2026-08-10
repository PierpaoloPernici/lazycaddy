package caddyfile

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Role classifies an advisory semantic role of a source span.
//
// The classifier is presentation-only: it never defines Caddy syntax, it
// never replaces Caddy validation, and it never rejects or hides unknown or
// plugin directives (unknown tokens simply get no role). It degrades
// gracefully on partially parsed files: lexical roles (strings, heredocs,
// placeholders, durations, status codes, paths, ports, IP/CIDR values) are
// still produced, while tree-dependent roles (site addresses, directive
// names, matcher definitions) are produced only where the parse tree is
// intact.
type Role int

const (
	// RoleNone is the fallback: the span has no known advisory role.
	RoleNone Role = iota
	// RoleSiteAddress is a token of a site block header.
	RoleSiteAddress
	// RoleDomain is a hostname or domain, optionally wildcard-prefixed.
	RoleDomain
	// RolePath is a path token or sub-span starting with "/".
	RolePath
	// RolePort is a ":digits" port suffix.
	RolePort
	// RoleIP is an IPv4 or IPv6 address.
	RoleIP
	// RoleCIDR is an IP address with a CIDR mask.
	RoleCIDR
	// RoleMatcherDefinition is the @name token of a matcher definition
	// line.
	RoleMatcherDefinition
	// RoleMatcherReference is an @name token referring to a matcher.
	RoleMatcherReference
	// RolePlaceholder is a "{...}" placeholder sub-span inside a word or
	// string.
	RolePlaceholder
	// RoleDuration is a duration such as 10s, 5m30s or 1d.
	RoleDuration
	// RoleStatusCode is an HTTP status code in the range 100-599.
	RoleStatusCode
	// RoleString is a double-quoted or backtick-quoted string token.
	RoleString
	// RoleHeredoc is a whole heredoc token, markers included.
	RoleHeredoc
	// RoleHeredocMarker is the <<MARKER opener or the closing MARKER of a
	// heredoc.
	RoleHeredocMarker
	// RoleDirectiveName is the first token of a directive line.
	RoleDirectiveName
)

func (r Role) String() string {
	switch r {
	case RoleSiteAddress:
		return "site-address"
	case RoleDomain:
		return "domain"
	case RolePath:
		return "path"
	case RolePort:
		return "port"
	case RoleIP:
		return "ip"
	case RoleCIDR:
		return "cidr"
	case RoleMatcherDefinition:
		return "matcher-definition"
	case RoleMatcherReference:
		return "matcher-reference"
	case RolePlaceholder:
		return "placeholder"
	case RoleDuration:
		return "duration"
	case RoleStatusCode:
		return "status-code"
	case RoleString:
		return "string"
	case RoleHeredoc:
		return "heredoc"
	case RoleHeredocMarker:
		return "heredoc-marker"
	case RoleDirectiveName:
		return "directive-name"
	default:
		return "none"
	}
}

// Classified is one advisory role span at byte offsets.
type Classified struct {
	Role       Role
	Start, End int
}

// SemanticRoles is the advisory semantic classification of one document's
// source. Spans are emitted in source order; sub-spans (placeholders, port
// suffixes, heredoc markers) are nested inside their parent span.
type SemanticRoles struct {
	Spans []Classified
}

// add appends a span when it covers at least one byte.
func (r *SemanticRoles) add(start, end int, role Role) {
	if start < end {
		r.Spans = append(r.Spans, Classified{Role: role, Start: start, End: end})
	}
}

// Classify classifies src, using the tolerant parser for tree context.
// Lexical highlighting and raw editing never depend on the result.
func Classify(src []byte) *SemanticRoles {
	return classifyDoc(Parse(src))
}

// ClassifyDoc classifies an already parsed document.
func ClassifyDoc(doc *Document) *SemanticRoles {
	return classifyDoc(doc)
}

var (
	statusCodeRe = regexp.MustCompile(`^[1-5]\d\d$`)
	portRe       = regexp.MustCompile(`^:\d+$`)
	// domainRe is intentionally conservative: labels are alphanumeric with
	// hyphens, the final label must start with a letter (a TLD is never
	// all digits, so protocol tokens like tls1.2 stay unclassified), and a
	// single leading "*." wildcard is allowed.
	domainRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)*[a-z]([a-z0-9-]*[a-z0-9])?$`)
)

func classifyDoc(doc *Document) *SemanticRoles {
	src := doc.Source
	out := &SemanticRoles{}
	toks, _ := lex(src)

	// Tree context: byte spans of site header tokens, directive names and
	// matcher definitions. Span identity is used instead of token equality
	// so context survives lexer reruns.
	siteHeaders := map[string]bool{}
	directiveNames := map[string]bool{}
	matcherDefs := map[string]bool{}
	key := func(a, b int) string {
		return strconv.Itoa(a) + ":" + strconv.Itoa(b)
	}

	var walk func(ns []Node)
	walk = func(ns []Node) {
		for _, n := range ns {
			switch n.Kind {
			case KindSite:
				for _, t := range siteHeaderTokens(src, n) {
					siteHeaders[key(t.Start, t.End)] = true
				}
			case KindDirective:
				if n.Name == "" {
					break
				}
				nt, err := lex([]byte(n.Range.Text(src)))
				if err != nil || len(nt) == 0 || nt[0].Kind != tokenWord {
					break
				}
				start, end := n.Range.Start+nt[0].Start, n.Range.Start+nt[0].End
				if strings.HasPrefix(n.Name, "@") {
					matcherDefs[key(start, end)] = true
				} else {
					directiveNames[key(start, end)] = true
				}
			}
			walk(n.Children)
		}
	}
	walk(doc.Nodes)

	for _, tok := range toks {
		k := key(tok.Start, tok.End)
		switch tok.Kind {
		case tokenQuoted:
			out.add(tok.Start, tok.End, RoleString)
		case tokenHeredoc:
			out.add(tok.Start, tok.End, RoleHeredoc)
			opener, closer := heredocMarkers(src, tok)
			out.add(opener.Start, opener.End, RoleHeredocMarker)
			out.add(closer.Start, closer.End, RoleHeredocMarker)
		case tokenWord:
			switch {
			case matcherDefs[k]:
				out.add(tok.Start, tok.End, RoleMatcherDefinition)
			case siteHeaders[k]:
				out.add(tok.Start, tok.End, RoleSiteAddress)
				classifyWordSubspans(src, tok, out)
			case directiveNames[k]:
				out.add(tok.Start, tok.End, RoleDirectiveName)
			default:
				classifyWord(src, tok, out)
			}
		}
	}
	return out
}

// siteHeaderTokens returns the address tokens of a site block header: the
// tokens before the first unquoted open brace for braced sites, or the
// first logical line's tokens for a brace-less site. Offsets are absolute
// into the document.
func siteHeaderTokens(src []byte, n Node) []Token {
	raw := n.Range.Text(src)
	toks, err := lex([]byte(raw))
	if err != nil || len(toks) == 0 {
		return nil
	}
	shift := n.Range.Start
	for i := range toks {
		toks[i].Start += shift
		toks[i].End += shift
		if toks[i].Kind == tokenOpenBrace {
			return toks[:i]
		}
	}
	groups := groupLines(toks)
	if len(groups) > 0 {
		return groups[0]
	}
	return nil
}

// heredocMarkers returns the byte spans of a heredoc token's <<MARKER
// opener and its closing marker. A heredoc token always covers the opener
// line, the body and the closing marker.
func heredocMarkers(src []byte, tok Token) (opener, closer Classified) {
	raw := src[tok.Start:tok.End]
	nl := strings.IndexByte(string(raw), '\n')
	if nl < 0 {
		return Classified{}, Classified{}
	}
	opener = Classified{Role: RoleHeredocMarker, Start: tok.Start, End: tok.Start + nl}
	end := len(raw)
	start := end
	for start > 0 && isMarkerChar(raw[start-1]) {
		start--
	}
	closer = Classified{Role: RoleHeredocMarker, Start: tok.Start + start, End: tok.Start + end}
	return opener, closer
}

// isMarkerChar reports whether b may appear in a heredoc marker.
func isMarkerChar(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}

// emitPlaceholders adds RolePlaceholder sub-spans for every "{...}" inside
// a word or string token.
func emitPlaceholders(src []byte, tok Token, out *SemanticRoles) {
	for _, sp := range placeholdersIn(src, Span{Start: tok.Start, End: tok.End}) {
		out.add(sp.Start, sp.End, RolePlaceholder)
	}
}

// classifyWord classifies a bareword token that has no tree context: it
// emits the token's whole-word role (domain, path, port, IP, CIDR,
// duration, status code) plus nested placeholder and port sub-spans.
// Unknown words and unknown/plugin directives stay unclassified.
func classifyWord(src []byte, tok Token, out *SemanticRoles) {
	word := tok.Text
	if strings.HasPrefix(word, "@") {
		out.add(tok.Start, tok.End, RoleMatcherReference)
		return
	}
	emitPlaceholders(src, tok, out)

	rest := word
	restStart := tok.Start
	if i := strings.Index(rest, "://"); i > 0 {
		rest = rest[i+3:]
		restStart = tok.Start + i + 3
	}

	// host:digits — a port suffix on a host, IP or bracketed IPv6 address.
	if i := strings.LastIndex(rest, ":"); i >= 0 && i+1 < len(rest) {
		if digits := rest[i+1:]; isAllDigits(digits) && isHostLike(rest[:i]) {
			out.add(restStart+i, tok.End, RolePort)
			classifyHost(rest[:i], restStart, out)
			return
		}
	}

	if strings.Contains(rest, "/") {
		if _, err := netip.ParsePrefix(rest); err == nil {
			out.add(restStart, tok.End, RoleCIDR)
			return
		}
		if strings.HasPrefix(rest, "/") {
			out.add(restStart, tok.End, RolePath)
			return
		}
	}
	if _, err := netip.ParseAddr(rest); err == nil {
		out.add(restStart, tok.End, RoleIP)
		return
	}
	if portRe.MatchString(rest) {
		out.add(restStart, tok.End, RolePort)
		return
	}
	if isDurationWord(rest) {
		out.add(restStart, tok.End, RoleDuration)
		return
	}
	if statusCodeRe.MatchString(rest) {
		out.add(restStart, tok.End, RoleStatusCode)
		return
	}
	if domainRe.MatchString(strings.ToLower(rest)) {
		out.add(restStart, tok.End, RoleDomain)
	}
}

// classifyWordSubspans emits the nested sub-spans of a site address token
// (placeholders, port suffixes, paths). The token itself already received
// RoleSiteAddress, so no whole-word role is emitted here.
func classifyWordSubspans(src []byte, tok Token, out *SemanticRoles) {
	emitPlaceholders(src, tok, out)
	rest := tok.Text
	restStart := tok.Start
	if i := strings.Index(rest, "://"); i > 0 {
		rest = rest[i+3:]
		restStart = tok.Start + i + 3
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 && i+1 < len(rest) {
		if digits := rest[i+1:]; isAllDigits(digits) && isHostLike(rest[:i]) {
			out.add(restStart+i, tok.End, RolePort)
		}
	}
	if strings.HasPrefix(rest, "/") {
		out.add(restStart, tok.End, RolePath)
	}
}

// classifyHost adds the role of a host prefix (domain, IP or bracketed
// IPv6) at the given byte offset.
func classifyHost(host string, start int, out *SemanticRoles) {
	if _, err := netip.ParseAddr(host); err == nil {
		out.add(start, start+len(host), RoleIP)
		return
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		if _, err := netip.ParseAddr(host[1 : len(host)-1]); err == nil {
			out.add(start, start+len(host), RoleIP)
			return
		}
	}
	if domainRe.MatchString(strings.ToLower(host)) {
		out.add(start, start+len(host), RoleDomain)
	}
}

func isHostLike(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		if _, err := netip.ParseAddr(s[1 : len(s)-1]); err == nil {
			return true
		}
	}
	return domainRe.MatchString(strings.ToLower(s))
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isDurationWord reports whether s parses as a Go duration or as a Caddy
// days value like "1d".
func isDurationWord(s string) bool {
	if _, err := time.ParseDuration(s); err == nil {
		return true
	}
	if len(s) > 1 && s[len(s)-1] == 'd' {
		if _, err := strconv.ParseInt(s[:len(s)-1], 10, 64); err == nil {
			return true
		}
	}
	return false
}
