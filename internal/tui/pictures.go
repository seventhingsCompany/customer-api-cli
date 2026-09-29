package tui

import (
	"context"
	"fmt"
	"image"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-cli/internal/filesave"
	"github.com/SeventhingsCompany/customer-api-go/client"
)

// overlayState tracks the image drawn over the screen (iTerm2/Sixel).
type overlayState struct {
	key   string // what is (to be) drawn and where; "" for nothing
	shown bool   // an image is on screen and must be cleared before changes
	gen   int    // invalidates pending draws
	cache map[string]string
}

type overlayMsg struct{ gen int }

type picture struct {
	img       image.Image
	err       error
	loading   bool
	thumbOnly bool // placeholder while the full-size image loads
}

// pictureKey caches thumbnails and full-size images separately.
func pictureKey(uuid string, full bool) string {
	if full {
		return uuid + "#full"
	}
	return uuid
}

// loadPicture fetches the preview of the record in the detail view, once
// per file and size. It does not count as loading, so the spinner stays
// quiet.
func (m *Model) loadPicture() tea.Cmd {
	uuid, _ := pictureFile(m.detailItem)
	full := m.gfx != gfxBlocks
	key := pictureKey(uuid, full)
	// A failed fetch is retried the next time the picture is needed.
	if p := m.pictures[key]; m.hidePictures || uuid == "" || p != nil && p.err == nil {
		return nil
	}
	m.pictures[key] = &picture{loading: true}
	cl, clientErr := m.client()
	session := m.session
	return func() tea.Msg {
		if clientErr != nil {
			return pictureMsg{key: key, err: clientErr, session: session}
		}
		img, err := fetchPicture(m.ctx, cl, uuid, full)
		return pictureMsg{key: key, img: img, err: err, session: session}
	}
}

// syncOverlay keeps an iTerm2/Sixel image in step with the view. These
// images live outside the cell renderer, so whenever the picture, its place
// or anything that could move or cover it changes, the screen is cleared
// (removing the old image) and the image is drawn again once the new frame
// is on screen.
func (m *Model) syncOverlay() tea.Cmd {
	key := ""
	if m.gfx.overlay() && m.mode == modeDetail && m.pane.img != nil {
		key = fmt.Sprintf("%s|%s|%dx%d@%d,%d|%dx%d|y%d", m.gfx, m.pane.uuid, m.pane.cols, m.pane.rows,
			m.pane.row, m.pane.col, m.width, m.height, m.detail.YOffset())
	}
	if key == m.ov.key {
		return nil
	}
	var cmds []tea.Cmd
	if m.ov.shown {
		cmds = append(cmds, tea.ClearScreen)
		m.ov.shown = false
	}
	m.ov.key = key
	m.ov.gen++
	if key != "" {
		gen := m.ov.gen
		// Draw after the renderer has painted the frame with the blank pane.
		cmds = append(cmds, tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return overlayMsg{gen: gen} }))
	}
	return tea.Batch(cmds...)
}

// overlaySeq returns the encoded image for the current pane (cached).
func (m *Model) overlaySeq() string {
	key := fmt.Sprintf("%s|%s|%dx%d", m.gfx, m.pane.uuid, m.pane.cols, m.pane.rows)
	if seq, ok := m.ov.cache[key]; ok {
		return seq
	}
	seq, err := overlayImage(m.gfx, m.pane.img, m.pane.cols, m.pane.rows, m.kitty.cellW, m.kitty.cellH)
	if err != nil {
		return ""
	}
	m.ov.cache[key] = seq
	return seq
}

// setGraphics switches the picture protocol after detection.
func (m *Model) setGraphics(g graphics) tea.Cmd {
	if g == m.gfx {
		return nil
	}
	m.gfx = g
	m.kitty.enabled = g == gfxKitty
	if m.mode == modeDetail && m.detailItem != nil {
		return tea.Batch(m.loadPicture(), m.refreshDetail())
	}
	return nil
}

// openInViewer downloads the record's file (the file itself on the Files
// tab, else its picture or first attachment) and opens it in the system's
// default application.
func (m *Model) openInViewer(r *resource, it Item) tea.Cmd {
	uuid, name := "", ""
	if r.download != nil {
		uuid, name = r.id(it), str(it["name"])
	} else if uuid, name = pictureFile(it); uuid == "" {
		uuid, name = firstAttachment(it)
	}
	if uuid == "" {
		m.setStatus("No file to open", true)
		return nil
	}
	if isRemote(m.deps.Env) {
		m.setStatus(errRemote.Error(), true)
		return nil
	}
	open := m.opener
	cmd, _ := m.fetch(func(ctx context.Context, cl *client.Client) tea.Msg {
		data, err := cl.FileGetData(ctx, uuid)
		if err != nil {
			return doneMsg{err: err}
		}
		path, err := viewerPath(uuid, name)
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = filesave.Write(path, data, true)
		}
		if err == nil {
			err = open(path)
		}
		return doneMsg{text: fmt.Sprintf("Opened %s in the default viewer", firstNonEmpty(name, uuid)), err: err}
	})
	return cmd
}

// firstAttachment returns the first attached file of any type. Files are
// told apart from other object lists (e.g. task references) by their size.
func firstAttachment(it Item) (uuid, name string) {
	keys := make([]string, 0, len(it))
	for k := range it {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		files, _ := it[k].([]any)
		for _, raw := range files {
			f, _ := raw.(map[string]any)
			if u := str(f["uuid"]); u != "" && f["name"] != nil && f["size"] != nil {
				return u, str(f["name"])
			}
		}
	}
	return "", ""
}
