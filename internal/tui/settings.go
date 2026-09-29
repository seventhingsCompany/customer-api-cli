package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

const (
	// DefaultPageSize is the number of rows per page unless the profile
	// sets one.
	DefaultPageSize = 50
	// MaxPageSize keeps pages within what the API returns in one response.
	MaxPageSize = 100
)

// Settings is the configuration shown on the settings tab.
type Settings struct {
	Credentials     string // where the tokens come from (keyring, file, env:…)
	RateLimit       int    // requests per minute, 0 = unlimited
	RateLimitSource string // --rate-limit, SEVENTHINGS_RATE_LIMIT, profile or default
	PageSize        int
	ProfileOverride string // explains why switching the effective profile is blocked
}

type settingsRow int

const (
	rowPageSize settingsRow = iota
	rowRateLimit
	rowLogout
	rowProfile
	settingsRows
)

type logoutMsg struct{ err error }

// onSettings reports whether the settings tab is shown, including its forms
// and confirmations.
func (m *Model) onSettings() bool {
	return m.mode == modeSettings || (m.mode == modeForm || m.mode == modeConfirm) && m.prev == modeSettings
}

func (m *Model) openSettings() tea.Cmd {
	m.navigate()
	m.mode = modeSettings
	m.setStatus("", false)
	m.searching = false
	m.search.Blur()
	return nil
}

func (m *Model) handleSettingsKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "q":
		return m, tea.Quit
	case "esc", "backspace":
		return m, m.switchTab(m.tab)
	case "tab", "right":
		return m, m.switchTab(0)
	case "shift+tab", "left":
		return m, m.switchTab(len(m.res) - 1)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9", "0":
		n := int(k[0]-'0') - 1
		if k == "0" {
			n = 9
		}
		if n < len(m.res) {
			return m, m.switchTab(n)
		}
	case "up", "k":
		m.settingsRow = (m.settingsRow + settingsRows - 1) % settingsRows
	case "down", "j":
		m.settingsRow = (m.settingsRow + 1) % settingsRows
	case "enter":
		return m, m.settingsAction()
	}
	return m, nil
}

func (m *Model) settingsAction() tea.Cmd {
	if m.writing > 0 {
		m.setStatus("Wait for the pending operation before changing settings", true)
		return nil
	}
	s := m.deps.Settings()
	switch m.settingsRow {
	case rowPageSize:
		return m.openValueForm(formPageSize, fmt.Sprintf("Rows per page (1–%d)", MaxPageSize), s.PageSize, 1, MaxPageSize)
	case rowRateLimit:
		return m.openValueForm(formRateLimit, "Requests per minute (0 = no limit)", s.RateLimit, 0, 100000)
	case rowLogout:
		if strings.HasPrefix(s.Credentials, "env:") {
			m.setStatus(fmt.Sprintf("Credentials come from the environment (%s); unset them to log out", strings.TrimPrefix(s.Credentials, "env:")), true)
			return nil
		}
		name, url, _, _ := m.deps.Profile()
		m.ask(fmt.Sprintf("Log out of %s (profile %s)? [y/N]", url, name), func() tea.Cmd {
			m.operation = "Logging out…"
			return m.async(0, func() tea.Msg { return logoutMsg{err: m.deps.Logout(m.ctx)} })
		})
	case rowProfile:
		if s.ProfileOverride != "" {
			m.setStatus(s.ProfileOverride, true)
			return nil
		}
		name, _, _, _ := m.deps.Profile()
		m.profileChoice = name
		var options []huh.Option[string]
		for _, p := range m.deps.Profiles() {
			options = append(options, huh.NewOption(p, p))
		}
		if len(options) < 2 {
			m.setStatus("No other profiles; add one with seventhings config set <name> --url <URL>", false)
			return nil
		}
		m.form = huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Title("Switch profile").
			Options(options...).Value(&m.profileChoice).Height(min(len(options), 8)))).
			WithWidth(m.formWidth()).WithHeight(m.formHeight()).WithShowHelp(true).WithTheme(formTheme)
		m.formKind, m.prev, m.mode = formProfile, modeSettings, modeForm
		return m.form.Init()
	}
	return nil
}

// valueForm asks for a single number.
type valueForm struct {
	value string
	form  *huh.Form
}

func (m *Model) openValueForm(kind formKind, title string, current, lo, hi int) tea.Cmd {
	vf := &valueForm{value: strconv.Itoa(current)}
	vf.form = huh.NewForm(huh.NewGroup(
		huh.NewInput().Title(title).Value(&vf.value).Validate(func(s string) error {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || n < lo || n > hi {
				return fmt.Errorf("enter a whole number from %d to %d", lo, hi)
			}
			return nil
		}),
	)).WithWidth(m.formWidth()).WithShowHelp(true).WithTheme(formTheme)
	m.value = vf
	m.form = vf.form
	m.formKind = kind
	m.prev = m.mode
	m.mode = modeForm
	return m.form.Init()
}

