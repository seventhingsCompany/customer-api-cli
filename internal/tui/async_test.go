package tui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// press sends a key without running the command it returns, so a test can
// decide when (and in which order) the request's answer arrives.
func (d *driver) press(k string) tea.Cmd {
	var msg tea.KeyPressMsg
	switch k {
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
	}
	_, cmd := d.m.Update(msg)
	return cmd
}

// pagedObjects answers every objects page with one item named after it.
func pagedObjects(f *fakeAPI) {
	f.extra = map[string]http.HandlerFunc{
		"GET objects": func(w http.ResponseWriter, r *http.Request) {
			page := r.URL.Query().Get("page")
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
				{"asset_uuid": "o" + page, "inventory_name": "Item p" + page},
			}})
		},
	}
}

func TestStalePageDropped(t *testing.T) {
	f := newFakeAPI(t)
	pagedObjects(f)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, settings: Settings{PageSize: 1}})
	d.expect("Item p1")

	page2 := d.press("]")
	page3 := d.press("]")
	d.exec(page3)
	d.exec(page2) // late answer for a page the user already left
	d.expect("Item p3", "page 3")
	if strings.Contains(d.screen(), "Item p2") {
		t.Errorf("stale page 2 shown:\n%s", d.screen())
	}
	if d.m.loading != 0 {
		t.Errorf("loading = %d, want 0", d.m.loading)
	}
}

func TestStaleDetailDropped(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.expect("Laptop Dell")

	detail := d.press("enter")
	d.key("2") // Rooms, before the object arrives
	d.exec(detail)
	if d.m.mode != modeList || d.m.tab != 1 {
		t.Fatalf("mode %v tab %d, want list on Rooms", d.m.mode, d.m.tab)
	}
	d.expect("Boardroom")
	if d.m.loading != 0 {
		t.Errorf("loading = %d, want 0", d.m.loading)
	}
}

func TestEscCancelsLoad(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.expect("Laptop Dell")

	detail := d.press("enter")
	d.key("esc")
	d.exec(detail)
	if d.m.mode != modeList {
		t.Fatalf("mode %v, want list", d.m.mode)
	}
	d.expect("Cancelled")
	if d.m.loading != 0 || len(d.m.reads) != 0 {
		t.Errorf("loading = %d, reads = %d, want 0", d.m.loading, len(d.m.reads))
	}
}

func TestPageTotalFromCount(t *testing.T) {
	f := newFakeAPI(t)
	pagedObjects(f)
	f.extra["GET objects/count"] = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"count":2}`))
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, settings: Settings{PageSize: 1}})
	d.expect("Item p1", "page 1 of 2 · 2 objects")
	d.key("]")
	d.expect("Item p2", "page 2 of 2")
	d.key("]") // last page: no request
	d.expect("Item p2")
	if n := countCalls(f, "GET objects/count"); n != 1 {
		t.Errorf("count requested %d times, want once per search", n)
	}
	d.key("r")
	if n := countCalls(f, "GET objects/count"); n != 2 {
		t.Errorf("reload did not recount (%d calls)", n)
	}
}

func TestPagingPastTheEnd(t *testing.T) {
	f := newFakeAPI(t)
	f.extra = map[string]http.HandlerFunc{
		"GET objects": func(w http.ResponseWriter, r *http.Request) {
			items := []map[string]any{}
			if page := r.URL.Query().Get("page"); page < "3" {
				items = append(items, map[string]any{"asset_uuid": "o" + page, "inventory_name": "Item p" + page})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		},
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, settings: Settings{PageSize: 1}})
	d.key("]", "]")
	d.expect("Item p2", "No more objects", "page 2")
	before := countCalls(f, "GET objects?")
	d.key("]") // the end is known now
	if countCalls(f, "GET objects?") != before {
		t.Error("paged past the known end again")
	}
}

func TestPersonsTotal(t *testing.T) {
	f := newFakeAPI(t)
	f.extra = map[string]http.HandlerFunc{
		"GET persons": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"items":[{"person_uuid":"p1","first_name":"Ada"}],"page":1,"per_page":50,"total":120}`))
		},
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("4")
	d.expect("Ada", "page 1 of 3 · 120 persons")
}

func countCalls(f *fakeAPI, prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}
