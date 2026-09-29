// Package cmd implements the seventhings command tree.
package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SeventhingsCompany/customer-api-cli/internal/auth"
	"github.com/SeventhingsCompany/customer-api-cli/internal/config"
	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-cli/internal/mode"
	"github.com/SeventhingsCompany/customer-api-cli/internal/output"
	"github.com/SeventhingsCompany/customer-api-cli/internal/ratelimit"
	"github.com/SeventhingsCompany/customer-api-go/client"
)

// IO bundles the process streams and whether they are terminals.
type IO struct {
	In        io.Reader
	Out       io.Writer
	Err       io.Writer
	StdinTTY  bool
	StdoutTTY bool
}

// BuildInfo is injected by main via -ldflags.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// App holds global state shared by all commands.
type App struct {
	tuiMu     sync.Mutex // protects adapter state shared with login/logout workers
	io        IO
	getenv    func(string) string
	transport http.RoundTripper // innermost transport; replaced in tests
	build     BuildInfo

	// global flags
	profileFlag string
	urlFlag     string
	outputFlag  string
	jqFlag      string
	rawFlag     bool
	fieldsFlag  []string
	agentFlag   bool
	yes         bool
	quiet       bool
	debug       bool
	dryRun      bool
	rateLimit   int

	mode     mode.Mode
	modeSet  bool
	printer  *output.Printer
	cfg      *config.Config
	cfgDir   string
	store    auth.Store
	client   *client.Client
	session  *auth.Session
	baseHTTP http.RoundTripper

	// notify, when set, receives rate-limit messages instead of stderr
	// (the TUI owns the screen).
	notify func(string)
}

func newApp(stdio IO, getenv func(string) string, build BuildInfo) *App {
	return &App{io: stdio, getenv: getenv, transport: http.DefaultTransport, build: build, rateLimit: -1}
}

// setup runs before every command, after flags are parsed.
func (a *App) setup() error {
	a.resolveMode()
	format := output.JSON
	if a.mode == mode.Interactive {
		format = output.Table
	}
	if a.outputFlag != "" {
		f, err := output.ParseFormat(a.outputFlag)
		if err != nil {
			return exitcode.Usagef("%v", err)
		}
		format = f
	}
	p, err := output.New(a.io.Out, format, a.fieldsFlag, a.jqFlag)
	if err != nil {
		return exitcode.Usagef("%v", err)
	}
	p.Raw = a.rawFlag
	a.printer = p

	if a.cfgDir, err = config.Dir(a.getenv); err != nil {
		return err
	}
	a.cfg, err = config.Load(a.cfgDir)
	return err
}

func (a *App) resolveMode() {
	if !a.modeSet {
		a.mode = mode.Resolve(a.agentFlag, a.getenv, a.io.StdinTTY, a.io.StdoutTTY)
		a.modeSet = true
	}
}

func (a *App) interactive() bool { return a.mode == mode.Interactive }

// profile returns the active profile name and its settings (never nil).
func (a *App) profile() (string, *config.Profile) {
	name := a.cfg.ActiveName(a.profileFlag, a.getenv)
	if p, ok := a.cfg.Profiles[name]; ok {
		return name, p
	}
	return name, &config.Profile{}
}

func (a *App) baseURL() (string, error) {
	name, p := a.profile()
	switch {
	case a.urlFlag != "":
		return a.urlFlag, nil
	case a.getenv("SEVENTHINGS_BASE_URL") != "":
		return a.getenv("SEVENTHINGS_BASE_URL"), nil
	case p.URL != "":
		return p.URL, nil
	}
	return "", &exitcode.AuthError{Msg: fmt.Sprintf(
		"no instance URL for profile %q; run `seventhings auth login --url https://<tenant>.seventhings.com` or set SEVENTHINGS_BASE_URL", name)}
}

func (a *App) clientID() string {
	if id := a.getenv("SEVENTHINGS_CLIENT_ID"); id != "" {
		return id
	}
	_, p := a.profile()
	return p.ClientID
}

// rateLimitPerMinute: --rate-limit > SEVENTHINGS_RATE_LIMIT > profile > default.
func (a *App) rateLimitPerMinute() (int, error) {
	n, _, err := a.rateLimitSetting()
	return n, err
}

// rateLimitSetting also returns where the limit comes from.
func (a *App) rateLimitSetting() (n int, source string, err error) {
	if a.rateLimit >= 0 {
		return a.rateLimit, "--rate-limit", nil
	}
	if s := a.getenv("SEVENTHINGS_RATE_LIMIT"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return 0, "", exitcode.Usagef("SEVENTHINGS_RATE_LIMIT must be a non-negative integer, got %q", s)
		}
		return n, "SEVENTHINGS_RATE_LIMIT", nil
	}
	if _, p := a.profile(); p.RateLimit != nil {
		return *p.RateLimit, "profile", nil
	}
	return ratelimit.DefaultPerMinute, "default", nil
}