// submitSetting saves a value entered on the settings tab.
func (m *Model) submitSetting(kind formKind) {
	n, _ := strconv.Atoi(strings.TrimSpace(m.value.value))
	switch kind {
	case formPageSize:
		if err := m.deps.SetPageSize(n); err != nil {
			m.setStatus("Saving page size: "+err.Error(), true)
			return
		}
		m.pageSize = n
		// Pages were cut at the old size: reload them from the start.
		for i := range m.tabs {
			m.tabs[i].page, m.tabs[i].loaded = 1, false
		}
		m.setStatus(fmt.Sprintf("Page size set to %d rows", n), false)
	case formRateLimit:
		if err := m.deps.SetRateLimit(n); err != nil {
			m.setStatus("Saving rate limit: "+err.Error(), true)
			return
		}
		if s := m.deps.Settings(); s.RateLimitSource != "profile" {
			m.setStatus(fmt.Sprintf("Saved to the profile, but %s (%d/min) takes precedence", s.RateLimitSource, s.RateLimit), true)
			return
		}
		m.setStatus(fmt.Sprintf("Rate limit set to %s", rateText(n)), false)
	}
}

// loggedOut resets the session state and shows the login form.
func (m *Model) loggedOut() tea.Cmd {
	cleanup := m.resetSession()
	m.openLogin()
	m.setStatus("Logged out", false)
	return tea.Batch(cleanup, m.form.Init())
}

func (m *Model) resetSession() tea.Cmd {
	m.cancelReads()
	m.session++
	for i := range m.tabs {
		m.tabs[i] = tabState{page: 1, total: noTotal}
	}
	m.tab, m.settingsRow = 0, 0
	m.detailItem = nil
	m.defs = map[models.AssetTrackingTemplate][]models.FieldDefinition{}
	m.choiceCache = map[choiceSource][][2]string{}
	m.pictures = map[string]*picture{}
	cleanup := m.kitty.cleanup()
	m.kitty.ids = map[kittyKey]int{}
	m.ov.cache = map[string]string{}
	m.pane = detailPane{}
	m.detail.SetContent("")
	m.showHelp = false
	m.form, m.record, m.login, m.path = nil, nil, nil, nil
	m.prefill, m.custom = nil, nil
	m.pageSize = m.deps.Settings().PageSize
	m.refreshTable()
	m.search.SetValue("")
	if cleanup != "" {
		return tea.Raw(cleanup)
	}
	return nil
}

func (m *Model) submitProfile() tea.Cmd {
	if err := m.deps.SwitchProfile(m.profileChoice); err != nil {
		return m.fail(err)
	}
	cleanup := m.resetSession()
	if !m.deps.LoggedIn() {
		m.openLogin()
		m.setStatus("Log in to profile "+m.profileChoice, false)
		return tea.Batch(cleanup, m.form.Init())
	}
	m.mode = modeList
	m.setStatus("Switched to profile "+m.profileChoice, false)
	m.keepStatus = true
	return tea.Batch(cleanup, m.load())
}

func rateText(n int) string {
	if n == 0 {
		return "no limit"
	}
	return fmt.Sprintf("%d requests/min", n)
}

func (m *Model) renderSettings() string {
	name, url, clientID, username := m.deps.Profile()
	s := m.deps.Settings()
	var b strings.Builder
	info := [][2]string{{"Profile", name}, {"Instance", url}, {"Client ID", clientID}, {"User", username}, {"Credentials", s.Credentials}}
	for _, kv := range info {
		fmt.Fprintf(&b, "  %s  %s\n", keyStyle.Render(fmt.Sprintf("%-12s", kv[0])), kv[1])
	}
	b.WriteString("\n")
	rows := []struct{ label, value, hint string }{
		{"Page size", fmt.Sprintf("%d rows", s.PageSize), ""},
		{"Rate limit", rateText(s.RateLimit), "(" + s.RateLimitSource + ")"},
		{"Log out", "", ""},
		{"Switch profile", name, s.ProfileOverride},
	}
	for i, r := range rows {
		line := fmt.Sprintf("%-12s  %s", r.label, r.value)
		if r.hint != "" {
			line += " " + dimStyle.Render(r.hint)
		}
		if settingsRow(i) == m.settingsRow {
			b.WriteString(keyStyle.Render("› ") + promptStyle.Render(fmt.Sprintf("%-12s", r.label)) + strings.TrimPrefix(line, fmt.Sprintf("%-12s", r.label)) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}
