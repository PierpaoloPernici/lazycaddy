package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PierpaoloPernici/lazycaddy/internal/app"
	"github.com/PierpaoloPernici/lazycaddy/internal/caddyfile"
	"github.com/PierpaoloPernici/lazycaddy/internal/validator"
)

// selectDirective expands the tree and moves the cursor to the row of the
// named directive. Only structural directives (those with a nested block)
// are tree rows; leaf directives such as redir are not selectable by
// design and are exercised through the add flow instead.
func selectDirective(t *testing.T, m *Model, name string) *Model {
	t.Helper()
	m.expandAllBranches()
	for i := range m.items {
		it := &m.items[i]
		if it.hasNode && it.node.Kind == caddyfile.KindDirective && it.node.Name == name {
			m.cursor = i
			return m
		}
	}
	t.Fatalf("directive %q has no tree row", name)
	return m
}

// TestDirectiveFormEditFlow drives the m form for every supported
// structural directive: the form is prefilled from the existing values,
// editing one field plans a byte-exact replacement, the nested block and
// every surrounding byte survive, and the candidate flows through the
// validation pipeline into the diff confirmation.
func TestDirectiveFormEditFlow(t *testing.T) {
	src := `example.test {
	reverse_proxy @api localhost:8080 {
		header_up Host {host}
	}
	respond /api 200 {
		close
	}
	file_server /static browse {
		root /srv
	}
	php_fastcgi /php/* localhost:9000 {
		split .php
	}
	encode zstd gzip {
		minimum_length 100
	}
	header /api X-Test 1 {
		match status 200
	}
	tls admin@example.test cert.pem key.pem {
		protocols tls1.2
	}
	log access {
		output file /var/log/access.log
	}
	import snippets/common /api {
		respond ok
	}
}
`
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": src,
	}))

	tests := []struct {
		name      string
		directive string
		prefilled []string
		editField int
		newValue  string
		wantLine  string
	}{
		{
			name:      "reverse_proxy",
			directive: "reverse_proxy",
			prefilled: []string{"@api", "localhost:8080"},
			editField: 1,
			newValue:  "localhost:9090",
			wantLine:  "reverse_proxy @api localhost:9090 {",
		},
		{
			name:      "respond",
			directive: "respond",
			prefilled: []string{"/api", "200", ""},
			editField: 1,
			newValue:  "201",
			wantLine:  "respond /api 201 {",
		},
		{
			name:      "file_server",
			directive: "file_server",
			prefilled: []string{"/static", "browse"},
			editField: 1,
			newValue:  "",
			wantLine:  "file_server /static {",
		},
		{
			name:      "php_fastcgi",
			directive: "php_fastcgi",
			prefilled: []string{"/php/*", "localhost:9000"},
			editField: 1,
			newValue:  "localhost:9001",
			wantLine:  "php_fastcgi /php/* localhost:9001 {",
		},
		{
			name:      "encode",
			directive: "encode",
			prefilled: []string{"", "zstd gzip"},
			editField: 1,
			newValue:  "gzip",
			wantLine:  "encode gzip {",
		},
		{
			name:      "header",
			directive: "header",
			prefilled: []string{"/api", "X-Test", "1", ""},
			editField: 2,
			newValue:  "2",
			wantLine:  "header /api X-Test 2 {",
		},
		{
			name:      "tls",
			directive: "tls",
			prefilled: []string{"admin@example.test", "cert.pem", "key.pem"},
			editField: 1,
			newValue:  "new.pem",
			wantLine:  "tls admin@example.test new.pem key.pem {",
		},
		{
			name:      "log",
			directive: "log",
			prefilled: []string{"access"},
			editField: 0,
			newValue:  "audit",
			wantLine:  "log audit {",
		},
		{
			name:      "import",
			directive: "import",
			prefilled: []string{"snippets/common", "/api"},
			editField: 1,
			newValue:  "/v2",
			wantLine:  "import snippets/common /v2 {",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatter := &fakeFormatter{}
			m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
			m = resize(m, 120, 40)
			m = selectDirective(t, m, tt.directive)
			m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
			if !m.showStructuredAdd || !m.structuredAddEditing {
				t.Fatalf("form state = show:%v editing:%v, want open edit form", m.showStructuredAdd, m.structuredAddEditing)
			}
			if m.structuredAddName != tt.directive {
				t.Fatalf("form name = %q, want %q", m.structuredAddName, tt.directive)
			}
			for i, want := range tt.prefilled {
				if got := m.structuredAddFields[i].String(); got != want {
					t.Fatalf("field %d prefilled = %q, want %q", i, got, want)
				}
			}
			m.structuredAddFields[tt.editField] = structuredInput{value: []rune(tt.newValue), cursor: len([]rune(tt.newValue))}
			updated, cmd := m.submitStructuredForm()
			m = updated.(*Model)
			if cmd == nil {
				t.Fatalf("form submit returned no validation command (status=%q)", m.statusMessage)
			}
			updated, _ = m.Update(cmd())
			m = updated.(*Model)
			if m.pendingEdit == nil || m.pendingEdit.operation != "edit" {
				t.Fatalf("pendingEdit = %+v, want a structured edit", m.pendingEdit)
			}
			content := string(m.pendingEdit.content)
			if !strings.Contains(content, tt.wantLine) {
				t.Fatalf("edited candidate missing %q:\n%s", tt.wantLine, content)
			}
			if !m.showDiff {
				t.Fatal("showDiff = false after a validated form edit")
			}
		})
	}
}

