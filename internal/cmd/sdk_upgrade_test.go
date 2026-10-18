package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

func TestCatalogCRUD(t *testing.T) {
	api := newRouteAPI(t)
	api.json("GET catalog-item/c1", 200, map[string]any{"uuid": "c1", "fields": map[string]any{
		"catalog_object_inventory_name": "Laptop", "custom": "keep", "nullable": "clear",
	}})
	api.routes["POST catalog-item"] = func(w http.ResponseWriter, _ []byte) {
		w.Header().Set("Location-UUID", "c2")
		w.WriteHeader(201)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		api.routes[method+" catalog-item/c1"] = func(w http.ResponseWriter, _ []byte) { w.WriteHeader(204) }
	}
	env := api.env(t)
	r := run(t, env, "", "catalog-items", "get", "c1")
	if r.code != 0 || r.json(t).(map[string]any)["uuid"] != "c1" {
		t.Fatalf("get: %+v", r)
	}
	r = run(t, env, "", "catalog-items", "create", "--set", "catalog_object_inventory_name=New", "--jq", ".uuid", "--raw")
	if r.code != 0 || r.out != "c2\n" {
		t.Fatalf("create: %+v", r)
	}
	if got := api.body("POST catalog-item"); !reflect.DeepEqual(got, map[string]any{"fields": map[string]any{"catalog_object_inventory_name": "New"}}) {
		t.Fatalf("create body: %v", got)
	}
	r = run(t, env, "", "catalog-items", "update", "c1", "--set", "catalog_object_inventory_name=Updated", "--set", "nullable:=null")
	if r.code != 0 {
		t.Fatalf("update: %+v", r)
	}
	got := api.body("PUT catalog-item/c1")["fields"].(map[string]any)
	if got["catalog_object_inventory_name"] != "Updated" || got["custom"] != "keep" || got["nullable"] != nil || len(got) != 3 {
		t.Errorf("replacement lost fields or identity leaked: %v", got)
	}
	for _, verb := range []string{"create", "update"} {
		args := []string{"catalog-items", verb}
		if verb == "update" {
			args = append(args, "c1")
		}
		if r := run(t, env, "", args...); r.code != exitcode.Usage {
			t.Errorf("%s without body: %+v", verb, r)
		}
	}
	r = run(t, env, "", "catalog-items", "delete", "c1")
	if r.code != exitcode.Usage {
		t.Fatalf("delete without confirmation: %+v", r)
	}
	if _, sent := api.bodies["DELETE catalog-item/c1"]; sent {
		t.Fatal("unconfirmed delete reached server")
	}
	r = run(t, env, "", "catalog", "delete", "c1", "--yes")
	if r.code != 0 {
		t.Fatalf("delete: %+v", r)
	}
}

