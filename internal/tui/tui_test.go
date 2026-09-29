package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"
)

type fakeAPI struct {
	mu    sync.Mutex
	calls []string
	srv   *httptest.Server
	tasks map[string]string // uuid → status
	// defsExtra is appended to the asset field definitions.
	defsExtra []models.FieldDefinition
	// extra routes ("METHOD path") take precedence over the built-in ones.
	extra map[string]http.HandlerFunc
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{tasks: map[string]string{"t1": "open"}}
	objects := []map[string]any{
		{"asset_uuid": "o1", "inventory_name": "Laptop Dell", "barcode": "BC-1", "inventory_group": "IT",
			"documents": []any{map[string]any{"uuid": "f9", "name": "manual.pdf"}}},
		{"asset_uuid": "o2", "inventory_name": "Desk", "barcode": "BC-2", "inventory_group": "Möbel"},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/customer-api/v1/")
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+path+"?"+r.URL.RawQuery+" "+string(body))
		f.mu.Unlock()
		enc := json.NewEncoder(w)
		if h, ok := f.extra[r.Method+" "+path]; ok {
			h(w, r)
			return
		}
		switch {
		case r.Method == "GET" && path == "objects":
			items := objects
			if strings.Contains(r.URL.RawQuery, "Desk") {
				items = objects[1:]
			}
			_ = enc.Encode(map[string]any{"items": items})
		case r.Method == "GET" && (path == "object/by-barcode/BC-1" || path == "object/by-barcode/o1" || path == "object/o1"):
			_ = enc.Encode(objects[0])
		case r.Method == "GET" && path == "object/o2":
			_ = enc.Encode(objects[1])
		case r.Method == "DELETE" && path == "object/o1", r.Method == "PATCH" && path == "object/o1":
			w.WriteHeader(204)
		case r.Method == "GET" && path == "rooms":
			_ = enc.Encode(map[string]any{"items": []map[string]any{{"uuid": "r1", "id": 7, "name": "Boardroom", "number": "B-1", "building_id": 1}}})
		case r.Method == "GET" && path == "asset-tracking/asset/field-definitions":
			_ = enc.Encode(append([]models.FieldDefinition{
				{FieldKey: "barcode", Label: "Barcode", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeBarcode},
					Attributes: []models.FieldAttribute{{Type: "mandatory", Value: "yes"}}},
				{FieldKey: "inventory_name", Label: "Name", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeText}},
				{FieldKey: "asset_uuid", Label: "UUID", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeText},
					Attributes: []models.FieldAttribute{{Type: "mandatory", Value: "yes"}}},
				{FieldKey: "documents", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeAttachment}},
			}, f.defsExtra...))
		case r.Method == "GET" && path == "task-management/tasks":
			f.mu.Lock()
			st := f.tasks["t1"]
			f.mu.Unlock()
			_ = enc.Encode([]map[string]any{{"uuid": "t1", "title": "Check extinguisher", "status": st}})
		case r.Method == "POST" && path == "file":
			w.Header().Set("Location", "/customer-api/v1/file/f10")
			w.WriteHeader(201)
		case r.Method == "POST" && (path == "object/o1/add-file" || path == "object/o1/remove-file"):
			w.WriteHeader(200)
		case r.Method == "POST" && path == "circularity-hub/suggest-category":
			_ = enc.Encode(map[string]string{"o1": "electronics"})
		case r.Method == "POST" && path == "circularity-hub/suggest-rest-price":
			_ = enc.Encode(map[string]any{"o1": 42.5})
		case r.Method == "POST" && path == "circularity-hub/add-objects-to-circularity-hub":
			w.WriteHeader(204)
		case r.Method == "GET" && path == "circularity-hub/items":
			_ = enc.Encode(map[string]any{"items": []map[string]any{{"id": 12, "asset_id": 3, "price": "30.00", "object_data": map[string]any{"internal_identifier": "BC-CHAIR"}}}})
		case r.Method == "POST" && path == "circularity-hub/orders":
			w.Header().Set("Location-Id", "99")
			w.WriteHeader(201)
		case r.Method == "POST" && path == "rental-management/rental-case":
			w.Header().Set("Location", "/customer-api/v1/rental-management/rental-case/rc9")
			w.WriteHeader(201)
		case r.Method == "GET" && path == "rental-management/rental-cases":
			_ = enc.Encode(map[string]any{"items": []any{}})
		case r.Method == "GET" && path == "users":
			_ = enc.Encode(map[string]any{"items": []map[string]any{
				{"uuid": "u-1", "email": "ada@example.test", "display_name": "Ada"},
				{"uuid": "u-2", "email": "linus@example.test"},
			}})
		case r.Method == "POST" && path == "task-management/task":
			w.Header().Set("Location", "/customer-api/v1/task-management/task/t2")
			w.WriteHeader(201)
		case r.Method == "PUT" && path == "task-management/task/t1/status":
			var b map[string]string
			_ = json.Unmarshal(body, &b)
			f.mu.Lock()
			f.tasks["t1"] = b["status"]
			f.mu.Unlock()
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

