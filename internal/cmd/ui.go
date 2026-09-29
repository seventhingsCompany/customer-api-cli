package cmd

import (
	"context"
	"net/http"

	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-cli/internal/tui"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/spf13/cobra"
)

// tuiDeps adapts App to tui.Deps so the TUI shares auth, token refresh and
// the rate limiter with the rest of the CLI.
type tuiDeps struct{ a *App }

func (d tuiDeps) Client() (*client.Client, error) { return d.a.Client() }

func (d tuiDeps) LoggedIn() bool {
	a := d.a
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
	base, err := a.httpBase()
	if err != nil {
		return err
	}
	sdk := client.New(url, client.WithHTTPClient(&http.Client{Transport: base}))
	tok, err := sdk.Login(ctx, username, password, clientID)
	if err != nil {
		return err
	}
	name, _ := a.profile()
	if _, err := a.saveLogin(name, url, clientID, username, tok); err != nil {
		return err
	}
	a.urlFlag = "" // the saved profile now holds the URL
	return nil
}

func (d tuiDeps) Env(key string) string { return d.a.getenv(key) }

func (d tuiDeps) Profile() (name, url, clientID, username string) {
	a := d.a
	name, p := a.profile()
	url, _ = a.baseURL()
	return name, url, a.clientID(), firstNonEmpty(a.getenv("SEVENTHINGS_USERNAME"), p.Username)
}

func (a *App) runTUI(ctx context.Context) error {
	if !a.interactive() {
		return exitcode.Usagef("the interactive UI needs a terminal; use subcommands in agent mode (see `seventhings describe`)")
	}
	opts := []tea.ProgramOption{tea.WithInput(a.io.In)}
	return tui.Run(ctx, tuiDeps{a}, a.io.Out, opts, func(fn func(string)) { a.notify = fn })
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
