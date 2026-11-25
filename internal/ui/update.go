package ui

// The Update catalog screen (SPEC.md 4, docs/phase2-design.md 9.10): u fetches both catalogs and their release
// lists, the only time meshflash asks the network for one. Esc while fetching stops it; whatever happens, the saved
// catalog stays unless a new one came whole.

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/catalog"
)

// updateScreen is the Update catalog screen's state: the fetch under way (its generation, start and cancel), or how
// it ended.
type updateScreen struct {
	gen      int
	fetching bool
	since    time.Time
	cancel   context.CancelFunc
	cat      *catalog.Catalog
	err      error
}

// updatedMsg is UpdateCatalog's answer, for the fetch numbered gen.
type updatedMsg struct {
	gen int
	cat *catalog.Catalog
	err error
}

// openUpdate is u on the Board: the probes stop (it leaves the Board for something else) and the fetch starts.
func (m *Model) openUpdate() tea.Cmd {
	m.cancelProbes()
	m.screen = scrUpdate
	return m.fetchCatalog()
}

// fetchCatalog asks UpdateCatalog in a Cmd, with a context Esc cancels.
func (m *Model) fetchCatalog() tea.Cmd {
	if m.upd.cancel != nil {
		m.upd.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.upd = updateScreen{gen: m.upd.gen + 1, fetching: true, since: m.now(), cancel: cancel}
	gen, be := m.upd.gen, m.backend
	return func() tea.Msg {
		c, err := be.UpdateCatalog(ctx)
		return updatedMsg{gen: gen, cat: c, err: err}
	}
}

// gotUpdate takes the fetch's answer. A catalog that came whole is the saved one now, whichever screen shows (even
// after Esc, when the stop came too late); the screen's words are only for the fetch it still waits for, and Help
// closes when they change (as at a Job's end).
func (m *Model) gotUpdate(msg updatedMsg) tea.Cmd {
	if msg.err == nil && msg.cat != nil {
		m.cat, m.catErr, m.more.catRead = msg.cat, nil, true
	}
	if msg.gen != m.upd.gen || !m.upd.fetching {
		return nil
	}
	if m.upd.cancel != nil {
		m.upd.cancel()
	}
	m.upd.fetching, m.upd.cancel = false, nil
	m.upd.cat, m.upd.err = msg.cat, msg.err
	if msg.err == nil && msg.cat == nil {
		m.upd.err = errors.New("the server gave no catalog")
	}
	if m.screen == scrUpdate {
		m.help = false
	}
	return nil
}

func (m *Model) updateKey(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	switch {
	case m.upd.fetching:
		if k == "esc" {
			if m.upd.cancel != nil {
				m.upd.cancel()
			}
			m.upd.fetching, m.upd.cancel = false, nil
			m.upd.gen++
			return m.toBoard("")
		}
	case m.upd.err != nil && k == "u":
		return m.fetchCatalog()
	case k == "enter" || k == "esc":
		return m.toBoard("")
	}
	return nil
}

// updateWhy says why an update failed, in plain words.
func updateWhy(err error) string {
	var fe *catalog.FetchError
	switch {
	case errors.As(err, &fe) && fe.Status != 0:
		return host(fe.URL) + " answered HTTP " + itoa(fe.Status) + "."
	case errors.Is(err, catalog.ErrUnreachable):
		return "No network, or the server didn't answer."
	}
	return capital(plainErr(err))
}

func (m *Model) updateView(w, h int) ([]string, string) {
	var lines []string
	add := func(s string) { lines = append(lines, wrap(s, w)...) }
	switch {
	case m.upd.fetching:
		add("Fetching the catalog… (" + elapsed(m.now().Sub(m.upd.since)) + ")")
		return lines, "esc stop  ? help"
	case m.upd.err != nil:
		add("The saved catalog stays.")
		add(updateWhy(m.upd.err))
		return lines, "u try again  esc back  ? help"
	}
	c := m.upd.cat
	if c == nil {
		return lines, "enter done  ? help"
	}
	mc, zc := 0, 0
	for _, d := range c.Devices {
		if d.Source == catalog.SourceZephCore {
			zc++
		} else {
			mc++
		}
	}
	s := plural(len(c.Devices), "board", "boards")
	if n := newest(c); n != "" { // the update's own catalog, shown once
		s += ", newest " + n
	}
	add(s + ", " + byteSize(c.Size))
	if zc > 0 {
		add("(" + plural(mc, "MeshCore board", "MeshCore boards") + " and " + itoa(zc) + " that only ZephCore has)")
	}
	return lines, "enter done  ? help"
}

func (m *Model) updateHelp() []string {
	switch {
	case m.upd.fetching:
		return []string{keyHelp("esc", "stop (the saved catalog stays)")}
	case m.upd.err != nil:
		return []string{keyHelp("u", "try again"), keyHelp("esc", "back")}
	}
	return []string{keyHelp("enter, esc", "back to the boards")}
}
