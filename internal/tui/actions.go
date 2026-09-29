package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

// attachmentFields returns the attachment field keys of a template.
func attachmentFields(defs []models.FieldDefinition) [][2]string {
	var out [][2]string
	for _, d := range defs {
		if d.FieldType.Name == models.FieldTypeAttachment {
			out = append(out, [2]string{fieldLabel(d), d.FieldKey})
		}
	}
	return out
}

// startAttach uploads a local file and attaches it to a file field.
func (m *Model) startAttach(r *resource, it Item) tea.Cmd {
	m.navigate()
	id := r.id(it)
	return m.withDefs(r.template, func() tea.Cmd {
		fieldChoices := attachmentFields(m.defs[r.template])
		if len(fieldChoices) == 0 {
			m.setStatus("This template has no file fields", true)
			return nil
		}
		fields := []*editField{
			{key: "field", label: "File field", required: true, kind: models.FieldTypeText, choices: fieldChoices},
			{key: "path", label: "File to upload (path)", required: true, kind: kindPath},
		}
		title := fmt.Sprintf("Attach a file to %q", displayName(it, id))
		return m.openCustomForm(title, fields, func(rf *recordForm) tea.Cmd {
			field, path := rf.value("field"), expandHome(rf.value("path"))
			return m.run(func(ctx context.Context, cl *client.Client) tea.Msg {
				fileUUID, err := uploadFile(ctx, cl, path)
				if err != nil {
					return doneMsg{err: fmt.Errorf("upload: %w", err)}
				}
				resp, err := r.attach(ctx, cl, id, []models.FileAttachment{{FieldKey: field, FileUUID: fileUUID}}, false)
				return attachResult(resp, err, fmt.Sprintf("Attached %s to %s", filepath.Base(path), field))
			})
		})
	})
}

// startDetach removes one attached file after confirmation.
func (m *Model) startDetach(r *resource, it Item) tea.Cmd {
	m.navigate()
	id := r.id(it)
	return m.withDefs(r.template, func() tea.Cmd {
		var choices [][2]string
		names := map[string]string{}
		for _, fc := range attachmentFields(m.defs[r.template]) {
			files, _ := it[fc[1]].([]any)
			for _, raw := range files {
				f, _ := raw.(map[string]any)
				if uuid := str(f["uuid"]); uuid != "" {
					label := fmt.Sprintf("%s: %s", fc[1], firstNonEmpty(str(f["name"]), uuid))
					choices = append(choices, [2]string{label, fc[1] + "|" + uuid})
					names[fc[1]+"|"+uuid] = label
				}
			}
		}
		if len(choices) == 0 {
			m.setStatus("No attached files", false)
			return nil
		}
		fields := []*editField{{key: "file", label: "File to remove", required: true, kind: models.FieldTypeText, choices: choices}}
		return m.openCustomForm(fmt.Sprintf("Remove a file from %q", displayName(it, id)), fields, func(rf *recordForm) tea.Cmd {
			choice := rf.value("file")
			field, fileUUID, _ := strings.Cut(choice, "|")
			m.ask(fmt.Sprintf("Remove %s? The file stays in Files. [y/N]", names[choice]), func() tea.Cmd {
				return m.run(func(ctx context.Context, cl *client.Client) tea.Msg {
					resp, err := r.attach(ctx, cl, id, []models.FileAttachment{{FieldKey: field, FileUUID: fileUUID}}, true)
					return attachResult(resp, err, "Removed "+names[choice])
				})
			})
			return nil
		})
	})
}

// attachResult reports 200 and 207 (partial) responses.
func attachResult(resp *client.Response, err error, ok string) tea.Msg {
	if err != nil {
		return doneMsg{err: err}
	}
	if resp.StatusCode == http.StatusMultiStatus {
		return doneMsg{err: fmt.Errorf("partially failed (HTTP 207): %s", strings.Join(strings.Fields(string(resp.Body)), " ")), reload: true}
	}
	return doneMsg{text: ok, reload: true}
}

// startHubOffer offers an object on the circularity hub. Category and price
// are prefilled from the API's suggestions and must be confirmed.
func (m *Model) startHubOffer(r *resource, it Item) tea.Cmd {
	m.navigate()
	id := r.id(it)
	name := displayName(it, id)
	return m.prepare(func(ctx context.Context, cl *client.Client) tea.Msg {
		cat, price := hubSuggestions(ctx, cl, id)
		return cmdMsg{fn: func() tea.Cmd {
			fields := []*editField{
				{key: "category", label: "Category", required: true, kind: models.FieldTypeText, orig: cat},
				{key: "price", label: "Price", required: true, kind: models.FieldTypeDecimal, orig: price},
			}
			return m.openCustomForm(fmt.Sprintf("Offer %q on the circularity hub", name), fields, func(rf *recordForm) tea.Cmd {
				category, price := rf.value("category"), strings.ReplaceAll(rf.value("price"), ",", ".")
				m.ask(fmt.Sprintf("Offer %q on the circularity hub as %q for %s? This publishes a resale listing. [y/N]",
					name, category, price), func() tea.Cmd {
					return m.run(func(ctx context.Context, cl *client.Client) tea.Msg {
						err := cl.CircularityHubAddObjects(ctx, map[string]models.AddObjectEntry{id: {Category: category, Price: price}})
						return doneMsg{text: fmt.Sprintf("Offered %q on the circularity hub", name), err: err}
					})
				})
				return nil
			})
		}}
	})
}

// hubSuggestions asks the API for a category and price. Failures leave the
// fields empty for the user to fill in.
func hubSuggestions(ctx context.Context, cl *client.Client, uuid string) (category, price string) {
	cats, err := cl.CircularityHubSuggestCategory(ctx, models.FilterObject{
		Filter: map[string]map[models.FilterOperator]any{"asset_uuid": {models.FilterIn: []string{uuid}}},
	})
	if err == nil {
		category = cats[uuid]
	}
	// Not CircularityHubSuggestRestPrice: the SDK expects string prices but the
	// API returns numbers (customer-api-go v1.4.0).
	body, _ := json.Marshal(map[string]string{uuid: category})
	resp, err := cl.Post(ctx, "circularity-hub/suggest-rest-price", bytes.NewReader(body))
	if err == nil {
		var prices map[string]any
		if json.Unmarshal(resp.Body, &prices) == nil {
			if p := str(prices[uuid]); p != "" && p != "0" {
				price = p
			}
		}
	}
	return category, price
}

// startHubOrder creates an order for a hub item after confirmation.
func (m *Model) startHubOrder(r *resource, it Item) {
	idStr := r.id(it)
	m.ask(fmt.Sprintf("Create a circularity hub order for item #%s? [y/N]", idStr), func() tea.Cmd {
		return m.run(func(ctx context.Context, cl *client.Client) tea.Msg {
			id, err := strconv.Atoi(idStr)
			if err != nil {
				return doneMsg{err: err}
			}
			orderID, err := cl.CircularityHubOrderCreate(ctx, []int{id})
			return doneMsg{text: fmt.Sprintf("Created hub order %d", orderID), err: err, reload: true}
		})
	})
}
