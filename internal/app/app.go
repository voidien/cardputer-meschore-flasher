// Package app starts meshflash: the flags, the store and config, the backend over the real boards and network (or
// the demo build's fake world), the screens in a terminal or on the LCD, and the end of the program: the draft saved,
// the running job stopped within a bounded time, and how it ended written to screen.log.
//
// Its shape is the CardputerZero app kit's internal/app (code/internal/app/app.go, the owner's own code): Main,
// screenLog, run, absorbSIGTERM and colorProfile, with meshflash's dependencies in place of Loom's. Where it departs
// from the kit's (docs/phase2-design.md 7.1):
//
//   - Main's work is run(args, stdin, stdout, stderr), which returns the exit code; parse, check, start, deps,
//     openScreenLog and finish are its parts, so tests reach each without the process-wide ones (the memory limit,
//     the signals, the real folders).
//   - screen.log is opened on every run, in the terminal too (SPEC.md 8), and --version opens nothing, so it never
//     empties the log of a meshflash already running.
//   - SIGHUP is absorbed as SIGTERM is: the CLI app's terminal closing sends it, and a flash under way is then left to
//     finish with no screen (owner question 19), as after a crash.
//   - In a terminal Bubble Tea's own signal handler is replaced by the app's (inTerminal), as internal/screen replaces
//     it on the LCD, and for the same reason.
//   - --backlight-sys, screen.Options.TabHeld and ui.Options.TerminalKeys wire the screen lock (docs/phase2-design.md
//     18).
//   - After the screens end, finish stops the running Job the safe way (or, after a crash, lets it finish), waits a
//     bounded time, writes how it ended to screen.log and saves a radio the node took.
//   - The real dependencies are built in one function that refuses to run in a test (realDeps), and the demo build
//     draws only into a file (check).
//
// Phase 2 contract (docs/phase2-design.md): builder E writes the bodies; Program, Env, Fake and Main's signature are
// fixed.
package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"meshflash/internal/flash"
	"meshflash/internal/screen"
	"meshflash/internal/store"
	"meshflash/internal/ui"
)

// The real backend and store are what the screens take, and the screens' model is what internal/screen asks whether
// to go dark (the screen lock: ui can't import screen, so screen.Run finds Dark only by a type assertion, which would
// fail quietly, leaving the LCD lit, if Dark were renamed); the build stops here if any of them drifts.
var (
	_ ui.Backend      = (*flash.Backend)(nil)
	_ ui.Store        = (*store.Store)(nil)
	_ screen.Dimmable = (*ui.Model)(nil)
)

// Program says which program this is: meshflash, or the demo build's meshflash-demo.
type Program struct {
	// Name is the program's own name: its folders (~/.config/<Name>, ~/.cache/<Name>), what --version prints
	// ("<Name> <Version>", S3), and screen.log's first line.
	Name string
	// Command is what it is installed as (~/.local/bin/<Command>); it starts its error messages.
	Command string
	// Version is meshflash's own version (CHANGELOG.md): Help shows it and the User-Agent sends it.
	Version string
	// Fake, when set, replaces the boards, the network, the battery and the clock with a fake world: the demo build
	// sets it (cmd/meshflash/demo.go, -tags demo). The release binary leaves it nil and never links a fake. Every
	// test of this package sets it: the real dependencies (serialport.Open, usbid.Scan, the real catalog client) are
	// built in one function that refuses to run under testing.Testing(), so no test opens a real port.
	Fake Fake
}

// Fake makes the backend's dependencies for the demo build, given the folders the app uses.
type Fake func(Env) (flash.Deps, error)

// Env is what a Fake is given.
type Env struct {
	ConfigDir, CacheDir string // the Program's own folders
	Version             string // Program.Version
	// Log is screen.log: the one locked writer that the screens (ui.Options.Log) and the backend (Deps.Logf) write
	// too, so lines never interleave.
	Log io.Writer
}

