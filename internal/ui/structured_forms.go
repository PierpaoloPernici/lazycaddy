package ui

import (
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
)

// formField is one editable field of a structured directive form. The
// label is shown verbatim before the field value in the modal.
type formField struct {
	label string
}

// directiveForm describes one structured directive form. The planner
// remains authoritative for parsing and byte-level edits; the form is only
// the presentation of the typed values.
type directiveForm struct {
	fields []formField
}

// directiveForms lists the directives with a dedicated structured form.
// Every entry must also be handled by loadFormValues and the per-directive
// planning functions in this file. Directives without a form keep the raw
// $EDITOR path (e/E) and are never hidden or rewritten.
var directiveForms = map[string]directiveForm{
	"reverse_proxy": {fields: []formField{{label: "matcher> "}, {label: "upstreams> "}}},
	"respond":       {fields: []formField{{label: "matcher> "}, {label: "status> "}, {label: "body> "}}},
	"redir":         {fields: []formField{{label: "matcher> "}, {label: "to> "}, {label: "status> "}}},
	"file_server":   {fields: []formField{{label: "matcher> "}, {label: "mode (browse)> "}}},
	"php_fastcgi":   {fields: []formField{{label: "matcher> "}, {label: "gateways> "}}},
	"encode":        {fields: []formField{{label: "matcher> "}, {label: "formats> "}}},
	"header":        {fields: []formField{{label: "matcher> "}, {label: "field> "}, {label: "value/find> "}, {label: "replace> "}}},
	"tls":           {fields: []formField{{label: "email/internal> "}, {label: "cert file> "}, {label: "key file> "}}},
	"log":           {fields: []formField{{label: "logger name> "}}},
	"import":        {fields: []formField{{label: "pattern> "}, {label: "args> "}}},
}

// formSupported reports whether the directive name has a dedicated
// structured form.
func formSupported(name string) bool {
	_, ok := directiveForms[name]
	return ok
}

// loadFormValues reads the current positional values of a directive node
// through the planner and returns them in the form's field order. Errors
// from GetXFields (ambiguous or unsupported constructs) propagate so the
// UI can disable the form and keep the raw editor as the only path.
func loadFormValues(p *caddyfile.Planner, n caddyfile.Node) ([]string, error) {
	switch n.Name {
	case "reverse_proxy":
		f, err := p.GetReverseProxyFields(n)
		if err != nil {
			return nil, err
		}
		return []string{f.Matcher, strings.Join(f.Upstreams, " ")}, nil
	case "respond":
		f, err := p.GetRespondFields(n)
		if err != nil {
			return nil, err
		}
		return []string{f.Matcher, f.Status, f.Body}, nil
	case "redir":
		f, err := p.GetRedirFields(n)
		if err != nil {
			return nil, err
		}
		return []string{f.Matcher, f.To, f.Status}, nil
	case "file_server":
		f, err := p.GetFileServerFields(n)
		if err != nil {
			return nil, err
		}
		browse := ""
		if f.Browse {
			browse = "browse"
		}
		return []string{f.Matcher, browse}, nil
	case "php_fastcgi":
		f, err := p.GetPhpFastcgiFields(n)
		if err != nil {
			return nil, err
		}
		return []string{f.Matcher, strings.Join(f.Upstreams, " ")}, nil
	case "encode":
		f, err := p.GetEncodeFields(n)
		if err != nil {
			return nil, err
		}
		return []string{f.Matcher, strings.Join(f.Formats, " ")}, nil
	case "header":
		f, err := p.GetHeaderFields(n)
		if err != nil {
			return nil, err
		}
		return []string{f.Matcher, f.Field, f.Value, f.Replace}, nil
	case "tls":
		f, err := p.GetTlsFields(n)
		if err != nil {
			return nil, err
		}
		return []string{f.Email, f.CertFile, f.KeyFile}, nil
	case "log":
		f, err := p.GetLogFields(n)
		if err != nil {
			return nil, err
		}
		return []string{f.Name}, nil
	case "import":
		f, err := p.GetImportFields(n)
		if err != nil {
			return nil, err
		}
		return []string{f.Pattern, strings.Join(f.Args, " ")}, nil
	}
	return nil, errors.New("unsupported directive for structured form")
}

