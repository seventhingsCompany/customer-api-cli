package tui

import (
	"charm.land/bubbles/v2/table"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// Brand palette from seventhings.com (--brand-colours--*, --system-colours--*).
var (
	brandGreen     = lipgloss.Color("#00fa8c") // primary brand colour
	brandSky       = lipgloss.Color("#d4f4ff") // tertiary brand colour
	brandBlack     = lipgloss.Color("#212121")
	brandVeryDark  = lipgloss.Color("#373737")
	brandDarkGrey  = lipgloss.Color("#707070")
	brandGrey      = lipgloss.Color("#d8d8d8")
	brandLightGrey = lipgloss.Color("#f1f1f1")
	successGreen   = lipgloss.Color("#027a48")
	errorRed       = lipgloss.Color("#b42318")
	errorRedBright = lipgloss.Color("#ef6051") // readable on dark backgrounds
)

// Styles used by the views. applyTheme sets them for the terminal
// background; they start with the dark variant, the most common setup.
var (
	titleStyle  lipgloss.Style
	tabStyle    lipgloss.Style
	activeTab   lipgloss.Style
	keyStyle    lipgloss.Style
	dimStyle    lipgloss.Style
	errStyle    lipgloss.Style
	okStyle     lipgloss.Style
	promptStyle lipgloss.Style
	boxStyle    lipgloss.Style
	tableTheme  table.Styles
	formTheme   = huh.ThemeFunc(brandFormTheme)
)

func init() { applyTheme(true) }

// applyTheme sets the styles for a dark or light terminal background. The
// bright brand green carries text only on dark backgrounds; on light ones
// text accents use the darker success green, and green backgrounds keep
// black text, which reads well on both.
func applyTheme(isDark bool) {
	ld := lipgloss.LightDark(isDark)
	accentText := ld(successGreen, brandGreen)
	muted := ld(brandDarkGrey, lipgloss.Color("#9a9a9a"))

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(brandBlack).Background(brandGreen).Padding(0, 1)
	tabStyle = lipgloss.NewStyle().Padding(0, 1).Foreground(muted)
	activeTab = lipgloss.NewStyle().Padding(0, 1).Bold(true).Foreground(accentText).Underline(true)
	keyStyle = lipgloss.NewStyle().Bold(true).Foreground(accentText)
	dimStyle = lipgloss.NewStyle().Foreground(muted)
	errStyle = lipgloss.NewStyle().Bold(true).Foreground(ld(errorRed, errorRedBright))
	okStyle = lipgloss.NewStyle().Foreground(accentText)
	promptStyle = lipgloss.NewStyle().Bold(true).Foreground(ld(brandBlack, brandSky))
	boxStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(brandGreen).Padding(0, 1)

	tableTheme = table.DefaultStyles()
	tableTheme.Header = tableTheme.Header.Bold(true).
		BorderStyle(lipgloss.NormalBorder()).BorderBottom(true).BorderForeground(ld(brandGrey, brandVeryDark))
	tableTheme.Selected = tableTheme.Selected.Bold(false).Foreground(brandBlack).Background(brandGreen)
}

// brandFormTheme styles huh forms with the brand palette.
func brandFormTheme(isDark bool) *huh.Styles {
	t := huh.ThemeBase(isDark)
	ld := lipgloss.LightDark(isDark)
	accentText := ld(successGreen, brandGreen)
	normal := ld(brandBlack, brandLightGrey)
	muted := ld(brandDarkGrey, lipgloss.Color("#9a9a9a"))
	errColor := ld(errorRed, errorRedBright)

	t.Focused.Base = t.Focused.Base.BorderForeground(brandGreen)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(accentText).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(accentText).Bold(true).MarginBottom(1)
	t.Focused.Description = t.Focused.Description.Foreground(muted)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(errColor)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(errColor)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(accentText)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(accentText)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(accentText)
	t.Focused.Option = t.Focused.Option.Foreground(normal)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(accentText)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(accentText)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(accentText).SetString("✓ ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(muted).SetString("• ")
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(normal)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(brandBlack).Background(brandGreen).Bold(true)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(normal).Background(ld(brandGrey, brandVeryDark))
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(brandGreen)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(muted)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(accentText)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description
	return t
}
