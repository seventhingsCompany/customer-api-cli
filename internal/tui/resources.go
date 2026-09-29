package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/SeventhingsCompany/customer-api-cli/internal/input"
	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

// Item is one record, normalized to plain JSON values.
type Item = map[string]any

type column struct {
	title string
	key   string
	width int // relative weight
}

type query struct {
	page, perPage int
	search        string
	searchKey     string // field for a server-side search (resource.searchKey)
	filters       []models.FilterEntry
	sorts         []sortKey
}

// formField is a field in a fixed (non-template) form.
type formField struct {
	key, label string
	required   bool
	kind       models.FieldTypeName
	createOnly bool
	source     choiceSource // offer these records in a select
	noneLabel  string
	check      func(s string, get func(key string) string) error
}

// Form kinds that exist only in the TUI.
const (
	// kindUserList is a select whose value is sent as a one-element array
	// (task assignees).
	kindUserList models.FieldTypeName = "USER_LIST"
	// kindObjectRef takes an object UUID or barcode, sent as an asset
	// reference list.
	kindObjectRef models.FieldTypeName = "OBJECT_REF"
	// kindPath is a local file path that must exist.
	kindPath models.FieldTypeName = "PATH"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resolveObjectRefs turns a reference given as UUID or barcode into the
// API's reference list.
func resolveObjectRefs(ctx context.Context, cl *client.Client, body Item) error {
	ref, ok := body["references"].(string)
	if !ok {
		return nil
	}
	uuid := ref
	if !uuidRe.MatchString(ref) {
		obj, err := cl.ObjectGetByBarcode(ctx, ref)
		if err != nil {
			return fmt.Errorf("object with barcode %q: %w", ref, err)
		}
		uuid = str(obj["asset_uuid"])
	}
	body["references"] = []map[string]string{{"type": "asset", "uuid": uuid}}
	return nil
}

// resource describes one tab. Nil funcs disable the matching action.
type resource struct {
	title    string
	singular string
	cli      string // CLI command group, e.g. "rental-cases" or "hub items"
	// searchKey is the field a search matches on the server (like);
	// resources without one search client-side.
	searchKey string
	idKey     string // key holding the ID used by get/update/delete
	columns   []column

	// template drives create/edit forms from field definitions; fixed is
	// used when a resource has no template.
	template models.AssetTrackingTemplate
	fixed    []formField

	// filterable resources take the CLI's filter syntax (key f); sort is
	// how they can be sorted, sortFields limits the fields.
	filterable bool
	sort       sortMode
	sortFields []string

	// load returns one page and the total number of matching records, or
	// noTotal when only count (if set) can tell.
	load    func(ctx context.Context, cl *client.Client, q query) ([]Item, int, error)
	count   func(ctx context.Context, cl *client.Client, q query) (int, error)
	get     func(ctx context.Context, cl *client.Client, id string) (Item, error)
	create  func(ctx context.Context, cl *client.Client, body Item) (string, error)
	update  func(ctx context.Context, cl *client.Client, id string, body Item) error
	del     func(ctx context.Context, cl *client.Client, id string) error
	history func(ctx context.Context, cl *client.Client, id string) (any, error)

	// toggle flips a status (tasks: open ↔ closed).
	toggle func(ctx context.Context, cl *client.Client, it Item) (string, error)
	// download/upload are file-only actions.
	download func(ctx context.Context, cl *client.Client, id string) ([]byte, error)
	upload   func(ctx context.Context, cl *client.Client, path string) (string, error)

	// attach adds or removes file attachments (objects only in the SDK).
	attach func(ctx context.Context, cl *client.Client, id string, atts []models.FileAttachment, remove bool) (*client.Response, error)
	// hubOffer: records can be offered on the circularity hub (objects).
	hubOffer bool
	// hubOrder: records can be ordered (hub items).
	hubOrder bool
}

func (r *resource) id(it Item) string { return str(it[r.idKey]) }

// lookup reads a possibly dotted key ("object_data.internal_identifier").
func lookup(it Item, key string) any {
	var cur any = it
	for part := range strings.SplitSeq(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

// toItems converts any SDK result to []Item.
func toItems(v any) ([]Item, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out []Item
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func toItem(v any) (Item, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out Item
	return out, json.Unmarshal(b, &out)
}

// str renders a JSON value for display.
func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case map[string]any, []any:
		b, _ := json.Marshal(t)
		return string(b)
	}
	return fmt.Sprint(v)
}

// serverSearch returns list options with a like filter on key.
// Zero page and perPage (as count uses) are left out of the query.
func serverSearch(q query) *models.ListOptions {
	o := models.NewListOptions().WithPage(q.page).WithPerPage(q.perPage)
	if q.search != "" {
		o.Where(models.Like(q.searchKey, q.search))
	}
	for _, f := range q.filters {
		o.Where(f)
	}
	for _, s := range q.sorts {
		o.SortBy(s.field, s.dir)
	}
	return o
}

// noTotal marks a page whose total number of records is unknown.
const noTotal = -1

// withoutTotal adapts list calls that do not report a total.
func withoutTotal(items []Item, err error) ([]Item, int, error) {
	return items, noTotal, err
}

// clientPage filters all items by search and returns the requested page
// and the number of matches.
// Every word of the search must appear in some value (case-insensitive),
// so "Henry Rausch" finds a first and a last name in separate fields.
func clientPage(all []Item, q query) ([]Item, int) {
	if words := strings.Fields(strings.ToLower(q.search)); len(words) > 0 {
		var hits []Item
		for _, it := range all {
			var text strings.Builder
			for _, v := range it {
				text.WriteString(strings.ToLower(str(v)) + "\n")
			}
			if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(text.String(), w) }) {
				hits = append(hits, it)
			}
		}
		all = hits
	}
	start := min((q.page-1)*q.perPage, len(all))
	end := min(start+q.perPage, len(all))
	return all[start:end], len(all)
}

