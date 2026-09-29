package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

// routeAPI serves canned responses keyed by "METHOD path" and records bodies.
type routeAPI struct {
	mu     sync.Mutex
	routes map[string]func(w http.ResponseWriter, body []byte)
	bodies map[string][]byte
	srv    *httptest.Server
}

func newRouteAPI(t *testing.T) *routeAPI {
	r := &routeAPI{routes: map[string]func(http.ResponseWriter, []byte){}, bodies: map[string][]byte{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		key := req.Method + " " + strings.TrimPrefix(req.URL.Path, "/customer-api/v1/")
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies[key] = b
		h := r.routes[key]
		r.mu.Unlock()
		if h == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h(w, b)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *routeAPI) json(key string, status int, v any) {
	r.routes[key] = func(w http.ResponseWriter, _ []byte) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
}

func (r *routeAPI) body(key string) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var m map[string]any
	_ = json.Unmarshal(r.bodies[key], &m)
	return m
}

func (r *routeAPI) env(t *testing.T) map[string]string {
	return map[string]string{
		"SEVENTHINGS_CONFIG_DIR": t.TempDir(), "SEVENTHINGS_BASE_URL": r.srv.URL,
		"SEVENTHINGS_TOKEN": "t", "SEVENTHINGS_RATE_LIMIT": "0",
	}
}

func TestTaskUpdateKeepsUnchangedFields(t *testing.T) {
	api := newRouteAPI(t)
	deadline, comment := "2026-12-31", "keep me"
	api.json("GET task-management/task/t1", 200, models.Task{
		UUID: "t1", Title: "Old", Deadline: &deadline, Comment: &comment, Status: "open",
		Assignees:   []string{"user-1"},
		References:  []models.TaskReference{{Type: "asset", UUID: "obj-1", Name: "Laptop", ID: 3}},
		Attachments: []models.AttachmentFile{{UUID: "file-1", Name: "a.pdf"}},
	})
	api.routes["PUT task-management/task/t1"] = func(w http.ResponseWriter, _ []byte) { w.WriteHeader(204) }

	r := run(t, api.env(t), "", "tasks", "update", "t1", "--title", "New")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	sent := api.body("PUT task-management/task/t1")
	if sent["title"] != "New" || sent["deadline"] != deadline || sent["comment"] != comment {
		t.Errorf("fields not preserved: %v", sent)
	}
	if refs := sent["references"].([]any); len(refs) != 1 || refs[0].(map[string]any)["uuid"] != "obj-1" || refs[0].(map[string]any)["name"] != nil {
		t.Errorf("references not converted to input shape: %v", refs)
	}
	if att := sent["attachments"].([]any); len(att) != 1 || att[0] != "file-1" {
		t.Errorf("attachments not converted to UUIDs: %v", att)
	}
	if _, ok := sent["status"]; ok {
		t.Errorf("read-only field sent: %v", sent)
	}

	r = run(t, api.env(t), "", "tasks", "update", "t1", "--set", "titel=typo")
	if r.code != exitcode.Usage {
		t.Errorf("unknown field: exit %d", r.code)
	}
}

func TestTaskCreateFlags(t *testing.T) {
	api := newRouteAPI(t)
	api.routes["POST task-management/task"] = func(w http.ResponseWriter, _ []byte) {
		w.Header().Set("Location", "/customer-api/v1/task/new")
		w.WriteHeader(201)
	}
	r := run(t, api.env(t), "", "tasks", "create", "--title", "Check", "--object", "o1", "--assignee", "u1", "--deadline", "2026-12-31")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	r = run(t, api.env(t), "", "tasks", "create", "--title", "No object", "--assignee", "u1", "--deadline", "2026-12-31")
	if r.code != exitcode.Usage {
		t.Errorf("missing object: exit %d", r.code)
	}
	r = run(t, api.env(t), "", "tasks", "create", "--title", "Check", "--object", "o1", "--assignee", "u1", "--deadline", "2026-12-31")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	sent := api.body("POST task-management/task")
	if sent["title"] != "Check" || len(sent["references"].([]any)) != 1 || sent["reminders"] == nil {
		t.Errorf("body: %v", sent)
	}
	r = run(t, api.env(t), "", "tasks", "create", "--title", "No deadline", "--object", "o1", "--assignee", "u1")
	if r.code != exitcode.Usage {
		t.Errorf("missing deadline: exit %d", r.code)
	}
	r = run(t, api.env(t), "", "tasks", "create", "--title", "No assignee")
	if r.code != exitcode.Usage {
		t.Errorf("missing assignee: exit %d", r.code)
	}
	r = run(t, api.env(t), "", "tasks", "create", "--comment", "x")
	if r.code != exitcode.Usage {
		t.Errorf("missing title: exit %d", r.code)
	}
	r = run(t, api.env(t), "", "tasks", "set-status", "t1", "done")
	if r.code != exitcode.Usage {
		t.Errorf("bad status: exit %d", r.code)
	}
}

func TestRentalRenterFlag(t *testing.T) {
	api := newRouteAPI(t)
	api.routes["POST rental-management/rental-case"] = func(w http.ResponseWriter, _ []byte) {
		w.Header().Set("Location", "/x/rc-1")
		w.WriteHeader(201)
	}
	r := run(t, api.env(t), "", "rental-cases", "create", "--title", "Beamer", "--renter", "plain:Jane Doe", "--object", "o1")
	if r.code != exitcode.Usage {
		t.Fatalf("missing dates/responsible: exit %d", r.code)
	}
	r = run(t, api.env(t), "", "rental-cases", "create", "--title", "Beamer", "--renter", "plain:Jane Doe", "--object", "o1",
		"--issue-date", "2026-10-01", "--due-date", "2026-10-08", "--responsible", "u1")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	renter := api.body("POST rental-management/rental-case")["renter"].(map[string]any)
	if renter["type"] != "plain" || renter["value"] != "Jane Doe" {
		t.Errorf("renter: %v", renter)
	}
	r = run(t, api.env(t), "", "rental-cases", "create", "--renter", "bob")
	if r.code != exitcode.Usage {
		t.Errorf("bad renter: exit %d", r.code)
	}
}

func TestPersonsKeepCustomFields(t *testing.T) {
	api := newRouteAPI(t)
	api.json("GET person/p1", 200, map[string]any{"person_uuid": "p1", "email": "a@b.c", "custom_badge": "B-7"})
	r := run(t, api.env(t), "", "persons", "get", "p1")
	if r.code != 0 || r.json(t).(map[string]any)["custom_badge"] != "B-7" {
		t.Fatalf("exit %d: %s %s", r.code, r.out, r.err)
	}
}

func TestFieldsMissingExit5(t *testing.T) {
	api := newRouteAPI(t)
	api.json("GET asset-tracking/asset/field-definitions", 200, []models.FieldDefinition{
		{FieldKey: "inventory_name", Attributes: []models.FieldAttribute{{Type: "mandatory", Value: "yes"}}},
		{FieldKey: "serial"},
	})
	r := run(t, api.env(t), "", "fields", "missing", "--set", "serial=1")
	if r.code != exitcode.Invalid {
		t.Fatalf("exit %d: %s %s", r.code, r.out, r.err)
	}
	if m := r.json(t).(map[string]any); m["valid"] != false || len(m["missing"].([]any)) != 1 {
		t.Errorf("out: %s", r.out)
	}
	r = run(t, api.env(t), "", "fields", "missing", "--set", "inventory_name=x")
	if r.code != 0 {
		t.Errorf("valid record: exit %d %s", r.code, r.err)
	}
}

func TestFilesDownloadAndUpload(t *testing.T) {
	api := newRouteAPI(t)
	api.routes["GET file/f1/data"] = func(w http.ResponseWriter, _ []byte) { _, _ = w.Write([]byte("PDFDATA")) }
	api.routes["POST file"] = func(w http.ResponseWriter, _ []byte) {
		w.Header().Set("Location", "/customer-api/v1/file/f2")
		w.WriteHeader(201)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "x.pdf")
	r := run(t, api.env(t), "", "files", "download", "f1", "--out", out)
	if b, _ := os.ReadFile(out); r.code != 0 || string(b) != "PDFDATA" {
		t.Fatalf("exit %d file %q: %s", r.code, b, r.err)
	}
	r = run(t, api.env(t), "", "files", "download", "f1", "--out", "-")
	if r.out != "PDFDATA" {
		t.Errorf("stdout: %q", r.out)
	}
	r = run(t, api.env(t), "", "files", "download", "f1")
	if r.code != exitcode.Usage {
		t.Errorf("missing --out: exit %d", r.code)
	}

	src := filepath.Join(dir, "note.txt")
	_ = os.WriteFile(src, []byte("hi"), 0o600)
	r = run(t, api.env(t), "", "files", "upload", src, "--jq", ".uuid", "--raw")
	if r.code != 0 || r.out != "f2\n" {
		t.Errorf("upload: exit %d out %q err %s", r.code, r.out, r.err)
	}
}

func TestHubCommands(t *testing.T) {
	api := newRouteAPI(t)
	api.routes["POST circularity-hub/add-objects-to-circularity-hub"] = func(w http.ResponseWriter, _ []byte) { w.WriteHeader(204) }
	api.routes["POST circularity-hub/orders"] = func(w http.ResponseWriter, _ []byte) {
		w.Header().Set("Location-Id", "9")
		w.WriteHeader(201)
	}
	api.json("POST circularity-hub/suggest-category", 200, map[string]string{"o1": "furniture"})

	r := run(t, api.env(t), "", "hub", "items", "add-objects", "--object", "o1=furniture,12.50")
	if r.code != 0 {
		t.Fatalf("add: exit %d %s", r.code, r.err)
	}
	if e := api.body("POST circularity-hub/add-objects-to-circularity-hub")["o1"].(map[string]any); e["price"] != "12.50" {
		t.Errorf("add body: %v", e)
	}
	r = run(t, api.env(t), "", "hub", "orders", "create", "--item", "3,4", "--item", "5")
	if r.code != 0 || r.json(t).(map[string]any)["id"] != float64(9) {
		t.Fatalf("order: exit %d %s %s", r.code, r.out, r.err)
	}
	r = run(t, api.env(t), "", "hub", "items", "suggest-category", "--filter", "asset_uuid in o1,o2")
	if r.code != 0 {
		t.Fatalf("suggest: exit %d %s", r.code, r.err)
	}
	f := api.body("POST circularity-hub/suggest-category")["filter"].(map[string]any)["asset_uuid"].(map[string]any)
	if len(f["in"].([]any)) != 2 {
		t.Errorf("filter object: %v", f)
	}
	// The API returns numeric prices (the SDK types them as strings).
	api.json("POST circularity-hub/suggest-rest-price", 200, map[string]any{"o1": 42.5})
	r = run(t, api.env(t), "", "hub", "items", "suggest-price", "--set", "o1=furniture")
	if r.code != 0 || r.json(t).(map[string]any)["o1"] != 42.5 {
		t.Fatalf("suggest-price: exit %d %s %s", r.code, r.out, r.err)
	}
	r = run(t, api.env(t), "", "hub", "orders", "update", "9", "--set", "cancelled:=true")
	if r.code != exitcode.Usage {
		t.Errorf("cancel without reason: exit %d", r.code)
	}
	r = run(t, api.env(t), "", "hub", "items", "get", "abc")
	if r.code != exitcode.Usage {
		t.Errorf("non-numeric id: exit %d", r.code)
	}
}
