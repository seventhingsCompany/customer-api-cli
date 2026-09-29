package auth

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/client"
)

// refreshSkew renews tokens this long before they expire.
const refreshSkew = 60 * time.Second

// Placeholder is set as the SDK client's token so the SDK adds an
// Authorization header to authenticated requests; Transport swaps in the real
// token. Unauthenticated SDK calls (login, ping) carry no header and pass
// through untouched.
const Placeholder = "managed-by-seventhings-cli"

// Session hands out a valid access token, refreshing or re-logging in as
// needed. It is safe for concurrent use.
type Session struct {
	Profile string

	mu        sync.Mutex
	creds     *Credentials
	store     Store          // nil: don't persist (env credentials)
	refresher *client.Client // unauthenticated client used for token calls
	username  string         // set for env-based password login
	password  string
	static    bool // token given directly; cannot be renewed
	now       func() time.Time
}

// SessionConfig configures NewSession.
type SessionConfig struct {
	Profile  string
	URL      string
	ClientID string
	// HTTP is used for token calls; it must not include Transport itself.
	HTTP *http.Client

	// Exactly one source is used, in this order:
	Token    string       // static token (SEVENTHINGS_TOKEN)
	Username string       // password login held in memory (env)
	Password string       //
	Store    Store        // persisted credentials from `auth login`
	Creds    *Credentials // preloaded from Store
}

// NewSession builds a session from cfg.
func NewSession(cfg SessionConfig) *Session {
	s := &Session{
		Profile:   cfg.Profile,
		refresher: client.New(cfg.URL, client.WithHTTPClient(cfg.HTTP), client.WithClientID(cfg.ClientID)),
		now:       time.Now,
	}
	switch {
	case cfg.Token != "":
		s.creds = &Credentials{AccessToken: cfg.Token}
		s.static = true
	case cfg.Username != "" && cfg.Password != "":
		s.username, s.password = cfg.Username, cfg.Password
	default:
		s.store = cfg.Store
		s.creds = cfg.Creds
	}
	return s
}

// Credentials returns a copy of the current credentials, or nil.
func (s *Session) Credentials() *Credentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creds == nil {
		return nil
	}
	c := *s.creds
	return &c
}

// Token returns a usable access token, renewing it if it expires soon.
func (s *Session) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creds == nil {
		if s.password == "" {
			return "", s.notLoggedIn()
		}
		if err := s.renewLocked(ctx); err != nil {
			return "", err
		}
	}
	if !s.static && !s.creds.ExpiresAt.IsZero() && s.now().Add(refreshSkew).After(s.creds.ExpiresAt) {
		if err := s.renewLocked(ctx); err != nil {
			return "", err
		}
	}
	return s.creds.AccessToken, nil
}

// Renew is called after the server rejected failed with 401. If another
// request already renewed the token, the new one is returned without a call.
func (s *Session) Renew(ctx context.Context, failed string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creds != nil && s.creds.AccessToken != failed {
		return s.creds.AccessToken, nil
	}
	if s.static {
		return "", &exitcode.AuthError{Msg: "token from SEVENTHINGS_TOKEN was rejected (401)"}
	}
	if err := s.renewLocked(ctx); err != nil {
		return "", err
	}
	return s.creds.AccessToken, nil
}

// Refresh forces a token renewal (`auth refresh`).
func (s *Session) Refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.static {
		return &exitcode.AuthError{Msg: "cannot refresh a token passed via SEVENTHINGS_TOKEN"}
	}
	if s.creds == nil && s.password == "" {
		return s.notLoggedIn()
	}
	return s.renewLocked(ctx)
}

func (s *Session) renewLocked(ctx context.Context) error {
	var refreshErr error
	if s.creds != nil && s.creds.RefreshToken != "" {
		tok, err := s.refresher.Refresh(ctx, s.creds.RefreshToken)
		if err == nil {
			return s.setLocked(FromToken(tok, s.now()))
		}
		refreshErr = err
	}
	if s.password != "" {
		tok, err := s.refresher.Login(ctx, s.username, s.password, s.refresher.ClientID())
		if err != nil {
			return &exitcode.AuthError{Msg: fmt.Sprintf("login failed: %v", err)}
		}
		return s.setLocked(FromToken(tok, s.now()))
	}
	if refreshErr != nil {
		return &exitcode.AuthError{Msg: fmt.Sprintf("session expired and refresh failed (%v); run `seventhings auth login --profile %s`", refreshErr, s.Profile)}
	}
	return &exitcode.AuthError{Msg: fmt.Sprintf("session expired; run `seventhings auth login --profile %s`", s.Profile)}
}

func (s *Session) setLocked(c *Credentials) error {
	s.creds = c
	if s.store != nil {
		if err := s.store.Save(s.Profile, c); err != nil {
			return fmt.Errorf("save credentials: %w", err)
		}
	}
	return nil
}

func (s *Session) notLoggedIn() error {
	return &exitcode.AuthError{Msg: fmt.Sprintf(
		"not logged in (profile %q); run `seventhings auth login` or set SEVENTHINGS_TOKEN / SEVENTHINGS_USERNAME+SEVENTHINGS_PASSWORD", s.Profile)}
}