type fakeDeps struct {
	env      map[string]string
	url      string
	loggedIn bool
	mu       sync.Mutex
	login    []string
	settings Settings
}

func (d *fakeDeps) Client() (*client.Client, error) { return client.NewWithToken(d.url, "tok"), nil }
func (d *fakeDeps) LoggedIn() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.loggedIn
}
func (d *fakeDeps) Login(_ context.Context, url, clientID, username, password string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.login = []string{url, clientID, username, password}
	d.loggedIn = true
	return nil
}
func (d *fakeDeps) Profile() (string, string, string, string) { return "test", d.url, "cid", "me" }
func (d *fakeDeps) Env(key string) string                     { return d.env[key] }
func (d *fakeDeps) Settings() Settings {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.settings
	if s.PageSize == 0 {
		s.PageSize = DefaultPageSize
	}
	if s.RateLimitSource == "" {
		s.RateLimit, s.RateLimitSource = 200, "default"
	}
	if s.Credentials == "" {
		s.Credentials = "file"
	}
	return s
}
func (d *fakeDeps) SetRateLimit(n int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.settings.RateLimit, d.settings.RateLimitSource = n, "profile"
	return nil
}
func (d *fakeDeps) SetPageSize(n int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.settings.PageSize = n
	return nil
}
func (d *fakeDeps) Logout(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.loggedIn = false
	return nil
}

func (d *fakeDeps) Profiles() []string         { return []string{"test"} }
func (d *fakeDeps) SwitchProfile(string) error { return nil }

// driver runs the model synchronously: it feeds messages to Update and
// executes the returned commands, so tests can assert on the rendered view.
type driver struct {
	t   *testing.T
	m   *Model
	raw []string // escape sequences sent with tea.Raw
}

func newDriver(t *testing.T, deps Deps) *driver {
	d := &driver{t: t, m: New(context.Background(), deps)}
	d.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	d.exec(d.m.Init())
	return d
}

func (d *driver) send(msg tea.Msg) {
	_, cmd := d.m.Update(msg)
	d.exec(cmd)
}

// exec runs cmd and feeds its messages back. Timers (spinner ticks, cursor
// blinks) are dropped so the loop terminates.
func (d *driver) exec(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(150 * time.Millisecond):
		return // a timer; irrelevant for tests
	}
	switch msg := msg.(type) {
	case nil, spinner.TickMsg:
		return
	case tea.BatchMsg:
		for _, c := range msg {
			d.exec(c)
		}
		return
	case tea.QuitMsg:
		return
	case tea.RawMsg:
		d.raw = append(d.raw, fmt.Sprint(msg.Msg))
		return
	}
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[tea.Cmd]() {
		for i := range v.Len() { // tea.Sequence
			d.exec(v.Index(i).Interface().(tea.Cmd))
		}
		return
	}
	d.send(msg)
}

