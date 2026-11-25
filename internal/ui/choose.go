package ui

// Choose board (SPEC.md 4, docs/phase2-design.md 9.2): the best matches first (the board the node names, the boards
// the USB table is sure of, the ones it only infers), then every other board; the ones meshflash can't flash on this
// port grey, with why. The owner always picks: the cursor only starts somewhere.

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
)

// chooseScreen is Choose board's state: the selected board (by name, so it stays put as rows come and go), whether
// the owner has moved it, the find box and the filter it keeps, and the list's scroll.
type chooseScreen struct {
	sel     string
	moved   bool
	filter  string
	box     editor
	boxOpen bool
	top     int
}

// chooseRow is a heading, or a board with whether it fits the port and why not.
type chooseRow struct {
	head  string
	dev   *catalog.Device
	ok    bool
	why   string
	short string
}

// findLimit is how long the find box's text may get: longer than any board's name.
const findLimit = 40

func (m *Model) openChoose() {
	m.screen = scrChoose
	m.choose = chooseScreen{}
	m.choose.sel = m.chooseStart(m.chooseRows())
}

// chooseRows are the groups in order, each in catalog order, every board once, filtered by the find box's text.
func (m *Model) chooseRows() []chooseRow {
	c := m.cat
	if c == nil {
		return nil
	}
	index := map[*catalog.Device]int{}
	for i := range c.Devices {
		index[&c.Devices[i]] = i
	}
	used := map[int]bool{}
	var rows []chooseRow
	group := func(head string, idxs []int, alwaysHead bool) {
		var boards []chooseRow
		for _, i := range idxs {
			d := &c.Devices[i]
			if used[i] || !matches(d.Name, m.choose.filter) {
				continue
			}
			used[i] = true
			ok, why, short := flash.Fits(*d, m.flow.port)
			boards = append(boards, chooseRow{dev: d, ok: ok, why: why, short: short})
		}
		if len(boards) > 0 || (alwaysHead && m.choose.filter == "") {
			rows = append(rows, chooseRow{head: head})
			rows = append(rows, boards...)
		}
	}
	named := -1
	if n := m.flow.node; answered(n) && n.Info.Board != "" {
		var idxs []int
		if d := m.find(n.Info.Board); d != nil {
			named = index[d]
			idxs = []int{named}
		}
		group("The board says: "+n.Info.Board, idxs, true)
	}
	var sure, unsure []int
	for _, cd := range m.flow.port.Candidates {
		d := m.find(cd.Device)
		if d == nil || index[d] == named {
			continue
		}
		if cd.Unverified {
			unsure = append(unsure, index[d])
		} else {
			sure = append(sure, index[d])
		}
	}
	slices.Sort(sure)
	slices.Sort(unsure)
	matchHead := "USB " + m.flow.port.VIDPID() + " matches:"
	if named >= 0 {
		matchHead = "USB " + m.flow.port.VIDPID() + " also matches:"
	}
	group(matchHead, sure, false)
	group("Might also be (unverified):", unsure, false)
	all := make([]int, len(c.Devices))
	for i := range all {
		all[i] = i
	}
	group("All boards:", all, false)
	return rows
}

// chooseStart is where the cursor starts: on the board remembered for this board's serial number when it is listed;
// else on the only match, when the board the node named and the sure matches come to exactly one; else on the first
// board. It never picks for the owner.
func (m *Model) chooseStart(rows []chooseRow) string {
	if k := m.flow.port.Key; k != "" {
		if name := m.cfg.Boards[k]; name != "" {
			for _, r := range rows {
				if r.dev != nil && r.dev.Name == name {
					return name
				}
			}
		}
	}
	var best []string
	for _, r := range rows {
		if r.head != "" {
			continue
		}
		if !slices.Contains(best, r.dev.Name) && m.chooseIsMatch(r.dev) {
			best = append(best, r.dev.Name)
		}
	}
	if len(best) == 1 {
		return best[0]
	}
	for _, r := range rows {
		if r.dev != nil {
			return r.dev.Name
		}
	}
	return ""
}

// chooseIsMatch says d is the board the node named or one the USB table is sure of.
func (m *Model) chooseIsMatch(d *catalog.Device) bool {
	if n := m.flow.node; answered(n) && n.Info.Board != "" {
		if nd := m.find(n.Info.Board); nd != nil && nd.Name == d.Name {
			return true
		}
	}
	for _, cd := range m.flow.port.Candidates {
		if !cd.Unverified && m.find(cd.Device) == d {
			return true
		}
	}
	return false
}

// chooseAnswered redraws the groups when the probe answers on this screen: the cursor stays on its board, and moves
// to where it would have started only if the owner hasn't moved it.
func (m *Model) chooseAnswered() {
	rows := m.chooseRows()
	if !m.choose.moved || !chooseListed(rows, m.choose.sel) {
		m.choose.sel = m.chooseStart(rows)
	}
}

func chooseListed(rows []chooseRow, name string) bool {
	for _, r := range rows {
		if r.dev != nil && r.dev.Name == name {
			return true
		}
	}
	return false
}

// chooseSelected is the selected row, if any board is listed.
func (m *Model) chooseSelected(rows []chooseRow) (chooseRow, bool) {
	for _, r := range rows {
		if r.dev != nil && r.dev.Name == m.choose.sel {
			return r, true
		}
	}
	for _, r := range rows {
		if r.dev != nil {
			return r, true
		}
	}
	return chooseRow{}, false
}

