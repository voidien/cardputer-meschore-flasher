package ui

// The Version screen (SPEC.md 4, docs/phase2-design.md 9.4): the role's versions, newest first, with which of their
// files are saved and how big; Enter goes on to Confirm, n shows the notes, d downloads without flashing.

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
)

// versionScreen is the Version screen's state: the selection, the list's scroll, the saved files (asked for when it
// opens and after a download), and a download's failure, shown first until the next key.
type versionScreen struct {
	sel, top int
	saved    []catalog.Entry
	failed   []string
}

// savedFilesMsg is SavedFiles' answer, for the screen that asked (Version, or Saved files).
type savedFilesMsg struct {
	to   screen
	list []catalog.Entry
	err  error
}

func (m *Model) openVersion() tea.Cmd {
	m.screen = scrVersion
	m.ver = versionScreen{}
	return m.savedFiles(scrVersion)
}

// savedFiles lists the saved files in a Cmd.
func (m *Model) savedFiles(to screen) tea.Cmd {
	be := m.backend
	return func() tea.Msg {
		l, err := be.SavedFiles()
		return savedFilesMsg{to: to, list: l, err: err}
	}
}

func (m *Model) gotSavedFiles(msg savedFilesMsg) {
	switch msg.to {
	case scrVersion:
		if msg.err == nil {
			m.ver.saved = msg.list
		}
	case scrFiles:
		m.files.list, m.files.err, m.files.loaded = msg.list, msg.err, true
		m.files.sel = max(0, min(m.files.sel, len(msg.list)-1))
	}
}

// versions are the role's versions, none before a role is picked.
func (m *Model) versions() []catalog.Version {
	if m.flow.fw == nil {
		return nil
	}
	return m.flow.fw.Versions
}

func (m *Model) versionKey(msg tea.KeyMsg) tea.Cmd {
	m.ver.failed = nil
	vs := m.versions()
	switch msg.String() {
	case "j", "down":
		m.ver.sel = min(m.ver.sel+1, max(0, len(vs)-1))
	case "k", "up":
		m.ver.sel = max(m.ver.sel-1, 0)
	case "enter":
		if len(vs) == 0 {
			return nil
		}
		m.flow.ver = &m.flow.fw.Versions[m.ver.sel]
		return m.openConfirm("")
	case "n":
		if len(vs) == 0 {
			return nil
		}
		m.openNotes(&m.flow.fw.Versions[m.ver.sel])
	case "d":
		return m.versionDownload()
	case "esc":
		m.screen = scrRole
	}
	return nil
}

// versionDownload is d: the version's files meshflash would flash (an ESP32's update and full-wipe images both, so
// either works offline later: owner question 3), those already saved skipped.
func (m *Model) versionDownload() tea.Cmd {
	vs := m.versions()
	if len(vs) == 0 || !m.canStart() {
		return nil
	}
	v := &m.flow.fw.Versions[m.ver.sel]
	files := flashFiles(m.flow.dev, *v)
	if len(files) == 0 {
		m.note = "This version has no file meshflash can flash."
		return nil
	}
	var need []catalog.File
	var last time.Time
	for _, f := range files {
		if e, ok := savedEntry(m.ver.saved, f); ok {
			if e.Saved.After(last) {
				last = e.Saved
			}
			continue
		}
		need = append(need, f)
	}
	if len(need) == 0 {
		m.note = "Saved already, " + day(last, m.now()) + "."
		return nil
	}
	m.flow.ver = v
	return m.startDownload(need, scrVersion)
}

func (m *Model) versionView(w, h int) ([]string, string) {
	vs := m.versions()
	m.ver.sel = max(0, min(m.ver.sel, len(vs)-1))
	var head []string
	for _, s := range m.ver.failed {
		head = append(head, wrap(s, w)...)
	}
	col := 0
	for _, v := range vs {
		col = max(col, width(v.Name))
	}
	col += 3
	var list []string
	for i, v := range vs {
		mark := "  "
		if i == m.ver.sel {
			mark = "▸ "
		}
		line := mark + v.Name
		if note := savedNote(m.flow.dev, v, m.ver.saved); note != "" {
			// A column, padded rather than wrapped (wrap would fold the spaces: brief P5); the note is a label, cut.
			line = mark + padRight(v.Name, col) + note
		}
		list = append(list, cut(line, w))
	}
	if len(vs) == 0 {
		list = wrap("The catalog has no version of this role.", w)
	}
	room := max(1, h-len(head))
	follow(&m.ver.top, m.ver.sel, m.ver.sel+1, len(list), room)
	return append(head, list[m.ver.top:min(len(list), m.ver.top+room)]...),
		"enter next  n notes  d download  ? help"
}
