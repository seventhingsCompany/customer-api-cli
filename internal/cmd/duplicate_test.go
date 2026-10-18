package cmd

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/SeventhingsCompany/customer-api-cli/internal/exitcode"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

func duplicateAPI(t *testing.T, catalog bool) (*routeAPI, string, string, map[string]any) {
	t.Helper()
	api := newRouteAPI(t)
	group, path, prefix := "objects", "object", ""
	if catalog {
		group, path, prefix = "catalog-items", "catalog-item", "catalog_object_"
	}
	defs := []models.FieldDefinition{}
	for key, kind := range map[string]models.FieldTypeName{
		"inventory_name": models.FieldTypeText, "barcode": models.FieldTypeBarcode,
		"custom": models.FieldTypeText, "enabled": models.FieldTypeBoolean,
		"price": models.FieldTypeMoney, "nullable": models.FieldTypeText,
		"actual_room": models.FieldTypeLinkedRoom, "photos": models.FieldTypeAttachment,
		"related": models.FieldTypeLinkedAssets, "computed": models.FieldTypeFieldValueComparison,
		"asset_uuid": models.FieldTypeText, "created_at": models.FieldTypeDatetime,
		"archived_at": models.FieldTypeDatetime, "id": models.FieldTypeNumber,
	} {
		defs = append(defs, models.FieldDefinition{FieldKey: key, FieldType: models.FieldDefinitionFieldType{Name: kind}})
	}
	api.json("GET asset-tracking/asset/field-definitions", 200, defs)
	fields := map[string]any{}
	for key, value := range map[string]any{
		"inventory_name": "Laptop", "barcode": "OLD", "custom": "keep", "enabled": false,
		"price": 12.5, "nullable": nil, "actual_room": 7,
		"photos": []any{map[string]any{"uuid": "f1"}}, "related": []any{"o2"}, "computed": "read only",
		"asset_uuid": "source", "created_at": "yesterday", "archived_at": "today", "id": 9, "unknown": "omit",
	} {
		fields[prefix+key] = value
	}
	var source any = fields
	if catalog {
		source = map[string]any{"uuid": "source", "fields": fields}
	}
	api.json("GET "+path+"/source", 200, source)
	api.routes["POST "+path] = func(w http.ResponseWriter, _ []byte) {
		w.Header().Set("Location-UUID", "copy")
		w.Header().Set("Location", "/customer-api/v1/"+path+"/copy")
		w.WriteHeader(201)
	}
	return api, group, path, map[string]any{
		prefix + "inventory_name": "Laptop", prefix + "custom": "keep", prefix + "enabled": false,
		prefix + "price": 12.5, prefix + "nullable": nil, prefix + "actual_room": float64(7),
	}
}

func TestDuplicateCommands(t *testing.T) {
	for _, catalog := range []bool{false, true} {
		api, group, path, want := duplicateAPI(t, catalog)
		t.Run(group, func(t *testing.T) {
			prefix := ""
			if catalog {
				prefix = "catalog_object_"
			}
			args := []string{group, "duplicate", "source", "--set", prefix + "inventory_name=Copy", "--data", `{"` + prefix + `custom":"override"}`, "--jq", ".uuid", "--raw"}
			if !catalog {
				args = append(args, "--set", "barcode=NEW")
				want["barcode"] = "NEW"
			}
			r := run(t, api.env(t), "", args...)
			if r.code != 0 || r.out != "copy\n" {
				t.Fatalf("duplicate: %+v", r)
			}
			want[prefix+"inventory_name"], want[prefix+"custom"] = "Copy", "override"
			got := api.body("POST " + path)
			if catalog {
				got = got["fields"].(map[string]any)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("body: %v; want %v", got, want)
			}
			if _, sent := api.bodies["PATCH "+path+"/source"]; sent {
				t.Fatal("source was modified")
			}
		})
	}
}

func TestDuplicateDryRun(t *testing.T) {
	for _, catalog := range []bool{false, true} {
		api, group, path, want := duplicateAPI(t, catalog)
		args := []string{group, "duplicate", "source", "--dry-run"}
		if !catalog {
			args = append(args, "--set", "barcode=NEW")
			want["barcode"] = "NEW"
		}
		r := run(t, api.env(t), "", args...)
		if r.code != 0 {
			t.Fatalf("dry run: %+v", r)
		}
		request := r.json(t).(map[string]any)
		body := request["body"].(map[string]any)
		if catalog {
			body = body["fields"].(map[string]any)
		}
		if request["method"] != "POST" || !reflect.DeepEqual(body, want) {
			t.Fatalf("dry run body: %v", request)
		}
		if _, sent := api.bodies["POST "+path]; sent {
			t.Fatal("dry run created a record")
		}
	}
}

func TestDuplicateValidationAndFailures(t *testing.T) {
	for _, catalog := range []bool{false, true} {
		for _, scenario := range []string{"missing-arg", "extra-arg", "bad-json", "read-only", "source-error", "schema-error", "create-error", "missing-barcode", "same-barcode", "null-barcode"} {
			api, group, path, _ := duplicateAPI(t, catalog)
			t.Run(group+"/"+scenario, func(t *testing.T) {
				args := []string{group, "duplicate", "source"}
				if !catalog {
					args = append(args, "--set", "barcode=NEW")
				}
				code := exitcode.Usage
				switch scenario {
				case "missing-arg":
					args = []string{group, "duplicate"}
				case "extra-arg":
					args = append(args, "extra")
				case "bad-json":
					args = append(args, "--data", "{")
				case "read-only":
					args = append(args, "--set", "asset_uuid=bad")
				case "source-error":
					api.json("GET "+path+"/source", 404, nil)
					code = exitcode.NotFound
				case "schema-error":
					api.json("GET asset-tracking/asset/field-definitions", 403, nil)
					code = exitcode.Auth
				case "create-error":
					api.json("POST "+path, 422, map[string]any{"message": "invalid copy"})
					code = exitcode.Invalid
				case "missing-barcode":
					if catalog {
						t.Skip("catalog does not require barcode")
					}
					args = []string{group, "duplicate", "source"}
				case "same-barcode":
					if catalog {
						t.Skip("catalog does not require barcode")
					}
					args = append(args, "--set", "barcode=OLD")
				case "null-barcode":
					if catalog {
						t.Skip("catalog does not require barcode")
					}
					args = append(args, "--set", "barcode:=null")
				}
				r := run(t, api.env(t), "", args...)
				if r.code != code {
					t.Fatalf("got %+v; want code %d", r, code)
				}
				if _, sent := api.bodies["POST "+path]; sent && scenario != "create-error" {
					t.Fatal("failed preparation created a record")
				}
			})
		}
	}
}
