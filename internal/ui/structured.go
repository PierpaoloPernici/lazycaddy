package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/diff"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
	tea "github.com/charmbracelet/bubbletea"
)

// structuredInput is the deliberately small single-line editor used by the
// structured-add prompt. It avoids adding a second clipboard dependency just
// for a short directive input; the application clipboard remains the only
// clipboard boundary.
type structuredInput struct {
	value  []rune
	cursor int
}

const maxStructuredInputRunes = 512

type structuredAddMode int

const (
	structuredAddPicker structuredAddMode = iota
	structuredAddArgs
	structuredAddReverseProxy
)

const (
	structuredReverseProxyMatcher = iota
	structuredReverseProxyUpstreams
)

func (i *structuredInput) update(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyRunes:
		if len(msg.Runes) == 0 {
			return
		}
		remaining := maxStructuredInputRunes - len(i.value)
		if remaining <= 0 {
			return
		}
		if len(msg.Runes) > remaining {
			msg.Runes = msg.Runes[:remaining]
		}
		i.value = append(i.value[:i.cursor], append(append([]rune(nil), msg.Runes...), i.value[i.cursor:]...)...)
		i.cursor += len(msg.Runes)
	case tea.KeyBackspace:
		if i.cursor > 0 {
			i.value = append(i.value[:i.cursor-1], i.value[i.cursor:]...)
			i.cursor--
		}
	case tea.KeyDelete:
		if i.cursor < len(i.value) {
			i.value = append(i.value[:i.cursor], i.value[i.cursor+1:]...)
		}
	case tea.KeyLeft:
		if i.cursor > 0 {
			i.cursor--
		}
	case tea.KeyRight:
		if i.cursor < len(i.value) {
			i.cursor++
		}
	case tea.KeyHome, tea.KeyCtrlA:
		i.cursor = 0
	case tea.KeyEnd, tea.KeyCtrlE:
		i.cursor = len(i.value)
	}
}

func (i *structuredInput) String() string { return string(i.value) }

func (i *structuredInput) View() string {
	if i.cursor >= len(i.value) {
		return i.String() + "▌"
	}
	return string(i.value[:i.cursor]) + "▌" + string(i.value[i.cursor:])
}

// canAddStructured reports whether the selected row can be used as a
// context-aware structured insertion target.
func (m *Model) canAddStructured() bool {
	if m.state == nil || m.state.Graph == nil || m.state.Settings.ReadOnly ||
		m.saver == nil || m.formatter == nil || m.structuredAddBusy ||
		m.busy || m.saving || m.editing || m.deleting || m.reloading || m.rollingBack {
		return false
	}
	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.doc == nil {
		return false
	}
	if sel.node.Kind == caddyfile.KindDirective {
		return sel.node.Name == "route" || sel.node.Name == "handle" ||
			sel.node.Name == "handle_path" || sel.node.Name == "handle_errors"
	}
	return sel.node.Kind == caddyfile.KindSite || sel.node.Kind == caddyfile.KindSnippet ||
		sel.node.Kind == caddyfile.KindNamedRoute || sel.node.Kind == caddyfile.KindGlobalOptions
}

func (m *Model) canEditReverseProxy() bool {
	if m.state == nil || m.state.Graph == nil || m.state.Settings.ReadOnly ||
		m.saver == nil || m.formatter == nil || m.structuredAddBusy ||
		m.busy || m.saving || m.editing || m.deleting || m.reloading || m.rollingBack {
		return false
	}
	sel := m.selectedItem()
	return sel != nil && sel.hasNode && sel.doc != nil && sel.node.IsDirective("reverse_proxy")
}

// startStructuredAdd opens the context-aware directive picker. The planner,
// rather than the UI, remains authoritative about the selected context.
func (m *Model) startStructuredAdd() (tea.Model, tea.Cmd) {
	if !m.canAddStructured() {
		m.statusMessage = "✗ add unavailable: select a supported block in writable mode"
		return m, nil
	}
	sel := m.selectedItem()
	m.structuredAddInput = structuredInput{}
	m.structuredAddDoc = sel.doc
	m.structuredAddParent = sel.node
	m.structuredAddKey = sel.key
	m.structuredAddMode = structuredAddPicker
	m.structuredAddName = ""
	m.structuredAddItems = caddyfile.InsertableDirectiveNames(sel.node)
	m.structuredAddCursor = 0
	m.structuredAddMatcher = structuredInput{}
	m.structuredAddUpstreams = structuredInput{}
	m.structuredAddRPField = structuredReverseProxyUpstreams
	m.structuredAddEditing = false
	m.showStructuredAdd = true
	m.statusMessage = ""
	return m, nil
}

