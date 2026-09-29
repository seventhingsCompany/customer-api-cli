// Package tui is the interactive full-screen interface started by running
// `seventhings` without arguments in a terminal.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

// Deps is what the TUI needs from the CLI: an authenticated client (with
// token refresh and rate limiting), login, and profile information.
type Deps interface {
	Client() (*client.Client, error)
	LoggedIn() bool
	Login(ctx context.Context, url, clientID, username, password string) error
	Profile() (name, url, clientID, username string)
}

const perPage = 50

type mode int

const (
	modeLogin mode = iota
	modeList
	modeDetail
	modeHistory
	modeForm
	modeConfirm
)

type formKind int

const (
	formLogin formKind = iota
	formCreate
	formEdit
	formUpload
	formDownload
	formCustom // action form; submit runs m.custom
)

type tabState struct {
	items   []Item
	page    int
	search  string
	loaded  bool
	hasMore bool
}

// Model is the root Bubble Tea model.
type Model struct {
	ctx  context.Context
	deps Deps
	res  []*resource
	tabs []tabState
	tab  int
	mode mode
	prev mode // mode to return to from confirm/form/history

	width, height int
	table         table.Model
	detail        viewport.Model
	detailItem    Item
	search        textinput.Model
	searching     bool
	spinner       spinner.Model
	loading       int
	showHelp      bool

	status    string
	statusErr bool

	// form state
	formKind   formKind
	form       *huh.Form
	record     *recordForm
	login      *loginForm
	path       *pathForm
	formTarget Item
	allFields  bool              // form shows every field, not just the common ones
	prefill    map[string]string // initial values for the next create form
	custom     func(rf *recordForm) tea.Cmd

	// choiceCache holds picker options per source; a nil entry means the
	// source was too large and the field falls back to typing an ID.
	choiceCache map[choiceSource][][2]string
	afterDefs   func() tea.Cmd // continuation once field definitions are loaded

	// confirm state
	confirmPrompt string
	confirmCmd    func() tea.Cmd
	keepStatus    bool // next list reload keeps the action message

	defs map[models.AssetTrackingTemplate][]models.FieldDefinition
}

// New builds the root model.
func New(ctx context.Context, deps Deps) *Model {
	res := resources()
	m := &Model{
		ctx: ctx, deps: deps, res: res, tabs: make([]tabState, len(res)),
		width: 100, height: 30, mode: modeList,
		defs:        map[models.AssetTrackingTemplate][]models.FieldDefinition{},
		choiceCache: map[choiceSource][][2]string{},
	}
	for i := range m.tabs {
		m.tabs[i].page = 1
	}
	m.table = table.New(table.WithFocused(true), table.WithStyles(tableStyles()))
	m.detail = viewport.New()
	m.search = textinput.New()
	m.search.Prompt = "/ "
	m.search.Placeholder = "search"
	m.spinner = spinner.New(spinner.WithSpinner(spinner.MiniDot))
	if !deps.LoggedIn() {
		m.openLogin()
	}
	return m
}

// --- messages ---

type itemsMsg struct {
	tab   int
	page  int
	items []Item
	err   error
}

type detailMsg struct {
	item Item
	err  error
}

type historyMsg struct {
	text string
	err  error
}

type defsMsg struct {
	template models.AssetTrackingTemplate
	defs     []models.FieldDefinition
	err      error
}

type choicesMsg struct {
	loaded map[choiceSource][][2]string
	next   func() tea.Cmd
	err    error
}

// cmdMsg runs a follow-up command on the UI goroutine (used when a
// background step needs to open a form or confirmation).
type cmdMsg struct{ fn func() tea.Cmd }

type doneMsg struct {
	text   string
	err    error
	reload bool
	back   bool // leave detail view (after delete)
}

type loginMsg struct{ err error }

// StatusMsg shows a transient message (e.g. rate-limit waits) in the footer.
type StatusMsg string

