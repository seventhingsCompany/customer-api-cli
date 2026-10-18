package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// chrome counts header, tabs, search, footer and any subview navigation row.
func (m *Model) chrome() int {
	if m.hasSubviews() {
		return 6
	}
	return 5
}

func (m *Model) layout() {
	bodyH := max(m.height-m.chrome(), 3)
	m.table.SetWidth(m.width)
	m.table.SetHeight(bodyH)
	m.detail.SetWidth(m.width)
	m.detail.SetHeight(max(bodyH-1, 2)) // detail and history add a title line
	m.search.SetWidth(max(m.width-4, 10))
	m.help.SetWidth(m.width)
	m.help.SetHeight(bodyH)
	m.help.SetContent(ansi.Wrap(helpText, max(m.width, 1), ""))
	m.refreshTable()
}

// columnsFor returns the resource's columns, or derives them from the data
// when a resource has none (hub items have a tenant-specific shape).
func (m *Model) columnsFor(r *resource, items []Item) []column {
	if len(r.columns) > 0 {
		return r.columns
	}
	var keys []string
	if len(items) > 0 {
		for k := range items[0] {
			keys = append(keys, k)
		}
		slices.Sort(keys)
	}
	cols := []column{{"ID", r.idKey, 1}}
	for _, k := range keys {
		if k != r.idKey && len(cols) < 6 {
			cols = append(cols, column{k, k, 3})
		}
	}
	return cols
}

func (m *Model) refreshTable() {
	r := m.res[m.tab]
	st := m.tabs[m.tab]
	cols := m.columnsFor(r, st.items)
	// Keep the leading (essential) columns readable instead of squeezing every
	// column below its minimum width on narrow terminals.
	cols = cols[:min(len(cols), max((m.width-2)/10, 1))]
	total := 0
	for _, c := range cols {
		total += c.width
	}
	avail := max(m.width-2*len(cols)-2, len(cols)*4)
	extra := max(avail-len(cols)*4, 0)
	tcols := make([]table.Column, len(cols))
	for i, c := range cols {
		tcols[i] = table.Column{Title: c.title, Width: 4 + extra*c.width/max(total, 1)}
	}
	rows := make([]table.Row, len(st.items))
	for i, it := range st.items {
		row := make(table.Row, len(cols))
		for j, c := range cols {
			row[j] = summary(lookup(it, c.key))
		}
		rows[i] = row
	}
	// Columns must be set before rows so row widths match. Clearing the
	// rows resets the cursor to -1, so restore it.
	cursor := m.table.Cursor()
	m.table.SetRows(nil)
	m.table.SetColumns(tcols)
	m.table.SetRows(rows)
	m.table.SetCursor(min(max(cursor, 0), max(len(rows)-1, 0)))
}

// View renders the screen.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "seventhings"
	return v
}