func (d *driver) key(keys ...string) {
	for _, k := range keys {
		switch k {
		case "enter":
			d.send(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "esc":
			d.send(tea.KeyPressMsg{Code: tea.KeyEscape})
		case "down":
			d.send(tea.KeyPressMsg{Code: tea.KeyDown})
		case "up":
			d.send(tea.KeyPressMsg{Code: tea.KeyUp})
		case "tab":
			d.send(tea.KeyPressMsg{Code: tea.KeyTab})
		case "left":
			d.send(tea.KeyPressMsg{Code: tea.KeyLeft})
		case "backspace":
			d.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
		default:
			for _, c := range k {
				d.send(tea.KeyPressMsg{Code: c, Text: string(c)})
			}
		}
	}
}

func (d *driver) screen() string { return ansi.Strip(d.m.render()) }

func (d *driver) expect(want ...string) {
	d.t.Helper()
	s := d.screen()
	for _, w := range want {
		if !strings.Contains(s, w) {
			d.t.Fatalf("screen does not contain %q:\n%s", w, s)
		}
	}
}

func TestProgramEndToEnd(t *testing.T) {
	f := newFakeAPI(t)
	tm := teatest.NewTestModel(t, New(context.Background(), &fakeDeps{url: f.srv.URL, loggedIn: true}),
		teatest.WithInitialTermSize(120, 30),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.Ascii)))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte("Laptop Dell")) },
		teatest.WithDuration(3*time.Second))
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestListAndDetail(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.expect("Laptop Dell", "Desk", "2 objects on page 1", "1 Objects", "0 Hub orders")

	d.key("down", "enter")
	d.expect("inventory_name", "Desk", "esc back")
	d.key("esc", "up", "enter")
	d.expect("barcode", "BC-1")
	if !f.called("GET object/o1") {
		t.Errorf("detail not fetched: %v", f.calls)
	}
}

func TestTabSwitch(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("2")
	d.expect("Boardroom", "B-1", "Location ID")
	d.key("tab", "tab", "tab", "tab")
	d.expect("Check extinguisher")
}

func TestSearchUsesServerFilter(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("s", "Desk", "enter")
	d.expect(`search: "Desk"`, "1 objects")
	if !f.called("GET objects?page=1&per_page=50&filter[inventory_name][like][]=Desk") {
		t.Errorf("no filtered request: %v", f.calls)
	}
	d.key("esc")
	d.expect("Laptop Dell", "page 1")
}

func TestDeleteNeedsConfirmation(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("d")
	d.expect(`Delete object "Laptop Dell"? [y/N]`)
	d.key("n")
	d.expect("Cancelled")
	if f.called("DELETE") {
		t.Fatal("deleted without confirmation")
	}
	d.key("d", "y")
	d.expect("Deleted object o1")
	if !f.called("DELETE object/o1") {
		t.Errorf("no DELETE: %v", f.calls)
	}
}

func TestEditFormFromFieldDefinitions(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("e")
	// Mandatory barcode first; server-managed asset_uuid is not offered.
	d.expect(`Edit object "Laptop Dell"`, "barcode *", "inventory_name", "esc cancel")
	if strings.Contains(d.screen(), "asset_uuid") {
		t.Error("server-managed field offered")
	}
	d.key("enter", " 15", "enter")
	d.expect("Updated object (1 field(s))")
	if !f.called(`PATCH object/o1? {"inventory_name":"Laptop Dell 15"}`) {
		t.Errorf("patch body: %v", f.calls)
	}

	d.key("e", "esc")
	d.expect("Cancelled")
}

func TestTaskToggle(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("6")
	d.expect("Check extinguisher", "open", "c open/close")
	d.key("c")
	d.expect("Task is now closed", "closed")
	if !f.called(`PUT task-management/task/t1/status? {"status":"closed"}`) {
		t.Errorf("calls: %v", f.calls)
	}
}

