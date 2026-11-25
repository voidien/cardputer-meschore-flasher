package ui

// Phase 2 contract (docs/phase2-design.md 18): this file is fixed and complete (the integrator's). It is the screen
// lock the owner asked for (SPEC.md 4 and 5): Tab held 3 s locks meshflash, on every screen; locked, every key is
// ignored but the unlock, which is the CardputerZero home screen's (any key wakes the screen, then Tab, then Enter);
// the lock page goes dark a few seconds after locking and again after each wake. Nothing locks by itself. Jobs carry
// on underneath, and the page follows them, with how long the running step has run (S17): "Flashing · 42% (0:38)",
// "Setting up · step 4 of 20 (0:03)", and how one ended when it ends while locked; the screen it ended on (Result,
// Setup's end) shows once unlocked.
//
// It is the frame's, above every screen: while locked the frame gives every key press to lockKey before anything else
// (a held Tab's repeats to nothing), and View draws the lock page in place of the screen, the question and Help,
// which all stay as they were underneath. The hold's first Tab is an ordinary press, so it has already answered an
// open question no and closed Help when the lock comes; a question opened after it (by a message, or by a key pressed
// on the LCD with Tab still down) waits underneath. The page's time moves because the frame scans every second, each
// scan an Update and so a View. It never touches the LCD or its backlight: Dark says the screen should be dark,
// internal/screen (screen.Dimmable) turns the backlight off while it is, and View is then blank, which is all a
// terminal shows. Nobody else declares a top-level name starting with "lock".

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/flash"
)

const (
	// lockHold is how long Tab is held to lock (the owner's 3 s, as the home screen's own lock uses a held Tab).
	lockHold = 3 * time.Second
	// lockTabGap: in a terminal (Options.TerminalKeys), where a held key arrives again and again and its release is
	// never seen, a Tab less than this after the previous key, itself a Tab, is the held Tab repeating. The CLI app's
	// terminal is taken to repeat a held key every 33 ms after 250 ms, Linux's default (UNVERIFIED: Phase 3 measures
	// it, docs/phase2-design.md 18.9). Half a second keeps a held Tab one hold, as heldYGap does for a held y; the
	// price is that two deliberate Tabs less than 0.5 s apart act as one there (SPEC.md 11).
	lockTabGap = 500 * time.Millisecond
	// lockDarkAfter is how long the lock page stays lit after locking, and after each key while locked.
	lockDarkAfter = 5 * time.Second
)

// LockMsg locks the screen. internal/screen sends it (screen.Options.TabHeld) once Tab has been held down for 3 s on
// the LCD, where it sees each press and release; in a terminal the frame finds the hold from Tab's repeats itself
// (lockTab). A LockMsg while locked, or while quitting, does nothing.
type LockMsg struct{}

// lockDarkMsg darkens the lock page once lockDarkAfter has passed with no key (lockState.until).
type lockDarkMsg struct{}

// lockState is the screen lock (Model.lock).
type lockState struct {
	on    bool // locked
	lit   bool // the lock page shows; false: the screen is dark
	armed bool // Tab was pressed on the lit page: Enter now unlocks
	// until is when the lit page goes dark; timer says a lockDarkMsg is out (one at a time, however many keys).
	until time.Time
	timer bool
	// ended is how the Job that ended while locked ended (its Outcome's Title), shown until unlocked.
	ended string
	// seen is the running Job's last picture the frame was given, for the page's "Flashing · 42%".
	seen flash.Progress
	// tabFirst and tabLast are when the Tab now held was first pressed and last arrived (TerminalKeys only).
	tabFirst, tabLast time.Time
}

// lockTabKind is what a key is to the terminal's Tab-hold rule.
type lockTabKind int

const (
	lockTabNone   lockTabKind = iota // not a held Tab: the key acts as usual (a Tab acts as one Tab)
	lockTabRepeat                    // the held Tab repeating: it does nothing
	lockTabHeld                      // the held Tab has lasted lockHold: lock now
)