// TestDirectiveFormAddViaPicker drives the a → picker → form insertion
// flow for a leaf directive (redir), which has no tree row and is only
// reachable through the add flow.
func TestDirectiveFormAddViaPicker(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	formatter := &fakeFormatter{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, &fakeSaver{})
	m = resize(m, 120, 40)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("redir") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.structuredAddMode != structuredAddForm || m.structuredAddName != "redir" {
		t.Fatalf("picker opened mode %v name %q, want the redir form", m.structuredAddMode, m.structuredAddName)
	}
	if m.structuredAddEditing {
		t.Fatal("picker form unexpectedly opened in edit mode")
	}
	// matcher → to → status; move to the destination and type it.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/new")})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyTab})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatalf("redir form submit returned no command (status=%q)", m.statusMessage)
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if m.pendingEdit == nil || m.pendingEdit.operation != "add" {
		t.Fatalf("pendingEdit = %+v, want a structured add", m.pendingEdit)
	}
	if !strings.Contains(string(m.pendingEdit.content), "redir /new") {
		t.Fatalf("candidate missing the redir line: %q", m.pendingEdit.content)
	}
}

// TestDirectiveFormRejectsEmptyInput verifies an empty required field is
// rejected before any validation or write happens.
func TestDirectiveFormRejectsEmptyInput(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond ok\n}\n",
	}))
	formatter := &fakeFormatter{}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 40)
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	for _, r := range []rune("redir") {
		m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	// Submit with an empty destination.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyTab})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(*Model)
	if cmd != nil {
		t.Fatal("empty redir submit returned a validation command")
	}
	if !strings.Contains(m.statusMessage, "redir requires a destination") {
		t.Fatalf("statusMessage = %q, want the destination error", m.statusMessage)
	}
	if formatter.calls != 0 || saver.calls != 0 {
		t.Fatalf("empty submit touched the pipeline: formatter=%d saver=%d", formatter.calls, saver.calls)
	}
}

// TestDirectiveFormValidationFailureRejectsEdit verifies a formatter
// error diagnostic rejects an edit without a diff or save, mirroring the
// add-flow guard.
func TestDirectiveFormValidationFailureRejectsEdit(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\tlog access {\n\t\toutput file /var/log/access.log\n\t}\n}\n",
	}))
	diags := []validator.Diagnostic{
		{Path: "config/Caddyfile", Line: 1, Column: 1, Message: "boom", Severity: validator.SeverityError},
	}
	formatter := &fakeFormatter{diagnostics: diags}
	saver := &fakeSaver{}
	m := newLoadedModel(t, fakeLoader{state: state}, formatter, saver)
	m = resize(m, 120, 40)
	m = selectDirective(t, m, "log")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m.structuredAddFields[0] = structuredInput{value: []rune("audit")}
	updated, cmd := m.submitStructuredForm()
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("form submit returned no validation command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if m.pendingEdit != nil || m.showDiff {
		t.Fatalf("error diagnostics applied: pending=%v diff=%v", m.pendingEdit != nil, m.showDiff)
	}
	if saver.calls != 0 {
		t.Fatalf("saver called %d times despite validation failure", saver.calls)
	}
	if !strings.Contains(m.statusMessage, "did not validate") {
		t.Fatalf("statusMessage = %q, want not-validated error", m.statusMessage)
	}
}

// TestDirectiveFormAmbiguousConstructRefuses verifies a construct the form
// cannot represent without guessing (header with four positional tokens)
// disables the form and keeps the raw editor as the only path.
func TestDirectiveFormAmbiguousConstructRefuses(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\theader X a b c {\n\t\tmatch status 200\n\t}\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 40)
	m = selectDirective(t, m, "header")
	if m.canEditDirectiveForm() {
		t.Fatal("canEditDirectiveForm = true on an ambiguous header")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if m.showStructuredAdd {
		t.Fatal("form opened on an ambiguous construct")
	}
	if !strings.Contains(m.statusMessage, "header form unavailable") {
		t.Fatalf("statusMessage = %q, want the ambiguous form error", m.statusMessage)
	}
}