// httpBase is the shared transport stack below auth: debug logging and rate
// limiting. One instance per process so every request shares the budget.
func (a *App) httpBase() (http.RoundTripper, error) {
	if a.baseHTTP != nil {
		return a.baseHTTP, nil
	}
	rpm, err := a.rateLimitPerMinute()
	if err != nil {
		return nil, err
	}
	base := a.transport
	if a.debug {
		base = &debugTransport{Base: base, Err: a.io.Err}
	}
	a.baseHTTP = ratelimit.New(base, rpm, ratelimit.WithNotify(func(wait time.Duration, attempt int) {
		msg := fmt.Sprintf("rate limited (429), retrying in %s (attempt %d/%d)",
			wait.Round(100*time.Millisecond), attempt, ratelimit.DefaultMaxRetries)
		a.tuiMu.Lock()
		notify := a.notify
		a.tuiMu.Unlock()
		switch {
		case notify != nil:
			notify(msg)
		case a.debug || a.interactive():
			_, _ = fmt.Fprintln(a.io.Err, msg)
		}
	}))
	return a.baseHTTP, nil
}

// credentialStore opens the token store lazily (probing the keyring is slow).
func (a *App) credentialStore() auth.Store {
	if a.store == nil {
		a.store = auth.NewStore(a.cfgDir, a.getenv)
	}
	return a.store
}

// credentialSource describes where credentials come from, for `auth status`.
func (a *App) credentialSource() string {
	switch {
	case a.getenv("SEVENTHINGS_TOKEN") != "":
		return "env:SEVENTHINGS_TOKEN"
	case a.getenv("SEVENTHINGS_USERNAME") != "" && a.getenv("SEVENTHINGS_PASSWORD") != "":
		return "env:SEVENTHINGS_USERNAME/PASSWORD"
	}
	return a.credentialStore().Name()
}

// Client returns the authenticated SDK client for the active profile.
func (a *App) Client() (*client.Client, error) {
	if a.client != nil {
		return a.client, nil
	}
	url, err := a.baseURL()
	if err != nil {
		return nil, err
	}
	base, err := a.httpBase()
	if err != nil {
		return nil, err
	}
	name, _ := a.profile()
	cfg := auth.SessionConfig{Profile: name, URL: url, ClientID: a.clientID(), HTTP: &http.Client{Transport: base}}
	switch {
	case a.getenv("SEVENTHINGS_TOKEN") != "":
		cfg.Token = a.getenv("SEVENTHINGS_TOKEN")
	case a.getenv("SEVENTHINGS_USERNAME") != "" && a.getenv("SEVENTHINGS_PASSWORD") != "":
		cfg.Username, cfg.Password = a.getenv("SEVENTHINGS_USERNAME"), a.getenv("SEVENTHINGS_PASSWORD")
	default:
		cfg.Store = a.credentialStore()
		if cfg.Creds, err = cfg.Store.Load(name); err != nil {
			return nil, fmt.Errorf("load credentials: %w", err)
		}
	}
	a.session = auth.NewSession(cfg)

	var rt http.RoundTripper = &auth.Transport{Base: base, Session: a.session}
	if a.dryRun {
		rt = &dryRunTransport{Base: rt, Out: a.io.Out}
	}
	a.client = client.NewWithToken(url, auth.Placeholder,
		client.WithHTTPClient(&http.Client{Transport: rt}), client.WithClientID(a.clientID()))
	return a.client, nil
}

// print renders a result unless this is a dry run (the request was printed).
func (a *App) print(v any) error {
	if a.dryRun {
		return nil
	}
	return a.printer.Print(v)
}

// done reports a write that has no response body. Interactive table output
// gets a short message, everything else the JSON object.
func (a *App) done(msg string, v map[string]any) error {
	if a.dryRun {
		return nil
	}
	if a.printer.Format == output.Table && a.jqFlag == "" {
		if !a.quiet {
			_, _ = fmt.Fprintln(a.io.Out, "✓ "+msg)
		}
		return nil
	}
	return a.printer.Print(v)
}

// confirm guards destructive actions. Agents must pass --yes.
func (a *App) confirm(action string) error {
	if a.yes || a.dryRun {
		return nil
	}
	if !a.interactive() {
		return exitcode.Usagef("%s is destructive; pass --yes to confirm", action)
	}
	_, _ = fmt.Fprintf(a.io.Err, "%s? [y/N] ", action)
	line, _ := bufio.NewReader(a.io.In).ReadString('\n')
	if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
		return exitcode.Usagef("aborted")
	}
	return nil
}

// ttyFile returns stdin as *os.File when it is a terminal (for password input).
func (a *App) ttyFile() (*os.File, bool) {
	f, ok := a.io.In.(*os.File)
	return f, ok && a.io.StdinTTY
}
