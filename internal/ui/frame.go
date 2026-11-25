// Package ui is meshflash's screens, as one Bubble Tea model (SPEC.md 3-5): Board, Choose board, Role, Version,
// Notes, Confirm, Download, Flashing, Result, Setup, Update catalog, Saved files and Help, and the screen lock over
// them all, designed for the CLI app's 45×10 grid, still usable at 40×10, and drawn at 53×14 on the LCD (S10). It
// never does serial or network I/O itself: it asks a Backend (a *flash.Backend) and watches long work through a
// *flash.Job.
//
// Its shape is the CardputerZero app kit's internal/ui (code/internal/ui/model.go and help.go, the owner's own code):
// one Model, one Update, View laying out the status line, the body, a message and the key hints.
package ui

// Phase 2 contract (docs/phase2-design.md): this file is fixed and complete (the integrator's). It is the frame every
// screen is drawn in and the order every message is handled in (design 8.1, 8.2), written once so that builder C
// (the screens in model.go and its files) and builder D (Setup) can each test their own screens through it before
// the other's code exists. It calls builder C's screen methods (screenKey, screenView, …, placeholders in model.go)
// and builder D's setup methods (setup.go) as hooks.go lists them, and the screen lock (lock.go, fixed too) above them
// all. Nobody adds a type, func or var whose name starts with "frame" or "lock", and nobody redeclares a name defined
// here.

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
	// heldYGap: a y less than this after the previous y is taken for a held y repeating: it answers no question, the
	// question stays, and it starts nothing, so release and press again (owner question 5, for every y question:
	// Confirm's, the battery's, the SoftDevice's, Setup's). Keys between the two don't matter: a deliberate y never
	// follows another that fast. A held key is taken to repeat every 33 ms after 250 ms, Linux's default, on the LCD's
	// keyboard and in the CLI app's terminal alike (UNVERIFIED: Phase 3 measures both, docs/phase2-design.md 18.9).
	heldYGap = 500 * time.Millisecond
	// scrollStep is how many lines j and k scroll text: Help, Notes. Two, as Loom's (the owner found one line slow
	// going there; owner question 15 confirms it for meshflash). Result and Setup's end scroll a paragraph at a time
	// (endview.go).
	scrollStep = 2
	// escRepeatGap: in a terminal (Options.TerminalKeys), which sends a held key again and again and never its
	// release, an Esc less than this after the previous Esc, no other key between, is a held Esc repeating, and is
	// dropped, as a held Tab's repeats are (lock.go). Else holding Esc for the launcher's quit (3 s) would walk back
	// through every screen to the Board and quit in a third of a second, losing a text box's text that its first
	// tap cancelled (SPEC 8: kept 3.5 s for the SIGTERM), and answer a "Stop now?" its first tap opened. So two Escs
	// pressed less than 0.5 s apart act as one there. On the LCD internal/screen sends Esc only on its press.
	escRepeatGap = 500 * time.Millisecond
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
		showBattery: o.ShowBattery, terminalKeys: o.TerminalKeys, nowFn: o.Now, afterFn: o.After, waitFn: o.Wait}
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

// Update handles one message, in this order (the kit's): the size; a key (the lock, Ctrl+C, Help, a question, ?, the
// screen); a Job's picture; the frame's scans and battery; the lock's own; builder C's messages; builder D's.
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
	case LockMsg:
		return m, m.lockNow()
	case lockDarkMsg:
		return m, m.lockDarken()
	}
	if cmd, ok := m.screenMsg(msg); ok {
		return m, cmd
	}
	if cmd, ok := m.setupResult(msg); ok {
		return m, cmd
	}
	return m, nil
}

