package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

// readOnlyKeys are managed by the server and never shown in edit forms, in
// addition to models.SystemManagedFieldKeys.
var readOnlyKeys = map[string]bool{
	"asset_uuid": true, "room_uuid": true, "location_uuid": true, "created_by_user_id": true,
	"deleted": true, "locked_by": true, "archived_at": true, "scan_time": true,
	"scanned_by_user_id": true, "asset_like_counter": true, "inventory_time": true, "inventory_user": true,
}

// editableTypes can be edited with a text input, select or confirm.
var editableTypes = map[models.FieldTypeName]bool{
	models.FieldTypeText: true, models.FieldTypeLongText: true, models.FieldTypeBarcode: true,
	models.FieldTypeLink: true, models.FieldTypeNumber: true, models.FieldTypeDecimal: true,
	models.FieldTypeMoney: true, models.FieldTypeDate: true, models.FieldTypeDatetime: true,
	models.FieldTypeDropdown: true, models.FieldTypeBoolean: true,
	models.FieldTypeLinkedLocation: true, models.FieldTypeLinkedRoom: true, models.FieldTypeLinkedPerson: true,
	models.FieldTypeLinkedUser: true,
}

// commonKeys are shown in the short form (besides mandatory fields and
// fields that already have a value). E/N open the form with every field.
var commonKeys = map[models.AssetTrackingTemplate][]string{
	models.AssetTrackingTemplateAsset: {"inventory_name", "description", "manufacturer", "inventory_group",
		"purchasing_price", "purchasing_date", "link", "actual_building", "actual_room"},
	models.AssetTrackingTemplateRoom:   {"name", "number", "building_id", "additional_information"},
	models.AssetTrackingTemplatePerson: {"first_name", "last_name", "email", "department"},
}

// fieldsPerGroup is how many fields a form page shows.
const fieldsPerGroup = 7

// editField is one field in a record form.
type editField struct {
	key, label string
	kind       models.FieldTypeName
	required   bool
	options    []string     // dropdown values (label = value)
	source     choiceSource // records offered as choices (loaded before the form opens)
	choices    [][2]string  // select options as (label, value)
	noneLabel  string       // label of the empty choice of optional selects
	orig       string       // stringified original value ("" when creating)
	text       *string
	flag       *bool

	// check adds validation that depends on other fields; get returns the
	// current text of another field.
	check func(s string, get func(key string) string) error
	get   func(key string) string
}

// recordForm edits a record; it knows how to turn answers into a body.
type recordForm struct {
	title  string
	fields []*editField
	form   *huh.Form
}

// fieldsFromDefinitions picks editable fields from a template schema.
// Mandatory fields come first. Unless all is set, only mandatory, common
// and already-filled fields are included.
func fieldsFromDefinitions(defs []models.FieldDefinition, tmpl models.AssetTrackingTemplate, current Item, all bool) []*editField {
	var mandatory, rest []*editField
	for _, d := range defs {
		if models.SystemManagedFieldKeys[d.FieldKey] || readOnlyKeys[d.FieldKey] || !editableTypes[d.FieldType.Name] {
			continue
		}
		if !all && !d.IsMandatory() && !slices.Contains(commonKeys[tmpl], d.FieldKey) && str(current[d.FieldKey]) == "" {
			continue
		}
		f := &editField{key: d.FieldKey, label: fieldLabel(d), kind: d.FieldType.Name, required: d.IsMandatory(),
			source: linkedSources[d.FieldType.Name]}
		if vals, ok := d.FieldType.AllowedValues(); ok {
			for _, v := range vals {
				f.options = append(f.options, str(v))
			}
		}
		if f.required {
			mandatory = append(mandatory, f)
		} else {
			rest = append(rest, f)
		}
	}
	fields := slices.Concat(mandatory, rest)
	for _, f := range fields {
		f.orig = str(current[f.key])
	}
	return fields
}

// fieldLabel prefers a human label; built-in fields carry translation keys
// such as "itexia_scancode", so those show the field key instead.
func fieldLabel(d models.FieldDefinition) string {
	l := strings.TrimSpace(d.Label)
	if l == "" || l == d.FieldKey || !strings.Contains(l, " ") {
		return d.FieldKey
	}
	return fmt.Sprintf("%s (%s)", l, d.FieldKey)
}

func fieldsFromFixed(fixed []formField, current Item) []*editField {
	var out []*editField
	for _, ff := range fixed {
		if ff.createOnly && current != nil {
			continue
		}
		out = append(out, &editField{key: ff.key, label: ff.label, kind: ff.kind, required: ff.required,
			orig: str(current[ff.key]), source: ff.source, noneLabel: ff.noneLabel, check: ff.check})
	}
	return out
}

