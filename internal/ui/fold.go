package ui

import (
	"fmt"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// Source folding is a display-only projection of the tree expansion state:
// when a structural tree row is collapsed, the lines strictly between its
// header and closing brace (or every line after the header for brace-less and
// unclosed blocks) become one indicator row. The underlying source is never
// rewritten, reformatted or reordered; every byte, line number and source
// range stays valid for patching, selection and copying.
//
// The tree's stable item key is the only source of truth for expansion. The
// folded rendering is driven by caddyfile.FoldLayoutFor, which supplies the
// lossless display-row mapping. Quoted braces are string tokens and never
// create folds; comments, imports and leaf directives are never foldable.

// treeKeyForFold finds the tree row that owns a parser fold. Keeping this
// lookup range-based makes the mapping resilient to tree rebuilds while still
// using the exact same identity as navigation and selection.
func treeKeyForFold(doc *caddyfile.Document, f caddyfile.Fold) (string, bool) {
	if doc == nil {
		return "", false
	}
	var walk func([]caddyfile.Node) (string, bool)
	walk = func(nodes []caddyfile.Node) (string, bool) {
		for i := range nodes {
			n := &nodes[i]
			if n.Kind == f.Kind && n.Name == f.Name &&
				n.Range.Start == f.Range.Start && n.Range.End == f.Range.End {
				return itemKey(doc, n), true
			}
			if key, ok := walk(n.Children); ok {
				return key, true
			}
		}
		return "", false
	}
	return walk(doc.Nodes)
}

func treeKeyForFoldRange(doc *caddyfile.Document, f caddyfile.FoldRange) (string, bool) {
	return treeKeyForFold(doc, caddyfile.Fold{
		Kind: f.Kind, Name: f.Name, Range: f.Range,
		StartLine: f.StartLine, EndLine: f.EndLine,
		CloseBraceLine: f.CloseBraceLine,
	})
}

// activeFoldRanges returns the folds whose owning tree rows are collapsed.
// FoldLayoutFor normalizes nested active ranges, so a collapsed parent
// naturally subsumes its collapsed descendants.
func (m *Model) activeFoldRanges(doc *caddyfile.Document) []caddyfile.FoldRange {
	if doc == nil {
		return nil
	}
	folds := caddyfile.Folds(doc)
	out := make([]caddyfile.FoldRange, 0, len(folds))
	for _, f := range folds {
		key, ok := treeKeyForFold(doc, f)
		if !ok || !m.collapsed[key] {
			continue
		}
		out = append(out, caddyfile.FoldRange{
			Kind:           f.Kind,
			Name:           f.Name,
			Range:          f.Range,
			StartLine:      f.StartLine,
			EndLine:        f.EndLine,
			CloseBraceLine: f.CloseBraceLine,
		})
	}
	return out
}

// foldLayoutFor returns the cached folded display layout for doc,
// recomputing it only when the document changed, its source was replaced
// (a save, reload or rollback), or a fold toggle invalidated it
// (foldVersion). The cache is consumed by both the renderer and the
// selection pane, so the rows painted and the rows the mouse maps always
// agree.
func (m *Model) foldLayoutFor(doc *caddyfile.Document) *caddyfile.FoldLayout {
	state := m.foldStateSignature(doc)
	if m.foldLayoutDoc != doc || m.foldLayoutVersion != m.foldVersion ||
		m.foldLayoutState != state || !sameSourceBytes(m.foldLayoutSource, sourceOf(doc)) {
		m.foldLayoutDoc = doc
		m.foldLayoutVersion = m.foldVersion
		m.foldLayoutState = state
		m.foldLayout = nil
		m.foldLayoutSource = nil
		if doc != nil {
			src := doc.Source
			m.foldLayout = caddyfile.FoldLayoutFor(src, m.activeFoldRanges(doc))
			m.foldLayoutSource = src
		}
	}
	return m.foldLayout
}

// foldStateSignature lets the layout cache notice tree-state changes made by
// search and reveal helpers, which may update collapsed directly rather than
// through a key handler. It is based on active ranges, so unrelated tree
// branches do not invalidate the source layout.
func (m *Model) foldStateSignature(doc *caddyfile.Document) string {
	state := ""
	for _, f := range m.activeFoldRanges(doc) {
		state += fmt.Sprintf("%d:%s:%d:%d;", f.Kind, f.Name, f.Range.Start, f.Range.End)
	}
	return state
}

// sourceOf returns the document source, or nil for a nil document.
func sourceOf(doc *caddyfile.Document) []byte {
	if doc == nil {
		return nil
	}
	return doc.Source
}

// sameSourceBytes reports whether two byte slices share the same backing
// storage and length. Document sources are replaced with fresh slices
// (append to nil) on saves, reloads and rollbacks, so pointer identity
// detects every in-place replacement cheaply without a content scan.
func sameSourceBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	return &a[0] == &b[0]
}

// foldCovering reports whether the collapsed fold hides line (1-based):
// any line strictly between the header and the visible closing brace, or
// any line after the header for a brace-less or unclosed block.
func foldCovering(f caddyfile.Fold, line int) bool {
	if f.CloseBraceLine > f.StartLine {
		return line > f.StartLine && line < f.CloseBraceLine
	}
	return line > f.StartLine && line <= f.EndLine
}