// key handles a key: while locked only the lock sees it, and only as presses (lock.go); else a held Tab (in a
// terminal) first, then Ctrl+C, then Help if open, then a question, then ? (outside a text box), then the screen. The
// hold's first Tab is an ordinary press: it answers an open question no and closes Help before the lock comes.
func (m *Model) key(msg tea.KeyMsg) tea.Cmd {
	if m.quit {
		return nil
	}
	isY := msg.String() == "y"
	now := m.now()
	m.yHeld = isY && !m.lastY.IsZero() && now.Sub(m.lastY) < heldYGap
	if isY {
		m.lastY = now
	}
	held := m.lockTab(msg, now)
	if m.lock.on {
		if held != lockTabNone {
			// A held Tab's repeats, the one that locked included, are not presses: they neither arm the unlock nor
			// keep the page lit, as on the LCD, where internal/screen never sends them.
			return nil
		}
		return m.lockKey(msg)
	}
	switch held {
	case lockTabRepeat:
		return nil // a held Tab acts once, as its first press
	case lockTabHeld:
		return m.lockNow()
	}
	if m.escRepeat(msg, now) {
		return nil
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

// escRepeat says msg is a held Esc repeating in a terminal (escRepeatGap), which acts as nothing.
func (m *Model) escRepeat(msg tea.KeyMsg, now time.Time) bool {
	if !m.terminalKeys {
		return false
	}
	if msg.Type != tea.KeyEsc {
		m.lastEsc = time.Time{}
		return false
	}
	repeat := !m.lastEsc.IsZero() && now.Sub(m.lastEsc) < escRepeatGap
	m.lastEsc = now
	return repeat
}

// ctrlC quits at once, but asks y first while a flash runs (S20: stopping one can leave the board with no working
// firmware). A setup being sent is stopped where it is, with no question (owner question 16): quitNow writes to
// screen.log what the node has been sent and what it is left with, and internal/app adds how the setup ended.
func (m *Model) ctrlC() tea.Cmd {
	if m.job != nil && m.job.Kind() == flash.JobFlash {
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
	m.jobSeenAt, m.lock.seen = m.now(), p
	var wait tea.Cmd
	if p.Done {
		if k := m.job.Kind(); k == flash.JobFlash || k == flash.JobSetup {
			m.busy--
		}
		m.lockJobDone(p)
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
	m.job, m.jobSeenAt, m.lock.seen = j, m.now(), flash.Progress{Job: j.ID(), Kind: j.Kind()}
	if k := j.Kind(); k == flash.JobFlash || k == flash.JobSetup {
		m.busy++
	}
	return m.waitFn(j, 0)
}

// quitNow ends the program: what's unsent saved, probes cancelled, the Job asked to stop the safe way, with a line in
// screen.log saying what that leaves the board with (internal/app waits for it, bounded, through Shutdown, and writes
// how it ended).
func (m *Model) quitNow() tea.Cmd {
	m.SaveUnsent()
	m.cancelProbes()
	if m.job != nil {
		m.logStop("quit")
		m.job.Cancel()
	}
	m.quit = true
	return tea.Quit
}

// logStop writes to screen.log, as a quit stops the running flash or setup, where it was and what stopping there
// leaves the board with (the latest picture's Risk), so the log says it even if the Job doesn't finish before the
// program ends; internal/app then adds how it ended (its Outcome's Summary, State and Recovery). A step's words never
// hold a password (flash.Step.Text).
func (m *Model) logStop(why string) {
	k := m.job.Kind()
	if k != flash.JobFlash && k != flash.JobSetup {
		return
	}
	p := m.job.Snapshot()
	if p.Done {
		return
	}
	doing := lockVerbs[k]
	if n := len(p.Steps); n > 0 {
		at := fmt.Sprintf("step %d", n)
		if p.Planned > 0 {
			at += fmt.Sprintf(" of %d", p.Planned)
		}
		doing += " (" + at + ": " + p.Steps[n-1].Text + ")"
	}
	m.logf("%s: %s while %s: stopping it the safe way. %s", m.name, why, strings.ToLower(doing[:1])+doing[1:],
		stopLeaves(k, p.Risk))
}

// stopLeaves says what stopping a flash or a setup now leaves the board with, by the picture's Risk.
func stopLeaves(k flash.JobKind, r flash.Risk) string {
	if k == flash.JobSetup {
		switch r {
		case flash.RiskNone:
			return "Nothing had been saved on the node."
		case flash.RiskSettings:
			return "The node keeps the settings it took so far, still on its old radio: set it up again to finish."
		case flash.RiskRadioSent:
			return "Its new radio is saved but it hasn't restarted, so it stays on its old radio until it does: " +
				"set it up again to finish, or restart it to switch."
		case flash.RiskRestarted:
			return "It restarted with the new settings; only the check after is skipped."
		}
		return ""
	}
	switch r {
	case flash.RiskNone:
		return "Nothing had been written: the board keeps its old firmware."
	case flash.RiskErased:
		return "It has no working firmware until it is flashed again."
	case flash.RiskWritten:
		return "It was already written: only the check is skipped."
	case flash.RiskBootloader:
		return "Nothing had been written, but it may be in its bootloader: press its reset button once to start " +
			"its old firmware."
	}
	return ""
}

// SaveUnsent keeps what was typed and not sent (the Setup form) as a draft, so a quit, or the SIGTERM a held Esc
// sends, never loses it (S7). It may run more than once (a quit, then Shutdown).
func (m *Model) SaveUnsent() {
	if m.screen == scrSetup {
		m.saveSetupDraft()
	}
}

// Shutdown is the end of the program, however it ended (internal/app calls it after Run returns: the model never
// sees an Esc-hold's or a SIGTERM's quit, locked or not): it saves what's unsent, clears Setup's passwords, cancels
// probes and the running Job (with logStop's line, unless the model's own quit already wrote it), and returns that
// Job (nil when none) so the app can wait a bounded time for it to stop and write how it ended (its Outcome's
// Summary, State and Recovery) to screen.log.
func (m *Model) Shutdown() *flash.Job {
	m.SaveUnsent()
	m.closeSetup()
	m.cancelProbes()
	j := m.job
	if j != nil {
		if !m.quit {
			m.logStop("told to end (Esc held, or SIGTERM)")
		}
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

// listed says p is plugged in now: the same board is in the last scan, by the backend's own rule (flash.SameBoard:
// usbid.SameBoard, or, for a port it can't follow, no useful serial and not a bridge, the same tty with the same
// identity in the same socket), so the mark never shows ● for a board the backend would say isn't plugged in.
func (m *Model) listed(p flash.Port) bool {
	for _, q := range m.scan.Ports {
		if flash.SameBoard(p, q) {
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
	var title string
	switch {
	case m.help:
		title = "Help"
	case m.screen == scrSetup:
		title = m.setupTitle()
	default:
		title = m.screenTitle()
	}
	return m.statusLineTitled(w, title)
}

// statusLineTitled is the status line with title on the left (the lock page's is "<Name> · locked").
func (m *Model) statusLineTitled(w int, title string) string {
	right := m.mark()
	if m.showBattery && m.hasBattery && m.battery >= 0 {
		right = fmt.Sprintf("%d%% %s", m.battery, right)
	}
	title = cut(title, w-width(right)-1)
	return title + strings.Repeat(" ", max(1, w-width(title)-width(right))) + right
}

// View draws the screen: the status line, the body, any message, and the key hints, exactly h lines of at most w
// cells (S10). The body is cut from the bottom to fit, so each screen fits its own (a step list shows its end, a
// long text scrolls): the frame never wraps or scrolls a body for it. While locked it draws the lock page instead,
// or nothing once that has gone dark (lock.go).
func (m *Model) View() string {
	if m.quit {
		return ""
	}
	w, h := m.size()
	if m.lock.on {
		return m.lockView(w, h)
	}
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

// helpLines are Help's entries: the screen's keys ("key" padded to 15 runes, then what it does), the lock, Ctrl+C,
// then the footer (S19: the name and version, the servers, the folders, the lock and the Esc hold, the dialout
// command).
func (m *Model) helpLines() (keys, footer []string) {
	if m.screen == scrSetup {
		keys = m.setupHelp()
	} else {
		keys = m.screenHelp()
	}
	keys = append(keys,
		"hold tab       lock the screen (3 s); tab then enter unlocks",
		"Ctrl+C         quit, from anywhere (asks first while flashing; a setup stops where it is)")
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
			"what it runs, on the Board and Choose board screens, and to flash or set it up.",
		"Keeps its settings in ~/.config/" + m.name + " and its catalog, files and screen.log in ~/.cache/" +
			m.name + ".",
		"Reads the messenger's radio from ~/.config/mesh/config.json; never writes it.",
		m.lockHelp(),
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
