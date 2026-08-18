package caddyfile

import (
	"bytes"
	"sort"
)

// InlineSeverity classifies an advisory inline finding. These are
// presentation-only hints derived from the parse tree; they never define,
// replace or reject Caddy syntax and never block a save, write or reload.
// Caddy remains the authoritative validator through `caddy fmt`/`caddy
// validate`.
type InlineSeverity int

const (
	// SeverityAdvisoryInfo flags a benign but potentially unintended
	// pattern (for example a matcher that is defined but never used).
	SeverityAdvisoryInfo InlineSeverity = iota
	// SeverityAdvisoryHint flags a likely problem (for example a matcher
	// that is referenced but never defined). It is still advisory: Caddy
	// decides validity.
	SeverityAdvisoryHint
)

func (s InlineSeverity) String() string {
	switch s {
	case SeverityAdvisoryInfo:
		return "info"
	case SeverityAdvisoryHint:
		return "hint"
	default:
		return "unknown"
	}
}

// InlineFinding is one advisory finding located in a document. Start/End are
// byte offsets and StartLine is the 1-based line of the finding (the end line
// is available on the node range when needed). Column is 1-based when the
// finding targets a single token; otherwise 0.
type InlineFinding struct {
	// Message is the human-readable advisory text.
	Message string
	// Severity classifies the finding (info vs hint).
	Severity InlineSeverity
	// Start and End locate the finding in the document source.
	Start, End int
	// StartLine is the 1-based line of the finding.
	StartLine int
	// Column is the 1-based column of the offending token, or 0 when the
	// finding spans several tokens.
	Column int
}

// InlineProblems returns the advisory inline validation findings for a
// document, derived only from the parse tree when it can identify roles
// reliably. The rule set is deliberately conservative to avoid false
// positives, because Caddy is the authority on syntax and validity:
//
//   - a matcher referenced but never defined in the document (hint);
//   - a matcher defined but never referenced in the document (info).
//
// The analysis is document-local: it inspects only the named matchers
// present in this single Document, not the ones in imported siblings.
// Caddy expands imports before parsing and requires a named matcher to be
// defined in the site block that uses it, but lazycaddy keeps imported
// files as separate documents. Because a matcher may be defined in an
// imported or sibling document that this call cannot see, the findings
// never claim global correctness: "referenced but never defined" means
// "not defined here", which deliberately avoids false positives at the
// cost of not covering every imported document.
//
// Findings are emitted in source order. Unknown/plugin directives and
// malformed or partially parsed documents never produce findings; the rule
// set only inspects named matcher occurrences that the parser resolved.
func InlineProblems(doc *Document) []InlineFinding {
	if doc == nil || doc.Err != nil {
		// A partially parsed or malformed document is not a reliable basis
		// for advisory findings; surface nothing rather than guess.
		return nil
	}
	refs := Matchers(doc)
	if len(refs) == 0 {
		return nil
	}

	// Collect matcher names by whether they are defined or referenced.
	defined := map[string]bool{}
	used := map[string]int{} // name -> first reference line
	for _, r := range refs {
		if r.Definition {
			defined[r.Name] = true
			continue
		}
		if _, ok := used[r.Name]; !ok {
			used[r.Name] = r.Node.Range.StartLine
		}
	}

	var out []InlineFinding
	// Referenced but never defined: one hint per referencing occurrence.
	for _, r := range refs {
		if r.Definition {
			continue
		}
		if !defined[r.Name] {
			out = append(out, InlineFinding{
				Message:   "matcher @" + r.Name + " is referenced but never defined in this document",
				Severity:  SeverityAdvisoryHint,
				Start:     r.Start,
				End:       r.End,
				StartLine: r.Node.Range.StartLine,
				Column:    inlineColumn(doc.Source, r.Node.Range.Start, r.Start),
			})
		}
	}
	// Defined but never used: one info per definition.
	for _, r := range refs {
		if !r.Definition {
			continue
		}
		if _, ok := used[r.Name]; !ok {
			out = append(out, InlineFinding{
				Message:   "matcher @" + r.Name + " is defined but never referenced in this document",
				Severity:  SeverityAdvisoryInfo,
				Start:     r.Start,
				End:       r.End,
				StartLine: r.Node.Range.StartLine,
				Column:    inlineColumn(doc.Source, r.Node.Range.Start, r.Start),
			})
		}
	}

	// Source order: stable by start offset.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// inlineColumn returns the 1-based column of off relative to the token line
// that starts at lineStart, or 0 when the token is not on the same line as
// lineStart (the caller then leaves the column unspecified rather than
// report a misleading one for multi-line tokens).
func inlineColumn(src []byte, lineStart, off int) int {
	if off < lineStart || bytes.IndexByte(src[lineStart:off], '\n') >= 0 {
		return 0
	}
	return off - lineStart + 1
}
