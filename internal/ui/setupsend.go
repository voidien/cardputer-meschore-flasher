package ui

// Sending a setup and its end (SPEC.md 4 "Setup"; docs/phase2-design.md 10.2, 10.5, 8.9): y starts one setup Job,
// the screen lists its steps as they go, and at the end shows the Outcome with what the node is left with kept in
// view.

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/flash"
)

// setupSend sends the form (y): refused with the problems named while any row has one, and doing nothing for a held
// y (owner question 5). The draft is saved first, so a kill mid-send keeps it; Setup is called here in Update, only
// when no Job runs, and its Job watched at once, so a second y can never start a second setup. Both passwords are
// wiped as soon as the request is built: r after a failure brings the form back with them empty.
func (m *Model) setupSend() tea.Cmd {
	s := &m.setup
	if m.yHeld {
		return nil
	}
	m.setupCheck()
	if rows := m.setupProblemRows(); len(rows) > 0 {
		names := make([]string, len(rows))
		for i, r := range rows {
			names[i] = setupRows[r].label
		}
		s.focus = rows[0]
		m.note = "Fix these first: " + strings.Join(names, ", ") + "."
		return nil
	}
	if !m.canStart() {
		m.note = "Wait for the work under way to end first."
		return nil
	}
	_ = m.setupSaveDraft() // a failure is in screen.log; the setup is sent all the same
	set, _ := m.setupSettings()
	j := m.backend.Setup(context.Background(), flash.SetupRequest{Node: s.node, Settings: set})
	s.admin.Clear()
	s.guest.Clear()
	m.setupCheck() // the password rows say "type one" again, for r
	if j == nil {
		m.note = "The setup didn't start."
		return nil
	}
	s.gen++ // a new tick chain, for the elapsed times
	s.mode, s.jobID, s.picture, s.stopping, s.sentAt = setupSending, j.ID(), flash.Progress{}, false, m.now()
	return tea.Batch(m.watch(j), m.after(setupTickEvery, setupTickMsg{s.gen}))
}

// setupSendingView is the setup under way: the board, its steps (folded at the top when they don't fit, the one
// under way always in view; a password step shows ••••), and a warning not to unplug, or that it is stopping.
func (m *Model) setupSendingView(w, h int) ([]string, string) {
	s := &m.setup
	head := wrap(m.setupWho()+" on "+s.port.Name, w)
	foot := "Don't unplug the board."
	hint := "esc stop  ? help"
	if s.stopping {
		foot, hint = "Stopping after the command under way…", "? help"
	}
	footL := wrap(foot, w)
	room := h - len(head) - len(footL)
	if room < 1 {
		head, room = nil, max(1, h-len(footL))
	}
	var steps []string
	if len(s.picture.Steps) == 0 {
		steps = wrap("Starting… ("+elapsed(m.now().Sub(s.sentAt))+")", w)
	} else {
		steps = fitSteps(s.picture, w, room, m.now().Sub(m.jobSeenAt))
	}
	lines := append(append(head, steps...), footL...)
	return lines, hint
}

// setupEndView is how the setup ended: the Outcome's Summary, then (pinned at the bottom, so what to do now is
// always in view) what the node is left with, how to finish, and r's line; the Summary scrolls above them, or
// everything when they alone don't fit (design 8.9).
func (m *Model) setupEndView(w, h int) ([]string, string) {
	s := &m.setup
	o := s.outcome
	var top, pinned []string
	for _, p := range o.Summary {
		top = append(top, wrap(p, w)...)
	}
	if o.State != "" {
		pinned = append(pinned, wrap(o.State, w)...)
	}
	if o.Recovery != "" {
		pinned = append(pinned, wrap(o.Recovery, w)...)
	}
	retry := s.mode == setupFailed && o.Retry
	if retry {
		pinned = append(pinned, wrap("Press r to go back to the form and send it again (type the password "+
			"again).", w)...)
	}
	all := append(append([]string(nil), top...), pinned...)
	scrolls := len(all) > h
	var body []string
	switch {
	case !scrolls:
		body = all
	case len(pinned) < h:
		body = append(scrolled(top, &s.endTop, h-len(pinned)), pinned...)
	default:
		body = scrolled(all, &s.endTop, h)
	}
	var hint string
	switch {
	case retry && scrolls:
		hint = "r retry  j/k more  esc back  ? help"
	case retry:
		hint = "r retry  esc back  ? help"
	case s.mode == setupFailed && scrolls:
		hint = "j/k more  esc back  ? help"
	case s.mode == setupFailed:
		hint = "esc back  ? help"
	case scrolls:
		hint = "j/k more  enter done  ? help"
	default:
		hint = "enter done  ? help"
	}
	return body, hint
}

// setupEndKey handles a key at the end: scrolling, r back to the form after a failure that can be retried, and
// Enter or Esc back to the boards.
func (m *Model) setupEndKey(msg tea.KeyMsg) tea.Cmd {
	s := &m.setup
	_, h := m.size()
	page := max(1, h-3)
	switch msg.String() {
	case "j", "down":
		s.endTop += scrollStep
	case "k", "up":
		s.endTop -= scrollStep
	case " ":
		s.endTop += page
	case "b":
		s.endTop -= page
	case "r":
		if s.mode == setupFailed && s.outcome.Retry {
			m.setupBackToForm()
		}
	case "enter", "esc":
		return m.toBoard("")
	}
	return nil
}

// setupBackToForm is r after a failed setup: the form again with what was sent (the draft y saved is still kept),
// the passwords empty, and the admin password's row in view, since it must be typed again. The board may show on
// another tty after the setup's reboot: the form follows it.
func (m *Model) setupBackToForm() {
	s := &m.setup
	if b := s.outcome.Board; b != nil {
		s.port, s.node.Port = *b, *b
	}
	s.admin.Clear()
	s.guest.Clear()
	s.mode, s.focus, s.outcome, s.picture, s.jobID = setupForm, setupAdmin, flash.Outcome{}, flash.Progress{}, 0
	m.setupCheck()
}
