package ui

// The Board screen (SPEC.md 4, docs/phase2-design.md 9.1): every plugged-in port, what its USB identity suggests,
// what it runs once asked, and the catalog's age; and the probes that ask, one port at a time, on Board and Choose
// board only (design 8.5).

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/flash"
	"meshflash/internal/usbid"
)

// boardScreen is the Board's state: the selection (followed by its port's key, so a port coming or going above it
// doesn't move it), the list's scroll, and the probes: each port's answer by key, the ports waiting to be asked, the
// one being asked now with its cancel.
type boardScreen struct {
	sel    int
	selKey string
	top    int

	nodes    map[string]flash.Node
	queue    []string
	probing  string
	probeAt  time.Time
	probeGen int
	cancel   context.CancelFunc
	// opened says a probe has opened a port, so the scan's dialout hint is no longer only a guess (S16: the block
	// goes once a port opens).
	opened bool
}

// probedMsg is a probe's answer for the port with key, from the probe numbered gen (an answer for a probe since
// cancelled, or a port since gone, is dropped).
type probedMsg struct {
	gen  int
	key  string
	node flash.Node
}

// portKey tells one port from another the way the probes need: its tty and its whole USB identity, so the same
// board on another tty, or another mode on the same tty (an nRF52's bootloader), is asked afresh.
func portKey(p flash.Port) string {
	return strings.Join([]string{p.Name, p.VIDPID(), p.Serial, p.USBPath, p.Interface}, "|")
}

// boardIndex is the selected port's place in the last scan.
func (m *Model) boardIndex() int {
	ports := m.scan.Ports
	for i, p := range ports {
		if portKey(p) == m.board.selKey {
			return i
		}
	}
	return max(0, min(m.board.sel, len(ports)-1))
}

// boardSelected is the selected port, if any is plugged in.
func (m *Model) boardSelected() (flash.Port, bool) {
	if len(m.scan.Ports) == 0 {
		return flash.Port{}, false
	}
	return m.scan.Ports[m.boardIndex()], true
}

func (m *Model) boardMove(by int) {
	if n := len(m.scan.Ports); n > 0 {
		m.board.sel = max(0, min(m.boardIndex()+by, n-1))
		m.board.selKey = portKey(m.scan.Ports[m.board.sel])
	}
}

func (m *Model) boardKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "j", "down":
		m.boardMove(1)
	case "k", "up":
		m.boardMove(-1)
	case "enter":
		return m.boardChoose()
	case "u":
		return m.openUpdate()
	case "s":
		return m.boardSetup()
	case "c":
		return m.openFiles()
	case "r":
		return m.boardRescan()
	case "esc":
		return m.quitNow()
	}
	return nil
}

// boardChoose goes on to Choose board for the selected port; the probe of it carries on there.
func (m *Model) boardChoose() tea.Cmd {
	p, ok := m.boardSelected()
	if !ok {
		return nil
	}
	if m.cat == nil {
		m.note = "Press u to fetch the catalog first."
		return nil
	}
	m.flow = flow{port: p}
	if n, ok := m.board.nodes[portKey(p)]; ok {
		m.flow.node = &n
	}
	m.openChoose()
	return nil
}

// boardSetup sets up the selected board without flashing it, when it answered as a repeater or room server.
func (m *Model) boardSetup() tea.Cmd {
	p, ok := m.boardSelected()
	if !ok {
		return nil
	}
	if n, ok := m.board.nodes[portKey(p)]; !ok || n.Problem != flash.NoProblem {
		m.note = "s sets up a repeater or room server; this board didn't answer as one."
		return nil
	}
	m.cancelProbes()
	// Its answer will be stale once the setup has changed it: ask it again when the Board shows next.
	delete(m.board.nodes, portKey(p))
	return m.openSetup(p, m.rememberedBoard(p), false)
}

// rememberedBoard is the catalog board picked last time for this board's serial number (store.Config.Boards), ""
// when none is, or when the catalog no longer has it.
func (m *Model) rememberedBoard(p flash.Port) string {
	if p.Key == "" {
		return ""
	}
	name := m.cfg.Boards[p.Key]
	if name == "" || (m.cat != nil && m.cat.Find(name) == nil) {
		return ""
	}
	if m.cat != nil {
		return m.cat.Find(name).Name
	}
	return name
}

// boardRescan is r: every port is forgotten and asked again once the new scan is back.
func (m *Model) boardRescan() tea.Cmd {
	m.stopProbe()
	m.board.nodes, m.board.queue = nil, nil
	for _, p := range m.scan.Ports {
		m.board.queue = append(m.board.queue, portKey(p))
	}
	return m.rescan()
}

// probesHere says the screen asks boards what they run: Board and Choose board only (design 13).
func (m *Model) probesHere() bool { return m.screen == scrBoard || m.screen == scrChoose }

