package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// sourceGutterWidth returns the cell width of the source line-number
// gutter: at least 7 cells ("NNNN│X ", where X is the always-reserved
// marker cell) and grows with the number of digits so line numbers beyond
// 9999 never misalign the source text. The selection Pane uses the same
// width so coordinate mapping matches the rendered gutter. The marker cell
// is reserved on every line — a badge when the line carries a finding or
// diagnostic, a plain space otherwise — so the source text never shifts
// horizontally between marked and unmarked lines.
func sourceGutterWidth(lineCount int) int {
	w := len(strconv.Itoa(lineCount)) + 3 // digits + "│" + marker + " "
	if w < 7 {
		return 7
	}
	return w
}

// highlightSource renders src with line numbers and syntax highlighting.
// The 1-based inclusive range [selStartLine, selEndLine] marks the selected
// section: its line numbers are emphasized and a subtle vertical bar is
// drawn in the gutter for those lines. Passing 0 for both bounds renders
// the gutter plainly. Returns the dim "(empty source ...)" message for an
// empty src, exactly like numberedSource. Optional advisory inline findings
// and authoritative caddy diagnostics are layered over the source
// (non-destructive) so suspicious parse-tree patterns and real validation
// errors stand out without changing any byte. Caddy diagnostics take
// precedence: their lines carry an 'E' gutter marker and their tokens
// render over advisory findings.
func highlightSource(src []byte, selStartLine, selEndLine int, findings []caddyfile.InlineFinding, diags []validator.Diagnostic) string {
	if len(src) == 0 {
		return dimStyle.Render("(empty source — raw view still available)")
	}
	lineSpans := caddyfile.Highlight(src)
	roles := caddyfile.Classify(src).Spans
	inlineByLine := inlineFindingsByLine(src, findings)
	diagsByLine := caddyDiagsByLine(diags)
	lines := strings.Split(string(src), "\n")
	gutterW := sourceGutterWidth(len(lines))
	var b strings.Builder
	base := 0
	for i, ln := range lines {
		lineNo := i + 1
		marker := inlineGutterMarker(inlineByLine[lineNo])
		if m := caddyGutterMarker(diagsByLine[lineNo]); m != 0 {
			// An authoritative caddy error (or warning) outranks every
			// advisory marker so the most actionable state reads first.
			marker = m
		}
		// Every line reserves the marker cell (a badge, or a space when
		// clean) so the source text stays horizontally aligned.
		badge := gutterMarkerBadge(marker)
		if selStartLine > 0 && lineNo >= selStartLine && lineNo <= selEndLine {
			b.WriteString(selectedGutterNumberStyle.Render(fmt.Sprintf("%*d", gutterW-3, lineNo)))
			b.WriteString(selectedGutterBarStyle.Render("▎"))
			b.WriteString(badge)
			b.WriteByte(' ')
		} else {
			fmt.Fprintf(&b, "%*d", gutterW-3, lineNo)
			b.WriteRune('│')
			b.WriteString(badge)
			b.WriteByte(' ')
		}
		if i < len(lineSpans) {
			b.WriteString(renderHighlightedLine(ln, base, lineSpans[i], roles, inlineByLine[lineNo], diagsByLine[lineNo]))
		} else {
			b.WriteString(ln)
		}
		b.WriteByte('\n')
		base += len(ln) + 1
	}
	return b.String()
}

// renderHighlightedLine styles one source line. It converts the
// base-relative lexical caddyfile spans and the document-absolute role spans
// into line-relative styledSpans (with the same clamping as before) and
// delegates to renderStyledLine, so the cell-accurate machinery is shared
// with the log renderer. Semantic role spans are layered after the lexical
// spans so a role overrides the lexical base only where the parse tree
// identified it reliably; nested role sub-spans (ports, paths, heredoc
// markers) override their parent role because they arrive later in source
// order. Inline advisory findings, when supplied for this line, are layered
// next so a suspicious token is unmistakable. Authoritative caddy
// diagnostics are layered last: the offending token (or the whole line when
// caddy reported no column) renders over everything else.
func renderHighlightedLine(ln string, base int, spans []caddyfile.Span, roles []caddyfile.Classified, inline []caddyfile.InlineFinding, diags []validator.Diagnostic) string {
	styled := make([]styledSpan, 0, len(spans)+len(roles)+len(inline)+len(diags))
	for _, sp := range spans {
		clamped, ok := clampSpan(sp.Start, sp.End, base, len(ln), styleKeyFor(sp.Kind))
		if ok {
			styled = append(styled, clamped)
		}
	}
	for _, sp := range roles {
		key := semanticRoleKeyFor(sp.Role)
		if key == 0 {
			continue
		}
		clamped, ok := clampSpan(sp.Start, sp.End, base, len(ln), key)
		if ok {
			styled = append(styled, clamped)
		}
	}
	for _, f := range inline {
		key := inlineStyleKeyFor(f.Severity)
		if key == 0 {
			continue
		}
		if clamped, ok := clampSpan(f.Start, f.End, base, len(ln), key); ok {
			styled = append(styled, clamped)
		}
	}
	for _, d := range diags {
		key := diagnosticStyleKeyFor(d.Severity)
		if key == 0 {
			continue
		}
		start, end, ok := diagnosticTokenSpan(ln, d)
		if !ok {
			continue
		}
		styled = append(styled, styledSpan{start: start, end: end, key: key})
	}
	return renderStyledLine(ln, styled, styleForKey)
}

