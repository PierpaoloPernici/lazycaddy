package ui

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sort"
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
	structuredAddForm
	structuredAddNewPicker
	structuredAddNewForm
	structuredAddReorder
	structuredAddCommentPlacement
)

// structuredAddCommentEntry is the synthetic directive-picker entry that
// opens the comment-placement flow from a top-level block. It is not a
// Caddy directive and never reaches the planner.
const structuredAddCommentEntry = "comment"

// Comment placement picker entries.
const (
	commentPlacementTop    = "at the top of the file"
	commentPlacementBottom = "at the bottom of the file"
	commentPlacementBefore = "before this block"
	commentPlacementAfter  = "after this block"
)

// commentTemplate seeds the editor temp file for a comment insertion.
const commentTemplate = "# \n"

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

// formAvailableForSelection reports whether the selected row is a
// directive that has a dedicated structured form. It is the cheap gate
// used by the command palette to hide the form command on unsupported
// rows; canEditDirectiveForm adds the writable-mode and ambiguity checks.
func (m *Model) formAvailableForSelection() bool {
	if m.state == nil || m.state.Graph == nil {
		return false
	}
	sel := m.selectedItem()
	return sel != nil && sel.hasNode && sel.doc != nil && formSupported(sel.node.Name)
}

// canEditDirectiveForm reports whether the selected directive can be
// edited through its dedicated structured form: the model must be
// writable with a formatter and saver, the selection must be a supported
// directive, and the planner must be able to read the construct without
// ambiguity. Ambiguous constructs disable the form and keep the raw
// editor available.
func (m *Model) canEditDirectiveForm() bool {
	if m.state == nil || m.state.Graph == nil || m.state.Settings.ReadOnly ||
		m.saver == nil || m.formatter == nil || m.structuredAddBusy ||
		m.busy || m.saving || m.editing || m.deleting || m.reloading || m.rollingBack {
		return false
	}
	sel := m.selectedItem()
	if sel == nil || !sel.hasNode || sel.doc == nil || !formSupported(sel.node.Name) {
		return false
	}
	planner := caddyfile.NewPlanner(sel.doc)
	_, err := loadFormValues(planner, sel.node)
	return err == nil
}

// canAddComment reports whether the selected row supports comment
// insertion with a: a document row (header/footer placement), a top-level
// block (before/after placement) or a comment group (append after).
func (m *Model) canAddComment() bool {
	if m.state == nil || m.state.Graph == nil || m.state.Settings.ReadOnly ||
		m.saver == nil || m.formatter == nil || m.structuredAddBusy ||
		m.busy || m.saving || m.editing || m.deleting || m.reloading || m.rollingBack {
		return false
	}
	sel := m.selectedItem()
	if sel == nil || sel.doc == nil {
		return false
	}
	if sel.comment != nil {
		return true
	}
	if !sel.hasNode {
		// Only the document row itself offers header/footer placement:
		// the virtual comments branch is a container row, not a document.
		return sel.key == itemKey(sel.doc, nil)
	}
	return isTopLevelNode(sel.doc, sel.node)
}

// isTopLevelNode reports whether n is a direct child of its document, by
// stable key.
func isTopLevelNode(doc *caddyfile.Document, n caddyfile.Node) bool {
	if doc == nil {
		return false
	}
	want := nodeKey(&n)
	for i := range doc.Nodes {
		if nodeKey(&doc.Nodes[i]) == want {
			return true
		}
	}
	return false
}

// structuredAddDirectiveItems returns the directive picker items for the
// current parent, plus the synthetic comment entry for top-level blocks
// (comments inside nested blocks are not first-class groups). The result
// is sorted so the comment entry sits at its alphabetical position.
func (m *Model) structuredAddDirectiveItems() []string {
	items := caddyfile.InsertableDirectiveNames(m.structuredAddParent)
	if m.structuredAddDoc != nil && isTopLevelNode(m.structuredAddDoc, m.structuredAddParent) {
		items = append(items, structuredAddCommentEntry)
	}
	sort.Strings(items)
	return items
}