func (m *Model) render() string {
	if m.width < 40 || m.height < 16 {
		return ansi.Wrap("Terminal too small. Resize to at least 40×16. Ctrl+C quits.", max(m.width, 1), "")
	}
	name, url, _, _ := m.deps.Profile()
	header := ansi.Truncate(titleStyle.Render("seventhings")+" "+dimStyle.Render(fmt.Sprintf("%s · %s", name, url)), m.width, "…")

	if m.mode == modeLogin {
		body := dimStyle.Render("Logging in…")
		if m.form != nil {
			body = m.form.View()
		}
		return lipgloss.JoinVertical(lipgloss.Left, header, "", body, "", m.footer())
	}

	tabBar := m.tabBar(false)
	if lipgloss.Width(tabBar) > m.width {
		tabBar = m.tabBar(true) // narrow terminal: numbers, active title only
	}
	tabBar = ansi.Truncate(tabBar, m.width, "…")

	var body string
	switch m.mode {
	case modeForm:
		if m.form != nil {
			body = m.form.View()
		}
	case modeConfirm:
		body = boxStyle.Render(promptStyle.Render(m.confirmPrompt))
	case modeDetail:
		r := m.res[m.tab]
		body = keyStyle.Render(displayName(m.detailItem, r.id(m.detailItem))) + "\n" + m.detailBody()
	case modeHistory:
		body = keyStyle.Render("History: "+displayName(m.detailOrSelected(), "")) + "\n" + m.detail.View()
	case modeSettings:
		// Fill the body like the table does, so the footer stays at the bottom.
		body = lipgloss.NewStyle().Height(max(m.height-m.chrome(), 3)).Render(m.renderSettings())
	default:
		st := m.tabs[m.tab]
		switch {
		case m.showHelp:
			body = m.help.View()
		case len(st.items) == 0:
			text := "Loading " + strings.ToLower(m.res[m.tab].title) + "…"
			if st.loaded {
				if st.search != "" || st.filterText != "" {
					text = "No matches. Press Esc to clear search and filters."
				} else {
					text = "No " + strings.ToLower(m.res[m.tab].title) + " yet."
					if m.res[m.tab].create != nil {
						text += " Press n to create one."
					} else if m.res[m.tab].upload != nil {
						text += " Press n to upload a file."
					}
				}
			} else if m.loading == 0 {
				text = "Not loaded. Press r to retry."
			}
			body = lipgloss.NewStyle().Height(max(m.height-m.chrome(), 3)).Render(ansi.Wrap(text, m.width, ""))
		default:
			body = m.table.View()
		}
	}

	st := m.tabs[m.tab]
	var searchLine string
	switch {
	case m.onSettings():
		searchLine = dimStyle.Render("settings")
	case m.searching:
		searchLine = m.search.View()
	case st.search != "" || st.filterText != "" || st.sortText != "":
		searchLine = dimStyle.Render(ansi.Truncate(st.filterSummary()+"  (esc to clear)", m.width, "…"))
	default:
		page := fmt.Sprintf("page %d", st.page)
		if st.total != noTotal {
			page += fmt.Sprintf(" of %d", pageCount(st.total, m.pageSize))
		}
		searchLine = dimStyle.Render(page)
	}
	lines := []string{header, tabBar}
	if m.hasSubviews() {
		lines = append(lines, m.subviewBar())
	}
	lines = append(lines, searchLine, body, m.footer())
	out := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().MaxWidth(m.width).Render(out)
}