// inlineFindingsByLine groups advisory findings by their 1-based source line.
// Findings whose range cannot be pinned (column 0, out-of-range) are ignored
// so the source view is never annotated on unreliable coordinates.
func inlineFindingsByLine(src []byte, findings []caddyfile.InlineFinding) map[int][]caddyfile.InlineFinding {
	out := map[int][]caddyfile.InlineFinding{}
	srcLen := len(src)
	for _, f := range findings {
		if f.StartLine <= 0 || f.Start < 0 || f.End > srcLen || f.Start >= f.End {
			continue
		}
		line := f.StartLine
		out[line] = append(out[line], f)
	}
	return out
}

// inlineGutterMarker returns the gutter marker byte for the findings on one
// line: '!' for a hint (likely problem), 'i' for an info, or 0 when the line
// has no finding. It never relies on colour alone, so the marker is always a
// distinct character even in monochrome terminals.
func inlineGutterMarker(findings []caddyfile.InlineFinding) byte {
	var marker byte
	for _, f := range findings {
		switch f.Severity {
		case caddyfile.SeverityAdvisoryHint:
			return '!'
		case caddyfile.SeverityAdvisoryInfo:
			marker = 'i'
		}
	}
	return marker
}

// caddyDiagsByLine groups authoritative caddy validate diagnostics by their
// 1-based source line. Diagnostics without a reported line cannot be pinned
// and are ignored so the source view is never annotated on unreliable
// coordinates.
func caddyDiagsByLine(diags []validator.Diagnostic) map[int][]validator.Diagnostic {
	out := map[int][]validator.Diagnostic{}
	for _, d := range diags {
		if d.Line <= 0 {
			continue
		}
		out[d.Line] = append(out[d.Line], d)
	}
	return out
}

// caddyGutterMarker returns the gutter marker byte for the caddy
// diagnostics on one line: 'E' for an error, 'W' for a warning, or 0 when
// only non-actionable levels are present (the advisory marker survives).
// Like the advisory markers it never relies on colour alone.
func caddyGutterMarker(diags []validator.Diagnostic) byte {
	var marker byte
	for _, d := range diags {
		switch d.Severity {
		case validator.SeverityError:
			return 'E'
		case validator.SeverityWarning:
			marker = 'W'
		}
	}
	return marker
}

// gutterMarkerBadge renders the reserved gutter marker cell for one line:
// the marker character on a colored background badge, or a plain space when
// the line has no marker. Every line emits exactly one cell, so the source
// text never shifts horizontally between marked and unmarked lines and the
// badge is always the same size. The badge never relies on colour alone
// (the marker character is still distinct), and the background echoes the
// token styles: blue for advisory info, amber for advisory hint, red with
// white bold text for caddy errors, orange for caddy warnings.
func gutterMarkerBadge(marker byte) string {
	switch marker {
	case 'i':
		return gutterInfoBadgeStyle.Render("i")
	case '!':
		return gutterHintBadgeStyle.Render("!")
	case 'E':
		return gutterErrorBadgeStyle.Render("E")
	case 'W':
		return gutterWarningBadgeStyle.Render("W")
	default:
		return " "
	}
}

