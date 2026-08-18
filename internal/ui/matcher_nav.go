package ui

import (
	"bytes"
	"fmt"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// matcherNav is the transient state of the matcher definition↔reference
// cycler (the g keybinding). It is presentation-only: it reads the parse
// tree through caddyfile.Matchers and never mutates the source.
type matcherNav struct {
	// docPath is the document the refs list was built for, so a press after
	// switching documents rebuilds the list instead of indexing into a
	// stale one.
	docPath string
	// source is a copy of the document source the refs list was derived
	// from. A structurally edited save, a reload or a rollback can replace
	// the document with new matchers while keeping the same path, so the
	// session is also invalidated whenever the source no longer matches.
	source []byte
	// refs holds every named matcher occurrence (definitions first, then
	// references) of the document, in source order.
	refs []caddyfile.MatcherRef
	// cursor is the index of the occurrence currently revealed in the
	// source pane. The next press advances (wrapping) unless the session
	// is stale, in which case the list is rebuilt.
	cursor int
}

// gotoNextMatcher advances the matcher navigator to the next occurrence of
// a named matcher in the currently selected document and reveals it in the
// source pane. When no session is active, the selected document changed, or
// the document's source was replaced (a save, reload or rollback), the list
// is rebuilt from caddyfile.Matchers and the cursor starts at the first
// occurrence at-or-after the current selection's line. When the document has
// no named matchers, an informational status is shown and the session stays
// cleared. It is read-only: no source bytes change, only the selection reveal
// and the status message.
func (m *Model) gotoNextMatcher() {
	doc := m.sourceDoc
	if doc == nil {
		m.statusMessage = "no document selected — point at a configuration file first"
		m.matcherNav = nil
		return
	}

	// Rebuild the list when the session is stale, belongs to another
	// document (after switching selection) or was derived from a different
	// source (a save, reload or rollback can replace the document with new
	// matchers while keeping the same path).
	stale := m.matcherNav == nil ||
		m.matcherNav.docPath != doc.Path ||
		!bytes.Equal(m.matcherNav.source, doc.Source)
	var refs []caddyfile.MatcherRef
	if stale {
		refs = caddyfile.Matchers(doc)
	} else {
		refs = m.matcherNav.refs
	}
	if len(refs) == 0 {
		m.statusMessage = "no named matchers in this document"
		m.matcherNav = nil
		return
	}

	cursor := 0
	if stale {
		// Start at the first occurrence at-or-after the current selection's
		// source line so repeated presses feel like "next matcher from here".
		if sel := m.selectedItem(); sel != nil && sel.hasNode {
			curLine := sel.node.Range.StartLine
			cursor = matcherStartingRef(refs, curLine)
		}
	} else {
		cursor = (m.matcherNav.cursor + 1) % len(refs)
	}

	m.matcherNav = &matcherNav{
		docPath: doc.Path,
		source:  append([]byte(nil), doc.Source...),
		refs:    refs,
		cursor:  cursor,
	}
	m.revealMatcher(refs[cursor], len(refs))
}

// matcherStartingRef returns the index of the first occurrence whose
// containing node starts at or after the given line, falling back to 0 when
// every occurrence precedes that line.
func matcherStartingRef(refs []caddyfile.MatcherRef, curLine int) int {
	for i, r := range refs {
		if r.Node.Range.StartLine >= curLine {
			return i
		}
	}
	return 0
}

// revealMatcher re-anchors the tree on the node containing the matcher
// occurrence, reveals its source line and reports the current position.
func (m *Model) revealMatcher(r caddyfile.MatcherRef, total int) {
	if m.state == nil || m.state.Graph == nil {
		// No tree to anchor into, but the source reveal can still run.
		m.sourceRevealLine = r.Node.Range.StartLine
		m.statusMessage = matcherStatus(r, m.matcherNav.cursor, total)
		return
	}
	expandNodeAncestors(m.sourceDoc, r.Node, m.collapsed)
	if nodeIsTreeRow(&r.Node) {
		m.rebuildTree(itemKey(m.sourceDoc, &r.Node))
	} else if parent := nearestVisibleAncestor(m.sourceDoc, r.Node); parent != nil {
		m.rebuildTree(itemKey(m.sourceDoc, parent))
	} else {
		m.rebuildTree(itemKey(m.sourceDoc, nil))
	}
	m.sourceRevealLine = r.Node.Range.StartLine
	m.statusMessage = matcherStatus(r, m.matcherNav.cursor, total)
}

// matcherStatus renders the command-palette-style status for the current
// occurrence: it names the matcher, whether this is the definition or a
// reference, the position in the cycle and the containing directive.
func matcherStatus(r caddyfile.MatcherRef, idx, total int) string {
	kind := "ref"
	if r.Definition {
		kind = "def"
	}
	name := r.Name
	directive := r.Node.Name
	if directive == "" {
		directive = fmt.Sprintf("%s", r.Node.Kind)
	}
	return fmt.Sprintf("matcher @%s: %s %d/%d · %s · line %d",
		name, kind, idx+1, total, directive, r.Node.Range.StartLine)
}
