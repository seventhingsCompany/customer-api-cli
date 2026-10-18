package tui

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

type catalogFixture struct {
	mu         sync.Mutex
	items      map[string]Item
	created    Item
	updated    Item
	failCreate bool
}

func newCatalogAPI(t *testing.T) (*fakeAPI, *catalogFixture) {
	f := newFakeAPI(t)
	c := &catalogFixture{items: map[string]Item{
		"c1": {"catalog_object_inventory_name": "Catalog Laptop", "catalog_object_description": "Keep this", "catalog_object_manufacturer": "Example", "id": 1, "uneditable": map[string]any{"keep": true}},
		"c2": {"catalog_object_inventory_name": "Catalog Chair", "id": 2},
	}}
	f.defsExtra = []models.FieldDefinition{
		{FieldKey: "description", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeText}},
		{FieldKey: "manufacturer", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeText}},
	}
	f.extra = map[string]http.HandlerFunc{
		"GET catalog-items": func(w http.ResponseWriter, r *http.Request) {
			c.mu.Lock()
			defer c.mu.Unlock()
			ids := make([]string, 0, len(c.items))
			for id, fields := range c.items {
				if search := r.URL.Query().Get("filter[catalog_object_inventory_name][like][]"); search != "" && !strings.Contains(strings.ToLower(str(fields["catalog_object_inventory_name"])), strings.ToLower(search)) {
					continue
				}
				ids = append(ids, id)
			}
			slices.Sort(ids)
			if r.URL.Query().Get("sort[id]") == "DESC" {
				slices.Reverse(ids)
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
			page, perPage = max(page, 1), max(perPage, 1)
			start, end := min((page-1)*perPage, len(ids)), min(page*perPage, len(ids))
			items := []Item{}
			for _, id := range ids[start:end] {
				items = append(items, Item{"uuid": id, "fields": c.items[id]})
			}
			_ = json.NewEncoder(w).Encode(Item{"items": items, "total": len(ids), "page": page, "per_page": perPage, "sort": []any{}})
		},
		"POST catalog-item": func(w http.ResponseWriter, r *http.Request) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.failCreate {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message":"Please correct the catalog item"}`))
				return
			}
			if err := json.NewDecoder(r.Body).Decode(&c.created); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			c.items["c3"] = c.created["fields"].(map[string]any)
			w.Header().Set("Location-UUID", "c3")
			w.WriteHeader(http.StatusCreated)
		},
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		f.extra["GET catalog-item/"+id] = func(w http.ResponseWriter, _ *http.Request) {
			c.mu.Lock()
			defer c.mu.Unlock()
			fields, ok := c.items[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(Item{"uuid": id, "fields": fields})
		}
		f.extra["PUT catalog-item/"+id] = func(w http.ResponseWriter, r *http.Request) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.updated = nil
			if err := json.NewDecoder(r.Body).Decode(&c.updated); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			c.items[id] = c.updated["fields"].(map[string]any)
			w.WriteHeader(http.StatusNoContent)
		}
		f.extra["DELETE catalog-item/"+id] = func(w http.ResponseWriter, _ *http.Request) {
			c.mu.Lock()
			defer c.mu.Unlock()
			delete(c.items, id)
			w.WriteHeader(http.StatusNoContent)
		}
	}
	return f, c
}

func finishCatalogForm(d *driver) {
	d.t.Helper()
	for range len(d.m.record.fields) {
		d.key("enter")
	}
}

func catalogQuerySent(f *fakeAPI, key, value string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if raw, ok := strings.CutPrefix(call, "GET catalog-items?"); ok {
			raw, _, _ = strings.Cut(raw, " ")
			query, _ := url.ParseQuery(raw)
			if query.Get(key) == value {
				return true
			}
		}
	}
	return false
}

