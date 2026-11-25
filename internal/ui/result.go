package ui

// The Result screen (SPEC.md 4, docs/phase2-design.md 9.9): how the flash ended, in the backend's words as they are
// (the screens never translate a protocol error): what was written and how it was checked, or what went wrong, what
// the board is left with and how to recover it. State, Recovery and what to press now stay in view at the bottom
// while the rest scrolls (design 8.9).

import (
	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/flash"
)

// resultScreen is the Result screen's state: the Outcome and the Summary's scroll.
type resultScreen struct {
	o   flash.Outcome
	top int
}

// canSetUp says s makes sense: the backend said so (MeshCore's own repeater or room server, answering as one) and
// the board is on USB to set up.
func (m *Model) canSetUp() bool { return m.res.o.Setup && m.res.o.Board != nil }

// resultLines are the Summary, which scrolls, and what stays in view: State, Recovery, and the key to press.
func (m *Model) resultLines(w int) (summary, pinned []string) {
	o := m.res.o
	for _, s := range o.Summary {
		summary = append(summary, wrap(s, w)...)
	}
	for _, s := range []string{o.State, o.Recovery} {
		if s != "" {
			pinned = append(pinned, wrap(s, w)...)
		}
	}
	if o.Retry {
		pinned = append(pinned, wrap("Press r to try again: the file is saved, so no Wi-Fi is needed.", w)...)
	}
	if m.canSetUp() {
		pinned = append(pinned, wrap("Press s to set it up.", w)...)
	}
	return summary, pinned
}

// resultScrolls says the body doesn't fit h lines, so j/k scroll it.
func (m *Model) resultScrolls(w, h int) bool {
	s, p := m.resultLines(w)
	return len(s)+len(p) > h
}

func (m *Model) resultKey(msg tea.KeyMsg) tea.Cmd {
	_, h := m.size()
	page := max(1, h-3)
	switch msg.String() {
	case "s":
		if m.canSetUp() {
			m.cancelProbes()
			return m.openSetup(*m.res.o.Board, m.devName(), true)
		}
	case "r":
		if m.res.o.Retry {
			return m.retry()
		}
	case "enter", "esc":
		return m.toBoard("")
	case "j", "down":
		m.res.top += scrollStep
	case "k", "up":
		m.res.top -= scrollStep
	case " ":
		m.res.top += page
	case "b":
		m.res.top -= page
	}
	return nil
}

// retry is r: back to Confirm with the board and version as they were (owner question 4), the probe's answer dropped
// (it no longer says what the board runs) and the State shown instead; an ESP32's kind is cleared, so u or w is
// pressed again; an nRF52 goes straight to y.
func (m *Model) retry() tea.Cmd {
	if m.flow.dev == nil || m.flow.fw == nil || m.flow.ver == nil {
		return m.toBoard("")
	}
	if b := m.res.o.Board; b != nil {
		m.flow.port = *b
	}
	m.flow.node = nil
	return m.openConfirm(m.res.o.State)
}

func (m *Model) resultView(w, h int) ([]string, string) {
	sum, pin := m.resultLines(w)
	scrolls := len(sum)+len(pin) > h
	var body []string
	switch {
	case !scrolls:
		m.res.top = 0
		body = append(sum, pin...)
	case len(pin) < h:
		body = append(append([]string(nil), scrolled(sum, &m.res.top, h-len(pin))...), pin...)
	default:
		body = scrolled(append(sum, pin...), &m.res.top, h)
	}
	var hint string
	switch {
	case m.canSetUp() && scrolls:
		hint = fitHint(w, "s set up  j/k more  enter done  ? help", "s set up  enter done  ? help")
	case m.canSetUp():
		hint = "s set up  enter done  ? help"
	case m.res.o.Retry && scrolls:
		hint = fitHint(w, "r retry  j/k more  esc back  ? help", "r retry  esc back  ? help")
	case m.res.o.Retry:
		hint = "r retry  esc back  ? help"
	case scrolls:
		hint = fitHint(w, "j/k more  enter done  ? help", "enter done  ? help")
	default:
		hint = "enter done  ? help"
	}
	return body, hint
}

func (m *Model) resultHelp() []string {
	var out []string
	if m.canSetUp() {
		out = append(out, keyHelp("s", "set it up"))
	}
	if m.res.o.Retry {
		out = append(out, keyHelp("r", "try again from the saved file"))
	}
	return append(out, keyHelp("j/k", "scroll"), keyHelp("enter, esc", "back to the boards"))
}
