package tui

import (
	"net/http"
	"strings"
	"testing"
)

func TestCLICommand(t *testing.T) {
	f := newFakeAPI(t)
	f.extra = map[string]http.HandlerFunc{
		"GET persons": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"items":[{"person_uuid":"p1","first_name":"Ada"}],"total":1}`))
		},
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	cmd := func() string {
		t.Helper()
		c, _ := d.m.cliCommand()
		return c
	}
	if got, want := cmd(), "seventhings -p test objects list"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	d.key("s", "Lap top", "enter")
	d.key("f", "barcode = it's", "enter", "-updated_at", "enter")
	if got, want := cmd(), `seventhings -p test objects list --filter 'inventory_name like Lap top' --filter 'barcode = it'\''s' --sort -updated_at`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}

	d.key("esc", "down", "enter")
	if got, want := cmd(), "seventhings -p test objects get o2"; got != want {
		t.Errorf("detail: got %q, want %q", got, want)
	}
	d.key("Y")
	if !strings.HasPrefix(d.m.status, "Copied: seventhings -p test objects get o2") {
		t.Errorf("status %q", d.m.status)
	}
	d.key("y")
	if d.m.status != "Copied o2" {
		t.Errorf("status %q", d.m.status)
	}

	d.key("esc", "4", "f", "last_name:desc", "enter")
	if got, want := cmd(), "seventhings -p test persons list --sort last_name --order desc"; got != want {
		t.Errorf("persons: got %q, want %q", got, want)
	}
	d.key("6", "s", "extinguisher", "enter")
	c, note := d.m.cliCommand()
	if c != "seventhings -p test tasks list" || note == "" {
		t.Errorf("tasks: %q, note %q", c, note)
	}
}

func TestShellJoin(t *testing.T) {
	for in, want := range map[string]string{
		"plain-arg_1.2": "plain-arg_1.2",
		"two words":     "'two words'",
		"it's":          `'it'\''s'`,
		"":              "''",
		"$HOME":         "'$HOME'",
	} {
		if got := shellJoin([]string{in}); got != want {
			t.Errorf("shellJoin(%q) = %s, want %s", in, got, want)
		}
	}
}
