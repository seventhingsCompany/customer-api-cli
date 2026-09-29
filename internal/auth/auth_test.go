package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/client"
)

type fakeAPI struct {
	valid     atomic.Value // current valid access token
	refreshes atomic.Int32
	logins    atomic.Int32
	calls     atomic.Int32
}

func newFakeAPI(t *testing.T, valid string) (*fakeAPI, *httptest.Server) {
	f := &fakeAPI{}
	f.valid.Store(valid)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/customer-api/v1/auth_token":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			switch body["grant_type"] {
			case "refresh_token":
				if body["refresh_token"] != "rt-1" || body["client_id"] != "cid" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				f.refreshes.Add(1)
			case "password":
				if body["password"] != "pw" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				f.logins.Add(1)
			}
			f.valid.Store("fresh")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "fresh", "refresh_token": "rt-1", "expires_in": 3600, "user_id": 7,
			})
		default:
			f.calls.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+f.valid.Load().(string) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"uuid": "x", "body": r.Method})
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func sdkClient(srv *httptest.Server, s *Session) *client.Client {
	hc := &http.Client{Transport: &Transport{Base: http.DefaultTransport, Session: s}}
	return client.NewWithToken(srv.URL, Placeholder, client.WithHTTPClient(hc), client.WithClientID("cid"))
}

func TestRefreshOn401AndPersist(t *testing.T) {
	f, srv := newFakeAPI(t, "fresh")
	f.valid.Store("server-rotated") // stored token "stale" is rejected

	store := &FileStore{path: filepath.Join(t.TempDir(), "credentials.json")}
	creds := &Credentials{AccessToken: "stale", RefreshToken: "rt-1", ExpiresAt: time.Now().Add(time.Hour)}
	s := NewSession(SessionConfig{Profile: "p", URL: srv.URL, ClientID: "cid", HTTP: http.DefaultClient, Store: store, Creds: creds})

	// PATCH with a body verifies the body is replayed on retry.
	if err := sdkClient(srv, s).ObjectPatch(context.Background(), "x", map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if f.refreshes.Load() != 1 || f.calls.Load() != 2 {
		t.Fatalf("refreshes=%d calls=%d, want 1 and 2", f.refreshes.Load(), f.calls.Load())
	}
	saved, _ := store.Load("p")
	if saved == nil || saved.AccessToken != "fresh" || saved.UserID != 7 {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestProactiveRefreshBeforeExpiry(t *testing.T) {
	f, srv := newFakeAPI(t, "fresh")
	creds := &Credentials{AccessToken: "old", RefreshToken: "rt-1", ExpiresAt: time.Now().Add(10 * time.Second)}
	s := NewSession(SessionConfig{Profile: "p", URL: srv.URL, ClientID: "cid", HTTP: http.DefaultClient, Creds: creds})

	if _, err := sdkClient(srv, s).ObjectGet(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if f.refreshes.Load() != 1 || f.calls.Load() != 1 {
		t.Fatalf("refreshes=%d calls=%d, want 1 and 1 (no 401 round trip)", f.refreshes.Load(), f.calls.Load())
	}
}

func TestEnvPasswordLoginIsLazy(t *testing.T) {
	f, srv := newFakeAPI(t, "fresh")
	s := NewSession(SessionConfig{Profile: "p", URL: srv.URL, ClientID: "cid", HTTP: http.DefaultClient, Username: "u", Password: "pw"})
	if f.logins.Load() != 0 {
		t.Fatal("login before first request")
	}
	if _, err := sdkClient(srv, s).ObjectGet(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if f.logins.Load() != 1 {
		t.Fatalf("logins = %d", f.logins.Load())
	}
}

func TestNotLoggedIn(t *testing.T) {
	_, srv := newFakeAPI(t, "fresh")
	s := NewSession(SessionConfig{Profile: "p", URL: srv.URL, HTTP: http.DefaultClient})
	_, err := sdkClient(srv, s).ObjectGet(context.Background(), "x")
	if exitcode.For(err) != exitcode.Auth {
		t.Fatalf("err = %v, exit %d", err, exitcode.For(err))
	}
}

func TestStaticTokenRejectedSurfaces401(t *testing.T) {
	f, srv := newFakeAPI(t, "fresh")
	s := NewSession(SessionConfig{Profile: "p", URL: srv.URL, HTTP: http.DefaultClient, Token: "wrong"})
	_, err := sdkClient(srv, s).ObjectGet(context.Background(), "x")
	if exitcode.For(err) != exitcode.Auth || f.refreshes.Load() != 0 {
		t.Fatalf("err = %v refreshes=%d", err, f.refreshes.Load())
	}
}

func TestUnauthenticatedCallsPassThrough(t *testing.T) {
	_, srv := newFakeAPI(t, "fresh")
	s := NewSession(SessionConfig{Profile: "p", URL: srv.URL, HTTP: http.DefaultClient}) // not logged in
	c := sdkClient(srv, s)
	if _, err := c.Login(context.Background(), "u", "pw", "cid"); err != nil {
		t.Fatalf("login through auth transport should not need a session: %v", err)
	}
}
