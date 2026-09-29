// Package tui is the interactive full-screen interface started by running
// `seventhings` without arguments in a terminal.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/charmbracelet/x/ansi"
)

// Deps is what the TUI needs from the CLI: an authenticated client (with
// token refresh and rate limiting), login, and profile information.
type Deps interface {
	Client() (*client.Client, error)
	LoggedIn() bool
	Login(ctx context.Context, url, clientID, username, password string) error
	Profile() (name, url, clientID, username string)
	// Env returns a configuration value (e.g. SEVENTHINGS_IMAGES).
	Env(key string) string
	// Settings, SetRateLimit, SetPageSize and Logout back the settings tab;
	// the setters save to the active profile.
	Settings() Settings
	SetRateLimit(perMinute int) error
	SetPageSize(rows int) error
	Logout(ctx context.Context) error
	Profiles() []string
	SwitchProfile(name string) error
}

type mode int

const (
	modeLogin mode = iota
	modeList
	modeDetail
	modeHistory
	modeForm
	modeConfirm
	modeSettings
)

type formKind int

const (
	formLogin formKind = iota
	formCreate
	formEdit
	formUpload
	formDownload
	formCustom // action form; submit runs m.custom
	formPageSize
	formRateLimit
	formFilter
	formProfile
)

type tabState struct {
	items   []Item
	page    int
	search  string
	loaded  bool
	hasMore bool

	seq    int                // id of the newest load; older answers are dropped
	cancel context.CancelFunc // cancels the newest load

	total    int    // matching records, noTotal if unknown
	countFor string // query the cached total belongs to ("" = none)
	lastPage int    // found by paging past the end; 0 = unknown

	// Filter and sort (key f), as typed and parsed.
	filterText, sortText string
	filters              []models.FilterEntry
	sorts                []sortKey
}

// invalidate forgets what is known about the result set (after a write,
// reload or new search).
func (st *tabState) invalidate() {
	st.countFor, st.lastPage = "", 0
}

// pageCount is the number of pages for total records.
func pageCount(total, perPage int) int {
	return max((total+perPage-1)/max(perPage, 1), 1)
}

// Model is the root Bubble Tea model.
type Model struct {
	ctx  context.Context
	deps Deps
	res  []*resource
	tabs []tabState
	tab  int
	mode mode
	prev mode // mode to return to from confirm/form/history

	width, height int
	table         table.Model
	detail        viewport.Model
	help          viewport.Model
	detailItem    Item
	search        textinput.Model
	searching     bool
	spinner       spinner.Model
	loading       int
	showHelp      bool
	pageSize      int
	settingsRow   settingsRow

	status    string
	statusErr bool

	// reads in flight by fetch id, for esc to cancel.
	reads   map[int]context.CancelFunc
	readSeq int
	// nav changes whenever the user moves between views; detail and history
	// answers for an older nav are dropped. navCancel cancels that fetch.
	nav       int
	navCancel context.CancelFunc
	session   int // old-session results are discarded
	writing   int // mutations in flight
	operation string
	recovery  *formRecovery

	// form state
	formKind      formKind
	form          *huh.Form
	record        *recordForm
	login         *loginForm
	path          *pathForm
	value         *valueForm
	filter        *filterForm
	profileChoice string
	formTarget    Item
	allFields     bool              // form shows every field, not just the common ones
	prefill       map[string]string // initial values for the next create form
	custom        func(rf *recordForm) tea.Cmd

	// choiceCache holds picker options per source; a nil entry means the
	// source was too large and the field falls back to typing an ID.
	choiceCache map[choiceSource][][2]string

	// confirm state
	confirmPrompt string
	confirmCmd    func() tea.Cmd
	keepStatus    bool // next list reload keeps the action message

	defs map[models.AssetTrackingTemplate][]models.FieldDefinition

	// pictures caches previews by file UUID.
	pictures     map[string]*picture
	hidePictures bool
	imageMode    string // SEVENTHINGS_IMAGES: auto, kitty, iterm2, sixel, blocks, off
	gfx          graphics
	kitty        kittyState
	pane         detailPane
	ov           overlayState
	opener       func(path string) error // opens a file in the system viewer
}