func (f *editField) validate(s string) error {
	s = strings.TrimSpace(s)
	if f.check != nil && f.get != nil {
		if err := f.check(s, f.get); err != nil {
			return err
		}
	}
	if s == "" {
		if f.required {
			return fmt.Errorf("%s is required", f.label)
		}
		return nil
	}
	switch f.kind {
	case models.FieldTypeNumber, models.FieldTypeLinkedLocation, models.FieldTypeLinkedRoom,
		models.FieldTypeLinkedPerson, models.FieldTypeLinkedUser:
		if _, err := strconv.Atoi(s); err != nil {
			return fmt.Errorf("must be a whole number (ID)")
		}
	case models.FieldTypeDecimal, models.FieldTypeMoney:
		if _, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64); err != nil {
			return fmt.Errorf("must be a number")
		}
	case models.FieldTypeDate:
		if _, err := time.Parse(time.DateOnly, s); err != nil {
			return fmt.Errorf("use YYYY-MM-DD")
		}
	case kindPath:
		if _, err := os.Stat(expandHome(s)); err != nil {
			return fmt.Errorf("file not found")
		}
	case models.FieldTypeDatetime:
		valid := false
		for _, layout := range []string{time.DateOnly, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"} {
			if _, err := time.Parse(layout, s); err == nil {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("use a valid YYYY-MM-DD or YYYY-MM-DDTHH:MM:SS (optional timezone)")
		}
	}
	return nil
}

// convert turns form text into the JSON value the API expects.
func (f *editField) convert(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	switch f.kind {
	case kindUserList:
		return []string{s}
	case kindObjectRef:
		return s // resolved to a reference list by the resource's create
	case models.FieldTypeNumber, models.FieldTypeLinkedLocation, models.FieldTypeLinkedRoom,
		models.FieldTypeLinkedPerson, models.FieldTypeLinkedUser:
		n, _ := strconv.Atoi(s)
		return n
	case models.FieldTypeDecimal, models.FieldTypeMoney:
		x, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
		return x
	}
	return s
}

func newRecordForm(title string, fields []*editField, width int, heights ...int) *recordForm {
	height := 30
	if len(heights) > 0 {
		height = heights[0]
	}
	rf := &recordForm{title: title, fields: fields}
	for _, f := range fields {
		f.get = rf.value
	}
	var groups []*huh.Group
	var cur []huh.Field
	used, budget := 0, max(height-9, 4)
	flush := func() {
		if len(cur) > 0 {
			groups = append(groups, huh.NewGroup(cur...))
			cur = nil
			used = 0
		}
	}
	for _, f := range fields {
		cost := 3
		if f.kind == models.FieldTypeLongText {
			cost = 5
		} else if len(f.choices) > 6 || len(f.options) > 6 {
			cost = 10
		}
		if len(cur) > 0 && used+cost > budget {
			flush()
		}
		used += cost
		initial := f.orig
		if f.text != nil {
			initial = *f.text
		}
		label := f.label
		if f.required {
			label += " *"
		}
		switch {
		case f.kind == models.FieldTypeBoolean:
			v := f.orig == "true"
			if f.flag != nil {
				v = *f.flag
			}
			f.flag = &v
			cur = append(cur, huh.NewConfirm().Key(f.key).Title(label).Value(f.flag))
		case len(f.choices) > 0:
			v := initial
			f.text = &v
			var opts []huh.Option[string]
			if !f.required {
				none := f.noneLabel
				if none == "" {
					none = "(none)"
				}
				opts = append(opts, huh.NewOption(none, ""))
			}
			for _, c := range f.choices {
				opts = append(opts, huh.NewOption(c[0], c[1]))
			}
			if v == "" && f.required {
				v = f.choices[0][1]
			}
			sel := huh.NewSelect[string]().Key(f.key).Title(label).Options(opts...).Value(f.text).Validate(f.validate)
			if len(opts) > 6 {
				sel.Height(8).Description("↑↓ to scroll, / to filter")
			}
			cur = append(cur, sel)
		case len(f.options) > 0:
			v := initial
			f.text = &v
			var opts []huh.Option[string]
			if !f.required {
				opts = append(opts, huh.NewOption("(none)", ""))
			}
			for _, o := range f.options {
				opts = append(opts, huh.NewOption(o, o))
			}
			cur = append(cur, huh.NewSelect[string]().Key(f.key).Title(label).Options(opts...).Value(f.text).Validate(f.validate).Height(min(len(opts), 8)))
		case f.kind == models.FieldTypeLongText:
			v := initial
			f.text = &v
			cur = append(cur, huh.NewText().Key(f.key).Title(label).Value(f.text).Lines(3).Validate(f.validate))
		default:
			v := initial
			f.text = &v
			field := f
			in := huh.NewInput().Key(f.key).Title(label).Value(f.text).Validate(field.validate)
			if hint := kindHint(f.kind); hint != "" {
				in.Placeholder(hint)
			}
			cur = append(cur, in)
		}
		if len(cur) == fieldsPerGroup {
			flush()
		}
	}
	flush()
	for i, g := range groups {
		if len(groups) > 1 {
			g.Title(fmt.Sprintf("%s (%d/%d)", title, i+1, len(groups)))
		} else {
			g.Title(title)
		}
	}
	rf.form = huh.NewForm(groups...).WithWidth(width).WithHeight(max(height-6, 5)).WithShowHelp(true).WithTheme(formTheme)
	return rf
}

