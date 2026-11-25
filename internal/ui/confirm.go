package ui

// The Confirm screen (SPEC.md 4, docs/phase2-design.md 9.6): everything that is about to happen, and nothing written
// until y. There is no preselected kind on an ESP32 (u update, w full wipe); an nRF52 has only an update. A file not
// saved yet is downloaded by the first y, and Confirm shows again with its SHA-256; a low or unreadable battery, and
// an nRF52 whose SoftDevice the catalog doesn't name, ask once more.
//
// Races, and how they are closed (design 9.6, 8.8): every Prepare bumps the generation and only the latest answer,
// for the board, version and kind now picked, is kept; y waits for it, and what is flashed is that answer's Request,
// refused unless its file is of the kind picked. A held y (m.yHeld) does nothing, and so does a y while the battery
// reading of an earlier y is out, so one press starts at most one Job; Flash itself is called in Update.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
)

// lowBattery is the charge under which Confirm warns and y asks again (SPEC.md 6.1).
const lowBattery = 30

// confirmScreen is the Confirm screen's state.
type confirmScreen struct {
	// gen is the latest Prepare asked, askedAt when; prep the latest answer (current only when prep.gen == gen).
	gen     int
	askedAt time.Time
	prep    *preparedMsg
	// yGen numbers the battery readings y asks for; yOut says one is out; yPct is the last one's percent, for the
	// low-battery question (negative: it couldn't be read).
	yGen int
	yOut bool
	yPct int
	// readyAt: y does nothing before it (0.5 s after Confirm shows again from a download: design 8.8).
	readyAt time.Time
	// failed is a download's failure, until the next key; state is the failed flash's State after r on Result.
	failed []string
	state  string
}

// preparedMsg is Prepare's answer for req, with a battery reading taken in the same Cmd.
type preparedMsg struct {
	gen        int
	req        flash.Request
	p          flash.Prepared
	battery    int
	hasBattery bool
}

// yBatteryMsg is the battery read when y would flash.
type yBatteryMsg struct {
	gen     int
	percent int
	ok      bool
}

// current is the latest Prepare's answer, nil while it is out.
func (c *confirmScreen) current() *preparedMsg {
	if c.prep != nil && c.prep.gen == c.gen {
		return c.prep
	}
	return nil
}

// openConfirm shows Confirm for the version picked: an nRF52's only kind is update; an ESP32 has none until u or w.
// state is a failed flash's State, after r on Result ("" otherwise).
func (m *Model) openConfirm(state string) tea.Cmd {
	if !m.flowPicked() {
		return nil
	}
	m.flow.kind = 0
	if m.flow.dev.Type != catalog.ESP32 {
		m.flow.kind = catalog.Update
	}
	m.conf = confirmScreen{gen: m.conf.gen, yGen: m.conf.yGen, state: state}
	m.screen = scrConfirm
	return m.prepare()
}

// flowPicked says the board, role and version are picked, as Confirm needs them.
func (m *Model) flowPicked() bool { return m.flow.dev != nil && m.flow.fw != nil && m.flow.ver != nil }

// request is the flash as picked so far.
func (m *Model) request() flash.Request {
	return flash.Request{Port: m.flow.port, Device: *m.flow.dev, Firmware: *m.flow.fw, Version: *m.flow.ver,
		Kind: m.flow.kind}
}

// prepare asks Prepare (and the battery) again, in a Cmd: when Confirm opens, at u and w, after a download. A y
// whose battery reading is still out is dropped: it was pressed on what Confirm showed before, which may not be what
// it shows now (a kind changed between the y and its reading would otherwise be written without a y of its own).
func (m *Model) prepare() tea.Cmd {
	m.conf.gen++
	m.conf.yOut = false
	m.conf.askedAt = m.now()
	gen, req, be := m.conf.gen, m.request(), m.backend
	return func() tea.Msg {
		p := be.Prepare(req)
		pct, ok := be.Battery()
		return preparedMsg{gen: gen, req: req, p: p, battery: pct, hasBattery: ok}
	}
}

// gotPrepared keeps Prepare's answer if it is the latest one and still for what is picked.
func (m *Model) gotPrepared(msg preparedMsg) {
	f := m.flow
	if msg.gen != m.conf.gen || f.dev == nil || f.ver == nil || msg.req.Device.Name != f.dev.Name ||
		msg.req.Version.Name != f.ver.Name || msg.req.Kind != f.kind {
		return
	}
	m.conf.prep = &msg
}

func (m *Model) confirmScreenKey(msg tea.KeyMsg) tea.Cmd {
	m.conf.failed = nil
	if !m.flowPicked() {
		if msg.String() == "esc" {
			m.screen = scrVersion
		}
		return nil
	}
	switch msg.String() {
	case "u":
		return m.confirmPickKind(catalog.Update)
	case "w":
		return m.confirmPickKind(catalog.Wipe)
	case "y":
		return m.confirmY()
	case "esc":
		m.screen = scrVersion
		return m.savedFiles(scrVersion)
	}
	return nil
}

