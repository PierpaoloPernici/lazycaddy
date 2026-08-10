package ui

import (
	"context"
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

// startStructuredAdd opens the raw one-line directive prompt. The planner,
// rather than the UI, decides whether the requested directive is supported in
// the selected context.
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
	m.showStructuredAdd = true
	m.statusMessage = ""
	return m, nil
}

// updateStructuredAddKey handles the text prompt and starts validation when
// the operator submits a directive line.
func (m *Model) updateStructuredAddKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeStructuredAdd()
		m.statusMessage = "add cancelled"
		return m, nil
	case "enter":
		return m.submitStructuredAdd()
	}
	m.structuredAddInput.update(msg)
	return m, nil
}

func (m *Model) closeStructuredAdd() {
	m.showStructuredAdd = false
	m.structuredAddInput = structuredInput{}
	m.structuredAddDoc = nil
	m.structuredAddParent = caddyfile.Node{}
	m.structuredAddKey = ""
}

func splitStructuredDirective(line string) (name, args string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, "\r\n") {
		return "", "", false
	}
	idx := strings.IndexAny(line, " \t")
	if idx < 0 {
		return line, "", true
	}
	return line[:idx], strings.TrimSpace(line[idx:]), true
}

func (m *Model) submitStructuredAdd() (tea.Model, tea.Cmd) {
	if m.structuredAddBusy {
		return m, nil
	}
	name, args, ok := splitStructuredDirective(m.structuredAddInput.String())
	if !ok {
		m.statusMessage = "✗ add requires one directive line"
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
	m.statusMessage = "validating add…"
	return m, m.structuredAddValidateCmd(docPath, original, candidate, name, parent, itemKey)
}

func (m *Model) structuredAddValidateCmd(path string, original, candidate []byte, name string, parent caddyfile.Node, itemKey string) tea.Cmd {
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
			Diagnostics: diagnostics, Name: name, Parent: parent,
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
		m.statusMessage = "✗ structured add did not validate — not applied"
		m.recordError("structured add", "candidate did not validate", "fix the directive and retry the add")
		return m, nil
	}
	if msg.Err != nil {
		m.statusMessage = "✗ structured add validation failed: " + msg.Err.Error()
		m.recordError("structured add", msg.Err.Error(), "fix the directive and retry the add")
		return m, nil
	}
	if len(msg.Diagnostics) > 0 {
		m.statusMessage = "✗ structured add has warnings — not applied"
		m.recordError("structured add", "candidate has warnings", "review the warnings and retry the add")
		return m, nil
	}
	content := msg.Content
	if msg.Formatted != nil {
		content = msg.Formatted
	}
	m.pendingEdit = &pendingEdit{
		path: msg.Path, original: msg.Original, content: content,
		nodeName: msg.Parent.Name, startLine: msg.Parent.Range.StartLine,
		itemKey: msg.ItemKey, operation: "add",
	}
	lines, err := diff.Unified(msg.Original, content, msg.Path, msg.Path+" (after add)")
	if err != nil {
		m.pendingEdit = nil
		m.statusMessage = "✗ structured add diff failed: " + err.Error()
		m.recordError("structured add diff", err.Error(), "retry the add")
		return m, nil
	}
	m.showDiffModal(lines, "Add "+msg.Name+" · "+msg.Path)
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
	boxH := 8
	if height < boxH {
		boxH = height
	}
	contentW := boxW - 4
	if contentW < 1 {
		contentW = 1
	}
	parent := m.structuredAddParent.Name
	body := "Target: " + parent + "\n\n" + "directive> " + m.structuredAddInput.View() + "\n\n" + dimStyle.Render("Example: reverse_proxy localhost:8080 · Enter plans and validates")
	return focusedPaneStyle.Width(boxW - 2).Height(boxH - 2).Render(activeTitleStyle.Render("Add structured directive · Esc cancel") + "\n" + body)
}

func (m *Model) structuredAddOverlay(base string, width, height int) string {
	return m.modalOverlay(base, m.structuredAddView(width, height), width, height)
}