func kindHint(k models.FieldTypeName) string {
	switch k {
	case models.FieldTypeDate:
		return "YYYY-MM-DD"
	case models.FieldTypeDatetime:
		return "YYYY-MM-DDTHH:MM:SS"
	case models.FieldTypeLinkedLocation:
		return "location ID"
	case models.FieldTypeLinkedRoom:
		return "room ID"
	case models.FieldTypeLinkedPerson:
		return "person ID"
	case models.FieldTypeLinkedUser:
		return "user ID"
	case models.FieldTypeMoney, models.FieldTypeDecimal, models.FieldTypeNumber:
		return "number"
	}
	return ""
}

// value returns the current text of the field with key.
func (rf *recordForm) value(key string) string {
	for _, f := range rf.fields {
		if f.key == key && f.text != nil {
			return strings.TrimSpace(*f.text)
		}
	}
	return ""
}

// body returns the request body. For edits only changed fields are sent;
// for creates only non-empty ones.
func (rf *recordForm) body(isEdit bool) Item {
	out := Item{}
	for _, f := range rf.fields {
		var val any
		var changed bool
		if f.flag != nil {
			val = *f.flag
			// An unset boolean that stays false is not a change.
			changed = strconv.FormatBool(*f.flag) != f.orig && (f.orig != "" || *f.flag)
		} else {
			val = f.convert(*f.text)
			changed = strings.TrimSpace(*f.text) != f.orig
		}
		if isEdit {
			if changed {
				out[f.key] = val
			}
		} else if val != nil && val != false {
			out[f.key] = val
		}
	}
	return out
}

// loginForm collects credentials.
type loginForm struct {
	url, clientID, username, password string
	form                              *huh.Form
}

func newLoginForm(url, clientID, username string, width int) *loginForm {
	lf := &loginForm{url: url, clientID: clientID, username: username}
	nonEmpty := func(name string) func(string) error {
		return func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("%s is required", name)
			}
			return nil
		}
	}
	lf.form = huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Instance URL").Placeholder("https://acme.seventhings.com").Value(&lf.url).Validate(func(s string) error {
			if !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "http://") {
				return fmt.Errorf("must start with https://")
			}
			return nil
		}),
		huh.NewInput().Title("Client ID").Value(&lf.clientID).Validate(nonEmpty("client ID")),
		huh.NewInput().Title("Username").Value(&lf.username).Validate(nonEmpty("username")),
		huh.NewInput().Title("Password").EchoMode(huh.EchoModePassword).Value(&lf.password).Validate(nonEmpty("password")),
	).Title("Log in to seventhings")).WithWidth(width).WithShowHelp(true).WithTheme(formTheme)
	return lf
}

// pathForm asks for a single file path (upload source or download target).
type pathForm struct {
	path  string
	title string
	form  *huh.Form
}

func newPathForm(title, initial string, mustExist bool, width int) *pathForm {
	pf := &pathForm{title: title, path: initial}
	pf.form = huh.NewForm(huh.NewGroup(
		huh.NewInput().Title(title).Value(&pf.path).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("path is required")
			}
			if mustExist {
				if _, err := os.Stat(expandHome(s)); err != nil {
					return fmt.Errorf("file not found")
				}
			}
			return nil
		}),
	)).WithWidth(width).WithShowHelp(true).WithTheme(formTheme)
	return pf
}

