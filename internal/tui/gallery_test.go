package tui

import (
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func galleryAPI(t *testing.T) *fakeAPI {
	f, c := newCatalogAPI(t)
	c.items["c1"][catalogPrefix+"picture"] = []any{
		Item{"uuid": "gallery-front", "name": "front.png", "type": "image/png"},
		Item{"uuid": "gallery-back", "name": "back.png", "type": "image/png"},
	}
	c.items["c1"][catalogPrefix+"custom"] = []any{
		Item{"uuid": "gallery-front", "name": "duplicate.png", "type": "image/png"},
		Item{"uuid": "gallery-side", "name": "side.png", "type": "image/png"},
		Item{"uuid": "not-image", "name": "manual.pdf", "type": "application/pdf"},
	}
	data := testPNG(t)
	for _, id := range []string{"gallery-front", "gallery-back", "gallery-side"} {
		for _, path := range []string{"thumbnail", "data"} {
			f.extra["GET file/"+id+"/"+path] = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }
		}
	}
	return f
}

func TestGalleryOrderAndDeduplication(t *testing.T) {
	item := Item{
		"picture": []any{Item{"uuid": "first", "type": "image/png"}, Item{"uuid": "second", "type": "image/jpeg"}},
		"z":       []any{Item{"uuid": "last", "type": "image/png"}},
		"a":       []any{Item{"uuid": "first", "type": "image/png"}, Item{"uuid": "third", "type": "image/png"}, nil, Item{"uuid": "pdf", "type": "application/pdf"}},
	}
	want := []pictureRef{{uuid: "first"}, {uuid: "second"}, {uuid: "third"}, {uuid: "last"}}
	if got := pictureFiles(item); !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered images: %v", got)
	}
	if got := pictureFiles(Item{"fields": Item{catalogPrefix + "picture": item["picture"]}}); !reflect.DeepEqual(got, want[:2]) {
		t.Fatalf("catalog gallery: %v", got)
	}
}

func TestGalleryNavigationCachingAndSelectedViewer(t *testing.T) {
	f := galleryAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("0", "enter")
	d.expect("1/3 front.png", "[/] images", "▀")
	if f.called("GET file/gallery-back/") || f.called("GET file/gallery-side/") {
		t.Fatal("gallery eagerly downloaded unselected images")
	}
	d.key("]")
	d.expect("2/3 back.png")
	d.key("]")
	d.expect("3/3 side.png")
	d.key("]")
	d.expect("1/3 front.png")
	d.key("[")
	d.expect("3/3 side.png")
	d.key("p", "]", "p")
	d.expect("3/3 side.png")
	d.send(tea.WindowSizeMsg{Width: 144, Height: 36})
	d.expect("3/3 side.png")
	for _, id := range []string{"gallery-front", "gallery-back", "gallery-side"} {
		if countCalls(f, "GET file/"+id+"/thumbnail?") != 1 {
			t.Fatal("gallery failed to cache " + id)
		}
	}
	var opened string
	d.m.opener = func(path string) error { opened = path; return nil }
	d.key("O")
	d.expect("Opened side.png in the default viewer")
	if !strings.HasSuffix(opened, "gallery-side-side.png") {
		t.Fatalf("opened wrong image: %s", opened)
	}
	t.Cleanup(func() { _ = os.Remove(opened) })
	d.key("esc", "enter")
	d.expect("1/3 front.png")
	d.key("esc", "down", "enter")
	if uuid, _ := d.m.currentPicture(); uuid != "" || d.m.pictureIndex != 0 {
		t.Fatal("gallery state leaked into another record")
	}
}

func TestGalleryLateImageAndFailure(t *testing.T) {
	f := galleryAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("0", "enter")
	second := d.press("]")
	d.key("]")
	d.expect("3/3 side.png")
	d.exec(second)
	d.expect("3/3 side.png")
	if uuid, _ := d.m.currentPicture(); uuid != "gallery-side" {
		t.Fatal("late image replaced the selected picture")
	}
	f.extra["GET file/gallery-back/thumbnail"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }
	f.extra["GET file/gallery-back/data"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }
	delete(d.m.pictures, "gallery-back")
	d.key("[")
	d.expect("2/3 back.png", "no preview:")
	d.key("]")
	d.expect("3/3 side.png", "▀")
}

func TestGalleryFullSizeProtocols(t *testing.T) {
	for _, mode := range []string{"kitty", "iterm2", "sixel", "off"} {
		t.Run(mode, func(t *testing.T) {
			f := galleryAPI(t)
			d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, env: map[string]string{"SEVENTHINGS_IMAGES": mode}})
			d.key("0", "enter", "]")
			if mode == "off" {
				if d.m.pictureIndex != 0 || f.called("GET file/gallery-") {
					t.Fatal("gallery fetched images while disabled")
				}
				return
			}
			d.expect("2/3 back.png")
			if !f.called("GET file/gallery-back/data?") {
				t.Fatal("gallery did not fetch the full-size selected image")
			}
			d.key("[")
			d.expect("1/3 front.png")
			if countCalls(f, "GET file/gallery-front/data?") != 1 {
				t.Fatal("full-size image cache not reused")
			}
		})
	}
}
