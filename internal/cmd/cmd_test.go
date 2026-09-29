package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
)

// fakeAPI is a minimal in-memory customer API.
type fakeAPI struct {
	mu      sync.Mutex
	objects []map[string]any
	calls   []string
	srv     *httptest.Server
}

func newFakeAPI(t *testing.T, n int) *fakeAPI {
	f := &fakeAPI{}
	for i := 1; i <= n; i++ {
		f.objects = append(f.objects, map[string]any{"uuid": fmt.Sprintf("u%d", i), "name": fmt.Sprintf("Object %d", i)})
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/customer-api/v1/")
	f.calls = append(f.calls, r.Method+" "+path)

	if path == "ping" {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer test-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	parts := strings.Split(path, "/")
	switch {
	case r.Method == "GET" && path == "objects":
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, per = max(page, 1), cmp(per, 100)
		start, end := min((page-1)*per, len(f.objects)), min(page*per, len(f.objects))
		_ = json.NewEncoder(w).Encode(map[string]any{"items": f.objects[start:end]})
	case r.Method == "GET" && path == "objects/count":
		_ = json.NewEncoder(w).Encode(map[string]int{"count": len(f.objects)})
	case r.Method == "POST" && path == "object":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["uuid"] = "new-uuid"
		f.objects = append(f.objects, body)
		w.Header().Set("Location", "/customer-api/v1/object/new-uuid")
		w.WriteHeader(http.StatusCreated)
	case len(parts) == 3 && parts[0] == "object" && parts[2] == "add-file":
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`[{"file-uuid":"f1","status":200},{"file-uuid":"f2","status":404}]`))
	case len(parts) == 2 && parts[0] == "object":
		obj := f.find(parts[1])
		if obj == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Object not found"}`))
			return
		}
		switch r.Method {
		case "GET":
			_ = json.NewEncoder(w).Encode(obj)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func cmp(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func (f *fakeAPI) find(uuid string) map[string]any {
	for _, o := range f.objects {
		if o["uuid"] == uuid {
			return o
		}
	}
	return nil
}

func (f *fakeAPI) wrote() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET ") {
			out = append(out, c)
		}
	}
	return out
}

type result struct {
	out, err string
	code     int
}

func (r result) json(t *testing.T) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(r.out), &v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, r.out)
	}
	return v
}

func (r result) errJSON(t *testing.T) errorBody {
	t.Helper()
	var e errorJSON
	if err := json.Unmarshal([]byte(r.err), &e); err != nil {
		t.Fatalf("stderr is not a JSON error: %v\n%s", err, r.err)
	}
	return e.Error
}

func run(t *testing.T, env map[string]string, stdin string, args ...string) result {
	t.Helper()
	var out, errBuf bytes.Buffer
	getenv := func(k string) string { return env[k] }
	code := Execute(context.Background(), args, IO{In: strings.NewReader(stdin), Out: &out, Err: &errBuf}, getenv, BuildInfo{Version: "test"})
	return result{out.String(), errBuf.String(), code}
}

func agentEnv(t *testing.T, f *fakeAPI) map[string]string {
	return map[string]string{
		"SEVENTHINGS_CONFIG_DIR":       t.TempDir(),
		"SEVENTHINGS_CREDENTIAL_STORE": "file",
		"SEVENTHINGS_BASE_URL":         f.srv.URL,
		"SEVENTHINGS_TOKEN":            "test-token",
		"SEVENTHINGS_RATE_LIMIT":       "0",
	}
}

func TestObjectsList(t *testing.T) {
	f := newFakeAPI(t, 3)
	r := run(t, agentEnv(t, f), "", "objects", "list", "--per-page", "2")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	if items := r.json(t).([]any); len(items) != 2 {
		t.Fatalf("got %d items", len(items))
	}
}

func TestObjectsListAllStreamsNDJSON(t *testing.T) {
	f := newFakeAPI(t, 5)
	r := run(t, agentEnv(t, f), "", "objects", "list", "--all", "--per-page", "2", "--fields", "uuid")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 5 || lines[4] != `{"uuid":"u5"}` {
		t.Fatalf("ndjson:\n%s", r.out)
	}
}

func TestNotFoundError(t *testing.T) {
	f := newFakeAPI(t, 1)
	r := run(t, agentEnv(t, f), "", "objects", "get", "missing")
	if r.code != exitcode.NotFound || r.out != "" {
		t.Fatalf("exit %d out %q", r.code, r.out)
	}
	e := r.errJSON(t)
	if e.Code != "not_found" || e.Status != 404 || !strings.Contains(e.Message, "Object not found") {
		t.Fatalf("error: %+v", e)
	}
}

func TestDestructiveNeedsYesInAgentMode(t *testing.T) {
	f := newFakeAPI(t, 1)
	env := agentEnv(t, f)
	r := run(t, env, "", "objects", "delete", "u1")
	if r.code != exitcode.Usage || len(f.wrote()) != 0 {
		t.Fatalf("exit %d, writes %v", r.code, f.wrote())
	}
	r = run(t, env, "", "objects", "delete", "u1", "--yes")
	if r.code != 0 || r.json(t).(map[string]any)["deleted"] != true {
		t.Fatalf("exit %d: %s %s", r.code, r.out, r.err)
	}
}

