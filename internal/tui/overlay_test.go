package tui

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestChooseOverlay(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		name string
		da1  []int
		env  func(string) string
		want graphics
	}{
		{"VS Code with images enabled", []int{62, 4, 22}, env("TERM_PROGRAM", "vscode"), gfxITerm2},
		{"VS Code with images disabled", []int{62, 22}, env("TERM_PROGRAM", "vscode"), gfxBlocks},
		{"iTerm2", []int{62, 22}, env("TERM_PROGRAM", "iTerm.app"), gfxITerm2},
		{"iTerm2 over SSH", []int{62}, env("LC_TERMINAL", "iTerm2"), gfxITerm2},
		{"Sixel terminal (Windows Terminal, foot, …)", []int{61, 4, 6}, env(), gfxSixel},
		{"no graphics", []int{62, 22}, env(), gfxBlocks},
	}
	for _, c := range cases {
		if got := chooseOverlay(c.da1, c.env); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func overlayDriver(t *testing.T, env map[string]string) (*driver, *fakeAPI) {
	f := pictureAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, env: env})
	return d, f
}

func TestITerm2OverlayInVSCode(t *testing.T) {
	d, f := overlayDriver(t, map[string]string{"TERM_PROGRAM": "vscode"})
	if rawContaining(d.raw, "\x1b[c") != 1 {
		t.Fatal("device attributes not requested")
	}
	d.send(uv.PrimaryDeviceAttributesEvent{62, 4, 22})
	if d.m.gfx != gfxITerm2 {
		t.Fatalf("gfx = %s", d.m.gfx)
	}

	d.key("enter")
	if !f.called("GET file/img1/data") {
		t.Error("full-size image not loaded")
	}
	if strings.Contains(d.screen(), "▀") {
		t.Error("half blocks drawn although the terminal can show images")
	}
	draws := rawContaining(d.raw, "\x1b]1337;File=", "inline=1", "width=", "\x1b7", "\x1b8")
	if draws != 1 || !d.m.ov.shown {
		t.Fatalf("image drawn %d times (shown=%v)", draws, d.m.ov.shown)
	}
	// Drawn at the pane's position (1-based CUP).
	if rawContaining(d.raw, "\x1b[6;") != 1 {
		t.Errorf("image not positioned at the pane: %q", d.raw[len(d.raw)-1][:40])
	}

	// Leaving the detail view clears the image.
	d.key("esc")
	if d.m.ov.shown || d.m.ov.key != "" {
		t.Error("overlay still marked as shown after leaving the detail view")
	}
	// Reopening draws it again, from the cache.
	d.key("enter")
	if n := rawContaining(d.raw, "\x1b]1337;File="); n != 2 {
		t.Errorf("image drawn %d times, want 2", n)
	}
}

func TestSixelOverlay(t *testing.T) {
	d, _ := overlayDriver(t, nil)
	d.send(uv.PrimaryDeviceAttributesEvent{61, 4})
	d.key("enter")
	if rawContaining(d.raw, "\x1bP0;1q") != 1 {
		t.Fatalf("no sixel image drawn")
	}
}

func TestKittyWinsOverDA1(t *testing.T) {
	d, _ := overlayDriver(t, map[string]string{"TERM_PROGRAM": "vscode"})
	d.send(uv.KittyGraphicsEvent{Options: kittyOK().Options, Payload: []byte("OK")})
	d.send(uv.PrimaryDeviceAttributesEvent{62, 4})
	if d.m.gfx != gfxKitty {
		t.Errorf("gfx = %s, want kitty", d.m.gfx)
	}
}

func kittyOK() uv.KittyGraphicsEvent {
	var e uv.KittyGraphicsEvent
	e.Options.ID = kittyQueryID
	return e
}

func TestForcedModesSkipDetection(t *testing.T) {
	d, _ := overlayDriver(t, map[string]string{"SEVENTHINGS_IMAGES": "sixel"})
	if rawContaining(d.raw, "\x1b[c") != 0 || d.m.gfx != gfxSixel {
		t.Errorf("forced sixel: gfx=%s raws=%q", d.m.gfx, d.raw)
	}
}

func TestOpenInViewer(t *testing.T) {
	f := pictureAPI(t)
	f.extra["GET file/img1/data"] = func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("PNGDATA")) }
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	var opened string
	d.m.opener = func(p string) error { opened = p; return nil }

	d.key("enter", "O")
	d.expect("Opened front.png in the default viewer")
	if data, _ := os.ReadFile(opened); string(data) != "PNGDATA" || !strings.HasSuffix(opened, "img1-front.png") {
		t.Errorf("opened %q with %q", opened, data)
	}
	_ = os.Remove(opened)

	// Over SSH the viewer would open on the remote machine.
	d = newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, env: map[string]string{"SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"}})
	d.m.opener = func(string) error { t.Fatal("opened a viewer over SSH"); return nil }
	d.key("enter", "O")
	d.expect("runs over SSH")
}

func TestViewerPathSanitizes(t *testing.T) {
	p, err := viewerPath("u1", `../..\evil:name?.pdf`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, "u1-.._.._evil_name_.pdf") || strings.Contains(p[len(os.TempDir()):], "..\\") {
		t.Errorf("path %q", p)
	}
	p, err = viewerPath("../../x", "a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != filepath.Join(os.TempDir(), "seventhings-files") || filepath.Base(p) != "x-a.pdf" {
		t.Errorf("uuid escaped the directory: %q", p)
	}
}

func TestFirstAttachmentSkipsReferences(t *testing.T) {
	task := Item{"references": []any{map[string]any{"type": "asset", "uuid": "obj-1", "name": "Laptop", "id": 3}}}
	if u, _ := firstAttachment(task); u != "" {
		t.Errorf("reference taken as attachment: %q", u)
	}
	task["documents"] = []any{map[string]any{"uuid": "f1", "name": "a.pdf", "type": "application/pdf", "size": 10}}
	if u, _ := firstAttachment(task); u != "f1" {
		t.Errorf("got %q, want f1", u)
	}
}
