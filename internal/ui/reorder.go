package ui

import (
	"fmt"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	tea "github.com/charmbracelet/bubbletea"
)

// canReorderSelected reports whether the selected visible structural node has
// at least one visible structural sibling in the same source document. The
// planner remains authoritative for the final identity and context checks.
func (m *Model) canReorderSelected() bool {
	if m.state == nil || m.state.Graph == nil || m.state.Settings.ReadOnly ||
		m.saver == nil || m.formatter == nil || m.structuredAddBusy ||
		m.busy || m.saving || m.editing || m.deleting || m.reloading || m.rollingBack {
		return false
	}
	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.doc == nil || sel.node.Kind == caddyfile.KindGlobalOptions {
		return false
	}
	return len(m.reorderTargets(sel.doc, sel.node)) > 0
}

// reorderTargets returns visible structural siblings, excluding source. The
// global options node is a valid target because moving after it keeps global
// options first. Leaf directives remain in the parse tree and can be moved by
// the planner, but they are not tree rows and therefore are not targets here.
func (m *Model) reorderTargets(doc *caddyfile.Document, source caddyfile.Node) []caddyfile.Node {
	siblings, err := caddyfile.NewPlanner(doc).SiblingNodes(source)
	if err != nil {
		return nil
	}
	targets := make([]caddyfile.Node, 0, len(siblings))
	for _, sibling := range siblings {
		if nodeKey(&sibling) == nodeKey(&source) || !renderedNode(sibling) {
			continue
		}
		targets = append(targets, sibling)
	}
	return targets
}

// startReorder opens the sibling picker. The selected block is moved
// immediately after the chosen sibling.
func (m *Model) startReorder() (tea.Model, tea.Cmd) {
	if !m.canReorderSelected() {
		m.statusMessage = "✗ reorder unavailable: select a block with a reorderable sibling"
		return m, nil
	}
	sel := m.selectedItem()
	m.structuredAddDoc = sel.doc
	m.structuredAddParent = sel.node
	m.structuredAddKey = sel.key
	m.structuredAddName = "reorder"
	m.structuredAddReorderTargets = m.reorderTargets(sel.doc, sel.node)
	m.structuredAddCursor = 0
	m.structuredAddMode = structuredAddReorder
	m.structuredAddBusy = false
	m.showStructuredAdd = true
	m.statusMessage = ""
	m.clearTextSelection()
	return m, nil
}

func (m *Model) updateReorderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.closeStructuredAdd()
		m.statusMessage = "reorder cancelled"
	case "ctrl+c":
		m.closeStructuredAdd()
		m.statusMessage = "reorder cancelled"
	case "up", "ctrl+k":
		if m.structuredAddCursor > 0 {
			m.structuredAddCursor--
		}
	case "down", "ctrl+j":
		if m.structuredAddCursor < len(m.structuredAddReorderTargets)-1 {
			m.structuredAddCursor++
		}
	case "enter":
		return m.submitReorder()
	}
	return m, nil
}

func (m *Model) submitReorder() (tea.Model, tea.Cmd) {
	if m.structuredAddBusy || m.structuredAddDoc == nil || len(m.structuredAddReorderTargets) == 0 {
		return m, nil
	}
	if m.structuredAddCursor < 0 || m.structuredAddCursor >= len(m.structuredAddReorderTargets) {
		return m, nil
	}
	source := m.structuredAddParent
	target := m.structuredAddReorderTargets[m.structuredAddCursor]
	edit, err := caddyfile.NewPlanner(m.structuredAddDoc).MoveAfter(source, target)
	if err != nil {
		m.statusMessage = "✗ reorder unavailable: " + err.Error()
		return m, nil
	}
	return m.queueStructuredAddValidation(
		fmt.Sprintf("%s after %s", source.Name, target.Name),
		"reorder",
		edit,
	)
}
