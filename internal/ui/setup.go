package ui

// The Setup screen (SPEC.md 4 "Setup", 7; docs/phase2-design.md 10): read a MeshCore repeater or room server's
// settings over its serial console, show them as a form the owner edits, send them as one setup Job, and show how
// it ended. Builder D's: this file holds the hooks the frame calls (hooks.go) and setupScreen; the form is in
// setupform.go, drafts in setupdraft.go, sending and the end in setupsend.go, the preset list in presets.go.
//
// The admin password is typed each time, sent to the node and dropped (CLAUDE.md): it lives only in an editor's
// runes, which are wiped (editor.Clear) as soon as the setup request is built, when the screen closes and when a
// password box is cancelled. It never reaches the screen (rows show "••••", the box one • per character),
// screen.log, a draft, config.json or a progress message. The guest password, read from a room server, is kept the
// same way, masked from the start.
//
// Every top-level name here, methods on Model included, starts with setup, preset or pick (design section 11), so
// builder C's names can never clash with them.

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/flash"
	"meshflash/internal/nodecli"
	"meshflash/internal/usbid"
)

// setupMode is where the Setup screen is (design 10.2).
type setupMode int

const (
	setupClosed     setupMode = iota // not open: never opened, or closed by closeSetup
	setupReading                     // ReadNode under way (m.busy held until its answer comes)
	setupReadFailed                  // the node can't be set up: the Node's Problem in words
	setupForm                        // the rows, one focused
	setupEditing                     // a text box open on the focused row
	setupPicking                     // a pick list open on the focused row
	setupPresets                     // MeshCore's presets, to fill the radio rows
	setupSending                     // the setup Job running
	setupDone                        // it worked
	setupFailed                      // it stopped: a step failed, the board went, or the owner stopped it
)

const (
	// setupCancelKeep is how long the text of a text box Esc just cancelled stays that row's draft value: an Esc
	// hold reaches the model as one Esc tap first, then the SIGTERM's Shutdown saves the draft, which should hold
	// the half-typed line (S7; design 10.6). The launcher's hold is 3 s.
	setupCancelKeep = 3500 * time.Millisecond
	// setupTickEvery is how often the screen redraws while reading or sending, so the elapsed time moves (S17).
	setupTickEvery = time.Second
)

// setupScreen is the Setup screen's state: reading the node, the form (its rows, the focused one, a text box or
// pick list open, the presets list, a just-cancelled text box's text), the setup under way, and its end.
type setupScreen struct {
	mode setupMode
	// gen tells this opening's ReadNode answer and tick chain from an earlier one's; it only grows, across openings.
	gen int
	// The board: the port as the screen that opened Setup saw it, the catalog board ("" when not known), and
	// whether it was just flashed (ReadNode then waits longer for the console).
	port      flash.Port
	device    string
	firstBoot bool
	// Reading: when it began, and the cancel of ReadNode's context.
	readAt time.Time
	cancel context.CancelFunc
	// node is ReadNode's answer, its Current.GuestPassword dropped once copied into guest.
	node flash.Node
	// What the form opened with: the Zero's clock synchronised, the messenger file's note, the TX power cap's note.
	synced        bool
	messengerNote string
	capNote       string
	// The form: each row's default text and current text (the passwords' are in admin and guest, never here),
	// each row's problem, the focused row and the scroll offset.
	defs     [setupRowCount]string
	vals     [setupRowCount]string
	problems [setupRowCount]string
	focus    setupRow
	top      int
	// The passwords, as runes Clear can wipe. admin starts empty; guest (a room server's) starts as the node's.
	admin, guest editor
	// The open text box (setupEditing) on focus: a copy of the row's text, kept only by Enter or Tab.
	box editor
	// cancelled is the text a text box held when Esc cancelled it, kept as the draft's for setupCancelKeep.
	cancelled setupCancelled
	// The open pick list (setupPicking) on focus: the option selected and the scroll offset.
	pick, pickTop int
	// The presets list (setupPresets): the preset selected and the scroll offset.
	preset, presetTop int
	// Sending: the Job's ID (pictures of any other Job are ignored), its latest picture, whether the owner asked it
	// to stop, and when it was started.
	jobID    uint64
	picture  flash.Progress
	stopping bool
	sentAt   time.Time
	// The end: how it ended, and the scroll of its Summary (endview.go).
	outcome flash.Outcome
	end     endScroll
}

