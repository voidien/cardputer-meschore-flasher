package screen

// meshflash's tests (not the kit's) of the held keys: the screen lock's Tab hold and the Esc hold, as readKeys sees
// them from the keyboard, in virtual time. The reader hands out one event at a time and moves the holds' clock on
// before each, as the kernel would space them; nothing here sleeps.

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// virtualTimers is an afterFunc on a clock that only a test moves.
type virtualTimers struct {
	mu  sync.Mutex
	now time.Duration
	ts  []*virtualTimer
}

type virtualTimer struct {
	at   time.Duration
	f    func()
	done bool // fired or stopped
}

func (c *virtualTimers) after(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &virtualTimer{at: c.now + d, f: f}
	c.ts = append(c.ts, t)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		was := !t.done
		t.done = true
		return was
	}
}

// advance moves the clock on by d, running each timer that falls due on the way, earliest first.
func (c *virtualTimers) advance(d time.Duration) {
	c.mu.Lock()
	end := c.now + d
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *virtualTimer
		for _, t := range c.ts {
			if !t.done && t.at <= end && (next == nil || t.at < next.at) {
				next = t
			}
		}
		if next == nil {
			c.now = end
			c.mu.Unlock()
			return
		}
		c.now, next.done = next.at, true
		c.mu.Unlock()
		next.f()
	}
}

// keyStep is one key event, wait after the one before it.
type keyStep struct {
	wait  time.Duration
	code  uint16
	value int32
}

// keyScript is the keyboard: each Read moves the clock on by the next step's wait, then gives its event.
type keyScript struct {
	clock *virtualTimers
	steps []keyStep
}

func (r *keyScript) Read(p []byte) (int, error) {
	if len(r.steps) == 0 {
		return 0, io.EOF
	}
	st := r.steps[0]
	r.steps = r.steps[1:]
	r.clock.advance(st.wait)
	return copy(p, event(st.code, st.value)), nil
}

// held is a key pressed, repeating every 33 ms after 250 ms as the Zero's keyboard does, and released after d.
func held(code uint16, d time.Duration) []keyStep {
	steps := []keyStep{{0, code, 1}}
	at := 250 * time.Millisecond
	last := time.Duration(0)
	for ; at < d; at += 33 * time.Millisecond {
		steps = append(steps, keyStep{at - last, code, 2})
		last = at
	}
	return append(steps, keyStep{d - last, code, 0})
}

// tapped is a key pressed and released after wait.
func tapped(wait time.Duration, code uint16) []keyStep {
	return []keyStep{{wait, code, 1}, {50 * time.Millisecond, code, 0}}
}

type lockTestMsg struct{}

// keysFor runs readKeys over steps with the screen lock's Tab hold (unless noLock) and gives what it sent: each key
// as its String, the lock as "LOCK", a quit as "QUIT".
func keysFor(t *testing.T, steps []keyStep, noLock bool, then time.Duration) string {
	t.Helper()
	clock := &virtualTimers{}
	var mu sync.Mutex
	var sent []string
	note := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, s)
	}
	h := holds{esc: 3 * time.Second, quit: func() { note("QUIT") }, tab: 3 * time.Second, tabMsg: lockTestMsg{},
		after: clock.after}
	if noLock {
		h.tabMsg = nil
	}
	err := readKeys(&keyScript{clock: clock, steps: steps}, h, func(m tea.Msg) {
		switch m := m.(type) {
		case lockTestMsg:
			note("LOCK")
		case tea.KeyMsg:
			note(m.String())
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(then)
	mu.Lock()
	defer mu.Unlock()
	return strings.Join(sent, ",")
}

func TestTabHeldIsOneTabThenTheLock(t *testing.T) {
	if got := keysFor(t, held(keyTab, 4*time.Second), false, 0); got != "tab,LOCK" {
		t.Errorf("Tab held 4 s sent %q, want one tab and then the lock", got)
	}
}

func TestTabTappedIsOneTab(t *testing.T) {
	steps := append(tapped(0, keyTab), tapped(time.Second, 30)...)
	if got := keysFor(t, steps, false, 10*time.Second); got != "tab,a" {
		t.Errorf("a tapped Tab sent %q, want a tab and nothing more", got)
	}
}

func TestTabReleasedBeforeThreeSecondsDoesntLock(t *testing.T) {
	steps := append(held(keyTab, 2900*time.Millisecond), tapped(100*time.Millisecond, keyTab)...)
	if got := keysFor(t, steps, false, 10*time.Second); got != "tab,tab" {
		t.Errorf("Tab held 2.9 s, then tapped, sent %q, want two tabs and no lock", got)
	}
}

func TestShiftTabHeldLocksToo(t *testing.T) {
	steps := append([]keyStep{{0, keyLeftShift, 1}}, held(keyTab, 3500*time.Millisecond)...)
	if got := keysFor(t, steps, false, 0); got != "shift+tab,LOCK" {
		t.Errorf("Shift+Tab held sent %q, want one shift+tab and the lock", got)
	}
}

// Without TabHeld, Tab is the kit's: an ordinary key whose repeats are sent.
func TestTabWithoutTheLockRepeats(t *testing.T) {
	got := keysFor(t, held(keyTab, 400*time.Millisecond), true, 10*time.Second)
	if n := strings.Count(got, "tab"); n < 3 || strings.Contains(got, "LOCK") {
		t.Errorf("Tab held without the lock sent %q, want it repeated and no lock", got)
	}
}

func TestEscHeldQuitsInVirtualTime(t *testing.T) {
	if got := keysFor(t, held(keyEsc, 3500*time.Millisecond), false, 0); got != "esc,QUIT" {
		t.Errorf("Esc held 3.5 s sent %q, want one esc, then the quit", got)
	}
	if got := keysFor(t, held(keyEsc, 2*time.Second), false, 10*time.Second); got != "esc" {
		t.Errorf("Esc held 2 s sent %q, want one esc and no quit", got)
	}
}

// Esc and Tab held together each keep their own hold.
func TestEscAndTabHeldTogether(t *testing.T) {
	steps := []keyStep{{0, keyTab, 1}, {time.Second, keyEsc, 1}, {2500 * time.Millisecond, keyTab, 0}}
	if got := keysFor(t, steps, false, 5*time.Second); got != "tab,esc,LOCK,QUIT" {
		t.Errorf("Tab, then Esc, held: sent %q", got)
	}
}
