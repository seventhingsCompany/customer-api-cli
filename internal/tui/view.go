package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// chrome is the number of lines used by header, tabs, search and footer.
func (m *Model) chrome() int {
	n := 5
	if m.showHelp {
		n += 3
	}
	return n
}

func (m *Model) layout() {
	bodyH := max(m.height-m.chrome(), 3)
	m.table.SetWidth(m.width)
	m.table.SetHeight(bodyH)
	m.detail.SetWidth(m.width)
	m.detail.SetHeight(max(bodyH-1, 2)) // detail and history add a title line
	m.search.SetWidth(max(m.width-4, 10))
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
	total := 0
	for _, c := range cols {
		total += c.width
	}
	avail := max(m.width-2*len(cols)-2, len(cols)*4)
	tcols := make([]table.Column, len(cols))
	for i, c := range cols {
		tcols[i] = table.Column{Title: c.title, Width: max(avail*c.width/max(total, 1), 4)}
	}
	rows := make([]table.Row, len(st.items))
	for i, it := range st.items {
		row := make(table.Row, len(cols))
		for j, c := range cols {
			row[j] = strings.ReplaceAll(str(lookup(it, c.key)), "\n", " ")
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
	name, url, _, _ := m.deps.Profile()
	header := titleStyle.Render("seventhings") + " " + dimStyle.Render(fmt.Sprintf("%s · %s", name, url))

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
	default:
		body = m.table.View()
	}

	st := m.tabs[m.tab]
	var searchLine string
	switch {
	case m.searching:
		searchLine = m.search.View()
	case st.search != "":
		searchLine = dimStyle.Render(fmt.Sprintf("filter: %q  (esc to clear)", st.search))
	default:
		searchLine = dimStyle.Render(fmt.Sprintf("page %d", st.page))
	}
	out := lipgloss.JoinVertical(lipgloss.Left, header, tabBar, searchLine, body, m.footer())
	return lipgloss.NewStyle().MaxWidth(m.width).Render(out)
}

func (m *Model) tabBar(compact bool) string {
	var tabs []string
	for i, r := range m.res {
		label := fmt.Sprintf("%d %s", (i+1)%10, r.title)
		if compact && i != m.tab {
			label = fmt.Sprint((i + 1) % 10)
		}
		if i == m.tab {
			tabs = append(tabs, activeTab.Render(label))
		} else {
			tabs = append(tabs, tabStyle.Render(label))
		}
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
		status = m.spinner.View() + " loading…"
		if m.status != "" && !m.statusErr {
			status += "  " + dimStyle.Render(m.status)
		}
	case m.statusErr:
		status = errStyle.Render("✗ " + m.status)
	case m.status != "":
		status = okStyle.Render(m.status)
	}
	help := dimStyle.Render(m.keyHelp())
	out := status + "\n" + help
	if m.showHelp && m.mode == modeList {
		out += "\n" + dimStyle.Render("tab/1-0 switch · ↑↓ move · [ ] page · / search · esc clear · r reload\n"+
			"enter details · n new · e edit (N/E: all fields) · d delete · h history · t new task for object\n"+
			"a/x attach/detach file · O open file in viewer · o offer on hub / order hub item · s open/close task · D download file · q quit")
	}
	return out
}

func (m *Model) keyHelp() string {
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
	case modeDetail:
		keys = append(keys, "↑↓ scroll", "esc back")
		if u, _ := pictureFile(m.detailItem); u != "" {
			keys = append(keys, "p picture")
		}
		if u, _ := firstAttachment(m.detailItem); u != "" || r.download != nil {
			keys = append(keys, "O open")
		}
	default:
		keys = append(keys, "enter open", "/ search")
		if r.create != nil || r.upload != nil {
			keys = append(keys, "n new")
		}
	}
	if r.update != nil {
		keys = append(keys, "e edit")
	}
	if r.del != nil {
		keys = append(keys, "d delete")
	}
	if r.history != nil {
		keys = append(keys, "h history")
	}
	if r.toggle != nil {
		keys = append(keys, "s open/close")
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
