// Package auth persists tokens and keeps them fresh. The SDK stores only the
// access token and never refreshes, so the CLI owns the refresh lifecycle.
package auth

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/zalando/go-keyring"
)

const keyringService = "seventhings-cli"

// Credentials are the secrets stored per profile.
type Credentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	UserID       int       `json:"user_id,omitempty"`
}

// FromToken converts an SDK token response.
func FromToken(tok *models.TokenResponse, now time.Time) *Credentials {
	return &Credentials{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    now.Add(time.Duration(tok.ExpiresIn) * time.Second),
		UserID:       tok.UserID,
	}
}

// Store persists Credentials per profile.
type Store interface {
	// Load returns nil, nil when the profile has no stored credentials.
	Load(profile string) (*Credentials, error)
	Save(profile string, c *Credentials) error
	Delete(profile string) error
	Name() string
}

// NewStore picks a credential store. SEVENTHINGS_CREDENTIAL_STORE=keyring|file
// forces one; otherwise the OS keyring is used when reachable, with a 0600
// file in dir as fallback (headless servers, containers, CI).
func NewStore(dir string, getenv func(string) string) Store {
	file := &FileStore{path: filepath.Join(dir, "credentials.json")}
	switch getenv("SEVENTHINGS_CREDENTIAL_STORE") {
	case "file":
		return file
	case "keyring":
		return KeyringStore{}
	}
	if _, err := keyring.Get(keyringService, "__probe__"); err == nil || errors.Is(err, keyring.ErrNotFound) {
		return KeyringStore{}
	}
	return file
}

// KeyringStore uses the OS keychain / Secret Service / Credential Manager.
type KeyringStore struct{}

func (KeyringStore) Name() string { return "keyring" }

func (KeyringStore) Load(profile string) (*Credentials, error) {
	s, err := keyring.Get(keyringService, profile)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (KeyringStore) Save(profile string, c *Credentials) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return keyring.Set(keyringService, profile, string(b))
}

func (KeyringStore) Delete(profile string) error {
	err := keyring.Delete(keyringService, profile)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// FileStore keeps all profiles in one 0600 JSON file.
type FileStore struct {
	path string
	mu   sync.Mutex
}

func (f *FileStore) Name() string { return "file:" + f.path }

func (f *FileStore) read() (map[string]*Credentials, error) {
	m := map[string]*Credentials{}
	data, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (f *FileStore) write(m map[string]*Credentials) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

func (f *FileStore) Load(profile string) (*Credentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.read()
	if err != nil {
		return nil, err
	}
	return m[profile], nil
}

func (f *FileStore) Save(profile string, c *Credentials) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.read()
	if err != nil {
		return err
	}
	m[profile] = c
	return f.write(m)
}

func (f *FileStore) Delete(profile string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.read()
	if err != nil {
		return err
	}
	delete(m, profile)
	return f.write(m)
}
