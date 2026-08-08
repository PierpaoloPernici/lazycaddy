package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/logs"
)

// highlightSource renders src with line numbers and syntax highlighting.
// The 1-based inclusive range [selStartLine, selEndLine] marks the selected
// section: its line numbers are emphasized and a subtle vertical bar is
// drawn in the gutter for those lines. Passing 0 for both bounds renders
// the gutter plainly. Returns the dim "(empty source ...)" message for an
// empty src, exactly like numberedSource.
func highlightSource(src []byte, selStartLine, selEndLine int) string {
	if len(src) == 0 {
		return dimStyle.Render("(empty source — raw view still available)")
	}
	lineSpans := caddyfile.Highlight(src)
	lines := strings.Split(string(src), "\n")
	var b strings.Builder
	base := 0
	for i, ln := range lines {
		lineNo := i + 1
		if selStartLine > 0 && lineNo >= selStartLine && lineNo <= selEndLine {
			b.WriteString(selectedGutterNumberStyle.Render(fmt.Sprintf("%4d", lineNo)))
			b.WriteString(selectedGutterBarStyle.Render("▎"))
			b.WriteByte(' ')
		} else {
			fmt.Fprintf(&b, "%4d│ ", lineNo)
		}
		if i < len(lineSpans) {
			b.WriteString(renderHighlightedLine(ln, base, lineSpans[i]))
		} else {
			b.WriteString(ln)
		}
		b.WriteByte('\n')
		base += len(ln) + 1
	}
	return b.String()
}

// renderHighlightedLine styles one source line. It converts the
// base-relative caddyfile spans into line-relative styledSpans (with the
// same clamping as before) and delegates to renderStyledLine, so the
// cell-accurate machinery is shared with the log renderer.
func renderHighlightedLine(ln string, base int, spans []caddyfile.Span) string {
	styled := make([]styledSpan, 0, len(spans))
	for _, sp := range spans {
		s := sp.Start - base
		e := sp.End - base
		if s < 0 {
			s = 0
		}
		if e > len(ln) {
			e = len(ln)
		}
		if s >= e {
			continue
		}
		styled = append(styled, styledSpan{start: s, end: e, key: styleKeyFor(sp.Kind)})
	}
	return renderStyledLine(ln, styled, styleForKey)
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
// The zero key renders verbatim (no ANSI codes).
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
	default:
		return syntaxWordStyle
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