func (m *Model) startReverseProxyEdit() (tea.Model, tea.Cmd) {
	if !m.canEditReverseProxy() {
		m.statusMessage = "✗ reverse_proxy edit unavailable: select a directive in writable mode"
		return m, nil
	}
	sel := m.selectedItem()
	planner := caddyfile.NewPlanner(sel.doc)
	fields, err := planner.GetReverseProxyFields(sel.node)
	if err != nil {
		m.statusMessage = "✗ reverse_proxy edit unavailable: " + err.Error()
		return m, nil
	}
	m.structuredAddInput = structuredInput{}
	m.structuredAddDoc = sel.doc
	m.structuredAddParent = sel.node
	m.structuredAddKey = sel.key
	m.structuredAddMode = structuredAddReverseProxy
	m.structuredAddName = "reverse_proxy"
	m.structuredAddItems = nil
	m.structuredAddCursor = 0
	m.structuredAddMatcher = structuredInput{value: []rune(fields.Matcher), cursor: len([]rune(fields.Matcher))}
	m.structuredAddUpstreams = structuredInput{value: []rune(strings.Join(fields.Upstreams, " "))}
	m.structuredAddUpstreams.cursor = len(m.structuredAddUpstreams.value)
	m.structuredAddRPField = structuredReverseProxyUpstreams
	m.structuredAddEditing = true
	m.showStructuredAdd = true
	m.statusMessage = ""
	return m, nil
}

// updateStructuredAddKey handles the text prompt and starts validation when
// the operator submits a directive line.
func (m *Model) updateStructuredAddKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.structuredAddMode == structuredAddPicker {
		return m.updateStructuredPickerKey(msg)
	}
	if m.structuredAddMode == structuredAddReverseProxy {
		return m.updateStructuredReverseProxyKey(msg)
	}
	switch msg.String() {
	case "esc":
		m.returnToStructuredPicker()
		return m, nil
	case "ctrl+c":
		m.closeStructuredAdd()
		m.statusMessage = "add cancelled"
		return m, nil
	case "enter":
		return m.submitStructuredAdd()
	}
	m.structuredAddInput.update(msg)
	return m, nil
}

func (m *Model) returnToStructuredPicker() {
	m.structuredAddMode = structuredAddPicker
	m.structuredAddName = ""
	m.structuredAddInput = structuredInput{}
	m.structuredAddMatcher = structuredInput{}
	m.structuredAddUpstreams = structuredInput{}
	m.structuredAddRPField = structuredReverseProxyUpstreams
	m.structuredAddEditing = false
	m.structuredAddCursor = 0
}

func (m *Model) closeStructuredAdd() {
	m.showStructuredAdd = false
	m.structuredAddInput = structuredInput{}
	m.structuredAddDoc = nil
	m.structuredAddParent = caddyfile.Node{}
	m.structuredAddKey = ""
	m.structuredAddMode = structuredAddPicker
	m.structuredAddName = ""
	m.structuredAddItems = nil
	m.structuredAddCursor = 0
	m.structuredAddMatcher = structuredInput{}
	m.structuredAddUpstreams = structuredInput{}
	m.structuredAddRPField = structuredReverseProxyUpstreams
	m.structuredAddEditing = false
}

