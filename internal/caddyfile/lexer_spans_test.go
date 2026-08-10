package caddyfile

import (
	"testing"
)

// columnOf returns the 1-based Column of the first token whose Text matches,
// and -1 when no such token exists.
func columnOf(t *testing.T, src, text string) int {
	t.Helper()
	toks := lexString(t, src)
	for _, tok := range toks {
		if tok.Text == text {
			return tok.Column
		}
	}
	t.Fatalf("no token %q in %+v", text, toks)
	return -1
}

func TestTokenColumnBasic(t *testing.T) {
	src := "host:123 {\n\tdirective arg\n}"
	toks := lexString(t, src)
	want := []struct {
		text   string
		line   int
		column int
	}{
		{"host:123", 1, 1},
		{"{", 1, 10},
		{"directive", 2, 2},
		{"arg", 2, 12},
		{"}", 3, 1},
	}
	if len(toks) != len(want) {
		t.Fatalf("tokens = %+v, want %d tokens", toks, len(want))
	}
	for i, w := range want {
		if toks[i].Text != w.text || toks[i].Line != w.line || toks[i].Column != w.column {
			t.Errorf("token[%d] = %+v, want text %q line %d column %d", i, toks[i], w.text, w.line, w.column)
		}
	}
}

func TestTokenColumnUTF8(t *testing.T) {
	// "café" is six characters; é is two bytes. The trailing token must be
	// positioned by characters, not bytes.
	src := "directive \"café\" x\n"
	toks := lexString(t, src)
	if len(toks) != 3 {
		t.Fatalf("tokens = %+v, want 3", toks)
	}
	// "café" spans columns 11-16; the separating space is 17; x is 18.
	if toks[0].Column != 1 || toks[1].Column != 11 || toks[2].Column != 18 {
		t.Errorf("columns = %d %d %d, want 1 11 18", toks[0].Column, toks[1].Column, toks[2].Column)
	}
	// A multi-byte rune in a bareword keeps later columns character-based.
	if got := columnOf(t, "a é b", "b"); got != 5 {
		t.Errorf("column of b after 'é' = %d, want 5", got)
	}
}

func TestTokenColumnCRLF(t *testing.T) {
	src := "a b\r\n\tc\r\n"
	toks := lexString(t, src)
	if len(toks) != 3 {
		t.Fatalf("tokens = %+v, want 3", toks)
	}
	if toks[0].Line != 1 || toks[0].Column != 1 {
		t.Errorf("token[0] = line %d column %d, want 1 1", toks[0].Line, toks[0].Column)
	}
	if toks[1].Line != 1 || toks[1].Column != 3 {
		t.Errorf("token[1] = line %d column %d, want 1 3", toks[1].Line, toks[1].Column)
	}
	if toks[2].Line != 2 || toks[2].Column != 2 {
		t.Errorf("token[2] = line %d column %d, want 2 2", toks[2].Line, toks[2].Column)
	}
}

func TestTokenColumnBOM(t *testing.T) {
	// The byte order mark is invisible: the first token sits at column 1
	// even though its byte offset is 3.
	src := "\xEF\xBB\xBF:8080 {\n\trespond ok\n}\n"
	toks := lexString(t, src)
	if len(toks) != 5 {
		t.Fatalf("tokens = %+v, want 5", toks)
	}
	if toks[0].Column != 1 || toks[0].Start != 3 {
		t.Errorf("first token column = %d start = %d, want column 1 start 3", toks[0].Column, toks[0].Start)
	}
	if toks[2].Column != 2 || toks[2].Line != 2 {
		t.Errorf("respond token = line %d column %d, want line 2 column 2", toks[2].Line, toks[2].Column)
	}
}

func TestTokenColumnMultilineString(t *testing.T) {
	src := "a \"multi\nline\" b\n"
	toks := lexString(t, src)
	if len(toks) != 3 {
		t.Fatalf("tokens = %+v, want 3", toks)
	}
	// The quoted token starts on line 1, column 3; the token after it
	// continues on the string's last physical line (line 2: `line" b`).
	if toks[1].Line != 1 || toks[1].Column != 3 {
		t.Errorf("quoted token = line %d column %d, want 1 3", toks[1].Line, toks[1].Column)
	}
	if toks[2].Line != 2 || toks[2].Column != 7 {
		t.Errorf("token after multiline string = line %d column %d, want 2 7", toks[2].Line, toks[2].Column)
	}
}

func TestTokenColumnHeredoc(t *testing.T) {
	src := "heredoc <<EOF\ncontent\nEOF tail\n"
	toks := lexString(t, src)
	var heredoc *Token
	for i := range toks {
		if toks[i].Kind == tokenHeredoc {
			heredoc = &toks[i]
			break
		}
	}
	if heredoc == nil {
		t.Fatalf("no heredoc token in %+v", toks)
	}
	// "heredoc " is 8 characters, so <<EOF starts at column 9.
	if heredoc.Column != 9 {
		t.Errorf("heredoc column = %d, want 9", heredoc.Column)
	}
	// "tail" follows the closing marker on the same physical line.
	if tail := toks[len(toks)-1]; tail.Text != "tail" || tail.Line != 3 || tail.Column != 5 {
		t.Errorf("token after heredoc = %+v, want tail on line 3 column 5", tail)
	}
}