// startStructuredAdd opens the context-aware insertion flow. The planner,
// rather than the UI, remains authoritative about the selected context: a
// supported block opens the directive picker, a top-level block adds a
// comment entry to it, a document row offers comment placement at the
// file header/footer, and a comment group appends a new group after it.
func (m *Model) startStructuredAdd() (tea.Model, tea.Cmd) {
	if !m.canAddStructured() && !m.canAddComment() {
		m.statusMessage = "✗ add unavailable: select a supported block, document or comment in writable mode"
		return m, nil
	}
	sel := m.selectedItem()
	m.structuredAddInput = structuredInput{}
	m.structuredAddDoc = sel.doc
	m.structuredAddParent = sel.node
	m.structuredAddKey = sel.key
	m.structuredAddName = ""
	m.structuredAddCursor = 0
	m.structuredAddFields = nil
	m.structuredAddFieldLabels = nil
	m.structuredAddFieldCursor = 0
	m.structuredAddEditing = false
	m.structuredAddPlacementFromPicker = false
	m.showStructuredAdd = true
	m.statusMessage = ""
	// The structured-add modal is an unrelated workflow: any active text
	// selection is dropped.
	m.clearTextSelection()

	if sel.comment != nil {
		// Append a new group right after this one: the single placement
		// needs no picker, so the editor opens directly.
		m.closeStructuredAdd()
		return m.startCommentInsert(sel.doc, sel.comment.Range.End, commentTemplate)
	}
	if !sel.hasNode {
		// A document row hosts no directives: offer only comment
		// placement at the file header or footer.
		m.structuredAddMode = structuredAddCommentPlacement
		m.structuredAddItems = []string{commentPlacementTop, commentPlacementBottom}
		return m, nil
	}
	// A node row: the context-aware directive picker, plus a comment
	// entry for top-level blocks (comments inside nested blocks are not
	// first-class groups).
	m.structuredAddMode = structuredAddPicker
	m.structuredAddItems = m.structuredAddDirectiveItems()
	return m, nil
}

func (m *Model) startDirectiveForm() (tea.Model, tea.Cmd) {
	return m.openStructuredForm()
}

// structuredAddFieldLabel returns the label for a form field, falling
// back to a numbered placeholder when the label list is missing (for
// example in stale modal state).
func (m *Model) structuredAddFieldLabel(i int) string {
	if i >= 0 && i < len(m.structuredAddFieldLabels) {
		return m.structuredAddFieldLabels[i]
	}
	return "field> "
}

// updateStructuredAddKey handles the text prompt and starts validation when
// the operator submits a directive line.
func (m *Model) updateStructuredAddKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.structuredAddCreating {
		return m.updateNewNodeKey(msg)
	}
	if m.structuredAddMode == structuredAddReorder {
		return m.updateReorderKey(msg)
	}
	if m.structuredAddMode == structuredAddCommentPlacement {
		return m.updateCommentPlacementKey(msg)
	}
	if m.structuredAddMode == structuredAddPicker {
		return m.updateStructuredPickerKey(msg)
	}
	if msg.String() == "ctrl+h" {
		return m.startHelpURL(
			caddyfileDirectiveHelpURL(m.structuredAddParent, m.structuredAddName),
			m.structuredAddName+" help",
		)
	}
	if m.structuredAddMode == structuredAddForm {
		return m.updateStructuredFormKey(msg)
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
	m.structuredAddFields = nil
	m.structuredAddFieldLabels = nil
	m.structuredAddFieldCursor = 0
	m.structuredAddEditing = false
	m.structuredAddCursor = 0
	// Restore the directive items: the placement sub-picker replaced
	// them, and the form/args flows left them empty.
	m.structuredAddItems = m.structuredAddDirectiveItems()
	m.structuredAddPlacementFromPicker = false
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
	m.structuredAddFields = nil
	m.structuredAddFieldLabels = nil
	m.structuredAddFieldCursor = 0
	m.structuredAddEditing = false
	m.structuredAddCreating = false
	m.structuredAddNewKind = caddyfile.Kind(0)
	m.structuredAddNewName = structuredInput{}
	m.structuredAddNewArgs = structuredInput{}
	m.structuredAddNewField = newNodeNameField
	m.structuredAddNewTop = false
	m.structuredAddReorderTargets = nil
	m.structuredAddPlacementFromPicker = false
}

