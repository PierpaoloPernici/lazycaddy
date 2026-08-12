package ui

import (
	"strings"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	newNodeNameField = iota
	newNodeArgsField
)

func (m *Model) canNewNode() bool {
	if m.state == nil || m.state.Graph == nil || m.state.Settings.ReadOnly ||
		m.saver == nil || m.formatter == nil || m.structuredAddBusy ||
		m.busy || m.saving || m.editing || m.deleting || m.reloading || m.rollingBack {
		return false
	}
	sel := m.selectedItem()
	if sel == nil || sel.doc == nil {
		return false
	}
	if !sel.hasNode {
		return true
	}
	if sel.node.Kind == caddyfile.KindSite || sel.node.Kind == caddyfile.KindSnippet || sel.node.Kind == caddyfile.KindNamedRoute {
		return true
	}
	return sel.node.Kind == caddyfile.KindDirective && isNewNodeHandlerParent(sel.node.Name)
}

func isNewNodeHandlerParent(name string) bool {
	switch name {
	case "route", "handle", "handle_path", "handle_errors":
		return true
	default:
		return false
	}
}

func newNodeOptions(topLevel bool) []string {
	if topLevel {
		return []string{"site", "snippet", "named route", "global options"}
	}
	return []string{"route", "handle", "handle_path", "handle_errors"}
}

func (m *Model) startNewNode() (tea.Model, tea.Cmd) {
	if !m.canNewNode() {
		m.statusMessage = "✗ new node unavailable: select a writable document or structural block"
		return m, nil
	}
	sel := m.selectedItem()
	m.structuredAddInput = structuredInput{}
	m.structuredAddDoc = sel.doc
	m.structuredAddParent = caddyfile.Node{}
	m.structuredAddKey = sel.key
	m.structuredAddNewTop = !sel.hasNode
	if sel.hasNode {
		m.structuredAddParent = sel.node
	}
	m.structuredAddItems = newNodeOptions(m.structuredAddNewTop)
	m.structuredAddCursor = 0
	m.structuredAddName = ""
	m.structuredAddNewKind = caddyfile.Kind(0)
	m.structuredAddNewName = structuredInput{}
	m.structuredAddNewArgs = structuredInput{}
	m.structuredAddNewField = newNodeNameField
	m.structuredAddCreating = true
	m.structuredAddEditing = false
	m.structuredAddMode = structuredAddNewPicker
	m.showStructuredAdd = true
	m.statusMessage = ""
	// The structured-add modal is an unrelated workflow: any active text
	// selection is dropped.
	m.clearTextSelection()
	return m, nil
}

func (m *Model) updateNewNodeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.structuredAddMode == structuredAddNewPicker {
		return m.updateNewNodePickerKey(msg)
	}
	return m.updateNewNodeFormKey(msg)
}

func (m *Model) updateNewNodePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeStructuredAdd()
		m.statusMessage = "new node cancelled"
		return m, nil
	case "ctrl+h":
		return m.startCaddyfileHelp()
	case "up", "ctrl+k":
		if m.structuredAddCursor > 0 {
			m.structuredAddCursor--
		}
		return m, nil
	case "down", "ctrl+j":
		if m.structuredAddCursor < len(m.structuredAddItems)-1 {
			m.structuredAddCursor++
		}
		return m, nil
	case "enter":
		if len(m.structuredAddItems) == 0 {
			return m, nil
		}
		m.structuredAddName = m.structuredAddItems[m.structuredAddCursor]
		m.structuredAddNewKind = newNodeKind(m.structuredAddNewTop, m.structuredAddName)
		m.structuredAddNewName = structuredInput{}
		m.structuredAddNewArgs = structuredInput{}
		m.structuredAddNewField = newNodeFirstField(m.structuredAddNewKind)
		m.structuredAddMode = structuredAddNewForm
		return m, nil
	}
	return m, nil
}

func newNodeKind(topLevel bool, label string) caddyfile.Kind {
	if !topLevel {
		return caddyfile.KindDirective
	}
	switch label {
	case "site":
		return caddyfile.KindSite
	case "snippet":
		return caddyfile.KindSnippet
	case "named route":
		return caddyfile.KindNamedRoute
	case "global options":
		return caddyfile.KindGlobalOptions
	default:
		return caddyfile.Kind(-1)
	}
}

func newNodeFirstField(kind caddyfile.Kind) int {
	if kind == caddyfile.KindDirective || kind == caddyfile.KindGlobalOptions {
		return newNodeArgsField
	}
	return newNodeNameField
}

