package config

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Profiles == nil || len(c.Profiles) != 0 || c.CurrentProfile != "" {
		t.Errorf("want an empty config, got %+v", c)
	}
}

func TestLoadParseError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("profiles: [oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "parse ") {
		t.Errorf("err = %v, want a parse error", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "seventhings") // created by Save
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	rate, rows := 120, 25
	c.CurrentProfile = "acme"
	c.Profiles["acme"] = &Profile{URL: "https://acme.example", ClientID: "cid", Username: "me", RateLimit: &rate, PageSize: &rows}
	c.Profiles["beta"] = &Profile{URL: "https://beta.example"}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil { // overwriting works too
		t.Fatal(err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := got.Profiles["acme"]
	if got.CurrentProfile != "acme" || p == nil || p.URL != "https://acme.example" || p.ClientID != "cid" ||
		p.Username != "me" || p.RateLimit == nil || *p.RateLimit != 120 || p.PageSize == nil || *p.PageSize != 25 {
		t.Errorf("round trip lost data: %+v %+v", got, p)
	}
	if b := got.Profiles["beta"]; b == nil || b.RateLimit != nil || b.PageSize != nil {
		t.Errorf("beta: %+v", b)
	}
	if got.Path() != filepath.Join(dir, "config.yaml") {
		t.Errorf("path %q", got.Path())
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if fi, _ := os.Stat(got.Path()); fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, want 0600", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v, want 0700", fi.Mode().Perm())
	}
}

func TestActiveName(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	c := &Config{}
	if got := c.ActiveName("", getenv); got != DefaultProfile {
		t.Errorf("empty: %q", got)
	}
	c.CurrentProfile = "current"
	if got := c.ActiveName("", getenv); got != "current" {
		t.Errorf("current_profile: %q", got)
	}
	env["SEVENTHINGS_PROFILE"] = "env"
	if got := c.ActiveName("", getenv); got != "env" {
		t.Errorf("env: %q", got)
	}
	if got := c.ActiveName("flag", getenv); got != "flag" {
		t.Errorf("flag: %q", got)
	}
}

func TestNamesSorted(t *testing.T) {
	c := &Config{Profiles: map[string]*Profile{"b": {}, "a": {}, "c": {}}}
	if got := c.Names(); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("Names() = %v", got)
	}
}

func TestDir(t *testing.T) {
	if d, _ := Dir(func(string) string { return "/x/y" }); d != "/x/y" {
		t.Errorf("SEVENTHINGS_CONFIG_DIR ignored: %q", d)
	}
	if d, err := Dir(func(string) string { return "" }); err == nil && filepath.Base(d) != "seventhings" {
		t.Errorf("default dir %q", d)
	}
}