func (m *Model) chooseMove(by int) {
	rows := m.chooseRows()
	var names []string
	for _, r := range rows {
		if r.dev != nil {
			names = append(names, r.dev.Name)
		}
	}
	if len(names) == 0 {
		return
	}
	i := max(0, slices.Index(names, m.choose.sel))
	m.choose.sel = names[max(0, min(i+by, len(names)-1))]
	m.choose.moved = true
}

func (m *Model) chooseKey(msg tea.KeyMsg) tea.Cmd {
	if m.choose.boxOpen {
		return m.chooseBoxKey(msg)
	}
	switch msg.String() {
	case "j", "down":
		m.chooseMove(1)
	case "k", "up":
		m.chooseMove(-1)
	case "f":
		m.choose.box = newEditor(m.choose.filter, findLimit)
		m.choose.boxOpen = true
	case "enter":
		return m.chooseEnter()
	case "esc":
		return m.toBoard("")
	}
	return nil
}

// chooseBoxKey is a key in the find box: Enter keeps the filter, Esc clears it, the arrows up and down still move,
// anything else edits it and filters every group as typed.
func (m *Model) chooseBoxKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		m.choose.boxOpen = false
		return nil
	case tea.KeyEsc:
		m.choose.box.Clear()
		m.choose.boxOpen, m.choose.filter = false, ""
		m.chooseRefiltered()
		return nil
	case tea.KeyUp:
		m.chooseMove(-1)
		return nil
	case tea.KeyDown:
		m.chooseMove(1)
		return nil
	}
	if m.choose.box.key(msg) {
		m.choose.filter = m.choose.box.Value()
		m.chooseRefiltered()
	}
	return nil
}

// chooseRefiltered keeps the cursor on its board when the new filter still lists it, else puts it on the first.
func (m *Model) chooseRefiltered() {
	rows := m.chooseRows()
	if !chooseListed(rows, m.choose.sel) {
		if r, ok := m.chooseSelected(rows); ok {
			m.choose.sel = r.dev.Name
		}
	}
}

// chooseEnter picks the board: one that doesn't fit the port says why instead; one that does is remembered for
// the board's serial number (none for a board without a useful one) and the roles come next.
func (m *Model) chooseEnter() tea.Cmd {
	r, ok := m.chooseSelected(m.chooseRows())
	if !ok {
		return nil
	}
	if !r.ok {
		m.note = r.why
		return nil
	}
	m.rememberBoard(m.flow.port.Key, r.dev.Name)
	m.flow.dev = r.dev
	m.cancelProbes()
	m.openRole()
	return nil
}

// rememberBoard keeps the board picked for a serial number, in config.json and in the model's copy of it. A write
// that fails is logged: the pick still holds for this run.
func (m *Model) rememberBoard(key, name string) {
	if key == "" {
		return
	}
	if m.store != nil {
		if err := m.store.RememberBoard(key, name); err != nil {
			m.logf("meshflash: couldn't remember the board picked: %v", err)
		}
	}
	boards := make(map[string]string, len(m.cfg.Boards)+1)
	for k, v := range m.cfg.Boards {
		boards[k] = v
	}
	boards[key] = name
	m.cfg.Boards = boards
}

func (m *Model) chooseView(w, h int) ([]string, string) {
	var head []string
	k := portKey(m.flow.port)
	switch {
	case m.board.probing == k:
		head = append(head, wrap(m.probeState(m.flow.port), w)...)
	case slices.Contains(m.board.queue, k) && m.probesHere():
		head = append(head, wrap("Waiting to ask it…", w)...)
	}
	switch {
	case m.choose.boxOpen:
		const label = "Find: "
		ls, at := m.choose.box.lines(max(2, w-width(label)), true)
		head = append(head, label+window(ls, 1, at)[0])
	case m.choose.filter != "":
		head = append(head, grey.Render(cut("Find: "+m.choose.filter, w)))
	}
	rows := m.chooseRows()
	sel, hasSel := m.chooseSelected(rows)
	var list []string
	// from and to are the selected board's line and its group's heading just above it, kept in view together.
	from, to := 0, 0
	for i, r := range rows {
		if r.head != "" {
			list = append(list, heading(cut(r.head, w)))
			continue
		}
		mark := "  "
		if hasSel && r.dev == sel.dev {
			mark = "▸ "
			from, to = len(list), len(list)+1
			if i > 0 && rows[i-1].head != "" {
				from--
			}
		}
		line := cut(mark+r.dev.Name, w)
		if !r.ok {
			// The tag says why it can't be picked: the name is cut first, so the tag stays whole.
			tag := " · " + r.short
			line = grey.Render(cut(mark+cut(r.dev.Name, max(1, w-width(mark)-width(tag)))+tag, w))
		}
		list = append(list, line)
	}
	if !hasSel {
		if m.choose.filter != "" {
			list = append(list, wrap("No board matches “"+m.choose.filter+"”.", w)...)
		} else {
			list = append(list, wrap("The catalog has no boards.", w)...)
		}
	}
	room := max(1, h-len(head))
	if to-from > room {
		from = to - 1
	}
	follow(&m.choose.top, from, to, len(list), room)
	body := append(head, list[m.choose.top:min(len(list), m.choose.top+room)]...)
	if m.choose.boxOpen {
		return body, "enter keep  esc clear"
	}
	return body, "j/k move  enter pick  f find  ? help"
}

func (m *Model) chooseHelp() []string {
	return []string{
		keyHelp("j/k", "move"),
		keyHelp("enter", "pick this board"),
		keyHelp("f", "find by name: type to filter every group; enter keeps it, esc clears it, backspace "+
			"deletes"),
		keyHelp("esc", "back"),
	}
}
