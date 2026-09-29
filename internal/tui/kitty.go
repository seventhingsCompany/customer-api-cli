package tui

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// Full-resolution pictures use the kitty graphics protocol (kitty, Ghostty,
// WezTerm, …) with Unicode placeholders: the image is transmitted once as a
// "virtual placement", and the view contains ordinary text cells
// (U+10EEEE plus row/column diacritics, coloured with the image ID) that the
// terminal replaces with the image. Being plain text, the placeholders work
// with Bubble Tea's cell renderer, scroll and clip like any other content.
//
// Support is detected at startup: the TUI sends a 1×1 query and switches from
// half blocks to kitty only when the terminal answers OK. Terminals without
// the protocol (and tmux, which does not pass it through by default) never
// answer and keep half blocks.

// Image modes, set with SEVENTHINGS_IMAGES (see also overlay.go).
const (
	imagesAuto   = "auto"   // detect the best protocol (default)
	imagesKitty  = "kitty"  // assume kitty support
	imagesBlocks = "blocks" // always half blocks
	imagesOff    = "off"    // no pictures
)

// kittyQueryID identifies our support query's answer.
const kittyQueryID = 7_654_321

// Default cell size in pixels until the terminal reports its own.
const defaultCellW, defaultCellH = 10, 20

// kittyQuery asks whether the terminal speaks the graphics protocol (a 1×1
// RGB image that is checked but not stored).
func kittyQuery() string {
	return ansi.KittyGraphics([]byte("AAAA"), "i="+strconv.Itoa(kittyQueryID), "s=1", "v=1", "a=q", "t=d", "f=24")
}

// cellSizeQuery asks for the cell size in pixels (XTWINOPS 16), used to fit
// pictures to their aspect ratio.
var cellSizeQuery = ansi.WindowOp(16)

// kittyFit returns the cell grid for an image of w×h pixels that fits in
// maxCols×maxRows cells of cellW×cellH pixels, keeping the aspect ratio and
// never upscaling.
func kittyFit(w, h, maxCols, maxRows, cellW, cellH int) (cols, rows int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	scale := math.Min(float64(maxCols*cellW)/float64(w), float64(maxRows*cellH)/float64(h))
	scale = math.Min(scale, 1)
	cols = max(int(math.Round(float64(w)*scale/float64(cellW))), 1)
	rows = max(int(math.Round(float64(h)*scale/float64(cellH))), 1)
	return min(cols, maxCols), min(rows, maxRows)
}

// kittyTransmit encodes img as a PNG virtual placement of cols×rows cells.
// The image is first scaled to the pixel size it will occupy, which keeps the
// escape sequence small.
func kittyTransmit(img image.Image, id, cols, rows, cellW, cellH int) (string, error) {
	scaled := scaleDown(img, cols*cellW, rows*cellH)
	var buf bytes.Buffer
	err := kitty.EncodeGraphics(&buf, scaled, &kitty.Options{
		Action:           kitty.TransmitAndPut,
		Transmission:     kitty.Direct,
		Format:           kitty.PNG,
		ID:               id,
		VirtualPlacement: true,
		Columns:          cols,
		Rows:             rows,
		Quiet:            2, // no responses
		Chunk:            true,
	})
	return buf.String(), err
}

// kittyDelete frees an image in the terminal.
func kittyDelete(id int) string {
	return ansi.KittyGraphics(nil, "a=d", "d=I", "i="+strconv.Itoa(id), "q=2")
}

// kittyPlaceholders renders the cells that show image id. The foreground
// colour carries the ID (24 bits); diacritics carry row and column.
func kittyPlaceholders(id, cols, rows int) string {
	fg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", (id>>16)&0xff, (id>>8)&0xff, id&0xff)
	var sb strings.Builder
	for r := range rows {
		sb.WriteString(fg)
		for c := range cols {
			sb.WriteRune(kitty.Placeholder)
			sb.WriteRune(kitty.Diacritic(r))
			sb.WriteRune(kitty.Diacritic(c))
		}
		sb.WriteString("\x1b[39m")
		if r < rows-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// scaleDown shrinks img to fit maxW×maxH pixels with a box filter.
func scaleDown(img image.Image, maxW, maxH int) image.Image {
	b := img.Bounds()
	scale := math.Min(math.Min(float64(maxW)/float64(b.Dx()), float64(maxH)/float64(b.Dy())), 1)
	if scale >= 1 {
		return img
	}
	tw, th := max(int(float64(b.Dx())*scale), 1), max(int(float64(b.Dy())*scale), 1)
	out := image.NewNRGBA64(image.Rect(0, 0, tw, th))
	for y := range th {
		for x := range tw {
			out.SetNRGBA64(x, y, boxAverage(img, b, x, y, tw, th))
		}
	}
	return out
}

// kittyKey identifies one transmitted placement.
type kittyKey struct {
	uuid       string
	cols, rows int
}

// kittyState tracks protocol support and transmitted images.
type kittyState struct {
	enabled      bool
	cellW, cellH int
	nextID       int
	ids          map[kittyKey]int
}

func newKittyState(enabled bool) kittyState {
	return kittyState{enabled: enabled, cellW: defaultCellW, cellH: defaultCellH, nextID: 1, ids: map[kittyKey]int{}}
}

// place returns the image ID for uuid at cols×rows, and the escape sequence
// to transmit it if it is new. Earlier sizes of the same file are deleted,
// so resizing does not pile up images in the terminal.
func (k *kittyState) place(uuid string, img image.Image, cols, rows int) (id int, transmit tea.Cmd) {
	key := kittyKey{uuid, cols, rows}
	if id, ok := k.ids[key]; ok {
		return id, nil
	}
	seq, err := kittyTransmit(img, k.nextID, cols, rows, k.cellW, k.cellH)
	if err != nil {
		return 0, nil
	}
	var stale strings.Builder
	for old, oldID := range k.ids {
		if old.uuid == uuid {
			stale.WriteString(kittyDelete(oldID))
			delete(k.ids, old)
		}
	}
	id = k.nextID
	k.nextID++
	k.ids[key] = id
	return id, tea.Raw(stale.String() + seq)
}

// cleanup deletes every transmitted image.
func (k *kittyState) cleanup() string {
	var sb strings.Builder
	for _, id := range k.ids {
		sb.WriteString(kittyDelete(id))
	}
	return sb.String()
}
