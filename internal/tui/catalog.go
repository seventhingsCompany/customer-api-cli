package tui

import (
	"context"
	"maps"
	"strings"

	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

const catalogPrefix = "catalog_object_"

func catalogResource() *resource {
	return &resource{
		title: "Catalog", cli: "catalog-items", shortcut: "0", singular: "catalog item", idKey: "uuid",
		searchKey: catalogPrefix + "inventory_name", filterable: true, sort: sortMany,
		template: models.AssetTrackingTemplateAsset, fields: catalogFields,
		columns: []column{
			{"Name", "fields.catalog_object_inventory_name", 4},
			{"Manufacturer", "fields.catalog_object_manufacturer", 3},
			{"Group", "fields.catalog_object_inventory_group", 2},
			{"ID", "fields.id", 1},
		},
		load: func(ctx context.Context, cl *client.Client, q query) ([]Item, int, error) {
			page, err := cl.CatalogItemsList(ctx, serverSearch(q))
			if err != nil {
				return nil, 0, err
			}
			items, err := toItems(page.Items)
			return items, page.Total, err
		},
		get: func(ctx context.Context, cl *client.Client, id string) (Item, error) {
			item, err := cl.CatalogItemGet(ctx, id)
			if err != nil {
				return nil, err
			}
			return toItem(item)
		},
		create: func(ctx context.Context, cl *client.Client, body Item) (string, error) {
			return cl.CatalogItemCreate(ctx, body)
		},
		update: func(ctx context.Context, cl *client.Client, id string, body Item) error {
			// PUT replaces the field map. Read afresh, rather than merging a stale
			// list/detail snapshot, and retain fields the form cannot edit.
			current, err := cl.CatalogItemGet(ctx, id)
			if err != nil {
				return err
			}
			fields := maps.Clone(current.Fields)
			if fields == nil {
				fields = models.Fields{}
			}
			maps.Copy(fields, body)
			return cl.CatalogItemUpdate(ctx, id, fields)
		},
		del: func(ctx context.Context, cl *client.Client, id string) error {
			return cl.CatalogItemDelete(ctx, id)
		},
	}
}

// Catalog master data shares the object schema, but object-only mandatory
// constraints (notably barcode) do not apply. Translate editable object keys to
// their catalog namespace without changing the cached object definitions.
func catalogFields(defs []models.FieldDefinition, current Item, all bool) []*editField {
	values := Item{}
	if fields, ok := current["fields"].(map[string]any); ok {
		for key, value := range fields {
			if field, ok := strings.CutPrefix(key, catalogPrefix); ok {
				values[field] = value
			}
		}
	}
	catalogDefs := make([]models.FieldDefinition, 0, len(defs))
	for _, def := range defs {
		def.Attributes = nil
		catalogDefs = append(catalogDefs, def)
	}
	fields := fieldsFromDefinitions(catalogDefs, models.AssetTrackingTemplateAsset, values, all)
	var name *editField
	var rest []*editField
	for _, field := range fields {
		if field.key == "inventory_name" {
			field.label, field.required = "Name", true
			name = field
		} else {
			rest = append(rest, field)
		}
		field.key = catalogPrefix + field.key
	}
	if name == nil {
		name = &editField{key: catalogPrefix + "inventory_name", label: "Name", required: true, kind: models.FieldTypeText, orig: str(values["inventory_name"])}
	}
	return append([]*editField{name}, rest...)
}
