package ui

// The Notes screen (SPEC.md 4, docs/phase2-design.md 9.5): a version's release notes, wrapped line by line (they
// are lists and links, not prose: brief P6), j/k two lines at a time, Space/b a page.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
)

// notesScreen is the Notes screen's state: the version and the scroll.
type notesScreen struct {
	ver *catalog.Version
	top int
}

func (m *Model) openNotes(v *catalog.Version) {
	m.screen = scrNotes
	m.notes = notesScreen{ver: v}
}

// notesLines wraps release notes to w: each line of the notes starts a line, a list item ("* ", "- ") hangs its
// later lines under its text, an indented line keeps its indent (up to 4), and a long URL breaks where it must.
// Markdown emphasis goes (plain), since the CLI app shows neither bold nor italic.
func notesLines(text string, w int) []string {
	text = plain(text)
	if strings.TrimSpace(text) == "" {
		return []string{"No release notes."}
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		ind := min(len(line)-len(trimmed), 4)
		if w-ind < 8 {
			ind = 0
		}
		pad := strings.Repeat(" ", ind)
		t := strings.TrimSpace(trimmed)
		marker := ""
		if strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "- ") {
			marker, t = t[:2], strings.TrimSpace(t[2:])
		}
		ls := wrap(t, max(1, w-ind-len(marker)))
		for i, l := range ls {
			if i == 0 {
				out = append(out, pad+marker+l)
			} else {
				out = append(out, pad+strings.Repeat(" ", len(marker))+l)
			}
		}
	}
	return out
}

func (m *Model) notesKey(msg tea.KeyMsg) tea.Cmd {
	_, h := m.size()
	page := max(1, h-3)
	switch msg.String() {
	case "j", "down":
		m.notes.top += scrollStep
	case "k", "up":
		m.notes.top -= scrollStep
	case " ":
		m.notes.top += page
	case "b":
		m.notes.top -= page
	case "esc":
		m.screen = scrVersion
	}
	return nil
}

func (m *Model) notesView(w, h int) ([]string, string) {
	text := ""
	if m.notes.ver != nil {
		text = m.notes.ver.Notes
	}
	return scrolled(notesLines(text, w), &m.notes.top, h), "j/k scroll  space page  esc back  ? help"
}
