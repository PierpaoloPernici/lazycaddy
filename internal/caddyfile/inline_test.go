package caddyfile

import "testing"

func TestInlineProblems_ReferencedButNotDefined(t *testing.T) {
	// @api is referenced via reverse_proxy but never declared.
	src := []byte("example.test {\n\treverse_proxy @api localhost:8080\n}\n")
	doc := Parse(src)
	findings := InlineProblems(doc)
	if len(findings) == 0 {
		t.Fatal("expected a finding for a matcher referenced but never defined")
	}
	f := findings[0]
	if f.Severity != SeverityAdvisoryHint {
		t.Errorf("severity = %v, want hint", f.Severity)
	}
	if f.StartLine != 2 {
		t.Errorf("StartLine = %d, want 2", f.StartLine)
	}
	// The directive node range starts at the indented tab, so @ lies at
	// column 16 (the +1 columns from the tab offset via Range.Start).
	wantCol := 16
	if f.Column != wantCol {
		t.Errorf("Column = %d, want %d (the @api token position)", f.Column, wantCol)
	}
	span := doc.Source[f.Start:f.End]
	if string(span) != "@api" {
		t.Errorf("finding span = %q, want @api", span)
	}
}

func TestInlineProblems_DefinedButNeverUsed(t *testing.T) {
	// @api is declared but no directive references it.
	src := []byte("example.test {\n\t@api path /api/*\n\trespond ok\n}\n")
	doc := Parse(src)
	findings := InlineProblems(doc)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	f := findings[0]
	if f.Severity != SeverityAdvisoryInfo {
		t.Errorf("severity = %v, want info", f.Severity)
	}
	if f.StartLine != 2 {
		t.Errorf("StartLine = %d, want 2", f.StartLine)
	}
	if got := string(doc.Source[f.Start:f.End]); got != "@api" {
		t.Errorf("span = %q, want @api", got)
	}
}

func TestInlineProblems_SelfConsistent(t *testing.T) {
	// @api defined and referenced -> no findings.
	src := []byte("example.test {\n\t@api path /api/*\n\treverse_proxy @api localhost:8080\n\thandle @api {\n\t\trespond ok\n\t}\n}\n")
	if findings := InlineProblems(Parse(src)); len(findings) != 0 {
		t.Errorf("self-consistent matchers produced findings: %+v", findings)
	}
}

func TestInlineProblems_MultipleFindingsSourceOrder(t *testing.T) {
	// Two distinct problems: @a referenced-but-undefined, @b defined-but-unused.
	src := []byte("example.test {\n\t@b path /b/*\n\treverse_proxy @a localhost\n}\n")
	findings := InlineProblems(Parse(src))
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(findings))
	}
	// Source order: @b (line 2, info) precedes the @a reference (line 3, hint).
	if findings[0].Start > findings[1].Start {
		t.Errorf("findings not in source order: %+v", findings)
	}
	if findings[0].Severity != SeverityAdvisoryInfo {
		t.Errorf("findings[0].Severity = %v, want info", findings[0].Severity)
	}
	if findings[1].Severity != SeverityAdvisoryHint {
		t.Errorf("findings[1].Severity = %v, want hint", findings[1].Severity)
	}
}

func TestInlineProblems_NilOrErroneousDoc(t *testing.T) {
	if findings := InlineProblems(nil); findings != nil {
		t.Errorf("nil doc produced findings: %+v", findings)
	}
	// A partially parsed / malformed document must not produce advisory
	// findings (unreliable basis).
	bad := Parse([]byte("example.test {\n"))
	if bad.Err == nil {
		t.Fatal("expected a parse error for the unclosed site block")
	}
	if findings := InlineProblems(bad); findings != nil {
		t.Errorf("malformed doc produced findings: %+v", findings)
	}
}

func TestInlineProblems_NoMatchers(t *testing.T) {
	src := []byte("example.test {\n\trespond ok\n}\n")
	if findings := InlineProblems(Parse(src)); findings != nil {
		t.Errorf("no-matcher document produced findings: %+v", findings)
	}
}

func TestInlineSeverity_String(t *testing.T) {
	tests := []struct {
		s    InlineSeverity
		want string
	}{
		{SeverityAdvisoryInfo, "info"},
		{SeverityAdvisoryHint, "hint"},
		{InlineSeverity(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("InlineSeverity(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestInlineColumn(t *testing.T) {
	src := []byte("example.test {\n\t\t@api localhost\n")
	// off on the first line after a tab-indented directive.
	if got := inlineColumn(src, 0, 14); got != 15 {
		t.Errorf("same-line column = %d, want 15", got)
	}
	// Multi-line subject: a newline between lineStart and off -> 0.
	if got := inlineColumn(src, 0, 30); got != 0 {
		t.Errorf("cross-line column = %d, want 0", got)
	}
	// off before lineStart -> 0.
	if got := inlineColumn(src, 20, 5); got != 0 {
		t.Errorf("off-before-start column = %d, want 0", got)
	}
}