// diagnosticTokenSpan converts a caddy diagnostic's 1-based column into a
// byte span [start, end) relative to its line (ln). Column 0 (caddy did not
// report one) marks the whole line. The span covers the token starting at
// the column (word characters, so directive names, matchers, domains and
// paths are highlighted whole); when the column falls past the line end no
// span is produced and the line is never annotated on unreliable
// coordinates. Columns are char-based, so multi-byte runes are never split.
func diagnosticTokenSpan(ln string, d validator.Diagnostic) (start, end int, ok bool) {
	if d.Column <= 0 {
		return 0, len(ln), true
	}
	col := 0
	runeIdx := 1
	for runeIdx < d.Column {
		if col >= len(ln) {
			return 0, 0, false // column beyond the line
		}
		_, size := utf8.DecodeRuneInString(ln[col:])
		col += size
		runeIdx++
	}
	tokEnd := col
	for tokEnd < len(ln) {
		r, size := utf8.DecodeRuneInString(ln[tokEnd:])
		if !tokenBoundaryRune(r) {
			break
		}
		tokEnd += size
	}
	if tokEnd == col {
		// No token at the column: mark the single character (or give up
		// when the line ends exactly at the column).
		_, size := utf8.DecodeRuneInString(ln[col:])
		if size == 0 {
			return 0, 0, false
		}
		tokEnd = col + size
	}
	return col, tokEnd, true
}

// tokenBoundaryRune reports whether r continues a Caddyfile token, so a
// diagnostic column expands to the full offending token (directive names,
// matchers, domains, paths). Structural characters and whitespace end the
// token.
func tokenBoundaryRune(r rune) bool {
	return !unicode.IsSpace(r) && !strings.ContainsRune("{}()#\"'=,[];", r)
}

// tokenFromMessage extracts the offending token caddy names in an
// unpositioned error message: the text after the last ": " with surrounding
// quotes stripped. "unrecognized matcher name: @phantom" yields
// "@phantom"; messages without a named token ("unexpected EOF") yield ""
// so the caller never pins on unreliable text.
func tokenFromMessage(msg string) string {
	i := strings.LastIndex(msg, ": ")
	if i < 0 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(msg[i+2:]), `"'`)
}

// pinDiagnostic locates the token named in an unpositioned caddy message
// (see tokenFromMessage) inside src and returns the 1-based line and
// column of its first word-boundary occurrence, or 0s when no reliable
// hit exists. It is a best-effort presentation mapping: caddy itself did
// not report a position, so the pinned coordinates are advisory and never
// block or modify anything.
func pinDiagnostic(src []byte, msg string) (line, col int) {
	tok := tokenFromMessage(msg)
	if tok == "" {
		return 0, 0
	}
	for i, ln := range strings.Split(string(src), "\n") {
		if c, ok := tokenColumn(ln, tok); ok {
			return i + 1, c
		}
	}
	return 0, 0
}

// tokenColumn finds tok in ln at a token boundary and returns its 1-based
// rune column (matching caddy's char-based columns), or ok=false when the
// token does not appear as a standalone word (so "@phantom" never matches
// inside "http@phantom").
func tokenColumn(ln, tok string) (int, bool) {
	for i := 0; ; {
		j := strings.Index(ln[i:], tok)
		if j < 0 {
			return 0, false
		}
		abs := i + j
		end := abs + len(tok)
		beforeOK := abs == 0 || tokenBoundaryByte(ln[abs-1])
		afterOK := end >= len(ln) || tokenBoundaryByte(ln[end])
		if beforeOK && afterOK {
			return utf8.RuneCountInString(ln[:abs]) + 1, true
		}
		i = abs + len(tok)
	}
}

// tokenBoundaryByte reports whether byte b is a token boundary, i.e. it
// does NOT continue a token (whitespace or a structural character). Bytes
// that are part of a multi-byte rune (>= 0x80) are never confirmed
// boundaries, so boundary checks stay conservative on non-ASCII text.
func tokenBoundaryByte(b byte) bool {
	if b >= 0x80 {
		return false
	}
	return !tokenBoundaryRune(rune(b))
}

// clampSpan converts an absolute [start, end) byte range to a line-relative
// styledSpan, clamped to the line's extent. Coordinates that fall entirely
// outside the line produce ok=false so multi-line tokens like heredocs and
// quoted strings are reported only on the lines they cover.
func clampSpan(start, end, base, lineLen int, key int) (styledSpan, bool) {
	s := start - base
	e := end - base
	if s < 0 {
		s = 0
	}
	if e > lineLen {
		e = lineLen
	}
	if s >= e {
		return styledSpan{}, false
	}
	return styledSpan{start: s, end: e, key: key}, true
}

