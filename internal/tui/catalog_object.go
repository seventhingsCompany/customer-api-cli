package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-cli/internal/duplicate"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

func (m *Model) startObjectFromCatalog(it Item) tea.Cmd {
	m.navigate()
	id := m.res[m.tab].id(it)
	objectsTab := 0
	for i, r := range m.res {
		if r.cli == "objects" {
			objectsTab = i
			break
		}
	}
	return m.prepare(func(ctx context.Context, cl *client.Client) tea.Msg {
		draft, err := duplicate.ObjectFromCatalog(ctx, cl, id)
		if err != nil {
			return doneMsg{err: err}
		}
		return cmdMsg{fn: func() tea.Cmd {
			m.defs[models.AssetTrackingTemplateAsset] = draft.Definitions
			fields := requireNewBarcode(fieldsFromDefinitions(draft.Definitions, models.AssetTrackingTemplateAsset, draft.Fields, false))
			return m.openCustomForm(fmt.Sprintf("New object from catalog %q", displayName(it, id)), fields, func(rf *recordForm) tea.Cmd {
				changes := rf.body(true)
				for _, field := range rf.fields {
					if field.required && field.flag != nil && field.orig == "" {
						changes[field.key] = *field.flag
					}
				}
				body, err := draft.Body(changes)
				if err != nil {
					cmd := m.restoreForm(m.recovery)
					return tea.Batch(cmd, m.fail(err))
				}
				m.operation = "Creating object from catalog…"
				return m.run(func(ctx context.Context, cl *client.Client) tea.Msg {
					newID, err := cl.ObjectCreate(ctx, body)
					return doneMsg{text: "Created object " + newID + " from catalog", err: err, reload: true, reloadTab: &objectsTab}
				})
			})
		}}
	})
}