func (m *Model) filteredStructuredItems() []string {
	query := strings.ToLower(strings.TrimSpace(m.structuredAddInput.String()))
	if query == "" {
		return append([]string(nil), m.structuredAddItems...)
	}
	var filtered []string
	for _, name := range m.structuredAddItems {
		meta := caddyfile.Catalog(name)
		if strings.Contains(strings.ToLower(name), query) ||
			(meta != nil && strings.Contains(strings.ToLower(meta.Description), query)) {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

func (m *Model) updateStructuredPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeStructuredAdd()
		m.statusMessage = "add cancelled"
		return m, nil
	case "up", "ctrl+k":
		if m.structuredAddCursor > 0 {
			m.structuredAddCursor--
		}
		return m, nil
	case "down", "ctrl+j":
		items := m.filteredStructuredItems()
		if m.structuredAddCursor < len(items)-1 {
			m.structuredAddCursor++
		}
		return m, nil
	case "ctrl+h":
		items := m.filteredStructuredItems()
		if len(items) == 0 {
			m.statusMessage = "✗ no supported directive is selected"
			return m, nil
		}
		if m.structuredAddCursor >= len(items) {
			m.structuredAddCursor = len(items) - 1
		}
		name := items[m.structuredAddCursor]
		return m.startHelpURL(caddyfileDirectiveHelpURL(m.structuredAddParent, name), name+" help")
	case "enter":
		items := m.filteredStructuredItems()
		if len(items) == 0 {
			m.statusMessage = "✗ no supported directives match the current filter"
			return m, nil
		}
		if m.structuredAddCursor >= len(items) {
			m.structuredAddCursor = len(items) - 1
		}
		m.structuredAddName = items[m.structuredAddCursor]
		m.structuredAddInput = structuredInput{}
		if m.structuredAddName == "reverse_proxy" {
			m.structuredAddMode = structuredAddReverseProxy
			m.structuredAddMatcher = structuredInput{}
			m.structuredAddUpstreams = structuredInput{}
			m.structuredAddRPField = structuredReverseProxyUpstreams
			m.structuredAddEditing = false
		} else {
			m.structuredAddMode = structuredAddArgs
		}
		m.statusMessage = ""
		return m, nil
	}
	m.structuredAddInput.update(msg)
	items := m.filteredStructuredItems()
	if len(items) == 0 {
		m.structuredAddCursor = 0
	} else if m.structuredAddCursor >= len(items) {
		m.structuredAddCursor = len(items) - 1
	}
	return m, nil
}

func (m *Model) updateStructuredReverseProxyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.returnToStructuredPicker()
		return m, nil
	case "ctrl+c":
		m.closeStructuredAdd()
		m.statusMessage = "add cancelled"
		return m, nil
	case "tab", "down":
		m.structuredAddRPField = structuredReverseProxyUpstreams
		return m, nil
	case "shift+tab", "up":
		m.structuredAddRPField = structuredReverseProxyMatcher
		return m, nil
	case "enter":
		if m.structuredAddRPField == structuredReverseProxyMatcher {
			m.structuredAddRPField = structuredReverseProxyUpstreams
			return m, nil
		}
		return m.submitStructuredAdd()
	}
	if m.structuredAddRPField == structuredReverseProxyMatcher {
		m.structuredAddMatcher.update(msg)
	} else {
		m.structuredAddUpstreams.update(msg)
	}
	return m, nil
}

func (m *Model) submitStructuredAdd() (tea.Model, tea.Cmd) {
	if m.structuredAddBusy {
		return m, nil
	}
	if m.structuredAddMode == structuredAddReverseProxy {
		return m.submitStructuredReverseProxy()
	}
	name := m.structuredAddName
	args := strings.TrimSpace(m.structuredAddInput.String())
	if name == "" {
		m.statusMessage = "✗ add requires a directive selection"
		return m, nil
	}
	if m.structuredAddDoc == nil {
		m.closeStructuredAdd()
		m.statusMessage = "✗ add failed: source document is unavailable"
		return m, nil
	}
	planner := caddyfile.NewPlanner(m.structuredAddDoc)
	edit, err := planner.Insert(m.structuredAddParent, caddyfile.DirectiveInsert{
		Name:     name,
		Args:     args,
		Position: caddyfile.InsertAtEnd,
	})
	if err != nil {
		m.statusMessage = "✗ add unavailable: " + err.Error()
		return m, nil
	}
	return m.queueStructuredAddValidation(name, "add", edit)
}

