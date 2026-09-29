package tui

import (
	"testing"
)

func TestSettingsTab(t *testing.T) {
	f := newFakeAPI(t)
	deps := &fakeDeps{url: f.srv.URL, loggedIn: true}
	d := newDriver(t, deps)
	d.key(",")
	d.expect("1 Objects", "0 Hub orders", "Settings", "Profile", "test", "Page size", "50 rows", "Rate limit", "200 requests/min (default)", "Log out")

	// Page size: saved, and lists reload with it.
	d.key("enter")
	d.expect("Rows per page (1–100)")
	d.key("backspace", "backspace", "500", "enter")
	d.expect("enter a whole number from 1 to 100")
	d.key("backspace", "backspace", "backspace", "20", "enter")
	d.expect("Page size set to 20 rows", "20 rows")
	if deps.Settings().PageSize != 20 {
		t.Errorf("page size not saved: %+v", deps.settings)
	}
	d.key("1")
	d.expect("Laptop Dell")
	if !f.called("GET objects?page=1&per_page=20") {
		t.Errorf("calls: %v", f.calls)
	}

	// Rate limit: shift+tab from the first tab wraps to settings.
	d.key("left", "down", "enter")
	d.expect("Requests per minute (0 = no limit)")
	d.key("backspace", "backspace", "backspace", "0", "enter")
	d.expect("Rate limit set to no limit", "no limit (profile)")

	// Log out asks first, then shows the login form.
	d.key("down", "enter")
	d.expect("Log out of " + f.srv.URL + " (profile test)? [y/N]")
	d.key("n")
	d.expect("Cancelled", "Page size")
	d.key("enter", "y")
	d.expect("Logged out", "Log in to seventhings")
	if deps.LoggedIn() {
		t.Error("still logged in")
	}
}

func TestSettingsLogoutEnvCredentials(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, settings: Settings{Credentials: "env:SEVENTHINGS_TOKEN"}})
	d.key(",", "down", "down", "enter")
	d.expect("Credentials come from the environment (SEVENTHINGS_TOKEN)")
}