// personOptions and userOptions carry the sort of q (one field at most).
func personOptions(q query) *models.PersonListOptions {
	o := models.NewPersonListOptions()
	if len(q.sorts) > 0 {
		o.WithSort(q.sorts[0].field, q.sorts[0].order())
	}
	return o
}

func userOptions(q query) *models.UserListOptions {
	o := models.NewUserListOptions()
	if len(q.sorts) > 0 {
		o.WithSort(models.UserSortBy(q.sorts[0].field), q.sorts[0].order())
	}
	return o
}

func atoi(id string) (int, error) {
	n, err := strconv.Atoi(id)
	if err != nil {
		return 0, fmt.Errorf("invalid numeric ID %q", id)
	}
	return n, nil
}

func personItem(p *models.Person) Item {
	if p.Fields != nil {
		return Item(p.Fields)
	}
	it, _ := toItem(p)
	return it
}

func resources() []*resource {
	return []*resource{
		{
			title: "Objects", cli: "objects", searchKey: "inventory_name", filterable: true, sort: sortMany, singular: "object", idKey: "asset_uuid", template: models.AssetTrackingTemplateAsset,
			columns: []column{{"Name", "inventory_name", 4}, {"Barcode", "barcode", 3}, {"Group", "inventory_group", 2}, {"Updated", "updated_at", 3}},
			load: func(ctx context.Context, cl *client.Client, q query) ([]Item, int, error) {
				return withoutTotal(cl.ObjectsList(ctx, serverSearch(q)))
			},
			count: func(ctx context.Context, cl *client.Client, q query) (int, error) {
				return cl.ObjectsCount(ctx, serverSearch(q))
			},
			get:    func(ctx context.Context, cl *client.Client, id string) (Item, error) { return cl.ObjectGet(ctx, id) },
			create: func(ctx context.Context, cl *client.Client, b Item) (string, error) { return cl.ObjectCreate(ctx, b) },
			update: func(ctx context.Context, cl *client.Client, id string, b Item) error {
				return cl.ObjectPatch(ctx, id, b)
			},
			del: func(ctx context.Context, cl *client.Client, id string) error { return cl.ObjectDelete(ctx, id) },
			history: func(ctx context.Context, cl *client.Client, id string) (any, error) {
				return cl.ObjectHistory(ctx, id, &models.HistoryListOptions{PerPage: 100})
			},
			attach: func(ctx context.Context, cl *client.Client, id string, atts []models.FileAttachment, remove bool) (*client.Response, error) {
				if remove {
					return cl.ObjectRemoveFiles(ctx, id, atts)
				}
				return cl.ObjectAddFiles(ctx, id, atts)
			},
			hubOffer: true,
		},
		{
			title: "Rooms", cli: "rooms", searchKey: "name", filterable: true, sort: sortMany, singular: "room", idKey: "uuid", template: models.AssetTrackingTemplateRoom,
			columns: []column{{"Name", "name", 4}, {"Number", "number", 2}, {"Location ID", "building_id", 2}, {"Updated", "updated_at", 3}},
			load: func(ctx context.Context, cl *client.Client, q query) ([]Item, int, error) {
				return withoutTotal(cl.RoomsList(ctx, serverSearch(q)))
			},
			count: func(ctx context.Context, cl *client.Client, q query) (int, error) {
				return cl.RoomsCount(ctx, serverSearch(q))
			},
			get:    func(ctx context.Context, cl *client.Client, id string) (Item, error) { return cl.RoomGet(ctx, id) },
			create: func(ctx context.Context, cl *client.Client, b Item) (string, error) { return cl.RoomCreate(ctx, b) },
			update: func(ctx context.Context, cl *client.Client, id string, b Item) error {
				_, err := cl.RoomPatch(ctx, id, b)
				return err
			},
			del: func(ctx context.Context, cl *client.Client, id string) error { return cl.RoomDelete(ctx, id) },
			history: func(ctx context.Context, cl *client.Client, id string) (any, error) {
				return cl.RoomHistory(ctx, id, &models.HistoryListOptions{PerPage: 100})
			},
		},
		{
			title: "Locations", cli: "locations", searchKey: "name", filterable: true, sort: sortMany, singular: "location", idKey: "uuid",
			columns: []column{{"Name", "name", 4}, {"ID", "id", 1}, {"City", "city", 3}, {"Address", "address", 4}, {"Country", "country", 2}},
			fixed: []formField{
				{key: "name", label: "Name", required: true, kind: models.FieldTypeText},
				{key: "address", label: "Address", kind: models.FieldTypeText},
				{key: "zip", label: "ZIP", kind: models.FieldTypeText},
				{key: "city", label: "City", kind: models.FieldTypeText},
				{key: "country", label: "Country", kind: models.FieldTypeText},
			},
			load: func(ctx context.Context, cl *client.Client, q query) ([]Item, int, error) {
				return withoutTotal(cl.LocationsList(ctx, serverSearch(q)))
			},
			count: func(ctx context.Context, cl *client.Client, q query) (int, error) {
				return cl.LocationsCount(ctx, serverSearch(q))
			},
			get:    func(ctx context.Context, cl *client.Client, id string) (Item, error) { return cl.LocationGet(ctx, id) },
			create: func(ctx context.Context, cl *client.Client, b Item) (string, error) { return cl.LocationCreate(ctx, b) },
			update: func(ctx context.Context, cl *client.Client, id string, b Item) error {
				_, err := cl.LocationPatch(ctx, id, b)
				return err
			},
			del: func(ctx context.Context, cl *client.Client, id string) error { return cl.LocationDelete(ctx, id) },
			history: func(ctx context.Context, cl *client.Client, id string) (any, error) {
				return cl.LocationHistory(ctx, id, &models.HistoryListOptions{PerPage: 100})
			},
		},
		{
			title: "Persons", cli: "persons", sort: sortOne, singular: "person", idKey: "person_uuid", template: models.AssetTrackingTemplatePerson,
			columns: []column{{"First name", "first_name", 3}, {"Last name", "last_name", 3}, {"E-mail", "email", 5}, {"Department", "department", 3}},
			load: func(ctx context.Context, cl *client.Client, q query) ([]Item, int, error) {
				if q.search != "" { // no server-side search: scan everything
					var all []Item
					for p, err := range cl.PersonsAll(ctx, personOptions(q)) {
						if err != nil {
							return nil, 0, err
						}
						all = append(all, personItem(&p))
					}
					items, total := clientPage(all, q)
					return items, total, nil
				}
				res, err := cl.PersonsList(ctx, personOptions(q).WithPage(q.page).WithPerPage(q.perPage))
				if err != nil {
					return nil, 0, err
				}
				out := make([]Item, len(res.Items))
				for i := range res.Items {
					out[i] = personItem(&res.Items[i])
				}
				return out, res.Total, nil
			},
			get: func(ctx context.Context, cl *client.Client, id string) (Item, error) {
				p, err := cl.PersonGet(ctx, id)
				if err != nil {
					return nil, err
				}
				return personItem(p), nil
			},
			create: func(ctx context.Context, cl *client.Client, b Item) (string, error) { return cl.PersonCreate(ctx, b) },
			update: func(ctx context.Context, cl *client.Client, id string, b Item) error {
				return cl.PersonPatch(ctx, id, b)
			},
			del: func(ctx context.Context, cl *client.Client, id string) error { return cl.PersonDelete(ctx, id) },
			history: func(ctx context.Context, cl *client.Client, id string) (any, error) {
				return cl.PersonHistory(ctx, id, &models.HistoryListOptions{PerPage: 100})
			},
		},
		{
			title: "Users", cli: "users", sort: sortOne, sortFields: []string{"id", "email"}, singular: "user", idKey: "uuid",
			columns: []column{{"Display name", "display_name", 4}, {"E-mail", "email", 5}, {"ID", "id", 1}},
			load: func(ctx context.Context, cl *client.Client, q query) ([]Item, int, error) {
				if q.search != "" {
					var all []Item
					for u, err := range cl.UsersAll(ctx, userOptions(q)) {
						if err != nil {
							return nil, 0, err
						}
						it, _ := toItem(u)
						all = append(all, it)
					}
					items, total := clientPage(all, q)
					return items, total, nil
				}
				res, err := cl.UsersList(ctx, userOptions(q).WithPage(q.page).WithPerPage(q.perPage))
				if err != nil {
					return nil, 0, err
				}
				items, err := toItems(res.Items)
				return items, res.Total, err
			},
			get: func(ctx context.Context, cl *client.Client, id string) (Item, error) {
				u, err := cl.UserGet(ctx, id)
				if err != nil {
					return nil, err
				}
				return toItem(u)
			},
		},
		{
			title: "Tasks", cli: "tasks", singular: "task", idKey: "uuid",
			columns: []column{{"Title", "title", 5}, {"Status", "status", 1}, {"Deadline", "deadline", 2}, {"Updated", "updated_at", 3}},
			fixed: []formField{
				{key: "title", label: "Title", required: true, kind: models.FieldTypeText},
				{key: "assignees", label: "Assignee", required: true, kind: kindUserList, createOnly: true, source: srcUserUUID},
				{key: "references", label: "Object (UUID or barcode)", required: true, kind: kindObjectRef, createOnly: true},
				{key: "deadline", label: "Deadline (YYYY-MM-DD)", required: true, kind: models.FieldTypeDate},
				{key: "comment", label: "Comment", kind: models.FieldTypeLongText},
			},
			load: func(ctx context.Context, cl *client.Client, q query) ([]Item, int, error) {
				tasks, err := cl.TasksList(ctx, nil) // not paginated by the API
				if err != nil {
					return nil, 0, err
				}
				all, err := toItems(tasks)
				items, total := clientPage(all, q)
				return items, total, err
			},
			get: func(ctx context.Context, cl *client.Client, id string) (Item, error) {
				t, err := cl.TaskGet(ctx, id)
				if err != nil {
					return nil, err
				}
				return toItem(t)
			},
			create: func(ctx context.Context, cl *client.Client, b Item) (string, error) {
				if err := resolveObjectRefs(ctx, cl, b); err != nil {
					return "", err
				}
				var in models.CreateTask
				if err := input.Decode(b, &in); err != nil {
					return "", err
				}
				in.Reminders = []models.TimeInterval{}
				return cl.TaskCreate(ctx, in)
			},
			update: func(ctx context.Context, cl *client.Client, id string, b Item) error {
				cur, err := cl.TaskGet(ctx, id)
				if err != nil {
					return err
				}
				var in models.UpdateTask
				if err := input.Merge(cur, b, &in, input.AttachmentUUIDs); err != nil {
					return err
				}
				return cl.TaskUpdate(ctx, id, in)
			},
			del: func(ctx context.Context, cl *client.Client, id string) error { return cl.TaskDelete(ctx, id) },
			history: func(ctx context.Context, cl *client.Client, id string) (any, error) {
				return cl.TaskHistory(ctx, id, &models.HistoryListOptions{PerPage: 100})
			},
			toggle: func(ctx context.Context, cl *client.Client, it Item) (string, error) {
				next := models.TaskStatusClosed
				if str(it["status"]) == string(models.TaskStatusClosed) {
					next = models.TaskStatusOpen
				}
				return string(next), cl.TaskUpdateStatus(ctx, str(it["uuid"]), next)
			},
		},
		{
			title: "Rentals", cli: "rental-cases", searchKey: "title", filterable: true, sort: sortMany, singular: "rental case", idKey: "uuid",
			columns: []column{{"Title", "title", 5}, {"Status", "status", 2}, {"Issue", "issue_date", 2}, {"Due", "due_date", 2}},
			// The API requires every field except the comment.
			fixed: []formField{
				{key: "title", label: "Title", required: true, kind: models.FieldTypeText},
				{key: "renter_user", label: "Renter", kind: models.FieldTypeText, createOnly: true, source: srcUserUUID,
					noneLabel: "(external renter: enter a name below)"},
				{key: "renter_name", label: "Renter name (external)", kind: models.FieldTypeText, createOnly: true,
					check: func(s string, get func(string) string) error {
						if s == "" && get("renter_user") == "" {
							return fmt.Errorf("choose a user above or enter a name")
						}
						return nil
					}},
				{key: "references", label: "Object (UUID or barcode)", required: true, kind: kindObjectRef, createOnly: true},
				{key: "issue_date", label: "Issue date (YYYY-MM-DD)", required: true, kind: models.FieldTypeDate},
				{key: "due_date", label: "Due date (YYYY-MM-DD)", required: true, kind: models.FieldTypeDate},
				{key: "responsible_user_uuid", label: "Responsible user", required: true, kind: models.FieldTypeText,
					createOnly: true, source: srcUserUUID},
				{key: "comment", label: "Comment", kind: models.FieldTypeLongText},
			},
			create: func(ctx context.Context, cl *client.Client, b Item) (string, error) {
				if err := resolveObjectRefs(ctx, cl, b); err != nil {
					return "", err
				}
				user, _ := b["renter_user"].(string)
				name, _ := b["renter_name"].(string)
				delete(b, "renter_user")
				delete(b, "renter_name")
				if user != "" {
					b["renter"] = map[string]string{"type": "user", "value": user}
				} else {
					b["renter"] = map[string]string{"type": "plain", "value": name}
				}
				var in models.CreateRentalCase
				if err := input.Decode(b, &in); err != nil {
					return "", err
				}
				if in.Attachments == nil {
					in.Attachments = []string{}
				}
				return cl.RentalCaseCreate(ctx, in)
			},
			load: func(ctx context.Context, cl *client.Client, q query) ([]Item, int, error) {
				cases, err := cl.RentalCasesList(ctx, serverSearch(q))
				if err != nil {
					return nil, 0, err
				}
				return withoutTotal(toItems(cases))
			},
			get: func(ctx context.Context, cl *client.Client, id string) (Item, error) {
				rc, err := cl.RentalCaseGet(ctx, id)
				if err != nil {
					return nil, err
				}
				return toItem(rc)
			},
			update: func(ctx context.Context, cl *client.Client, id string, b Item) error {
				cur, err := cl.RentalCaseGet(ctx, id)
				if err != nil {
					return err
				}
				var in models.UpdateRentalCase
				if err := input.Merge(cur, b, &in, input.AttachmentUUIDs); err != nil {
					return err
				}
				return cl.RentalCaseUpdate(ctx, id, in)
			},
			del: func(ctx context.Context, cl *client.Client, id string) error { return cl.RentalCaseDelete(ctx, id) },
			history: func(ctx context.Context, cl *client.Client, id string) (any, error) {
				return cl.RentalCaseHistory(ctx, id, &models.HistoryListOptions{PerPage: 100})
			},
		},
		{
			title: "Files", cli: "files", singular: "file", idKey: "uuid",
			columns: []column{{"Name", "name", 5}, {"Type", "type", 2}, {"Size", "size", 1}, {"Created", "created_at", 3}},
			load: func(ctx context.Context, cl *client.Client, q query) ([]Item, int, error) {
				files, err := cl.FilesList(ctx)
				if err != nil {
					return nil, 0, err
				}
				all, err := toItems(files)
				items, total := clientPage(all, q)
				return items, total, err
			},
			get: func(ctx context.Context, cl *client.Client, id string) (Item, error) {
				f, err := cl.FileGet(ctx, id)
				if err != nil {
					return nil, err
				}
				return toItem(f)
			},
			download: func(ctx context.Context, cl *client.Client, id string) ([]byte, error) {
				return cl.FileGetData(ctx, id)
			},
			upload: uploadFile,
		},
		{
			title: "Hub items", cli: "hub items", searchKey: "name", filterable: true, sort: sortMany, singular: "hub item", idKey: "id", hubOrder: true,
			columns: []column{{"ID", "id", 1}, {"Object ID", "asset_id", 1}, {"Barcode", "object_data.internal_identifier", 3},
				{"Price", "price", 1}, {"Created", "created_at", 2}},
			load: func(ctx context.Context, cl *client.Client, q query) ([]Item, int, error) {
				return withoutTotal(cl.CircularityHubItemsList(ctx, serverSearch(q)))
			},
			get: func(ctx context.Context, cl *client.Client, id string) (Item, error) {
				n, err := atoi(id)
				if err != nil {
					return nil, err
				}
				return cl.CircularityHubItemGet(ctx, n)
			},
			del: func(ctx context.Context, cl *client.Client, id string) error {
				n, err := atoi(id)
				if err != nil {
					return err
				}
				return cl.CircularityHubItemDelete(ctx, n)
			},
		},
		{
			title: "Hub orders", cli: "hub orders", searchKey: "order_number", filterable: true, sort: sortMany, singular: "hub order", idKey: "id",
			columns: []column{{"Order", "order_number", 3}, {"Created", "created_at", 3}, {"Total", "total_price", 2}, {"Completed", "completed", 1}, {"Cancelled", "cancelled", 1}},
			load: func(ctx context.Context, cl *client.Client, q query) ([]Item, int, error) {
				orders, err := cl.CircularityHubOrdersList(ctx, serverSearch(q))
				if err != nil {
					return nil, 0, err
				}
				return withoutTotal(toItems(orders))
			},
			get: func(ctx context.Context, cl *client.Client, id string) (Item, error) {
				n, err := atoi(id)
				if err != nil {
					return nil, err
				}
				o, err := cl.CircularityHubOrderGet(ctx, n)
				if err != nil {
					return nil, err
				}
				return toItem(o)
			},
		},
	}
}
