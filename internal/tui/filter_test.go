package tui

import (
	"net/http"
	"strings"
	"testing"
)

// calledWith reports whether a request starts with prefix and contains
// every part.
func calledWith(f *fakeAPI, prefix string, parts ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) && !lacksAny(c, parts) {
			return true
		}
	}
	return false
}

// lacksAny reports whether s lacks any of parts.
func lacksAny(s string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func TestFilterAndSort(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("f")
	d.expect("Filter", "Sort")
	d.key("inventory_name like Desk", "enter", "-updated_at", "enter")
	if !calledWith(f, "GET objects?", "page=1", "sort[updated_at]=DESC", "filter[inventory_name][like][]=Desk") {
		t.Fatalf("no filtered request: %v", f.calls)
	}
	d.expect("filter: inventory_name like Desk", "sort: -updated_at", "Desk")

	d.key("esc") // clears filter and sort
	if s := d.screen(); strings.Contains(s, "sort: -updated_at") {
		t.Errorf("filter not cleared:\n%s", s)
	}
	d.expect("Laptop Dell")
}

func TestFilterInvalid(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("f", "name ?? x", "enter")
	d.expect("unknown operator")
	if d.m.mode != modeForm {
		t.Errorf("mode %v, want the form to stay open", d.m.mode)
	}
}

func TestSortPersonsAndUsers(t *testing.T) {
	f := newFakeAPI(t)
	f.extra = map[string]http.HandlerFunc{
		"GET persons": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"items":[{"person_uuid":"p1","first_name":"Ada"}],"total":1}`))
		},
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("4", "f")
	if strings.Contains(d.screen(), "Filter") {
		t.Error("persons offer a filter the API does not support")
	}
	d.key("-last_name", "enter")
	if !calledWith(f, "GET persons?", "sort_by=last_name", "order=desc") {
		t.Fatalf("no sorted request: %v", f.calls)
	}

	d.key("5", "f", "name", "enter")
	d.expect("users can be sorted by id or email")
}

func TestFilterUnsupported(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("6", "f")
	d.expect("Tasks cannot be filtered or sorted")
	if d.m.mode != modeList {
		t.Errorf("mode %v, want list", d.m.mode)
	}
}
