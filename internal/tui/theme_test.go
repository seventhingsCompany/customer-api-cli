package tui

import (
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func sameColor(a, b color.Color) bool {
	r1, g1, b1, a1 := a.RGBA()
	r2, g2, b2, a2 := b.RGBA()
	return r1 == r2 && g1 == g2 && b1 == b2 && a1 == a2
}

func TestThemeFollowsTerminalBackground(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	t.Cleanup(func() { applyTheme(true) })

	d.send(tea.BackgroundColorMsg{Color: color.White})
	if !sameColor(keyStyle.GetForeground(), successGreen) {
		t.Errorf("light background: accent text should be the dark success green, got %v", keyStyle.GetForeground())
	}
	if !sameColor(titleStyle.GetBackground(), brandGreen) || !sameColor(titleStyle.GetForeground(), brandBlack) {
		t.Error("title bar should be black on brand green")
	}

	d.send(tea.BackgroundColorMsg{Color: color.Black})
	if !sameColor(keyStyle.GetForeground(), brandGreen) {
		t.Errorf("dark background: accent text should be brand green, got %v", keyStyle.GetForeground())
	}
	d.expect("Laptop Dell") // still renders after switching
}
