package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func newCHubAPI(t *testing.T) *fakeAPI {
	f := newFakeAPI(t)
	f.extra = map[string]http.HandlerFunc{
		"GET circularity-hub/orders": func(w http.ResponseWriter, r *http.Request) {
			page := r.URL.Query().Get("page")
			_ = json.NewEncoder(w).Encode(Item{"items": []Item{{"id": 99, "order_number": "CH-" + page, "total_price": 30, "completed": false, "cancelled": false}}})
		},
		"GET circularity-hub/item/12": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(Item{"id": 12, "price": "30.00", "object_data": Item{"internal_identifier": "BC-CHAIR"}})
		},
		"GET circularity-hub/order/99": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(Item{"id": 99, "order_number": "CH-1", "total_price": 30})
		},
		"DELETE circularity-hub/item/12": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) },
	}
	return f
}

func TestCHubSinglePaneAndNavigation(t *testing.T) {
	f := newCHubAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	bar := ansi.Strip(d.m.tabBar(false))
	if strings.Count(bar, "CHub") != 1 || strings.Contains(bar, "Hub items") || strings.Contains(bar, "Hub orders") {
		t.Fatalf("expected one CHub top-level tab: %s", bar)
	}
	d.key("8", "tab")
	d.expect("9 CHub", "[Items]  Orders", "v switch", "BC-CHAIR")
	if f.called("GET circularity-hub/orders?") {
		t.Fatal("opening CHub eagerly loaded Orders")
	}
	d.key("tab")
	if d.m.res[d.m.tab].cli != "catalog-items" {
		t.Fatal("Tab should leave CHub, not visit another top-level hub tab")
	}
	d.key("left", "v")
	d.expect("9 CHub", "Items  [Orders]", "CH-1")
	d.key("tab", "left")
	d.expect("Items  [Orders]", "CH-1")
	d.key("1", "9")
	d.expect("Items  [Orders]")
	d.key("0")
	d.expect("0 Catalog")
	if d.m.res[d.m.tab].cli != "catalog-items" {
		t.Fatal("0 must open Catalog rather than CHub Orders")
	}
	d.key("left")
	d.expect("Items  [Orders]")
	d.key(",", "9")
	d.expect("Items  [Orders]")
	d.key("left")
	if d.m.res[d.m.tab].cli != "files" {
		t.Fatal("previous pane from Orders should be Files")
	}
	d.key("tab", "v", "enter")
	d.expect("BC-CHAIR", "esc back")
	d.key("v")
	if d.m.mode != modeList {
		t.Fatal("switching subview from details should return to its list")
	}
	d.expect("Items  [Orders]")
}

func TestCHubIndependentQueriesAndCopiedCommands(t *testing.T) {
	f := newCHubAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, settings: Settings{PageSize: 1}})
	d.key("9", "s", "vase", "enter", "f", "price = 30", "enter", "-created_at", "enter", "j")
	itemsTab := d.m.tab
	d.key("v")
	ordersTab := d.m.tab
	if d.m.tabs[ordersTab].page != 1 || d.m.tabs[ordersTab].search != "" || d.m.tabs[ordersTab].filterText != "" {
		t.Fatal("Orders inherited Items query state")
	}
	d.key("s", "invoice", "enter", "f", "completed = false", "enter", "-id", "enter", "j", "Y")
	if lacksAny(d.m.status, []string{"hub orders list", "--page 2", "order_number like invoice", "completed = false", "--sort -id"}) {
		t.Fatalf("wrong Orders command: %s", d.m.status)
	}
	d.key("enter", "Y")
	d.expect("hub orders get 99")
	d.key("esc", "v", "Y")
	d.expect("[Items]  Orders")
	if lacksAny(d.m.status, []string{"hub items list", "--page 2", "name like vase", "price = 30", "--sort -created_at"}) {
		t.Fatalf("wrong Items command: %s", d.m.status)
	}
	if d.m.tab != itemsTab || d.m.tabs[itemsTab].search != "vase" || d.m.tabs[ordersTab].search != "invoice" {
		t.Fatal("switching lost separate search state")
	}
	d.key("enter", "Y")
	d.expect("hub items get 12")
	if !calledWith(f, "GET circularity-hub/items?", "page=2", "filter[name][like][]=vase", "sort[created_at]=DESC") || !calledWith(f, "GET circularity-hub/orders?", "page=2", "filter[order_number][like][]=invoice", "sort[id]=DESC") {
		t.Fatal("subview queries used the wrong endpoint or fields")
	}
}

func TestCHubActionsAndRefresh(t *testing.T) {
	f := newCHubAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("9", "v", "o", "d")
	if d.m.mode != modeList || f.called("POST circularity-hub/orders?") || f.called("DELETE circularity-hub/") {
		t.Fatal("item-only actions enabled for Orders")
	}
	d.key("v", "o", "n")
	if f.called("POST circularity-hub/orders?") {
		t.Fatal("cancelled order reached server")
	}
	d.key("o")
	write := d.press("y")
	d.key("v") // Orders may already be cached while the write is in flight.
	before := countCalls(f, "GET circularity-hub/orders?")
	d.exec(write)
	d.expect("Items  [Orders]", "Created hub order 99")
	if countCalls(f, "GET circularity-hub/orders?") != before+1 {
		t.Fatal("new order did not refresh the active Orders subview")
	}
	d.key("v", "d", "n")
	if f.called("DELETE circularity-hub/") {
		t.Fatal("cancelled delete reached server")
	}
	d.key("d", "y")
	if !f.called("DELETE circularity-hub/item/12?") {
		t.Fatal("item delete used the wrong endpoint")
	}
}

func TestCHubOrderDoesNotSwitchSubview(t *testing.T) {
	f := newCHubAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("9", "o", "y")
	d.expect("Created hub order 99", "[Items]  Orders")
	if d.m.res[d.m.tab].cli != "hub items" || f.called("GET circularity-hub/orders?") {
		t.Fatal("ordering must not automatically jump to Orders")
	}
}

func TestCHubStaleDetailAndUnavailableSubview(t *testing.T) {
	f := newCHubAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("9")
	detail := d.press("enter")
	d.key("v")
	d.exec(detail)
	if d.m.mode != modeList || d.m.res[d.m.tab].cli != "hub orders" {
		t.Fatal("stale item detail replaced Orders")
	}
	f.extra["GET circularity-hub/orders"] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"message":"Orders unavailable"}`))
	}
	d.key("r")
	d.expect("Orders unavailable", "Items  [Orders]")
	d.key("v")
	d.expect("BC-CHAIR", "[Items]  Orders")
	d.key("s", "v")
	if d.m.search.Value() != "v" || d.m.res[d.m.tab].cli != "hub items" {
		t.Fatal("subview shortcut intercepted search input")
	}
}

func TestCHubFitsSmallTerminals(t *testing.T) {
	f := newCHubAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	for _, size := range [][2]int{{40, 16}, {40, 24}, {80, 24}, {144, 36}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			d.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			d.key("9")
			for _, key := range []string{"", "v", "enter", "esc", "f", "esc", "?", "esc", ",", "esc"} {
				if key != "" {
					d.key(key)
				}
				screen := d.screen()
				if len(strings.Split(screen, "\n")) > size[1] {
					t.Fatalf("view exceeds height:\n%s", screen)
				}
				for _, line := range strings.Split(screen, "\n") {
					if ansi.StringWidth(line) > size[0] {
						t.Fatalf("view exceeds width: %q", line)
					}
				}
			}
		})
	}
}
