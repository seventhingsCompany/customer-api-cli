package tui

import (
	"context"
	"fmt"
	"image"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/charmbracelet/x/ansi"
)

func (m *Model) openDetail(it Item) tea.Cmd {
	r := m.res[m.tab]
	id := r.id(it)
	m.navigate()
	if r.get == nil || id == "" {
		m.detailItem = it
		m.mode = modeDetail
		return tea.Batch(m.loadPicture(), m.refreshDetail())
	}
	nav := m.nav
	cmd, cancel := m.fetch(func(ctx context.Context, cl *client.Client) tea.Msg {
		full, err := r.get(ctx, cl, id)
		return detailMsg{nav: nav, item: full, err: err}
	})
	m.navCancel = cancel
	return cmd
}

// refreshDetail re-renders the detail view. It returns a command when a
// picture has to be transmitted to the terminal first (kitty protocol).
func (m *Model) refreshDetail() tea.Cmd {
	content, cmd := m.renderDetail()
	m.detail.SetContent(content)
	return cmd
}

// renderDetail lays out the detail view: the fields go into the scrolling
// viewport; the picture, if any, into a fixed pane next to the fields on
// wide terminals and above them on narrow ones. Returns the viewport content.
func (m *Model) renderDetail() (string, tea.Cmd) {
	it := m.detailItem
	uuid, name := pictureFile(it)
	bodyH := max(m.height-m.chrome()-1, 3) // minus the title line
	m.pane = detailPane{}
	if uuid == "" || m.hidePictures {
		m.detail.SetWidth(m.width)
		m.detail.SetHeight(bodyH)
		out := renderItem(it, m.width-4)
		if uuid != "" && m.imageMode != imagesOff {
			out += "\n" + dimStyle.Render("picture hidden (p to show)")
		}
		return out, nil
	}

	side := m.width >= 100
	cols := min(40, max(m.width/3, 16))
	rows := min(20, max(bodyH-2, 4))
	if !side {
		cols = min(m.width-4, 40)
		rows = min(rows, max(bodyH/2-2, 3))
	}

	var pic string
	var cmd tea.Cmd
	full := m.gfx != gfxBlocks
	p := m.pictures[pictureKey(uuid, full)]
	if full && (p == nil || p.loading) {
		// Full size still loading: show the thumbnail meanwhile, if any.
		if thumb := m.pictures[pictureKey(uuid, false)]; thumb != nil && thumb.img != nil {
			p = &picture{img: thumb.img, thumbOnly: true}
		}
	}
	switch {
	case p == nil || p.loading:
		pic = dimStyle.Render("loading picture…")
	case p.err != nil:
		pic = dimStyle.Render(ansi.Truncate("no preview: "+errText(p.err), cols, "…"))
	case p.thumbOnly || m.gfx == gfxBlocks:
		pic = renderHalfBlocks(p.img, cols, rows)
	case m.gfx == gfxKitty:
		b := p.img.Bounds()
		c, r := kittyFit(b.Dx(), b.Dy(), cols, rows, m.kitty.cellW, m.kitty.cellH)
		var id int
		if id, cmd = m.kitty.place(uuid, p.img, c, r); id == 0 {
			pic = renderHalfBlocks(p.img, cols, rows) // encoding failed
		} else {
			pic = kittyPlaceholders(id, c, r)
		}
	default: // iTerm2 / Sixel: reserve blank cells, the image is drawn over them
		b := p.img.Bounds()
		c, r := kittyFit(b.Dx(), b.Dy(), cols, rows, m.kitty.cellW, m.kitty.cellH)
		// One spare row: if the terminal never reported its cell size, the
		// image may come out slightly taller than computed.
		pic = blankPane(c, r+1)
		m.pane.img, m.pane.uuid, m.pane.cols, m.pane.rows = p.img, uuid, c, r
	}
	// The caption sits above the picture, so an image never covers it.
	const hints = " · p hides · O opens"
	caption := dimStyle.Render(truncate(truncate(name, max(cols-lipgloss.Width(hints), 1))+hints, cols))
	m.pane.content = lipgloss.JoinVertical(lipgloss.Left, caption, pic)
	m.pane.side = side

	const top = 5 // header, tabs, search line, title, caption
	if side {
		vpW := m.width - cols - 3
		m.detail.SetWidth(vpW)
		m.detail.SetHeight(bodyH)
		m.pane.row, m.pane.col = top, vpW+3
		return renderItem(it, vpW-2), cmd
	}
	paneH := lipgloss.Height(m.pane.content) + 1
	m.detail.SetWidth(m.width)
	m.detail.SetHeight(max(bodyH-paneH, 2))
	m.pane.row, m.pane.col = top, 0
	return renderItem(it, m.width-4), cmd
}