// styledSpan is a byte range in a line with an already-resolved style key.
// The key groups consecutive spans that share a style so they emit as one
// chunk; key 0 means "no style" (verbatim).
type styledSpan struct {
	start, end int // byte offsets relative to ln (0-based)
	key        int // style key; 0 = verbatim
}

// styleResolver resolves a style key to the lipgloss style to apply.
// The Caddyfile and log renderers use disjoint key spaces, so each domain
// supplies its own resolver (styleForKey / logStyleForKey).
type styleResolver func(key int) lipgloss.Style

// renderStyledLine renders ln applying spans with cell accuracy. It maps
// the line's bytes to display cells (tabs and wide runes included) so
// spans apply cell-accurately, then emits the line as consecutive styled
// chunks, resolving each cell's style key through resolve. Every byte of
// ln is emitted exactly once; if the cell mapping ever misaligns, chunk
// boundaries fall back to rune/byte boundaries so the rendered view stays
// lossless. Spans are expected to be clamped to [0, len(ln)]; out-of-range
// offsets are handled gracefully.
func renderStyledLine(ln string, spans []styledSpan, resolve styleResolver) string {
	if ln == "" {
		return ""
	}
	// cellBytes[i] is the byte offset (into ln) of the rune that occupies
	// display cell i. A wide rune occupies two cells, both mapped to its own
	// byte offset, so a span boundary can never split a rune. Every rune
	// occupies at least one cell (tabs count as 1 cell) so no byte is ever
	// dropped from the rendered output.
	var cellBytes []int
	for off, r := range ln {
		w := lipgloss.Width(string(r))
		if w < 1 {
			w = 1
		}
		for i := 0; i < w; i++ {
			cellBytes = append(cellBytes, off)
		}
	}
	// Per-cell style keys, defaulting to the unstyled key. Spans arrive in
	// source order, so assigning in order makes later spans (nested
	// placeholders) override the cells of their parent word/string span.
	cells := make([]int, len(cellBytes))
	for _, sp := range spans {
		if sp.start >= sp.end {
			continue
		}
		cs := sort.Search(len(cellBytes), func(i int) bool { return cellBytes[i] >= sp.start })
		ce := sort.Search(len(cellBytes), func(i int) bool { return cellBytes[i] >= sp.end })
		for i := cs; i < ce; i++ {
			cells[i] = sp.key
		}
	}
	// Emit consecutive same-style cells as one chunk covering their bytes.
	var b strings.Builder
	for i := 0; i < len(cells); {
		key := cells[i]
		j := i
		for j < len(cells) && cells[j] == key {
			j++
		}
		startByte := cellBytes[i]
		endByte := len(ln)
		if j < len(cellBytes) {
			endByte = cellBytes[j]
		}
		chunk := ln[startByte:endByte]
		if key == 0 {
			// Unstyled text is written verbatim: lipgloss would expand tabs.
			b.WriteString(chunk)
		} else {
			writeStyledChunk(&b, resolve(key), chunk)
		}
		i = j
	}
	return b.String()
}

// writeStyledChunk renders chunk with the style while keeping tab characters
// byte-exact: lipgloss expands tabs to spaces (or drops them with
// TabWidth(0)), so tabs are emitted raw between styled segments.
func writeStyledChunk(b *strings.Builder, st lipgloss.Style, chunk string) {
	for {
		i := strings.IndexByte(chunk, '\t')
		if i < 0 {
			break
		}
		if i > 0 {
			b.WriteString(st.Render(chunk[:i]))
		}
		b.WriteByte('\t')
		chunk = chunk[i+1:]
	}
	if chunk != "" {
		b.WriteString(st.Render(chunk))
	}
}

// styleKeyFor maps a span kind to a grouping key. The zero key means "no
// style" (barewords and unstyled whitespace); each non-zero key maps to one
// lipgloss style via styleForKey.
func styleKeyFor(k caddyfile.SpanKind) int {
	switch k {
	case caddyfile.SpanComment:
		return 1
	case caddyfile.SpanString:
		return 2
	case caddyfile.SpanHeredoc:
		return 3
	case caddyfile.SpanPlaceholder:
		return 4
	case caddyfile.SpanOpenBrace, caddyfile.SpanCloseBrace:
		return 5
	default:
		return 0
	}
}