// openStructuredForm opens the dedicated form for the selected directive,
// prefilled from its current values. Ambiguous constructs refuse the form
// with the planner's specific reason so the raw editor remains the only
// way to touch them.
func (m *Model) openStructuredForm() (tea.Model, tea.Cmd) {
	sel := m.selectedItem()
	if m.state == nil || m.state.Graph == nil || m.state.Settings.ReadOnly ||
		m.saver == nil || m.formatter == nil || m.structuredAddBusy ||
		m.busy || m.saving || m.editing || m.deleting || m.reloading || m.rollingBack ||
		sel == nil || !sel.hasNode || sel.doc == nil || !formSupported(sel.node.Name) {
		m.statusMessage = "✗ directive form unavailable: select a supported directive in writable mode"
		return m, nil
	}
	planner := caddyfile.NewPlanner(sel.doc)
	values, err := loadFormValues(planner, sel.node)
	if err != nil {
		m.statusMessage = "✗ " + sel.node.Name + " form unavailable: " + err.Error()
		return m, nil
	}
	m.startFormModal(sel.doc, sel.node, sel.key, sel.node.Name, values, true)
	return m, nil
}

// startFormModal opens the generic form modal for a directive, prefilled
// from an existing directive (editing) or empty for insertion. The caller
// has already resolved the target document and parent node.
func (m *Model) startFormModal(doc *caddyfile.Document, parent caddyfile.Node, itemKey, name string, values []string, editing bool) {
	form := directiveForms[name]
	m.structuredAddInput = structuredInput{}
	m.structuredAddDoc = doc
	m.structuredAddParent = parent
	m.structuredAddKey = itemKey
	m.structuredAddMode = structuredAddForm
	m.structuredAddName = name
	m.structuredAddItems = nil
	m.structuredAddCursor = 0
	m.structuredAddFields = make([]structuredInput, len(form.fields))
	m.structuredAddFieldLabels = make([]string, len(form.fields))
	for i, field := range form.fields {
		value := ""
		if i < len(values) {
			value = values[i]
		}
		m.structuredAddFields[i] = structuredInput{value: []rune(value), cursor: len([]rune(value))}
		m.structuredAddFieldLabels[i] = field.label
	}
	m.structuredAddFieldCursor = 0
	m.structuredAddEditing = editing
	m.showStructuredAdd = true
	m.statusMessage = ""
	// The form is an unrelated workflow: any active text selection is
	// dropped.
	m.clearTextSelection()
}

// structuredFormValues returns the trimmed text of each form field.
func (m *Model) structuredFormValues() []string {
	values := make([]string, len(m.structuredAddFields))
	for i := range m.structuredAddFields {
		values[i] = strings.TrimSpace(m.structuredAddFields[i].String())
	}
	return values
}

// submitStructuredForm validates the typed values and routes the result
// through the normal validation/diff/save pipeline, either as an edit of
// the selected directive or as an insertion into the selected block.
func (m *Model) submitStructuredForm() (tea.Model, tea.Cmd) {
	if m.structuredAddBusy {
		return m, nil
	}
	name := m.structuredAddName
	if m.structuredAddDoc == nil {
		m.closeStructuredAdd()
		m.statusMessage = "✗ form failed: source document is unavailable"
		return m, nil
	}
	values := m.structuredFormValues()
	planner := caddyfile.NewPlanner(m.structuredAddDoc)
	var edit *caddyfile.PlannedEdit
	var err error
	switch name {
	case "reverse_proxy":
		edit, err = m.planReverseProxyForm(planner, values)
	case "respond":
		edit, err = m.planRespondForm(planner, values)
	case "redir":
		edit, err = m.planRedirForm(planner, values)
	case "file_server":
		edit, err = m.planFileServerForm(planner, values)
	case "php_fastcgi":
		edit, err = m.planPhpFastcgiForm(planner, values)
	case "encode":
		edit, err = m.planEncodeForm(planner, values)
	case "header":
		edit, err = m.planHeaderForm(planner, values)
	case "tls":
		edit, err = m.planTlsForm(planner, values)
	case "log":
		edit, err = m.planLogForm(planner, values)
	case "import":
		edit, err = m.planImportForm(planner, values)
	default:
		err = errors.New("unsupported directive for structured form")
	}
	if err != nil {
		m.statusMessage = "✗ " + name + " form rejected: " + err.Error()
		return m, nil
	}
	operation := "add"
	if m.structuredAddEditing {
		operation = "edit"
	}
	return m.queueStructuredAddValidation(name, operation, edit)
}

