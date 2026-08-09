package caddyfile

import (
	"strings"
	"testing"
)

func lexString(t *testing.T, src string) []Token {
	t.Helper()
	toks, err := lex([]byte(src))
	if err != nil {
		t.Fatalf("lex(%q): %v", src, err)
	}
	return toks
}

func wantLexErr(t *testing.T, src, wantSubstr string) {
	t.Helper()
	if _, err := lex([]byte(src)); err == nil {
		t.Fatalf("lex(%q): expected error containing %q, got nil", src, wantSubstr)
	} else if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("lex(%q): error = %q, want it to contain %q", src, err, wantSubstr)
	}
}

func TestLexBasicTokens(t *testing.T) {
	toks := lexString(t, "host:123 {\n\tdirective arg\n}")
	want := []struct {
		text string
		kind TokenKind
		line int
	}{
		{"host:123", tokenWord, 1},
		{"{", tokenOpenBrace, 1},
		{"directive", tokenWord, 2},
		{"arg", tokenWord, 2},
		{"}", tokenCloseBrace, 3},
	}
	if len(toks) != len(want) {
		t.Fatalf("tokens = %v, want %d tokens", toks, len(want))
	}
	for i, w := range want {
		if toks[i].Text != w.text || toks[i].Kind != w.kind || toks[i].Line != w.line {
			t.Errorf("token[%d] = %+v, want text %q kind %d line %d", i, toks[i], w.text, w.kind, w.line)
		}
	}
}