func TestCreateAndDryRun(t *testing.T) {
	f := newFakeAPI(t, 0)
	env := agentEnv(t, f)

	r := run(t, env, "", "objects", "create", "--set", "name=Laptop", "--set", "price:=999", "--dry-run")
	if r.code != 0 || len(f.wrote()) != 0 {
		t.Fatalf("dry run sent writes: %v (exit %d %s)", f.wrote(), r.code, r.err)
	}
	dr := r.json(t).(map[string]any)
	if dr["method"] != "POST" || dr["body"].(map[string]any)["price"] != float64(999) {
		t.Fatalf("dry run output: %s", r.out)
	}

	r = run(t, env, `{"name":"Laptop"}`, "objects", "create", "--data", "-")
	if r.code != 0 || r.json(t).(map[string]any)["uuid"] != "new-uuid" {
		t.Fatalf("exit %d: %s %s", r.code, r.out, r.err)
	}

	r = run(t, env, "", "objects", "create")
	if r.code != exitcode.Usage {
		t.Fatalf("empty create: exit %d", r.code)
	}
}

func TestPartialFilesExit8(t *testing.T) {
	f := newFakeAPI(t, 1)
	r := run(t, agentEnv(t, f), "", "objects", "files", "add", "u1", "--file", "photos=f1", "--file", "photos=f2")
	if r.code != exitcode.Partial {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	if items := r.json(t).([]any); len(items) != 2 {
		t.Fatalf("out: %s", r.out)
	}
}

func TestNotLoggedIn(t *testing.T) {
	f := newFakeAPI(t, 1)
	env := agentEnv(t, f)
	delete(env, "SEVENTHINGS_TOKEN")
	r := run(t, env, "", "objects", "list")
	if r.code != exitcode.Auth || r.errJSON(t).Code != "auth" {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
}

func TestAPICommand(t *testing.T) {
	f := newFakeAPI(t, 4)
	r := run(t, agentEnv(t, f), "", "api", "GET", "objects/count")
	if r.code != 0 || r.json(t).(map[string]any)["count"] != float64(4) {
		t.Fatalf("exit %d: %s %s", r.code, r.out, r.err)
	}
	r = run(t, agentEnv(t, f), "", "api", "post", "object", "--set", "name=X", "--include")
	res := r.json(t).(map[string]any)
	if r.code != 0 || res["status"] != float64(201) {
		t.Fatalf("exit %d: %s", r.code, r.out)
	}
	r = run(t, agentEnv(t, f), "", "api", "TRACE", "x")
	if r.code != exitcode.Usage {
		t.Fatalf("bad method exit %d", r.code)
	}
}

func TestJQAndYAML(t *testing.T) {
	f := newFakeAPI(t, 3)
	r := run(t, agentEnv(t, f), "", "objects", "list", "--jq", ".[].name", "-o", "ndjson")
	if r.out != "\"Object 1\"\n\"Object 2\"\n\"Object 3\"\n" {
		t.Fatalf("jq: %q", r.out)
	}
	r = run(t, agentEnv(t, f), "", "objects", "get", "u1", "-o", "yaml")
	if r.out != "name: Object 1\nuuid: u1\n" {
		t.Fatalf("yaml: %q", r.out)
	}
}

func TestUsageErrors(t *testing.T) {
	f := newFakeAPI(t, 0)
	for _, args := range [][]string{
		{"nope"},
		{"objects", "get"},
		{"objects", "list", "--bogus"},
		{"objects", "list", "--filter", "name ?? x"},
		{"-o", "xml", "objects", "list"},
		{},
	} {
		r := run(t, agentEnv(t, f), "", args...)
		if r.code != exitcode.Usage {
			t.Errorf("%v: exit %d, stderr %s", args, r.code, r.err)
			continue
		}
		if e := r.errJSON(t); e.Code != "usage" {
			t.Errorf("%v: %+v", args, e)
		}
	}
}

func TestLoginStatusLogout(t *testing.T) {
	var revoked bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /customer-api/v1/auth_token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "refresh_token": "rt", "expires_in": 3600, "user_id": 42})
		case "DELETE /customer-api/v1/auth_token":
			revoked = r.Header.Get("Authorization") == "Bearer at"
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	env := map[string]string{"SEVENTHINGS_CONFIG_DIR": t.TempDir(), "SEVENTHINGS_CREDENTIAL_STORE": "file", "SEVENTHINGS_RATE_LIMIT": "0"}

	r := run(t, env, "secret\n", "auth", "login", "--url", srv.URL, "--client-id", "cli", "--username", "me", "--password-stdin", "-p", "acme")
	if r.code != 0 || r.json(t).(map[string]any)["user_id"] != float64(42) {
		t.Fatalf("login exit %d: %s %s", r.code, r.out, r.err)
	}
	r = run(t, env, "", "auth", "status")
	st := r.json(t).(map[string]any)
	if r.code != 0 || st["profile"] != "acme" || st["logged_in"] != true || st["url"] != srv.URL {
		t.Fatalf("status exit %d: %s", r.code, r.out)
	}
	r = run(t, env, "", "auth", "token")
	if strings.TrimSpace(r.out) != "at" {
		t.Fatalf("token: %q %s", r.out, r.err)
	}
	r = run(t, env, "", "auth", "logout")
	if r.code != 0 || !revoked {
		t.Fatalf("logout exit %d revoked=%v: %s", r.code, revoked, r.err)
	}
	r = run(t, env, "", "auth", "status")
	if r.code != exitcode.Auth {
		t.Fatalf("status after logout: exit %d", r.code)
	}

	// Agent mode never prompts for a password.
	r = run(t, env, "", "auth", "login", "--username", "me")
	if r.code != exitcode.Usage {
		t.Fatalf("login without password: exit %d %s", r.code, r.err)
	}
}