// formEditOrInsert plans either a SetXFields edit of the selected
// directive or an Insert of a new directive line with the same argument
// text, depending on whether the form was opened on an existing directive.
func (m *Model) formEditOrInsert(planner *caddyfile.Planner, name, args string, planSet func(*caddyfile.Planner) (*caddyfile.PlannedEdit, error)) (*caddyfile.PlannedEdit, error) {
	if m.structuredAddEditing {
		return planSet(planner)
	}
	return planner.Insert(m.structuredAddParent, caddyfile.DirectiveInsert{
		Name:     name,
		Args:     args,
		Position: caddyfile.InsertAtEnd,
	})
}

func (m *Model) planReverseProxyForm(p *caddyfile.Planner, v []string) (*caddyfile.PlannedEdit, error) {
	if v[1] == "" {
		return nil, errors.New("reverse_proxy requires at least one upstream")
	}
	fields := caddyfile.ReverseProxyFields{Matcher: v[0], Upstreams: caddyfile.SplitFieldTokens(v[1])}
	return m.formEditOrInsert(p, "reverse_proxy", joinFormArgs(append([]string{fields.Matcher}, fields.Upstreams...)...), func(p *caddyfile.Planner) (*caddyfile.PlannedEdit, error) {
		return p.SetReverseProxyFields(m.structuredAddParent, fields)
	})
}

func (m *Model) planRespondForm(p *caddyfile.Planner, v []string) (*caddyfile.PlannedEdit, error) {
	if v[1] == "" && v[2] == "" {
		return nil, errors.New("respond requires a status code or a body")
	}
	fields := caddyfile.RespondFields{Matcher: v[0], Status: v[1], Body: v[2]}
	// The documented grammar orders a body before its status code.
	return m.formEditOrInsert(p, "respond", joinFormArgs(fields.Matcher, fields.Body, fields.Status), func(p *caddyfile.Planner) (*caddyfile.PlannedEdit, error) {
		return p.SetRespondFields(m.structuredAddParent, fields)
	})
}

func (m *Model) planRedirForm(p *caddyfile.Planner, v []string) (*caddyfile.PlannedEdit, error) {
	if v[1] == "" {
		return nil, errors.New("redir requires a destination")
	}
	fields := caddyfile.RedirFields{Matcher: v[0], To: v[1], Status: v[2]}
	return m.formEditOrInsert(p, "redir", joinFormArgs(fields.Matcher, fields.To, fields.Status), func(p *caddyfile.Planner) (*caddyfile.PlannedEdit, error) {
		return p.SetRedirFields(m.structuredAddParent, fields)
	})
}

func (m *Model) planFileServerForm(p *caddyfile.Planner, v []string) (*caddyfile.PlannedEdit, error) {
	if v[1] != "" && v[1] != "browse" {
		return nil, errors.New(`file_server mode must be "browse"`)
	}
	fields := caddyfile.FileServerFields{Matcher: v[0], Browse: v[1] == "browse"}
	args := fields.Matcher
	if fields.Browse {
		args = joinFormArgs(args, "browse")
	}
	return m.formEditOrInsert(p, "file_server", args, func(p *caddyfile.Planner) (*caddyfile.PlannedEdit, error) {
		return p.SetFileServerFields(m.structuredAddParent, fields)
	})
}

