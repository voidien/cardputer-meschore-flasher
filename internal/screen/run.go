package screen

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Options say where the screen, keyboard and font are. The zero value means the CardputerZero's.
type Options struct {
	Framebuffer string // default /dev/fb0
	Sys         string // its sysfs folder, default /sys/class/graphics/fb0
	Keyboard    string // default DefaultKeyboard
	Font        string // default DefaultFont
	Log         io.Writer
	HoldToQuit  time.Duration // how long Esc is held to quit, default 3 s (as the launcher does for terminal programs)

	// meshflash's (the package doc lists what isn't the kit's):

	// TabHeld, when set, is the message sent to the model once Tab has been held down for HoldToLock: meshflash's
	// screen lock (internal/ui's LockMsg). Tab then acts once per press, its repeats dropped, so a held Tab is one
	// Tab and then the lock. nil leaves Tab an ordinary key that repeats, as the kit's.
	TabHeld    tea.Msg
	HoldToLock time.Duration // how long Tab is held for TabHeld, default 3 s
	// Backlight is a backlight's sysfs folder (brightness), switched off while the model is Dark (Dimmable) and on
	// again before Run returns. "" is the device's own (the first in BacklightClass) when Framebuffer is
	// DeviceFramebuffer, and none otherwise. A folder under /sys is used only when Framebuffer is DeviceFramebuffer,
	// so a run into a file never touches a real backlight: it may be given a fake folder, for a test.
	Backlight string

	// For this package's tests: a fake backlight in place of Backlight's, and a virtual clock for the key holds
	// (nil: time.AfterFunc).
	light Backlight
	after afterFunc
}

func (o *Options) defaults() {
	if o.Framebuffer == "" {
		o.Framebuffer = DeviceFramebuffer
	}
	if o.Sys == "" {
		o.Sys = "/sys/class/graphics/fb0"
	}
	if o.Keyboard == "" {
		o.Keyboard = DefaultKeyboard
	}
	if o.Font == "" {
		o.Font = DefaultFont
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.HoldToQuit <= 0 {
		o.HoldToQuit = 3 * time.Second
	}
	if o.HoldToLock <= 0 {
		o.HoldToLock = 3 * time.Second
	}
}

// Open loads the font and opens the framebuffer as a Screen. Close the file when done.
func Open(o Options) (*Screen, *os.File, error) {
	o.defaults()
	font, err := LoadFontFile(o.Font)
	if err != nil {
		return nil, nil, fmt.Errorf("loading the font: %w", err)
	}
	font.AddStandIns()
	geo, err := FramebufferGeometry(o.Sys)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the screen's size: %w", err)
	}
	fb, err := os.OpenFile(o.Framebuffer, os.O_WRONLY, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("opening the screen: %w", err)
	}
	s, err := NewScreen(fb, geo, font)
	if err != nil {
		fb.Close()
		return nil, nil, err
	}
	return s, fb, nil
}

// Run runs a Bubble Tea model on the LCD until it quits, Esc is held, or the program gets SIGTERM. It returns the
// final model (unwrapped) so the caller can save what's unsent. While the model is Dark (Dimmable) the screen is
// drawn black and the backlight is off; however Run ends (a quit, an Esc hold, SIGTERM, a panic Bubble Tea caught),
// the backlight is on again before the screen is blanked (S8), so the home screen is never left dark.
func Run(m tea.Model, o Options) (tea.Model, error) {
	o.defaults()
	s, fb, err := Open(o)
	if err != nil {
		return m, err
	}
	defer fb.Close()
	kbd, err := os.Open(o.Keyboard)
	if err != nil {
		return m, fmt.Errorf("opening the keyboard: %w", err)
	}
	light := o.backlight()

	frames := make(chan frame, 1)
	p := tea.NewProgram(tap{Model: m, frames: frames}, tea.WithInput(nil), tea.WithOutput(io.Discard),
		tea.WithoutSignalHandler())
	defer onSignals(p.Quit)()
	defer quietTerminal(int(os.Stdin.Fd()))()

	drawn := make(chan struct{})
	go func() {
		defer close(drawn)
		draw(s, light, frames, o.Log)
	}()
	go p.Send(tea.WindowSizeMsg{Width: s.Cols, Height: s.Rows})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				light.On()
				fmt.Fprintln(o.Log, "keyboard: stopped:", r)
			}
		}()
		err := readKeys(kbd, holds{esc: o.HoldToQuit, quit: p.Quit, tab: o.HoldToLock, tabMsg: o.TabHeld,
			after: o.after}, p.Send)
		if err != nil {
			fmt.Fprintln(o.Log, "keyboard:", err)
		}
	}()

	final, err := p.Run()
	kbd.Close()
	close(frames)
	<-drawn
	leave(s, light, o.Log)
	return Unwrap(final), err
}

// frame is one View for the screen, and whether the model wants the screen dark.
type frame struct {
	view string
	dark bool
}

