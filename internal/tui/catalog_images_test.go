package tui

import (
	"net/http"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func catalogPictureAPI(t *testing.T) *fakeAPI {
	f, c := newCatalogAPI(t)
	c.items["c1"][catalogPrefix+"picture"] = []any{Item{"uuid": "catalog-img", "name": "catalog-front.png", "type": "image/png", "size": 2048}}
	png := testPNG(t)
	f.extra["GET file/catalog-img/thumbnail"] = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(png) }
	f.extra["GET file/catalog-img/data"] = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(png) }
	return f
}

func TestCatalogPictureSelection(t *testing.T) {
	fields := Item{
		catalogPrefix + "custom":    []any{Item{"uuid": "custom", "name": "side.jpg", "type": "image/jpeg", "size": 100}},
		catalogPrefix + "picture":   []any{Item{"uuid": "picture", "name": "front.png", "type": "image/png", "size": 100}},
		catalogPrefix + "documents": []any{Item{"uuid": "document", "name": "manual.pdf", "type": "application/pdf", "size": 100}},
	}
	item := Item{"uuid": "catalog-record", "fields": fields}
	if id, name := pictureFile(item); id != "picture" || name != "front.png" {
		t.Fatalf("catalog picture did not take priority: %s %s", id, name)
	}
	delete(fields, catalogPrefix+"picture")
	if id, _ := pictureFile(item); id != "custom" {
		t.Fatalf("custom image attachment fallback: %s", id)
	}
	delete(fields, catalogPrefix+"custom")
	if id, _ := pictureFile(item); id != "" {
		t.Fatalf("non-image attachment selected: %s", id)
	}
	if id, name := firstAttachment(item); id != "document" || name != "manual.pdf" {
		t.Fatalf("catalog attachment not found: %s %s", id, name)
	}
	if item["uuid"] != "catalog-record" || len(item) != 2 {
		t.Fatal("image discovery changed the record shape")
	}
	if id, _ := pictureFile(Item{"uuid": "empty", "fields": nil}); id != "" {
		t.Fatal("empty catalog item selected an image")
	}
}

func TestCatalogDetailPictureToggleCacheAndViewer(t *testing.T) {
	f := catalogPictureAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("0", "enter")
	d.expect("Catalog Laptop", "catalog-front.png · p hides", "▀", "p picture", "O open")
	if countCalls(f, "GET file/catalog-img/thumbnail?") != 1 {
		t.Fatal("catalog thumbnail not loaded")
	}
	d.key("p")
	d.expect("picture hidden (p to show)")
	if strings.Contains(d.screen(), "▀") {
		t.Fatal("catalog picture stayed visible")
	}
	d.key("p", "esc", "enter")
	d.expect("▀")
	if countCalls(f, "GET file/catalog-img/thumbnail?") != 1 {
		t.Fatal("catalog thumbnail cache was not reused")
	}
	var opened string
	d.m.opener = func(path string) error { opened = path; return nil }
	d.key("O")
	d.expect("Opened catalog-front.png in the default viewer")
	if opened == "" {
		t.Fatal("catalog image was not opened")
	}
	t.Cleanup(func() { _ = os.Remove(opened) })
	if data, err := os.ReadFile(opened); err != nil || string(data) != string(testPNG(t)) {
		t.Fatalf("viewer file: %v", err)
	}
	d.key("Y")
	d.expect("catalog-items get c1")
}

func TestCatalogPictureProtocols(t *testing.T) {
	for _, protocol := range []string{"kitty", "iterm2", "sixel", "off"} {
		t.Run(protocol, func(t *testing.T) {
			f := catalogPictureAPI(t)
			d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, env: map[string]string{"SEVENTHINGS_IMAGES": protocol}})
			d.key("0", "enter")
			d.expect("Catalog Laptop")
			if protocol == "off" {
				if f.called("GET file/catalog-img/") || d.m.pane.content != "" {
					t.Fatal("images disabled but catalog picture loaded")
				}
				return
			}
			if !f.called("GET file/catalog-img/data?") {
				t.Fatal("full-size catalog picture was not fetched")
			}
			switch protocol {
			case "kitty":
				if rawContaining(d.raw, "a=T", "U=1") != 1 {
					t.Fatal("catalog image not transmitted to kitty")
				}
			case "iterm2":
				if rawContaining(d.raw, "1337;File=") != 1 {
					t.Fatal("catalog image not transmitted to iTerm2")
				}
			case "sixel":
				if rawContaining(d.raw, "\x1bP0;1q") != 1 {
					t.Fatal("catalog image not transmitted as sixel")
				}
			}
		})
	}
}

func TestCatalogPictureFallbackAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		f := catalogPictureAPI(t)
		f.extra["GET file/catalog-img/thumbnail"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }
		if fail {
			f.extra["GET file/catalog-img/data"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }
		}
		d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
		d.key("0", "enter")
		d.expect("Catalog Laptop")
		if d.m.mode != modeDetail {
			t.Fatal("image failure prevented catalog details")
		}
		if fail {
			d.expect("no preview:")
		} else {
			d.expect("▀")
		}
	}
}

func TestCatalogPictureFitsScreen(t *testing.T) {
	f := catalogPictureAPI(t)
	for _, size := range [][2]int{{144, 36}, {80, 24}, {40, 24}, {40, 16}} {
		d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
		d.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		d.key("0", "enter")
		d.expect("▀", "p picture")
		screen := d.screen()
		if len(strings.Split(screen, "\n")) > size[1] {
			t.Fatalf("%v: picture exceeds terminal height:\n%s", size, screen)
		}
		for _, line := range strings.Split(screen, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("%v: picture exceeds terminal width: %q", size, line)
			}
		}
	}
}
