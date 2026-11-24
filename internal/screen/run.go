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
}

func (o *Options) defaults() {
	if o.Framebuffer == "" {
		o.Framebuffer = "/dev/fb0"
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
// final model (unwrapped) so the caller can save what's unsent.
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

	frames := make(chan string, 1)
	p := tea.NewProgram(tap{Model: m, frames: frames}, tea.WithInput(nil), tea.WithOutput(io.Discard),
		tea.WithoutSignalHandler())
	defer onSignals(p.Quit)()
	defer quietTerminal(int(os.Stdin.Fd()))()

	drawn := make(chan struct{})
	go func() {
		defer close(drawn)
		if err := s.Clear(); err != nil {
			fmt.Fprintln(o.Log, "screen:", err)
		}
		for v := range frames {
			if err := s.Draw(Parse(v, s.Cols, s.Rows)); err != nil {
				fmt.Fprintln(o.Log, "screen:", err)
			}
		}
	}()
	go p.Send(tea.WindowSizeMsg{Width: s.Cols, Height: s.Rows})
	go func() {
		err := ReadKeys(kbd, o.HoldToQuit, p.Send, p.Quit)
		if err != nil {
			fmt.Fprintln(o.Log, "keyboard:", err)
		}
	}()

	final, err := p.Run()
	kbd.Close()
	close(frames)
	<-drawn
	if cerr := s.Clear(); cerr != nil {
		fmt.Fprintln(o.Log, "screen:", cerr)
	}
	return Unwrap(final), err
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
	var kb Keyboard
	esc := escHold{hold: hold, quit: quit}
	return ReadKeyEvents(r, func(e KeyEvent) {
		if e.Code == keyEsc {
			esc.key(e.Value)
			if e.Value == 1 {
				send(tea.KeyMsg{Type: tea.KeyEsc})
			}
			return
		}
		if msg, ok := kb.Key(e.Code, e.Value); ok {
			send(msg)
		}
	})
}

// escHold calls quit once Esc has been held down for hold.
type escHold struct {
	hold  time.Duration
	quit  func()
	mu    sync.Mutex
	timer *time.Timer
}

// key takes an Esc event: 1 pressed starts the wait, 0 released cancels it, and 2 (a repeat) changes nothing.
func (h *escHold) key(value int32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch value {
	case 1:
		if h.timer != nil {
			h.timer.Stop()
		}
		h.timer = time.AfterFunc(h.hold, h.quit)
	case 0:
		if h.timer != nil {
			h.timer.Stop()
			h.timer = nil
		}
	}
}

// tap passes every frame the model draws to the screen, keeping only the latest if drawing falls behind.
type tap struct {
	tea.Model
	frames chan string
}

func (t tap) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := t.Model.Update(msg)
	return tap{Model: m, frames: t.frames}, cmd
}

func (t tap) View() string {
	v := t.Model.View()
	select {
	case t.frames <- v:
	default:
		select {
		case <-t.frames:
		default:
		}
		select {
		case t.frames <- v:
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
