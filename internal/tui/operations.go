package tui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/SeventhingsCompany/customer-api-cli/internal/filesave"
	"github.com/SeventhingsCompany/customer-api-go/client"
)

type operationScope struct {
	tab, nav, session int
}

// formRecovery retains input and original values independently of the current
// view. Rebuilding the form makes it editable again without changing the diff.
type formRecovery struct {
	kind   formKind
	prev   mode
	record *recordForm
	path   *pathForm
	target Item
	custom func(*recordForm) tea.Cmd
}

func (m *Model) rememberForm() {
	m.recovery = &formRecovery{kind: m.formKind, prev: m.prev, record: m.record,
		path: m.path, target: m.formTarget, custom: m.custom}
}

func (m *Model) restoreForm(saved *formRecovery) tea.Cmd {
	m.formKind, m.prev, m.formTarget, m.custom = saved.kind, saved.prev, saved.target, saved.custom
	m.mode = modeForm
	switch saved.kind {
	case formCreate, formEdit, formCustom:
		m.record = newRecordForm(saved.record.title, saved.record.fields, m.formWidth(), m.height)
		m.form = m.record.form
	case formUpload, formDownload:
		m.path = newPathForm(saved.path.title, saved.path.path, saved.kind == formUpload, m.formWidth())
		m.form = m.path.form
	}
	if m.form == nil {
		return nil
	}
	m.form = m.form.WithHeight(m.formHeight())
	return m.form.Init()
}

func (m *Model) download(path, id string, overwrite bool) tea.Cmd {
	r := m.res[m.tab]
	cmd, _ := m.fetch(func(ctx context.Context, cl *client.Client) tea.Msg {
		data, err := r.download(ctx, cl, id)
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = filesave.Write(path, data, overwrite)
		}
		if errors.Is(err, fs.ErrExist) {
			err = fmt.Errorf("%s already exists; choose another path or confirm overwrite", path)
		}
		return doneMsg{text: fmt.Sprintf("Saved %d bytes to %s", len(data), path), err: err}
	})
	return cmd
}

func (m *Model) confirmDownload(path, id string) tea.Cmd {
	if _, err := os.Lstat(path); err == nil {
		m.ask(fmt.Sprintf("Replace existing file %q? [y/N]", path), func() tea.Cmd {
			return m.download(path, id, true)
		})
		return nil
	}
	return m.download(path, id, false)
}
