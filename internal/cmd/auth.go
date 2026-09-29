package cmd

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/SeventhingsCompany/customer-api-cli/internal/auth"
	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (a *App) authCmd() *cobra.Command {
	c := &cobra.Command{Use: "auth", Short: "Log in, log out and inspect credentials"}
	c.AddCommand(a.authLoginCmd(), a.authLogoutCmd(), a.authStatusCmd(), a.authTokenCmd(), a.authRefreshCmd())
	return c
}

func (a *App) authLoginCmd() *cobra.Command {
	var (
		clientID, username string
		passwordStdin      bool
		ssoProvider, code  string
		appTarget          string
	)
	c := &cobra.Command{
		Use:   "login",
		Short: "Log in and store tokens for the profile",
		Long: `Log in with username/password (or an SSO authorization code) and store the
tokens in the OS keyring (or a 0600 file where no keyring is available).
URL, client ID and username are saved to the profile for next time.

Password sources, in order: --password-stdin, SEVENTHINGS_PASSWORD, prompt
(interactive mode only).`,
		Example: `  seventhings auth login --url https://acme.seventhings.com --client-id cli --username me@acme.com
  echo "$PW" | seventhings auth login --profile ci --password-stdin
  seventhings auth login --sso azure --code "$AUTH_CODE"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, prof := a.profile()
			url, err := a.baseURL()
			if err != nil {
				return err
			}
			if clientID == "" {
				clientID = a.clientID()
			}
			if clientID == "" {
				return exitcode.Usagef("--client-id is required on first login")
			}
			base, err := a.httpBase()
			if err != nil {
				return err
			}
			sdk := client.New(url, client.WithHTTPClient(&http.Client{Transport: base}))

			var tok *models.TokenResponse
			if ssoProvider != "" {
				provider, err := parseSSOProvider(ssoProvider)
				if err != nil {
					return err
				}
				if code == "" {
					return exitcode.Usagef("--code is required with --sso")
				}
				var target *models.SSOAppTarget
				if appTarget != "" {
					t := models.SSOAppTarget(appTarget)
					target = &t
				}
				tok, err = sdk.LoginSSO(cmd.Context(), provider, code, clientID, target)
				if err != nil {
					return err
				}
			} else {
				if username == "" {
					username = firstNonEmpty(a.getenv("SEVENTHINGS_USERNAME"), prof.Username)
				}
				if username == "" {
					return exitcode.Usagef("--username is required on first login")
				}
				password, err := a.readPassword(passwordStdin)
				if err != nil {
					return err
				}
				tok, err = sdk.Login(cmd.Context(), username, password, clientID)
				if err != nil {
					return err
				}
			}

			creds, err := a.saveLogin(name, url, clientID, username, tok)
			if err != nil {
				return err
			}
			store := a.credentialStore()
			return a.done(fmt.Sprintf("Logged in to %s as user %d (profile %q)", url, creds.UserID, name), map[string]any{
				"profile": name, "url": url, "user_id": creds.UserID, "expires_at": creds.ExpiresAt, "store": store.Name(),
			})
		},
	}
	f := c.Flags()
	f.StringVar(&clientID, "client-id", "", "OAuth client ID (env SEVENTHINGS_CLIENT_ID)")
	f.StringVar(&username, "username", "", "username / e-mail (env SEVENTHINGS_USERNAME)")
	f.BoolVar(&passwordStdin, "password-stdin", false, "read the password from stdin")
	f.StringVar(&ssoProvider, "sso", "", "SSO provider: azure, google, onelogin")
	f.StringVar(&code, "code", "", "SSO authorization code (with --sso)")
	f.StringVar(&appTarget, "app-target", "", "SSO app target: web or mobile")
	return annotate(c, annAuth, "none")
}

// saveLogin persists tokens and the profile after a successful login.
func (a *App) saveLogin(name, url, clientID, username string, tok *models.TokenResponse) (*auth.Credentials, error) {
	_, prof := a.profile()
	creds := auth.FromToken(tok, time.Now())
	if err := a.credentialStore().Save(name, creds); err != nil {
		return nil, fmt.Errorf("save credentials: %w", err)
	}
	updated := *prof
	updated.URL, updated.ClientID, updated.Username = url, clientID, username
	a.cfg.Profiles[name] = &updated
	if a.cfg.CurrentProfile == "" {
		a.cfg.CurrentProfile = name
	}
	if err := a.cfg.Save(); err != nil {
		return nil, err
	}
	// Drop the cached client so the next call picks up the new session.
	a.client, a.session = nil, nil
	return creds, nil
}

func (a *App) readPassword(fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(a.io.In).ReadString('\n')
		if err != nil && line == "" {
			return "", exitcode.Usagef("--password-stdin: no input")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	if pw := a.getenv("SEVENTHINGS_PASSWORD"); pw != "" {
		return pw, nil
	}
	f, ok := a.ttyFile()
	if !ok || !a.interactive() {
		return "", exitcode.Usagef("no password: use --password-stdin or SEVENTHINGS_PASSWORD in agent mode")
	}
	_, _ = fmt.Fprint(a.io.Err, "Password: ")
	b, err := term.ReadPassword(int(f.Fd()))
	_, _ = fmt.Fprintln(a.io.Err)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func parseSSOProvider(s string) (models.SSOProviderName, error) {
	switch strings.ToLower(s) {
	case "azure", string(models.SSOProviderAzure):
		return models.SSOProviderAzure, nil
	case "google", string(models.SSOProviderGoogle):
		return models.SSOProviderGoogle, nil
	case "onelogin", "one-login", string(models.SSOProviderOneLogin):
		return models.SSOProviderOneLogin, nil
	}
	return "", exitcode.Usagef("unknown SSO provider %q (want azure, google or onelogin)", s)
}

func (a *App) authLogoutCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "logout",
		Short: "Revoke tokens on the server and delete them locally",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := a.profile()
			revoked, err := a.logout(cmd.Context())
			if err != nil {
				return err
			}
			return a.done(fmt.Sprintf("Logged out (profile %q)", name), map[string]any{"profile": name, "revoked": revoked})
		},
	}
	return write(annotate(c, annAuth, "none"))
}

// logout revokes the active profile's tokens on the server (best effort) and
// deletes them locally.
func (a *App) logout(ctx context.Context) (revoked bool, err error) {
	name, _ := a.profile()
	if src := a.credentialSource(); strings.HasPrefix(src, "env:") {
		return false, exitcode.Usagef("credentials come from the environment (%s); unset them instead of logging out", strings.TrimPrefix(src, "env:"))
	}
	if cl, err := a.Client(); err == nil {
		revoked = cl.RevokeTokens(ctx) == nil
	}
	if err := a.credentialStore().Delete(name); err != nil {
		return revoked, err
	}
	a.client, a.session = nil, nil
	return revoked, nil
}

func (a *App) authStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "status",
		Short: "Show the active profile and token state (no API call)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := a.profile()
			url, _ := a.baseURL()
			rpm, err := a.rateLimitPerMinute()
			if err != nil {
				return err
			}
			st := map[string]any{
				"profile": name, "url": url, "mode": a.mode.String(),
				"credential_source": a.credentialSource(), "rate_limit_per_minute": rpm, "logged_in": false,
			}
			switch {
			case a.getenv("SEVENTHINGS_TOKEN") != "", a.getenv("SEVENTHINGS_USERNAME") != "" && a.getenv("SEVENTHINGS_PASSWORD") != "":
				st["logged_in"] = true
			default:
				creds, err := a.credentialStore().Load(name)
				if err != nil {
					return err
				}
				if creds != nil {
					st["logged_in"] = true
					st["user_id"] = creds.UserID
					st["expires_at"] = creds.ExpiresAt
					st["expired"] = time.Now().After(creds.ExpiresAt)
					st["refreshable"] = creds.RefreshToken != ""
				}
			}
			if err := a.print(st); err != nil {
				return err
			}
			if st["logged_in"] == false {
				return &exitcode.AuthError{Msg: fmt.Sprintf("not logged in (profile %q)", name)}
			}
			return nil
		},
	}
	return annotate(c, annAuth, "none")
}

func (a *App) authTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Print a valid access token (refreshed if needed)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := a.Client(); err != nil {
				return err
			}
			tok, err := a.session.Token(cmd.Context())
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(a.io.Out, tok)
			return err
		},
	}
}

func (a *App) authRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Force a token refresh",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := a.Client(); err != nil {
				return err
			}
			if err := a.session.Refresh(cmd.Context()); err != nil {
				return err
			}
			c := a.session.Credentials()
			return a.done("Token refreshed", map[string]any{"user_id": c.UserID, "expires_at": c.ExpiresAt})
		},
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
