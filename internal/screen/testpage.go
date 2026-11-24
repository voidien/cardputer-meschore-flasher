package screen

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// TestPage is the screen `loom --screen-test` shows: every printable character, the client's symbols, the 16 colors,
// a beat line and a chapter title as the client draws them, and the last key pressed.
func TestPage(cols int, lastKey string, keys int) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s); b.WriteByte('\n') }
	line(fmt.Sprintf("Loom screen test · %d columns", cols))
	var ascii strings.Builder
	for r := '!'; r <= '~'; r++ {
		ascii.WriteRune(r)
	}
	a := ascii.String()
	for len(a) > 0 {
		n := min(cols, len(a))
		line(a[:n])
		a = a[n:]
	}
	line("“Quotes” it’s — dash … ▸ ✎ ● ○ · ¢ • →")
	var colors strings.Builder
	for c := 0; c < 16; c++ {
		if c < 8 {
			fmt.Fprintf(&colors, "\x1b[3%dm%X", c, c)
		} else {
			fmt.Fprintf(&colors, "\x1b[9%dm%X", c-8, c)
		}
	}
	line(colors.String() + "\x1b[0m \x1b[7m reversed \x1b[0m")
	line("\x1b[90m▸ a beat, in grey\x1b[0m")
	line("\x1b[7m Chapter 2: a title, reversed \x1b[0m")
	if keys == 0 {
		line("Press keys: each one shows here.")
	} else {
		line(fmt.Sprintf("Key %d: %s", keys, lastKey))
	}
	b.WriteString("\x1b[90mHold Esc 3 s to leave (or wait 2 min)\x1b[0m")
	return b.String()
}

// RunTest shows the test page on the LCD and the keys pressed, until Esc is held or limit passes. It logs each key
// event (its Linux code and what the client makes of it) to o.Log.
func RunTest(o Options, limit time.Duration) error {
	o.defaults()
	s, fb, err := Open(o)
	if err != nil {
		return err
	}
	defer fb.Close()
	kbd, err := os.Open(o.Keyboard)
	if err != nil {
		return fmt.Errorf("opening the keyboard: %w", err)
	}
	fmt.Fprintf(o.Log, "screen test: %d×%d cells, font %s\n", s.Cols, s.Rows, o.Font)

	var mu sync.Mutex
	last, count := "", 0
	draw := func() {
		mu.Lock()
		defer mu.Unlock()
		if err := s.Draw(Parse(TestPage(s.Cols, last, count), s.Cols, s.Rows)); err != nil {
			fmt.Fprintln(o.Log, "screen:", err)
		}
	}
	if err := s.Clear(); err != nil {
		return err
	}
	draw()

	done := make(chan struct{})
	var once sync.Once
	quit := func() { once.Do(func() { close(done) }) }
	defer onSignals(quit)()
	defer quietTerminal(int(os.Stdin.Fd()))()
	reading := make(chan struct{})
	go func() {
		defer close(reading)
		var kb Keyboard
		esc := escHold{hold: o.HoldToQuit, quit: quit}
		err := ReadKeyEvents(kbd, func(e KeyEvent) {
			if e.Code == keyEsc {
				esc.key(e.Value)
			}
			msg, ok := kb.Key(e.Code, e.Value)
			name := fmt.Sprintf("code %d", e.Code)
			if ok {
				name = fmt.Sprintf("%q (code %d)", msg.String(), e.Code)
			}
			fmt.Fprintf(o.Log, "%s %s\n", name, map[int32]string{0: "up", 1: "down", 2: "repeat"}[e.Value])
			if e.Value == 1 {
				mu.Lock()
				last, count = name, count+1
				mu.Unlock()
				draw()
			}
		})
		if err != nil {
			fmt.Fprintln(o.Log, "keyboard:", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(limit):
	}
	// Closing the keyboard ends the reader; once it has, nothing else draws.
	kbd.Close()
	<-reading
	fmt.Fprintln(o.Log, "screen test: done")
	return s.Clear()
}
