package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/charmbracelet/x/ansi"
)

func TestStaleFormPreparationDropped(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	edit := d.press("e")
	if d.m.writing != 0 {
		t.Fatal("field-definition reads counted as mutations")
	}
	d.key("2")
	d.exec(edit)
	if d.m.mode != modeList || d.m.tab != 1 || d.m.form != nil {
		t.Fatal("stale object form opened on Rooms")
	}
	d.expect("Boardroom")
	called := false
	scope := operationScope{tab: d.m.tab, nav: d.m.nav, session: d.m.session}
	d.key(",")
	d.send(loadedMsg{read: 999, scope: scope, msg: choicesMsg{next: func() tea.Cmd { called = true; return nil }}})
	if called || d.m.mode != modeSettings {
		t.Fatal("stale picker continuation replaced settings")
	}
}

func TestSettingsInvalidatesPendingDetail(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	detail := d.press("enter")
	d.key(",")
	d.exec(detail)
	if d.m.mode != modeSettings {
		t.Fatal("pending detail replaced Settings")
	}
}

func TestCachedTabRestoresOwnFooter(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("2")
	d.expect("1 rooms on page 1")
	d.key("1")
	d.expect("2 objects on page 1")
	if strings.Contains(d.m.status, "rooms") || countCalls(f, "GET objects?") != 1 {
		t.Fatal("cached tab kept another resource's status or unnecessarily reloaded")
	}
}

func TestWriteCompletionUsesOriginatingTab(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("d")
	deletion := d.press("y")
	d.key("2")
	before := countCalls(f, "GET rooms")
	d.exec(deletion)
	if d.m.tabs[0].loaded || !d.m.tabs[1].loaded || d.m.tab != 1 || d.m.mode != modeList {
		t.Fatal("write completion affected the wrong tab")
	}
	if countCalls(f, "GET rooms") != before {
		t.Fatal("object delete reloaded Rooms")
	}
	d.key("1")
	if countCalls(f, "GET objects?") != 2 {
		t.Fatal("originating tab was not reloaded on return")
	}
}

func TestDuplicateMutationsBlocked(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("6")
	first := d.press("c")
	second := d.press("c")
	if second != nil || d.m.writing != 1 {
		t.Fatal("duplicate mutation was allowed")
	}
	d.key(",", "enter")
	if d.m.mode != modeSettings {
		t.Fatal("settings changed while a write was pending")
	}
	d.exec(first)
	if countCalls(f, "PUT task-management/task/t1/status") != 1 || d.m.writing != 0 {
		t.Fatal("duplicate request or stuck pending state")
	}
}