// Main reads the flags (--version, --screen, --screen-test, --font, --framebuffer, --keyboard, --framebuffer-sys, as
// the kit's, and meshflash's --backlight-sys: with --screen, a backlight folder holding brightness, for testing; ""
// is the device's own when drawing on /dev/fb0 and none with any other --framebuffer) and runs the program; it exits
// with 1 on an error, which it also writes to screen.log. Under --screen it gives screen.Run TabHeld: ui.LockMsg{}
// (the screen lock's 3 s Tab hold) and the backlight folder; in a terminal it sets ui.Options.TerminalKeys
// (docs/phase2-design.md 7.1 and 18).
func Main(p Program) {
	if code := p.run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

// run is Main with its command line and files passed in. It returns the exit code: 0; 1 on an error, written to
// stderr and to screen.log; 2 for a command line it can't read, as the flag package's own.
func (p Program) run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	f, err := p.parse(args, stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return 0
	case err != nil:
		return 2
	case f.version:
		// Before anything is opened: screen.log is started afresh each run, and --version while meshflash runs
		// mustn't empty the running one's.
		fmt.Fprintln(stdout, p.Name, p.Version)
		return 0
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, p.Command+":", err)
		return 1
	}
	if err := p.check(f); err != nil {
		return fail(err)
	}
	st, err := store.OpenApp(p.Name)
	if err != nil {
		return fail(err)
	}
	log := p.openLog(st.CacheDir(), f, stderr)
	defer log.close()

	if f.screenTest {
		err = screen.RunTest(*f.screenOptions(log), 2*time.Minute)
	} else {
		// The device has about 100 MB free, shared with the launcher: past 32 MB the garbage collector works harder
		// rather than let the heap grow (a soft limit, S9).
		debug.SetMemoryLimit(32 << 20)
		absorbSIGTERM()
		hups, _ := absorbSIGHUP()
		err = p.start(f, st, log, stdin, stdout, stderr, realRunners(hups))
	}
	if err != nil {
		if log.file != nil {
			log.linef("%s: %v", p.Command, err)
		}
		return fail(err)
	}
	return 0
}

// flags are the command line.
type flags struct {
	version, screen, screenTest                 bool
	font, framebuffer, keyboard, sys, backlight string
}

// parse reads the command line. The flag package writes what's wrong, and the usage, to stderr; -h and --help give
// flag.ErrHelp.
func (p Program) parse(args []string, stderr io.Writer) (flags, error) {
	var f flags
	fs := flag.NewFlagSet(p.Command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&f.version, "version", false, "print the version and exit")
	fs.BoolVar(&f.screen, "screen", false, "draw on the LCD itself in a pixel font, rather than in a terminal")
	fs.BoolVar(&f.screenTest, "screen-test", false, "show a test page on the LCD and the keys pressed, then exit")
	fs.StringVar(&f.font, "font", screen.DefaultFont, "with --screen: the PSF font to draw in")
	fs.StringVar(&f.framebuffer, "framebuffer", screen.DeviceFramebuffer,
		"with --screen: where to draw (a file, for testing)")
	fs.StringVar(&f.keyboard, "keyboard", screen.DefaultKeyboard,
		"with --screen: where to read keys (a FIFO, for testing)")
	fs.StringVar(&f.sys, "framebuffer-sys", "/sys/class/graphics/fb0", "with --screen: a folder giving the screen's size "+
		"(virtual_size, bits_per_pixel), for testing")
	fs.StringVar(&f.backlight, "backlight-sys", "", "with --screen: a backlight folder holding brightness, for testing "+
		"(default: the LCD's own when drawing on "+screen.DeviceFramebuffer+", none with any other --framebuffer)")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if fs.NArg() > 0 {
		// The kit ignores them; here "meshflash screen" (no dashes) would start in the terminal without a word.
		err := fmt.Errorf("unexpected argument %q", fs.Arg(0))
		fmt.Fprintf(stderr, "%s: %v\n", p.Command, err)
		fs.Usage()
		return f, err
	}
	return f, nil
}

// check refuses what this build must never do. The demo build (a Fake) draws only into a regular file, never on the
// LCD or any other device: its fake boards must never be mistaken for real ones on the device's screen, and drawing
// only into a file it never switches the real backlight either (internal/screen switches one under /sys only when
// drawing on /dev/fb0).
func (p Program) check(f flags) error {
	if p.Fake == nil || !(f.screen || f.screenTest) {
		return nil
	}
	if fi, err := os.Stat(f.framebuffer); err == nil && fi.Mode().IsRegular() {
		return nil
	}
	return fmt.Errorf("%s draws only into a file made for it (--framebuffer <a file>), never on %s", p.Name,
		f.framebuffer)
}

