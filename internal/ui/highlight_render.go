package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// highlightSource renders src with line numbers and syntax highlighting.
// It is the highlighted successor of numberedSource and keeps the same
// signature contract. Returns the dim "(empty source ...)" message for an
// empty src, exactly like numberedSource.
func highlightSource(src []byte) string {
	if len(src) == 0 {
		return dimStyle.Render("(empty source — raw view still available)")
	}
	lineSpans := caddyfile.Highlight(src)
	lines := strings.Split(string(src), "\n")
	var b strings.Builder
	base := 0
	for i, ln := range lines {
		fmt.Fprintf(&b, "%4d│ ", i+1)
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

// renderHighlightedLine styles one source line. It maps the line's bytes to
// display cells (tabs and wide runes included) so spans apply cell-accurately,
// then emits the line as consecutive styled chunks. Every byte of ln is
// emitted exactly once; if the cell mapping ever misaligns, chunk boundaries
// fall back to rune/byte boundaries so the rendered view stays lossless.
func renderHighlightedLine(ln string, base int, spans []caddyfile.Span) string {
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
		key := styleKeyFor(sp.Kind)
		cs := sort.Search(len(cellBytes), func(i int) bool { return cellBytes[i] >= s })
		ce := sort.Search(len(cellBytes), func(i int) bool { return cellBytes[i] >= e })
		for i := cs; i < ce; i++ {
			cells[i] = key
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
			writeStyledChunk(&b, styleForKey(key), chunk)
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
