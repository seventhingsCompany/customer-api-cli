package tui

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/charmbracelet/x/ansi"
)

func setFormValue(t *testing.T, d *driver, key, value string) {
	t.Helper()
	for _, field := range d.m.record.fields {
		if field.key == key {
			*field.text = value
			return
		}
	}
	t.Fatalf("form has no %s field", key)
}

func TestCatalogCreateObjectReviewAndSubmit(t *testing.T) {
	f, c := newCatalogAPI(t)
	f.defsExtra = append(f.defsExtra,
		models.FieldDefinition{FieldKey: "serial_number", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeText}, Attributes: []models.FieldAttribute{{Type: "mandatory", Value: "yes"}}},
		models.FieldDefinition{FieldKey: "enabled", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeBoolean}},
		models.FieldDefinition{FieldKey: "price", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeMoney}},
		models.FieldDefinition{FieldKey: "inspected", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeBoolean}, Attributes: []models.FieldAttribute{{Type: "mandatory", Value: "yes"}}},
	)
	c.items["c1"][catalogPrefix+"enabled"] = false
	c.items["c1"][catalogPrefix+"price"] = 12.5
	c.items["c1"][catalogPrefix+"barcode"] = "TEMPLATE"
	var created Item
	f.extra["POST object"] = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&created)
		w.Header().Set("Location", "/customer-api/v1/object/new-object")
		w.WriteHeader(201)
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("0")
	d.expect("b create object")
	c.mu.Lock()
	c.items["c1"][catalogPrefix+"inventory_name"] = "Fresh catalog name"
	c.mu.Unlock()
	d.key("b")
	d.expect("New object from catalog", "barcode *", "serial_number *")
	if d.m.record.value("inventory_name") != "Fresh catalog name" || d.m.record.value("barcode") != "" {
		t.Fatal("form used a stale list record or inherited the barcode")
	}
	if f.called("POST object?") {
		t.Fatal("object created before review")
	}
	setFormValue(t, d, "barcode", "NEW")
	d.exec(d.m.submitForm())
	d.expect("serial_number is required")
	setFormValue(t, d, "serial_number", "SN-NEW")
	setFormValue(t, d, "barcode", "TEMPLATE")
	d.exec(d.m.submitForm())
	d.expect("different from the source")
	setFormValue(t, d, "barcode", "NEW")
	setFormValue(t, d, "description", "")
	d.exec(d.m.submitForm())
	d.expect("Created object new-object from catalog")
	want := Item{"inventory_name": "Fresh catalog name", "description": nil, "manufacturer": "Example", "barcode": "NEW", "serial_number": "SN-NEW", "enabled": false, "price": 12.5, "inspected": false}
	if !reflect.DeepEqual(created, want) {
		t.Fatalf("created: %v; want %v", created, want)
	}
	if d.m.res[d.m.tab].cli != "catalog-items" || d.m.mode != modeList {
		t.Fatal("creation should retain the catalog context")
	}
	if d.m.tabs[0].loaded {
		t.Fatal("new object did not invalidate the Objects list")
	}
	if f.called("POST catalog-item?") || f.called("PUT catalog-item/") {
		t.Fatal("creation wrote to the catalog")
	}
}

func TestCatalogCreateObjectCancelFailureRetryAndGuards(t *testing.T) {
	f, _ := newCatalogAPI(t)
	fail := true
	f.extra["POST object"] = func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"message":"Correct the object"}`))
			return
		}
		w.Header().Set("Location", "/customer-api/v1/object/new-object")
		w.WriteHeader(201)
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("0", "enter", "b", "esc")
	if d.m.mode != modeDetail || f.called("POST object?") {
		t.Fatal("cancel changed the view or created an object")
	}
	d.key("b")
	setFormValue(t, d, "barcode", "NEW")
	d.exec(d.m.submitForm())
	d.expect("Correct the object", "New object from catalog")
	if d.m.mode != modeForm || d.m.record.value("barcode") != "NEW" || countCalls(f, "POST object?") != 1 {
		t.Fatal("failed create lost input or retried automatically")
	}
	fail = false
	d.exec(d.m.submitForm())
	d.expect("Created object new-object from catalog")
	d.key("esc")
	d.m.writing = 1
	if cmd := d.press("b"); cmd != nil {
		t.Fatal("creation allowed during another write")
	}
	d.m.writing = 0
	prepare := d.press("b")
	d.key("2")
	d.exec(prepare)
	if d.m.mode != modeList || d.m.res[d.m.tab].cli != "rooms" {
		t.Fatal("stale catalog preparation replaced Rooms")
	}
	d.key("b")
	if d.m.mode != modeList {
		t.Fatal("create-from-catalog action enabled on another resource")
	}
}

func TestCatalogCreateObjectReadFailure(t *testing.T) {
	f, _ := newCatalogAPI(t)
	f.extra["GET catalog-item/c1"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("0", "b")
	if d.m.mode != modeList || f.called("POST object?") {
		t.Fatal("failed source read allowed object creation")
	}
}

func TestCatalogObjectFormFitsScreen(t *testing.T) {
	f, _ := newCatalogAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	for _, size := range [][2]int{{40, 16}, {40, 24}, {144, 36}} {
		d.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		d.key("0", "b")
		if d.m.mode != modeForm {
			t.Fatal("object form did not open")
		}
		screen := d.screen()
		if len(strings.Split(screen, "\n")) > size[1] {
			t.Fatalf("%v: object form exceeds height:\n%s", size, screen)
		}
		for _, line := range strings.Split(screen, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("%v: object form exceeds width: %q", size, line)
			}
		}
		d.key("esc")
	}
}
