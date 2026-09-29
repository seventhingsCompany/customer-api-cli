package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // register decoders
	_ "image/jpeg"
	_ "image/png"
	"slices"
	"strings"

	"github.com/SeventhingsCompany/customer-api-go/client"
)

// Pictures are drawn with Unicode half blocks: each cell shows two
// vertically stacked pixels, the upper one as the foreground of "▀" and the
// lower one as its background. That is plain coloured text, so it works in
// any truecolor terminal, over SSH and in tmux, and with Bubble Tea's
// cell-based renderer.

const (
	upperHalf = "▀"
	lowerHalf = "▄"
	// alphaCutoff: pixels more transparent than this show the terminal
	// background.
	alphaCutoff = 0x8000
	// maxImageBytes bounds full-size downloads when no thumbnail exists.
	maxImageBytes = 8 << 20
)

// renderHalfBlocks scales img to fit maxCols × maxRows cells (keeping the
// aspect ratio; one cell holds two square-ish pixels) and renders it.
func renderHalfBlocks(img image.Image, maxCols, maxRows int) string {
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 || maxCols < 1 || maxRows < 1 {
		return ""
	}
	// Target size in pixels: width = cols, height = 2 × rows.
	w, h := b.Dx(), b.Dy()
	scale := min(float64(maxCols)/float64(w), float64(2*maxRows)/float64(h), 1)
	tw, th := max(int(float64(w)*scale), 1), max(int(float64(h)*scale), 1)
	if th%2 == 1 {
		th++ // whole cells
	}

	px := func(x, y int) (color.RGBA64, bool) {
		if y >= th {
			return color.RGBA64{}, false
		}
		c := boxAverage(img, b, x, y, tw, th)
		return c, c.A >= alphaCutoff
	}

	var sb strings.Builder
	for y := 0; y < th; y += 2 {
		for x := range tw {
			top, topOK := px(x, y)
			bot, botOK := px(x, y+1)
			switch {
			case topOK && botOK:
				fmt.Fprintf(&sb, "\x1b[38;2;%sm\x1b[48;2;%sm%s", rgb(top), rgb(bot), upperHalf)
			case topOK:
				fmt.Fprintf(&sb, "\x1b[49m\x1b[38;2;%sm%s", rgb(top), upperHalf)
			case botOK:
				fmt.Fprintf(&sb, "\x1b[49m\x1b[38;2;%sm%s", rgb(bot), lowerHalf)
			default:
				sb.WriteString("\x1b[0m ")
			}
		}
		sb.WriteString("\x1b[0m")
		if y+2 < th {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// boxAverage returns the average colour of the source area that maps to the
// target pixel (x, y) of a tw × th image. Colours are premultiplied, so
// transparent pixels do not darken the edges.
func boxAverage(img image.Image, b image.Rectangle, x, y, tw, th int) color.RGBA64 {
	x0 := b.Min.X + x*b.Dx()/tw
	x1 := max(b.Min.X+(x+1)*b.Dx()/tw, x0+1)
	y0 := b.Min.Y + y*b.Dy()/th
	y1 := max(b.Min.Y+(y+1)*b.Dy()/th, y0+1)
	var r, g, bl, a, n uint64
	for sy := y0; sy < y1; sy++ {
		for sx := x0; sx < x1; sx++ {
			cr, cg, cb, ca := img.At(sx, sy).RGBA()
			r, g, bl, a = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca)
			n++
		}
	}
	if a == 0 {
		return color.RGBA64{}
	}
	// Un-premultiply for display.
	return color.RGBA64{
		R: uint16(r * 0xffff / a), G: uint16(g * 0xffff / a), B: uint16(bl * 0xffff / a),
		A: uint16(a / n),
	}
}

func rgb(c color.RGBA64) string {
	return fmt.Sprintf("%d;%d;%d", c.R>>8, c.G>>8, c.B>>8)
}

// pictureFile picks the image to preview for a record: the file itself on
// the Files tab, otherwise the first image in the "picture" field, then in
// any other attachment field.
func pictureFile(it Item) (uuid, name string) {
	if strings.HasPrefix(str(it["type"]), "image/") && str(it["uuid"]) != "" && it["data_uri"] != nil {
		return str(it["uuid"]), str(it["name"])
	}
	check := func(v any) (string, string) {
		files, _ := v.([]any)
		for _, raw := range files {
			f, _ := raw.(map[string]any)
			if strings.HasPrefix(str(f["type"]), "image/") && str(f["uuid"]) != "" {
				return str(f["uuid"]), str(f["name"])
			}
		}
		return "", ""
	}
	if u, n := check(it["picture"]); u != "" {
		return u, n
	}
	keys := make([]string, 0, len(it))
	for k := range it {
		keys = append(keys, k)
	}
	slices.Sort(keys) // deterministic choice
	for _, k := range keys {
		if u, n := check(it[k]); k != "picture" && u != "" {
			return u, n
		}
	}
	return "", ""
}

// fetchPicture downloads a picture. Half blocks only need the thumbnail;
// kitty (full) prefers the original file, since thumbnails are small (the
// API serves about 150×100 px). Each falls back to the other.
func fetchPicture(ctx context.Context, cl *client.Client, uuid string, full bool) (image.Image, error) {
	first, second := cl.FileGetThumbnail, cl.FileGetData
	if full {
		first, second = second, first
	}
	data, err := first(ctx, uuid)
	if err != nil || len(data) == 0 || len(data) > maxImageBytes {
		if data, err = second(ctx, uuid); err != nil {
			return nil, err
		}
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("image too large to preview (%d MB)", len(data)>>20)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("cannot preview this image format: %w", err)
	}
	return img, nil
}
