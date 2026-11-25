package ui

// The Saved files screen (SPEC.md 4, docs/phase2-design.md 9.11): each downloaded file with its board, role, version,
// size and date; x deletes one after y (S20).

import (
	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
)

// filesScreen is the Saved files screen's state: the list as last read, the selection and scroll, and the file the
// delete question is about (taken at x, so the answer deletes what was asked about).
type filesScreen struct {
	sel, top int
	list     []catalog.Entry
	loaded   bool
	err      error
	del      string
}

// deletedMsg is DeleteFile's answer.
type deletedMsg struct {
	name string
	err  error
}

// openFiles is c on the Board: the probes stop (it leaves the Board for something else) and the list is read.
func (m *Model) openFiles() tea.Cmd {
	m.cancelProbes()
	m.screen = scrFiles
	m.files = filesScreen{}
	return m.savedFiles(scrFiles)
}

func (m *Model) filesKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		m.files.sel = min(m.files.sel+1, max(0, len(m.files.list)-1))
	case "k", "up":
		m.files.sel = max(m.files.sel-1, 0)
	case "x":
		if len(m.files.list) > 0 {
			m.files.del = m.files.list[min(m.files.sel, len(m.files.list)-1)].Name
			m.ask(confirmDelete)
		}
	case "esc":
		return m.toBoard("")
	}
	return nil
}

// deleteFile is y to "Delete …?": in a Cmd.
func (m *Model) deleteFile() tea.Cmd {
	name, be := m.files.del, m.backend
	if name == "" {
		return nil
	}
	return func() tea.Msg { return deletedMsg{name: name, err: be.DeleteFile(name)} }
}

// gotDeleted drops the file's row at once (S16), then reads the list again.
func (m *Model) gotDeleted(msg deletedMsg) tea.Cmd {
	if msg.err != nil {
		m.logf("meshflash: couldn't delete %s: %v", msg.name, msg.err)
		m.note = "Couldn't delete it: screen.log says why."
	} else {
		for i, e := range m.files.list {
			if e.Name == msg.name {
				m.files.list = append(m.files.list[:i:i], m.files.list[i+1:]...)
				break
			}
		}
	}
	if m.screen != scrFiles {
		return nil
	}
	return m.savedFiles(scrFiles)
}

// fileAbout is a saved file's details: the first catalog board whose version lists it ("and 2 more boards" when
// several do), the role and version, the size and the day; just the size and day when no board does.
func (m *Model) fileAbout(e catalog.Entry) string {
	s := ""
	if os := m.owners(e); len(os) > 0 {
		s = os[0].dev
		if n := len(os) - 1; n > 0 {
			s += " and " + plural(n, "more board", "more boards")
		}
		s += " · " + os[0].fw + " " + os[0].ver + " · "
	}
	return s + byteSize(e.Size) + " · " + day(e.Saved, m.now())
}

func (m *Model) filesView(w, h int) ([]string, string) {
	var list []string
	selStart, selEnd := 0, 0
	switch {
	case !m.files.loaded:
		list = wrap("Reading the saved files…", w)
	case m.files.err != nil:
		list = wrap("Can't list the saved files: "+plainErr(m.files.err), w)
	case len(m.files.list) == 0:
		list = wrap("No files saved yet. d on a version downloads one.", w)
	}
	m.files.sel = max(0, min(m.files.sel, len(m.files.list)-1))
	var starts []int
	if m.files.loaded && m.files.err == nil {
		for i, e := range m.files.list {
			starts = append(starts, len(list))
			mark := "  "
			if i == m.files.sel {
				mark, selStart = "▸ ", len(list)
			}
			list = append(list, cut(mark+e.Name, w))
			list = append(list, greyAll(indented(m.fileAbout(e), w))...)
			if i == m.files.sel {
				selEnd = len(list)
			}
		}
	}
	follow(&m.files.top, selStart, selEnd, len(list), h)
	m.files.top = itemTop(m.files.top, starts)
	body := list[m.files.top:min(len(list), m.files.top+h)]
	if len(m.files.list) == 0 || !m.files.loaded || m.files.err != nil {
		return body, "esc back  ? help"
	}
	return body, "j/k move  x delete  esc back  ? help"
}