func TestTokenColumnEscapedNewline(t *testing.T) {
	src := "line1\\\nescaped1\\\nescaped2\nline4"
	toks := lexString(t, src)
	// Escaped newlines keep the logical line but restart the physical
	// column: every continuation starts at column 1.
	want := []struct {
		text   string
		line   int
		column int
	}{
		{"line1", 1, 1},
		{"escaped1", 1, 1},
		{"escaped2", 1, 1},
		{"line4", 4, 1},
	}
	if len(toks) != len(want) {
		t.Fatalf("tokens = %+v, want %d", toks, len(want))
	}
	for i, w := range want {
		if toks[i].Text != w.text || toks[i].Line != w.line || toks[i].Column != w.column {
			t.Errorf("token[%d] = %+v, want text %q line %d column %d", i, toks[i], w.text, w.line, w.column)
		}
	}
}

func TestTokenColumnQuotedBraces(t *testing.T) {
	// Quoted braces are ordinary string tokens with ordinary columns.
	src := "dir \"{\" x\n"
	toks := lexString(t, src)
	if len(toks) != 3 {
		t.Fatalf("tokens = %+v, want 3", toks)
	}
	if toks[0].Kind == tokenOpenBrace || toks[1].Kind == tokenOpenBrace {
		t.Errorf("quoted braces must stay string tokens: %+v", toks)
	}
	if toks[0].Column != 1 || toks[1].Column != 5 || toks[2].Column != 9 {
		t.Errorf("columns = %d %d %d, want 1 5 9", toks[0].Column, toks[1].Column, toks[2].Column)
	}
}

func TestTokenColumnsAllPositive(t *testing.T) {
	// Every token in a mixed corpus reports a sane, 1-based column and a
	// byte range inside the source.
	srcs := []string{
		"host:123 {\n\tdirective arg\n}\n",
		"\"multi\nline\" string\n",
		"heredoc <<EOF\nbody\nEOF tail\n",
		"a \\\n b\n",
		"x # comment only\n",
		"directive {args[0]} {\n\tsub\n}\n",
		"\xEF\xBB\xBFa:80 {\n}\n",
		"\t\tdeep\tindent\n",
	}
	for _, src := range srcs {
		toks := lexString(t, src)
		if len(toks) == 0 {
			t.Fatalf("no tokens for %q", src)
		}
		for _, tok := range toks {
			if tok.Column < 1 {
				t.Errorf("token %+v in %q has column %d, want >= 1", tok, src, tok.Column)
			}
			if tok.Start > tok.End || tok.End > len(src) {
				t.Errorf("token %+v in %q has invalid range", tok, src)
			}
		}
	}
}

func TestTokenDocIdentity(t *testing.T) {
	src := []byte("example.test {\n\trespond ok\n}\n")
	// lex carries no identity.
	toks, err := lex(src)
	if err != nil {
		t.Fatalf("lex: %v", err)
	}
	for _, tok := range toks {
		if tok.Doc != "" {
			t.Errorf("lex token %+v has Doc %q, want empty", tok, tok.Doc)
		}
	}
	// lexDoc records the identity on every token.
	docToks, err := lexDoc(src, "sites/extra.caddy")
	if err != nil {
		t.Fatalf("lexDoc: %v", err)
	}
	if len(docToks) != len(toks) {
		t.Fatalf("lexDoc produced %d tokens, want %d", len(docToks), len(toks))
	}
	for i, tok := range docToks {
		if tok.Doc != "sites/extra.caddy" {
			t.Errorf("token[%d] has Doc %q, want sites/extra.caddy", i, tok.Doc)
		}
		if tok.Start != toks[i].Start || tok.End != toks[i].End || tok.Line != toks[i].Line {
			t.Errorf("token[%d] spans/line differ from lex: %+v vs %+v", i, tok, toks[i])
		}
	}
}

// TestTokenColumnsSurviveGrouping pins that column tracking does not disturb
// the logical-line grouping used by the parser.
func TestTokenColumnsSurviveGrouping(t *testing.T) {
	src := "a b\nc \"multi\nline\" d\ne"
	toks := lexString(t, src)
	groups := groupLines(toks)
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3 (%+v)", len(groups), groups)
	}
	// The multi-line quoted token is still reported on its start line.
	quoted := groups[1][1]
	if quoted.Text != "multi\nline" || quoted.Line != 2 || quoted.Column != 3 {
		t.Errorf("quoted token = %+v, want text %q on line 2 column 3", quoted, "multi\nline")
	}
}

// TestTokenColumnsInCommentOnlyInput guards the comment path: comments are
// not tokens, so a comment-only file yields no tokens and no column math.
func TestTokenColumnsInCommentOnlyInput(t *testing.T) {
	toks := lexString(t, "# only a comment\n")
	if len(toks) != 0 {
		t.Fatalf("tokens = %+v, want none", toks)
	}
}
