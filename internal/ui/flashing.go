package ui

// The Flashing screen (SPEC.md 4, docs/phase2-design.md 9.8): each step with its mark, the bytes written, how long it
// has taken (S17), and "Don't unplug the board."; Esc asks before stopping, in words that say what stopping leaves.
// When the Job ends, however it ends, the screen moves on to Result in the same Update (S16): it never stays here.

import (
	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/flash"
)

// flashingScreen is the Flashing screen's state: the latest picture, and whether it is being stopped.
type flashingScreen struct {
	p        flash.Progress
	stopping bool
}

// flashProgress takes the flash's pictures; at its end, Result.
func (m *Model) flashProgress(p flash.Progress) tea.Cmd {
	m.fl.p = p
	if p.Done {
		m.res = resultScreen{o: p.Outcome}
		m.fl.stopping = false
		m.screen = scrResult
	}
	return nil
}

// flashRisk is what stopping the flash now would leave, by its latest picture.
func (m *Model) flashRisk() flash.Risk { return m.fl.p.Risk }

// stopFlash is y to "Stop now?": the Job is asked to stop the safe way, and says how it ended when it has.
func (m *Model) stopFlash() {
	if m.ownFlash() {
		m.fl.stopping = true
		m.job.Cancel()
	}
}

func (m *Model) flashingKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "esc" && !m.fl.stopping && m.ownFlash() {
		m.ask(confirmStop)
	}
	return nil
}

func (m *Model) flashingView(w, h int) ([]string, string) {
	head := wrap(m.devName()+" on "+m.flow.port.Name, w)
	foot, hint := "Don't unplug the board.", "esc stop  ? help"
	if m.fl.stopping {
		foot, hint = "Stopping…", "? help"
	}
	footLines := wrap(foot, w)
	room := max(1, h-len(head)-len(footLines)-1)
	steps := fitSteps(m.fl.p, w, room, m.now().Sub(m.jobSeenAt))
	if len(steps) == 0 {
		steps = wrap("Starting…", w)
	}
	body := append(append(head, steps...), "")
	if len(body)+len(footLines) > h {
		body = body[:len(body)-1]
	}
	return append(body, footLines...), hint
}