// setupCancelled is a text box's text as Esc cancelled it (design 10.6).
type setupCancelled struct {
	ok   bool
	row  setupRow
	text string
	at   time.Time
}

// Setup's own messages.
type (
	// setupReadMsg is ReadNode's answer, with the Zero's clock state and the messenger's radio, all read in one Cmd
	// when the form opens.
	setupReadMsg struct {
		gen       int
		node      flash.Node
		synced    bool
		messenger nodecli.MessengerRadio
		note      string
	}
	// setupTickMsg redraws while reading or sending; it carries only the generation of its chain.
	setupTickMsg struct{ gen int }
)

// openSetup starts Setup for the board on p (hooks.go): it reads the node, its settings, the Zero's clock state and
// the messenger's radio in one Cmd, and shows "Reading…" until they come.
func (m *Model) openSetup(p flash.Port, device string, firstBoot bool) tea.Cmd {
	m.closeSetup()
	gen := m.setup.gen + 1
	ctx, cancel := context.WithCancel(context.Background())
	m.setup = setupScreen{mode: setupReading, gen: gen, port: p, device: device, firstBoot: firstBoot,
		readAt: m.now(), cancel: cancel}
	m.screen, m.note = scrSetup, "" // a note from the screen that opened Setup is about that screen
	m.busy++                        // let go when the answer comes, whoever it is for (setupResult)
	be := m.backend
	read := func() tea.Msg {
		n := be.ReadNode(ctx, p, firstBoot)
		mr, note := be.MessengerRadio()
		return setupReadMsg{gen: gen, node: n, synced: be.ClockSynced(), messenger: mr, note: note}
	}
	return tea.Batch(read, m.after(setupTickEvery, setupTickMsg{gen}))
}

// setupKey handles a key on scrSetup.
func (m *Model) setupKey(msg tea.KeyMsg) tea.Cmd {
	s := &m.setup
	// Any key after the Esc that cancelled a text box drops its text: only an Esc hold's SIGTERM, which follows the
	// Esc with no key between, saves it.
	s.cancelled = setupCancelled{}
	switch s.mode {
	case setupReading:
		if msg.Type == tea.KeyEsc {
			return m.toBoard("")
		}
	case setupReadFailed:
		if msg.Type == tea.KeyEsc || msg.Type == tea.KeyEnter {
			return m.toBoard("")
		}
	case setupForm:
		return m.setupFormKey(msg)
	case setupEditing:
		m.setupBoxKey(msg)
	case setupPicking:
		m.pickKey(msg)
	case setupPresets:
		m.presetKey(msg)
	case setupSending:
		if msg.Type == tea.KeyEsc && !s.stopping {
			m.ask(confirmStopSetup)
		}
	case setupDone, setupFailed:
		return m.setupEndKey(msg)
	}
	return nil
}

// setupView draws scrSetup's body and gives its key hint.
func (m *Model) setupView(w, h int) ([]string, string) {
	s := &m.setup
	switch s.mode {
	case setupReading:
		what := "Reading the node's settings…"
		if s.firstBoot {
			what = "Waiting for it to start, then reading its settings…"
		}
		lines := wrap(m.setupWho()+" on "+s.port.Name, w)
		lines = append(lines, wrap(what+" ("+elapsed(m.now().Sub(s.readAt))+")", w)...)
		return lines, "esc back  ? help"
	case setupReadFailed:
		lines := wrap(m.setupWho()+" on "+s.port.Name, w)
		for _, l := range m.setupReadProblem() {
			lines = append(lines, wrap(l, w)...)
		}
		return lines, "enter back  ? help"
	case setupForm, setupEditing:
		return m.setupFormView(w, h)
	case setupPicking:
		return m.pickView(w, h)
	case setupPresets:
		return m.presetView(w, h)
	case setupSending:
		return m.setupSendingView(w, h)
	case setupDone, setupFailed:
		return m.setupEndView(w, h)
	}
	return nil, "esc back  ? help"
}

// setupTitle is the status line's title on scrSetup: the port while reading, the node's role and version on the
// form and while sending, and how it ended at the end.
func (m *Model) setupTitle() string {
	s := &m.setup
	switch s.mode {
	case setupForm, setupEditing, setupPicking, setupPresets:
		return "Set up " + m.setupRole()
	case setupSending:
		return "Setting up " + m.setupRole()
	case setupDone, setupFailed:
		if s.outcome.Title != "" {
			return s.outcome.Title
		}
		return "Setup ended"
	}
	return strings.TrimSpace("Set up " + s.port.Name)
}