func TestLexHashIsCommentOnlyAtTokenStart(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"full line comment", "# comment\nreverse_proxy localhost:8080", []string{"reverse_proxy", "localhost:8080"}},
		{"trailing comment", "directive arg # trailing", []string{"directive", "arg"}},
		{"hash inside url", "redir /some/#/path", []string{"redir", "/some/#/path"}},
		{"hash inside word", "directive /path#fragment", []string{"directive", "/path#fragment"}},
		{"hash after hash token", "directive #comment", []string{"directive"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks := lexString(t, tt.src)
			got := make([]string, len(toks))
			for i, tok := range toks {
				got[i] = tok.Text
			}
			if len(got) != len(tt.want) {
				t.Fatalf("texts = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("texts = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestLexQuotedStrings(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"simple", `a "quoted value" b`, []string{"a", "quoted value", "b"}},
		{"escaped quotes", `A "quoted \"value\" inside" B`, []string{"A", `quoted "value" inside`, "B"}},
		{"unescapable backslash", `"unescapable\ in quotes"`, []string{`unescapable\ in quotes`}},
		{"escaped backslash", `"don't\\escape"`, []string{`don't\\escape`}},
		{"windows path", `"C:\php\php-cgi.exe"`, []string{`C:\php\php-cgi.exe`}},
		{"empty string", `empty "" string`, []string{"empty", "", "string"}},
		{"backtick raw", "simple `backtick quoted` string", []string{"simple", "backtick quoted", "string"}},
		{"backtick with braces", "`{\"foo\": \"bar\"}`", []string{`{"foo": "bar"}`}},
		{"nested quotes in backticks", "nested `\"quotes inside\" backticks` string", []string{"nested", `"quotes inside" backticks`, "string"}},
		{"multiline double", "A \"first line\n\tsecond line\" {", []string{"A", "first line\n\tsecond line", "{"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks := lexString(t, tt.src)
			got := make([]string, len(toks))
			for i, tok := range toks {
				got[i] = tok.Text
			}
			if len(got) != len(tt.want) {
				t.Fatalf("texts = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("texts = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestLexQuotedBraceIsNotStructural(t *testing.T) {
	toks := lexString(t, `dir1 "{" a b "}"`)
	if toks[1].Kind == tokenOpenBrace || toks[4].Kind == tokenCloseBrace {
		t.Errorf("quoted braces must be literal tokens: %+v", toks)
	}
	if toks[1].Text != "{" || toks[4].Text != "}" {
		t.Errorf("quoted brace texts = %q %q, want { and }", toks[1].Text, toks[4].Text)
	}
}

func TestLexHeredoc(t *testing.T) {
	tests := []struct {
		name        string
		src         string
		wantHeredoc bool
		wantText    string
		wantAfter   []string // token texts after the heredoc token, or all tokens when no heredoc
		wantErrSub  string
	}{
		{
			name:        "basic with trailing arg",
			src:         "heredoc <<EOF\ncontent\nEOF same-line-arg\n",
			wantHeredoc: true,
			wantText:    "content",
			wantAfter:   []string{"same-line-arg"},
		},
		{
			name:        "extra blank line keeps trailing newline",
			src:         "heredoc <<EOF\nextra-newline\n\nEOF same-line-arg\n",
			wantHeredoc: true,
			wantText:    "extra-newline\n",
			wantAfter:   []string{"same-line-arg"},
		},
		{
			name:        "empty heredoc",
			src:         "heredoc <<EOF\nEOF\n\tHERE same-line-arg\n",
			wantHeredoc: true,
			wantText:    "",
			wantAfter:   []string{"HERE", "same-line-arg"},
		},
		{
			name:        "indented marker strips indentation",
			src:         "heredoc <<EOF\n\t\tmulti\n\t\tline\n\tEOF same-line-arg\n",
			wantHeredoc: true,
			wantText:    "\tmulti\n\tline",
			wantAfter:   []string{"same-line-arg"},
		},
		{
			name:        "single char marker",
			src:         "heredoc <<s\ncontent\ns same-line-arg\n",
			wantHeredoc: true,
			wantText:    "content",
			wantAfter:   []string{"same-line-arg"},
		},
		{
			name:      "escaped opener is a word",
			src:       `escaped-heredoc \<< >>`,
			wantAfter: []string{"escaped-heredoc", "<<", ">>"},
		},
		{
			name:      "<< with space is a word",
			src:       "not-a-heredoc << >>",
			wantAfter: []string{"not-a-heredoc", "<<", ">>"},
		},
		{
			name:      "single < is a word",
			src:       "not-a-heredoc <EOF\ncontent\n",
			wantAfter: []string{"not-a-heredoc", "<EOF", "content"},
		},
		{
			name:      "<<< on one line is a word",
			src:       "not-a-heredoc <<<EOF content",
			wantAfter: []string{"not-a-heredoc", "<<<EOF", "content"},
		},
		{
			name:      "<<HERE with space is a word",
			src:       "not-a-heredoc <<HERE SAME LINE\n",
			wantAfter: []string{"not-a-heredoc", "<<HERE", "SAME", "LINE"},
		},
		{
			name:       "missing marker",
			src:        "not-a-heredoc <<\n",
			wantErrSub: "missing opening heredoc marker",
		},
		{
			name:       "too many less-than",
			src:        "heredoc <<<EOF\n\tcontent\n\tEOF\n",
			wantErrSub: "too many '<'",
		},
		{
			name:       "incomplete heredoc",
			src:        "heredoc <<EOF\n\tcontent\n",
			wantErrSub: "incomplete heredoc <<EOF",
		},
		{
			name:       "mismatched indentation",
			src:        "heredoc <<EOF\n\tcontent\n\t\tEOF\n",
			wantErrSub: "mismatched leading whitespace",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, err := lex([]byte(tt.src))
			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got tokens %+v", tt.wantErrSub, toks)
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("error = %q, want it to contain %q", err, tt.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("lex: %v", err)
			}

			var texts []string
			for _, tok := range toks {
				texts = append(texts, tok.Text)
			}
			if !tt.wantHeredoc {
				if len(texts) != len(tt.wantAfter) {
					t.Fatalf("texts = %v, want %v", texts, tt.wantAfter)
				}
				for i := range texts {
					if texts[i] != tt.wantAfter[i] {
						t.Errorf("texts = %v, want %v", texts, tt.wantAfter)
					}
				}
				return
			}

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
			if heredoc.Text != tt.wantText {
				t.Errorf("heredoc text = %q, want %q", heredoc.Text, tt.wantText)
			}
			if heredoc.Quote != '<' {
				t.Errorf("heredoc quote = %q, want '<'", heredoc.Quote)
			}
			if heredoc.Start >= heredoc.End || heredoc.End > len(tt.src) {
				t.Errorf("heredoc range [%d:%d) invalid for %d-byte source", heredoc.Start, heredoc.End, len(tt.src))
			}
			after := texts[findIndex(toks, *heredoc)+1:]
			if len(after) != len(tt.wantAfter) {
				t.Fatalf("tokens after heredoc = %v, want %v", after, tt.wantAfter)
			}
			for i := range after {
				if after[i] != tt.wantAfter[i] {
					t.Errorf("tokens after heredoc = %v, want %v", after, tt.wantAfter)
				}
			}
		})
	}
}

func findIndex(toks []Token, target Token) int {
	for i, tok := range toks {
		if tok.Start == target.Start && tok.End == target.End {
			return i
		}
	}
	return -1
}

func TestLexCRLFAndBOM(t *testing.T) {
	toks := lexString(t, "\xEF\xBB\xBF:8080\r\n\trespond ok\r\n")
	if len(toks) != 3 {
		t.Fatalf("tokens = %+v, want 3", toks)
	}
	if toks[0].Text != ":8080" || toks[0].Line != 1 {
		t.Errorf("token[0] = %+v, want :8080 on line 1", toks[0])
	}
	if toks[1].Text != "respond" || toks[1].Line != 2 {
		t.Errorf("token[1] = %+v, want respond on line 2", toks[1])
	}
	if toks[2].Text != "ok" || toks[2].Line != 2 {
		t.Errorf("token[2] = %+v, want ok on line 2", toks[2])
	}
}

func TestLexEscapedNewline(t *testing.T) {
	// A trailing backslash chains arguments onto the next line: all tokens
	// stay on the same logical line.
	toks := lexString(t, "line1\\\nescaped1\\\nescaped2\nline4")
	want := []string{"line1", "escaped1", "escaped2", "line4"}
	if len(toks) != len(want) {
		t.Fatalf("tokens = %+v, want %v", toks, want)
	}
	for i, tok := range toks {
		if tok.Text != want[i] {
			t.Errorf("token[%d] = %q, want %q", i, tok.Text, want[i])
		}
	}
	// escaped1 and escaped2 continue line 1; line4 starts line 4.
	if toks[1].Line != 1 || toks[2].Line != 1 || toks[3].Line != 4 {
		t.Errorf("token lines = %d %d %d %d, want 1 1 1 4", toks[0].Line, toks[1].Line, toks[2].Line, toks[3].Line)
	}
}

func TestLexUnterminatedQuote(t *testing.T) {
	wantLexErr(t, "respond \"abc", "unterminated")
}

func TestLexOffsetsMatchSource(t *testing.T) {
	src := "directive arg \"quoted value\"\n"
	toks := lexString(t, src)
	for i, tok := range toks {
		if tok.End > len(src) || tok.Start > tok.End {
			t.Fatalf("token[%d] %+v has invalid range", i, tok)
		}
		if got := string(src[tok.Start:tok.End]); got != tok.Text && tok.Quote == 0 {
			t.Errorf("token[%d] raw = %q, want %q", i, got, tok.Text)
		}
	}
	// The quoted token's raw span includes the quotes.
	if got := string(src[toks[2].Start:toks[2].End]); got != `"quoted value"` {
		t.Errorf("quoted raw = %q, want %q", got, `"quoted value"`)
	}
}

func TestGroupLines(t *testing.T) {
	src := "a b\nc \"multi\nline\" d\ne"
	toks := lexString(t, src)
	groups := groupLines(toks)
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3 (%+v)", len(groups), groups)
	}
	got := make([]string, len(groups))
	for i, g := range groups {
		parts := make([]string, len(g))
		for j, tok := range g {
			parts[j] = tok.Text
		}
		got[i] = strings.Join(parts, " ")
	}
	want := []string{"a b", "c multi\nline d", "e"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("group[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestLexQuoted_UnterminatedBacktick verifies an unterminated backtick
// string reports the backtick kind in its error message.
func TestLexQuoted_UnterminatedBacktick(t *testing.T) {
	_, err := lex([]byte("`unterminated"))
	if err == nil {
		t.Fatal("lex with an unterminated backtick string: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "unterminated backtick string") {
		t.Errorf("error = %q, want the backtick kind in the message", err)
	}
}