// confirmPickKind is u or w: on an ESP32 the kind is picked and the file checked again; an nRF52 has only an update.
func (m *Model) confirmPickKind(k catalog.Kind) tea.Cmd {
	if m.flow.dev.Type != catalog.ESP32 {
		if k == catalog.Wipe {
			m.note = "An nRF52 has only an update."
		}
		return nil
	}
	if m.flow.kind == k {
		return nil
	}
	m.flow.kind = k
	return m.prepare()
}

// confirmY is y: nothing while it would be unsafe or unclear (a held y, a Job running, no kind, the check not back,
// an earlier y's reading out, just back from a download, the board unplugged, a refusal); a file not saved is
// downloaded; a saved one has the battery read, then asks or flashes.
func (m *Model) confirmY() tea.Cmd {
	if m.yHeld || !m.canStart() {
		return nil
	}
	if m.flow.kind == 0 {
		m.note = "Press u to update or w for a full wipe first."
		return nil
	}
	pr := m.conf.current()
	if pr == nil || m.conf.yOut || m.now().Before(m.conf.readyAt) || !m.listed(m.flow.port) || pr.p.Problem != "" {
		return nil
	}
	if !pr.p.Saved {
		f := pr.p.Flash.File
		if f.URL == "" {
			fl, err := catalog.Choose(pr.req.Device, pr.req.Version, pr.req.Kind)
			if err != nil {
				return nil
			}
			f = fl.File
		}
		return m.startDownload([]catalog.File{f}, scrConfirm)
	}
	m.conf.yGen++
	m.conf.yOut = true
	gen, be := m.conf.yGen, m.backend
	return func() tea.Msg {
		pct, ok := be.Battery()
		return yBatteryMsg{gen: gen, percent: pct, ok: ok}
	}
}

// gotYBattery goes on from y once the battery is read: under 30% or unreadable asks; else the SoftDevice question,
// if Prepare had one; else the flash.
func (m *Model) gotYBattery(msg yBatteryMsg) tea.Cmd {
	if msg.gen != m.conf.yGen || !m.conf.yOut {
		return nil
	}
	m.conf.yOut = false
	if m.screen != scrConfirm || m.confirm != noConfirm {
		return nil
	}
	m.conf.yPct = msg.percent
	if msg.ok && msg.percent < lowBattery {
		m.ask(confirmLowBattery)
		return nil
	}
	return m.afterLowBattery()
}

// afterLowBattery is y to the low-battery question (or no question needed): Prepare's own question next, if any.
func (m *Model) afterLowBattery() tea.Cmd {
	if pr := m.conf.current(); pr != nil && pr.p.Ask != "" {
		m.ask(confirmUnchecked)
		return nil
	}
	return m.startFlash()
}

// startFlash flashes what Confirm shows, checked once more: no Job running, the latest Prepared, saved and not
// refused, the board plugged in, and its file of the kind picked (a kind changed since can't be written).
func (m *Model) startFlash() tea.Cmd {
	pr := m.conf.current()
	if !m.canStart() || m.screen != scrConfirm || pr == nil || pr.p.Problem != "" || !pr.p.Saved ||
		!m.listed(m.flow.port) {
		return nil
	}
	if pr.p.Flash.Kind != m.flow.kind || pr.req.Kind != m.flow.kind {
		m.logf("meshflash: refused to flash: the file checked is for %v, and %v is picked", pr.p.Flash.Kind,
			m.flow.kind)
		return nil
	}
	m.cancelProbes()
	// What the boards run is about to change: the Board asks them all again when it shows next.
	m.board.nodes = nil
	j := m.backend.Flash(context.Background(), pr.req)
	cmd := m.startedJob(j)
	if m.ownJob() {
		m.fl = flashingScreen{}
		m.screen = scrFlashing
	}
	return cmd
}

// confirmHead is the first line: the board, its tty, and what it said it runs.
func (m *Model) confirmHead() string {
	s := m.devName() + " on " + m.flow.port.Name
	if n := m.flow.node; answered(n) {
		v := n.Info.Version
		what := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
		if n.Info.Role != "" {
			what = roleWords(n.Info.Role) + " " + what
		}
		s += " (" + what + ")"
	}
	return s
}