// setupSubject is the board scrSetup is about, for the status mark: the node's port while reading, on the form and
// while sending; once the setup has ended, the Outcome's Board (nil when it's gone).
func (m *Model) setupSubject() (*flash.Port, bool) {
	s := &m.setup
	switch s.mode {
	case setupClosed:
		return nil, true
	case setupDone, setupFailed:
		return s.outcome.Board, true
	}
	p := s.port
	return &p, true
}

// setupEditing says a text box is open on scrSetup: ? (and y, j, k) are then letters.
func (m *Model) setupEditing() bool { return m.setup.mode == setupEditing }

// setupResult takes a message nobody else knows; ok says it was Setup's.
func (m *Model) setupResult(msg tea.Msg) (tea.Cmd, bool) {
	s := &m.setup
	switch msg := msg.(type) {
	case setupReadMsg:
		m.busy-- // every ReadNode answer comes back exactly once, also after Esc cancelled it
		if msg.gen != s.gen || s.mode != setupReading {
			return nil, true // an earlier opening's, or one cancelled: dropped
		}
		if s.cancel != nil {
			s.cancel()
			s.cancel = nil
		}
		m.setupGotNode(msg)
		return nil, true
	case setupTickMsg:
		if msg.gen == s.gen && (s.mode == setupReading || s.mode == setupSending) {
			return m.after(setupTickEvery, msg), true
		}
		return nil, true
	}
	return nil, false
}

// setupGotNode takes ReadNode's answer: the form when the node can be set up, else why not.
func (m *Model) setupGotNode(msg setupReadMsg) {
	s := &m.setup
	s.node, s.synced, s.messengerNote = msg.node, msg.synced, msg.note
	if s.node.Problem != flash.NoProblem {
		s.node.Current.GuestPassword = ""
		s.mode = setupReadFailed
		return
	}
	m.setupFillForm(msg.messenger)
}

// setupProgress takes a picture of the running setup Job: pictures of a Job this screen didn't start are ignored
// (the frame's own tests watch Jobs from the Board). At Done it saves Outcome.Sent whether or not the setup worked
// (the node took that radio), deletes the draft when it worked and keeps it when it didn't.
func (m *Model) setupProgress(p flash.Progress) tea.Cmd {
	s := &m.setup
	if p.Job == 0 || p.Job != s.jobID || s.mode != setupSending {
		return nil
	}
	s.picture = p
	if !p.Done {
		return nil
	}
	// Outcome.Log is already in screen.log: the backend is its one writer (flash.Outcome.Log).
	o := p.Outcome
	if o.Sent != nil {
		r := SavedRadio(*o.Sent)
		m.cfg.Radio = &r
		if m.store != nil {
			if err := m.store.SaveRadio(r); err != nil {
				m.logf("%s: couldn't save the radio sent in config.json: %v", m.name, err)
			}
		}
	}
	s.outcome, s.end, s.stopping = o, endScroll{}, false
	if o.OK {
		s.mode = setupDone
		m.setupDropDraft()
	} else {
		s.mode = setupFailed
	}
	return nil
}

// setupHelp is Help's list of scrSetup's keys, for the state Setup is in. Help can't open inside a text box (? is a
// letter there), so the form's list says what the text box's keys do too.
func (m *Model) setupHelp() []string {
	switch m.setup.mode {
	case setupReading:
		return []string{"esc            stop reading, back to the boards"}
	case setupReadFailed:
		return []string{"enter, esc     back to the boards"}
	case setupForm, setupEditing:
		return []string{
			"tab, j, k      move between the rows (shift+tab goes back)",
			"enter          edit the row: a text box, or a list to pick from",
			"p              radio presets: they fill the four radio rows and the path hash",
			"y              send it all to the node, check it, and restart the node",
			"esc            back to the boards (keeps what you typed)",
			"In a text box:",
			"enter          keep it",
			"esc            cancel: the old value comes back",
			"tab            keep it and go to the next row",
			"backspace      delete the character before the cursor",
		}
	case setupPicking:
		return []string{
			"j/k            pick a value",
			"enter          keep it",
			"esc            cancel: the old value stays",
			"tab            keep it and go to the next row",
		}
	case setupPresets:
		return []string{
			"j/k            move",
			"enter          use this preset: its radio, and its path hash when it has one",
			"esc            back to the form",
		}
	case setupSending:
		return []string{"esc            stop between two commands (asks y, saying what the node keeps)"}
	case setupDone:
		return []string{
			"j/k            scroll (space, b: a page)",
			"enter, esc     back to the boards",
		}
	case setupFailed:
		help := []string{"j/k            scroll (space, b: a page)", "esc, enter     back to the boards"}
		if m.setup.outcome.Retry {
			help = append([]string{"r              back to the form to send again (" + m.setupRetypeWords() + ")"},
				help...)
		}
		return help
	}
	return nil
}

