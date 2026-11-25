// Package ui is meshflash's screens, as one Bubble Tea model (SPEC.md 3-5): Board, Choose board, Role, Version,
// Notes, Confirm, Download, Flashing, Result, Setup, Update catalog, Saved files and Help, designed for the CLI app's
// 45×10 grid, still usable at 40×10, and drawn at 53×14 on the LCD (S10). It never does serial or network I/O
// itself: it asks a Backend (a *flash.Backend) and watches long work through a *flash.Job.
//
// Its shape is the CardputerZero app kit's internal/ui (code/internal/ui/model.go and help.go, the owner's own code):
// one Model, one Update, View laying out the status line, the body, a message and the key hints.
package ui

// Phase 2 contract (docs/phase2-design.md): this file is fixed and complete (the integrator's). It is the frame every
// screen is drawn in and the order every message is handled in (design 8.1, 8.2), written once so that builder C
// (the screens in model.go and its files) and builder D (Setup) can each test their own screens through it before
// the other's code exists. It calls builder C's screen methods (screenKey, screenView, …, placeholders in model.go)
// and builder D's setup methods (setup.go) as hooks.go lists them. Nobody adds a type, func or var whose name starts
// with "frame", and nobody redeclares a name defined here.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/flash"
	"meshflash/internal/store"
	"meshflash/internal/usbid"
)

const (
	// scanEvery is how often the frame reads sysfs for the USB ports (Backend.Scan, which opens no port): on every
	// screen, so the status line's mark follows the board (S13, S16). Help says so.
	scanEvery = time.Second
	// batteryEvery is how often the status line's battery is read again while it shows (ShowBattery).
	batteryEvery = 30 * time.Second
	// heldYGap: a y less than this after the previous key, a y too, is a held key repeating (the LCD's keyboard
	// repeats every 33 ms after 250 ms, and so does the CLI app's terminal): it answers no question and starts
	// nothing, so release and press again (owner question 5).
	heldYGap = 500 * time.Millisecond
	// scrollStep is how many lines j and k scroll text: Help, Notes, Result. Two, as Loom's (the owner found one line
	// slow going there; owner question 15 confirms it for meshflash).
	scrollStep = 2
)

// The frame's own messages.
type (
	frameScanMsg    struct{ gen int }
	frameScannedMsg struct {
		gen  int
		scan flash.Scan
		err  error
	}
	frameBatteryTickMsg struct{}
	frameBatteryMsg     struct {
		percent int
		ok      bool
	}
)

// New makes the program's model.
func New(o Options) *Model {
	m := &Model{backend: o.Backend, store: o.Store, cfg: o.Config, name: o.Name, version: o.Version, log: o.Log,
		showBattery: o.ShowBattery, nowFn: o.Now, afterFn: o.After, waitFn: o.Wait}
	if m.name == "" {
		m.name = "meshflash"
	}
	if m.nowFn == nil {
		m.nowFn = time.Now
	}
	if m.afterFn == nil {
		m.afterFn = func(d time.Duration, msg tea.Msg) tea.Cmd {
			return tea.Tick(d, func(time.Time) tea.Msg { return msg })
		}
	}
	if m.waitFn == nil {
		m.waitFn = func(j *flash.Job, seen uint64) tea.Cmd {
			return func() tea.Msg {
				p, _ := j.Next(context.Background(), seen)
				return p
			}
		}
	}
	return m
}

// Init looks for boards, reads the battery, and does what the screens do at start (builder C's start: the catalog).
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.rescan(), m.readBattery(), m.start())
}