func (m *Model) filteredStructuredItems() []string {
	query := strings.ToLower(strings.TrimSpace(m.structuredAddInput.String()))
	if query == "" {
		return append([]string(nil), m.structuredAddItems...)
	}
	var filtered []string
	for _, name := range m.structuredAddItems {
		meta := caddyfile.Catalog(name)
		description := ""
		if meta != nil {
			description = meta.Description
		}
		if name == structuredAddCommentEntry {
			description = "insert a comment before or after this block"
		}
		if strings.Contains(strings.ToLower(name), query) ||
			(description != "" && strings.Contains(strings.ToLower(description), query)) {
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
		if name == structuredAddCommentEntry {
			m.statusMessage = "comments are free-form annotations — no documentation page"
			return m, nil
		}
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
		name := items[m.structuredAddCursor]
		if name == structuredAddCommentEntry {
			m.structuredAddMode = structuredAddCommentPlacement
			m.structuredAddItems = []string{commentPlacementBefore, commentPlacementAfter}
			m.structuredAddCursor = 0
			m.structuredAddInput = structuredInput{}
			m.structuredAddPlacementFromPicker = true
			m.statusMessage = ""
			return m, nil
		}
		m.structuredAddName = name
		m.structuredAddInput = structuredInput{}
		if formSupported(name) {
			// Dedicated structured form: empty fields, insertion target
			// is the selected block.
			m.startFormModal(m.structuredAddDoc, m.structuredAddParent, m.structuredAddKey, name, nil, false)
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

func (m *Model) updateStructuredFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.structuredAddEditing {
			m.closeStructuredAdd()
			m.statusMessage = m.structuredAddName + " form cancelled"
			return m, nil
		}
		m.returnToStructuredPicker()
		return m, nil
	case "ctrl+c":
		m.closeStructuredAdd()
		m.statusMessage = "add cancelled"
		return m, nil
	case "tab", "down":
		if m.structuredAddFieldCursor < len(m.structuredAddFields)-1 {
			m.structuredAddFieldCursor++
		}
		return m, nil
	case "shift+tab", "up":
		if m.structuredAddFieldCursor > 0 {
			m.structuredAddFieldCursor--
		}
		return m, nil
	case "enter":
		if m.structuredAddFieldCursor < len(m.structuredAddFields)-1 {
			m.structuredAddFieldCursor++
			return m, nil
		}
		return m.submitStructuredForm()
	}
	if m.structuredAddFieldCursor < 0 || m.structuredAddFieldCursor >= len(m.structuredAddFields) {
		return m, nil
	}
	m.structuredAddFields[m.structuredAddFieldCursor].update(msg)
	return m, nil
}

// updateCommentPlacementKey handles the placement sub-picker of a comment
// insertion: file header/footer on a document row, before/after on a
// top-level block. Enter computes the insertion offset and opens the
// editor; Esc returns to the directive picker when the placement came
// from one, otherwise it cancels.
func (m *Model) updateCommentPlacementKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.structuredAddPlacementFromPicker {
			m.returnToStructuredPicker()
		} else {
			m.closeStructuredAdd()
			m.statusMessage = "add cancelled"
		}
		return m, nil
	case "ctrl+c":
		m.closeStructuredAdd()
		m.statusMessage = "add cancelled"
		return m, nil
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
		return m.submitCommentPlacement()
	}
	return m, nil
}

