package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-cli/internal/duplicate"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

func (m *Model) startDuplicate(r *resource, it Item) tea.Cmd {
	m.navigate()
	id := r.id(it)
	kind := duplicate.Object
	if r.cli == "catalog-items" {
		kind = duplicate.Catalog
	}
	return m.prepare(func(ctx context.Context, cl *client.Client) tea.Msg {
		draft, err := duplicate.Load(ctx, cl, kind, id)
		if err != nil {
			return doneMsg{err: err}
		}
		return cmdMsg{fn: func() tea.Cmd {
			m.defs[r.template] = draft.Definitions
			current := Item(draft.Fields)
			if kind == duplicate.Catalog {
				current = Item{"fields": draft.Fields}
			}
			fields := m.buildFields(r, current)
			if kind == duplicate.Object {
				fields = requireNewBarcode(fields)
			}
			return m.openCustomForm(fmt.Sprintf("Duplicate %s %q", r.singular, displayName(it, id)), fields, func(rf *recordForm) tea.Cmd {
				// Overlay only edits to preserve original types, false and null values.
				body, err := draft.Body(rf.body(true))
				if err != nil {
					cmd := m.restoreForm(m.recovery)
					return tea.Batch(cmd, m.fail(err))
				}
				m.operation = "Duplicating " + r.singular + "…"
				return m.run(func(ctx context.Context, cl *client.Client) tea.Msg {
					newID, err := r.create(ctx, cl, body)
					return doneMsg{text: fmt.Sprintf("Duplicated %s as %s", r.singular, newID), err: err, reload: true}
				})
			})
		}}
	})
}

func requireNewBarcode(fields []*editField) []*editField {
	for _, field := range fields {
		if field.key == "barcode" {
			field.required = true
			return fields
		}
	}
	return append([]*editField{{key: "barcode", label: "New barcode", required: true, kind: models.FieldTypeBarcode}}, fields...)
}
