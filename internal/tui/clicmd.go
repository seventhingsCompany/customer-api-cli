package tui

import (
	"regexp"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-cli/internal/config"
)

// cliCommand returns the seventhings command that shows what the screen
// shows: get for a record, list with the tab's search, filter, sort and
// page otherwise. note explains anything the command cannot express.
func (m *Model) cliCommand() (cmd, note string) {
	r, st := m.res[m.tab], m.tabs[m.tab]
	args := []string{"seventhings"}
	if name, _, _, _ := m.deps.Profile(); name != "" && name != config.DefaultProfile {
		args = append(args, "-p", name)
	}
	args = append(args, strings.Fields(r.cli)...)

	if m.mode == modeDetail || m.mode == modeHistory {
		it := m.detailOrSelected()
		if m.mode == modeDetail {
			it = m.detailItem
		}
		verb := "get"
		if m.mode == modeHistory {
			verb = "history"
		}
		return shellJoin(append(args, verb, r.id(it))), ""
	}

	args = append(args, "list")
	paged := r.filterable || r.sort == sortOne // tasks and files have no paging
	if paged && st.page > 1 {
		args = append(args, "--page", strconv.Itoa(st.page))
	}
	if paged && m.pageSize != DefaultPageSize {
		args = append(args, "--per-page", strconv.Itoa(m.pageSize))
	}
	if st.search != "" {
		if r.searchKey != "" {
			args = append(args, "--filter", r.searchKey+" like "+st.search)
		} else {
			note = "the search runs in the UI and is not part of the command"
		}
	}
	for line := range strings.Lines(st.filterText) {
		if line = strings.TrimSpace(line); line != "" {
			args = append(args, "--filter", line)
		}
	}
	for _, s := range st.sorts {
		switch r.sort {
		case sortMany:
			args = append(args, "--sort", s.String())
		case sortOne:
			args = append(args, "--sort", s.field, "--order", string(s.order()))
		}
	}
	return shellJoin(args), note
}

// copyCLICommand puts the equivalent CLI command on the clipboard.
func (m *Model) copyCLICommand() tea.Cmd {
	cmd, note := m.cliCommand()
	msg := "Copied: " + cmd
	if note != "" {
		msg += " (" + note + ")"
	}
	m.setStatus(msg, false)
	return tea.SetClipboard(cmd)
}

// copyID puts a record's ID on the clipboard.
func (m *Model) copyID(r *resource, it Item) tea.Cmd {
	id := r.id(it)
	if id == "" {
		m.setStatus("This record has no ID", true)
		return nil
	}
	m.setStatus("Copied "+id, false)
	return tea.SetClipboard(id)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellJoin quotes args for a POSIX shell.
func shellJoin(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if shellSafe.MatchString(a) {
			out[i] = a
		} else {
			out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(out, " ")
}