// New builds the root model.
func New(ctx context.Context, deps Deps) *Model {
	res := resources()
	m := &Model{
		ctx: ctx, deps: deps, res: res, tabs: make([]tabState, len(res)),
		width: 100, height: 30, mode: modeList,
		defs:        map[models.AssetTrackingTemplate][]models.FieldDefinition{},
		choiceCache: map[choiceSource][][2]string{},
		pictures:    map[string]*picture{},
		reads:       map[int]context.CancelFunc{},
	}
	m.imageMode = strings.ToLower(deps.Env("SEVENTHINGS_IMAGES"))
	switch m.imageMode {
	case imagesKitty:
		m.gfx = gfxKitty
	case imagesITerm2:
		m.gfx = gfxITerm2
	case imagesSixel:
		m.gfx = gfxSixel
	case imagesBlocks, imagesOff:
	default:
		m.imageMode = imagesAuto // start with half blocks, upgrade on detection
	}
	m.kitty = newKittyState(m.gfx == gfxKitty)
	m.hidePictures = m.imageMode == imagesOff
	m.ov.cache = map[string]string{}
	m.opener = openFile
	for i := range m.tabs {
		m.tabs[i].page, m.tabs[i].total = 1, noTotal
	}
	m.pageSize = deps.Settings().PageSize
	m.table = table.New(table.WithFocused(true), table.WithStyles(tableTheme))
	m.detail = viewport.New()
	m.help = viewport.New()
	m.search = textinput.New()
	m.search.Prompt = "/ "
	m.search.Placeholder = "search"
	m.spinner = spinner.New(spinner.WithSpinner(spinner.MiniDot))
	if !deps.LoggedIn() {
		m.openLogin()
	}
	return m
}

// --- messages ---

type itemsMsg struct {
	tab      int
	seq      int
	total    int
	countFor string // set when total came from the count endpoint
	page     int
	perPage  int
	items    []Item
	err      error
}

type detailMsg struct {
	nav  int
	item Item
	err  error
}

type historyMsg struct {
	nav  int
	text string
	err  error
}

// loadedMsg carries the result of a background request (see async).
type loadedMsg struct {
	read     int // fetch id, 0 for writes
	msg      tea.Msg
	scope    operationScope
	recovery *formRecovery
}

type defsMsg struct {
	template models.AssetTrackingTemplate
	defs     []models.FieldDefinition
	err      error
	next     func() tea.Cmd
}

type choicesMsg struct {
	loaded map[choiceSource][][2]string
	next   func() tea.Cmd
	err    error
}

// cmdMsg runs a follow-up command on the UI goroutine (used when a
// background step needs to open a form or confirmation).
type cmdMsg struct{ fn func() tea.Cmd }

type doneMsg struct {
	text   string
	err    error
	reload bool
	back   bool // leave detail view (after delete)
	scope  *operationScope
}

type loginMsg struct{ err error }

type pictureMsg struct {
	key     string
	img     image.Image
	err     error
	session int
}

// StatusMsg shows a transient message (e.g. rate-limit waits) in the footer.
type StatusMsg string

func (m *Model) Init() tea.Cmd {
	// The terminal reports its background (the theme adapts, see theme.go)
	// and, for auto image mode, whether it speaks the kitty graphics protocol.
	cmds := []tea.Cmd{tea.RequestBackgroundColor}
	switch m.imageMode {
	case imagesAuto:
		// Kitty query first: terminals answer in order, so a kitty OK arrives
		// before the device attributes that decide between iTerm2 and Sixel.
		cmds = append(cmds, tea.Raw(kittyQuery()+ansi.RequestPrimaryDeviceAttributes+cellSizeQuery))
	case imagesKitty, imagesITerm2, imagesSixel:
		cmds = append(cmds, tea.Raw(cellSizeQuery))
	}
	if m.mode == modeLogin {
		return tea.Batch(append(cmds, m.form.Init())...)
	}
	return tea.Batch(append(cmds, m.load())...)
}

// --- commands ---

func (m *Model) client() (*client.Client, error) { return m.deps.Client() }

// load fetches the current page of the active tab. A newer load of the
// same tab cancels this one and its answer is dropped.
func (m *Model) load() tea.Cmd {
	tab, perPage := m.tab, m.pageSize
	st := &m.tabs[tab]
	if st.cancel != nil {
		st.cancel()
	}
	st.seq++
	seq, q := st.seq, query{page: st.page, perPage: perPage, search: st.search, searchKey: m.res[tab].searchKey, filters: st.filters, sorts: st.sorts}
	r := m.res[tab]
	// The count endpoint is asked once per search, not on every page.
	countFor, cached := "q:"+q.search+"\x00"+st.filterText, st.total
	recount := r.count != nil && st.countFor != countFor
	cmd, cancel := m.fetch(func(ctx context.Context, cl *client.Client) tea.Msg {
		items, total, err := r.load(ctx, cl, q)
		msg := itemsMsg{tab: tab, seq: seq, page: q.page, perPage: perPage, items: items, total: total, err: err}
		switch {
		case err != nil || r.count == nil:
		case recount:
			cq := q
			cq.page, cq.perPage = 0, 0
			// Without a total the list still works, so a failed count is not an error.
			if n, err := r.count(ctx, cl, cq); err == nil {
				msg.total, msg.countFor = n, countFor
			}
		default:
			msg.total, msg.countFor = cached, countFor
		}
		return msg
	})
	st.cancel = cancel
	return cmd
}

