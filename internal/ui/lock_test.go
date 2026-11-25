package ui

// The integrator's tests of the screen lock (lock.go), through the real frame and the test driver, in the driver's
// virtual time. No builder owns this file or the goldens under testdata/TestLock* (docs/phase2-design.md 11). They
// stay on the Board screen with nothing plugged in, so the screens' own code is never asked anything the lock
// doesn't need; the Jobs they watch are started here, which the screens' jobProgress and setupProgress ignore
// (hooks.go).

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"meshflash/internal/flash"
)

// lockDriver is the Board with nothing plugged in and the battery at 87% on the status line, as on the LCD.
func lockDriver(t *testing.T, w, h int, opts ...func(*Options)) *driver {
	t.Helper()
	fb := newFakeBackend()
	fb.battery, fb.hasBattery = 87, true
	opts = append([]func(*Options){func(o *Options) { o.ShowBattery = true }}, opts...)
	return newDriver(t, w, h, fb, opts...)
}

// watchJob starts a Job the way the screens do (in Update, watched at once) and shows the test's first picture.
func watchJob(d *driver, kind flash.JobKind, picture func(p *flash.Progress)) *flash.Job {
	d.t.Helper()
	j := newJob(kind)
	d.run(d.m.watch(j))
	if picture != nil {
		j.Set(picture)
		d.settle()
	}
	return j
}

// writing is a flash 38 s into writing its 361 KB, done bytes of it written.
func writing(done int64) func(p *flash.Progress) {
	return func(p *flash.Progress) {
		p.Now, p.Risk = testNow, flash.RiskErased
		p.Steps = []flash.Step{
			{Text: "Into the bootloader (1200-baud touch)", State: flash.StepDone},
			{Text: "Erased", State: flash.StepDone, Took: 10 * time.Second},
			{Text: "Writing", State: flash.StepNow, Done: done, Total: 361_000,
				Started: testNow.Add(-38 * time.Second)},
		}
	}
}

// lockHoldTab holds Tab in a terminal (Options.TerminalKeys) for upTo: a press, then a repeat every 33 ms, the
// CLI app's terminal's rate as Phase 3 is to check it (docs/phase2-design.md 18.9).
func lockHoldTab(d *driver, upTo time.Duration) {
	d.t.Helper()
	const every = 33 * time.Millisecond
	d.keys("tab")
	for at := every; at <= upTo; at += every {
		d.advance(every)
		d.keys("tab")
	}
}

func lines(v string) []string { return strings.Split(v, "\n") }

// The lock page with nothing running: meshflash's status line, the screen's name, how to unlock.
func TestLockPage(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := lockDriver(t, w, h)
		d.send(LockMsg{})
		d.check(w, h)
	})
}

// A flash carries on underneath, and the page says how far it is.
func TestLockFlashing(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := lockDriver(t, w, h)
		watchJob(d, flash.JobFlash, writing(151_000))
		d.send(LockMsg{})
		d.check(w, h)
	})
}

// A setup says which step of how many (Progress.Planned).
func TestLockSettingUp(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := lockDriver(t, w, h)
		watchJob(d, flash.JobSetup, func(p *flash.Progress) {
			p.Now, p.Planned, p.Risk = testNow, 20, flash.RiskSettings
			p.Steps = []flash.Step{
				{Text: "Set name to “Hilltop 2”", State: flash.StepDone},
				{Text: "Set radio 869.618 MHz, 62.5 kHz, SF8, CR8", State: flash.StepDone},
				{Text: "Set TX power 22 dBm", State: flash.StepDone},
				{Text: "Set admin password ••••", State: flash.StepNow, Started: testNow.Add(-3 * time.Second)},
			}
		})
		d.send(LockMsg{})
		d.check(w, h)
	})
}

// A flash that ends while locked: the page says how in one line (S16), and the end shows once unlocked.
func TestLockEnded(t *testing.T) {
	eachSize(t, func(t *testing.T, w, h int) {
		d := lockDriver(t, w, h)
		j := watchJob(d, flash.JobFlash, writing(151_000))
		d.send(LockMsg{})
		j.Finish(flash.Outcome{Title: "Flashing stopped", Summary: []string{"The board stopped answering."},
			State: "Its app is erased.", Retry: true})
		d.settle()
		d.check(w, h)
		if d.m.job != nil || d.m.busy != 0 {
			t.Error("the Job wasn't let go at its end while locked")
		}
		d.keys("tab", "enter")
		if d.m.lock.on || d.m.lock.ended != "" {
			t.Error("Tab, Enter didn't unlock, or the lock kept how the flash ended")
		}
	})
}