// Dark says the screen should be dark now: locked, with no key for lockDarkAfter. internal/screen asks after every
// View (screen.Dimmable) and keeps the LCD's backlight off while it is true; the View itself is then blank.
func (m *Model) Dark() bool { return m.lock.on && !m.lock.lit && !m.quit }

// lockTab sorts a key by the terminal's Tab-hold rule (Options.TerminalKeys; under --screen internal/screen does
// this from the presses and releases it sees, so every Tab reaching the model is a press): a Tab (or Shift+Tab, the
// same key) less than lockTabGap after the previous key, itself a Tab, is the held Tab repeating; once the run of
// them has lasted lockHold from its first Tab, the hold locks, once.
func (m *Model) lockTab(msg tea.KeyMsg, now time.Time) lockTabKind {
	if !m.terminalKeys {
		return lockTabNone
	}
	if msg.Type != tea.KeyTab && msg.Type != tea.KeyShiftTab {
		m.lock.tabFirst, m.lock.tabLast = time.Time{}, time.Time{}
		return lockTabNone
	}
	repeat := !m.lock.tabLast.IsZero() && now.Sub(m.lock.tabLast) < lockTabGap
	m.lock.tabLast = now
	switch {
	case !repeat:
		m.lock.tabFirst = now
		return lockTabNone
	case !m.lock.tabFirst.IsZero() && now.Sub(m.lock.tabFirst) >= lockHold:
		m.lock.tabFirst = time.Time{} // one lock a hold: the repeats after it are only repeats
		return lockTabHeld
	}
	return lockTabRepeat
}

// lockNow locks the screen, lit at first.
func (m *Model) lockNow() tea.Cmd {
	if m.lock.on || m.quit {
		return nil
	}
	m.lock.on, m.lock.armed, m.lock.ended, m.note = true, false, "", ""
	return m.lockLight()
}

// lockLight lights the lock page for lockDarkAfter from now, setting the timer that darkens it if none is out.
func (m *Model) lockLight() tea.Cmd {
	m.lock.lit, m.lock.until = true, m.now().Add(lockDarkAfter)
	if m.lock.timer {
		return nil
	}
	m.lock.timer = true
	return m.after(lockDarkAfter, lockDarkMsg{})
}

// lockDarken darkens the lock page when its time is up, or waits for the rest of it (a key since moved it on).
func (m *Model) lockDarken() tea.Cmd {
	m.lock.timer = false
	if !m.lock.on || !m.lock.lit || m.quit {
		return nil
	}
	if left := m.lock.until.Sub(m.now()); left > 0 {
		m.lock.timer = true
		return m.after(left, lockDarkMsg{})
	}
	m.lock.lit, m.lock.armed = false, false
	return nil
}

// lockKey takes every key press while locked, as the home screen does: on a dark screen any key only wakes it; on the
// lit page Tab arms the unlock and Enter after it unlocks, and any other key disarms it. Nothing else happens: a y
// answers no question, Ctrl+C doesn't quit, ? opens no Help. (The launcher still ends meshflash on a 3 s Esc hold,
// locked or not, on the LCD and in the CLI app alike: internal/app's shutdown stops a running Job the safe way.)
func (m *Model) lockKey(msg tea.KeyMsg) tea.Cmd {
	if !m.lock.lit {
		m.lock.armed = false
		return m.lockLight()
	}
	switch {
	case msg.Type == tea.KeyTab || msg.Type == tea.KeyShiftTab:
		m.lock.armed = true
	case msg.Type == tea.KeyEnter && m.lock.armed:
		m.lock.on, m.lock.lit, m.lock.armed, m.lock.ended = false, false, false, ""
		return nil
	default:
		m.lock.armed = false
	}
	return m.lockLight()
}

// lockJobDone notes, at a Job's last picture, how it ended, for the lock page while locked.
func (m *Model) lockJobDone(p flash.Progress) {
	if !m.lock.on {
		return
	}
	m.lock.ended = p.Outcome.Title
	if m.lock.ended == "" {
		m.lock.ended = lockVerbs[p.Kind] + " ended"
	}
}