// draw shows each frame until frames is closed. The first dark one draws the screen black and turns the light off;
// the next one that isn't draws its picture first and then turns the light on, so the old picture never shows. A
// panic while drawing turns the light on and stops drawing, but not the program (a flash under way carries on).
func draw(s *Screen, light Backlight, frames <-chan frame, log io.Writer) {
	defer func() {
		if r := recover(); r != nil {
			light.On()
			fmt.Fprintln(log, "screen: drawing stopped:", r)
			for range frames {
			}
		}
	}()
	if err := s.Clear(); err != nil {
		fmt.Fprintln(log, "screen:", err)
	}
	dark := false
	for f := range frames {
		if f.dark {
			if !dark {
				dark = true
				if err := s.Draw(NewGrid(s.Cols, s.Rows)); err != nil {
					fmt.Fprintln(log, "screen:", err)
				}
				light.Off()
			}
			continue
		}
		if err := s.Draw(Parse(f.view, s.Cols, s.Rows)); err != nil {
			fmt.Fprintln(log, "screen:", err)
		}
		if dark {
			dark = false
			light.On()
		}
	}
}

// leave ends a run: the light on first, then the screen blank (S8), so the launcher's redraw is clean and lit.
func leave(s *Screen, light Backlight, log io.Writer) {
	light.On()
	if err := s.Clear(); err != nil {
		fmt.Fprintln(log, "screen:", err)
	}
}

// onSignals calls quit on SIGTERM or SIGINT until the returned function is called. Bubble Tea's own signal handler
// isn't used: it hands its quit to the event loop on a channel nobody reads once the program has quit, and its
// shutdown waits for that forever. On the device that is the usual case, not a rare one: an Esc hold quits the client
// after 3 s, and the launcher, watching Esc too, sends SIGTERM a moment later, while Bubble Tea draws its last frame;
// the client then hung until the launcher's SIGKILL, never blanking the screen or saving. quit (Program.Quit) doesn't
// block once the program has ended.
func onSignals(quit func()) (stop func()) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sigs:
				quit()
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(sigs)
		close(done)
	}
}

// ReadKeys reads key events from r and sends each key as a message. Esc is sent once when pressed, not repeated;
// held for hold, it calls quit instead (the launcher's Esc hold, which ends a terminal program, does the same).
func ReadKeys(r io.Reader, hold time.Duration, send func(tea.Msg), quit func()) error {
	return readKeys(r, holds{esc: hold, quit: quit}, send)
}

// holds are what holding a key down does: Esc held for esc calls quit; with tabMsg set, Tab held for tab sends
// tabMsg (meshflash's screen lock), and Tab acts once per press, its repeats dropped. after is the clock (nil:
// time.AfterFunc).
type holds struct {
	esc    time.Duration
	quit   func()
	tab    time.Duration
	tabMsg tea.Msg
	after  afterFunc
}

func readKeys(r io.Reader, h holds, send func(tea.Msg)) error {
	var kb Keyboard
	esc := keyHold{hold: h.esc, fire: h.quit, after: h.after}
	tab := keyHold{hold: h.tab, fire: func() { send(h.tabMsg) }, after: h.after}
	return ReadKeyEvents(r, func(e KeyEvent) {
		switch {
		case e.Code == keyEsc:
			esc.key(e.Value)
			if e.Value == 1 {
				send(tea.KeyMsg{Type: tea.KeyEsc})
			}
			return
		case e.Code == keyTab && h.tabMsg != nil:
			tab.key(e.Value)
			if e.Value == 2 {
				return // a held Tab is one Tab, then the lock: never a run of Tabs
			}
		}
		if msg, ok := kb.Key(e.Code, e.Value); ok {
			send(msg)
		}
	})
}

// afterFunc runs f once d has passed and returns what cancels it: time.AfterFunc's, or a test's virtual clock.
type afterFunc func(d time.Duration, f func()) (stop func() bool)

func realAfter(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }

// keyHold calls fire once a key has been held down for hold. (The kit's escHold, with the clock passed in.)
type keyHold struct {
	hold  time.Duration
	fire  func()
	after afterFunc
	mu    sync.Mutex
	stop  func() bool
}

// key takes the key's event: 1 pressed starts the wait, 0 released cancels it, and 2 (a repeat) changes nothing.
func (h *keyHold) key(value int32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch value {
	case 1:
		if h.stop != nil {
			h.stop()
		}
		after := h.after
		if after == nil {
			after = realAfter
		}
		h.stop = after(h.hold, h.fire)
	case 0:
		if h.stop != nil {
			h.stop()
			h.stop = nil
		}
	}
}

// tap passes every frame the model draws to the screen, keeping only the latest if drawing falls behind, with
// whether the model wants the screen dark (Dimmable). Dark is asked right after View, on Bubble Tea's own goroutine,
// so the model's state is read where it is changed.
type tap struct {
	tea.Model
	frames chan frame
}

func (t tap) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := t.Model.Update(msg)
	return tap{Model: m, frames: t.frames}, cmd
}

func (t tap) View() string {
	v := t.Model.View()
	f := frame{view: v}
	if d, ok := t.Model.(Dimmable); ok {
		f.dark = d.Dark()
	}
	select {
	case t.frames <- f:
	default:
		select {
		case <-t.frames:
		default:
		}
		select {
		case t.frames <- f:
		default:
		}
	}
	return v
}

// Unwrap is the model inside a tap, or m itself.
func Unwrap(m tea.Model) tea.Model {
	if t, ok := m.(tap); ok {
		return t.Model
	}
	return m
}
