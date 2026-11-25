package ui

// The Result screen (SPEC.md 4, docs/phase2-design.md 9.9): how the flash ended, in the backend's words as they are
// (the screens never translate a protocol error): what was written and how it was checked, or what went wrong, what
// the board is left with and how to recover it. State, Recovery and what to press now stay in view at the bottom
// while the rest scrolls (design 8.9).

import (
	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/flash"
)

// resultScreen is the Result screen's state: the Outcome and the Summary's scroll (endview.go).
type resultScreen struct {
	o      flash.Outcome
	scroll endScroll
}

// canSetUp says s makes sense: the backend said so (MeshCore's own repeater or room server, answering as one) and
// the board is on USB to set up.
func (m *Model) canSetUp() bool { return m.res.o.Setup && m.res.o.Board != nil }

// resultRetry is the line r's retry adds: the generic one, which may scroll with the Summary when there is no room
// (endPara.movable).
const resultRetry = "Press r to try again: the file is saved, so no Wi-Fi is needed."

// resultParts are the Summary, which scrolls, and what stays in view: State, Recovery, and the key to press.
func (m *Model) resultParts() (summary []string, pinned []endPara) {
	o := m.res.o
	pinned = []endPara{{text: o.State}, {text: o.Recovery}}
	if o.Retry {
		pinned = append(pinned, endPara{text: resultRetry, movable: true})
	}
	if m.canSetUp() {
		pinned = append(pinned, endPara{text: "Press s to set it up."})
	}
	return o.Summary, pinned
}

func (m *Model) resultKey(msg tea.KeyMsg) tea.Cmd {
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
	default:
		m.res.scroll.key(msg.String())
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
	sum, pin := m.resultParts()
	body, scrolls := endLayout(sum, pin, w, h, &m.res.scroll)
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
	return append(out, keyHelp("j/k", "scroll (space, b: a page)"), keyHelp("enter, esc", "back to the boards"))
}
