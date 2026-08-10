package caddyfile

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// TokenKind classifies lexed tokens.
type TokenKind int

const (
	// tokenWord is a bareword: any run of non-whitespace characters that is
	// not an unquoted structural brace. Hashes, curly braces inside
	// placeholders, single quotes and backslashes are all part of the word.
	tokenWord TokenKind = iota
	// tokenQuoted is a double-quoted or backtick-quoted string. It may span
	// multiple lines.
	tokenQuoted
	// tokenHeredoc is a <<MARKER ... MARKER heredoc.
	tokenHeredoc
	// tokenOpenBrace is an unquoted token that is exactly "{".
	tokenOpenBrace
	// tokenCloseBrace is an unquoted token that is exactly "}".
	tokenCloseBrace
)

// Token is one lexed unit with exact byte offsets into the source document.
type Token struct {
	Kind TokenKind
	// Text is the token value: the bareword itself, the quoted content
	// (without the enclosing quotes and with \" resolved), or the heredoc
	// content (without the opening marker line and the closing marker).
	Text string
	// Quote is the enclosing quote: 0 for barewords, '"' or '`' for quoted
	// strings, and '<' for heredocs.
	Quote rune
	// Start and End are the byte offsets of the token in the source,
	// covering the full raw text, quotes and heredoc markers included.
	Start, End int
	// Line is the 1-based logical line where the token starts. Escaped
	// newlines do not advance the logical line: tokens chained by a
	// trailing backslash keep the same Line so they group together.
	Line int
	// Column is the 1-based character column of the token's first byte on
	// its physical line. A byte order mark, leading whitespace and the
	// current horizontal scroll are not counted; tabs count as one
	// character and multi-byte UTF-8 runes count as one character, so
	// Column matches the character position a text editor reports rather
	// than a terminal cell width.
	Column int
	// Doc is the identity of the source document the token was lexed from,
	// or "" when no identity was provided. Lexing always covers exactly one
	// document, so the identity is usually implied by context; callers that
	// need it on the token (for example multi-document edit planning) set
	// it through lexDoc.
	Doc string
}

var heredocMarkerRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// lex tokenizes src into tokens, mirroring the behavior of Caddy's Caddyfile
// lexer: comments start only at the beginning of a token (after whitespace or
// at the start of a line), hashes inside tokens stay in the token, quoted
// strings and heredocs may span lines, and a trailing backslash escapes the
// newline so the following token keeps the same logical line. The original
// bytes are never modified; every token carries its exact offsets. Tokens
// lexed through lex carry no document identity.
func lex(src []byte) ([]Token, error) {
	return lexDoc(src, "")
}

// lexDoc is lex with an explicit source document identity recorded on every
// token. The identity is advisory: byte offsets, lines and columns are
// always relative to the single document being lexed.
func lexDoc(src []byte, docID string) ([]Token, error) {
	var tokens []Token
	i := 0
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		i = 3 // skip byte order mark without shifting offsets
	}
	// line is the logical line (escaped newlines do not advance it); col is
	// the rune count since the last newline on the current physical line.
	line, col, skipped := 1, 0, 0
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\r':
			i++
			col++
		case '\n':
			line += 1 + skipped
			skipped = 0
			col = 0
			i++
		case '#':
			// A comment runs to the end of its line. It can only start here
			// because a `#` in the middle of a token is part of the token.
			for i < len(src) && src[i] != '\n' {
				i++
			}
		default:
			tok, err := lexToken(src, &i, &line, &skipped, &col, docID)
			if err != nil {
				return tokens, err
			}
			tokens = append(tokens, tok)
		}
	}
	return tokens, nil
}

// advanceCol consumes one byte at *i and advances the physical column by one
// character when the byte starts a UTF-8 rune (continuation bytes of a
// multi-byte rune count as part of the same character).
func advanceCol(src []byte, i, col *int) {
	if src[*i]&0xC0 != 0x80 {
		*col++
	}
	*i++
}

// lexToken lexes one token starting at *i.
func lexToken(src []byte, i *int, line, skipped, col *int, docID string) (Token, error) {
	start, startLine, startCol := *i, *line, *col
	if src[*i] == '"' || src[*i] == '`' {
		return lexQuoted(src, i, line, skipped, col, src[*i], start, startLine, startCol, docID)
	}

	var val []byte
	var heredocEscaped bool
	for *i < len(src) {
		ch := src[*i]
		switch {
		case len(val) > 1 && val[0] == '<' && val[1] == '<' && !heredocEscaped:
			// Candidate heredoc opener: the marker must end at a newline.
			if ch == ' ' {
				goto done // "<<" followed by a space is a regular token
			}
			if ch == '\n' {
				if len(val) == 2 {
					return Token{}, fmt.Errorf("missing opening heredoc marker on line %d; must contain only alphanumeric characters, dashes and underscores; got empty string", startLine)
				}
				if val[2] == '<' {
					return Token{}, fmt.Errorf("too many '<' for heredoc on line %d; only use two, for example <<END", startLine)
				}
				marker := string(val[2:])
				if !heredocMarkerRe.MatchString(marker) {
					return Token{}, fmt.Errorf("heredoc marker on line %d must contain only alphanumeric characters, dashes and underscores; got '%s'", startLine, marker)
				}
				*i++ // consume the newline
				*skipped++
				*col = 0 // the heredoc body starts on a new physical line
				return lexHeredoc(src, i, line, skipped, col, marker, start, startLine, startCol, docID)
			}
			val = append(val, ch)
			advanceCol(src, i, col)
		case ch == '\n' || ch == '\r' || ch == ' ' || ch == '\t':
			goto done
		case ch == '\\':
			if *i+1 < len(src) && (src[*i+1] == '\n' || (src[*i+1] == '\r' && *i+2 < len(src) && src[*i+2] == '\n')) {
				// An escaped newline ends the token without advancing the
				// logical line, so the next token continues on this line.
				if src[*i+1] == '\n' {
					*i += 2
				} else {
					*i += 3
				}
				*skipped++
				*col = 0 // the continuation starts on a new physical line
				if len(val) > 0 {
					goto done
				}
				continue
			}
			if *i+1 < len(src) && src[*i+1] == '<' {
				heredocEscaped = true // \<< prevents heredoc parsing
				*i++
				*col++
				continue
			}
			val = append(val, ch)
			*i++
			*col++
		default:
			val = append(val, ch)
			advanceCol(src, i, col)
		}
	}

done:
	switch string(val) {
	case "{":
		return Token{Kind: tokenOpenBrace, Text: "{", Start: start, End: *i, Line: startLine, Column: startCol + 1, Doc: docID}, nil
	case "}":
		return Token{Kind: tokenCloseBrace, Text: "}", Start: start, End: *i, Line: startLine, Column: startCol + 1, Doc: docID}, nil
	}
	return Token{Kind: tokenWord, Text: string(val), Start: start, End: *i, Line: startLine, Column: startCol + 1, Doc: docID}, nil
}

