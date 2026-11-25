package ui

// Phase 2 contract (docs/phase2-design.md): builder C owns this file. The methods hooks.go names keep their
// signatures. It is where the frame (frame.go, fixed) meets every screen but Setup: each hook here hands over to the
// screen showing, whose own file (board.go, choose.go, role.go, version.go, notes.go, confirm.go, download.go,
// flashing.go, result.go, update.go, files.go) holds its state, keys, view and messages. catalogview.go and ctext.go
// hold what several screens share.
//
// Rules kept here for every screen (hooks.go): Download and Flash are started in Update, only when canStart(), and
// watched in the same Update; every other Backend call is made in a tea.Cmd that captured its arguments first; no
// tea.Tick (ticks go through m.after); a picture of a Job these screens didn't start is ignored; m.setup is never
// touched.

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
	"meshflash/internal/flash"
)

// flow is what has been picked so far on the way to a flash: the port on Board, the board on Choose board, the role,
// the version, and on Confirm the kind (zero until u or w on an ESP32: there is no default).
type flow struct {
	port flash.Port
	node *flash.Node // the port's probe answer, if any (dropped after a failed flash: design 9.9)
	dev  *catalog.Device
	fw   *catalog.Firmware
	ver  *catalog.Version
	kind catalog.Kind
}

// modelMore is what the screens keep that belongs to no one screen.
type modelMore struct {
	// job is the ID of the Job these screens started (a download or a flash), 0 for none. jobProgress takes only its
	// pictures: the frame's own tests watch Jobs of theirs from the Board screen, and those must change nothing here.
	job uint64
	// tickOut says a tickMsg is on its way (m.after), so there is never more than one; tickGen tells it from an
	// older one.
	tickOut bool
	tickGen int
	// catRead says the first LoadCatalog has answered, so the Board can tell "no catalog" from "still reading it".
	catRead bool
	// views is what View works out from a catalog once rather than on every frame (the Zero's CPU is small, and
	// View runs after every message): the newest version and which boards list each file.
	views catalogViews
}

// tickMsg only redraws: every time on screen is computed from the clocks in View (S17).
type tickMsg struct{ gen int }

// catalogMsg is LoadCatalog's answer.
type catalogMsg struct {
	cat *catalog.Catalog
	err error
}

// start loads the catalog (hooks.go): the saved one, no network.
func (m *Model) start() tea.Cmd { return m.loadCatalog() }

// loadCatalog reads the saved catalog in a Cmd.
func (m *Model) loadCatalog() tea.Cmd {
	be := m.backend
	return func() tea.Msg {
		c, err := be.LoadCatalog()
		return catalogMsg{cat: c, err: err}
	}
}

// screenKey handles a key on every screen but Setup (hooks.go), after the frame's lock, Ctrl+C, Help, question and
// ? handling. A Tab reaching it is one press: no screen here gives Tab a meaning, so it does nothing.
func (m *Model) screenKey(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	switch m.screen {
	case scrBoard:
		cmd = m.boardKey(msg)
	case scrChoose:
		cmd = m.chooseKey(msg)
	case scrRole:
		cmd = m.roleKey(msg)
	case scrVersion:
		cmd = m.versionKey(msg)
	case scrNotes:
		cmd = m.notesKey(msg)
	case scrConfirm:
		cmd = m.confirmScreenKey(msg)
	case scrDownload:
		cmd = m.downloadKey(msg)
	case scrFlashing:
		cmd = m.flashingKey(msg)
	case scrResult:
		cmd = m.resultKey(msg)
	case scrUpdate:
		cmd = m.updateKey(msg)
	case scrFiles:
		cmd = m.filesKey(msg)
	}
	return tea.Batch(cmd, m.tick())
}

// screenView draws every screen but Setup in w×h cells (hooks.go), each fitting its own body.
func (m *Model) screenView(w, h int) ([]string, string) {
	switch m.screen {
	case scrBoard:
		return m.boardView(w, h)
	case scrChoose:
		return m.chooseView(w, h)
	case scrRole:
		return m.roleView(w, h)
	case scrVersion:
		return m.versionView(w, h)
	case scrNotes:
		return m.notesView(w, h)
	case scrConfirm:
		return m.confirmView(w, h)
	case scrDownload:
		return m.downloadView(w, h)
	case scrFlashing:
		return m.flashingView(w, h)
	case scrResult:
		return m.resultView(w, h)
	case scrUpdate:
		return m.updateView(w, h)
	case scrFiles:
		return m.filesView(w, h)
	}
	return nil, "? help"
}