func (m *Model) submitStructuredReverseProxy() (tea.Model, tea.Cmd) {
	upstreamText := strings.TrimSpace(m.structuredAddUpstreams.String())
	if upstreamText == "" {
		m.statusMessage = "✗ reverse_proxy requires at least one upstream"
		return m, nil
	}
	if m.structuredAddDoc == nil {
		m.closeStructuredAdd()
		m.statusMessage = "✗ add failed: source document is unavailable"
		return m, nil
	}
	args := append([]string(nil), strings.Fields(upstreamText)...)
	if matcher := strings.TrimSpace(m.structuredAddMatcher.String()); matcher != "" {
		args = append([]string{matcher}, args...)
	}
	planner := caddyfile.NewPlanner(m.structuredAddDoc)
	var edit *caddyfile.PlannedEdit
	var err error
	if m.structuredAddEditing {
		edit, err = planner.SetReverseProxyFields(m.structuredAddParent, caddyfile.ReverseProxyFields{
			Matcher:   strings.TrimSpace(m.structuredAddMatcher.String()),
			Upstreams: strings.Fields(upstreamText),
		})
	} else {
		edit, err = planner.Insert(m.structuredAddParent, caddyfile.DirectiveInsert{
			Name:     "reverse_proxy",
			Args:     strings.Join(args, " "),
			Position: caddyfile.InsertAtEnd,
		})
	}
	if err != nil {
		m.statusMessage = "✗ reverse_proxy unavailable: " + err.Error()
		return m, nil
	}
	operation := "add"
	if m.structuredAddEditing {
		operation = "edit"
	}
	return m.queueStructuredAddValidation("reverse_proxy", operation, edit)
}

func (m *Model) queueStructuredAddValidation(name, operation string, edit *caddyfile.PlannedEdit) (tea.Model, tea.Cmd) {
	candidate, err := edit.Apply(m.structuredAddDoc.Source)
	if err != nil {
		m.statusMessage = "✗ add failed: " + err.Error()
		return m, nil
	}
	original := append([]byte(nil), m.structuredAddDoc.Source...)
	docPath := m.structuredAddDoc.Path
	parent := m.structuredAddParent
	itemKey := m.structuredAddKey
	m.closeStructuredAdd()
	m.structuredAddBusy = true
	m.statusMessage = "validating " + operation + "…"
	return m, m.structuredAddValidateCmd(docPath, original, candidate, name, operation, parent, itemKey)
}

func (m *Model) structuredAddValidateCmd(path string, original, candidate []byte, name, operation string, parent caddyfile.Node, itemKey string) tea.Cmd {
	timeout := m.validatorTimeout
	formatter := m.formatter
	return func() tea.Msg {
		ctx := context.Background()
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		formatted, diagnostics, err := formatter.FormatAndValidate(ctx, path, candidate)
		return structuredAddValidatedMsg{
			Path: path, Original: original, Content: candidate, Formatted: formatted,
			Diagnostics: diagnostics, Name: name, Operation: operation, Parent: parent,
			ItemKey: itemKey, Err: err,
		}
	}
}

func (m *Model) handleStructuredAddValidated(msg structuredAddValidatedMsg) (tea.Model, tea.Cmd) {
	m.structuredAddBusy = false
	var errorDiags []validator.Diagnostic
	for _, d := range msg.Diagnostics {
		if d.Severity == validator.SeverityError {
			errorDiags = append(errorDiags, d)
		}
	}
	if len(errorDiags) > 0 {
		m.diagnostics = errorDiags
		m.diagCursor = 0
		m.showDiagnostics = true
		m.statusMessage = "✗ structured " + msg.Operation + " did not validate — not applied"
		m.recordError("structured "+msg.Operation, "candidate did not validate", "fix the directive and retry the edit")
		return m, nil
	}
	if msg.Err != nil {
		m.statusMessage = "✗ structured " + msg.Operation + " validation failed: " + msg.Err.Error()
		m.recordError("structured "+msg.Operation, msg.Err.Error(), "fix the directive and retry the edit")
		return m, nil
	}
	if len(msg.Diagnostics) > 0 {
		m.statusMessage = "✗ structured " + msg.Operation + " has warnings — not applied"
		m.recordError("structured "+msg.Operation, "candidate has warnings", "review the warnings and retry the edit")
		return m, nil
	}
	content := msg.Content
	if msg.Formatted != nil {
		content = msg.Formatted
	}
	m.pendingEdit = &pendingEdit{
		path: msg.Path, original: msg.Original, content: content,
		nodeName: msg.Parent.Name, startLine: msg.Parent.Range.StartLine,
		itemKey: msg.ItemKey, operation: msg.Operation,
	}
	lines, err := diff.Unified(msg.Original, content, msg.Path, msg.Path+" (after "+msg.Operation+")")
	if err != nil {
		m.pendingEdit = nil
		m.statusMessage = "✗ structured " + msg.Operation + " diff failed: " + err.Error()
		m.recordError("structured "+msg.Operation+" diff", err.Error(), "retry the edit")
		return m, nil
	}
	title := "Add " + msg.Name
	if msg.Operation == "edit" {
		title = "Edit " + msg.Name
	}
	m.showDiffModal(lines, title+" · "+msg.Path)
	return m, nil
}