// async runs fn in the background with the spinner showing until its
// result arrives. read is the fetch id (0 for writes).
func (m *Model) async(read int, fn func() tea.Msg) tea.Cmd {
	if read == 0 {
		if m.writing > 0 {
			m.setStatus("An operation is already in progress", true)
			return nil
		}
		m.writing++
		if m.operation == "" {
			m.operation = "Saving…"
		}
	}
	scope := operationScope{tab: m.tab, nav: m.nav, session: m.session}
	recovery := m.recovery
	m.recovery = nil
	m.loading++
	return tea.Batch(m.spinner.Tick, func() tea.Msg { return loadedMsg{read: read, msg: fn(), scope: scope, recovery: recovery} })
}

// withClient adapts fn to async; a missing client is reported as doneMsg.
func (m *Model) withClient(ctx context.Context, fn func(ctx context.Context, cl *client.Client) tea.Msg) func() tea.Msg {
	// Resolve on the UI goroutine so background work cannot lazily mutate App.
	cl, err := m.client()
	return func() tea.Msg {
		if err != nil {
			return doneMsg{err: err}
		}
		return fn(ctx, cl)
	}
}

// run performs a write. Writes are not cancellable: stopping one halfway
// would leave its outcome unknown.
func (m *Model) run(fn func(ctx context.Context, cl *client.Client) tea.Msg) tea.Cmd {
	return m.async(0, m.withClient(m.ctx, fn))
}

// prepare performs navigation-sensitive read-only work before opening a form.
func (m *Model) prepare(fn func(ctx context.Context, cl *client.Client) tea.Msg) tea.Cmd {
	cmd, cancel := m.fetch(fn)
	m.navCancel = cancel
	return cmd
}

// fetch performs a read that esc (cancelReads) can cancel. The returned
// func cancels just this read.
func (m *Model) fetch(fn func(ctx context.Context, cl *client.Client) tea.Msg) (tea.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(m.ctx)
	m.readSeq++
	m.reads[m.readSeq] = cancel
	return m.async(m.readSeq, m.withClient(ctx, fn)), cancel
}

// cancelReads cancels every read in flight.
func (m *Model) cancelReads() {
	for id, cancel := range m.reads {
		cancel()
		delete(m.reads, id)
	}
	m.navigate()
}

// navigate invalidates pending detail and history loads, so a late answer
// cannot open a view the user has already left.
func (m *Model) navigate() {
	m.nav++
	m.recovery = nil
	if m.navCancel != nil {
		m.navCancel()
		m.navCancel = nil
	}
}

func (m *Model) invalidateTab(tab int) {
	st := &m.tabs[tab]
	st.invalidate()
	st.loaded = false
	st.seq++
	if st.cancel != nil {
		st.cancel()
	}
}

// --- helpers ---

func (m *Model) setStatus(s string, isErr bool) {
	m.status, m.statusErr = s, isErr
}

func (m *Model) fail(err error) tea.Cmd {
	if errors.Is(err, context.Canceled) {
		return nil // the user cancelled; the status already says so
	}
	m.setStatus(errText(err), true)
	if isAuthError(err) && m.mode != modeLogin {
		m.openLogin()
		return m.form.Init()
	}
	return nil
}

func isAuthError(err error) bool {
	return exitcode.For(err) == exitcode.Auth
}

func errText(err error) string {
	var apiErr *models.APIError
	if errors.As(err, &apiErr) {
		var body map[string]any
		if json.Unmarshal([]byte(apiErr.Body), &body) == nil {
			for _, k := range []string{"detail", "message", "title"} {
				if s, ok := body[k].(string); ok && s != "" {
					return fmt.Sprintf("HTTP %d: %s", apiErr.StatusCode, s)
				}
			}
		}
		b := strings.Join(strings.Fields(apiErr.Body), " ")
		if len(b) > 200 {
			b = b[:200] + "…"
		}
		if b != "" {
			return fmt.Sprintf("HTTP %d: %s", apiErr.StatusCode, b)
		}
		return fmt.Sprintf("HTTP %d", apiErr.StatusCode)
	}
	return err.Error()
}

func displayName(it Item, fallback string) string {
	for _, k := range []string{"inventory_name", "name", "title", "display_name", "email", "order_number"} {
		if s := str(it[k]); s != "" {
			return s
		}
	}
	return fallback
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(n-1, 0)]) + "…"
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// Run starts the TUI and blocks until the user quits. setNotify, if
// non-nil, receives a function the CLI can call to show status messages
// (e.g. rate-limit waits) while the TUI owns the screen.
func Run(ctx context.Context, deps Deps, out io.Writer, opts []tea.ProgramOption, setNotify func(func(string))) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := New(ctx, deps)
	p := tea.NewProgram(m, append([]tea.ProgramOption{tea.WithContext(ctx), tea.WithOutput(out)}, opts...)...)
	if setNotify != nil {
		setNotify(func(s string) { p.Send(StatusMsg(s)) })
		defer setNotify(nil)
	}
	_, err := p.Run()
	// Free images transmitted with the kitty protocol.
	if seq := m.kitty.cleanup(); seq != "" {
		_, _ = io.WriteString(out, seq)
	}
	if errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
