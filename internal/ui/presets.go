package ui

// The Setup form's presets (SPEC.md 4 and 7; docs/phase2-design.md 10.3): `p` lists MeshCore's 27 regional radio
// presets (nodecli.Presets, the built-in copy), and Enter fills the four radio rows and, when the preset gives one,
// the path hash, as nodecli.Preset.Apply does.

import (
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/nodecli"
)

// presetOpen opens the list on the preset whose radio the form has now, else on the first.
func (m *Model) presetOpen() {
	s := &m.setup
	s.preset, s.presetTop = 0, 0
	if r, ok := m.presetRadio(); ok {
		for i, p := range nodecli.Presets {
			if p.Radio.Near(r) {
				s.preset = i
				break
			}
		}
	}
	s.mode = setupPresets
}

// presetRadio is the radio the form's four rows hold, when they all read as numbers.
func (m *Model) presetRadio() (nodecli.Radio, bool) {
	v := m.setup.vals
	f, okF := setupNumber(v[setupFreq])
	bw, okB := setupNumber(v[setupBW])
	sf, errS := strconv.Atoi(v[setupSF])
	cr, errC := strconv.Atoi(v[setupCR])
	return nodecli.Radio{FreqMHz: f, BWkHz: bw, SF: sf, CR: cr}, okF && okB && errS == nil && errC == nil
}

// presetKey handles a key on the presets list: j/k move, Enter uses the preset, Esc goes back to the form unchanged.
func (m *Model) presetKey(msg tea.KeyMsg) {
	s := &m.setup
	switch msg.String() {
	case "j", "down":
		s.preset = min(s.preset+1, len(nodecli.Presets)-1)
	case "k", "up":
		s.preset = max(s.preset-1, 0)
	case "enter":
		// Through Preset.Apply itself, so the form does exactly what it does: the radio, and the path hash only when
		// the preset gives one.
		p := nodecli.Presets[s.preset]
		var set nodecli.Settings
		p.Apply(&set)
		s.vals[setupFreq] = setupFreqText(set.Radio.FreqMHz)
		s.vals[setupBW] = setupBWText(set.Radio.BWkHz)
		s.vals[setupSF] = strconv.Itoa(set.Radio.SF)
		s.vals[setupCR] = strconv.Itoa(set.Radio.CR)
		if set.PathHashBytes != 0 {
			s.vals[setupPathHash] = strconv.Itoa(set.PathHashBytes)
		}
		s.mode, s.focus = setupForm, setupFreq // the rows it changed, in view
		m.note = "Radio set from the " + p.Title + " preset."
		m.setupCheck()
	case "esc":
		s.mode = setupForm
	}
}

// presetView is the list, the selected preset's radio (and path hash) under it in grey, scrolled to keep it in view.
func (m *Model) presetView(w, h int) ([]string, string) {
	s := &m.setup
	var list []string
	from, to := 0, 0
	for i, p := range nodecli.Presets {
		if i != s.preset {
			list = append(list, "  "+cut(p.Title, w-2))
			continue
		}
		from = len(list)
		list = append(list, "▸ "+cut(p.Title, w-2))
		what := p.Radio.Display()
		if p.PathHashBytes != 0 {
			what += ", path hash " + plural(p.PathHashBytes, "byte", "bytes")
		}
		for _, l := range wrap(what, max(1, w-4)) {
			list = append(list, grey.Render("    "+l))
		}
		to = len(list)
	}
	room := max(1, h-1)
	s.presetTop = setupScroll(s.presetTop, from, to, room, len(list))
	lines := []string{heading(cut("Radio presets", w))}
	lines = append(lines, list[s.presetTop:min(len(list), s.presetTop+room)]...)
	return lines, "j/k move  enter use  esc back  ? help"
}

// presetPathHash is the path hash size the presets with radio r give, for the form's default (owner question 6:
// "the preset's"), or 0 when none gives one or they don't agree: EU/UK (Narrow), Netherlands (Limburg) and
// Switzerland share one radio but not a path hash, and a guess there would set another country's.
func presetPathHash(r nodecli.Radio) int {
	n := 0
	for _, p := range nodecli.Presets {
		if !p.Radio.Near(r) {
			continue
		}
		if p.PathHashBytes == 0 || n != 0 && n != p.PathHashBytes {
			return 0
		}
		n = p.PathHashBytes
	}
	return n
}