// detailBody composes the fields viewport and the picture pane.
func (m *Model) detailBody() string {
	if m.pane.content == "" {
		return m.detail.View()
	}
	if m.pane.side {
		return lipgloss.JoinHorizontal(lipgloss.Top, m.detail.View(), "   ", m.pane.content)
	}
	return m.pane.content + "\n\n" + m.detail.View()
}

// renderItem formats a record as aligned key/value lines, well-known keys
// first, nested fields (attachments, references, …) expanded below them.
// Empty values are left out.
func renderItem(it Item, width int) string {
	var keys []string
	for k, v := range it {
		if !isEmpty(v) {
			keys = append(keys, k)
		}
	}
	rank := func(k string) int {
		for i, p := range []string{"inventory_name", "name", "title", "display_name", "barcode", "email", "status", "asset_uuid", "uuid", "person_uuid", "id"} {
			if k == p {
				return i
			}
		}
		if !inline(decode(it[k])) {
			return 200
		}
		return 100
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := rank(keys[i]), rank(keys[j])
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	pad := 0
	for _, k := range keys {
		pad = max(pad, len(k))
	}
	pad = min(pad, 32)
	var b strings.Builder
	valWidth := max(width-pad-2, 10)
	for _, k := range keys {
		// One line per field, cut to the available width: the viewport does
		// not wrap, and anything wider pushes side content off screen.
		v := decode(it[k])
		if !inline(v) {
			b.WriteString(keyStyle.Render(truncate(k, max(width, 10))) + "\n")
			for _, line := range expand(v) {
				b.WriteString(ansi.Truncate("  "+line, max(width, 10), "…") + "\n")
			}
			continue
		}
		b.WriteString(keyStyle.Render(fmt.Sprintf("%-*s", pad, truncate(k, pad))) + "  " + ansi.Truncate(summary(v), valWidth, "…") + "\n")
	}
	return b.String()
}

// renderHistory formats a history response (newest first).
func renderHistory(h any, width int) string {
	it, err := toItem(h)
	if err != nil {
		return err.Error()
	}
	items, _ := it["items"].([]any)
	if len(items) == 0 {
		return dimStyle.Render("No history entries.")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", dimStyle.Render(fmt.Sprintf("%v entries (newest first)", it["total"])))
	for _, raw := range items {
		e, _ := raw.(map[string]any)
		when := firstNonEmpty(str(e["occurred_at"]), str(e["created_at"]), str(e["date"]))
		what := firstNonEmpty(str(e["description"]), str(e["event_name"]), str(e["type"]))
		b.WriteString(keyStyle.Render(when) + "  " + what + "\n")
		detail := firstNonEmpty(summary(e["details"]), summary(e["properties"]))
		if detail != "" {
			if len(detail) > width*2 {
				detail = detail[:width*2] + "…"
			}
			b.WriteString(dimStyle.Render("    "+detail) + "\n")
		}
	}
	return b.String()
}

// detailPane is the picture pane of the detail view, laid out by
// renderDetail. It sits next to (or above) the fields and does not scroll.
type detailPane struct {
	content  string // rendered pane (picture + caption); "" without a picture
	side     bool   // next to the fields (true) or above them
	row, col int    // screen position of the picture's top-left cell
	// Overlay protocols draw img into the blank cols×rows cells at row/col.
	img        image.Image
	uuid       string
	cols, rows int
}
