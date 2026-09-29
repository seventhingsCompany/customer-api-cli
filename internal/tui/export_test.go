package tui

import "context"

// ListCommand builds the CLI list command for tab with the given search,
// filter and sort, for the round-trip test in package tui_test.
func ListCommand(tab int, search, filter, sort string) (string, error) {
	m := New(context.Background(), &fakeDeps{loggedIn: true})
	m.tab = tab
	st, r := &m.tabs[tab], m.res[tab]
	var err error
	if st.filters, err = parseFilters(filter); err != nil {
		return "", err
	}
	if st.sorts, err = parseSorts(sort, r); err != nil {
		return "", err
	}
	st.search, st.filterText, st.sortText, st.page = search, filter, sort, 2
	c, _ := m.cliCommand()
	return c, nil
}