// syncProbes follows the last scan: a port gone drops its answer at once (S16) and stops its probe; on Board and
// Choose board a new port is queued, and the next in the queue is asked.
func (m *Model) syncProbes() tea.Cmd {
	cur := map[string]flash.Port{}
	for _, p := range m.scan.Ports {
		cur[portKey(p)] = p
	}
	for k := range m.board.nodes {
		if _, ok := cur[k]; !ok {
			delete(m.board.nodes, k)
		}
	}
	m.board.queue = slices.DeleteFunc(m.board.queue, func(k string) bool { _, ok := cur[k]; return !ok })
	if m.board.probing != "" {
		if _, ok := cur[m.board.probing]; !ok {
			m.stopProbe()
			m.board.queue = slices.DeleteFunc(m.board.queue, func(k string) bool { _, ok := cur[k]; return !ok })
		}
	}
	if len(m.scan.Ports) > 0 {
		i := m.boardIndex()
		m.board.sel, m.board.selKey = i, portKey(m.scan.Ports[i])
	}
	if !m.probesHere() {
		return nil
	}
	for _, p := range m.scan.Ports {
		k := portKey(p)
		if _, done := m.board.nodes[k]; !done && k != m.board.probing && !slices.Contains(m.board.queue, k) {
			m.board.queue = append(m.board.queue, k)
		}
	}
	return m.nextProbe(cur)
}