// Update handles one message, in this order (the kit's): the size; a key (Ctrl+C, Help, a question, ?, the screen);
// a Job's picture; the frame's scans and battery; builder C's messages; builder D's.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		return m, m.key(msg)
	case flash.Progress:
		return m, m.progress(msg)
	case frameScanMsg:
		if msg.gen != m.scanGen || m.quit {
			return m, nil
		}
		return m, m.scanCmd()
	case frameScannedMsg:
		return m, m.gotScan(msg)
	case frameBatteryTickMsg:
		if m.quit {
			return m, nil
		}
		return m, m.readBattery()
	case frameBatteryMsg:
		m.battery, m.hasBattery = msg.percent, msg.ok
		return m, m.after(batteryEvery, frameBatteryTickMsg{})
	}
	if cmd, ok := m.screenMsg(msg); ok {
		return m, cmd
	}
	if cmd, ok := m.setupResult(msg); ok {
		return m, cmd
	}
	return m, nil
}

// key handles a key: Ctrl+C first, then Help if open, then a question, then ? (outside a text box), then the screen.
func (m *Model) key(msg tea.KeyMsg) tea.Cmd {
	if m.quit {
		return nil
	}
	isY := msg.String() == "y"
	now := m.now()
	m.yHeld = isY && !m.lastY.IsZero() && now.Sub(m.lastY) < heldYGap
	if isY {
		m.lastY = now
	} else {
		m.lastY = time.Time{}
	}
	if msg.Type == tea.KeyCtrlC {
		return m.ctrlC()
	}
	m.note = ""
	if m.help {
		return m.helpKey(msg)
	}
	if m.confirm != noConfirm {
		return m.confirmKey(msg)
	}
	if msg.String() == "?" && !m.textBoxOpen() {
		m.help, m.helpTop = true, 0
		return nil
	}
	if m.screen == scrSetup {
		return m.setupKey(msg)
	}
	return m.screenKey(msg)
}

// ctrlC quits, asking first while a flash or a setup is under way (owner question 16: a setup asks too).
func (m *Model) ctrlC() tea.Cmd {
	if m.job != nil && m.job.Kind() != flash.JobDownload {
		m.ask(confirmQuit)
		return nil
	}
	return m.quitNow()
}

// confirmKey answers the open question (S20): y does it, any other key says no and does nothing else, and a held y
// leaves the question as it is.
func (m *Model) confirmKey(msg tea.KeyMsg) tea.Cmd {
	if m.yHeld {
		return nil
	}
	k := m.confirm
	m.confirm = noConfirm
	if msg.String() != "y" {
		return nil
	}
	if k == confirmQuit {
		return m.quitNow()
	}
	if m.screen == scrSetup {
		return m.setupConfirm(k)
	}
	return m.screenConfirm(k)
}

// ask opens a question (closing Help, so it shows).
func (m *Model) ask(k confirmKind) {
	m.confirm, m.confirmAt, m.help = k, m.now(), false
}

// confirmText is the open question's words: Setup's on scrSetup, the other screens' elsewhere.
func (m *Model) confirmText() string {
	if m.screen == scrSetup {
		return m.setupConfirmText(m.confirm)
	}
	return m.screenConfirmText(m.confirm)
}

// textBoxOpen says a text box has the keys: ? (and y, j, k) are then letters.
func (m *Model) textBoxOpen() bool {
	if m.screen == scrSetup {
		return m.setupEditing()
	}
	return m.screenTextBox()
}

// progress takes a Job's picture: a stale one (not m.job's) is dropped; otherwise the next one is waited for, or at
// Done the Job is let go, Help closed and a stop or quit question dropped (it no longer means anything: S16), and
// then the picture goes to the screen that started the Job (Setup's to builder D, the rest to builder C).
func (m *Model) progress(p flash.Progress) tea.Cmd {
	if m.job == nil || p.Job != m.job.ID() {
		return nil
	}
	m.jobSeenAt = m.now()
	var wait tea.Cmd
	if p.Done {
		if k := m.job.Kind(); k == flash.JobFlash || k == flash.JobSetup {
			m.busy--
		}
		m.job, m.help = nil, false
		switch m.confirm {
		case confirmStop, confirmQuit, confirmStopSetup:
			m.confirm = noConfirm
		}
	} else {
		wait = m.waitFn(m.job, p.Seq)
	}
	var next tea.Cmd
	if p.Kind == flash.JobSetup {
		next = m.setupProgress(p)
	} else {
		next = m.jobProgress(p)
	}
	return tea.Batch(wait, next)
}

