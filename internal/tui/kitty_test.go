package tui

import (
	"image"
	"net/http"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

func TestKittyPlaceholders(t *testing.T) {
	out := kittyPlaceholders(0x010203, 4, 3)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("rows = %d", len(lines))
	}
	for r, l := range lines {
		if !strings.HasPrefix(l, "\x1b[38;2;1;2;3m") {
			t.Errorf("row %d: image ID not encoded in the foreground colour: %q", r, l)
		}
		if w := ansi.StringWidth(l); w != 4 {
			t.Errorf("row %d: %d cells wide, want 4", r, w)
		}
		// Second cell of the row: placeholder, row diacritic, column diacritic.
		cell := string([]rune{kitty.Placeholder, kitty.Diacritic(r), kitty.Diacritic(1)})
		if !strings.Contains(l, cell) {
			t.Errorf("row %d: missing placeholder for column 1", r)
		}
	}
}

func TestKittyFit(t *testing.T) {
	cases := []struct {
		w, h, maxC, maxR, cw, ch int
		cols, rows               int
	}{
		{800, 600, 40, 20, 10, 20, 40, 15}, // landscape, width-bound
		{600, 900, 40, 20, 10, 20, 27, 20}, // portrait, height-bound
		{100, 100, 40, 20, 10, 20, 10, 5},  // small: never upscaled
		{800, 600, 40, 20, 8, 16, 40, 15},  // other cell size
	}
	for _, c := range cases {
		cols, rows := kittyFit(c.w, c.h, c.maxC, c.maxR, c.cw, c.ch)
		if cols != c.cols || rows != c.rows {
			t.Errorf("%dx%d in %dx%d cells of %dx%d px: got %dx%d, want %dx%d",
				c.w, c.h, c.maxC, c.maxR, c.cw, c.ch, cols, rows, c.cols, c.rows)
		}
	}
}

func pictureAPI(t *testing.T) *fakeAPI {
	f := newFakeAPI(t)
	f.extra = map[string]http.HandlerFunc{
		"GET object/o1": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"asset_uuid":"o1","inventory_name":"Laptop Dell","picture":[{"uuid":"img1","name":"front.png","type":"image/png"}]}`))
		},
		"GET file/img1/thumbnail": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(testPNG(t)) },
		"GET file/img1/data":      func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(testPNG(t)) },
	}
	return f
}

func rawContaining(raws []string, parts ...string) int {
	n := 0
	for _, r := range raws {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(r, p)
		}
		if ok {
			n++
		}
	}
	return n
}

func TestKittyDetectedAtRuntime(t *testing.T) {
	f := pictureAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	if rawContaining(d.raw, "\x1b_G", "a=q") != 1 {
		t.Fatalf("no kitty support query sent: %q", d.raw)
	}

	d.key("enter")
	d.expect("▀") // half blocks until the terminal answers
	if strings.ContainsRune(d.screen(), kitty.Placeholder) {
		t.Fatal("placeholders before kitty support was confirmed")
	}

	d.send(uv.KittyGraphicsEvent{Options: kitty.Options{ID: kittyQueryID}, Payload: []byte("OK")})
	if !strings.ContainsRune(d.screen(), kitty.Placeholder) || strings.Contains(d.screen(), "▀") {
		t.Fatalf("kitty placeholders not shown after OK:\n%s", d.screen())
	}
	if rawContaining(d.raw, "a=T", "U=1", "f=100") != 1 {
		t.Fatalf("image not transmitted as a virtual placement: %d raws", len(d.raw))
	}
	if !f.called("GET file/img1/data") {
		t.Error("kitty mode should load the full-size image, not only the thumbnail")
	}

	// Leaving and reopening reuses the transmitted image.
	d.key("esc", "enter")
	if n := rawContaining(d.raw, "a=T"); n != 1 {
		t.Errorf("image transmitted %d times, want 1", n)
	}
	if !strings.Contains(d.m.kitty.cleanup(), "a=d") {
		t.Error("cleanup does not delete images")
	}
}

func TestKittyIgnoresOtherReplies(t *testing.T) {
	f := pictureAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.send(uv.KittyGraphicsEvent{Options: kitty.Options{ID: kittyQueryID}, Payload: []byte("ENOTSUPPORTED")})
	d.send(uv.KittyGraphicsEvent{Options: kitty.Options{ID: 42}, Payload: []byte("OK")})
	d.key("enter")
	d.expect("▀")
}

func TestKittyLayoutFits(t *testing.T) {
	f := pictureAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, env: map[string]string{"SEVENTHINGS_IMAGES": "kitty"}})
	d.send(uv.CellSizeEvent{Width: 9, Height: 18})
	for _, size := range [][2]int{{140, 34}, {80, 24}} {
		d.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		d.key("enter")
		for i, l := range strings.Split(d.screen(), "\n") {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%dx%d: line %d is %d cells wide", size[0], size[1], i, w)
			}
		}
		if !strings.ContainsRune(d.screen(), kitty.Placeholder) {
			t.Errorf("%dx%d: no placeholders", size[0], size[1])
		}
		d.key("esc")
	}
}

func TestImageModes(t *testing.T) {
	f := pictureAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, env: map[string]string{"SEVENTHINGS_IMAGES": "blocks"}})
	if rawContaining(d.raw, "a=q") != 0 {
		t.Error("blocks mode must not query kitty support")
	}
	d.send(uv.KittyGraphicsEvent{Options: kitty.Options{ID: kittyQueryID}, Payload: []byte("OK")})
	d.key("enter")
	d.expect("▀")

	f2 := pictureAPI(t)
	d = newDriver(t, &fakeDeps{url: f2.srv.URL, loggedIn: true, env: map[string]string{"SEVENTHINGS_IMAGES": "off"}})
	d.key("enter")
	d.expect("inventory_name")
	if strings.Contains(d.screen(), "p hides") || strings.Contains(d.screen(), "picture hidden") || f2.called("GET file/") {
		t.Errorf("off mode fetched or mentioned pictures:\n%s", d.screen())
	}
}

func TestScaleDown(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1000, 500))
	if b := scaleDown(img, 400, 400).Bounds(); b.Dx() != 400 || b.Dy() != 200 {
		t.Errorf("scaled to %v", b)
	}
	if scaleDown(img, 2000, 2000) != image.Image(img) {
		t.Error("upscaled")
	}
}