func TestTaskCreateWithAssignee(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("6", "n")
	d.expect("New task", "Title *", "Assignee *", "Ada <ada@example.test>", "linus@example.test", "Object (UUID or barcode) *")
	d.key("Inspect", "enter", "down", "enter", "BC-1", "enter", "2026-12-24", "enter", "enter")
	d.expect("Created task t2")
	if !f.called(`POST task-management/task? {"title":"Inspect","deadline":"2026-12-24","assignees":["u-2"],"references":[{"type":"asset","uuid":"o1"}]`) {
		t.Errorf("create body: %v", f.calls)
	}
}

func TestNewTaskFromObject(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("t")
	d.expect("New task", "> o1")
	d.key("Service", "enter", "enter", "enter", "enter")
	d.expect("Deadline", "is required") // the API rejects tasks without a deadline
	d.key("2027-01-15", "enter", "enter")
	d.expect("Created task t2")
	if !f.called(`POST task-management/task? {"title":"Service","deadline":"2027-01-15","assignees":["u-1"],"references":[{"type":"asset","uuid":"o1"}]`) {
		t.Errorf("create body: %v", f.calls)
	}
}

func TestRentalCaseCreate(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("7", "n")
	d.expect("New rental case", "Renter", "(external renter: enter a name below)", "Responsible user *")
	// Title, keep "external renter", leave the name empty: blocked.
	d.key("Beamer", "enter", "enter", "enter")
	d.expect("choose a user above or enter a name")
	d.key("Jane Doe", "enter", "BC-1", "enter", "2026-10-01", "enter", "2026-10-08", "enter", "enter", "enter")
	d.expect("Created rental case rc9")
	want := `POST rental-management/rental-case? {"title":"Beamer","renter":{"type":"plain","value":"Jane Doe"},` +
		`"references":[{"type":"asset","uuid":"o1"}],"issue_date":"2026-10-01","due_date":"2026-10-08",` +
		`"comment":"","responsible_user_uuid":"u-1","attachments":[]}`
	if !f.called(want) {
		t.Errorf("create body:\n%v", f.calls)
	}
}

func TestLinkedFieldPicker(t *testing.T) {
	f := newFakeAPI(t)
	f.defsExtra = []models.FieldDefinition{{FieldKey: "actual_room", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeLinkedRoom}}}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("e")
	d.expect("actual_room", "Boardroom (B-1)")
	d.key("enter", "enter", "down", "enter")
	d.expect("Updated object (1 field(s))")
	if !f.called(`PATCH object/o1? {"actual_room":7}`) {
		t.Errorf("calls: %v", f.calls)
	}
}

