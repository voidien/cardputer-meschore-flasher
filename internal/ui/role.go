package ui

// The Role screen (SPEC.md 4, docs/phase2-design.md 9.3): the board's roles in three groups, MeshCore's own, ZephCore
// (a third-party port) and Ripple (closed source, third-party), with the selected role's notices above them.

import (
	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
)

// roleScreen is the Role screen's state: the selected role, as its place in roleOrder.
type roleScreen struct {
	sel int
}

// roleGroup is a heading and its roles, as indexes into the board's Firmware.
type roleGroup struct {
	head string
	fws  []int
}

// roleGroups sorts the board's roles into the three groups, each in catalog order; empty groups are left out.
func (m *Model) roleGroups() []roleGroup {
	if m.flow.dev == nil {
		return nil
	}
	groups := []roleGroup{{head: "MeshCore"}, {head: "ZephCore · third-party port"},
		{head: "Ripple · closed source, third-party"}}
	for i, f := range m.flow.dev.Firmware {
		switch {
		case f.Source == catalog.SourceZephCore:
			groups[1].fws = append(groups[1].fws, i)
		case f.ClosedSource:
			groups[2].fws = append(groups[2].fws, i)
		default:
			groups[0].fws = append(groups[0].fws, i)
		}
	}
	var out []roleGroup
	for _, g := range groups {
		if len(g.fws) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// roleOrder is every role in the order the screen lists them.
func (m *Model) roleOrder() []int {
	var out []int
	for _, g := range m.roleGroups() {
		out = append(out, g.fws...)
	}
	return out
}

func (m *Model) openRole() {
	m.screen = scrRole
	m.role = roleScreen{}
}

func (m *Model) roleKey(msg tea.KeyMsg) tea.Cmd {
	order := m.roleOrder()
	switch msg.String() {
	case "j", "down":
		m.role.sel = min(m.role.sel+1, max(0, len(order)-1))
	case "k", "up":
		m.role.sel = max(m.role.sel-1, 0)
	case "enter":
		if len(order) == 0 {
			return nil
		}
		m.flow.fw = &m.flow.dev.Firmware[order[min(m.role.sel, len(order)-1)]]
		return m.openVersion()
	case "esc":
		// Back to the boards as they were left, the probes carrying on.
		m.screen = scrChoose
		return m.syncProbes()
	}
	return nil
}

func (m *Model) roleView(w, h int) ([]string, string) {
	order := m.roleOrder()
	m.role.sel = max(0, min(m.role.sel, len(order)-1))
	var lines []string
	var notices []int // where each notice starts
	selLine := 0
	if len(order) > 0 {
		for _, n := range m.flow.dev.Firmware[order[m.role.sel]].Notices {
			notices = append(notices, len(lines))
			lines = append(lines, greyAll(wrap(n, w))...)
		}
	}
	listStart := len(lines)
	k := 0
	for _, g := range m.roleGroups() {
		lines = append(lines, heading(cut(g.head, w)))
		for _, i := range g.fws {
			mark := "  "
			if k == m.role.sel {
				mark, selLine = "▸ ", len(lines)
			}
			lines = append(lines, cut(mark+roleName(m.flow.dev.Firmware[i]), w))
			k++
		}
	}
	if len(order) == 0 {
		lines = append(lines, wrap("The catalog has no firmware for this board.", w)...)
	}
	// The notices show while the selection leaves room for them, and scroll off the top as it moves down, a whole
	// notice at a time (a notice's last lines alone would read as nonsense); the list then scrolls a line at a time.
	top := max(0, selLine-h+1)
	if top > 0 && top < listStart {
		top = itemTop(top, append(notices, listStart))
	}
	return lines[top:min(len(lines), top+h)], "j/k move  enter pick  esc back  ? help"
}