// TestAgentContract runs every command from `describe` with no arguments in
// agent mode: nothing may hang or prompt, stdout must be empty or JSON, and
// failures must be a single JSON error line on stderr.
func TestAgentContract(t *testing.T) {
	f := newFakeAPI(t, 2)
	env := agentEnv(t, f)
	r := run(t, env, "", "describe")
	if r.code != 0 {
		t.Fatalf("describe: exit %d %s", r.code, r.err)
	}
	cat := r.json(t).(map[string]any)
	cmds := cat["commands"].([]any)
	if len(cmds) < 15 {
		t.Fatalf("only %d commands described", len(cmds))
	}
	for _, c := range cmds {
		d := c.(map[string]any)
		name := d["command"].(string)
		if name == "auth token" || name == "config path" {
			continue // plain-text output by design
		}
		r := run(t, env, "", strings.Fields(name)...)
		if strings.TrimSpace(r.out) != "" {
			for line := range strings.SplitSeq(strings.TrimSpace(r.out), "\n") {
				if !json.Valid([]byte(line)) && !json.Valid([]byte(r.out)) {
					t.Errorf("%s: stdout not JSON: %q", name, r.out)
					break
				}
			}
		}
		if r.code != 0 {
			r.errJSON(t)
		}
		if d["destructive"] == true && r.code == 0 {
			t.Errorf("%s: destructive command succeeded without --yes", name)
		}
	}
	if w := f.wrote(); len(w) != 0 {
		t.Errorf("argument-less runs performed writes: %v", w)
	}
}

func TestRateLimit429RetriedThroughCLI(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]int{"count": 7})
	}))
	defer srv.Close()
	env := map[string]string{
		"SEVENTHINGS_CONFIG_DIR": t.TempDir(), "SEVENTHINGS_BASE_URL": srv.URL, "SEVENTHINGS_TOKEN": "t",
	}
	r := run(t, env, "", "objects", "count")
	if r.code != 0 || hits != 2 || r.json(t).(map[string]any)["count"] != float64(7) {
		t.Fatalf("exit %d hits %d: %s %s", r.code, hits, r.out, r.err)
	}
	r = run(t, env, "", "auth", "status")
	if r.json(t).(map[string]any)["rate_limit_per_minute"] != float64(200) {
		t.Fatalf("default rate limit not 200: %s", r.out)
	}
	r = run(t, env, "", "auth", "status", "--rate-limit", "60")
	if r.json(t).(map[string]any)["rate_limit_per_minute"] != float64(60) {
		t.Fatalf("--rate-limit ignored: %s", r.out)
	}
}

func TestInteractiveTable(t *testing.T) {
	f := newFakeAPI(t, 2)
	var out, errBuf bytes.Buffer
	env := agentEnv(t, f)
	code := Execute(context.Background(), []string{"objects", "list"},
		IO{In: strings.NewReader(""), Out: &out, Err: &errBuf, StdinTTY: true, StdoutTTY: true},
		func(k string) string { return env[k] }, BuildInfo{})
	if code != 0 || out.String() != "UUID  NAME\nu1    Object 1\nu2    Object 2\n" {
		t.Fatalf("exit %d:\n%s%s", code, out.String(), errBuf.String())
	}

	// Interactive delete prompts; answering "n" aborts without a request.
	out.Reset()
	code = Execute(context.Background(), []string{"objects", "delete", "u1"},
		IO{In: strings.NewReader("n\n"), Out: &out, Err: &errBuf, StdinTTY: true, StdoutTTY: true},
		func(k string) string { return env[k] }, BuildInfo{})
	if code != exitcode.Usage || len(f.wrote()) != 0 || !strings.Contains(errBuf.String(), "[y/N]") {
		t.Fatalf("exit %d writes %v: %s", code, f.wrote(), errBuf.String())
	}
}