func TestAttachAndDetach(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	path := filepath.Join(t.TempDir(), "photo.png")
	_ = os.WriteFile(path, []byte("png"), 0o600)

	d.key("a")
	d.expect(`Attach a file to "Laptop Dell"`, "File field *", "File to upload (path) *")
	d.key("enter", "/nope", "enter")
	d.expect("file not found")
	for range len("/nope") {
		d.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	d.key(path, "enter")
	d.expect("Attached photo.png to documents")
	if !f.called(`POST object/o1/add-file? [{"field-key":"documents","file-uuid":"f10"}]`) {
		t.Errorf("calls: %v", f.calls)
	}

	d.key("x")
	d.expect("documents: manual.pdf")
	d.key("enter")
	d.expect("Remove documents: manual.pdf? The file stays in Files. [y/N]")
	d.key("y")
	d.expect("Removed documents: manual.pdf")
	if !f.called(`POST object/o1/remove-file? [{"field-key":"documents","file-uuid":"f9"}]`) {
		t.Errorf("calls: %v", f.calls)
	}
}

func TestHubOfferAndOrder(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("o")
	d.expect(`Offer "Laptop Dell" on the circularity hub`, "electronics", "42.5")
	d.key("enter", "enter")
	d.expect(`Offer "Laptop Dell" on the circularity hub as "electronics" for 42.5? This publishes a resale listing. [y/N]`)
	d.key("n")
	if f.called("POST circularity-hub/add-objects") {
		t.Fatal("offered without confirmation")
	}
	d.key("o", "enter", "enter", "y")
	d.expect(`Offered "Laptop Dell" on the circularity hub`)
	if !f.called(`POST circularity-hub/add-objects-to-circularity-hub? {"o1":{"category":"electronics","price":"42.5"}}`) {
		t.Errorf("calls: %v", f.calls)
	}

	d.key("9")
	d.expect("BC-CHAIR", "30.00", "o order")
	d.key("o")
	d.expect("Create a circularity hub order for item #12? [y/N]")
	d.key("y")
	d.expect("Created hub order 99")
	if !f.called(`POST circularity-hub/orders?`) {
		t.Errorf("calls: %v", f.calls)
	}
}

func TestLoginForm(t *testing.T) {
	f := newFakeAPI(t)
	deps := &fakeDeps{url: f.srv.URL}
	d := newDriver(t, deps)
	d.expect("Log in to seventhings", "Instance URL", "Password")
	d.key("enter", "enter", "enter", "s3cret", "enter")
	d.expect("Logged in", "Laptop Dell")
	if strings.Join(deps.login, "|") != f.srv.URL+"|cid|me|s3cret" {
		t.Errorf("login args: %v", deps.login)
	}
	if strings.Contains(d.screen(), "s3cret") {
		t.Error("password shown on screen")
	}
}

func TestShortAndFullForms(t *testing.T) {
	text := models.FieldDefinitionFieldType{Name: models.FieldTypeText}
	defs := []models.FieldDefinition{
		{FieldKey: "barcode", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeBarcode},
			Attributes: []models.FieldAttribute{{Type: "mandatory", Value: "yes"}}},
		{FieldKey: "inventory_name", FieldType: text},
		{FieldKey: "custom_9", FieldType: text},
		{FieldKey: "custom_7", FieldType: text},
		{FieldKey: "asset_uuid", FieldType: text, Attributes: []models.FieldAttribute{{Type: "mandatory", Value: "yes"}}},
		{FieldKey: "picture", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeAttachment}},
	}
	keys := func(fs []*editField) string {
		var k []string
		for _, f := range fs {
			k = append(k, f.key)
		}
		return strings.Join(k, ",")
	}
	tmpl := models.AssetTrackingTemplateAsset
	if got := keys(fieldsFromDefinitions(defs, tmpl, nil, false)); got != "barcode,inventory_name" {
		t.Errorf("short create: %s", got)
	}
	if got := keys(fieldsFromDefinitions(defs, tmpl, Item{"custom_7": "x"}, false)); got != "barcode,inventory_name,custom_7" {
		t.Errorf("short edit keeps filled fields: %s", got)
	}
	if got := keys(fieldsFromDefinitions(defs, tmpl, nil, true)); got != "barcode,inventory_name,custom_9,custom_7" {
		t.Errorf("full: %s", got)
	}
}

func TestRecordFormBody(t *testing.T) {
	fields := []*editField{
		{key: "name", kind: models.FieldTypeText, orig: "Old"},
		{key: "price", kind: models.FieldTypeMoney, orig: "10"},
		{key: "room", kind: models.FieldTypeLinkedRoom, orig: ""},
		{key: "active", kind: models.FieldTypeBoolean, orig: ""},
		{key: "note", kind: models.FieldTypeText, orig: "x"},
	}
	rf := newRecordForm("t", fields, 80)
	*fields[0].text = "New"
	*fields[1].text = "12,50"
	*fields[2].text = "3"
	*fields[4].text = ""
	b := rf.body(true)
	if b["name"] != "New" || b["price"] != 12.5 || b["room"] != 3 || b["note"] != nil || len(b) != 4 {
		t.Errorf("edit body: %#v", b)
	}
	if _, ok := b["active"]; ok {
		t.Errorf("unchanged boolean sent: %#v", b)
	}
	if err := fields[1].validate("abc"); err == nil {
		t.Error("money validation")
	}
	if err := (&editField{kind: models.FieldTypeDate}).validate("31.12.2026"); err == nil {
		t.Error("date validation")
	}
}