// The page follows the Job while it shows (S16, S17): each new picture, and the running step's time between them
// (the frame's every-second scans redraw it on the device).
func TestLockFollowsTheJob(t *testing.T) {
	d := lockDriver(t, 45, 10)
	j := watchJob(d, flash.JobFlash, writing(151_000))
	d.send(LockMsg{})
	j.Set(writing(300_000))
	d.settle()
	if v := d.view(); !strings.Contains(v, "Flashing · 83% (0:38)") {
		t.Errorf("the lock page didn't follow the flash:\n%s", v)
	}
	d.advance(3 * time.Second)
	if v := d.view(); !strings.Contains(v, "Flashing · 83% (0:41)") {
		t.Errorf("the lock page's time didn't move with no new picture:\n%s", v)
	}
}

// Dark a few seconds after locking: blank, and Dark for internal/screen to turn the backlight off. Any key wakes it
// and does nothing else (a Tab that wakes doesn't arm the unlock); then Tab, then Enter unlocks.
func TestLockDarkensAndWakes(t *testing.T) {
	d := lockDriver(t, 45, 10)
	d.send(LockMsg{})
	d.advance(lockDarkAfter - time.Second)
	if d.m.Dark() {
		t.Fatal("dark before lockDarkAfter")
	}
	d.advance(time.Second)
	if !d.m.Dark() {
		t.Fatal("still lit after lockDarkAfter")
	}
	ls := lines(d.view())
	if len(ls) != 10 || strings.TrimSpace(strings.Join(ls, "")) != "" {
		t.Errorf("the dark page isn't 10 blank lines: %q", ls)
	}
	d.keys("tab")
	if d.m.Dark() || !d.m.lock.on {
		t.Fatal("a key didn't wake the page, or it unlocked")
	}
	d.keys("enter")
	if !d.m.lock.on {
		t.Fatal("the Tab that woke the screen armed the unlock")
	}
	d.keys("tab")
	if last := lines(d.view())[9]; last != "Enter to unlock" {
		t.Errorf("after Tab the hint is %q", last)
	}
	d.keys("x")
	if last := lines(d.view())[9]; last != "Tab then Enter to unlock" {
		t.Errorf("a key after Tab didn't disarm the unlock: the hint is %q", last)
	}
	d.keys("tab", "enter")
	if d.m.lock.on || d.m.Dark() {
		t.Error("Tab, Enter on the lit page didn't unlock")
	}
}

// Each key while locked keeps the page lit for lockDarkAfter more.
func TestLockStaysLitWhileKeysCome(t *testing.T) {
	d := lockDriver(t, 45, 10)
	d.send(LockMsg{})
	d.advance(4 * time.Second)
	d.keys("a")
	d.advance(4 * time.Second)
	if d.m.Dark() {
		t.Fatal("dark 4 s after a key")
	}
	d.advance(time.Second)
	if !d.m.Dark() {
		t.Error("not dark 5 s after the last key")
	}
}

// Locked, no key does anything but the unlock: a y answers no question, Ctrl+C doesn't quit, ? opens no Help, Esc
// doesn't go back. The question is the frame's own, Ctrl+C's while a flash runs, opened between the hold's first Tab
// and its lock, as Ctrl+C pressed on the LCD with Tab still down opens it (internal/screen's Tab hold carries on
// whatever else is pressed): it waits, open, underneath, and shows again once unlocked.
func TestLockedKeysDoNothing(t *testing.T) {
	d := lockDriver(t, 45, 10)
	watchJob(d, flash.JobFlash, writing(151_000))
	d.keys("tab", "ctrl+c")
	if d.m.confirm != confirmQuit {
		t.Fatalf("Ctrl+C while flashing didn't ask: %v", d.m.confirm)
	}
	d.send(LockMsg{})
	before := len(d.m.backend.(*fakeBackend).called())
	d.keys("y", "y", "ctrl+c", "?", "esc", "j", "enter", "y")
	if d.quit || d.m.quit {
		t.Fatal("a key quit while locked")
	}
	if d.m.confirm != confirmQuit || d.m.help || !d.m.lock.on || d.m.screen != scrBoard {
		t.Errorf("a key acted while locked: confirm %v, help %v, locked %v, screen %v", d.m.confirm, d.m.help,
			d.m.lock.on, d.m.screen)
	}
	if after := d.m.backend.(*fakeBackend).called(); len(after) != before {
		t.Errorf("keys while locked asked the backend: %v", after[before:])
	}
	d.keys("tab", "enter")
	if d.m.lock.on {
		t.Fatal("didn't unlock")
	}
	if last := lines(d.view())[9]; d.m.confirm != confirmQuit || last != "y yes  any other key no" {
		t.Errorf("the question didn't show again once unlocked: %v, hint %q", d.m.confirm, last)
	}
	d.keys("n")
	if d.quit || d.m.confirm != noConfirm || d.m.job == nil {
		t.Errorf("n after unlocking: quit %v, question %v, the flash let go %v", d.quit, d.m.confirm, d.m.job == nil)
	}
}

