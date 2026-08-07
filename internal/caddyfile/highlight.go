package caddyfile

// SpanKind classifies a highlighted span.
type SpanKind int

const (
	// SpanWord is the fallback kind: any bareword token.
	SpanWord SpanKind = iota
	// SpanString is a double-quoted or backtick-quoted token, possibly
	// multi-line.
	SpanString
	// SpanHeredoc is a heredoc body token (including the <<MARKER opener).
	SpanHeredoc
	// SpanComment is a "# ..." comment; the lexer drops comments, so
	// Highlight re-derives them.
	SpanComment
	// SpanOpenBrace is an unquoted "{" token.
	SpanOpenBrace
	// SpanCloseBrace is an unquoted "}" token.
	SpanCloseBrace
	// SpanPlaceholder is a "{...}" sub-span inside a SpanWord or SpanString.
	SpanPlaceholder
)

func (k SpanKind) String() string {
	switch k {
	case SpanWord:
		return "word"
	case SpanString:
		return "string"
	case SpanHeredoc:
		return "heredoc"
	case SpanComment:
		return "comment"
	case SpanOpenBrace:
		return "{"
	case SpanCloseBrace:
		return "}"
	case SpanPlaceholder:
		return "placeholder"
	default:
		return "unknown"
	}
}

// Span is one highlighted range at byte offsets, clipped to a single logical
// line.
type Span struct {
	Kind       SpanKind
	Start, End int // byte offsets into src
}

// Highlight returns semantic spans grouped by logical line. len(result) equals
// len(strings.Split(string(src), "\n")) — one entry per line, empty slice for
// blank lines. Returns nil for an empty src. Multi-line tokens (quoted strings,
// heredocs) are reported on every line they cover, clipped to that line's byte
// range. Placeholder spans overlap (are nested inside) their parent word/string
// span and include the braces.
func Highlight(src []byte) [][]Span {
	if len(src) == 0 {
		return nil
	}
	// Physical line starts: 0 and the offset just after every '\n'. This
	// guarantees result[i] corresponds to strings.Split(string(src), "\n")[i].
	starts := lineStarts(src)
	n := len(starts)
	srcLen := len(src)

	tokens, _ := lex(src)

	lineIdx := func(off int) int {
		lo, hi := 0, n-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if starts[mid] <= off {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo
	}

	// First pass: clip every lexed token to each physical line it covers.
	tokenSpans := make([][]Span, n)
	for _, tok := range tokens {
		kind := spanKindFor(tok.Kind)
		for li := lineIdx(tok.Start); li < n; li++ {
			lineEnd := contentEnd(starts, li, srcLen)
			s, e := tok.Start, tok.End
			if s < starts[li] {
				s = starts[li]
			}
			if e > lineEnd {
				e = lineEnd
			}
			if s < e {
				tokenSpans[li] = append(tokenSpans[li], Span{Kind: kind, Start: s, End: e})
			}
			if tok.End <= lineEnd {
				break
			}
		}
	}

	// Second pass: walk each line left to right, emitting token spans (with
	// their nested placeholders) at covered positions and re-deriving
	// comments at uncovered '#' positions. Any byte inside a token span —
	// including '#' inside a quoted string or heredoc — is never inspected.
	result := make([][]Span, n)
	for li := 0; li < n; li++ {
		lineEnd := contentEnd(starts, li, srcLen)
		covered := tokenSpans[li]
		ci := 0
		var spans []Span
		for pos := starts[li]; pos < lineEnd; {
			if ci < len(covered) && pos >= covered[ci].Start {
				spans = append(spans, covered[ci])
				spans = append(spans, placeholdersIn(src, covered[ci])...)
				pos = covered[ci].End
				ci++
				continue
			}
			switch src[pos] {
			case '#':
				spans = append(spans, Span{Kind: SpanComment, Start: pos, End: lineEnd})
				pos = lineEnd
			case ' ', '\t', '\r':
				pos++
			default:
				// Skip forward to the next token start, whitespace or '#'.
				next := pos + 1
				for next < lineEnd {
					c := src[next]
					if c == '#' || c == ' ' || c == '\t' || c == '\r' {
						break
					}
					if ci < len(covered) && next == covered[ci].Start {
						break
					}
					next++
				}
				pos = next
			}
		}
		result[li] = spans
	}
	return result
}

// lineStarts returns the byte offset of each physical line's first byte: 0
// and the offset just after every '\n'.
func lineStarts(src []byte) []int {
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// contentEnd returns the byte offset one past the last content byte of line
// li, excluding the trailing '\n'. The '\r' of a CRLF pair is part of the
// line's content and is included.
func contentEnd(starts []int, li, srcLen int) int {
	if li+1 < len(starts) {
		return starts[li+1] - 1
	}
	return srcLen
}

// spanKindFor maps a lexer token kind to its SpanKind.
func spanKindFor(k TokenKind) SpanKind {
	switch k {
	case tokenOpenBrace:
		return SpanOpenBrace
	case tokenCloseBrace:
		return SpanCloseBrace
	case tokenQuoted:
		return SpanString
	case tokenHeredoc:
		return SpanHeredoc
	default:
		return SpanWord
	}
}

// placeholdersIn extracts "{...}" sub-spans (including the braces) from a
// clipped word or string span. No nesting is supported: scanning resumes
// after the first '}' following a '{'. A '}' without a preceding '{' is left
// alone. Heredoc spans are never scanned.
func placeholdersIn(src []byte, sp Span) []Span {
	if sp.Kind != SpanWord && sp.Kind != SpanString {
		return nil
	}
	var out []Span
	for i := sp.Start; i < sp.End; {
		if src[i] != '{' {
			i++
			continue
		}
		j := i + 1
		for j < sp.End && src[j] != '}' {
			j++
		}
		if j < sp.End {
			out = append(out, Span{Kind: SpanPlaceholder, Start: i, End: j + 1})
			i = j + 1
			continue
		}
		i++
	}
	return out
}