func (m *Model) structuredAddView(width, height int) string {
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
	contentW := boxW - 4
	if contentW < 1 {
		contentW = 1
	}
	target := truncateToWidth("Target: "+m.structuredAddParent.Name, contentW)
	if m.structuredAddMode == structuredAddReverseProxy {
		body := target + "\n\n" +
			structuredAddFieldLine("matcher> ", m.structuredAddMatcher, m.structuredAddRPField == structuredReverseProxyMatcher, contentW) + "\n" +
			structuredAddFieldLine("upstreams> ", m.structuredAddUpstreams, m.structuredAddRPField == structuredReverseProxyUpstreams, contentW) + "\n\n" +
			dimStyle.Render("Tab/↑↓ switch field · Enter continue/submit · Esc picker")
		return focusedPaneStyle.Width(boxW - 2).Height(boxH - 2).Render(
			activeTitleStyle.Render("Add reverse_proxy · Esc cancel") + "\n" + body,
		)
	}
	if m.structuredAddMode == structuredAddArgs {
		body := target + "\n\n" +
			truncateToWidth("args> "+m.structuredAddInput.View(), contentW) + "\n\n" +
			dimStyle.Render("Enter arguments only · Esc returns to directive picker")
		return focusedPaneStyle.Width(boxW - 2).Height(boxH - 2).Render(
			activeTitleStyle.Render("Add "+m.structuredAddName+" · Esc cancel") + "\n" + body,
		)
	}

	items := m.filteredStructuredItems()
	rows := boxH - 6
	if rows < 1 {
		rows = 1
	}
	start := m.structuredAddCursor - rows/2
	if start < 0 {
		start = 0
	}
	if start+rows > len(items) {
		start = len(items) - rows
	}
	if start < 0 {
		start = 0
	}
	end := start + rows
	if end > len(items) {
		end = len(items)
	}
	var body strings.Builder
	body.WriteString(target + "\n")
	body.WriteString(truncateToWidth("filter> "+m.structuredAddInput.View(), contentW) + "\n\n")
	if len(items) == 0 {
		body.WriteString(dimStyle.Render("no supported directives match this filter"))
	} else {
		rowWidth := max(1, contentW-2)
		for i := start; i < end; i++ {
			meta := caddyfile.Catalog(items[i])
			description := ""
			if meta != nil {
				description = meta.Description
			}
			body.WriteString(structuredPickerRow(items[i], description, i == m.structuredAddCursor, rowWidth))
			body.WriteByte('\n')
		}
	}
	title := truncateToWidth("Add directive · ↑/↓ choose · Ctrl-H help · Enter select · Esc cancel", contentW)
	return focusedPaneStyle.Width(boxW - 2).Height(boxH - 2).Render(
		activeTitleStyle.Render(title) + "\n" + body.String(),
	)
}

func structuredAddFieldLine(label string, input structuredInput, active bool, width int) string {
	value := input.String()
	if active {
		value = input.View()
	}
	prefix := "  "
	if active {
		prefix = "› "
	}
	return truncateToWidth(prefix+label+value, width)
}

// structuredPickerRow returns one bounded terminal row. Keeping the cursor,
// directive label and description within one explicit width prevents
// Lipgloss from wrapping a long catalog description into a second row.
func structuredPickerRow(name, description string, selected bool, width int) string {
	prefix := "  "
	if selected {
		prefix = "› "
	}
	textWidth := max(1, width-len(prefix))
	description = truncateToWidth(description, max(1, textWidth-17))
	line := fmt.Sprintf("%-16s %s", name, description)
	return prefix + truncateToWidth(line, textWidth)
}

func (m *Model) structuredAddOverlay(base string, width, height int) string {
	return m.modalOverlay(base, m.structuredAddView(width, height), width, height)
}