// canStart says a Job may be started now: call Download, Flash or Setup only when it does, then watch the Job in
// the same Update.
func (m *Model) canStart() bool { return m.job == nil && !m.quit }

// watch makes j the running Job (m.job; m.busy++ for a flash or a setup) and returns the Cmd that waits for its
// first picture. A second Job while one runs is a bug: it is stopped at once, logged, and not watched.
func (m *Model) watch(j *flash.Job) tea.Cmd {
	if j == nil {
		return nil
	}
	if m.job != nil {
		j.Cancel()
		m.logf("meshflash: a second job was started while one ran; it was stopped")
		return nil
	}
	m.job, m.jobSeenAt = j, m.now()
	if k := j.Kind(); k == flash.JobFlash || k == flash.JobSetup {
		m.busy++
	}
	return m.waitFn(j, 0)
}

// quitNow ends the program: what's unsent saved, probes cancelled, the Job asked to stop (internal/app waits for
// it, bounded, through Shutdown).
func (m *Model) quitNow() tea.Cmd {
	m.SaveUnsent()
	m.cancelProbes()
	if m.job != nil {
		m.job.Cancel()
	}
	m.quit = true
	return tea.Quit
}

// SaveUnsent keeps what was typed and not sent (the Setup form) as a draft, so a quit, or the SIGTERM a held Esc
// sends, never loses it (S7). It may run more than once (a quit, then Shutdown).
func (m *Model) SaveUnsent() {
	if m.screen == scrSetup {
		m.saveSetupDraft()
	}
}

// Shutdown is the end of the program, however it ended (internal/app calls it after Run returns: the model never
// sees an Esc-hold's or a SIGTERM's quit): it saves what's unsent, clears Setup's passwords, cancels probes and the
// running Job, and returns that Job (nil when none) so the app can wait a bounded time for it to stop and write how
// it ended (its Outcome's State and Recovery) to screen.log.
func (m *Model) Shutdown() *flash.Job {
	m.SaveUnsent()
	m.closeSetup()
	m.cancelProbes()
	j := m.job
	if j != nil {
		j.Cancel()
	}
	return j
}

// Running is the running Job, not stopped: for internal/app when the program ended by a panic or a lost terminal
// and the owner chose to let a flash finish (owner question 19). The app calls SaveUnsent first.
func (m *Model) Running() *flash.Job { return m.job }

// toBoard goes back to the Board screen (ending Setup if it was open, rescanning), with note shown once.
func (m *Model) toBoard(note string) tea.Cmd {
	if m.screen == scrSetup {
		m.closeSetup()
	}
	m.screen, m.help, m.confirm, m.note = scrBoard, false, noConfirm, note
	return tea.Batch(m.rescan(), m.boardOpened())
}

// rescan scans now and every scanEvery after, dropping a scan under way (r on Board, toBoard).
func (m *Model) rescan() tea.Cmd {
	m.scanGen++
	return m.scanCmd()
}

func (m *Model) scanCmd() tea.Cmd {
	gen, be := m.scanGen, m.backend
	return func() tea.Msg {
		s, err := be.Scan()
		return frameScannedMsg{gen: gen, scan: s, err: err}
	}
}

// gotScan keeps a scan, tells builder C's screens (afterScan: the Board's probes, a Confirm whose board went), and
// sets the next one.
func (m *Model) gotScan(msg frameScannedMsg) tea.Cmd {
	if msg.gen != m.scanGen || m.quit {
		return nil
	}
	prev := m.scan.Ports
	m.scanned = true
	if msg.err != nil {
		m.scanErr = msg.err
	} else {
		m.scan, m.scanErr = msg.scan, nil
	}
	return tea.Batch(m.afterScan(prev), m.after(scanEvery, frameScanMsg{gen: msg.gen}))
}

