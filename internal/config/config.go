// Package config reads and writes the non-secret CLI configuration: named
// profiles pointing at seventhings instances. Tokens live in internal/auth.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// DefaultProfile is used when no profile is selected.
const DefaultProfile = "default"

// Profile describes one seventhings instance and login.
type Profile struct {
	URL      string `yaml:"url"`
	ClientID string `yaml:"client_id,omitempty"`
	Username string `yaml:"username,omitempty"`
	// RateLimit in requests per minute; nil means the API default.
	RateLimit *int `yaml:"rate_limit,omitempty"`
}

// Config is the on-disk configuration file.
type Config struct {
	CurrentProfile string              `yaml:"current_profile,omitempty"`
	Profiles       map[string]*Profile `yaml:"profiles,omitempty"`

	path string
}

// Dir returns the configuration directory. SEVENTHINGS_CONFIG_DIR overrides
// the OS default (~/.config/seventhings on Linux).
func Dir(getenv func(string) string) (string, error) {
	if d := getenv("SEVENTHINGS_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "seventhings"), nil
}

// Load reads config.yaml from dir. A missing file yields an empty config.
func Load(dir string) (*Config, error) {
	c := &Config{path: filepath.Join(dir, "config.yaml"), Profiles: map[string]*Profile{}}
	data, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", c.path, err)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]*Profile{}
	}
	return c, nil
}

// Save writes the config atomically.
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Path returns the config file path.
func (c *Config) Path() string { return c.path }

// ActiveName resolves the profile name: explicit flag, then
// SEVENTHINGS_PROFILE, then current_profile, then "default".
func (c *Config) ActiveName(flag string, getenv func(string) string) string {
	switch {
	case flag != "":
		return flag
	case getenv("SEVENTHINGS_PROFILE") != "":
		return getenv("SEVENTHINGS_PROFILE")
	case c.CurrentProfile != "":
		return c.CurrentProfile
	}
	return DefaultProfile
}

// Names returns the profile names, sorted.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
