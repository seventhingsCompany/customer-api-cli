package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/SeventhingsCompany/customer-api-cli/internal/listflags"
	"github.com/SeventhingsCompany/customer-api-go/models"
)

// sortMode is how a resource can be sorted.
type sortMode int

const (
	sortNone sortMode = iota
	sortMany          // generic sort[field]=DIR, any number of fields
	sortOne           // sort_by + order (persons, users)
)

type sortKey struct {
	field string
	dir   models.SortDirection
}

// String is s as the CLI's --sort takes it.
func (s sortKey) String() string {
	if s.dir == models.SortDESC {
		return "-" + s.field
	}
	return s.field
}

// order is the sort_by/order direction of the persons and users endpoints.
func (s sortKey) order() models.UserSortOrder {
	return models.UserSortOrder(strings.ToLower(string(s.dir)))
}

// parseFilters parses one "field op value" filter per line, the syntax of
// the CLI's --filter.
func parseFilters(text string) ([]models.FilterEntry, error) {
	var out []models.FilterEntry
	for line := range strings.Lines(text) {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		e, err := listflags.ParseFilter(line)
		if err != nil {
			return nil, fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "--filter "))
		}
		out = append(out, e)
	}
	return out, nil
}

// parseSorts parses sort fields separated by commas or spaces: "-field",
// "field", "field:asc" or "field:desc".
func parseSorts(text string, r *resource) ([]sortKey, error) {
	var out []sortKey
	for f := range strings.FieldsFuncSeq(text, func(c rune) bool { return c == ',' || c == ' ' }) {
		field, dir, err := listflags.ParseSort(f)
		if err != nil {
			return nil, fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "--sort "))
		}
		if len(r.sortFields) > 0 && !slices.Contains(r.sortFields, field) {
			return nil, fmt.Errorf("%s can be sorted by %s", strings.ToLower(r.title), strings.Join(r.sortFields, " or "))
		}
		out = append(out, sortKey{field, dir})
	}
	if r.sort == sortOne && len(out) > 1 {
		return nil, fmt.Errorf("%s can be sorted by one field only", strings.ToLower(r.title))
	}
	return out, nil
}

// filterForm edits the filter and sort of the current tab.
type filterForm struct {
	filter, sort string
	form         *huh.Form
}

func (m *Model) openFilterForm() tea.Cmd {
	r, st := m.res[m.tab], m.tabs[m.tab]
	if !r.filterable && r.sort == sortNone {
		m.setStatus(fmt.Sprintf("%s cannot be filtered or sorted by the API; use s to search", r.title), true)
		return nil
	}
	ff := &filterForm{filter: st.filterText, sort: st.sortText}
	m.navigate()
	var fields []huh.Field
	if r.filterable {
		fields = append(fields, huh.NewText().Title("Filter").
			Description("One per line: field op value, e.g. inventory_name like Laptop.\nOperators: = != > >= < <= like nlike in nin (and more, see --help)").
			Lines(4).Value(&ff.filter).Validate(func(s string) error { _, err := parseFilters(s); return err }))
	}
	if r.sort != sortNone {
		desc := "field, -field for descending"
		if len(r.sortFields) > 0 {
			desc += "; one of " + strings.Join(r.sortFields, ", ")
		}
		fields = append(fields, huh.NewInput().Title("Sort").Description(desc).Value(&ff.sort).
			Validate(func(s string) error { _, err := parseSorts(s, r); return err }))
	}
	ff.form = huh.NewForm(huh.NewGroup(fields...)).WithWidth(m.formWidth()).WithShowHelp(true).WithTheme(formTheme)
	m.filter = ff
	m.form = ff.form
	m.formKind = formFilter
	m.prev = m.mode
	m.mode = modeForm
	return m.form.Init()
}

// applyFilter stores the submitted filter and sort and reloads from page 1.
func (m *Model) applyFilter() tea.Cmd {
	r, st := m.res[m.tab], &m.tabs[m.tab]
	filters, err := parseFilters(m.filter.filter)
	if err != nil {
		return m.fail(err)
	}
	sorts, err := parseSorts(m.filter.sort, r)
	if err != nil {
		return m.fail(err)
	}
	st.filterText, st.sortText = strings.TrimSpace(m.filter.filter), strings.TrimSpace(m.filter.sort)
	st.filters, st.sorts = filters, sorts
	st.page = 1
	st.invalidate()
	return m.load()
}

// filterSummary describes the active filter and sort for the search line.
func (st *tabState) filterSummary() string {
	var parts []string
	if st.search != "" {
		parts = append(parts, fmt.Sprintf("search: %q", st.search))
	}
	if st.filterText != "" {
		parts = append(parts, "filter: "+strings.Join(strings.Fields(strings.ReplaceAll(st.filterText, "\n", " · ")), " "))
	}
	if st.sortText != "" {
		parts = append(parts, "sort: "+st.sortText)
	}
	return strings.Join(parts, "  ")
}

// clearFilter drops search, filter and sort; it reports whether any was set.
func (st *tabState) clearFilter() bool {
	if st.search == "" && st.filterText == "" && st.sortText == "" {
		return false
	}
	st.search, st.filterText, st.sortText = "", "", ""
	st.filters, st.sorts = nil, nil
	st.page = 1
	st.invalidate()
	return true
}