func TestFailedEditRetainsValuesAndDiff(t *testing.T) {
	f := newFakeAPI(t)
	f.extra = map[string]http.HandlerFunc{
		"PATCH object/o1": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"detail":"Please correct the name"}`))
		},
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("e")
	for _, field := range d.m.record.fields {
		if field.key == "inventory_name" {
			*field.text = "Corrected name"
		}
	}
	d.exec(d.m.submitForm())
	if d.m.mode != modeForm || d.m.record.value("inventory_name") != "Corrected name" {
		t.Fatal("failed edit lost entered values")
	}
	body := d.m.record.body(true)
	if len(body) != 1 || body["inventory_name"] != "Corrected name" {
		t.Fatalf("original diff was lost: %v", body)
	}
	d.expect("Please correct the name")
	if countCalls(f, "PATCH object/o1") != 1 {
		t.Fatal("failed mutation was retried automatically")
	}
	f.extra["PATCH object/o1"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }
	d.exec(d.m.submitForm())
	if d.m.mode != modeList || countCalls(f, "PATCH object/o1") != 2 {
		t.Fatal("editable retry failed")
	}
}

func TestCancelledSessionDropsOldResponses(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	scope := operationScope{tab: 0, nav: d.m.nav, session: d.m.session}
	d.m.kitty.ids[kittyKey{uuid: "old", cols: 1, rows: 1}] = 1
	d.m.ov.cache["old"] = "old graphics"
	d.exec(d.m.loggedOut())
	d.send(loadedMsg{read: 999, scope: scope, msg: itemsMsg{tab: 0, seq: 0, items: []Item{{"inventory_name": "Old tenant"}}}})
	d.send(pictureMsg{key: "old", session: scope.session})
	if len(d.m.tabs[0].items) != 0 || len(d.m.pictures) != 0 || len(d.m.kitty.ids) != 0 || len(d.m.ov.cache) != 0 || d.m.mode != modeLogin {
		t.Fatal("old session data survived logout")
	}
}

func TestReauthenticationClearsTenantCaches(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.m.defs[models.AssetTrackingTemplateAsset] = []models.FieldDefinition{{FieldKey: "old"}}
	d.m.pictures["old"] = &picture{}
	d.m.choiceCache[srcRoomID] = [][2]string{{"Old room", "1"}}
	session := d.m.session
	d.send(loginMsg{})
	if d.m.session == session || len(d.m.defs) != 0 || len(d.m.pictures) != 0 || len(d.m.choiceCache) != 0 {
		t.Fatal("login retained previous tenant caches")
	}
}

func TestNetworkFailureRequiresManualReview(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("e")
	d.m.rememberForm()
	saved := d.m.recovery
	d.m.recovery = nil
	d.m.form, d.m.mode = nil, modeList
	d.send(loadedMsg{scope: operationScope{tab: d.m.tab, nav: d.m.nav, session: d.m.session}, recovery: saved,
		msg: doneMsg{err: &url.Error{Op: "PATCH", URL: f.srv.URL, Err: context.DeadlineExceeded}, reload: true}})
	if d.m.mode != modeForm || !strings.Contains(d.m.status, "Outcome unknown") || countCalls(f, "PATCH object/o1") != 0 {
		t.Fatal("network failure did not preserve a manual-review form")
	}
}

func TestPopulatedViewsFitShortTerminal(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	for _, size := range [][2]int{{40, 16}, {60, 20}, {80, 24}} {
		d.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, key := range []string{"", "e", "esc", "enter", "esc", ",", "esc"} {
			if key != "" {
				d.key(key)
			}
			screen := d.screen()
			for _, line := range strings.Split(screen, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("%v, mode %v: line exceeds width: %q", size, d.m.mode, line)
				}
			}
			if len(strings.Split(screen, "\n")) > size[1] {
				t.Fatalf("%v, mode %v: screen exceeds height:\n%s", size, d.m.mode, screen)
			}
		}
	}
}

func TestDownloadOverwriteConfirmation(t *testing.T) {
	f := newFakeAPI(t)
	f.extra = map[string]http.HandlerFunc{
		"GET files": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"items":[{"uuid":"f1","name":"note.txt"}]}`))
		},
		"GET file/f1/data": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("new")) },
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("8")
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, answer := range []string{"n", "y"} {
		d.key("D")
		d.m.path.path = path
		d.exec(d.m.submitForm())
		if d.m.mode != modeConfirm {
			t.Fatal("existing download did not ask for confirmation")
		}
		d.key(answer)
		want := "old"
		if answer == "y" {
			want = "new"
		}
		if b, err := os.ReadFile(path); err != nil || string(b) != want {
			t.Fatalf("answer %s: %q, %v", answer, b, err)
		}
	}
}

func TestCalendarValidation(t *testing.T) {
	for _, tc := range []struct {
		kind  models.FieldTypeName
		value string
		valid bool
	}{
		{models.FieldTypeDate, "2026-02-29", false},
		{models.FieldTypeDate, "2024-02-29", true},
		{models.FieldTypeDate, "2026-99-99", false},
		{models.FieldTypeDatetime, "2026-09-29T25:00:00", false},
		{models.FieldTypeDatetime, "2026-09-29garbage", false},
		{models.FieldTypeDatetime, "2026-09-29T10:30:00Z", true},
		{models.FieldTypeDatetime, "2026-09-29 10:30", true},
	} {
		err := (&editField{kind: tc.kind}).validate(tc.value)
		if (err == nil) != tc.valid {
			t.Errorf("%s: valid=%v, error=%v", tc.value, tc.valid, err)
		}
	}
}

