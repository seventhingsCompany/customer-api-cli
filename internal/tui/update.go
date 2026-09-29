package tui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-go/client"
	uv "github.com/charmbracelet/ultraviolet"
)

// Update handles a message, then keeps any overlay image in sync with the
// view (see syncOverlay).
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.update(msg)
	if oc := m.syncOverlay(); oc != nil {
		cmd = tea.Batch(cmd, oc)
	}
	return model, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		var cmd tea.Cmd
		if m.mode == modeDetail && m.detailItem != nil {
			cmd = m.refreshDetail()
		}
		if m.form != nil {
			m.form = m.form.WithWidth(m.formWidth()).WithHeight(m.formHeight())
		}
		return m, cmd
	case uv.KittyGraphicsEvent:
		if msg.Options.ID == kittyQueryID && string(msg.Payload) == "OK" && m.imageMode == imagesAuto && m.gfx == gfxBlocks {
			return m, m.setGraphics(gfxKitty)
		}
		return m, nil
	case uv.PrimaryDeviceAttributesEvent:
		if m.imageMode == imagesAuto && m.gfx == gfxBlocks {
			return m, m.setGraphics(chooseOverlay(msg, m.deps.Env))
		}
		return m, nil
	case overlayMsg:
		if msg.gen != m.ov.gen || m.ov.key == "" {
			return m, nil
		}
		seq := m.overlaySeq()
		if seq == "" {
			return m, nil
		}
		m.ov.shown = true
		return m, tea.Raw(placeAt(m.pane.row, m.pane.col, seq))
	case uv.CellSizeEvent:
		if msg.Width > 0 && msg.Height > 0 && (msg.Width != m.kitty.cellW || msg.Height != m.kitty.cellH) {
			// Placements were sized for the old cells: drop and redo them.
			old := m.kitty.cleanup()
			m.kitty = kittyState{enabled: m.kitty.enabled, cellW: msg.Width, cellH: msg.Height, nextID: m.kitty.nextID, ids: map[kittyKey]int{}}
			m.ov.cache = map[string]string{}
			cmds := []tea.Cmd{}
			if old != "" {
				cmds = append(cmds, tea.Raw(old))
			}
			if m.mode == modeDetail && m.detailItem != nil {
				cmds = append(cmds, m.refreshDetail())
			}
			return m, tea.Sequence(cmds...)
		}
		return m, nil
	case tea.BackgroundColorMsg:
		applyTheme(msg.IsDark())
		m.table.SetStyles(tableTheme)
		m.refreshTable()
		var cmd tea.Cmd
		if m.mode == modeDetail && m.detailItem != nil {
			cmd = m.refreshDetail()
		}
		if m.form != nil { // huh adapts its own theme
			_, fcmd := m.updateForm(msg)
			return m, tea.Batch(cmd, fcmd)
		}
		return m, cmd
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
	case loadedMsg:
		m.loading = max(m.loading-1, 0)
		if msg.read == 0 {
			m.writing = max(m.writing-1, 0)
			m.operation = ""
		}
		if cancel, ok := m.reads[msg.read]; ok {
			cancel() // release the context
			delete(m.reads, msg.read)
		}
		if msg.scope.session != m.session {
			return m, nil
		}
		switch result := msg.msg.(type) {
		case defsMsg, choicesMsg, cmdMsg:
			if msg.scope.nav != m.nav || msg.scope.tab != m.tab {
				return m, nil
			}
		case doneMsg:
			result.scope = &msg.scope
			msg.msg = result
			if result.err != nil && result.reload {
				m.invalidateTab(msg.scope.tab)
			}
			if result.err != nil && msg.recovery != nil && msg.scope.nav == m.nav && msg.scope.tab == m.tab && !isAuthError(result.err) {
				cmd := m.restoreForm(msg.recovery)
				failure := m.fail(result.err)
				var networkError net.Error
				if msg.read == 0 && errors.As(result.err, &networkError) {
					m.setStatus("Outcome unknown; check the record before retrying: "+errText(result.err), true)
				}
				return m, tea.Batch(cmd, failure)
			}
		}
		return m.update(msg.msg)
	case itemsMsg:
		st := &m.tabs[msg.tab]
		if msg.seq != st.seq {
			return m, nil // a newer load replaced this one
		}
		if msg.err != nil {
			return m, m.fail(msg.err)
		}
		title := strings.ToLower(m.res[msg.tab].title)
		if msg.page > 1 && len(msg.items) == 0 && msg.tab == m.tab {
			// Paged past the end (a full last page, or records were deleted).
			st.page, st.lastPage = msg.page-1, msg.page-1
			m.setStatus("No more "+title, false)
			m.keepStatus = true
			return m, m.load()
		}
		st.items, st.loaded, st.total = msg.items, true, msg.total
		if msg.countFor != "" {
			st.countFor = msg.countFor
		}
		if st.total != noTotal {
			st.hasMore = msg.page*msg.perPage < st.total
		} else {
			st.hasMore = len(msg.items) == msg.perPage && (st.lastPage == 0 || msg.page < st.lastPage)
		}
		if msg.tab == m.tab {
			m.refreshTable()
			if m.keepStatus {
				m.keepStatus = false
				return m, nil
			}
			if st.total != noTotal {
				m.setStatus(fmt.Sprintf("page %d of %d · %d %s", msg.page, pageCount(st.total, msg.perPage), st.total, title), false)
			} else {
				m.setStatus(fmt.Sprintf("%d %s on page %d", len(msg.items), title, msg.page), false)
			}
		}
		return m, nil
	case detailMsg:
		if msg.nav != m.nav {
			return m, nil
		}
		if msg.err != nil {
			return m, m.fail(msg.err)
		}
		m.detailItem = msg.item
		m.mode = modeDetail
		cmd := m.loadPicture()
		cmd2 := m.refreshDetail()
		m.detail.GotoTop()
		return m, tea.Batch(cmd, cmd2)
	case pictureMsg:
		if msg.session != m.session {
			return m, nil
		}
		m.pictures[msg.key] = &picture{img: msg.img, err: msg.err}
		if m.mode == modeDetail {
			return m, m.refreshDetail()
		}
		return m, nil
	case historyMsg:
		if msg.nav != m.nav {
			return m, nil
		}
		if msg.err != nil {
			return m, m.fail(msg.err)
		}
		m.prev = m.mode
		m.detail.SetContent(msg.text)
		m.detail.GotoTop()
		m.mode = modeHistory
		return m, nil
	case defsMsg:
		if msg.err != nil {
			return m, m.fail(msg.err)
		}
		m.defs[msg.template] = msg.defs
		if msg.next != nil {
			return m, msg.next()
		}
		return m, nil
	case choicesMsg:
		if msg.err != nil {
			return m, m.fail(msg.err)
		}
		for src, c := range msg.loaded {
			m.choiceCache[src] = c
		}
		return m, msg.next()
	case cmdMsg:
		return m, msg.fn()
	case doneMsg:
		if msg.err != nil {
			return m, m.fail(msg.err)
		}
		m.setStatus(msg.text, false)
		var cmds []tea.Cmd
		tab, current := m.tab, true
		if msg.scope != nil {
			tab = msg.scope.tab
			current = tab == m.tab && msg.scope.nav == m.nav
		}
		if msg.back && current {
			m.navigate()
			m.mode = modeList
		}
		if msg.reload {
			m.invalidateTab(tab)
			if tab != m.tab || m.mode == modeSettings || m.mode == modeLogin || m.mode == modeForm || m.mode == modeConfirm {
				return m, nil
			}
			m.keepStatus = true
			cmds = append(cmds, m.load())
			if current && m.mode == modeDetail && m.detailItem != nil {
				cmds = append(cmds, m.openDetail(m.detailItem))
			}
		}
		return m, tea.Batch(cmds...)
	case loginMsg:
		if msg.err != nil {
			m.setStatus("Login failed: "+errText(msg.err), true)
			_, url, clientID, username := m.deps.Profile()
			if m.login != nil {
				url, clientID, username = m.login.url, m.login.clientID, m.login.username
			}
			m.openLoginWith(url, clientID, username)
			return m, m.form.Init()
		}
		cleanup := m.resetSession()
		m.mode = modeList
		name, url, _, _ := m.deps.Profile()
		m.setStatus(fmt.Sprintf("Logged in to %s (profile %s)", url, name), false)
		m.keepStatus = true
		return m, tea.Batch(cleanup, m.load())
	case logoutMsg:
		if msg.err != nil {
			m.setStatus("Logout failed: "+errText(msg.err), true)
			return m, nil
		}
		return m, m.loggedOut()
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
	if m.width < 40 || m.height < 16 {
		if k == "q" {
			return m, tea.Quit
		}
		return m, nil // do not perform invisible actions in an unusable terminal
	}
	if m.writing > 0 && (m.mode == modeList || m.mode == modeDetail) {
		switch k {
		case "n", "N", "e", "E", "d", "c", "t", "a", "x", "o":
			m.setStatus("An operation is already in progress", true)
			return m, nil
		}
	}
	switch m.mode {
	case modeLogin, modeForm:
		if k == "esc" && m.mode == modeForm {
			m.navigate()
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
			m.recovery = nil
			m.setStatus("Cancelled", false)
		}
		return m, nil
	case modeHistory:
		if k == "Y" {
			return m, m.copyCLICommand()
		}
		if k == "esc" || k == "q" || k == "backspace" {
			m.navigate()
			m.mode = m.prev
			if m.mode == modeDetail && m.detailItem != nil {
				return m, m.refreshDetail()
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	case modeSettings:
		return m.handleSettingsKey(k)
	case modeDetail:
		switch k {
		case "esc", "q", "backspace":
			m.navigate()
			m.mode = modeList
			return m, nil
		case "p":
			if m.imageMode == imagesOff {
				return m, nil
			}
			m.hidePictures = !m.hidePictures
			return m, tea.Batch(m.loadPicture(), m.refreshDetail())
		case "Y":
			return m, m.copyCLICommand()
		}
		if cmd, ok := m.action(k, m.detailItem); ok {
			return m, cmd
		}
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	}

	// list mode
	if m.showHelp {
		switch k {
		case "?", "esc":
			m.showHelp = false
			return m, nil
		case "q":
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.help, cmd = m.help.Update(msg)
		return m, cmd
	}
	if m.searching {
		switch k {
		case "enter":
			m.searching = false
			m.search.Blur()
			m.navigate()
			st := &m.tabs[m.tab]
			st.search, st.page = strings.TrimSpace(m.search.Value()), 1
			st.invalidate()
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
		m.help.GotoTop()
		return m, nil
	case "tab", "right":
		if m.tab == len(m.res)-1 {
			return m, m.openSettings()
		}
		return m, m.switchTab(m.tab + 1)
	case "shift+tab", "left":
		if m.tab == 0 {
			return m, m.openSettings()
		}
		return m, m.switchTab(m.tab - 1)
	case ",":
		return m, m.openSettings()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9", "0":
		n := int(k[0]-'0') - 1
		if k == "0" {
			n = 9
		}
		if n < len(m.res) {
			return m, m.switchTab(n)
		}
		return m, nil
	case "s", "/":
		m.navigate()
		m.searching = true
		m.search.SetValue(st.search)
		return m, m.search.Focus()
	case "esc":
		if len(m.reads) > 0 {
			m.cancelReads()
			m.setStatus("Cancelled (r reloads)", false)
			return m, nil
		}
		if st.clearFilter() {
			m.search.SetValue("")
			return m, m.load()
		}
		return m, nil
	case "f":
		return m, m.openFilterForm()
	case "Y":
		return m, m.copyCLICommand()
	case "r":
		m.navigate()
		st.invalidate()
		return m, m.load()
	case "j", "]", "pgdown":
		if st.hasMore {
			m.navigate()
			st.page++
			return m, m.load()
		}
		return m, nil
	case "k", "[", "pgup":
		if st.page > 1 {
			m.navigate()
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
			m.operation = "Deleting " + r.singular + "…"
			return m.run(func(ctx context.Context, cl *client.Client) tea.Msg {
				err := r.del(ctx, cl, id)
				return doneMsg{text: fmt.Sprintf("Deleted %s %s", r.singular, id), err: err, reload: true, back: true}
			})
		})
		return nil, true
	case "h":
		if r.history == nil {
			return nil, false
		}
		m.navigate()
		nav, width := m.nav, m.width-4
		cmd, cancel := m.fetch(func(ctx context.Context, cl *client.Client) tea.Msg {
			h, err := r.history(ctx, cl, id)
			if err != nil {
				return historyMsg{nav: nav, err: err}
			}
			return historyMsg{nav: nav, text: renderHistory(h, width)}
		})
		m.navCancel = cancel
		return cmd, true
	case "c":
		if r.toggle == nil {
			return nil, false
		}
		return m.run(func(ctx context.Context, cl *client.Client) tea.Msg {
			next, err := r.toggle(ctx, cl, it)
			return doneMsg{text: fmt.Sprintf("Task is now %s", next), err: err, reload: true}
		}), true
	case "t":
		if r.idKey != "asset_uuid" {
			return nil, false
		}
		// New task for this object: switch to Tasks with the reference filled in.
		m.navigate()
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
	case "O":
		return m.openInViewer(r, it), true
	case "y":
		return m.copyID(r, it), true
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
	m.navigate()
	m.tab = n
	m.mode = modeList
	m.searching = false
	m.search.SetValue(m.tabs[n].search)
	m.refreshTable()
	if !m.tabs[n].loaded {
		return m.load()
	}
	st := m.tabs[n]
	title := strings.ToLower(m.res[n].title)
	if st.total != noTotal {
		m.setStatus(fmt.Sprintf("page %d of %d · %d %s", st.page, pageCount(st.total, m.pageSize), st.total, title), false)
	} else {
		m.setStatus(fmt.Sprintf("%d %s on page %d", len(st.items), title, st.page), false)
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
