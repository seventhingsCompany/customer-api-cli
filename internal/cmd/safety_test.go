package cmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SeventhingsCompany/customer-api-cli/internal/config"
	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

func TestRawDeleteConfirmation(t *testing.T) {
	f := newFakeAPI(t, 1)
	env := agentEnv(t, f)
	r := run(t, env, "", "api", "DELETE", "object/u1")
	if r.code != exitcode.Usage || len(f.wrote()) != 0 {
		t.Fatalf("unconfirmed delete: %+v, writes %v", r, f.wrote())
	}
	r = run(t, env, "", "api", "DELETE", "object/u1", "--dry-run")
	if r.code != 0 || len(f.wrote()) != 0 {
		t.Fatalf("dry run: %+v", r)
	}
	var out, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"api", "DELETE", "object/u1"},
		IO{In: strings.NewReader("n\n"), Out: &out, Err: &stderr, StdinTTY: true, StdoutTTY: true},
		func(k string) string { return env[k] }, BuildInfo{})
	if code != exitcode.Usage || len(f.wrote()) != 0 || !strings.Contains(stderr.String(), "[y/N]") {
		t.Fatalf("interactive cancellation: exit %d: %s", code, stderr.String())
	}
	r = run(t, env, "", "api", "DELETE", "object/u1", "--yes")
	if r.code != 0 || len(f.wrote()) != 1 {
		t.Fatalf("confirmed delete: %+v", r)
	}
}

func TestRawPartialWithInclude(t *testing.T) {
	api := newRouteAPI(t)
	api.json("POST partial", 207, []map[string]int{{"status": 200}, {"status": 404}})
	for _, format := range []string{"json", "ndjson", "yaml", "table"} {
		for _, include := range []bool{false, true} {
			args := []string{"api", "POST", "partial", "--output", format}
			if include {
				args = append(args, "--include")
			}
			r := run(t, api.env(t), "", args...)
			if r.code != exitcode.Partial || r.out == "" {
				t.Fatalf("%v: %+v", args, r)
			}
		}
	}
}

func TestDownloadOverwriteFlag(t *testing.T) {
	api := newRouteAPI(t)
	api.routes["GET file/f1/data"] = func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte("new")) }
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := run(t, api.env(t), "", "files", "download", "f1", "--out", path)
	if b, err := os.ReadFile(path); r.code != exitcode.Usage || err != nil || string(b) != "old" {
		t.Fatalf("unconfirmed overwrite: %+v, %q, %v", r, b, err)
	}
	r = run(t, api.env(t), "", "files", "download", "f1", "--out", path, "--overwrite")
	if b, err := os.ReadFile(path); r.code != 0 || err != nil || string(b) != "new" {
		t.Fatalf("overwrite: %+v, %q, %v", r, b, err)
	}
}

func TestLoginKeepsProfilePreferences(t *testing.T) {
	env := map[string]string{"SEVENTHINGS_CONFIG_DIR": t.TempDir(), "SEVENTHINGS_CREDENTIAL_STORE": "file"}
	a := newApp(IO{Out: io.Discard, Err: io.Discard}, func(k string) string { return env[k] }, BuildInfo{})
	if err := a.setup(); err != nil {
		t.Fatal(err)
	}
	pageSize, rateLimit := 20, 30
	a.cfg.Profiles["default"] = &config.Profile{URL: "https://old.test", PageSize: &pageSize, RateLimit: &rateLimit}
	if _, err := a.saveLogin("default", "https://new.test", "cid", "user", &models.TokenResponse{}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(env["SEVENTHINGS_CONFIG_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Profiles["default"]
	if p.PageSize == nil || *p.PageSize != 20 || p.RateLimit == nil || *p.RateLimit != 30 || p.URL != "https://new.test" {
		t.Fatalf("preferences lost: %+v", p)
	}
}

func TestAPIHelpQueryExamples(t *testing.T) {
	r := run(t, nil, "", "api", "--help")
	if r.code != 0 || strings.Contains(r.out, " -q ") || !strings.Contains(r.out, "--query per_page=5") {
		t.Fatalf("incorrect examples: %+v", r)
	}
}

func TestProfileSwitchAdapter(t *testing.T) {
	env := map[string]string{"SEVENTHINGS_CONFIG_DIR": t.TempDir(), "SEVENTHINGS_CREDENTIAL_STORE": "file"}
	a := newApp(IO{Out: io.Discard, Err: io.Discard}, func(k string) string { return env[k] }, BuildInfo{})
	if err := a.setup(); err != nil {
		t.Fatal(err)
	}
	a.cfg.Profiles["first"] = &config.Profile{URL: "https://first.test"}
	pageSize, rate := 25, 60
	a.cfg.Profiles["second"] = &config.Profile{URL: "https://second.test", PageSize: &pageSize, RateLimit: &rate}
	a.cfg.CurrentProfile = "first"
	d := tuiDeps{a}
	if _, err := d.Client(); err != nil {
		t.Fatal(err)
	}
	if err := d.SwitchProfile("second"); err != nil {
		t.Fatal(err)
	}
	name, url, _, _ := d.Profile()
	if name != "second" || url != "https://second.test" || a.client != nil || a.session != nil || a.baseHTTP != nil {
		t.Fatalf("profile not reset: %s %s", name, url)
	}
	if s := d.Settings(); s.PageSize != 25 || s.RateLimit != 60 {
		t.Fatalf("settings not switched: %+v", s)
	}
	for _, key := range []string{"SEVENTHINGS_PROFILE", "SEVENTHINGS_BASE_URL", "SEVENTHINGS_TOKEN"} {
		env[key] = "override"
		if err := d.SwitchProfile("first"); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("override %s not respected: %v", key, err)
		}
		delete(env, key)
	}
	a.profileFlag = "second"
	if err := d.SwitchProfile("first"); err == nil || !strings.Contains(err.Error(), "--profile") {
		t.Fatalf("flag override not respected: %v", err)
	}
}