// styleForKey returns the style for a grouping key produced by styleKeyFor.
// The zero key renders verbatim (no ANSI codes). Lexical keys occupy 1-5;
// semantic role keys occupy 6-16; inline advisory findings use 100+ so they
// never collide with the lexical or semantic spaces.
func styleForKey(key int) lipgloss.Style {
	switch key {
	case 1:
		return syntaxCommentStyle
	case 2:
		return syntaxStringStyle
	case 3:
		return syntaxHeredocStyle
	case 4:
		return syntaxPlaceholderStyle
	case 5:
		return syntaxBraceStyle
	case 6:
		return syntaxSiteStyle
	case 7:
		return syntaxDirectiveStyle
	case 8:
		return syntaxDomainStyle
	case 9:
		return syntaxPathStyle
	case 10:
		return syntaxPortStyle
	case 11:
		return syntaxAddressStyle
	case 12:
		return syntaxMatcherDefStyle
	case 13:
		return syntaxMatcherRefStyle
	case 14:
		return syntaxDurationStyle
	case 15:
		return syntaxStatusCodeStyle
	case 16:
		return syntaxHeredocMarkerStyle
	case 100:
		return syntaxInlineHintStyle
	case 101:
		return syntaxInlineInfoStyle
	case 102:
		return syntaxCaddyErrorStyle
	case 103:
		return syntaxCaddyWarningStyle
	default:
		return syntaxWordStyle
	}
}

// diagnosticStyleKeyFor maps a caddy validation severity to a style key in
// the caddy space (102+), or 0 for a severity that is never annotated in
// the source pane. The diagnostics modal filters to errors today, so
// warnings map to their own key for completeness without being emitted.
func diagnosticStyleKeyFor(s validator.Severity) int {
	switch s {
	case validator.SeverityError:
		return 102
	case validator.SeverityWarning:
		return 103
	default:
		return 0
	}
}

// inlineStyleKeyFor maps an advisory finding severity to a style key in the
// inline space (100+), or 0 for an unknown severity (no annotation).
func inlineStyleKeyFor(s caddyfile.InlineSeverity) int {
	switch s {
	case caddyfile.SeverityAdvisoryHint:
		return 100
	case caddyfile.SeverityAdvisoryInfo:
		return 101
	default:
		return 0
	}
}

// semanticRoleKeyFor maps an advisory semantic role to a style key.
// Roles that mirror a lexical kind (string, heredoc, placeholder) reuse the
// lexical keys so there is no redundant styling conflict. Tree-dependent
// and value roles (site address, directive name, domain, path, port,
// IP/CIDR, matchers, duration, status code, heredoc markers) map to their
// own keys. The zero key means "no semantic style": unknown directives and
// unclassified barewords keep their lexical base.
func semanticRoleKeyFor(k caddyfile.Role) int {
	switch k {
	case caddyfile.RoleString:
		return 2
	case caddyfile.RoleHeredoc:
		return 3
	case caddyfile.RolePlaceholder:
		return 4
	case caddyfile.RoleSiteAddress:
		return 6
	case caddyfile.RoleDirectiveName:
		return 7
	case caddyfile.RoleDomain:
		return 8
	case caddyfile.RolePath:
		return 9
	case caddyfile.RolePort:
		return 10
	case caddyfile.RoleIP, caddyfile.RoleCIDR:
		return 11
	case caddyfile.RoleMatcherDefinition:
		return 12
	case caddyfile.RoleMatcherReference:
		return 13
	case caddyfile.RoleDuration:
		return 14
	case caddyfile.RoleStatusCode:
		return 15
	case caddyfile.RoleHeredocMarker:
		return 16
	default:
		return 0
	}
}

// logStyleKeyFor maps a logs.SpanKind to a style key (0 = verbatim).
func logStyleKeyFor(k logs.SpanKind) int {
	switch k {
	case logs.SpanKey:
		return 1
	case logs.SpanString:
		return 2
	case logs.SpanNumber:
		return 3
	case logs.SpanBool:
		return 4
	case logs.SpanNull:
		return 5
	case logs.SpanDelimiter:
		return 6
	case logs.SpanTimestamp:
		return 7
	case logs.SpanMsg:
		return 8
	case logs.SpanLogger:
		return 9
	case logs.SpanLevelDebug:
		return 10
	case logs.SpanLevelInfo:
		return 11
	case logs.SpanLevelWarn:
		return 12
	case logs.SpanLevelError:
		return 13
	case logs.SpanLevelOther:
		return 14
	case logs.SpanStatus1xx:
		return 15
	case logs.SpanStatus2xx:
		return 16
	case logs.SpanStatus3xx:
		return 17
	case logs.SpanStatus4xx:
		return 18
	case logs.SpanStatus5xx:
		return 19
	case logs.SpanStatusOther:
		return 20
	case logs.SpanMethod:
		return 21
	case logs.SpanURI:
		return 22
	default:
		return 0
	}
}