// submitCommentPlacement resolves the chosen placement to an insertion
// offset and opens the editor with the comment template. Inserting at the
// file header skips a leading byte order mark so the comment never lands
// before it; every other byte outside the zero-length insertion range is
// preserved.
func (m *Model) submitCommentPlacement() (tea.Model, tea.Cmd) {
	if m.structuredAddDoc == nil || m.structuredAddCursor < 0 || m.structuredAddCursor >= len(m.structuredAddItems) {
		return m, nil
	}
	doc := m.structuredAddDoc
	placement := m.structuredAddItems[m.structuredAddCursor]
	var pos int
	switch placement {
	case commentPlacementTop:
		pos = 0
		if bytes.HasPrefix(doc.Source, []byte{0xEF, 0xBB, 0xBF}) {
			pos = 3
		}
	case commentPlacementBottom:
		pos = len(doc.Source)
	case commentPlacementBefore:
		pos = m.structuredAddParent.Range.Start
	case commentPlacementAfter:
		pos = m.structuredAddParent.Range.End
	default:
		m.statusMessage = "✗ comment insert failed: unknown placement"
		return m, nil
	}
	m.closeStructuredAdd()
	return m.startCommentInsert(doc, pos, commentTemplate)
}

func (m *Model) submitStructuredAdd() (tea.Model, tea.Cmd) {
	if m.structuredAddBusy {
		return m, nil
	}
	if m.structuredAddMode == structuredAddForm {
		return m.submitStructuredForm()
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
	anchorStartLine := parent.Range.StartLine
	// A reorder edit records the exact post-edit line of the moved block, so
	// the selection can be re-anchored to the moved node after save even when
	// several siblings share the same directive name.
	if edit.NewStartLine > 0 {
		anchorStartLine = edit.NewStartLine
	}
	m.closeStructuredAdd()
	m.structuredAddBusy = true
	m.statusMessage = "validating " + structuredOperationLabel(operation) + "…"
	return m, m.structuredAddValidateCmd(docPath, original, candidate, name, operation, parent, itemKey, anchorStartLine)
}

func (m *Model) structuredAddValidateCmd(path string, original, candidate []byte, name, operation string, parent caddyfile.Node, itemKey string, anchorStartLines ...int) tea.Cmd {
	anchorStartLine := parent.Range.StartLine
	if len(anchorStartLines) > 0 {
		anchorStartLine = anchorStartLines[0]
	}
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
			ItemKey: itemKey, AnchorStartLine: anchorStartLine, Err: err,
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
		m.clearTextSelection()
		m.statusMessage = "✗ structured " + structuredOperationLabel(msg.Operation) + " did not validate — not applied"
		m.recordError("structured "+structuredOperationLabel(msg.Operation), "candidate did not validate", "fix the directive and retry the edit")
		return m, nil
	}
	if msg.Err != nil {
		m.statusMessage = "✗ structured " + structuredOperationLabel(msg.Operation) + " validation failed: " + msg.Err.Error()
		m.recordError("structured "+structuredOperationLabel(msg.Operation), msg.Err.Error(), "fix the directive and retry the edit")
		return m, nil
	}
	if len(msg.Diagnostics) > 0 {
		m.statusMessage = "✗ structured " + structuredOperationLabel(msg.Operation) + " has warnings — not applied"
		m.recordError("structured "+structuredOperationLabel(msg.Operation), "candidate has warnings", "review the warnings and retry the edit")
		return m, nil
	}
	content := msg.Content
	if msg.Formatted != nil {
		content = msg.Formatted
	}
	m.pendingEdit = &pendingEdit{
		path: msg.Path, original: msg.Original, content: content,
		nodeName: msg.Parent.Name, startLine: msg.AnchorStartLine,
		itemKey: msg.ItemKey, operation: msg.Operation,
	}
	lines, err := diff.Unified(msg.Original, content, msg.Path, msg.Path+" (after "+structuredOperationLabel(msg.Operation)+")")
	if err != nil {
		m.pendingEdit = nil
		m.statusMessage = "✗ structured " + msg.Operation + " diff failed: " + err.Error()
		m.recordError("structured "+msg.Operation+" diff", err.Error(), "retry the edit")
		return m, nil
	}
	title := "Add " + msg.Name
	if msg.Operation == "edit" {
		title = "Edit " + msg.Name
	} else if msg.Operation == "new" {
		title = "New " + msg.Name
	} else if msg.Operation == "reorder" {
		title = "Move " + msg.Name
	}
	m.showDiffModal(lines, title+" · "+msg.Path)
	return m, nil
}