func TestCatalogBrowseNavigationAndCopy(t *testing.T) {
	f, _ := newCatalogAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, settings: Settings{PageSize: 1}})
	d.expect("0 Catalog")
	d.key("0")
	d.expect("Catalog Laptop", "Example", "page 1 of 2")
	d.key("y")
	d.expect("Copied c1")
	d.key("Y")
	d.expect("catalog-items list --per-page 1")
	d.key("j")
	d.expect("Catalog Chair", "page 2 of 2")
	d.key("k", "enter")
	d.expect("Catalog Laptop", "Keep this", "esc back")
	if !f.called("GET catalog-item/c1?") {
		t.Fatal("catalog details were not fetched")
	}
	d.key("Y")
	d.expect("catalog-items get c1")
	d.key("esc", "9", "tab")
	if d.m.res[d.m.tab].cli != "catalog-items" {
		t.Fatal("tab after CHub must open Catalog")
	}
	d.key("tab")
	if d.m.mode != modeSettings {
		t.Fatal("Settings must remain the last tab")
	}
	d.key("C")
	d.expect("Catalog Laptop")
	d.key(",", "0")
	d.expect("Catalog Laptop", "0 Catalog")
	if d.m.mode != modeList || d.m.res[d.m.tab].cli != "catalog-items" {
		t.Fatal("0 from Settings must open Catalog")
	}
	d.key(",", "left")
	if d.m.res[d.m.tab].cli != "catalog-items" {
		t.Fatal("left from Settings must open Catalog")
	}
	seen := map[string]bool{}
	for _, i := range d.m.paneTabs() {
		key := d.m.tabKey(i)
		if seen[key] {
			t.Fatalf("duplicate tab shortcut %s", key)
		}
		seen[key] = true
	}
}

func TestCatalogSearchFilterSortAndSmallScreen(t *testing.T) {
	f, _ := newCatalogAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true, settings: Settings{PageSize: 1}})
	d.key("C", "s", "Chair", "enter")
	d.expect("Catalog Chair", "page 1 of 1")
	if !catalogQuerySent(f, "filter[catalog_object_inventory_name][like][]", "Chair") {
		t.Fatal("catalog search did not use the prefixed name key")
	}
	d.key("esc", "f", "catalog_object_inventory_group = IT", "enter", "-id", "enter")
	d.expect("Catalog Chair")
	if !catalogQuerySent(f, "filter[catalog_object_inventory_group][eq]", "IT") || !catalogQuerySent(f, "sort[id]", "DESC") {
		t.Fatal("catalog filter was not sent")
	}
	st := d.m.tabs[d.m.tab]
	if len(st.sorts) != 1 || st.sorts[0].String() != "-id" {
		t.Fatal("catalog sort was not retained")
	}
	d.send(tea.WindowSizeMsg{Width: 40, Height: 24})
	d.expect("0 Catalog", "Name")
	if displayName(d.m.selected(), "") != "Catalog Chair" {
		t.Fatal("resizing lost the catalog selection")
	}
	d.key("n")
	d.expect("New catalog item", "Name *")
	d.key("esc")
	if d.m.mode != modeList {
		t.Fatal("catalog form did not cancel")
	}
}

func TestCatalogCreateEditClearAndDelete(t *testing.T) {
	f, c := newCatalogAPI(t)
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("C")
	c.mu.Lock()
	c.items["c1"]["catalog_object_description"] = "Server changed"
	c.mu.Unlock()
	d.key("e")
	d.expect(`Edit catalog item "Catalog Laptop"`, "Name *")
	d.key(" Updated")
	finishCatalogForm(d)
	d.expect("Updated catalog item (1 field(s))")
	c.mu.Lock()
	updated := maps.Clone(c.items["c1"])
	c.mu.Unlock()
	if updated["catalog_object_inventory_name"] != "Catalog Laptop Updated" || updated["catalog_object_description"] != "Server changed" || updated["uneditable"].(map[string]any)["keep"] != true {
		t.Fatalf("catalog update lost fields: %v", updated)
	}
	d.key("e", "enter") // description follows Name
	for range len("Server changed") {
		d.key("backspace")
	}
	d.key("enter", "enter")
	d.expect("Updated catalog item (1 field(s))")
	c.mu.Lock()
	cleared := c.items["c1"]["catalog_object_description"] == nil
	c.mu.Unlock()
	if !cleared {
		t.Fatal("clearing a catalog field did not send null")
	}
	d.key("n", "enter")
	d.expect("Name is required")
	d.key("New catalog")
	finishCatalogForm(d)
	d.expect("Created catalog item c3")
	c.mu.Lock()
	created := maps.Clone(c.created)
	c.mu.Unlock()
	if !reflect.DeepEqual(created, Item{"fields": map[string]any{"catalog_object_inventory_name": "New catalog"}}) {
		t.Fatalf("create must wrap the catalog field map, without object mandatory fields: %v", created)
	}
	d.key("d", "n")
	d.expect("Cancelled")
	if f.called("DELETE catalog-item/") {
		t.Fatal("canceled delete sent a request")
	}
	d.key("d", "y")
	d.expect("Deleted catalog item c1")
	if !f.called("DELETE catalog-item/c1?") || f.called("DELETE object/") {
		t.Fatal("catalog deletion used the wrong endpoint")
	}
}

