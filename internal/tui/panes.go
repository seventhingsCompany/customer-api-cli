package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// paneRoot identifies the one top-level tab shared by a pane's subviews.
func (m *Model) paneRoot(tab int) int {
	if pane := m.res[tab].pane; pane != "" {
		for i, r := range m.res {
			if r.pane == pane {
				return i
			}
		}
	}
	return tab
}

func (m *Model) paneTabs() []int {
	var tabs []int
	for i := range m.res {
		if m.paneRoot(i) == i {
			tabs = append(tabs, i)
		}
	}
	return tabs
}

func (m *Model) paneTitle(tab int) string {
	if pane := m.res[tab].pane; pane != "" {
		return pane
	}
	return m.res[tab].title
}

func (m *Model) switchPane(tab int) tea.Cmd {
	if remembered, ok := m.paneViews[m.res[tab].pane]; ok && m.paneRoot(tab) == tab {
		tab = remembered
	}
	return m.switchTab(tab)
}

func (m *Model) stepPane(delta int) tea.Cmd {
	tabs := m.paneTabs()
	for i, tab := range tabs {
		if tab == m.paneRoot(m.tab) {
			next := i + delta
			if next < 0 || next >= len(tabs) {
				return m.openSettings()
			}
			return m.switchPane(tabs[next])
		}
	}
	return nil
}

func (m *Model) hasSubviews() bool {
	return m.res[m.tab].pane != "" && !m.onSettings() && m.mode != modeLogin
}

// Writes in one subview can change another (ordering an item adds an order).
func (m *Model) invalidatePane(tab int) {
	root := m.paneRoot(tab)
	for i := range m.res {
		if m.paneRoot(i) == root {
			m.invalidateTab(i)
		}
	}
}

func (m *Model) switchSubview() tea.Cmd {
	if !m.hasSubviews() {
		return nil
	}
	pane := m.res[m.tab].pane
	for offset := 1; offset < len(m.res); offset++ {
		next := (m.tab + offset) % len(m.res)
		if m.res[next].pane == pane {
			return m.switchTab(next)
		}
	}
	return nil
}

func (m *Model) subviewBar() string {
	var views []string
	for i, r := range m.res {
		if r.pane != m.res[m.tab].pane {
			continue
		}
		if i == m.tab {
			views = append(views, keyStyle.Render("["+r.subview+"]"))
		} else {
			views = append(views, dimStyle.Render(r.subview))
		}
	}
	return strings.Join(views, "  ") + dimStyle.Render(" · v switch")
}
