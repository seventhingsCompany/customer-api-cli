package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestRenderHalfBlocks(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255}) // top-left red
	img.Set(0, 1, color.NRGBA{0, 0, 255, 255}) // bottom-left blue
	img.Set(1, 0, color.NRGBA{0, 255, 0, 255}) // top-right green, bottom-right transparent

	got := renderHalfBlocks(img, 10, 10)
	want := "\x1b[38;2;255;0;0m\x1b[48;2;0;0;255m▀" + "\x1b[49m\x1b[38;2;0;255;0m▀" + "\x1b[0m"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestRenderHalfBlocksFitsBox(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	for y := range 100 {
		for x := range 100 {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	out := ansi.Strip(renderHalfBlocks(img, 40, 20))
	lines := strings.Split(out, "\n")
	if len(lines) != 20 || len([]rune(lines[0])) != 40 {
		t.Errorf("square image in 40x20 cells: got %d rows x %d cols", len(lines), len([]rune(lines[0])))
	}
	// Never upscales small images.
	small := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	if lines := strings.Split(ansi.Strip(renderHalfBlocks(small, 40, 20)), "\n"); len(lines) != 2 || len([]rune(lines[0])) != 4 {
		t.Errorf("4x4 image: got %d rows x %d cols", len(lines), len([]rune(lines[0])))
	}
}

func TestPictureFile(t *testing.T) {
	obj := Item{
		"documents": []any{map[string]any{"uuid": "d1", "type": "application/pdf", "name": "manual.pdf"}},
		"custom":    []any{map[string]any{"uuid": "c1", "type": "image/jpeg", "name": "side.jpg"}},
		"picture":   []any{map[string]any{"uuid": "p1", "type": "image/png", "name": "front.png"}},
	}
	if u, n := pictureFile(obj); u != "p1" || n != "front.png" {
		t.Errorf("prefers picture field: %s %s", u, n)
	}
	delete(obj, "picture")
	if u, _ := pictureFile(obj); u != "c1" {
		t.Errorf("falls back to any image attachment: %s", u)
	}
	file := Item{"uuid": "f1", "name": "x.png", "type": "image/png", "data_uri": "/file/f1/data"}
	if u, _ := pictureFile(file); u != "f1" {
		t.Errorf("file itself: %s", u)
	}
	if u, _ := pictureFile(Item{"uuid": "f2", "type": "text/plain", "data_uri": "x"}); u != "" {
		t.Errorf("non-image file: %s", u)
	}
}

func testPNG(t *testing.T) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDetailShowsPicture(t *testing.T) {
	f := newFakeAPI(t)
	f.extra = map[string]http.HandlerFunc{
		"GET object/o1": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"asset_uuid":"o1","inventory_name":"Laptop Dell","picture":[{"uuid":"img1","name":"front.png","type":"image/png"}]}`))
		},
		"GET file/img1/thumbnail": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(testPNG(t)) },
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("enter")
	d.expect("inventory_name", "▀", "front.png · p hides", "p picture")
	if !f.called("GET file/img1/thumbnail") {
		t.Errorf("thumbnail not fetched: %v", f.calls)
	}

	d.key("p")
	d.expect("picture hidden (p to show)")
	if strings.Contains(d.screen(), "▀") {
		t.Error("picture still shown after p")
	}
	d.key("p")
	d.expect("▀")
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "GET file/img1/thumbnail") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("thumbnail fetched %d times, want 1 (cached)", n)
	}
}

func TestFailedPictureIsRetried(t *testing.T) {
	f := newFakeAPI(t)
	fail := true
	f.extra = map[string]http.HandlerFunc{
		"GET object/o1": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"asset_uuid":"o1","inventory_name":"Laptop Dell","picture":[{"uuid":"img1","name":"front.png","type":"image/png"}]}`))
		},
		"GET file/img1/thumbnail": func(w http.ResponseWriter, r *http.Request) {
			if fail {
				w.WriteHeader(503)
				return
			}
			_, _ = w.Write(testPNG(t))
		},
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("enter")
	d.expect("inventory_name")
	if strings.Contains(d.screen(), "▀") {
		t.Fatal("picture shown despite error")
	}
	fail = false
	d.key("p", "p")
	d.expect("▀")
}

func TestDetailPictureFallsBackToFullFile(t *testing.T) {
	f := newFakeAPI(t)
	f.extra = map[string]http.HandlerFunc{
		"GET object/o1": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"asset_uuid":"o1","inventory_name":"Laptop Dell","picture":[{"uuid":"img2","name":"x.png","type":"image/png"}]}`))
		},
		"GET file/img2/thumbnail": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) },
		"GET file/img2/data":      func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(testPNG(t)) },
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("enter")
	d.expect("▀")
}

func TestDetailFitsScreen(t *testing.T) {
	long := strings.Repeat("x", 400)
	f := newFakeAPI(t)
	f.extra = map[string]http.HandlerFunc{
		"GET object/o1": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"asset_uuid":"o1","inventory_name":"Laptop Dell","description":"` + long +
				`","picture":[{"uuid":"img1","name":"front.png","type":"image/png"}]}`))
		},
		"GET file/img1/thumbnail": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(testPNG(t)) },
	}
	for _, size := range [][2]int{{140, 34}, {80, 24}} {
		d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
		d.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		d.key("enter")
		screen := d.screen()
		lines := strings.Split(screen, "\n")
		if len(lines) > size[1] {
			t.Errorf("%dx%d: %d lines, taller than the terminal", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%dx%d: line %d is %d cells wide", size[0], size[1], i, w)
			}
		}
		if !strings.Contains(screen, "▀") || !strings.Contains(screen, "p picture") {
			t.Errorf("%dx%d: picture or footer missing:\n%s", size[0], size[1], screen)
		}
	}
}