func (m *Model) readBattery() tea.Cmd {
	if !m.showBattery {
		return nil
	}
	be := m.backend
	return func() tea.Msg {
		p, ok := be.Battery()
		return frameBatteryMsg{percent: p, ok: ok}
	}
}

// listed says p is plugged in now: the same board (usbid.SameBoard) is in the last scan, or, for a port SameBoard
// can't follow (no useful serial, not a bridge), the same tty with the same identity in the same socket.
func (m *Model) listed(p flash.Port) bool {
	for _, q := range m.scan.Ports {
		if usbid.SameBoard(p.Port, q.Port) ||
			(p.Name == q.Name && p.VID == q.VID && p.PID == q.PID && p.USBPath == q.USBPath) {
			return true
		}
	}
	return false
}

// mark is the status line's board mark (S13, the owner's "the board"): … while a board operation is under way;
// else, on a screen about one board (screenSubject, setupSubject), ● while that board is listed and ○ when it isn't
// (or is gone: a nil subject); on a screen about no board, ● when any port is listed, ○ when none is.
func (m *Model) mark() string {
	if m.busy > 0 {
		return "…"
	}
	var p *flash.Port
	var named bool
	if m.screen == scrSetup {
		p, named = m.setupSubject()
	} else {
		p, named = m.screenSubject()
	}
	switch {
	case !named:
		if len(m.scan.Ports) > 0 {
			return "●"
		}
	case p != nil && m.listed(*p):
		return "●"
	}
	return "○"
}

// statusLine is the title on the left and, on the right, the battery (on the LCD) and the board's mark.
func (m *Model) statusLine(w int) string {
	right := m.mark()
	if m.showBattery && m.hasBattery && m.battery >= 0 {
		right = fmt.Sprintf("%d%% %s", m.battery, right)
	}
	var title string
	switch {
	case m.help:
		title = "Help"
	case m.screen == scrSetup:
		title = m.setupTitle()
	default:
		title = m.screenTitle()
	}
	title = cut(title, w-width(right)-1)
	return title + strings.Repeat(" ", max(1, w-width(title)-width(right))) + right
}

// View draws the screen: the status line, the body, any message, and the key hints, exactly h lines of at most w
// cells (S10). The body is cut from the bottom to fit, so each screen fits its own (a step list shows its end, a
// long text scrolls): the frame never wraps or scrolls a body for it.
func (m *Model) View() string {
	if m.quit {
		return ""
	}
	w, h := m.size()
	var msgLines []string
	if m.confirm != noConfirm {
		msgLines = wrap(m.confirmText(), w)
	} else if m.note != "" {
		msgLines = wrap(m.note, w)
	}
	bodyH := max(h-2-len(msgLines), 1)
	var body []string
	var hint string
	switch {
	case m.help:
		body, hint = m.helpView(w, bodyH)
	case m.screen == scrSetup:
		body, hint = m.setupView(w, bodyH)
	default:
		body, hint = m.screenView(w, bodyH)
	}
	if m.confirm != noConfirm {
		hint = "y yes  any other key no"
	}
	if len(body) > bodyH {
		body = body[:bodyH]
	}
	for len(body) < bodyH {
		body = append(body, "")
	}
	lines := make([]string, 0, h)
	lines = append(lines, m.statusLine(w))
	lines = append(lines, body...)
	lines = append(lines, msgLines...)
	lines = append(lines, cut(hint, w))
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	return strings.Join(lines, "\n")
}