func (m *Model) tabBar(compact bool) string {
	var tabs []string
	if compact {
		// Keep the active resource and the catalog discoverable even at the
		// minimum width; inactive numeric shortcuts may trail off-screen.
		if m.onSettings() {
			tabs = append(tabs, activeTab.Render("Settings"))
		} else {
			tabs = append(tabs, activeTab.Render(m.tabKey(m.paneRoot(m.tab))+" "+m.paneTitle(m.tab)))
		}
		for _, i := range m.paneTabs() {
			if m.res[i].shortcut != "" && (i != m.paneRoot(m.tab) || m.onSettings()) {
				tabs = append(tabs, tabStyle.Render(m.tabKey(i)+" "+m.paneTitle(i)))
			}
		}
		for _, i := range m.paneTabs() {
			if m.res[i].shortcut == "" && (i != m.paneRoot(m.tab) || m.onSettings()) {
				tabs = append(tabs, tabStyle.Render(m.tabKey(i)))
			}
		}
		if !m.onSettings() {
			tabs = append(tabs, tabStyle.Render(","))
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
	}
	for _, i := range m.paneTabs() {
		label := fmt.Sprintf("%s %s", m.tabKey(i), m.paneTitle(i))
		if i == m.paneRoot(m.tab) && !m.onSettings() {
			tabs = append(tabs, activeTab.Render(label))
		} else {
			tabs = append(tabs, tabStyle.Render(label))
		}
	}
	// No key prefix, so all tabs still fit in 120 columns; "," opens it.
	label := "Settings"
	if m.onSettings() {
		tabs = append(tabs, activeTab.Render(label))
	} else {
		tabs = append(tabs, tabStyle.Render(label))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

func (m *Model) detailOrSelected() Item {
	if m.detailItem != nil && m.prev == modeDetail {
		return m.detailItem
	}
	return m.selected()
}

func (m *Model) footer() string {
	var status string
	switch {
	case m.loading > 0:
		operation := m.operation
		if operation == "" {
			operation = "loading…"
		}
		status = m.spinner.View() + " " + operation
		if m.status != "" && !m.statusErr {
			status += "  " + dimStyle.Render(m.status)
		}
	case m.statusErr:
		status = errStyle.Render("✗ " + m.status)
	case m.status != "":
		status = okStyle.Render(m.status)
	}
	help := dimStyle.Render(m.keyHelp())
	return ansi.Truncate(status, m.width, "…") + "\n" + ansi.Truncate(help, m.width, "…")
}

const helpText = "Keys (actions depend on the resource)\n\n" +
	"tab / shift+tab: switch pane\n1–8: objects through files\n9: CHub (last subview)\n0 / C: product catalog\nv: switch CHub Items / Orders\n,: settings and profile switching\n↑↓: select a row\nj / k: next / previous page (lists)\nPage Down / Page Up or ] / [: alternative paging keys\nj / k or ↑↓: scroll details, history and help\ns or /: search\nf: filter and sort\nesc: clear filters or cancel a pending read\nr: reload\n\n" +
	"enter: open details\nn / N: new record (common / all fields)\ne / E: edit record (common / all fields)\nu: duplicate object / catalog item (review before creating)\nb: create object from catalog item\nd: delete with confirmation\nh: history\nt: new task for object\na / x: attach / detach file\n[ / ]: previous / next image (details only)\np: hide / show picture\nO: open selected image or file in viewer\no: offer on hub / order hub item\nc: open / close task\nD: download file\ny: copy ID\nY: copy as CLI command\n\n? / esc: close this help\nq: quit\n"

func (m *Model) keyHelp() string {
	if m.showHelp && m.mode == modeList {
		return "↑↓ scroll · ?/esc back · q quit"
	}
	r := m.res[m.tab]
	var keys []string
	switch m.mode {
	case modeLogin:
		return "enter next · shift+tab back · ctrl+c quit"
	case modeForm:
		return "enter next · shift+tab back · esc cancel"
	case modeConfirm:
		return "y confirm · n cancel"
	case modeHistory:
		return "↑↓ scroll · esc back"
	case modeSettings:
		return "↑↓ select · enter change · tab/1-0/C switch · q quit"
	case modeDetail:
		keys = append(keys, "↑↓ scroll", "esc back")
		if m.hasSubviews() {
			keys = append(keys, "v items/orders")
		}
		if u, _ := pictureFile(m.detailItem); u != "" && m.imageMode != imagesOff {
			if len(pictureFiles(m.detailItem)) > 1 && !m.hidePictures {
				keys = append(keys, "[/] images")
			}
			keys = append(keys, "p picture")
		}
		picture, _ := pictureFile(m.detailItem)
		if u, _ := firstAttachment(m.detailItem); u != "" || picture != "" || r.download != nil {
			keys = append(keys, "O open")
		}
	default:
		if m.hasSubviews() {
			keys = append(keys, "v items/orders")
		}
		keys = append(keys, "enter open", "j/k page", "s search")
		if r.filterable || r.sort != sortNone {
			keys = append(keys, "f filter")
		}
		if r.create != nil || r.upload != nil {
			keys = append(keys, "n new")
		}
	}
	if r.cli == "catalog-items" {
		keys = append(keys, "b create object")
	}
	if r.update != nil {
		keys = append(keys, "e edit")
	}
	if r.cli == "objects" || r.cli == "catalog-items" {
		keys = append(keys, "u duplicate")
	}
	if r.del != nil {
		keys = append(keys, "d delete")
	}
	if r.history != nil {
		keys = append(keys, "h history")
	}
	if r.toggle != nil {
		keys = append(keys, "c open/close")
	}
	if r.download != nil {
		keys = append(keys, "D download")
	}
	if r.attach != nil {
		keys = append(keys, "a attach", "x detach")
	}
	if r.hubOffer {
		keys = append(keys, "o offer on hub")
	}
	if r.hubOrder {
		keys = append(keys, "o order")
	}
	if m.mode == modeList {
		keys = append(keys, "? more", "q quit")
	}
	return strings.Join(keys, " · ")
}