// The hold's first Tab is an ordinary press, before anything can tell it is a hold: it answers an open question no
// (the safe answer, as any key but y is) and closes Help, and then the lock comes, nothing stopped.
func TestLockFirstTabOfTheHoldAnswersNo(t *testing.T) {
	d := lockDriver(t, 45, 10, func(o *Options) { o.TerminalKeys = true })
	j := watchJob(d, flash.JobFlash, writing(151_000))
	d.keys("ctrl+c")
	lockHoldTab(d, lockHold+33*time.Millisecond)
	if !d.m.lock.on || d.quit || d.m.confirm != noConfirm || d.m.job != j {
		t.Fatalf("locked %v, quit %v, question %v, the flash still watched %v", d.m.lock.on, d.quit, d.m.confirm,
			d.m.job == j)
	}
	d.advance(lockTabGap)
	d.keys("tab", "enter", "?")
	if d.m.lock.on || !d.m.help {
		t.Fatalf("unlocked %v, Help open %v", !d.m.lock.on, d.m.help)
	}
	d.advance(lockTabGap)
	lockHoldTab(d, lockHold+33*time.Millisecond)
	if !d.m.lock.on || d.m.help {
		t.Errorf("locked %v, Help still open underneath %v", d.m.lock.on, d.m.help)
	}
}

// In a terminal the hold is found from Tab's repeats: locked once they have lasted 3 s from the first Tab, not
// before; a pause of lockTabGap is a release, and the count starts again. The owner is still holding Tab when it
// locks: the repeats after the lock are not presses either, so they neither arm the unlock (one Enter must not
// unlock) nor keep the page lit.
func TestLockHeldTabInATerminal(t *testing.T) {
	d := lockDriver(t, 45, 10, func(o *Options) { o.TerminalKeys = true })
	lockHoldTab(d, 2900*time.Millisecond)
	if d.m.lock.on {
		t.Fatal("locked before 3 s")
	}
	d.advance(lockTabGap)
	lockHoldTab(d, 2900*time.Millisecond)
	if d.m.lock.on {
		t.Fatal("two holds of 2.9 s with a release between them locked")
	}
	d.advance(lockTabGap)
	lockHoldTab(d, lockHold+2*time.Second) // locked at the first repeat at or past 3 s, then held 2 s more
	if !d.m.lock.on {
		t.Fatal("a 3 s hold didn't lock")
	}
	if last := lines(d.view())[9]; d.m.lock.armed || last != "Tab then Enter to unlock" {
		t.Fatalf("the held Tab's repeats after the lock armed the unlock: the hint is %q", last)
	}
	d.keys("enter")
	if !d.m.lock.on {
		t.Fatal("one Enter after holding Tab past the lock unlocked")
	}
	d.advance(lockTabGap)
	lockHoldTab(d, lockDarkAfter+time.Second) // a fresh hold while locked: its first Tab arms, its repeats do nothing
	if !d.m.Dark() {
		t.Error("a held Tab's repeats kept the lock page lit")
	}
	d.keys("enter", "enter")
	if !d.m.lock.on {
		t.Fatal("Enter on the dark page, then Enter, unlocked")
	}
	d.keys("tab", "enter")
	if d.m.lock.on {
		t.Error("Tab, Enter after the hold didn't unlock")
	}
}