// screenOptions are internal/screen's, with --screen or --screen-test; nil in a terminal. They carry the screen
// lock's Tab hold (TabHeld; HoldToLock is left at its 3 s) and the backlight folder for its dark screen, as given:
// whether that folder is used is internal/screen's to decide (one under /sys only when drawing on /dev/fb0, so a run
// into a file can switch only a fake one).
func (f flags) screenOptions(log io.Writer) *screen.Options {
	if !f.screen && !f.screenTest {
		return nil
	}
	return &screen.Options{Font: f.font, Framebuffer: f.framebuffer, Keyboard: f.keyboard, Sys: f.sys, Log: log,
		TabHeld: ui.LockMsg{}, Backlight: f.backlight}
}

// uiOptions start the screens.
func (p Program) uiOptions(be ui.Backend, st ui.Store, cfg store.Config, log io.Writer, onLCD bool) ui.Options {
	return ui.Options{Backend: be, Store: st, Config: cfg, Name: p.Name, Version: p.Version, Log: log,
		// The launcher's bar, with its battery, is frozen while meshflash draws the LCD, so the status line shows it.
		ShowBattery: onLCD,
		// In a terminal a held Tab arrives as Tab again and again and its release never does, so the frame finds the
		// lock's hold from the repeats; on the LCD internal/screen finds it from the key's press and release
		// (screen.Options.TabHeld).
		TerminalKeys: !onLCD}
}

// errTerminalClosed is how the screens ended when the terminal went away.
var errTerminalClosed = errors.New("the terminal closed (SIGHUP)")

// start runs the screens once the flags are read and screen.log is open: the config, the backend over its
// dependencies, the screens on the LCD or in the terminal until they end, then the end of the program (finish).
func (p Program) start(f flags, st appStore, log *logWriter, stdin io.Reader, stdout, stderr io.Writer,
	r runners) error {
	cfg, err := st.LoadConfig()
	if err != nil {
		// A damaged config.json already reads as none; one that can't be read at all doesn't stop meshflash either.
		log.linef("%s: can't read %s, so it starts without the boards and the radio it remembers: %v", p.Name,
			store.ConfigFile, err)
		cfg = store.Config{}
	}
	// Before ui.New, so before any style is made.
	lipgloss.SetColorProfile(colorProfile())

	d, err := deps(p, Env{ConfigDir: st.ConfigDir(), CacheDir: st.CacheDir(), Version: p.Version, Log: log})
	if err != nil {
		return err
	}
	so := f.screenOptions(log)
	// m is the model the screens update in place (pointer methods), and the one finish ends: screen.Run gives back
	// nil after a panic Bubble Tea caught, and the screens' Job is still m's.
	m := ui.New(p.uiOptions(flash.New(d), st, cfg, log, so != nil))
	if so != nil {
		_, err = r.lcd(m, *so)
	} else {
		var hungUp bool
		if hungUp, err = r.terminal(m, stdin, stdout); hungUp {
			err = errTerminalClosed
		}
	}
	// Every way the screens end by being told to (a quit, an Esc hold, SIGTERM) returns no error; an error is a
	// panic Bubble Tea caught, the terminal gone or failing, or screens that never started (then nothing runs).
	end := ending{lost: err != nil}
	switch {
	case errors.Is(err, tea.ErrProgramPanic):
		end.why = "it crashed"
	case errors.Is(err, errTerminalClosed):
		end.why = "its terminal closed"
	case err != nil:
		end.why = err.Error()
	}
	finish(m, end, st, log, p.Name, stderr)
	return err
}

// appStore is what start needs of the store: *store.Store, or a test's over it with a config.json it can't read.
type appStore interface {
	ui.Store
	ConfigDir() string
	CacheDir() string
	LoadConfig() (store.Config, error)
}

var _ appStore = (*store.Store)(nil)

// runners start the screens and return when they end: on the LCD, or in the terminal. Tests replace them, so they see
// exactly what the screens are given, and nothing is drawn.
type runners struct {
	// lcd is screen.Run.
	lcd func(m tea.Model, o screen.Options) (tea.Model, error)
	// terminal runs the screens in the terminal; hungUp says the terminal went away (SIGHUP) first.
	terminal func(m tea.Model, in io.Reader, out io.Writer) (hungUp bool, err error)
}

