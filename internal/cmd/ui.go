package cmd

import (
	"context"
	"fmt"
	"net/http"

	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-cli/internal/config"
	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-cli/internal/tui"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/spf13/cobra"
)

// tuiDeps adapts App to tui.Deps so the TUI shares auth, token refresh and
// the rate limiter with the rest of the CLI.
type tuiDeps struct{ a *App }

func (d tuiDeps) Client() (*client.Client, error) {
	d.a.tuiMu.Lock()
	defer d.a.tuiMu.Unlock()
	return d.a.Client()
}

func (d tuiDeps) LoggedIn() bool {
	a := d.a
	a.tuiMu.Lock()
	defer a.tuiMu.Unlock()
	if _, err := a.baseURL(); err != nil {
		return false
	}
	if a.getenv("SEVENTHINGS_TOKEN") != "" || (a.getenv("SEVENTHINGS_USERNAME") != "" && a.getenv("SEVENTHINGS_PASSWORD") != "") {
		return true
	}
	name, _ := a.profile()
	creds, err := a.credentialStore().Load(name)
	return err == nil && creds != nil
}

func (d tuiDeps) Login(ctx context.Context, url, clientID, username, password string) error {
	a := d.a
	a.tuiMu.Lock()
	base, err := a.httpBase()
	name, _ := a.profile()
	a.tuiMu.Unlock()
	if err != nil {
		return err
	}
	sdk := client.New(url, client.WithHTTPClient(&http.Client{Transport: base}))
	tok, err := sdk.Login(ctx, username, password, clientID)
	if err != nil {
		return err
	}
	a.tuiMu.Lock()
	defer a.tuiMu.Unlock()
	if _, err := a.saveLogin(name, url, clientID, username, tok); err != nil {
		return err
	}
	a.urlFlag = "" // the saved profile now holds the URL
	return nil
}

func (d tuiDeps) Env(key string) string { return d.a.getenv(key) }

func (d tuiDeps) Settings() tui.Settings {
	a := d.a
	a.tuiMu.Lock()
	defer a.tuiMu.Unlock()
	_, p := a.profile()
	s := tui.Settings{Credentials: a.credentialSource(), PageSize: tui.DefaultPageSize}
	s.ProfileOverride = d.profileOverride()
	s.RateLimit, s.RateLimitSource, _ = a.rateLimitSetting()
	if p.PageSize != nil {
		s.PageSize = *p.PageSize
	}
	return s
}

// SetRateLimit saves the limit to the profile and rebuilds the client, so
// it applies to the next request.
func (d tuiDeps) SetRateLimit(n int) error {
	a := d.a
	a.tuiMu.Lock()
	defer a.tuiMu.Unlock()
	if err := a.updateProfile(func(p *config.Profile) { p.RateLimit = &n }); err != nil {
		return err
	}
	a.baseHTTP, a.client, a.session = nil, nil, nil
	return nil
}

func (d tuiDeps) SetPageSize(n int) error {
	d.a.tuiMu.Lock()
	defer d.a.tuiMu.Unlock()
	return d.a.updateProfile(func(p *config.Profile) { p.PageSize = &n })
}

func (d tuiDeps) Logout(ctx context.Context) error {
	a := d.a
	a.tuiMu.Lock()
	if src := a.credentialSource(); len(src) >= 4 && src[:4] == "env:" {
		a.tuiMu.Unlock()
		return exitcode.Usagef("credentials come from the environment; unset them instead of logging out")
	}
	name, _ := a.profile()
	store := a.credentialStore()
	cl, clientErr := a.Client()
	a.tuiMu.Unlock()
	// Do not hold the state lock during network I/O or rate-limit callbacks.
	if clientErr == nil {
		_ = cl.RevokeTokens(ctx)
	}
	a.tuiMu.Lock()
	defer a.tuiMu.Unlock()
	if err := store.Delete(name); err != nil {
		return err
	}
	a.client, a.session = nil, nil
	return nil
}

// profileOverride is called with tuiMu held. URL/credential overrides are also
// blocked: otherwise a new profile could still use the previous tenant's token.
func (d tuiDeps) profileOverride() string {
	a := d.a
	if a.profileFlag != "" {
		return "Profile switching is disabled by --profile; restart without it"
	}
	if a.urlFlag != "" {
		return "Profile switching is disabled by --url; restart without it"
	}
	for _, key := range []string{"SEVENTHINGS_PROFILE", "SEVENTHINGS_BASE_URL", "SEVENTHINGS_TOKEN", "SEVENTHINGS_USERNAME", "SEVENTHINGS_PASSWORD"} {
		if a.getenv(key) != "" {
			return fmt.Sprintf("Profile switching is disabled by %s; unset it and restart", key)
		}
	}
	return ""
}

func (d tuiDeps) Profiles() []string {
	d.a.tuiMu.Lock()
	defer d.a.tuiMu.Unlock()
	return d.a.cfg.Names()
}

func (d tuiDeps) SwitchProfile(name string) error {
	a := d.a
	a.tuiMu.Lock()
	defer a.tuiMu.Unlock()
	if reason := d.profileOverride(); reason != "" {
		return exitcode.Usagef("%s", reason)
	}
	if _, ok := a.cfg.Profiles[name]; !ok {
		return exitcode.Usagef("unknown profile %q", name)
	}
	old := a.cfg.CurrentProfile
	a.cfg.CurrentProfile = name
	if err := a.cfg.Save(); err != nil {
		a.cfg.CurrentProfile = old
		return err
	}
	a.client, a.session = nil, nil
	// Apply the new profile's rate-limit setting.
	a.baseHTTP = nil
	return nil
}

// updateProfile changes the active profile and saves the config.
func (a *App) updateProfile(change func(p *config.Profile)) error {
	name, p := a.profile()
	if _, ok := a.cfg.Profiles[name]; !ok {
		a.cfg.Profiles[name] = p
	}
	change(p)
	return a.cfg.Save()
}

func (d tuiDeps) Profile() (name, url, clientID, username string) {
	a := d.a
	a.tuiMu.Lock()
	defer a.tuiMu.Unlock()
	name, p := a.profile()
	url, _ = a.baseURL()
	return name, url, a.clientID(), firstNonEmpty(a.getenv("SEVENTHINGS_USERNAME"), p.Username)
}

func (a *App) runTUI(ctx context.Context) error {
	if !a.interactive() {
		return exitcode.Usagef("the interactive UI needs a terminal; use subcommands in agent mode (see `seventhings describe`)")
	}
	opts := []tea.ProgramOption{tea.WithInput(a.io.In)}
	return tui.Run(ctx, tuiDeps{a}, a.io.Out, opts, func(fn func(string)) {
		a.tuiMu.Lock()
		a.notify = fn
		a.tuiMu.Unlock()
	})
}

func (a *App) uiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Open the interactive full-screen UI (also: run seventhings without arguments)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runTUI(cmd.Context())
		},
	}
}
