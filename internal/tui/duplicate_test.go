package tui

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

func TestDuplicateObjectReviewAndCreate(t *testing.T) {
	f := newFakeAPI(t)
	f.defsExtra = []models.FieldDefinition{
		{FieldKey: "enabled", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeBoolean}},
		{FieldKey: "nullable", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeText}},
	}
	var created Item
	f.extra = map[string]http.HandlerFunc{
		"GET object/o1": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(Item{"asset_uuid": "o1", "inventory_name": "Fresh name", "barcode": "BC-1", "enabled": false, "nullable": nil, "created_at": "yesterday"})
		},
		"POST object": func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.Header().Set("Location", "/customer-api/v1/object/o3")
			w.WriteHeader(201)
		},
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.expect("u duplicate")
	d.key("u")
	d.expect(`Duplicate object "Laptop Dell"`, "Fresh name", "barcode *")
	if d.m.record.value("barcode") != "" || f.called("POST object?") {
		t.Fatal("copy created before review or barcode inherited")
	}
	d.exec(d.m.submitForm())
	d.expect("required")
	for _, field := range d.m.record.fields {
		if field.key == "barcode" {
			*field.text = "BC-1"
		}
	}
	d.exec(d.m.submitForm())
	d.expect("different from the source")
	if d.m.mode != modeForm || f.called("POST object?") {
		t.Fatal("invalid barcode created a copy or lost the form")
	}
	for _, field := range d.m.record.fields {
		if field.key == "barcode" {
			*field.text = "BC-NEW"
		}
	}
	d.exec(d.m.submitForm())
	d.expect("Duplicated object as o3")
	want := Item{"inventory_name": "Fresh name", "barcode": "BC-NEW", "enabled": false, "nullable": nil}
	if !reflect.DeepEqual(created, want) {
		t.Fatalf("created: %v; want %v", created, want)
	}
	if f.called("PATCH object/") {
		t.Fatal("source was changed")
	}
}

func TestDuplicateCatalogCancelRetryAndDetail(t *testing.T) {
	f, c := newCatalogAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("C", "enter", "u")
	d.expect(`Duplicate catalog item "Catalog Laptop"`, "Name *")
	if d.m.record.value("catalog_object_inventory_name") != "Catalog Laptop" {
		t.Fatal("name not prefilled")
	}
	d.key("esc")
	if d.m.mode != modeDetail || f.called("POST catalog-item?") {
		t.Fatal("cancel did not return to detail without writing")
	}
	c.mu.Lock()
	c.failCreate = true
	c.mu.Unlock()
	d.key("u")
	for _, field := range d.m.record.fields {
		if field.key == "catalog_object_inventory_name" {
			*field.text = "Copy"
		}
		if field.key == "catalog_object_description" {
			*field.text = ""
		}
	}
	d.exec(d.m.submitForm())
	d.expect("Please correct the catalog item", "Duplicate catalog item", "Copy")
	if d.m.mode != modeForm || countCalls(f, "POST catalog-item?") != 1 {
		t.Fatal("failed copy lost form or retried automatically")
	}
	c.mu.Lock()
	c.failCreate = false
	c.mu.Unlock()
	d.exec(d.m.submitForm())
	d.expect("Duplicated catalog item as c3")
	c.mu.Lock()
	defer c.mu.Unlock()
	want := Item{"fields": map[string]any{"catalog_object_inventory_name": "Copy", "catalog_object_description": nil, "catalog_object_manufacturer": "Example"}}
	if !reflect.DeepEqual(c.created, want) {
		t.Fatalf("created: %v", c.created)
	}
	if c.items["c1"]["catalog_object_inventory_name"] != "Catalog Laptop" || c.items["c1"]["catalog_object_description"] != "Keep this" {
		t.Fatal("source changed")
	}
}

func TestDuplicatePreparationAndWriteGuards(t *testing.T) {
	f := newFakeAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	cmd := d.press("u")
	d.key("2")
	d.exec(cmd)
	if d.m.mode != modeList || d.m.tab != 1 {
		t.Fatal("stale copy form replaced Rooms")
	}
	d.key("u")
	if d.m.mode != modeList {
		t.Fatal("unsupported resource allowed duplication")
	}
	d.key("1")
	d.m.writing = 1
	if cmd := d.press("u"); cmd != nil {
		t.Fatal("duplication allowed during a write")
	}
	d.expect("already in progress")
	d.m.writing = 0
	f.extra = map[string]http.HandlerFunc{"GET object/o1": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }}
	d.key("u")
	if d.m.mode == modeForm || f.called("POST object?") {
		t.Fatal("failed read allowed duplication")
	}
}

func TestDuplicateFormFitsSmallTerminal(t *testing.T) {
	f, _ := newCatalogAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	for _, tab := range []string{"1", "C"} {
		for _, size := range [][2]int{{40, 16}, {80, 24}, {120, 30}} {
			d.key(tab)
			d.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			d.key("u")
			if d.m.mode != modeForm {
				t.Fatal("copy form did not open")
			}
			if len(strings.Split(d.screen(), "\n")) > size[1] {
				t.Fatalf("copy form exceeds height at %v:\n%s", size, d.screen())
			}
			d.key("esc")
		}
	}
}
