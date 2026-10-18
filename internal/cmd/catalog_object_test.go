package cmd

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
)

func TestObjectCreateFromCatalog(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		api, _, _, _ := duplicateAPI(t, true)
		api.routes["POST object"] = func(w http.ResponseWriter, _ []byte) {
			w.Header().Set("Location", "/customer-api/v1/object/new-object")
			w.WriteHeader(201)
		}
		args := []string{"objects", "create", "--from-catalog", "source", "--set", "barcode=NEW", "--set", "inventory_name=My laptop", "--data", `{"custom":"override"}`}
		if dryRun {
			args = append(args, "--dry-run")
		}
		r := run(t, api.env(t), "", args...)
		if r.code != 0 {
			t.Fatalf("create from catalog: %+v", r)
		}
		body := api.body("POST object")
		if dryRun {
			if _, sent := api.bodies["POST object"]; sent {
				t.Fatal("dry run created an object")
			}
			request := r.json(t).(map[string]any)
			if request["method"] != "POST" {
				t.Fatalf("dry run request: %v", request)
			}
			body = request["body"].(map[string]any)
		} else if r.json(t).(map[string]any)["uuid"] != "new-object" {
			t.Fatalf("new UUID not returned: %+v", r)
		}
		want := map[string]any{"barcode": "NEW", "inventory_name": "My laptop", "custom": "override", "enabled": false, "nullable": nil, "price": 12.5, "actual_room": float64(7)}
		if !reflect.DeepEqual(body, want) {
			t.Fatalf("body: %v; want %v", body, want)
		}
		if _, changed := api.bodies["PUT catalog-item/source"]; changed {
			t.Fatal("source catalog item was changed")
		}
	}
}

func TestObjectCreateFromCatalogFailures(t *testing.T) {
	for _, scenario := range []string{"no-barcode", "same-barcode", "null-barcode", "empty-catalog", "read-only", "source", "schema", "create"} {
		t.Run(scenario, func(t *testing.T) {
			api, _, _, _ := duplicateAPI(t, true)
			args := []string{"objects", "create", "--from-catalog", "source", "--set", "barcode=NEW"}
			code := exitcode.Usage
			switch scenario {
			case "empty-catalog":
				args[3] = ""
			case "no-barcode":
				args = args[:4]
			case "same-barcode":
				args = append(args, "--set", "barcode=OLD")
			case "null-barcode":
				args = append(args, "--set", "barcode:=null")
			case "read-only":
				args = append(args, "--set", "asset_uuid=source")
			case "source":
				api.json("GET catalog-item/source", 404, nil)
				code = exitcode.NotFound
			case "schema":
				api.json("GET asset-tracking/asset/field-definitions", 403, nil)
				code = exitcode.Auth
			case "create":
				api.json("POST object", 422, map[string]any{"message": "invalid object"})
				code = exitcode.Invalid
			}
			r := run(t, api.env(t), "", args...)
			if r.code != code {
				t.Fatalf("got %+v; want %d", r, code)
			}
			if _, sent := api.bodies["POST object"]; sent && scenario != "create" {
				t.Fatal("invalid preparation created an object")
			}
		})
	}
}