// setupConfirmText words a question on scrSetup: confirmStopSetup, by the latest picture's Risk (state.go). It
// words no other question: Ctrl+C while sending quits at once (owner question 16).
func (m *Model) setupConfirmText(k confirmKind) string {
	if k != confirmStopSetup {
		return ""
	}
	switch m.setup.picture.Risk {
	case flash.RiskNone:
		return "Stop now? Nothing has been sent yet."
	case flash.RiskRadioSent:
		return "Stop now? The node keeps what it was sent so far, and switches to the new radio when it restarts."
	case flash.RiskRestarted:
		return "Stop now? It has restarted with the new settings; meshflash just won't check them."
	}
	return "Stop now? The node keeps what it was sent so far."
}

// setupConfirm does one of Setup's questions after y: confirmStopSetup stops the setup between two commands; the
// Job then finishes "Setup stopped", saying what the node keeps.
func (m *Model) setupConfirm(k confirmKind) tea.Cmd {
	s := &m.setup
	if k == confirmStopSetup && s.mode == setupSending && m.job != nil && m.job.ID() == s.jobID {
		m.job.Cancel()
		s.stopping = true
	}
	return nil
}

// closeSetup ends Setup: clears both passwords (the form's, the open text box's) and cancels a ReadNode still
// running. Safe to call when Setup never opened, and twice.
func (m *Model) closeSetup() {
	s := &m.setup
	s.admin.Clear()
	s.guest.Clear()
	s.box.Clear()
	s.cancelled = setupCancelled{}
	s.node.Current.GuestPassword = ""
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.mode = setupClosed
}

// setupWho names the board in a line "<who> on ttyACM0": the catalog board when known, else what the node calls its
// board, else "The board".
func (m *Model) setupWho() string {
	s := &m.setup
	switch {
	case s.device != "":
		return s.device
	case s.node.Info.Board != "":
		return s.node.Info.Board
	}
	return "The board"
}

// setupRole is the node's role and version as the titles say it: "repeater v1.18.0", "room server v1.17.1".
func (m *Model) setupRole() string {
	info := m.setup.node.Info
	return setupRoleWord(info.Role) + " " + info.Version.String()
}

// setupReadProblem is why the node can't be set up, in words, one entry per line (the dialout commands each on
// their own, never wrapped together).
func (m *Model) setupReadProblem() []string {
	n := m.setup.node
	switch n.Problem {
	case flash.Silent:
		return []string{"It didn't answer as a MeshCore repeater or room server: meshflash sets up only those."}
	case flash.Stuck:
		return []string{"It echoes but doesn't answer: press its reset button (or unplug it if it has no battery), " +
			"then press s on the Board again."}
	case flash.NotSettable:
		return []string{notSettable(n)}
	case flash.InBootloader:
		return []string{"It's in its bootloader: flash it first, or press its reset button once to start its " +
			"firmware."}
	case flash.NoPermission:
		hint := m.scan.Hint
		if hint == "" {
			hint = usbid.DialoutHint
		}
		return append([]string{"Serial ports need the dialout group. Run these once:"},
			strings.Split(hint, "\n")...)
	case flash.Busy:
		return []string{"Another program has this port open: close it, then press s on the Board again."}
	case flash.Gone:
		return []string{"The board went away: plug it in, then press s on the Board again."}
	}
	reason := strings.TrimSuffix(n.Reason, ".")
	if reason == "" {
		reason = "it gave no answer meshflash understands"
	}
	return []string{"Couldn't read its settings: " + reason + "."}
}