// screenTitle is the status line's title (hooks.go; design 8.3).
func (m *Model) screenTitle() string {
	switch m.screen {
	case scrChoose:
		return "Which board is on " + m.flow.port.Name + "?"
	case scrRole:
		return m.devName()
	case scrVersion:
		return m.fwName() + " · " + m.devName()
	case scrNotes:
		if m.notes.ver != nil {
			return "Notes · " + m.notes.ver.Name
		}
		return "Notes"
	case scrConfirm:
		return "Flash this?"
	case scrDownload:
		return "Downloading " + m.fwName() + " " + m.verName()
	case scrFlashing:
		return "Flashing " + m.fwName() + " " + m.verName()
	case scrResult:
		if m.res.o.Title != "" {
			return m.res.o.Title
		}
		return "Flashing ended"
	case scrUpdate:
		return "Update the catalog"
	case scrFiles:
		return "Saved files"
	}
	return m.name
}

// screenSubject is the board the screen is about, for the status mark (hooks.go): Board's selected port, the board
// picked from Choose board to Flashing, where the board is now on Result (nil when it's gone), none on Update catalog
// and Saved files.
func (m *Model) screenSubject() (*flash.Port, bool) {
	switch m.screen {
	case scrBoard:
		if p, ok := m.boardSelected(); ok {
			return &p, true
		}
		return nil, false
	case scrChoose, scrRole, scrVersion, scrNotes, scrConfirm, scrDownload, scrFlashing:
		p := m.flow.port
		return &p, true
	case scrResult:
		return m.res.o.Board, true
	}
	return nil, false
}

// screenHelp is Help's list of the screen's keys (hooks.go): "key" padded to 15 runes, then what it does.
func (m *Model) screenHelp() []string {
	switch m.screen {
	case scrBoard:
		return m.boardHelp()
	case scrChoose:
		return m.chooseHelp()
	case scrRole:
		return []string{keyHelp("j/k", "move"), keyHelp("enter", "pick this role"), keyHelp("esc", "back")}
	case scrVersion:
		return []string{keyHelp("j/k", "move"), keyHelp("enter", "flash this version"),
			keyHelp("n", "release notes"), keyHelp("d", "download only (Wi-Fi)"), keyHelp("esc", "back")}
	case scrNotes:
		return []string{keyHelp("j/k", "scroll"), keyHelp("space/b", "a page"), keyHelp("esc", "back")}
	case scrConfirm:
		return m.confirmHelp()
	case scrDownload:
		return []string{keyHelp("esc", "stop the download")}
	case scrFlashing:
		return []string{keyHelp("esc", "stop (asks y: says what stopping leaves)")}
	case scrResult:
		return m.resultHelp()
	case scrUpdate:
		return m.updateHelp()
	case scrFiles:
		return []string{keyHelp("j/k", "move"), keyHelp("x", "delete (asks y)"), keyHelp("esc", "back")}
	}
	return nil
}

// screenTextBox says a text box is open (hooks.go): Choose board's find box.
func (m *Model) screenTextBox() bool { return m.screen == scrChoose && m.choose.boxOpen }

// screenConfirmText words a question (hooks.go). Only these screens' own questions, and the frame's confirmQuit,
// are worded here.
func (m *Model) screenConfirmText(k confirmKind) string {
	switch k {
	case confirmStop:
		return stopWords(m.flashRisk(), false)
	case confirmQuit:
		if !m.ownFlash() {
			// A flash this screen didn't start (the frame's tests watch one from Board): no picture of ours says
			// how far it is, so say only what is sure.
			return "Quit now? The flash under way stops the safe way, and meshflash quits."
		}
		return stopWords(m.flashRisk(), true)
	case confirmLowBattery:
		if m.conf.yPct < 0 {
			return "Battery level unknown. Flash anyway?"
		}
		return "Battery " + itoa(m.conf.yPct) + "%. Flash anyway?"
	case confirmUnchecked:
		if p := m.conf.current(); p != nil {
			return p.p.Ask
		}
	case confirmDelete:
		return "Delete " + m.files.del + "? It downloads again when needed."
	}
	return ""
}