// helpLines are Help's entries: the screen's keys ("key" padded to 15 runes, then what it does), Ctrl+C, then the
// footer (S19: the name and version, the servers, the folders, the dialout command).
func (m *Model) helpLines() (keys, footer []string) {
	if m.screen == scrSetup {
		keys = m.setupHelp()
	} else {
		keys = m.screenHelp()
	}
	keys = append(keys, "Ctrl+C         quit, from anywhere (asks first while flashing or setting up)")
	hint := m.scan.Hint
	if hint == "" {
		hint = usbid.DialoutHint
	}
	footer = []string{
		"",
		m.name + " " + m.version + " for CardputerZero",
		"Catalog and files: flasher.meshcore.io, zephcore.meshcore.dev; GitHub when a MeshCore file isn't on the " +
			"flasher site. A catalog update downloads about 1.5 MB.",
		"Looks for boards on USB every second, reading only what Linux lists; it opens a port only to ask a board " +
			"on the Board screen what it runs.",
		"Keeps its settings in ~/.config/" + m.name + " and its catalog, files and screen.log in ~/.cache/" +
			m.name + ".",
		"Reads the messenger's radio from ~/.config/mesh/config.json; never writes it.",
		"Serial ports need the dialout group. Run these once:",
	}
	footer = append(footer, strings.Split(hint, "\n")...)
	return keys, footer
}

func (m *Model) helpView(w, h int) ([]string, string) {
	keys, footer := m.helpLines()
	lines := []string{heading("Keys")}
	for _, l := range keys {
		lines = append(lines, wrapIndent(l, w)...)
	}
	for _, l := range footer {
		lines = append(lines, wrap(l, w)...)
	}
	m.helpTop = min(max(m.helpTop, 0), max(0, len(lines)-h))
	return lines[m.helpTop:min(len(lines), m.helpTop+h)],
		fitHint(w, "j/k scroll  space page  any other key back", "j/k scroll  space page  other keys back")
}

func (m *Model) helpKey(msg tea.KeyMsg) tea.Cmd {
	_, h := m.size()
	page := max(1, h-3)
	switch msg.String() {
	case "j", "down":
		m.helpTop += scrollStep
	case "k", "up":
		m.helpTop -= scrollStep
	case " ":
		m.helpTop += page
	case "b":
		m.helpTop -= page
	default:
		m.help = false
	}
	return nil
}

// wrapIndent wraps a "key           what it does" line, indenting what follows under the description. From the kit's
// help.go.
func wrapIndent(s string, w int) []string {
	const col = 15
	r := []rune(s)
	if len(r) > col && r[col-1] == ' ' && w > col+10 {
		lines := wrap(string(r[col:]), w-col)
		out := []string{string(r[:col]) + lines[0]}
		for _, l := range lines[1:] {
			out = append(out, strings.Repeat(" ", col)+l)
		}
		return out
	}
	return wrap(s, w)
}

// after returns a Cmd that brings msg back after d (Options.After). Call it only in Update, never in a Cmd.
func (m *Model) after(d time.Duration, msg tea.Msg) tea.Cmd { return m.afterFn(d, msg) }

// now is the screens' clock (Options.Now).
func (m *Model) now() time.Time { return m.nowFn() }

// size is the grid: 45×10 (the CLI app) until the first tea.WindowSizeMsg.
func (m *Model) size() (int, int) {
	w, h := m.w, m.h
	if w <= 0 {
		w = 45
	}
	if h <= 0 {
		h = 10
	}
	return w, h
}

// logf writes one line to screen.log. Never give it a password.
func (m *Model) logf(format string, args ...any) {
	if m.log != nil {
		fmt.Fprintf(m.log, format+"\n", args...)
	}
}

// SavedRadio is the radio a setup sent, as config.json keeps it: for builder D's Setup when its Job ends, and for
// internal/app when a quit cut the Job off and its Outcome.Sent says the node took the radio anyway.
func SavedRadio(s flash.Sent) store.Radio {
	return store.Radio{FreqMHz: s.Radio.FreqMHz, BWkHz: s.Radio.BWkHz, SF: s.Radio.SF, CR: s.Radio.CR,
		TxDBm: s.TxDBm, PathHashBytes: s.PathHashBytes}
}