func (m *Model) updateNewNodeFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.structuredAddMode = structuredAddNewPicker
		m.structuredAddNewName = structuredInput{}
		m.structuredAddNewArgs = structuredInput{}
		return m, nil
	case "ctrl+c":
		m.closeStructuredAdd()
		m.statusMessage = "new node cancelled"
		return m, nil
	case "ctrl+h":
		return m.startCaddyfileHelp()
	case "tab", "down", "up", "shift+tab":
		if m.structuredAddNewKind == caddyfile.KindDirective {
			m.structuredAddNewField = newNodeArgsField
		}
		return m, nil
	case "enter":
		return m.submitNewNode()
	}
	if m.structuredAddNewKind == caddyfile.KindDirective {
		m.structuredAddNewArgs.update(msg)
	} else if m.structuredAddNewKind != caddyfile.KindGlobalOptions {
		m.structuredAddNewName.update(msg)
	}
	return m, nil
}

func (m *Model) submitNewNode() (tea.Model, tea.Cmd) {
	if m.structuredAddBusy || m.structuredAddDoc == nil {
		return m, nil
	}
	spec := caddyfile.NodeSpec{
		Kind:     m.structuredAddNewKind,
		Position: caddyfile.InsertAtEnd,
	}
	switch spec.Kind {
	case caddyfile.KindSite, caddyfile.KindSnippet, caddyfile.KindNamedRoute:
		spec.Name = strings.TrimSpace(m.structuredAddNewName.String())
	case caddyfile.KindDirective:
		spec.Name = m.structuredAddName
		spec.Args = strings.TrimSpace(m.structuredAddNewArgs.String())
	case caddyfile.KindGlobalOptions:
	default:
		m.statusMessage = "✗ new node has no supported type"
		return m, nil
	}
	planner := caddyfile.NewPlanner(m.structuredAddDoc)
	var parent *caddyfile.Node
	if !m.structuredAddNewTop {
		p := m.structuredAddParent
		parent = &p
	}
	edit, err := planner.CreateNode(parent, spec)
	if err != nil {
		m.statusMessage = "✗ new node unavailable: " + err.Error()
		return m, nil
	}
	return m.queueStructuredAddValidation(newNodeDisplayName(spec), "new", edit)
}

func newNodeDisplayName(spec caddyfile.NodeSpec) string {
	switch spec.Kind {
	case caddyfile.KindGlobalOptions:
		return "global options"
	case caddyfile.KindDirective:
		return spec.Name
	default:
		return spec.Name
	}
}

func (m *Model) newNodeView(width, height int) string {
	boxW := width - 8
	if boxW < 44 {
		boxW = width - 2
	}
	if boxW > 88 {
		boxW = 88
	}
	if boxW < 1 {
		boxW = 1
	}
	boxH := 14
	if height < boxH {
		boxH = height
	}
	contentW := max(1, boxW-4)
	target := truncateToWidth("Target: "+m.newNodeTarget(), contentW)
	if m.structuredAddMode == structuredAddNewPicker {
		var body strings.Builder
		body.WriteString(target + "\n\n")
		for i, label := range m.structuredAddItems {
			prefix := "  "
			if i == m.structuredAddCursor {
				prefix = "› "
			}
			body.WriteString(truncateToWidth(prefix+label, contentW) + "\n")
		}
		title := truncateToWidth("New node · ↑/↓ choose · Ctrl-H help · Enter select · Esc cancel", contentW)
		return focusedPaneStyle.Width(boxW - 2).Height(boxH - 2).Render(activeTitleStyle.Render(title) + "\n" + body.String())
	}

	label := "name> "
	input := m.structuredAddNewName
	if m.structuredAddNewKind == caddyfile.KindDirective {
		label = "args> "
		input = m.structuredAddNewArgs
	}
	body := target + "\n\n"
	if m.structuredAddNewKind == caddyfile.KindGlobalOptions {
		body += dimStyle.Render("Creates an empty global options block") + "\n\n"
	} else {
		body += structuredAddFieldLine(label, input, true, contentW) + "\n\n"
	}
	body += dimStyle.Render("Enter create · Ctrl-H help · Esc back")
	title := "New " + m.structuredAddName + " · Esc cancel"
	return focusedPaneStyle.Width(boxW - 2).Height(boxH - 2).Render(activeTitleStyle.Render(truncateToWidth(title, contentW)) + "\n" + body)
}

func (m *Model) newNodeTarget() string {
	if m.structuredAddNewTop {
		return "document top level"
	}
	return m.structuredAddParent.Name
}