// nextProbe asks the next port in the queue, when none is being asked: in a Cmd, with a context stopProbe cancels.
func (m *Model) nextProbe(cur map[string]flash.Port) tea.Cmd {
	if m.board.probing != "" || m.quit {
		return nil
	}
	for len(m.board.queue) > 0 {
		k := m.board.queue[0]
		m.board.queue = m.board.queue[1:]
		p, ok := cur[k]
		if !ok {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.board.probeGen++
		m.board.probing, m.board.probeAt, m.board.cancel = k, m.now(), cancel
		m.busy++
		gen, be := m.board.probeGen, m.backend
		return func() tea.Msg {
			return probedMsg{gen: gen, key: k, node: be.Probe(ctx, p)}
		}
	}
	return nil
}

// stopProbe cancels the probe under way, if any, and puts its port back at the head of the queue, so it is asked
// again when probes resume; its answer, when it comes, is dropped.
func (m *Model) stopProbe() {
	if m.board.probing == "" {
		return
	}
	if m.board.cancel != nil {
		m.board.cancel()
	}
	m.board.queue = append([]string{m.board.probing}, m.board.queue...)
	m.board.probing, m.board.cancel = "", nil
	m.board.probeGen++
}

// gotProbe keeps a probe's answer for a port still listed, and asks the next one.
func (m *Model) gotProbe(msg probedMsg) tea.Cmd {
	m.busy--
	if msg.gen != m.board.probeGen || msg.key != m.board.probing {
		return nil
	}
	if m.board.cancel != nil {
		m.board.cancel()
	}
	m.board.probing, m.board.cancel = "", nil
	cur := map[string]flash.Port{}
	for _, p := range m.scan.Ports {
		cur[portKey(p)] = p
	}
	if _, ok := cur[msg.key]; ok && msg.node.Problem != flash.Gone {
		if m.board.nodes == nil {
			m.board.nodes = map[string]flash.Node{}
		}
		m.board.nodes[msg.key] = msg.node
		switch msg.node.Problem {
		case flash.NoProblem, flash.Silent, flash.Stuck, flash.NotSettable, flash.Busy:
			m.board.opened = true
		}
		if m.screen == scrChoose && portKey(m.flow.port) == msg.key {
			n := msg.node
			m.flow.node = &n
			m.chooseAnswered()
		}
	}
	if !m.probesHere() {
		return nil
	}
	return m.nextProbe(cur)
}

// probeState is what the Board and Choose board say of a port's probe: under way, waiting, or its answer.
func (m *Model) probeState(p flash.Port) string {
	k := portKey(p)
	if m.board.probing == k {
		return "Asking it what it runs… (" + elapsed(m.now().Sub(m.board.probeAt)) + ")"
	}
	if n, ok := m.board.nodes[k]; ok {
		return nodeSays(n)
	}
	if slices.Contains(m.board.queue, k) {
		return "Waiting to ask it…"
	}
	return ""
}

// followFlowPort keeps the board picked on its tty as it is now, when it re-enumerated under another name (the
// same board by usbid.SameBoard), so the screens name the tty it is on.
func (m *Model) followFlowPort() {
	switch m.screen {
	case scrChoose, scrRole, scrVersion, scrNotes, scrConfirm, scrDownload:
	default:
		return
	}
	for _, q := range m.scan.Ports {
		if portKey(q) == portKey(m.flow.port) {
			return
		}
	}
	for _, q := range m.scan.Ports {
		if usbid.SameBoard(m.flow.port.Port, q.Port) {
			m.flow.port = q
			return
		}
	}
}

// couldBe is what the USB identity suggests: "Could be: A, B, C." for three or fewer boards, "Could be one of 27
// boards." for more (SPEC.md 4), "" for none.
func couldBe(p flash.Port) string {
	var names []string
	for _, c := range p.Candidates {
		if !slices.Contains(names, c.Device) {
			names = append(names, c.Device)
		}
	}
	switch {
	case len(names) == 0:
		return ""
	case len(names) > 3:
		return "Could be one of " + itoa(len(names)) + " boards."
	}
	return "Could be: " + listing(names) + "."
}

// portLines are a port's lines on the Board: "▸ ttyACM0 · XIAO nRF52840 (2886:8044)", only the label cut; then,
// indented and grey, what its identity suggests and what it said.
func (m *Model) portLines(p flash.Port, selected bool, w int) []string {
	mark := "  "
	if selected {
		mark = "▸ "
	}
	id := " (" + p.VIDPID() + ")"
	line := mark + p.Name + id
	if room := w - width(mark) - width(p.Name) - width(" · ") - width(id); p.Label != "" && room >= 2 {
		line = mark + p.Name + " · " + cut(p.Label, room) + id
	}
	out := []string{cut(line, w)}
	detail := strings.TrimSpace(couldBe(p) + " " + m.probeState(p))
	if detail != "" {
		out = append(out, greyAll(indented(detail, w))...)
	}
	return out
}

// dialoutShown says the Board shows the dialout commands: a probe was refused the port, or no probe has opened one
// yet and this process isn't in the group (design 9.1).
func (m *Model) dialoutShown() bool {
	for _, n := range m.board.nodes {
		if n.Problem == flash.NoPermission {
			return true
		}
	}
	// A scan that failed says nothing about the group: its Dialout is only the zero value.
	return m.scanned && m.scanErr == nil && !m.board.opened && !m.scan.Dialout
}

// dialoutLines are the block that says how to get the permission, each command on its own line (SPEC.md 9.3).
func (m *Model) dialoutLines(w int) []string {
	hint := m.scan.Hint
	if hint == "" {
		hint = usbid.DialoutHint
	}
	out := wrap("Serial ports need the dialout group. Run these once:", w)
	for _, l := range strings.Split(hint, "\n") {
		out = append(out, wrap(l, w)...)
	}
	return out
}

func (m *Model) boardView(w, h int) ([]string, string) {
	var head, list, foot []string
	if m.scanErr != nil {
		head = wrap("Can't read the USB ports: "+plainErr(m.scanErr), w)
	}
	ports := m.scan.Ports
	selStart, selEnd := 0, 0
	var starts []int
	switch {
	case len(ports) > 0:
		sel := m.boardIndex()
		for i, p := range ports {
			ls := m.portLines(p, i == sel, w)
			if i == sel {
				selStart, selEnd = len(list), len(list)+len(ls)
			}
			starts = append(starts, len(list))
			list = append(list, ls...)
		}
	case m.scanErr != nil:
		// The error says why no port shows; "plug a board in" would be a guess.
	case m.scanned:
		list = wrap("Plug a LoRa board into the USB-A port.", w)
	default:
		list = wrap("Looking for boards…", w)
	}
	if m.dialoutShown() {
		foot = append(foot, m.dialoutLines(w)...)
	}
	foot = append(foot, greyAll(wrap(m.catalogLine(), w))...)
	room := max(1, h-len(head)-len(foot))
	follow(&m.board.top, selStart, selEnd, len(list), room)
	m.board.top = itemTop(m.board.top, starts)
	body := append(head, list[m.board.top:min(len(list), m.board.top+room)]...)
	for len(body)+len(foot) < h {
		body = append(body, "")
	}
	body = append(body, foot...)
	return body, m.boardHint()
}

func (m *Model) boardHint() string {
	settable := false
	if p, ok := m.boardSelected(); ok {
		n, ok := m.board.nodes[portKey(p)]
		settable = ok && n.Problem == flash.NoProblem
	}
	switch {
	case len(m.scan.Ports) == 0:
		return "r rescan  u update  c files  ? help"
	case settable && m.cat == nil:
		return "s set up  u update  c files  ? help"
	case m.cat == nil:
		return "u update  c files  r rescan  ? help"
	case settable:
		return "enter choose  s set up  u update  ? help"
	}
	return "enter choose  u update  c files  ? help"
}

func (m *Model) boardHelp() []string {
	return []string{
		keyHelp("enter", "choose this port's board"),
		keyHelp("u", "update the catalog (Wi-Fi, about 1.5 MB)"),
		keyHelp("s", "set up a repeater or room server, no flash"),
		keyHelp("c", "saved files"),
		keyHelp("r", "look for boards again"),
		keyHelp("j/k", "move"),
		keyHelp("esc", "quit"),
	}
}