func TestCatalogFailedCreateRetainsInputForRetry(t *testing.T) {
	f, c := newCatalogAPI(t)
	c.failCreate = true
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("C", "n", "Retry catalog")
	finishCatalogForm(d)
	d.expect("Please correct the catalog item", "New catalog item", "Retry catalog")
	if countCalls(f, "POST catalog-item?") != 1 {
		t.Fatal("failed create was retried automatically")
	}
	c.mu.Lock()
	c.failCreate = false
	c.mu.Unlock()
	finishCatalogForm(d)
	d.expect("Created catalog item c3")
}

func TestCatalogUnavailableDoesNotBreakOtherTabs(t *testing.T) {
	f, _ := newCatalogAPI(t)
	f.extra["GET catalog-items"] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Product catalog is not enabled"}`))
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("C")
	d.expect("Product catalog is not enabled")
	d.key("1")
	d.expect("Laptop Dell")
}

func TestCatalogEmptyStateAndPermissionDeniedSubmission(t *testing.T) {
	f, c := newCatalogAPI(t)
	clear(c.items)
	f.extra["POST catalog-item"] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Catalog create permission required"}`))
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("C")
	d.expect("No catalog yet. Press n to create one.")
	d.key("n", "Keep this name")
	finishCatalogForm(d)
	d.expect("Catalog create permission required", "New catalog item", "Keep this name")
	if d.m.mode != modeForm {
		t.Fatal("permission denial must preserve the form, not open login")
	}
	d.key("esc", "1")
	d.expect("Laptop Dell")
}

func TestCatalogUnauthorizedStillOpensLogin(t *testing.T) {
	f, _ := newCatalogAPI(t)
	f.extra["GET catalog-items"] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}
	d := newDriver(t, &fakeDeps{url: f.srv.URL, loggedIn: true})
	d.key("C")
	d.expect("Log in to seventhings")
}

func TestCatalogFailedReadPreventsReplacement(t *testing.T) {
	f, _ := newCatalogAPI(t)
	f.extra["GET catalog-item/c1"] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}
	cl := client.NewWithToken(f.srv.URL, "t", client.WithRateLimit(0))
	err := catalogResource().update(t.Context(), cl, "c1", Item{"catalog_object_inventory_name": "must not be sent"})
	if err == nil || f.called("PUT catalog-item/") {
		t.Fatal("failed read must prevent the replacement request")
	}
}

func TestCatalogFormSchema(t *testing.T) {
	defs := []models.FieldDefinition{
		{FieldKey: "barcode", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeBarcode}, Attributes: []models.FieldAttribute{{Type: "mandatory", Value: "yes"}}},
		{FieldKey: "asset_uuid", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeText}},
		{FieldKey: "inventory_name", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeText}},
		{FieldKey: "custom", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeText}},
		{FieldKey: "actual_room", FieldType: models.FieldDefinitionFieldType{Name: models.FieldTypeLinkedRoom}},
	}
	current := Item{"uuid": "c1", "fields": Item{"catalog_object_inventory_name": "Laptop", "catalog_object_custom": "keep", "catalog_object_actual_room": 7}}
	fields := catalogFields(defs, current, false)
	for _, field := range fields {
		key, prefixed := strings.CutPrefix(field.key, catalogPrefix)
		if !prefixed || key == "asset_uuid" || key == "barcode" {
			t.Fatalf("invalid catalog field: %+v", field)
		}
	}
	if fields[0].key != catalogPrefix+"inventory_name" || fields[0].orig != "Laptop" || !fields[0].required {
		t.Fatal("catalog name was not first, required and prefilled")
	}
	if len(fields) != 3 || fields[2].source != srcRoomID || fields[2].orig != "7" {
		t.Fatalf("custom/linked catalog fields not preserved: %v", fields)
	}
	all := catalogFields(defs, nil, true)
	for _, field := range all {
		if strings.TrimPrefix(field.key, catalogPrefix) == "barcode" && field.required {
			t.Fatal("object barcode requirement leaked into catalog")
		}
	}
	if !defs[0].IsMandatory() {
		t.Fatal("catalog form mutated the cached object schema")
	}
	if got := catalogFields(nil, nil, false); len(got) != 1 || got[0].label != "Name" {
		t.Fatalf("missing fallback Name field: %v", got)
	}
}