// The terminal's rule, key by key: a Tab acts once (its repeats don't), the hold locks once, and any other key ends
// the hold. Shift+Tab is the same key.
func TestLockTabRule(t *testing.T) {
	m := &Model{terminalKeys: true}
	tab, shiftTab := tea.KeyMsg{Type: tea.KeyTab}, tea.KeyMsg{Type: tea.KeyShiftTab}
	a := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}
	type step struct {
		at   time.Duration
		key  tea.KeyMsg
		want lockTabKind
	}
	ms := time.Millisecond
	steps := []step{
		{0, tab, lockTabNone},
		{300 * ms, tab, lockTabRepeat},
		{900 * ms, tab, lockTabNone}, // 0.6 s after the last Tab: released, and pressed again
		{1000 * ms, a, lockTabNone},
		{1100 * ms, tab, lockTabNone}, // after another key: a press, the hold's first Tab
	}
	for at := 1400 * ms; at < 4100*ms; at += 300 * ms {
		steps = append(steps, step{at, tab, lockTabRepeat})
	}
	steps = append(steps,
		step{4100 * ms, tab, lockTabHeld}, // 3 s from its first Tab
		step{4400 * ms, tab, lockTabRepeat},
		step{4700 * ms, shiftTab, lockTabRepeat},
		step{5300 * ms, tab, lockTabNone})
	for i, s := range steps {
		if got := m.lockTab(s.key, testNow.Add(s.at)); got != s.want {
			t.Errorf("step %d (%v at %v): %v, want %v", i, s.key, s.at, got, s.want)
		}
	}
	m.terminalKeys = false
	if got := m.lockTab(tab, testNow.Add(5400*ms)); got != lockTabNone {
		t.Errorf("under --screen a Tab is %v: internal/screen finds the hold there", got)
	}
}

// On the LCD the frame never finds a hold in Tabs (internal/screen sends only presses, and LockMsg itself).
func TestLockNotFromTabsOnTheLCD(t *testing.T) {
	d := lockDriver(t, 45, 10)
	for i := 0; i < 150; i++ {
		d.keys("tab")
		d.advance(33 * time.Millisecond)
	}
	if d.m.lock.on {
		t.Error("Tabs locked the screen without TerminalKeys")
	}
}

// Nothing locks by itself (the owner's answer: no auto-lock).
func TestNothingLocksByItself(t *testing.T) {
	d := lockDriver(t, 45, 10)
	for i := 0; i < 10; i++ {
		d.advance(time.Minute)
	}
	if d.m.lock.on || d.m.Dark() {
		t.Error("the screen locked or went dark with no Tab held")
	}
}

// A LockMsg while locked or after quitting changes nothing.
func TestLockMsgTwice(t *testing.T) {
	d := lockDriver(t, 45, 10)
	d.send(LockMsg{})
	d.advance(lockDarkAfter)
	d.send(LockMsg{})
	if !d.m.Dark() {
		t.Error("a second LockMsg lit the page")
	}
	d.keys("a", "tab", "enter")
	d.keys("ctrl+c")
	d.send(LockMsg{})
	if d.m.lock.on {
		t.Error("a LockMsg after quitting locked")
	}
}

// The page names every screen when nothing runs.
func TestLockNamesEveryScreen(t *testing.T) {
	m := &Model{}
	for s := scrBoard; s <= scrFiles; s++ {
		m.screen = s
		if m.lockWhat() == "" {
			t.Errorf("screen %d has no name on the lock page", s)
		}
	}
	if got := lockJobLine(flash.Progress{}, flash.JobDownload, time.Second); got != "Downloading" {
		t.Errorf("a download with no picture yet: %q", got)
	}
	p := flash.Progress{Now: testNow, Steps: []flash.Step{{Text: "Erasing", State: flash.StepNow,
		Started: testNow.Add(-4 * time.Second), Expect: 10 * time.Second}}}
	if got := lockJobLine(p, flash.JobFlash, 0); got != "Flashing · Erasing (0:04)" {
		t.Errorf("an erase: %q", got)
	}
	if got := lockJobLine(p, flash.JobFlash, 2*time.Second); got != "Flashing · Erasing (0:06)" {
		t.Errorf("an erase 2 s after its picture: %q", got)
	}
	p.Steps[0].State, p.Steps[0].Text = flash.StepDone, "Erased"
	if got := lockJobLine(p, flash.JobFlash, 0); got != "Flashing · Erased" {
		t.Errorf("between steps: %q", got)
	}
}

// Help lists the gesture (S19), and says an Esc hold still ends meshflash, locked or not: on the LCD and in the CLI
// app alike, where the launcher sends SIGTERM on it too (the kit's docs/device.md, its Keys table).
func TestLockInHelp(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		m := New(Options{Backend: newFakeBackend(), TerminalKeys: terminal})
		keys, footer := m.helpLines()
		if !strings.Contains(strings.Join(keys, "\n"), "hold tab       lock the screen") {
			t.Errorf("TerminalKeys %v: Help's keys don't list the lock:\n%s", terminal, strings.Join(keys, "\n"))
		}
		want := "Holding Esc 3 s still ends meshflash, locked or not (the launcher does that)"
		if all := strings.Join(footer, "\n"); !strings.Contains(all, want) {
			t.Errorf("TerminalKeys %v: Help doesn't say an Esc hold ends meshflash:\n%s", terminal, all)
		}
	}
}