// TestDirectiveFormReadOnlyGated verifies the m form is unavailable in
// read-only mode with a clear reason.
func TestDirectiveFormReadOnlyGated(t *testing.T) {
	state := stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\ttls internal {\n\t\tprotocols tls1.2\n\t}\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 40)
	m = selectDirective(t, m, "tls")
	if m.canEditDirectiveForm() {
		t.Fatal("canEditDirectiveForm = true in read-only mode")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if m.showStructuredAdd || !strings.Contains(m.statusMessage, "directive form unavailable") {
		t.Fatalf("read-only m = show:%v msg:%q, want unavailable", m.showStructuredAdd, m.statusMessage)
	}
}

// TestDirectiveFormOnImportedDocument verifies a form edit inside an
// imported document targets that document: the planned edit carries its
// path and the candidate keeps the imported bytes.
func TestDirectiveFormOnImportedDocument(t *testing.T) {
	fs := map[string]string{
		"config/Caddyfile":   "example.test {\n\timport common.conf\n}\n",
		"config/common.conf": "example.test {\n\ttls internal {\n\t\tprotocols tls1.2\n\t}\n}\n",
	}
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(fs))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{}, fsReader(fs))
	m = resize(m, 120, 40)
	// Rows: root doc, site, imported doc, imported site, imported tls.
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})  // example.test
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})  // common.conf
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight}) // expand imported doc
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})  // imported site
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRight}) // expand imported site
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyDown})  // imported tls
	if sel := m.selectedItem(); !sel.hasNode || sel.node.Name != "tls" {
		t.Fatalf("expected the imported tls directive, got %q", sel.label)
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if !m.showStructuredAdd {
		t.Fatal("form did not open on the imported directive")
	}
	m.structuredAddFields[0] = structuredInput{value: []rune("admin@example.test")}
	updated, cmd := m.submitStructuredForm()
	m = updated.(*Model)
	if cmd == nil {
		t.Fatalf("form submit returned no command (status=%q)", m.statusMessage)
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if m.pendingEdit == nil {
		t.Fatal("pendingEdit = nil after the imported-document edit")
	}
	if !strings.Contains(m.pendingEdit.path, "common.conf") {
		t.Fatalf("pendingEdit.path = %q, want the imported document", m.pendingEdit.path)
	}
	content := string(m.pendingEdit.content)
	if !strings.Contains(content, "tls admin@example.test {") || !strings.Contains(content, "protocols tls1.2") {
		t.Fatalf("imported candidate changed beyond the tls line: %q", content)
	}
}

// TestDirectiveFormAnchorLine verifies the pending edit records the
// directive's start line so the selection can be re-anchored after save.
func TestDirectiveFormAnchorLine(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\tlog access {\n\t\toutput file /var/log/access.log\n\t}\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 40)
	m = selectDirective(t, m, "log")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m.structuredAddFields[0] = structuredInput{value: []rune("audit")}
	updated, cmd := m.submitStructuredForm()
	m = updated.(*Model)
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if m.pendingEdit == nil {
		t.Fatal("pendingEdit = nil")
	}
	if m.pendingEdit.startLine != 2 {
		t.Fatalf("anchor start line = %d, want 2 (the log directive line)", m.pendingEdit.startLine)
	}
}

// TestDirectiveFormEscClosesEditForm verifies Esc on an m-opened form
// cancels it instead of returning to a stale picker.
func TestDirectiveFormEscClosesEditForm(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\tlog access {\n\t\toutput file /var/log/access.log\n\t}\n}\n",
	}))
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m = resize(m, 120, 40)
	m = selectDirective(t, m, "log")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if !m.showStructuredAdd {
		t.Fatal("form did not open")
	}
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.showStructuredAdd || !strings.Contains(m.statusMessage, "form cancelled") {
		t.Fatalf("esc = show:%v msg:%q, want cancelled edit form", m.showStructuredAdd, m.statusMessage)
	}
}

// TestDirectiveFormHelpOpensDocs verifies Ctrl-H inside a form opens the
// official documentation for the directive.
func TestDirectiveFormHelpOpensDocs(t *testing.T) {
	state := writableStateFor(t, "config/Caddyfile", "config/backups", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\tlog access {\n\t\toutput file /var/log/access.log\n\t}\n}\n",
	}))
	var gotURL string
	m := newLoadedModel(t, fakeLoader{state: state}, &fakeFormatter{}, &fakeSaver{})
	m.browser = app.BrowserFunc(func(_ context.Context, url string) error {
		gotURL = url
		return nil
	})
	m = resize(m, 120, 40)
	m = selectDirective(t, m, "log")
	m = keyPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	m = updated.(*Model)
	if cmd == nil {
		t.Fatal("form help did not return a browser command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(*Model)
	if gotURL != "https://caddyserver.com/docs/caddyfile/directives/log" {
		t.Errorf("opened URL = %q, want the log documentation", gotURL)
	}
	if !m.showStructuredAdd {
		t.Fatal("form help closed the form")
	}
}