// lockView is the lock page in w×h: meshflash's status line, saying it is locked; what is running, or how it ended,
// or the screen's name, in the middle; how to unlock on the hint line. Dark, it is h blank lines.
func (m *Model) lockView(w, h int) string {
	lines := make([]string, h)
	if !m.lock.lit || h < 1 {
		return strings.Join(lines, "\n")
	}
	lines[0] = m.statusLineTitled(w, m.name+" · locked")
	if h < 3 {
		return strings.Join(lines, "\n")
	}
	what := wrap(m.lockWhat(), w)
	top := 1 + max(0, (h-2-len(what))/2)
	for i, l := range what {
		if top+i < h-1 {
			lines[top+i] = center(l, w)
		}
	}
	hint := "Tab then Enter to unlock"
	if m.lock.armed {
		hint = "Enter to unlock"
	}
	lines[h-1] = cut(hint, w)
	return strings.Join(lines, "\n")
}

// lockWhat is the lock page's middle line: the running Job and how far it is, how a Job that ended while locked
// ended, or the name of the screen underneath.
func (m *Model) lockWhat() string {
	switch {
	case m.job != nil:
		return lockJobLine(m.lock.seen, m.job.Kind(), m.now().Sub(m.jobSeenAt))
	case m.lock.ended != "":
		return m.lock.ended
	}
	return lockScreenNames[m.screen]
}

// lockHelp is Help's footer line about the lock: what it does, and that an Esc hold still ends meshflash, locked or
// not, in the CLI app as on the LCD (the launcher's own hold, which meshflash can't stop: the kit's docs/device.md,
// "In short", its Keys table and "Apps started from the home screen").
func (m *Model) lockHelp() string {
	return "Holding Tab 3 s locks meshflash: every key is then ignored but the unlock (any key wakes the screen, " +
		"then Tab, then Enter), and a flash or setup carries on. Nothing locks by itself. Holding Esc 3 s still " +
		"ends meshflash, locked or not (the launcher does that): a flash or setup under way stops the safe way, " +
		"and screen.log says what the board is left with."
}

// lockVerbs name what a Job is doing on the lock page.
var lockVerbs = map[flash.JobKind]string{
	flash.JobDownload: "Downloading", flash.JobFlash: "Flashing", flash.JobSetup: "Setting up",
}

// lockJobLine words a running Job from its last picture: the running step's percentage when it moves bytes
// ("Flashing · 42%"), else which step of how many when the Job knows (Progress.Planned: "Setting up · step 4 of
// 20"), else the running step's words ("Flashing · Erasing"), else what it does; then, while a step is under way,
// how long it has run (S17: "Flashing · Erasing (0:04)"), as stepLines times it: extra is how long ago the picture
// arrived (m.now() − m.jobSeenAt). A step's words never hold a password (flash.Step.Text).
func lockJobLine(p flash.Progress, k flash.JobKind, extra time.Duration) string {
	verb := lockVerbs[k]
	n := len(p.Steps)
	if n == 0 {
		return verb
	}
	s := p.Steps[n-1]
	line := verb
	switch {
	case s.State == flash.StepNow && s.Total > 0:
		line = fmt.Sprintf("%s · %d%%", verb, percent(s.Done, s.Total))
	case p.Planned > 0:
		line = fmt.Sprintf("%s · step %d of %d", verb, n, p.Planned)
	case s.Text != "":
		line = verb + " · " + s.Text
	}
	if s.State == flash.StepNow {
		running := extra
		if !s.Started.IsZero() && !p.Now.IsZero() {
			running += p.Now.Sub(s.Started)
		}
		line += " (" + elapsed(running) + ")"
	}
	return line
}

// lockScreenNames are the screens' names (SPEC.md 4), for the lock page when nothing runs.
var lockScreenNames = map[screen]string{
	scrBoard: "Board", scrChoose: "Choose board", scrRole: "Role", scrVersion: "Version", scrNotes: "Notes",
	scrConfirm: "Confirm", scrDownload: "Download", scrFlashing: "Flashing", scrResult: "Result", scrSetup: "Setup",
	scrUpdate: "Update catalog", scrFiles: "Saved files",
}