func TestRequiredNonInputFields(t *testing.T) {
	m := New(context.Background(), &fakeDeps{loggedIn: true})
	for _, kind := range []models.FieldTypeName{models.FieldTypeDropdown, models.FieldTypeLongText} {
		field := &editField{key: "required", label: "Required field", kind: kind, required: true}
		if kind == models.FieldTypeDropdown {
			field.options = []string{"one", "two"}
		}
		m.record = newRecordForm("test", []*editField{field}, 80)
		*field.text = ""
		m.form, m.formKind, m.mode = m.record.form, formCreate, modeForm
		if cmd := m.submitForm(); cmd != nil || m.form == nil || m.mode != modeForm || !m.statusErr {
			t.Fatalf("empty mandatory %s submitted", kind)
		}
	}
}

func TestSmallTerminalAndEmptyStates(t *testing.T) {
	f := newFakeAPI(t)
	f.extra = map[string]http.HandlerFunc{
		"GET objects": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"items":[]}`)) },
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.expect("No objects yet", "Press n to create")
	d.m.tabs[0].search = "missing"
	d.expect("No matches", "Esc to clear")
	for _, size := range [][2]int{{40, 16}, {60, 20}, {80, 24}} {
		d.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, line := range strings.Split(d.screen(), "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("%v: line exceeds terminal: %q", size, line)
			}
		}
		if len(strings.Split(d.screen(), "\n")) > size[1] {
			t.Fatalf("%v: screen exceeds terminal height", size)
		}
		d.key("?")
		d.expect("Keys")
		d.key("esc")
	}
	d.send(tea.WindowSizeMsg{Width: 30, Height: 10})
	d.expect("Terminal too small")
}

func TestFormFitsShortTerminal(t *testing.T) {
	var fields []*editField
	for i := range 14 {
		fields = append(fields, &editField{key: fmt.Sprint(i), label: fmt.Sprintf("Field %d", i), kind: models.FieldTypeText})
	}
	rf := newRecordForm("Short form", fields, 36, 16)
	if !strings.Contains(ansi.Strip(rf.form.View()), "(1/7)") {
		t.Fatalf("short terminal did not paginate fields: %s", rf.form.View())
	}
}

type profileDeps struct {
	*fakeDeps
	active string
	urls   map[string]string
	auth   map[string]bool
}

func (d *profileDeps) Profiles() []string { return []string{"first", "second", "new"} }
func (d *profileDeps) Profile() (string, string, string, string) {
	return d.active, d.urls[d.active], "cid", "me"
}
func (d *profileDeps) Client() (*client.Client, error) {
	return client.NewWithToken(d.urls[d.active], "tok"), nil
}
func (d *profileDeps) LoggedIn() bool                  { return d.auth[d.active] }
func (d *profileDeps) SwitchProfile(name string) error { d.active = name; return nil }

func TestProfileSwitchClearsTenantState(t *testing.T) {
	first, second := newFakeAPI(t), newFakeAPI(t)
	second.extra = map[string]http.HandlerFunc{
		"GET objects": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []Item{{"asset_uuid": "new", "inventory_name": "Second tenant"}}})
		},
	}
	deps := &profileDeps{fakeDeps: &fakeDeps{}, active: "first", urls: map[string]string{"first": first.srv.URL, "second": second.srv.URL, "new": second.srv.URL}, auth: map[string]bool{"first": true, "second": true}}
	d := newDriver(t, deps)
	pending := d.press("enter")
	d.key(",")
	d.m.settingsRow = rowProfile
	d.key("enter")
	d.m.profileChoice = "second"
	d.exec(d.m.submitForm())
	d.exec(pending)
	d.expect("Second tenant", "Switched to profile second")
	if strings.Contains(d.screen(), "Laptop Dell") || d.m.mode != modeList {
		t.Fatal("profile switch retained old tenant data")
	}
	d.key(",")
	d.m.settingsRow = rowProfile
	d.key("enter")
	d.m.profileChoice = "new"
	d.exec(d.m.submitForm())
	if d.m.mode != modeLogin || len(d.m.tabs[0].items) != 0 {
		t.Fatal("unauthenticated profile did not reset to login")
	}
}
