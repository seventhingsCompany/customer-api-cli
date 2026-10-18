// Package duplicate prepares safe create bodies from existing records.
package duplicate

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

type Kind string

const (
	Object        Kind = "object"
	Catalog       Kind = "catalog item"
	CatalogPrefix      = "catalog_object_"
)

type Draft struct {
	Fields      map[string]any
	Definitions []models.FieldDefinition
	kind        Kind
	barcode     string
}

// ReadOnly reports server-managed keys, including their catalog equivalents.
func ReadOnly(key string) bool {
	key = strings.TrimPrefix(key, CatalogPrefix)
	if models.SystemManagedFieldKeys[key] {
		return true
	}
	switch key {
	case "asset_uuid", "room_uuid", "location_uuid", "created_by_user_id",
		"deleted", "locked_by", "archived_at", "scan_time", "scanned_by_user_id",
		"asset_like_counter", "inventory_time", "inventory_user":
		return true
	}
	return false
}

// Load reads the full source and schema. Only writable scalar fields are copied;
// attachment, relationship-list and calculated read values are not create input.
func Load(ctx context.Context, cl *client.Client, kind Kind, id string) (*Draft, error) {
	var source map[string]any
	var err error
	if kind == Catalog {
		item, getErr := cl.CatalogItemGet(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		source = item.Fields
	} else {
		source, err = cl.ObjectGet(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	defs, err := cl.FieldDefinitionsList(ctx, models.AssetTrackingTemplateAsset)
	if err != nil {
		return nil, err
	}
	d := &Draft{Fields: map[string]any{}, Definitions: defs, kind: kind}
	d.barcode, _ = source["barcode"].(string)
	if kind == Catalog {
		d.barcode, _ = source[CatalogPrefix+"barcode"].(string)
	}
	for _, def := range defs {
		if ReadOnly(def.FieldKey) || def.FieldKey == "barcode" || !copyable(def.FieldType.Name) {
			continue
		}
		key := def.FieldKey
		if kind == Catalog {
			key = CatalogPrefix + key
		}
		if value, ok := source[key]; ok {
			d.Fields[key] = value
		}
	}
	return d, nil
}

// ObjectFromCatalog translates catalog master data to a new object's fields.
// It uses the same safe field policy as duplication and never writes the catalog.
func ObjectFromCatalog(ctx context.Context, cl *client.Client, id string) (*Draft, error) {
	draft, err := Load(ctx, cl, Catalog, id)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]any, len(draft.Fields))
	for key, value := range draft.Fields {
		if objectKey, ok := strings.CutPrefix(key, CatalogPrefix); ok {
			fields[objectKey] = value
		}
	}
	draft.Fields, draft.kind = fields, Object
	return draft, nil
}

func copyable(kind models.FieldTypeName) bool {
	switch kind {
	case models.FieldTypeText, models.FieldTypeLongText, models.FieldTypeLink,
		models.FieldTypeNumber, models.FieldTypeDecimal, models.FieldTypeMoney,
		models.FieldTypeDate, models.FieldTypeDatetime, models.FieldTypeDropdown,
		models.FieldTypeBoolean, models.FieldTypeLinkedLocation, models.FieldTypeLinkedRoom,
		models.FieldTypeLinkedPerson, models.FieldTypeLinkedUser:
		return true
	}
	return false
}

// Body overlays explicit changes without changing the source draft. Barcodes
// are never inherited, and an object needs a distinct, nonempty primary barcode.
func (d *Draft) Body(changes map[string]any) (map[string]any, error) {
	for key := range changes {
		if ReadOnly(key) {
			return nil, fmt.Errorf("%s is managed by the server and cannot be duplicated", key)
		}
	}
	body := maps.Clone(d.Fields)
	maps.Copy(body, changes)
	if d.kind == Object {
		barcode, _ := body["barcode"].(string)
		if strings.TrimSpace(barcode) == "" || strings.TrimSpace(barcode) == strings.TrimSpace(d.barcode) {
			return nil, fmt.Errorf("a new barcode different from the source is required")
		}
	}
	return body, nil
}