func structuredOperationLabel(operation string) string {
	if operation == "new" {
		return "new node"
	}
	if operation == "reorder" {
		return "move after"
	}
	return operation
}

func (m *Model) structuredAddView(width, height int) string {
	if m.structuredAddCreating {
		return m.newNodeView(width, height)
	}
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
	if m.structuredAddMode == structuredAddCommentPlacement {
		targetName := filepath.Base(m.structuredAddDoc.Path)
		if m.structuredAddPlacementFromPicker {
			targetName = nodeLabel(m.structuredAddParent)
		}
		target = truncateToWidth("Target: "+targetName, contentW)
		rows := boxH - 6
		if rows < 1 {
			rows = 1
		}
		start := m.structuredAddCursor - rows/2
		if start < 0 {
			start = 0
		}
		if start+rows > len(m.structuredAddItems) {
			start = len(m.structuredAddItems) - rows
		}
		if start < 0 {
			start = 0
		}
		end := start + rows
		if end > len(m.structuredAddItems) {
			end = len(m.structuredAddItems)
		}
		var body strings.Builder
		body.WriteString(target + "\n\n")
		body.WriteString(dimStyle.Render("Insert comment:") + "\n")
		for i := start; i < end; i++ {
			prefix := "  "
			if i == m.structuredAddCursor {
				prefix = "› "
			}
			body.WriteString(truncateToWidth(prefix+m.structuredAddItems[i], contentW) + "\n")
		}
		body.WriteString("\n" + dimStyle.Render("↑/↓ choose · Enter open editor · Esc cancel"))
		return focusedPaneStyle.Width(boxW - 2).Height(boxH - 2).Render(
			activeTitleStyle.Render("Insert comment · Esc cancel") + "\n" + body.String(),
		)
	}
	if m.structuredAddMode == structuredAddReorder {
		rows := boxH - 7
		if rows < 1 {
			rows = 1
		}
		start := m.structuredAddCursor - rows/2
		if start < 0 {
			start = 0
		}
		if start+rows > len(m.structuredAddReorderTargets) {
			start = len(m.structuredAddReorderTargets) - rows
		}
		if start < 0 {
			start = 0
		}
		end := start + rows
		if end > len(m.structuredAddReorderTargets) {
			end = len(m.structuredAddReorderTargets)
		}
		var body strings.Builder
		body.WriteString(truncateToWidth("Selected: "+nodeLabel(m.structuredAddParent), contentW) + "\n\n")
		body.WriteString(dimStyle.Render("Move after sibling:") + "\n")
		for i := start; i < end; i++ {
			prefix := "  "
			if i == m.structuredAddCursor {
				prefix = "› "
			}
			body.WriteString(truncateToWidth(prefix+nodeLabel(m.structuredAddReorderTargets[i]), contentW) + "\n")
		}
		body.WriteString("\n" + dimStyle.Render("↑/↓ choose · Enter move after · Esc cancel"))
		return focusedPaneStyle.Width(boxW - 2).Height(boxH - 2).Render(
			activeTitleStyle.Render("Move block after") + "\n" + body.String(),
		)
	}
	if m.structuredAddMode == structuredAddForm {
		title := "Add " + m.structuredAddName
		if m.structuredAddEditing {
			title = "Edit " + m.structuredAddName
		}
		var body strings.Builder
		body.WriteString(target + "\n\n")
		for i, field := range m.structuredAddFields {
			label := m.structuredAddFieldLabel(i)
			body.WriteString(structuredAddFieldLine(label, field, m.structuredAddFieldCursor == i, contentW) + "\n")
		}
		body.WriteString("\n" + dimStyle.Render("Tab/↑↓ switch field · Enter continue/submit · Esc cancel"))
		return focusedPaneStyle.Width(boxW - 2).Height(boxH - 2).Render(
			activeTitleStyle.Render(title+" · Esc cancel") + "\n" + body.String(),
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
			if items[i] == structuredAddCommentEntry {
				description = "insert a comment before or after this block"
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