// logStyleForKey returns the style for a log style key produced by
// logStyleKeyFor. The zero key renders verbatim.
func logStyleForKey(key int) lipgloss.Style {
	switch key {
	case 1:
		return logKeyStyle
	case 2:
		return logStringStyle
	case 3:
		return logNumberStyle
	case 4:
		return logBoolStyle
	case 5:
		return logNullStyle
	case 6:
		return logDelimiterStyle
	case 7:
		return logTimestampStyle
	case 8:
		return logMsgStyle
	case 9:
		return logLoggerStyle
	case 10:
		return logLevelDebugStyle
	case 11:
		return logLevelInfoStyle
	case 12:
		return logLevelWarnStyle
	case 13:
		return logLevelErrorStyle
	case 14:
		return logLevelOtherStyle
	case 15:
		return logStatus1xxStyle
	case 16:
		return logStatus2xxStyle
	case 17:
		return logStatus3xxStyle
	case 18:
		return logStatus4xxStyle
	case 19:
		return logStatus5xxStyle
	case 20:
		return logStatusOtherStyle
	case 21:
		return logMethodStyle
	case 22:
		return logURIStyle
	default:
		return syntaxWordStyle
	}
}

// renderLogDetail renders the FULL raw log line (lossless, no truncation)
// as a highlighted, wrapped block suitable for a detail viewport. Long
// lines are wrapped at maxW cells AFTER highlighting, wrapping on plain
// text so no ANSI sequence is split. Non-JSON lines (Parsed false) are
// returned verbatim (wrapped). The returned slice has one string per
// wrapped visual line; concatenating them with "" reproduces the raw line
// byte-for-byte (modulo ANSI styling).
func renderLogDetail(entry logs.Entry, maxW int) []string {
	if len(entry.Raw) == 0 {
		return nil
	}
	// Non-JSON (or, defensively, invalid JSON): wrap the raw line
	// verbatim, lossless.
	if !entry.Parsed || !json.Valid(entry.Raw) {
		out := make([]string, 0, 4)
		for _, r := range wrapBytes(entry.Raw, maxW) {
			out = append(out, string(entry.Raw[r[0]:r[1]]))
		}
		return out
	}
	spans := logs.HighlightJSON(entry.Raw)
	out := make([]string, 0, 4)
	for _, r := range wrapBytes(entry.Raw, maxW) {
		ls, le := r[0], r[1]
		wrapped := string(entry.Raw[ls:le])
		// Emit only the spans intersecting this wrapped line's byte
		// range, translated to the line and clamped.
		var styled []styledSpan
		for _, sp := range spans {
			if sp.End <= ls || sp.Start >= le {
				continue
			}
			s := sp.Start - ls
			if s < 0 {
				s = 0
			}
			e := sp.End - ls
			if e > le-ls {
				e = le - ls
			}
			if s >= e {
				continue
			}
			styled = append(styled, styledSpan{start: s, end: e, key: logStyleKeyFor(sp.Kind)})
		}
		if len(styled) == 0 {
			out = append(out, wrapped)
		} else {
			out = append(out, renderStyledLine(wrapped, styled, logStyleForKey))
		}
	}
	return out
}

// wrapBytes wraps plain text at maxW display cells, returning the byte
// range [start,end) of each wrapped line. Tabs count as one cell. Wide
// runes are not split. A single rune wider than maxW still occupies its
// own line (overflow is acceptable; it cannot be split). Every byte of s
// belongs to exactly one line and ordering is preserved — lossless.
func wrapBytes(s []byte, maxW int) [][2]int {
	if len(s) == 0 {
		return nil
	}
	if maxW < 1 {
		maxW = 1
	}
	var out [][2]int
	lineStart := 0
	lineW := 0
	for off, r := range string(s) {
		rw := lipgloss.Width(string(r))
		if rw < 1 {
			rw = 1
		}
		if lineW > 0 && lineW+rw > maxW {
			out = append(out, [2]int{lineStart, off})
			lineStart = off
			lineW = 0
		}
		lineW += rw
	}
	if lineStart < len(s) {
		out = append(out, [2]int{lineStart, len(s)})
	}
	return out
}