// realRunners are the real ones, with the SIGHUPs absorbSIGHUP catches.
func realRunners(hups <-chan os.Signal) runners {
	return runners{lcd: screen.Run, terminal: func(m tea.Model, in io.Reader, out io.Writer) (bool, error) {
		return inTerminal(m, in, out, hups)
	}}
}

// inTerminal runs the screens in the terminal's alternate screen (Bubble Tea, reading keys from in) until they quit,
// SIGTERM or SIGINT quits them, or a SIGHUP on hups says the terminal has gone.
//
// Bubble Tea's own signal handler isn't used, as internal/screen doesn't use it on the LCD, and for the same reason: it
// hands its quit to the event loop on a channel nobody reads once the program has quit, and its shutdown then waits
// for that forever. A SIGTERM that comes just as the screens quit (the launcher's, at the end of an Esc hold) would
// leave meshflash stuck until SIGKILL, the flash not stopped and screen.log not told. Program.Quit doesn't block once
// the program has ended.
//
// A closed terminal may end the input with no error at all (Bubble Tea ignores the end of its input and waits for
// keys that never come), so the SIGHUP that comes with it ends the screens, by a quit like SIGTERM's: what Bubble Tea
// then draws and restores goes nowhere, and does no harm. Not by Kill, whose shutdown closes the input while Bubble
// Tea's reader may still be reading it (a data race in Bubble Tea v1.3.10 and cancelreader).
func inTerminal(m tea.Model, in io.Reader, out io.Writer, hups <-chan os.Signal) (hungUp bool, err error) {
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithInput(in), tea.WithOutput(out), tea.WithoutSignalHandler())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)
	var lost atomic.Bool
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-sigs:
				prog.Quit()
			case <-hups:
				lost.Store(true)
				prog.Quit()
			case <-done:
				return
			}
		}
	}()
	_, err = prog.Run()
	return lost.Load(), err
}

// absorbSIGTERM stops a SIGTERM from killing the program outright, so the shutdown always runs: the draft saved, the
// running flash stopped the safe way and screen.log told what it left. The screens' handler (Bubble Tea's in the
// kit; inTerminal's and internal/screen's here) turns the first SIGTERM into a quit, but stops listening when the
// screens end, and a later one would end the program at once, before finish. On the LCD that happens: holding Esc
// quits after 3 s, and the launcher, which watches Esc too, sends the whole program SIGTERM a moment later
// (docs/device.md in the kit, "Apps started from the home screen"). The program exits by itself once finish is done,
// which is bounded (shutdownWait), since only the launcher's SIGKILL, 3 s after its SIGTERM, can end it after this.
// It returns a function that undoes it, for tests.
func absorbSIGTERM() (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	return func() { signal.Stop(ch) }
}

// absorbSIGHUP stops a SIGHUP from killing the program, as absorbSIGTERM does a SIGTERM: the CLI app's terminal
// closing sends it, and a flash under way is then left to finish with no screen (owner question 19) rather than cut
// off part-way. It returns the channel the SIGHUPs arrive on: in a terminal they end the screens (inTerminal); on the
// LCD, whose screens and keys never needed the terminal meshflash was started from, nothing reads it and they change
// nothing. And a function that undoes it, for tests.
func absorbSIGHUP() (hups <-chan os.Signal, stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	return ch, func() { signal.Stop(ch) }
}

// colorProfile is the 16 basic colors, whatever TERM says, unless NO_COLOR asks for none. The device shows only those
// and reverse video (docs/device.md in the kit). The CLI app claims true color, which comes out wrong; and a program
// the home screen opens in its terminal gets TERM=vt100, which termenv reads as no styling at all, losing the text
// box's cursor, the grey secondary text and the reversed headings. On the LCD stdout isn't a terminal at all, which
// lipgloss would read as no color. Bubble Tea needs an ANSI terminal to draw anything anyway.
func colorProfile() termenv.Profile {
	if termenv.EnvNoColor() {
		return termenv.Ascii
	}
	return termenv.ANSI
}
