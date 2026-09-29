package tui

import (
	"net/http"
	"strings"
	"testing"
)

func TestJKPageNavigation(t *testing.T) {
	f := newFakeAPI(t)
	pagedObjects(f)
	f.extra["GET objects/count"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"count":3}`))
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, settings: Settings{PageSize: 1}})
	d.expect("Item p1", "j/k page")
	d.key("j")
	d.expect("Item p2", "page 2 of 3")
	d.key("k")
	d.expect("Item p1", "page 1 of 3")
	before := countCalls(f, "GET objects?")
	d.key("k")
	if countCalls(f, "GET objects?") != before || d.m.tabs[0].page != 1 {
		t.Fatal("k paged before the first page")
	}
	d.key("j", "j")
	d.expect("Item p3", "page 3 of 3")
	before = countCalls(f, "GET objects?")
	d.key("j")
	if countCalls(f, "GET objects?") != before || d.m.tabs[0].page != 3 {
		t.Fatal("j paged beyond the last page")
	}
}

func TestJKDoNotMoveListSelectionOrInterruptSearch(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("down")
	if d.m.table.Cursor() != 1 {
		t.Fatal("arrow keys no longer select rows")
	}
	d.key("k", "j")
	if d.m.table.Cursor() != 1 {
		t.Fatal("paging keys fell through to row selection at a page boundary")
	}
	d.key("s", "j", "k")
	if d.m.search.Value() != "jk" || d.m.tabs[0].page != 1 {
		t.Fatal("paging keys intercepted search input")
	}
}

func TestJKScrollOtherViews(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	for _, view := range []mode{modeDetail, modeHistory} {
		d.m.mode = view
		d.m.detail.SetContent(strings.Repeat("Long detail line\n", 100))
		d.m.detail.GotoTop()
		d.key("j")
		if d.m.detail.YOffset() == 0 {
			t.Fatalf("j did not scroll view %v", view)
		}
		d.key("k")
		if d.m.detail.YOffset() != 0 || d.m.tabs[0].page != 1 {
			t.Fatalf("k did not scroll view %v back without paging", view)
		}
	}
	d.m.mode = modeList
	d.key("?")
	d.key("j")
	if d.m.help.YOffset() == 0 {
		t.Fatal("j did not scroll help")
	}
	d.key("k", "esc", ",", "j")
	if d.m.settingsRow != rowRateLimit {
		t.Fatal("j did not move down in Settings")
	}
	d.key("k")
	if d.m.settingsRow != rowPageSize {
		t.Fatal("k did not move up in Settings")
	}
}