func (m *Model) planPhpFastcgiForm(p *caddyfile.Planner, v []string) (*caddyfile.PlannedEdit, error) {
	if v[1] == "" {
		return nil, errors.New("php_fastcgi requires at least one gateway")
	}
	fields := caddyfile.PhpFastcgiFields{Matcher: v[0], Upstreams: caddyfile.SplitFieldTokens(v[1])}
	return m.formEditOrInsert(p, "php_fastcgi", joinFormArgs(append([]string{fields.Matcher}, fields.Upstreams...)...), func(p *caddyfile.Planner) (*caddyfile.PlannedEdit, error) {
		return p.SetPhpFastcgiFields(m.structuredAddParent, fields)
	})
}

func (m *Model) planEncodeForm(p *caddyfile.Planner, v []string) (*caddyfile.PlannedEdit, error) {
	fields := caddyfile.EncodeFields{Matcher: v[0], Formats: caddyfile.SplitFieldTokens(v[1])}
	return m.formEditOrInsert(p, "encode", joinFormArgs(append([]string{fields.Matcher}, fields.Formats...)...), func(p *caddyfile.Planner) (*caddyfile.PlannedEdit, error) {
		return p.SetEncodeFields(m.structuredAddParent, fields)
	})
}

func (m *Model) planHeaderForm(p *caddyfile.Planner, v []string) (*caddyfile.PlannedEdit, error) {
	if v[1] == "" && (v[2] != "" || v[3] != "") {
		return nil, errors.New("header value and replacement require a field")
	}
	fields := caddyfile.HeaderFields{Matcher: v[0], Field: v[1], Value: v[2], Replace: v[3]}
	return m.formEditOrInsert(p, "header", joinFormArgs(fields.Matcher, fields.Field, fields.Value, fields.Replace), func(p *caddyfile.Planner) (*caddyfile.PlannedEdit, error) {
		return p.SetHeaderFields(m.structuredAddParent, fields)
	})
}

func (m *Model) planTlsForm(p *caddyfile.Planner, v []string) (*caddyfile.PlannedEdit, error) {
	if (v[1] == "") != (v[2] == "") {
		return nil, errors.New("tls requires both a certificate file and a key file, or neither")
	}
	fields := caddyfile.TlsFields{Email: v[0], CertFile: v[1], KeyFile: v[2]}
	return m.formEditOrInsert(p, "tls", joinFormArgs(fields.Email, fields.CertFile, fields.KeyFile), func(p *caddyfile.Planner) (*caddyfile.PlannedEdit, error) {
		return p.SetTlsFields(m.structuredAddParent, fields)
	})
}

func (m *Model) planLogForm(p *caddyfile.Planner, v []string) (*caddyfile.PlannedEdit, error) {
	fields := caddyfile.LogFields{Name: v[0]}
	return m.formEditOrInsert(p, "log", fields.Name, func(p *caddyfile.Planner) (*caddyfile.PlannedEdit, error) {
		return p.SetLogFields(m.structuredAddParent, fields)
	})
}

func (m *Model) planImportForm(p *caddyfile.Planner, v []string) (*caddyfile.PlannedEdit, error) {
	if v[0] == "" {
		return nil, errors.New("import requires a pattern")
	}
	fields := caddyfile.ImportFields{Pattern: v[0], Args: caddyfile.SplitFieldTokens(v[1])}
	return m.formEditOrInsert(p, "import", joinFormArgs(append([]string{fields.Pattern}, fields.Args...)...), func(p *caddyfile.Planner) (*caddyfile.PlannedEdit, error) {
		return p.SetImportFields(m.structuredAddParent, fields)
	})
}

// joinFormArgs joins non-empty raw tokens with single spaces, mirroring
// the planner's SetXFields argument rendering for the insert path.
func joinFormArgs(parts ...string) string {
	var out []string
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, " ")
}