// lexQuoted lexes a double-quoted or backtick-quoted string, which may span
// multiple lines. Inside double quotes, \" is an escaped quote and any other
// backslash is kept literally; inside backticks nothing is escaped.
func lexQuoted(src []byte, i *int, line, skipped, col *int, quote byte, start, startLine, startCol int, docID string) (Token, error) {
	*i++   // opening quote
	*col++ // the quote occupies one character column
	var val []byte
	escaped := false
	for *i < len(src) {
		ch := src[*i]
		*i++
		if ch == '\n' {
			*line += 1 + *skipped
			*skipped = 0
			*col = 0
		} else if ch&0xC0 != 0x80 {
			*col++
		}
		if escaped {
			if ch != quote {
				val = append(val, '\\')
			}
			escaped = false
			val = append(val, ch)
			continue
		}
		if quote == '"' && ch == '\\' {
			escaped = true
			continue
		}
		if ch == quote {
			return Token{Kind: tokenQuoted, Text: string(val), Quote: rune(quote), Start: start, End: *i, Line: startLine, Column: startCol + 1, Doc: docID}, nil
		}
		val = append(val, ch)
	}
	return Token{}, fmt.Errorf("unterminated %s string starting on line %d", quoteName(quote), startLine)
}

// lexHeredoc reads the body of a heredoc after its <<MARKER opener line has
// been consumed. It ends at the closing marker, which may be indented.
func lexHeredoc(src []byte, i *int, line, skipped, col *int, marker string, start, startLine, startCol int, docID string) (Token, error) {
	var content []byte
	for {
		if *i >= len(src) {
			return Token{}, fmt.Errorf("incomplete heredoc <<%s starting on line %d; expected ending marker %s", marker, startLine, marker)
		}
		ch := src[*i]
		*i++
		if ch == '\n' {
			*skipped++
			*col = 0
		} else if ch&0xC0 != 0x80 {
			*col++
		}
		content = append(content, ch)
		if len(content) >= len(marker) && bytes.Equal(content[len(content)-len(marker):], []byte(marker)) {
			content = content[:len(content)-len(marker)]
			break
		}
	}
	text, err := finalizeHeredoc(content, marker, startLine)
	if err != nil {
		return Token{}, err
	}
	*line += *skipped
	*skipped = 0
	return Token{Kind: tokenHeredoc, Text: text, Quote: '<', Start: start, End: *i, Line: startLine, Column: startCol + 1, Doc: docID}, nil
}

// finalizeHeredoc strips the closing marker's indentation from every content
// line and removes the trailing newline, mirroring Caddy's heredoc handling.
// An extra blank line before the closing marker retains the trailing newline.
func finalizeHeredoc(content []byte, marker string, startLine int) (string, error) {
	s := string(content)
	lastNewline := strings.LastIndex(s, "\n")
	lines := strings.Split(s[:lastNewline+1], "\n")
	padding := s[lastNewline+1:]

	var out strings.Builder
	for _, line := range lines[:len(lines)-1] {
		if line == "" || line == "\r" {
			out.WriteByte('\n')
			continue
		}
		if !strings.HasPrefix(line, padding) {
			return "", fmt.Errorf("mismatched leading whitespace in heredoc <<%s on line %d, expected whitespace to match the closing marker", marker, startLine)
		}
		out.WriteString(strings.ReplaceAll(line[len(padding):], "\r", ""))
		out.WriteByte('\n')
	}
	res := out.String()
	if len(res) > 0 && res[len(res)-1] == '\n' {
		res = res[:len(res)-1]
	}
	return res, nil
}

func quoteName(q byte) string {
	if q == '"' {
		return "double-quoted"
	}
	return "backtick"
}

// numLineBreaks counts how many source lines a token spans beyond its start,
// mirroring Caddy's Token.NumLineBreaks: heredocs add two for the opening
// delimiter line and the removed trailing newline.
func numLineBreaks(t Token) int {
	n := strings.Count(t.Text, "\n")
	if t.Quote == '<' {
		n += 2
	}
	return n
}

// groupLines groups tokens into logical lines: a token starts a new group
// when it begins on a line after the previous token ended.
func groupLines(tokens []Token) [][]Token {
	var groups [][]Token
	var cur []Token
	for _, t := range tokens {
		if len(cur) > 0 && cur[len(cur)-1].Line+numLineBreaks(cur[len(cur)-1]) < t.Line {
			groups = append(groups, cur)
			cur = nil
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups
}
