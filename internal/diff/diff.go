package diff

import (
	"fmt"
	"strings"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

// Kind classifies a rendered unified-diff line.
type Kind int

const (
	KindContext    Kind = iota // unchanged line
	KindAdd                    // line added in the new text (starts with '+')
	KindRemove                 // line removed from the old text (starts with '-')
	KindHunkHeader             // '@@ -l,c +l,c @@' hunk header
	KindFileHeader             // '--- old' / '+++ new' pair at the top
)

// Line is one classified line of a unified diff.
type Line struct {
	Kind Kind
	Text string
}

// Unified computes a line-oriented unified diff between oldSrc and
// newSrc. oldLabel and newLabel are rendered in the ---/+++ header
// lines. The slice always contains the file-header lines; when the
// texts are identical no hunks follow (no KindAdd/KindRemove/KindHunkHeader
// lines). The returned error is reserved for a future implementation;
// the underlying library is infallible for the current inputs.
func Unified(oldSrc, newSrc []byte, oldLabel, newLabel string) ([]Line, error) {
	// myers.ComputeEdits produces minimal line edits; gotextdiff then
	// renders them as standard unified-diff text. The URI only locates
	// edits and never appears in the rendered output, so oldLabel is a
	// fine stand-in for a document URI.
	edits := myers.ComputeEdits(span.URIFromPath(oldLabel), string(oldSrc), string(newSrc))
	// fmt.Sprint drives the gotextdiff Unified.Format formatter, which
	// renders ---/+++ headers, @@ hunks and +/-/space-prefixed lines as
	// a single string that we then split and classify.
	text := fmt.Sprint(gotextdiff.ToUnified(oldLabel, newLabel, string(oldSrc), edits))

	// Split the rendered diff into lines. Every rendered line ends with
	// '\n', so the final split segment is empty; dropping it avoids a
	// phantom empty Line. Lines keep their trailing '\r' for CRLF
	// sources, which classification below preserves for the UI.
	raw := strings.Split(text, "\n")
	if n := len(raw); n > 0 && raw[n-1] == "" {
		raw = raw[:n-1]
	}

	lines := make([]Line, 0, len(raw))
	for _, l := range raw {
		lines = append(lines, Line{Kind: classify(l), Text: l})
	}

	// gotextdiff renders the ---/+++ header pair only when at least one
	// hunk exists, so for identical inputs it prints nothing at all. The
	// contract promises the file-header lines are always present (the UI
	// renders them), so synthesize the pair from the labels when no hunk
	// was rendered.
	if len(lines) == 0 {
		lines = []Line{
			{Kind: KindFileHeader, Text: "--- " + oldLabel},
			{Kind: KindFileHeader, Text: "+++ " + newLabel},
		}
	}
	return lines, nil
}

// classify maps the leading marker of a rendered unified-diff line to its
// Kind. Order matters: the two-character header markers ("--- " and
// "+++ ") must win over their single-character relatives ("-" and "+"),
// and the "@@" hunk header must be recognized before content lines. The
// marker stays part of Line.Text so the UI can color whole lines by Kind;
// the "\ No newline at end of file" note has no marker and lands in
// KindContext.
func classify(line string) Kind {
	switch {
	case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
		return KindFileHeader
	case strings.HasPrefix(line, "@@"):
		return KindHunkHeader
	case strings.HasPrefix(line, "+"):
		return KindAdd
	case strings.HasPrefix(line, "-"):
		return KindRemove
	default:
		return KindContext
	}
}