// screenConfirm does a question after y (hooks.go; the frame does confirmQuit itself).
func (m *Model) screenConfirm(k confirmKind) tea.Cmd {
	var cmd tea.Cmd
	switch k {
	case confirmStop:
		m.stopFlash()
	case confirmLowBattery:
		cmd = m.afterLowBattery()
	case confirmUnchecked:
		cmd = m.startFlash()
	case confirmDelete:
		cmd = m.deleteFile()
	}
	return tea.Batch(cmd, m.tick())
}

// screenMsg takes builder C's own messages (hooks.go).
func (m *Model) screenMsg(msg tea.Msg) (tea.Cmd, bool) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tickMsg:
		if msg.gen != m.more.tickGen {
			return nil, true
		}
		m.more.tickOut = false
	case catalogMsg:
		m.more.catRead = true
		m.cat, m.catErr = msg.cat, msg.err
		if msg.err == nil && msg.cat == nil {
			m.catErr = catalog.ErrNoCatalog
		}
	case probedMsg:
		cmd = m.gotProbe(msg)
	case updatedMsg:
		cmd = m.gotUpdate(msg)
	case savedFilesMsg:
		m.gotSavedFiles(msg)
	case deletedMsg:
		cmd = m.gotDeleted(msg)
	case preparedMsg:
		m.gotPrepared(msg)
	case yBatteryMsg:
		cmd = m.gotYBattery(msg)
	default:
		return nil, false
	}
	return tea.Batch(cmd, m.tick()), true
}

// jobProgress takes a picture of a download or flash Job (hooks.go): only of the one these screens started, and at
// Done it moves on in the same Update (S16), locked or not.
func (m *Model) jobProgress(p flash.Progress) tea.Cmd {
	if m.more.job == 0 || p.Job != m.more.job {
		return nil
	}
	if p.Done {
		m.more.job = 0
	}
	var cmd tea.Cmd
	switch p.Kind {
	case flash.JobDownload:
		cmd = m.downloadProgress(p)
	case flash.JobFlash:
		cmd = m.flashProgress(p)
	}
	return tea.Batch(cmd, m.tick())
}

// afterScan is told of every scan (hooks.go): the Board's probes follow the ports, and the board picked follows its
// tty when it re-enumerates.
func (m *Model) afterScan(prev []flash.Port) tea.Cmd {
	m.followFlowPort()
	return tea.Batch(m.syncProbes(), m.tick())
}

// boardOpened is called when the Board shows again (hooks.go): what was picked is let go, and the ports not yet
// asked are asked.
func (m *Model) boardOpened() tea.Cmd {
	m.flow = flow{}
	return tea.Batch(m.syncProbes(), m.tick())
}

// cancelProbes stops every probe under way (hooks.go): before a flash or a setup, and at a quit.
func (m *Model) cancelProbes() { m.stopProbe() }

// tick sets the one tick that redraws a time on screen, while something timed shows (S17).
func (m *Model) tick() tea.Cmd {
	if m.more.tickOut || m.quit || !m.timedNow() {
		return nil
	}
	m.more.tickOut = true
	m.more.tickGen++
	return m.after(time.Second, tickMsg{gen: m.more.tickGen})
}

// timedNow says a time is on screen: a probe under way (Board, Choose board), Prepare checking the file (Confirm),
// the catalog being fetched, a download or a flash.
func (m *Model) timedNow() bool {
	switch m.screen {
	case scrBoard, scrChoose:
		return m.board.probing != ""
	case scrConfirm:
		return m.conf.current() == nil
	case scrUpdate:
		return m.upd.fetching
	case scrDownload, scrFlashing:
		return m.ownJob()
	}
	return false
}

// ownJob says the running Job is the one these screens started.
func (m *Model) ownJob() bool { return m.job != nil && m.more.job != 0 && m.job.ID() == m.more.job }

// ownFlash says the running Job is a flash these screens started.
func (m *Model) ownFlash() bool { return m.ownJob() && m.job.Kind() == flash.JobFlash }

// startedJob keeps the Job these screens just started, and watches it (the frame's watch, in the same Update). The
// screen moves to it only once it is watched (ownJob): watch refuses a second Job, which canStart already rules out.
func (m *Model) startedJob(j *flash.Job) tea.Cmd {
	if j == nil {
		return nil
	}
	cmd := m.watch(j)
	if m.job == j {
		m.more.job = j.ID()
	}
	return cmd
}