func expandHome(p string) string {
	p = strings.TrimSpace(p)
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

func uploadFile(ctx context.Context, cl *client.Client, path string) (string, error) {
	f, err := os.Open(expandHome(path))
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return cl.FileUpload(ctx, filepath.Base(path), f)
}

func (m *Model) formWidth() int { return max(min(m.width-4, 90), 30) }

func (m *Model) formHeight() int { return max(m.height-m.chrome(), 5) }

func (m *Model) openLogin() {
	_, url, clientID, username := m.deps.Profile()
	m.openLoginWith(url, clientID, username)
}

func (m *Model) openLoginWith(url, clientID, username string) {
	m.login = newLoginForm(url, clientID, username, m.formWidth())
	m.form = m.login.form.WithHeight(m.formHeight())
	m.formKind = formLogin
	m.mode = modeLogin
}

func (m *Model) openPathForm(kind formKind, title, initial string, mustExist bool) {
	m.navigate()
	m.path = newPathForm(title, initial, mustExist, m.formWidth())
	m.form = m.path.form.WithHeight(m.formHeight())
	m.formKind = kind
	if m.mode != modeForm {
		m.prev = m.mode
	}
	m.mode = modeForm
}

func (m *Model) startCreate() tea.Cmd {
	m.navigate()
	m.prev = m.mode
	return m.prepareRecordForm(nil)
}

func (m *Model) startEdit(it Item) tea.Cmd {
	m.navigate()
	return m.prepareRecordForm(it)
}

// withDefs runs next once the field definitions of tmpl are loaded.
func (m *Model) withDefs(tmpl models.AssetTrackingTemplate, next func() tea.Cmd) tea.Cmd {
	if _, ok := m.defs[tmpl]; ok || tmpl == "" {
		return next()
	}
	return m.prepare(func(ctx context.Context, cl *client.Client) tea.Msg {
		defs, err := cl.FieldDefinitionsList(ctx, tmpl)
		return defsMsg{template: tmpl, defs: defs, err: err, next: next}
	})
}

// withChoices loads the picker options fields need, then runs next.
func (m *Model) withChoices(fields []*editField, next func() tea.Cmd) tea.Cmd {
	var need []choiceSource
	for _, f := range fields {
		if _, cached := m.choiceCache[f.source]; f.source != "" && !cached && !slices.Contains(need, f.source) {
			need = append(need, f.source)
		}
	}
	if len(need) == 0 {
		return next()
	}
	return m.prepare(func(ctx context.Context, cl *client.Client) tea.Msg {
		loaded := map[choiceSource][][2]string{}
		for _, src := range need {
			c, err := loadChoices(ctx, cl, src)
			switch {
			case errors.Is(err, errTooManyChoices):
				loaded[src] = nil // fall back to typing the ID
			case err != nil:
				return choicesMsg{err: fmt.Errorf("load %s: %w", src, err)}
			default:
				loaded[src] = c
			}
		}
		return choicesMsg{loaded: loaded, next: next}
	})
}

// applyChoices attaches cached picker options to fields.
func (m *Model) applyChoices(fields []*editField) error {
	for _, f := range fields {
		if f.source == "" {
			continue
		}
		f.choices = m.choiceCache[f.source]
		if len(f.choices) == 0 && f.required && f.kind != models.FieldTypeLinkedUser &&
			f.kind != models.FieldTypeLinkedPerson && f.kind != models.FieldTypeLinkedLocation && f.kind != models.FieldTypeLinkedRoom {
			return fmt.Errorf("no %s available for %s", f.source, f.label)
		}
	}
	return nil
}

func (m *Model) buildFields(r *resource, edit Item) []*editField {
	var fields []*editField
	if r.template != "" {
		fields = fieldsFromDefinitions(m.defs[r.template], r.template, edit, m.allFields)
	} else {
		fields = fieldsFromFixed(r.fixed, edit)
	}
	if edit == nil {
		for _, f := range fields {
			if v, ok := m.prefill[f.key]; ok {
				f.orig = v
			}
		}
	}
	return fields
}

// prepareRecordForm loads field definitions and picker options, then opens
// the create (edit == nil) or edit form.
func (m *Model) prepareRecordForm(edit Item) tea.Cmd {
	r := m.res[m.tab]
	return m.withDefs(r.template, func() tea.Cmd {
		fields := m.buildFields(r, edit)
		return m.withChoices(fields, func() tea.Cmd { return m.openRecordForm(edit) })
	})
}

func (m *Model) openRecordForm(edit Item) tea.Cmd {
	r := m.res[m.tab]
	fields := m.buildFields(r, edit)
	m.prefill = nil
	if err := m.applyChoices(fields); err != nil {
		m.mode = m.prev
		return m.fail(err)
	}
	title := "New " + r.singular
	m.formKind = formCreate
	if edit != nil {
		title = fmt.Sprintf("Edit %s %q", r.singular, displayName(edit, r.id(edit)))
		m.formKind = formEdit
	}
	m.formTarget = edit
	m.record = newRecordForm(title, fields, m.formWidth(), m.height)
	m.form = m.record.form
	m.mode = modeForm
	return m.form.Init()
}

// openCustomForm shows an action form; submit receives the answers.
func (m *Model) openCustomForm(title string, fields []*editField, submit func(rf *recordForm) tea.Cmd) tea.Cmd {
	return m.withChoices(fields, func() tea.Cmd {
		if err := m.applyChoices(fields); err != nil {
			return m.fail(err)
		}
		if m.mode != modeForm {
			m.prev = m.mode
		}
		m.record = newRecordForm(title, fields, m.formWidth(), m.height)
		m.form = m.record.form
		m.formKind = formCustom
		m.custom = submit
		m.mode = modeForm
		return m.form.Init()
	})
}

// ask shows a y/N confirmation and runs cmd on "y".
func (m *Model) ask(prompt string, cmd func() tea.Cmd) {
	// A confirmation replaces the current view, but a submitted custom form
	// still needs its recovery snapshot if the confirmed operation fails.
	recovery := m.recovery
	m.navigate()
	m.recovery = recovery
	if m.mode != modeConfirm {
		m.prev = m.mode
	}
	m.mode = modeConfirm
	m.confirmPrompt = prompt
	m.confirmCmd = cmd
}

func (m *Model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.form == nil {
		return m, nil
	}
	fm, cmd := m.form.Update(msg)
	if f, ok := fm.(*huh.Form); ok {
		m.form = f
	}
	switch m.form.State {
	case huh.StateAborted:
		if m.mode == modeLogin {
			return m, tea.Quit
		}
		m.navigate()
		m.form = nil
		m.mode = m.prev
		m.setStatus("Cancelled", false)
		return m, nil
	case huh.StateCompleted:
		return m, m.submitForm()
	}
	return m, cmd
}

func (m *Model) submitForm() tea.Cmd {
	r := m.res[m.tab]
	kind := m.formKind
	if kind == formCreate || kind == formEdit || kind == formCustom {
		for _, f := range m.record.fields {
			if f.text != nil {
				if err := f.validate(*f.text); err != nil {
					return m.fail(err)
				}
			}
		}
	}
	if kind == formCreate || kind == formEdit || kind == formUpload || kind == formDownload || kind == formCustom {
		m.rememberForm()
	}
	m.form = nil
	if kind != formLogin {
		m.mode = m.prev
	}
	switch kind {
	case formLogin:
		m.operation = "Logging in…"
		lf := m.login
		return m.async(0, func() tea.Msg {
			return loginMsg{err: m.deps.Login(m.ctx, strings.TrimSpace(lf.url), strings.TrimSpace(lf.clientID), strings.TrimSpace(lf.username), lf.password)}
		})
	case formCreate:
		m.operation = "Creating " + r.singular + "…"
		body := m.record.body(false)
		return m.run(func(ctx context.Context, cl *client.Client) tea.Msg {
			id, err := r.create(ctx, cl, body)
			return doneMsg{text: fmt.Sprintf("Created %s %s", r.singular, id), err: err, reload: true}
		})
	case formEdit:
		m.operation = "Saving " + r.singular + "…"
		body := m.record.body(true)
		if len(body) == 0 {
			m.recovery, m.operation = nil, ""
			m.setStatus("No changes", false)
			return nil
		}
		id := r.id(m.formTarget)
		return m.run(func(ctx context.Context, cl *client.Client) tea.Msg {
			err := r.update(ctx, cl, id, body)
			return doneMsg{text: fmt.Sprintf("Updated %s (%d field(s))", r.singular, len(body)), err: err, reload: true}
		})
	case formUpload:
		m.operation = "Uploading file…"
		path := m.path.path
		return m.run(func(ctx context.Context, cl *client.Client) tea.Msg {
			id, err := r.upload(ctx, cl, path)
			return doneMsg{text: fmt.Sprintf("Uploaded %s as %s", filepath.Base(path), id), err: err, reload: true}
		})
	case formCustom:
		return m.custom(m.record)
	case formPageSize, formRateLimit:
		m.submitSetting(kind)
		return nil
	case formFilter:
		return m.applyFilter()
	case formProfile:
		return m.submitProfile()
	case formDownload:
		path := expandHome(m.path.path)
		id := r.id(m.formTarget)
		return m.confirmDownload(path, id)
	}
	return nil
}