func (m *Model) confirmView(w, h int) ([]string, string) {
	if !m.flowPicked() {
		return nil, "esc back  ? help"
	}
	// Each part is wrapped whole; when they don't all fit (40×10 with a question open), the ones that only explain
	// go first: what the kind does, then Prepare's notes; then the head is shortened to the board's name, then the
	// file line loses its size. What is about to be written, and every warning, stay: if even then it doesn't fit,
	// the top lines go (confirmFit), never the warnings and the file's check at the bottom.
	type part struct {
		lines    []string
		optional int      // 0: always shown; else dropped in this order (1 first) when the screen is short
		short    []string // its shorter form, from drop shortAt on (nil: none)
		shortAt  int
	}
	var parts []part
	add := func(optional int, s string) { parts = append(parts, part{lines: wrap(s, w), optional: optional}) }
	add(0, m.confirmHead())
	parts[0].short, parts[0].shortAt = []string{cut(m.devName(), w)}, 3
	if m.conf.state != "" {
		add(0, m.conf.state)
	}
	pr := m.conf.current()
	kind := m.flow.kind
	file := m.fwName() + " " + m.verName()
	short := ""
	switch {
	case kind == 0:
	case pr != nil && pr.p.Saved:
		short = file + " · " + kind.String()
		file += " · " + byteSize(pr.p.Entry.Size) + " · " + kind.String()
	case pr != nil && pr.p.Problem == "":
		file += " · " + kind.String() + " · not saved yet"
	default:
		file += " · " + kind.String()
	}
	add(0, file)
	if short != "" {
		parts[len(parts)-1].short, parts[len(parts)-1].shortAt = wrap(short, w), 4
	}
	switch kind {
	case catalog.Update:
		add(1, "Keeps identity, contacts and pairing.")
	case catalog.Wipe:
		add(1, "Erases the whole chip: identity, contacts and pairing go.")
	default:
		add(0, "Press u to update or w for a full wipe.")
	}
	if kind != 0 {
		switch {
		case pr == nil:
			add(0, "Checking the file… ("+elapsed(m.now().Sub(m.conf.askedAt))+")")
		case pr.p.Problem != "":
			add(0, capital(sentence(pr.p.Problem)))
		case pr.p.Saved:
			add(0, "File saved "+day(pr.p.Entry.Saved, m.now())+", SHA-256 "+shortHash(pr.p.Entry.SHA256)+" ✓")
		case len(m.conf.failed) == 0:
			// After a failed download its Summary says this, and why.
			add(0, "Not saved yet: y downloads it first (Wi-Fi needed).")
		}
	}
	if pr != nil {
		for _, n := range pr.p.Notes {
			add(2, n)
		}
	}
	for _, s := range m.conf.failed {
		add(0, s)
	}
	if pr != nil && pr.hasBattery && pr.battery < lowBattery {
		if pr.battery < 0 {
			add(0, "The battery level can't be read.")
		} else {
			add(0, "Battery "+itoa(pr.battery)+"%. Flashing takes about a minute; plug the Zero in if you can.")
		}
	}
	gone := !m.listed(m.flow.port)
	if gone {
		add(0, "The board isn't plugged in now.")
	}
	shown := func(drop int) []string {
		var lines []string
		for _, p := range parts {
			switch {
			case p.optional != 0 && p.optional <= drop:
			case p.short != nil && drop >= p.shortAt:
				lines = append(lines, p.short...)
			default:
				lines = append(lines, p.lines...)
			}
		}
		return lines
	}
	drop := 0
	for drop < 4 && len(shown(drop)) > h {
		drop++
	}
	return confirmFit(shown(drop), h), m.confirmHint(w, pr, gone)
}

// confirmFit keeps Confirm's last h lines when nothing more can be left out: the frame would cut the bottom, where
// the file's check and the warnings are (the battery, a board not plugged in).
func confirmFit(lines []string, h int) []string {
	if len(lines) > h {
		return lines[len(lines)-h:]
	}
	return lines
}

func (m *Model) confirmHint(w int, pr *preparedMsg, gone bool) string {
	esp := m.flow.dev.Type == catalog.ESP32
	kind := m.flow.kind
	switch {
	case esp && kind == 0, esp && pr != nil && pr.p.Problem != "" && !gone:
		// No kind yet, or this kind refused: the other may do.
		return "u update  w full wipe  esc back  ? help"
	case gone, pr == nil, pr.p.Problem != "":
		return "esc back  ? help"
	case esp && pr.p.Saved:
		return "y flash  u update  w full wipe  ? help"
	case esp:
		return fitHint(w, "y download  u update  w full wipe  ? help", "y download  u update  w wipe  ? help")
	case pr.p.Saved:
		return "y flash  esc back  ? help"
	}
	return "y download  esc back  ? help"
}

func (m *Model) confirmHelp() []string {
	var out []string
	if m.flow.dev != nil && m.flow.dev.Type == catalog.ESP32 {
		out = append(out,
			keyHelp("u", "update (ESP32: keeps identity, contacts, pairing)"),
			keyHelp("w", "full wipe (ESP32: erases everything)"))
	}
	return append(out, keyHelp("y", "flash (downloads first if needed)"), keyHelp("esc", "back"))
}

// capital starts s with a capital letter: a Problem from the backend reads as a sentence on its own line.
func capital(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}