// expandFoldsForReveal removes every collapsed fold that hides one of the
// target source lines, so a reveal — a selection change, a search hit, a
// diagnostic jump, a matcher cycle or a re-anchor after a save — always
// lands on visible content. It mirrors the auto-expansion of collapsed
// regions when a cursor enters them in mainstream editors. Folds that do
// not cover the target stay untouched, so fold state remains stable
// across unrelated selections. The targets are the one-shot reveal line
// (search/diagnostic/matcher hits) and the selected node's header line;
// the node's own closing line is deliberately not a target, so selecting
// a folded block in the tree keeps its fold closed (the header stays
// visible) instead of immediately re-opening what the operator just
// collapsed.
func (m *Model) expandFoldsForReveal(doc *caddyfile.Document, revealLine int, key selectionKey) {
	if doc == nil {
		return
	}
	var targets []int
	if revealLine > 0 {
		targets = append(targets, revealLine)
	}
	if key.hasNode {
		targets = append(targets, key.start)
	}
	if len(targets) == 0 {
		return
	}
	changed := false
	for _, f := range caddyfile.Folds(doc) {
		fk, ok := treeKeyForFold(doc, f)
		if !ok || !m.collapsed[fk] {
			continue
		}
		for _, t := range targets {
			if foldCovering(f, t) {
				delete(m.collapsed, fk)
				changed = true
				break
			}
		}
	}
	if changed {
		m.foldVersion++
		anchor := ""
		if sel := m.selectedItem(); sel != nil {
			anchor = sel.key
		}
		m.rebuildTree(anchor)
	}
}

// openFoldAtRow opens the fold whose indicator row contains the given
// absolute display row (the mouse click-to-expand path), or is a no-op
// when the row is not an indicator.
func (m *Model) openFoldAtRow(absRow int) {
	layout := m.foldLayout
	if layout == nil || absRow < 0 || absRow >= len(layout.Rows) || layout.Rows[absRow] != 0 {
		return
	}
	idx := layout.FoldAt[absRow]
	if idx < 0 || idx >= len(layout.Folds) {
		return
	}
	doc := m.foldLayoutDoc
	if doc == nil {
		return
	}
	key, ok := treeKeyForFoldRange(doc, layout.Folds[idx])
	if !ok || !m.collapsed[key] {
		return
	}
	delete(m.collapsed, key)
	m.foldVersion++
	m.rebuildTree(key)
	m.statusMessage = fmt.Sprintf("opened fold · %s (lines %d-%d)",
		foldLabel(caddyfile.Fold{Kind: layout.Folds[idx].Kind, Name: layout.Folds[idx].Name}),
		layout.Folds[idx].StartLine, layout.Folds[idx].EndLine)
}

// foldLabel renders a concise block label for fold status messages and
// indicators, mirroring the tree row labels.
func foldLabel(f caddyfile.Fold) string {
	switch f.Kind {
	case caddyfile.KindGlobalOptions:
		return "global options"
	case caddyfile.KindSnippet:
		return "snippet (" + f.Name + ")"
	case caddyfile.KindNamedRoute:
		return "route &(" + f.Name + ")"
	case caddyfile.KindSite:
		if f.Name == "" {
			return "site"
		}
		return "site " + f.Name
	default:
		if f.Name != "" {
			return f.Name
		}
		return f.Kind.String()
	}
}

// foldIndicatorLabel renders the text of a fold indicator row: the number
// of hidden source lines.
func foldIndicatorLabel(hidden int) string {
	if hidden == 1 {
		return "⋯ 1 line"
	}
	return fmt.Sprintf("⋯ %d lines", hidden)
}

// rowForLine returns the display row of a 1-based source line: the
// layout's mapping when folds are active, the identity row otherwise.
// Hidden lines (which callers expand before revealing) map to the closest
// visible line above them so a reveal never lands past the content.
func (m *Model) rowForLine(line int) int {
	layout := m.foldLayout
	if layout != nil && line > 0 && line < len(layout.LineRow) {
		if r := layout.LineRow[line]; r >= 0 {
			return r
		}
		for l := line - 1; l >= 1; l-- {
			if r := layout.LineRow[l]; r >= 0 {
				return r
			}
		}
		return 0
	}
	return line - 1
}

// lastVisibleLine returns the 0-based index of the last visible source
// line of the current folded layout, or -1 when no layout is active (the
// caller then uses the full line count).
func (m *Model) lastVisibleSourceLine() int {
	layout := m.foldLayout
	if layout == nil {
		return -1
	}
	for line := len(layout.LineRow) - 1; line >= 1; line-- {
		if layout.LineRow[line] >= 0 {
			return line - 1
		}
	}
	return -1
}

// foldCount reports how many folds of the document are currently closed;
// it is used for the source pane title so the folded state is always
// visible.
func (m *Model) foldCount(doc *caddyfile.Document) int {
	if doc == nil {
		return 0
	}
	n := 0
	for _, f := range caddyfile.Folds(doc) {
		if key, ok := treeKeyForFold(doc, f); ok && m.collapsed[key] {
			n++
		}
	}
	return n
}