func TestCatalogListAndAll(t *testing.T) {
	var pages []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/customer-api/v1/catalog-items" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		q := r.URL.Query()
		if q.Get("filter[catalog_object_inventory_name][like][]") != "Laptop" || q.Get("sort[id]") != "DESC" || q.Get("per_page") != "10" {
			t.Errorf("query: %v", q)
		}
		page, _ := strconv.Atoi(q.Get("page"))
		pages = append(pages, page)
		// The server caps the requested size to one item. All must still continue.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []any{map[string]any{"uuid": fmt.Sprint(page), "fields": map[string]any{"custom": "value"}}},
			"page":  page, "per_page": 1, "total": 2, "sort": []any{},
		})
	}))
	defer srv.Close()
	env := map[string]string{"SEVENTHINGS_CONFIG_DIR": t.TempDir(), "SEVENTHINGS_BASE_URL": srv.URL, "SEVENTHINGS_TOKEN": "t", "SEVENTHINGS_RATE_LIMIT": "0"}
	args := []string{"catalog-items", "list", "--page", "2", "--per-page", "10", "--filter", "catalog_object_inventory_name like Laptop", "--sort", "-id"}
	r := run(t, env, "", args...)
	if r.code != 0 || len(r.json(t).([]any)) != 1 {
		t.Fatalf("list: %+v", r)
	}
	pages = nil
	r = run(t, env, "", append(args, "--all")...)
	if r.code != 0 || !reflect.DeepEqual(pages, []int{1, 2}) {
		t.Fatalf("all: pages=%v, result=%+v", pages, r)
	}
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"uuid":"1"`) || !strings.Contains(lines[1], `"uuid":"2"`) {
		t.Errorf("NDJSON: %s", r.out)
	}
}

func TestCatalogDryRunAndErrors(t *testing.T) {
	api := newRouteAPI(t)
	api.json("GET catalog-item/c1", 200, map[string]any{"uuid": "c1", "fields": map[string]any{"custom": "keep"}})
	r := run(t, api.env(t), "", "catalog-items", "update", "c1", "--set", "catalog_object_inventory_name=New", "--dry-run")
	if r.code != 0 {
		t.Fatalf("dry run: %+v", r)
	}
	request := r.json(t).(map[string]any)
	fields := request["body"].(map[string]any)["fields"].(map[string]any)
	if request["method"] != "PUT" || fields["custom"] != "keep" || fields["catalog_object_inventory_name"] != "New" {
		t.Errorf("dry-run request: %v", request)
	}
	if _, sent := api.bodies["PUT catalog-item/c1"]; sent {
		t.Fatal("dry run sent PUT")
	}
	api.json("GET catalog-item/c1", 403, map[string]any{"message": "no permission"})
	r = run(t, api.env(t), "", "catalog-items", "update", "c1", "--set", "custom=new")
	if r.code != exitcode.Auth {
		t.Fatalf("GET failure: %+v", r)
	}
	if _, sent := api.bodies["PUT catalog-item/c1"]; sent {
		t.Fatal("failed read must prevent replacement")
	}
	api.json("GET catalog-items", 404, map[string]any{"message": "feature unavailable"})
	for _, all := range []bool{false, true} {
		args := []string{"catalog-items", "list"}
		if all {
			args = append(args, "--all")
		}
		if r := run(t, api.env(t), "", args...); r.code != exitcode.NotFound {
			t.Errorf("list error: %+v", r)
		}
	}
}

func TestCatalogIteratorTermination(t *testing.T) {
	for _, mode := range []string{"empty", "short", "stop", "error"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if mode == "error" && requests == 2 {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				items := []any{}
				if mode != "empty" {
					items = append(items, map[string]any{"uuid": "c1", "fields": map[string]any{}})
				}
				response := map[string]any{"items": items}
				if mode != "short" {
					response["per_page"] = 1
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer srv.Close()
			cl := client.NewWithToken(srv.URL, "t", client.WithRateLimit(0))
			opts := &models.ListOptions{Page: 9, PerPage: 1}
			if mode == "short" {
				// Without response metadata, fall back to the requested size.
				opts.PerPage = 2
			}
			items, errors := 0, 0
			for _, err := range catalogItemsAll(t.Context(), cl, opts) {
				if err != nil {
					errors++
				} else {
					items++
				}
				if mode == "stop" {
					break
				}
			}
			wantRequests, wantItems, wantErrors := 1, 1, 0
			if mode == "empty" {
				wantItems = 0
			}
			if mode == "error" {
				wantRequests, wantErrors = 2, 1
			}
			if requests != wantRequests || items != wantItems || errors != wantErrors || opts.Page != 9 {
				t.Errorf("requests=%d items=%d errors=%d options=%+v", requests, items, errors, opts)
			}
		})
	}
}

func TestFlatFieldResourcesRemainUntouched(t *testing.T) {
	api := newRouteAPI(t)
	for _, resource := range []string{"room", "location", "person"} {
		body := map[string]any{"uuid": "custom-uuid", "fields": map[string]any{"custom": "nested"}, "name": "Office", "custom": "keep"}
		if resource == "person" {
			body["person_uuid"] = "p1"
		}
		api.json("GET "+resource+"/r1", 200, body)
		r := run(t, api.env(t), "", resource, "get", "r1")
		if r.code != 0 || !reflect.DeepEqual(r.json(t), body) {
			t.Errorf("%s fields were changed: %+v", resource, r)
		}
	}
}

func TestThumbnailDimensions(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/customer-api/v1/file/f1/thumbnail" {
			t.Errorf("path: %s", r.URL.Path)
		}
		queries = append(queries, r.URL.RawQuery)
		_, _ = io.WriteString(w, "thumbnail")
	}))
	defer srv.Close()
	env := map[string]string{"SEVENTHINGS_CONFIG_DIR": t.TempDir(), "SEVENTHINGS_BASE_URL": srv.URL, "SEVENTHINGS_TOKEN": "t", "SEVENTHINGS_RATE_LIMIT": "0"}
	base := []string{"files", "thumbnail", "f1", "--out", "-"}
	r := run(t, env, "", base...)
	if r.code != 0 || r.out != "thumbnail" || queries[0] != "" {
		t.Fatalf("default thumbnail: %+v, queries %v", r, queries)
	}
	out := filepath.Join(t.TempDir(), "thumb.png")
	r = run(t, env, "", "files", "thumbnail", "f1", "--width", "320", "--height", "240", "--out", out)
	data, err := os.ReadFile(out)
	if r.code != 0 || err != nil || string(data) != "thumbnail" || queries[1] != "width=320&height=240" {
		t.Fatalf("custom thumbnail: %+v, file %q, error %v, queries %v", r, data, err, queries)
	}
	for _, flags := range [][]string{
		{"--width", "320"}, {"--height", "240"}, {"--width", "0", "--height", "240"},
		{"--width", "2001", "--height", "240"}, {"--width", "320", "--height", "-1"},
	} {
		r := run(t, env, "", append(base, flags...)...)
		if r.code != exitcode.Usage {
			t.Errorf("invalid dimensions %v: %+v", flags, r)
		}
	}
	if len(queries) != 2 {
		t.Errorf("invalid dimensions sent requests: %v", queries)
	}
}

func TestTaskReminderSemantics(t *testing.T) {
	api := newRouteAPI(t)
	api.routes["POST task-management/task"] = func(w http.ResponseWriter, _ []byte) {
		w.Header().Set("Location", "/task/t1")
		w.WriteHeader(201)
	}
	api.json("GET task-management/task/t1", 200, map[string]any{
		"uuid": "t1", "title": "Task", "reminders": []any{map[string]any{"unit": "days", "value": 2}},
	})
	api.routes["PUT task-management/task/t1"] = func(w http.ResponseWriter, _ []byte) { w.WriteHeader(204) }
	for _, verb := range []string{"create", "update"} {
		for _, value := range []string{"omitted", "[]", `[{"unit":"days","value":0}]`, "null"} {
			t.Run(verb+"/"+value, func(t *testing.T) {
				args := []string{"tasks", verb}
				method := "POST task-management/task"
				if verb == "create" {
					args = append(args, "--object", "o1", "--assignee", "u1", "--deadline", "2026-12-31")
				} else {
					args = append(args, "t1")
					method = "PUT task-management/task/t1"
				}
				args = append(args, "--title", "New")
				if value != "omitted" {
					args = append(args, "--set", "reminders:="+value)
				}
				r := run(t, api.env(t), "", args...)
				if r.code != 0 {
					t.Fatalf("result: %+v", r)
				}
				got, exists := api.body(method)["reminders"]
				switch {
				case value == "null" || (value == "omitted" && verb == "create"):
					if exists {
						t.Errorf("unspecified reminders must be omitted, got %v", got)
					}
				case value == "omitted":
					if got.([]any)[0].(map[string]any)["value"] != float64(2) {
						t.Errorf("stored reminders lost: %v", got)
					}
				case value == "[]":
					if !reflect.DeepEqual(got, []any{}) {
						t.Errorf("disable reminders: %v", got)
					}
				default:
					if got.([]any)[0].(map[string]any)["value"] != float64(0) {
						t.Errorf("on-deadline reminder lost: %v", got)
					}
				}
			})
		}
	}
}

func TestTimedRentalCreateAndUpdate(t *testing.T) {
	api := newRouteAPI(t)
	api.routes["POST rental-management/rental-case"] = func(w http.ResponseWriter, _ []byte) {
		w.Header().Set("Location", "/rental-case/r1")
		w.WriteHeader(201)
	}
	issue, due := "2026-12-01T09:00:00+02:00", "2026-12-01T11:00:00+02:00"
	base := []string{"rental-cases", "create", "--title", "Timed", "--renter", "plain:Jane", "--object", "o1",
		"--responsible", "u1", "--issue-date", "2026-12-01", "--due-date", "2026-12-01"}
	r := run(t, api.env(t), "", append(base, "--issue-at", issue, "--due-at", due, "--is-full-day=false")...)
	if r.code != 0 {
		t.Fatalf("create timed: %+v", r)
	}
	body := api.body("POST rental-management/rental-case")
	if body["is_full_day"] != false || body["issue_at"] != issue || body["due_at"] != due {
		t.Fatalf("timed body: %v", body)
	}
	r = run(t, api.env(t), "", base...)
	if r.code != 0 {
		t.Fatalf("full-day create: %+v", r)
	}
	for _, key := range []string{"is_full_day", "issue_at", "due_at"} {
		if _, ok := api.body("POST rental-management/rental-case")[key]; ok {
			t.Errorf("optional %s must be omitted", key)
		}
	}
	current := map[string]any{"uuid": "r1", "title": "Timed", "renter": map[string]any{"type": "plain", "value": "Jane"},
		"is_full_day": false, "issue_at": issue, "due_at": due, "issue_date": "2026-12-01", "due_date": "2026-12-01",
		"references": []any{map[string]any{"type": "asset", "uuid": "o1"}}, "responsible_user_uuid": "u1",
		"attachments": []any{map[string]any{"uuid": "f1", "name": "keep.pdf"}},
	}
	api.json("GET rental-management/rental-case/r1", 200, current)
	api.routes["PUT rental-management/rental-case/r1"] = func(w http.ResponseWriter, _ []byte) { w.WriteHeader(204) }
	for _, tc := range []struct {
		flags      []string
		issue, due any
		fullDay    any
	}{
		{[]string{"--comment", "keep times"}, issue, due, false},
		{[]string{"--due-date", "2026-12-02"}, nil, nil, nil},
		{[]string{"--is-full-day"}, nil, nil, true},
		{[]string{"--due-at", "2026-12-01T12:00:00Z"}, issue, "2026-12-01T12:00:00Z", false},
	} {
		r := run(t, api.env(t), "", append([]string{"rental-cases", "update", "r1"}, tc.flags...)...)
		if r.code != 0 {
			t.Fatalf("update %v: %+v", tc.flags, r)
		}
		got := api.body("PUT rental-management/rental-case/r1")
		if got["is_full_day"] != tc.fullDay || got["issue_at"] != tc.issue || got["due_at"] != tc.due {
			t.Errorf("update %v times: %v", tc.flags, got)
		}
		if got["renter"].(map[string]any)["value"] != "Jane" || got["attachments"].([]any)[0] != "f1" || got["responsible_user_uuid"] != "u1" {
			t.Errorf("update dropped state: %v", got)
		}
	}
	r = run(t, api.env(t), "", "rental-cases", "update", "r1", "--set", "renter:=null")
	if r.code != exitcode.Usage {
		t.Errorf("missing renter should be usage: %+v", r)
	}
	for _, timestamp := range []string{"2026-12-01", "2026-12-01T09:00:00", "invalid"} {
		if r := run(t, api.env(t), "", append(base, "--issue-at", timestamp)...); r.code != exitcode.Usage {
			t.Errorf("invalid timestamp %q: %+v", timestamp, r)
		}
	}
}

type sdkUpgradeTransport func(*http.Request) (*http.Response, error)

func (f sdkUpgradeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSDKDoesNotAddAnotherRateLimit(t *testing.T) {
	for _, rpm := range []string{"0", "600"} {
		t.Run(rpm, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				env := map[string]string{"SEVENTHINGS_CONFIG_DIR": t.TempDir(), "SEVENTHINGS_BASE_URL": "https://example.test", "SEVENTHINGS_TOKEN": "t", "SEVENTHINGS_RATE_LIMIT": rpm}
				a := newApp(IO{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}, func(k string) string { return env[k] }, BuildInfo{})
				a.transport = sdkUpgradeTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
				})
				if err := a.setup(); err != nil {
					t.Fatal(err)
				}
				cl, err := a.Client()
				if err != nil {
					t.Fatal(err)
				}
				start := time.Now()
				for range 3 {
					if _, err := cl.Get(t.Context(), "objects"); err != nil {
						t.Fatal(err)
					}
				}
				if elapsed := time.Since(start); elapsed != 0 {
					t.Errorf("SDK throttled requests within the CLI burst: %s", elapsed)
				}
			})
		})
	}
}