func (m *Model) Init() tea.Cmd {
	if m.mode == modeLogin {
		return m.form.Init()
	}
	return m.load()
}

// --- commands ---

func (m *Model) client() (*client.Client, error) { return m.deps.Client() }

func (m *Model) load() tea.Cmd {
	tab, st := m.tab, m.tabs[m.tab]
	r := m.res[tab]
	m.loading++
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		cl, err := m.client()
		if err != nil {
			return itemsMsg{tab: tab, err: err}
		}
		items, err := r.load(m.ctx, cl, query{page: st.page, perPage: perPage, search: st.search})
		return itemsMsg{tab: tab, page: st.page, items: items, err: err}
	})
}

func (m *Model) run(fn func(cl *client.Client) tea.Msg) tea.Cmd {
	m.loading++
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		cl, err := m.client()
		if err != nil {
			return doneMsg{err: err}
		}
		return fn(cl)
	})
}

// --- update ---

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		if m.form != nil {
			m.form = m.form.WithWidth(m.formWidth())
		}
		return m, nil
	case spinner.TickMsg:
		if m.loading == 0 {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case StatusMsg:
		m.setStatus(string(msg), false)
		return m, nil
	case itemsMsg:
		m.loading = max(m.loading-1, 0)
		if msg.err != nil {
			return m, m.fail(msg.err)
		}
		st := &m.tabs[msg.tab]
		st.items, st.loaded, st.hasMore = msg.items, true, len(msg.items) == perPage
		if msg.tab == m.tab {
			m.refreshTable()
			if m.keepStatus {
				m.keepStatus = false
				return m, nil
			}
			m.setStatus(fmt.Sprintf("%d %s on page %d", len(msg.items), strings.ToLower(m.res[msg.tab].title), msg.page), false)
		}
		return m, nil
	case detailMsg:
		m.loading = max(m.loading-1, 0)
		if msg.err != nil {
			return m, m.fail(msg.err)
		}
		m.detailItem = msg.item
		m.detail.SetContent(renderItem(msg.item, m.width-4))
		m.detail.GotoTop()
		m.mode = modeDetail
		return m, nil
	case historyMsg:
		m.loading = max(m.loading-1, 0)
		if msg.err != nil {
			return m, m.fail(msg.err)
		}
		m.prev = m.mode
		m.detail.SetContent(msg.text)
		m.detail.GotoTop()
		m.mode = modeHistory
		return m, nil
	case defsMsg:
		m.loading = max(m.loading-1, 0)
		if msg.err != nil {
			return m, m.fail(msg.err)
		}
		m.defs[msg.template] = msg.defs
		if next := m.afterDefs; next != nil {
			m.afterDefs = nil
			return m, next()
		}
		return m, nil
	case choicesMsg:
		m.loading = max(m.loading-1, 0)
		if msg.err != nil {
			return m, m.fail(msg.err)
		}
		for src, c := range msg.loaded {
			m.choiceCache[src] = c
		}
		return m, msg.next()
	case cmdMsg:
		m.loading = max(m.loading-1, 0)
		return m, msg.fn()
	case doneMsg:
		m.loading = max(m.loading-1, 0)
		if msg.err != nil {
			return m, m.fail(msg.err)
		}
		m.setStatus(msg.text, false)
		var cmds []tea.Cmd
		if msg.back {
			m.mode = modeList
		}
		if msg.reload {
			m.keepStatus = true
			cmds = append(cmds, m.load())
			if m.mode == modeDetail && m.detailItem != nil {
				cmds = append(cmds, m.openDetail(m.detailItem))
			}
		}
		return m, tea.Batch(cmds...)
	case loginMsg:
		m.loading = max(m.loading-1, 0)
		if msg.err != nil {
			m.setStatus("Login failed: "+errText(msg.err), true)
			_, url, clientID, username := m.deps.Profile()
			if m.login != nil {
				url, clientID, username = m.login.url, m.login.clientID, m.login.username
			}
			m.openLoginWith(url, clientID, username)
			return m, m.form.Init()
		}
		m.form, m.login = nil, nil
		m.mode = modeList
		name, url, _, _ := m.deps.Profile()
		m.setStatus(fmt.Sprintf("Logged in to %s (profile %s)", url, name), false)
		m.keepStatus = true
		return m, m.load()
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	if m.mode == modeForm || m.mode == modeLogin {
		return m.updateForm(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeLogin, modeForm:
		if k == "esc" && m.mode == modeForm {
			m.form = nil
			m.mode = m.prev
			m.setStatus("Cancelled", false)
			return m, nil
		}
		return m.updateForm(msg)
	case modeConfirm:
		switch k {
		case "y", "Y":
			m.mode = m.prev
			return m, m.confirmCmd()
		case "n", "N", "esc", "q":
			m.mode = m.prev
			m.setStatus("Cancelled", false)
		}
		return m, nil
	case modeHistory:
		if k == "esc" || k == "q" || k == "backspace" {
			m.mode = m.prev
			if m.mode == modeDetail && m.detailItem != nil {
				m.detail.SetContent(renderItem(m.detailItem, m.width-4))
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	case modeDetail:
		switch k {
		case "esc", "q", "backspace":
			m.mode = modeList
			return m, nil
		}
		if cmd, ok := m.action(k, m.detailItem); ok {
			return m, cmd
		}
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	}

	// list mode
	if m.searching {
		switch k {
		case "enter":
			m.searching = false
			m.search.Blur()
			st := &m.tabs[m.tab]
			st.search, st.page = strings.TrimSpace(m.search.Value()), 1
			return m, m.load()
		case "esc":
			m.searching = false
			m.search.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		return m, cmd
	}

	st := &m.tabs[m.tab]
	switch k {
	case "q":
		return m, tea.Quit
	case "?":
		m.showHelp = !m.showHelp
		m.layout()
		return m, nil
	case "tab", "right", "l":
		return m, m.switchTab((m.tab + 1) % len(m.res))
	case "shift+tab", "left":
		return m, m.switchTab((m.tab + len(m.res) - 1) % len(m.res))
	case "1", "2", "3", "4", "5", "6", "7", "8", "9", "0":
		n := int(k[0]-'0') - 1
		if k == "0" {
			n = 9
		}
		if n < len(m.res) {
			return m, m.switchTab(n)
		}
		return m, nil
	case "/":
		m.searching = true
		m.search.SetValue(st.search)
		return m, m.search.Focus()
	case "esc":
		if st.search != "" {
			st.search, st.page = "", 1
			m.search.SetValue("")
			return m, m.load()
		}
		return m, nil
	case "r":
		return m, m.load()
	case "]", "pgdown":
		if st.hasMore {
			st.page++
			return m, m.load()
		}
		return m, nil
	case "[", "pgup":
		if st.page > 1 {
			st.page--
			return m, m.load()
		}
		return m, nil
	case "enter":
		if it := m.selected(); it != nil {
			return m, m.openDetail(it)
		}
		return m, nil
	case "n", "N":
		r := m.res[m.tab]
		if r.create != nil {
			m.allFields = k == "N"
			return m, m.startCreate()
		}
		if r.upload != nil {
			m.openPathForm(formUpload, "Upload file (path)", "", true)
			return m, m.form.Init()
		}
		return m, nil
	}
	if cmd, ok := m.action(k, m.selected()); ok {
		return m, cmd
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

// action handles keys shared by list and detail views.
func (m *Model) action(k string, it Item) (tea.Cmd, bool) {
	if it == nil {
		return nil, false
	}
	r := m.res[m.tab]
	id := r.id(it)
	switch k {
	case "e", "E":
		if r.update == nil {
			return nil, false
		}
		m.allFields = k == "E"
		m.prev = m.mode
		return m.startEdit(it), true
	case "d":
		if r.del == nil {
			return nil, false
		}
		m.ask(fmt.Sprintf("Delete %s %q? [y/N]", r.singular, displayName(it, id)), func() tea.Cmd {
			return m.run(func(cl *client.Client) tea.Msg {
				err := r.del(m.ctx, cl, id)
				return doneMsg{text: fmt.Sprintf("Deleted %s %s", r.singular, id), err: err, reload: true, back: true}
			})
		})
		return nil, true
	case "h":
		if r.history == nil {
			return nil, false
		}
		return m.run(func(cl *client.Client) tea.Msg {
			h, err := r.history(m.ctx, cl, id)
			if err != nil {
				return historyMsg{err: err}
			}
			return historyMsg{text: renderHistory(h, m.width-4)}
		}), true
	case "s":
		if r.toggle == nil {
			return nil, false
		}
		return m.run(func(cl *client.Client) tea.Msg {
			next, err := r.toggle(m.ctx, cl, it)
			return doneMsg{text: fmt.Sprintf("Task is now %s", next), err: err, reload: true}
		}), true
	case "t":
		if r.idKey != "asset_uuid" {
			return nil, false
		}
		// New task for this object: switch to Tasks with the reference filled in.
		for i, other := range m.res {
			if other.title == "Tasks" {
				m.tab = i
				m.refreshTable()
			}
		}
		m.prefill = map[string]string{"references": id}
		m.allFields = false
		m.mode = modeList
		return m.startCreate(), true
	case "a":
		if r.attach == nil {
			return nil, false
		}
		return m.startAttach(r, it), true
	case "x":
		if r.attach == nil {
			return nil, false
		}
		return m.startDetach(r, it), true
	case "o":
		switch {
		case r.hubOffer:
			return m.startHubOffer(r, it), true
		case r.hubOrder:
			m.startHubOrder(r, it)
			return nil, true
		}
		return nil, false
	case "D":
		if r.download == nil {
			return nil, false
		}
		m.prev = m.mode
		m.formTarget = it
		m.openPathForm(formDownload, "Save to", str(it["name"]), false)
		return m.form.Init(), true
	}
	return nil, false
}

func (m *Model) switchTab(n int) tea.Cmd {
	m.tab = n
	m.mode = modeList
	m.searching = false
	m.search.SetValue(m.tabs[n].search)
	m.refreshTable()
	if !m.tabs[n].loaded {
		return m.load()
	}
	return nil
}

func (m *Model) selected() Item {
	st := m.tabs[m.tab]
	i := m.table.Cursor()
	if i < 0 || i >= len(st.items) {
		return nil
	}
	return st.items[i]
}

func (m *Model) openDetail(it Item) tea.Cmd {
	r := m.res[m.tab]
	id := r.id(it)
	if r.get == nil || id == "" {
		m.detailItem = it
		m.detail.SetContent(renderItem(it, m.width-4))
		m.mode = modeDetail
		return nil
	}
	return m.run(func(cl *client.Client) tea.Msg {
		full, err := r.get(m.ctx, cl, id)
		return detailMsg{item: full, err: err}
	})
}

// --- forms ---

func (m *Model) formWidth() int { return max(min(m.width-4, 90), 30) }

func (m *Model) openLogin() {
	_, url, clientID, username := m.deps.Profile()
	m.openLoginWith(url, clientID, username)
}

func (m *Model) openLoginWith(url, clientID, username string) {
	m.login = newLoginForm(url, clientID, username, m.formWidth())
	m.form = m.login.form
	m.formKind = formLogin
	m.mode = modeLogin
}

func (m *Model) openPathForm(kind formKind, title, initial string, mustExist bool) {
	m.path = newPathForm(title, initial, mustExist, m.formWidth())
	m.form = m.path.form
	m.formKind = kind
	if m.mode != modeForm {
		m.prev = m.mode
	}
	m.mode = modeForm
}

func (m *Model) startCreate() tea.Cmd {
	m.prev = m.mode
	return m.prepareRecordForm(nil)
}

func (m *Model) startEdit(it Item) tea.Cmd {
	return m.prepareRecordForm(it)
}

// withDefs runs next once the field definitions of tmpl are loaded.
func (m *Model) withDefs(tmpl models.AssetTrackingTemplate, next func() tea.Cmd) tea.Cmd {
	if _, ok := m.defs[tmpl]; ok || tmpl == "" {
		return next()
	}
	m.afterDefs = next
	m.loading++
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		cl, err := m.client()
		if err != nil {
			return defsMsg{err: err}
		}
		defs, err := cl.FieldDefinitionsList(m.ctx, tmpl)
		return defsMsg{template: tmpl, defs: defs, err: err}
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
	m.loading++
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		cl, err := m.client()
		if err != nil {
			return choicesMsg{err: err}
		}
		loaded := map[choiceSource][][2]string{}
		for _, src := range need {
			c, err := loadChoices(m.ctx, cl, src)
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
	m.record = newRecordForm(title, fields, m.formWidth())
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
		m.record = newRecordForm(title, fields, m.formWidth())
		m.form = m.record.form
		m.formKind = formCustom
		m.custom = submit
		m.mode = modeForm
		return m.form.Init()
	})
}

// ask shows a y/N confirmation and runs cmd on "y".
func (m *Model) ask(prompt string, cmd func() tea.Cmd) {
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
	m.form = nil
	if kind != formLogin {
		m.mode = m.prev
	}
	switch kind {
	case formLogin:
		lf := m.login
		m.loading++
		return tea.Batch(m.spinner.Tick, func() tea.Msg {
			return loginMsg{err: m.deps.Login(m.ctx, strings.TrimSpace(lf.url), strings.TrimSpace(lf.clientID), strings.TrimSpace(lf.username), lf.password)}
		})
	case formCreate:
		body := m.record.body(false)
		return m.run(func(cl *client.Client) tea.Msg {
			id, err := r.create(m.ctx, cl, body)
			return doneMsg{text: fmt.Sprintf("Created %s %s", r.singular, id), err: err, reload: true}
		})
	case formEdit:
		body := m.record.body(true)
		if len(body) == 0 {
			m.setStatus("No changes", false)
			return nil
		}
		id := r.id(m.formTarget)
		return m.run(func(cl *client.Client) tea.Msg {
			err := r.update(m.ctx, cl, id, body)
			return doneMsg{text: fmt.Sprintf("Updated %s (%d field(s))", r.singular, len(body)), err: err, reload: true}
		})
	case formUpload:
		path := m.path.path
		return m.run(func(cl *client.Client) tea.Msg {
			id, err := r.upload(m.ctx, cl, path)
			return doneMsg{text: fmt.Sprintf("Uploaded %s as %s", filepath.Base(path), id), err: err, reload: true}
		})
	case formCustom:
		return m.custom(m.record)
	case formDownload:
		path := expandHome(m.path.path)
		id := r.id(m.formTarget)
		return m.run(func(cl *client.Client) tea.Msg {
			data, err := r.download(m.ctx, cl, id)
			if err == nil {
				err = os.WriteFile(path, data, 0o644)
			}
			return doneMsg{text: fmt.Sprintf("Saved %d bytes to %s", len(data), path), err: err}
		})
	}
	return nil
}

// --- helpers ---

func (m *Model) setStatus(s string, isErr bool) {
	m.status, m.statusErr = s, isErr
}

func (m *Model) fail(err error) tea.Cmd {
	m.setStatus(errText(err), true)
	if isAuthError(err) && m.mode != modeLogin {
		m.openLogin()
		return m.form.Init()
	}
	return nil
}

func isAuthError(err error) bool {
	return exitcode.For(err) == exitcode.Auth
}

func errText(err error) string {
	var apiErr *models.APIError
	if errors.As(err, &apiErr) {
		var body map[string]any
		if json.Unmarshal([]byte(apiErr.Body), &body) == nil {
			for _, k := range []string{"detail", "message", "title"} {
				if s, ok := body[k].(string); ok && s != "" {
					return fmt.Sprintf("HTTP %d: %s", apiErr.StatusCode, s)
				}
			}
		}
		b := strings.Join(strings.Fields(apiErr.Body), " ")
		if len(b) > 200 {
			b = b[:200] + "…"
		}
		if b != "" {
			return fmt.Sprintf("HTTP %d: %s", apiErr.StatusCode, b)
		}
		return fmt.Sprintf("HTTP %d", apiErr.StatusCode)
	}
	return err.Error()
}

func displayName(it Item, fallback string) string {
	for _, k := range []string{"inventory_name", "name", "title", "display_name", "email", "order_number"} {
		if s := str(it[k]); s != "" {
			return s
		}
	}
	return fallback
}

// renderItem formats a record as aligned key/value lines, well-known keys
// first, empty values last.
func renderItem(it Item, width int) string {
	var keys, empty []string
	for k, v := range it {
		if v == nil || str(v) == "" || str(v) == "[]" {
			empty = append(empty, k)
		} else {
			keys = append(keys, k)
		}
	}
	rank := func(k string) int {
		for i, p := range []string{"inventory_name", "name", "title", "display_name", "barcode", "email", "status", "asset_uuid", "uuid", "person_uuid", "id"} {
			if k == p {
				return i
			}
		}
		return 100
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := rank(keys[i]), rank(keys[j])
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	sort.Strings(empty)
	pad := 0
	for _, k := range keys {
		pad = max(pad, len(k))
	}
	pad = min(pad, 32)
	var b strings.Builder
	valWidth := max(width-pad-2, 20)
	for _, k := range keys {
		v := str(it[k])
		if len(v) > valWidth*3 {
			v = v[:valWidth*3] + "…"
		}
		b.WriteString(keyStyle.Render(fmt.Sprintf("%-*s", pad, k)) + "  " + v + "\n")
	}
	if len(empty) > 0 {
		b.WriteString("\n" + dimStyle.Render("empty: "+strings.Join(empty, ", ")) + "\n")
	}
	return b.String()
}

// renderHistory formats a history response (newest first).
func renderHistory(h any, width int) string {
	it, err := toItem(h)
	if err != nil {
		return err.Error()
	}
	items, _ := it["items"].([]any)
	if len(items) == 0 {
		return dimStyle.Render("No history entries.")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", dimStyle.Render(fmt.Sprintf("%v entries (newest first)", it["total"])))
	for _, raw := range items {
		e, _ := raw.(map[string]any)
		when := firstNonEmpty(str(e["occurred_at"]), str(e["created_at"]), str(e["date"]))
		what := firstNonEmpty(str(e["description"]), str(e["event_name"]), str(e["type"]))
		b.WriteString(keyStyle.Render(when) + "  " + what + "\n")
		detail := firstNonEmpty(str(e["details"]), str(e["properties"]))
		if detail != "" && detail != "{}" && detail != "null" {
			if len(detail) > width*2 {
				detail = detail[:width*2] + "…"
			}
			b.WriteString(dimStyle.Render("    "+detail) + "\n")
		}
	}
	return b.String()
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// Run starts the TUI and blocks until the user quits. setNotify, if
// non-nil, receives a function the CLI can call to show status messages
// (e.g. rate-limit waits) while the TUI owns the screen.
func Run(ctx context.Context, deps Deps, opts []tea.ProgramOption, setNotify func(func(string))) error {
	m := New(ctx, deps)
	p := tea.NewProgram(m, append([]tea.ProgramOption{tea.WithContext(ctx)}, opts...)...)
	if setNotify != nil {
		setNotify(func(s string) { p.Send(StatusMsg(s)) })
		defer setNotify(nil)
	}
	_, err := p.Run()
	if errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
