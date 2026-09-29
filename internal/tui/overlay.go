package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/iterm2"
	"github.com/charmbracelet/x/ansi/sixel"
)

// Graphics protocols, best first. Kitty images are part of the text (Unicode
// placeholders, see kitty.go). iTerm2 inline images and Sixel are drawn
// "over" the screen: the view leaves a blank pane and the image is written
// into it after the frame is painted (see Model.syncOverlay).
type graphics int

const (
	gfxBlocks graphics = iota // half blocks (any truecolor terminal)
	gfxKitty                  // kitty graphics protocol
	gfxITerm2                 // iTerm2 inline images (iTerm2, VS Code)
	gfxSixel                  // Sixel (Windows Terminal, foot, Konsole, xterm, …)
)

func (g graphics) overlay() bool { return g == gfxITerm2 || g == gfxSixel }

func (g graphics) String() string {
	return [...]string{"blocks", "kitty", "iterm2", "sixel"}[g]
}

// Additional SEVENTHINGS_IMAGES values (see kitty.go for the others).
const (
	imagesITerm2 = "iterm2"
	imagesSixel  = "sixel"
)

// chooseOverlay picks a protocol from the primary device attributes (DA1)
// reply, sent by every terminal. Attribute 4 announces Sixel. VS Code's
// terminal (xterm.js) reports it only when terminal.integrated.enableImages
// is on, and then also understands iTerm2 images, which keep full colour.
func chooseOverlay(da1 []int, env func(string) string) graphics {
	termProgram := env("TERM_PROGRAM")
	if termProgram == "iTerm.app" || env("LC_TERMINAL") == "iTerm2" {
		return gfxITerm2
	}
	if !slices.Contains(da1, 4) {
		return gfxBlocks
	}
	if termProgram == "vscode" {
		return gfxITerm2
	}
	return gfxSixel
}

// overlayImage encodes img for an overlay protocol, sized to cols×rows
// cells of cellW×cellH pixels.
func overlayImage(g graphics, img image.Image, cols, rows, cellW, cellH int) (string, error) {
	scaled := scaleDown(img, cols*cellW, rows*cellH)
	switch g {
	case gfxITerm2:
		var buf bytes.Buffer
		if err := png.Encode(&buf, scaled); err != nil {
			return "", err
		}
		return ansi.ITerm2(iterm2.File{
			Name:            base64.StdEncoding.EncodeToString([]byte("picture.png")),
			Size:            int64(buf.Len()),
			Width:           iterm2.Cells(cols),
			Height:          iterm2.Cells(rows),
			Inline:          true,
			DoNotMoveCursor: true,
			Content:         []byte(base64.StdEncoding.EncodeToString(buf.Bytes())),
		}), nil
	case gfxSixel:
		var buf bytes.Buffer
		if err := new(sixel.Encoder).Encode(&buf, scaled); err != nil {
			return "", err
		}
		// P2=1: pixels without colour keep the background.
		return ansi.SixelGraphics(0, 1, 0, buf.Bytes()), nil
	}
	return "", fmt.Errorf("%s is not an overlay protocol", g)
}

// placeAt wraps an image sequence so it is drawn at the 0-based screen cell
// (row, col) without disturbing the cursor.
func placeAt(row, col int, seq string) string {
	return "\x1b7" + fmt.Sprintf("\x1b[%d;%dH", row+1, col+1) + seq + "\x1b8"
}

// blankPane returns rows lines of cols spaces: the area an overlay image
// is drawn into.
func blankPane(cols, rows int) string {
	line := strings.Repeat(" ", cols)
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}
